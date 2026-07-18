//go:build windows

package tools

import (
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// setProcessGroup gives the command its own process group and terminates the
// whole tree on cancellation via taskkill /T. Full job-object containment is
// tracked in the roadmap's phase 1 exit gate.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		if err := kill.Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
}
