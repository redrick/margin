package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const tickEvery = 30 * time.Second

type tickMsg struct{}

func tick() tea.Cmd {
	return tea.Tick(tickEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

var filterNames = []string{"all notes", "problems and questions", "problems only"}

func (m *Model) activate() {
	if m.cur < 0 || m.cur >= len(m.rows) {
		return
	}
	switch r := m.rows[m.cur]; r.kind {
	case rowLink:
		m.setStation(r.target)
		if r.noteAt >= 0 {
			m.gotoNote(r.noteAt)
		}
	case rowFold:
		m.unfold = true
		m.rebuildKeep()
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
	m.filter = (m.filter + 1) % len(filterNames)
	m.rebuildKeep()
	m.setStatus(false, "showing %s", filterNames[m.filter])
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
