package coverage

import (
	"slices"
	"strings"
	"testing"
)

func TestProfileToTest(t *testing.T) {
	profile := `mode: set
example.com/m/calc/calc.go:3.24,4.11 1 1
example.com/m/calc/calc.go:4.11,6.3 1 0
example.com/m/calc/calc.go:7.2,7.14 1 1
example.com/m/other/other.go:3.1,5.2 1 1
`
	blocks, err := ParseProfile(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	mods := []Module{{Path: "example.com/m", Dir: "go"}, {Path: "example.com/m/other", Dir: "vendored"}}
	keep := map[string]bool{"go/calc/calc.go": true}
	test, exec := FromBlocks("TestAdd", "./calc", blocks, mods, keep)
	if got := test.Lines["go/calc/calc.go"]; !slices.Equal(got, []int{3, 4, 7}) {
		t.Errorf("run lines = %v", got)
	}
	if got := SortedLines(exec["go/calc/calc.go"]); !slices.Equal(got, []int{3, 4, 5, 6, 7}) {
		t.Errorf("executable = %v", got)
	}
	if len(test.Lines) != 1 {
		t.Errorf("files outside the review should be dropped: %v", test.Lines)
	}
	if rel, ok := Rel(mods, "example.com/m/other/x.go"); !ok || rel != "vendored/x.go" {
		t.Errorf("longest module path should win, got %q", rel)
	}

	if _, err := ParseProfile(strings.NewReader("hello\n")); err == nil {
		t.Error("a file without mode: is not a profile")
	}
	if p := ModulePath([]byte("// x\nmodule \"example.com/q\"\n\ngo 1.24\n")); p != "example.com/q" {
		t.Errorf("module path = %q", p)
	}
}

func TestMerge(t *testing.T) {
	s := &Set{Tests: []Test{
		{Name: "TestOld", Source: SourceGo},
		{Name: "test_py", Source: "agent", Lines: map[string][]int{"a.py": {1}}},
	}}
	s.Merge(&Set{Tests: []Test{{Name: "TestNew"}}}, SourceGo, true)
	var names []string
	for _, tc := range s.Tests {
		names = append(names, tc.Name)
	}
	if !slices.Equal(names, []string{"TestNew", "test_py"}) {
		t.Fatalf("a fresh Go run should drop old Go tests and keep the agent's: %v", names)
	}
	s.Merge(&Set{Tests: []Test{{Name: "test_py", Lines: map[string][]int{"a.py": {2}}}}}, "agent", false)
	if len(s.Tests) != 2 || s.Tests[1].Lines["a.py"][0] != 2 {
		t.Fatalf("a test with the same name replaces the old one: %+v", s.Tests)
	}
	if _, err := Parse([]byte(`{"tests": [{"name": "x", "lines": {"a.go": [0]}}]}`)); err == nil {
		t.Error("line 0 should be rejected")
	}
}
