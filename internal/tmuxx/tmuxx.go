package tmuxx

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
)

var shells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true,
	"tcsh": true, "csh": true, "nu": true, "elvish": true, "xonsh": true, "pwsh": true,
}

func InTmux() bool { return os.Getenv("TMUX") != "" && CurrentPane() != "" }

func CurrentPane() string { return os.Getenv("TMUX_PANE") }

func run(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Split opens argv in a new pane above target, taking percent of its height, and returns the new
// pane id. The pane is tagged @gw_companion so gw keeps it with its worktree, and it closes when
// argv exits, except after an error so the message can be read.
func Split(target string, argv []string, percent int) (string, error) {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = Quote(a)
	}
	inner := strings.Join(quoted, " ") + ` || { printf '\nmargin exited with an error, press enter to close '; read -r _; }`
	args := []string{"split-window", "-v", "-b", "-l", fmt.Sprintf("%d%%", percent), "-P", "-F", "#{pane_id}"}
	if target != "" {
		args = append(args, "-t", target)
	}
	if wd, err := os.Getwd(); err == nil {
		args = append(args, "-c", wd)
	}
	pane, err := run(append(args, "sh -c "+Quote(inner))...)
	if err != nil {
		return "", err
	}
	run("set-option", "-p", "-t", pane, "remain-on-exit", "off")
	run("set-option", "-p", "-t", pane, "@gw_companion", "margin")
	return pane, nil
}

type Layout struct {
	Dir  string
	Name string
	// AgentCmd starts the agent with its instructions, as a shell command.
	AgentCmd string
	// InAgent means margin was run from inside the agent's own pane; Kickoff is typed into it instead.
	InAgent       bool
	Kickoff       string
	Viewer        func(agentPane string) []string
	ViewerPercent int
}

// Launch puts the viewer beside the agent. Inside tmux it only splits the current pane: an agent
// already running there gets Kickoff as a message, otherwise this process becomes the agent.
// Outside tmux it creates a session and attaches to it.
func Launch(l Layout) error {
	if InTmux() {
		pane := CurrentPane()
		if _, err := Split(pane, l.Viewer(pane), l.ViewerPercent); err != nil {
			return err
		}
		if l.InAgent {
			return SendLater(pane, l.Kickoff, 1500*time.Millisecond)
		}
		if l.Dir != "" {
			if err := os.Chdir(l.Dir); err != nil {
				return err
			}
		}
		return syscall.Exec("/bin/sh", []string{"sh", "-c", l.AgentCmd}, os.Environ())
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		return errors.New("tmux is needed to run the agent next to the viewer; install it or use --no-agent")
	}
	session := sessionName(l.Name)
	args := []string{"new-session", "-d", "-P", "-F", "#{pane_id}", "-s", session, "-n", l.Name, "-c", l.Dir}
	if w, h, err := term.GetSize(os.Stdout.Fd()); err == nil {
		args = append(args, "-x", strconv.Itoa(w), "-y", strconv.Itoa(h))
	}
	agent, err := run(append(args, l.AgentCmd)...)
	if err != nil {
		return err
	}
	if _, err := Split(agent, l.Viewer(agent), l.ViewerPercent); err != nil {
		return err
	}
	return syscall.Exec(tmux, []string{"tmux", "attach-session", "-t", session}, os.Environ())
}

type Session struct {
	Socket        string
	Dir           string
	AgentCmd      string
	Viewer        func(agentPane string) []string
	ViewerPercent int
	// ViewerAlive reports whether the viewer still runs; when it does not, reattaching adds a new one.
	ViewerAlive func() bool
}

// RunSession attaches this terminal to a private tmux server holding the viewer above the agent,
// creating it first unless it is already running, in which case the review is simply resumed.
func RunSession(s Session) error {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		return errors.New("tmux is needed to run the agent next to the viewer; install it or use --no-agent")
	}
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "TMUX=") && !strings.HasPrefix(e, "TMUX_PANE=") {
			env = append(env, e)
		}
	}
	inner := func(args ...string) (string, error) {
		cmd := exec.Command(tmux, append([]string{"-L", s.Socket, "-f", "/dev/null"}, args...)...)
		cmd.Env = env
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("tmux %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
		}
		return strings.TrimSpace(string(out)), nil
	}
	if _, err := inner("has-session", "-t", "=review"); err == nil && s.ViewerAlive != nil && !s.ViewerAlive() {
		agent := ""
		if out, err := inner("list-panes", "-t", "=review", "-F", "#{pane_id}\t#{pane_start_command}"); err == nil {
			for _, line := range strings.Split(out, "\n") {
				if id, start, ok := strings.Cut(line, "\t"); ok && !strings.Contains(start, "open") {
					agent = id
					break
				}
			}
		}
		if agent == "" {
			inner("kill-server")
		} else if _, err := inner("split-window", "-v", "-b", "-l", fmt.Sprintf("%d%%", s.ViewerPercent), "-t", agent, "-c", s.Dir, hold(s.Viewer(agent))); err != nil {
			return err
		}
	}
	if _, err := inner("has-session", "-t", "=review"); err != nil {
		args := []string{"new-session", "-d", "-s", "review", "-P", "-F", "#{pane_id}", "-c", s.Dir}
		if w, h, err := term.GetSize(os.Stdout.Fd()); err == nil {
			args = append(args, "-x", strconv.Itoa(w), "-y", strconv.Itoa(h))
		}
		agent, err := inner(append(args, s.AgentCmd)...)
		if err != nil {
			return err
		}
		for _, opt := range [][]string{{"status", "off"}, {"mouse", "on"}, {"escape-time", "10"}} {
			inner("set-option", "-g", opt[0], opt[1])
		}
		inner("set-option", "-ga", "terminal-features", ",*:RGB")
		if _, err := inner("split-window", "-v", "-b", "-l", fmt.Sprintf("%d%%", s.ViewerPercent), "-t", agent, "-c", s.Dir, hold(s.Viewer(agent))); err != nil {
			inner("kill-server")
			return err
		}
	}
	return syscall.Exec(tmux, []string{"tmux", "-L", s.Socket, "attach-session", "-t", "=review"}, env)
}

func hold(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = Quote(a)
	}
	return "sh -c " + Quote(strings.Join(quoted, " ")+` || { printf '\nmargin exited with an error, press enter to close '; read -r _; }`)
}

func SessionName() string {
	name, _ := run("display-message", "-p", "#S")
	return name
}

// PaneStartedWith reports whether any pane on the current tmux server was started with s in its command.
func PaneStartedWith(s string) bool {
	out, err := run("list-panes", "-a", "-F", "#{pane_start_command}")
	return err == nil && strings.Contains(out, s)
}

// SendLater types text into pane and submits it after a delay, from a tmux background job, so it
// arrives once the command that scheduled it has returned control to the agent.
func SendLater(pane, text string, delay time.Duration) error {
	socket, err := run("display-message", "-p", "-t", pane, "#{socket_path}")
	if err != nil {
		return err
	}
	client := "tmux -S " + Quote(socket)
	job := fmt.Sprintf("sleep %.1f; %s send-keys -t %s -l -- %s; sleep 0.2; %s send-keys -t %s Enter",
		delay.Seconds(), client, Quote(pane), Quote(text), client, Quote(pane))
	_, err = run("run-shell", "-b", job)
	return err
}

func sessionName(name string) string {
	base := strings.NewReplacer(".", "-", ":", "-", " ", "-").Replace(name)
	s := base
	for n := 2; exec.Command("tmux", "has-session", "-t", "="+s).Run() == nil; n++ {
		s = fmt.Sprintf("%s-%d", base, n)
	}
	return s
}

func PaneCommand(pane string) (string, error) {
	return run("display-message", "-p", "-t", pane, "#{pane_current_command}")
}

// Send types text into pane, refusing plain shells so the text is never run as a command.
func Send(pane, text string, submit bool) error {
	if pane == "" {
		return errors.New("no agent pane")
	}
	cmd, err := PaneCommand(pane)
	if err != nil {
		return err
	}
	if shells[cmd] {
		return fmt.Errorf("pane %s is running %s, not an agent", pane, cmd)
	}
	if _, err := run("send-keys", "-t", pane, "-l", "--", text); err != nil {
		return err
	}
	if !submit {
		return nil
	}
	time.Sleep(150 * time.Millisecond)
	_, err = run("send-keys", "-t", pane, "Enter")
	return err
}
