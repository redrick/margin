package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestAgentArgv(t *testing.T) {
	for agent, want := range map[string][]string{
		"claude":                     {"claude", "go"},
		"gemini -i {prompt}":         {"gemini", "-i", "go"},
		"opencode --prompt {prompt}": {"opencode", "--prompt", "go"},
		"claude --model opus":        {"claude", "--model", "opus", "go"},
	} {
		if got := (Config{Agent: agent}).AgentArgv("go"); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: argv = %q, want %q", agent, got, want)
		}
	}
	if got := (Config{Agent: "claude"}).AgentShell("it's"); got != `claude 'it'\''s'` {
		t.Errorf("shell = %s", got)
	}
}

func TestLoadSaveAndEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MARGIN_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("MARGIN_DIR", "")
	t.Setenv("MARGIN_AGENT", "")
	if c := Load(); c.Reviews != InRepo || c.Agent != "claude" {
		t.Fatalf("defaults = %+v", c)
	}
	if err := (Config{Reviews: "notes"}).Save(); err == nil {
		t.Fatal("a relative reviews directory should be refused")
	}
	if err := (Config{Reviews: "~/notes/{project}", Agent: "codex"}).Save(); err != nil {
		t.Fatal(err)
	}
	if c := Load(); c.Reviews != "~/notes/{project}" || c.Agent != "codex" || !NotesFolder(c.Reviews) {
		t.Fatalf("saved = %+v", c)
	}
	t.Setenv("MARGIN_DIR", "/r")
	t.Setenv("MARGIN_AGENT", "gemini -i {prompt}")
	if c := Load(); c.Reviews != "/r/{repo}" || c.Agent != "gemini -i {prompt}" || NotesFolder(c.Reviews) {
		t.Fatalf("env = %+v", c)
	}
}
