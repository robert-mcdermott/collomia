package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// idleModel never quits on its own, so only the context can end the program.
type idleModel struct{}

func (idleModel) Init() tea.Cmd                         { return nil }
func (m idleModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (idleModel) View() string                          { return "" }

// A dependency contract, pinned because the shutdown path now rests on it.
//
// Bubble Tea installs its own handler for SIGINT and SIGTERM and quits on
// them, which is why those two always reached `defer runtime.Close()`. It does
// not handle SIGHUP. Registering SIGHUP without also giving the program the
// shutdown context would have been strictly worse than the crash it replaced:
// the signal would be captured, the runtime's default disposition would no
// longer fire, and the interface would keep running against a terminal that no
// longer exists.
//
// tea.WithContext is the whole mechanism that turns a cancelled context into a
// returned Run. If an upgrade changes that, this fails here rather than as a
// hung session nobody can reproduce.
func TestProgramExitsWhenTheShutdownContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	program := tea.NewProgram(idleModel{},
		tea.WithContext(ctx),
		tea.WithInput(bytes.NewReader(nil)),
		tea.WithOutput(io.Discard),
	)
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	// Let the program reach its event loop before cancelling, so the test
	// exercises cancellation of a running program rather than a start-up race.
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, tea.ErrProgramKilled) {
			t.Fatalf("Run returned %v, want it to wrap tea.ErrProgramKilled", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("cancelling the context did not stop the program; teardown would never run on SIGHUP")
	}
}

// The companion to the above: run() converts that particular error into a
// clean exit, because a hangup is how an interactive session normally ends
// when the terminal goes away, not a failure to report.
func TestKilledProgramWithCancelledContextIsNotAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !shutdownRequested(tea.ErrProgramKilled, ctx) {
		t.Error("a killed program under a cancelled shutdown context should exit cleanly")
	}
	wrapped := errors.Join(tea.ErrProgramKilled, context.Canceled)
	if !shutdownRequested(wrapped, ctx) {
		t.Error("a wrapped kill error should still be recognized")
	}
	// An ordinary failure must still be reported, and so must a kill that no
	// shutdown signal explains — that one is a real bug, not a hangup.
	if shutdownRequested(errors.New("render failed"), ctx) {
		t.Error("an ordinary program error was swallowed")
	}
	if shutdownRequested(tea.ErrProgramKilled, context.Background()) {
		t.Error("a kill with no shutdown request was swallowed")
	}
}
