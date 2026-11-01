package cli

import (
	"os"
	"testing"

	"github.com/redrick/margin/skills"
)

func TestInstallSkill(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	path, err := InstallSkill("claude")
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != string(skills.Margin) {
		t.Fatal("installed skill differs from the shipped one")
	}
	for _, s := range Skills() {
		if s.Agent == "claude" && !s.Current || s.Agent == "codex" && s.Installed {
			t.Errorf("status %+v", s)
		}
	}
	if _, err := InstallSkill("nope"); err == nil {
		t.Error("unknown agent should fail")
	}
}
