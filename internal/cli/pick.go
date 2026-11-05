package cli

import (
	"errors"
	"flag"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/redrick/margin/internal/gitx"
	"github.com/redrick/margin/internal/tui"
	"github.com/redrick/margin/internal/workspace"
)

// cmdPick lets the reader choose what to review from their folders of repositories, then starts
// it as `margin [target]` in the chosen worktree would. A folder given is added to the list.
func cmdPick(args []string) error {
	fs := flag.NewFlagSet("pick", flag.ContinueOnError)
	noAgent := fs.Bool("no-agent", false, "")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return errors.New("usage: margin pick [folder] [--no-agent]")
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	if len(pos) == 1 {
		abs, err := filepath.Abs(pos[0])
		if err != nil {
			return err
		}
		if _, err := workspace.AddFolder(abs); err != nil {
			return err
		}
	}
	p := tui.NewPicker(wd)
	if _, err := tea.NewProgram(p, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run(); err != nil {
		return err
	}
	c := p.Choice()
	if c == nil {
		return nil
	}
	if c.Review != "" {
		args := []string{c.Review}
		if *noAgent {
			args = append(args, "--no-agent")
		}
		return cmdReview(args)
	}
	if err := os.Chdir(c.Dir); err != nil {
		return err
	}
	if *noAgent {
		c.Args = append(c.Args, "--no-agent")
	}
	return cmdReview(c.Args)
}

// inRepo says whether dir is inside a git worktree.
func inRepo(dir string) bool {
	_, err := gitx.Run(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil
}
