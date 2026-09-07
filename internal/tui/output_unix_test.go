//go:build !windows

package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/creack/pty"
)

func TestStoppedPTYReaderCannotHangOutput(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer slave.Close()
	defer master.Close()
	w := NewGuardedOutput(slave, 50*time.Millisecond, nil)
	// Deliberately never read the master: reproduce the stopped terminal
	// bridge rather than simulating slow model output.
	started := time.Now()
	_, err = w.Write(bytes.Repeat([]byte("x"), 2<<20))
	if !errors.Is(err, ErrTerminalOutputStalled) || time.Since(started) > 3*time.Second {
		t.Fatalf("PTY remained hung: %v", err)
	}
}

type queryNativeSize struct{}

// Observe sizes delivered by Bubble Tea's actual terminal detection. Never
// inject WindowSizeMsg: doing so concealed the guarded-writer regression.
type nativeSizeModel struct {
	Model
	sizes chan tea.WindowSizeMsg
}

func (m nativeSizeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(queryNativeSize); ok {
		return m, tea.WindowSize()
	}
	updated, cmd := m.Model.Update(msg)
	m.Model = updated.(Model)
	if size, ok := msg.(tea.WindowSizeMsg); ok && m.ready && m.View() != "Starting Collomia…" {
		select {
		case m.sizes <- size:
		default:
		}
	}
	return m, cmd
}

func TestGuardedTerminalDeliversStartupAndResize(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer slave.Close()
	defer master.Close()
	if err := pty.Setsize(slave, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, master) }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	base := newTestModel(t)
	output := NewGuardedOutput(slave, time.Second, func(error) { cancel() })
	sizes := make(chan tea.WindowSizeMsg, 4)
	m := nativeSizeModel{Model: NewWithOutput(ctx, base.runtime, NewApprovalBroker(), "", output), sizes: sizes}
	program := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(slave), tea.WithOutput(output), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() { _, err := program.Run(); done <- err }()
	defer func() {
		// Quit normally so Bubble Tea joins its terminal-input reader before
		// closing it. Context cancellation uses the dependency's kill path.
		go program.Quit()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("native terminal shutdown: %v", err)
			}
		case <-time.After(3 * time.Second):
			cancel()
			t.Error("native terminal failed to shut down")
		}
	}()
	for _, want := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 110, Height: 36}} {
		if want.Width == 110 {
			if err := pty.Setsize(slave, &pty.Winsize{Rows: uint16(want.Height), Cols: uint16(want.Width)}); err != nil {
				t.Fatal(err)
			}
			program.Send(queryNativeSize{})
		}
		select {
		case got := <-sizes:
			if got != want {
				t.Fatalf("native size=%+v want=%+v", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("no native size event for %+v; UI remains on Starting Collomia", want)
		}
	}
}
