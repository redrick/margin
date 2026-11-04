package main

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/redrick/margin/internal/config"
	"github.com/redrick/margin/internal/diffmap"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/testrun"
	"github.com/redrick/margin/internal/tryout"
)

// TestSetup is how the review's repository runs tests, as the settings say.
type TestSetup struct {
	Dir  string `json:"dir"`
	Repo string `json:"repo"`
	// Main is the repository's main worktree, or Dir.
	Main    string `json:"main"`
	Command string `json:"command"`
	// Match is the settings' repo the command came from; empty with Command set means every repo.
	Match   string            `json:"match"`
	Found   bool              `json:"found"`
	Suggest string            `json:"suggest"`
	Places  map[string]string `json:"places"`
	Running bool              `json:"running"`
}

type TestRun struct {
	Run  int           `json:"run"`
	Dir  string        `json:"dir"`
	Jobs []testrun.Job `json:"jobs"`
	// Change says what a tried change does; Compare that the jobs run once without it first.
	Change  string `json:"change,omitempty"`
	Compare bool   `json:"compare,omitempty"`
}

type testEvent struct {
	Run int `json:"run"`
	tryout.Event
}

type testRuns struct {
	mu      sync.Mutex
	n       int
	running bool
	runner  testrun.Runner
	done    chan struct{}
	// last is the change the last run tried, for KeepChange and TellAgentTryout.
	last []tryout.Edit
}

func (r *Reviews) TestSetup() (TestSetup, error) {
	rv, err := review.Load(r.current())
	if err != nil {
		return TestSetup{}, err
	}
	dir := rv.RepoDir()
	main := tryout.MainWorktree(dir)
	s := TestSetup{Dir: dir, Main: dir, Repo: filepath.Base(dir), Places: testrun.Placeholders}
	if main != "" {
		s.Main, s.Repo = main, filepath.Base(main)
	}
	if t, ok := config.Load().TestCommandFor(dir, main); ok {
		s.Command, s.Match, s.Found = t.Command, t.Repo, true
	}
	if d, err := r.build(r.current()); err == nil {
		s.Suggest = tryout.Suggest(d)
	}
	r.tests.mu.Lock()
	s.Running = r.tests.running
	r.tests.mu.Unlock()
	return s, nil
}

// SaveTestCommand sets the command for repositories matching repo, replacing one set for the same repo.
func (r *Reviews) SaveTestCommand(repo, command string) (TestSetup, error) {
	if err := config.SetTestCommand(repo, command); err != nil {
		return TestSetup{}, err
	}
	return r.TestSetup()
}

// TestsFor lists the tests that run a part's changed lines (any of its lines when none changed);
// part -1 means every part of the stop.
func (r *Reviews) TestsFor(station string, part int) ([]int, error) {
	d, err := r.build(r.current())
	if err != nil {
		return nil, err
	}
	_, st := d.Station(station)
	if st == nil || d.Coverage == nil {
		return []int{}, nil
	}
	collect := func(changedOnly bool) []int {
		seen := map[int]bool{}
		out := []int{}
		for pi, p := range st.Parts {
			if (part >= 0 && pi != part) || p.Err != nil || p.Base {
				continue
			}
			for i := range p.Lines {
				if changedOnly && (i >= len(p.Kinds) || (p.Kinds[i] != diffmap.Added && p.Kinds[i] != diffmap.Changed)) {
					continue
				}
				for _, ti := range d.Coverage.TestsAt(p, i) {
					if !seen[ti] {
						seen[ti] = true
						out = append(out, ti)
					}
				}
			}
		}
		return out
	}
	if out := collect(true); len(out) > 0 {
		return out, nil
	}
	return collect(false), nil
}

func (r *Reviews) planTests(tests []int) (*doc.Doc, string, []testrun.Job, error) {
	d, err := r.build(r.current())
	if err != nil {
		return nil, "", nil, err
	}
	dir := d.Review.RepoDir()
	jobs, err := tryout.Jobs(d, tests)
	return d, dir, jobs, err
}

// PlanTests shows the commands RunTestsHere would run.
func (r *Reviews) PlanTests(tests []int) (TestRun, error) {
	_, dir, jobs, err := r.planTests(tests)
	return TestRun{Dir: dir, Jobs: jobs}, err
}

// RunTestsHere runs the tests with the repository's test command and streams the output as
// tests:* events. Starting a run stops the one before it.
func (r *Reviews) RunTestsHere(tests []int) (TestRun, error) {
	_, dir, jobs, err := r.planTests(tests)
	if err != nil {
		return TestRun{}, err
	}
	return r.start(tryout.Plan{Dir: dir, Jobs: jobs}), nil
}

// TryRequest is a change to one line of a stop, and the tests to run against it.
type TryRequest struct {
	Station string `json:"station"`
	Part    int    `json:"part"`
	Line    int    `json:"line"` // 0-based, as the view counts
	// End is the block's last line, 0-based; below Line means the one line.
	End  int    `json:"end"`
	Text string `json:"text"`
	// Tests are coverage indices; none means the tests that run the lines.
	Tests   []int `json:"tests"`
	Compare bool  `json:"compare"`
}

// LinesText is lines from to to (0-based, inclusive) as the file has them, tabs and all, to start
// an edit from.
func (r *Reviews) LinesText(station string, part, from, to int) (string, error) {
	_, p, err := r.part(station, part, from)
	if err != nil {
		return "", err
	}
	if to < from || to >= len(p.Lines) {
		to = from
	}
	return strings.Join(p.Lines[from:to+1], "\n"), nil
}

func (r *Reviews) part(station string, part, line int) (*doc.Doc, *doc.Part, error) {
	d, err := r.build(r.current())
	if err != nil {
		return nil, nil, err
	}
	_, st := d.Station(station)
	if st == nil || part < 0 || part >= len(st.Parts) {
		return nil, nil, errors.New("no such part")
	}
	p := st.Parts[part]
	switch {
	case p.Err != nil:
		return nil, nil, p.Err
	case p.Base:
		return nil, nil, errors.New("this part shows the code before the change; try changes on the new side")
	case line < 0 || line >= len(p.Lines):
		return nil, nil, errors.New("no such line")
	}
	return d, p, nil
}

// TryChange writes the edited line to the working tree, runs the tests against it (first without
// it when Compare), and puts the line back when they end.
func (r *Reviews) TryChange(req TryRequest) (TestRun, error) {
	d, p, err := r.part(req.Station, req.Part, req.Line)
	if err != nil {
		return TestRun{}, err
	}
	end := req.End
	if end < req.Line || end >= len(p.Lines) {
		end = req.Line
	}
	tests := req.Tests
	if len(tests) == 0 && d.Coverage != nil {
		seen := map[int]bool{}
		for l := req.Line; l <= end; l++ {
			for _, ti := range d.Coverage.TestsAt(p, l) {
				if !seen[ti] {
					seen[ti] = true
					tests = append(tests, ti)
				}
			}
		}
	}
	if len(tests) == 0 {
		return TestRun{}, errors.New("no test runs these lines, so none would see the change")
	}
	_, dir, jobs, err := r.planTests(tests)
	if err != nil {
		return TestRun{}, err
	}
	edits := []tryout.Edit{{File: p.Spec.File, Line: req.Line + 1, Old: strings.Join(p.Lines[req.Line:end+1], "\n"), New: req.Text}}
	if edits[0].New == edits[0].Old {
		return TestRun{}, errors.New("nothing changed")
	}
	if err := tryout.Check(dir, edits); err != nil {
		return TestRun{}, err
	}
	r.tests.mu.Lock()
	r.tests.last = edits
	r.tests.mu.Unlock()
	run := r.start(tryout.Plan{Dir: dir, Jobs: jobs, Edits: edits, Compare: req.Compare})
	run.Change = tryout.Describe(edits)
	return run, nil
}

// Recovered lists files margin put back on start, from a run it did not live to undo.
func (r *Reviews) Recovered() ([]string, error) {
	if r.recoverErr != "" {
		return r.recovered, errors.New(r.recoverErr)
	}
	return r.recovered, nil
}

// KeepChange writes the last tried change for good.
func (r *Reviews) KeepChange() error {
	r.tests.mu.Lock()
	edits, running := r.tests.last, r.tests.running
	r.tests.mu.Unlock()
	switch {
	case len(edits) == 0:
		return errors.New("no change tried yet")
	case running:
		return errors.New("wait for the run to end")
	}
	rv, err := review.Load(r.current())
	if err != nil {
		return err
	}
	return tryout.Write(rv.RepoDir(), edits)
}

// TellAgentTryout sends the agent what the last tried change did to the tests.
func (r *Reviews) TellAgentTryout(before, after map[string]string, broken []string) error {
	path := r.current()
	if !r.agents.Running(path) {
		return errors.New("start the agent first")
	}
	r.tests.mu.Lock()
	edits := r.tests.last
	r.tests.mu.Unlock()
	if len(edits) == 0 {
		return errors.New("no change tried yet")
	}
	return r.agents.Send(path, tryout.Message(edits, tryout.Judge(before, after, broken)))
}

func (r *Reviews) start(p tryout.Plan) TestRun {
	r.StopTests()
	done := make(chan struct{})
	r.tests.mu.Lock()
	r.tests.n++
	run := r.tests.n
	r.tests.running = true
	r.tests.done = done
	r.tests.mu.Unlock()

	var mu sync.Mutex
	var buf []testEvent
	flush := func() {
		mu.Lock()
		out := buf
		buf = nil
		mu.Unlock()
		if len(out) > 0 {
			r.app.Event.Emit("tests:batch", out)
		}
	}
	stop := make(chan struct{})
	go func() {
		tick := time.NewTicker(120 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				flush()
			case <-stop:
				return
			}
		}
	}()
	go func() {
		defer close(done)
		tryout.Run(&r.tests.runner, p, func(e tryout.Event) {
			mu.Lock()
			buf = append(buf, testEvent{Run: run, Event: e})
			mu.Unlock()
		})
		close(stop)
		flush()
		r.tests.mu.Lock()
		if r.tests.n == run {
			r.tests.running = false
		}
		r.tests.mu.Unlock()
		r.app.Event.Emit("tests:done", run)
	}()
	return TestRun{Run: run, Dir: p.Dir, Jobs: p.Jobs, Compare: p.Compare && len(p.Edits) > 0}
}

// StopTests stops the run and waits until it put back any line it changed.
func (r *Reviews) StopTests() {
	r.tests.runner.Stop()
	r.tests.mu.Lock()
	done := r.tests.done
	r.tests.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	}
}
