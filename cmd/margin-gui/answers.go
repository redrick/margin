package main

import (
	"errors"
	"fmt"

	"github.com/redrick/margin/internal/answers"
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
	n, err := answers.Load(path)
	if err != nil {
		return nil, err
	}
	v := &AnswersView{Path: n.Path, Text: n.Text, Exists: n.Exists, Sections: map[string]string{}}
	for id, sec := range n.Sections {
		v.Sections[id] = n.Text[sec[0]:sec[1]]
	}
	return v, nil
}

// SaveAnswers replaces the comments note with the reader's edit.
func (r *Reviews) SaveAnswers(body string) (*AnswersView, error) {
	path := r.current()
	if path == "" {
		return nil, errors.New("no review open")
	}
	if err := answers.Save(path, body); err != nil {
		return nil, err
	}
	return r.Answers()
}

// Reply adds the reader's reply under the agent's answer to comment id and tells the agent.
func (r *Reviews) Reply(id, body string) (*AnswersView, error) {
	path := r.current()
	if path == "" {
		return nil, errors.New("no review open")
	}
	if !r.agents.Running(path) {
		return nil, errors.New("start the agent first, so it can answer the reply")
	}
	msg, err := answers.Reply(path, id, body)
	if err != nil {
		return nil, err
	}
	if err := r.agents.Send(path, msg); err != nil {
		return nil, fmt.Errorf("saved in the note, but not sent: %w", err)
	}
	return r.Answers()
}
