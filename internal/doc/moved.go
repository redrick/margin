package doc

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/redrick/margin/internal/diffmap"
)

// A move is at least minMoved lines, minSubstantial of them more than brackets, so that common
// boilerplate such as an error check does not count as a move by coincidence.
const (
	minMoved       = 4
	minSubstantial = 3
	maxMovedLines  = 50000
)

// removed is a run of lines that left a file: removed lines drawn before line at of a current file,
// or a stretch of a file shown on its base side.
type removed struct {
	part  *Part
	lines []string
	at    int
	start int
	ghost bool
	used  []bool
}

type pos struct{ run, idx int }

// markMoved finds added lines that are removed lines put somewhere else unchanged, apart from
// indentation, and labels both ends so the reader can skim a move instead of reading it twice.
func (d *Doc) markMoved(b builder) {
	keys := make([]string, 0, len(b.cache))
	for k := range b.cache {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	current := map[string]bool{}
	for _, k := range keys {
		if c := b.cache[k]; c.Err == nil && !c.Base && c.Kinds != nil {
			current[c.Spec.File] = true
		}
	}

	var runs []*removed
	total := 0
	for _, k := range keys {
		c := b.cache[k]
		switch {
		case c.Err != nil:
		case c.Base && !current[c.Spec.File]:
			for i := 0; i < len(c.Kinds); {
				if c.Kinds[i] != diffmap.Removed {
					i++
					continue
				}
				j := i
				for j < len(c.Kinds) && c.Kinds[j] == diffmap.Removed {
					j++
				}
				runs = append(runs, &removed{part: c, lines: c.Lines[i:j], start: i})
				total += j - i
				i = j
			}
		case !c.Base && c.Kinds != nil:
			base := ghostBaseLines(c)
			for at, g := range c.Ghosts {
				runs = append(runs, &removed{part: c, lines: g, at: at, start: base[at], ghost: true})
				total += len(g)
			}
		}
	}
	if len(runs) == 0 || total > maxMovedLines {
		return
	}
	sort.SliceStable(runs, func(i, j int) bool {
		if runs[i].part.Spec.File != runs[j].part.Spec.File {
			return runs[i].part.Spec.File < runs[j].part.Spec.File
		}
		return runs[i].start < runs[j].start
	})
	index := map[string][]pos{}
	for ri, r := range runs {
		r.used = make([]bool, len(r.lines))
		for i, l := range r.lines {
			if n := norm(l); substantial(n) {
				index[n] = append(index[n], pos{ri, i})
			}
		}
	}

	for _, k := range keys {
		c := b.cache[k]
		if c.Err != nil || c.Base || c.Kinds == nil {
			continue
		}
		for i := 0; i < len(c.Kinds); {
			if c.Kinds[i] != diffmap.Added || !substantial(norm(c.Lines[i])) {
				i++
				continue
			}
			best, bestLen := pos{-1, -1}, 0
			for _, p := range index[norm(c.Lines[i])] {
				r := runs[p.run]
				n := 0
				for i+n < len(c.Kinds) && c.Kinds[i+n] == diffmap.Added && p.idx+n < len(r.lines) &&
					!r.used[p.idx+n] && norm(r.lines[p.idx+n]) == norm(c.Lines[i+n]) {
					n++
				}
				if n > bestLen {
					best, bestLen = p, n
				}
			}
			if bestLen < minMoved || substantialCount(c.Lines[i:i+bestLen]) < minSubstantial {
				i++
				continue
			}
			r := runs[best.run]
			from := fmt.Sprintf("moved from %s, old line %d", path.Base(sourceFile(r.part)), r.start+best.idx+1)
			to := fmt.Sprintf("moved to %s:%d", path.Base(c.Spec.File), i+1)
			for n := 0; n < bestLen; n++ {
				c.Moved[i+n] = from
				r.used[best.idx+n] = true
				if r.ghost {
					if r.part.GhostMoved[r.at] == nil {
						r.part.GhostMoved[r.at] = map[int]string{}
					}
					r.part.GhostMoved[r.at][best.idx+n] = to
				} else {
					r.part.Moved[r.start+best.idx+n] = to
				}
			}
			i += bestLen
		}
	}
}

// ghostBaseLines maps each current line with removed lines before it to the base line number of
// the first of them.
func ghostBaseLines(p *Part) map[int]int {
	out := map[int]int{}
	base := 0
	for i := 0; i <= len(p.Kinds); i++ {
		out[i] = base
		base += len(p.Ghosts[i])
		if i < len(p.Kinds) && p.Kinds[i] == diffmap.Same {
			base++
		}
	}
	return out
}

func sourceFile(p *Part) string {
	if p.Spec.BaseFile != "" {
		return p.Spec.BaseFile
	}
	return p.Spec.File
}

func norm(l string) string { return strings.TrimSpace(l) }

// substantial lines say something on their own; a bracket or a blank line matches anywhere.
func substantial(n string) bool {
	return len(strings.Trim(n, "{}()[],; \t")) >= 4
}

func substantialCount(lines []string) int {
	n := 0
	for _, l := range lines {
		if substantial(norm(l)) {
			n++
		}
	}
	return n
}

// MovedCount counts the lines of the part's range that were moved here unchanged.
func (p *Part) MovedCount() int {
	n := 0
	for l := p.Range.Start; l <= p.Range.End; l++ {
		if p.Moved[l] != "" {
			n++
		}
	}
	return n
}
