// Package workspace is what a reader can review: the folders they keep, the repositories and
// worktrees in them, and what each worktree offers. Both front ends list it the same way.
package workspace

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redrick/margin/internal/gitx"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/target"
)

type Folder struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Repos []Repo `json:"repos"`
	Err   string `json:"err,omitempty"`
}

type Repo struct {
	Name      string     `json:"name"`
	Path      string     `json:"path"`
	Worktrees []Worktree `json:"worktrees"`
}

type Worktree struct {
	Path     string `json:"path"`
	Branch   string `json:"branch"`
	Head     string `json:"head"`
	Main     bool   `json:"main"`
	Detached bool   `json:"detached"`
}

// ReviewInfo sums up an existing review file, so the reader can see what is in progress.
type ReviewInfo struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Kicker  string `json:"kicker"`
	Repo    string `json:"repo"`
	Stops   int    `json:"stops"`
	Judged  int    `json:"judged"`
	Drafts  int    `json:"drafts"`
	Notes   int    `json:"notes"`
	Updated string `json:"updated"`
	Mod     int64  `json:"mod"`
}

type Branch struct {
	Name     string      `json:"name"`
	When     string      `json:"when"`
	Subject  string      `json:"subject"`
	Current  bool        `json:"current"`
	Remote   bool        `json:"remote"`
	Ahead    int         `json:"ahead"`
	Behind   int         `json:"behind"`
	Worktree string      `json:"worktree,omitempty"`
	Review   *ReviewInfo `json:"review,omitempty"`
}

type Commit struct {
	SHA     string `json:"sha"`
	Short   string `json:"short"`
	When    string `json:"when"`
	Author  string `json:"author"`
	Subject string `json:"subject"`
}

type Targets struct {
	Dir          string       `json:"dir"`
	Branch       string       `json:"branch"`
	Base         string       `json:"base"`
	Dirty        int          `json:"dirty"`
	Staged       int          `json:"staged"`
	Changes      *ReviewInfo  `json:"changes,omitempty"`
	StagedReview *ReviewInfo  `json:"stagedReview,omitempty"`
	Branches     []Branch     `json:"branches"`
	Commits      []Commit     `json:"commits"`
	Reviews      []ReviewInfo `json:"reviews"`
	GH           bool         `json:"gh"`
}

type PullRequest struct {
	Number  int         `json:"number"`
	Title   string      `json:"title"`
	Author  string      `json:"author"`
	Branch  string      `json:"branch"`
	Base    string      `json:"base"`
	Updated string      `json:"updated"`
	Draft   bool        `json:"draft"`
	Fetched bool        `json:"fetched"`
	Review  *ReviewInfo `json:"review,omitempty"`
}

// Folders scans every folder in the workspace for repositories and their worktrees.
func Folders() []Folder {
	paths := loadPrefs().Folders
	out := make([]Folder, len(paths))
	var wg sync.WaitGroup
	for i, p := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f := Folder{Path: p, Name: filepath.Base(p)}
			repos, err := scan(p)
			if err != nil {
				f.Err = err.Error()
			}
			f.Repos = repos
			out[i] = f
		}()
	}
	wg.Wait()
	return out
}

// AddFolder adds a repository, or a folder holding repositories, to the workspace.
func AddFolder(path string) ([]Folder, error) {
	path = filepath.Clean(path)
	if _, err := scan(path); err != nil {
		return nil, err
	}
	p := loadPrefs()
	if !slices.Contains(p.Folders, path) {
		p.Folders = append(p.Folders, path)
		slices.Sort(p.Folders)
	}
	if err := p.save(); err != nil {
		return nil, err
	}
	return Folders(), nil
}

// RemoveFolder takes a folder out of the workspace; nothing on disk changes.
func RemoveFolder(path string) ([]Folder, error) {
	p := loadPrefs()
	p.Folders = slices.DeleteFunc(p.Folders, func(s string) bool { return s == path })
	if err := p.save(); err != nil {
		return nil, err
	}
	return Folders(), nil
}

// Status counts the changed files of each worktree, for the badges beside them.
func Status(dirs []string) map[string]int {
	out := map[string]int{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, d := range dirs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			n, _, err := dirty(d)
			if err != nil {
				return
			}
			mu.Lock()
			out[d] = n
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// RecentReviews are the reviews opened lately, newest first, that still exist.
func RecentReviews() []ReviewInfo {
	var out []ReviewInfo
	for _, p := range loadPrefs().Reviews {
		if info := summarize(p); info != nil {
			out = append(out, *info)
		}
	}
	return out
}

// ForgetReview takes a review off the recent list; the file stays.
func ForgetReview(path string) error {
	p := loadPrefs()
	p.Reviews = slices.DeleteFunc(p.Reviews, func(s string) bool { return s == path })
	return p.save()
}

// TargetsIn lists what the worktree at dir offers to review: its uncommitted and staged changes,
// branches with how far they are from the main branch, recent commits, and the reviews already
// kept for the repository.
func TargetsIn(dir string) (*Targets, error) {
	t := &Targets{Dir: dir, Reviews: []ReviewInfo{}}
	if out, err := gitx.Run(dir, "branch", "--show-current"); err == nil {
		t.Branch = strings.TrimSpace(string(out))
	}
	var err error
	if t.Dirty, t.Staged, err = dirty(dir); err != nil {
		return nil, err
	}
	t.Base, _ = target.DefaultBranch(dir)
	_, t.GH = lookGH()

	existing := map[string]string{}
	for _, p := range target.Existing(dir) {
		existing[filepath.Base(p)] = p
		if info := summarize(p); info != nil && sameRepo(info.Repo, dir) {
			t.Reviews = append(t.Reviews, *info)
		}
	}
	slices.SortFunc(t.Reviews, func(a, b ReviewInfo) int { return int(b.Mod - a.Mod) })
	find := func(branch, head string, staged bool) *ReviewInfo {
		if p, ok := existing[target.FileName(branch, head, staged)]; ok {
			return summarize(p)
		}
		return nil
	}
	if head, err := gitx.RevParse(dir, "HEAD"); err == nil {
		t.Changes, t.StagedReview = find("", head, false), find("", head, true)
	}

	t.Branches = branches(dir, t.Base, t.Branch)
	for i := range t.Branches {
		t.Branches[i].Review = find(t.Branches[i].Name, "", false)
	}
	out, err := gitx.Run(dir, "log", "-n", "80", "--format=%H%x09%h%x09%cr%x09%an%x09%s")
	if err != nil {
		return t, nil
	}
	for _, l := range lines(out) {
		f := strings.SplitN(l, "\t", 5)
		if len(f) < 5 {
			continue
		}
		t.Commits = append(t.Commits, Commit{SHA: f[0], Short: f[1], When: f[2], Author: f[3], Subject: f[4]})
	}
	return t, nil
}

// branches lists local and remote branches, newest first, with how many commits each is ahead of
// and behind base and the worktree it is checked out in.
func branches(dir, base, current string) []Branch {
	format := "%(refname)%09%(refname:short)%09%(committerdate:relative)%09%(worktreepath)%09%(subject)"
	if base != "" {
		format = "%(refname)%09%(refname:short)%09%(committerdate:relative)%09%(worktreepath)%09%(ahead-behind:" + base + ")%09%(subject)"
	}
	args := []string{"for-each-ref", "--sort=-committerdate", "--count=200", "--format=" + format, "refs/heads", "refs/remotes"}
	out, err := gitx.Run(dir, args...)
	if err != nil && base != "" {
		// git before 2.41 has no ahead-behind.
		return branches(dir, "", current)
	}
	var bs []Branch
	for _, l := range lines(out) {
		f := strings.Split(l, "\t")
		n := 5
		if base != "" {
			n = 6
		}
		if len(f) < n {
			continue
		}
		b := Branch{Name: f[1], When: f[2], Worktree: f[3], Subject: strings.Join(f[n-1:], "\t")}
		b.Remote = strings.HasPrefix(f[0], "refs/remotes/")
		if b.Remote && strings.HasSuffix(f[0], "/HEAD") {
			continue
		}
		if base != "" {
			if ab := strings.Fields(f[4]); len(ab) == 2 {
				b.Ahead, _ = strconv.Atoi(ab[0])
				b.Behind, _ = strconv.Atoi(ab[1])
			}
		}
		b.Current = !b.Remote && b.Name == current
		bs = append(bs, b)
	}
	return bs
}

// PullRequests lists the repository's open pull requests through gh, read-only.
func PullRequests(dir string) ([]PullRequest, error) {
	gh, ok := lookGH()
	if !ok {
		return nil, errors.New("install the GitHub CLI gh to list pull requests")
	}
	cmd := exec.Command(gh, "pr", "list", "--limit", "50", "--json", "number,title,author,headRefName,baseRefName,updatedAt,isDraft,headRefOid")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, errors.New(strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	var raw []struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		HeadRefName string `json:"headRefName"`
		BaseRefName string `json:"baseRefName"`
		HeadRefOid  string `json:"headRefOid"`
		UpdatedAt   string `json:"updatedAt"`
		IsDraft     bool   `json:"isDraft"`
		Author      struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	prs := []PullRequest{}
	for _, r := range raw {
		pr := PullRequest{Number: r.Number, Title: r.Title, Author: r.Author.Login, Branch: r.HeadRefName, Base: r.BaseRefName, Draft: r.IsDraft}
		if t, err := time.Parse(time.RFC3339, r.UpdatedAt); err == nil {
			pr.Updated = ago(t)
		}
		_, err := gitx.RevParse(dir, r.HeadRefOid)
		pr.Fetched = err == nil
		prs = append(prs, pr)
	}
	return prs, nil
}

// FetchPR fetches a pull request's head commit, the one git write margin offers, and only on request.
func FetchPR(dir string, number int) error {
	_, err := gitx.Run(dir, "fetch", "origin", "pull/"+strconv.Itoa(number)+"/head")
	return err
}

func lookGH() (string, bool) {
	p, err := exec.LookPath("gh")
	return p, err == nil
}

func dirty(dir string) (changed, staged int, err error) {
	out, err := gitx.Run(dir, "status", "--porcelain=v1", "-uall")
	if err != nil {
		return 0, 0, err
	}
	for _, l := range strings.Split(string(out), "\n") {
		if len(l) < 3 {
			continue
		}
		changed++
		if l[0] != ' ' && l[0] != '?' {
			staged++
		}
	}
	return changed, staged, nil
}

// sameRepo says whether a review's repository is dir or one of its worktrees.
func sameRepo(reviewRepo, dir string) bool {
	if reviewRepo == "" {
		return true
	}
	a, err1 := gitx.Run(reviewRepo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	b, err2 := gitx.Run(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	return err1 == nil && err2 == nil && strings.TrimSpace(string(a)) == strings.TrimSpace(string(b))
}

func summarize(path string) *ReviewInfo {
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	r, err := review.Load(path)
	if err != nil {
		return &ReviewInfo{Path: path, Title: filepath.Base(path), Kicker: err.Error(), Mod: fi.ModTime().Unix(), Updated: ago(fi.ModTime())}
	}
	info := &ReviewInfo{Path: path, Title: r.Title, Kicker: r.Kicker, Repo: r.Repo, Stops: len(r.Stations)}
	mod := fi.ModTime()
	for _, s := range r.Stations {
		info.Notes += len(s.Notes)
	}
	if st, err := state.Load(review.StatePath(path)); err == nil {
		for _, s := range r.Stations {
			if st.Verdicts[s.ID] != "" {
				info.Judged++
			}
		}
		for _, q := range st.Questions {
			if q.IsComment() && q.Draft {
				info.Drafts++
			}
		}
	}
	if sfi, err := os.Stat(review.StatePath(path)); err == nil && sfi.ModTime().After(mod) {
		mod = sfi.ModTime()
	}
	info.Mod, info.Updated = mod.Unix(), ago(mod)
	return info
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + " min ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + " h ago"
	case d < 14*24*time.Hour:
		return strconv.Itoa(int(d.Hours()/24)) + " days ago"
	}
	return t.Format("2 Jan 2006")
}

// scan finds the git repositories at root: root itself, or the repositories up to two levels below it.
func scan(root string) ([]Repo, error) {
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return nil, errors.New(root + " is not a folder")
	}
	var dirs []string
	if isRepo(root) {
		dirs = []string{root}
	} else {
		dirs = findRepos(root, 2)
	}
	seen := map[string]bool{}
	var repos []Repo
	for _, d := range dirs {
		r, ok := repoAt(d)
		if !ok || seen[r.Path] {
			continue
		}
		seen[r.Path] = true
		repos = append(repos, r)
	}
	if len(repos) == 0 {
		return nil, errors.New("no git repositories in " + root)
	}
	slices.SortFunc(repos, func(a, b Repo) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return repos, nil
}

func isRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

func findRepos(root string, depth int) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
			continue
		}
		p := filepath.Join(root, name)
		switch {
		case isRepo(p):
			out = append(out, p)
		case depth > 1:
			out = append(out, findRepos(p, depth-1)...)
		}
	}
	return out
}

// RepoAt describes the repository dir is in, if it is in one.
func RepoAt(dir string) (Repo, bool) {
	out, err := gitx.Run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return Repo{}, false
	}
	return repoAt(strings.TrimSpace(string(out)))
}

// repoAt describes the repository dir belongs to, keyed by its main worktree so linked worktrees
// found while scanning fold into one entry.
func repoAt(dir string) (Repo, bool) {
	out, err := gitx.Run(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return Repo{}, false
	}
	var wts []Worktree
	var cur *Worktree
	for _, l := range strings.Split(string(out), "\n") {
		key, val, _ := strings.Cut(l, " ")
		switch key {
		case "worktree":
			wts = append(wts, Worktree{Path: val, Main: len(wts) == 0})
			cur = &wts[len(wts)-1]
		case "HEAD":
			if cur != nil {
				cur.Head = gitx.Short(val)
			}
		case "branch":
			if cur != nil {
				cur.Branch = strings.TrimPrefix(val, "refs/heads/")
			}
		case "detached":
			if cur != nil {
				cur.Detached = true
			}
		case "bare":
			if cur != nil {
				wts = wts[:len(wts)-1]
				cur = nil
			}
		}
	}
	if len(wts) == 0 {
		return Repo{}, false
	}
	main := wts[0].Path
	if !wts[0].Main {
		main = dir
	}
	return Repo{Name: filepath.Base(main), Path: main, Worktrees: wts}, true
}

func lines(out []byte) []string {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// prefs is what margin-gui and margin-tui remember between runs: the workspace folders and the reviews opened.
type prefs struct {
	Folders []string `json:"folders"`
	Reviews []string `json:"reviews"`
	// Recent is the folder list of earlier versions, read once into Folders.
	Recent []string `json:"recent,omitempty"`
}

const maxRecentReviews = 12

func prefsPath() string {
	d, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "margin", "gui.json")
}

func loadPrefs() prefs {
	var p prefs
	if path := prefsPath(); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, &p)
		}
	}
	if p.Folders == nil && len(p.Recent) > 0 {
		for _, f := range p.Recent {
			if fi, err := os.Stat(f); err == nil && fi.IsDir() {
				p.Folders = append(p.Folders, f)
			}
		}
		slices.Sort(p.Folders)
	}
	p.Recent = nil
	return p
}

func (p prefs) save() error {
	path := prefsPath()
	if path == "" {
		return errors.New("no config directory")
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// OpenedReview puts a review first on the recent list.
func OpenedReview(path string) {
	p := loadPrefs()
	p.Reviews = slices.DeleteFunc(p.Reviews, func(s string) bool { return s == path })
	p.Reviews = append([]string{path}, p.Reviews...)
	if len(p.Reviews) > maxRecentReviews {
		p.Reviews = p.Reviews[:maxRecentReviews]
	}
	_ = p.save()
}
