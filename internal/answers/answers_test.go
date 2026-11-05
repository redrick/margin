package answers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redrick/margin/internal/review"
)

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

func TestReply(t *testing.T) {
	rv := filepath.Join(t.TempDir(), "x.review.yaml")
	os.WriteFile(review.CommentsPath(rv), []byte("## c1 · a.go:3\nanswer one\n## c2 · b.go:9\nanswer two\n"), 0o644)
	msg, err := Reply(rv, "c1", "why not?")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(msg, "[margin reply]") || !strings.Contains(msg, "why not?") {
		t.Fatalf("msg %q", msg)
	}
	n, _ := Load(rv)
	if got := n.Text[n.Sections["c1"][0]:n.Sections["c1"][1]]; !strings.Contains(got, "**Reply from the reader:** why not?") {
		t.Fatalf("the reply goes under c1's answer: %q", got)
	}
	if ids := n.IDs(); len(ids) != 2 || ids[0] != "c1" {
		t.Fatalf("ids %v", ids)
	}
	if _, err := Reply(rv, "c1", "  "); err == nil {
		t.Fatal("an empty reply is refused")
	}
}
