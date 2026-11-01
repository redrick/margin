package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/redrick/margin/internal/fsx"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/text"
)

// The comments note is the markdown file, beside the review, where the agent answers the reader's
// comments. The window shows it, lets the reader edit it, and replies under one comment's answer.

type AnswersView struct {
	Path     string            `json:"path"`
	Text     string            `json:"text"`
	Exists   bool              `json:"exists"`
	Sections map[string]string `json:"sections"`
}

// Answers reads the comments note of the open review.
func (r *Reviews) Answers() (*AnswersView, error) {
	path := r.current()
	if path == "" {
		return nil, errors.New("no review open")
	}
	v := &AnswersView{Path: review.CommentsPath(path), Sections: map[string]string{}}
	data, err := os.ReadFile(v.Path)
	if errors.Is(err, os.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return nil, err
	}
	v.Text, v.Exists = string(data), true
	for id, sec := range sections(v.Text) {
		v.Sections[id] = v.Text[sec[0]:sec[1]]
	}
	return v, nil
}

// SaveAnswers replaces the comments note with the reader's edit.
func (r *Reviews) SaveAnswers(body string) (*AnswersView, error) {
	path := r.current()
	if path == "" {
		return nil, errors.New("no review open")
	}
	if err := fsx.WriteFileAtomic(review.CommentsPath(path), []byte(body), 0o644); err != nil {
		return nil, err
	}
	return r.Answers()
}

// Reply adds the reader's reply under the agent's answer to comment id and tells the agent.
func (r *Reviews) Reply(id, body string) (*AnswersView, error) {
	path := r.current()
	body = text.Sanitize(strings.TrimSpace(body))
	if path == "" || body == "" {
		return nil, errors.New("the reply is empty")
	}
	if !r.agents.Running(path) {
		return nil, errors.New("start the agent first, so it can answer the reply")
	}
	note := review.CommentsPath(path)
	data, err := os.ReadFile(note)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	doc := string(data)
	reply := "\n**Reply from the reader:** " + strings.ReplaceAll(body, "\n", "\n> ") + "\n"
	if sec, ok := sections(doc)[id]; ok {
		end := sec[1]
		doc = strings.TrimRight(doc[:end], "\n") + "\n" + reply + "\n" + doc[end:]
	} else {
		doc = strings.TrimRight(doc, "\n") + "\n\n## " + id + "\n" + reply
	}
	if err := fsx.WriteFileAtomic(note, []byte(doc), 0o644); err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("[margin reply] The reader replied to your answer to %s in %s: %s — Answer under the reply in that note. "+
		"Change code only if the reply explicitly asks for it. If your short answer changes, edit the note that resolved %s in the review file.",
		id, note, strings.Join(strings.Fields(body), " "), id)
	if err := r.agents.Send(path, msg); err != nil {
		return nil, fmt.Errorf("saved in the note, but not sent: %w", err)
	}
	return r.Answers()
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
