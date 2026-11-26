package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/redrick/margin/internal/control"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/testutil"
)

func TestReadingPositionSurvivesRestart(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := testutil.Example(t)
	reader, err := state.Load(review.StatePath(path))
	if err != nil {
		t.Fatal(err)
	}
	reader.Visited["reserve"] = true
	reader.Verdicts["reserve"] = "good"
	reader.Questions = []state.Question{{ID: "c1", Kind: state.KindComment, Text: "Check this", Draft: true}}
	if err := reader.Save(); err != nil {
		t.Fatal(err)
	}
	r := &Reviews{path: path}
	position := ReadingPosition{Station: "reserve", Step: &ReadingStep{Kind: "change", From: 2, To: 4}, Scroll: 125}
	if err := r.SaveReadingPosition(path, position, true); err != nil {
		t.Fatal(err)
	}
	reopened := (&Workspace{}).initial(nil)
	if reopened.Review != path || !reopened.Restore {
		t.Fatalf("bare launch = %+v", reopened)
	}
	other := filepath.Join(t.TempDir(), "other.review.yaml")
	if explicit := (&Workspace{}).initial([]string{other}); explicit.Review != other || explicit.Restore {
		t.Fatalf("explicit launch = %+v", explicit)
	}
	next := &Reviews{path: reopened.Review}
	if saved := next.ReadingPosition(path); !reflect.DeepEqual(saved, &position) {
		t.Fatalf("restored position = %+v", saved)
	}
	reader, err = state.Load(review.StatePath(path))
	if err != nil {
		t.Fatal(err)
	}
	if !reader.Visited["reserve"] || reader.Verdicts["reserve"] != "good" ||
		len(reader.Questions) != 1 || !reader.Questions[0].Draft {
		t.Fatalf("reader state did not survive restart: %+v", reader)
	}
	for _, file := range []string{resumePath(path), filepath.Join(guiStateDir(), "last-review.json")} {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("state file mode = %v, want 0600", info.Mode().Perm())
		}
	}
	if err := os.WriteFile(resumePath(path), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if next.ReadingPosition(path) != nil {
		t.Fatal("corrupt saved position was used")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if missing := (&Workspace{}).initial(nil); missing.Review != "" || missing.Restore {
		t.Fatalf("launch with missing review = %+v", missing)
	}
}

func TestReviewCreationKeepsItsAgentLease(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	base := t.TempDir()
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(base, alias); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(alias, ".margin", "reviews", "new.review.yaml")
	lease, err := leaseAgent(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	other, err := leaseAgent(filepath.Join(base, ".margin", "reviews", "new.review.yaml"))
	if other != nil {
		other.Close()
	}
	if !errors.Is(err, errAgentInUse) {
		t.Fatalf("acquiring agent lease after creation = %v, want %v", err, errAgentInUse)
	}
}

func TestClaudeResumeUsesSavedAgentAndStopDisablesAutomaticResume(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	path, repo := resumeFixture(t)
	log, claude := testAgent(t, "claude")
	t.Setenv("MARGIN_AGENT", claude)
	first := &Agents{}
	t.Cleanup(func() { _ = first.ServiceShutdown() })
	if _, err := first.Start(path, repo, false, 80, 24); err != nil {
		t.Fatal(err)
	}
	args := waitAgentArgs(t, log)
	if len(args) != 3 || args[0] != "--session-id" || !validSessionID("claude", args[1]) {
		t.Fatalf("new Claude launch args = %q", args)
	}
	id := args[1]
	if err := first.sessions[path].pty.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("agent PTY must allow a blocked read to be interrupted: %v", err)
	}
	if _, err := (&Agents{}).Start(path, repo, false, 80, 24); !errors.Is(err, errAgentInUse) {
		t.Fatalf("second process could acquire active review: %v", err)
	}
	if err := first.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	waitAgentExit(t, first.sessions[path])
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MARGIN_AGENT", "another-agent")
	next := &Agents{}
	t.Cleanup(func() { _ = next.ServiceShutdown() })
	if saved := next.ResumeStatus(path, repo); !saved.Auto || !saved.Exact || saved.Name != "claude" {
		t.Fatalf("saved agent = %+v", saved)
	}
	if _, err := next.Resume(path, repo, true, 80, 24); err != nil {
		t.Fatal(err)
	}
	if got := waitAgentArgs(t, log); !reflect.DeepEqual(got, []string{"--resume", id}) {
		t.Fatalf("Claude resume sent %q", got)
	}
	if err := next.Stop(path); err != nil {
		t.Fatal(err)
	}
	waitAgentExit(t, next.sessions[path])
	if saved := (&Agents{}).ResumeStatus(path, repo); saved.Auto || !saved.Exact {
		t.Fatalf("stopped agent = %+v", saved)
	}
	stopped := &Agents{}
	if _, err := stopped.Resume(path, repo, true, 80, 24); err != nil || stopped.Running(path) {
		t.Fatalf("automatic restore started a stopped agent: %v", err)
	}

	// A provider can reject a conversation after its process starts successfully.
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MARGIN_TEST_AGENT_FAIL", "1")
	last := &Agents{}
	t.Cleanup(func() { _ = last.ServiceShutdown() })
	if _, err := last.Resume(path, repo, false, 80, 24); err != nil {
		t.Fatal(err)
	}
	waitAgentExit(t, last.sessions[path])
	if saved := last.ResumeStatus(path, repo); !saved.Exact || !saved.Failed || saved.Auto {
		t.Fatalf("failed resume = %+v", saved)
	}
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	if _, err := last.Continue(path, repo, 80, 24); err != nil {
		t.Fatal(err)
	}
	got := waitAgentArgs(t, log)
	if len(got) != 3 || !strings.Contains(got[2], "--continue") || strings.Contains(got[2], "Start the review") {
		t.Fatalf("continuation args = %q", got)
	}
}

func TestRegistersOnlyTheCodexReviewThatStartedTheAgent(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	runtime, err := os.MkdirTemp("/tmp", "margin-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtime) })
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	path, repo := resumeFixture(t)
	other := filepath.Join(filepath.Dir(path), "other.review.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, data, 0o600); err != nil {
		t.Fatal(err)
	}
	log, codex := testAgent(t, "codex")
	t.Setenv("MARGIN_AGENT", codex)
	a := &Agents{}
	t.Cleanup(func() { _ = a.ServiceShutdown() })
	if _, err := a.Start(path, repo, false, 80, 24); err != nil {
		t.Fatal(err)
	}
	args := waitAgentArgs(t, log)
	re := regexp.MustCompile(`--gui-agent '([^']+)'`)
	field := re.FindStringSubmatch(args[0])
	if len(field) != 2 {
		t.Fatal("Codex launch has no registration endpoint")
	}
	const id = "a42a0ae9-8912-4afd-9580-147fdb7e6184"
	control.RegisterAgent(field[1], id, other)
	if saved := a.ResumeStatus(path, repo); saved.Exact {
		t.Fatal("registration accepted the wrong review")
	}
	control.RegisterAgent(field[1], id, path)
	if saved := a.ResumeStatus(path, repo); !saved.Exact || !saved.Auto {
		t.Fatalf("registered Codex agent = %+v", saved)
	}
	if saved := readReviewResume(path).Agent; saved.ID != id {
		t.Fatalf("registered conversation ID = %q", saved.ID)
	}
	if got, err := nativeResumeArgs(*readReviewResume(path).Agent); err != nil || !reflect.DeepEqual(got, []string{codex, "resume", id}) {
		t.Fatalf("Codex resume args = %q, %v", got, err)
	}
}

func TestLinkConversationUsesAnExactID(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path, repo := resumeFixture(t)
	const uuid = "a42a0ae9-8912-4afd-9580-147fdb7e6184"
	for _, tt := range []struct {
		command string
		id      string
		want    []string
	}{
		{command: "gemini -i {prompt}", id: uuid, want: []string{"gemini", "--resume", uuid}},
		{command: "opencode --prompt {prompt}", id: "ses_someid", want: []string{"opencode", "--session", "ses_someid"}},
	} {
		t.Run(tt.command, func(t *testing.T) {
			if err := clearReviewResume(path); err != nil {
				t.Fatal(err)
			}
			t.Setenv("MARGIN_AGENT", tt.command)
			a := &Agents{}
			if err := a.LinkConversation(path, repo, "latest"); err == nil {
				t.Fatal("an unscoped session selector was accepted")
			}
			if err := a.LinkConversation(path, repo, tt.id); err != nil {
				t.Fatal(err)
			}
			if got, err := nativeResumeArgs(*readReviewResume(path).Agent); err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("linked resume args = %q, %v", got, err)
			}
		})
	}
}

func resumeFixture(t *testing.T) (string, string) {
	t.Helper()
	path := testutil.Example(t)
	rv, err := review.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, rv.RepoDir()
}

func testAgent(t *testing.T, name string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	t.Setenv("MARGIN_TEST_AGENT_LOG", log)
	binary := filepath.Join(dir, name)
	const sh = `#!/bin/sh
printf '%s\000' "$@" > "$MARGIN_TEST_AGENT_LOG"
if [ "$MARGIN_TEST_AGENT_FAIL" = 1 ] && [ "$1" = --resume ]; then
    exit 1
fi
trap 'exit 0' TERM
while IFS= read -r line; do :; done
`
	if err := os.WriteFile(binary, []byte(sh), 0o700); err != nil {
		t.Fatal(err)
	}
	return log, binary
}

func waitAgentArgs(t *testing.T, log string) []string {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(log); err == nil && len(data) != 0 && data[len(data)-1] == 0 {
			return strings.Split(string(data[:len(data)-1]), "\x00")
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, err := os.ReadFile(log)
	t.Fatalf("the agent did not report its complete launch arguments (%d bytes, %v)", len(data), err)
	return nil
}

func waitAgentExit(t *testing.T, s *session) {
	t.Helper()
	select {
	case <-s.exited:
	case <-time.After(3 * time.Second):
		t.Fatal("the agent did not exit")
	}
}
