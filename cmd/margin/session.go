package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/redrick/margin/internal/control"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/target"
	"github.com/redrick/margin/internal/tmuxx"
)

// cmdSession runs the review in this terminal: a private tmux server with the viewer on top and
// the agent below. A surrounding tmux, or a layout manager such as gw, only ever sees one pane.
func cmdSession(args []string) error {
	fs := flag.NewFlagSet("session", flag.ContinueOnError)
	reviewFlag := fs.String("review", "", "")
	isNew := fs.Bool("new", false, "")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if *reviewFlag == "" || len(pos) != 0 {
		return errors.New("usage: margin session --review <review.yaml> [--new]")
	}
	path, err := filepath.Abs(*reviewFlag)
	if err != nil {
		return err
	}
	r, err := review.Load(path)
	if err != nil {
		return err
	}
	agent := agentCommand()
	if err := requireAgent(agent); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(path))
	return tmuxx.RunSession(tmuxx.Session{
		Socket:   "margin-" + hex.EncodeToString(sum[:4]),
		Dir:      r.RepoDir(),
		AgentCmd: agent + " " + tmuxx.Quote(agentPrompt(r.Title, r.Kicker, r.Head != "", target.LocationOf(path), *isNew)),
		Viewer: func(pane string) []string {
			return []string{exe, "open", "--agent-pane", pane, path}
		},
		ViewerPercent: 76,
		ViewerAlive:   func() bool { return control.Alive(control.SocketPath(path)) },
	})
}

// openInGW asks gw for a new sub-window of the active worktree that runs `margin session`.
func openInGW(exe string, sessionArgs []string, path string) error {
	gw, err := exec.LookPath("gw")
	if err != nil {
		return errors.New("running inside gw, but the gw binary is not on PATH")
	}
	if out, err := exec.Command(gw, append([]string{"--new-subwindow", exe}, sessionArgs...)...).CombinedOutput(); err != nil {
		return fmt.Errorf("gw --new-subwindow: %v: %s", err, strings.TrimSpace(string(out)))
	}
	for range 30 {
		if tmuxx.PaneStartedWith(path) {
			fmt.Println("review opened in a new gw sub-window; ^a n / ^a p switch between it and this one")
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("gw did not start the review; this gw build cannot run a command in a sub-window yet")
}

func agentCommand() string {
	if agent := strings.TrimSpace(os.Getenv("MARGIN_AGENT")); agent != "" {
		return agent
	}
	return "claude"
}

func requireAgent(agent string) error {
	if _, err := exec.LookPath(strings.Fields(agent)[0]); err != nil {
		return fmt.Errorf("agent %q not found; set MARGIN_AGENT or use --no-agent", agent)
	}
	return nil
}
