package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runtimeevent "github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// completeTurn drives a turn through the runtime's own event funnel rather than
// appending straight to the session, because the funnel is what teaches the
// change tracker where the turn boundary is. A test that bypassed it would pass
// while the coupling was broken.
func completeTurn(m *Model, prompt string) {
	m.runtime.Session.AppendMessage(provider.Message{Role: "user", Content: prompt})
	m.runtime.Session.AppendMessage(provider.Message{Role: "assistant", Content: "done"})
	m.runtime.LogEvent(runtimeevent.New(runtimeevent.KindTurnEnd))
}

// agentWrite performs a mutation and records it the way a built-in file tool
// does, so it lands in the turn currently in progress.
func agentWrite(t *testing.T, m *Model, path, content string) {
	t.Helper()
	var before *string
	if data, err := os.ReadFile(path); err == nil {
		text := string(data)
		before = &text
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m.runtime.Changes.Record(path, "write", before, &content)
}

func TestRestoreMovesConversationAndWorkspaceTogether(t *testing.T) {
	m := newTestModel(t)
	kept := filepath.Join(m.runtime.Workspace, "kept.txt")
	changed := filepath.Join(m.runtime.Workspace, "changed.txt")
	created := filepath.Join(m.runtime.Workspace, "created.txt")

	agentWrite(t, &m, kept, "turn one\n")
	agentWrite(t, &m, changed, "turn one\n")
	completeTurn(&m, "first prompt")
	agentWrite(t, &m, changed, "turn two\n")
	agentWrite(t, &m, created, "turn two\n")
	completeTurn(&m, "second prompt")

	originalID := m.runtime.Session.Meta.ID
	if quit, cmd := (&m).slash("/restore 1"); quit || cmd != nil {
		t.Fatalf("restore unexpectedly quit or returned a command: quit=%t cmd=%v", quit, cmd)
	}

	// The conversation half: a new branch holding exactly one turn, with the
	// source session left intact.
	if m.runtime.Session.Meta.ID == originalID || m.runtime.Session.Meta.ForkedFrom != originalID || m.runtime.Session.Meta.Turns != 1 {
		t.Fatalf("restored session=%+v original=%s", m.runtime.Session.Meta, originalID)
	}
	if transcript := m.runtime.Session.TranscriptMessages(); len(transcript) != 2 || transcript[0].Content != "first prompt" {
		t.Fatalf("restored transcript=%+v", transcript)
	}
	original, err := m.runtime.Sessions.Load(originalID)
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	if original.Meta.Turns != 2 {
		t.Fatalf("the source conversation must be untouched: %+v", original.Meta)
	}

	// The workspace half, which is what /rewind deliberately does not do.
	if data, err := os.ReadFile(changed); err != nil || string(data) != "turn one\n" {
		t.Fatalf("turn-two edit not reversed: data=%q err=%v", data, err)
	}
	if data, err := os.ReadFile(kept); err != nil || string(data) != "turn one\n" {
		t.Fatalf("turn-one file must survive: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(created); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file created after the checkpoint must be removed: %v", err)
	}

	last := m.blocks[len(m.blocks)-1].content
	for _, want := range []string{"Restored to completed turn 1", "2 changes in 2 files", "were not reversed"} {
		if !strings.Contains(last, want) {
			t.Fatalf("restore notice missing %q: %q", want, last)
		}
	}
}

func TestRestoreRefusedByDriftLeavesBothHalvesUntouched(t *testing.T) {
	m := newTestModel(t)
	drifted := filepath.Join(m.runtime.Workspace, "drifted.txt")
	clean := filepath.Join(m.runtime.Workspace, "clean.txt")

	agentWrite(t, &m, drifted, "turn one\n")
	agentWrite(t, &m, clean, "turn one\n")
	completeTurn(&m, "first prompt")
	agentWrite(t, &m, drifted, "turn two\n")
	agentWrite(t, &m, clean, "turn two\n")
	completeTurn(&m, "second prompt")

	// The user edits a file the agent had written.
	if err := os.WriteFile(drifted, []byte("my own edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	originalID := m.runtime.Session.Meta.ID
	if quit, cmd := (&m).slash("/restore 1"); quit || cmd != nil {
		t.Fatalf("restore unexpectedly quit or returned a command: quit=%t cmd=%v", quit, cmd)
	}

	// Fail closed means the conversation did not move either. This is the whole
	// point of verifying the workspace before branching.
	if m.runtime.Session.Meta.ID != originalID || m.runtime.Session.Meta.Turns != 2 {
		t.Fatalf("a refused restore must not branch the conversation: %+v", m.runtime.Session.Meta)
	}
	if data, err := os.ReadFile(drifted); err != nil || string(data) != "my own edit\n" {
		t.Fatalf("external edit was clobbered: data=%q err=%v", data, err)
	}
	if data, err := os.ReadFile(clean); err != nil || string(data) != "turn two\n" {
		t.Fatalf("a refused restore must write nothing: data=%q err=%v", data, err)
	}

	last := m.blocks[len(m.blocks)-1].content
	for _, want := range []string{"drifted.txt", "changed outside Collomia", "Nothing was restored", "/rewind"} {
		if !strings.Contains(last, want) {
			t.Fatalf("refusal panel missing %q: %q", want, last)
		}
	}
	// The refusal names the file the way the rest of the interface does.
	if strings.Contains(last, m.runtime.Workspace) {
		t.Fatalf("refusal should name workspace-relative paths: %q", last)
	}
}

func TestRestorePickerSaysWhatEachCheckpointCosts(t *testing.T) {
	m := newTestModel(t)
	path := filepath.Join(m.runtime.Workspace, "a.txt")
	agentWrite(t, &m, path, "turn one\n")
	completeTurn(&m, "first prompt")
	agentWrite(t, &m, path, "turn two\n")
	completeTurn(&m, "second prompt")

	if quit, cmd := (&m).slash("/restore"); quit || cmd != nil {
		t.Fatalf("restore picker unexpectedly quit or returned a command: quit=%t cmd=%v", quit, cmd)
	}
	if m.picker == nil || m.picker.title != "Restore conversation and files" {
		t.Fatalf("restore picker=%+v", m.picker)
	}
	if len(m.picker.matches) != 2 || m.picker.matches[0].id != "1" || m.picker.matches[1].id != "0" {
		t.Fatalf("restore targets=%+v", m.picker.matches)
	}
	// Restoring to turn 1 reverses turn two's single edit; restoring to turn 0
	// reverses both. The number is the part of the decision the turn does not
	// convey, so it has to be on the entry.
	if !strings.Contains(m.picker.matches[0].desc, "1 change in 1 file") {
		t.Fatalf("turn-1 entry=%q", m.picker.matches[0].desc)
	}
	if !strings.Contains(m.picker.matches[1].desc, "2 changes in 1 file") {
		t.Fatalf("turn-0 entry=%q", m.picker.matches[1].desc)
	}
}

func TestRestoreOnAResumedSessionReportsThatNothingWasReversed(t *testing.T) {
	m := newTestModel(t)
	for turn := 1; turn <= 2; turn++ {
		completeTurn(&m, fmt.Sprintf("prompt %d", turn))
	}
	id := m.runtime.Session.Meta.ID

	// Reopen the session the way a resume does. The conversation's turns come
	// back; the in-memory record of which files they touched does not.
	if err := m.runtime.SwitchSession(id); err != nil {
		t.Fatal(err)
	}
	if got := m.runtime.Changes.CompletedTurns(); got != 2 {
		t.Fatalf("a resumed session must align the tracker's turn numbering, got %d", got)
	}
	if quit, cmd := (&m).slash("/restore 1"); quit || cmd != nil {
		t.Fatalf("restore unexpectedly quit or returned a command: quit=%t cmd=%v", quit, cmd)
	}
	last := m.blocks[len(m.blocks)-1].content
	if !strings.Contains(last, "No tracked file changes needed reversing") {
		t.Fatalf("a restore with nothing to reverse must say so rather than implying it undid files: %q", last)
	}
}

func TestRewindPointsAtTheCoupledRestore(t *testing.T) {
	m := newTestModel(t)
	for turn := 1; turn <= 2; turn++ {
		completeTurn(&m, fmt.Sprintf("prompt %d", turn))
	}
	if quit, cmd := (&m).slash("/rewind 1"); quit || cmd != nil {
		t.Fatalf("rewind unexpectedly quit or returned a command: quit=%t cmd=%v", quit, cmd)
	}
	last := m.blocks[len(m.blocks)-1].content
	if !strings.Contains(last, "were not undone") || !strings.Contains(last, "/restore 1") {
		t.Fatalf("rewind should keep its honest limits and name the coupled alternative: %q", last)
	}
}
