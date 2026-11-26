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

type QuestionTurn struct {
	ID      string    `json:"id"`
	Text    string    `json:"text"`
	Asked   time.Time `json:"asked"`
	Status  string    `json:"status"`
	Answer  string    `json:"answer,omitempty"`
	Problem string    `json:"problem,omitempty"`
}

func (t QuestionTurn) busy() bool {
	return t.Status == questionQueued || t.Status == questionAnswering
}

type DiffQuestion struct {
	QuestionTurn
	Station string `json:"station"`
	Title   string `json:"title"`
	File    string `json:"file"`
	Side    string `json:"side"`
	Ref     string `json:"ref"`
	Line    int    `json:"line"`
	End     int    `json:"end"`
	Excerpt string `json:"excerpt"`
}

type LocatedQuestion struct {
	DiffQuestion
	DiscussionLocation
	FollowUps []QuestionTurn `json:"followUps,omitempty"`
}

type questionEvent struct {
	Review  string `json:"review"`
	ID      string `json:"id"`
	Turn    string `json:"turn"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Status  string `json:"status"`
	Problem string `json:"problem,omitempty"`
}

type questionRequest struct {
	thread  string
	turn    QuestionTurn
	initial *DiffQuestion
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
	return r.enqueueQuestion(d, questionRequest{thread: q.ID, turn: q.QuestionTurn, initial: &q})
}

func newQuestionTurn(body string) (QuestionTurn, error) {
	body = strings.TrimSpace(text.Sanitize(body))
	if body == "" || len(body) > 4<<10 {
		return QuestionTurn{}, errors.New("enter a question of up to 4 KB")
	}
	return QuestionTurn{ID: rand.Text(), Text: body, Asked: time.Now().UTC()}, nil
}

func newDiffQuestion(d *doc.Doc, in QuestionInput) (DiffQuestion, error) {
	turn, err := newQuestionTurn(in.Text)
	if err != nil {
		return DiffQuestion{}, err
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
		QuestionTurn: turn, Station: s.ID, Title: s.Title, File: p.Spec.File, Side: side, Ref: ref,
		Line: in.Line + 1, End: in.End + 1, Excerpt: strings.Join(lines, "\n"),
	}, nil
}

func (r *Reviews) FollowUpQuestion(thread, text string, version int) error {
	d, err := r.questionDocument(version)
	if err != nil {
		return err
	}
	turn, err := newQuestionTurn(text)
	if err != nil {
		return err
	}
	return r.enqueueQuestion(d, questionRequest{thread: thread, turn: turn})
}

func (r *Reviews) RetryQuestion(id string, version int) error {
	return r.RetryQuestionTurn(id, id, version)
}

func (r *Reviews) RetryQuestionTurn(thread, id string, version int) error {
	d, err := r.questionDocument(version)
	if err != nil {
		return err
	}
	return r.enqueueQuestion(d, questionRequest{thread: thread, turn: QuestionTurn{ID: id}})
}

func (r *Reviews) questionDocument(version int) (*doc.Doc, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loaded == nil || r.version != version {
		return nil, errors.New("the review changed; select the code again")
	}
	return r.loaded, nil
}

func (r *Reviews) enqueueQuestion(d *doc.Doc, request questionRequest) error {
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
	return r.questions.enqueue(d.Review.Path, s.cmd.Dir, s.command, request, func(ev questionEvent) {
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

func questionTurnPath(rootFile, thread, turn string) string {
	if thread == turn {
		return rootFile
	}
	return filepath.Join(strings.TrimSuffix(rootFile, ".json"), turn+".json")
}

func readQuestionThread(file, id string) (DiffQuestion, []QuestionTurn, error) {
	root, err := readQuestion(file)
	if err != nil {
		return DiffQuestion{}, nil, err
	}
	if root.ID != id {
		return DiffQuestion{}, nil, errors.New("unknown question")
	}
	dir := strings.TrimSuffix(file, ".json")
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return DiffQuestion{}, nil, err
	}
	var followUps []QuestionTurn
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !validQuestionID(name) || name == id {
			continue
		}
		var turn QuestionTurn
		if err := readGUIStateJSON(filepath.Join(dir, entry.Name()), &turn, 256<<10); err == nil && turn.ID == name {
			followUps = append(followUps, turn)
		}
	}
	slices.SortFunc(followUps, func(a, b QuestionTurn) int {
		if order := a.Asked.Compare(b.Asked); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return root, followUps, nil
}

func latestQuestionTurn(root *DiffQuestion, followUps []QuestionTurn) *QuestionTurn {
	if len(followUps) == 0 {
		return &root.QuestionTurn
	}
	return &followUps[len(followUps)-1]
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
		q, followUps, err := readQuestionThread(file, id)
		if err != nil {
			continue
		}
		if latestQuestionTurn(&q, followUps).busy() {
			if lock, err := lockQuestion(file); err == nil {
				// Re-read under the lease. A worker may have just saved its answer.
				q, followUps, err = readQuestionThread(file, id)
				lock.Close()
				if err != nil {
					continue
				}
				if latest := latestQuestionTurn(&q, followUps); latest.busy() {
					latest.Status = questionInterrupted
				}
			}
		}
		thread := locateQuestion(d, q)
		thread.FollowUps = followUps
		out = append(out, thread)
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
	if location.station != "" {
		q.Station = location.station
	}
	return LocatedQuestion{DiffQuestion: q, DiscussionLocation: location}
}

type questionJob struct {
	question DiffQuestion
	turn     QuestionTurn
	history  []QuestionTurn
	review   string
	repo     string
	command  string
	file     string
	lease    *os.File
	publish  func(questionEvent)
}

func (j questionJob) save() error {
	if j.turn.ID == j.question.ID {
		j.question.QuestionTurn = j.turn
		return writeResumeJSON(j.file, j.question)
	}
	return writeResumeJSON(j.file, j.turn)
}

func (j questionJob) update() {
	if j.publish != nil {
		q := j.question
		j.publish(questionEvent{Review: j.review, ID: q.ID, Turn: j.turn.ID, File: q.File, Line: q.Line, Status: j.turn.Status, Problem: j.turn.Problem})
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

func (q *questionQueue) enqueue(reviewPath, repo, command string, request questionRequest, publish func(questionEvent)) error {
	thread, id := request.thread, request.turn.ID
	if !validQuestionID(thread) || !validQuestionID(id) {
		return errors.New("unknown question")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closing {
		return errors.New("Margin is closing")
	}
	rootFile := filepath.Join(questionDir(reviewPath), thread+".json")
	lease, err := lockQuestion(rootFile)
	if err != nil {
		return err
	}
	job := questionJob{review: reviewPath, repo: repo, command: command, file: questionTurnPath(rootFile, thread, id), lease: lease, publish: publish}
	err = job.prepare(rootFile, request)
	if err == nil {
		job.turn.Status, job.turn.Answer, job.turn.Problem = questionQueued, "", ""
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

// prepare runs under the thread lease, so a new turn always follows the saved transcript.
func (j *questionJob) prepare(rootFile string, request questionRequest) error {
	j.turn = request.turn
	if request.initial != nil {
		j.question = *request.initial
		return nil
	}
	root, followUps, err := readQuestionThread(rootFile, request.thread)
	if err != nil {
		return err
	}
	j.question = root
	latest := latestQuestionTurn(&root, followUps)
	if j.turn.Text == "" {
		if latest.ID != j.turn.ID || (!latest.busy() && latest.Status != questionFailed && latest.Status != questionInterrupted) {
			return errors.New("only the latest unanswered question can be retried")
		}
		j.turn = *latest
	} else {
		if root.Status != questionAnswered {
			return errors.New("the first question needs an answer before you can follow up")
		}
		if !j.turn.Asked.After(latest.Asked) {
			j.turn.Asked = latest.Asked.Add(time.Nanosecond)
		}
		if latest.busy() {
			// Holding the lease means the previous process quit before finalizing this turn.
			latest.Status = questionInterrupted
			if err := writeResumeJSON(questionTurnPath(rootFile, root.ID, latest.ID), *latest); err != nil {
				return err
			}
		}
	}
	if j.turn.ID != root.ID {
		j.history = append([]QuestionTurn{root.QuestionTurn}, followUps...)
	}
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
	j.turn.Status = questionAnswering
	err := j.save()
	if err == nil {
		j.update()
		j.turn.Answer, err = run(ctx, *j)
	}
	switch {
	case err == nil:
		j.turn.Status = questionAnswered
	case errors.Is(ctx.Err(), context.Canceled):
		j.turn.Status, j.turn.Answer = questionInterrupted, ""
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		j.turn.Status, j.turn.Answer, j.turn.Problem = questionFailed, "", "The agent timed out."
	default:
		j.turn.Status, j.turn.Answer, j.turn.Problem = questionFailed, "", err.Error()
	}
	j.saveResult()
}

func (j *questionJob) saveResult() {
	if err := j.save(); err != nil {
		j.turn.Status = questionFailed
		j.turn.Problem = fmt.Sprintf("Could not save the question result: %v", err)
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
		job.turn.Status = questionInterrupted
		job.saveResult()
		job.lease.Close()
		job.update()
	}
	q.workers.Wait()
}
