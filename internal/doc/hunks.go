package doc

import (
	"errors"

	"github.com/redrick/margin/internal/anchor"
	"github.com/redrick/margin/internal/diffmap"
	"github.com/redrick/margin/internal/review"
)

const hunkContext = 3

// parts expands a `hunks: true` part into one part per changed region.
func (b builder) parts(spec review.Part) []*Part {
	p := b.part(spec)
	if !spec.Hunks || p.Err != nil {
		return []*Part{p}
	}
	if p.Kinds == nil {
		p.Err = errors.New("hunks need a base to compare against")
		return []*Part{p}
	}
	ranges := HunkRanges(p, hunkContext)
	if len(ranges) == 0 {
		p.Err = errors.New("no line changes")
		return []*Part{p}
	}
	out := make([]*Part, len(ranges))
	for i, r := range ranges {
		c := *p
		c.Range, c.Hunk, c.HunkCount = r, i+1, len(ranges)
		out[i] = &c
	}
	return out
}

// HunkRanges returns changed regions with ctx lines of context, merging regions that nearly touch.
func HunkRanges(p *Part, ctx int) []anchor.Range {
	n := len(p.Lines)
	marked := make([]bool, n)
	changed := false
	for i, k := range p.Kinds {
		if k != diffmap.Same {
			marked[i], changed = true, true
		}
	}
	for i, g := range p.Ghosts {
		if len(g) == 0 {
			continue
		}
		changed = true
		if i < n {
			marked[i] = true
		}
		if i > 0 && i-1 < n {
			marked[i-1] = true
		}
	}
	var out []anchor.Range
	for i := 0; i < n; i++ {
		if !marked[i] {
			continue
		}
		start, end := max(i-ctx, 0), i
		for {
			next := -1
			for k := end + 1; k < n && k <= end+2*ctx+1; k++ {
				if marked[k] {
					next = k
					break
				}
			}
			if next < 0 {
				break
			}
			end = next
		}
		out = append(out, anchor.Range{Start: start, End: min(end+ctx, n-1)})
		i = end
	}
	if len(out) == 0 && changed {
		out = append(out, anchor.Range{Start: 0, End: n - 1})
	}
	return out
}
