package state

import (
	"regexp"
	"strings"
)

// Label is a kind of review comment, after Conventional Comments (conventionalcomments.org): the
// label says what the author is asked to do, so a comment's tone and weight are clear.
type Label struct {
	Key      string
	Name     string
	Blocking bool
	// Tip says what a good comment of this kind contains.
	Tip string
}

var Labels = []Label{
	{"i", "issue", true, "what goes wrong, for which input, and what you would do instead"},
	{"s", "suggestion", false, "what to change, and why it would be better"},
	{"q", "question", false, "what you want to know; there may be a good reason"},
	{"n", "nitpick", false, "a small preference the author is free to ignore"},
	{"p", "praise", false, "what is done well, and why it is good"},
	{"t", "thought", false, "an idea to consider, not a request"},
}

func LabelByKey(key string) (Label, bool) {
	for _, l := range Labels {
		if l.Key == key {
			return l, true
		}
	}
	return Label{}, false
}

// Prefix is how a labelled comment starts: "issue (blocking): ".
func Prefix(name string, blocking bool) string {
	if blocking {
		return name + " (blocking): "
	}
	if name == "issue" || name == "suggestion" {
		return name + " (non-blocking): "
	}
	return name + ": "
}

var labelled = regexp.MustCompile(`^([a-z]+)(?: \(([a-z, -]+)\))?: `)

// CommentLabel reads the label a comment starts with, and whether it blocks the change. The name is
// empty for a comment without a label.
func CommentLabel(text string) (name string, blocking bool) {
	m := labelled.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return "", false
	}
	for _, l := range Labels {
		if l.Name == m[1] {
			return m[1], strings.Contains(m[2], "blocking") && !strings.Contains(m[2], "non-blocking")
		}
	}
	return "", false
}
