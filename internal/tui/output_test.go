package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync/atomic"
	"testing"
	"time"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

type stalledWriter struct {
	release chan struct{}
	calls   atomic.Int32
}

func (w *stalledWriter) Write(p []byte) (int, error) { w.calls.Add(1); <-w.release; return len(p), nil }

func TestOutputStallIsBoundedAndTeardownDoesNotWriteAgain(t *testing.T) {
	underlying := &stalledWriter{release: make(chan struct{})}
	defer close(underlying.release)
	var failures atomic.Int32
	w := NewGuardedOutput(underlying, 25*time.Millisecond, func(error) { failures.Add(1) })
	if _, err := w.Write([]byte("frame")); !errors.Is(err, ErrTerminalOutputStalled) {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := w.Write([]byte("reset terminal")); !errors.Is(err, ErrTerminalOutputStalled) {
			t.Fatal(err)
		}
	}
	if underlying.calls.Load() != 1 || failures.Load() != 1 {
		t.Fatal("teardown retried the blocked writer")
	}
}

func TestGuardedOutputPreservesBytes(t *testing.T) {
	var b bytes.Buffer
	w := NewGuardedOutput(&b, time.Second, nil)
	for _, frame := range []string{"\x1b[2J", "hello\n", "\x1b[0m"} {
		if n, err := io.WriteString(w, frame); err != nil || n != len(frame) {
			t.Fatalf("%d %v", n, err)
		}
	}
	if b.String() != "\x1b[2Jhello\n\x1b[0m" || w.Err() != nil {
		t.Fatal(b.String(), w.Err())
	}
}

func TestGuardedOutputBorrowsTerminalFile(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	w := NewGuardedOutput(file, time.Second, nil)
	if w.Fd() != file.Fd() {
		t.Fatal("terminal descriptor was lost")
	}
	if _, err := w.Write([]byte("frame")); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if got, err := io.ReadAll(w); err != nil || string(got) != "frame" {
		t.Fatalf("read=%q err=%v", got, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("still owned by caller"); err != nil {
		t.Fatalf("wrapper closed borrowed terminal: %v", err)
	}
}

type floodingClient struct {
	stopped chan struct{}
	filled  chan struct{}
}

func (*floodingClient) Name() string { return "fixture" }
func (c *floodingClient) Chat(ctx context.Context, _ provider.Request, emit func(provider.Delta)) (provider.Response, error) {
	defer close(c.stopped)
	for i := 0; i < 1000; i++ {
		if i == 70 {
			close(c.filled)
		}
		emit(provider.Delta{Reasoning: "streamed progress"})
		if err := ctx.Err(); err != nil {
			return provider.Response{}, err
		}
	}
	return provider.Response{Content: "answer"}, nil
}

func TestDisplayBackpressureDoesNotPreventAgentCancellation(t *testing.T) {
	m := newTestModel(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m.runContext = ctx
	c := &floodingClient{stopped: make(chan struct{}), filled: make(chan struct{})}
	m.runtime.Agent.SetProvider("fixture", "fixture", appconfig.Provider{}, c)
	m.startTurn("Answer") // Intentionally never consume runEvents.
	deadline := time.After(time.Second)
	for len(m.runEvents) < cap(m.runEvents) {
		select {
		case <-deadline:
			t.Fatal("did not fill the display queue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case <-c.stopped:
	case <-time.After(time.Second):
		t.Fatal("display queue trapped provider callback after cancellation")
	}
	if !m.WaitForRuns(time.Second) {
		t.Fatal("agent did not finish before session close")
	}
}
