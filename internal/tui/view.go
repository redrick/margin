package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/redrick/margin/internal/diffmap"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/render"
)

type teaCmd = tea.Cmd

func wrap(s string, w int) []string {
	return strings.Split(ansi.Wrap(s, w, ""), "\n")
}

func (m *Model) View() string {
	if m.width == 0 {
		return ""
	}
	if m.doc == nil {
		msg := "loading " + m.opts.ReviewPath + " …"
		if m.loadErr != nil {
			msg = "margin could not load the review:\n\n" + m.loadErr.Error() + "\n\nfix the file (it reloads on save) or press q"
		}
		var out []string
		for _, l := range strings.Split(msg, "\n") {
			for _, w := range wrap(l, max(m.width-2, 10)) {
				out = append(out, " "+w)
			}
		}
		return strings.Join(out, "\n")
	}
	return m.headerView() + "\n" + m.bodyView() + "\n" + m.footerView()
}

func (m *Model) headerView() string {
	st := m.doc.Stations[m.station]
	name := st.ID
	segs := []seg{{text: fmt.Sprintf(" margin › %s %d/%d  ", name, m.station, len(m.doc.Stations)-1), fg: colTitle, bg: colBar, bold: true}}
	segs = append(segs, m.progressDots()...)
	if st.Kind == doc.Code {
		if st.Risk != "" {
			segs = append(segs, seg{text: " " + strings.ToUpper(st.Risk) + " RISK ", fg: colInk, bg: riskColor(st.Risk), bold: true})
		}
		meta := fmt.Sprintf(" %d lines", st.LOC())
		if st.Concern != "" {
			meta = " " + st.Concern + " ·" + meta
		}
		segs = append(segs, seg{text: meta + "   ", fg: colDim, bg: colBar}, seg{text: st.Title, fg: colText, bg: colBar})
	} else {
		segs = append(segs, seg{text: st.Title, fg: colText, bg: colBar})
	}
	return m.bar(segs, m.width, doc.Short(m.doc.Review.Kicker, max(m.width/4, 10))+" ")
}

func (m *Model) progressDots() []seg {
	var codes []int
	for i, s := range m.doc.Stations {
		if s.Kind == doc.Code {
			codes = append(codes, i)
		}
	}
	if len(codes) < 2 || len(codes) > 40 {
		return nil
	}
	var segs []seg
	for _, i := range codes {
		dot, fg := "○", colDim
		switch {
		case i == m.station:
			dot, fg = "●", colTitle
		case m.state.Visited[m.doc.Stations[i].ID]:
			dot, fg = "●", colAdded
		}
		segs = append(segs, seg{text: dot, fg: fg, bg: colBar})
	}
	return append(segs, seg{text: "  ", bg: colBar})
}

func (m *Model) footerView() string {
	if m.asking {
		return ansi.Truncate(m.input.View(), m.width, "")
	}
	c := m.counts()
	pill := func(text, fg, bg string) []seg {
		return []seg{{text: " " + text + " ", fg: fg, bg: bg, bold: bg != colBar}, {text: " ", bg: colBar}}
	}
	segs := []seg{{text: " ", bg: colBar}}
	segs = append(segs, pill(fmt.Sprintf("✓ %d/%d", c.Reviewed, c.Notes), colText, colBar)...)
	if c.Issues > 0 {
		segs = append(segs, pill(fmt.Sprintf("! %d", c.Issues), colInk, colProblem)...)
	}
	if c.Questions > 0 {
		segs = append(segs, pill(fmt.Sprintf("? %d", c.Questions), colInk, colChanged)...)
	}
	if len(m.fresh) > 0 {
		segs = append(segs, pill(fmt.Sprintf("new %d", len(m.fresh)), colInk, colAdded)...)
	}
	if c.Lost > 0 {
		segs = append(segs, pill(fmt.Sprintf("lost %d", c.Lost), colInk, colProblem)...)
	}
	if c.Open > 0 {
		segs = append(segs, pill(fmt.Sprintf("%d waiting for the agent", c.Open), colChanged, colBar)...)
	}
	if m.filter > 0 {
		segs = append(segs, pill(filterNames[m.filter], colInk, colHeading)...)
	}
	pace, warn := m.pace()
	paceFg := colDim
	if warn {
		paceFg = colChanged
	}
	segs = append(segs, pill(pace, paceFg, colBar)...)
	statusFg := colDim
	if m.statusErr {
		statusFg = colProblem
	}
	segs = append(segs, seg{text: m.status, fg: statusFg, bg: colBar})
	return m.bar(segs, m.width, "H help ")
}

func (m *Model) bodyView() string {
	h := m.bodyHeight()
	cw := m.codeWidth()
	var side []string
	var marks map[int]string
	if m.notesShown() {
		side, marks = m.notesPanel(m.width-cw-1, h)
	}
	numW := m.numWidth()
	lines := make([]string, h)
	for i := range lines {
		idx := m.top + i
		if idx < len(m.rows) {
			lines[i] = m.renderRow(idx, cw, numW)
		} else {
			lines[i] = strings.Repeat(" ", cw)
		}
		if side != nil {
			div, fg := "│", colBar
			if c, ok := marks[i]; ok {
				div, fg = "┤", c
			}
			lines[i] += m.paint.Text(div, 1, fg, "", false, false) + side[i]
		}
	}
	switch {
	case m.helpOpen:
		m.overlay(lines, helpLines, -1)
	case m.listOpen:
		entries := []string{" stations · enter jumps · esc closes"}
		for i, s := range m.doc.Stations {
			entries = append(entries, fmt.Sprintf(" %2d  %s", i, s.Title))
		}
		m.overlay(lines, entries, m.listCur+1)
	}
	return strings.Join(lines, "\n")
}

func (m *Model) numWidth() int {
	n := 0
	for _, p := range m.doc.Stations[m.station].Parts {
		n = max(n, len(p.Lines))
	}
	return max(len(strconv.Itoa(n)), 3)
}

func toneStyle(t tone) (fg string, bold, italic bool) {
	switch t {
	case toneTitle:
		return colTitle, true, false
	case toneDim:
		return colDim, false, false
	case toneHeading:
		return colHeading, true, false
	case toneProblem:
		return colProblem, false, false
	case toneLede:
		return colText, false, true
	}
	return colText, false, false
}

func tint(k diffmap.Kind, cursor bool) (bg, fg string) {
	switch k {
	case diffmap.Added:
		bg, fg = tintAdded, colAdded
		if cursor {
			bg = tintAddedCur
		}
	case diffmap.Changed:
		bg, fg = tintChanged, colChanged
		if cursor {
			bg = tintChangedCur
		}
	case diffmap.Removed:
		bg, fg = tintRemoved, colRemoved
		if cursor {
			bg = tintRemovedCur
		}
	default:
		if cursor {
			bg = tintCursor
		}
	}
	return
}

func (m *Model) renderRow(idx, width, numW int) string {
	r := m.rows[idx]
	p := m.paint
	sel := idx == m.cur
	switch r.kind {
	case rowText:
		fg, bold, italic := toneStyle(r.tone)
		if r.fg != "" {
			fg = r.fg
		}
		off := 0
		if r.tone == toneFlow {
			off = m.hoff
		}
		return p.Code([]render.Span{{Text: " " + r.text, Color: fg, Bold: bold, Italic: italic}}, width, off, "")
	case rowHeader:
		s := r.text + " "
		if fill := width - runewidth.StringWidth(s); fill > 0 {
			s += strings.Repeat("─", fill)
		}
		return p.Text(s, width, colHeading, "", false, false)
	case rowLink:
		fg, bg, prefix := r.fg, "", "  "
		if fg == "" {
			fg = colText
		}
		if sel {
			bg, prefix = tintCursor, "▸ "
		}
		return p.Code([]render.Span{{Text: prefix + r.text, Color: fg, Bold: sel}}, width, 0, bg)
	case rowFold:
		bg := ""
		if sel {
			bg = tintCursor
		}
		return p.Text(fmt.Sprintf("%s⋯ %d unchanged lines", strings.Repeat(" ", numW+6), r.fold), width, colDim, bg, false, true)
	}

	st := m.doc.Stations[m.station]
	part := st.Parts[r.part]
	codeW := max(width-numW-6, 1)
	if r.kind == rowGhost {
		hs, he := -1, -1
		if cur := r.line + r.ghost; cur < len(part.Lines) && part.Pairs[cur] == r.text {
			if as, ae, _, _, ok := render.ChangedRange(r.text, part.Lines[cur]); ok {
				hs, he = as, ae
			}
		}
		return strings.Repeat(" ", numW+4) + p.Text("▎ ", 2, colRemoved, tintRemoved, false, false) +
			p.CodeHL([]render.Span{{Text: r.text, Color: colGhost}}, codeW, m.hoff, tintRemoved, hs, he, tintRemovedHL)
	}

	kind := diffmap.Same
	if r.line < len(part.Kinds) {
		kind = part.Kinds[r.line]
	}
	bg, barFg := tint(kind, sel)
	gutter := "  "
	if kind != diffmap.Same {
		gutter = "▎ "
	}
	label := "   "
	if len(r.notes) > 0 {
		s := strconv.Itoa(r.notes[0] + 1)
		if len(r.notes) > 1 {
			s += "+"
		}
		labelBg := m.noteStyle(st.Notes[r.notes[0]]).color
		if slices.Contains(r.notes, m.note) {
			labelBg = colNoteSel
		}
		label = p.Text(fmt.Sprintf("%2s ", s), 3, colInk, labelBg, true, false)
	}
	numFg, numBg := colDim, ""
	if sel {
		numFg, numBg = colInk, colCursorNum
	}
	num := p.Text(fmt.Sprintf("%*d ", numW, r.line+1), numW+1, numFg, numBg, sel, false)
	hs, he := -1, -1
	if kind == diffmap.Changed {
		if old, ok := part.Pairs[r.line]; ok {
			if _, _, bs, be, ok := render.ChangedRange(old, part.Lines[r.line]); ok {
				hs, he = bs, be
			}
		}
	}
	return label + num + p.Text(gutter, 2, barFg, bg, false, false) + p.CodeHL(m.spansFor(part)[r.line], codeW, m.hoff, bg, hs, he, tintChangedHL)
}

func (m *Model) spansFor(part *doc.Part) [][]render.Span {
	key := fmt.Sprintf("%v|%s", part.Base, part.Spec.File)
	s, ok := m.spans[key]
	if !ok {
		s = render.Highlight(part.Spec.File, part.Lines)
		m.spans[key] = s
	}
	return s
}

var helpLines = []string{
	" keys · any key closes",
	"",
	" j k  ↑ ↓   move                 enter  open a link, unfold",
	" ] [        next / previous note N      next new note",
	" } {        next / previous stop tab    station list",
	" space      mark note reviewed   x      dismiss note as not useful",
	" ?          flag note            v      show notes (your pass first)",
	" a          ask the agent        F      filter: all, problems+questions, problems",
	" z          fold unchanged lines f      whole file",
	" d          removed lines        n      notes column",
	" h l        scroll sideways      q      quit",
}

// overlay draws a centred box of entries over the body; highlight is the selected entry or -1.
func (m *Model) overlay(lines, entries []string, highlight int) {
	w := min(84, m.width-4)
	if w < 30 {
		return
	}
	pad := strings.Repeat(" ", (m.width-w)/2)
	for i, e := range entries {
		y := 1 + i
		if y >= len(lines) {
			break
		}
		fg, bg, bold := colText, colBar, false
		switch {
		case i == 0:
			fg, bold = colHeading, true
		case i == highlight:
			fg, bg, bold = colInk, colCursorNum, true
		}
		lines[y] = pad + m.paint.Text(e, w, fg, bg, bold, false)
	}
}
