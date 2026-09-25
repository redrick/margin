package target

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/redrick/margin/internal/fsx"
	"github.com/redrick/margin/internal/gitx"
	"github.com/redrick/margin/internal/ignore"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/source"
	"github.com/redrick/margin/internal/text"
)

type Mode string

const (
	Worktree Mode = "worktree"
	Branch   Mode = "branch"
	Commit   Mode = "commit"
)

type Change = gitx.Change

// Target is what `margin [branch|commit]` reviews. An empty Head means the working tree, or the
// index when Staged is set.
type Target struct {
	Mode    Mode
	Repo    string
	Name    string
	Base    string
	Head    string
	Staged  bool
	Title   string
	Kicker  string
	Changes []Change
	// Asked is what the change was meant to do, from commit messages, a pull request or the reader.
	Asked     string
	AskedFrom string
}

type Options struct {
	Base   string
	Staged bool
	// PR is a pull request number or URL, looked up read-only with gh.
	PR     string
	Intent string
}

func Resolve(dir, arg string, o Options) (*Target, error) {
	out, err := gitx.Run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, errors.New("not inside a git repository")
	}
	for _, ref := range []string{arg, o.Base} {
		if strings.HasPrefix(ref, "-") {
			return nil, fmt.Errorf("invalid ref %q", ref)
		}
	}
	if o.Staged && (arg != "" || o.PR != "") {
		return nil, errors.New("--staged reviews the index; it takes no branch, commit or --pr")
	}
	if o.PR != "" && arg != "" {
		return nil, errors.New("pass either a branch or commit, or --pr")
	}
	t := &Target{Repo: strings.TrimSpace(string(out))}
	switch {
	case o.PR != "":
		err = t.pullRequest(o.PR, o.Base)
	case arg == "":
		err = t.worktree(o.Base, o.Staged)
	case isBranch(t.Repo, arg):
		err = t.branch(arg, o.Base)
	default:
		err = t.commit(arg, o.Base)
	}
	if err != nil {
		return nil, err
	}
	if len(t.Changes) == 0 {
		return nil, fmt.Errorf("nothing to review: no changes (%s)", t.Kicker)
	}
	if intent := strings.TrimSpace(o.Intent); intent != "" {
		from := "you"
		if t.Asked != "" {
			intent += "\n\n" + t.AskedFrom + ":\n" + t.Asked
			from = "you, and " + t.AskedFrom
		}
		t.Asked, t.AskedFrom = intent, from
	}
	return t, nil
}

func (t *Target) Label() string {
	switch {
	case t.Mode == Worktree && t.Staged:
		return "staged"
	case t.Mode == Worktree:
		return "changes"
	}
	return t.Name
}

func (t *Target) worktree(baseRef string, staged bool) error {
	t.Mode, t.Staged = Worktree, staged
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
		if head, err := gitx.RevParse(t.Repo, "HEAD"); err == nil && head != base {
			t.commitMessages(base, head)
		}
	}
	t.Base, t.Name = base, gitx.Short(base)
	t.Title = "Uncommitted changes"
	t.Kicker = fmt.Sprintf("%s · working tree vs %s", filepath.Base(t.Repo), label)
	if staged {
		t.Title = "Staged changes"
		t.Kicker = fmt.Sprintf("%s · index vs %s", filepath.Base(t.Repo), label)
	}
	var err error
	t.Changes, err = gitx.Changes(t.Repo, base, "", staged)
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
	t.commitMessages(t.Base, tip)
	t.Changes, err = gitx.Changes(t.Repo, t.Base, t.Head, false)
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
	if msg, err := gitx.Run(t.Repo, "log", "-1", "--format=%B", sha); err == nil {
		t.Asked, t.AskedFrom = cleanMessage(string(msg)), "the commit message"
	}
	t.Changes, err = gitx.Changes(t.Repo, t.Base, sha, false)
	return err
}

// maxCommits caps how many commit messages go into asked; a long branch is summarised by its newest.
const maxCommits = 30

// commitMessages records the messages of the commits in base..tip, oldest first.
func (t *Target) commitMessages(base, tip string) {
	out, err := gitx.Run(t.Repo, "log", "--reverse", "--format=%x1e%B", base+".."+tip)
	if err != nil {
		return
	}
	if asked, from := formatMessages(strings.Split(string(out), "\x1e")); asked != "" {
		t.Asked, t.AskedFrom = asked, from
	}
}

// formatMessages lists commit messages as asked text, keeping the newest when there are many.
func formatMessages(raw []string) (asked, from string) {
	var msgs []string
	for _, m := range raw {
		if m = cleanMessage(m); m != "" {
			msgs = append(msgs, m)
		}
	}
	switch len(msgs) {
	case 0:
		return "", ""
	case 1:
		return msgs[0], "the commit message"
	}
	skipped := 0
	if len(msgs) > maxCommits {
		skipped = len(msgs) - maxCommits
		msgs = msgs[skipped:]
	}
	var b strings.Builder
	if skipped > 0 {
		fmt.Fprintf(&b, "(%d older commits left out)\n", skipped)
	}
	for _, m := range msgs {
		lines := strings.Split(m, "\n")
		b.WriteString("- " + lines[0] + "\n")
		for _, l := range lines[1:] {
			if strings.TrimSpace(l) == "" {
				b.WriteString("\n")
				continue
			}
			b.WriteString("  " + l + "\n")
		}
	}
	return strings.TrimSpace(b.String()), fmt.Sprintf("%d commit messages", len(msgs)+skipped)
}

func cleanMessage(s string) string {
	return strings.TrimSpace(text.Sanitize(s))
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

// ParseNameStatus parses `git diff --name-status -z` output.
func ParseNameStatus(out []byte) []Change { return gitx.ParseNameStatus(out) }

var unsafeID = regexp.MustCompile(`[^a-z0-9_.-]+`)

// Stations makes one station per changed file that ign does not match; used collects ids already taken.
func Stations(t *Target, used map[string]bool, ign *ignore.Matcher) []review.Station {
	var out []review.Station
	for _, c := range t.Changes {
		if ign.Match(c.Path) {
			continue
		}
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
	if review.Reserved(id) {
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
		Version:   1,
		Repo:      t.Repo,
		Base:      t.Base,
		Head:      t.Head,
		Staged:    t.Staged,
		Title:     t.Title,
		Kicker:    t.Kicker,
		Asked:     t.Asked,
		AskedFrom: t.AskedFrom,
		Stations:  Stations(t, map[string]bool{}, t.ignore()),
	}
}

// ignore reads the repository's .margin/ignore; files it matches get no stop of their own, and the
// viewer lists them instead.
func (t *Target) ignore() *ignore.Matcher {
	r := &review.Review{Version: 1, Repo: t.Repo, Base: t.Base, Head: t.Head, Staged: t.Staged}
	files, err := source.Open(r)
	if err != nil {
		return nil
	}
	defer files.Close()
	return ignore.Parse(files.RepoConfig("ignore"))
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
	fields := []struct{ key, have, want string }{{"head", r.Head, t.Head}}
	// What the reader said with --intent outlives later runs that do not repeat it.
	if !strings.HasPrefix(r.AskedFrom, "you") || strings.HasPrefix(t.AskedFrom, "you") {
		fields = append(fields,
			struct{ key, have, want string }{"asked", r.Asked, t.Asked},
			struct{ key, have, want string }{"asked_from", r.AskedFrom, t.AskedFrom})
	}
	for _, f := range fields {
		if f.have != f.want {
			if err := review.SetScalar(path, f.key, f.want); err != nil {
				return false, 0, err
			}
		}
	}
	covered, used := map[string]bool{}, map[string]bool{}
	for _, s := range r.Stations {
		used[s.ID] = true
		for _, p := range s.Parts {
			covered[p.File] = true
		}
	}
	for _, st := range Stations(t, used, t.ignore()) {
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
