package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redrick/margin/internal/control"
	"github.com/redrick/margin/internal/diffmap"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/tui"
)

// The window serves the same control socket as the terminal viewer, so the agent's `margin goto`,
// `where`, `reload` and `questions` drive it the same way. Where the reader is lives in the page, so
// goto and where ask the page and wait for its answer.

type ctlServer struct {
	mu      sync.Mutex
	srv     *control.Server
	path    string
	next    int
	waiting map[int]chan Position
	errs    map[int]string
}

// Position is where the reader is in the page: the stop, the selected note and line, the code in view
// and the spotlit test. Lines are 0-based, -1 for none.
type Position struct {
	Station int           `json:"station"`
	Note    int           `json:"note"`
	Part    int           `json:"part"`
	Line    int           `json:"line"`
	Visible []VisibleLine `json:"visible"`
	Spot    int           `json:"spot"`
	Err     string        `json:"err"`
}

type VisibleLine struct {
	Part int `json:"part"`
	From int `json:"from"`
	To   int `json:"to"`
}

type CtlRequest struct {
	ID  int    `json:"id"`
	Cmd string `json:"cmd"`
	Arg string `json:"arg"`
}

// serve starts answering the socket of the open review, unless another viewer already does.
func (r *Reviews) serve(path string) (shared bool) {
	c := &r.ctl
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.path == path && c.srv != nil {
		return false
	}
	if c.srv != nil {
		c.srv.Close()
		c.srv, c.path = nil, ""
	}
	sock := control.SocketPath(path)
	if control.Alive(sock) {
		return true
	}
	srv, err := control.Serve(sock, func(req control.Request) control.Response { return r.handle(path, req) })
	if err != nil {
		return true
	}
	c.srv, c.path = srv, path
	return false
}

func (r *Reviews) handle(path string, req control.Request) control.Response {
	switch req.Cmd {
	case "ping":
		return control.OK("pong")
	case "review":
		return control.OK(map[string]string{"review": path, "state": review.StatePath(path)})
	case "reload":
		return r.whereAfter(path, "reload", "")
	case "questions":
		rv, err := review.Load(path)
		if err != nil {
			return control.Fail(err)
		}
		st, err := state.Load(review.StatePath(path))
		if err != nil {
			return control.Fail(err)
		}
		return control.OK(tui.Questions(rv, st, req.Arg == "all"))
	case "where", "goto":
		return r.whereAfter(path, req.Cmd, req.Arg)
	}
	return control.Fail(fmt.Errorf("unknown command %q", req.Cmd))
}

// whereAfter asks the page to carry out cmd and report where the reader is, then describes it the way
// the terminal viewer does.
func (r *Reviews) whereAfter(path, cmd, arg string) control.Response {
	if r.current() != path {
		return control.Fail(errors.New("this review is no longer open in the window"))
	}
	c := &r.ctl
	c.mu.Lock()
	if c.waiting == nil {
		c.waiting = map[int]chan Position{}
	}
	c.next++
	id := c.next
	ch := make(chan Position, 1)
	c.waiting[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.waiting, id)
		c.mu.Unlock()
	}()
	r.app.Event.Emit("ctl:request", CtlRequest{ID: id, Cmd: cmd, Arg: strings.TrimSpace(arg)})
	var pos Position
	select {
	case pos = <-ch:
	case <-time.After(15 * time.Second):
		return control.Fail(errors.New("viewer did not respond"))
	}
	if pos.Err != "" {
		return control.Fail(errors.New(pos.Err))
	}
	d, err := r.build(path)
	if err != nil {
		return control.Fail(err)
	}
	st, err := state.Load(review.StatePath(path))
	if err != nil {
		return control.Fail(err)
	}
	return control.OK(whereOf(path, d, st, pos))
}

// CtlReply is the page's answer to a control request.
func (r *Reviews) CtlReply(id int, pos Position) {
	c := &r.ctl
	c.mu.Lock()
	ch := c.waiting[id]
	c.mu.Unlock()
	if ch != nil {
		ch <- pos
	}
}

var kindNames = map[diffmap.Kind]string{diffmap.Same: "same", diffmap.Added: "added", diffmap.Changed: "changed", diffmap.Removed: "removed"}

var covNames = map[doc.LineCov]string{doc.CovInert: "not executable", doc.CovRun: "run", doc.CovIdle: "not run", doc.CovMissed: "untested"}

func whereOf(path string, d *doc.Doc, st *state.State, pos Position) tui.Where {
	w := tui.Where{Review: path, Counts: tui.CountsOf(d, st), Problems: len(d.Problems)}
	if pos.Station < 0 || pos.Station >= len(d.Stations) {
		return w
	}
	s := d.Stations[pos.Station]
	w.Station = tui.Station{Index: pos.Station, Last: len(d.Stations) - 1, ID: s.ID, Title: s.Title}
	if c := d.Coverage; c != nil && pos.Spot >= 0 && pos.Spot < len(c.Tests) {
		w.Spotlight = c.Tests[pos.Spot].Name
	}
	if pos.Note >= 0 && pos.Note < len(s.Notes) {
		n := s.Notes[pos.Note]
		w.Note = &tui.NoteRef{Number: pos.Note + 1, Kind: n.Level(), At: n.At, Text: n.Text, Problem: n.Problem,
			Reviewed: st.Reviewed[n.Key], Flagged: st.Flagged[n.Key], Dismissed: st.Dismissed[n.Key]}
	}
	if pos.Part >= 0 && pos.Part < len(s.Parts) && s.Parts[pos.Part].Err == nil && pos.Line >= 0 && pos.Line < len(s.Parts[pos.Part].Lines) {
		p := s.Parts[pos.Part]
		kind := diffmap.Same
		if pos.Line < len(p.Kinds) {
			kind = p.Kinds[pos.Line]
		}
		w.Cursor = &tui.LineRef{File: p.Spec.File, Line: pos.Line + 1, Side: p.Spec.Side, Kind: kindNames[kind], Text: p.Lines[pos.Line],
			Coverage: covNames[d.Coverage.Line(p, pos.Line)], Moved: p.Moved[pos.Line]}
		for _, t := range d.Coverage.TestsAt(p, pos.Line) {
			w.Cursor.Tests = append(w.Cursor.Tests, d.Coverage.Tests[t].Name)
		}
	}
	for _, v := range pos.Visible {
		if v.Part < 0 || v.Part >= len(s.Parts) {
			continue
		}
		file := s.Parts[v.Part].Spec.File
		if n := len(w.Visible); n > 0 && w.Visible[n-1].File == file && w.Visible[n-1].To >= v.From {
			w.Visible[n-1].To = max(w.Visible[n-1].To, v.To+1)
			continue
		}
		w.Visible = append(w.Visible, tui.Visible{File: file, From: v.From + 1, To: v.To + 1})
	}
	return w
}
