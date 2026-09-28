package around

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redrick/margin/internal/diffmap"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/gitx"
	"github.com/redrick/margin/internal/text"
)

// Commit is a commit that wrote some of the code a stop replaces or removes.
type Commit struct {
	SHA     string
	Author  string
	Date    string
	Subject string
	Lines   int
}

const (
	maxBlamed  = 2000
	maxCommits = 5
)

// History blames the base-side lines a stop replaces or removes, so the reader sees why the old
// code was written the way it was.
func History(repo, base string, st *doc.Station) ([]Commit, error) {
	lines := map[string]map[int]bool{}
	mark := func(file string, l int) {
		if lines[file] == nil {
			lines[file] = map[int]bool{}
		}
		lines[file][l+1] = true
	}
	for _, p := range st.Parts {
		if p.Err != nil || p.Spec.Background || p.Kinds == nil || p.NewFile {
			continue
		}
		file := p.Spec.File
		if p.Spec.BaseFile != "" {
			file = p.Spec.BaseFile
		}
		if p.Base {
			for l := p.Range.Start; l <= p.Range.End; l++ {
				if p.Kinds[l] == diffmap.Removed {
					mark(file, l)
				}
			}
			continue
		}
		baseAt := doc.GhostBaseLines(p)
		for l := p.Range.Start; l <= p.Range.End+1 && l <= len(p.Lines); l++ {
			for k := range p.Ghosts[l] {
				mark(file, baseAt[l]+k)
			}
		}
	}
	byCommit := map[string]*Commit{}
	total := 0
	files := make([]string, 0, len(lines))
	for f := range lines {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, file := range files {
		for _, rg := range spans(lines[file]) {
			if total += rg[1] - rg[0] + 1; total > maxBlamed {
				break
			}
			out, err := gitx.Run(repo, "blame", "--porcelain", "-L", strconv.Itoa(rg[0])+","+strconv.Itoa(rg[1]), base, "--", file)
			if err != nil {
				return nil, err
			}
			for _, c := range ParseBlame(string(out)) {
				if have := byCommit[c.SHA]; have != nil {
					have.Lines += c.Lines
				} else {
					c := c
					byCommit[c.SHA] = &c
				}
			}
		}
	}
	out := make([]Commit, 0, len(byCommit))
	for _, c := range byCommit {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lines != out[j].Lines {
			return out[i].Lines > out[j].Lines
		}
		return out[i].Date > out[j].Date
	})
	if len(out) > maxCommits {
		out = out[:maxCommits]
	}
	return out, nil
}

// spans turns a set of 1-based line numbers into sorted [first, last] ranges.
func spans(set map[int]bool) [][2]int {
	ls := make([]int, 0, len(set))
	for l := range set {
		ls = append(ls, l)
	}
	sort.Ints(ls)
	var out [][2]int
	for _, l := range ls {
		if n := len(out); n > 0 && out[n-1][1] == l-1 {
			out[n-1][1] = l
			continue
		}
		out = append(out, [2]int{l, l})
	}
	return out
}

// ParseBlame reads `git blame --porcelain` output into commits with how many lines each wrote.
func ParseBlame(porcelain string) []Commit {
	var order []string
	commits := map[string]*Commit{}
	var cur *Commit
	for _, l := range strings.Split(porcelain, "\n") {
		switch {
		case strings.HasPrefix(l, "\t"):
			continue
		case isHeader(l):
			sha := l[:strings.IndexByte(l, ' ')]
			if commits[sha] == nil {
				commits[sha] = &Commit{SHA: sha}
				order = append(order, sha)
			}
			cur = commits[sha]
			cur.Lines++
		case cur == nil:
		case strings.HasPrefix(l, "author "):
			cur.Author = text.Line(strings.TrimPrefix(l, "author "))
		case strings.HasPrefix(l, "author-time "):
			if t, err := strconv.ParseInt(strings.TrimPrefix(l, "author-time "), 10, 64); err == nil {
				cur.Date = time.Unix(t, 0).UTC().Format("2006-01-02")
			}
		case strings.HasPrefix(l, "summary "):
			cur.Subject = text.Line(strings.TrimPrefix(l, "summary "))
		}
	}
	out := make([]Commit, 0, len(order))
	for _, sha := range order {
		if !strings.HasPrefix(sha, "0000000") {
			out = append(out, *commits[sha])
		}
	}
	return out
}

func isHeader(l string) bool {
	f := strings.Fields(l)
	if len(f) < 3 || len(f[0]) < 40 {
		return false
	}
	for _, c := range f[0] {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// Message is a commit's full message, for reading in the viewer.
func Message(repo, sha string) (string, error) {
	out, err := gitx.Run(repo, "log", "-1", "--format=%h %an, %as%n%n%B", "--end-of-options", sha)
	if err != nil {
		return "", err
	}
	return text.Sanitize(strings.TrimSpace(string(out))), nil
}
