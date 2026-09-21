package tui

import (
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/render"
)

type hit struct{ station, part, line int }

func (h hit) before(o hit) bool {
	if h.station != o.station {
		return h.station < o.station
	}
	if h.part != o.part {
		return h.part < o.part
	}
	return h.line < o.line
}

func (m *Model) startSearch() tea.Cmd {
	m.searching = true
	m.input.Prompt = " / "
	m.input.Placeholder = "search the code, enter jumps, esc cancels"
	if m.lastQuery != "" {
		m.input.Placeholder = "search the code, enter repeats /" + m.lastQuery
	}
	m.input.SetValue("")
	return m.input.Focus()
}

func (m *Model) updateSearch(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		m.searching = false
		m.input.Blur()
		return nil
	case tea.KeyEnter:
		m.searching = false
		m.input.Blur()
		if q := m.input.Value(); q != "" {
			m.lastQuery = q
		}
		m.query = m.lastQuery
		if m.query != "" {
			m.searchNext(1)
		}
		return nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

func (m *Model) endSearch() {
	m.query = ""
	m.setStatus(false, "")
}

// matchesIn returns the rune ranges of line that match the query, ignoring case unless the query has
// an upper-case letter, like vim's smartcase.
func (m *Model) matchesIn(line string) [][2]int {
	q := []rune(m.query)
	if len(q) == 0 {
		return nil
	}
	fold := true
	for _, r := range q {
		if unicode.IsUpper(r) {
			fold = false
			break
		}
	}
	rs := []rune(line)
	var out [][2]int
	for i := 0; i+len(q) <= len(rs); {
		if runesMatch(rs[i:i+len(q)], q, fold) {
			out = append(out, [2]int{i, i + len(q)})
			i += len(q)
		} else {
			i++
		}
	}
	return out
}

func runesMatch(a, b []rune, fold bool) bool {
	for i := range a {
		if a[i] != b[i] && (!fold || unicode.ToLower(a[i]) != b[i]) {
			return false
		}
	}
	return true
}

func (m *Model) hits() []hit {
	var out []hit
	for si, st := range m.doc.Stations {
		if st.Kind != doc.Code {
			continue
		}
		for pi, p := range st.Parts {
			if p.Err != nil {
				continue
			}
			rng := m.partRange(si, pi)
			for i := rng.Start; i <= rng.End && i < len(p.Lines); i++ {
				if len(m.matchesIn(p.Lines[i])) > 0 {
					out = append(out, hit{si, pi, i})
				}
			}
		}
	}
	return out
}

func (m *Model) searchNext(dir int) {
	if m.doc == nil {
		return
	}
	hs := m.hits()
	if len(hs) == 0 {
		m.setStatus(true, "/%s not found", m.query)
		return
	}
	cur := hit{m.station, -1, -1}
	if r, ok := m.cursorRow(); ok {
		cur.part, cur.line = r.part, r.line
	}
	idx := -1
	if dir > 0 {
		for i, h := range hs {
			if cur.before(h) {
				idx = i
				break
			}
		}
	} else {
		for i := len(hs) - 1; i >= 0; i-- {
			if hs[i].before(cur) {
				idx = i
				break
			}
		}
	}
	wrapped := ""
	if idx < 0 {
		idx, wrapped = 0, " · wrapped to the first stop"
		if dir < 0 {
			idx, wrapped = len(hs)-1, " · wrapped to the last stop"
		}
	}
	m.jump(hs[idx])
	m.setStatus(false, "match %d of %d%s", idx+1, len(hs), wrapped)
}

func (m *Model) jump(h hit) {
	if m.station != h.station {
		m.setStation(h.station)
	}
	idx := m.rowOf(h)
	if idx < 0 && !m.unfold {
		m.unfold = true
		m.rebuild()
		idx = m.rowOf(h)
	}
	if idx < 0 {
		return
	}
	m.setCursor(idx)
	m.showMatch(h)
}

func (m *Model) rowOf(h hit) int {
	for i, r := range m.rows {
		if r.kind == rowCode && r.part == h.part && r.line == h.line {
			return i
		}
	}
	return -1
}

// showMatch scrolls sideways when the first match on the line is out of view.
func (m *Model) showMatch(h hit) {
	p := m.doc.Stations[h.station].Parts[h.part]
	line := p.Lines[h.line]
	rs := m.matchesIn(line)
	if len(rs) == 0 {
		return
	}
	start, end := displayCol(line, rs[0][0]), displayCol(line, rs[0][1])
	total := m.codeWidth() - m.numWidth() - 7
	if m.splitPart(p) {
		total = m.codeWidth() - m.numWidth() - 9
		total -= total / 2
	}
	switch {
	case start >= m.hoff && end <= m.hoff+total:
	case end <= total:
		m.hoff = 0
	default:
		m.hoff = max(start-total/4, 0)
	}
}

func displayCol(line string, runes int) int {
	col := 0
	for i, r := range []rune(line) {
		if i >= runes {
			break
		}
		if r == '\t' {
			col += render.TabWidth - col%render.TabWidth
		} else {
			col += runewidth.RuneWidth(r)
		}
	}
	return col
}

func (m *Model) lineSpans(p *doc.Part, line int) []render.Span {
	s := m.spansFor(p)[line]
	if m.query == "" {
		return s
	}
	return markSpans(s, m.matchesIn(p.Lines[line]))
}

// markSpans splits spans at the sorted rune ranges and draws the runes inside them as matches.
func markSpans(spans []render.Span, ranges [][2]int) []render.Span {
	if len(ranges) == 0 {
		return spans
	}
	var out []render.Span
	pos := 0
	for _, sp := range spans {
		rs := []rune(sp.Text)
		end := pos + len(rs)
		for at := pos; at < end; {
			in, until := false, end
			for _, r := range ranges {
				if at >= r[0] && at < r[1] {
					in, until = true, min(end, r[1])
					break
				}
				if r[0] > at {
					until = min(end, r[0])
					break
				}
			}
			piece := sp
			piece.Text = string(rs[at-pos : until-pos])
			if in {
				piece.Color, piece.Bg = colInk, colMatch
			}
			out = append(out, piece)
			at = until
		}
		pos = end
	}
	return out
}
