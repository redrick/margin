package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/redrick/margin/internal/testutil"
	"github.com/redrick/margin/internal/verdict"
)

func newGuided(t *testing.T) *Model {
	t.Helper()
	path := testutil.Example(t)
	m := New(Options{ReviewPath: path, Guide: true})
	d, st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m.Update(loadedMsg{doc: d, state: st})
	return m
}

func TestGuidedWalk(t *testing.T) {
	m := newGuided(t)
	text := rowsText(m)
	for _, want := range []string{"① Orient", "For        Record every reservation in an audit log. Support", "Gap", "The route",
		"3 ○ high     35 lines  The new audit log", "does this change make sense, done this way?"} {
		if m.where().Station.ID != "overview" || !strings.Contains(text, want) {
			t.Fatalf("a first guided review opens on orient, missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "complexity") || strings.Contains(text, "Focus areas") {
		t.Error("orient keeps the long overview behind e")
	}
	press(m, "e")
	if !strings.Contains(rowsText(m), "Focus areas") {
		t.Error("e shows the whole overview")
	}
	press(m, "e", "y")
	if m.state.Orient != verdict.OrientYes || m.where().Station.ID != "store" || m.step != 0 {
		t.Fatalf("y answers the orient question and walks the first stop: %q %s", m.state.Orient, m.where().Station.ID)
	}
	text = rowsText(m)
	for _, want := range []string{"② Walk", "stop 1 of 5 · step 1 of 6 · what this stop is", "Risk       low:", "Check      Does it do what was asked",
		"Your call  1 decision", "Ahead: 3 changes, then 1 import edit at a glance", "e shows what and why"} {
		if !strings.Contains(text, want) {
			t.Errorf("store card misses %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "What it does") {
		t.Error("the card keeps what and why behind e")
	}
	if !m.notesShown() {
		t.Error("the guided walk keeps the notes column beside the code")
	}

	press(m, " ")
	text = rowsText(m)
	if !strings.Contains(text, "step 2 of 6 · change 1 of 3") || !strings.Contains(text, "── inventory/stock.go · lines") {
		t.Fatalf("space should open the first change:\n%s", text)
	}
	if r, ok := m.cursorRow(); !ok || lineKind(m.doc.Stations[m.station].Parts[r.part], r.line) == 0 {
		t.Errorf("the cursor should start on the first changed line, got %+v", r)
	}
	if !strings.Contains(text, "The agent left no note on this change") || !strings.Contains(text, "▸ Can you say what each changed line does") {
		t.Errorf("a change without notes says so and asks its question:\n%s", text)
	}
	read := len(m.state.Reviewed)
	press(m, " ", " ")
	text = rowsText(m)
	if len(m.state.Reviewed) <= read {
		t.Error("moving past a change counts its notes as read")
	}
	for _, want := range []string{"Settle here", "! Breaking signature", "◆ your call: Should a store", "▸ The agent sees a problem here"} {
		if !strings.Contains(text, want) {
			t.Errorf("the change with a problem and a call settles them in place, missing %q:\n%s", want, text)
		}
	}
	press(m, " ")
	if text := rowsText(m); !strings.Contains(text, "housekeeping") || !strings.Contains(text, "── inventory/stock.go · lines 3–6") {
		t.Fatalf("import edits come together after the changes:\n%s", text)
	}
	press(m, " ")
	if text := rowsText(m); !strings.Contains(text, "your verdict") || !strings.Contains(text, "Still open") || strings.Contains(text, "☐ Does it do") {
		t.Fatalf("the walk ends on the wrap-up, checks as a reminder only:\n%s", text)
	}
	press(m, " ")
	if m.where().Station.ID != "store" || !strings.Contains(m.status, "verdict first") {
		t.Fatalf("leaving without a verdict is nudged once: %s %q", m.where().Station.ID, m.status)
	}
	press(m, "1")
	if m.state.Verdicts["store"] != verdict.Good {
		t.Fatalf("verdicts = %v", m.state.Verdicts)
	}
	press(m, " ")
	if w := m.where(); w.Station.ID != "reserve" || m.step != 0 {
		t.Fatalf("after the verdict, space goes on to the next stop's card: %s step %d", w.Station.ID, m.step)
	}
	press(m, "b")
	if w := m.where(); w.Station.ID != "store" || !strings.Contains(rowsText(m), "your verdict") {
		t.Fatalf("b from a card goes back to the previous wrap-up: %s", w.Station.ID)
	}
}

func TestOrientNo(t *testing.T) {
	m := newGuided(t)
	press(m, "n")
	if !m.designing || !strings.Contains(m.input.Prompt, "why not") {
		t.Fatal("n asks why the change does not make sense")
	}
	press(m, "a history in memory is lost on restart", "enter")
	if m.state.Orient != verdict.OrientNo || m.where().Station.ID != "recap" {
		t.Fatalf("n records the objection and goes to the decision: %q %s", m.state.Orient, m.where().Station.ID)
	}
	o := verdict.Recommend(m.doc, m.state)
	if o.Decision != verdict.RequestChanges || !strings.Contains(strings.Join(o.Blockers, "|"), "lost on restart") {
		t.Fatalf("an objection to the design blocks the change: %+v", o)
	}
	if !strings.Contains(rowsText(m), "③ Decide") {
		t.Error("the recap is the decide phase")
	}
}

func TestReadingUnits(t *testing.T) {
	dir := t.TempDir()
	base := "package a\n\nimport (\n\t\"fmt\"\n)\n\nfunc A() {\n\tfmt.Println(1)\n" + strings.Repeat("\tx()\n", 10) + "\tfmt.Println(2)\n}\n"
	cur := strings.Replace(strings.Replace(base, "Println(1)", "Println(10)", 1), "Println(2)", "Println(20)", 1)
	cur = strings.Replace(cur, "\t\"fmt\"\n", "\t\"fmt\"\n\t\"os\"\n", 1)
	cur = strings.Replace(cur, "func A() {\n", "func A() {\n\n", 1)
	for name, content := range map[string]string{
		"base/a.go":     base,
		"current/a.go":  cur,
		"x.review.yaml": "version: 1\nrepo: current\nbase_dir: base\ntitle: units\nstations:\n  - id: a\n    title: a\n    parts: [{file: a.go}]\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "x.review.yaml")
	m := New(Options{ReviewPath: path, Guide: true})
	d, st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m.Update(loadedMsg{doc: d, state: st})
	mustGoto(t, m, "a")
	var kinds []stepKind
	for _, s := range m.guideSteps(m.doc.Stations[m.station]) {
		kinds = append(kinds, s.kind)
	}
	// The two edits of A, eleven lines apart, are one reading unit; the import waits for
	// housekeeping, and the added blank line gets no step.
	if want := []stepKind{stepBrief, stepChange, stepMech, stepWrap}; fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Fatalf("steps = %v, want %v", kinds, want)
	}
}

func TestGuidedJumpsAndToggle(t *testing.T) {
	m := newGuided(t)
	w := mustGoto(t, m, "reserve:2")
	if w.Note == nil || w.Note.Number != 2 || !strings.Contains(rowsText(m), "change 1 of 1") {
		t.Fatalf("goto a note opens the step that holds it: %+v\n%s", w.Note, rowsText(m))
	}
	if !strings.Contains(rowsText(m), "▸ Is a quantity of zero or less refused before the lock is taken?") {
		t.Error("a part's check is the question its steps ask")
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "Still matches") || !strings.Contains(rowsText(m), "notes beside the code") {
		t.Errorf("the change's notes sit in the column beside it:\n%s", view)
	}
	m.Update(tea.WindowSizeMsg{Width: 70, Height: 20})
	painted := 0
	for _, r := range m.rows {
		if r.kind == rowPainted {
			painted++
		}
	}
	if painted < 6 {
		t.Fatalf("a narrow pane draws the notes in full under the code, %d painted rows", painted)
	}
	press(m, "G")
	top := m.top
	for range 20 {
		press(m, "j")
	}
	if m.top <= top || !strings.Contains(ansi.Strip(m.View()), "Recorded while the lock is held") {
		t.Fatalf("j past the last code line scrolls the notes under it into view, top %d → %d:\n%s", top, m.top, ansi.Strip(m.View()))
	}
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	if err := m.gotoFileLine("inventory/audit.go", 30); err != nil {
		t.Fatal(err)
	}
	if r, ok := m.cursorRow(); !ok || r.line != 29 {
		t.Fatalf("goto file:line lands on the line inside its step, got %+v", r)
	}
	press(m, "w")
	if m.guide || !m.state.Full || !m.notesShown() {
		t.Fatal("w switches to whole stops and remembers it")
	}
	if r, ok := m.cursorRow(); !ok || r.line != 29 {
		t.Errorf("switching keeps the cursor line, got %+v", r)
	}
	press(m, "w")
	if !m.guide || m.state.Full {
		t.Fatal("w switches back")
	}
}

func TestLabelledComment(t *testing.T) {
	m := newGuided(t)
	mustGoto(t, m, "reserve:1")
	press(m, "c")
	if !m.labeling || !strings.Contains(m.footerView(), "suggestion") {
		t.Fatal("c first asks what kind of comment it is")
	}
	press(m, "i")
	if !strings.Contains(m.input.Prompt, "issue (blocking):") || !strings.Contains(m.input.Placeholder, "what goes wrong") {
		t.Fatalf("prompt %q placeholder %q", m.input.Prompt, m.input.Placeholder)
	}
	press(m, "zero is refused but negative is not logged", "enter")
	press(m, "c", "!", "n", "rename qty to quantity", "enter")
	qs := m.state.Questions
	if len(qs) != 2 || qs[0].Text != "issue (blocking): zero is refused but negative is not logged" || qs[1].Text != "nitpick (blocking): rename qty to quantity" {
		t.Fatalf("comments = %+v", qs)
	}
	press(m, "2")
	if o := verdict.Recommend(m.doc, m.state); o.Decision != verdict.RequestChanges || len(o.Blockers) != 2 {
		t.Fatalf("recommendation = %+v", o)
	}
}

func TestNoteAtEndFitsOnScreen(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("This note is long on purpose, so its card is taller than what is left of the file. ", 2) + "THE END."
	for name, content := range map[string]string{
		"base/a.go":    "package a\n\nfunc A() int {\n\treturn 1\n}\n",
		"current/a.go": "package a\n\nfunc A() int {\n\treturn 2\n}\n",
		"x.review.yaml": `version: 1
repo: current
base_dir: base
title: end
stations:
  - id: a
    title: a
    parts: [{file: a.go}]
    notes:
      - at: "}"
        text: "` + long + `"
`,
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "x.review.yaml")
	for _, guide := range []bool{false, true} {
		m := New(Options{ReviewPath: path, Guide: guide})
		d, st, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		m.Update(tea.WindowSizeMsg{Width: 120, Height: 14})
		m.Update(loadedMsg{doc: d, state: st})
		mustGoto(t, m, "a:1")
		if view := ansi.Strip(m.View()); !strings.Contains(view, "THE END.") {
			t.Errorf("guide %v: the whole card of a note on the last line should fit:\n%s", guide, view)
		}

		m.Update(tea.WindowSizeMsg{Width: 120, Height: 7})
		mustGoto(t, m, "a:1")
		view := ansi.Strip(m.View())
		if strings.Contains(view, "THE END.") || !strings.Contains(view, "enter reads it all") {
			t.Fatalf("guide %v: a card taller than the pane says how to read the rest:\n%s", guide, view)
		}
		press(m, "enter")
		if view := ansi.Strip(m.View()); !strings.Contains(view, "notes on a.go:5 · j k scroll") {
			t.Fatalf("guide %v: enter on the line opens its notes in full:\n%s", guide, view)
		}
		press(m, "j", "j")
		if view := ansi.Strip(m.View()); !strings.Contains(view, "THE END.") {
			t.Errorf("guide %v: j scrolls the notes to their end:\n%s", guide, view)
		}
		press(m, "x")
		if m.peek != nil {
			t.Error("any other key closes the notes")
		}
	}
}
