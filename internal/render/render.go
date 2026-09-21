package render

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const TabWidth = 4

type Span struct {
	Text   string
	Color  string
	Bold   bool
	Italic bool
	Strike bool
	Bg     string
}

// Highlight tokenises the whole file at once, so multi-line strings and comments colour correctly.
func Highlight(filename string, lines []string) [][]Span {
	out := make([][]Span, len(lines))
	content := strings.Join(lines, "\n")
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Analyse(content)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	style := styles.Get("github-dark")
	it, err := chroma.Coalesce(lexer).Tokenise(nil, content)
	if err != nil {
		for i, l := range lines {
			out[i] = []Span{{Text: l}}
		}
		return out
	}
	line := 0
	for tok := it(); tok != chroma.EOF; tok = it() {
		e := style.Get(tok.Type)
		sp := Span{Bold: e.Bold == chroma.Yes, Italic: e.Italic == chroma.Yes}
		if e.Colour.IsSet() {
			sp.Color = e.Colour.String()
		}
		for i, piece := range strings.Split(tok.Value, "\n") {
			if i > 0 {
				line++
			}
			if line >= len(out) {
				return out
			}
			if piece != "" {
				sp.Text = piece
				out[line] = append(out[line], sp)
			}
		}
	}
	return out
}

type styleKey struct {
	fg, bg               string
	bold, italic, strike bool
}

type Painter struct {
	cache map[styleKey]lipgloss.Style
}

func NewPainter() *Painter { return &Painter{cache: map[styleKey]lipgloss.Style{}} }

func (p *Painter) style(k styleKey) lipgloss.Style {
	if s, ok := p.cache[k]; ok {
		return s
	}
	s := lipgloss.NewStyle().Bold(k.bold).Italic(k.italic).Strikethrough(k.strike)
	if k.fg != "" {
		s = s.Foreground(lipgloss.Color(k.fg))
	}
	if k.bg != "" {
		s = s.Background(lipgloss.Color(k.bg))
	}
	p.cache[k] = s
	return s
}

// Code renders spans into exactly width cells, skipping the first offset cells and expanding tabs.
func (p *Painter) Code(spans []Span, width, offset int, bg string) string {
	return p.CodeHL(spans, width, offset, bg, -1, -1, "")
}

// CodeHL is Code with the runes [hlStart, hlEnd) of the line drawn on hlBg.
func (p *Painter) CodeHL(spans []Span, width, offset int, bg string, hlStart, hlEnd int, hlBg string) string {
	var b strings.Builder
	col, used, ri := 0, 0, 0
	full := false
	for _, sp := range spans {
		if full {
			break
		}
		var seg strings.Builder
		segBg := bg
		flush := func() {
			if seg.Len() > 0 {
				b.WriteString(p.style(styleKey{fg: sp.Color, bg: segBg, bold: sp.Bold, italic: sp.Italic, strike: sp.Strike}).Render(seg.String()))
				seg.Reset()
			}
		}
		put := func(r rune, w int, cellBg string) bool {
			if col < offset {
				col += w
				return true
			}
			if used+w > width {
				return false
			}
			if cellBg != segBg {
				flush()
				segBg = cellBg
			}
			seg.WriteRune(r)
			col += w
			used += w
			return true
		}
		for _, r := range sp.Text {
			cellBg := bg
			if ri >= hlStart && ri < hlEnd {
				cellBg = hlBg
			}
			if sp.Bg != "" {
				cellBg = sp.Bg
			}
			ri++
			ok := true
			if r == '\t' {
				for n := TabWidth - col%TabWidth; n > 0 && ok; n-- {
					ok = put(' ', 1, cellBg)
				}
			} else {
				ok = put(r, runewidth.RuneWidth(r), cellBg)
			}
			if !ok {
				full = true
				break
			}
		}
		flush()
	}
	if used < width {
		b.WriteString(p.style(styleKey{bg: bg}).Render(strings.Repeat(" ", width-used)))
	}
	return b.String()
}

// ChangedRange returns the rune ranges where a and b differ once their common prefix and suffix are
// removed. ok is false when the lines share too little for a word-level highlight to help.
func ChangedRange(a, b string) (aStart, aEnd, bStart, bEnd int, ok bool) {
	ra, rb := []rune(a), []rune(b)
	pre := 0
	for pre < len(ra) && pre < len(rb) && ra[pre] == rb[pre] {
		pre++
	}
	suf := 0
	for suf < len(ra)-pre && suf < len(rb)-pre && ra[len(ra)-1-suf] == rb[len(rb)-1-suf] {
		suf++
	}
	indent := 0
	for indent < len(ra) && (ra[indent] == ' ' || ra[indent] == '\t') {
		indent++
	}
	// Shared indentation says nothing about the lines, so require a few shared characters beyond it.
	if max(pre-indent, 0)+suf < 2 {
		return 0, 0, 0, 0, false
	}
	return pre, len(ra) - suf, pre, len(rb) - suf, true
}

func (p *Painter) Text(s string, width int, fg, bg string, bold, italic bool) string {
	return p.Code([]Span{{Text: s, Color: fg, Bold: bold, Italic: italic}}, width, 0, bg)
}
