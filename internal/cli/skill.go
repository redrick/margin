package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/redrick/margin/internal/fsx"
	"github.com/redrick/margin/skills"
)

// Skill is where an agent looks for margin's skill, and whether it is there.
type Skill struct {
	Agent     string
	Path      string
	Installed bool
	// Current is set when the installed skill is the one this margin ships.
	Current bool
}

// Skills lists the agents margin can install its skill for.
func Skills() []Skill {
	home, _ := os.UserHomeDir()
	dirs := []struct{ agent, env, def string }{
		{"claude", "CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude")},
		{"codex", "CODEX_HOME", filepath.Join(home, ".codex")},
	}
	var out []Skill
	for _, d := range dirs {
		root := os.Getenv(d.env)
		if root == "" {
			root = d.def
		}
		s := Skill{Agent: d.agent, Path: filepath.Join(root, "skills", "margin", "SKILL.md")}
		if data, err := os.ReadFile(s.Path); err == nil {
			s.Installed, s.Current = true, bytes.Equal(data, skills.Margin)
		}
		out = append(out, s)
	}
	return out
}

// InstallSkill writes margin's skill where agent finds it and returns the path.
func InstallSkill(agent string) (string, error) {
	for _, s := range Skills() {
		if s.Agent != agent {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
			return "", err
		}
		return s.Path, fsx.WriteFileAtomic(s.Path, skills.Margin, 0o644)
	}
	return "", fmt.Errorf("unknown agent %q; margin installs its skill for claude and codex", agent)
}

func cmdSkill(args []string) error {
	switch {
	case len(args) == 0:
		_, err := os.Stdout.Write(skills.Margin)
		return err
	case args[0] == "status" && len(args) == 1:
		for _, s := range Skills() {
			status := "not installed"
			switch {
			case s.Current:
				status = "installed"
			case s.Installed:
				status = "installed, older or edited; install again to update"
			}
			fmt.Printf("%-7s %s  (%s)\n", s.Agent, status, s.Path)
		}
		return nil
	case args[0] == "install" && len(args) <= 2:
		agent := "claude"
		if len(args) == 2 {
			agent = args[1]
		}
		path, err := InstallSkill(agent)
		if err != nil {
			return err
		}
		fmt.Println("installed", path)
		return nil
	}
	return errors.New("usage: margin skill [status | install [claude|codex]]")
}
