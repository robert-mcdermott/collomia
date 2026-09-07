package tui

import (
	"errors"
	"io"
	"sync"
	"time"

	"github.com/charmbracelet/x/term"
)

var ErrTerminalOutputStalled = errors.New("terminal output stopped accepting data; session retained for resume in a working terminal")

// GuardedOutput bounds a renderer write when a PTY host stops draining output.
// A failed writer stays failed: subsequent teardown writes must not deadlock
// again. At most one underlying write can remain blocked until process exit.
// This wrapper belongs only on interactive display output, never session data.
type GuardedOutput struct {
	mu        sync.Mutex
	writer    io.Writer
	timeout   time.Duration
	err       error
	onFailure func(error)
}

// Bubble Tea detects terminal output through the complete term.File interface,
// not Fd alone. Losing this contract prevents initial sizing and TUI startup.
var _ term.File = (*GuardedOutput)(nil)

func NewGuardedOutput(writer io.Writer, timeout time.Duration, onFailure func(error)) *GuardedOutput {
	return &GuardedOutput{writer: writer, timeout: timeout, onFailure: onFailure}
}

func (w *GuardedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	// The renderer can reuse its buffer after timeout while the kernel write
	// is still blocked. Give that one write its own immutable bytes.
	data := append([]byte(nil), p...)
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() { n, err := w.writer.Write(data); done <- result{n, err} }()
	timer := time.NewTimer(w.timeout)
	defer timer.Stop()
	n := 0
	select {
	case r := <-done:
		n, w.err = r.n, r.err
		if w.err == nil && n != len(p) {
			w.err = io.ErrShortWrite
		}
	case <-timer.C:
		w.err = ErrTerminalOutputStalled
	}
	if w.err != nil && w.onFailure != nil {
		w.onFailure(w.err)
	}
	return n, w.err
}

func (w *GuardedOutput) Err() error { w.mu.Lock(); defer w.mu.Unlock(); return w.err }

// Read preserves the terminal-file contract; only display writes are guarded.
func (w *GuardedOutput) Read(p []byte) (int, error) {
	if reader, ok := w.writer.(io.Reader); ok {
		return reader.Read(p)
	}
	return 0, errors.ErrUnsupported
}

// Close does not close the borrowed output. Its creator owns the terminal's
// lifetime, including shared stdout; renderer teardown must leave it open.
func (w *GuardedOutput) Close() error { return nil }

// Fd preserves terminal detection and window sizing through the wrapper.
func (w *GuardedOutput) Fd() uintptr {
	if file, ok := w.writer.(interface{ Fd() uintptr }); ok {
		return file.Fd()
	}
	return ^uintptr(0)
}
