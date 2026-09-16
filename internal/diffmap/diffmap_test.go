package diffmap

import (
	"reflect"
	"strings"
	"testing"
)

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func TestCompute(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		current string
		kinds   []Kind
		ghosts  map[int][]string
	}{
		{
			name:    "identical",
			base:    "a\nb\nc",
			current: "a\nb\nc",
			kinds:   []Kind{Same, Same, Same},
			ghosts:  map[int][]string{},
		},
		{
			name:    "pure insert",
			base:    "a\nc",
			current: "a\nb\nc",
			kinds:   []Kind{Same, Added, Same},
			ghosts:  map[int][]string{},
		},
		{
			name:    "pure delete",
			base:    "a\nb\nc",
			current: "a\nc",
			kinds:   []Kind{Same, Same},
			ghosts:  map[int][]string{1: {"b"}},
		},
		{
			name:    "replace",
			base:    "a\nold\nc",
			current: "a\nnew1\nnew2\nc",
			kinds:   []Kind{Same, Changed, Changed, Same},
			ghosts:  map[int][]string{1: {"old"}},
		},
		{
			name:    "delete at end",
			base:    "a\nb",
			current: "a",
			kinds:   []Kind{Same},
			ghosts:  map[int][]string{1: {"b"}},
		},
		{
			name:    "from empty",
			base:    "",
			current: "x\ny",
			kinds:   []Kind{Added, Added},
			ghosts:  map[int][]string{},
		},
		{
			name:    "removed function slides below the shared brace",
			base:    "f {\na\nreturn\n}\n\ng {\nb\n}",
			current: "f {\nA\nreturn\n}",
			kinds:   []Kind{Same, Changed, Same, Same},
			ghosts:  map[int][]string{1: {"a"}, 4: {"", "g {", "b", "}"}},
		},
		{
			name:    "added block slides below the shared blank",
			base:    "a\n\nb",
			current: "a\n\nx\n\nb",
			kinds:   []Kind{Same, Same, Added, Added, Same},
			ghosts:  map[int][]string{},
		},
		{
			name:    "interleaved",
			base:    "1\n2\n3\n4\n5",
			current: "1\n3\nx\n5\n6",
			kinds:   []Kind{Same, Same, Changed, Same, Added},
			ghosts:  map[int][]string{1: {"2"}, 2: {"4"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Compute(lines(tt.base), lines(tt.current))
			if !reflect.DeepEqual(m.Kinds, tt.kinds) {
				t.Errorf("kinds = %v, want %v", m.Kinds, tt.kinds)
			}
			if !reflect.DeepEqual(m.Ghosts, tt.ghosts) {
				t.Errorf("ghosts = %v, want %v", m.Ghosts, tt.ghosts)
			}
		})
	}
}

func TestComputePairsChangedLines(t *testing.T) {
	m := Compute(lines("a\nold one\nold two\nc"), lines("a\nnew one\nc"))
	if want := map[int]string{1: "old one"}; !reflect.DeepEqual(m.Pairs, want) {
		t.Fatalf("pairs = %v, want %v", m.Pairs, want)
	}
}

func TestComputeReconstructs(t *testing.T) {
	base := lines("package x\n\nfunc a() {\n\treturn 1\n}\n\nfunc b() {\n\treturn 2\n}")
	current := lines("package x\n\nimport \"fmt\"\n\nfunc a() {\n\tfmt.Println()\n\treturn 1\n}\n\nfunc c() {}")
	m := Compute(base, current)

	var rebuilt []string
	for i, l := range current {
		rebuilt = append(rebuilt, m.Ghosts[i]...)
		if m.Kinds[i] == Same {
			rebuilt = append(rebuilt, l)
		}
	}
	rebuilt = append(rebuilt, m.Ghosts[len(current)]...)
	if !reflect.DeepEqual(rebuilt, base) {
		t.Fatalf("same lines plus ghosts do not rebuild base:\n%q\n%q", rebuilt, base)
	}
}
