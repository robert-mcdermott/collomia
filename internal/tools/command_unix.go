//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
	"time"
)

// setProcessGroup runs the command in its own process group and makes
// cancellation kill the whole group, so pipelines and background children
// cannot outlive a timeout or a user interrupt.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid signals the entire process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
}
