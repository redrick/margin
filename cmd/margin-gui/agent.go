package main

import (
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/redrick/margin/internal/cli"
	"github.com/redrick/margin/internal/config"
)

// scrollback is how much agent output is kept to replay when the review is shown again.
const scrollback = 512 << 10

type Agents struct {
	app *application.App

	mu       sync.Mutex
	sessions map[string]*session
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
	cmd  *exec.Cmd
	pty  *os.File
	mu   sync.Mutex
	buf  []byte
	done bool
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
	if a.sessions == nil {
		a.sessions = map[string]*session{}
	}
	if s, ok := a.sessions[reviewPath]; ok && !s.done {
		s.mu.Lock()
		defer s.mu.Unlock()
		return base64.StdEncoding.EncodeToString(s.buf), nil
	}
	argv := config.Load().AgentArgv(kickoff(reviewPath, created))
	if _, err := exec.LookPath(argv[0]); err != nil {
		return "", errors.New(argv[0] + " is not on PATH; pick the agent in Settings")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = repo
	cmd.Env = append(cli.AgentEnv(cleanEnv()), "TERM=xterm-256color", "COLORTERM=truecolor", "MARGIN_REVIEW="+reviewPath)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(max(cols, 20)), Rows: uint16(max(rows, 5))})
	if err != nil {
		return "", err
	}
	s := &session{cmd: cmd, pty: f}
	a.sessions[reviewPath] = s
	go a.pump(reviewPath, s)
	return "", nil
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

func (a *Agents) Stop(reviewPath string) {
	if s := a.session(reviewPath); s != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(os.Interrupt)
		_ = s.pty.Close()
	}
}

func (a *Agents) ServiceShutdown() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.sessions {
		if !s.done && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
	}
	return nil
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
			a.app.Event.Emit("agent:data", AgentData{Review: reviewPath, Data: base64.StdEncoding.EncodeToString(buf[:n])})
		}
		if err != nil {
			break
		}
	}
	_ = s.cmd.Wait()
	a.mu.Lock()
	s.done = true
	a.mu.Unlock()
	a.app.Event.Emit("agent:exit", reviewPath)
}

func kickoff(reviewPath string, created bool) string {
	brief := "margin brief --review '" + strings.ReplaceAll(reviewPath, "'", `'\''`) + "'"
	if created {
		brief += " --new"
	}
	return "Code review with margin. The person reads the review in the margin desktop app beside you, " +
		"which reloads whenever the review file changes. Review file: " + reviewPath + "\n" +
		"Start the review: run `" + brief + "` and follow its instructions."
}

// cleanEnv drops variables that would make the agent think it runs inside tmux or another agent.
func cleanEnv() []string {
	var out []string
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		switch k {
		case "TMUX", "TMUX_PANE", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "TERM", "COLORTERM", "MARGIN_REVIEW":
			continue
		}
		out = append(out, e)
	}
	return out
}
