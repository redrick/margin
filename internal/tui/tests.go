package tui

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/redrick/margin/internal/coverage"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/source"
	"github.com/redrick/margin/internal/text"
	"github.com/redrick/margin/internal/tmuxx"
)

// testPicker lists the tests that run one line, so the reader can spotlight one, read its source or
// have the agent run them.
type testPicker struct {
	list   []int // indices into Coverage.Tests
	cur    int
	picked map[int]bool
	file   string
	line   int // 1-based
	needle string
}

// openTests opens the picker for the cursor line.
func (m *Model) openTests() {
	r, ok := m.cursorRow()
	switch {
	case !ok:
		m.setStatus(false, "%s", m.needLine("see the tests that run it"))
		return
	case m.doc.Coverage == nil:
		m.setStatus(false, "no coverage yet: ask the agent to run margin coverage")
		return
	}
	p := m.doc.Stations[m.station].Parts[r.part]
	c := m.doc.Coverage
	if c.Line(p, r.line) != doc.CovRun {
		m.setStatus(false, "no test runs line %d", r.line+1)
		return
	}
	list := append([]int(nil), c.TestsAt(p, r.line)...)
	sort.SliceStable(list, func(a, b int) bool { return c.Tests[list[a]].Label() < c.Tests[list[b]].Label() })
	m.tests = testPicker{list: list, picked: map[int]bool{}, file: p.Spec.File, line: r.line + 1,
		needle: strings.TrimSpace(p.Lines[r.line])}
	m.testsOpen = true
}

func (m *Model) updateTests(msg tea.KeyMsg) tea.Cmd {
	tp := &m.tests
	if len(tp.list) == 0 {
		m.testsOpen = false
		return nil
	}
	switch msg.String() {
	case "j", "down":
		tp.cur = min(tp.cur+1, len(tp.list)-1)
	case "k", "up":
		tp.cur = max(tp.cur-1, 0)
	case "enter":
		m.testsOpen = false
		m.spotlight(tp.list[tp.cur])
	case "p":
		file, line, err := m.testSource(tp.list[tp.cur])
		if err != nil {
			m.setStatus(true, "%v", err)
			return nil
		}
		m.peekFile(file, line-1)
	case " ":
		ti := tp.list[tp.cur]
		if tp.picked[ti] {
			delete(tp.picked, ti)
		} else {
			tp.picked[ti] = true
		}
		tp.cur = min(tp.cur+1, len(tp.list)-1)
	case "a":
		all := len(tp.picked) < len(tp.list)
		tp.picked = map[int]bool{}
		for _, ti := range tp.list {
			if all {
				tp.picked[ti] = true
			}
		}
	case "r":
		return m.runTests()
	case "esc", "q", "t":
		m.testsOpen = false
	}
	return nil
}

// testsLines is the picker as overlay entries: a title, then the tests in view, and which is selected.
func (m *Model) testsLines() ([]string, int) {
	tp := m.tests
	c := m.doc.Coverage
	title := fmt.Sprintf(" %d tests run %s:%d · enter spotlight · p source · space pick · a all · r agent runs · esc",
		len(tp.list), path.Base(tp.file), tp.line)
	if len(tp.list) == 1 {
		title = strings.Replace(title, "1 tests run", "1 test runs", 1)
	}
	room := max(m.bodyHeight()-3, 1)
	first := max(min(tp.cur-room/2, len(tp.list)-room), 0)
	out := []string{title}
	for i := first; i < len(tp.list) && i < first+room; i++ {
		t := c.Tests[tp.list[i]]
		box := "☐ "
		if tp.picked[tp.list[i]] {
			box = "☑ "
		}
		where := t.Group
		if t.File != "" && t.Line > 0 {
			where = fmt.Sprintf("%s:%d", t.File, t.Line)
		}
		label := t.Label()
		if t.New {
			label += "  (new)"
		}
		out = append(out, fmt.Sprintf(" %s%s   %s", box, label, where))
	}
	return out, tp.cur - first + 1
}

// testSource finds where test ti is defined: from the coverage when it says, otherwise by looking
// in its file, or by searching the reviewed side for a definition of its name.
func (m *Model) testSource(ti int) (string, int, error) {
	t := m.doc.Coverage.Tests[ti]
	if t.File != "" && t.Line > 0 {
		return t.File, t.Line, nil
	}
	top := coverage.TopName(t.Name)
	if top == "" {
		return "", 0, fmt.Errorf("%s: no name to look for", t.Label())
	}
	files, err := source.Open(m.doc.Review)
	if err != nil {
		return "", 0, err
	}
	defer files.Close()
	find := func(file string) (int, bool) {
		f, err := files.Current(file)
		if err != nil {
			return 0, false
		}
		l := coverage.DefLine(text.Lines(text.Sanitize(f.Content)), t.Name)
		return l + 1, l >= 0
	}
	if t.File != "" {
		if l, ok := find(t.File); ok {
			return t.File, l, nil
		}
	}
	ms, err := files.Grep(coverage.DefPattern(top))
	if err != nil {
		return "", 0, err
	}
	seen := map[string]bool{}
	var cands []string
	for _, mt := range ms {
		if !seen[mt.File] {
			seen[mt.File] = true
			cands = append(cands, mt.File)
		}
	}
	// The test's own package or file first, then anything that looks like a test file.
	score := func(f string) int {
		s := 0
		if t.Group != "" && (strings.HasSuffix(t.Group, path.Dir(f)) || strings.HasSuffix(f, t.Group)) {
			s += 2
		}
		if strings.Contains(path.Base(f), "test") || strings.Contains(f, "test/") || strings.Contains(f, "spec") {
			s++
		}
		return s
	}
	sort.SliceStable(cands, func(a, b int) bool { return score(cands[a]) > score(cands[b]) })
	for _, f := range cands {
		if l, ok := find(f); ok {
			return f, l, nil
		}
	}
	return "", 0, errors.New("could not find where " + t.Label() + " is defined")
}

// runTests asks the agent to run the picked tests, or the selected one when none is picked. margin
// never runs tests itself.
func (m *Model) runTests() tea.Cmd {
	tp := m.tests
	var ids []int
	for _, ti := range tp.list {
		if tp.picked[ti] {
			ids = append(ids, ti)
		}
	}
	if len(ids) == 0 {
		ids = []int{tp.list[tp.cur]}
	}
	if m.opts.AgentPane == "" {
		m.setStatus(false, "no agent pane; run the tests yourself, or open margin beside an agent")
		return nil
	}
	c := m.doc.Coverage
	names := make([]string, len(ids))
	for i, ti := range ids {
		names[i] = testRef(c.Tests[ti])
	}
	m.testsOpen = false
	id := fmt.Sprintf("run %d tests", len(ids))
	if len(ids) == 1 {
		id = "run " + c.Tests[ids[0]].Label()
	}
	pane, submit, msg := m.opts.AgentPane, m.opts.Submit, RunTestsMessage(names, tp.file, tp.line, tp.needle)
	m.setStatus(false, "sending %s…", id)
	return func() tea.Msg { return sentMsg{id: id, err: tmuxx.Send(pane, msg, submit)} }
}

func testRef(t doc.CovTest) string {
	s := t.Label()
	if t.Group != "" {
		s = t.Group + " " + s
	}
	return s
}

// RunTestsMessage is the one line the agent receives when the reader wants tests run.
func RunTestsMessage(tests []string, file string, line int, needle string) string {
	return fmt.Sprintf("[margin run] Run these tests, which run %s:%d (`%s`), and report whether they pass and what each one checks about that line; only run them, change nothing: %s",
		file, line, doc.Short(needle, 60), strings.Join(tests, " | "))
}
