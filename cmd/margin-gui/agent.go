package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/unix"

	"github.com/redrick/margin/internal/cli"
	"github.com/redrick/margin/internal/config"
	"github.com/redrick/margin/internal/control"
)

// scrollback is how much agent output is kept to replay when the review is shown again.
const scrollback = 512 << 10
const agentStopGrace = 900 * time.Millisecond

type Agents struct {
	app *application.App

	mu       sync.Mutex
	sessions map[string]*session
	closing  bool
	// hold types messages without pressing Enter, like `margin open --no-submit`.
	hold bool
}

// SetSubmit says whether messages for the agent are submitted, or only typed for the reader to send.
func (a *Agents) SetSubmit(submit bool) {
	a.mu.Lock()
	a.hold = !submit
	a.mu.Unlock()
}

type session struct {
	cmd          *exec.Cmd
	command      string
	pty          *os.File
	lease        *os.File
	exited       chan struct{}
	run          string
	resuming     bool
	registration *control.Server
	stopping     bool
	done         bool

	mu  sync.Mutex
	buf []byte
}

type AgentData struct {
	Review string `json:"review"`
	Data   string `json:"data"`
}

// Start runs the agent for a review in a terminal, unless one is already running, and returns
// the output so far, base64 encoded.
func (a *Agents) Start(reviewPath, repo string, created bool, cols, rows int) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s, ok := a.sessions[reviewPath]; ok && !s.done {
		return s.replay(), nil
	}
	return "", startLeasedAgent(reviewPath, func(lease *os.File) error {
		if _, err := loadAgentReview(reviewPath, repo); err != nil {
			return err
		}
		return a.launchNew(lease, reviewPath, repo, config.Load().Agent, created, false, cols, rows)
	})
}

func (s *session) replay() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return base64.StdEncoding.EncodeToString(s.buf)
}

func (a *Agents) launchNew(lease *os.File, path, repo, command string, created, continued bool, cols, rows int) error {
	saved := savedAgent{Command: command, Repo: resumeIdentity(repo), Run: rand.Text(), Auto: true}
	var registration *control.Server
	var endpoint string
	provider := agentPreset(command)
	if provider == "codex" {
		var err error
		registration, endpoint, err = a.codexRegistration(path, saved.Run)
		if err != nil {
			log.Printf("registering the Codex conversation: %v", err)
		}
	}
	argv := (config.Config{Agent: command}).AgentArgv(kickoff(path, created, continued, endpoint))
	if provider == "claude" {
		saved.ID = newClaudeID()
		argv = append([]string{argv[0], "--session-id", saved.ID}, argv[1:]...)
	}
	if err := a.launch(lease, path, repo, argv, saved, registration, false, cols, rows); err != nil {
		if registration != nil {
			registration.Close()
		}
		return err
	}
	return nil
}

// launch runs with a.mu held so registration cannot race the saved launch record.
func (a *Agents) launch(lease *os.File, path, repo string, argv []string, saved savedAgent, registration *control.Server, resuming bool, cols, rows int) error {
	if a.closing {
		return errors.New("Margin is closing")
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		return errors.New(argv[0] + " is not available; check the agent in Settings")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = repo
	cmd.Env = append(cli.AgentEnv(cleanEnv()), "TERM=xterm-256color", "COLORTERM=truecolor", "MARGIN_REVIEW="+path)
	f, err := startAgentPTY(cmd, &pty.Winsize{Cols: uint16(max(cols, 20)), Rows: uint16(max(rows, 5))})
	if err != nil {
		return err
	}
	s := &session{
		cmd: cmd, command: saved.Command, pty: f, lease: lease, run: saved.Run, resuming: resuming,
		exited: make(chan struct{}), registration: registration,
	}
	if err := updateReviewResume(path, func(record *reviewResume) bool {
		record.Agent = &saved
		return true
	}); err != nil {
		s.signal(syscall.SIGKILL)
		f.Close()
		_ = cmd.Wait()
		return fmt.Errorf("save agent session: %w", err)
	}
	if a.sessions == nil {
		a.sessions = map[string]*session{}
	}
	a.sessions[path] = s
	go a.pump(path, s)
	return nil
}

func startAgentPTY(cmd *exec.Cmd, size *pty.Winsize) (*os.File, error) {
	master, err := pty.StartWithSize(cmd, size)
	if err != nil {
		return nil, err
	}
	defer master.Close()
	// A nonblocking descriptor lets Close interrupt a read even if a descendant still has the slave open.
	fd, err := unix.FcntlInt(master.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err == nil {
		err = unix.SetNonblock(fd, true)
		if err == nil {
			return os.NewFile(uintptr(fd), master.Name()), nil
		}
		_ = unix.Close(fd)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	return nil, err
}

// Live lists the reviews whose agent is running.
func (a *Agents) Live() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []string{}
	for p, s := range a.sessions {
		if !s.done {
			out = append(out, p)
		}
	}
	return out
}

func (a *Agents) Running(reviewPath string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[reviewPath]
	return ok && !s.done
}

func (a *Agents) Scrollback(reviewPath string) string {
	if s := a.session(reviewPath); s != nil {
		return s.replay()
	}
	return ""
}

func (a *Agents) Write(reviewPath, data string) error {
	s := a.session(reviewPath)
	if s == nil {
		return errors.New("no agent running for this review")
	}
	_, err := s.pty.WriteString(data)
	return err
}

// Send types text into the agent's prompt and submits it, the way a person would.
func (a *Agents) Send(reviewPath, text string) error {
	s := a.session(reviewPath)
	if s == nil {
		return errors.New("no agent running for this review")
	}
	if _, err := s.pty.WriteString(text); err != nil {
		return err
	}
	a.mu.Lock()
	hold := a.hold
	a.mu.Unlock()
	if hold {
		return nil
	}
	time.Sleep(200 * time.Millisecond)
	_, err := s.pty.WriteString("\r")
	return err
}

func (a *Agents) Resize(reviewPath string, cols, rows int) {
	if s := a.session(reviewPath); s != nil && cols > 0 && rows > 0 {
		_ = pty.Setsize(s.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	}
}

func (a *Agents) Stop(reviewPath string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.sessions[reviewPath]
	if s == nil || s.done || s.stopping {
		return nil
	}
	err := a.updateStopped(reviewPath, s, false)
	s.stopping = true
	s.signal(syscall.SIGTERM)
	time.AfterFunc(agentStopGrace, s.forceStop)
	return err
}

func (a *Agents) stopForReset(path string) error {
	s := a.session(path)
	if s == nil {
		return nil
	}
	if err := a.Stop(path); err != nil {
		return err
	}
	select {
	case <-s.exited:
		return nil
	case <-time.After(2 * agentStopGrace):
		return errors.New("the previous agent is still stopping")
	}
}

func (a *Agents) ServiceShutdown() error {
	a.mu.Lock()
	a.closing = true
	active := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		if !s.done {
			active = append(active, s)
		}
	}
	a.mu.Unlock()
	for _, s := range active {
		s.signal(syscall.SIGTERM)
	}
	timeout := time.NewTimer(agentStopGrace)
	defer timeout.Stop()
	for i, s := range active {
		select {
		case <-s.exited:
		case <-timeout.C:
			for _, pending := range active[i:] {
				pending.forceStop()
			}
			return nil
		}
	}
	return nil
}

func (s *session) signal(signal syscall.Signal) {
	_ = s.cmd.Process.Signal(signal)
}

func (s *session) forceStop() {
	s.signal(syscall.SIGKILL)
	_ = s.pty.Close()
}

func (a *Agents) updateStopped(path string, s *session, failed bool) error {
	return updateReviewResume(path, func(saved *reviewResume) bool {
		if saved.Agent == nil || saved.Agent.Run != s.run || (!saved.Agent.Auto && saved.Agent.Failed == failed) {
			return false
		}
		saved.Agent.Auto, saved.Agent.Failed = false, failed
		return true
	})
}

func (a *Agents) session(reviewPath string) *session {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s, ok := a.sessions[reviewPath]; ok && !s.done {
		return s
	}
	return nil
}

func (a *Agents) pump(reviewPath string, s *session) {
	buf := make([]byte, 32<<10)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.buf = append(s.buf, buf[:n]...)
			if over := len(s.buf) - scrollback; over > 0 {
				s.buf = s.buf[over:]
			}
			s.mu.Unlock()
			if a.app != nil {
				a.app.Event.Emit("agent:data", AgentData{Review: reviewPath, Data: base64.StdEncoding.EncodeToString(buf[:n])})
			}
		}
		if err != nil {
			break
		}
	}
	waitErr := s.cmd.Wait()
	_ = s.pty.Close()
	a.mu.Lock()
	if s.registration != nil {
		s.registration.Close()
		s.registration = nil
	}
	if !a.closing && !s.stopping {
		if err := a.updateStopped(reviewPath, s, s.resuming && waitErr != nil); err != nil {
			log.Printf("saving stopped agent: %v", err)
		}
	}
	_ = s.lease.Close()
	s.done = true
	close(s.exited)
	a.mu.Unlock()
	if a.app != nil {
		a.app.Event.Emit("agent:exit", reviewPath)
	}
}

func kickoff(reviewPath string, created, continued bool, endpoint string) string {
	command := "margin"
	if endpoint != "" {
		// A reused Codex app server can supply its own PATH to shell commands.
		if self, err := cli.Self(); err == nil && filepath.Base(self) == "margin" {
			command = quoteAgentArg(self)
		}
	}
	brief := command + " brief --review " + quoteAgentArg(reviewPath)
	if endpoint != "" {
		brief += " --gui-agent " + quoteAgentArg(endpoint)
	}
	if continued {
		return "Continue the saved Margin review at " + reviewPath + ". Run `" + brief + " --continue` for the saved context. " +
			"Wait for the reader's next instruction before reviewing again, running tests or editing files."
	}
	if created {
		brief += " --new"
	}
	return "Code review with margin. The person reads the review in the margin desktop app beside you, " +
		"which reloads whenever the review file changes. Review file: " + reviewPath + "\n" +
		"Start the review: run `" + brief + "` and follow its instructions."
}

func quoteAgentArg(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

// cleanEnv drops variables that would make the agent think it runs inside tmux or another agent.
func cleanEnv() []string {
	var out []string
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		switch k {
		case "TMUX", "TMUX_PANE", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CODEX_THREAD_ID", "CODEX_SESSION_ID",
			"TERM", "COLORTERM", "MARGIN_REVIEW":
			continue
		}
		out = append(out, e)
	}
	return out
}
