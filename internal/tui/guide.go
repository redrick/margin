package tui

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/redrick/margin/internal/anchor"
	"github.com/redrick/margin/internal/around"
	"github.com/redrick/margin/internal/diffmap"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/text"
	"github.com/redrick/margin/internal/verdict"
)

// The guided walk shows a stop one step at a time, the way a code tour does: a card of what to
// expect, the unchanged code to know first, each change on its own with the agent's notes in full
// below it, and a wrap-up where the reader settles the stop. Reviewers hold only a few change parts
// in mind at once, so showing one at a time keeps the rest out of the way.

type stepKind uint8

const (
	stepBrief stepKind = iota
	stepBackground
	stepChange
	stepMech
	stepWrap
)

type step struct {
	kind     stepKind
	part     int
	from, to int
	// n counts the changes from 1; of is how many there are.
	n, of int
	// subs are the small mechanical edits a housekeeping step shows together.
	subs []step
}

const (
	// blockContext is how many unchanged lines a change shows around it; changes closer than twice
	// that share a step.
	blockContext = 3
	// maxBlock is the most lines one step shows before it is split, at a blank line where it can be.
	maxBlock = 40
	// joinGap is how many unchanged lines may separate two changes that are still read as one.
	joinGap = 8
)

func (m *Model) guideOn() bool {
	return m.guide && m.doc != nil && m.doc.Stations[m.station].Kind == doc.Code
}

// guideSteps lays a stop out as the walk reads it: the brief, the code to know first, each change
// as one reading unit, the mechanical edits together, and the wrap-up.
func (m *Model) guideSteps(st *doc.Station) []step {
	steps := []step{{kind: stepBrief}}
	for pi, p := range st.Parts {
		if p.Spec.Background {
			s := step{kind: stepBackground, part: pi, from: -1, to: -1}
			if p.Err == nil {
				s.from, s.to = p.Range.Start, p.Range.End
			}
			steps = append(steps, s)
		}
	}
	first := len(steps)
	var mech []step
	for pi, p := range st.Parts {
		switch {
		case p.Spec.Background:
		case p.Err != nil:
			steps = append(steps, step{kind: stepChange, part: pi, from: -1, to: -1})
		default:
			for _, b := range blocks(st, pi, m.partRange(m.station, pi)) {
				s := step{kind: stepChange, part: pi, from: b.from, to: b.to}
				if b.mech {
					mech = append(mech, s)
				} else {
					steps = append(steps, s)
				}
			}
		}
	}
	for i := first; i < len(steps); i++ {
		steps[i].n, steps[i].of = i-first+1, len(steps)-first
	}
	if len(mech) > 0 {
		steps = append(steps, step{kind: stepMech, part: -1, from: -1, to: -1, subs: mech})
	}
	return append(steps, step{kind: stepWrap})
}

type block struct {
	from, to int
	// mech marks an edit with nothing to judge, such as an import, that the walk shows in passing.
	mech bool
}

// blocks splits a part into reading units: each run of changed lines with a little context, runs
// in the same function or close together joined, and anything long split up.
func blocks(st *doc.Station, pi int, rng anchor.Range) []block {
	p := st.Parts[pi]
	var changed []int
	if !p.Base && !p.NewFile && p.Kinds != nil {
		for l := rng.Start; l <= rng.End; l++ {
			if (lineKind(p, l) != diffmap.Same || len(p.Ghosts[l]) > 0) && !blankChange(p, l) {
				changed = append(changed, l)
			}
		}
		if rng.End+1 == len(p.Lines) && len(p.Ghosts[len(p.Lines)]) > 0 {
			changed = append(changed, rng.End)
		}
	}
	if len(changed) == 0 {
		var out []block
		for _, c := range chunk(p, rng.Start, rng.End) {
			out = append(out, block{from: c[0], to: c[1]})
		}
		return out
	}
	type run struct {
		from, to, first, last int
		mech                  bool
	}
	var runs []run
	for _, l := range changed {
		from, to := max(l-blockContext, rng.Start), min(l+blockContext, rng.End)
		mech := mechanical(st, pi, l, l)
		if k := len(runs) - 1; k >= 0 && from <= runs[k].to+1 && runs[k].mech == mech {
			runs[k].to, runs[k].last = max(runs[k].to, to), l
			continue
		}
		if k := len(runs) - 1; k >= 0 {
			// Runs of another kind meet halfway, so an import edit does not drag the code below it in.
			if from <= runs[k].to {
				mid := (runs[k].last + l + 1) / 2
				runs[k].to, from = max(mid-1, runs[k].last), max(mid, runs[k].last+1)
			}
		}
		runs = append(runs, run{from: from, to: to, first: l, last: l, mech: mech})
	}
	var joined []run
	for _, r := range runs {
		if k := len(joined) - 1; k >= 0 {
			prev := &joined[k]
			if prev.mech == r.mech && r.to-prev.from < maxBlock &&
				(r.first-prev.last <= joinGap || sameFunc(p, prev.last, r.first)) {
				prev.to, prev.last = r.to, r.last
				continue
			}
		}
		joined = append(joined, r)
	}
	var out []block
	for _, r := range joined {
		for _, c := range chunk(p, r.from, r.to) {
			out = append(out, block{from: c[0], to: c[1], mech: r.mech})
		}
	}
	return out
}

// blankChange reports whether all that changed at a line is blank lines, which need no step of
// their own.
func blankChange(p *doc.Part, l int) bool {
	for _, g := range p.Ghosts[l] {
		if strings.TrimSpace(g) != "" {
			return false
		}
	}
	return lineKind(p, l) == diffmap.Same || strings.TrimSpace(p.Lines[l]) == ""
}

func sameFunc(p *doc.Part, a, b int) bool {
	na, da, oka := around.Enclosing(p.Spec.File, p.Lines, a)
	nb, db, okb := around.Enclosing(p.Spec.File, p.Lines, b)
	return oka && okb && na == nb && da == db
}

var goImport = regexp.MustCompile(`^(?:[\w.]+\s+)?"[^"\s]+"$`)

// mechanical reports whether every changed line between first and last is an import or blank,
// and no note on them asks the reader for anything.
func mechanical(st *doc.Station, pi, first, last int) bool {
	p := st.Parts[pi]
	for _, n := range st.Notes {
		if n.Part == pi && n.Line >= first && n.Line <= last+1 && asks(n) {
			return false
		}
	}
	for l := first; l <= last && l <= len(p.Lines); l++ {
		for _, g := range p.Ghosts[l] {
			if !importLine(p, l, g) {
				return false
			}
		}
		if l < len(p.Lines) && lineKind(p, l) != diffmap.Same && !importLine(p, l, p.Lines[l]) {
			return false
		}
	}
	return true
}

// asks reports whether a note wants something from the reader: an answer, a call or a judgement.
func asks(n *doc.Note) bool {
	switch n.Level() {
	case doc.KindIssue, doc.KindQuestion, doc.KindDecide:
		return true
	}
	return false
}

func importLine(p *doc.Part, l int, s string) bool {
	t := strings.TrimSpace(s)
	switch {
	case t == "", t == "import (", t == ")" && inGoImports(p, l):
		return true
	case strings.HasPrefix(t, "import "), strings.HasPrefix(t, "#include "):
		return true
	case strings.HasPrefix(t, "from ") && strings.Contains(t, " import "):
		return true
	}
	return goImport.MatchString(t) && inGoImports(p, l)
}

// inGoImports reports whether line l of a Go file sits inside an import ( … ) block.
func inGoImports(p *doc.Part, l int) bool {
	if !strings.HasSuffix(p.Spec.File, ".go") {
		return false
	}
	for i := min(l, len(p.Lines)) - 1; i >= 0; i-- {
		t := strings.TrimSpace(p.Lines[i])
		switch {
		case t == "import (":
			return true
		case t == ")", strings.HasPrefix(t, "func "), strings.HasPrefix(t, "type "), strings.HasPrefix(t, "var "):
			return false
		}
	}
	return false
}

func chunk(p *doc.Part, from, to int) [][2]int {
	var out [][2]int
	for from <= to {
		end := min(from+maxBlock-1, to)
		if end < to {
			for b := end; b > from+maxBlock*2/3; b-- {
				if strings.TrimSpace(p.Lines[b]) == "" {
					end = b
					break
				}
			}
		}
		out = append(out, [2]int{from, end})
		from = end + 1
	}
	return out
}

// shows reports whether a step draws a line of a part.
func (s step) shows(st *doc.Station, part, line int) bool {
	if s.kind == stepMech {
		for _, sub := range s.subs {
			if sub.shows(st, part, line) {
				return true
			}
		}
		return false
	}
	if s.part != part || (s.kind != stepChange && s.kind != stepBackground) {
		return false
	}
	p := st.Parts[part]
	end := s.to
	if end == p.Range.End && end+1 == len(p.Lines) {
		end++
	}
	return line >= s.from && line <= end
}

// stepOf finds the step that shows a line of a part, or -1.
func (m *Model) stepOf(st *doc.Station, part, line int) int {
	for i, s := range m.guideSteps(st) {
		if s.shows(st, part, line) {
			return i
		}
	}
	return -1
}

// showStep switches the guided walk to the step that holds a line, so jumps land on it.
func (m *Model) showStep(part, line int) {
	if !m.guideOn() {
		return
	}
	if i := m.stepOf(m.doc.Stations[m.station], part, line); i >= 0 && i != m.step {
		m.step = i
		m.rebuild()
	}
}

func (m *Model) guideRows(st *doc.Station) {
	steps := m.guideSteps(st)
	m.step = min(max(m.step, 0), len(steps)-1)
	s := steps[m.step]
	m.progressRow(steps)
	switch s.kind {
	case stepBrief:
		m.briefRows(st, steps)
	case stepBackground:
		m.add(rowText, toneHeading, "Know this first")
		m.wrapped(toneDim, 2, "Unchanged code the change relies on. Read it so the change makes sense; there is nothing to judge in it.")
		m.add(rowText, tonePlain, "")
		m.blockRows(st, s)
	case stepChange:
		m.blockRows(st, s)
	case stepMech:
		m.mechRows(st, s)
	case stepWrap:
		m.wrapRows(st)
	}
}

// codeStops counts the stops of the walk, and says which of them the current stop is.
func (m *Model) codeStops() (at, of int) {
	for i, s := range m.doc.Stations {
		if s.Kind != doc.Code {
			continue
		}
		of++
		if i <= m.station {
			at = of
		}
	}
	return at, of
}

// phaseRow says which of the three phases of the review the reader is in: orient, walk, decide.
func (m *Model) phaseRow(phase int, detail string) {
	names := []string{"① Orient", "② Walk", "③ Decide"}
	for i := range names {
		if i+1 != phase {
			names[i] = strings.ToLower(names[i][len("① "):])
		}
	}
	line := strings.Join(names, " ─ ")
	if detail != "" {
		line += "   " + detail
	}
	m.colored(colHeading, line)
}

func (m *Model) progressRow(steps []step) {
	var label string
	switch s := steps[m.step]; s.kind {
	case stepBrief:
		label = "what this stop is"
	case stepBackground:
		label = "know this first"
	case stepChange:
		label = fmt.Sprintf("change %d of %d", s.n, s.of)
	case stepMech:
		label = "housekeeping"
	case stepWrap:
		label = "your verdict"
	}
	at, of := m.codeStops()
	m.phaseRow(2, fmt.Sprintf("stop %d of %d · step %d of %d · %s", at, of, m.step+1, len(steps), label))
	bar := strings.Repeat("━", m.step+1) + strings.Repeat("─", len(steps)-m.step-1)
	if len(steps) <= 30 {
		m.colored(colDim, bar)
	}
	m.add(rowText, tonePlain, "")
}

// briefRows is the stop card: what the stop is, why it comes now and the one thing to check, in a
// few lines. e shows the rest.
func (m *Model) briefRows(st *doc.Station, steps []step) {
	m.wrapped(toneTitle, 0, st.Title)
	if st.Lede != "" {
		m.wrapped(toneLede, 0, st.Lede)
	}
	m.add(rowText, tonePlain, "")
	if st.Auto {
		m.wrapped(toneProblem, 0, "These changes are in no stop of the tour, so nobody has explained them. Read them one by one, or ask the agent to place them in a stop or in skip.")
		m.add(rowText, tonePlain, "")
	}
	if st.RiskWhy != "" {
		m.cardLine("Risk", strings.TrimSpace(st.Risk+": "+oneLine(st.RiskWhy)), riskColor(st.Risk))
	}
	if st.OrderWhy != "" {
		m.cardLine("Why now", oneLine(st.OrderWhy), colText)
	}
	checks, _ := checksFor(st)
	check := checks[0]
	if len(checks) > 1 {
		check += fmt.Sprintf("  (+%d more at the end)", len(checks)-1)
	}
	m.cardLine("Check", check, colChanged)
	if n := st.Decisions(); n > 0 {
		m.cardLine("Your call", plural(n, "decision only you can make comes", "decisions only you can make come")+" up on the way", colDecide)
	}
	m.add(rowText, tonePlain, "")
	background, changes, mech := 0, 0, 0
	for _, s := range steps {
		switch s.kind {
		case stepBackground:
			background++
		case stepChange:
			changes++
		case stepMech:
			mech = len(s.subs)
		}
	}
	ahead := plural(changes, "change", "changes")
	if background > 0 {
		ahead = plural(background, "piece of code to know first", "pieces of code to know first") + ", then " + ahead
	}
	if mech > 0 {
		ahead += ", then " + plural(mech, "import edit", "import edits") + " at a glance"
	}
	m.wrapped(toneDim, 0, fmt.Sprintf("Ahead: %s, %d lines. space starts.", ahead, st.LOC()))
	if !m.more {
		if more := m.moreLabel(st); more != "" {
			m.wrapped(toneDim, 0, "e shows "+more+".")
		}
		return
	}
	m.add(rowText, tonePlain, "")
	for _, b := range []labelled{{"What it does", st.What}, {"Why it changed", st.Why}} {
		if strings.TrimSpace(b.text) == "" {
			continue
		}
		m.add(rowText, toneHeading, b.label)
		m.marked(2, b.text, colText, false)
		m.add(rowText, tonePlain, "")
	}
	if st.Flow != "" {
		m.add(rowText, toneHeading, "Flow")
		for _, l := range text.Lines(text.Sanitize(st.Flow)) {
			m.add(rowText, toneFlow, "  "+l)
		}
		m.add(rowText, tonePlain, "")
	}
	if m.exampleRows(st) {
		m.add(rowText, tonePlain, "")
	}
	if len(checks) > 1 {
		m.add(rowText, toneHeading, "All the checks")
		for _, c := range checks {
			m.wrapped(tonePlain, 2, "· "+c)
		}
		m.add(rowText, tonePlain, "")
	}
	m.wrapped(toneDim, 0, "e hides this again.")
}

// moreLabel names what e would add to the stop card, or "" when there is nothing.
func (m *Model) moreLabel(st *doc.Station) string {
	var what []string
	if strings.TrimSpace(st.What+st.Why) != "" {
		what = append(what, "what and why")
	}
	if st.Flow != "" {
		what = append(what, "the flow")
	}
	if n := len(st.Examples); n > 0 {
		what = append(what, plural(n, "before/after example", "before/after examples"))
	}
	if checks, _ := checksFor(st); len(checks) > 1 {
		what = append(what, "all the checks")
	}
	if len(what) == 0 {
		return ""
	}
	return shortList(what)
}

// cardLine is one labelled line of a card: the label in a fixed column, the text wrapped after it.
func (m *Model) cardLine(label, s, fg string) {
	m.cardCapped(label, s, fg, 1<<30)
}

// cardCol is the width of a card's label column.
const cardCol = 11

// checksFor is the stop's own checks, or general questions for its kind of change when the agent
// wrote none. own reports which.
func checksFor(st *doc.Station) (checks []string, own bool) {
	if len(st.Checks) > 0 {
		for _, c := range st.Checks {
			checks = append(checks, oneLine(text.Sanitize(c)))
		}
		return checks, true
	}
	switch st.Concern {
	case "fix":
		checks = []string{"Does it fix the case that was broken?", "Does anything change for callers that worked before?"}
	case "feature":
		checks = []string{"Does it do what was asked, also for empty, zero and very large inputs?", "Would you know how to use it from the names alone?"}
	case "refactor":
		checks = []string{"Does anything behave differently than before?", "Is it easier to read than what it replaces?"}
	case "tests":
		checks = []string{"Would these tests fail if the code they cover were wrong?", "Do they test behaviour rather than details that may change?"}
	case "config":
		checks = []string{"What happens when this value is missing or wrong, in each environment?"}
	case "docs":
		checks = []string{"Is it true, and does it match the code?"}
	default:
		checks = []string{"Do you understand what every changed line does?", "Could it be simpler?"}
	}
	if st.Risk == "high" {
		checks = append(checks, "What is the worst that happens if this is wrong, and would anyone notice?")
	}
	return checks, false
}

func shortList(names []string) string {
	if len(names) > 4 {
		return strings.Join(names[:3], ", ") + fmt.Sprintf(" and %d more", len(names)-3)
	}
	if len(names) > 1 {
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
	return names[0]
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// blockRows draws one step's code, then everything said about it in full: the agent's notes, the
// reader's comments, and who else calls the function it changes.
func (m *Model) blockRows(st *doc.Station, s step) {
	if !m.codeBlock(st, s, false) {
		return
	}
	m.add(rowText, tonePlain, "")
	notes := m.stepNotes(st, s)
	switch {
	case m.blind(st):
		m.colored(colChanged, "Your pass first: the agent's notes stay hidden on this high-risk stop. Decide what you think, then press v to compare.")
	case len(notes) == 0 && !st.Auto && s.kind == stepChange:
		m.colored(colDim, "The agent left no note on this change, so read it on your own.")
	case m.notesShown() && len(notes) > 0:
		m.colored(colDim, fmt.Sprintf("%s beside the code · ] [ step through them.", plural(len(notes), "note", "notes")))
	}
	m.stepCards(st, s, notes)
	m.settleRows(st, notes)
	m.callerRows(st, s)
	m.add(rowText, tonePlain, "")
	if s.kind == stepBackground {
		m.wrapped(toneDim, 0, "space: on to the change.")
		return
	}
	m.wrapped(toneFlow, 0, "▸ "+m.stepQuestion(st, s, notes))
	m.wrapped(toneDim, 0, "space: yes, go on · c comment on the cursor line · a ask the agent · b back")
}

// codeBlock draws one step's code under a header naming the file and what the code is.
func (m *Model) codeBlock(st *doc.Station, s step, mech bool) bool {
	p := st.Parts[s.part]
	title := "── " + p.Spec.File
	switch defs := around.Defines(p.Spec.File, p.Lines, s.from, s.to); {
	case (p.Base || p.NewFile) && len(defs) > 0:
		verb := "adds "
		if p.Base {
			verb = "removes "
		}
		title += " · " + verb + shortList(defs)
	case p.Base:
		title += " · removed code"
	case p.NewFile:
		title += " · new file"
	default:
		if name := enclosingName(p, s); name != "" {
			title += " · in " + name
		}
	}
	if s.from >= 0 {
		title += fmt.Sprintf(" · lines %d–%d", s.from+1, s.to+1)
	}
	m.rows = append(m.rows, row{kind: rowHeader, part: s.part, text: title})
	if about := strings.TrimSpace(p.Spec.About); about != "" && !mech && (s.kind == stepBackground || s.from <= p.Range.Start) {
		m.marked(2, about, colDim, true)
	}
	if p.Err != nil {
		m.wrapped(toneProblem, 2, p.Err.Error())
		return false
	}
	all := st.NotesByLine()
	split := m.splitPart(p)
	for i := s.from; i <= s.to; i++ {
		start := changeStart(p, i)
		if !split || start < 0 {
			m.ghostRows(p, s.part, i, 0)
		}
		m.rows = append(m.rows, row{kind: rowCode, part: s.part, line: i, notes: m.visibleAt(st, all[doc.LineKey(p, i)])})
		if split && start >= 0 && (i == s.to || changeStart(p, i+1) != start) {
			m.ghostRows(p, s.part, start, i-start+1)
		}
	}
	if s.to == p.Range.End && s.to+1 == len(p.Lines) {
		m.ghostRows(p, s.part, len(p.Lines), 0)
	}
	return true
}

// stepCards puts the notes and comments under the code when the pane is too narrow for the notes
// column.
func (m *Model) stepCards(st *doc.Station, s step, notes []int) {
	if m.notesShown() {
		return
	}
	w := max(m.codeWidth()-3, 20)
	p := st.Parts[s.part]
	for _, ni := range notes {
		m.cardRows(m.noteCard(st, ni, w, ni == m.note))
	}
	for l := s.from; l <= s.to; l++ {
		for _, q := range m.commentsAt(p, l) {
			m.cardRows(m.commentCard(&q, w))
		}
	}
}

// settleRows lists what the step's notes ask of the reader, right where they read it: calls to
// make and problems to agree with or dismiss.
func (m *Model) settleRows(st *doc.Station, notes []int) {
	heading := false
	for _, ni := range notes {
		n := st.Notes[ni]
		lvl := n.Level()
		if m.state.Dismissed[n.Key] || (lvl != doc.KindDecide && lvl != doc.KindIssue && lvl != doc.KindQuestion) {
			continue
		}
		if !heading {
			m.add(rowText, toneHeading, "Settle here · j moves down to it")
			heading = true
		}
		if lvl == doc.KindDecide {
			m.toggleRow(colDecide, n.Key, m.fitHint("☐ ◆ your call: ", oneLine(plain(n.Text)), " · enter when decided")[len("☐ "):], ni)
			continue
		}
		if lvl == doc.KindQuestion {
			m.questionRow(st, ni, "")
			continue
		}
		mark := "! "
		if m.state.Flagged[n.Key] {
			mark = "⚑ "
		}
		m.rows = append(m.rows, row{kind: rowLink, fg: colProblem, text: m.fitHint(mark, oneLine(plain(n.Text)), " · ? agree · x dismiss"),
			target: m.station, noteAt: ni, comment: -1, notes: []int{ni}})
	}
}

// stepQuestion is the one thing the reader answers before going on.
func (m *Model) stepQuestion(st *doc.Station, s step, notes []int) string {
	if c := strings.TrimSpace(st.Parts[s.part].Spec.Check); c != "" && !m.blind(st) {
		return oneLine(text.Sanitize(c))
	}
	kinds := map[string]bool{}
	for _, ni := range notes {
		kinds[st.Notes[ni].Level()] = true
	}
	switch {
	case kinds[doc.KindIssue]:
		return "The agent sees a problem here. Do you agree? ? agrees and flags it, x dismisses it."
	case kinds[doc.KindDecide]:
		return "There is a call to make here. Decide, then enter ticks it."
	case kinds[doc.KindQuestion]:
		return "The agent asks you something here. Answer it from the list below, or x dismisses it."
	case len(notes) > 0:
		return "Does the code do what the notes say, and is that the right thing to do?"
	}
	return "Can you say what each changed line does, and is it right?"
}

// mechRows shows a stop's import edits together, to glance at rather than judge.
func (m *Model) mechRows(st *doc.Station, s step) {
	m.add(rowText, toneHeading, "Housekeeping")
	m.wrapped(toneDim, 2, "Import lines that follow from the changes you just read. Glance over them; there is nothing to judge unless something looks out of place.")
	m.add(rowText, tonePlain, "")
	for _, sub := range s.subs {
		if m.codeBlock(st, sub, true) {
			m.stepCards(st, sub, m.stepNotes(st, sub))
		}
		m.add(rowText, tonePlain, "")
	}
	m.wrapped(toneDim, 0, "space: on to your verdict · c comments on the cursor line")
}

func (m *Model) stepNotes(st *doc.Station, s step) []int {
	if m.blind(st) {
		return nil
	}
	p := st.Parts[s.part]
	end := s.to
	if end == p.Range.End && end+1 == len(p.Lines) {
		end++
	}
	var out []int
	for i, n := range st.Notes {
		if n.Part == s.part && n.Line >= s.from && n.Line <= end && m.noteVisible(st, n) {
			out = append(out, i)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return st.Notes[out[a]].Line < st.Notes[out[b]].Line })
	return out
}

// enclosingName names the function the step's first change sits in, looking past changed lines
// above the function, such as its doc comment.
func enclosingName(p *doc.Part, s step) string {
	if s.from < 0 {
		return ""
	}
	for l := s.from; l <= s.to; l++ {
		if lineKind(p, l) == diffmap.Same && len(p.Ghosts[l]) == 0 {
			continue
		}
		for c := l; c <= s.to; c++ {
			if name, _, ok := around.Enclosing(p.Spec.File, p.Lines, c); ok {
				return name
			}
		}
		return ""
	}
	return ""
}

// cardRows adds lines already painted to the code width, such as note cards.
func (m *Model) cardRows(lines []string) {
	for _, l := range lines {
		m.rows = append(m.rows, row{kind: rowPainted, text: l})
	}
	m.add(rowText, tonePlain, "")
}

// maxCallers is the most outside uses of a name a step lists.
const maxCallers = 5

// callerRows lists who outside the review uses a function or type this step changes.
func (m *Model) callerRows(st *doc.Station, s step) {
	e := m.around[aroundKey(st)]
	if e == nil || !e.done || s.from < 0 {
		return
	}
	p := st.Parts[s.part]
	for _, sym := range e.report.Symbols {
		if sym.File != p.Spec.File || sym.Line-1 < s.from || sym.Line-1 > s.to || sym.TooMany > 0 {
			continue
		}
		// Unused or widely used names say little at this point; the whole view lists them all.
		if len(sym.Uses) == 0 || len(sym.Uses) > maxCallers {
			continue
		}
		verb := "call"
		if sym.Type {
			verb = "use"
		}
		if len(sym.Uses) == 1 {
			verb += "s"
		}
		m.colored(colChanged, fmt.Sprintf("Outside this review, %s %s %s. Do they still work? enter peeks:", plural(len(sym.Uses), "place", "places"), verb, sym.Name))
		for _, u := range sym.Uses {
			m.rows = append(m.rows, row{kind: rowLink, fg: colText, text: fmt.Sprintf("  %s:%d  %s", u.File, u.Line, strings.TrimSpace(u.Text)),
				target: m.station, noteAt: -1, comment: -1, peek: true, file: u.File, line: u.Line - 1})
		}
	}
}

// wrapRows is the end of a stop: what is still open, and the reader's verdict.
func (m *Model) wrapRows(st *doc.Station) {
	m.wrapped(toneTitle, 0, "End of stop: "+st.Title)
	m.add(rowText, tonePlain, "")

	if m.blind(st) {
		m.colored(colChanged, "The agent's notes are still hidden. Press v to compare them with what you found, before you decide.")
		m.add(rowText, tonePlain, "")
	}
	checks, _ := checksFor(st)
	m.add(rowText, toneHeading, "You should be able to answer")
	for _, c := range checks {
		m.wrapped(tonePlain, 2, "· "+c)
	}
	m.add(rowText, tonePlain, "")

	heading := false
	open := func() {
		if !heading {
			m.add(rowText, toneHeading, "Still open · enter ticks a call or answers a question, ? agrees with a problem, x dismisses it")
			heading = true
		}
	}
	for ni, n := range st.Notes {
		if n.Level() != doc.KindDecide || m.state.Dismissed[n.Key] || m.state.Reviewed[n.Key] || !m.noteVisible(st, n) {
			continue
		}
		open()
		m.toggleRow(colDecide, n.Key, m.fitHint("☐ ◆ ", oneLine(plain(n.Text)), noteAt(st, n))[len("☐ "):], ni)
	}
	for ni, n := range st.Notes {
		if n.Level() != doc.KindIssue || m.state.Dismissed[n.Key] || m.state.Flagged[n.Key] || !m.noteVisible(st, n) {
			continue
		}
		open()
		m.rows = append(m.rows, row{kind: rowLink, fg: colProblem, text: m.fitHint("! ", oneLine(plain(n.Text)), noteAt(st, n)),
			target: m.station, noteAt: ni, comment: -1, notes: []int{ni}})
	}
	for ni, n := range st.Notes {
		if n.Level() != doc.KindQuestion || n.Q != "" || m.state.Dismissed[n.Key] || m.answered(n.Key) || !m.noteVisible(st, n) {
			continue
		}
		open()
		m.questionRow(st, ni, noteAt(st, n))
	}
	if heading {
		m.add(rowText, tonePlain, "")
	}

	resolved := m.doc.AnsweredQuestions()
	heading = false
	for _, q := range m.state.Questions {
		if !q.IsComment() || q.Station != st.ID {
			continue
		}
		if !heading {
			m.add(rowText, toneHeading, "Your comments here")
			heading = true
		}
		status := "sent"
		switch {
		case q.Draft:
			status = "draft"
		case resolved[q.ID]:
			status = "resolved"
		}
		m.wrapped(tonePlain, 2, fmt.Sprintf("✎ %s %s · %s:%d — %s", q.ID, status, path.Base(q.File), q.Line, q.Text))
	}
	if heading {
		m.add(rowText, tonePlain, "")
	}

	m.add(rowText, toneHeading, "How does this stop look to you?")
	cur := m.state.Verdicts[st.ID]
	for _, v := range verdicts {
		mark := "○ "
		if cur == v.id {
			mark = "● "
		}
		m.rows = append(m.rows, row{kind: rowLink, fg: v.fg, text: mark + v.key + "  " + v.label, target: m.station, noteAt: -1, comment: -1, verdict: v.id})
	}
	m.add(rowText, tonePlain, "")
	switch cur {
	case verdict.Good:
		m.wrapped(toneDim, 0, "space: on to the next stop.")
	case verdict.Changes:
		m.wrapped(toneFlow, 0, "Say what needs to change: b goes back to the code, c comments on a line. S sends your comments to the agent. Then space moves on.")
	case verdict.Unsure:
		m.wrapped(toneFlow, 0, "Not sure is a fine answer. b goes back and a asks the agent about the line that bothers you; w shows the whole stop at once. Then space moves on.")
	default:
		m.wrapped(toneDim, 0, "Press 1, 2 or 3.")
	}
}

var verdicts = []struct {
	id, key, label, fg string
}{
	{verdict.Good, "1", "looks good", colAdded},
	{verdict.Changes, "2", "needs changes", colProblem},
	{verdict.Unsure, "3", "not sure yet", colChanged},
}

func noteAt(st *doc.Station, n *doc.Note) string {
	if n.Part < 0 {
		return ""
	}
	return fmt.Sprintf(" (%s:%d)", path.Base(st.Parts[n.Part].Spec.File), n.Line+1)
}

// fitHint joins prefix, text and hint on one row, shortening text so the hint with its keys stays on
// screen.
func (m *Model) fitHint(prefix, text, hint string) string {
	room := m.codeWidth() - 3 - runewidth.StringWidth(prefix) - runewidth.StringWidth(hint)
	if room < 20 {
		return prefix + text + hint
	}
	return prefix + doc.Short(text, room) + hint
}

// questionRow lists a question the agent asks the reader. enter answers it as a draft comment on the
// question's line; a question with no line jumps to the note instead.
func (m *Model) questionRow(st *doc.Station, ni int, where string) {
	n := st.Notes[ni]
	mark, hint := "? ", " · enter answers · x dismiss"
	if m.answered(n.Key) {
		mark, hint = "✓ ", " · answered in your comments"
	}
	if n.Part < 0 {
		hint = " · enter goes to it · x dismiss"
	}
	m.rows = append(m.rows, row{kind: rowLink, fg: m.noteStyle(n).color, text: m.fitHint(mark, oneLine(plain(n.Text)), where+hint),
		target: m.station, noteAt: ni, comment: -1, notes: []int{ni}, answer: n.Part >= 0})
}

func (m *Model) toggleRow(fg, key, s string, note int) {
	box := "☐ "
	if m.state.Reviewed[key] {
		box = "☑ "
	}
	var notes []int
	if note >= 0 {
		notes = []int{note}
	}
	m.rows = append(m.rows, row{kind: rowLink, fg: fg, text: box + s, target: m.station, noteAt: -1, comment: -1, toggle: key, notes: notes})
}

func (m *Model) toggle(key string) {
	if m.state.Reviewed[key] {
		delete(m.state.Reviewed, key)
	} else {
		m.state.Reviewed[key] = true
	}
	if err := m.state.Save(); err != nil {
		m.setStatus(true, "saving marks: %v", err)
	}
	m.rebuildKeep()
}

func (m *Model) setVerdict(v string) {
	st := m.doc.Stations[m.station]
	if st.Kind != doc.Code {
		return
	}
	if m.state.Verdicts[st.ID] == v {
		delete(m.state.Verdicts, st.ID)
	} else {
		m.state.Verdicts[st.ID] = v
	}
	if err := m.state.Save(); err != nil {
		m.setStatus(true, "saving verdict: %v", err)
		return
	}
	if m.guideOn() {
		m.step = len(m.guideSteps(st)) - 1
	}
	m.rebuild()
	m.focusVerdict()
	switch m.state.Verdicts[st.ID] {
	case verdict.Changes:
		if !m.hasComments(st.ID) {
			m.setStatus(false, "needs changes: say what with c on the line")
			return
		}
	case "":
		m.setStatus(false, "verdict cleared")
		return
	}
	m.setStatus(false, "%s: %s", st.ID, verdictLabel(m.state.Verdicts[st.ID]))
}

func verdictLabel(id string) string {
	for _, v := range verdicts {
		if v.id == id {
			return v.label
		}
	}
	return "no verdict"
}

func (m *Model) focusVerdict() {
	for i, r := range m.rows {
		if r.verdict != "" && r.verdict == m.state.Verdicts[m.doc.Stations[m.station].ID] {
			m.setCursor(i)
			return
		}
	}
}

func (m *Model) hasComments(station string) bool {
	for _, q := range m.state.Questions {
		if q.IsComment() && q.Station == station {
			return true
		}
	}
	return false
}

// nextStep moves on: the notes of the step just read count as read, and after the wrap-up the
// walk goes on to the next stop, once the reader gave a verdict or pressed space twice.
func (m *Model) nextStep() {
	st := m.doc.Stations[m.station]
	steps := m.guideSteps(st)
	m.readStep(st, steps[m.step])
	if m.step < len(steps)-1 {
		m.step++
		m.enterStep()
		return
	}
	if m.state.Verdicts[st.ID] == "" && !m.nudged {
		m.nudged = true
		m.setStatus(false, "give this stop a verdict first: 1 looks good · 2 needs changes · 3 not sure (space again skips)")
		return
	}
	m.setStation(m.station + 1)
}

func (m *Model) prevStep() {
	if m.step > 0 {
		m.step--
		m.enterStep()
		return
	}
	if m.station == 0 {
		return
	}
	m.setStation(m.station - 1)
	if m.guideOn() {
		m.step = len(m.guideSteps(m.doc.Stations[m.station])) - 1
		m.enterStep()
	}
}

// enterStep draws the step and puts the cursor on its first changed line, where c and a act.
func (m *Model) enterStep() {
	m.nudged = false
	m.rebuild()
	m.cur, m.top = 0, 0
	st := m.doc.Stations[m.station]
	first := -1
	for i, r := range m.rows {
		if r.kind != rowCode {
			continue
		}
		if first < 0 {
			first = i
		}
		if lineKind(st.Parts[r.part], r.line) != diffmap.Same || len(r.notes) > 0 {
			first = i
			break
		}
	}
	switch {
	case first >= 0:
		m.setCursor(first)
		m.top = 0
		m.follow()
	case m.step == len(m.guideSteps(st))-1:
		m.clamp()
		m.focusVerdict()
	default:
		m.clamp()
		m.top = 0
	}
}

// readStep counts the notes a step showed as read, since the reader just went through them. Calls
// stay open until the reader ticks them.
func (m *Model) readStep(st *doc.Station, s step) {
	if s.kind == stepMech {
		for _, sub := range s.subs {
			m.readStep(st, sub)
		}
		return
	}
	if (s.kind != stepChange && s.kind != stepBackground) || s.from < 0 {
		return
	}
	changed := false
	for _, ni := range m.stepNotes(st, s) {
		n := st.Notes[ni]
		if n.Level() == doc.KindDecide || m.state.Reviewed[n.Key] {
			continue
		}
		m.state.Reviewed[n.Key] = true
		if n.Changed != "" {
			m.state.Seen[n.Key] = n.Changed
		}
		changed = true
	}
	if changed {
		if err := m.state.Save(); err != nil {
			m.setStatus(true, "saving marks: %v", err)
		}
	}
}

// toggleGuide switches between the guided walk and the whole stop, keeping the cursor line.
func (m *Model) toggleGuide() {
	st := m.doc.Stations[m.station]
	r, onCode := m.cursorRow()
	m.guide = !m.guide
	m.state.Full = !m.guide
	if err := m.state.Save(); err != nil {
		m.setStatus(true, "saving view: %v", err)
	}
	if m.guide {
		m.step = 0
		if onCode {
			if i := m.stepOf(st, r.part, r.line); i >= 0 {
				m.step = i
			}
		}
		m.enterStep()
		if onCode {
			m.restoreLine(r.part, r.line)
		}
		m.setStatus(false, "guided: one step at a time · space next · b back · w whole stop")
		return
	}
	m.rebuild()
	if onCode {
		m.restoreLine(r.part, r.line)
	}
	m.setStatus(false, "whole stop · w goes back to the guided walk")
}

func (m *Model) restoreLine(part, line int) {
	for i, r := range m.rows {
		if r.kind == rowCode && r.part == part && r.line == line {
			m.setCursor(i)
			return
		}
	}
}

// verdictRows opens the recap with the recommendation for the whole change.
func (m *Model) verdictRows() {
	o := verdict.Recommend(m.doc, m.state)
	fg, title := colChanged, "Not finished yet"
	switch o.Decision {
	case verdict.Approve:
		fg, title = colAdded, "Recommendation: approve"
	case verdict.RequestChanges:
		fg, title = colProblem, "Recommendation: request changes"
	}
	m.colored(fg, title)
	for _, s := range o.Blockers {
		m.wrapped(toneProblem, 2, "· "+s)
	}
	for _, s := range o.Left {
		m.wrapped(tonePlain, 2, "· "+s)
	}
	for _, s := range o.Notes {
		m.wrapped(toneDim, 2, "· "+s)
	}
	m.add(rowText, tonePlain, "")
	m.add(rowText, toneHeading, "Stops")
	for i, s := range m.doc.Stations {
		if s.Kind != doc.Code {
			continue
		}
		v := m.state.Verdicts[s.ID]
		fg, mark := colDim, "○"
		for _, d := range verdicts {
			if d.id == v {
				fg, mark = d.fg, "●"
			}
		}
		m.link(fg, fmt.Sprintf("%s %s · %s — %s", mark, s.ID, verdictLabel(v), s.Title), i, -1)
	}
	m.add(rowText, tonePlain, "")
}

// orientRows is the first phase: what the change is for, what it does, the route through it, and
// one question before any code: does the change make sense, done this way?
func (m *Model) orientRows() {
	r := m.doc.Review
	_, stops := m.codeStops()
	m.phaseRow(1, fmt.Sprintf("then %s, then your decision", plural(stops, "stop", "stops")))
	m.add(rowText, tonePlain, "")
	m.wrapped(toneTitle, 0, r.Title)
	if r.Kicker != "" {
		m.wrapped(toneDim, 0, r.Kicker)
	}
	m.add(rowText, tonePlain, "")

	cut := false
	switch asked := strings.TrimSpace(r.Asked); {
	case asked != "":
		cut = m.cardCapped("For", asked, colText, 3) || cut
	case strings.TrimSpace(r.Motivation) != "":
		cut = m.cardCapped("For", r.Motivation, colText, 3) || cut
	default:
		m.cardLine("For", "not recorded; margin --intent \"...\" says it next time", colDim)
	}
	switch {
	case r.Did != "":
		cut = m.cardCapped("Does", plain(r.Did), colText, 2) || cut
	case r.Summary != "":
		cut = m.cardCapped("Does", r.Summary, colText, 2) || cut
	default:
		m.cardLine("Does", "the agent is still reading the change; this fills in by itself", colDim)
	}
	switch {
	case r.Gap == "":
	case noGap(r.Gap):
		m.cardLine("Gap", "none: it does what was asked", colAdded)
	default:
		cut = m.cardCapped("Gap", plain(r.Gap), colChanged, 3) || cut
	}
	if cut {
		m.wrapped(toneDim, 0, "e shows it all, with the summary, the flow and the focus areas.")
	}
	m.add(rowText, tonePlain, "")

	m.add(rowText, toneHeading, "The route · enter opens a stop")
	total, read, n := 0, 0, 0
	for i, s := range m.doc.Stations {
		if s.Kind != doc.Code {
			continue
		}
		n++
		loc := s.LOC()
		total += loc
		dot, fg := "○", colText
		if v := m.state.Verdicts[s.ID]; v != "" {
			dot = "●"
			read += loc
			for _, d := range verdicts {
				if d.id == v {
					fg = d.fg
				}
			}
		} else if m.state.Visited[s.ID] {
			dot = "◐"
		}
		if s.Auto {
			fg = colProblem
		}
		risk := s.Risk
		if risk == "" {
			risk = "–"
		}
		m.link(fg, fmt.Sprintf("%2d %s %-6s %4d lines  %s", n, dot, risk, loc, s.Title), i, -1)
		if s.OrderWhy != "" {
			m.colored(colDim, "                        "+doc.Short(oneLine(s.OrderWhy), max(m.codeWidth()-26, 20)))
		}
	}
	m.add(rowText, tonePlain, "")
	mins := max((total-read)*60/400, 1)
	pace := fmt.Sprintf("%d lines, about %d minutes.", total-read, mins)
	if total-read > 400 {
		pace += " More than one sitting: stop after about 400 lines; margin picks up where you left."
	}
	m.wrapped(toneDim, 0, pace)
	m.add(rowText, tonePlain, "")

	m.add(rowText, toneHeading, "Before any code: does this change make sense, done this way?")
	answers := []struct{ id, key, label, fg string }{
		{verdict.OrientYes, "y", "yes, walk it", colAdded},
		{verdict.OrientNo, "n", "no: say why; that is the review, and it blocks the change", colProblem},
		{verdict.OrientUnsure, "?", "not sure yet, walk it anyway", colChanged},
	}
	for _, a := range answers {
		mark := "○ "
		if m.state.Orient == a.id {
			mark = "● "
		}
		m.colored(a.fg, "  "+mark+a.key+"  "+a.label)
	}
	if m.state.Orient == verdict.OrientNo && m.state.Design != "" {
		m.wrapped(toneProblem, 4, "you wrote: "+m.state.Design)
	}
	if m.state.Orient != "" {
		m.add(rowText, tonePlain, "")
		m.wrapped(toneDim, 0, "space walks the first stop.")
	}
}

// cardCapped is a card line cut to a few lines. It reports whether it cut anything.
func (m *Model) cardCapped(label, s, fg string, most int) bool {
	// A paragraph break, such as after a commit subject, ends a sentence when the text did not.
	var parts []string
	para := false
	for _, l := range text.Lines(text.Sanitize(strings.TrimSpace(s))) {
		if l = strings.TrimSpace(l); l == "" {
			para = true
			continue
		}
		if k := len(parts) - 1; k >= 0 && para && !strings.ContainsAny(parts[k][len(parts[k])-1:], ".:;!?,") {
			parts[k] += "."
		}
		parts, para = append(parts, l), false
	}
	flat := oneLine(strings.Join(parts, " "))
	wrapped := wrap(flat, max(m.codeWidth()-2-cardCol, 10))
	cut := len(wrapped) > most
	if cut {
		wrapped = wrapped[:most]
		wrapped[most-1] += " …"
	}
	for i, l := range wrapped {
		head := strings.Repeat(" ", cardCol)
		if i == 0 {
			head = fmt.Sprintf("%-*s", cardCol, label)
		}
		m.colored(fg, head+l)
	}
	return cut
}

// orient records the reader's answer to the first question and moves on: into the walk, or for a
// no, into writing why.
func (m *Model) orient(answer string) tea.Cmd {
	if answer == verdict.OrientNo {
		m.asking, m.designing = true, true
		m.input.SetValue(m.state.Design)
		m.input.Prompt = " why not? › "
		m.input.Placeholder = "what is wrong with the approach, for the author; enter saves, esc cancels"
		return m.input.Focus()
	}
	m.state.Orient, m.state.Design = answer, ""
	if err := m.state.Save(); err != nil {
		m.setStatus(true, "saving answer: %v", err)
	}
	m.setStation(m.station + 1)
	if m.guideOn() {
		m.step = 0
		m.enterStep()
	}
	return nil
}

// design saves why the change does not make sense and goes to the decision.
func (m *Model) design(why string) {
	m.state.Orient, m.state.Design = verdict.OrientNo, why
	if err := m.state.Save(); err != nil {
		m.setStatus(true, "saving answer: %v", err)
		return
	}
	for i, s := range m.doc.Stations {
		if s.Kind == doc.Recap {
			m.setStation(i)
			break
		}
	}
	m.setStatus(false, "saved: this blocks the change. You can still walk the stops with }.")
}
