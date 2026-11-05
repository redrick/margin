// Package answers reads and writes the comments note: the markdown file beside a review where the
// agent answers the reader's comments, and where the reader can edit or reply under an answer.
package answers

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/redrick/margin/internal/fsx"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/text"
)

type Note struct {
	Path   string
	Text   string
	Exists bool
	// Sections are where each comment's answer starts and ends in Text, by comment id.
	Sections map[string][2]int
}

// IDs are the answered comments in the order the note has them.
func (n *Note) IDs() []string {
	ids := make([]string, 0, len(n.Sections))
	for id := range n.Sections {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool { return n.Sections[ids[a]][0] < n.Sections[ids[b]][0] })
	return ids
}

// Load reads the comments note of the review at reviewPath; a note not written yet is empty.
func Load(reviewPath string) (*Note, error) {
	n := &Note{Path: review.CommentsPath(reviewPath), Sections: map[string][2]int{}}
	data, err := os.ReadFile(n.Path)
	if errors.Is(err, os.ErrNotExist) {
		return n, nil
	}
	if err != nil {
		return nil, err
	}
	n.Text, n.Exists = string(data), true
	n.Sections = sections(n.Text)
	return n, nil
}

// Save replaces the note with the reader's edit.
func Save(reviewPath, body string) error {
	return fsx.WriteFileAtomic(review.CommentsPath(reviewPath), []byte(body), 0o644)
}

// Reply writes the reader's reply under the answer to comment id, and returns the message that
// asks the agent to answer it.
func Reply(reviewPath, id, body string) (string, error) {
	body = text.Sanitize(strings.TrimSpace(body))
	if body == "" {
		return "", errors.New("the reply is empty")
	}
	n, err := Load(reviewPath)
	if err != nil {
		return "", err
	}
	doc := n.Text
	reply := "\n**Reply from the reader:** " + strings.ReplaceAll(body, "\n", "\n> ") + "\n"
	if sec, ok := n.Sections[id]; ok {
		end := sec[1]
		doc = strings.TrimRight(doc[:end], "\n") + "\n" + reply + "\n" + doc[end:]
	} else {
		doc = strings.TrimRight(doc, "\n") + "\n\n## " + id + "\n" + reply
	}
	if err := fsx.WriteFileAtomic(n.Path, []byte(doc), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("[margin reply] The reader replied to your answer to %s in %s: %s — Answer under the reply in that note. "+
		"Change code only if the reply explicitly asks for it. If your short answer changes, edit the note that resolved %s in the review file.",
		id, n.Path, strings.Join(strings.Fields(body), " "), id), nil
}

var heading = regexp.MustCompile(`(?m)^(#{1,6})\s+(.*)$`)

var commentID = regexp.MustCompile(`\bc\d+\b`)

// sections finds, for each comment id named in a heading, where its section starts and ends: up to
// the next heading of the same or a higher level.
func sections(doc string) map[string][2]int {
	out := map[string][2]int{}
	hs := heading.FindAllStringSubmatchIndex(doc, -1)
	for i, h := range hs {
		level := h[3] - h[2]
		id := commentID.FindString(doc[h[4]:h[5]])
		if id == "" {
			continue
		}
		end := len(doc)
		for _, next := range hs[i+1:] {
			if next[3]-next[2] <= level {
				end = next[0]
				break
			}
		}
		if _, seen := out[id]; !seen {
			out[id] = [2]int{h[0], end}
		}
	}
	return out
}
