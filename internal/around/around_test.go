package around

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/source"
	"github.com/redrick/margin/internal/testutil"
)

func build(t *testing.T, path string) (*doc.Doc, *source.Files) {
	t.Helper()
	r, err := review.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	files, err := source.Open(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(files.Close)
	return doc.Build(r, files), files
}

func TestSymbolsAndUses(t *testing.T) {
	d, files := build(t, testutil.Example(t))
	_, st := d.Station("reserve")
	rep := Study(d, st, files)
	if rep.Err != nil {
		t.Fatal(rep.Err)
	}
	if len(rep.Symbols) == 0 || rep.Symbols[0].Name != "Reserve" || rep.Symbols[0].Removed || rep.Symbols[0].Line != 31 {
		t.Fatalf("reserve changes Store.Reserve, got %+v", rep.Symbols)
	}
	uses := rep.Symbols[0].Uses
	if len(uses) == 0 || uses[0].File != "inventory/stock_test.go" || !strings.Contains(uses[0].Text, ".Reserve(") {
		t.Fatalf("the tests call Reserve outside the tour, got %+v", uses)
	}

	_, st = d.Station("removed")
	rep = Study(d, st, files)
	if len(rep.Symbols) != 1 || rep.Symbols[0].Name != "ReleaseAll" || !rep.Symbols[0].Removed || len(rep.Symbols[0].Uses) != 0 {
		t.Fatalf("ReleaseAll is removed and nothing uses it any more, got %+v", rep.Symbols)
	}

	_, st = d.Station("audit")
	if syms := Symbols(st); len(syms) != 0 {
		t.Errorf("audit.go is new, so nothing outside can depend on it yet: %+v", syms)
	}
}

func TestParseBlame(t *testing.T) {
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	out := a + " 1 1 2\nauthor Ann\nauthor-time 1700000000\nsummary Add stock\n\tline one\n" +
		a + " 2 2\n\tline two\n" +
		b + " 5 3 1\nauthor Bob\nauthor-time 1710000000\nsummary Fix \x1b[31mred\x1b[0m\n\tline three\n"
	got := ParseBlame(out)
	if len(got) != 2 || got[0].Lines != 2 || got[0].Author != "Ann" || got[0].Date != "2023-11-14" || got[1].Author != "Bob" {
		t.Fatalf("got %+v", got)
	}
	if strings.Contains(got[1].Subject, "\x1b") {
		t.Errorf("subjects must be sanitized: %q", got[1].Subject)
	}
	if s := spans(map[int]bool{3: true, 4: true, 9: true}); len(s) != 2 || s[0] != [2]int{3, 4} || s[1] != [2]int{9, 9} {
		t.Errorf("spans = %v", s)
	}
}

// TestHistoryOfThisRepo blames two early commits of margin itself, read-only.
func TestHistoryOfThisRepo(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	repo := filepath.Join(filepath.Dir(file), "..", "..")
	for _, c := range []string{"cdb551f", "bc136e0"} {
		if exec.Command("git", "-C", repo, "cat-file", "-e", c+"^{commit}").Run() != nil {
			t.Skip("the repository's early history is not available")
		}
	}
	path := filepath.Join(t.TempDir(), "h.review.yaml")
	data := "version: 1\nrepo: " + repo + "\nbase: cdb551f\nhead: bc136e0\ntitle: h\nstations:\n" +
		"  - id: render\n    title: render\n    parts: [{file: internal/render/render.go, hunks: true}]\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	d, files := build(t, path)
	_, st := d.Station("render")
	hist, err := History(files.Repo, files.BaseCommit(), st)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].Subject != "init" || hist[0].Lines == 0 || !strings.HasPrefix(hist[0].SHA, "cdb551f") {
		t.Fatalf("the replaced lines all come from the first commit, got %+v", hist)
	}
	msg, err := Message(files.Repo, hist[0].SHA)
	if err != nil || !strings.Contains(msg, "init") {
		t.Fatalf("message = %q, %v", msg, err)
	}
}
