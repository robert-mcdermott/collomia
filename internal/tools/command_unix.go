//go:build !windows

package tools

import (
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// A shell signal status is 128 plus a valid signal, not every status >=128.
// npm uses 254 for ENOENT. Treating that as interruption fences a safe repair.
func ordinaryCommandExit(code int) bool {
	if code <= 0 || code > 255 {
		return false
	}
	signal := code - 128
	if signal > 0 && (unix.SignalName(syscall.Signal(signal)) != "" ||
		(runtime.GOOS == "linux" && signal <= 64)) {
		return false
	}
	return true
}

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
