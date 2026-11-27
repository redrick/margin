package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/redrick/margin/internal/fsx"
	"github.com/redrick/margin/internal/github"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/text"
)

// The GitHub note is the markdown file, beside the review, where the agent says where the pull
// request's review threads stand: one section per thread, headed by the thread's id. margin writes
// each section with the thread as GitHub has it and "Status: pending"; the agent replaces the status
// and says what it found below it.

// GitHubCheck is what the agent said about one thread.
type GitHubCheck struct {
	// Status is done, partly, not done, unclear, or pending while the agent has not answered.
	Status string `json:"status"`
	Text   string `json:"text"`
}

const ghPending = "pending"

var ghHeading = regexp.MustCompile(`(?m)^## +(\S+)`)
var ghStatusLine = regexp.MustCompile(`(?mi)^[*_\s]*status[*_\s]*:[*_\s]*([^\n]*)$`)

// ghSections splits the note into its thread sections, by thread id, in the order they come.
func ghSections(doc string) (ids []string, sections map[string]string) {
	sections = map[string]string{}
	hs := ghHeading.FindAllStringSubmatchIndex(doc, -1)
	for i, h := range hs {
		end := len(doc)
		if i+1 < len(hs) {
			end = hs[i+1][0]
		}
		id := doc[h[2]:h[3]]
		if _, seen := sections[id]; seen {
			continue
		}
		ids = append(ids, id)
		sections[id] = doc[h[0]:end]
	}
	return ids, sections
}

// ghCheck reads the agent's status and answer out of one section: the last status line, and what
// follows it.
func ghCheck(section string) GitHubCheck {
	ms := ghStatusLine.FindAllStringSubmatchIndex(section, -1)
	if len(ms) == 0 {
		return GitHubCheck{Status: ghPending}
	}
	m := ms[len(ms)-1]
	status := strings.ToLower(strings.Trim(section[m[2]:m[3]], "*_ .`"))
	switch {
	case strings.HasPrefix(status, "pending"):
		status = ghPending
	case strings.HasPrefix(status, "not done"), strings.HasPrefix(status, "not fixed"), strings.HasPrefix(status, "open"):
		status = "not done"
	case strings.HasPrefix(status, "partly"), strings.HasPrefix(status, "partial"):
		status = "partly"
	case strings.HasPrefix(status, "done"), strings.HasPrefix(status, "fixed"), strings.HasPrefix(status, "resolved"):
		status = "done"
	default:
		status = "unclear"
	}
	return GitHubCheck{Status: status, Text: strings.TrimSpace(section[m[1]:])}
}

// GitHubChecks reads what the agent said about the pull request's threads, by thread id.
func (r *Reviews) GitHubChecks() (map[string]GitHubCheck, error) {
	path := r.current()
	if path == "" {
		return nil, errors.New("no review open")
	}
	data, err := os.ReadFile(review.GitHubPath(path))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]GitHubCheck{}, nil
	}
	if err != nil {
		return nil, err
	}
	_, secs := ghSections(string(data))
	out := map[string]GitHubCheck{}
	for id, sec := range secs {
		out[id] = ghCheck(sec)
	}
	return out, nil
}

// AskGitHubStatus writes the threads into the GitHub note, waiting for the agent, and asks the agent
// to check each against the code and say where it stands. Sections of other threads stay.
func (r *Reviews) AskGitHubStatus(ids []string, extra string) (map[string]GitHubCheck, error) {
	path := r.current()
	if path == "" {
		return nil, errors.New("no review open")
	}
	if !r.agents.Running(path) {
		return nil, errors.New("start the agent first, so it can check the threads")
	}
	if len(ids) == 0 {
		return nil, errors.New("no threads to check")
	}
	d, err := r.document(0)
	if err != nil {
		return nil, err
	}
	r.gh.mu.Lock()
	pr, known := r.gh.pr, r.gh.threads
	if r.gh.path != d.Review.Path {
		pr = nil
	}
	r.gh.mu.Unlock()
	if pr == nil {
		return nil, errors.New("pull from GitHub first")
	}
	var threads []*github.Thread
	for _, id := range ids {
		t := known[id]
		if t == nil {
			return nil, fmt.Errorf("margin does not know thread %s; pull from GitHub again", id)
		}
		threads = append(threads, t)
	}
	note := review.GitHubPath(path)
	data, err := os.ReadFile(note)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	doc := string(data)
	if strings.TrimSpace(doc) == "" {
		doc = fmt.Sprintf("# Review threads on PR #%d\n\nmargin writes each thread below; the agent replaces `Status: pending` and says what it found.\n", pr.Number)
	}
	for _, t := range threads {
		doc = putGHSection(doc, t.ID, ghSection(t))
	}
	if err := fsx.WriteFileAtomic(note, []byte(doc), 0o644); err != nil {
		return nil, err
	}
	if err := r.agents.Send(path, ghStatusMessage(pr, threads, note, extra)); err != nil {
		return nil, fmt.Errorf("written to %s, but not sent: %w", note, err)
	}
	return r.GitHubChecks()
}

// putGHSection replaces the section of thread id, or adds it at the end.
func putGHSection(doc, id, section string) string {
	_, secs := ghSections(doc)
	if old, ok := secs[id]; ok {
		i := strings.Index(doc, old)
		return doc[:i] + section + "\n" + doc[i+len(old):]
	}
	return strings.TrimRight(doc, "\n") + "\n\n" + section
}

func ghSection(t *github.Thread) string {
	var b strings.Builder
	where := t.Path
	switch {
	case t.Line > 0:
		where += fmt.Sprintf(":%d", t.Line)
	case t.OriginalLine > 0:
		where += fmt.Sprintf(":%d (outdated, first written on that line)", t.OriginalLine)
	}
	state := "open"
	if t.Resolved {
		state = "resolved"
	}
	starter := "ghost"
	if len(t.Comments) > 0 && t.Comments[0].Author != "" {
		starter = t.Comments[0].Author
	}
	fmt.Fprintf(&b, "## %s\n\n`%s` · started by %s · %s on GitHub\n\n", t.ID, where, starter, state)
	for i, c := range t.Comments {
		if i > 0 {
			b.WriteString(">\n")
		}
		author := c.Author
		if author == "" {
			author = "ghost"
		}
		body := text.Sanitize(strings.TrimSpace(c.Body))
		fmt.Fprintf(&b, "> **%s:** %s\n", author, strings.ReplaceAll(body, "\n", "\n> "))
	}
	b.WriteString("\nStatus: pending\n")
	return b.String()
}

func ghStatusMessage(pr *github.PR, threads []*github.Thread, note, extra string) string {
	list := make([]string, len(threads))
	for i, t := range threads {
		list[i] = t.ID
	}
	msg := fmt.Sprintf("[margin github] The reader wants to know where %d review thread(s) on PR #%d stand (head %s). "+
		"Each is written out in %s, one section per thread, headed by its id: %s. "+
		"For each section read the thread, then check the current code, and any commit a reply names (git show <sha>), "+
		"to decide whether what the thread asks for is really done. "+
		"Replace `Status: pending` with exactly one of `Status: done`, `Status: partly`, `Status: not done` or `Status: unclear`, "+
		"then below it say in a few short sentences what you found: what was fixed, what is still missing or wrong. "+
		"Name the code as repo-relative path:line with current line numbers (e.g. `internal/x/y.go:42`), so the reader can jump there; name commits by short sha. "+
		"Do not change code, do not post or resolve anything on GitHub, and leave the quoted thread and the other sections as they are.",
		len(threads), pr.Number, pr.HeadSHA[:min(9, len(pr.HeadSHA))], note, strings.Join(list, ", "))
	if extra = strings.Join(strings.Fields(text.Sanitize(extra)), " "); extra != "" {
		msg += " The reader adds: " + extra
	}
	return msg
}
