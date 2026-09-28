package tui

import (
	"fmt"
	"path"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/redrick/margin/internal/around"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/source"
	"github.com/redrick/margin/internal/text"
)

// aroundEntry is what margin found around one stop, or nil fields while it is still looking.
type aroundEntry struct {
	done   bool
	report around.Report
}

type aroundMsg struct {
	doc    *doc.Doc
	key    string
	report around.Report
}

const maxUsesShown = 8

func aroundKey(st *doc.Station) string { return st.ID + "|" + st.Title }

// lookAround starts the search around the current stop the first time the reader opens it.
func (m *Model) lookAround() tea.Cmd {
	if m.doc == nil || m.station >= len(m.doc.Stations) {
		return nil
	}
	st := m.doc.Stations[m.station]
	if st.Kind != doc.Code {
		return nil
	}
	key := aroundKey(st)
	if m.around[key] != nil {
		return nil
	}
	m.around[key] = &aroundEntry{}
	d := m.doc
	return func() tea.Msg {
		files, err := source.Open(d.Review)
		if err != nil {
			return aroundMsg{doc: d, key: key, report: around.Report{Err: err}}
		}
		defer files.Close()
		return aroundMsg{doc: d, key: key, report: around.Study(d, st, files)}
	}
}

func (m *Model) gotAround(msg aroundMsg) {
	if msg.doc != m.doc {
		return
	}
	for i := range msg.report.Symbols {
		around.SortUses(msg.report.Symbols[i].Uses)
	}
	m.around[msg.key] = &aroundEntry{done: true, report: msg.report}
	if st := m.doc.Stations[m.station]; aroundKey(st) == msg.key {
		m.rebuildKeep()
	}
}

// aroundRows draws who outside the tour uses what the stop changes, and which commits wrote the
// code it replaces. It reports whether anything was drawn.
func (m *Model) aroundRows(st *doc.Station) bool {
	e := m.around[aroundKey(st)]
	if e == nil {
		return false
	}
	if !e.done {
		m.add(rowText, toneDim, "Around this stop: looking for callers and history…")
		return true
	}
	r := e.report
	if len(r.Symbols) == 0 && len(r.History) == 0 && r.Err == nil {
		return false
	}
	m.add(rowText, toneHeading, "Around this stop")
	if r.Err != nil {
		m.wrapped(toneProblem, 2, r.Err.Error())
	}
	for _, s := range r.Symbols {
		m.symbolRows(s)
	}
	if len(r.History) > 0 {
		m.add(rowText, toneDim, "  the replaced code was written in · enter reads the commit")
		for _, c := range r.History {
			line := fmt.Sprintf("    %s %s %s  %s", c.SHA, c.Date, c.Author, c.Subject)
			m.rows = append(m.rows, row{kind: rowLink, fg: colText, text: line, target: m.station, noteAt: -1, comment: -1, peek: true, sha: c.SHA})
		}
	}
	return true
}

func (m *Model) symbolRows(s around.Symbol) {
	what := "func"
	if s.Type {
		what = "type"
	}
	state := "changed"
	if s.Removed {
		state = "removed"
	}
	head := fmt.Sprintf("  %s %s, %s (%s:%d)", what, s.Name, state, path.Base(s.File), s.Line)
	switch {
	case s.TooMany > 0:
		m.colored(colDim, fmt.Sprintf("%s: used in %d+ places, too common a name to list", head, s.TooMany))
		return
	case len(s.Uses) == 0 && s.Removed:
		m.colored(colAdded, head+": nothing else uses it")
		return
	case len(s.Uses) == 0:
		m.colored(colDim, head+": no other users outside the tour")
		return
	}
	fg := colChanged
	if s.Removed {
		fg = colProblem
	}
	summary := fmt.Sprintf("%s: %d outside the tour", head, len(s.Uses))
	if s.Inside > 0 {
		summary += fmt.Sprintf(", %d inside", s.Inside)
	}
	m.colored(fg, summary+" · enter peeks")
	for i, u := range s.Uses {
		if i == maxUsesShown {
			m.colored(colDim, fmt.Sprintf("    … %d more", len(s.Uses)-maxUsesShown))
			break
		}
		line := fmt.Sprintf("    %s:%d  %s", u.File, u.Line, strings.TrimSpace(u.Text))
		m.rows = append(m.rows, row{kind: rowLink, fg: colText, text: line, target: m.station, noteAt: -1, comment: -1, peek: true, file: u.File, line: u.Line - 1})
	}
}

// exampleRows draws the agent's before/after table. It reports whether anything was drawn.
func (m *Model) exampleRows(st *doc.Station) bool {
	if len(st.Examples) == 0 {
		return false
	}
	m.add(rowText, toneHeading, "Before and after")
	w := max(m.codeWidth()-12, 10)
	field := func(fg, label, s string) {
		for i, l := range wrap(oneLine(text.Sanitize(s)), w) {
			if i == 0 {
				m.colored(fg, fmt.Sprintf("    %-7s %s", label, l))
			} else {
				m.colored(fg, "            "+l)
			}
		}
	}
	for _, e := range st.Examples {
		m.wrapped(tonePlain, 2, "▸ "+oneLine(text.Sanitize(e.Input)))
		if e.Before == e.After {
			field(colDim, "same", e.After)
		} else {
			field(colRemoved, "before", e.Before)
			field(colAdded, "after", e.After)
		}
		if e.Note != "" {
			m.wrapped(toneDim, 4, oneLine(text.Sanitize(e.Note)))
		}
	}
	return true
}

// openPeek shows a commit message, or the code around a line, in an overlay.
func (m *Model) openPeek(r row) {
	if r.sha != "" {
		msg, err := around.Message(m.doc.Repo, r.sha)
		if err != nil {
			m.setStatus(true, "reading commit %s: %v", r.sha, err)
			return
		}
		lines := text.Lines(msg)
		m.peek, m.peekAt, m.peekTop = append([]string{" commit " + r.sha + " · any key closes"}, indent(lines)...), -1, 0
		m.peekStyled = false
		return
	}
	files, err := source.Open(m.doc.Review)
	if err != nil {
		m.setStatus(true, "%v", err)
		return
	}
	defer files.Close()
	f, err := files.Current(r.file)
	if err != nil {
		m.setStatus(true, "reading %s: %v", r.file, err)
		return
	}
	lines := text.Lines(text.Sanitize(f.Content))
	if r.line >= len(lines) {
		m.setStatus(true, "%s has no line %d", r.file, r.line+1)
		return
	}
	span := max((m.bodyHeight()-3)/2, 3)
	from, to := max(r.line-span, 0), min(r.line+span, len(lines)-1)
	out := []string{fmt.Sprintf(" %s:%d · any key closes", r.file, r.line+1)}
	numW := len(fmt.Sprint(to + 1))
	for i := from; i <= to; i++ {
		if i == r.line {
			m.peekAt = len(out)
		}
		out = append(out, fmt.Sprintf(" %*d  %s", numW, i+1, strings.ReplaceAll(lines[i], "\t", "    ")))
	}
	m.peek, m.peekTop, m.peekStyled = out, 0, false
}

// peekKey scrolls a peek taller than the pane with j and k; any other key closes it.
func (m *Model) peekKey(msg tea.KeyMsg) {
	last := max(len(m.peek)-1-(m.bodyHeight()-2), 0)
	switch msg.String() {
	case "j", "down":
		m.peekTop = min(m.peekTop+1, last)
	case "k", "up":
		m.peekTop = max(m.peekTop-1, 0)
	case "ctrl+d", "pgdown", " ":
		m.peekTop = min(m.peekTop+m.bodyHeight()/2, last)
	case "ctrl+u", "pgup":
		m.peekTop = max(m.peekTop-m.bodyHeight()/2, 0)
	default:
		m.peek = nil
	}
}

// peekLines is the part of the peek in view, under its title line.
func (m *Model) peekLines() ([]string, int) {
	title := m.peek[0]
	if len(m.peek)-1 > m.bodyHeight()-2 {
		title = strings.Replace(title, "any key closes", "j k scroll · any other key closes", 1)
	}
	return append([]string{title}, m.peek[1+m.peekTop:]...), m.peekAt - m.peekTop
}

func indent(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = "  " + strings.ReplaceAll(l, "\t", "    ")
	}
	return out
}

// readNotes shows the notes on a line in full in an overlay, for a card too tall for the column.
func (m *Model) readNotes(r row) {
	st := m.doc.Stations[m.station]
	if len(r.notes) == 0 || m.blind(st) {
		return
	}
	// The same cards as the notes column, full length, inside the overlay's box.
	w := min(peekWidth, m.width-4)
	if w < 30 {
		return
	}
	p := m.paint
	edge := p.Text(" ", 1, colText, colBar, false, false)
	blank := p.Text("", w, colText, colBar, false, false)
	out := []string{fmt.Sprintf(" notes on %s:%d · any key closes", path.Base(st.Parts[r.part].Spec.File), r.line+1)}
	for _, ni := range r.notes {
		out = append(out, blank)
		for _, l := range m.noteCardOn(st, ni, w-2, false, colBar) {
			out = append(out, edge+l+edge)
		}
	}
	out = append(out, blank)
	m.peek, m.peekAt, m.peekTop, m.peekStyled = out, -1, 0, true
}
