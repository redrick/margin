package tui

import (
	"strings"
	"testing"

	"github.com/redrick/margin/internal/render"
)

func TestPaintCellsCursorPastEnd(t *testing.T) {
	spans := []render.Span{{Text: "\tif x {"}}
	got := paintCells(spans, 7, func(i int) string {
		if i == 7 {
			return colInsCur
		}
		return ""
	})
	last := got[len(got)-1]
	if last.Text != " " || last.Bg != colInsCur {
		t.Fatalf("a cursor past the end gets a cell of its own: %+v", got)
	}
	got = paintCells(spans, 7, func(i int) string {
		if i == 0 {
			return colEditCur
		}
		return ""
	})
	var b strings.Builder
	for _, s := range got {
		b.WriteString(s.Text)
	}
	if b.String() != "    if x {" || got[0].Bg != colEditCur {
		t.Fatalf("a cursor on a tab takes its first cell and keeps its width: %q %+v", b.String(), got)
	}
}
