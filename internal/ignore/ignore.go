// Package ignore matches repository paths against gitignore-style patterns.
package ignore

import (
	"regexp"
	"strings"
)

type rule struct {
	re  *regexp.Regexp
	neg bool
}

type Matcher struct{ rules []rule }

// Parse reads one pattern per line; blank lines and lines starting with # are skipped, and the last
// matching pattern wins, so a later !pattern brings a path back.
func Parse(text string) *Matcher {
	m := &Matcher{}
	for _, l := range strings.Split(text, "\n") {
		m.Add(l)
	}
	return m
}

func (m *Matcher) Add(pattern string) {
	p := strings.TrimSpace(pattern)
	if p == "" || strings.HasPrefix(p, "#") {
		return
	}
	neg := strings.HasPrefix(p, "!")
	p = strings.TrimPrefix(p, "!")
	dirOnly := strings.HasSuffix(p, "/")
	anchored := strings.HasPrefix(p, "/")
	p = strings.Trim(p, "/")
	if p == "" {
		return
	}
	anchored = anchored || strings.Contains(p, "/")
	var b strings.Builder
	b.WriteString("^")
	if !anchored {
		b.WriteString("(?:.*/)?")
	}
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case strings.HasPrefix(p[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(p[i:], "/**") && i+3 == len(p):
			b.WriteString("/.*")
			i += 2
		case strings.HasPrefix(p[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	if dirOnly {
		b.WriteString("/.*$")
	} else {
		b.WriteString("(?:/.*)?$")
	}
	if re, err := regexp.Compile(b.String()); err == nil {
		m.rules = append(m.rules, rule{re: re, neg: neg})
	}
}

func (m *Matcher) Match(path string) bool {
	if m == nil {
		return false
	}
	hit := false
	for _, r := range m.rules {
		if r.re.MatchString(path) {
			hit = !r.neg
		}
	}
	return hit
}

func (m *Matcher) Empty() bool { return m == nil || len(m.rules) == 0 }
