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

func TestTestCommandFor(t *testing.T) {
	c := Config{Tests: []TestCommand{
		{Repo: "/work/api", Command: "docker compose run --rm app go test {dir} -run {run}"},
		{Repo: "shop-*", Command: "make test T={run}"},
		{Command: "go test {dir} -run {run}"},
	}}
	for _, tc := range []struct{ dir, main, want string }{
		{"/work/api", "", "docker compose run --rm app go test {dir} -run {run}"},
		{"/work/api/sub", "", "docker compose run --rm app go test {dir} -run {run}"},
		{"/wt/api-feature", "/work/api", "docker compose run --rm app go test {dir} -run {run}"},
		{"/work/apis", "", "go test {dir} -run {run}"},
		{"/x/shop-web", "", "make test T={run}"},
	} {
		got, ok := c.TestCommandFor(tc.dir, tc.main)
		if !ok || got.Command != tc.want {
			t.Errorf("%s: got %q, want %q", tc.dir, got.Command, tc.want)
		}
	}
	if _, ok := (Config{}).TestCommandFor("/a", ""); ok {
		t.Error("no tests set up should match nothing")
	}
	if err := (Config{Tests: []TestCommand{{Repo: "x"}}}).Check(); err == nil {
		t.Error("a blank test command should not pass Check")
	}
}
