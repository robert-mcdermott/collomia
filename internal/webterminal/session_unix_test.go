//go:build !windows

package webterminal

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPTYProcessStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	proc, err := startPTY(ctx, processSpec{
		Executable: "/bin/sh",
		Args:       []string{"-c", "trap 'exit 0' TERM; while :; do sleep 30; done"},
		Dir:        t.TempDir(),
		Cols:       80,
		Rows:       24,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitDone := make(chan error, 1)
	go func() {
		_, err := proc.Wait()
		waitDone <- err
	}()
	cancel()
	select {
	case <-waitDone:
		_ = proc.Close()
	case <-time.After(3 * time.Second):
		_ = proc.Close()
		t.Fatal("PTY process survived context cancellation")
	}
}

func TestPTYProcessForwardsInterruptSignal(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	proc, err := startPTY(ctx, processSpec{
		Executable: os.Args[0],
		Args:       []string{"-test.run=^TestPTYSignalHelper$"},
		Dir:        t.TempDir(),
		Env:        append(os.Environ(), "COLLO_WEB_SIGNAL_HELPER=1"),
		Cols:       80,
		Rows:       24,
	})
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(proc).ReadString('\n')
	if err != nil || !strings.Contains(line, "ready") {
		_ = proc.Close()
		t.Fatalf("PTY did not become ready: line=%q err=%v", line, err)
	}
	cancel(&terminationCause{signal: syscall.SIGINT})
	waitDone := make(chan int, 1)
	go func() {
		code, _ := proc.Wait()
		waitDone <- code
	}()
	select {
	case code := <-waitDone:
		_ = proc.Close()
		if code != 42 {
			t.Fatalf("SIGINT was not forwarded to the PTY group: exit=%d", code)
		}
	case <-time.After(3 * time.Second):
		_ = proc.Close()
		t.Fatal("PTY process did not handle forwarded SIGINT")
	}
}

func TestPTYSignalHelper(t *testing.T) {
	if os.Getenv("COLLO_WEB_SIGNAL_HELPER") != "1" {
		return
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	fmt.Println("ready")
	received := <-signals
	if received == os.Interrupt {
		os.Exit(42)
	}
	os.Exit(43)
}
