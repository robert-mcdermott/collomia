//go:build windows

package webterminal

import (
	"context"
	"sync"
	"time"

	"github.com/robert-mcdermott/collomia/internal/conpty"
)

const platformPTYSupported = conpty.Supported

type ptyProcess struct {
	process   *conpty.Process
	closeOnce sync.Once
}

func startPTY(ctx context.Context, spec processSpec) (terminalProcess, error) {
	argv := append([]string{spec.Executable}, spec.Args...)
	process, err := conpty.Start(conpty.Config{
		Argv: argv, Dir: spec.Dir, Env: spec.Env,
		Cols: spec.Cols, Rows: spec.Rows,
	})
	if err != nil {
		return nil, err
	}
	proc := &ptyProcess{process: process}
	go proc.watchCancellation(ctx)
	return proc, nil
}

func (p *ptyProcess) Read(data []byte) (int, error)  { return p.process.Read(data) }
func (p *ptyProcess) Write(data []byte) (int, error) { return p.process.Write(data) }

func (p *ptyProcess) Resize(cols, rows int) error { return p.process.Resize(cols, rows) }

func (p *ptyProcess) Wait() (int, error) {
	code, err := p.process.Wait()
	// The served TUI owns this console. Once it exits, nothing it started
	// should survive as an orphan; the job object is what makes that reliable
	// rather than a best-effort walk of the process tree.
	_ = p.process.Terminate()
	if code < 0 {
		code = 1
	}
	return code, err
}

func (p *ptyProcess) Close() error {
	p.closeOnce.Do(func() { _ = p.process.Close() })
	return nil
}

// watchCancellation ends the child when the server's context is cancelled.
//
// The Unix path sends SIGTERM, waits, then SIGKILL. Windows has no signal to
// send a process on another console, so the graceful step is closing the
// child's console input: a TUI blocked on input sees it end and can shut down.
// The forceful step is the same either way. The grace period matches the Unix
// path's so a browser disconnect behaves the same on both platforms.
func (p *ptyProcess) watchCancellation(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-p.process.Done():
		return
	}
	_ = p.process.CloseInput()
	select {
	case <-time.After(2 * time.Second):
		_ = p.process.Terminate()
	case <-p.process.Done():
	}
}
