//go:build windows

package tools

import (
	"context"
	"io"
	"time"

	"github.com/robert-mcdermott/collomia/internal/conpty"
	"golang.org/x/sys/windows"
)

// ptySupported reports whether this platform can run commands under a
// pseudo-terminal.
const ptySupported = conpty.Supported

// runUnderPTY executes argv attached to a Windows pseudoconsole, streaming its
// output into the buffer. Programs that refuse to run non-interactively or
// that colorize based on whether they own a console behave normally under it.
//
// The cancellation contract matches the ordinary command path rather than
// merely resembling it. The child is created suspended and assigned to a job
// object before it runs, so cancellation kills the whole tree with no window
// in which a descendant could escape it, and the existing descendant walk is
// reused to wait for the kernel to finish the teardown — returning earlier
// leaves processes holding the workspace directory open, which breaks
// workspace removal at shutdown.
func runUnderPTY(ctx context.Context, argv []string, dir string, env []string, buffer io.Writer) error {
	process, err := conpty.Start(conpty.Config{Argv: argv, Dir: dir, Env: env})
	if err != nil {
		return err
	}
	// Descendants are enumerated at cancellation time, not at exit: once the
	// tree is gone there is nothing left to enumerate, and the point is to
	// hold handles opened while those processes still existed.
	watcher := make(chan struct{})
	go func() {
		defer close(watcher)
		select {
		case <-ctx.Done():
			descendants := openDescendants(uint32(process.Pid()))
			_ = process.Terminate()
			waitAllExited(descendants, 5*time.Second)
			for _, handle := range descendants {
				_ = windows.CloseHandle(handle)
			}
		case <-process.Done():
		}
	}()

	copied := make(chan struct{})
	go func() {
		defer close(copied)
		// The read ends when Close releases the pseudoconsole, which is the
		// normal end-of-stream for a console rather than a failure.
		_, _ = io.Copy(buffer, process)
	}()

	_, waitErr := process.Wait()
	// Close is what unblocks the copy above: it releases the console, which
	// releases the output pipe's last writer.
	_ = process.Close()
	<-copied
	<-watcher
	if ctxErr := ctx.Err(); ctxErr != nil {
		// A killed tree reports a termination status that says nothing useful.
		// The caller's timeout and cancellation handling keys on the context
		// error, exactly as it does without a PTY.
		return ctxErr
	}
	return waitErr
}
