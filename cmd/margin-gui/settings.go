package main

import (
	"os"
	"os/exec"
	"strings"

	"github.com/redrick/margin/internal/cli"
	"github.com/redrick/margin/internal/config"
)

// Settings is the shared margin settings file, seen and changed from the window.
type Settings struct{}

type SettingsView struct {
	Path   string        `json:"path"`
	File   config.Config `json:"file"`
	Effect config.Config `json:"effect"`
	Err    string        `json:"err,omitempty"`
	// EnvDir and EnvAgent are set when $MARGIN_DIR or $MARGIN_AGENT override the file.
	EnvDir   string        `json:"envDir,omitempty"`
	EnvAgent string        `json:"envAgent,omitempty"`
	Agents   []AgentChoice `json:"agents"`
	Skills   []SkillView   `json:"skills"`
}

type AgentChoice struct {
	Command string `json:"command"`
	Found   bool   `json:"found"`
}

type SkillView struct {
	Agent     string `json:"agent"`
	Path      string `json:"path"`
	Installed bool   `json:"installed"`
	Current   bool   `json:"current"`
}

func (s *Settings) Get() SettingsView {
	v := SettingsView{Path: config.Path(), Effect: config.Load(), EnvDir: os.Getenv("MARGIN_DIR"), EnvAgent: os.Getenv("MARGIN_AGENT")}
	f, err := config.File()
	if err != nil {
		v.Err = err.Error()
	}
	v.File = f
	for _, a := range config.Agents {
		_, err := exec.LookPath(strings.Fields(a)[0])
		v.Agents = append(v.Agents, AgentChoice{Command: a, Found: err == nil})
	}
	for _, sk := range cli.Skills() {
		v.Skills = append(v.Skills, SkillView{Agent: sk.Agent, Path: sk.Path, Installed: sk.Installed, Current: sk.Current})
	}
	return v
}

func (s *Settings) Save(c config.Config) (SettingsView, error) {
	c.Reviews, c.Agent, c.AgentNote = strings.TrimSpace(c.Reviews), strings.TrimSpace(c.Agent), strings.TrimSpace(c.AgentNote)
	if c.Tests == nil {
		old, err := config.File()
		if err != nil {
			return SettingsView{}, err
		}
		c.Tests = old.Tests
	}
	for i := range c.Tests {
		c.Tests[i].Repo, c.Tests[i].Command = strings.TrimSpace(c.Tests[i].Repo), strings.TrimSpace(c.Tests[i].Command)
	}
	if err := c.Save(); err != nil {
		return SettingsView{}, err
	}
	return s.Get(), nil
}

// InstallSkill writes margin's skill for agent ("claude" or "agents") so that agent knows margin
// even in a session margin did not start.
func (s *Settings) InstallSkill(agent string) (SettingsView, error) {
	if _, err := cli.InstallSkill(agent); err != nil {
		return SettingsView{}, err
	}
	return s.Get(), nil
}
