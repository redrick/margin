package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/github"
	"github.com/redrick/margin/internal/gitx"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/verdict"
)

// ghCache keeps the pull request of the open review, as last read from GitHub, so what the reader
// confirms is what gets posted.
type ghCache struct {
	mu      sync.Mutex
	path    string
	pr      *github.PR
	posting bool
	// threads holds the ids of the review threads last read from GitHub; only those can be resolved.
	threads map[string]bool
}

var ghRun github.Runner = github.GH

type GitHubView struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	State   string `json:"state"`
	Author  string `json:"author"`
	Viewer  string `json:"viewer"`
	Own     bool   `json:"own"`
	HeadSHA string `json:"headSha"`
	// Local is the commit the review shows; Differs says it is not what the pull request has.
	Local   string `json:"local"`
	Differs bool   `json:"differs"`
}

type GitHubPlan struct {
	PR       GitHubView         `json:"pr"`
	Comments []github.Placement `json:"comments"`
	Suggest  string             `json:"suggest"`
}

// GitHubPR finds the pull request the open review belongs to; refresh reads it from GitHub again.
func (r *Reviews) GitHubPR(refresh bool) (*GitHubView, error) {
	d, err := r.document(0)
	if err != nil {
		return nil, err
	}
	pr, err := r.pullRequest(d, refresh)
	if err != nil {
		return nil, err
	}
	v := prView(d, pr)
	return &v, nil
}

func (r *Reviews) pullRequest(d *doc.Doc, refresh bool) (*github.PR, error) {
	r.gh.mu.Lock()
	defer r.gh.mu.Unlock()
	if !refresh && r.gh.path == d.Review.Path && r.gh.pr != nil {
		return r.gh.pr, nil
	}
	pr, err := github.Find(ghRun, d.Review.RepoDir(), d.Review.Title, d.Review.Head)
	if err != nil {
		return nil, err
	}
	r.gh.path, r.gh.pr = d.Review.Path, pr
	return pr, nil
}

func prView(d *doc.Doc, pr *github.PR) GitHubView {
	v := GitHubView{Number: pr.Number, Title: pr.Title, URL: pr.URL, State: pr.State, Author: pr.Author, Viewer: pr.Viewer,
		Own: pr.Own(), HeadSHA: pr.HeadSHA, Local: d.Review.Head}
	if v.Local == "" {
		v.Local, _ = gitx.RevParse(d.Review.RepoDir(), "HEAD")
	}
	// An unknown local commit (not a git checkout) says nothing about whether the two differ.
	v.Differs = v.Local != "" && v.Local != pr.HeadSHA
	return v
}

func toGitHub(q state.Question) github.Comment {
	return github.Comment{ID: q.ID, Path: q.File, Base: q.Side == review.SideBase, Line: q.Line, Needle: q.Needle,
		Body: strings.TrimSpace(state.WithoutAnswer(q.Text))}
}

// postable says whether a comment can still go to GitHub: the reader's own, not a fix request, not
// posted yet.
func postable(q state.Question) bool { return q.IsComment() && q.Fix == "" && q.GitHub == "" }

// PlanGitHubComment shows where one comment would land on the pull request, before the reader
// confirms posting it. It reads the pull request afresh.
func (r *Reviews) PlanGitHubComment(id string) (*GitHubPlan, error) {
	d, q, err := r.comment(id)
	if err != nil {
		return nil, err
	}
	pr, err := r.pullRequest(d, true)
	if err != nil {
		return nil, err
	}
	return &GitHubPlan{PR: prView(d, pr), Comments: []github.Placement{pr.Place(toGitHub(q))}}, nil
}

func (r *Reviews) comment(id string) (*doc.Doc, state.Question, error) {
	d, err := r.document(0)
	if err != nil {
		return nil, state.Question{}, err
	}
	st, err := state.Load(review.StatePath(d.Review.Path))
	if err != nil {
		return nil, state.Question{}, err
	}
	q, ok := st.Question(id)
	if !ok || !q.IsComment() {
		return nil, q, fmt.Errorf("no comment %s", id)
	}
	if q.GitHub != "" {
		return nil, q, fmt.Errorf("%s is on GitHub already", id)
	}
	if !postable(q) {
		return nil, q, fmt.Errorf("%s asks the agent for a fix; it does not go to GitHub", id)
	}
	return d, q, nil
}

// PostGitHubComment posts one comment to the pull request right away, as the reader confirmed it.
func (r *Reviews) PostGitHubComment(id string) (*ReaderUpdate, error) {
	d, q, err := r.comment(id)
	if err != nil {
		return nil, err
	}
	pr, err := r.confirmedPR(d)
	if err != nil {
		return nil, err
	}
	if !r.startPosting() {
		return nil, errors.New("still posting the last one")
	}
	defer r.stopPosting()
	posted, err := pr.PostComment(ghRun, d.Review.RepoDir(), pr.Place(toGitHub(q)))
	if err != nil {
		return nil, fmt.Errorf("not posted: %w", err)
	}
	return r.change(func(_ *doc.Doc, st *state.State) error {
		for i := range st.Questions {
			if st.Questions[i].ID == id {
				st.Questions[i].GitHub, st.Questions[i].ForGitHub = posted.URL, false
			}
		}
		return nil
	})
}

// confirmedPR is the pull request the reader saw in the confirmation, never one read behind their back.
func (r *Reviews) confirmedPR(d *doc.Doc) (*github.PR, error) {
	r.gh.mu.Lock()
	defer r.gh.mu.Unlock()
	if r.gh.path != d.Review.Path || r.gh.pr == nil {
		return nil, errors.New("open the confirmation first")
	}
	return r.gh.pr, nil
}

func (r *Reviews) startPosting() bool {
	r.gh.mu.Lock()
	defer r.gh.mu.Unlock()
	if r.gh.posting {
		return false
	}
	r.gh.posting = true
	return true
}

func (r *Reviews) stopPosting() {
	r.gh.mu.Lock()
	r.gh.posting = false
	r.gh.mu.Unlock()
}

// ToggleForGitHub picks a comment for the review sent to GitHub, or takes it out again.
func (r *Reviews) ToggleForGitHub(id string) (*ReaderUpdate, error) {
	return r.change(func(_ *doc.Doc, st *state.State) error {
		i := slices.IndexFunc(st.Questions, func(q state.Question) bool { return q.ID == id })
		if i < 0 || !st.Questions[i].IsComment() {
			return fmt.Errorf("no comment %s", id)
		}
		if !postable(st.Questions[i]) {
			return fmt.Errorf("%s does not go to GitHub", id)
		}
		st.Questions[i].ForGitHub = !st.Questions[i].ForGitHub
		return nil
	})
}

// PlanGitHubReview shows the review the reader is about to send: the picked comments, where each
// lands, and the decision margin suggests from their verdicts.
func (r *Reviews) PlanGitHubReview() (*GitHubPlan, error) {
	r.mu.Lock()
	d, walk := r.loaded, r.walk
	r.mu.Unlock()
	if d == nil {
		return nil, errors.New("open a review first")
	}
	pr, err := r.pullRequest(d, true)
	if err != nil {
		return nil, err
	}
	st, err := state.Load(review.StatePath(d.Review.Path))
	if err != nil {
		return nil, err
	}
	plan := &GitHubPlan{PR: prView(d, pr), Comments: placements(pr, st), Suggest: github.EventComment}
	switch verdict.Recommend(d, st, walk.alternate).Decision {
	case verdict.Approve:
		plan.Suggest = github.EventApprove
	case verdict.RequestChanges:
		plan.Suggest = github.EventRequestChanges
	}
	if pr.Own() {
		plan.Suggest = github.EventComment
	}
	return plan, nil
}

func placements(pr *github.PR, st *state.State) []github.Placement {
	out := []github.Placement{}
	for _, q := range st.Questions {
		if q.ForGitHub && postable(q) {
			out = append(out, pr.Place(toGitHub(q)))
		}
	}
	return out
}

// SubmitGitHubReview sends the picked comments as one pull request review with the reader's
// decision and summary, as they confirmed it.
func (r *Reviews) SubmitGitHubReview(event, summary string) (*ReaderUpdate, error) {
	d, err := r.document(0)
	if err != nil {
		return nil, err
	}
	pr, err := r.confirmedPR(d)
	if err != nil {
		return nil, err
	}
	st, err := state.Load(review.StatePath(d.Review.Path))
	if err != nil {
		return nil, err
	}
	pls := placements(pr, st)
	payload, err := pr.BuildReview(event, summary, pls)
	if err != nil {
		return nil, err
	}
	if !r.startPosting() {
		return nil, errors.New("still posting the last one")
	}
	defer r.stopPosting()
	posted, err := pr.SubmitReview(ghRun, d.Review.RepoDir(), payload)
	if err != nil {
		return nil, fmt.Errorf("not sent: %w", err)
	}
	sent := map[string]bool{}
	for _, pl := range pls {
		sent[pl.ID] = true
	}
	return r.change(func(_ *doc.Doc, st *state.State) error {
		for i, q := range st.Questions {
			if sent[q.ID] {
				st.Questions[i].GitHub, st.Questions[i].ForGitHub = posted.URL, false
			}
		}
		return nil
	})
}

// OpenURL opens a GitHub page in the browser.
func (r *Reviews) OpenURL(url string) error {
	if !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("not opening %q", url)
	}
	return r.app.Browser.OpenURL(url)
}

// ThreadView is a review thread on GitHub as the window shows it. Threads started by one of the
// reader's posted comments are keyed by that comment; the others carry where they sit in the review.
type ThreadView struct {
	ID           string            `json:"id"`
	Resolved     bool              `json:"resolved"`
	CanResolve   bool              `json:"canResolve"`
	CanUnresolve bool              `json:"canUnresolve"`
	Outdated     bool              `json:"outdated"`
	Comments     []github.TComment `json:"comments"`
	Reactions    []github.Reaction `json:"reactions"`
	Replies      []github.TComment `json:"replies"`
	// Path and PRLine are where GitHub has it; PRLine is 0 when GitHub lost the line.
	Path   string `json:"path"`
	PRLine int    `json:"prLine"`
	// Station, Line (1-based) and Side place it in the review; Station is empty when no stop shows it.
	Station string `json:"station,omitempty"`
	Line    int    `json:"line,omitempty"`
	Side    string `json:"side,omitempty"`
}

// GitHubPull is everything said on the pull request, as last read.
type GitHubPull struct {
	Mine         map[string]ThreadView `json:"mine"`
	Threads      []ThreadView          `json:"threads"`
	Conversation []github.Post         `json:"conversation"`
}

// GitHubThreads reads the pull request's review threads and conversation. Threads of the reader's
// posted comments come back keyed by comment id; every other thread is placed on its line in the
// review when a stop shows that line.
func (r *Reviews) GitHubThreads() (*GitHubPull, error) {
	d, err := r.document(0)
	if err != nil {
		return nil, err
	}
	pr, err := r.pullRequest(d, false)
	if err != nil {
		return nil, err
	}
	st, err := state.Load(review.StatePath(d.Review.Path))
	if err != nil {
		return nil, err
	}
	repo := d.Review.RepoDir()
	threads, err := pr.Threads(ghRun, repo)
	if err != nil {
		return nil, err
	}
	conv, err := pr.Conversation(ghRun, repo)
	if err != nil {
		return nil, err
	}
	out := &GitHubPull{Mine: map[string]ThreadView{}, Threads: []ThreadView{}, Conversation: conv}
	mine := map[*github.Thread]bool{}
	for _, q := range st.Questions {
		if !q.IsComment() || q.GitHub == "" {
			continue
		}
		c := toGitHub(q)
		if t := github.FindThread(threads, q.GitHub, pr.Place(c).Path, c.Body); t != nil {
			mine[t] = true
			out.Mine[q.ID] = threadView(t)
		}
	}
	known := map[string]bool{}
	for _, t := range threads {
		known[t.ID] = true
		if mine[t] {
			continue
		}
		v := threadView(t)
		v.Station, v.Line, v.Side = placeThread(d, pr, t)
		out.Threads = append(out.Threads, v)
	}
	r.gh.mu.Lock()
	if r.gh.path == d.Review.Path {
		r.gh.threads = known
	}
	r.gh.mu.Unlock()
	return out, nil
}

func threadView(t *github.Thread) ThreadView {
	v := ThreadView{ID: t.ID, Resolved: t.Resolved, CanResolve: t.CanResolve, CanUnresolve: t.CanUnresolve, Outdated: t.Outdated,
		Comments: t.Comments, Reactions: []github.Reaction{}, Replies: []github.TComment{}, Path: t.Path, PRLine: t.Line}
	if v.Comments == nil {
		v.Comments = []github.TComment{}
	}
	if len(t.Comments) > 0 {
		v.Reactions = t.Comments[0].Reactions
		v.Replies = t.Comments[1:]
	}
	return v
}

// placeThread finds the stop and line that show a thread's line. The line is found by its text in
// the pull request's diff, since the reviewed copy may not be what was pushed; without the text the
// number is taken as it is.
func placeThread(d *doc.Doc, pr *github.PR, t *github.Thread) (station string, line int, side string) {
	if t.Line == 0 {
		return "", 0, ""
	}
	base := t.Side == "LEFT"
	if base {
		side = review.SideBase
	}
	var text string
	if f := pr.Files[t.Path]; f != nil {
		lines := f.Right
		if base {
			lines = f.Left
		}
		text = strings.TrimSpace(lines[t.Line])
	}
	for _, s := range d.Stations {
		for _, p := range s.Parts {
			if p.Spec.File != t.Path || p.Base != base || p.Err != nil {
				continue
			}
			at := -1
			if text == "" {
				at = t.Line - 1
			} else {
				for i, l := range p.Lines {
					if strings.TrimSpace(l) == text && (at < 0 || abs(i-(t.Line-1)) < abs(at-(t.Line-1))) {
						at = i
					}
				}
			}
			if at >= p.Range.Start && at <= p.Range.End && at < len(p.Lines) {
				return s.ID, at + 1, side
			}
		}
	}
	return "", 0, side
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// ResolveGitHubThread resolves a review thread on GitHub, or opens it again, when the reader clicks
// it. GitHub refuses it when they may not.
func (r *Reviews) ResolveGitHubThread(thread string, resolved bool) (*GitHubPull, error) {
	d, err := r.document(0)
	if err != nil {
		return nil, err
	}
	r.gh.mu.Lock()
	pr, known := r.gh.pr, r.gh.threads[thread]
	if r.gh.path != d.Review.Path {
		pr = nil
	}
	r.gh.mu.Unlock()
	if pr == nil || !known {
		return nil, errors.New("margin does not know that thread; pull from GitHub first")
	}
	if err := pr.SetResolved(ghRun, d.Review.RepoDir(), thread, resolved); err != nil {
		return nil, err
	}
	return r.GitHubThreads()
}
