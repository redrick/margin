package doc

import (
	"sort"
	"strings"

	"github.com/redrick/margin/internal/coverage"
	"github.com/redrick/margin/internal/diffmap"
)

type LineCov uint8

const (
	CovNone   LineCov = iota // file not measured, or measured on other code
	CovInert                 // nothing on the line can run
	CovRun                   // a test runs it
	CovIdle                  // unchanged and no test runs it
	CovMissed                // changed and no test runs it
)

type CovTest struct {
	Name, Package string
	// Group is the package, or the test file when the tool has no packages.
	Group   string
	Display string
	Depth   int
	// Synthetic marks a parent the agent reported no coverage for, shown only to group its subtests.
	Synthetic bool
	New       bool
}

type Coverage struct {
	Tests    []CovTest
	Stale    map[string]bool
	run      map[string]map[int][]int
	exec     map[string]map[int]bool
	measured map[string]bool
}

// AttachCoverage indexes set against the review's files and adds a Tests stop to show it on.
func (d *Doc) AttachCoverage(set *coverage.Set) {
	d.Coverage = nil
	if set == nil || (len(set.Tests) == 0 && len(set.Executable) == 0) {
		return
	}
	c := &Coverage{Stale: map[string]bool{}, run: map[string]map[int][]int{}, exec: map[string]map[int]bool{}, measured: map[string]bool{}}
	fresh := map[string]bool{}
	if _, st := d.Station("tests"); st != nil {
		for _, g := range st.Tests {
			for name := range g.New {
				fresh[name] = true
			}
			if g.NewFile {
				for _, n := range g.Names {
					fresh[n.Name] = true
				}
			}
		}
	}
	have := map[string]bool{}
	for _, t := range set.Tests {
		have[t.Package+"\x00"+t.Name] = true
	}
	var list []coverage.Test
	for _, t := range set.Tests {
		list = append(list, t)
		parts := strings.Split(t.Name, "/")
		for i := 1; i < len(parts); i++ {
			parent := strings.Join(parts[:i], "/")
			if !have[t.Package+"\x00"+parent] {
				have[t.Package+"\x00"+parent] = true
				list = append(list, coverage.Test{Name: parent, Package: t.Package, File: t.File, Source: "synthetic"})
			}
		}
	}
	group := func(t coverage.Test) string {
		if t.Package != "" {
			return t.Package
		}
		return t.File
	}
	sort.SliceStable(list, func(i, j int) bool {
		if gi, gj := group(list[i]), group(list[j]); gi != gj {
			return gi < gj
		}
		return list[i].Name < list[j].Name
	})
	for ti, t := range list {
		parts := strings.Split(t.Name, "/")
		display := parts[len(parts)-1]
		if len(parts) > 1 {
			display = strings.ReplaceAll(display, "_", " ")
		}
		c.Tests = append(c.Tests, CovTest{Name: t.Name, Package: t.Package, Group: group(t), Display: display, Depth: len(parts) - 1,
			Synthetic: t.Source == "synthetic", New: fresh[parts[len(parts)-1]] || fresh[display]})
		for file, lines := range t.Lines {
			c.measured[file] = true
			if c.run[file] == nil {
				c.run[file] = map[int][]int{}
			}
			for _, l := range lines {
				c.run[file][l-1] = append(c.run[file][l-1], ti)
			}
		}
	}
	for file, lines := range set.Executable {
		c.measured[file] = true
		c.exec[file] = map[int]bool{}
		for _, l := range lines {
			c.exec[file][l-1] = true
		}
	}
	for _, st := range d.Stations {
		for _, p := range st.Parts {
			if h, ok := set.Hashes[p.Spec.File]; ok && !p.Base && p.Err == nil && coverage.Hash(p.Lines) != h {
				c.Stale[p.Spec.File] = true
			}
		}
	}
	d.Coverage = c
	if _, st := d.Station("tests"); st == nil {
		d.Stations = append(d.Stations, &Station{Kind: Tests, ID: "tests", Title: "Tests"})
	}
}

// Label is the test's full name as people write it: go test turns spaces in subtest names into _.
func (t CovTest) Label() string {
	top, rest, ok := strings.Cut(t.Name, "/")
	if !ok {
		return top
	}
	return top + "/" + strings.ReplaceAll(rest, "_", " ")
}

func (c *Coverage) usable(p *Part) bool {
	return c != nil && p.Err == nil && !p.Base && c.measured[p.Spec.File] && !c.Stale[p.Spec.File]
}

func (c *Coverage) Line(p *Part, line int) LineCov {
	if !c.usable(p) {
		return CovNone
	}
	if line < 0 || line >= len(p.Lines) || trivial(p.Lines[line]) {
		return CovInert
	}
	file := p.Spec.File
	if len(c.run[file][line]) > 0 {
		return CovRun
	}
	if ex, ok := c.exec[file]; ok && !ex[line] {
		return CovInert
	}
	if k := lineKindOf(p, line); k == diffmap.Added || k == diffmap.Changed {
		return CovMissed
	}
	return CovIdle
}

// TestsAt lists the tests that run line, as indices into Tests.
func (c *Coverage) TestsAt(p *Part, line int) []int {
	if !c.usable(p) {
		return nil
	}
	return c.run[p.Spec.File][line]
}

func (c *Coverage) Runs(test int, p *Part, line int) bool {
	for _, t := range c.TestsAt(p, line) {
		if t == test || c.isChild(t, test) {
			return true
		}
	}
	return false
}

func (c *Coverage) isChild(t, parent int) bool {
	a, b := c.Tests[t], c.Tests[parent]
	return a.Package == b.Package && strings.HasPrefix(a.Name, b.Name+"/")
}

type StopCov struct {
	Run, Missed int
	Measured    bool
	Stale       bool
	// Current is set when the stop shows current code at all, which a stop of removed code does not.
	Current bool
}

func (s StopCov) Total() int { return s.Run + s.Missed }

// Coverage counts the station's changed lines that tests run and miss; test < 0 means any test.
func (st *Station) Coverage(c *Coverage, test int) StopCov {
	var out StopCov
	if c == nil {
		return out
	}
	seen := map[string]bool{}
	for _, p := range st.Parts {
		if p.Err != nil || p.Base {
			continue
		}
		out.Current = true
		if c.Stale[p.Spec.File] {
			out.Stale = true
			continue
		}
		if !c.usable(p) {
			continue
		}
		out.Measured = true
		for i := p.Range.Start; i <= p.Range.End && i < len(p.Lines); i++ {
			k := LineKey(p, i)
			if seen[k] {
				continue
			}
			seen[k] = true
			if kind := lineKindOf(p, i); kind != diffmap.Added && kind != diffmap.Changed {
				continue
			}
			switch c.Line(p, i) {
			case CovRun:
				if test < 0 || c.Runs(test, p, i) {
					out.Run++
				} else {
					out.Missed++
				}
			case CovMissed:
				out.Missed++
			}
		}
	}
	return out
}

func lineKindOf(p *Part, line int) diffmap.Kind {
	if line < len(p.Kinds) {
		return p.Kinds[line]
	}
	return diffmap.Same
}

// trivial lines hold nothing that runs: blanks, lone brackets, comments.
func trivial(line string) bool {
	s := strings.TrimSpace(line)
	if strings.Trim(s, "{}()[],;") == "" || s == "end" {
		return true
	}
	for _, p := range []string{"//", "#", "/*"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
