package anchor

import (
	"strings"
	"testing"

	"github.com/redrick/margin/internal/review"
)

func split(s string) []string { return strings.Split(strings.TrimSuffix(s, "\n"), "\n") }

const goSrc = `package x

// Reserve does things.
func (s *Store) Reserve() error {
	return nil
}

func Reserve() {}

type Store struct {
	a int
}
`

const pySrc = `class Report:
    def summarize(self, entries):
        total = 0
        for e in entries:
            total += e

        return total

    def other(self):
        pass


def summarize(x):
    return x
`

const jsSrc = `export async function load(path) {
  const x = "}";
  return x;
}
const handler = (req) => {
  return load(req.path);
};
`

func TestResolve(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		src     string
		part    review.Part
		want    Range
		wantErr string
	}{
		{name: "go method", path: "x.go", src: goSrc, part: review.Part{Func: "Store.Reserve"}, want: Range{2, 5}},
		{name: "go pointer receiver syntax", path: "x.go", src: goSrc, part: review.Part{Func: "(*Store).Reserve"}, want: Range{2, 5}},
		{name: "go type", path: "x.go", src: goSrc, part: review.Part{Func: "Store"}, want: Range{9, 11}},
		{name: "go ambiguous", path: "x.go", src: goSrc, part: review.Part{Func: "Reserve"}, wantErr: "matches 2"},
		{name: "go missing", path: "x.go", src: goSrc, part: review.Part{Func: "Nope"}, wantErr: "not found"},
		{name: "python qualified method", path: "r.py", src: pySrc, part: review.Part{Func: "Report.summarize"}, want: Range{1, 6}},
		{name: "python method", path: "r.py", src: pySrc, part: review.Part{Func: "other"}, want: Range{8, 9}},
		{name: "python ambiguous", path: "r.py", src: pySrc, part: review.Part{Func: "summarize"}, wantErr: "matches 2"},
		{name: "js function with brace in string", path: "a.js", src: jsSrc, part: review.Part{Func: "load"}, want: Range{0, 3}},
		{name: "js arrow", path: "a.js", src: jsSrc, part: review.Part{Func: "handler"}, want: Range{4, 6}},
		{name: "from without to", path: "a.js", src: jsSrc, part: review.Part{From: "const x"}, want: Range{1, 1}},
		{name: "from to", path: "a.js", src: jsSrc, part: review.Part{From: "function load", To: "return x"}, want: Range{0, 2}},
		{name: "from ambiguous", path: "a.js", src: jsSrc, part: review.Part{From: "return"}, wantErr: "matches 2"},
		{name: "lines", path: "a.js", src: jsSrc, part: review.Part{Lines: "2-3"}, want: Range{1, 2}},
		{name: "lines out of range", path: "a.js", src: jsSrc, part: review.Part{Lines: "9"}, wantErr: "outside"},
		{name: "whole file", path: "a.js", src: jsSrc, part: review.Part{}, want: Range{0, 6}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.path, split(tt.src), tt.part)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("range = %+v, want %+v", got, tt.want)
			}
		})
	}
}
