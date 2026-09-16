package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/redrick/margin/internal/control"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/render"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/source"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/text"
	"github.com/redrick/margin/internal/tmuxx"
	"github.com/redrick/margin/internal/watch"
)

type Options struct {
	ReviewPath string
	AgentPane  string
	Submit     bool
}

type ChangedMsg struct{}

type CtlMsg struct {
	Req   control.Request
	Reply chan control.Response
}

type loadedMsg struct {
	doc     *doc.Doc
	state   *state.State
	err     error
	replies []chan control.Response
}

type sentMsg struct {
	id  string
	err error
}

type Model struct {
	opts    Options
	watcher *watch.Watcher
	doc     *doc.Doc
	state   *state.State
	loadErr error

	status    string
	statusErr bool

	width, height int
	station       int
	rows          []row
	cur, top      int
	hoff          int
	note          int
	ghosts        bool
	notes         bool
	whole         map[[2]int]bool

	listOpen bool
	listCur  int
	asking   bool
	input    textinput.Model

	paint *render.Painter
	spans map[string][][]render.Span

	loaded      bool
	loading     bool
	reloadAgain bool
	pending     []chan control.Response
	waiting     []CtlMsg

	seen  map[string]bool
	fresh map[string]bool

	filter       int
	unfold       bool
	helpOpen     bool
	lastInput    time.Time
	sessionSecs  int
	sessionLines int
	readSession  map[string]bool
}

func New(opts Options) *Model {
	in := textinput.New()
	in.Prompt = " ask › "
	in.CharLimit = 4000
	return &Model{
		opts:   opts,
		ghosts: true,
		notes:  true,
		note:   -1,
		whole:  map[[2]int]bool{},
		fresh:  map[string]bool{},
		paint:  render.NewPainter(),
		spans:  map[string][][]render.Span{},
		input:  in,

		lastInput:   time.Now(),
		readSession: map[string]bool{},
	}
}

func (m *Model) SetWatcher(w *watch.Watcher) { m.watcher = w }

// Handler turns control requests into messages for the running program and waits for the reply.
func Handler(p *tea.Program) func(control.Request) control.Response {
	return func(req control.Request) control.Response {
		reply := make(chan control.Response, 1)
		go p.Send(CtlMsg{Req: req, Reply: reply})
		select {
		case r := <-reply:
			return r
		case <-time.After(15 * time.Second):
			return control.Fail(errors.New("viewer did not respond"))
		}
	}
}

func Load(path string) (*doc.Doc, *state.State, error) {
	r, err := review.Load(path)
	if err != nil {
		return nil, nil, err
	}
	files, err := source.Open(r)
	if err != nil {
		return nil, nil, err
	}
	defer files.Close()
	st, err := state.Load(review.StatePath(r.Path))
	if err != nil {
		return nil, nil, err
	}
	return doc.Build(r, files), st, nil
}

func (m *Model) Init() tea.Cmd {
	m.loading = true
	return tea.Batch(m.load(nil), tick())
}

func (m *Model) load(replies []chan control.Response) tea.Cmd {
	path := m.opts.ReviewPath
	return func() tea.Msg {
		d, st, err := Load(path)
		return loadedMsg{doc: d, state: st, err: err, replies: replies}
	}
}

func (m *Model) reload(reply chan control.Response) tea.Cmd {
	if m.loading {
		m.reloadAgain = true
		if reply != nil {
			m.pending = append(m.pending, reply)
		}
		return nil
	}
	m.loading = true
	var replies []chan control.Response
	if reply != nil {
		replies = append(replies, reply)
	}
	return m.load(replies)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = max(msg.Width-12, 10)
		m.rebuild()
		m.follow()
	case loadedMsg:
		return m, m.applyLoad(msg)
	case ChangedMsg:
		return m, m.reload(nil)
	case CtlMsg:
		return m, m.control(msg)
	case sentMsg:
		if msg.err != nil {
			m.setStatus(true, "%s saved, not sent: %v", msg.id, msg.err)
		} else {
			m.setStatus(false, "%s sent to the agent pane", msg.id)
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.move(-3)
			case tea.MouseButtonWheelDown:
				m.move(3)
			}
		}
	case tickMsg:
		if time.Since(m.lastInput) < 5*time.Minute {
			m.sessionSecs += int(tickEvery / time.Second)
		}
		return m, tick()
	case tea.KeyMsg:
		m.lastInput = time.Now()
		switch {
		case m.asking:
			return m, m.updateAsk(msg)
		case m.listOpen:
			m.updateList(msg)
			return m, nil
		case m.helpOpen:
			m.helpOpen = false
			return m, nil
		}
		return m, m.key(msg)
	}
	return m, nil
}

func (m *Model) applyLoad(msg loadedMsg) tea.Cmd {
	m.loading = false
	if msg.err != nil {
		m.loadErr = msg.err
		m.setStatus(true, "reload failed: %v", msg.err)
	} else {
		stationID, file, line := m.position()
		m.doc, m.loadErr = msg.doc, nil
		if m.state == nil {
			m.state = msg.state
		}
		m.spans = map[string][][]render.Span{}
		if m.watcher != nil {
			m.watcher.Set(m.doc.WatchPaths())
		}
		added, stations := m.trackNotes(m.doc)
		switch {
		case !m.loaded:
			m.loaded = true
			m.setStation(min(1, len(m.doc.Stations)-1))
		case added > 0:
			m.restore(stationID, file, line)
			m.setStatus(false, "%s", freshStatus(added, stations))
		default:
			m.restore(stationID, file, line)
			m.setStatus(false, "reloaded")
		}
	}
	for _, r := range msg.replies {
		if msg.err != nil {
			r <- control.Fail(msg.err)
		} else {
			r <- control.OK(m.where())
		}
	}
	waiting := m.waiting
	m.waiting = nil
	for _, w := range waiting {
		m.control(w)
	}
	if m.reloadAgain {
		m.reloadAgain = false
		m.loading = true
		replies := m.pending
		m.pending = nil
		return m.load(replies)
	}
	return nil
}

func (m *Model) position() (station, file string, line int) {
	if m.doc == nil || m.station >= len(m.doc.Stations) {
		return "", "", -1
	}
	st := m.doc.Stations[m.station]
	if r, ok := m.cursorRow(); ok {
		return st.ID, st.Parts[r.part].Spec.File, r.line
	}
	return st.ID, "", -1
}

func (m *Model) restore(stationID, file string, line int) {
	note := m.note
	i, _ := m.doc.Station(stationID)
	if i < 0 {
		i = min(m.station, len(m.doc.Stations)-1)
	}
	top := m.top
	m.station = i
	m.rebuild()
	best := -1
	for idx, r := range m.rows {
		if r.kind != rowCode || m.doc.Stations[i].Parts[r.part].Spec.File != file {
			continue
		}
		if best < 0 || abs(r.line-line) < abs(m.rows[best].line-line) {
			best = idx
		}
	}
	if best >= 0 {
		m.cur = best
	}
	m.top = top
	m.clamp()
	if n := len(m.doc.Stations[i].Notes); note >= n {
		note = n - 1
	}
	m.note = note
	m.follow()
}

func (m *Model) key(msg tea.KeyMsg) tea.Cmd {
	h := m.bodyHeight()
	switch msg.String() {
	case "q", "ctrl+c":
		return tea.Quit
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "ctrl+d", "pgdown":
		m.move(h / 2)
	case "ctrl+u", "pgup":
		m.move(-h / 2)
	case "g", "home":
		m.move(-len(m.rows))
	case "G", "end":
		m.move(len(m.rows))
	case "h", "left":
		m.hoff = max(m.hoff-4, 0)
	case "l", "right":
		m.hoff += 4
	case "0":
		m.hoff = 0
	case "}":
		m.setStation(m.station + 1)
	case "{":
		m.setStation(m.station - 1)
	case "]":
		m.nextNote(1)
	case "[":
		m.nextNote(-1)
	case "N":
		m.nextFresh()
	case "enter":
		m.activate()
	case "z":
		m.unfold = !m.unfold
		m.rebuildKeep()
	case "F":
		m.cycleFilter()
	case "x":
		m.dismiss()
	case "v":
		m.reveal()
	case "H":
		m.helpOpen = true
	case "tab":
		m.listOpen, m.listCur = true, m.station
	case "d":
		m.ghosts = !m.ghosts
		m.rebuildKeep()
	case "n":
		m.notes = !m.notes
		m.rebuildKeep()
	case "f":
		if r, ok := m.cursorRow(); ok {
			k := [2]int{m.station, r.part}
			m.whole[k] = !m.whole[k]
			m.rebuildKeep()
		}
	case " ":
		m.mark(false)
	case "?":
		m.mark(true)
	case "a":
		return m.startAsk()
	case "r":
		return m.reload(nil)
	}
	return nil
}

func (m *Model) updateList(msg tea.KeyMsg) {
	if m.doc == nil {
		m.listOpen = false
		return
	}
	switch msg.String() {
	case "j", "down":
		m.listCur = min(m.listCur+1, len(m.doc.Stations)-1)
	case "k", "up":
		m.listCur = max(m.listCur-1, 0)
	case "enter", " ":
		m.listOpen = false
		m.setStation(m.listCur)
	case "esc", "tab", "q":
		m.listOpen = false
	}
}

func (m *Model) updateAsk(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		m.asking = false
		m.input.Blur()
		return nil
	case tea.KeyEnter:
		q := text.Line(m.input.Value())
		m.asking = false
		m.input.Blur()
		if q == "" {
			return nil
		}
		return m.ask(q)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

func (m *Model) setStatus(isErr bool, format string, args ...any) {
	m.status, m.statusErr = fmt.Sprintf(format, args...), isErr
}

func (m *Model) mark(flag bool) {
	n := m.selectedNote()
	if n == nil {
		m.setStatus(false, "no note selected")
		return
	}
	s := m.state
	switch {
	case flag && s.Flagged[n.Key]:
		delete(s.Flagged, n.Key)
	case flag:
		s.Flagged[n.Key] = true
	case s.Reviewed[n.Key]:
		delete(s.Reviewed, n.Key)
	default:
		s.Reviewed[n.Key] = true
		if n.Changed != "" {
			s.Seen[n.Key] = n.Changed
		}
	}
	if err := s.Save(); err != nil {
		m.setStatus(true, "saving marks: %v", err)
		return
	}
	if !flag && s.Reviewed[n.Key] {
		m.nextNote(1)
	}
}

func (m *Model) startAsk() tea.Cmd {
	r, ok := m.cursorRow()
	if !ok {
		m.setStatus(false, "move the cursor onto a code line to ask about it")
		return nil
	}
	p := m.doc.Stations[m.station].Parts[r.part]
	m.asking = true
	m.input.SetValue("")
	m.input.Placeholder = fmt.Sprintf("question about %s:%d, enter sends, esc cancels", p.Spec.File, r.line+1)
	return m.input.Focus()
}

func (m *Model) ask(question string) tea.Cmd {
	st := m.doc.Stations[m.station]
	r, ok := m.cursorRow()
	if !ok {
		return nil
	}
	p := st.Parts[r.part]
	line := r.line
	needle := strings.TrimSpace(p.Lines[line])
	for needle == "" && line > p.Range.Start {
		line--
		needle = strings.TrimSpace(p.Lines[line])
	}
	q := state.Question{
		ID:      m.state.NextQuestionID(),
		Station: st.ID,
		File:    p.Spec.File,
		Side:    p.Spec.Side,
		Line:    r.line + 1,
		Needle:  needle,
		Text:    question,
		Asked:   time.Now().UTC().Truncate(time.Second),
	}
	m.state.Questions = append(m.state.Questions, q)
	if err := m.state.Save(); err != nil {
		m.setStatus(true, "saving question: %v", err)
		return nil
	}
	if m.opts.AgentPane == "" {
		m.setStatus(false, "%s saved; no agent pane, ask your agent to run `margin questions`", q.ID)
		return nil
	}
	pane, submit, msg := m.opts.AgentPane, m.opts.Submit, AgentMessage(q)
	m.setStatus(false, "%s saved, sending…", q.ID)
	return func() tea.Msg { return sentMsg{id: q.ID, err: tmuxx.Send(pane, msg, submit)} }
}

func AgentMessage(q state.Question) string {
	return fmt.Sprintf("[margin %s] %s:%d in station %s, line `%s`: %s (answer here, then record it with: margin answer %s \"<answer>\")",
		q.ID, q.File, q.Line, q.Station, doc.Short(q.Needle, 80), q.Text, q.ID)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
