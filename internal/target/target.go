package target

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/redrick/margin/internal/fsx"
	"github.com/redrick/margin/internal/gitx"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/text"
)

type Mode string

const (
	Worktree Mode = "worktree"
	Branch   Mode = "branch"
	Commit   Mode = "commit"
)

type Change struct {
	Status  byte
	Path    string
	OldPath string
}

// Target is what `margin [branch|commit]` reviews. An empty Head means the working tree.
type Target struct {
	Mode    Mode
	Repo    string
	Name    string
	Base    string
	Head    string
	Title   string
	Kicker  string
	Changes []Change
}

func Resolve(dir, arg, baseRef string) (*Target, error) {
	out, err := gitx.Run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, errors.New("not inside a git repository")
	}
	for _, ref := range []string{arg, baseRef} {
		if strings.HasPrefix(ref, "-") {
			return nil, fmt.Errorf("invalid ref %q", ref)
		}
	}
	t := &Target{Repo: strings.TrimSpace(string(out))}
	switch {
	case arg == "":
		err = t.worktree(baseRef)
	case isBranch(t.Repo, arg):
		err = t.branch(arg, baseRef)
	default:
		err = t.commit(arg, baseRef)
	}
	if err != nil {
		return nil, err
	}
	if len(t.Changes) == 0 {
		return nil, fmt.Errorf("nothing to review: no changes (%s)", t.Kicker)
	}
	return t, nil
}

func (t *Target) Label() string {
	if t.Mode == Worktree {
		return "changes"
	}
	return t.Name
}

func (t *Target) worktree(baseRef string) error {
	t.Mode = Worktree
	base, label := gitx.EmptyTree, "an empty repository"
	if head, err := gitx.RevParse(t.Repo, "HEAD"); err == nil {
		base, label = head, "HEAD"
	}
	if baseRef != "" {
		var err error
		if base, err = gitx.RevParse(t.Repo, baseRef); err != nil {
			return err
		}
		label = baseRef
	}
	t.Base, t.Name = base, gitx.Short(base)
	t.Title = "Uncommitted changes"
	t.Kicker = fmt.Sprintf("%s · working tree vs %s", filepath.Base(t.Repo), label)
	var err error
	t.Changes, err = changes(t.Repo, base, "")
	return err
}

func (t *Target) branch(name, baseRef string) error {
	t.Mode, t.Name = Branch, name
	tip, err := gitx.RevParse(t.Repo, name)
	if err != nil {
		return err
	}
	if baseRef == "" {
		if baseRef, err = defaultBranch(t.Repo); err != nil {
			return err
		}
	}
	baseTip, err := gitx.RevParse(t.Repo, baseRef)
	if err != nil {
		return err
	}
	if t.Base, err = gitx.MergeBase(t.Repo, baseTip, tip); err != nil {
		return fmt.Errorf("%s and %s share no history", name, baseRef)
	}
	if t.Base == tip {
		return fmt.Errorf("branch %s has no commits beyond %s; pass --base", name, baseRef)
	}
	t.Head = tip
	where := "not checked out, read-only"
	if cur, err := gitx.Run(t.Repo, "symbolic-ref", "--quiet", "HEAD"); err == nil && strings.TrimSpace(string(cur)) == "refs/heads/"+name {
		t.Head, where = "", "checked out, includes uncommitted changes"
	}
	t.Title = "Branch " + name
	t.Kicker = fmt.Sprintf("%s · %s vs %s (merge-base %s) · %s", filepath.Base(t.Repo), name, baseRef, gitx.Short(t.Base), where)
	t.Changes, err = changes(t.Repo, t.Base, t.Head)
	return err
}

func (t *Target) commit(ref, baseRef string) error {
	sha, err := gitx.RevParse(t.Repo, ref)
	if err != nil {
		return fmt.Errorf("%q is not a branch or commit in %s", ref, t.Repo)
	}
	t.Mode, t.Name, t.Head = Commit, gitx.Short(sha), sha
	switch {
	case baseRef != "":
		if t.Base, err = gitx.RevParse(t.Repo, baseRef); err != nil {
			return err
		}
	default:
		if t.Base, err = gitx.RevParse(t.Repo, sha+"^"); err != nil {
			t.Base = gitx.EmptyTree
		}
	}
	subject, _ := gitx.Run(t.Repo, "log", "-1", "--format=%s", sha)
	t.Title = "Commit " + t.Name + ": " + text.Line(string(subject))
	t.Kicker = fmt.Sprintf("%s · %s vs its parent", filepath.Base(t.Repo), t.Name)
	t.Changes, err = changes(t.Repo, t.Base, sha)
	return err
}

func isBranch(repo, name string) bool {
	for _, prefix := range []string{"refs/heads/", "refs/remotes/"} {
		if _, err := gitx.RevParse(repo, prefix+name); err == nil {
			return true
		}
	}
	return false
}

func defaultBranch(repo string) (string, error) {
	if out, err := gitx.Run(repo, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	for _, c := range []string{"main", "master", "origin/main", "origin/master"} {
		if _, err := gitx.RevParse(repo, c); err == nil {
			return c, nil
		}
	}
	return "", errors.New("cannot find the main branch; pass --base <ref>")
}

func changes(repo, base, head string) ([]Change, error) {
	args := []string{"diff", "--name-status", "-z", "-M", "--no-ext-diff", "--no-textconv", base}
	if head != "" {
		args = append(args, head)
	}
	out, err := gitx.Run(repo, args...)
	if err != nil {
		return nil, err
	}
	list := ParseNameStatus(out)
	if head == "" {
		others, err := gitx.Run(repo, "ls-files", "--others", "--exclude-standard", "-z")
		if err != nil {
			return nil, err
		}
		for _, p := range strings.Split(string(others), "\x00") {
			if p != "" {
				list = append(list, Change{Status: 'A', Path: p})
			}
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Path < list[j].Path })
	return list, nil
}

// ParseNameStatus parses `git diff --name-status -z` output.
func ParseNameStatus(out []byte) []Change {
	f := strings.Split(string(out), "\x00")
	var list []Change
	for i := 0; i < len(f); i++ {
		if f[i] == "" {
			continue
		}
		c := Change{Status: f[i][0]}
		switch c.Status {
		case 'R', 'C':
			if i+2 >= len(f) {
				return list
			}
			c.OldPath, c.Path = f[i+1], f[i+2]
			i += 2
		default:
			if i+1 >= len(f) {
				return list
			}
			c.Path = f[i+1]
			i++
		}
		list = append(list, c)
	}
	return list
}

var unsafeID = regexp.MustCompile(`[^a-z0-9_.-]+`)

// Stations makes one station per changed file; used collects ids already taken.
func Stations(t *Target, used map[string]bool) []review.Station {
	var out []review.Station
	for _, c := range t.Changes {
		p := review.Part{File: c.Path, Hunks: true}
		st := review.Station{ID: uniqueID(c.Path, used), Title: c.Path}
		switch c.Status {
		case 'D':
			p.Side = review.SideBase
		case 'R', 'C':
			p.BaseFile = c.OldPath
			st.Title = c.OldPath + " → " + c.Path
		}
		st.Parts = []review.Part{p}
		out = append(out, st)
	}
	return out
}

func uniqueID(path string, used map[string]bool) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if name == "" {
		name = filepath.Base(path)
	}
	id := strings.Trim(unsafeID.ReplaceAllString(strings.ToLower(name), "-"), "-.")
	if id == "" {
		id = "file"
	}
	if id == "overview" || id == "tests" {
		id = "file-" + id
	}
	cand := id
	for n := 2; used[cand]; n++ {
		cand = fmt.Sprintf("%s-%d", id, n)
	}
	used[cand] = true
	return cand
}

func New(t *Target) *review.Review {
	return &review.Review{
		Version:  1,
		Repo:     t.Repo,
		Base:     t.Base,
		Head:     t.Head,
		Title:    t.Title,
		Kicker:   t.Kicker,
		Stations: Stations(t, map[string]bool{}),
	}
}

// Ensure creates the review file, or brings an existing one up to date with new changed files.
func Ensure(loc Location, t *Target, fresh bool) (created bool, added int, err error) {
	dirPerm, filePerm := os.FileMode(0o700), os.FileMode(0o600)
	if loc.Vault != "" {
		dirPerm, filePerm = 0o755, 0o644
	}
	path := loc.Path
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return false, 0, err
	}
	if _, statErr := os.Stat(path); fresh || errors.Is(statErr, fs.ErrNotExist) {
		data, err := marshal(New(t), loc)
		if err != nil {
			return false, 0, err
		}
		return true, len(t.Changes), fsx.WriteFileAtomic(path, data, filePerm)
	}
	r, err := review.Load(path)
	if err != nil {
		return false, 0, fmt.Errorf("%w (start over with --fresh)", err)
	}
	if r.Base != t.Base {
		if err := review.SetScalar(path, "base", t.Base); err != nil {
			return false, 0, err
		}
	}
	if r.Head != t.Head {
		if err := review.SetScalar(path, "head", t.Head); err != nil {
			return false, 0, err
		}
	}
	covered, used := map[string]bool{}, map[string]bool{}
	for _, s := range r.Stations {
		used[s.ID] = true
		for _, p := range s.Parts {
			covered[p.File] = true
		}
	}
	for _, st := range Stations(t, used) {
		if covered[st.Parts[0].File] {
			continue
		}
		if err := review.AppendStation(path, st); err != nil {
			return false, added, err
		}
		added++
	}
	return false, added, nil
}

func marshal(r *review.Review, loc Location) ([]byte, error) {
	var buf bytes.Buffer
	if loc.Vault != "" {
		fmt.Fprintf(&buf, "# ---\n# title: %s\n# project: %s\n# topic: %s\n# type: review\n# status: open\n# date: %s\n# ---\n",
			r.Title, loc.Project, loc.Topic, time.Now().Format("2006-01-02"))
	}
	buf.WriteString("# margin review. Edit freely; `margin agent-help` describes the format.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
