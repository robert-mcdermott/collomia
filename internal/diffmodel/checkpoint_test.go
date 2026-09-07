package diffmodel

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckpointBoundsBinaryAndRootIdentity(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker(root)
	var saved json.RawMessage
	save := func(raw json.RawMessage) error { saved = append(json.RawMessage(nil), raw...); return nil }
	if err := tracker.BindCheckpoint(nil, 0, save); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "binary.bin")
	before := string([]byte{0xff, 0x00, 0x80})
	after := "after"
	if err := os.WriteFile(path, []byte(after), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	tracker.RecordWithMode(path, "write", &before, &after, info.Mode().Perm(), info.Mode().Perm())
	resumed := NewTracker(root)
	if err := resumed.BindCheckpoint(saved, 0, save); err != nil {
		t.Fatal(err)
	}
	if _, err := resumed.RestoreTo(0); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != before {
		t.Fatal("binary prior bytes corrupted")
	}
	huge := strings.Repeat("x", checkpointFileBytes+1)
	tracker.RecordWithMode(path, "write", &before, &huge, 0600, 0600)
	if err := tracker.VerifyRestore(0); err == nil || !strings.Contains(err.Error(), "not retained") {
		t.Fatalf("oversized content silently restored: %v", err)
	}
	if len(saved) > checkpointFileBytes {
		t.Fatal("unavailable content leaked into checkpoint")
	}
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := NewTracker(root).BindCheckpoint(saved, 0, nil); err == nil {
		t.Fatal("replacement directory accepted old checkpoint")
	}
}
func TestCheckpointInterruptAndExplicitKeep(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	tracker := NewTracker(root)
	var saved json.RawMessage
	breakRestore := false
	save := func(raw json.RawMessage) error {
		saved = append(json.RawMessage(nil), raw...)
		var state checkpointState
		if err := json.Unmarshal(raw, &state); err != nil {
			return err
		}
		if state.Pending && breakRestore {
			breakRestore = false
			if err := os.Remove(b); err != nil {
				return err
			}
			return os.Mkdir(b, 0700)
		}
		return nil
	}
	if err := tracker.BindCheckpoint(nil, 0, save); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{a, b} {
		write(t, tracker, path, "before")
	}
	tracker.CompleteTurn()
	for _, path := range []string{a, b} {
		write(t, tracker, path, "after")
	}
	breakRestore = true
	if _, err := tracker.RestoreTo(1); err == nil {
		t.Fatal("injected partial restore passed")
	}
	resumed := NewTracker(root)
	if err := resumed.BindCheckpoint(saved, 2, save); err != nil {
		t.Fatal(err)
	}
	if err := resumed.VerifyRestore(1); err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("interrupted restore hidden: %v", err)
	}
	if err := resumed.KeepCheckpoint("User inspected and keeps current files"); err != nil {
		t.Fatal(err)
	}
	if len(resumed.Changed()) != 0 {
		t.Fatal("uncertain history survived explicit keep")
	}
	data, _ := os.ReadFile(a)
	if string(data) != "before" {
		t.Fatal("keep mutated the workspace")
	}
}
func TestCheckpointRetentionFloorAndStorageFailure(t *testing.T) {
	root := t.TempDir()
	tracker := NewTracker(root)
	var saved json.RawMessage
	save := func(raw json.RawMessage) error { saved = append(json.RawMessage(nil), raw...); return nil }
	if err := tracker.BindCheckpoint(nil, 0, save); err != nil {
		t.Fatal(err)
	}
	// Tiny distinct-turn entries exercise the cap without filesystem churn.
	for i := 0; i < checkpointEntries+2; i++ {
		tracker.RecordWithMode(filepath.Join(root, "file"), "write", nil, nil, 0, 0)
		tracker.CompleteTurn()
	}
	resumed := NewTracker(root)
	if err := resumed.BindCheckpoint(saved, checkpointEntries+2, save); err != nil {
		t.Fatal(err)
	}
	if err := resumed.VerifyRestore(0); err == nil {
		t.Fatal("retention gap claimed as complete coverage")
	}
	failing := NewTracker(root)
	sentinel := errors.New("sync failed")
	if err := failing.BindCheckpoint(nil, 0, func(json.RawMessage) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal("storage failure hidden")
	}
	if !errors.Is(failing.CheckpointError(), sentinel) {
		t.Fatal("storage failure not latched")
	}
}
