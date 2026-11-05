package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/redrick/margin/internal/workspace"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// pickerRepos makes a folder with two repositories, the first with a branch "feature" and an
// uncommitted file, and puts the folder on the picker's list.
func pickerRepos(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("GH_CONFIG_DIR", t.TempDir())
	bin := t.TempDir()
	os.Symlink(must(exec.LookPath("git")), filepath.Join(bin, "git"))
	t.Setenv("PATH", bin)
	root := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(root, name)
		os.MkdirAll(dir, 0o755)
		gitIn(t, dir, "init", "-q", "-b", "main")
		os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
		gitIn(t, dir, "add", ".")
		gitIn(t, dir, "commit", "-q", "-m", "first in "+name)
	}
	alpha := filepath.Join(root, "alpha")
	gitIn(t, alpha, "checkout", "-q", "-b", "feature")
	os.WriteFile(filepath.Join(alpha, "a.txt"), []byte("b\n"), 0o644)
	gitIn(t, alpha, "commit", "-q", "-am", "the feature")
	gitIn(t, alpha, "checkout", "-q", "main")
	os.WriteFile(filepath.Join(alpha, "new.txt"), []byte("x\n"), 0o644)
	if _, err := workspace.AddFolder(root); err != nil {
		t.Fatal(err)
	}
	return root
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// drive runs a command and feeds what it returns back into the picker, until nothing is left.
func drive(p *Picker, cmd tea.Cmd) {
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case nil, tea.QuitMsg:
		default:
			_, next := p.Update(msg)
			queue = append(queue, next)
		}
	}
}

func newPicker(t *testing.T, here string) *Picker {
	t.Helper()
	p := NewPicker(here)
	p.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	drive(p, p.Init())
	return p
}

func pkeys(p *Picker, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		_, cmd := p.Update(msg)
		drive(p, cmd)
	}
}

func TestPickerBranch(t *testing.T) {
	root := pickerRepos(t)
	p := newPicker(t, t.TempDir())
	view := ansi.Strip(p.View())
	for _, want := range []string{"alpha", "beta", "1 changed", "uncommitted changes · 1 file", "feature", "first in alpha"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the picker shows %q:\n%s", want, view)
		}
	}
	pkeys(p, "enter")
	if p.focus != 1 {
		t.Fatal("enter on a worktree goes to what it offers")
	}
	pkeys(p, "/", "feat", "enter", "enter")
	c := p.Choice()
	if c == nil || c.Dir != filepath.Join(root, "alpha") || strings.Join(c.Args, " ") != "feature" {
		t.Fatalf("picking the branch starts its review in alpha: %+v", c)
	}
}

func TestPickerMouse(t *testing.T) {
	root := pickerRepos(t)
	p := newPicker(t, t.TempDir())
	beta := -1
	for i, it := range p.cols[0].vis {
		if it.text == "beta" {
			beta = i
		}
	}
	click := func(x, y int) {
		_, cmd := p.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		drive(p, cmd)
	}
	click(3, beta+1)
	if it, _ := p.cols[0].item(); it.dir != filepath.Join(root, "beta") {
		t.Fatalf("a click selects beta: %+v", it)
	}
	if view := ansi.Strip(p.View()); !strings.Contains(view, "first in beta") || !strings.Contains(view, "nothing uncommitted") {
		t.Fatalf("the middle column shows beta:\n%s", view)
	}
	x, _ := p.columns()
	commit := -1
	for i, it := range p.cols[1].vis {
		if it.commit != nil {
			commit = i
		}
	}
	click(x[1]+3, commit+1)
	if p.focus != 1 || p.Choice() != nil {
		t.Fatal("one click on a commit selects it")
	}
	click(x[1]+3, commit+1)
	if c := p.Choice(); c == nil || len(c.Args) != 1 || len(c.Args[0]) != 40 {
		t.Fatalf("a double click starts the commit's review: %+v", c)
	}
}

func TestPickerHereAndFolders(t *testing.T) {
	root := pickerRepos(t)
	other := filepath.Join(t.TempDir(), "gamma")
	os.MkdirAll(other, 0o755)
	gitIn(t, other, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(other, "a.txt"), []byte("a\n"), 0o644)
	gitIn(t, other, "add", ".")
	gitIn(t, other, "commit", "-q", "-m", "gamma")

	p := newPicker(t, other)
	if it, _ := p.cols[0].item(); it.dir != other {
		t.Fatalf("the repository here comes first: %+v", it)
	}
	pkeys(p, "j", "j", "X")
	if fs := workspace.Folders(); len(fs) != 0 {
		t.Fatalf("X takes the folder off the list: %+v", fs)
	}
	if _, err := os.Stat(filepath.Join(root, "alpha")); err != nil {
		t.Fatal("and leaves it on disk")
	}
	pkeys(p, "a")
	p.input.SetValue(root)
	pkeys(p, "enter")
	if fs := workspace.Folders(); len(fs) != 1 || fs[0].Path != root {
		t.Fatalf("a adds a folder: %+v", fs)
	}
}
