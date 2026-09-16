package tui

import (
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/redrick/margin/internal/anchor"
	"github.com/redrick/margin/internal/control"
	"github.com/redrick/margin/internal/diffmap"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/text"
)

type rowKind uint8

const (
	rowText rowKind = iota
	rowHeader
	rowCode
	rowGhost
	rowFold
	rowLink
)

type tone uint8

const (
	tonePlain tone = iota
	toneTitle
	toneDim
	toneHeading
	toneProblem
	toneLede
	toneFlow
)

type row struct {
	kind   rowKind
	tone   tone
	fg     string
	text   string
	part   int
	line   int
	ghost  int
	notes  []int
	fold   int
	target int
	noteAt int
}

func (r row) selectable() bool { return r.kind == rowCode || r.kind == rowLink || r.kind == rowFold }

func (m *Model) bodyHeight() int { return max(m.height-2, 1) }

func (m *Model) notesShown() bool {
	return m.notes && m.width >= 80 && m.doc != nil && m.doc.Stations[m.station].Kind == doc.Code
}

func (m *Model) codeWidth() int {
	if !m.notesShown() {
		return m.width
	}
	return m.width - min(max(m.width*35/100, 30), 64) - 1
}

func (m *Model) rebuildKeep() {
	stationID, file, line := m.position()
	m.restore(stationID, file, line)
}

func (m *Model) rebuild() {
	m.rows = nil
	if m.doc == nil || m.width == 0 {
		return
	}
	st := m.doc.Stations[m.station]
	switch st.Kind {
	case doc.Overview:
		m.overviewRows()
	case doc.Recap:
		m.recapRows()
	case doc.Tests:
		m.testRows(st)
	case doc.Code:
		m.codeRows(st)
	}
	m.clamp()
}

func (m *Model) add(kind rowKind, t tone, s string) {
	m.rows = append(m.rows, row{kind: kind, tone: t, text: s})
}

func (m *Model) colored(fg, s string) {
	m.rows = append(m.rows, row{kind: rowText, fg: fg, text: s})
}

func (m *Model) link(fg, s string, target, note int) {
	m.rows = append(m.rows, row{kind: rowLink, fg: fg, text: s, target: target, noteAt: note})
}

func (m *Model) wrapped(t tone, indent int, s string) {
	w := max(m.codeWidth()-2-indent, 10)
	pad := strings.Repeat(" ", indent)
	for _, l := range wrap(s, w) {
		m.add(rowText, t, pad+l)
	}
}

func (m *Model) blind(st *doc.Station) bool {
	return st.Kind == doc.Code && st.Risk == "high" && m.state != nil && !m.state.Revealed[st.ID]
}

func (m *Model) noteVisible(st *doc.Station, n *doc.Note) bool {
	if m.blind(st) {
		return false
	}
	switch m.filter {
	case 1:
		return n.Level() == doc.KindIssue || n.Level() == doc.KindQuestion
	case 2:
		return n.Level() == doc.KindIssue && !n.Unbacked()
	}
	return true
}

func (m *Model) overviewRows() {
	r := m.doc.Review
	m.wrapped(toneTitle, 0, r.Title)
	if r.Kicker != "" {
		m.wrapped(toneDim, 0, r.Kicker)
	}
	if m.doc.HasBase {
		shown := "working tree"
		if m.doc.HeadDesc != "" {
			shown = m.doc.HeadDesc
		}
		m.wrapped(toneDim, 0, "base: "+m.doc.BaseDesc+" · showing: "+shown)
	} else {
		m.wrapped(toneDim, 0, "no base: plain code, no diff marks")
	}
	if r.Summary != "" {
		m.add(rowText, tonePlain, "")
		m.add(rowText, toneHeading, "What and why")
		m.wrapped(toneLede, 0, r.Summary)
	}
	m.add(rowText, tonePlain, "")
	if r.Flow != "" {
		m.add(rowText, toneHeading, "Flow")
		for _, l := range text.Lines(text.Sanitize(r.Flow)) {
			m.add(rowText, toneFlow, "  "+l)
		}
		m.add(rowText, tonePlain, "")
	}
	m.add(rowText, toneHeading, "Tour · enter opens a stop")
	total, read := 0, 0
	for i, s := range m.doc.Stations {
		switch s.Kind {
		case doc.Code:
			loc := s.LOC()
			total += loc
			dot := "○"
			if m.state.Visited[s.ID] {
				dot = "●"
				read += loc
			}
			m.link(colText, fmt.Sprintf("%2d %s %s · %s", i, dot, s.ID, s.Title), i, -1)
			m.colored(riskColor(s.Risk), "       "+m.stationMeta(s))
		case doc.Recap:
			issues, questions := 0, 0
			for _, c := range m.doc.Stations {
				a, b := c.Findings()
				issues, questions = issues+a, questions+b
			}
			m.link(colProblem, fmt.Sprintf("%2d ▸ recap · %d problems, %d questions", i, issues, questions), i, -1)
		case doc.Tests:
			m.link(colText, fmt.Sprintf("%2d ▸ tests · %d files", i, len(s.Tests)), i, -1)
		}
	}
	m.add(rowText, tonePlain, "")
	m.wrapped(toneDim, 0, pacing(total, read))
	if len(r.Renames) > 0 {
		m.add(rowText, tonePlain, "")
		m.add(rowText, toneHeading, "Renames")
		for _, rn := range r.Renames {
			m.wrapped(tonePlain, 2, rn.Before+"  →  "+rn.After)
		}
	}
	if len(r.Skip) > 0 {
		m.add(rowText, tonePlain, "")
		m.add(rowText, toneHeading, "Safe to skip")
		for _, s := range r.Skip {
			line := s.What
			if s.Why != "" {
				line += " — " + s.Why
			}
			m.wrapped(tonePlain, 2, line)
		}
	}
	if len(m.doc.Problems) > 0 {
		m.add(rowText, tonePlain, "")
		m.add(rowText, toneProblem, fmt.Sprintf("Problems with the review file (%d)", len(m.doc.Problems)))
		for _, p := range m.doc.Problems {
			m.wrapped(toneProblem, 2, p.Station+": "+p.Msg)
		}
	}
}

func (m *Model) stationMeta(s *doc.Station) string {
	var meta []string
	if s.Risk != "" {
		meta = append(meta, s.Risk+" risk")
	}
	if s.Concern != "" {
		meta = append(meta, s.Concern)
	}
	meta = append(meta, fmt.Sprintf("%d lines", s.LOC()))
	issues, questions := s.Findings()
	if issues > 0 {
		meta = append(meta, fmt.Sprintf("! %d", issues))
	}
	if questions > 0 {
		meta = append(meta, fmt.Sprintf("? %d", questions))
	}
	reviewed := 0
	for _, n := range s.Notes {
		if m.state.Reviewed[n.Key] {
			reviewed++
		}
	}
	meta = append(meta, fmt.Sprintf("✓ %d/%d", reviewed, len(s.Notes)))
	switch {
	case len(s.TestNames) > 0:
		meta = append(meta, "tests: "+strings.Join(s.TestNames, ", "))
	case s.NeedsTests():
		meta = append(meta, "no tests")
	}
	return strings.Join(meta, " · ")
}

func pacing(total, read int) string {
	mins := max(total*60/400, 1)
	length := fmt.Sprintf("%d minutes", mins)
	if mins >= 60 {
		length = fmt.Sprintf("%.1f hours", float64(mins)/60)
	}
	return fmt.Sprintf("%d lines to read, %d read so far. At about 400 lines an hour that is %s; reviews catch less after 60 to 90 minutes, so take breaks.", total, read, length)
}

func (m *Model) recapRows() {
	m.wrapped(toneLede, 0, "Every problem and open question from the tour. Enter jumps to the note. A problem without evidence is listed as a question until the agent backs it up.")
	m.add(rowText, tonePlain, "")
	groups := []struct {
		title string
		fg    string
		match func(n *doc.Note) bool
	}{
		{"Problems", colProblem, func(n *doc.Note) bool { return n.Level() == doc.KindIssue && !n.Unbacked() }},
		{"Problems without evidence", colChanged, func(n *doc.Note) bool { return n.Unbacked() }},
		{"Questions", colChanged, func(n *doc.Note) bool { return n.Level() == doc.KindQuestion }},
	}
	found := false
	for _, g := range groups {
		heading := false
		for si, st := range m.doc.Stations {
			for ni, n := range st.Notes {
				if !g.match(n) || m.state.Dismissed[n.Key] {
					continue
				}
				if !heading {
					m.add(rowText, toneHeading, g.title)
					heading, found = true, true
				}
				where := st.ID
				if n.Part >= 0 {
					where = fmt.Sprintf("%s · %s:%d", st.ID, path.Base(st.Parts[n.Part].Spec.File), n.Line+1)
				}
				mark := ""
				if m.state.Reviewed[n.Key] {
					mark = "✓ "
				}
				m.link(g.fg, fmt.Sprintf("%s%s — %s", mark, where, doc.Short(plain(n.Text), 400)), si, ni)
			}
		}
		if heading {
			m.add(rowText, tonePlain, "")
		}
	}
	if !found {
		m.add(rowText, toneDim, "Nothing open: every problem and question was dismissed.")
	}
}

func (m *Model) testRows(st *doc.Station) {
	for _, g := range st.Tests {
		title := g.File
		if g.NewFile {
			title += "  · new file"
		}
		m.add(rowText, toneHeading, title)
		if g.Why != "" {
			m.wrapped(toneDim, 2, g.Why)
		}
		if g.Err != nil {
			m.wrapped(toneProblem, 2, g.Err.Error())
		}
		for _, n := range g.Names {
			line := strings.Repeat("  ", n.Depth+1) + "· " + n.Name
			if !g.NewFile && g.New[n.Name] {
				m.colored(colAdded, line+"  new")
			} else {
				m.add(rowText, tonePlain, line)
			}
		}
		m.add(rowText, tonePlain, "")
	}
}

func (m *Model) codeRows(st *doc.Station) {
	intro := false
	if st.Lede != "" {
		m.wrapped(toneLede, 0, st.Lede)
		intro = true
	}
	switch {
	case len(st.TestNames) > 0:
		m.colored(colAdded, "tests: "+strings.Join(st.TestNames, ", "))
		intro = true
	case st.NeedsTests():
		m.colored(colChanged, "no tests cover this stop")
		intro = true
	}
	if m.blind(st) {
		m.colored(colChanged, "your pass first: the agent's notes stay hidden on this high-risk stop until you press v")
		intro = true
	}
	if intro {
		m.add(rowText, tonePlain, "")
	}
	all := st.NotesByLine()
	for pi, p := range st.Parts {
		m.rows = append(m.rows, row{kind: rowHeader, part: pi, text: m.partHeader(st, pi, p)})
		if p.Err != nil {
			m.wrapped(toneProblem, 2, p.Err.Error())
			m.add(rowText, tonePlain, "")
			continue
		}
		rng := p.Range
		if m.whole[[2]int{m.station, pi}] {
			rng = anchor.Range{Start: 0, End: len(p.Lines) - 1}
		}
		noted := func(i int) bool { return len(all[doc.LineKey(p, i)]) > 0 }
		for _, sg := range foldPlan(p, rng, noted) {
			if sg.fold && !m.unfold {
				m.rows = append(m.rows, row{kind: rowFold, part: pi, line: sg.start, fold: sg.end - sg.start + 1})
				continue
			}
			for i := sg.start; i <= sg.end; i++ {
				m.ghostRows(p, pi, i)
				m.rows = append(m.rows, row{kind: rowCode, part: pi, line: i, notes: m.visibleAt(st, all[doc.LineKey(p, i)])})
			}
		}
		if rng.End+1 == len(p.Lines) {
			m.ghostRows(p, pi, len(p.Lines))
		}
		m.add(rowText, tonePlain, "")
	}
}

func (m *Model) ghostRows(p *doc.Part, pi, line int) {
	if !m.ghosts {
		return
	}
	for gi, g := range p.Ghosts[line] {
		m.rows = append(m.rows, row{kind: rowGhost, part: pi, line: line, ghost: gi, text: g})
	}
}

func (m *Model) visibleAt(st *doc.Station, idx []int) []int {
	var out []int
	for _, i := range idx {
		if m.noteVisible(st, st.Notes[i]) {
			out = append(out, i)
		}
	}
	return out
}

func (m *Model) partHeader(st *doc.Station, pi int, p *doc.Part) string {
	s := fmt.Sprintf("── %s · %s", p.Spec.File, p.Spec.Label())
	if p.HunkCount > 1 {
		s = fmt.Sprintf("── %s · change %d/%d", p.Spec.File, p.Hunk, p.HunkCount)
	}
	if p.Spec.BaseFile != "" {
		s += " · renamed from " + p.Spec.BaseFile
	}
	if p.Err == nil {
		s += fmt.Sprintf(" · %d–%d", p.Range.Start+1, p.Range.End+1)
	}
	switch {
	case p.Base:
		s += " · base side, removed"
	case p.NewFile:
		s += " · new file"
	}
	if p.Err == nil && (p.Kinds != nil) && !p.NewFile && !p.Base {
		a, c, r := doc.Counts([]*doc.Part{p})
		s += fmt.Sprintf(" · +%d ~%d -%d", a, c, r)
	}
	if m.whole[[2]int{m.station, pi}] {
		s += " · whole file"
	}
	if p.Err == nil && !m.blind(st) && !partHasNotes(st, pi) {
		s += " · no agent notes, read it yourself"
	}
	return s
}

func partHasNotes(st *doc.Station, pi int) bool {
	for _, n := range st.Notes {
		if n.Part == pi {
			return true
		}
	}
	return false
}

func (m *Model) hasSelectable() bool {
	for _, r := range m.rows {
		if r.selectable() {
			return true
		}
	}
	return false
}

func (m *Model) cursorRow() (row, bool) {
	if m.doc == nil || m.cur < 0 || m.cur >= len(m.rows) || m.rows[m.cur].kind != rowCode {
		return row{}, false
	}
	return m.rows[m.cur], true
}

func (m *Model) clamp() {
	if len(m.rows) == 0 {
		m.cur, m.top = 0, 0
		return
	}
	m.cur = min(max(m.cur, 0), len(m.rows)-1)
	if !m.rows[m.cur].selectable() && m.hasSelectable() {
		for d := 1; d < len(m.rows); d++ {
			if i := m.cur + d; i < len(m.rows) && m.rows[i].selectable() {
				m.cur = i
				break
			}
			if i := m.cur - d; i >= 0 && m.rows[i].selectable() {
				m.cur = i
				break
			}
		}
	}
	m.clampTop()
}

func (m *Model) clampTop() {
	m.top = min(m.top, len(m.rows)-m.bodyHeight())
	m.top = max(m.top, 0)
}

func (m *Model) move(delta int) {
	if !m.hasSelectable() {
		m.top += delta
		m.clampTop()
		return
	}
	step, n := 1, delta
	if delta < 0 {
		step, n = -1, -delta
	}
	i := m.cur
	for ; n > 0; n-- {
		j := i + step
		for j >= 0 && j < len(m.rows) && !m.rows[j].selectable() {
			j += step
		}
		if j < 0 || j >= len(m.rows) {
			break
		}
		i = j
	}
	if i == m.cur && delta < 0 {
		m.top = 0
		return
	}
	m.setCursor(i)
}

func (m *Model) setCursor(i int) {
	m.cur = i
	if notes := m.rows[i].notes; len(notes) > 0 {
		m.note = notes[0]
		m.markSeen()
	}
	m.follow()
}

func (m *Model) follow() {
	if len(m.rows) == 0 {
		return
	}
	h := m.bodyHeight()
	margin := min(3, h/4)
	if m.cur < m.top+margin {
		m.top = m.cur - margin
	}
	if m.cur > m.top+h-1-margin {
		m.top = m.cur - h + 1 + margin
	}
	m.clampTop()
}

func (m *Model) setStation(i int) {
	if m.doc == nil {
		return
	}
	m.station = min(max(i, 0), len(m.doc.Stations)-1)
	m.cur, m.top, m.hoff, m.note = 0, 0, 0, -1
	m.rebuild()
	st := m.doc.Stations[m.station]
	if len(st.Notes) > 0 {
		m.note = 0
	}
	if st.Kind != doc.Code || m.state == nil {
		return
	}
	if !m.readSession[st.ID] {
		m.readSession[st.ID] = true
		m.sessionLines += st.LOC()
	}
	if !m.state.Visited[st.ID] {
		m.state.Visited[st.ID] = true
		m.state.Save()
	}
}

func (m *Model) selectedNote() *doc.Note {
	if m.doc == nil {
		return nil
	}
	notes := m.doc.Stations[m.station].Notes
	if m.note < 0 || m.note >= len(notes) {
		return nil
	}
	return notes[m.note]
}

func (m *Model) gotoNote(i int) {
	st := m.doc.Stations[m.station]
	if i < 0 || i >= len(st.Notes) {
		return
	}
	n := st.Notes[i]
	if n.Part >= 0 {
		key := doc.LineKey(st.Parts[n.Part], n.Line)
		for idx, r := range m.rows {
			if r.kind == rowCode && doc.LineKey(st.Parts[r.part], r.line) == key {
				m.setCursor(idx)
				break
			}
		}
	} else {
		m.setStatus(true, "note %d: %s", i+1, n.Problem)
	}
	m.note = i
	m.markSeen()
}

func (m *Model) nextNote(dir int) {
	if m.doc == nil {
		return
	}
	i := m.note + dir
	for {
		notes := m.doc.Stations[m.station].Notes
		if i >= 0 && i < len(notes) {
			m.gotoNote(i)
			return
		}
		next := m.station + dir
		if next < 0 || next >= len(m.doc.Stations) {
			return
		}
		m.setStation(next)
		if dir > 0 {
			i = 0
		} else {
			i = len(m.doc.Stations[m.station].Notes) - 1
		}
		if len(m.doc.Stations[m.station].Notes) == 0 {
			i = -2 * dir
			if dir < 0 {
				i = len(m.doc.Stations[m.station].Notes)
			}
			continue
		}
	}
}

func (m *Model) findStation(s string) (int, error) {
	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 || n >= len(m.doc.Stations) {
			return 0, fmt.Errorf("station %d out of range 0-%d", n, len(m.doc.Stations)-1)
		}
		return n, nil
	}
	if i, _ := m.doc.Station(s); i >= 0 {
		return i, nil
	}
	return 0, fmt.Errorf("no station %q", s)
}

func (m *Model) gotoTarget(arg string) error {
	if m.doc == nil {
		return errors.New("review not loaded")
	}
	arg = strings.TrimSpace(arg)
	head, tail := arg, ""
	if i := strings.LastIndex(arg, ":"); i >= 0 {
		head, tail = arg[:i], arg[i+1:]
	}
	if tail != "" && strings.ContainsAny(head, "./") {
		if line, err := strconv.Atoi(tail); err == nil {
			return m.gotoFileLine(head, line)
		}
	}
	si, err := m.findStation(head)
	if err != nil {
		return err
	}
	m.setStation(si)
	if tail == "" {
		return nil
	}
	n, err := strconv.Atoi(tail)
	notes := m.doc.Stations[si].Notes
	if err != nil || n < 1 || n > len(notes) {
		return fmt.Errorf("station %s has %d notes", m.doc.Stations[si].ID, len(notes))
	}
	m.gotoNote(n - 1)
	return nil
}

// gotoFileLine looks for the line inside a part first, then with folds opened, then in the whole file.
func (m *Model) gotoFileLine(file string, line int) error {
	order := []int{m.station}
	for i := range m.doc.Stations {
		if i != m.station {
			order = append(order, i)
		}
	}
	match := func(p *doc.Part) bool {
		return p.Err == nil && !p.Base && (p.Spec.File == file || strings.HasSuffix(p.Spec.File, "/"+file))
	}
	for pass := 0; pass < 3; pass++ {
		if pass == 1 {
			if m.unfold {
				continue
			}
			m.unfold = true
			m.rebuild()
		}
		for _, si := range order {
			for pi, p := range m.doc.Stations[si].Parts {
				if !match(p) || (pass < 2 && !p.Range.Contains(line-1)) || line < 1 || line > len(p.Lines) {
					continue
				}
				if m.station != si {
					m.setStation(si)
				}
				if pass == 2 {
					m.whole[[2]int{si, pi}] = true
					m.rebuild()
				}
				for idx, r := range m.rows {
					if r.kind == rowCode && r.part == pi && r.line == line-1 {
						m.setCursor(idx)
						return nil
					}
				}
			}
		}
	}
	return fmt.Errorf("%s:%d is not in any station", file, line)
}

type Counts struct {
	Notes     int `json:"notes"`
	Reviewed  int `json:"reviewed"`
	Flagged   int `json:"flagged"`
	Dismissed int `json:"dismissed"`
	Issues    int `json:"issues"`
	Questions int `json:"questions"`
	Changed   int `json:"changed"`
	Lost      int `json:"lost"`
	Open      int `json:"open_questions"`
}

func (m *Model) counts() Counts {
	var c Counts
	if m.doc == nil || m.state == nil {
		return c
	}
	for _, n := range m.doc.CodeNotes() {
		c.Notes++
		if m.state.Reviewed[n.Key] {
			c.Reviewed++
		}
		if m.state.Flagged[n.Key] {
			c.Flagged++
		}
		switch {
		case m.state.Dismissed[n.Key]:
			c.Dismissed++
		case n.Unbacked() || n.Level() == doc.KindQuestion:
			c.Questions++
		case n.Level() == doc.KindIssue:
			c.Issues++
		}
		if n.Changed != "" && m.state.Seen[n.Key] != n.Changed {
			c.Changed++
		}
		if n.Problem != "" {
			c.Lost++
		}
	}
	answered := m.doc.AnsweredQuestions()
	for _, q := range m.state.Questions {
		if !answered[q.ID] {
			c.Open++
		}
	}
	return c
}

type Where struct {
	Review   string    `json:"review"`
	Station  Station   `json:"station"`
	Note     *NoteRef  `json:"note,omitempty"`
	Cursor   *LineRef  `json:"cursor,omitempty"`
	Visible  []Visible `json:"visible,omitempty"`
	Counts   Counts    `json:"counts"`
	Problems int       `json:"problems"`
}

type Station struct {
	Index int    `json:"index"`
	Last  int    `json:"last"`
	ID    string `json:"id"`
	Title string `json:"title"`
}

type NoteRef struct {
	Number    int    `json:"number"`
	Kind      string `json:"kind"`
	At        string `json:"at"`
	Text      string `json:"text"`
	Problem   string `json:"problem,omitempty"`
	Reviewed  bool   `json:"reviewed"`
	Flagged   bool   `json:"flagged"`
	Dismissed bool   `json:"dismissed"`
}

type LineRef struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Side string `json:"side,omitempty"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type Visible struct {
	File string `json:"file"`
	From int    `json:"from"`
	To   int    `json:"to"`
}

var kindNames = map[diffmap.Kind]string{diffmap.Same: "same", diffmap.Added: "added", diffmap.Changed: "changed", diffmap.Removed: "removed"}

func (m *Model) where() Where {
	w := Where{Review: m.opts.ReviewPath}
	if m.doc == nil {
		return w
	}
	st := m.doc.Stations[m.station]
	w.Station = Station{Index: m.station, Last: len(m.doc.Stations) - 1, ID: st.ID, Title: st.Title}
	w.Counts = m.counts()
	w.Problems = len(m.doc.Problems)
	if n := m.selectedNote(); n != nil {
		w.Note = &NoteRef{Number: m.note + 1, Kind: n.Level(), At: n.At, Text: n.Text, Problem: n.Problem,
			Reviewed: m.state.Reviewed[n.Key], Flagged: m.state.Flagged[n.Key], Dismissed: m.state.Dismissed[n.Key]}
	}
	if r, ok := m.cursorRow(); ok {
		p := st.Parts[r.part]
		kind := diffmap.Same
		if r.line < len(p.Kinds) {
			kind = p.Kinds[r.line]
		}
		w.Cursor = &LineRef{File: p.Spec.File, Line: r.line + 1, Side: p.Spec.Side, Kind: kindNames[kind], Text: p.Lines[r.line]}
	}
	for i := m.top; i < min(m.top+m.bodyHeight(), len(m.rows)); i++ {
		r := m.rows[i]
		if r.kind != rowCode {
			continue
		}
		file := st.Parts[r.part].Spec.File
		if n := len(w.Visible); n > 0 && w.Visible[n-1].File == file && w.Visible[n-1].To == r.line {
			w.Visible[n-1].To = r.line + 1
			continue
		}
		w.Visible = append(w.Visible, Visible{File: file, From: r.line + 1, To: r.line + 1})
	}
	return w
}

type QuestionRef struct {
	state.Question
	Answered bool `json:"answered"`
}

func Questions(r *review.Review, st *state.State, all bool) []QuestionRef {
	answered := map[string]bool{}
	for _, s := range r.Stations {
		for _, n := range s.Notes {
			if n.QID != "" {
				answered[n.QID] = true
			}
		}
	}
	out := []QuestionRef{}
	for _, q := range st.Questions {
		if all || !answered[q.ID] {
			out = append(out, QuestionRef{Question: q, Answered: answered[q.ID]})
		}
	}
	return out
}

func (m *Model) control(msg CtlMsg) (cmd teaCmd) {
	reply := func(r control.Response) { msg.Reply <- r }
	if m.doc == nil && m.loadErr == nil && msg.Req.Cmd != "ping" && msg.Req.Cmd != "review" && msg.Req.Cmd != "reload" {
		m.waiting = append(m.waiting, msg)
		return nil
	}
	switch msg.Req.Cmd {
	case "ping":
		reply(control.OK("pong"))
	case "review":
		reply(control.OK(map[string]string{"review": m.opts.ReviewPath, "state": review.StatePath(m.opts.ReviewPath)}))
	case "where":
		reply(control.OK(m.where()))
	case "goto":
		if err := m.gotoTarget(msg.Req.Arg); err != nil {
			reply(control.Fail(err))
		} else {
			reply(control.OK(m.where()))
		}
	case "reload":
		return m.reload(msg.Reply)
	case "questions":
		if m.doc == nil {
			reply(control.Fail(errors.New("review not loaded")))
		} else {
			reply(control.OK(Questions(m.doc.Review, m.state, msg.Req.Arg == "all")))
		}
	default:
		reply(control.Fail(fmt.Errorf("unknown command %q", msg.Req.Cmd)))
	}
	return nil
}
