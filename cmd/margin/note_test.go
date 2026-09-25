package main

import (
	"os"
	"strings"
	"testing"

	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/testutil"
)

func TestNoteAnchorsOnLine(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	path := testutil.Example(t)

	if code := run([]string{"note", "--review", path, "inventory/stock.go:37", "reads", "once"}); code != 0 {
		t.Fatalf("note exit %d", code)
	}
	if code := run([]string{"note", "--review", path, "inventory/stock.go:26", "not shown"}); code == 0 {
		t.Fatal("a line outside every station should be refused")
	}

	r, err := review.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	_, st := r.Station("reserve")
	n := st.Notes[len(st.Notes)-1]
	if n.At != "available := s.items[sku]" || n.Text != "reads once" || n.File != "inventory/stock.go" {
		t.Fatalf("note = %+v", n)
	}
	if code := run([]string{"lint", path}); code != 0 {
		t.Fatal("review no longer lints")
	}
}

func TestHunksReview(t *testing.T) {
	path := testutil.Example(t)
	data := `version: 1
repo: current
base_dir: base
title: hunks
stations:
  - id: stock
    title: stock.go
    parts:
      - file: inventory/stock.go
        hunks: true
  - id: gone
    title: deleted
    parts:
      - file: scripts/report.py
        hunks: true
`
	hunks := strings.Replace(path, "example.review.yaml", "hunks.review.yaml", 1)
	if err := os.WriteFile(hunks, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"lint", hunks}); code == 0 {
		t.Fatal("lint should fail while audit.go and stock_test.go are in no stop")
	}
	data += "skip:\n  - what: inventory/audit.go\n  - what: inventory/*_test.go\n"
	if err := os.WriteFile(hunks, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"lint", hunks}); code != 0 {
		t.Fatal("hunks review should lint once every change is placed")
	}
	if code := run([]string{"note", "--review", hunks, "inventory/stock.go:42", "logged under the lock"}); code != 0 {
		t.Fatal("note on a changed line should work")
	}
}
