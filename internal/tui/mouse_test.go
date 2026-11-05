package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/redrick/margin/internal/doc"
)

func click(m *Model, x, y int, shift bool) {
	m.Update(tea.MouseMsg{X: x, Y: y, Shift: shift, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
}

// codeStop goes to the first code stop and returns the screen rows of its new-side code lines.
func codeStop(t *testing.T, m *Model) []int {
	t.Helper()
	for i, s := range m.doc.Stations {
		if s.Kind == doc.Code {
			m.setStation(i)
			break
		}
	}
	var ys []int
	for i, r := range m.rows {
		if i >= m.top+m.bodyHeight() {
			break
		}
		if i >= m.top && r.kind == rowCode && !m.doc.Stations[m.station].Parts[r.part].Base {
			ys = append(ys, i-m.top+1)
		}
	}
	if len(ys) < 3 {
		t.Fatalf("the stop has too few lines on screen: %d", len(ys))
	}
	return ys
}

func TestClickLine(t *testing.T) {
	m := newModel(t)
	ys := codeStop(t, m)
	click(m, 5, ys[2], false)
	if m.cur != m.top+ys[2]-1 {
		t.Fatalf("a click moves the cursor to the line: %d", m.cur)
	}
	click(m, 5, ys[0], true)
	part, from, to, ok := m.block()
	if !ok || to-from != m.rows[m.top+ys[2]-1].line-m.rows[m.top+ys[0]-1].line || part != m.rows[m.cur].part {
		t.Fatalf("shift+click makes a block from the cursor: %d-%d %v", from, to, ok)
	}
	click(m, 5, ys[1], false)
	if m.blockAt != nil {
		t.Fatal("a plain click drops the block")
	}
}

func TestDragBlock(t *testing.T) {
	m := newModel(t)
	ys := codeStop(t, m)
	m.Update(tea.MouseMsg{X: 5, Y: ys[0], Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m.Update(tea.MouseMsg{X: 5, Y: ys[2], Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m.Update(tea.MouseMsg{X: 5, Y: ys[2], Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if _, from, to, ok := m.block(); !ok || to-from < 2 {
		t.Fatalf("dragging over lines makes a block of them: %d-%d %v", from, to, ok)
	}
	m.Update(tea.MouseMsg{X: 5, Y: ys[1], Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	if m.cur != m.top+ys[2]-1 {
		t.Fatal("moving after the release does nothing")
	}
}

func TestClickStops(t *testing.T) {
	m := newModel(t)
	press(m, "tab")
	if !m.listOpen {
		t.Fatal("tab opens the list of stops")
	}
	click(m, m.width/2, 2+2, false)
	if m.listOpen || m.station != 1 {
		t.Fatalf("a click on an entry goes to that stop: %d", m.station)
	}

	var codes []int
	for i, s := range m.doc.Stations {
		if s.Kind == doc.Code {
			codes = append(codes, i)
		}
	}
	if len(codes) < 2 {
		t.Skip("the example has one code stop, so no progress dots")
	}
	x := len([]rune(fmtHeaderTitle(m.doc.Stations[m.station].ID, m.station, len(m.doc.Stations))))
	click(m, x+1, 0, false)
	if m.station != codes[1] {
		t.Fatalf("a click on the second dot goes to the second code stop: %d", m.station)
	}
}

func TestWheel(t *testing.T) {
	m := newModel(t)
	codeStop(t, m)
	cur := m.cur
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if m.cur == cur {
		t.Fatal("the wheel moves through the lines")
	}
	press(m, "tab")
	at := m.listCur
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if m.listCur == at || m.cur == cur {
		t.Fatalf("with a list open, the wheel moves in the list: %d", m.listCur)
	}
}

func TestClickNoteCard(t *testing.T) {
	m := newModel(t)
	for i, s := range m.doc.Stations {
		if s.Kind != doc.Code || len(s.Notes) < 2 {
			continue
		}
		m.setStation(i)
		m.View()
		for line, row := range m.cardAt {
			if row == m.cur {
				continue
			}
			click(m, m.codeWidth()+3, line+1, false)
			if m.cur != row {
				t.Fatalf("a click on a card goes to its line: %d, want %d", m.cur, row)
			}
			return
		}
	}
	t.Skip("no stop with two cards on screen")
}
