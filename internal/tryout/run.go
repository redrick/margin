package tryout

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/redrick/margin/internal/config"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/gitx"
	"github.com/redrick/margin/internal/testrun"
)

// Phases of a run: tests as the code is, then with the reader's edits.
const (
	PhaseBase = "base"
	PhaseTry  = "try"
)

// Event is one thing a run reports. Kind is start, out, result or end for a job, applied once the
// edits are written and restored once they are undone.
type Event struct {
	Phase  string `json:"phase,omitempty"`
	Kind   string `json:"kind"`
	Job    int    `json:"job"`
	Text   string `json:"text,omitempty"`
	Test   string `json:"test,omitempty"`
	Status string `json:"status,omitempty"`
	Code   int    `json:"code"`
	Err    string `json:"err,omitempty"`
}

// Plan is one run: the jobs, and the edits to try them against.
type Plan struct {
	Dir   string
	Jobs  []testrun.Job
	Edits []Edit
	// Compare runs the jobs once without the edits first.
	Compare bool
}

// Run blocks until the run ends or r.Stop is called, and always undoes the edits before it returns.
func Run(r *testrun.Runner, p Plan, emit func(Event)) {
	pass := func(phase string) bool {
		stopped := false
		r.Run(p.Dir, p.Jobs, testrun.Events{
			Start:  func(i int, _ testrun.Job) { emit(Event{Phase: phase, Kind: "start", Job: i}) },
			Output: func(i int, s string) { emit(Event{Phase: phase, Kind: "out", Job: i, Text: s}) },
			Result: func(i int, res testrun.Result) {
				emit(Event{Phase: phase, Kind: "result", Job: i, Test: res.Test, Status: res.Status})
			},
			End: func(i int, code int, err error) {
				e := Event{Phase: phase, Kind: "end", Job: i, Code: code}
				if err != nil {
					e.Err = err.Error()
					stopped = stopped || code == -1
				}
				emit(e)
			},
		})
		return !stopped
	}
	if len(p.Edits) == 0 {
		pass("")
		return
	}
	if p.Compare && !pass(PhaseBase) {
		return
	}
	t, err := Apply(p.Dir, p.Edits)
	if err != nil {
		emit(Event{Phase: PhaseTry, Kind: "applied", Err: err.Error()})
		return
	}
	emit(Event{Phase: PhaseTry, Kind: "applied"})
	pass(PhaseTry)
	e := Event{Phase: PhaseTry, Kind: "restored"}
	if err := t.Restore(); err != nil {
		e.Err = err.Error()
	}
	emit(e)
}

// Describe says what the edits change, in a few words.
func Describe(edits []Edit) string {
	if len(edits) != 1 {
		return fmt.Sprintf("%d changed lines", len(edits))
	}
	e := edits[0]
	if n := len(e.OldLines()); n > 1 {
		if e.New == "" {
			return fmt.Sprintf("%s:%d-%d (deleted all %d lines)", e.File, e.Line, e.End(), n)
		}
		return fmt.Sprintf("%s:%d-%d (%d lines) into `%s`", e.File, e.Line, e.End(), n, doc.Short(strings.TrimSpace(oneLine(e.New)), 60))
	}
	if e.New == "" {
		return fmt.Sprintf("%s:%d `%s` (deleted)", e.File, e.Line, doc.Short(strings.TrimSpace(e.Old), 40))
	}
	return fmt.Sprintf("%s:%d `%s` → `%s`", e.File, e.Line, doc.Short(strings.TrimSpace(e.Old), 40), doc.Short(strings.TrimSpace(oneLine(e.New)), 40))
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ⏎ ")), " ")
}

// Verdict sums up what the tests said: which failed only with the change, which passed anyway.
type Verdict struct {
	Caught  []string // passed before (or unknown) and fail with the change
	Missed  []string // pass with the change
	Already []string // failed without the change too
	// Broken lists the commands that failed with the change without naming a failed test, which
	// is how a change that does not build shows.
	Broken []string
}

// Judge compares results by test name; before may be empty when the run did not compare.
func Judge(before, after map[string]string, broken []string) Verdict {
	v := Verdict{Broken: broken}
	for _, name := range sortedKeys(after) {
		switch {
		case after[name] == "fail" && before[name] == "fail":
			v.Already = append(v.Already, name)
		case after[name] == "fail":
			v.Caught = append(v.Caught, name)
		case after[name] == "pass":
			v.Missed = append(v.Missed, name)
		}
	}
	return v
}

func (v Verdict) String() string {
	switch {
	case len(v.Broken) > 0 && len(v.Caught) == 0:
		return "the tests did not get to run: the change breaks the build, or the command failed"
	case len(v.Caught) == 0 && len(v.Missed) == 0 && len(v.Already) == 0:
		return "no test result"
	case len(v.Caught) == 0 && len(v.Already) == 0:
		return "no test noticed the change"
	}
	var parts []string
	if n := len(v.Caught); n > 0 {
		parts = append(parts, fmt.Sprintf("%d caught it", n))
	}
	if n := len(v.Missed); n > 0 {
		parts = append(parts, fmt.Sprintf("%d still pass", n))
	}
	if n := len(v.Already); n > 0 {
		parts = append(parts, fmt.Sprintf("%d failed without it too", n))
	}
	return strings.Join(parts, ", ")
}

// Message is the one line the agent gets about a tried change.
func Message(edits []Edit, v Verdict) string {
	list := func(names []string) string {
		if len(names) == 0 {
			return "none"
		}
		if len(names) > 8 {
			return strings.Join(names[:8], ", ") + fmt.Sprintf(" and %d more", len(names)-8)
		}
		return strings.Join(names, ", ")
	}
	broken := ""
	if len(v.Broken) > 0 {
		broken = " These commands failed without naming a test (does it still build?): " + list(v.Broken) + "."
	}
	return fmt.Sprintf("[margin tryout] I tried changing %s and ran the tests that run it (margin put the line back after). Failed because of the change: %s. Still passed: %s. Failed without the change too: %s.%s Tell me what this says about the tests and the line; change nothing unless I ask.",
		Describe(edits), list(v.Caught), list(v.Missed), list(v.Already), broken)
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Jobs plans the commands that run tests ids of the review's coverage, with the test command the
// settings give the review's repository.
func Jobs(d *doc.Doc, ids []int) ([]testrun.Job, error) {
	if d.Coverage == nil {
		return nil, errors.New("no coverage yet: ask the agent to run margin coverage")
	}
	dir := d.Review.RepoDir()
	tc, ok := config.Load().TestCommandFor(dir, MainWorktree(dir))
	if !ok {
		return nil, ErrNoCommand
	}
	var picked []testrun.Test
	for _, ti := range ids {
		if ti < 0 || ti >= len(d.Coverage.Tests) || d.Coverage.Tests[ti].Synthetic {
			continue
		}
		t := d.Coverage.Tests[ti]
		picked = append(picked, testrun.Test{Name: t.Name, Package: t.Package, Group: t.Group, File: t.File})
	}
	if len(picked) == 0 {
		return nil, errors.New("no tests picked")
	}
	return testrun.Plan(tc.Command, dir, picked)
}

// ErrNoCommand is Jobs' error when the settings have no test command for the repository.
var ErrNoCommand = errors.New("no test command set up for this repository")

// Suggest is a test command to offer when the coverage has Go tests.
func Suggest(d *doc.Doc) string {
	if d.Coverage != nil {
		for _, t := range d.Coverage.Tests {
			if t.Package != "" {
				return testrun.GoDefault
			}
		}
	}
	return ""
}

// MainWorktree is the repository's main working tree, or "" when dir is not in one.
func MainWorktree(dir string) string {
	out, err := gitx.Run(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return ""
	}
	common := strings.TrimSpace(string(out))
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return ""
}
