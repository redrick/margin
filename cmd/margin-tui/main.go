// Command margin-tui reviews git changes in the terminal, with a coding agent beside the viewer.
package main

import (
	"os"

	"github.com/redrick/margin/internal/cli"
)

func main() { os.Exit(cli.Run(os.Args[1:])) }
