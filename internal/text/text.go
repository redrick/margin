package text

import (
	"strings"
	"unicode/utf8"
)

// Sanitize removes terminal control sequences and bidi overrides from untrusted file content,
// keeping newlines and tabs.
func Sanitize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if clean(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i, w := 0, 0; i < len(s); i += w {
		r, size := utf8.DecodeRuneInString(s[i:])
		w = size
		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteRune('�')
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case unsafe(r):
		case bidi(r):
			b.WriteRune('�')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Line is Sanitize for single-line input such as a typed question: newlines become spaces.
func Line(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\t", " ").Replace(s)
	return strings.TrimSpace(Sanitize(s))
}

func Lines(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func clean(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 0x20 && c != '\n' && c != '\t') || c == 0x7f || c >= 0x80 {
			return false
		}
	}
	return true
}

func unsafe(r rune) bool {
	return (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r < 0xa0)
}

func bidi(r rune) bool {
	return (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0x200e || r == 0x200f
}
