package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/testutil"
)

func TestDiffQuestionsUseIndependentSessions(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path, other := testutil.Example(t), testutil.Example(t)
	repo := filepath.Join(filepath.Dir(path), "current")
	r := &Reviews{agents: &Agents{sessions: map[string]*session{
		path: {command: "codex", cmd: &exec.Cmd{Dir: filepath.Dir(path)}},
	}}}
	t.Cleanup(r.questions.stop)
	open := func(r *Reviews, path string) int {
		t.Helper()
		r.setReview(path)
		view, err := r.Current()
		if err != nil {
			t.Fatal(err)
		}
		return view.Version
	}
	version := open(r, path)
	_, stop := r.loaded.Station("removed")
	part := stop.Parts[0]
	if !part.Base {
		t.Fatal("expected a base-side part in the test fixture")
	}
	release := map[string]chan struct{}{"first": make(chan struct{}), "second": make(chan struct{}), "third": make(chan struct{})}
	started := make(chan questionJob, 3)
	r.questions.run = func(ctx context.Context, job questionJob) (string, error) {
		started <- job
		select {
		case <-release[job.question.Text]:
			return "answer to " + job.question.Text, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	ask := func(body string) error {
		return r.Ask(QuestionInput{LineInput: LineInput{Station: "removed", Part: 0, Line: part.Range.Start, Text: body}, End: part.Range.Start + 1, Version: version})
	}
	waitStart := func(body string) {
		t.Helper()
		select {
		case job := <-started:
			q := job.question
			if q.Text != body || q.Side != "base" || q.End-q.Line != 1 || q.Excerpt == "" || job.review != path || job.repo != repo {
				t.Fatalf("unexpected job for %s: %+v", body, job)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("question %s did not start", body)
		}
	}
	if err := ask("wrong worktree"); err == nil || !strings.Contains(err.Error(), "worktree changed") {
		t.Fatalf("accepted an agent from the wrong worktree: %v", err)
	}
	r.agents.sessions[path].cmd.Dir = repo
	for _, body := range []string{"first", "second", "third"} {
		if err := ask(body); err != nil {
			t.Fatal(err)
		}
		if body != "third" {
			waitStart(body)
		}
	}
	questions, err := r.DiffQuestions(version)
	if err != nil || len(questions) != 3 || questions[2].Status != questionQueued {
		t.Fatalf("third question did not queue: %+v, %v", questions, err)
	}
	select {
	case <-started:
		t.Fatal("a third session started while two were answering")
	default:
	}
	if err := r.RetryQuestion(questions[2].ID, version); !errors.Is(err, errQuestionBusy) {
		t.Fatalf("retry while queued = %v", err)
	}
	otherVersion := open(r, other)
	if err := ask("stale"); err == nil {
		t.Fatal("accepted a selection from a previous review")
	}
	close(release["second"])
	waitStart("third")
	if got, err := r.DiffQuestions(otherVersion); err != nil || len(got) != 0 {
		t.Fatalf("questions from a different review: %+v, %v", got, err)
	}
	version = open(r, path)
	got, err := r.DiffQuestions(version)
	if err != nil || len(got) != 3 || got[0].Status != questionAnswering || got[1].Answer != "answer to second" {
		t.Fatalf("out-of-order completion: %+v, %v", got, err)
	}
	r.questions.stop()
	next := &Reviews{agents: r.agents}
	t.Cleanup(next.questions.stop)
	version = open(next, path)
	got, err = next.DiffQuestions(version)
	if err != nil || len(got) != 3 || got[0].Status != questionInterrupted || got[2].Status != questionInterrupted || got[1].Answer != "answer to second" {
		t.Fatalf("answers and interruptions after restart: %+v, %v", got, err)
	}
	if got[1].Part != 0 || got[1].At != part.Range.Start {
		t.Fatalf("base-side range moved: %+v", got[1])
	}
	if info, err := os.Stat(filepath.Join(questionDir(path), got[1].ID+".json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("question JSON should be private: %v, %v", info, err)
	}
	next.questions.run = func(_ context.Context, job questionJob) (string, error) {
		return "retried " + job.question.Text, nil
	}
	if err := next.RetryQuestion(got[0].ID, version); err != nil {
		t.Fatal(err)
	}
	next.questions.workers.Wait()
	got, err = next.DiffQuestions(version)
	if err != nil || len(got) != 3 || got[0].Answer != "retried first" {
		t.Fatalf("retry did not update the original question: %+v, %v", got, err)
	}
}

func TestLocateQuestionWhenCodeMoves(t *testing.T) {
	p := &doc.Part{Spec: review.Part{File: "code.go"}, Lines: []string{"new", "before", "selected", "lines"}}
	d := &doc.Doc{Stations: []*doc.Station{{ID: "change", Parts: []*doc.Part{p}}}}
	q := DiffQuestion{Station: "change", File: "code.go", Side: "head", Line: 1, End: 2, Excerpt: "selected\nlines"}
	if located := locateQuestion(d, q); located.At != 2 || located.Part != 0 {
		t.Fatalf("unique moved selection: %+v", located)
	}
	p.Lines = append(p.Lines, "selected", "lines")
	if located := locateQuestion(d, q); located.Part != -1 || located.Excerpt != q.Excerpt {
		t.Fatalf("ambiguous selection should keep only its original excerpt: %+v", located)
	}
}

func TestQuestionCLIAdapters(t *testing.T) {
	const fake = `#!/bin/sh
set -eu
agent=${0##*/}
case "$agent" in
  gemini)
    [ "$1 $2 $3" = '--approval-mode plan --policy' ]
    grep -Fq 'toolName = "*"' "$4"
    grep -Fq 'decision = "deny"' "$4"
    grep -Fq 'toolName = ["read_file", "grep_search", "glob", "list_directory"]' "$4"
    [ "$5 $6" = '--allowed-mcp-server-names margin-question-no-mcp' ]
    for arg do prompt=$arg; done
    ;;
  *) prompt=$(cat) ;;
esac
case "$prompt" in
  *'Diff side: base (old)'*'Lines: 5–6'*'selectedCode()'*'What happens?'*) ;;
  *) exit 27 ;;
esac
case "$agent" in
  codex)
    [ "$1 $2 $3 $4 $5 $6" = '--ask-for-approval never exec --sandbox read-only --ephemeral' ]
    while [ "$1" != '--output-last-message' ]; do shift; done
    printf 'A **reply**\n' > "$2"
    ;;
  claude) printf '%s\n' '{"result":"A **reply**","is_error":false}' ;;
  gemini) printf '%s\n' '{"response":"A **reply**"}' ;;
  opencode)
    printf '%s\n' '{"type":"tool_use","part":{"text":"internal"}}' '{"type":"text","part":{"text":"A **reply**"}}'
    ;;
esac
`
	for _, tt := range []struct{ name, suffix string }{
		{"codex", ""}, {"claude", ""}, {"gemini", " -i {prompt}"}, {"opencode", " --prompt {prompt}"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			exe := filepath.Join(dir, tt.name)
			if err := os.WriteFile(exe, []byte(fake), 0o700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			reply, err := runDiffQuestion(ctx, questionJob{repo: dir, command: exe + tt.suffix, question: DiffQuestion{
				File: "code.go", Side: "base", Ref: "old", Line: 5, End: 6, Excerpt: "selectedCode()\nreturn", Text: "What happens?",
			}})
			if err != nil || strings.TrimSpace(reply) != "A **reply**" {
				t.Fatalf("reply = %q, %v", reply, err)
			}
		})
	}
}
