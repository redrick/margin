package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/redrick/margin/internal/state"
)

func TestIntentOnOverview(t *testing.T) {
	m := newModel(t)
	mustGoto(t, m, "0")
	text := strings.Join(strings.Fields(rowsText(m)), " ")
	for _, want := range []string{
		"In plain words", "Nobody could tell why an item went out of stock",
		"Asked · from the commit message", "Record every reservation in an audit log",
		"Did", "Adds an in-memory AuditLog", "Gap", "neither of which was asked for",
		"complexity: medium", "because a second lock, taken while the store's lock is held",
		"Focus areas", "breaking-change · store · stock.go:20",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("overview misses %q", want)
		}
	}
	if strings.Contains(text, "The log keeps every entry") {
		t.Error("the focus list must not reveal notes of a high-risk stop the reader has not opened")
	}
}

func TestCallsChecklist(t *testing.T) {
	m := newModel(t)
	mustGoto(t, m, "store")
	if text := rowsText(m); !strings.Contains(text, "Your calls in this stop") || !strings.Contains(text, "☐ Should a store without an audit log") {
		t.Fatalf("the stop should open with its calls:\n%s", text)
	}
	if !strings.Contains(ansi.Strip(m.footerView()), "◆ 0/2") {
		t.Errorf("footer should count the calls: %s", ansi.Strip(m.footerView()))
	}
	mustGoto(t, m, "store:3")
	press(m, " ")
	mustGoto(t, m, "recap")
	text := rowsText(m)
	if !strings.Contains(text, "Your calls") || !strings.Contains(text, "☑ store · stock.go:21") || !strings.Contains(text, "☐ audit · audit.go:30") {
		t.Fatalf("recap should tick off the call that was made:\n%s", text)
	}
	if c := m.counts(); c.Calls != 2 || c.CallsMade != 1 {
		t.Errorf("counts = %+v", c)
	}
}

func TestComments(t *testing.T) {
	m := newModel(t)
	mustGoto(t, m, "reserve:1")
	press(m, "c", "log the rejected quantity too", "enter")
	if len(m.state.Questions) != 1 {
		t.Fatalf("questions = %+v", m.state.Questions)
	}
	q := m.state.Questions[0]
	if q.ID != "c1" || !q.IsComment() || !q.Draft || q.Line != 32 || q.Station != "reserve" {
		t.Fatalf("comment = %+v", q)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"✎ 1 draft · S sends", " ✎ ", "your comment c1 · draft"} {
		if !strings.Contains(view, want) {
			t.Errorf("view misses %q:\n%s", want, view)
		}
	}
	if qs := Questions(m.doc.Review, m.state, false); len(qs) != 0 {
		t.Errorf("drafts are not the agent's business until they are sent: %+v", qs)
	}

	press(m, "S")
	if !m.state.Questions[0].Draft || !strings.Contains(m.status, "no agent pane") {
		t.Fatalf("without an agent pane the draft stays a draft: %+v %q", m.state.Questions[0], m.status)
	}
	msg := CommentsMessage(m.drafts())
	for _, want := range []string{"[margin comments] 1 review comments", "c1 inventory/stock.go:32", "margin resolve"} {
		if !strings.Contains(msg, want) {
			t.Errorf("agent message misses %q: %s", want, msg)
		}
	}

	m.opts.AgentPane = "%999"
	cmd := m.sendComments()
	if cmd == nil || m.state.Questions[0].Draft {
		t.Fatal("S should mark the drafts sent and send them")
	}
	m.Update(sentMsg{id: "c1", err: os.ErrClosed, drafts: []string{"c1"}})
	if !m.state.Questions[0].Draft {
		t.Fatal("a failed send should turn the comments back into drafts")
	}

	mustGoto(t, m, "recap")
	found := false
	for i, r := range m.rows {
		if r.kind == rowLink && r.comment == 0 {
			m.cur, found = i, true
		}
	}
	if !found || !strings.Contains(rowsText(m), "c1 draft · stock.go:32") {
		t.Fatalf("recap should list the comment:\n%s", rowsText(m))
	}
	press(m, "enter")
	if w := m.where(); w.Station.ID != "reserve" || w.Cursor == nil || w.Cursor.Line != 32 {
		t.Fatalf("enter on a comment should jump to its line, got %s %+v", w.Station.ID, w.Cursor)
	}
	mustGoto(t, m, "recap")
	for i, r := range m.rows {
		if r.kind == rowLink && r.comment == 0 {
			m.cur = i
		}
	}
	press(m, "x")
	st, err := state.Load(m.state.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Questions) != 0 {
		t.Fatalf("x should delete the draft, left %+v", st.Questions)
	}
}

func TestMovedAndUnplaced(t *testing.T) {
	dir := t.TempDir()
	helper := "func Helper(x int) int {\n\ty := x * 2\n\tz := y + 3\n\tw := z * z\n\tv := w - y\n\tu := v + x\n\treturn u\n}\n"
	for name, content := range map[string]string{
		"base/a.go":    "package a\n\nfunc Keep() int {\n\treturn 1\n}\n\n" + helper,
		"current/a.go": "package a\n\nfunc Keep() int {\n\treturn 1\n}\n",
		"current/b.go": "package a\n\n" + helper,
		"current/c.go": "package a\n\nvar Forgotten = 1\n",
		"x.review.yaml": `version: 1
repo: current
base_dir: base
title: move
stations:
  - id: a
    title: a
    parts: [{file: a.go, hunks: true}]
  - id: b
    title: b
    parts: [{file: b.go, hunks: true}]
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
	m := New(Options{ReviewPath: path})
	d, st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m.Update(loadedMsg{doc: d, state: st})

	mustGoto(t, m, "b")
	view := ansi.Strip(m.View())
	for _, want := range []string{"lines moved from a.go, old line 7, unchanged", "8 moved unchanged"} {
		if !strings.Contains(view, want) {
			t.Errorf("b view misses %q:\n%s", want, view)
		}
	}
	press(m, "z")
	if view := ansi.Strip(m.View()); !strings.Contains(view, "▎→ func Helper(x int) int {") {
		t.Errorf("unfolded, the moved lines are marked with →:\n%s", view)
	}
	mustGoto(t, m, "a")
	if view := ansi.Strip(m.View()); !strings.Contains(view, "▎← func Helper(x int) int {") {
		t.Errorf("the removed side of a move is marked with ←:\n%s", view)
	}

	mustGoto(t, m, "0")
	if text := rowsText(m); !strings.Contains(text, "unplaced · Changes no stop shows") || !strings.Contains(text, "c.go") {
		t.Errorf("the overview should list the unplaced stop and report it:\n%s", text)
	}
	w := mustGoto(t, m, "unplaced")
	if text := rowsText(m); !strings.Contains(text, "in no stop of the tour") || strings.Contains(text, "no rationale yet") {
		t.Errorf("the unplaced stop explains itself instead of asking for a rationale:\n%s", text)
	}
	if w.Station.Title != "Changes no stop shows" {
		t.Errorf("where = %+v", w.Station)
	}
}
