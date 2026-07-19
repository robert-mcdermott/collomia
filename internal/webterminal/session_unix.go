//go:build !windows

package webterminal

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

const platformPTYSupported = true

type ptyProcess struct {
	cmd       *exec.Cmd
	master    *os.File
	done      chan struct{}
	closeDone sync.Once
	closePTY  sync.Once
}

func startPTY(ctx context.Context, spec processSpec) (terminalProcess, error) {
	cmd := exec.Command(spec.Executable, spec.Args...)
	cmd.Dir = spec.Dir
	if spec.Env != nil {
		cmd.Env = spec.Env
	}
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(spec.Cols), Rows: uint16(spec.Rows)})
	if err != nil {
		return nil, err
	}
	proc := &ptyProcess{cmd: cmd, master: master, done: make(chan struct{})}
	go proc.watchCancellation(ctx)
	return proc, nil
}

func (p *ptyProcess) Read(data []byte) (int, error)  { return p.master.Read(data) }
func (p *ptyProcess) Write(data []byte) (int, error) { return p.master.Write(data) }

func (p *ptyProcess) Resize(cols, rows int) error {
	return pty.Setsize(p.master, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (p *ptyProcess) Wait() (int, error) {
	err := p.cmd.Wait()
	p.closeDone.Do(func() { close(p.done) })
	// The TUI is the session leader. Once it exits, no descendant from that
	// terminal session should survive as an orphan.
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	code := p.cmd.ProcessState.ExitCode()
	if code < 0 {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			status, ok := exitErr.Sys().(syscall.WaitStatus)
			if ok && status.Signaled() {
				code = 128 + int(status.Signal())
			}
		}
		if code < 0 {
			code = 1
		}
	}
	return code, err
}

func (p *ptyProcess) Close() error {
	select {
	case <-p.done:
		p.closeMaster()
		return nil
	default:
	}
	p.terminate(syscall.SIGTERM)
	p.closeMaster()
	return nil
}

func (p *ptyProcess) closeMaster() {
	p.closePTY.Do(func() { _ = p.master.Close() })
}

func (p *ptyProcess) watchCancellation(ctx context.Context) {
	signal := syscall.SIGTERM
	if forwarded, ok := terminationSignal(ctx); ok {
		signal = forwarded
	}
	select {
	case <-ctx.Done():
		if forwarded, ok := terminationSignal(ctx); ok {
			signal = forwarded
		}
		p.terminate(signal)
	case <-p.done:
		return
	}
	select {
	case <-time.After(2 * time.Second):
		p.terminate(syscall.SIGKILL)
	case <-p.done:
	}
}

func (p *ptyProcess) terminate(signal syscall.Signal) {
	if p.cmd.Process != nil {
		_ = syscall.Kill(-p.cmd.Process.Pid, signal)
	}
}
