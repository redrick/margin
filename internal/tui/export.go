package tui

import (
	"github.com/redrick/margin/internal/anchor"
	"github.com/redrick/margin/internal/doc"
)

// This file lets other front ends, such as the desktop app, lay out the guided walk exactly as the
// terminal viewer does.

// WalkStep is one step of the guided walk through a stop.
type WalkStep struct {
	Kind  string     `json:"kind"`
	Part  int        `json:"part"`
	From  int        `json:"from"`
	To    int        `json:"to"`
	N     int        `json:"n"`
	Of    int        `json:"of"`
	Title string     `json:"title,omitempty"`
	Subs  []WalkStep `json:"subs,omitempty"`
}

var stepKinds = map[stepKind]string{stepBrief: "brief", stepBackground: "background", stepChange: "change", stepMech: "mech", stepWrap: "wrap"}

// Walk lays a stop out as the guided walk reads it; rangeOf gives the lines each part shows.
func Walk(st *doc.Station, rangeOf func(pi int) anchor.Range) []WalkStep {
	steps := stepsFor(st, rangeOf)
	out := make([]WalkStep, len(steps))
	for i, s := range steps {
		out[i] = walkStep(st, s)
	}
	return out
}

func walkStep(st *doc.Station, s step) WalkStep {
	w := WalkStep{Kind: stepKinds[s.kind], Part: s.part, From: s.from, To: s.to, N: s.n, Of: s.of}
	if (s.kind == stepChange || s.kind == stepBackground) && s.part >= 0 {
		w.Title = st.Parts[s.part].Spec.File + stepTitle(st.Parts[s.part], s)
	}
	for _, sub := range s.subs {
		w.Subs = append(w.Subs, walkStep(st, sub))
	}
	return w
}

// Checks is what the reader should be able to answer after a stop; own reports whether the agent
// wrote them or they are general ones for the stop's kind of change.
func Checks(st *doc.Station) (checks []string, own bool) { return checksFor(st) }

// TestSource finds where test ti of the review's coverage is defined.
func TestSource(d *doc.Doc, ti int) (string, int, error) { return testSourceIn(d, ti) }

// ExpandStep is how many lines of context one widening adds around a part.
const ExpandStep = expandStep

// Fold is a stretch of unchanged lines the viewer folds away; Label says what it hides when that is
// more than plain unchanged code.
type Fold struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Label string `json:"label,omitempty"`
}

// Folds lists what the viewer folds in a range of a part, with extra more lines of context kept
// around each change; noted reports lines that carry a note.
func Folds(p *doc.Part, rng anchor.Range, extra int, noted func(line int) bool) []Fold {
	var out []Fold
	for _, sg := range foldPlan(p, rng, foldContext+extra, noted) {
		if sg.fold {
			out = append(out, Fold{Start: sg.start, End: sg.end, Label: foldLabel(p, sg)})
		}
	}
	return out
}
