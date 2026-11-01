// Package config holds the settings shared by margin-tui and margin-gui, kept in
// $XDG_CONFIG_HOME/margin/config.yaml (or $MARGIN_CONFIG).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/redrick/margin/internal/fsx"
)

// Where reviews are kept, unless Reviews is a directory.
const (
	InRepo  = "repo"
	InState = "state"
)

type Config struct {
	// Reviews is where review files go: "repo" for .margin/reviews inside the repository (the
	// default), "state" for ~/.local/state/margin, or a directory. A directory may use {project},
	// {topic} and {repo}; with {project} or {topic} it is treated as a notes folder shared with
	// other notes, so files are named margin-… and start with frontmatter.
	Reviews string `yaml:"reviews,omitempty" json:"reviews"`
	// Agent is the command that starts the agent. The instructions are passed as its last
	// argument, or in place of {prompt}.
	Agent string `yaml:"agent,omitempty" json:"agent"`
	// AgentNote is added to the agent's instructions, for example how to file the review in a
	// notes vault. It may use {project}, {topic} and {dir}.
	AgentNote string `yaml:"agent_note,omitempty" json:"agent_note"`
}

// Agents are commands known to start an interactive session with an initial prompt.
var Agents = []string{"claude", "codex", "gemini -i {prompt}", "opencode --prompt {prompt}"}

// Path is the settings file.
func Path() string {
	if p := os.Getenv("MARGIN_CONFIG"); p != "" {
		return p
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(os.TempDir(), "margin", "config.yaml")
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "margin", "config.yaml")
}

// File reads the settings file as written; a missing file is an empty Config.
func File() (Config, error) {
	var c Config
	data, err := os.ReadFile(Path())
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("%s: %w", Path(), err)
	}
	return c, nil
}

// Load returns the settings in effect: the file, then $MARGIN_DIR and $MARGIN_AGENT, then defaults.
// A broken settings file falls back to the defaults, so margin still starts.
func Load() Config {
	c, _ := File()
	if d := strings.TrimSpace(os.Getenv("MARGIN_DIR")); d != "" {
		c.Reviews = filepath.Join(d, "{repo}")
	}
	if a := strings.TrimSpace(os.Getenv("MARGIN_AGENT")); a != "" {
		c.Agent = a
	}
	c.Reviews = strings.TrimSpace(c.Reviews)
	if c.Reviews == "" {
		c.Reviews = InRepo
	}
	if strings.TrimSpace(c.Agent) == "" {
		c.Agent = "claude"
	}
	return c
}

// Save writes c as the settings file.
func (c Config) Save() error {
	if err := c.Check(); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	head := "# margin settings, shared by margin-tui and margin-gui. `margin-tui config` explains them.\n"
	if err := os.MkdirAll(filepath.Dir(Path()), 0o755); err != nil {
		return err
	}
	return fsx.WriteFileAtomic(Path(), append([]byte(head), data...), 0o644)
}

// Check rejects settings that cannot work.
func (c Config) Check() error {
	r := strings.TrimSpace(c.Reviews)
	if r != "" && r != InRepo && r != InState && !filepath.IsAbs(Expand(r)) {
		return fmt.Errorf("reviews must be %q, %q or an absolute directory, not %q", InRepo, InState, r)
	}
	if len(strings.Fields(c.Agent)) == 0 && strings.TrimSpace(c.Agent) != "" {
		return errors.New("agent is blank")
	}
	return nil
}

// NotesFolder says whether a reviews directory is shared with other notes.
func NotesFolder(reviews string) bool {
	return strings.Contains(reviews, "{project}") || strings.Contains(reviews, "{topic}")
}

// Expand replaces a leading ~ with the home directory.
func Expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// AgentArgv is the agent command with prompt as its last argument, or in place of {prompt}.
func (c Config) AgentArgv(prompt string) []string {
	argv := strings.Fields(c.Agent)
	for i, a := range argv {
		if a == "{prompt}" {
			argv[i] = prompt
			return argv
		}
	}
	return append(argv, prompt)
}

// AgentShell is AgentArgv as one shell command line.
func (c Config) AgentShell(prompt string) string {
	argv := c.AgentArgv(prompt)
	for i, a := range argv {
		argv[i] = quote(a)
	}
	return strings.Join(argv, " ")
}

func quote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./=:@%+,") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
