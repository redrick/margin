package doc

import (
	"reflect"
	"testing"

	"github.com/redrick/margin/internal/anchor"
	"github.com/redrick/margin/internal/diffmap"
)

func part(n int, changed []int, ghosts map[int][]string) *Part {
	p := &Part{Lines: make([]string, n), Kinds: make([]diffmap.Kind, n), Ghosts: ghosts}
	for _, i := range changed {
		p.Kinds[i] = diffmap.Changed
	}
	return p
}

func rng(start, end int) anchor.Range { return anchor.Range{Start: start, End: end} }

func TestHunkRanges(t *testing.T) {
	tests := []struct {
		name string
		part *Part
		want []anchor.Range
	}{
		{"far apart", part(30, []int{2, 15}, nil), []anchor.Range{rng(0, 5), rng(12, 18)}},
		{"close ones merge", part(20, []int{2, 8}, nil), []anchor.Range{rng(0, 11)}},
		{"deletion at end of file", part(20, []int{12}, map[int][]string{20: {"gone"}}), []anchor.Range{rng(9, 19)}},
		{"no changes", part(10, nil, nil), nil},
		{"file emptied", part(0, nil, map[int][]string{0: {"a"}}), []anchor.Range{rng(0, -1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HunkRanges(tt.part, 3); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
