// Package clip puts text on the system clipboard.
package clip

import (
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/atotto/clipboard"
)

// Copy tries the desktop clipboard tools (wl-copy, xclip, xsel, pbcopy) first, then tmux, which
// forwards the text to the terminal's clipboard.
func Copy(s string) error {
	err := clipboard.WriteAll(s)
	if err == nil {
		return nil
	}
	if os.Getenv("TMUX") != "" {
		cmd := exec.Command("tmux", "load-buffer", "-w", "-")
		cmd.Stdin = strings.NewReader(s)
		if cmd.Run() == nil {
			return nil
		}
	}
	return errors.New("no clipboard: install wl-clipboard (Wayland) or xclip (X11)")
}
