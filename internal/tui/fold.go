package tui

import (
	"github.com/redrick/margin/internal/anchor"
	"github.com/redrick/margin/internal/diffmap"
	"github.com/redrick/margin/internal/doc"
)

const foldContext = 3

type segment struct {
	start, end int
	fold       bool
}

// foldPlan splits rng into shown and folded segments. Unchanged stretches further than foldContext
// lines from any change, removed line or note are folded; parts with nothing changed stay whole.
func foldPlan(p *doc.Part, rng anchor.Range, noted func(line int) bool) []segment {
	n := rng.End - rng.Start + 1
	if n <= 0 {
		return nil
	}
	if p.Kinds == nil {
		return []segment{{rng.Start, rng.End, false}}
	}
	keep := make([]bool, n)
	found := false
	for i := rng.Start; i <= rng.End; i++ {
		changed := i < len(p.Kinds) && p.Kinds[i] != diffmap.Same
		if !changed && !noted(i) && len(p.Ghosts[i]) == 0 && len(p.Ghosts[i+1]) == 0 {
			continue
		}
		found = true
		for j := max(0, i-rng.Start-foldContext); j <= min(n-1, i-rng.Start+foldContext); j++ {
			keep[j] = true
		}
	}
	if !found {
		return []segment{{rng.Start, rng.End, false}}
	}
	var segs []segment
	for i := 0; i < n; {
		j := i
		for j+1 < n && keep[j+1] == keep[i] {
			j++
		}
		segs = append(segs, segment{rng.Start + i, rng.Start + j, !keep[i] && j-i+1 > 2})
		i = j + 1
	}
	return segs
}
