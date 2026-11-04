package tui

import (
	"fmt"
	"path"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/redrick/margin/internal/render"
	"github.com/redrick/margin/internal/vimedit"
)

// Trying a change in place: the line or block turns into a small vim where it stands, and :w runs
// its tests against what it holds.

type editState struct {
	ed   *vimedit.Editor
	tt   *tryTarget
	old  string
	top  int // first screen row, counted with the editor's lines in place of the block's
	hoff int
}

const (
	tintEditSel = "#264f78"
	colEditCur  = "#e3b341"
	colInsCur   = "#39c5cf"
)

var editHelp = []string{
	" ✎ try a change · vim keys",
	"",
	" i a I A o O   insert",
	" esc           back to normal",
	" h j k l w b e 0 ^ $ gg G f t",
	" x dd D C cc cw dw ciw di( yy p",
	" v V           select, then d c y > <",
	" u ctrl+r .    undo, redo, repeat",
	" > < J ~ r     indent, join, case",
	"",
	" :w  ctrl+s    run the tests with this",
	" :q            cancel",
	"",
	" the tests run unchanged first,",
	" then with your edit in the file;",
	" margin puts the lines back after.",
	" an empty block deletes the lines.",
}

// openEditor starts the in-place editor on lines from to to of the cursor's part, holding text.
func (m *Model) openEditor(pi, from, to int, text string) tea.Cmd {
	st := m.doc.Stations[m.station]
	p := st.Parts[pi]
	switch {
	case p.Base:
		m.setStatus(false, "this part shows the code before the change; try changes on the new side")
		return nil
	case m.doc.Coverage == nil:
		m.setStatus(false, "no coverage yet: ask the agent to run margin coverage, then try a change")
		return nil
	}
	ids := m.blockTests(pi, from, to)
	if len(ids) == 0 {
		ids = m.stopTests(st)
	}
	if len(ids) == 0 {
		m.setStatus(false, "no test runs these lines or this stop, so none would see the change")
		return nil
	}
	old := strings.Join(p.Lines[from:to+1], "\n")
	if text == "" {
		text = old
	}
	m.blockAt = nil
	m.edit = &editState{
		ed:   vimedit.New(text, from+1),
		tt:   &tryTarget{station: st.ID, file: p.Spec.File, part: pi, line: from, end: to, ids: ids},
		old:  old,
		top:  m.top,
		hoff: m.hoff,
	}
	m.setStatus(false, "")
	return nil
}

func (m *Model) blockTests(pi, from, to int) []int {
	p := m.doc.Stations[m.station].Parts[pi]
	seen := map[int]bool{}
	var ids []int
	for l := from; l <= to; l++ {
		for _, ti := range m.doc.Coverage.TestsAt(p, l) {
			if !seen[ti] {
				seen[ti] = true
				ids = append(ids, ti)
			}
		}
	}
	return ids
}

// tryHere edits the marked block, or the cursor line, in place.
func (m *Model) tryHere() tea.Cmd {
	if m.blockAt != nil {
		pi, from, to, ok := m.block()
		if !ok {
			m.setStatus(false, "the cursor left the block's part: move back into it, or V drops the block")
			return nil
		}
		return m.openEditor(pi, from, to, "")
	}
	r, ok := m.cursorRow()
	if !ok {
		m.setStatus(false, "%s", m.needLine("try a change to it"))
		return nil
	}
	return m.openEditor(r.part, r.line, r.line, "")
}

func (m *Model) updateEdit(msg tea.KeyMsg) tea.Cmd {
	es := m.edit
	var keys []vimedit.Key
	switch {
	case msg.Type == tea.KeySpace:
		keys = []vimedit.Key{{Name: " ", Runes: []rune{' '}}}
	case msg.Type == tea.KeyRunes:
		// Fast typing and pastes come as several runes at once; vim reads them one by one.
		for _, r := range msg.Runes {
			keys = append(keys, vimedit.Key{Name: string(r), Runes: []rune{r}})
		}
	default:
		keys = []vimedit.Key{{Name: msg.String()}}
	}
	res := vimedit.None
	for _, k := range keys {
		if res = es.ed.Key(k); res != vimedit.None {
			break
		}
	}
	switch res {
	case vimedit.Cancel:
		m.edit = nil
		m.setStatus(false, "change dropped; the file was never touched")
	case vimedit.Run:
		text := es.ed.Text()
		if text == es.old {
			m.setStatus(false, "nothing changed yet; :q leaves")
			return nil
		}
		m.edit = nil
		es.tt.text = text
		return m.runTry(es.tt, es.old)
	}
	return nil
}

// editRows draws the body with the editor in place of the block's rows.
func (m *Model) editRows(h, cw, numW int) []string {
	es := m.edit
	first, last := -1, -1
	for i, r := range m.rows {
		if r.kind == rowCode && r.part == es.tt.part {
			if r.line == es.tt.line && first < 0 {
				first = i
			}
			if r.line == es.tt.end {
				last = i
			}
		}
	}
	if first < 0 || last < first {
		first, last = len(m.rows), len(m.rows)-1
	}
	n := len(es.ed.Lines)
	total := len(m.rows) - (last - first + 1) + n
	cur := first + es.ed.Row
	if cur < es.top+2 {
		es.top = max(cur-2, 0)
	}
	if cur > es.top+h-3 {
		es.top = cur - h + 3
	}
	es.top = min(max(es.top, 0), max(total-h, 0))

	hl := render.Highlight(es.tt.file, linesOf(es.ed))
	out := make([]string, h)
	for i := range out {
		v := es.top + i
		switch {
		case v < first:
			out[i] = m.renderRow(v, cw, numW)
		case v < first+n:
			out[i] = m.editLine(v-first, hl[v-first], cw, numW)
		case v-n+(last-first+1) < len(m.rows):
			out[i] = m.renderRow(v-n+(last-first+1), cw, numW)
		default:
			out[i] = strings.Repeat(" ", cw)
		}
	}
	return out
}

func linesOf(e *vimedit.Editor) []string {
	out := make([]string, len(e.Lines))
	for i, l := range e.Lines {
		out[i] = string(l)
	}
	return out
}

func (m *Model) editLine(k int, spans []render.Span, cw, numW int) string {
	es := m.edit
	ed := es.ed
	pt := m.paint
	numFg, numBg := colDim, ""
	if k == ed.Row {
		numFg, numBg = colInk, colMatch
	}
	codeW := max(cw-numW-7, 1)
	line := ed.Lines[k]
	// Keep the cursor in sight sideways.
	if k == ed.Row {
		col := dispCol(line, ed.Col)
		if col < es.hoff {
			es.hoff = max(col-8, 0)
		}
		if col >= es.hoff+codeW {
			es.hoff = col - codeW + 8
		}
	}
	r1, c1, r2, c2, lines, sel := ed.Selection()
	bgAt := func(i int) string {
		if k == ed.Row && i == ed.Col && ed.Mode != vimedit.Command {
			if ed.Mode == vimedit.Insert {
				return colInsCur
			}
			return colEditCur
		}
		if sel && k >= r1 && k <= r2 && (lines || ((k > r1 || i >= c1) && (k < r2 || i <= c2))) {
			return tintEditSel
		}
		return ""
	}
	spans = paintCells(spans, len(line), bgAt)
	gutter := pt.Text(fmt.Sprintf("%*d", numW, es.tt.line+k+1), numW, numFg, numBg, k == ed.Row, false) + " "
	return strings.Repeat(" ", 3) + gutter + pt.Text("▎✎ ", 3, colMatch, "", true, false) + pt.Code(spans, codeW, es.hoff, "")
}

// paintCells gives each rune of the line the background bgAt says, and a cell past its end for a
// cursor there.
func paintCells(spans []render.Span, n int, bgAt func(int) string) []render.Span {
	var out []render.Span
	i, col := 0, 0
	for _, sp := range spans {
		cur := render.Span{}
		flush := func() {
			if cur.Text != "" {
				out = append(out, cur)
			}
			cur = render.Span{}
		}
		for _, r := range sp.Text {
			bg := bgAt(i)
			cursor := bg == colEditCur || bg == colInsCur
			w := runewidth.RuneWidth(r)
			if r == '\t' {
				w = render.TabWidth - col%render.TabWidth
			}
			if r == '\t' && cursor {
				// A tab draws as spaces; the cursor sits on its first cell.
				flush()
				out = append(out, render.Span{Text: " ", Bg: bg}, render.Span{Text: strings.Repeat(" ", w-1)})
				i, col = i+1, col+w
				continue
			}
			if cur.Text != "" && cur.Bg != bg {
				flush()
			}
			if cur.Text == "" {
				cur = sp
				cur.Text, cur.Bg = "", bg
				if cursor {
					cur.Color = colInk
				}
			}
			cur.Text += string(r)
			i, col = i+1, col+w
		}
		flush()
	}
	for ; i <= n; i++ {
		if bg := bgAt(i); bg != "" {
			out = append(out, render.Span{Text: " ", Bg: bg})
		}
	}
	return out
}

func dispCol(line []rune, col int) int {
	c := 0
	for i, r := range line {
		if i >= col {
			break
		}
		if r == '\t' {
			c += render.TabWidth - c%render.TabWidth
		} else {
			c += runewidth.RuneWidth(r)
		}
	}
	return c
}

func (m *Model) editFooter() string {
	ed := m.edit.ed
	tt := m.edit.tt
	where := fmt.Sprintf("%s:%d", path.Base(tt.file), tt.line+1)
	if tt.end > tt.line {
		where += fmt.Sprintf("-%d", tt.end+1)
	}
	var segs []seg
	switch ed.Mode {
	case vimedit.Command:
		segs = []seg{{text: " :" + ed.Cmd, fg: colTitle, bg: colBar}, {text: "█", fg: colEditCur, bg: colBar}}
	default:
		bg := colEditCur
		if ed.Mode == vimedit.Insert {
			bg = colInsCur
		}
		segs = []seg{{text: " -- " + ed.Mode.String() + " -- ", fg: colInk, bg: bg, bold: true}, {text: " ", bg: colBar},
			{text: "✎ " + where + fmt.Sprintf(" · %d tests ", len(tt.ids)), fg: colText, bg: colBar}}
		if p := ed.Pending(); p != "" {
			segs = append(segs, seg{text: p + " ", fg: colMatch, bg: colBar, bold: true})
		}
		if ed.Msg != "" {
			segs = append(segs, seg{text: ed.Msg, fg: colProblem, bg: colBar})
		}
	}
	return m.bar(segs, m.width, " :w runs · :q cancels ")
}
