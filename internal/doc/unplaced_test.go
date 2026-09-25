package doc_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/source"
)

const helper = `func Helper(x int) int {
	y := x * 2
	z := y + 3
	return z
}
`

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func buildDirs(t *testing.T, base, cur map[string]string, yaml string) *doc.Doc {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, filepath.Join(dir, "base"), base)
	writeTree(t, filepath.Join(dir, "current"), cur)
	path := filepath.Join(dir, "x.review.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nrepo: current\nbase_dir: base\ntitle: t\n"+yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := review.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	files, err := source.Open(r)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	return doc.Build(r, files)
}

func TestUnplacedIgnoredAndMoved(t *testing.T) {
	ignoreFile := "# generated\n*.pb.go\n"
	base := map[string]string{
		".margin/ignore": ignoreFile,
		"a.go":           "package a\n\nfunc Keep() int {\n\treturn 1\n}\n\n" + helper,
	}
	cur := map[string]string{
		".margin/ignore": ignoreFile,
		"a.go":           "package a\n\nfunc Keep() int {\n\treturn 1\n}\n",
		"b.go":           "package a\n\n" + helper,
		"c.go":           "package a\n\nvar Forgotten = 1\n",
		"gen.pb.go":      "package a\n",
		"logo.png":       "\x89PNG\x00\x00",
	}
	d := buildDirs(t, base, cur, `stations:
  - id: a
    title: a
    parts: [{file: a.go, hunks: true}]
  - id: b
    title: b
    parts: [{file: b.go, hunks: true}]
`)

	_, un := d.Station("unplaced")
	if un == nil || !un.Auto || len(un.Parts) != 1 || un.Parts[0].Spec.File != "c.go" {
		t.Fatalf("c.go is in no stop and should get the unplaced stop, got %+v", un)
	}
	if len(d.Problems) != 1 || !strings.Contains(d.Problems[0].Msg, "c.go") {
		t.Errorf("lint should report the unplaced file, problems = %+v", d.Problems)
	}
	if len(d.Ignored) != 1 || d.Ignored[0].Path != "gen.pb.go" {
		t.Errorf("ignored = %+v, want gen.pb.go", d.Ignored)
	}
	if len(d.Unshown) != 1 || d.Unshown[0].Path != "logo.png" || d.Unshown[0].Reason != "binary file" {
		t.Errorf("unshown = %+v, want logo.png as a binary file", d.Unshown)
	}

	_, b := d.Station("b")
	p := b.Parts[0]
	for l := 2; l <= 6; l++ {
		if !strings.HasPrefix(p.Moved[l], "moved from a.go, old line 7") {
			t.Fatalf("b.go:%d should be marked as moved from a.go, got %q", l+1, p.Moved[l])
		}
	}
	if p.Moved[0] != "" {
		t.Errorf("the package line is not part of the move")
	}
	_, a := d.Station("a")
	var to []string
	for _, g := range a.Parts[0].GhostMoved {
		for _, dest := range g {
			to = append(to, dest)
		}
	}
	if len(to) != 5 || to[0] != "moved to b.go:3" {
		t.Errorf("the removed Helper should point at its new place, got %v", to)
	}

	d = buildDirs(t, base, cur, `stations:
  - id: a
    title: a
    parts: [{file: a.go, hunks: true}]
  - id: b
    title: b
    parts: [{file: b.go, hunks: true}]
skip:
  - what: c.go
    why: a leftover
  - what: "*.png"
`)
	if _, un := d.Station("unplaced"); un != nil || len(d.Problems) != 0 {
		t.Errorf("files listed in skip are accounted for, got stop %v, problems %+v", un, d.Problems)
	}
}
