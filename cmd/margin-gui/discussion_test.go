package main

import (
	"testing"

	"github.com/redrick/margin/internal/control"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/state"
)

func TestDiscussionAnchorUsesTheStoredDiffSide(t *testing.T) {
	p := &doc.Part{Spec: review.Part{File: "code.go"}, Lines: []string{"wanted", "head"}}
	base := &doc.Part{Spec: p.Spec, Base: true, Lines: []string{"first", "wanted", ""}}
	d := &doc.Doc{Stations: []*doc.Station{{ID: "code", Parts: []*doc.Part{p, base}}}}
	q := state.Question{Station: "code", File: "code.go", Side: "base", Line: 1, Needle: "wanted"}
	if got := locateDiscussionLine(d, q); got.Part != 1 || got.At != 1 {
		t.Fatalf("moved base-side anchor: %+v", got)
	}
	q.Line = 3
	if got := locateDiscussionLine(d, q); got.Part != 1 || got.At != 2 {
		t.Fatalf("blank line under its anchor: %+v", got)
	}
	q.Line = 1
	base.Lines = append(base.Lines, "wanted")
	if got := locateDiscussionLine(d, q); got.Part != -1 || got.At != -1 {
		t.Fatalf("ambiguous anchor: %+v", got)
	}
}

func TestAutomaticReplyJumpLeavesGUIReadingPositionAlone(t *testing.T) {
	r := &Reviews{}
	if got := r.handle("review.yaml", control.Request{Cmd: "goto", Arg: "stop:1", Reply: true}); !got.OK {
		t.Fatalf("GUI should acknowledge an automatic reply jump without moving: %+v", got)
	}
	if got := r.handle("review.yaml", control.Request{Cmd: "goto", Arg: "stop:1"}); got.OK {
		t.Fatal("explicit goto should still try to navigate the open review")
	}
}
