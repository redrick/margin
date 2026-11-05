package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/redrick/margin/internal/workspace"
)

// Workspace serves the sidebar: the folders the reader added, the repositories and worktrees in
// them, and what each worktree offers to review.
type Workspace struct {
	app *application.App
}

type (
	Folder      = workspace.Folder
	Repo        = workspace.Repo
	Worktree    = workspace.Worktree
	ReviewInfo  = workspace.ReviewInfo
	Branch      = workspace.Branch
	Commit      = workspace.Commit
	Targets     = workspace.Targets
	PullRequest = workspace.PullRequest
)

type Initial struct {
	Root   string `json:"root"`
	Target string `json:"target"`
	Review string `json:"review"`
}

// Initial is what the command line asked to open: margin-gui [folder] [branch | commit | .], or
// margin-gui <file.review.yaml>. A folder given here is added to the workspace.
func (w *Workspace) Initial() Initial {
	var in Initial
	if len(os.Args) > 1 && strings.HasSuffix(os.Args[1], ".review.yaml") {
		in.Review, _ = filepath.Abs(os.Args[1])
		return in
	}
	if len(os.Args) > 1 {
		if abs, err := filepath.Abs(os.Args[1]); err == nil {
			in.Root = abs
			_, _ = w.AddFolder(abs)
		}
	}
	if len(os.Args) > 2 {
		in.Target = os.Args[2]
	}
	return in
}

func (w *Workspace) PickFolder() (string, error) {
	return w.app.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		SetTitle("Add a repository or a folder of repositories").
		PromptForSingleSelection()
}

func (w *Workspace) Folders() []Folder                          { return workspace.Folders() }
func (w *Workspace) AddFolder(path string) ([]Folder, error)    { return workspace.AddFolder(path) }
func (w *Workspace) RemoveFolder(path string) ([]Folder, error) { return workspace.RemoveFolder(path) }
func (w *Workspace) Status(dirs []string) map[string]int        { return workspace.Status(dirs) }
func (w *Workspace) RecentReviews() []ReviewInfo                { return workspace.RecentReviews() }
func (w *Workspace) ForgetReview(path string) error             { return workspace.ForgetReview(path) }
func (w *Workspace) Targets(dir string) (*Targets, error)       { return workspace.TargetsIn(dir) }
func (w *Workspace) PullRequests(dir string) ([]PullRequest, error) {
	return workspace.PullRequests(dir)
}
func (w *Workspace) FetchPR(dir string, number int) error { return workspace.FetchPR(dir, number) }

func openedReview(path string) { workspace.OpenedReview(path) }
