package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/testutil"
)

func init() { lipgloss.SetColorProfile(termenv.Ascii) }

const width, height = 140, 40

func newModel(t *testing.T) *Model {
	t.Helper()
	path := testutil.Example(t)
	m := New(Options{ReviewPath: path})
	d, st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m.Update(loadedMsg{doc: d, state: st})
	return m
}

func press(m *Model, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case " ":
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		m.Update(msg)
	}
}

func mustGoto(t *testing.T, m *Model, target string) Where {
	t.Helper()
	if err := m.gotoTarget(target); err != nil {
		t.Fatal(err)
	}
	return m.where()
}

func TestNotesAndStations(t *testing.T) {
	m := newModel(t)
	if w := m.where(); w.Station.ID != "store" {
		t.Fatalf("should open on the first code station, got %s", w.Station.ID)
	}
	press(m, "}")
	if w := m.where(); w.Station.ID != "reserve" {
		t.Fatalf("} went to %s", w.Station.ID)
	}

	w := mustGoto(t, m, "reserve:2")
	if w.Cursor == nil || w.Cursor.Line != 39 || w.Note.Number != 2 {
		t.Fatalf("goto reserve:2 = %+v %+v", w.Cursor, w.Note)
	}
	press(m, "]", "]")
	if w := m.where(); w.Station.ID != "audit" || w.Note.Number != 1 {
		t.Fatalf("] past the last note should enter the next station, got %s note %+v", w.Station.ID, w.Note)
	}
	press(m, "[")
	if w := m.where(); w.Station.ID != "reserve" || w.Note.Number != 3 {
		t.Fatalf("[ should go back to reserve note 3, got %s %+v", w.Station.ID, w.Note)
	}
}

func TestGotoFileLine(t *testing.T) {
	m := newModel(t)
	if w := mustGoto(t, m, "inventory/stock.go:21"); w.Station.ID != "store" || w.Cursor.Line != 21 {
		t.Fatalf("got %+v", w)
	}
	w := mustGoto(t, m, "stock.go:26")
	if w.Cursor == nil || w.Cursor.Line != 26 {
		t.Fatalf("line outside every part should open the whole file, got %+v", w.Cursor)
	}
	if err := m.gotoTarget("inventory/stock.go:999"); err == nil {
		t.Fatal("expected error for a line past the end")
	}
	if err := m.gotoTarget("nope"); err == nil {
		t.Fatal("expected error for unknown station")
	}
}

func TestMarksPersist(t *testing.T) {
	m := newModel(t)
	mustGoto(t, m, "reserve:1")
	key := m.selectedNote().Key
	press(m, " ")
	if w := m.where(); w.Note.Number != 2 {
		t.Fatalf("space should advance to the next note, at %d", w.Note.Number)
	}
	press(m, "?")
	st, err := state.Load(review.StatePath(m.opts.ReviewPath))
	if err != nil {
		t.Fatal(err)
	}
	if !st.Reviewed[key] || len(st.Flagged) != 1 {
		t.Fatalf("state not saved: %+v", st)
	}
	if c := m.counts(); c.Reviewed != 1 || c.Flagged != 1 || c.Notes != 10 {
		t.Fatalf("counts = %+v", c)
	}
}

func TestAskSavesQuestion(t *testing.T) {
	m := newModel(t)
	mustGoto(t, m, "reserve:1")
	press(m, "a", "why before the lock?", "enter")
	if len(m.state.Questions) != 1 {
		t.Fatalf("questions = %+v", m.state.Questions)
	}
	q := m.state.Questions[0]
	if q.ID != "q1" || q.Needle != "if qty <= 0 {" || q.Line != 32 || q.Station != "reserve" || q.Text != "why before the lock?" {
		t.Fatalf("question = %+v", q)
	}
	if !strings.Contains(m.status, "no agent pane") {
		t.Fatalf("status = %q", m.status)
	}
	if !strings.Contains(AgentMessage(q), "margin answer q1") {
		t.Fatalf("agent message lacks the answer command: %s", AgentMessage(q))
	}
}

func TestNewNotesAfterReload(t *testing.T) {
	m := newModel(t)
	note := review.Note{At: "l.mu.Lock()", File: "inventory/audit.go", Nth: 1, Text: "Takes the lock."}
	if _, err := review.AppendNote(m.opts.ReviewPath, "audit", note); err != nil {
		t.Fatal(err)
	}
	d, st, err := Load(m.opts.ReviewPath)
	if err != nil {
		t.Fatal(err)
	}
	m.Update(loadedMsg{doc: d, state: st})
	if w := m.where(); w.Station.ID != "store" || !strings.Contains(m.status, "1 new note in audit") {
		t.Fatalf("reload should keep position and announce the note: station %s, status %q", w.Station.ID, m.status)
	}
	press(m, "N")
	if w := m.where(); w.Station.ID != "audit" || w.Note.Number != 3 {
		t.Fatalf("N should jump to the new note, got %s %+v", w.Station.ID, w.Note)
	}
	if len(m.fresh) != 0 {
		t.Fatal("visiting the note should clear its new mark")
	}
}

func TestRecapLinksJump(t *testing.T) {
	m := newModel(t)
	mustGoto(t, m, "recap")
	jumped := false
	for idx, r := range m.rows {
		if r.kind == rowLink {
			m.cur = idx
			press(m, "enter")
			jumped = true
			break
		}
	}
	if !jumped {
		t.Fatal("recap should list the fixture's problem and question")
	}
	if w := m.where(); w.Note == nil || w.Note.Kind != "issue" || w.Station.ID != "store" {
		t.Fatalf("enter should open the problem note, got %s %+v", w.Station.ID, w.Note)
	}
}

func TestYourPassFirstAndFilter(t *testing.T) {
	m := newModel(t)
	markers := func() int {
		n := 0
		for _, r := range m.rows {
			n += len(r.notes)
		}
		return n
	}
	mustGoto(t, m, "audit")
	if markers() != 0 {
		t.Fatal("a high-risk stop should hide the agent's notes until v")
	}
	press(m, "v")
	if markers() == 0 {
		t.Fatal("v should show the notes")
	}

	mustGoto(t, m, "store")
	press(m, "F", "F")
	if got := markers(); got != 1 {
		t.Fatalf("problems-only filter should leave the one problem marked, got %d", got)
	}
	press(m, "F")
	if markers() != 2 {
		t.Fatal("filter should cycle back to all notes")
	}
}

func TestViewFitsScreen(t *testing.T) {
	m := newModel(t)
	for _, target := range []string{"0", "reserve:2", "removed", "recap", "tests"} {
		mustGoto(t, m, target)
		lines := strings.Split(m.View(), "\n")
		if len(lines) != height {
			t.Fatalf("%s: %d lines, want %d", target, len(lines), height)
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w != width {
				t.Errorf("%s: line %d width %d, want %d: %q", target, i, w, width, ansi.Strip(l))
			}
		}
	}
	mustGoto(t, m, "reserve:2")
	view := ansi.Strip(m.View())
	for _, want := range []string{"Reserve validates before locking", "fmt.Errorf", "Still matches", "▎ \tif s.items[sku] < qty {", "looks right", "MEDIUM RISK"} {
		if !strings.Contains(view, strings.ReplaceAll(want, "\t", "    ")) {
			t.Errorf("view missing %q", want)
		}
	}
	if testing.Verbose() {
		t.Log("\n" + view)
	}
}
