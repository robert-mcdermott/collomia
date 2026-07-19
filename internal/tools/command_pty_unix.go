//go:build !windows

package tools

import (
	"context"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// ptySupported reports whether this platform can run commands under a
// pseudo-terminal.
const ptySupported = true

// runUnderPTY executes argv attached to a pseudo-terminal, streaming its
// output into the buffer. Programs that refuse to run non-interactively or
// colorize/paginate based on isatty behave normally under it. The child
// runs in its own session (setsid, which pty requires) and cancellation
// kills the whole group.
func runUnderPTY(ctx context.Context, argv []string, dir string, env []string, buffer io.Writer) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	if os.Getenv("TERM") == "" && env == nil {
		cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	}
	// pty.Start sets Setsid, making the child a session and group leader,
	// so killing -pid reaches every descendant. Setpgid must not also be
	// set; this replaces setProcessGroup for the PTY path.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
	master, err := pty.Start(cmd)
	if err != nil {
		return err
	}
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		// Reading from the master returns EIO once the child exits; that
		// is the normal end-of-stream signal for a pty, not a failure.
		_, _ = io.Copy(buffer, master)
	}()
	err = cmd.Wait()
	_ = master.Close()
	<-copied
	return err
}
