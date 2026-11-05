package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/redrick/margin/internal/doc"
)

// Clicks choose where the reader is: a line, a link, a note card, a stop, an entry of an open list.
// Every action stays on its key; the mouse never does what a key cannot.

// overlayOpen says whether a box is drawn over the body, which then takes the wheel and clicks.
func (m *Model) overlayOpen() bool {
	return m.listOpen || m.helpOpen || m.peek != nil || m.testsOpen || m.ans != nil || (m.outOpen && m.run != nil)
}

func (m *Model) mouse(msg tea.MouseMsg) tea.Cmd {
	if m.doc == nil || m.edit != nil || m.labeling || m.asking || m.searching {
		return nil
	}
	wheel := map[tea.MouseButton]tea.KeyType{tea.MouseButtonWheelUp: tea.KeyUp, tea.MouseButtonWheelDown: tea.KeyDown}
	if k, ok := wheel[msg.Button]; ok && msg.Action == tea.MouseActionPress {
		if m.overlayOpen() {
			for range 3 {
				m.Update(tea.KeyMsg{Type: k})
			}
			return nil
		}
		if k == tea.KeyUp {
			m.move(-3)
		} else {
			m.move(3)
		}
		return nil
	}
	switch {
	case msg.Action == tea.MouseActionRelease:
		m.dragFrom = -1
		return nil
	case msg.Action == tea.MouseActionMotion && msg.Button == tea.MouseButtonLeft:
		m.drag(msg.Y)
		return nil
	case msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft:
		return nil
	}
	m.lastInput = time.Now()
	h := m.bodyHeight()
	switch {
	case msg.Y == 0:
		m.clickHeader(msg.X)
		return nil
	case msg.Y > h:
		return nil
	case m.overlayOpen():
		return m.clickOverlay(msg.Y - 2)
	}
	line := msg.Y - 1
	if m.notesShown() && msg.X > m.codeWidth() {
		if row, ok := m.cardAt[line]; ok && row < len(m.rows) {
			m.setCursor(row)
		}
		return nil
	}
	idx := m.top + line
	if idx >= len(m.rows) || !m.rows[idx].selectable() {
		return nil
	}
	double := time.Since(m.lastClick) < doubleClick && m.clickRow == idx
	m.lastClick, m.clickRow = time.Now(), idx
	r := m.rows[idx]
	if r.kind != rowCode {
		m.setCursor(idx)
		m.activate()
		return nil
	}
	extend := msg.Shift || msg.Alt
	switch {
	case extend && m.blockAt == nil:
		m.startBlock(m.cur)
	case !extend && m.blockAt != nil:
		m.blockAt = nil
		m.setStatus(false, "")
	}
	m.setCursor(idx)
	m.dragFrom = idx
	if double && !extend {
		m.activate()
	}
	return nil
}

// drag grows a block from the line the press started on to the line under the pointer, scrolling
// when the pointer leaves the body.
func (m *Model) drag(y int) {
	if m.dragFrom < 0 || m.dragFrom >= len(m.rows) || m.overlayOpen() {
		return
	}
	h := m.bodyHeight()
	idx := m.top + y - 1
	switch {
	case y < 1:
		idx = m.cur - 1
	case y > h:
		idx = m.cur + 1
	}
	if idx < 0 || idx >= len(m.rows) || idx == m.cur {
		return
	}
	from, r := m.rows[m.dragFrom], m.rows[idx]
	if r.kind != rowCode || r.part != from.part {
		return
	}
	if m.blockAt == nil {
		m.startBlock(m.dragFrom)
		if m.blockAt == nil {
			m.dragFrom = -1
			return
		}
	}
	m.setCursor(idx)
}

// startBlock marks a block from row i, as V does on it.
func (m *Model) startBlock(i int) {
	cur := m.cur
	m.cur = i
	m.toggleMark()
	m.cur = cur
}

// clickHeader goes to the stop of a progress dot.
func (m *Model) clickHeader(x int) {
	dots := m.progressDots()
	if dots == nil {
		return
	}
	st := m.doc.Stations[m.station]
	first := runewidth.StringWidth(fmtHeaderTitle(st.ID, m.station, len(m.doc.Stations)))
	var codes []int
	for i, s := range m.doc.Stations {
		if s.Kind == doc.Code {
			codes = append(codes, i)
		}
	}
	if i := x - first; i >= 0 && i < len(codes) {
		m.setStation(codes[i])
	}
}

// clickOverlay picks entry e of the box drawn over the body; entry 0 is its title.
func (m *Model) clickOverlay(e int) tea.Cmd {
	switch {
	case e < 1:
		return nil
	case m.listOpen:
		if i := e - 1; i < len(m.doc.Stations) {
			m.listOpen = false
			m.setStation(i)
		}
	case m.testsOpen:
		_, hl := m.testsLines()
		first := m.tests.cur - (hl - 1)
		if i := first + e - 1; i >= 0 && i < len(m.tests.list) {
			double := i == m.tests.cur
			m.tests.cur = i
			if double {
				return m.updateTests(tea.KeyMsg{Type: tea.KeyEnter})
			}
		}
	case m.helpOpen:
		m.helpOpen = false
	}
	return nil
}
