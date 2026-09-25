package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const small = `version: 1
repo: .
title: T
stations:
  # keep me
  - id: one
    title: One
    parts:
      - file: a.go
  - id: two
    title: Two
    parts:
      - file: b.go
    notes:
      - at: x
        text: existing
`

func TestAppendNote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.review.yaml")
	if err := os.WriteFile(path, []byte(small), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := AppendNote(path, "one", Note{At: "func a", QID: "q1", Q: "why?", Text: "because\nof reasons"})
	if err != nil {
		t.Fatal(err)
	}
	if idx != 0 {
		t.Errorf("index = %d, want 0", idx)
	}
	if idx, err = AppendNote(path, "two", Note{At: "y", Text: "second"}); err != nil || idx != 1 {
		t.Fatalf("second append: idx=%d err=%v", idx, err)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "# keep me") {
		t.Errorf("comment lost:\n%s", data)
	}
	r, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	n := r.Stations[0].Notes[0]
	if n.QID != "q1" || n.Text != "because\nof reasons" {
		t.Errorf("note = %+v", n)
	}
	if got := len(r.Stations[1].Notes); got != 2 {
		t.Errorf("station two notes = %d, want 2", got)
	}

	if _, err := AppendNote(path, "missing", Note{At: "x", Text: "y"}); err == nil {
		t.Error("expected error for unknown station")
	}
}

func TestValidate(t *testing.T) {
	bad := `version: 1
repo: .
title: T
base: --output=/tmp/x
stations:
  - id: "a:b"
    title: A
    parts:
      - file: ../outside.go
        func: F
        lines: 1-2
`
	_, err := Parse("r.yaml", []byte(bad))
	if err == nil {
		t.Fatal("expected validation errors")
	}
	for _, want := range []string{"must not start with '-'", "must not contain ':'", "inside the repo", "only one of"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
	enums := "version: 1\nrepo: .\ntitle: T\nstations:\n  - id: a\n    title: A\n    risk: extreme\n    parts: [{file: a.go}]\n    notes: [{at: x, text: y, kind: blocker}]\n"
	if _, err := Parse("r.yaml", []byte(enums)); err == nil || !strings.Contains(err.Error(), "risk must be") || !strings.Contains(err.Error(), "kind must be") {
		t.Errorf("unknown risk and kind should fail: %v", err)
	}
	if _, err := Parse("r.yaml", []byte("version: 1\nrepo: .\ntitel: typo\nstations: []\n")); err == nil {
		t.Error("unknown field should fail")
	}
}

func TestSetScalarMultiline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.review.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nrepo: .\ntitle: t\nstations: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetScalar(path, "asked", "Fix login\n\nSessions expired early."); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "asked: |-\n  Fix login\n\n  Sessions expired early.") {
		t.Fatalf("multi-line text should be a literal block:\n%s", data)
	}
	r, err := Load(path)
	if err != nil || r.Asked != "Fix login\n\nSessions expired early." {
		t.Fatalf("asked = %q, %v", r.Asked, err)
	}
}
