package cli

import (
	"os"
	"path/filepath"
	"strings"
)

// The agent always calls `margin`, whichever binary started it. ShimDir holds a `margin` link to
// the running executable, and AgentEnv puts it first on the agent's PATH, so the agent runs the
// same version as the viewer it talks to. Each binary gets its own directory, so margin-tui and
// margin-gui running side by side do not take the link from each other.

// ShimDir returns the directory with the `margin` link, creating or refreshing the link.
func ShimDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	dir := filepath.Join(dataDir(), "bin", strings.TrimSuffix(filepath.Base(exe), ".exe"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	link := filepath.Join(dir, "margin")
	if cur, err := os.Readlink(link); err == nil && cur == exe {
		return dir, nil
	}
	tmp := link + ".new"
	_ = os.Remove(tmp)
	if err := os.Symlink(exe, tmp); err != nil {
		return "", err
	}
	return dir, os.Rename(tmp, link)
}

// Self is the command that runs this binary as the command line: the `margin` link when it can be
// made, since margin-gui opens a window unless it is called as margin.
func Self() (string, error) {
	dir, err := ShimDir()
	if err != nil {
		return os.Executable()
	}
	return filepath.Join(dir, "margin"), nil
}

// AgentEnv returns env with the shim directory first on PATH, or env unchanged if the link
// cannot be made.
func AgentEnv(env []string) []string {
	dir, err := ShimDir()
	if err != nil {
		return env
	}
	out := make([]string, 0, len(env)+1)
	path := os.Getenv("PATH")
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "PATH="); ok {
			path = v
			continue
		}
		out = append(out, e)
	}
	return append(out, "PATH="+dir+string(os.PathListSeparator)+path)
}

func dataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "margin")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "margin")
	}
	return filepath.Join(home, ".local", "share", "margin")
}
