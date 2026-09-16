package render

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func init() { lipgloss.SetColorProfile(termenv.Ascii) }

func TestCode(t *testing.T) {
	tests := []struct {
		name   string
		spans  []Span
		width  int
		offset int
		want   string
	}{
		{"tab expands", []Span{{Text: "\tab"}}, 6, 0, "    ab"},
		{"truncates", []Span{{Text: "\tab"}}, 5, 0, "    a"},
		{"horizontal offset", []Span{{Text: "\tab"}}, 6, 2, "  ab  "},
		{"wide rune does not overflow", []Span{{Text: "日本"}}, 3, 0, "日 "},
		{"tab stop counts earlier spans", []Span{{Text: "a"}, {Text: "\tb"}}, 6, 0, "a   b "},
		{"pads empty", nil, 3, 0, "   "},
	}
	p := NewPainter()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.Code(tt.spans, tt.width, tt.offset, ""); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChangedRange(t *testing.T) {
	as, ae, bs, be, ok := ChangedRange("\treturn ErrInsufficient", "\treturn fmt.Errorf(ErrInsufficient)")
	if !ok || as != 8 || ae != 23 || bs != 8 || be != 35 {
		t.Fatalf("got %d-%d %d-%d ok=%v", as, ae, bs, be, ok)
	}
	if _, _, _, _, ok := ChangedRange("abc", "xyz"); ok {
		t.Fatal("unrelated lines should not get a word-level highlight")
	}
	if _, _, _, _, ok := ChangedRange("\tfoo()", "\tbar()"); !ok {
		t.Fatal("lines sharing a call suffix should highlight the changed word")
	}
	if _, _, _, _, ok := ChangedRange("\tabc", "\txyz"); ok {
		t.Fatal("sharing only the indent is not enough")
	}
	p := NewPainter()
	if got := p.CodeHL([]Span{{Text: "abcd"}}, 5, 0, "", 1, 3, "#ffffff"); got != "abcd " {
		t.Fatalf("highlight must not change the text: %q", got)
	}
}

func TestHighlightKeepsLineCount(t *testing.T) {
	lines := []string{"package x", "", "var s = `multi", "line`", "func f() {}"}
	got := Highlight("x.go", lines)
	if len(got) != len(lines) {
		t.Fatalf("lines = %d, want %d", len(got), len(lines))
	}
	for i, spans := range got {
		var b strings.Builder
		for _, s := range spans {
			b.WriteString(s.Text)
		}
		if b.String() != lines[i] {
			t.Errorf("line %d = %q, want %q", i, b.String(), lines[i])
		}
	}
}
