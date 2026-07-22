package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPathGuardContainsWorkspaceAndSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	guard, err := NewPathGuard(root, false)
	if err != nil {
		t.Fatal(err)
	}
	inside, external, err := guard.Resolve("src/main.go")
	if err != nil || external {
		t.Fatalf("inside resolve: %s %t %v", inside, external, err)
	}
	if _, outsideFlag, err := guard.Resolve(filepath.Join(outside, "secret")); err == nil || !outsideFlag {
		t.Fatalf("outside should fail: outside=%t err=%v", outsideFlag, err)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(root, "escape")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		if _, outsideFlag, err := guard.Resolve(filepath.Join("escape", "secret")); err == nil || !outsideFlag {
			t.Fatalf("symlink escape should fail: outside=%t err=%v", outsideFlag, err)
		}
	}
}

func TestWriteFileBreaksWorkspaceHardLinkInsteadOfMutatingOtherName(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	inside := filepath.Join(root, "inside.txt")
	if err := os.WriteFile(outside, []byte("outside original"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, inside); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	guard, err := NewPathGuard(root, false)
	if err != nil {
		t.Fatal(err)
	}
	tool := WriteFileTool{Guard: guard}
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"path":"inside.txt","content":"workspace replacement"}`)); err != nil {
		t.Fatal(err)
	}
	outsideData, err := os.ReadFile(outside)
	if err != nil || string(outsideData) != "outside original" {
		t.Fatalf("outside hard link changed: %q err=%v", outsideData, err)
	}
	insideData, err := os.ReadFile(inside)
	if err != nil || string(insideData) != "workspace replacement" {
		t.Fatalf("workspace file not replaced: %q err=%v", insideData, err)
	}
}

func TestWriteFileRejectsEscapingSymlinkWithMissingDescendants(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ordinary Windows CI users cannot reliably create symbolic links")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	guard, err := NewPathGuard(root, false)
	if err != nil {
		t.Fatal(err)
	}
	tool := WriteFileTool{Guard: guard}
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"path":"escape/nested/file.txt","content":"no"}`)); err == nil {
		t.Fatal("escaping parent symlink unexpectedly succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "nested", "file.txt")); !os.IsNotExist(err) {
		t.Fatalf("outside file was created: %v", err)
	}
}

func TestMutationTargetRejectsReplacedWorkspaceRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "workspace")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	guard, err := NewPathGuard(root, false)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(base, "original-workspace")
	if err := os.Rename(root, moved); err != nil {
		t.Skipf("cannot replace workspace root on this platform: %v", err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	target, _, err := guard.MutationTarget("payload.txt")
	if target != nil {
		_ = target.Close()
	}
	if err == nil {
		t.Fatal("replacement workspace root unexpectedly accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "payload.txt")); !os.IsNotExist(err) {
		t.Fatalf("replacement workspace was modified: %v", err)
	}
}

func TestEditFileRequiresUniqueMatch(t *testing.T) {
	root := t.TempDir()
	guard, _ := NewPathGuard(root, false)
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("same\nsame\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := EditFileTool{Guard: guard}
	if _, err := tool.Execute(t.Context(), []byte(`{"path":"a.txt","old_text":"same","new_text":"new"}`)); err == nil {
		t.Fatal("expected ambiguous match error")
	}
	result, err := tool.Execute(t.Context(), []byte(`{"path":"a.txt","old_text":"same\nsame","new_text":"new"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result == "" {
		t.Fatal("missing result")
	}
}
