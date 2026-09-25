package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/redrick/margin/internal/diffmap"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/render"
)

// covMark is the gutter cell after the line number: whether any test runs the line.
func (m *Model) covMark(p *doc.Part, line int) (string, string, bool) {
	switch m.doc.Coverage.Line(p, line) {
	case doc.CovRun:
		return "┃", colTested, false
	case doc.CovMissed:
		return "✗", colProblem, true
	case doc.CovIdle:
		return "·", colDim, false
	}
	return " ", "", false
}

func covColor(sc doc.StopCov) string {
	switch {
	case sc.Missed == 0:
		return colAdded
	case sc.Run == 0:
		return colProblem
	}
	return colChanged
}

func covBar(sc doc.StopCov, w int) string {
	filled := (sc.Run*w + sc.Total()/2) / sc.Total()
	if sc.Run > 0 {
		filled = max(filled, 1)
	}
	if sc.Missed > 0 {
		filled = min(filled, w-1)
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", w-filled)
}

// covLine summarises a stop's coverage for the overview, or returns ok false when there is nothing to say.
func (m *Model) covLine(st *doc.Station) (string, string, bool) {
	c := m.doc.Coverage
	if c == nil {
		return "", "", false
	}
	sc := st.Coverage(c, -1)
	switch {
	case sc.Stale:
		return "coverage stale: the code changed after it was measured", colChanged, true
	case !sc.Measured && sc.Current:
		return "coverage: these files were not measured", colDim, true
	case !sc.Measured:
		return "", "", false
	case sc.Total() == 0:
		return "", "", false
	}
	s := fmt.Sprintf("tested %s %d/%d changed lines", covBar(sc, 12), sc.Run, sc.Total())
	if sc.Missed > 0 {
		s += fmt.Sprintf(" · %d untested", sc.Missed)
	}
	return s, covColor(sc), true
}

func (m *Model) coverPills(st *doc.Station) []seg {
	var segs []seg
	if c := m.doc.Coverage; c != nil && st.Kind == doc.Code {
		sc := st.Coverage(c, -1)
		switch {
		case sc.Stale:
			segs = append(segs, seg{text: " coverage stale ", fg: colInk, bg: colChanged, bold: true})
		case sc.Measured && sc.Total() > 0:
			segs = append(segs, seg{text: fmt.Sprintf(" %d/%d tested ", sc.Run, sc.Total()), fg: colInk, bg: covColor(sc), bold: true})
		}
		if len(segs) > 0 {
			segs = append(segs, seg{text: " ", bg: colBar})
		}
	}
	return segs
}

// needsTests is a stop the agent named no tests for, unless measured coverage shows tests run it.
func (m *Model) needsTests(st *doc.Station) bool {
	return st.NeedsTests() && st.Coverage(m.doc.Coverage, -1).Run == 0
}

func (m *Model) spotName() string {
	if m.spot < 0 || m.doc.Coverage == nil || m.spot >= len(m.doc.Coverage.Tests) {
		return ""
	}
	return m.doc.Coverage.Tests[m.spot].Label()
}

// dimmed reports whether the spotlight hides line: the spotlit test never runs it.
func (m *Model) dimmed(p *doc.Part, line int) bool {
	return m.spotName() != "" && !m.doc.Coverage.Runs(m.spot, p, line)
}

func (m *Model) gridRows() {
	c := m.doc.Coverage
	var cols []int
	for i, s := range m.doc.Stations {
		if s.Kind != doc.Code {
			continue
		}
		if sc := s.Coverage(c, -1); sc.Measured || sc.Stale {
			cols = append(cols, i)
		}
	}
	m.add(rowText, toneHeading, "What each test runs · enter on a test spotlights it")
	m.wrapped(toneDim, 0, "● runs every changed line of the stop, ◐ some, · none. Running a line is not checking it: a test can run a line and assert nothing about the result.")
	m.add(rowText, tonePlain, "")
	if len(cols) == 0 {
		m.wrapped(toneDim, 0, "None of the stops' files were measured.")
		return
	}

	nameW := 16
	groups := map[string]bool{}
	for _, t := range c.Tests {
		nameW = max(nameW, runewidth.StringWidth(testLabel(t))+2)
		groups[t.Group] = true
	}
	nameW = min(nameW, 48)
	labels := make([]string, len(cols))
	colW := 0
	for i, si := range cols {
		labels[i] = m.doc.Stations[si].ID
		colW = max(colW, runewidth.StringWidth(labels[i]))
	}
	colW = min(colW, 10) + 2
	avail := m.width - 4 - nameW
	numbered := colW*len(cols) > avail
	if numbered {
		colW = 4
		for i, si := range cols {
			labels[i] = strconv.Itoa(si)
		}
	}
	fit := min(len(cols), max(avail/colW, 1))

	head := []render.Span{{Text: strings.Repeat(" ", nameW)}}
	for i := range fit {
		head = append(head, render.Span{Text: center(doc.Short(labels[i], colW-2), colW), Color: colHeading, Bold: true})
	}
	m.gridText(head)

	for ti, t := range c.Tests {
		if len(groups) > 1 && t.Group != "" && (ti == 0 || c.Tests[ti-1].Group != t.Group) {
			m.gridText([]render.Span{{Text: t.Group, Color: colHeading}})
		}
		name := strings.Repeat("  ", t.Depth) + doc.Short(t.Display, nameW-2-2*t.Depth)
		fg := colText
		if t.Synthetic {
			fg = colDim
		}
		spans := []render.Span{{Text: name, Color: fg}}
		used := runewidth.StringWidth(name)
		if t.New && used+5 <= nameW {
			spans = append(spans, render.Span{Text: "  new", Color: colAdded})
			used += 5
		}
		spans = append(spans, render.Span{Text: strings.Repeat(" ", nameW-used)})
		for _, si := range cols[:fit] {
			glyph, gfg := " ", colDim
			if !t.Synthetic {
				sc := m.doc.Stations[si].Coverage(c, ti)
				switch {
				case sc.Stale:
					glyph, gfg = "?", colChanged
				case sc.Total() == 0:
				case sc.Missed == 0:
					glyph, gfg = "●", colAdded
				case sc.Run > 0:
					glyph, gfg = "◐", colChanged
				default:
					glyph = "·"
				}
			}
			spans = append(spans, render.Span{Text: center(glyph, colW), Color: gfg})
		}
		if t.Synthetic {
			m.gridText(spans)
		} else {
			m.rows = append(m.rows, row{kind: rowTest, target: ti, spans: spans})
		}
	}

	m.gridText([]render.Span{{Text: strings.Repeat("─", nameW+colW*fit), Color: colDim}})
	foot := []render.Span{{Text: runewidth.FillRight("changed lines no test runs", nameW), Color: colText}}
	for _, si := range cols[:fit] {
		sc := m.doc.Stations[si].Coverage(c, -1)
		text, fg := strconv.Itoa(sc.Missed), colAdded
		switch {
		case sc.Stale:
			text, fg = "stale", colChanged
		case sc.Missed > 0:
			fg = colProblem
		}
		foot = append(foot, render.Span{Text: center(text, colW), Color: fg, Bold: sc.Missed > 0})
	}
	m.gridText(foot)
	if numbered {
		var legend []string
		for _, si := range cols[:fit] {
			legend = append(legend, fmt.Sprintf("%d %s", si, m.doc.Stations[si].ID))
		}
		m.wrapped(toneDim, 0, "stops: "+strings.Join(legend, " · "))
	}
	if fit < len(cols) {
		m.wrapped(toneDim, 0, fmt.Sprintf("%d more stops do not fit; widen the pane to see them.", len(cols)-fit))
	}
	for _, si := range cols {
		if m.doc.Stations[si].Coverage(c, -1).Stale {
			m.wrapped(toneDim, 0, "? and stale: the code changed after coverage was measured; ask the agent to measure it again.")
			break
		}
	}
}

func (m *Model) gridText(spans []render.Span) {
	m.rows = append(m.rows, row{kind: rowText, spans: append([]render.Span{{Text: " "}}, spans...)})
}

func testLabel(t doc.CovTest) string {
	return strings.Repeat("  ", t.Depth) + t.Display
}

func center(s string, w int) string {
	sw := runewidth.StringWidth(s)
	if sw >= w {
		return s
	}
	left := (w - sw) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", w-sw-left)
}

func (m *Model) spotlight(ti int) {
	m.spot = ti
	name := m.spotName()
	var first, any *hit
	c := m.doc.Coverage
	for si, st := range m.doc.Stations {
		if st.Kind != doc.Code {
			continue
		}
		for pi, p := range st.Parts {
			if p.Err != nil {
				continue
			}
			rng := m.partRange(si, pi)
			for i := rng.Start; i <= rng.End && i < len(p.Lines) && first == nil; i++ {
				if !c.Runs(ti, p, i) {
					continue
				}
				h := hit{si, pi, i}
				if any == nil {
					any = &h
				}
				if k := lineKind(p, i); k == diffmap.Added || k == diffmap.Changed {
					first = &h
				}
			}
		}
	}
	if first == nil {
		first = any
	}
	if first == nil {
		m.setStatus(false, "%s runs none of the lines this review shows · esc ends", name)
		return
	}
	m.jump(*first)
	m.setStatus(false, "lines this test never runs are dimmed · esc ends")
}

func (m *Model) untested() []hit {
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
				if m.doc.Coverage.Line(p, i) == doc.CovMissed {
					out = append(out, hit{si, pi, i})
				}
			}
		}
	}
	return out
}

func (m *Model) nextUntested(dir int) {
	if m.doc == nil {
		return
	}
	if m.doc.Coverage == nil {
		m.setStatus(false, "no coverage yet: ask the agent to run margin coverage")
		return
	}
	hs := m.untested()
	if len(hs) == 0 {
		m.setStatus(false, "a test runs every measured changed line")
		return
	}
	idx, wrapped := m.pick(hs, dir)
	m.jump(hs[idx])
	m.setStatus(false, "untested line %d of %d%s", idx+1, len(hs), wrapped)
}

// coverInfo describes the cursor line's coverage for the bottom of the notes column.
func (m *Model) coverInfo(w int) []string {
	r, ok := m.cursorRow()
	c := m.doc.Coverage
	if !ok || c == nil {
		return nil
	}
	p := m.doc.Stations[m.station].Parts[r.part]
	switch c.Line(p, r.line) {
	case doc.CovRun:
		ts := c.TestsAt(p, r.line)
		word := "tests"
		if len(ts) == 1 {
			word = "test"
		}
		out := []string{m.paint.Text(fmt.Sprintf(" ┃ line %d runs in %d %s", r.line+1, len(ts), word), w, colTested, "", true, false)}
		for i, t := range ts {
			if i == 3 {
				out = append(out, m.paint.Text(fmt.Sprintf("   and %d more", len(ts)-3), w, colDim, "", false, false))
				break
			}
			out = append(out, m.paint.Text("   "+c.Tests[t].Label(), w, colText, "", false, false))
		}
		return out
	case doc.CovMissed:
		return []string{m.paint.Text(fmt.Sprintf(" ✗ line %d changed, and no test runs it", r.line+1), w, colProblem, "", true, false)}
	}
	return nil
}

// coverSummary is coverInfo in one line, for when the notes column has no room for more.
func (m *Model) coverSummary() string {
	r, ok := m.cursorRow()
	if !ok || m.doc.Coverage == nil {
		return ""
	}
	p := m.doc.Stations[m.station].Parts[r.part]
	switch m.doc.Coverage.Line(p, r.line) {
	case doc.CovRun:
		return fmt.Sprintf("┃ line %d runs in %d tests", r.line+1, len(m.doc.Coverage.TestsAt(p, r.line)))
	case doc.CovMissed:
		return fmt.Sprintf("✗ line %d: no test runs it", r.line+1)
	}
	return ""
}

func covName(k doc.LineCov) string {
	switch k {
	case doc.CovRun:
		return "run"
	case doc.CovMissed:
		return "untested"
	case doc.CovIdle:
		return "not run"
	case doc.CovInert:
		return "not executable"
	}
	return ""
}
