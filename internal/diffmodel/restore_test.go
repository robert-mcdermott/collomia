package diffmodel

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// write records a mutation the way a tool does: perform it, then tell the
// tracker. Returning the content keeps the assertions readable.
func write(t *testing.T, tracker *Tracker, path, content string) {
	t.Helper()
	var before *string
	if data, err := os.ReadFile(path); err == nil {
		text := string(data)
		before = &text
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tracker.Record(path, "write", before, &content)
}

func TestRestoreToReversesOnlyMutationsAfterTheCheckpoint(t *testing.T) {
	dir := t.TempDir()
	kept := filepath.Join(dir, "kept.txt")
	changed := filepath.Join(dir, "changed.txt")
	created := filepath.Join(dir, "created.txt")
	tracker := NewTracker(dir)

	// Turn one establishes the checkpoint state.
	write(t, tracker, kept, "turn one kept\n")
	write(t, tracker, changed, "turn one changed\n")
	tracker.CompleteTurn()

	// Turn two edits one file and creates another.
	write(t, tracker, changed, "turn two changed\n")
	write(t, tracker, created, "turn two created\n")

	result, err := tracker.RestoreTo(1)
	if err != nil {
		t.Fatalf("restore to turn 1: %v", err)
	}
	if result.Turn != 1 || len(result.Files) != 2 || result.Mutations != 2 {
		t.Fatalf("restore result=%+v", result)
	}
	if data, err := os.ReadFile(changed); err != nil || string(data) != "turn one changed\n" {
		t.Fatalf("turn-two edit not reversed: data=%q err=%v", data, err)
	}
	if data, err := os.ReadFile(kept); err != nil || string(data) != "turn one kept\n" {
		t.Fatalf("turn-one file must survive: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(created); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a file created after the checkpoint must be removed, stat err=%v", err)
	}
	// The checkpoint's own mutations remain undoable; the reversed ones do not.
	if files, mutations := tracker.PendingSince(1); files != 0 || mutations != 0 {
		t.Fatalf("history after restore still reports work: files=%d mutations=%d", files, mutations)
	}
	if files, mutations := tracker.PendingSince(0); files != 2 || mutations != 2 {
		t.Fatalf("turn-one history was discarded: files=%d mutations=%d", files, mutations)
	}
}

func TestRestoreToRefusesEveryDriftedFileAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	clean := filepath.Join(dir, "clean.txt")
	driftedA := filepath.Join(dir, "a-drifted.txt")
	driftedB := filepath.Join(dir, "b-drifted.txt")
	tracker := NewTracker(dir)
	for _, path := range []string{clean, driftedA, driftedB} {
		write(t, tracker, path, "checkpoint\n")
	}
	tracker.CompleteTurn()
	for _, path := range []string{clean, driftedA, driftedB} {
		write(t, tracker, path, "agent wrote this\n")
	}

	// The user edits two of the three after the agent did.
	for _, path := range []string{driftedA, driftedB} {
		if err := os.WriteFile(path, []byte("user edit\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, err := tracker.RestoreTo(1)
	var drift *DriftError
	if !errors.As(err, &drift) {
		t.Fatalf("restore must refuse with a DriftError, got %v", err)
	}
	if len(drift.Files) != 2 || drift.Files[0] != driftedA || drift.Files[1] != driftedB {
		t.Fatalf("drift must name every affected file in a stable order: %+v", drift.Files)
	}
	if drift.Turn != 1 {
		t.Fatalf("drift turn=%d", drift.Turn)
	}
	if !strings.Contains(drift.Error(), "changed outside the agent") {
		t.Fatalf("drift message=%q", drift.Error())
	}

	// The whole operation is refused: the file that had not drifted keeps the
	// agent's content rather than being quietly reverted on its own.
	if data, err := os.ReadFile(clean); err != nil || string(data) != "agent wrote this\n" {
		t.Fatalf("a refused restore must write nothing: data=%q err=%v", data, err)
	}
	for _, path := range []string{driftedA, driftedB} {
		if data, err := os.ReadFile(path); err != nil || string(data) != "user edit\n" {
			t.Fatalf("external edit was clobbered in %s: data=%q err=%v", path, data, err)
		}
	}
	// Nothing was consumed, so the same restore can be retried once the user
	// has dealt with the drift.
	if files, _ := tracker.PendingSince(1); files != 3 {
		t.Fatalf("a refused restore must keep its history, files=%d", files)
	}
}

func TestVerifyRestoreReportsDriftWithoutTouchingTheWorkspace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	tracker := NewTracker(dir)
	write(t, tracker, path, "checkpoint\n")
	tracker.CompleteTurn()
	write(t, tracker, path, "agent\n")

	if err := tracker.VerifyRestore(1); err != nil {
		t.Fatalf("clean workspace must verify: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "agent\n" {
		t.Fatalf("verification must not write: %q", data)
	}
	if err := os.WriteFile(path, []byte("user\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var drift *DriftError
	if err := tracker.VerifyRestore(1); !errors.As(err, &drift) {
		t.Fatalf("drifted workspace must fail verification, got %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "user\n" {
		t.Fatalf("verification must not write: %q", data)
	}
}

func TestRestoreToCollapsesRepeatedMutationsOfOneFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "busy.txt")
	tracker := NewTracker(dir)
	write(t, tracker, path, "checkpoint\n")
	tracker.CompleteTurn()
	for _, content := range []string{"second\n", "third\n", "fourth\n"} {
		write(t, tracker, path, content)
	}

	result, err := tracker.RestoreTo(1)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	// One file, but the three mutations that touched it are all accounted for.
	if len(result.Files) != 1 || result.Mutations != 3 {
		t.Fatalf("result=%+v", result)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "checkpoint\n" {
		t.Fatalf("collapsed restore landed wrong: data=%q err=%v", data, err)
	}
}

func TestRestoreToRecreatesAFileTheAgentDeleted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doomed.txt")
	tracker := NewTracker(dir)
	write(t, tracker, path, "checkpoint\n")
	tracker.CompleteTurn()

	before := "checkpoint\n"
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	tracker.RecordWithMode(path, "delete", &before, nil, 0o600, 0)

	if _, err := tracker.RestoreTo(1); err != nil {
		t.Fatalf("restore: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != before {
		t.Fatalf("deleted file not restored: data=%q err=%v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("restore lost the file's permission bits: %v", info.Mode().Perm())
	}
}

func TestRestoreToTreatsAReplacedDeletionAsDrift(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gone.txt")
	tracker := NewTracker(dir)
	write(t, tracker, path, "checkpoint\n")
	tracker.CompleteTurn()
	before := "checkpoint\n"
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	tracker.RecordWithMode(path, "delete", &before, nil, 0o644, 0)

	// The user puts a file back where the agent deleted one.
	if err := os.WriteFile(path, []byte("mine now\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var drift *DriftError
	if _, err := tracker.RestoreTo(1); !errors.As(err, &drift) {
		t.Fatalf("a recreated file must read as drift, got %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "mine now\n" {
		t.Fatalf("the user's file was overwritten: %q", data)
	}
}

func TestRestoreToTreatsAnExternallyDeletedFileAsDrift(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	tracker := NewTracker(dir)
	write(t, tracker, path, "checkpoint\n")
	tracker.CompleteTurn()
	write(t, tracker, path, "agent\n")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	var drift *DriftError
	if _, err := tracker.RestoreTo(1); !errors.As(err, &drift) {
		t.Fatalf("a file deleted outside the agent must read as drift, got %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused restore must not recreate the file: %v", err)
	}
}

func TestRestoreToWithNothingTrackedStillAlignsTurns(t *testing.T) {
	tracker := NewTracker(t.TempDir())
	tracker.SetCompletedTurns(7)
	result, err := tracker.RestoreTo(3)
	if err != nil {
		t.Fatalf("a restore with no tracked changes must succeed: %v", err)
	}
	if len(result.Files) != 0 || result.Mutations != 0 {
		t.Fatalf("result=%+v", result)
	}
	// Turn numbering follows the conversation even when there was no file work
	// to reverse, so the next mutation is attributed to turn 4 rather than 8.
	if tracker.CompletedTurns() != 3 {
		t.Fatalf("completed turns=%d", tracker.CompletedTurns())
	}
}

func TestSessionDiffAfterRestoreShowsOnlyWhatSurvived(t *testing.T) {
	dir := t.TempDir()
	kept := filepath.Join(dir, "kept.txt")
	reverted := filepath.Join(dir, "reverted.txt")
	created := filepath.Join(dir, "created.txt")
	if err := os.WriteFile(kept, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reverted, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker(dir)
	write(t, tracker, kept, "turn one\n")
	tracker.CompleteTurn()
	write(t, tracker, reverted, "turn two\n")
	write(t, tracker, created, "turn two\n")

	if _, err := tracker.RestoreTo(1); err != nil {
		t.Fatalf("restore: %v", err)
	}
	diff := tracker.Diff(dir)
	// The turn-one change is still the agent's work and belongs in /diff. The
	// reverted and removed files are back to where the session found them, so
	// showing them would describe changes that no longer exist.
	if !strings.Contains(diff, "kept.txt") || !strings.Contains(diff, "+turn one") {
		t.Fatalf("surviving change missing from the session diff:\n%s", diff)
	}
	for _, gone := range []string{"reverted.txt", "created.txt"} {
		if strings.Contains(diff, gone) {
			t.Fatalf("%s was restored but still appears in the session diff:\n%s", gone, diff)
		}
	}
}

func TestRestoreToRejectsANegativeTurn(t *testing.T) {
	tracker := NewTracker(t.TempDir())
	if _, err := tracker.RestoreTo(-1); err == nil {
		t.Fatal("a negative turn must be rejected")
	}
	if err := tracker.VerifyRestore(-1); err == nil {
		t.Fatal("a negative turn must be rejected by verification too")
	}
}

func TestRecordedMutationsCarryTheTurnTheyHappenedIn(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)
	write(t, tracker, filepath.Join(dir, "one.txt"), "a\n")
	tracker.CompleteTurn()
	write(t, tracker, filepath.Join(dir, "two.txt"), "b\n")
	tracker.CompleteTurn()
	tracker.CompleteTurn()
	write(t, tracker, filepath.Join(dir, "three.txt"), "c\n")

	if got := tracker.CompletedTurns(); got != 3 {
		t.Fatalf("completed turns=%d", got)
	}
	// A restore to turn 2 covers only the mutation made during turn 4, which is
	// what proves the turn is recorded rather than the position in history.
	if files, _ := tracker.PendingSince(3); files != 1 {
		t.Fatalf("pending since turn 3: files=%d", files)
	}
	if files, _ := tracker.PendingSince(1); files != 2 {
		t.Fatalf("pending since turn 1: files=%d", files)
	}
	if files, _ := tracker.PendingSince(0); files != 3 {
		t.Fatalf("pending since turn 0: files=%d", files)
	}
}
