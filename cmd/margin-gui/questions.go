package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/text"
)

const (
	questionQueued      = "queued"
	questionAnswering   = "answering"
	questionAnswered    = "answered"
	questionFailed      = "failed"
	questionInterrupted = "interrupted"
)

type QuestionInput struct {
	LineInput
	End     int `json:"end"`
	Version int `json:"version"`
}

type DiffQuestion struct {
	ID      string    `json:"id"`
	Station string    `json:"station"`
	Title   string    `json:"title"`
	File    string    `json:"file"`
	Side    string    `json:"side"`
	Ref     string    `json:"ref"`
	Line    int       `json:"line"`
	End     int       `json:"end"`
	Excerpt string    `json:"excerpt"`
	Text    string    `json:"text"`
	Asked   time.Time `json:"asked"`
	Status  string    `json:"status"`
	Answer  string    `json:"answer,omitempty"`
	Problem string    `json:"problem,omitempty"`
}

type LocatedQuestion struct {
	DiffQuestion
	DiscussionLocation
}

type questionEvent struct {
	Review  string `json:"review"`
	ID      string `json:"id"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Status  string `json:"status"`
	Problem string `json:"problem,omitempty"`
}

func (r *Reviews) Ask(in QuestionInput) error {
	d, err := r.questionDocument(in.Version)
	if err != nil {
		return err
	}
	q, err := newDiffQuestion(d, in)
	if err != nil {
		return err
	}
	return r.enqueueQuestion(d, q.ID, &q)
}

func newDiffQuestion(d *doc.Doc, in QuestionInput) (DiffQuestion, error) {
	body := strings.TrimSpace(text.Sanitize(in.Text))
	if body == "" || len(body) > 4<<10 {
		return DiffQuestion{}, errors.New("enter a question of up to 4 KB")
	}
	_, s := d.Station(in.Station)
	if s == nil || in.Part < 0 || in.Part >= len(s.Parts) {
		return DiffQuestion{}, errors.New("that selection is no longer in the review")
	}
	p := s.Parts[in.Part]
	if p.Err != nil || in.Line < 0 || in.End < in.Line || in.End >= len(p.Lines) {
		return DiffQuestion{}, errors.New("that selection is no longer in the review")
	}
	lines := p.Lines[in.Line : in.End+1]
	// Keep the captured code small enough to pass to every supported CLI.
	size := len(lines) - 1
	for _, line := range lines {
		size += len(line)
		if size > 8<<10 {
			return DiffQuestion{}, errors.New("select a smaller section of code to ask about")
		}
	}
	side, ref := "head", d.HeadDesc
	if p.Base {
		side, ref = "base", d.BaseDesc
	}
	return DiffQuestion{
		ID: rand.Text(), Station: s.ID, Title: s.Title, File: p.Spec.File, Side: side, Ref: ref,
		Line: in.Line + 1, End: in.End + 1, Excerpt: strings.Join(lines, "\n"), Text: body, Asked: time.Now().UTC(),
	}, nil
}

func (r *Reviews) RetryQuestion(id string, version int) error {
	d, err := r.questionDocument(version)
	if err != nil {
		return err
	}
	return r.enqueueQuestion(d, id, nil)
}

func (r *Reviews) questionDocument(version int) (*doc.Doc, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loaded == nil || r.version != version {
		return nil, errors.New("the review changed; select the code again")
	}
	return r.loaded, nil
}

func (r *Reviews) enqueueQuestion(d *doc.Doc, id string, first *DiffQuestion) error {
	s := r.agents.session(d.Review.Path)
	if s == nil {
		return errors.New("start the agent for this review before asking a question")
	}
	if resumeIdentity(s.cmd.Dir) != resumeIdentity(d.Review.RepoDir()) {
		return errors.New("the review's worktree changed; restart its agent")
	}
	if agentPreset(s.command) == "" {
		return errors.New("asking about the diff requires a built-in agent command in Settings")
	}
	return r.questions.enqueue(d.Review.Path, s.cmd.Dir, s.command, id, first, func(ev questionEvent) {
		if r.app != nil {
			r.app.Event.Emit("question:updated", ev)
		}
	})
}

func questionDir(reviewPath string) string {
	return filepath.Join(guiStateDir(), "questions", strings.TrimSuffix(filepath.Base(resumePath(reviewPath)), ".json"))
}

func validQuestionID(id string) bool {
	return len(id) == 26 && strings.Trim(id, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") == ""
}

func readQuestion(file string) (DiffQuestion, error) {
	var q DiffQuestion
	err := readGUIStateJSON(file, &q, 256<<10)
	return q, err
}

var errQuestionBusy = errors.New("this question is already queued or answering")

func lockQuestion(file string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(file+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errQuestionBusy
		}
		return nil, err
	}
	return lock, nil
}

func (r *Reviews) DiffQuestions(version int) ([]LocatedQuestion, error) {
	d, err := r.questionDocument(version)
	if err != nil {
		return nil, err
	}
	dir := questionDir(d.Review.Path)
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	out := make([]LocatedQuestion, 0, len(entries))
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !validQuestionID(id) {
			continue
		}
		file := filepath.Join(dir, entry.Name())
		q, err := readQuestion(file)
		if err != nil || q.ID != id {
			continue
		}
		if q.Status == questionQueued || q.Status == questionAnswering {
			if lock, err := lockQuestion(file); err == nil {
				// Re-read under the lease. A worker may have just saved its answer.
				q, err = readQuestion(file)
				lock.Close()
				if err != nil {
					continue
				}
				if q.Status == questionQueued || q.Status == questionAnswering {
					q.Status = questionInterrupted
				}
			}
		}
		out = append(out, locateQuestion(d, q))
	}
	slices.SortFunc(out, func(a, b LocatedQuestion) int { return a.Asked.Compare(b.Asked) })
	return out, nil
}

func locateQuestion(d *doc.Doc, q DiffQuestion) LocatedQuestion {
	location := locateDiscussionCode(d, q.Station, q.File, q.Side, func(lines []string) int {
		if q.Line < 1 || q.End < q.Line {
			return -1
		}
		excerpt := strings.Split(q.Excerpt, "\n")
		limit := len(lines) - len(excerpt)
		matches := func(i int) bool { return slices.Equal(lines[i:i+len(excerpt)], excerpt) }
		if at := q.Line - 1; at <= limit && matches(at) {
			return at
		}
		return uniqueDiscussionLine(limit+1, q.Excerpt, matches)
	})
	return LocatedQuestion{DiffQuestion: q, DiscussionLocation: location}
}

type questionJob struct {
	question DiffQuestion
	review   string
	repo     string
	command  string
	file     string
	lease    *os.File
	publish  func(questionEvent)
}

func (j questionJob) save() error { return writeResumeJSON(j.file, j.question) }

func (j questionJob) update() {
	if j.publish != nil {
		q := j.question
		j.publish(questionEvent{Review: j.review, ID: q.ID, File: q.File, Line: q.Line, Status: q.Status, Problem: q.Problem})
	}
}

type questionQueue struct {
	mu      sync.Mutex
	pending []questionJob
	active  map[string]context.CancelFunc
	workers sync.WaitGroup
	closing bool
	run     func(context.Context, questionJob) (string, error)
}

func (q *questionQueue) enqueue(reviewPath, repo, command, id string, first *DiffQuestion, publish func(questionEvent)) error {
	if !validQuestionID(id) {
		return errors.New("unknown question")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closing {
		return errors.New("Margin is closing")
	}
	file := filepath.Join(questionDir(reviewPath), id+".json")
	lease, err := lockQuestion(file)
	if err != nil {
		return err
	}
	job := questionJob{review: reviewPath, repo: repo, command: command, file: file, lease: lease, publish: publish}
	if first == nil {
		job.question, err = readQuestion(file)
		if err == nil && (job.question.ID != id || job.question.Status == questionAnswered) {
			err = errors.New("this question cannot be retried")
		}
	} else {
		job.question = *first
	}
	if err == nil {
		job.question.Status, job.question.Answer, job.question.Problem = questionQueued, "", ""
		err = job.save()
	}
	if err != nil {
		lease.Close()
		return fmt.Errorf("queue question: %w", err)
	}
	q.pending = append(q.pending, job)
	q.startNext()
	return nil
}

// startNext is called with mu held. Workers only exist while there are questions to answer.
func (q *questionQueue) startNext() {
	if q.closing || len(q.active) >= 2 || len(q.pending) == 0 {
		return
	}
	job := q.pending[0]
	q.pending[0] = questionJob{}
	q.pending = q.pending[1:]
	if q.active == nil {
		q.active = make(map[string]context.CancelFunc)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	q.active[job.file] = cancel
	q.workers.Go(func() {
		run := q.run
		if run == nil {
			run = runDiffQuestion
		}
		job.answer(ctx, run)
		cancel()
		q.mu.Lock()
		delete(q.active, job.file)
		job.lease.Close()
		q.startNext()
		q.mu.Unlock()
		job.update()
	})
}

func (j *questionJob) answer(ctx context.Context, run func(context.Context, questionJob) (string, error)) {
	j.question.Status = questionAnswering
	err := j.save()
	if err == nil {
		j.update()
		j.question.Answer, err = run(ctx, *j)
	}
	switch {
	case err == nil:
		j.question.Status = questionAnswered
	case errors.Is(ctx.Err(), context.Canceled):
		j.question.Status, j.question.Answer = questionInterrupted, ""
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		j.question.Status, j.question.Answer, j.question.Problem = questionFailed, "", "The agent timed out."
	default:
		j.question.Status, j.question.Answer, j.question.Problem = questionFailed, "", err.Error()
	}
	j.saveResult()
}

func (j *questionJob) saveResult() {
	if err := j.save(); err != nil {
		j.question.Status = questionFailed
		j.question.Problem = fmt.Sprintf("Could not save the question result: %v", err)
	}
}

func (q *questionQueue) stop() {
	q.mu.Lock()
	q.closing = true
	pending := q.pending
	q.pending = nil
	for _, cancel := range q.active {
		cancel()
	}
	q.mu.Unlock()
	for _, job := range pending {
		job.question.Status = questionInterrupted
		job.saveResult()
		job.lease.Close()
		job.update()
	}
	q.workers.Wait()
}
