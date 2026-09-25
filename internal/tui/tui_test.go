package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/redrick/margin/internal/render"
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

// rowsText is every row of the current stop as plain text, including the ones scrolled off screen.
func rowsText(m *Model) string {
	var b strings.Builder
	for _, r := range m.rows {
		b.WriteString(r.text)
		for _, sp := range r.spans {
			b.WriteString(sp.Text)
		}
		b.WriteString("\n")
	}
	return b.String()
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
	if c := m.counts(); c.Reviewed != 1 || c.Flagged != 1 || c.Notes != 12 || c.Calls != 2 {
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
	if w := m.where(); w.Station.ID != "audit" || w.Note.Number != 4 {
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
		if r.kind == rowLink && strings.Contains(r.text, "Breaking") {
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
	all := markers()
	press(m, "F", "F")
	if got := markers(); got != 1 {
		t.Fatalf("problems-only filter should leave the one problem marked, got %d", got)
	}
	press(m, "F")
	if fs := m.filters(); fs[m.filter] != "focus: breaking-change" || markers() != 1 {
		t.Fatalf("the next filter should be the first focus area, got %q with %d marks", fs[m.filter], markers())
	}
	for m.filter != 0 {
		press(m, "F")
	}
	if markers() != all {
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
	for _, want := range []string{"Reserve validates before locking", "fmt.Errorf", "Still matches", "▎- \tif s.items[sku] < qty {", "looks right", "MEDIUM RISK"} {
		if !strings.Contains(view, strings.ReplaceAll(want, "\t", "    ")) {
			t.Errorf("view missing %q", want)
		}
	}
	if testing.Verbose() {
		t.Log("\n" + view)
	}
}

func TestSideBySide(t *testing.T) {
	m := newModel(t)
	mustGoto(t, m, "reserve:2")
	press(m, "s")
	if m.sideBySide() {
		t.Fatal("side by side should not fit beside the notes column at this width")
	}
	if !strings.Contains(m.status, "wider") {
		t.Errorf("s without room should say why, got %q", m.status)
	}
	press(m, "n")
	if !m.sideBySide() {
		t.Fatal("hiding notes should leave room for side by side")
	}
	lines := strings.Split(m.View(), "\n")
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != width {
			t.Errorf("line %d width %d, want %d: %q", i, w, width, ansi.Strip(l))
		}
	}
	view := ansi.Strip(m.View())
	var pair, leftover string
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "available := s.items[sku]") {
			pair = l
		}
		if strings.Contains(l, "ReleaseAll") {
			leftover = l
		}
	}
	if !strings.Contains(pair, "- ") || !strings.Contains(pair, "if s.items[sku] < qty {") {
		t.Errorf("a changed line should sit beside the line it replaced: %q", pair)
	}
	if left, _, ok := strings.Cut(leftover, "│"); !ok || !strings.Contains(left, "ReleaseAll") {
		t.Errorf("a removed function should stay on the left: %q", leftover)
	}
	if strings.Contains(view, "wider") {
		t.Error("the narrow warning should clear once side by side fits")
	}
	if !strings.Contains(view, "s one column") {
		t.Error("footer should say how to leave side by side")
	}
	if testing.Verbose() {
		t.Log("\n" + view)
	}

	press(m, "s", "d")
	if strings.Contains(ansi.Strip(m.View()), "ReleaseAll") || !strings.Contains(ansi.Strip(m.View()), "removed lines hidden") {
		t.Error("d in one column should hide removed lines and say so")
	}
}

func TestSearch(t *testing.T) {
	m := newModel(t)
	notes := m.notes
	press(m, "/", "record", "enter")
	w := m.where()
	if w.Station.ID != "reserve" || w.Cursor == nil || w.Cursor.Line != 42 {
		t.Fatalf("/record from store = %s %+v", w.Station.ID, w.Cursor)
	}
	if !strings.Contains(m.status, "1 of 2") {
		t.Errorf("status %q", m.status)
	}
	press(m, "n")
	if w := m.where(); w.Station.ID != "audit" || w.Cursor.File != "inventory/audit.go" || w.Cursor.Line != 24 {
		t.Fatalf("n went to %s %+v", w.Station.ID, w.Cursor)
	}
	press(m, "n")
	if w := m.where(); w.Station.ID != "reserve" || !strings.Contains(m.status, "wrapped") {
		t.Fatalf("n past the last match should wrap, got %s, status %q", w.Station.ID, m.status)
	}
	press(m, "N")
	if w := m.where(); w.Station.ID != "audit" {
		t.Fatalf("N went to %s", w.Station.ID)
	}
	if m.notes != notes {
		t.Fatal("n during a search toggled the notes column")
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "/record") {
		t.Error("footer does not show the search")
	}

	press(m, "esc", "n")
	if m.query != "" || m.notes == notes {
		t.Fatalf("esc should end the search and give n back, query %q", m.query)
	}

	press(m, "/", "Record", "enter")
	if !strings.Contains(m.status, "of 2") {
		t.Errorf("upper case should still find Record: %q", m.status)
	}
	press(m, "/", "RECORD", "enter")
	if !strings.Contains(m.status, "not found") {
		t.Errorf("upper case should match case: %q", m.status)
	}
	press(m, "esc", "/", "enter")
	if m.query != "RECORD" {
		t.Errorf("empty / should repeat the last search, got %q", m.query)
	}
}

func TestMarkSpans(t *testing.T) {
	spans := []render.Span{{Text: "s.log", Color: "a"}, {Text: ".Record(x)", Color: "b"}}
	out := markSpans(spans, [][2]int{{2, 7}})
	var got []string
	for _, s := range out {
		mark := ""
		if s.Bg == colMatch {
			mark = "*"
		}
		got = append(got, mark+s.Text)
	}
	if want := "s.|*log|*.R|ecord(x)"; strings.Join(got, "|") != want {
		t.Fatalf("got %q, want %q", strings.Join(got, "|"), want)
	}
}

func TestCoverage(t *testing.T) {
	m := newModel(t)
	mustGoto(t, m, "0")
	if text := rowsText(m); !strings.Contains(text, "tested ███████░░░░░ 4/7 changed lines · 3 untested") {
		t.Errorf("overview misses the report bar:\n%s", text)
	}

	press(m, "u")
	if w := m.where(); w.Station.ID != "report" || w.Cursor.Line != 3 {
		t.Fatalf("u should stop at the untested import first, went to %s %+v", w.Station.ID, w.Cursor)
	}
	press(m, "u")
	w := m.where()
	if w.Station.ID != "report" || w.Cursor.Line != 15 || w.Cursor.Coverage != "untested" {
		t.Fatalf("u went to %s %+v", w.Station.ID, w.Cursor)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"15✗", "4/7 tested", "line 15 changed, and no test runs it"} {
		if !strings.Contains(view, want) {
			t.Errorf("report view missing %q", want)
		}
	}
	if strings.Contains(view, "no tests cover this stop") {
		t.Error("measured coverage should replace the agent's no tests warning")
	}
	press(m, "k")
	if w := m.where(); w.Cursor.Coverage != "run" || len(w.Cursor.Tests) != 1 || w.Cursor.Tests[0] != "test_summarize_totals" {
		t.Fatalf("line 14 = %+v", w.Cursor)
	}

	mustGoto(t, m, "tests")
	view = ansi.Strip(m.View())
	for _, want := range []string{"store   reserve   audit   report", "    rejects non-positive quantity  new", "changed lines no test runs"} {
		if !strings.Contains(view, want) {
			t.Errorf("grid missing %q:\n%s", want, view)
		}
	}
	for m.rows[m.cur].kind != rowTest || !strings.Contains(m.rows[m.cur].spans[0].Text, "rejects") {
		press(m, "j")
	}
	press(m, "enter")
	w = m.where()
	if w.Spotlight != "TestReserve/rejects_non-positive_quantity" || w.Station.ID != "store" || w.Cursor.Line != 20 {
		t.Fatalf("spotlight = %q at %s %+v", w.Spotlight, w.Station.ID, w.Cursor)
	}
	st := m.doc.Stations[2]
	if p := st.Parts[0]; !m.dimmed(p, 34) || m.dimmed(p, 31) {
		t.Error("the rejecting test runs the qty check but never takes the lock")
	}
	press(m, "esc")
	if m.where().Spotlight != "" {
		t.Fatal("esc should end the spotlight")
	}
}

func TestCoverageStale(t *testing.T) {
	m := newModel(t)
	stock := filepath.Join(filepath.Dir(m.opts.ReviewPath), "current", "inventory", "stock.go")
	data, err := os.ReadFile(stock)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stock, append(data, []byte("\n// edited\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	d, st, err := Load(m.opts.ReviewPath)
	if err != nil {
		t.Fatal(err)
	}
	m.Update(loadedMsg{doc: d, state: st})
	w := mustGoto(t, m, "reserve")
	view := ansi.Strip(m.View())
	if w.Cursor.Coverage != "" || !strings.Contains(view, "coverage stale") || strings.Contains(view, "32┃") {
		t.Fatalf("an edited file should show stale coverage and no marks:\n%s", view)
	}
}

func TestStopRationale(t *testing.T) {
	m := newModel(t)
	mustGoto(t, m, "reserve")
	view := ansi.Strip(m.View())
	for _, want := range []string{"Why a stop of its own", "What it does and how", "Reserve now rejects a quantity",
		"Why", "The whole of Reserve, since the check"} {
		if !strings.Contains(view, want) {
			t.Errorf("reserve misses %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "`available`") {
		t.Error("backticks should render as code, not literally")
	}

	path := m.opts.ReviewPath
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bare := regexp.MustCompile(`(?m)^    (scope|what|why): (>-\n(      .*\n)+|.*\n)`).ReplaceAll(data, nil)
	if err := os.WriteFile(path, bare, 0o644); err != nil {
		t.Fatal(err)
	}
	d, st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m.Update(loadedMsg{doc: d, state: st})
	mustGoto(t, m, "reserve")
	if view := ansi.Strip(m.View()); !strings.Contains(view, "no rationale yet") || strings.Contains(view, "What it does and how") {
		t.Errorf("a stop without rationale should say so:\n%s", view)
	}
}

func TestYank(t *testing.T) {
	m := newModel(t)
	var got string
	m.clip = func(s string) error { got = s; return nil }
	run := func(cmd tea.Cmd) {
		if cmd != nil {
			m.Update(cmd())
		}
	}

	mustGoto(t, m, "reserve:2")
	run(m.yank(false))
	want := "inventory/stock.go:39 · note 2 · looks right\nStill matches `errors.Is(err, ErrInsufficient)` because of `%w`.\n"
	if got != want || m.status != "copied note 2 to the clipboard" {
		t.Fatalf("y copied %q, status %q", got, m.status)
	}

	run(m.yank(true))
	for _, part := range []string{"reserve · Reserve validates before locking\nThe quantity check",
		"\n\nWhy a stop of its own\nThis is the only function", "\n\nstock.go · Store.Reserve: The whole of Reserve",
		"\n\ninventory/stock.go:32 · note 1 · context\n", "\n\ninventory/stock.go:42 · note 3 · context\n"} {
		if !strings.Contains(got, part) {
			t.Errorf("Y misses %q in:\n%s", part, got)
		}
	}

	mustGoto(t, m, "audit")
	got = ""
	run(m.yank(true))
	if got != "" || !strings.Contains(m.status, "press v") {
		t.Fatalf("a stop with hidden notes should not be copied: %q %q", got, m.status)
	}
}

func TestRecapWraps(t *testing.T) {
	m := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 60, Height: height})
	mustGoto(t, m, "recap")
	view := ansi.Strip(m.View())
	for _, want := range []string{"Zero entries come from", "cancelled", "somewhere?"} {
		if !strings.Contains(view, want) {
			t.Errorf("recap should wrap long notes instead of cutting them, missing %q:\n%s", want, view)
		}
	}
	press(m, "j", "j", "j")
	press(m, "enter")
	if w := m.where(); w.Station.ID != "report" || w.Note.Number != 1 {
		t.Fatalf("j should skip the wrapped lines to the next entry, enter opened %s %+v", w.Station.ID, w.Note)
	}
}
