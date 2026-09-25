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
	if name := m.spotName(); name != "" {
		segs = append(segs, seg{text: " spotlight: " + name + " ", fg: colInk, bg: colTested, bold: true}, seg{text: " ", bg: colBar})
	}
	if st.Kind == doc.Code {
		if st.Risk != "" {
			segs = append(segs, seg{text: " " + strings.ToUpper(st.Risk) + " RISK ", fg: colInk, bg: riskColor(st.Risk), bold: true})
		}
		segs = append(segs, m.coverPills(st)...)
		meta := fmt.Sprintf(" %d lines", st.LOC())
		if st.Concern != "" {
			meta = " " + st.Concern + " ·" + meta
		}
		segs = append(segs, seg{text: meta + "   ", fg: colDim, bg: colBar}, seg{text: st.Title, fg: colText, bg: colBar})
	} else {
		segs = append(segs, seg{text: st.Title, fg: colText, bg: colBar})
	}
	return m.bar(segs, m.width, "  "+doc.Short(m.doc.Review.Kicker, max(m.width/4, 10))+" ")
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
	if m.asking || m.searching {
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
	if c.Calls > 0 {
		segs = append(segs, pill(fmt.Sprintf("◆ %d/%d", c.CallsMade, c.Calls), colInk, colDecide)...)
	}
	if c.Drafts > 0 {
		label := fmt.Sprintf("✎ %d drafts · S sends", c.Drafts)
		if c.Drafts == 1 {
			label = "✎ 1 draft · S sends"
		}
		segs = append(segs, pill(label, colInk, colComment)...)
	}
	if c.Open > 0 {
		segs = append(segs, pill(fmt.Sprintf("%d waiting for the agent", c.Open), colChanged, colBar)...)
	}
	if m.doc.Stations[m.station].Kind == doc.Code && !m.ghosts && !m.sideBySide() {
		segs = append(segs, pill("removed lines hidden", colInk, colHeading)...)
	}
	if fs := m.filters(); m.filter > 0 && m.filter < len(fs) {
		segs = append(segs, pill(fs[m.filter], colInk, colHeading)...)
	}
	if m.query != "" {
		segs = append(segs, pill("/"+m.query+" · n N · esc", colInk, colMatch)...)
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
	return m.bar(segs, m.width, " "+m.viewHint()+"H help ")
}

// viewHint keeps the keys that change how the diff is drawn in sight, since they decide whether the
// red lines are on screen at all.
func (m *Model) viewHint() string {
	switch {
	case m.doc.Stations[m.station].Kind != doc.Code:
		return ""
	case m.sideBySide():
		return "s one column · "
	case m.ghosts:
		return "d hide removed · s side by side · "
	}
	return "d show removed · s side by side · "
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

// tint colours a line by whether it ends up in the change: added and changed lines are both new code,
// so they share green, and only lines that are gone are red.
func tint(k diffmap.Kind, cursor bool) (bg, fg string) {
	switch k {
	case diffmap.Added, diffmap.Changed:
		bg, fg = tintAdded, colAdded
		if cursor {
			bg = tintAddedCur
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
		if r.spans != nil {
			return p.Code(append([]render.Span{{Text: " "}}, r.spans...), width, 0, "")
		}
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
	case rowTest:
		bg, prefix := "", "  "
		if sel {
			bg, prefix = tintCursor, "▸ "
		}
		return p.Code(append([]render.Span{{Text: prefix, Color: colText, Bold: true}}, r.spans...), width, 0, bg)
	case rowFold:
		bg := ""
		if sel {
			bg = tintCursor
		}
		label := fmt.Sprintf("⋯ %d unchanged lines", r.fold)
		if r.text != "" {
			label = "⋯ " + r.text
		}
		return p.Text(strings.Repeat(" ", numW+7)+label, width, colDim, bg, false, true)
	}

	st := m.doc.Stations[m.station]
	part := st.Parts[r.part]
	if m.splitPart(part) {
		return m.renderSplit(r, part, width, numW, sel)
	}
	codeW := max(width-numW-7, 1)
	if r.kind == rowGhost {
		if part.GhostMoved[r.line][r.ghost] != "" {
			return strings.Repeat(" ", numW+4) + p.Text("▎← ", 3, colMoved, tintMoved, true, false) +
				p.Code([]render.Span{{Text: r.text, Color: colGhost}}, codeW, m.hoff, tintMoved)
		}
		hs, he := ghostRange(part, r)
		return strings.Repeat(" ", numW+4) + p.Text("▎- ", 3, colRemoved, tintRemoved, true, false) +
			p.CodeHL(struck(r.text, hs, he), codeW, m.hoff, tintRemoved, hs, he, tintRemovedHL)
	}

	kind := lineKind(part, r.line)
	bg, barFg := tint(kind, sel)
	mark := sign(kind, "▎")
	if part.Moved[r.line] != "" {
		bg, barFg, mark = tintMoved, colMoved, "▎→ "
		if part.Base {
			mark = "▎← "
		}
		if sel {
			bg = tintMovedCur
		}
	}
	hs, he := newRange(part, r.line)
	return m.noteLabel(st, r) + m.lineNum(part, r.line, numW, sel) + p.Text(mark, 3, barFg, bg, true, false) +
		p.CodeHL(m.lineSpans(part, r.line), codeW, m.hoff, bg, hs, he, tintAddedHL)
}

// renderSplit draws a code or removed row as base on the left and current code on the right, so a
// changed line sits beside the line it replaced. Line numbers belong to the current file.
func (m *Model) renderSplit(r row, part *doc.Part, width, numW int, sel bool) string {
	p := m.paint
	total := width - numW - 9
	lw, rw := total/2, total-total/2
	div := p.Text("│", 1, colDim, "", false, false)
	blankRight := strings.Repeat(" ", numW+3+rw)

	oldBg := tintRemoved
	if sel {
		oldBg = tintRemovedCur
	}
	if r.kind == rowGhost {
		hs, he := ghostRange(part, r)
		return "   " + p.Text("- ", 2, colRemoved, tintRemoved, true, false) +
			p.CodeHL(struck(r.text, hs, he), lw, m.hoff, tintRemoved, hs, he, tintRemovedHL) + div + blankRight
	}

	st := m.doc.Stations[m.station]
	kind := lineKind(part, r.line)
	var left string
	old, paired := part.Pairs[r.line]
	switch {
	case kind == diffmap.Same:
		bg := ""
		if sel {
			bg = tintCursor
		}
		left = p.Text("", 2, "", bg, false, false) + p.Code(m.lineSpans(part, r.line), lw, m.hoff, bg)
	case kind == diffmap.Changed && paired:
		hs, he := -1, -1
		if as, ae, _, _, ok := render.ChangedRange(old, part.Lines[r.line]); ok {
			hs, he = as, ae
		}
		left = p.Text("- ", 2, colRemoved, oldBg, true, false) + p.CodeHL(struck(old, hs, he), lw, m.hoff, oldBg, hs, he, tintRemovedHL)
	default:
		left = strings.Repeat(" ", lw+2)
	}
	bg, fg := tint(kind, sel)
	mark := sign(kind, "")
	if part.Moved[r.line] != "" {
		bg, fg, mark = tintMoved, colMoved, "→ "
		if sel {
			bg = tintMovedCur
		}
	}
	hs, he := newRange(part, r.line)
	return m.noteLabel(st, r) + left + div + m.lineNum(part, r.line, numW, sel) + p.Text(mark, 2, fg, bg, true, false) +
		p.CodeHL(m.lineSpans(part, r.line), rw, m.hoff, bg, hs, he, tintAddedHL)
}

// sign is the gutter mark for a line: + for code that is in the change, - for code that is gone.
func sign(k diffmap.Kind, bar string) string {
	switch k {
	case diffmap.Added, diffmap.Changed:
		return bar + "+ "
	case diffmap.Removed:
		return bar + "- "
	}
	return strings.Repeat(" ", len([]rune(bar))) + "  "
}

func lineKind(part *doc.Part, line int) diffmap.Kind {
	if line < len(part.Kinds) {
		return part.Kinds[line]
	}
	return diffmap.Same
}

// newRange is the part of a changed line that differs from the base line it replaced, or -1, -1.
func newRange(part *doc.Part, line int) (int, int) {
	if lineKind(part, line) != diffmap.Changed {
		return -1, -1
	}
	if old, ok := part.Pairs[line]; ok {
		if _, _, bs, be, ok := render.ChangedRange(old, part.Lines[line]); ok {
			return bs, be
		}
	}
	return -1, -1
}

// ghostRange is the part of a removed line that differs from the line that replaced it, or -1, -1.
func ghostRange(part *doc.Part, r row) (int, int) {
	if cur := r.line + r.ghost; cur < len(part.Lines) && part.Pairs[cur] == r.text {
		if as, ae, _, _, ok := render.ChangedRange(r.text, part.Lines[cur]); ok {
			return as, ae
		}
	}
	return -1, -1
}

// struck renders a removed line dimmed, with the runes [hs, he) that were rewritten struck through.
func struck(text string, hs, he int) []render.Span {
	rs := []rune(text)
	if hs < 0 || he > len(rs) || hs >= he {
		return []render.Span{{Text: text, Color: colGhost}}
	}
	return []render.Span{
		{Text: string(rs[:hs]), Color: colGhost},
		{Text: string(rs[hs:he]), Color: colGhost, Strike: true},
		{Text: string(rs[he:]), Color: colGhost},
	}
}

func (m *Model) noteLabel(st *doc.Station, r row) string {
	if len(r.notes) == 0 {
		if r.kind == rowCode && len(m.commentsAt(st.Parts[r.part], r.line)) > 0 {
			return m.paint.Text(" ✎ ", 3, colInk, colComment, true, false)
		}
		return "   "
	}
	s := strconv.Itoa(r.notes[0] + 1)
	if len(r.notes) > 1 {
		s += "+"
	}
	labelBg := m.noteStyle(st.Notes[r.notes[0]]).color
	if slices.Contains(r.notes, m.note) {
		labelBg = colNoteSel
	}
	return m.paint.Text(fmt.Sprintf("%2s ", s), 3, colInk, labelBg, true, false)
}

func (m *Model) lineNum(part *doc.Part, line, numW int, sel bool) string {
	numFg, numBg := colDim, ""
	if sel {
		numFg, numBg = colInk, colCursorNum
	}
	mark, markFg, bold := m.covMark(part, line)
	return m.paint.Text(fmt.Sprintf("%*d", numW, line+1), numW, numFg, numBg, sel, false) + m.paint.Text(mark, 1, markFg, "", bold, false)
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
	" space      mark note reviewed   x      dismiss note; in the recap, delete a draft",
	" ?          flag note            v      show notes (your pass first)",
	" a          ask the agent        F      filter: all, findings, problems, focus areas",
	" c          comment (a draft)    S      send the draft comments to the agent",
	" y          copy the note        Y      copy the stop: rationale and every note",
	" z          fold unchanged lines f      whole file",
	" d          removed lines        n      notes column",
	" s          side by side         r      reload",
	" /          search the code      n N    next / previous match, esc ends",
	" u U        next untested line   tests  enter on a test spotlights it, esc ends",
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
