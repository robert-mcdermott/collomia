package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileDiscoverySkipsGeneratedAndDependencyTrees(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "source.go"), []byte("package source // NEEDLE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".git", ".venv", "venv", ".uv-cache", ".pytest_cache", ".mypy_cache", ".ruff_cache", "__pycache__", "node_modules", "vendor", "dist", "build", "target"} {
		path := filepath.Join(workspace, dir)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "generated.txt"), []byte("NEEDLE\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	guard, err := NewPathGuard(workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	listing, err := (ListFilesTool{Guard: guard}).Execute(context.Background(), json.RawMessage(`{"path":".","max_depth":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listing, "source.go") {
		t.Fatalf("source file missing from listing:\n%s", listing)
	}
	for _, skipped := range []string{".venv/", ".uv-cache/", ".pytest_cache/", "node_modules/", "target/"} {
		if strings.Contains(listing, skipped) {
			t.Fatalf("generated tree %q leaked into listing:\n%s", skipped, listing)
		}
	}

	matches, err := (SearchFilesTool{Guard: guard}).Execute(context.Background(), json.RawMessage(`{"pattern":"NEEDLE","path":"."}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(matches) != "source.go:1:package source // NEEDLE" {
		t.Fatalf("generated trees leaked into search results:\n%s", matches)
	}
}
