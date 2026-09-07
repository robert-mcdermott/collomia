//go:build linux

package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPTYOutputWorker(t *testing.T) {
	dir := os.Getenv("COLLO_TEST_PTY_DRAIN")
	if dir == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "child.pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Print("FIRST")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "reader.ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			os.Exit(2)
		}
		time.Sleep(time.Millisecond)
	}
	fmt.Print("TRAILING_OUTPUT")
	os.Exit(0)
}

type pausedPTYWriter struct {
	buffer    bytes.Buffer
	readyPath string
	release   <-chan struct{}
	paused    bool
}

func (w *pausedPTYWriter) Write(p []byte) (int, error) {
	if !w.paused {
		w.paused = true
		if err := os.WriteFile(w.readyPath, nil, 0600); err != nil {
			return 0, err
		}
		<-w.release
	}
	return w.buffer.Write(p)
}

// Linux retains queued terminal output after the session leader exits. Darwin
// can discard that queue in the kernel on terminal hangup, so forcing the reader
// to pause across exit is a Linux-specific regression; the ordinary PTY and
// timeout tests continue to run on every platform.
// Hold the output reader while the child writes its final bytes and exits.
// The old wait/close/copy ordering discarded that tail deterministically.
func TestPTYDrainsOutputAfterChildExit(t *testing.T) {
	dir := t.TempDir()
	release := make(chan struct{})
	writer := &pausedPTYWriter{readyPath: filepath.Join(dir, "reader.ready"), release: release}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	finished := false
	go func() {
		done <- runUnderPTY(ctx, []string{os.Args[0], "-test.run=^TestPTYOutputWorker$"}, dir, append(os.Environ(), "COLLO_TEST_PTY_DRAIN="+dir), writer)
	}()
	defer func() {
		close(release)
		cancel()
		if !finished {
			<-done
		}
	}()
	deadline := time.Now().Add(8 * time.Second)
	for {
		data, _ := os.ReadFile(filepath.Join(dir, "child.pid"))
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil && pid > 0 && errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			break // Wait has reaped the child while the reader is still paused.
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not exit while the output reader was paused")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Release just the first write; deferred close also unblocks failure paths.
	release <- struct{}{}
	err := <-done
	finished = true
	if err != nil {
		t.Fatal(err)
	}
	if got := writer.buffer.String(); got != "FIRSTTRAILING_OUTPUT" {
		t.Fatalf("lost output after child exit: %q", got)
	}
}

// A child can exit while a detached descendant keeps the terminal open.
// Cancellation must still interrupt the drain rather than hang until that
// descendant exits on its own.
func TestPTYDrainRemainsCancellable(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	var output bytes.Buffer
	start := time.Now()
	err := runUnderPTY(ctx, []string{"sh", "-c", `trap '' HUP; sleep 30 & printf READY`}, t.TempDir(), nil, &output)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected drain cancellation, got %v (output %q)", err, output.String())
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("PTY drain ignored cancellation: %s", elapsed)
	}
}
