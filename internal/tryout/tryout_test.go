package tryout

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/redrick/margin/internal/testrun"
)

func setup(t *testing.T, content string) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return repo
}

func read(t *testing.T, repo string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repo, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplyRestore(t *testing.T) {
	const src = "one\r\ntwo\r\nthree\r\n"
	repo := setup(t, src)
	tr, err := Apply(repo, []Edit{{File: "a.go", Line: 3, Old: "three", New: "3"}, {File: "a.go", Line: 1, Old: "one", New: "1a\n1b"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, repo); got != "1a\r\n1b\r\ntwo\r\n3\r\n" {
		t.Fatalf("applied %q", got)
	}
	if fi, _ := os.Stat(filepath.Join(repo, "a.go")); fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if err := tr.Restore(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, repo); got != src {
		t.Fatalf("restored %q", got)
	}
	if left, _ := os.ReadDir(Dir()); len(left) != 0 {
		t.Fatalf("backups left: %v", left)
	}
}

func TestRestoreKeepsLaterChanges(t *testing.T) {
	repo := setup(t, "a\nb\nc\n")
	tr, err := Apply(repo, []Edit{{File: "a.go", Line: 2, Old: "b", New: "B"}})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "a.go"), []byte("a\nB\nc\nadded\n"), 0o644)
	if err := tr.Restore(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, repo); got != "a\nb\nc\nadded\n" {
		t.Fatalf("got %q", got)
	}
}

func TestRestoreLostEdit(t *testing.T) {
	repo := setup(t, "a\nb\n")
	tr, _ := Apply(repo, []Edit{{File: "a.go", Line: 2, Old: "b", New: "B"}})
	os.WriteFile(filepath.Join(repo, "a.go"), []byte("rewritten\n"), 0o644)
	if err := tr.Restore(); err == nil || !strings.Contains(err.Error(), "changed during the test run") {
		t.Fatalf("err %v", err)
	}
	if left, _ := os.ReadDir(Dir()); len(left) != 1 {
		t.Fatal("the backup should stay when the edit could not be undone")
	}
}

func TestCheckRefusesStaleLine(t *testing.T) {
	repo := setup(t, "a\nb\n")
	if err := Check(repo, []Edit{{File: "a.go", Line: 2, Old: "x", New: "y"}}); err == nil {
		t.Fatal("want an error when the line on disk differs")
	}
	if err := Check(repo, []Edit{{File: "a.go", Line: 3, Old: "", New: "y"}}); err == nil {
		t.Fatal("want an error past the last line")
	}
	if err := Check(repo, []Edit{{File: "../a.go", Line: 1, Old: "a", New: "y"}}); err == nil {
		t.Fatal("want an error outside the repository")
	}
}

func TestRecover(t *testing.T) {
	repo := setup(t, "a\nb\n")
	tr, err := Apply(repo, []Edit{{File: "a.go", Line: 1, Old: "a", New: "A"}})
	if err != nil {
		t.Fatal(err)
	}
	// As if this margin had died: the backup names a process that is gone.
	liveMu.Lock()
	delete(live, tr)
	liveMu.Unlock()
	rewritePID(t, 0)
	fixed, err := Recover()
	if err != nil || len(fixed) != 1 {
		t.Fatalf("fixed %v err %v", fixed, err)
	}
	if got := read(t, repo); got != "a\nb\n" {
		t.Fatalf("got %q", got)
	}
}

func rewritePID(t *testing.T, pid int) {
	entries, _ := os.ReadDir(Dir())
	for _, e := range entries {
		p := filepath.Join(Dir(), e.Name())
		b, _ := os.ReadFile(p)
		b = []byte(strings.Replace(string(b), `"pid":`+strconv.Itoa(os.Getpid()), `"pid":`+strconv.Itoa(pid), 1))
		os.WriteFile(p, b, 0o600)
	}
}

func TestRunComparesAndRestores(t *testing.T) {
	repo := setup(t, "ok\n")
	var r testrun.Runner
	var seen []string
	Run(&r, Plan{Dir: repo, Compare: true, Edits: []Edit{{File: "a.go", Line: 1, Old: "ok", New: "bad"}},
		Jobs: []testrun.Job{{Command: `grep -q ok a.go && echo '--- PASS: TestA (0s)' || { echo '--- FAIL: TestA (0s)'; exit 1; }`}}},
		func(e Event) {
			if e.Kind == "result" || e.Kind == "applied" || e.Kind == "restored" {
				seen = append(seen, e.Phase+" "+e.Kind+" "+e.Status+e.Err)
			}
		})
	want := "base result pass|try applied |try result fail|try restored "
	if got := strings.Join(seen, "|"); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if got := read(t, repo); got != "ok\n" {
		t.Fatalf("file not restored: %q", got)
	}
	v := Judge(map[string]string{"TestA": "pass"}, map[string]string{"TestA": "fail"}, nil)
	if len(v.Caught) != 1 || v.String() != "1 caught it" {
		t.Fatalf("verdict %+v %q", v, v.String())
	}
}

func TestBlockDeleteRestore(t *testing.T) {
	const src = "a\nb\nc\nd\ne\n"
	repo := setup(t, src)
	tr, err := Apply(repo, []Edit{{File: "a.go", Line: 2, Old: "b\nc\nd", New: ""}})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, repo); got != "a\ne\n" {
		t.Fatalf("applied %q", got)
	}
	os.WriteFile(filepath.Join(repo, "a.go"), []byte("a\ne\nadded\n"), 0o644)
	if err := tr.Restore(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, repo); got != "a\nb\nc\nd\ne\nadded\n" {
		t.Fatalf("restored %q", got)
	}
}

func TestBlockReplaceAndChecks(t *testing.T) {
	repo := setup(t, "a\nb\nc\n")
	tr, err := Apply(repo, []Edit{{File: "a.go", Line: 1, Old: "a\nb", New: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, repo); got != "x\nc\n" {
		t.Fatalf("applied %q", got)
	}
	tr.Restore()
	if err := Check(repo, []Edit{{File: "a.go", Line: 2, Old: "b\nX", New: ""}}); err == nil {
		t.Fatal("want an error when a line of the block differs")
	}
	if err := Check(repo, []Edit{{File: "a.go", Line: 2, Old: "b\nc\nd", New: ""}}); err == nil {
		t.Fatal("want an error past the last line")
	}
	if err := Check(repo, []Edit{{File: "a.go", Line: 1, Old: "a\nb", New: ""}, {File: "a.go", Line: 2, Old: "b", New: "y"}}); err == nil {
		t.Fatal("want an error for overlapping edits")
	}
}
