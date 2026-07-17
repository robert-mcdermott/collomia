package tools

import (
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
