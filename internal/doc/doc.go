package doc

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/redrick/margin/internal/anchor"
	"github.com/redrick/margin/internal/diffmap"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/source"
	"github.com/redrick/margin/internal/text"
)

type StationKind uint8

const (
	Overview StationKind = iota
	Code
	Recap
	Tests
)

const (
	KindIssue    = "issue"
	KindQuestion = "question"
	KindOK       = "ok"
	KindInfo     = "info"
	KindNit      = "nit"
)

type Doc struct {
	Review   *review.Review
	Repo     string
	HasBase  bool
	BaseDesc string
	HeadDesc string
	Stations []*Station
	Problems []Problem
}

type Problem struct {
	Station string
	Msg     string
}

type Station struct {
	Kind      StationKind
	ID        string
	Title     string
	Lede      string
	Risk      string
	Concern   string
	TestNames []string
	Parts     []*Part
	Notes     []*Note
	Tests     []TestGroup
}

type Part struct {
	Spec    review.Part
	Base    bool
	Lines   []string
	Range   anchor.Range
	Kinds   []diffmap.Kind
	Ghosts  map[int][]string
	Pairs   map[int]string
	NewFile bool
	Err     error

	Hunk      int
	HunkCount int
}

type Note struct {
	review.Note
	Station string
	Key     string
	Part    int
	Line    int
	Problem string
}

type Hit struct{ Part, Line int }

func Build(r *review.Review, files *source.Files) *Doc {
	d := &Doc{Review: r, Repo: files.Repo, HasBase: files.HasBase(), BaseDesc: files.BaseDesc, HeadDesc: files.HeadDesc}
	d.Stations = append(d.Stations, &Station{Kind: Overview, ID: "overview", Title: r.Title})
	b := builder{files: files, cache: map[string]*Part{}}
	findings := false
	for _, s := range r.Stations {
		st := &Station{Kind: Code, ID: s.ID, Title: s.Title, Lede: s.Lede, Risk: s.Risk, Concern: s.Concern, TestNames: s.Tests}
		for _, ps := range s.Parts {
			for _, p := range b.parts(ps) {
				if p.Err != nil {
					d.problem(st.ID, "%s (%s): %v", ps.File, ps.Label(), p.Err)
				}
				st.Parts = append(st.Parts, p)
			}
		}
		for _, n := range s.Notes {
			note := st.resolve(n)
			if note.Problem != "" {
				d.problem(st.ID, "note %q: %s", n.At, note.Problem)
			}
			st.Notes = append(st.Notes, note)
		}
		if issues, questions := st.Findings(); issues+questions > 0 {
			findings = true
		}
		d.Stations = append(d.Stations, st)
	}
	if findings {
		d.Stations = append(d.Stations, &Station{Kind: Recap, ID: "recap", Title: "Problems and open questions"})
	}
	if len(r.Tests) > 0 {
		st := &Station{Kind: Tests, ID: "tests", Title: "Tests"}
		for _, t := range r.Tests {
			g := TestGroup{File: t.File, Why: t.Why}
			f, err := files.Current(t.File)
			switch {
			case err != nil:
				g.Err = err
			case !f.Exists:
				g.Err = fmt.Errorf("%s does not exist", t.File)
			default:
				g.Names = ParseTests(t.File, text.Lines(f.Content))
				g.NewFile, g.New = newTests(files, t.File, g.Names)
			}
			if g.Err != nil {
				d.problem("tests", "%v", g.Err)
			}
			st.Tests = append(st.Tests, g)
		}
		d.Stations = append(d.Stations, st)
	}
	return d
}

func newTests(files *source.Files, file string, names []TestName) (bool, map[string]bool) {
	if !files.HasBase() {
		return false, nil
	}
	base, err := files.Base(file)
	if err != nil {
		return false, nil
	}
	if !base.Exists {
		return true, nil
	}
	old := map[string]bool{}
	for _, n := range ParseTests(file, text.Lines(base.Content)) {
		old[n.Name] = true
	}
	fresh := map[string]bool{}
	for _, n := range names {
		if !old[n.Name] {
			fresh[n.Name] = true
		}
	}
	return false, fresh
}

func (d *Doc) problem(station, format string, args ...any) {
	d.Problems = append(d.Problems, Problem{Station: station, Msg: fmt.Sprintf(format, args...)})
}

func (d *Doc) Station(id string) (int, *Station) {
	for i, s := range d.Stations {
		if s.ID == id {
			return i, s
		}
	}
	return -1, nil
}

// WatchPaths lists every file whose change should reload the review.
func (d *Doc) WatchPaths() []string {
	paths := []string{d.Review.Path}
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	for _, s := range d.Review.Stations {
		for _, p := range s.Parts {
			add(filepath.Join(d.Repo, filepath.FromSlash(p.File)))
		}
	}
	for _, t := range d.Review.Tests {
		add(filepath.Join(d.Repo, filepath.FromSlash(t.File)))
	}
	return paths
}

type builder struct {
	files *source.Files
	cache map[string]*Part
}

func (b builder) part(spec review.Part) *Part {
	p := &Part{Spec: spec, Base: spec.Side == review.SideBase}
	key := fmt.Sprintf("%v|%s|%s", p.Base, spec.File, spec.BaseFile)
	if c, ok := b.cache[key]; ok {
		p.Lines, p.Kinds, p.Ghosts, p.Pairs, p.NewFile, p.Err = c.Lines, c.Kinds, c.Ghosts, c.Pairs, c.NewFile, c.Err
	} else {
		b.load(p)
		b.cache[key] = p
	}
	if p.Err == nil {
		p.Range, p.Err = anchor.Resolve(spec.File, p.Lines, spec)
	}
	return p
}

func (b builder) load(p *Part) {
	file := p.Spec.File
	cur, err := b.files.Current(file)
	if err != nil {
		p.Err = err
		return
	}
	basePath := file
	if p.Spec.BaseFile != "" {
		basePath = p.Spec.BaseFile
	}
	var base source.File
	if b.files.HasBase() || p.Base {
		if base, err = b.files.Base(basePath); err != nil {
			p.Err = err
			return
		}
	}
	if p.Base {
		if !base.Exists {
			p.Err = fmt.Errorf("does not exist on the base side")
			return
		}
		p.Lines = text.Lines(base.Content)
		m := diffmap.Compute(text.Lines(cur.Content), p.Lines)
		p.Kinds = m.Kinds
		for i, k := range p.Kinds {
			if k != diffmap.Same {
				p.Kinds[i] = diffmap.Removed
			}
		}
		return
	}
	if !cur.Exists {
		p.Err = fmt.Errorf("does not exist")
		return
	}
	p.Lines = text.Lines(cur.Content)
	if !b.files.HasBase() {
		return
	}
	var m diffmap.Map
	if base.Exists {
		m = diffmap.Compute(text.Lines(base.Content), p.Lines)
	} else {
		m = diffmap.AllAdded(len(p.Lines))
		p.NewFile = true
	}
	p.Kinds, p.Ghosts, p.Pairs = m.Kinds, m.Ghosts, m.Pairs
}

// Hits finds needle across the station's parts, each file line counted once.
func (st *Station) Hits(file, needle string) []Hit {
	var hits []Hit
	seen := map[string]bool{}
	for pi, p := range st.Parts {
		if p.Err != nil || (file != "" && p.Spec.File != file) {
			continue
		}
		for _, l := range anchor.Find(p.Lines, p.Range, needle) {
			k := fmt.Sprintf("%v|%s|%d", p.Base, p.Spec.File, l)
			if !seen[k] {
				seen[k] = true
				hits = append(hits, Hit{pi, l})
			}
		}
	}
	return hits
}

func (st *Station) resolve(n review.Note) *Note {
	out := &Note{Note: n, Station: st.ID, Key: n.Key(st.ID), Part: -1, Line: -1}
	hits := st.Hits(n.File, n.At)
	var h *Hit
	switch {
	case n.Nth > 0 && n.Nth <= len(hits):
		h = &hits[n.Nth-1]
	case n.Nth > 0:
		out.Problem = fmt.Sprintf("lost anchor (nth %d of %d matches)", n.Nth, len(hits))
	case len(hits) == 1:
		h = &hits[0]
	case len(hits) == 0:
		out.Problem = "lost anchor"
	default:
		out.Problem = fmt.Sprintf("ambiguous anchor (%d matches); add file or nth", len(hits))
	}
	if h != nil {
		out.Part, out.Line = h.Part, h.Line
	}
	return out
}

// Level is the note's kind; a note without one counts as context, or as an issue when it starts with ⚠.
func (n *Note) Level() string {
	if n.Kind != "" {
		return n.Kind
	}
	if strings.HasPrefix(strings.TrimSpace(n.Text), "⚠") {
		return KindIssue
	}
	return KindInfo
}

// Unbacked reports an issue without evidence, which the reader should treat as a question.
func (n *Note) Unbacked() bool {
	return n.Level() == KindIssue && strings.TrimSpace(n.Evidence) == ""
}

// Findings counts backed issues, and questions including unbacked issues.
func (st *Station) Findings() (issues, questions int) {
	for _, n := range st.Notes {
		switch {
		case n.Unbacked():
			questions++
		case n.Level() == KindIssue:
			issues++
		case n.Level() == KindQuestion:
			questions++
		}
	}
	return issues, questions
}

// LOC counts the distinct lines the station shows.
func (st *Station) LOC() int {
	seen := map[string]bool{}
	for _, p := range st.Parts {
		if p.Err != nil {
			continue
		}
		for i := p.Range.Start; i <= p.Range.End; i++ {
			seen[LineKey(p, i)] = true
		}
	}
	return len(seen)
}

var untestedConcerns = map[string]bool{"tests": true, "test": true, "config": true, "docs": true, "build": true}

// NeedsTests reports a station that changes non-test code but names no tests covering it.
func (st *Station) NeedsTests() bool {
	if len(st.TestNames) > 0 || untestedConcerns[strings.ToLower(st.Concern)] {
		return false
	}
	for _, p := range st.Parts {
		if p.Err != nil || p.Base || isTestFile(p.Spec.File) {
			continue
		}
		for i := p.Range.Start; i <= p.Range.End && i < len(p.Kinds); i++ {
			if p.Kinds[i] != diffmap.Same {
				return true
			}
		}
	}
	return false
}

func isTestFile(path string) bool {
	base := filepath.Base(path)
	return strings.Contains(base, "_test.") || strings.HasPrefix(base, "test_") || strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.") || strings.Contains("/"+path, "/test/") || strings.Contains("/"+path, "/tests/")
}

func (st *Station) NoteAt(part, line int) []int {
	var idx []int
	for i, n := range st.Notes {
		if n.Part == part && n.Line == line {
			idx = append(idx, i)
		}
	}
	return idx
}

// NotesByLine maps file+side+line to note indices, so overlapping parts share markers.
func (st *Station) NotesByLine() map[string][]int {
	m := map[string][]int{}
	for i, n := range st.Notes {
		if n.Part < 0 {
			continue
		}
		p := st.Parts[n.Part]
		k := LineKey(p, n.Line)
		m[k] = append(m[k], i)
	}
	return m
}

func LineKey(p *Part, line int) string {
	return fmt.Sprintf("%v|%s|%d", p.Base, p.Spec.File, line)
}

func (d *Doc) CodeNotes() []*Note {
	var all []*Note
	for _, s := range d.Stations {
		all = append(all, s.Notes...)
	}
	return all
}

func (d *Doc) AnsweredQuestions() map[string]bool {
	m := map[string]bool{}
	for _, n := range d.CodeNotes() {
		if n.QID != "" {
			m[n.QID] = true
		}
	}
	return m
}

func Counts(parts []*Part) (added, changed, removed int) {
	for _, p := range parts {
		if p.Err != nil {
			continue
		}
		for i := p.Range.Start; i <= p.Range.End && i < len(p.Kinds); i++ {
			switch p.Kinds[i] {
			case diffmap.Added:
				added++
			case diffmap.Changed:
				changed++
			case diffmap.Removed:
				removed++
			}
		}
		for i := p.Range.Start; i <= p.Range.End; i++ {
			removed += len(p.Ghosts[i])
		}
	}
	return
}

func Summary(d *Doc) string {
	parts, notes := 0, 0
	for _, s := range d.Stations {
		parts += len(s.Parts)
		notes += len(s.Notes)
	}
	return fmt.Sprintf("%d stations · %d parts · %d notes", len(d.Review.Stations), parts, notes)
}

func Short(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
