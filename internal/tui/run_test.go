package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/redrick/margin/internal/config"
	"github.com/redrick/margin/internal/doc"
)

// coveredLine puts the cursor on a new-side line some test runs and returns its part.
func coveredLine(t *testing.T, m *Model) *doc.Part {
	t.Helper()
	for s := range m.doc.Stations {
		m.setStation(s)
		for i, r := range m.rows {
			if r.kind != rowCode {
				continue
			}
			p := m.doc.Stations[m.station].Parts[r.part]
			if !p.Base && strings.TrimSpace(p.Lines[r.line]) != "" && len(m.doc.Coverage.TestsAt(p, r.line)) > 0 {
				m.setCursor(i)
				return p
			}
		}
	}
	t.Fatal("the example has no covered line")
	return nil
}

func pump(t *testing.T, m *Model) {
	t.Helper()
	for i := 0; i < 1000 && m.run != nil && !m.run.done; i++ {
		m.Update(waitRun(m.run.id, m.run.events)())
	}
	if m.run == nil || !m.run.done {
		t.Fatal("the run did not end")
	}
}

func TestTryChange(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := newModel(t)
	p := coveredLine(t, m)
	repo := m.doc.Review.RepoDir()
	file := filepath.Join(repo, filepath.FromSlash(p.Spec.File))
	keep := filepath.Join(t.TempDir(), "orig")
	orig, _ := os.ReadFile(file)
	os.WriteFile(keep, orig, 0o644)

	press(m, "i")
	if m.edit == nil {
		t.Fatalf("i opens the line in place: %q", m.status)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "-- NORMAL --") || !strings.Contains(view, "▎✎") {
		t.Fatalf("the line turns into an editor where it stands:\n%s", view)
	}
	press(m, "c", "c", "broken()", "esc", ":", "w", "enter")
	if !m.cmdAsk {
		t.Fatalf("without a test command, margin asks for one: %q", m.status)
	}
	// Each test passes while the file is as it was, and fails once it differs.
	cmd := `s=0; for t in {tests}; do if cmp -s ` + file + ` ` + keep + `; then echo "--- PASS: $t (0.00s)"; else echo "--- FAIL: $t (0.00s)"; s=1; fi; done; exit $s`
	m.input.SetValue(cmd)
	press(m, "enter")
	if tc, ok := config.Load().TestCommandFor(repo, ""); !ok || tc.Command != cmd {
		t.Fatalf("the command is saved: %+v", tc)
	}
	if m.run == nil || m.run.edits == nil {
		t.Fatalf("saving the command goes on with the try: %q", m.status)
	}
	pump(t, m)
	v := m.run.verdict()
	if len(v.Caught) == 0 || len(v.Missed) != 0 {
		t.Fatalf("every test notices the change: %+v\n%s", v, strings.Join(m.run.out, "\n"))
	}
	if got, _ := os.ReadFile(file); string(got) != string(orig) {
		t.Fatal("the line is put back after the run")
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "caught it") || !strings.Contains(view, "unchanged · ") {
		t.Fatalf("the output shows both runs and the verdict:\n%s", view)
	}
	for _, j := range m.run.base {
		if j.status != "pass" {
			t.Fatalf("unchanged, the tests pass: %+v", m.run.base)
		}
	}

	press(m, "w")
	if got, _ := os.ReadFile(file); !strings.Contains(string(got), "broken()") {
		t.Fatal("w keeps the change")
	}
	os.WriteFile(file, orig, 0o644)

	press(m, "esc", "T")
	pump(t, m)
	if strings.Contains(m.run.summary(), "failed") || m.run.edits != nil {
		t.Fatalf("T runs the line's tests as the code is: %s", m.run.summary())
	}
}

func TestTryChangeThatDoesNotBuild(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := newModel(t)
	p := coveredLine(t, m)
	file := filepath.Join(m.doc.Review.RepoDir(), filepath.FromSlash(p.Spec.File))
	keep := filepath.Join(t.TempDir(), "orig")
	orig, _ := os.ReadFile(file)
	os.WriteFile(keep, orig, 0o644)
	// Like go test when the package does not compile: it fails and names no test.
	if err := config.SetTestCommand("", `cmp -s `+file+` `+keep+` && echo '--- PASS: TestReserve (0.00s)' || { echo 'build failed'; exit 1; }`); err != nil {
		t.Fatal(err)
	}
	press(m, "i", "c", "c", "not go", "esc", ":", "w", "enter")
	pump(t, m)
	v := m.run.verdict()
	if len(v.Caught) != 0 || len(v.Broken) == 0 || !strings.Contains(m.run.summary(), "did not get to run") {
		t.Fatalf("a change that does not build caught nothing: %+v %q", v, m.run.summary())
	}
}

func TestTryBlock(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := newModel(t)
	p := coveredLine(t, m)
	repo := m.doc.Review.RepoDir()
	file := filepath.Join(repo, filepath.FromSlash(p.Spec.File))
	keep := filepath.Join(t.TempDir(), "orig")
	orig, _ := os.ReadFile(file)
	os.WriteFile(keep, orig, 0o644)
	cmd := `s=0; for t in {tests}; do if cmp -s ` + file + ` ` + keep + `; then echo "--- PASS: $t (0.00s)"; else echo "--- FAIL: $t (0.00s)"; s=1; fi; done; exit $s`
	if err := config.SetTestCommand(repo, cmd); err != nil {
		t.Fatal(err)
	}

	start, _ := m.cursorRow()
	press(m, "V", "j", "j")
	_, from, to, ok := m.block()
	if !ok || from != start.line || to <= from {
		t.Fatalf("V then j grows a block: %v %d-%d", ok, from, to)
	}
	if !m.inBlock(p, from) || m.inBlock(p, to+1) {
		t.Fatal("the block's lines are marked")
	}
	press(m, "i")
	if m.edit == nil || m.blockAt != nil || len(m.edit.ed.Lines) != to-from+1 {
		t.Fatalf("i on a block opens all of it in place: %q", m.status)
	}
	press(m, "d", "G", ":", "w", "enter")
	if m.run == nil || len(m.run.edits) != 1 || m.run.edits[0].End() != to+1 || m.run.edits[0].New != "" {
		t.Fatalf("an emptied block is tried as deleted: %q", m.status)
	}
	pump(t, m)
	if v := m.run.verdict(); len(v.Caught) == 0 {
		t.Fatalf("the tests notice the deleted lines: %+v", v)
	}
	if got, _ := os.ReadFile(file); string(got) != string(orig) {
		t.Fatal("the block is put back after the run")
	}
}
