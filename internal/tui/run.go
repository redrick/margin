package tui

import (
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/redrick/margin/internal/config"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/testrun"
	"github.com/redrick/margin/internal/tmuxx"
	"github.com/redrick/margin/internal/tryout"
)

// Running tests from the viewer with the command the settings give the repository, and trying a
// change to a line against them.

const maxOutput = 3000

type jobState struct {
	testrun.Job
	status string // wait, running, pass, fail, error, stopped
	code   int
}

type testOutput struct {
	id      int
	dir     string
	what    string
	ids     []int
	jobs    []jobState
	base    []jobState
	out     []string
	done    bool
	stopped bool

	// A tried change: the edits, results with them, and what became of the file.
	edits  []tryout.Edit
	try    *tryTarget
	before map[string]string
	after  map[string]string
	// implied marks results taken from a command's exit code because its output named no result.
	implied  map[string]bool
	applied  bool
	restored string

	top    int
	follow bool
	events chan tryout.Event
}

// tryTarget is the line, or block of lines, a change is tried on, to edit it again.
type tryTarget struct {
	station, file   string
	part, line, end int
	text            string
	ids             []int
	nocompare       bool
}

func (t *tryTarget) block() bool { return t.end > t.line }

// blockMark is where V started a block of lines.
type blockMark struct {
	station string
	part    int
	line    int
}

type editedMsg struct {
	tt   *tryTarget
	old  string
	text string
	err  error
}

type runEventsMsg struct {
	id  int
	evs []tryout.Event
}

type runDoneMsg struct{ id int }

type recoveredMsg struct {
	files []string
	err   error
}

func recoverTrials() tea.Msg {
	files, err := tryout.Recover()
	return recoveredMsg{files: files, err: err}
}

func jobStates(jobs []testrun.Job) []jobState {
	out := make([]jobState, len(jobs))
	for i, j := range jobs {
		out[i] = jobState{Job: j, status: "wait"}
	}
	return out
}

// runLine runs the tests of the cursor line, or of the stop when the line has none.
func (m *Model) runLine() tea.Cmd {
	st := m.doc.Stations[m.station]
	if st.Kind == doc.Tests {
		if m.cur >= 0 && m.cur < len(m.rows) && m.rows[m.cur].kind == rowTest && m.doc.Coverage != nil {
			r := m.rows[m.cur]
			return m.runTestIDs([]int{r.target}, testLabel(m.doc.Coverage.Tests[r.target]))
		}
		return m.runAll()
	}
	if st.Kind != doc.Code {
		m.setStatus(false, "go to a stop with code to run its tests")
		return nil
	}
	if m.doc.Coverage == nil {
		m.setStatus(false, "no coverage yet: ask the agent to run margin coverage")
		return nil
	}
	if r, ok := m.cursorRow(); ok {
		p := st.Parts[r.part]
		if ids := m.doc.Coverage.TestsAt(p, r.line); len(ids) > 0 {
			return m.runTestIDs(ids, fmt.Sprintf("%s:%d", path.Base(p.Spec.File), r.line+1))
		}
	}
	ids := m.stopTests(st)
	if len(ids) == 0 {
		m.setStatus(false, "no test runs this stop's changes")
		return nil
	}
	return m.runTestIDs(ids, "stop "+st.ID)
}

// stopTests lists the tests that run the stop's changed lines, or any of its lines when none do.
// runAll runs every test the coverage names.
func (m *Model) runAll() tea.Cmd {
	if m.doc.Coverage == nil {
		m.setStatus(false, "no coverage yet: ask the agent to run margin coverage")
		return nil
	}
	var ids []int
	for i, t := range m.doc.Coverage.Tests {
		if !t.Synthetic {
			ids = append(ids, i)
		}
	}
	return m.runTestIDs(ids, "every test")
}

func (m *Model) stopTests(st *doc.Station) []int {
	c := m.doc.Coverage
	collect := func(changed bool) []int {
		seen := map[int]bool{}
		var out []int
		for _, p := range st.Parts {
			if p.Err != nil || p.Base {
				continue
			}
			for i := range p.Lines {
				if changed && !p.Changed(i) {
					continue
				}
				for _, ti := range c.TestsAt(p, i) {
					if !seen[ti] {
						seen[ti] = true
						out = append(out, ti)
					}
				}
			}
		}
		return out
	}
	if out := collect(true); len(out) > 0 {
		return out
	}
	return collect(false)
}

func (m *Model) runTestIDs(ids []int, what string) tea.Cmd {
	jobs, err := tryout.Jobs(m.doc, ids)
	if errors.Is(err, tryout.ErrNoCommand) {
		return m.askCommand(func() tea.Cmd { return m.runTestIDs(ids, what) })
	}
	if err != nil {
		m.setStatus(true, "%v", err)
		return nil
	}
	return m.startRun(tryout.Plan{Dir: m.doc.Review.RepoDir(), Jobs: jobs}, &testOutput{what: what, ids: ids})
}

func (m *Model) runTry(tt *tryTarget, old string) tea.Cmd {
	jobs, err := tryout.Jobs(m.doc, tt.ids)
	if errors.Is(err, tryout.ErrNoCommand) {
		return m.askCommand(func() tea.Cmd { return m.runTry(tt, old) })
	}
	if err != nil {
		m.setStatus(true, "%v", err)
		return nil
	}
	dir := m.doc.Review.RepoDir()
	edits := []tryout.Edit{{File: tt.file, Line: tt.line + 1, Old: old, New: tt.text}}
	if err := tryout.Check(dir, edits); err != nil {
		m.setStatus(true, "%v", err)
		return nil
	}
	what := fmt.Sprintf("%s:%d", path.Base(tt.file), tt.line+1)
	if tt.block() {
		what += fmt.Sprintf("-%d", tt.end+1)
	}
	out := &testOutput{what: what, ids: tt.ids, edits: edits, try: tt,
		before: map[string]string{}, after: map[string]string{}, implied: map[string]bool{}}
	return m.startRun(tryout.Plan{Dir: dir, Jobs: jobs, Edits: edits, Compare: !tt.nocompare}, out)
}

// askCommand asks for the repository's test command, saves it, then does next.
func (m *Model) askCommand(next func() tea.Cmd) tea.Cmd {
	m.asking, m.commenting, m.cmdAsk, m.afterCmd = true, false, true, next
	m.input.Prompt = " test command › "
	m.input.Width = max(m.width-20, 10)
	dir := m.doc.Review.RepoDir()
	current, ok := config.Load().TestCommandFor(dir, tryout.MainWorktree(dir))
	if ok {
		m.input.SetValue(current.Command)
	} else {
		m.input.SetValue(tryout.Suggest(m.doc))
	}
	m.input.CursorEnd()
	m.input.Placeholder = "how this repository runs tests, like go test -count=1 -v {dir} -run {run}"
	what := "no test command for this repository yet"
	if ok {
		what = "the test command for this repository"
	}
	m.setStatus(false, "%s: {dir} {run} {pkg} {tests} {file} are filled in, shell-quoted; enter saves it to your settings, esc keeps it", what)
	return m.input.Focus()
}

func (m *Model) saveCommand(command string) tea.Cmd {
	next := m.afterCmd
	m.afterCmd = nil
	dir := m.doc.Review.RepoDir()
	repo := tryout.MainWorktree(dir)
	if repo == "" {
		repo = dir
	}
	if err := config.SetTestCommand(repo, command); err != nil {
		m.setStatus(true, "saving the test command: %v", err)
		return nil
	}
	if next == nil {
		return nil
	}
	return next()
}

func (m *Model) startRun(p tryout.Plan, out *testOutput) tea.Cmd {
	m.StopTests()
	m.runs++
	out.id, out.dir, out.follow = m.runs, p.Dir, true
	out.jobs = jobStates(p.Jobs)
	if p.Compare && len(p.Edits) > 0 {
		out.base = jobStates(p.Jobs)
	}
	out.events = make(chan tryout.Event, 1024)
	if out.edits == nil || out.base != nil {
		for _, j := range p.Jobs {
			for _, t := range j.Tests {
				m.results[t] = "run"
			}
		}
	}
	m.run, m.outOpen = out, true
	done := make(chan struct{})
	m.runDone = done
	ch := out.events
	go func() {
		defer close(done)
		tryout.Run(&m.runner, p, func(e tryout.Event) { ch <- e })
		close(ch)
	}()
	return waitRun(out.id, ch)
}

// waitRun hands the program the events that came, a batch at a time.
func waitRun(id int, ch chan tryout.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return runDoneMsg{id: id}
		}
		evs := []tryout.Event{e}
	more:
		for len(evs) < 400 {
			select {
			case e, ok := <-ch:
				if !ok {
					break more
				}
				evs = append(evs, e)
			default:
				break more
			}
		}
		return runEventsMsg{id: id, evs: evs}
	}
}

// StopTests stops a run and waits until it put back any line it changed.
func (m *Model) StopTests() {
	if m.run != nil && !m.run.done {
		m.run.stopped = true
	}
	m.runner.Stop()
	if m.runDone == nil {
		return
	}
	// Nothing may read the events while the program waits here, so drain them, or a full channel
	// would keep the run from ever ending.
	ch := m.run.events
	for m.runDone != nil {
		select {
		case <-m.runDone:
			m.runDone = nil
		case _, ok := <-ch:
			if !ok {
				ch = nil
			}
		}
	}
}

func (m *Model) gotRunEvents(msg runEventsMsg) tea.Cmd {
	o := m.run
	if o == nil || msg.id != o.id {
		return nil
	}
	for _, e := range msg.evs {
		base := e.Phase == tryout.PhaseBase
		jobs := o.jobs
		if base {
			jobs = o.base
		}
		into := m.results
		if o.edits != nil && !base {
			into = o.after
		}
		var j *jobState
		if e.Job >= 0 && e.Job < len(jobs) {
			j = &jobs[e.Job]
		}
		switch e.Kind {
		case "out":
			o.print(e.Text)
		case "result":
			into[e.Test] = e.Status
			if base {
				o.before[e.Test] = e.Status
			}
		case "start":
			if j != nil {
				j.status = "running"
				tag := ""
				if o.base != nil {
					tag = "   # with change"
					if base {
						tag = "   # unchanged"
					}
				}
				o.print("$ " + j.Command + tag)
			}
		case "end":
			if j == nil {
				break
			}
			j.code = e.Code
			switch {
			case e.Err != "" && o.stopped:
				j.status = "stopped"
			case e.Err != "":
				j.status = "error"
				o.print(e.Err)
			case e.Code == 0:
				j.status = "pass"
			default:
				j.status = "fail"
			}
			for _, t := range j.Tests {
				if s, ok := into[t]; !ok || s == "run" {
					into[t] = map[string]string{"pass": "pass", "fail": "fail"}[j.status]
					if o.edits != nil && !base {
						o.implied[t] = true
					}
				}
				if base {
					if _, ok := o.before[t]; !ok {
						o.before[t] = into[t]
					}
				}
			}
		case "applied":
			o.applied = e.Err == ""
			if e.Err != "" {
				o.print("✎ could not make the change: " + e.Err)
			} else {
				o.print("✎ changed " + tryout.Describe(o.edits))
			}
		case "restored":
			o.restored = "ok"
			if e.Err != "" {
				o.restored = e.Err
				o.print("✎ " + e.Err)
				m.setStatus(true, "%s", e.Err)
			} else {
				o.print("✎ put the line back")
			}
		}
	}
	return waitRun(o.id, o.events)
}

func (m *Model) gotRunDone(msg runDoneMsg) {
	o := m.run
	if o == nil || msg.id != o.id {
		return
	}
	o.done = true
	if m.runDone != nil {
		<-m.runDone
		m.runDone = nil
	}
	if !m.outOpen {
		m.setStatus(false, "tests: %s · o shows the output", o.summary())
	}
}

func (o *testOutput) print(s string) {
	o.out = append(o.out, s)
	if len(o.out) > maxOutput {
		drop := len(o.out) - maxOutput
		o.out = o.out[drop:]
		o.top = max(o.top-drop, 0)
	}
}

func (o *testOutput) verdict() tryout.Verdict {
	// A command that failed without naming a failed test, as one that does not build, caught
	// nothing: it is broken.
	after := map[string]string{}
	for t, s := range o.after {
		if !(o.implied[t] && s == "fail") {
			after[t] = s
		}
	}
	var broken []string
	for _, j := range o.jobs {
		if j.status != "fail" && j.status != "error" {
			continue
		}
		named := false
		for t, s := range after {
			for _, jt := range j.Tests {
				if s == "fail" && (t == jt || strings.HasPrefix(t, jt+"/")) {
					named = true
				}
			}
		}
		if !named {
			broken = append(broken, j.Label)
		}
	}
	return tryout.Judge(o.before, after, broken)
}

func (o *testOutput) summary() string {
	n := len(o.jobs)
	count := func(list []jobState, st ...string) int {
		k := 0
		for _, j := range list {
			for _, s := range st {
				if j.status == s {
					k++
				}
			}
		}
		return k
	}
	switch {
	case !o.done && o.base != nil && !o.applied:
		return fmt.Sprintf("unchanged %d/%d", min(count(o.base, "pass", "fail", "error", "stopped")+1, n), n)
	case !o.done:
		at := min(count(o.jobs, "pass", "fail", "error", "stopped")+1, n)
		if o.edits != nil {
			return fmt.Sprintf("with change %d/%d", at, n)
		}
		return fmt.Sprintf("running %d/%d", at, n)
	case o.stopped:
		return "stopped"
	case o.edits != nil && o.applied:
		return o.verdict().String()
	}
	if f := count(o.jobs, "fail", "error"); f > 0 {
		return fmt.Sprintf("%d/%d failed", f, n)
	}
	if n > 1 {
		return fmt.Sprintf("all %d passed", n)
	}
	return "passed"
}

var jobIcons = map[string]string{"wait": "·", "running": "◌", "pass": "✓", "fail": "✗", "error": "!", "stopped": "■", "skip": "↷", "run": "◌"}

// outputLines is the output overlay: a title, the commands and what they print.
func (m *Model) outputLines() []string {
	o := m.run
	title := " tests"
	if o.edits != nil {
		title = " try " + tryout.Describe(o.edits)
	} else if o.what != "" {
		title += " · " + o.what
	}
	title += " · " + o.summary()
	keys := " j k scroll · R again · C test command · esc closes"
	if !o.done {
		keys = " j k scroll · s stop · esc closes"
	} else if o.edits != nil && o.applied && !o.stopped {
		keys = " j k scroll · R again · i edit again · w keep the change · a tell the agent · C command · esc"
	}
	out := []string{title, keys}
	jobs := func(list []jobState, tag string) {
		for _, j := range list {
			out = append(out, fmt.Sprintf(" %s %s%s", jobIcons[j.status], tag, j.Label))
		}
	}
	if o.base != nil {
		jobs(o.base, "unchanged · ")
		jobs(o.jobs, "with change · ")
	} else {
		jobs(o.jobs, "")
	}
	if o.edits != nil && o.done && o.applied {
		v := o.verdict()
		add := func(label string, names []string) {
			if len(names) > 0 {
				out = append(out, " "+label+": "+strings.Join(names, ", "))
			}
		}
		add("✗ caught it", v.Caught)
		add("✓ still pass", v.Missed)
		add("! failed without it too", v.Already)
		add("! failed with no test named", v.Broken)
	}
	out = append(out, "")
	room := max(m.bodyHeight()-len(out)-1, 3)
	if o.follow {
		o.top = max(len(o.out)-room, 0)
	}
	o.top = max(min(o.top, len(o.out)-room), 0)
	for i := o.top; i < len(o.out) && i < o.top+room; i++ {
		out = append(out, " "+o.out[i])
	}
	return out
}

func (m *Model) updateOutput(msg tea.KeyMsg) tea.Cmd {
	o := m.run
	room := max(m.bodyHeight()-8, 3)
	scroll := func(d int) {
		o.top = max(min(o.top+d, len(o.out)-room), 0)
		o.follow = o.top >= len(o.out)-room
	}
	switch msg.String() {
	case "j", "down":
		scroll(1)
	case "k", "up":
		scroll(-1)
	case "ctrl+d", "pgdown":
		scroll(room / 2)
	case "ctrl+u", "pgup":
		scroll(-room / 2)
	case "G", "end":
		o.follow = true
	case "g", "home":
		o.top, o.follow = 0, false
	case "s":
		if !o.done {
			o.stopped = true
			m.runner.Stop()
		}
	case "R":
		if !o.done {
			return nil
		}
		if o.try != nil {
			return m.runTry(o.try, o.edits[0].Old)
		}
		return m.runTestIDs(o.ids, o.what)
	case "i":
		if o.try != nil && o.done {
			m.outOpen = false
			if m.gotoLine(o.try) {
				m.openEditor(o.try.part, o.try.line, o.try.end, o.try.text)
			}
		}
	case "w":
		if o.edits != nil && o.done {
			if err := tryout.Write(o.dir, o.edits); err != nil {
				m.setStatus(true, "%v", err)
			} else {
				m.setStatus(false, "change written to %s", o.edits[0].File)
			}
		}
	case "a":
		if o.edits == nil || !o.done {
			return nil
		}
		if m.opts.AgentPane == "" {
			m.setStatus(false, "no agent pane to tell")
			return nil
		}
		pane, submit, text := m.opts.AgentPane, m.opts.Submit, tryout.Message(o.edits, o.verdict())
		m.setStatus(false, "telling the agent…")
		return func() tea.Msg { return sentMsg{id: "tryout result", err: tmuxx.Send(pane, text, submit)} }
	case "C":
		if o.done {
			m.outOpen = false
			return m.askCommand(nil)
		}
	case "esc", "q", "o":
		m.outOpen = false
	}
	return nil
}

// gotoLine puts the cursor back on a tried line.
func (m *Model) gotoLine(tt *tryTarget) bool {
	i, _ := m.doc.Station(tt.station)
	if i < 0 {
		m.setStatus(false, "that stop is gone")
		return false
	}
	if i != m.station {
		m.setStation(i)
	}
	m.restore(tt.station, tt.file, tt.line)
	r, ok := m.cursorRow()
	return ok && r.part == tt.part && r.line == tt.line
}

func (m *Model) toggleOutput() {
	if m.run == nil {
		m.setStatus(false, "no test run yet: T runs the line's tests, i tries a change to it")
		return
	}
	m.outOpen = !m.outOpen
}

// testMark is the last run's word on a test, as an icon.
func (m *Model) testMark(name string) string {
	if s, ok := m.results[name]; ok && s != "" {
		return jobIcons[s] + " "
	}
	return ""
}

// toggleMark starts a block of lines at the cursor, or drops it.
func (m *Model) toggleMark() {
	if m.blockAt != nil {
		m.blockAt = nil
		m.setStatus(false, "")
		return
	}
	r, ok := m.cursorRow()
	if !ok || r.kind != rowCode {
		m.setStatus(false, "%s", m.needLine("start a block there"))
		return
	}
	if m.doc.Stations[m.station].Parts[r.part].Base {
		m.setStatus(false, "this part shows the code before the change; blocks are for the new side")
		return
	}
	m.blockAt = &blockMark{station: m.doc.Stations[m.station].ID, part: r.part, line: r.line}
	m.setStatus(false, "block from line %d: move to grow it, i edits it in place (E in $EDITOR), V or esc drops it", r.line+1)
}

// block is the marked lines up to the cursor, when the cursor is still in the marked part.
func (m *Model) block() (part, from, to int, ok bool) {
	if m.blockAt == nil || m.doc == nil || m.doc.Stations[m.station].ID != m.blockAt.station {
		return 0, 0, 0, false
	}
	r, ok := m.cursorRow()
	if !ok || r.kind != rowCode || r.part != m.blockAt.part {
		return 0, 0, 0, false
	}
	return r.part, min(r.line, m.blockAt.line), max(r.line, m.blockAt.line), true
}

func (m *Model) inBlock(part *doc.Part, line int) bool {
	pi, from, to, ok := m.block()
	return ok && m.doc.Stations[m.station].Parts[pi] == part && line >= from && line <= to
}

// tryExternal edits the marked block, or the cursor line, in $VISUAL or $EDITOR.
func (m *Model) tryExternal() tea.Cmd {
	if m.edit == nil {
		m.tryHere()
	}
	es := m.edit
	if es == nil {
		return nil
	}
	m.edit = nil
	es.tt.text = es.old
	return m.editBlock(es.tt, es.old)
}

// editBlock opens the block in $VISUAL or $EDITOR, in a file named like the source so the editor
// highlights it.
func (m *Model) editBlock(tt *tryTarget, old string) tea.Cmd {
	f, err := os.CreateTemp("", "margin-try-*"+path.Ext(tt.file))
	if err == nil {
		_, err = f.WriteString(tt.text + "\n")
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		m.setStatus(true, "%v", err)
		return nil
	}
	cmd := editorCmd(f.Name())
	editor := strings.Join(cmd.Args[:len(cmd.Args)-1], " ")
	name := f.Name()
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(name)
		if err != nil {
			return editedMsg{tt: tt, old: old, err: fmt.Errorf("%s: %w", editor, err)}
		}
		b, err := os.ReadFile(name)
		text := strings.TrimSuffix(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
		return editedMsg{tt: tt, old: old, text: text, err: err}
	})
}

func (m *Model) gotEdited(msg editedMsg) tea.Cmd {
	switch {
	case msg.err != nil:
		m.setStatus(true, "%v", msg.err)
		return nil
	case msg.text == msg.old:
		m.setStatus(false, "the block is unchanged; nothing to try")
		return nil
	}
	msg.tt.text = msg.text
	return m.runTry(msg.tt, msg.old)
}
