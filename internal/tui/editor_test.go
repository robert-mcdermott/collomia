package tui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

func TestExternalEditorCommandUsesDirectPlaceholderArgs(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	path := filepath.Join(workspace, "folder", "answer.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package answer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := appconfig.EditorOptions{Command: executable, Args: []string{"--goto", "{file}:{line}:{column}"}}
	cmd, err := externalEditorCommand(cfg, workspace, path, 12, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{executable, "--goto", path + ":12:3"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("argv=%q want %q", cmd.Args, want)
	}
}

func TestExternalEditorCommandAppendsFileAndRejectsOutsidePath(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	inside := filepath.Join(workspace, "answer.go")
	if err := os.WriteFile(inside, []byte("package answer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inside, err = filepath.EvalSymlinks(inside)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := externalEditorCommand(appconfig.EditorOptions{Command: executable, Args: []string{"--wait"}}, workspace, inside, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := cmd.Args[len(cmd.Args)-1]; got != inside {
		t.Fatalf("last arg=%q want %q", got, inside)
	}
	outside := filepath.Join(t.TempDir(), "outside.go")
	if _, err := externalEditorCommand(appconfig.EditorOptions{Command: executable}, workspace, outside, 1, 1); err == nil {
		t.Fatal("outside-workspace editor target should be rejected")
	}
}

func TestUnifiedHunkLine(t *testing.T) {
	for header, want := range map[string]int{
		"@@ -1,2 +8,4 @@": 8,
		"@@ -9 +0,0 @@":   1,
		"malformed":       1,
	} {
		if got := unifiedHunkLine(header); got != want {
			t.Fatalf("unifiedHunkLine(%q)=%d want %d", header, got, want)
		}
	}
}

func TestContainedWorkspacePathResolvesMissingTargetThroughSymlink(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(workspace, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := containedWorkspacePath(workspace, filepath.Join(link, "deleted.go")); err == nil {
		t.Fatal("missing target below outside symlink should be rejected")
	}
}
