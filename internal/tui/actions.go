package tui

import (
	"fmt"
	"path"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/review"
)

const tickEvery = 30 * time.Second

type tickMsg struct{}

func tick() tea.Cmd {
	return tea.Tick(tickEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

var filterNames = []string{"all notes", "problems, questions and your calls", "problems only"}

// filters are the fixed filters followed by one per focus area the notes use.
func (m *Model) filters() []string {
	out := append([]string(nil), filterNames...)
	if m.doc == nil {
		return out
	}
	seen := map[string]bool{}
	for _, area := range review.FocusAreas {
		for _, n := range m.doc.CodeNotes() {
			if n.Focus == area && !seen[area] {
				seen[area] = true
				out = append(out, "focus: "+area)
			}
		}
	}
	return out
}

func (m *Model) activate() {
	if m.cur < 0 || m.cur >= len(m.rows) {
		return
	}
	switch r := m.rows[m.cur]; r.kind {
	case rowLink:
		m.setStation(r.target)
		switch {
		case r.noteAt >= 0:
			m.gotoNote(r.noteAt)
		case r.file != "":
			if err := m.gotoFileLine(r.file, r.line+1); err != nil {
				m.setStatus(true, "%v", err)
			}
		}
	case rowFold:
		m.unfold = true
		m.rebuildKeep()
	case rowTest:
		m.spotlight(r.target)
	}
}

func (m *Model) dismiss() {
	n := m.selectedNote()
	if n == nil {
		m.setStatus(false, "no note selected")
		return
	}
	if m.state.Dismissed[n.Key] {
		delete(m.state.Dismissed, n.Key)
	} else {
		m.state.Dismissed[n.Key] = true
	}
	if err := m.state.Save(); err != nil {
		m.setStatus(true, "saving marks: %v", err)
		return
	}
	if m.state.Dismissed[n.Key] {
		m.setStatus(false, "note %d dismissed as not useful", m.note+1)
	} else {
		m.setStatus(false, "note %d restored", m.note+1)
	}
}

func (m *Model) reveal() {
	st := m.doc.Stations[m.station]
	if !m.blind(st) {
		m.setStatus(false, "notes are already shown")
		return
	}
	m.state.Revealed[st.ID] = true
	if err := m.state.Save(); err != nil {
		m.setStatus(true, "saving marks: %v", err)
	}
	m.rebuildKeep()
	m.setStatus(false, "notes shown; compare them with what you found")
}

func (m *Model) cycleFilter() {
	fs := m.filters()
	m.filter = (m.filter + 1) % len(fs)
	m.rebuildKeep()
	m.setStatus(false, "showing %s", fs[m.filter])
}

// pace describes this session's reading and whether it has gone on long enough, or fast enough, to
// hurt defect detection (roughly 400 lines or 60–90 minutes, and more than 500 lines an hour).
func (m *Model) pace() (string, bool) {
	s := fmt.Sprintf("%dm · %d lines read", m.sessionSecs/60, m.sessionLines)
	switch {
	case m.sessionSecs >= 75*60:
		return s + " · time for a break", true
	case m.sessionSecs >= 10*60 && m.sessionLines*3600/m.sessionSecs > 500:
		return s + " · fast, over 500 lines/h", true
	}
	return s, false
}

type copiedMsg struct {
	what string
	err  error
}

// yank copies the selected note, or with all the whole stop: its rationale and every note, as text
// that reads on its own when pasted into a PR or a chat.
func (m *Model) yank(all bool) tea.Cmd {
	if m.doc == nil {
		return nil
	}
	st := m.doc.Stations[m.station]
	if m.blind(st) {
		m.setStatus(false, "the notes are hidden on this stop until you press v")
		return nil
	}
	var text, what string
	switch {
	case all && st.Kind == doc.Code:
		text, what = m.stopText(st), "stop "+st.ID
	case all:
		m.setStatus(false, "Y copies a code stop; open one first")
		return nil
	case m.selectedNote() == nil:
		m.setStatus(false, "no note selected")
		return nil
	default:
		text, what = m.noteText(st, m.note), fmt.Sprintf("note %d", m.note+1)
	}
	copyFn := m.clip
	return func() tea.Msg { return copiedMsg{what: what, err: copyFn(text)} }
}

func (m *Model) noteText(st *doc.Station, i int) string {
	n := st.Notes[i]
	where := st.ID
	if n.Part >= 0 {
		where = fmt.Sprintf("%s:%d", st.Parts[n.Part].Spec.File, n.Line+1)
	}
	lines := []string{fmt.Sprintf("%s · note %d · %s", where, i+1, m.noteStyle(n).label)}
	if n.Q != "" {
		lines = append(lines, "Q: "+strings.TrimSpace(n.Q))
	}
	lines = append(lines, strings.TrimSpace(n.Text))
	if n.Evidence != "" {
		lines = append(lines, "evidence: "+strings.TrimSpace(n.Evidence))
	}
	if n.Changed != "" {
		lines = append(lines, "changed: "+strings.TrimSpace(n.Changed))
	}
	return strings.Join(lines, "\n") + "\n"
}

func (m *Model) stopText(st *doc.Station) string {
	blocks := []string{st.ID + " · " + st.Title}
	if st.Lede != "" {
		blocks[0] += "\n" + strings.TrimSpace(st.Lede)
	}
	for _, r := range rationaleOf(st) {
		if t := strings.TrimSpace(r.text); t != "" {
			blocks = append(blocks, r.label+"\n"+t)
		}
	}
	for _, p := range st.Parts {
		if about := strings.TrimSpace(p.Spec.About); about != "" && p.Hunk <= 1 {
			blocks = append(blocks, path.Base(p.Spec.File)+" · "+p.Spec.Label()+": "+about)
		}
	}
	for i, n := range st.Notes {
		if m.noteVisible(st, n) && !m.state.Dismissed[n.Key] {
			blocks = append(blocks, strings.TrimSuffix(m.noteText(st, i), "\n"))
		}
	}
	return strings.Join(blocks, "\n\n") + "\n"
}
