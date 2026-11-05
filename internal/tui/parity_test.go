package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/redrick/margin/internal/answers"
	"github.com/redrick/margin/internal/config"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/review"
)

func TestAnswersOverlay(t *testing.T) {
	m := newModel(t)
	press(m, "A")
	if m.ans != nil || !strings.Contains(m.status, "not answered yet") {
		t.Fatalf("without a note, A says the agent has not answered: %q", m.status)
	}
	note := review.CommentsPath(m.opts.ReviewPath)
	os.WriteFile(note, []byte("# Answers\n\n## c1 · stock.go:32\nYes, zero is refused.\n\n## c2 · stock.go:39\nOnly the message changes.\n"), 0o644)
	press(m, "A")
	if m.ans == nil {
		t.Fatalf("A opens the answers: %q", m.status)
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "2 answered") || !strings.Contains(view, "▌ ## c1") {
		t.Fatalf("the overlay shows the note, on the first answer:\n%s", view)
	}
	press(m, "]")
	if view := ansi.Strip(m.View()); !strings.Contains(view, "▌ ## c2") {
		t.Fatalf("] goes to the next answer:\n%s", view)
	}
	press(m, "r")
	if m.asking {
		t.Fatal("without an agent pane, r says so instead of asking")
	}
	m.opts.AgentPane = "%0"
	press(m, "r")
	if !m.asking || !strings.Contains(m.input.Prompt, "c2") {
		t.Fatalf("r asks for a reply to the answer in view: %q", m.input.Prompt)
	}
	m.input.SetValue("and callers that print it?")
	press(m, "enter")
	n, _ := answers.Load(m.opts.ReviewPath)
	if got := n.Text[n.Sections["c2"][0]:n.Sections["c2"][1]]; !strings.Contains(got, "**Reply from the reader:** and callers that print it?") {
		t.Fatalf("the reply goes under c2's answer:\n%s", n.Text)
	}
	press(m, "esc")
	if m.ans != nil {
		t.Fatal("esc closes the answers")
	}
}

func TestChangeTestCommand(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := newModel(t)
	repo := m.doc.Review.RepoDir()
	config.SetTestCommand(repo, "old {run}")
	m.askCommand(nil)
	if m.input.Value() != "old {run}" {
		t.Fatalf("the prompt starts from the current command: %q", m.input.Value())
	}
	m.input.SetValue("new {run}")
	press(m, "enter")
	if tc, _ := config.Load().TestCommandFor(repo, ""); tc.Command != "new {run}" {
		t.Fatalf("enter saves it: %+v", tc)
	}
}

func TestTestsStopRuns(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := newModel(t)
	config.SetTestCommand(m.doc.Review.RepoDir(), `for t in {tests}; do echo "--- PASS: $t (0.00s)"; done`)
	for i, s := range m.doc.Stations {
		if s.Kind == doc.Tests {
			m.setStation(i)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if m.run == nil {
		t.Fatalf("ctrl+t in the Tests stop runs every test: %q", m.status)
	}
	pump(t, m)
	all := 0
	for _, j := range m.run.jobs {
		all += len(j.Tests)
	}
	for i, r := range m.rows {
		if r.kind == rowTest {
			m.setCursor(i)
			break
		}
	}
	press(m, "esc", "T")
	pump(t, m)
	one := 0
	for _, j := range m.run.jobs {
		one += len(j.Tests)
	}
	if one != 1 || all <= one {
		t.Fatalf("T runs the test under the cursor (%d), ctrl+t all of them (%d)", one, all)
	}
}

func TestTryOptions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := newModel(t)
	p := coveredLine(t, m)
	file := filepath.Join(m.doc.Review.RepoDir(), filepath.FromSlash(p.Spec.File))
	orig, _ := os.ReadFile(file)
	config.SetTestCommand(m.doc.Review.RepoDir(), `for t in {tests}; do echo "--- FAIL: $t (0.00s)"; done; exit 1`)
	start, _ := m.cursorRow()

	press(m, "i", ":", "above 2", "enter", ":", "below", "enter")
	es := m.edit
	if es == nil || es.tt.line != start.line-2 || es.tt.end != start.line+1 || len(es.ed.Lines) != 4 {
		t.Fatalf(":above 2 and :below take lines in: %+v %d lines", es.tt, len(es.ed.Lines))
	}
	if es.ed.Lines[2] == nil || string(es.ed.Lines[es.ed.Row]) != p.Lines[start.line] {
		t.Fatalf("the cursor stays on its line: row %d", es.ed.Row)
	}
	press(m, "d", "d", ":", "w! nocompare", "enter")
	if m.run == nil || m.run.try == nil || !m.run.try.nocompare {
		t.Fatalf(":w! nocompare runs: %q", m.status)
	}
	if len(m.run.ids) != len(m.stopTests(m.doc.Stations[m.station])) {
		t.Fatalf(":w! runs the stop's tests: %d", len(m.run.ids))
	}
	pump(t, m)
	if m.run.base != nil {
		t.Fatal("nocompare skips the run without the change")
	}
	if got, _ := os.ReadFile(file); string(got) != string(orig) {
		t.Fatal("the lines are put back")
	}
}
