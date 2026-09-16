package tui

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/render"
)

const collapsedCardLines = 5

// placeCards puts each card level with its anchor row, pushing it down while the previous card is in
// the way. It returns each card's first screen line, or -1 when it no longer fits above limit.
func placeCards(anchors, heights []int, top, first, limit int) []int {
	starts := make([]int, len(anchors))
	next := first
	for i := range anchors {
		y := max(anchors[i]-top, next)
		if y >= limit {
			starts[i] = -1
			continue
		}
		starts[i] = y
		next = y + heights[i] + 1
	}
	return starts
}

// notesPanel draws the notes beside the code they belong to. The returned marks colour the divider
// on anchor lines and card starts, which ties each card to its line.
func (m *Model) notesPanel(w, h int) ([]string, map[int]string) {
	st := m.doc.Stations[m.station]
	p := m.paint
	out := make([]string, h)
	for i := range out {
		out[i] = strings.Repeat(" ", w)
	}
	marks := map[int]string{}
	if m.blind(st) {
		msg := []string{"", " your pass first", "", " This station is high risk. Read it", " before the agent's notes, then press", " v to compare with what it found."}
		for i, l := range msg {
			if i < h {
				out[i] = p.Text(l, w, colChanged, "", i == 1, false)
			}
		}
		return out, marks
	}

	rowOf := map[string]int{}
	for i, r := range m.rows {
		if r.kind != rowCode {
			continue
		}
		if k := doc.LineKey(st.Parts[r.part], r.line); !hasKey(rowOf, k) {
			rowOf[k] = i
		}
	}
	type card struct {
		note, row int
		lines     []string
	}
	var cards []card
	above, below, loose := 0, 0, 0
	for i, n := range st.Notes {
		if !m.noteVisible(st, n) {
			continue
		}
		row, ok := 0, false
		if n.Part >= 0 {
			row, ok = rowOf[doc.LineKey(st.Parts[n.Part], n.Line)]
		}
		switch {
		case !ok:
			loose++
		case row < m.top:
			above++
		case row >= m.top+h:
			below++
		default:
			cards = append(cards, card{note: i, row: row})
		}
	}
	sort.SliceStable(cards, func(a, b int) bool { return cards[a].row < cards[b].row })

	first := 0
	if above > 0 {
		out[0] = p.Text(fmt.Sprintf(" ↑ %d more above", above), w, colDim, "", false, false)
		first = 1
	}
	anchors, heights := make([]int, len(cards)), make([]int, len(cards))
	for i := range cards {
		sel := cards[i].note == m.note
		lines := m.noteCard(st, cards[i].note, w, sel)
		if !sel && len(lines) > collapsedCardLines {
			lines = append(lines[:collapsedCardLines-1:collapsedCardLines-1], p.Text("  … select it to read the rest", w, colDim, "", false, true))
		}
		cards[i].lines = lines
		anchors[i], heights[i] = cards[i].row, len(lines)
	}
	limit := h - 1
	for i, y := range placeCards(anchors, heights, m.top, first, limit) {
		c := cards[i]
		if y < 0 {
			below++
			continue
		}
		for j, l := range c.lines {
			if y+j >= limit {
				break
			}
			out[y+j] = l
		}
		color := m.noteStyle(st.Notes[c.note]).color
		marks[c.row-m.top] = color
		marks[y] = color
	}

	pending := 0
	answered := m.doc.AnsweredQuestions()
	for _, q := range m.state.Questions {
		if q.Station == st.ID && !answered[q.ID] {
			pending++
		}
	}
	var tail []string
	if below > 0 {
		tail = append(tail, fmt.Sprintf("↓ %d more below", below))
	}
	if loose > 0 {
		tail = append(tail, fmt.Sprintf("%d without an anchor", loose))
	}
	if pending > 0 {
		tail = append(tail, fmt.Sprintf("%d questions waiting", pending))
	}
	if len(tail) > 0 && h > 1 {
		out[h-1] = p.Text(" "+strings.Join(tail, " · "), w, colDim, "", false, false)
	}
	return out, marks
}

func hasKey(m map[string]int, k string) bool {
	_, ok := m[k]
	return ok
}

func (m *Model) noteCard(st *doc.Station, i, w int, sel bool) []string {
	n := st.Notes[i]
	ks := m.noteStyle(n)
	p := m.paint
	bg := ""
	if sel {
		bg = tintNoteSel
	}
	textFg := colText
	if m.state.Dismissed[n.Key] {
		textFg = colDim
	}
	inner := max(w-3, 8)

	head := " " + ks.icon + " " + ks.label
	if n.Confidence != "" {
		head += " · " + n.Confidence + " confidence"
	}
	for _, f := range []struct {
		on    bool
		label string
	}{
		{m.fresh[n.Key], "new"},
		{m.state.Reviewed[n.Key], "✓"},
		{m.state.Flagged[n.Key], "flagged"},
		{m.state.Dismissed[n.Key], "dismissed"},
	} {
		if f.on {
			head += " · " + f.label
		}
	}
	changed := n.Changed != "" && m.state.Seen[n.Key] != n.Changed
	if changed {
		head += " · Δ"
	}
	switch {
	case n.Problem != "":
		head += "  " + n.Problem
	case n.Part >= 0:
		head += fmt.Sprintf("  %s:%d", path.Base(st.Parts[n.Part].Spec.File), n.Line+1)
	}
	label := fmt.Sprintf(" %d ", i+1)
	labelBg := ks.color
	if sel {
		labelBg = colNoteSel
	}
	lines := []string{p.Text(label, len(label), colInk, labelBg, true, false) + p.Text(head, w-len(label), ks.color, bg, sel, false)}

	bar := render.Span{Text: "▌ ", Color: ks.color}
	add := func(s string, fg string, italic bool) {
		for _, l := range wrap(s, inner) {
			lines = append(lines, p.Code([]render.Span{bar, {Text: l, Color: fg, Italic: italic}}, w, 0, bg))
		}
	}
	if n.Q != "" {
		add("Q: "+n.Q, colDim, true)
	}
	var bold, code bool
	for _, l := range wrap(n.Text, inner) {
		lines = append(lines, p.Code(append([]render.Span{bar}, markup(l, &bold, &code, textFg)...), w, 0, bg))
	}
	if n.Evidence != "" {
		add("evidence: "+n.Evidence, colDim, true)
	}
	if changed {
		add("Δ "+n.Changed, colChanged, false)
	}
	return lines
}

func markup(line string, bold, code *bool, fg string) []render.Span {
	var spans []render.Span
	emit := func(s string) {
		if s == "" {
			return
		}
		c := fg
		if *code {
			c = colCode
		}
		spans = append(spans, render.Span{Text: s, Color: c, Bold: *bold})
	}
	for line != "" {
		i := strings.IndexAny(line, "*`")
		if i < 0 {
			emit(line)
			break
		}
		if line[i] == '*' && (i+1 >= len(line) || line[i+1] != '*') {
			emit(line[:i+1])
			line = line[i+1:]
			continue
		}
		emit(line[:i])
		if line[i] == '`' {
			*code = !*code
			line = line[i+1:]
		} else {
			*bold = !*bold
			line = line[i+2:]
		}
	}
	return spans
}
