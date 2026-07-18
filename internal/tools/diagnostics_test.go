package tools

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

func TestDiagnosticsRejectsUnknownLanguage(t *testing.T) {
	guard, err := NewPathGuard(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	tool := DiagnosticsTool{Guard: guard}
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"paths":["notes.txt"]}`)); err == nil || !strings.Contains(err.Error(), "no language server mapping") {
		t.Fatalf("err=%v", err)
	}
}

func TestDiagnosticsRejectsMixedLanguages(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.go", "b.py"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	guard, err := NewPathGuard(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	tool := DiagnosticsTool{Guard: guard}
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"paths":["a.go","b.py"]}`)); err == nil || !strings.Contains(err.Error(), "share a language") {
		t.Fatalf("err=%v", err)
	}
}

func TestDiagnosticsReportsMissingServer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.rs"), []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	guard, err := NewPathGuard(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	// Point rust at a binary that certainly does not exist.
	tool := DiagnosticsTool{Guard: guard, Servers: map[string][]string{"rust": {"no-such-language-server"}}}
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"paths":["lib.rs"]}`)); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("err=%v", err)
	}
}

// TestDiagnosticsAgainstRealGopls exercises the full path against gopls
// when it is installed; skipped otherwise (e.g. bare CI runners).
func TestDiagnosticsAgainstRealGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	if testing.Short() {
		t.Skip("short mode")
	}
	dir := t.TempDir()
	writeFileOrFail := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFileOrFail("go.mod", "module diagfixture\n\ngo 1.22\n")
	writeFileOrFail("main.go", "package main\n\nfunc main() {\n\tundefinedCall()\n}\n")
	registry, _, _, err := Builtins(dir, appconfig.Config{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := registry.Execute(t.Context(), "diagnostics", json.RawMessage(`{"paths":["main.go"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "main.go:4") || !strings.Contains(strings.ToLower(out), "undefinedcall") {
		t.Fatalf("expected an undefined-call diagnostic at main.go:4:\n%s", out)
	}
}
