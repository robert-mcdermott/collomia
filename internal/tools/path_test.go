package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
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

// A read root makes a directory readable and nothing else. It exists because
// load_skill tells the model to open a skill's references with read_file, and
// the read was then denied for being outside the workspace — Collomia
// contradicting its own tool description over files the user installed.
func TestReadRootAllowsReadsAndNeverWrites(t *testing.T) {
	root := t.TempDir()
	skill := t.TempDir()
	reference := filepath.Join(skill, "references", "example-page.html")
	if err := os.MkdirAll(filepath.Dir(reference), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reference, []byte("<h1>reference</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	guard, err := NewPathGuard(root, false)
	if err != nil {
		t.Fatal(err)
	}

	// Before registration the read is refused, which is the reported failure.
	if _, _, err := guard.ResolveRead(reference); err == nil {
		t.Fatal("an unregistered outside path was readable")
	}
	guard.AddReadRoot(skill)

	resolved, outside, err := guard.ResolveRead(reference)
	if err != nil || !outside || resolved == "" {
		t.Fatalf("registered skill reference read: resolved=%q outside=%t err=%v", resolved, outside, err)
	}
	content, err := ReadFileTool{Guard: guard}.Execute(t.Context(), json.RawMessage(`{"path":`+strconv.Quote(reference)+`}`))
	if err != nil || !strings.Contains(content, "reference") {
		t.Fatalf("read_file on a skill reference: %q err=%v", content, err)
	}

	// The strict path is what every mutation resolves through, so the same
	// directory stays read-only however it is reached.
	if _, _, err := guard.Resolve(reference); err == nil {
		t.Fatal("a read root widened the strict resolver")
	}
	if _, err := (WriteFileTool{Guard: guard}).Execute(t.Context(), json.RawMessage(`{"path":`+strconv.Quote(reference)+`,"content":"tampered"}`)); err == nil {
		t.Fatal("a read root allowed a write into a skill directory")
	}
	if _, err := (EditFileTool{Guard: guard}).Execute(t.Context(), json.RawMessage(`{"path":`+strconv.Quote(reference)+`,"old_text":"reference","new_text":"tampered"}`)); err == nil {
		t.Fatal("a read root allowed an edit inside a skill directory")
	}
	after, err := os.ReadFile(reference)
	if err != nil || string(after) != "<h1>reference</h1>" {
		t.Fatalf("skill reference changed: %q err=%v", after, err)
	}
}

// Containment is checked against the path a request actually resolves to, so
// a link planted inside a skill bundle cannot turn a read allowance into a
// tour of the filesystem.
func TestReadRootDoesNotFollowLinksOutOfItself(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ordinary Windows CI users cannot reliably create symbolic links")
	}
	root := t.TempDir()
	skill := t.TempDir()
	secrets := t.TempDir()
	if err := os.WriteFile(filepath.Join(secrets, "creds"), []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secrets, filepath.Join(skill, "escape")); err != nil {
		t.Fatal(err)
	}
	guard, err := NewPathGuard(root, false)
	if err != nil {
		t.Fatal(err)
	}
	guard.AddReadRoot(skill)
	if _, _, err := guard.ResolveRead(filepath.Join(skill, "escape", "creds")); err == nil {
		t.Fatal("a symlink inside a read root escaped it")
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
