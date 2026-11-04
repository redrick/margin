// Command margin-gui is the desktop app of margin, built with Wails: pick a repository, a
// worktree and what to review, and read the review beside the agent writing it.
package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/redrick/margin/internal/cli"
	"github.com/redrick/margin/internal/tryout"
)

//go:embed assets
var assets embed.FS

const usage = `margin-gui — review git changes with an agent beside you, in a desktop window

  margin-gui                         open the window
  margin-gui <folder>                add a repository, or a folder of repositories, and open it
  margin-gui <repo> <branch|commit>  open the review of a branch or commit (. for uncommitted changes)
  margin-gui <file.review.yaml>      open a review file
  margin-gui --install               add margin to the desktop's applications (--uninstall removes it)
  margin-gui --version

  Settings (where reviews are kept, which agent runs) are shared with margin-tui. The agent gets a
  margin command that runs this binary; MARGIN_CLI=1 margin-gui <command> runs it too.
`

func main() {
	// Called as `margin` (the link the agent runs) or with MARGIN_CLI=1, it is the command line.
	if filepath.Base(os.Args[0]) == "margin" || os.Getenv("MARGIN_CLI") == "1" {
		os.Exit(cli.Run(os.Args[1:]))
	}
	if len(os.Args) == 2 && (os.Args[1] == "--install" || os.Args[1] == "--uninstall") {
		if err := install(os.Args[1] == "--uninstall"); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println("margin-gui", cli.Version())
		return
	}
	if len(os.Args) == 2 && (os.Args[1] == "--help" || os.Args[1] == "-h" || os.Args[1] == "help") {
		fmt.Print(usage)
		return
	}
	ws, rv, ag := &Workspace{}, &Reviews{}, &Agents{}
	rv.recovered, rv.recoverErr = recoverTrials()
	app := application.New(application.Options{
		Name:        "margin",
		Description: "Review git changes with an agent beside you",
		Services: []application.Service{
			application.NewService(ws),
			application.NewService(rv),
			application.NewService(ag),
			application.NewService(&Settings{}),
		},
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(assets),
		},
		Mac: application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	ws.app, rv.app, ag.app = app, app, app
	rv.agents = ag

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "margin",
		Width:            1500,
		Height:           950,
		URL:              "/",
		BackgroundColour: application.NewRGB(13, 17, 23),
		DevToolsEnabled:  true,
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// recoverTrials puts back lines a margin that died mid-run left changed.
func recoverTrials() ([]string, string) {
	fixed, err := tryout.Recover()
	if err != nil {
		return fixed, err.Error()
	}
	return fixed, ""
}
