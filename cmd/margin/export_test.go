package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/testutil"
)

// capture runs margin with args and returns what it printed on stdout.
func capture(t *testing.T, args ...string) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	code := run(args)
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	if code != 0 {
		t.Fatalf("margin %v exit %d: %s", args, code, out)
	}
	return string(out)
}

func TestResolveAndExport(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	path := testutil.Example(t)
	st, err := state.Load(review.StatePath(path))
	if err != nil {
		t.Fatal(err)
	}
	st.Questions = []state.Question{
		{ID: "c1", Kind: state.KindComment, Station: "reserve", File: "inventory/stock.go", Line: 32, Needle: "if qty <= 0 {", Text: "log the rejected quantity"},
		{ID: "c2", Kind: state.KindComment, Draft: true, Station: "reserve", File: "inventory/stock.go", Line: 36, Needle: "defer s.mu.Unlock()", Text: "not sent yet"},
	}
	r, err := review.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range r.Stations {
		for _, n := range s.Notes {
			if n.Kind == "issue" {
				st.Flagged[n.Key(s.ID)] = true
			}
		}
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}

	if code := run([]string{"resolve", "--review", path, "c2", "done"}); code == 0 {
		t.Fatal("a draft the reader has not sent cannot be resolved")
	}
	if out := capture(t, "questions", "--review", path); !strings.Contains(out, "c1  comment") || strings.Contains(out, "c2") {
		t.Fatalf("questions should list the sent comment only:\n%s", out)
	}
	if out := capture(t, "resolve", "--review", path, "c1", "Logged", "now."); !strings.Contains(out, "c1 resolved") {
		t.Fatalf("resolve output: %s", out)
	}

	md := capture(t, "export", "--review", path)
	for _, want := range []string{
		"# Review: Stock reservations get an audit trail",
		"## Compared with what was asked",
		"`inventory/stock.go:32`: log the rejected quantity\n\n  > Logged now.",
		"**Breaking signature.**",
		"## Decisions",
		"- [ ] Should a store without an audit log still be allowed",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown export misses %q:\n%s", want, md)
		}
	}

	var gh struct {
		Event    string `json:"event"`
		Body     string `json:"body"`
		Comments []struct {
			Path string `json:"path"`
			Line int    `json:"line"`
			Side string `json:"side"`
			Body string `json:"body"`
		} `json:"comments"`
	}
	if err := json.Unmarshal([]byte(capture(t, "export", "--format", "github", "--review", path)), &gh); err != nil {
		t.Fatal(err)
	}
	if gh.Event != "COMMENT" || len(gh.Comments) != 3 || !strings.Contains(gh.Body, "Decisions") {
		t.Fatalf("github export = %+v", gh)
	}
	if c := gh.Comments[0]; c.Path != "inventory/stock.go" || c.Line != 32 || c.Side != "RIGHT" {
		t.Errorf("first comment = %+v", c)
	}
}
