package diffmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnifiedDiff(t *testing.T) {
	before := "line one\nline two\nline three\n"
	after := "line one\nline 2\nline three\nline four\n"
	diff := Unified("file.txt", before, after)
	for _, want := range []string{"--- a/file.txt", "+++ b/file.txt", "-line two", "+line 2", "+line four", " line one"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff missing %q:\n%s", want, diff)
		}
	}
	if Unified("same.txt", "x\n", "x\n") != "" {
		t.Error("identical contents should produce no diff")
	}
}

func TestAlignForSideBySideReview(t *testing.T) {
	rows := Align("one\ntwo\nthree\n", "one\nTWO\nthree\nfour\n")
	var deleted, added bool
	for _, row := range rows {
		if row.Kind == '-' && row.Left == "two" && row.LeftNumber == 2 && row.RightNumber == 0 {
			deleted = true
		}
		if row.Kind == '+' && row.Right == "TWO" && row.RightNumber == 2 && row.LeftNumber == 0 {
			added = true
		}
	}
	if !deleted || !added {
		t.Fatalf("aligned rows missing replacement: %+v", rows)
	}
}

func TestTrackerDiffAndUndo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker()
	v1, v2 := "v1\n", "v2\n"
	if err := os.WriteFile(path, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	tracker.Record(path, "edit", &v1, &v2)

	diff := tracker.Diff(dir)
	if !strings.Contains(diff, "-v1") || !strings.Contains(diff, "+v2") {
		t.Fatalf("session diff wrong:\n%s", diff)
	}
	snapshot, err := tracker.Undo()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Path != path {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "v1\n" {
		t.Fatalf("undo did not restore: %q", data)
	}
	if _, err := tracker.Undo(); err == nil {
		t.Fatal("empty history must not undo")
	}
}

func TestUndoOfCreateRemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")
	content := "created\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker()
	tracker.Record(path, "write", nil, &content)
	if _, err := tracker.Undo(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("undo of a create should remove the file")
	}
}

func TestUndoRefusesExternalChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	v1, v2 := "v1\n", "v2\n"
	os.WriteFile(path, []byte(v2), 0o644)
	tracker := NewTracker()
	tracker.Record(path, "edit", &v1, &v2)
	// The user edits the file after the agent did.
	os.WriteFile(path, []byte("user change\n"), 0o644)
	if _, err := tracker.Undo(); err == nil || !strings.Contains(err.Error(), "changed outside") {
		t.Fatalf("undo must refuse to clobber external edits, got %v", err)
	}
}
