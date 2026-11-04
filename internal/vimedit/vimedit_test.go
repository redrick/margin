package vimedit

import (
	"strings"
	"testing"
)

// keys turns "cwfoo<esc>" into key presses; <name> is a named key.
func keys(s string) []Key {
	var out []Key
	for s != "" {
		if s[0] == '<' {
			if i := strings.IndexByte(s, '>'); i > 1 {
				out = append(out, Key{Name: s[1:i]})
				s = s[i+1:]
				continue
			}
		}
		r := []rune(s)[0]
		out = append(out, Key{Name: string(r), Runes: []rune{r}})
		s = s[len(string(r)):]
	}
	return out
}

func feed(e *Editor, s string) Result {
	res := None
	for _, k := range keys(s) {
		if r := e.Key(k); r != None {
			res = r
		}
	}
	return res
}

func TestEdits(t *testing.T) {
	const src = "\tif qty <= 0 {\n\t\treturn ErrInvalid\n\t}"
	cases := []struct{ name, keys, want string }{
		{"x", "x", "\tf qty <= 0 {\n\t\treturn ErrInvalid\n\t}"},
		{"cw", "wcwamount<esc>", "\tif amount <= 0 {\n\t\treturn ErrInvalid\n\t}"},
		{"ciw on space keeps words", "wwciwX<esc>", "\tif qty X 0 {\n\t\treturn ErrInvalid\n\t}"},
		{"f and r", "f=r<", "\tif qty << 0 {\n\t\treturn ErrInvalid\n\t}"},
		{"dt", "wdt<", "\tif <= 0 {\n\t\treturn ErrInvalid\n\t}"},
		{"dd", "dd", "\t\treturn ErrInvalid\n\t}"},
		{"2dd", "2dd", "\t}"},
		{"dG deletes all", "dG", ""},
		{"ggdG from below", "jjggdG", ""},
		{"d2j", "d2j", ""},
		{"o keeps indent", "jonil<esc>", "\tif qty <= 0 {\n\t\treturn ErrInvalid\n\t\tnil\n\t}"},
		{"A", "A // x<esc>", "\tif qty <= 0 { // x\n\t\treturn ErrInvalid\n\t}"},
		{"yyp", "yyp", "\tif qty <= 0 {\n\tif qty <= 0 {\n\t\treturn ErrInvalid\n\t}"},
		{"ddp swaps", "ddp", "\t\treturn ErrInvalid\n\tif qty <= 0 {\n\t}"},
		{"u undoes", "ddu", src},
		{"u then ctrl+r", "ddu<ctrl+r>", "\t\treturn ErrInvalid\n\t}"},
		{"dot repeats", "dd.", "\t}"},
		{"dot repeats an insert", "A;<esc>j.", "\tif qty <= 0 {;\n\t\treturn ErrInvalid;\n\t}"},
		{"J", "J", "\tif qty <= 0 { return ErrInvalid\n\t}"},
		{"Vjd", "Vjd", "\t}"},
		{"v e d", "wvex", "\tif  <= 0 {\n\t\treturn ErrInvalid\n\t}"},
		{"di{", "jdi{", "\tif qty <= 0 {\n\t}"},
		{"ci( on none", "ci(<esc>", src},
		{"C", "wC}<esc>", "\tif }\n\t\treturn ErrInvalid\n\t}"},
		{">>", ">>", "\t\tif qty <= 0 {\n\t\treturn ErrInvalid\n\t}"},
		{"enter splits with indent", "jA<enter>x<esc>", "\tif qty <= 0 {\n\t\treturn ErrInvalid\n\t\tx\n\t}"},
		{"backspace joins", "jI<backspace><backspace><backspace><esc>", "\tif qty <= 0 {return ErrInvalid\n\t}"},
		{"dw at last word stays on line", "j$bdw", "\tif qty <= 0 {\n\t\treturn \n\t}"},
		{"count x", "3x", "\tqty <= 0 {\n\t\treturn ErrInvalid\n\t}"},
		{"~", "~", "\tIf qty <= 0 {\n\t\treturn ErrInvalid\n\t}"},
		{"esc drops a pending operator", "d<esc>x", "\tf qty <= 0 {\n\t\treturn ErrInvalid\n\t}"},
	}
	for _, c := range cases {
		e := New(src, 32)
		feed(e, c.keys)
		if got := e.Text(); got != c.want {
			t.Errorf("%s (%q):\n got %q\nwant %q", c.name, c.keys, got, c.want)
		}
	}
}

func TestCommands(t *testing.T) {
	e := New("a\nb", 10)
	if r := feed(e, ":w<enter>"); r != Run {
		t.Fatalf(":w runs, got %v", r)
	}
	if r := feed(e, ":q<enter>"); r != Cancel {
		t.Fatalf(":q cancels, got %v", r)
	}
	if r := feed(e, "ix<ctrl+s>"); r != Run || e.Mode != Normal || e.Text() != "xa\nb" {
		t.Fatalf("ctrl+s runs from insert mode: %v %v %q", r, e.Mode, e.Text())
	}
	feed(e, ":11<enter>")
	if e.Row != 1 {
		t.Fatalf(":N goes to line N of the file, got row %d", e.Row)
	}
	feed(e, ":nope<enter>")
	if !strings.Contains(e.Msg, "nope") {
		t.Fatalf("an unknown command says so: %q", e.Msg)
	}
}

func TestMotions(t *testing.T) {
	e := New("foo.bar(baz)\n\nnext word", 1)
	feed(e, "w")
	if e.Col != 3 {
		t.Fatalf("w stops at punctuation: col %d", e.Col)
	}
	feed(e, "W")
	if e.Row != 1 || e.Col != 0 {
		t.Fatalf("W stops on an empty line: %d,%d", e.Row, e.Col)
	}
	feed(e, "w$")
	if e.Row != 2 || e.Col != 8 {
		t.Fatalf("$ goes to the last character: %d,%d", e.Row, e.Col)
	}
	feed(e, "bbb")
	if e.Row != 1 {
		t.Fatalf("b goes back over lines: %d,%d", e.Row, e.Col)
	}
	feed(e, "gg$k")
	if e.Row != 0 || e.Col != 11 {
		t.Fatalf("gg then $: %d,%d", e.Row, e.Col)
	}
	feed(e, "G")
	if e.Row != 2 {
		t.Fatalf("G goes to the last line: %d", e.Row)
	}
}
