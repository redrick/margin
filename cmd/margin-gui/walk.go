package main

import (
	"errors"
	"fmt"

	"github.com/redrick/margin/internal/anchor"
	"github.com/redrick/margin/internal/around"
	"github.com/redrick/margin/internal/cli"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/render"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/source"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/text"
	"github.com/redrick/margin/internal/tui"
	"github.com/redrick/margin/internal/verdict"
)

type LineInput struct {
	Station string `json:"station"`
	Part    int    `json:"part"`
	Line    int    `json:"line"`
	Text    string `json:"text"`
}

// ReadNotes marks the notes a walk step showed as read, the way space does in the terminal viewer.
func (r *Reviews) ReadNotes(keys []string) (*ReaderUpdate, error) {
	return r.change(func(d *doc.Doc, st *state.State) error {
		want := map[string]bool{}
		for _, k := range keys {
			want[k] = true
		}
		for _, n := range d.CodeNotes() {
			if !want[n.Key] || n.Level() == doc.KindDecide {
				continue
			}
			st.Reviewed[n.Key] = true
			if n.Changed != "" {
				st.Seen[n.Key] = n.Changed
			}
		}
		return nil
	})
}

// ExpandStep is how many lines one widening adds.
func (r *Reviews) ExpandStep() int { return tui.ExpandStep }

// Visit records that the reader opened a stop.
func (r *Reviews) Visit(station string) (*ReaderUpdate, error) {
	return r.change(func(_ *doc.Doc, st *state.State) error {
		st.Visited[station] = true
		return nil
	})
}

// SetFull switches between the guided walk and whole stops, remembered for the review.
func (r *Reviews) SetFull(full bool) (*ReaderUpdate, error) {
	return r.change(func(_ *doc.Doc, st *state.State) error {
		st.Full = full
		return nil
	})
}

// SetBlind hides the agent's notes on high-risk stops until the reader revealed them.
func (r *Reviews) SetBlind(blind bool) (*ReaderUpdate, error) {
	return r.change(func(_ *doc.Doc, st *state.State) error {
		st.Blind = blind
		return nil
	})
}

// SetNoteVisibility saves the GUI's note-card preference for the open review.
func (r *Reviews) SetNoteVisibility(path, mode string) (*ReaderUpdate, error) {
	if !validNoteVisibility(mode) {
		return nil, errors.New("unknown note visibility")
	}
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	d, err := r.document(0)
	if err != nil {
		return nil, err
	}
	if d.Review.Path != path {
		return nil, errors.New("the open review changed")
	}
	st, err := state.Load(review.StatePath(path))
	if err != nil {
		return nil, err
	}
	if err := updateReviewResume(path, func(saved *reviewResume) bool {
		if saved.NoteVisibility == mode {
			return false
		}
		saved.NoteVisibility = mode
		return true
	}); err != nil {
		return nil, err
	}
	update := r.readerUpdate(d, st)
	return &update, nil
}

func (r *Reviews) Reveal(path, station string) (*ReaderUpdate, error) {
	return r.change(func(d *doc.Doc, st *state.State) error {
		if d.Review.Path != path {
			return errors.New("the open review changed")
		}
		if _, stop := d.Station(station); stop == nil || stop.Kind != doc.Code {
			return errors.New("this stop is no longer in the review")
		}
		st.Revealed[station] = true
		return nil
	})
}

// Lines returns a part with extra lines of context around it (a multiple of ExpandStep), or the
// whole file, the way + - and f do in the terminal viewer.
func (r *Reviews) Lines(station string, part, extra int, whole bool, version int) (*PartView, error) {
	d, err := r.document(version)
	if err != nil {
		return nil, err
	}
	_, st := d.Station(station)
	if st == nil || part < 0 || part >= len(st.Parts) || st.Parts[part].Err != nil {
		return nil, errors.New("that part is no longer in the review")
	}
	p := st.Parts[part]
	rng := anchor.Range{Start: max(p.Range.Start-extra, 0), End: min(p.Range.End+extra, len(p.Lines)-1)}
	if whole {
		rng = anchor.Range{Start: 0, End: len(p.Lines) - 1}
	}
	pv := partOf(d, p, rng, extra, st.NotesByLine(), map[string][][]render.Span{})
	return &pv, nil
}

type AroundView struct {
	Symbols []SymbolView    `json:"symbols"`
	History []around.Commit `json:"history"`
	Err     string          `json:"err,omitempty"`
}

type SymbolView struct {
	Name    string         `json:"name"`
	File    string         `json:"file"`
	Line    int            `json:"line"`
	Removed bool           `json:"removed"`
	Type    bool           `json:"type"`
	Inside  int            `json:"inside"`
	TooMany int            `json:"tooMany"`
	Uses    []source.Match `json:"uses"`
}

// Around finds who outside the tour uses what a stop changes, and which commits wrote the code it replaces.
func (r *Reviews) Around(station string) (*AroundView, error) {
	d, err := r.document(0)
	if err != nil {
		return nil, err
	}
	_, st := d.Station(station)
	if st == nil || st.Kind != doc.Code {
		return &AroundView{}, nil
	}
	files, err := source.Open(d.Review)
	if err != nil {
		return nil, err
	}
	defer files.Close()
	rep := around.Study(d, st, files)
	out := &AroundView{History: rep.History, Symbols: []SymbolView{}}
	if rep.Err != nil {
		out.Err = rep.Err.Error()
	}
	for _, s := range rep.Symbols {
		around.SortUses(s.Uses)
		uses := s.Uses
		if uses == nil {
			uses = []source.Match{}
		}
		out.Symbols = append(out.Symbols, SymbolView{Name: s.Name, File: s.File, Line: s.Line, Removed: s.Removed, Type: s.Type,
			Inside: s.Inside, TooMany: s.TooMany, Uses: uses})
	}
	return out, nil
}

type PeekView struct {
	Title string `json:"title"`
	At    int    `json:"at"`
	First int    `json:"first"`
	Rows  []Row  `json:"rows"`
	Text  string `json:"text,omitempty"`
}

// Peek shows the code around a 0-based line of a file on the reviewed side.
func (r *Reviews) Peek(file string, line int) (*PeekView, error) {
	rv, err := review.Load(r.current())
	if err != nil {
		return nil, err
	}
	return peek(rv, file, line)
}

func peek(rv *review.Review, file string, line int) (*PeekView, error) {
	files, err := source.Open(rv)
	if err != nil {
		return nil, err
	}
	defer files.Close()
	f, err := files.Current(file)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}
	lines := text.Lines(text.Sanitize(f.Content))
	if line < 0 || line >= len(lines) {
		return nil, fmt.Errorf("%s has no line %d", file, line+1)
	}
	spans := render.Highlight(file, expandTabs(lines))
	from, to := max(line-40, 0), min(line+40, len(lines)-1)
	pv := &PeekView{Title: fmt.Sprintf("%s:%d", file, line+1), At: line, First: from}
	for i := from; i <= to; i++ {
		row := Row{N: i + 1}
		for _, s := range spans[i] {
			row.Spans = append(row.Spans, spanOf(s))
		}
		pv.Rows = append(pv.Rows, row)
	}
	return pv, nil
}

// Commit shows a commit's message.
func (r *Reviews) Commit(sha string) (*PeekView, error) {
	rv, err := review.Load(r.current())
	if err != nil {
		return nil, err
	}
	msg, err := around.Message(rv.RepoDir(), sha)
	if err != nil {
		return nil, err
	}
	return &PeekView{Title: "commit " + sha, Text: msg}, nil
}

// TestSource peeks at where a test of the coverage is defined.
func (r *Reviews) TestSource(ti, version int) (*PeekView, error) {
	d, err := r.document(version)
	if err != nil {
		return nil, err
	}
	if d.Coverage == nil || ti < 0 || ti >= len(d.Coverage.Tests) {
		return nil, errors.New("no such test")
	}
	file, line, err := tui.TestSource(d, ti)
	if err != nil {
		return nil, err
	}
	return peek(d.Review, file, line-1)
}

// TestsAt lists the tests that run a line, by index into the coverage's tests.
func (r *Reviews) TestsAt(station string, part, line, version int) ([]int, error) {
	d, err := r.document(version)
	if err != nil {
		return nil, err
	}
	_, st := d.Station(station)
	if st == nil || part < 0 || part >= len(st.Parts) || d.Coverage == nil {
		return []int{}, nil
	}
	return append([]int{}, d.Coverage.TestsAt(st.Parts[part], line)...), nil
}

// RunsLines lists, per part of every code stop, the lines a test runs, for the spotlight.
func (r *Reviews) RunsLines(ti, version int) (map[string][]int, error) {
	d, err := r.document(version)
	if err != nil {
		return nil, err
	}
	out := map[string][]int{}
	if d.Coverage == nil || ti < 0 || ti >= len(d.Coverage.Tests) {
		return out, nil
	}
	for _, st := range d.Stations {
		for pi, p := range st.Parts {
			if p.Err != nil {
				continue
			}
			key := fmt.Sprintf("%s|%d", st.ID, pi)
			out[key] = []int{}
			for i := range p.Lines {
				if d.Coverage.Runs(ti, p, i) {
					out[key] = append(out[key], i)
				}
			}
		}
	}
	return out, nil
}

// RunTests asks the agent to run tests that run a line; margin never runs tests itself.
func (r *Reviews) RunTests(tests []int, file string, line int, needle string, version int) error {
	d, err := r.document(version)
	if err != nil {
		return err
	}
	path := d.Review.Path
	if !r.agents.Running(path) {
		return errors.New("start the agent first")
	}
	if d.Coverage == nil {
		return errors.New("no coverage")
	}
	var names []string
	for _, ti := range tests {
		if ti >= 0 && ti < len(d.Coverage.Tests) {
			t := d.Coverage.Tests[ti]
			s := t.Label()
			if t.Group != "" {
				s = t.Group + " " + s
			}
			names = append(names, s)
		}
	}
	if len(names) == 0 {
		return errors.New("no tests picked")
	}
	return r.agents.Send(path, tui.RunTestsMessage(names, file, line, needle))
}

// Export renders the reader's comments, flagged notes and calls with `margin export`, to post by hand.
func (r *Reviews) Export(format string) (string, error) {
	r.mu.Lock()
	d, walk := r.loaded, r.walk
	r.mu.Unlock()
	if d == nil {
		return "", errors.New("open a review first")
	}
	st, err := state.Load(review.StatePath(d.Review.Path))
	if err != nil {
		return "", err
	}
	authored := d
	if walk.mode == fileWalk {
		authored = walk.alternate
	}
	return cli.FormatExport(format, authored, st, verdict.Recommend(d, st, walk.alternate))
}
