//go:build !windows

package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
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
	// PTY masters need nonblocking mode for Go's poller to enforce read
	// deadlines and interrupt a read on Close (creack/pty's documented contract).
	// Re-wrap a duplicate after setting O_NONBLOCK: on macOS pty.Open uses
	// os.NewFile on a blocking descriptor, so setting the flag alone does not
	// register that existing os.File with the runtime poller.
	fd, pollErr := unix.FcntlInt(master.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if pollErr == nil {
		pollErr = syscall.SetNonblock(fd, true)
		if pollErr != nil {
			_ = syscall.Close(fd)
		}
	}
	_ = master.Close()
	if pollErr != nil {
		_ = cmd.Cancel()
		_ = cmd.Wait()
		return fmt.Errorf("configure PTY output polling: %w", pollErr)
	}
	master = os.NewFile(uintptr(fd), "pty-output")
	defer master.Close()
	copied := make(chan error, 1)
	go func() {
		// Reading from the master returns EIO once the child exits; that
		// is the normal end-of-stream signal for a pty, not a failure.
		_, copyErr := io.Copy(buffer, master)
		copied <- copyErr
	}()
	err = cmd.Wait()
	// Process exit does not mean the reader has collected the trailing bytes.
	// Drain to EOF/EIO before closing the master. Bound the drain because a
	// descendant can retain the slave after the command's direct child exits;
	// exec.Cmd.WaitDelay does not cover this separately managed reader.
	if deadlineErr := master.SetReadDeadline(time.Now().Add(cmd.WaitDelay)); deadlineErr != nil {
		_ = cmd.Cancel()
		_ = master.Close()
		<-copied
		return fmt.Errorf("set PTY output drain deadline: %w", deadlineErr)
	}
	var copyErr error
	select {
	case copyErr = <-copied:
		if errors.Is(copyErr, os.ErrDeadlineExceeded) {
			_ = cmd.Cancel()
			copyErr = exec.ErrWaitDelay
		}
	case <-ctx.Done():
		_ = cmd.Cancel()
		_ = master.Close()
		<-copied
		return ctx.Err()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if copyErr != nil && !errors.Is(copyErr, syscall.EIO) {
		return fmt.Errorf("drain PTY output: %w", copyErr)
	}
	return err
}
