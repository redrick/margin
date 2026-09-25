package doc

import (
	"sort"
	"strings"

	"github.com/redrick/margin/internal/anchor"
	"github.com/redrick/margin/internal/ignore"
	"github.com/redrick/margin/internal/review"
)

// addUnplaced adds a stop holding every changed region no stop shows, so regrouping the tour can
// never hide a change. Files the repository ignores, the review skips or lists as tests are left
// out of it.
func (d *Doc) addUnplaced(b builder, ign *ignore.Matcher) {
	changes, err := b.files.Changed()
	if err != nil {
		d.problem("unplaced", "listing the changed files: %v", err)
		return
	}
	if len(changes) == 0 {
		return
	}
	shown := map[string][]anchor.Range{}
	baseShown := map[string]bool{}
	for _, s := range d.Stations {
		for _, p := range s.Parts {
			if p.Err != nil {
				continue
			}
			if p.Base {
				baseShown[p.Spec.File] = true
				continue
			}
			shown[p.Spec.File] = append(shown[p.Spec.File], p.Range)
			if p.Spec.BaseFile != "" {
				baseShown[p.Spec.BaseFile] = true
			}
		}
	}
	skip := &ignore.Matcher{}
	for _, t := range d.Review.Tests {
		skip.Add("/" + t.File)
	}
	for _, s := range d.Review.Skip {
		if what := strings.TrimSpace(s.What); what != "" && !strings.ContainsAny(what, " \t") {
			skip.Add(what)
		}
	}

	st := &Station{Kind: Code, ID: "unplaced", Title: "Changes no stop shows", Auto: true}
	var files []string
	regions := 0
	for _, c := range changes {
		switch {
		case ign.Match(c.Path):
			d.Ignored = append(d.Ignored, c)
			continue
		case skip.Match(c.Path):
			continue
		case c.Status == 'D':
			if baseShown[c.Path] {
				continue
			}
			p := b.part(review.Part{File: c.Path, Side: review.SideBase})
			if d.unshown(c.Path, p) {
				continue
			}
			st.Parts = append(st.Parts, p)
			files, regions = append(files, c.Path), regions+1
			continue
		}
		spec := review.Part{File: c.Path, Hunks: true}
		if c.Status == 'R' || c.Status == 'C' {
			spec.BaseFile = c.OldPath
		}
		whole := b.part(review.Part{File: c.Path, BaseFile: spec.BaseFile})
		if d.unshown(c.Path, whole) || whole.Kinds == nil {
			continue
		}
		var missing []anchor.Range
		for _, r := range HunkRanges(whole, 0) {
			if !within(whole, r, shown[c.Path]) {
				missing = append(missing, r)
			}
		}
		if len(missing) == 0 {
			continue
		}
		files, regions = append(files, c.Path), regions+len(missing)
		for _, p := range b.parts(spec) {
			for _, r := range missing {
				if p.Range.Start <= r.End && r.Start <= p.Range.End {
					st.Parts = append(st.Parts, p)
					break
				}
			}
		}
	}
	if len(st.Parts) == 0 {
		return
	}
	sort.Strings(files)
	d.Stations = append(d.Stations, st)
	d.problem("unplaced", "%d changed %s in %d %s in no stop; add them to a stop, or to skip with the path as what: %s",
		regions, plural(regions, "region", "regions"), len(files), plural(len(files), "file is", "files are"), strings.Join(files, ", "))
}

// unshown records a changed file that cannot be read as text and reports whether it was one.
func (d *Doc) unshown(path string, p *Part) bool {
	if p.Err == nil {
		return false
	}
	reason := p.Err.Error()
	if i := strings.LastIndex(reason, ": "); i >= 0 && strings.HasPrefix(reason, path) {
		reason = reason[i+2:]
	}
	d.Unshown = append(d.Unshown, Unshown{Path: path, Reason: reason})
	return true
}

// within reports whether every non-blank line of r is inside one of the ranges.
func within(p *Part, r anchor.Range, ranges []anchor.Range) bool {
	for l := r.Start; l <= r.End; l++ {
		if l < len(p.Lines) && strings.TrimSpace(p.Lines[l]) == "" {
			continue
		}
		in := false
		for _, s := range ranges {
			if s.Contains(l) {
				in = true
				break
			}
		}
		if !in {
			return false
		}
	}
	return true
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
