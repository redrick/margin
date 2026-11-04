//go:build !windows

package testrun

import (
	"os/exec"
	"syscall"
)

// setGroup puts the command in its own process group, so stopping it also stops what it started.
func setGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
