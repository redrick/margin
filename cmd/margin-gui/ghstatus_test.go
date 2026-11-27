package main

import (
	"strings"
	"testing"

	"github.com/redrick/margin/internal/github"
)

func TestGitHubNote(t *testing.T) {
	t1 := &github.Thread{ID: "PRRT_a", Path: "x.go", Line: 4, Comments: []github.TComment{
		{Author: "me", Body: "rename this\n## not a heading"}, {Author: "bob", Body: "fixed in abc123"}}}
	t2 := &github.Thread{ID: "PRRT_b", Path: "y.go", OriginalLine: 9, Outdated: true, Resolved: true,
		Comments: []github.TComment{{Author: "me", Body: "why?"}}}

	doc := putGHSection("# head\n", t1.ID, ghSection(t1))
	doc = putGHSection(doc, t2.ID, ghSection(t2))
	ids, secs := ghSections(doc)
	if strings.Join(ids, ",") != "PRRT_a,PRRT_b" {
		t.Fatalf("ids %v in\n%s", ids, doc)
	}
	if c := ghCheck(secs["PRRT_a"]); c.Status != ghPending || c.Text != "" {
		t.Fatalf("fresh section %+v", c)
	}
	if !strings.Contains(secs["PRRT_a"], "> **bob:** fixed in abc123") || !strings.Contains(secs["PRRT_b"], "`y.go:9 (outdated") {
		t.Fatalf("sections\n%s", doc)
	}

	// The agent answers the first; asking again about the second keeps that answer.
	doc = strings.Replace(doc, "Status: pending\n", "**Status:** Partly.\n\nRenamed, but `x.go:12` still uses the old name.\n", 1)
	doc = putGHSection(doc, t2.ID, ghSection(t2))
	_, secs = ghSections(doc)
	c := ghCheck(secs["PRRT_a"])
	if c.Status != "partly" || c.Text != "Renamed, but `x.go:12` still uses the old name." {
		t.Fatalf("answer %+v", c)
	}
	if ghCheck(secs["PRRT_b"]).Status != ghPending || strings.Count(doc, "## PRRT_b") != 1 {
		t.Fatalf("second ask\n%s", doc)
	}

	for in, want := range map[string]string{"Status: done": "done", "status: Not done — x": "not done", "Status: fixed": "done", "Status: hmm": "unclear"} {
		if got := ghCheck("## T\n\n" + in + "\n").Status; got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
