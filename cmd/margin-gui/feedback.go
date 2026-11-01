package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/text"
	"github.com/redrick/margin/internal/tui"
	"github.com/redrick/margin/internal/verdict"
)

// ReaderView is what the reader decided so far, kept in the same state file the terminal viewer uses.
type ReaderView struct {
	Verdicts  map[string]string `json:"verdicts"`
	Orient    string            `json:"orient"`
	Design    string            `json:"design"`
	Reviewed  []string          `json:"reviewed"`
	Flagged   []string          `json:"flagged"`
	Dismissed []string          `json:"dismissed"`
	Comments  []CommentView     `json:"comments"`
	Drafts    int               `json:"drafts"`
	Outcome   OutcomeView       `json:"outcome"`
	Labels    []LabelView       `json:"labels"`
	Questions []QuestionView    `json:"questions"`
	Full      bool              `json:"full"`
	Blind     bool              `json:"blind"`
	Revealed  []string          `json:"revealed"`
	Visited   []string          `json:"visited"`
	Seen      map[string]string `json:"seen"`
}

type QuestionView struct {
	state.Question
	Answered bool `json:"answered"`
}

type CommentView struct {
	state.Question
	Label    string `json:"label"`
	Blocking bool   `json:"blocking"`
	Resolved bool   `json:"resolved"`
}

type OutcomeView struct {
	Decision string   `json:"decision"`
	Blockers []string `json:"blockers"`
	Left     []string `json:"left"`
	Notes    []string `json:"notes"`
}

type LabelView struct {
	Name     string `json:"name"`
	Key      string `json:"key"`
	Blocking bool   `json:"blocking"`
	Tip      string `json:"tip"`
}

type CommentInput struct {
	Station  string `json:"station"`
	Part     int    `json:"part"`
	Line     int    `json:"line"`
	Label    string `json:"label"`
	Blocking bool   `json:"blocking"`
	Text     string `json:"text"`
	// Answers is the key of the agent's question note this comment answers.
	Answers string `json:"answers"`
}

func readerOf(d *doc.Doc, st *state.State) ReaderView {
	rv := ReaderView{Verdicts: st.Verdicts, Orient: st.Orient, Design: st.Design,
		Reviewed: keys(st.Reviewed), Flagged: keys(st.Flagged), Dismissed: keys(st.Dismissed), Comments: []CommentView{},
		Questions: []QuestionView{}, Full: st.Full, Blind: st.Blind, Revealed: keys(st.Revealed), Visited: keys(st.Visited), Seen: st.Seen}
	resolved := d.AnsweredQuestions()
	for _, q := range st.Questions {
		if !q.IsComment() {
			rv.Questions = append(rv.Questions, QuestionView{Question: q, Answered: resolved[q.ID]})
			continue
		}
		name, blocking := state.CommentLabel(q.Text)
		rv.Comments = append(rv.Comments, CommentView{Question: q, Label: name, Blocking: blocking, Resolved: resolved[q.ID]})
		if q.Draft {
			rv.Drafts++
		}
	}
	o := verdict.Recommend(d, st)
	rv.Outcome = OutcomeView{Decision: o.Decision, Blockers: o.Blockers, Left: o.Left, Notes: o.Notes}
	for _, l := range state.Labels {
		rv.Labels = append(rv.Labels, LabelView{Name: l.Name, Key: l.Key, Blocking: l.Blocking, Tip: l.Tip})
	}
	return rv
}

func keys(m map[string]bool) []string {
	out := []string{}
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// SetVerdict records the reader's verdict on a stop: good, changes or unsure; the same one again clears it.
func (r *Reviews) SetVerdict(station, v string) (*View, error) {
	if !slices.Contains([]string{verdict.Good, verdict.Changes, verdict.Unsure}, v) {
		return nil, fmt.Errorf("unknown verdict %q", v)
	}
	return r.change(func(_ *doc.Doc, st *state.State) error {
		if st.Verdicts[station] == v {
			delete(st.Verdicts, station)
		} else {
			st.Verdicts[station] = v
		}
		return nil
	})
}

// SetOrient records whether the change makes sense as done; design says why not when the answer is no.
func (r *Reviews) SetOrient(answer, design string) (*View, error) {
	if !slices.Contains([]string{verdict.OrientYes, verdict.OrientNo, verdict.OrientUnsure, ""}, answer) {
		return nil, fmt.Errorf("unknown answer %q", answer)
	}
	return r.change(func(_ *doc.Doc, st *state.State) error {
		st.Orient, st.Design = answer, ""
		if answer == verdict.OrientNo {
			st.Design = text.Sanitize(strings.TrimSpace(design))
		}
		return nil
	})
}

// MarkNote toggles a mark on the note with key: reviewed, flagged or dismissed.
func (r *Reviews) MarkNote(key, mark string) (*View, error) {
	return r.change(func(d *doc.Doc, st *state.State) error {
		var set map[string]bool
		switch mark {
		case "reviewed":
			set = st.Reviewed
		case "flagged":
			set = st.Flagged
		case "dismissed":
			set = st.Dismissed
		default:
			return fmt.Errorf("unknown mark %q", mark)
		}
		if set[key] {
			delete(set, key)
			return nil
		}
		set[key] = true
		if mark == "reviewed" {
			for _, n := range d.CodeNotes() {
				if n.Key == key && n.Changed != "" {
					st.Seen[key] = n.Changed
				}
			}
		}
		return nil
	})
}

// AddComment saves a draft review comment on a line of a stop; SendComments hands the drafts to the agent.
func (r *Reviews) AddComment(in CommentInput) (*View, error) {
	body := text.Sanitize(strings.TrimSpace(in.Text))
	if body == "" {
		return nil, errors.New("the comment is empty")
	}
	return r.change(func(d *doc.Doc, st *state.State) error {
		_, s := d.Station(in.Station)
		if s == nil || in.Part < 0 || in.Part >= len(s.Parts) {
			return errors.New("that line is no longer in the review")
		}
		p := s.Parts[in.Part]
		if in.Line < 0 || in.Line >= len(p.Lines) {
			return errors.New("that line is no longer in the review")
		}
		if in.Label != "" {
			for _, l := range state.Labels {
				if l.Name == in.Label {
					body = state.Prefix(l.Name, in.Blocking) + body
				}
			}
		}
		if in.Answers != "" {
			for i, n := range s.Notes {
				if n.Key == in.Answers {
					body = fmt.Sprintf("answer to your question (note %d: %s): %s", i+1, doc.Short(n.Text, 80), body)
				}
			}
		}
		q := lineQuestion(s, p, in.Line, st.NextCommentID(), body)
		q.Kind, q.Draft, q.Answers = state.KindComment, true, in.Answers
		st.Questions = append(st.Questions, q)
		return nil
	})
}

// lineQuestion anchors a question or comment on a line the way the terminal viewer does: by the
// line's text, or the nearest non-blank line above it.
func lineQuestion(s *doc.Station, p *doc.Part, line int, id, body string) state.Question {
	l := line
	needle := strings.TrimSpace(p.Lines[l])
	for needle == "" && l > p.Range.Start {
		l--
		needle = strings.TrimSpace(p.Lines[l])
	}
	return state.Question{ID: id, Station: s.ID, File: p.Spec.File, Side: p.Spec.Side, Line: line + 1, Needle: needle,
		Text: body, Asked: time.Now().UTC().Truncate(time.Second)}
}

// DeleteComment removes a draft; a sent comment stays, since the agent has it.
func (r *Reviews) DeleteComment(id string) (*View, error) {
	return r.change(func(_ *doc.Doc, st *state.State) error {
		i := slices.IndexFunc(st.Questions, func(q state.Question) bool { return q.ID == id })
		if i < 0 {
			return nil
		}
		if !st.Questions[i].Draft {
			return fmt.Errorf("%s was sent already; ask the agent to drop it", id)
		}
		st.Questions = slices.Delete(st.Questions, i, i+1)
		return nil
	})
}

// SendComments types every draft into the agent's terminal as one message and marks them sent.
func (r *Reviews) SendComments() (*View, error) {
	path := r.current()
	if !r.agents.Running(path) {
		return nil, errors.New("start the agent first; the drafts stay until then")
	}
	var msg string
	var ids []string
	v, err := r.change(func(_ *doc.Doc, st *state.State) error {
		var drafts []state.Question
		for i, q := range st.Questions {
			if q.IsComment() && q.Draft {
				drafts = append(drafts, q)
				ids = append(ids, q.ID)
				st.Questions[i].Draft = false
			}
		}
		if len(drafts) == 0 {
			return errors.New("no draft comments")
		}
		msg = tui.CommentsMessage(drafts, review.CommentsPath(path))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := r.agents.Send(path, msg); err != nil {
		// Like the terminal viewer, a send that failed leaves the comments drafts, so none is lost.
		_, _ = r.change(func(_ *doc.Doc, st *state.State) error {
			for i, q := range st.Questions {
				if slices.Contains(ids, q.ID) {
					st.Questions[i].Draft = true
				}
			}
			return nil
		})
		return nil, fmt.Errorf("not sent, kept as drafts: %w", err)
	}
	return v, nil
}

// change loads the reader's state, applies fn, saves it and returns the view built from it.
func (r *Reviews) change(fn func(*doc.Doc, *state.State) error) (*View, error) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	path := r.current()
	if path == "" {
		return nil, errors.New("no review open")
	}
	d, err := r.build(path)
	if err != nil {
		return nil, err
	}
	st, err := state.Load(review.StatePath(path))
	if err != nil {
		return nil, err
	}
	if err := fn(d, st); err != nil {
		return nil, err
	}
	if err := st.Save(); err != nil {
		return nil, err
	}
	v := viewOf(d)
	v.Reader = readerOf(d, st)
	v.Counts = tui.CountsOf(d, st)
	return v, nil
}
