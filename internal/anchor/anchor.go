package anchor

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/redrick/margin/internal/review"
)

// Range is an inclusive, 0-based line range.
type Range struct{ Start, End int }

func (r Range) Contains(i int) bool { return i >= r.Start && i <= r.End }

func Resolve(path string, lines []string, p review.Part) (Range, error) {
	whole := Range{0, len(lines) - 1}
	switch {
	case p.Lines != "":
		return parseLines(p.Lines, len(lines))
	case p.From != "":
		starts := Find(lines, whole, p.From)
		if len(starts) != 1 {
			return Range{}, countErr("from", p.From, starts)
		}
		s := starts[0]
		if p.To == "" {
			return Range{s, BlockEnd(path, lines, s)}, nil
		}
		ends := Find(lines, Range{s, whole.End}, p.To)
		if len(ends) == 0 {
			return Range{}, fmt.Errorf("to %q not found after line %d", p.To, s+1)
		}
		return Range{s, ends[0]}, nil
	case p.Func != "":
		if strings.EqualFold(filepath.Ext(path), ".go") {
			if r, ok, err := goFunc(path, lines, p.Func); ok {
				return r, err
			}
		}
		return genericFunc(path, lines, p.Func)
	}
	return whole, nil
}

// Find returns the lines within r that contain needle.
func Find(lines []string, r Range, needle string) []int {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return nil
	}
	var hits []int
	for i := max(r.Start, 0); i <= r.End && i < len(lines); i++ {
		if strings.Contains(lines[i], needle) {
			hits = append(hits, i)
		}
	}
	return hits
}

func countErr(what, needle string, hits []int) error {
	if len(hits) == 0 {
		return fmt.Errorf("%s %q not found", what, needle)
	}
	return fmt.Errorf("%s %q matches %d times (lines %s)", what, needle, len(hits), lineList(hits))
}

func lineList(hits []int) string {
	parts := make([]string, 0, len(hits))
	for i, h := range hits {
		if i == 5 {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, strconv.Itoa(h+1))
	}
	return strings.Join(parts, ", ")
}

func parseLines(s string, n int) (Range, error) {
	a, b, ranged := strings.Cut(strings.TrimSpace(s), "-")
	start, err := strconv.Atoi(strings.TrimSpace(a))
	end := start
	if err == nil && ranged {
		end, err = strconv.Atoi(strings.TrimSpace(b))
	}
	if err != nil {
		return Range{}, fmt.Errorf("lines %q: want N or N-M", s)
	}
	if start < 1 || end < start || end > n {
		return Range{}, fmt.Errorf("lines %q outside 1-%d", s, n)
	}
	return Range{start - 1, end - 1}, nil
}

func goFunc(path string, lines []string, name string) (Range, bool, error) {
	fset := token.NewFileSet()
	file, perr := parser.ParseFile(fset, path, strings.Join(lines, "\n"), parser.ParseComments|parser.SkipObjectResolution)
	if file == nil {
		return Range{}, false, nil
	}
	recv, fn := splitName(name)
	span := func(doc *ast.CommentGroup, from, to token.Pos) Range {
		if doc != nil {
			from = doc.Pos()
		}
		return Range{fset.Position(from).Line - 1, fset.Position(to).Line - 1}
	}
	var hits []Range
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.Name != fn || (recv != "" && recvName(d) != recv) {
				continue
			}
			hits = append(hits, span(d.Doc, d.Pos(), d.End()))
		case *ast.GenDecl:
			if recv != "" {
				continue
			}
			for _, spec := range d.Specs {
				if !specNames(spec, fn) {
					continue
				}
				if len(d.Specs) == 1 {
					hits = append(hits, span(d.Doc, d.Pos(), d.End()))
				} else {
					hits = append(hits, span(specDoc(spec), spec.Pos(), spec.End()))
				}
			}
		}
	}
	switch {
	case len(hits) == 1:
		return hits[0], true, nil
	case len(hits) == 0 && perr != nil:
		return Range{}, false, nil
	case len(hits) == 0:
		return Range{}, true, fmt.Errorf("func %q not found", name)
	}
	return Range{}, true, fmt.Errorf("func %q matches %d declarations; qualify it as Type.%s", name, len(hits), fn)
}

func splitName(name string) (recv, fn string) {
	name = strings.NewReplacer("(", "", ")", "", "*", "").Replace(strings.TrimSpace(name))
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[:i], name[i+1:]
	}
	return "", name
}

func recvName(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return ""
	}
	t := d.Recv.List[0].Type
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
		case *ast.ParenExpr:
			t = x.X
		case *ast.IndexExpr:
			t = x.X
		case *ast.IndexListExpr:
			t = x.X
		case *ast.Ident:
			return x.Name
		default:
			return ""
		}
	}
}

func specNames(spec ast.Spec, name string) bool {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		return s.Name.Name == name
	case *ast.ValueSpec:
		for _, n := range s.Names {
			if n.Name == name {
				return true
			}
		}
	}
	return false
}

func specDoc(spec ast.Spec) *ast.CommentGroup {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		return s.Doc
	case *ast.ValueSpec:
		return s.Doc
	}
	return nil
}

const modifiers = `(?:(?:export|public|private|protected|static|async|pub(?:\([\w:]+\))?|override|final|abstract|inline|suspend|open|internal|default|unsafe|const|extern|virtual)\s+)*`

func defPatterns(name string) []*regexp.Regexp {
	q := regexp.QuoteMeta(name)
	return []*regexp.Regexp{
		regexp.MustCompile(`^\s*` + modifiers + `(?:func|def|fn|function\*?|class|module|struct|enum|trait|interface|type|sub|proc|object|record|protocol|impl|defmodule|defp?)\s+(?:[\w*().<>\[\], &']*?[.\s])?` + q + `\b`),
		regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+` + q + `\s*=`),
		regexp.MustCompile(`^\s*` + modifiers + `[\w<>\[\],.?* ]*?\b` + q + `\s*\([^;]*\)\s*(?:throws\s+[\w., ]+)?\{\s*$`),
	}
}

var controlWords = regexp.MustCompile(`^\s*(?:return|if|else|while|for|switch|catch|do)\b`)

func genericFunc(path string, lines []string, name string) (Range, error) {
	qual, fn := "", name
	if i := strings.LastIndexAny(name, ".:#"); i >= 0 {
		qual, fn = strings.TrimRight(name[:i], ":"), name[i+1:]
	}
	hits := defHits(lines, fn)
	if qual != "" && len(hits) > 1 {
		var scoped []int
		for _, c := range defHits(lines, qual) {
			block := Range{c, BlockEnd(path, lines, c)}
			for _, h := range hits {
				if h > block.Start && block.Contains(h) {
					scoped = append(scoped, h)
				}
			}
		}
		hits = scoped
	}
	if len(hits) != 1 {
		return Range{}, countErr("func", name, hits)
	}
	s := hits[0]
	return Range{extendUp(lines, s), BlockEnd(path, lines, s)}, nil
}

func defHits(lines []string, name string) []int {
	for _, re := range defPatterns(name) {
		var hits []int
		for i, l := range lines {
			if re.MatchString(l) && !controlWords.MatchString(l) {
				hits = append(hits, i)
			}
		}
		if len(hits) > 0 {
			return hits
		}
	}
	return nil
}

func extendUp(lines []string, s int) int {
	for s > 0 {
		t := strings.TrimSpace(lines[s-1])
		if t == "" || !(strings.HasPrefix(t, "@") || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") ||
			strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "--")) {
			break
		}
		if strings.HasPrefix(t, "#!") {
			break
		}
		s--
	}
	return s
}

var indentLangs = map[string]bool{
	".py": true, ".rb": true, ".ex": true, ".exs": true, ".lua": true, ".jl": true, ".cr": true,
	".nim": true, ".yaml": true, ".yml": true, ".coffee": true, ".erb": true, ".rake": true,
}

// BlockEnd guesses where the block starting at line s ends: braces for C-like files,
// indentation (plus a closing `end`) for the rest.
func BlockEnd(path string, lines []string, s int) int {
	if !indentLangs[strings.ToLower(filepath.Ext(path))] && !strings.HasSuffix(strings.TrimSpace(lines[s]), ":") {
		if end, ok := braceEnd(lines, s); ok {
			return end
		}
	}
	return indentEnd(lines, s)
}

func braceEnd(lines []string, s int) (int, bool) {
	depth, opened, inBlock := 0, false, false
	var raw byte
	for i := s; i < len(lines); i++ {
		l := lines[i]
		if !opened && i > s {
			if t := strings.TrimSpace(l); i-s > 8 || t == "" || strings.HasSuffix(t, ";") {
				return 0, false
			}
		}
		var quote byte
		for j := 0; j < len(l); j++ {
			c := l[j]
			switch {
			case inBlock:
				if c == '*' && j+1 < len(l) && l[j+1] == '/' {
					inBlock = false
					j++
				}
			case raw != 0:
				if c == raw {
					raw = 0
				}
			case quote != 0:
				if c == '\\' {
					j++
				} else if c == quote {
					quote = 0
				}
			case c == '/' && j+1 < len(l) && l[j+1] == '/':
				j = len(l)
			case c == '/' && j+1 < len(l) && l[j+1] == '*':
				inBlock = true
				j++
			case c == '`':
				raw = c
			case c == '"':
				quote = c
			case c == '\'' && strings.IndexByte(l[j+1:], '\'') >= 0:
				quote = c
			case c == '{':
				depth++
				opened = true
			case c == '}':
				depth--
				if opened && depth == 0 {
					return i, true
				}
			}
		}
	}
	return 0, false
}

func indentEnd(lines []string, s int) int {
	base := indentOf(lines[s])
	end := s
	for i := s + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		if indentOf(lines[i]) <= base {
			if t == "end" || strings.HasPrefix(t, "end ") || strings.HasPrefix(t, "end;") || strings.HasPrefix(t, "end)") {
				end = i
			} else if strings.HasPrefix(t, ")") || strings.HasPrefix(t, "]") {
				end = i
				continue
			}
			break
		}
		end = i
	}
	return end
}

func indentOf(l string) int {
	n := 0
	for _, c := range l {
		switch c {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}
