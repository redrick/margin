package state

import "testing"

func TestCommentLabel(t *testing.T) {
	for _, c := range []struct {
		text     string
		name     string
		blocking bool
	}{
		{"issue (blocking): nil map write", "issue", true},
		{"issue (non-blocking): could be clearer", "issue", false},
		{"suggestion (security, blocking): hash it", "suggestion", true},
		{"praise: lovely test", "praise", false},
		{"just a remark: with a colon", "", false},
		{"why is this here?", "", false},
	} {
		name, blocking := CommentLabel(c.text)
		if name != c.name || blocking != c.blocking {
			t.Errorf("CommentLabel(%q) = %q, %v", c.text, name, blocking)
		}
	}
	if p := Prefix("issue", true); p != "issue (blocking): " {
		t.Errorf("prefix = %q", p)
	}
	if p := Prefix("praise", false); p != "praise: " {
		t.Errorf("prefix = %q", p)
	}
}
