package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/diffmodel"
)

func patchTool(t *testing.T) (ApplyPatchTool, string) {
	t.Helper()
	dir := t.TempDir()
	guard, err := NewPathGuard(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return ApplyPatchTool{Guard: guard, Tracker: diffmodel.NewTracker()}, guard.Workspace
}

func TestApplyPatchMultiFileAtomic(t *testing.T) {
	tool, dir := patchTool(t)
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nvar Old = 1\n"), 0o644)
	input := `{"operations":[
		{"op":"update","path":"a.go","old_text":"var Old = 1","new_text":"var New = 2"},
		{"op":"create","path":"b.go","content":"package a\n"},
		{"op":"delete","path":"a.go"}
	]}`
	out, err := tool.Execute(t.Context(), json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"applied"`) || !strings.Contains(out, "b.go") {
		t.Fatalf("changeset=%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "a.go")); statErr == nil {
		t.Fatal("a.go should be deleted")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "b.go")); statErr != nil {
		t.Fatal("b.go should exist")
	}
}

func TestApplyPatchValidationFailureChangesNothing(t *testing.T) {
	tool, dir := patchTool(t)
	original := "package a\nvar Old = 1\n"
	os.WriteFile(filepath.Join(dir, "a.go"), []byte(original), 0o644)
	input := `{"operations":[
		{"op":"update","path":"a.go","old_text":"var Old = 1","new_text":"var New = 2"},
		{"op":"update","path":"a.go","old_text":"DOES NOT EXIST","new_text":"x"}
	]}`
	if _, err := tool.Execute(t.Context(), json.RawMessage(input)); err == nil {
		t.Fatal("expected validation failure")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	if string(data) != original {
		t.Fatalf("file changed despite failed validation: %q", data)
	}
}

func TestApplyPatchInvalidParentChangesNothing(t *testing.T) {
	tool, dir := patchTool(t)
	original := "v1\n"
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte(original), 0o644)
	os.WriteFile(filepath.Join(dir, "blocker"), []byte("i am a file"), 0o644)
	// Second op tries to create a file beneath a path that is a regular file.
	// Secure rooted validation detects that invalid parent before applying the
	// first operation.
	input := `{"operations":[
		{"op":"update","path":"a.txt","old_text":"v1","new_text":"v2"},
		{"op":"create","path":"blocker/nested.txt","content":"x"}
	]}`
	if _, err := tool.Execute(t.Context(), json.RawMessage(input)); err == nil {
		t.Fatal("expected invalid-parent error")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(data) != original {
		t.Fatalf("first operation not rolled back: %q", data)
	}
}

func TestApplyPatchAssessCarriesDiffPreview(t *testing.T) {
	tool, dir := patchTool(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644)
	action, err := tool.Assess(json.RawMessage(`{"operations":[{"op":"update","path":"a.txt","old_text":"hello","new_text":"goodbye"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if action.Risk != RiskWrite || len(action.Paths) != 1 {
		t.Fatalf("action=%+v", action)
	}
	if !strings.Contains(action.Preview, "-hello") || !strings.Contains(action.Preview, "+goodbye") {
		t.Fatalf("preview=%q", action.Preview)
	}
}

func TestGitToolsInRealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\n"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "first")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\ntwo\n"), 0o644)

	statusTool := GitStatusTool{Workspace: dir}
	diffTool := GitDiffTool{Workspace: dir}
	logTool := GitLogTool{Workspace: dir}
	status, err := statusTool.Execute(t.Context(), json.RawMessage(`{}`))
	if err != nil || !strings.Contains(status, "f.txt") {
		t.Fatalf("status=%q err=%v", status, err)
	}
	diff, err := diffTool.Execute(t.Context(), json.RawMessage(`{}`))
	if err != nil || !strings.Contains(diff, "+two") {
		t.Fatalf("diff=%q err=%v", diff, err)
	}
	log, err := logTool.Execute(t.Context(), json.RawMessage(`{"limit":5}`))
	if err != nil || !strings.Contains(log, "first") {
		t.Fatalf("log=%q err=%v", log, err)
	}
	if _, err := diffTool.Execute(t.Context(), json.RawMessage(fmt.Sprintf(`{"ref":%q}`, "--exec=evil"))); err == nil {
		t.Fatal("flag-shaped ref must be rejected")
	}
}
