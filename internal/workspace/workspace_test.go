package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// Repo makes a repository at dir with one commit on main and a branch feature one commit ahead.
func repo(t *testing.T, dir string) {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	git(t, dir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "first")
	git(t, dir, "checkout", "-q", "-b", "feature")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("b\n"), 0o644)
	git(t, dir, "commit", "-q", "-am", "second")
	git(t, dir, "checkout", "-q", "main")
}

func TestFoldersAndTargets(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	repo(t, filepath.Join(root, "one"))
	repo(t, filepath.Join(root, "group", "two"))
	os.MkdirAll(filepath.Join(root, "empty"), 0o755)

	if _, err := AddFolder(filepath.Join(root, "empty")); err == nil {
		t.Fatal("a folder without repositories is refused")
	}
	fs, err := AddFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || len(fs[0].Repos) != 2 || fs[0].Repos[0].Name != "one" || fs[0].Repos[1].Name != "two" {
		t.Fatalf("both repositories are found, two levels down: %+v", fs)
	}

	dir := fs[0].Repos[0].Path
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x\n"), 0o644)
	ts, err := TargetsIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ts.Branch != "main" || ts.Dirty != 1 || len(ts.Commits) != 1 {
		t.Fatalf("the worktree's state: %+v", ts)
	}
	var feature *Branch
	for i := range ts.Branches {
		if ts.Branches[i].Name == "feature" {
			feature = &ts.Branches[i]
		}
	}
	if feature == nil || feature.Ahead != 1 {
		t.Fatalf("feature is one commit ahead of main: %+v", ts.Branches)
	}

	if fs, _ := RemoveFolder(root); len(fs) != 0 {
		t.Fatalf("the folder is gone: %+v", fs)
	}
}
