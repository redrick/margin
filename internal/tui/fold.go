package tui

import (
	"fmt"

	"github.com/redrick/margin/internal/anchor"
	"github.com/redrick/margin/internal/diffmap"
	"github.com/redrick/margin/internal/doc"
)

const foldContext = 3

type segment struct {
	start, end int
	fold       bool
}

// foldLabel names what a folded segment hides: plain unchanged lines, or lines moved here unchanged.
func foldLabel(p *doc.Part, sg segment) string {
	moved, from := 0, ""
	for i := sg.start; i <= sg.end; i++ {
		if p.Moved[i] != "" {
			moved++
			if from == "" {
				from = p.Moved[i]
			} else if from != p.Moved[i] {
				from = "-"
			}
		}
	}
	n := sg.end - sg.start + 1
	switch {
	case moved == 0:
		return ""
	case moved == n && from != "-":
		return fmt.Sprintf("%d lines %s, unchanged", n, from)
	}
	return fmt.Sprintf("%d lines, unchanged or moved unchanged (%d moved)", n, moved)
}

// foldPlan splits rng into shown and folded segments. Unchanged stretches, and code moved here
// unchanged, further than foldContext lines from any change, removed line or note are folded;
// parts with nothing changed stay whole.
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
		changed := i < len(p.Kinds) && p.Kinds[i] != diffmap.Same && p.Moved[i] == ""
		if !changed && !noted(i) && !removedHere(p, i) && !removedHere(p, i+1) {
			continue
		}
		found = true
		for j := max(0, i-rng.Start-foldContext); j <= min(n-1, i-rng.Start+foldContext); j++ {
			keep[j] = true
		}
	}
	if !found {
		// Only moved code here: fold it all, the fold says where it came from.
		return []segment{{rng.Start, rng.End, n > 2 && foundAny(p, rng)}}
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

// removedHere reports removed lines before line that did not just move elsewhere.
func removedHere(p *doc.Part, line int) bool {
	for g := range p.Ghosts[line] {
		if p.GhostMoved[line][g] == "" {
			return true
		}
	}
	return false
}

// foundAny reports whether the range holds any change at all, moved or not.
func foundAny(p *doc.Part, rng anchor.Range) bool {
	for i := rng.Start; i <= rng.End; i++ {
		if (i < len(p.Kinds) && p.Kinds[i] != diffmap.Same) || len(p.Ghosts[i]) > 0 || len(p.Ghosts[i+1]) > 0 {
			return true
		}
	}
	return false
}
