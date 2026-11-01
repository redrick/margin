package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/redrick/margin/internal/anchor"
	"github.com/redrick/margin/internal/coverage"
	"github.com/redrick/margin/internal/diffmap"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/render"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/source"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/target"
	"github.com/redrick/margin/internal/tui"
	"github.com/redrick/margin/internal/watch"
)

type Reviews struct {
	app    *application.App
	agents *Agents

	mu      sync.Mutex
	stateMu sync.Mutex
	path    string
	watcher *watch.Watcher
	ctl     ctlServer
}

type Opened struct {
	Path    string `json:"path"`
	Repo    string `json:"repo"`
	Label   string `json:"label"`
	Created bool   `json:"created"`
	Added   int    `json:"added"`
	Notes   bool   `json:"notes"`
	// Shared is set when another viewer already serves this review's control socket, so the agent's
	// margin goto and where reach that viewer instead of this window.
	Shared bool `json:"shared"`
}

type View struct {
	Path         string        `json:"path"`
	Title        string        `json:"title"`
	Kicker       string        `json:"kicker"`
	Asked        string        `json:"asked"`
	AskedFrom    string        `json:"askedFrom"`
	Did          string        `json:"did"`
	Gap          string        `json:"gap"`
	Summary      string        `json:"summary"`
	Motivation   string        `json:"motivation"`
	Outcome      string        `json:"outcome"`
	Complexity   string        `json:"complexity"`
	Flow         string        `json:"flow"`
	BaseDesc     string        `json:"baseDesc"`
	HeadDesc     string        `json:"headDesc"`
	Stats        string        `json:"stats"`
	Stations     []StationView `json:"stations"`
	Problems     []string      `json:"problems"`
	Unshown      []string      `json:"unshown"`
	Reader       ReaderView    `json:"reader"`
	Coverage     *CoverageView `json:"coverage,omitempty"`
	Ignored      []string      `json:"ignored,omitempty"`
	Guidance     string        `json:"guidance,omitempty"`
	Repo         string        `json:"repo"`
	Instructions string        `json:"instructions,omitempty"`
	Renames      []string      `json:"renames,omitempty"`
	Skip         []string      `json:"skip,omitempty"`
	Counts       tui.Counts    `json:"counts"`
	HasBase      bool          `json:"hasBase"`
}

type CoverageView struct {
	Tests []TestView `json:"tests"`
	// Grid holds, per test, the changed lines it runs in each code stop: [stop index] -> run, total.
	Grid  [][][2]int `json:"grid"`
	Stale []string   `json:"stale,omitempty"`
}

type TestView struct {
	Label     string `json:"label"`
	Display   string `json:"display"`
	Depth     int    `json:"depth"`
	Synthetic bool   `json:"synthetic,omitempty"`
	Group     string `json:"group,omitempty"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	New       bool   `json:"new,omitempty"`
}

type StopCovView struct {
	Run      int  `json:"run"`
	Missed   int  `json:"missed"`
	Measured bool `json:"measured"`
	Stale    bool `json:"stale"`
	Current  bool `json:"current"`
}

type TestGroupView struct {
	File    string     `json:"file"`
	Why     string     `json:"why,omitempty"`
	Err     string     `json:"err,omitempty"`
	NewFile bool       `json:"newFile,omitempty"`
	Names   []TestName `json:"names"`
}

type TestName struct {
	Name  string `json:"name"`
	Depth int    `json:"depth"`
	New   bool   `json:"new,omitempty"`
}

type StationView struct {
	ID        string           `json:"id"`
	Kind      string           `json:"kind"`
	Title     string           `json:"title"`
	Lede      string           `json:"lede,omitempty"`
	Scope     string           `json:"scope,omitempty"`
	What      string           `json:"what,omitempty"`
	Why       string           `json:"why,omitempty"`
	Risk      string           `json:"risk,omitempty"`
	RiskWhy   string           `json:"riskWhy,omitempty"`
	Concern   string           `json:"concern,omitempty"`
	OrderWhy  string           `json:"orderWhy,omitempty"`
	Checks    []string         `json:"checks,omitempty"`
	Examples  []review.Example `json:"examples,omitempty"`
	Auto      bool             `json:"auto,omitempty"`
	Added     int              `json:"added"`
	Changed   int              `json:"changed"`
	Removed   int              `json:"removed"`
	Issues    int              `json:"issues"`
	Questions int              `json:"questions"`
	Decisions int              `json:"decisions"`
	Parts     []PartView       `json:"parts"`
	Notes     []NoteView       `json:"notes"`
	Steps     []tui.WalkStep   `json:"steps,omitempty"`
	ChecksAll []string         `json:"checksAll,omitempty"`
	OwnChecks bool             `json:"ownChecks,omitempty"`
	LOC       int              `json:"loc"`
	Cov       *StopCovView     `json:"cov,omitempty"`
	NeedsTest bool             `json:"needsTests,omitempty"`
	TestNames []string         `json:"testNames,omitempty"`
	Tests     []TestGroupView  `json:"tests,omitempty"`
}

type PartView struct {
	File       string     `json:"file"`
	Label      string     `json:"label"`
	About      string     `json:"about,omitempty"`
	Check      string     `json:"check,omitempty"`
	Background bool       `json:"background,omitempty"`
	Base       bool       `json:"base,omitempty"`
	NewFile    bool       `json:"newFile,omitempty"`
	Hunk       int        `json:"hunk,omitempty"`
	HunkCount  int        `json:"hunkCount,omitempty"`
	Err        string     `json:"err,omitempty"`
	Rows       []Row      `json:"rows"`
	Start      int        `json:"start"`
	End        int        `json:"end"`
	Len        int        `json:"len"`
	Folds      []tui.Fold `json:"folds,omitempty"`
	BaseFile   string     `json:"baseFile,omitempty"`
	Plus       int        `json:"plus"`
	Minus      int        `json:"minus"`
	Diffed     bool       `json:"diffed"`
	MovedN     int        `json:"movedN,omitempty"`
}

type Row struct {
	N      int      `json:"n"`
	Kind   string   `json:"k,omitempty"`
	Spans  []Span   `json:"s"`
	Ghosts []string `json:"g,omitempty"`
	Was    string   `json:"was,omitempty"`
	Moved  string   `json:"mv,omitempty"`
	Notes  []int    `json:"notes,omitempty"`
	// GM says, per removed line before this one, where it moved to, when it only moved.
	GM []string `json:"gm,omitempty"`
	// HL is the changed range of a changed line, as rune offsets: old start, old end, new start, new end.
	HL     *[4]int `json:"hl,omitempty"`
	Cov    string  `json:"cov,omitempty"`
	TestsN int     `json:"tn,omitempty"`
}

type Span struct {
	Text   string `json:"t"`
	Color  string `json:"c,omitempty"`
	Bold   bool   `json:"b,omitempty"`
	Italic bool   `json:"i,omitempty"`
}

type NoteView struct {
	Key        string `json:"key"`
	Kind       string `json:"kind"`
	Text       string `json:"text"`
	Evidence   string `json:"evidence,omitempty"`
	Q          string `json:"q,omitempty"`
	QID        string `json:"qid,omitempty"`
	Focus      string `json:"focus,omitempty"`
	Confidence string `json:"confidence,omitempty"`
	Unbacked   bool   `json:"unbacked,omitempty"`
	Part       int    `json:"part"`
	Line       int    `json:"line"`
	At         string `json:"at"`
	Problem    string `json:"problem,omitempty"`
	Changed    string `json:"changed,omitempty"`
}

var stationKinds = map[doc.StationKind]string{doc.Overview: "overview", doc.Code: "code", doc.Recap: "recap", doc.Tests: "tests"}

var rowKinds = map[diffmap.Kind]string{diffmap.Added: "add", diffmap.Changed: "chg", diffmap.Removed: "del"}

// Open creates or resumes the review of arg in dir: "" for uncommitted changes, "--staged" for the
// index, "--pr N" for a pull request, else a branch or commit, the same as `margin [arg]`.
func (r *Reviews) Open(dir, arg string, fresh bool) (*Opened, error) {
	opts := target.Options{}
	switch {
	case arg == "--staged":
		arg, opts.Staged = "", true
	case strings.HasPrefix(arg, "--pr "):
		arg, opts.PR = "", strings.TrimPrefix(arg, "--pr ")
	}
	t, err := target.Resolve(dir, arg, opts)
	if err != nil {
		return nil, err
	}
	loc := target.Locate(t)
	created, added, err := target.Ensure(loc, t, fresh)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.path = loc.Path
	r.mu.Unlock()
	openedReview(loc.Path)
	return &Opened{Path: loc.Path, Repo: t.Repo, Label: t.Label(), Created: created, Added: added, Notes: loc.Notes, Shared: r.serve(loc.Path)}, nil
}

// OpenFile opens a review file directly, like `margin open`.
func (r *Reviews) OpenFile(path string) (*Opened, error) {
	rv, err := review.Load(path)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.path = rv.Path
	r.mu.Unlock()
	openedReview(rv.Path)
	loc := target.LocationOf(rv.Path)
	return &Opened{Path: rv.Path, Repo: rv.RepoDir(), Label: rv.Title, Notes: loc.Notes, Shared: r.serve(rv.Path)}, nil
}

// PickFile asks for a review file to open.
func (r *Reviews) PickFile() (string, error) {
	return r.app.Dialog.OpenFile().CanChooseFiles(true).SetTitle("Open a margin review").
		AddFilter("margin reviews", "*.review.yaml").PromptForSingleSelection()
}

// Current builds the view of the open review and watches the files it shows for changes.
func (r *Reviews) Current() (*View, error) {
	path := r.current()
	if path == "" {
		return nil, errors.New("no review open")
	}
	d, err := r.build(path)
	if err != nil {
		r.watch([]string{path})
		return nil, err
	}
	statePath := review.StatePath(path)
	st, err := state.Load(statePath)
	if err != nil {
		return nil, err
	}
	r.watch(append([]string{path, statePath, review.CommentsPath(path)}, d.WatchPaths()...))
	v := viewOf(d)
	v.Reader = readerOf(d, st)
	v.Counts = tui.CountsOf(d, st)
	return v, nil
}

func (r *Reviews) current() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.path
}

func (r *Reviews) build(path string) (*doc.Doc, error) {
	rv, err := review.Load(path)
	if err != nil {
		return nil, err
	}
	files, err := source.Open(rv)
	if err != nil {
		return nil, err
	}
	defer files.Close()
	d := doc.Build(rv, files)
	set, err := coverage.Load(review.CoveragePath(rv.Path))
	if err != nil {
		d.Problems = append(d.Problems, doc.Problem{Station: "tests", Msg: err.Error()})
	}
	d.AttachCoverage(set)
	return d, nil
}

func (r *Reviews) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.path = ""
	if r.watcher != nil {
		r.watcher.Set(nil)
	}
}

func (r *Reviews) watch(paths []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.watcher == nil {
		w, err := watch.New(func() { r.app.Event.Emit("review:changed") })
		if err != nil {
			return
		}
		r.watcher = w
	}
	r.watcher.Set(paths)
}

func (r *Reviews) ServiceShutdown() error {
	if r.ctl.srv != nil {
		r.ctl.srv.Close()
	}
	if r.watcher != nil {
		r.watcher.Close()
	}
	return nil
}

func viewOf(d *doc.Doc) *View {
	rv := d.Review
	v := &View{Repo: d.Repo, Guidance: d.Guidance, Instructions: rv.Instructions, HasBase: d.HasBase, Path: rv.Path, Title: rv.Title, Kicker: rv.Kicker, Asked: rv.Asked, AskedFrom: rv.AskedFrom,
		Did: rv.Did, Gap: rv.Gap, Summary: rv.Summary, Motivation: rv.Motivation, Outcome: rv.Outcome,
		Complexity: rv.Complexity, Flow: rv.Flow, BaseDesc: d.BaseDesc, HeadDesc: d.HeadDesc, Stats: doc.Summary(d)}
	for _, p := range d.Problems {
		v.Problems = append(v.Problems, fmt.Sprintf("%s: %s", p.Station, p.Msg))
	}
	for _, u := range d.Unshown {
		v.Unshown = append(v.Unshown, u.Path+" ("+u.Reason+")")
	}
	for _, c := range d.Ignored {
		v.Ignored = append(v.Ignored, fmt.Sprintf("%c %s", c.Status, c.Path))
	}
	for _, rn := range rv.Renames {
		v.Renames = append(v.Renames, rn.Before+"  →  "+rn.After)
	}
	for _, sk := range rv.Skip {
		line := sk.What
		if sk.Why != "" {
			line += " — " + sk.Why
		}
		v.Skip = append(v.Skip, line)
	}
	hl := map[string][][]render.Span{}
	for _, st := range d.Stations {
		v.Stations = append(v.Stations, stationOf(d, st, hl))
	}
	if c := d.Coverage; c != nil {
		cv := &CoverageView{}
		for f := range c.Stale {
			cv.Stale = append(cv.Stale, f)
		}
		for ti, t := range c.Tests {
			cv.Tests = append(cv.Tests, TestView{Label: t.Label(), Display: t.Display, Depth: t.Depth, Synthetic: t.Synthetic, Group: t.Group, File: t.File, Line: t.Line, New: t.New})
			row := make([][2]int, len(d.Stations))
			for si, st := range d.Stations {
				if st.Kind == doc.Code {
					sc := st.Coverage(c, ti)
					row[si] = [2]int{sc.Run, st.Coverage(c, -1).Total()}
				}
			}
			cv.Grid = append(cv.Grid, row)
		}
		v.Coverage = cv
	}
	return v
}

func stationOf(d *doc.Doc, st *doc.Station, hl map[string][][]render.Span) StationView {
	sv := StationView{ID: st.ID, Kind: stationKinds[st.Kind], Title: st.Title, Lede: st.Lede, Scope: st.Scope,
		What: st.What, Why: st.Why, Risk: st.Risk, RiskWhy: st.RiskWhy, Concern: st.Concern, OrderWhy: st.OrderWhy,
		Checks: st.Checks, Examples: st.Examples, Auto: st.Auto, Parts: []PartView{}, Notes: []NoteView{}}
	sv.Added, sv.Changed, sv.Removed = doc.Counts(st.Parts)
	sv.Issues, sv.Questions = st.Findings()
	sv.Decisions = st.Decisions()
	byLine := st.NotesByLine()
	for _, p := range st.Parts {
		sv.Parts = append(sv.Parts, partOf(d, p, p.Range, 0, byLine, hl))
	}
	sv.LOC, sv.TestNames = st.LOC(), st.TestNames
	if st.Kind == doc.Code {
		sv.Steps = tui.Walk(st, func(pi int) anchor.Range { return st.Parts[pi].Range })
		sv.ChecksAll, sv.OwnChecks = tui.Checks(st)
		sv.NeedsTest = st.NeedsTests() && st.Coverage(d.Coverage, -1).Run == 0
		if d.Coverage != nil {
			sc := st.Coverage(d.Coverage, -1)
			sv.Cov = &StopCovView{Run: sc.Run, Missed: sc.Missed, Measured: sc.Measured, Stale: sc.Stale, Current: sc.Current}
		}
	}
	for _, g := range st.Tests {
		gv := TestGroupView{File: g.File, Why: g.Why, NewFile: g.NewFile, Names: []TestName{}}
		if g.Err != nil {
			gv.Err = g.Err.Error()
		}
		for _, n := range g.Names {
			gv.Names = append(gv.Names, TestName{Name: n.Name, Depth: n.Depth, New: g.NewFile || g.New[n.Name]})
		}
		sv.Tests = append(sv.Tests, gv)
	}
	for _, n := range st.Notes {
		sv.Notes = append(sv.Notes, NoteView{Key: n.Key, Kind: n.Level(), Text: n.Text, Evidence: n.Evidence, Q: n.Q, QID: n.QID, Focus: n.Focus,
			Confidence: n.Confidence, Unbacked: n.Unbacked(), Part: n.Part, Line: n.Line, At: n.At, Problem: n.Problem, Changed: n.Changed})
	}
	return sv
}

func partOf(d *doc.Doc, p *doc.Part, rng anchor.Range, extra int, byLine map[string][]int, hl map[string][][]render.Span) PartView {
	pv := PartView{File: p.Spec.File, Label: p.Spec.Label(), About: p.Spec.About, Check: p.Spec.Check,
		Background: p.Spec.Background, Base: p.Base, NewFile: p.NewFile, Hunk: p.Hunk, HunkCount: p.HunkCount, Rows: []Row{},
		Start: rng.Start, End: rng.End, Len: len(p.Lines), BaseFile: p.Spec.BaseFile, MovedN: p.MovedCount()}
	if p.Err != nil {
		pv.Err = p.Err.Error()
		return pv
	}
	if p.Kinds != nil && !p.NewFile && !p.Base {
		a, c, rm := doc.Counts([]*doc.Part{p})
		pv.Plus, pv.Minus, pv.Diffed = a+c, rm, true
	}
	pv.Folds = tui.Folds(p, rng, extra, func(l int) bool { return len(byLine[doc.LineKey(p, l)]) > 0 })
	key := fmt.Sprintf("%v|%s", p.Base, p.Spec.File)
	spans, ok := hl[key]
	if !ok {
		spans = render.Highlight(p.Spec.File, expandTabs(p.Lines))
		hl[key] = spans
	}
	for i := rng.Start; i <= rng.End && i < len(p.Lines); i++ {
		row := Row{N: i + 1, Ghosts: p.Ghosts[i], Was: p.Pairs[i], Moved: p.Moved[i], Notes: byLine[doc.LineKey(p, i)], GM: ghostMoved(p, i)}
		if was, ok := p.Pairs[i]; ok {
			tab := strings.Repeat(" ", render.TabWidth)
			if a, b, c, e, ok := render.ChangedRange(strings.ReplaceAll(was, "\t", tab), strings.ReplaceAll(p.Lines[i], "\t", tab)); ok {
				row.HL = &[4]int{a, b, c, e}
			}
		}
		if i < len(p.Kinds) {
			row.Kind = rowKinds[p.Kinds[i]]
		}
		switch d.Coverage.Line(p, i) {
		case doc.CovRun:
			row.Cov, row.TestsN = "run", len(d.Coverage.TestsAt(p, i))
		case doc.CovMissed:
			row.Cov = "missed"
		case doc.CovIdle:
			row.Cov = "idle"
		}
		for _, s := range spans[i] {
			row.Spans = append(row.Spans, Span{Text: s.Text, Color: s.Color, Bold: s.Bold, Italic: s.Italic})
		}
		pv.Rows = append(pv.Rows, row)
	}
	if end := rng.End + 1; end == len(p.Lines) {
		if g := p.Ghosts[end]; len(g) > 0 {
			pv.Rows = append(pv.Rows, Row{N: 0, Ghosts: g, GM: ghostMoved(p, end)})
		}
	}
	return pv
}

func ghostMoved(p *doc.Part, line int) []string {
	gm := p.GhostMoved[line]
	if len(gm) == 0 {
		return nil
	}
	out := make([]string, len(p.Ghosts[line]))
	for g := range out {
		out[g] = gm[g]
	}
	return out
}

func expandTabs(lines []string) []string {
	out := make([]string, len(lines))
	tab := strings.Repeat(" ", render.TabWidth)
	for i, l := range lines {
		out[i] = strings.ReplaceAll(l, "\t", tab)
	}
	return out
}
