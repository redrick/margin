package main

import "testing"

func TestSections(t *testing.T) {
	doc := "# Answers\n\n## c1 · a.go:3\n> q\n\nanswer\n### detail\nmore\n## c2 · b.go:9\nsecond\n"
	got := sections(doc)
	if s := got["c1"]; doc[s[0]:s[1]] != "## c1 · a.go:3\n> q\n\nanswer\n### detail\nmore\n" {
		t.Errorf("c1 = %q", doc[s[0]:s[1]])
	}
	if s := got["c2"]; doc[s[0]:s[1]] != "## c2 · b.go:9\nsecond\n" {
		t.Errorf("c2 = %q", doc[s[0]:s[1]])
	}
	if len(got) != 2 {
		t.Errorf("sections = %v", got)
	}
}
