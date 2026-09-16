package tui

import (
	"reflect"
	"testing"

	"github.com/redrick/margin/internal/anchor"
	"github.com/redrick/margin/internal/diffmap"
	"github.com/redrick/margin/internal/doc"
)

func TestPlaceCards(t *testing.T) {
	if got := placeCards([]int{5, 6, 30}, []int{3, 2, 4}, 0, 0, 20); !reflect.DeepEqual(got, []int{5, 9, -1}) {
		t.Fatalf("cards should sit at their line, push down on overlap, and drop when off screen: %v", got)
	}
	if got := placeCards([]int{12}, []int{2}, 10, 1, 20); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("anchors are relative to the scroll position: %v", got)
	}
}

func TestFoldPlan(t *testing.T) {
	part := func(changed ...int) *doc.Part {
		p := &doc.Part{Lines: make([]string, 30), Kinds: make([]diffmap.Kind, 30), Ghosts: map[int][]string{}}
		for _, c := range changed {
			p.Kinds[c] = diffmap.Changed
		}
		return p
	}
	none := func(int) bool { return false }
	all := anchor.Range{Start: 0, End: 29}

	if got, want := foldPlan(part(15), all, none), []segment{{0, 11, true}, {12, 18, false}, {19, 29, true}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("one change: %v, want %v", got, want)
	}
	if got, want := foldPlan(part(15, 22), all, none), []segment{{0, 11, true}, {12, 25, false}, {26, 29, true}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("nearby changes share one shown block: %v, want %v", got, want)
	}
	if got, want := foldPlan(part(), all, func(i int) bool { return i == 3 }), []segment{{0, 6, false}, {7, 29, true}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("a note keeps its surroundings: %v, want %v", got, want)
	}
	if got := foldPlan(&doc.Part{Lines: make([]string, 30)}, all, none); len(got) != 1 || got[0].fold {
		t.Fatalf("code without a base is never folded: %v", got)
	}
}
