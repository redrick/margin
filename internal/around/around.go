// Package around looks at the code around a stop: who outside the review uses what the stop
// changes, and which commits wrote the code the stop replaces.
package around

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/redrick/margin/internal/anchor"
	"github.com/redrick/margin/internal/diffmap"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/source"
)

type Symbol struct {
	Name    string
	File    string
	Line    int // 1-based line of the definition
	Removed bool
	Type    bool
	// Uses are the places outside the tour that mention the symbol; Inside counts the ones the
	// tour already shows. TooMany is set, with Uses empty, when the name is too common to list.
	Uses    []source.Match
	Inside  int
	TooMany int
}

type Report struct {
	Symbols []Symbol
	History []Commit
	Err     error
}

// maxUses is how many places a symbol may be used in before it counts as too common to list.
const maxUses = 60

// Study looks around one code stop of d.
func Study(d *doc.Doc, st *doc.Station, files *source.Files) Report {
	var r Report
	inTour := tourLines(d)
	for _, sym := range Symbols(st) {
		ms, err := files.Grep(usePattern(sym))
		if err != nil {
			r.Err = err
			break
		}
		for _, m := range ms {
			switch {
			case m.File == sym.File && m.Line == sym.Line && !sym.Removed:
			case comment(m.Text):
			case unreachable(sym, m.File):
			case inTour(m.File, m.Line-1):
				sym.Inside++
			default:
				sym.Uses = append(sym.Uses, m)
			}
		}
		if len(sym.Uses) > maxUses {
			sym.TooMany, sym.Uses = len(sym.Uses), nil
		}
		r.Symbols = append(r.Symbols, sym)
	}
	if base := files.BaseCommit(); base != "" && r.Err == nil {
		r.History, r.Err = History(files.Repo, base, st)
	}
	return r
}

// unreachable reports whether a file cannot use the symbol at all: an unexported Go name is only
// visible inside its own package.
func unreachable(sym Symbol, file string) bool {
	if !strings.HasSuffix(sym.File, ".go") || sym.Name == "" || unicode.IsUpper([]rune(sym.Name)[0]) {
		return false
	}
	return filepath.Dir(file) != filepath.Dir(sym.File)
}

// tourLines reports whether a current-side line is shown by any stop of the review.
func tourLines(d *doc.Doc) func(file string, line int) bool {
	ranges := map[string][]anchor.Range{}
	for _, s := range d.Stations {
		for _, p := range s.Parts {
			if p.Err == nil && !p.Base {
				ranges[p.Spec.File] = append(ranges[p.Spec.File], p.Range)
			}
		}
	}
	return func(file string, line int) bool {
		for _, r := range ranges[file] {
			if r.Contains(line) {
				return true
			}
		}
		return false
	}
}

// Symbols lists the definitions a stop changes or removes. A definition the change adds is left
// out: nothing outside the change can depend on it yet.
func Symbols(st *doc.Station) []Symbol {
	seen := map[string]bool{}
	var out []Symbol
	add := func(s Symbol) {
		k := s.File + "|" + s.Name
		if seen[k] || len(s.Name) < 3 {
			return
		}
		seen[k] = true
		out = append(out, s)
	}
	for _, p := range st.Parts {
		if p.Err != nil || p.Spec.Background || p.Kinds == nil {
			continue
		}
		pats := defPatterns(p.Spec.File)
		if pats == nil {
			continue
		}
		if p.Base {
			for l := p.Range.Start; l <= p.Range.End; l++ {
				if name, typ, ok := definition(pats, p.Lines[l]); ok {
					add(Symbol{Name: name, File: p.Spec.File, Line: l + 1, Removed: true, Type: typ})
				}
			}
			continue
		}
		for l := p.Range.Start; l <= p.Range.End+1 && l <= len(p.Lines); l++ {
			for _, g := range p.Ghosts[l] {
				if name, typ, ok := definition(pats, g); ok {
					add(Symbol{Name: name, File: p.Spec.File, Line: l + 1, Removed: true, Type: typ})
				}
			}
			if l == len(p.Lines) || !changed(p, l) {
				continue
			}
			if d, name, typ, ok := enclosing(p, pats, l); ok && p.Kinds[d] != diffmap.Added {
				add(Symbol{Name: name, File: p.Spec.File, Line: d + 1, Type: typ})
			}
		}
	}
	return out
}

func changed(p *doc.Part, l int) bool {
	return (l < len(p.Kinds) && p.Kinds[l] != diffmap.Same) || len(p.Ghosts[l]) > 0
}

// enclosing finds the definition whose block holds line l.
func enclosing(p *doc.Part, pats []pattern, l int) (int, string, bool, bool) {
	return enclosingIn(p.Spec.File, p.Lines, pats, l)
}

// Enclosing names the function or type whose body holds line l of a file, and its line.
func Enclosing(file string, lines []string, l int) (name string, def int, ok bool) {
	pats := defPatterns(file)
	if pats == nil || l < 0 || l >= len(lines) {
		return "", 0, false
	}
	def, name, _, ok = enclosingIn(file, lines, pats, l)
	return name, def, ok
}

func enclosingIn(file string, lines []string, pats []pattern, l int) (int, string, bool, bool) {
	for d := l; d >= 0; d-- {
		name, typ, ok := definition(pats, lines[d])
		if !ok {
			continue
		}
		if anchor.BlockEnd(file, lines, d) >= l {
			return d, name, typ, true
		}
		return 0, "", false, false
	}
	return 0, "", false, false
}

type pattern struct {
	re  *regexp.Regexp
	typ bool
}

var langs = map[string][]pattern{}

func init() {
	def := func(typ bool, exprs ...string) []pattern {
		var ps []pattern
		for _, e := range exprs {
			ps = append(ps, pattern{regexp.MustCompile(e), typ})
		}
		return ps
	}
	join := func(groups ...[]pattern) []pattern {
		var out []pattern
		for _, g := range groups {
			out = append(out, g...)
		}
		return out
	}
	langs[".go"] = join(def(false, `^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)`), def(true, `^type\s+([A-Za-z_]\w*)`))
	langs[".py"] = join(def(false, `^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)`), def(true, `^\s*class\s+([A-Za-z_]\w*)`))
	js := join(def(false,
		`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][\w$]*)`,
		`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)\s*=>`),
		def(true, `^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`,
			`^\s*(?:export\s+)?(?:interface|type)\s+([A-Za-z_$][\w$]*)`))
	for _, ext := range []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs"} {
		langs[ext] = js
	}
	langs[".rs"] = join(def(false, `^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?(?:unsafe\s+)?fn\s+([A-Za-z_]\w*)`),
		def(true, `^\s*(?:pub(?:\([^)]*\))?\s+)?(?:struct|enum|trait)\s+([A-Za-z_]\w*)`))
	langs[".rb"] = join(def(false, `^\s*def\s+(?:self\.)?([A-Za-z_]\w*[?!]?)`), def(true, `^\s*(?:class|module)\s+([A-Z]\w*)`))
	langs[".php"] = join(def(false, `^\s*(?:(?:public|private|protected|static|final|abstract)\s+)*function\s+([A-Za-z_]\w*)`),
		def(true, `^\s*(?:final\s+|abstract\s+)?(?:class|interface|trait)\s+([A-Za-z_]\w*)`))
}

func defPatterns(file string) []pattern { return langs[strings.ToLower(filepath.Ext(file))] }

func definition(pats []pattern, line string) (name string, typ bool, ok bool) {
	for _, p := range pats {
		if m := p.re.FindStringSubmatch(line); m != nil {
			return m[1], p.typ, true
		}
	}
	return "", false, false
}

// usePattern finds calls of a function and any mention of a type, as an extended regular
// expression both git grep and Go understand.
func usePattern(s Symbol) string {
	name := regexp.QuoteMeta(s.Name)
	if s.Type {
		return `(^|[^A-Za-z0-9_$])` + name + `([^A-Za-z0-9_$]|$)`
	}
	return `(^|[^A-Za-z0-9_$])` + name + `[[:space:]]*\(`
}

func comment(line string) bool {
	t := strings.TrimSpace(line)
	for _, p := range []string{"//", "#", "/*", "*", "--"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// SortUses keeps a symbol's uses in file and line order.
func SortUses(ms []source.Match) {
	sort.Slice(ms, func(i, j int) bool {
		if ms[i].File != ms[j].File {
			return ms[i].File < ms[j].File
		}
		return ms[i].Line < ms[j].Line
	})
}

// Defines lists the functions and types defined on lines from to to of a file.
func Defines(file string, lines []string, from, to int) []string {
	pats := defPatterns(file)
	var out []string
	for l := max(from, 0); pats != nil && l <= to && l < len(lines); l++ {
		if name, _, ok := definition(pats, lines[l]); ok {
			out = append(out, name)
		}
	}
	return out
}
