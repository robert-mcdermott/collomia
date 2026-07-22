package diffmodel

import (
	"os"
	"path/filepath"
	"runtime"
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

func TestUndoRestoresOriginalModeWithAtomicReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")
	before, after := "#!/bin/sh\necho before\n", "#!/bin/sh\necho after\n"
	if err := os.WriteFile(path, []byte(after), 0o755); err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker(dir)
	tracker.RecordWithMode(path, "edit", &before, &after, 0o700, 0o755)
	if _, err := tracker.Undo(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != before {
		t.Fatalf("content=%q err=%v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("mode=%v", info.Mode().Perm())
	}
}

func TestUndoRejectsReplacedWorkspaceRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "workspace")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "file.txt")
	before, after := "before", "after"
	if err := os.WriteFile(path, []byte(after), 0o600); err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker(root)
	tracker.RecordWithMode(path, "edit", &before, &after, 0o600, 0o600)
	if err := os.Rename(root, filepath.Join(base, "original-workspace")); err != nil {
		t.Skipf("cannot replace workspace root on this platform: %v", err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.Undo(); err == nil || !strings.Contains(err.Error(), "root changed") {
		t.Fatalf("undo should reject a replacement workspace root, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "file.txt")); !os.IsNotExist(err) {
		t.Fatalf("replacement workspace was modified: %v", err)
	}
}
