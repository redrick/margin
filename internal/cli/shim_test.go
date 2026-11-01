package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShimDirPerBinary(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir, err := ShimDir()
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	if filepath.Base(dir) != strings.TrimSuffix(filepath.Base(exe), ".exe") {
		t.Errorf("shim dir %s is not named after the binary %s", dir, exe)
	}
	if got, _ := os.Readlink(filepath.Join(dir, "margin")); got != exe {
		t.Errorf("margin links to %q, want %q", got, exe)
	}
	env := AgentEnv([]string{"PATH=/usr/bin", "HOME=/h"})
	if last := env[len(env)-1]; last != "PATH="+dir+string(os.PathListSeparator)+"/usr/bin" {
		t.Errorf("PATH = %q", last)
	}
}
