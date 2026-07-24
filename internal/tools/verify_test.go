package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectVerificationGoModule(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/x\n\ngo 1.22\n")
	out, err := DetectVerificationTool{Workspace: dir}.Execute(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"go build ./...", "go vet ./...", "go test ./..."} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in: %s", want, out)
		}
	}
}

func TestDetectVerificationNodeUsesPackageScripts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"scripts":{"test":"vitest run","lint":"eslint ."}}`)
	out, err := DetectVerificationTool{Workspace: dir}.Execute(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "npm run test") || !strings.Contains(out, "npm run lint") {
		t.Fatalf("expected npm run scripts: %s", out)
	}
	if strings.Contains(out, "build") {
		t.Fatalf("did not expect a build suggestion without a build script: %s", out)
	}
}

func TestDetectVerificationNodePrefersPnpmLockfile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"scripts":{}}`)
	writeFile(t, dir, "pnpm-lock.yaml", "")
	out, err := DetectVerificationTool{Workspace: dir}.Execute(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "pnpm test") {
		t.Fatalf("expected pnpm test fallback: %s", out)
	}
}

func TestDetectVerificationRust(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Cargo.toml", "[package]\nname = \"x\"\n")
	out, err := DetectVerificationTool{Workspace: dir}.Execute(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cargo build", "cargo clippy", "cargo test"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in: %s", want, out)
		}
	}
}

func TestDetectVerificationPythonRuff(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", "[tool.ruff]\nline-length = 100\n")
	out, err := DetectVerificationTool{Workspace: dir}.Execute(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "pytest") || !strings.Contains(out, "ruff check .") {
		t.Fatalf("expected pytest and ruff: %s", out)
	}
}

func TestDetectVerificationMakefileTargets(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Makefile", "build:\n\tgo build ./...\n\ntest:\n\tgo test ./...\n\ndeploy:\n\techo skip\n")
	out, err := DetectVerificationTool{Workspace: dir}.Execute(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "make build") || !strings.Contains(out, "make test") {
		t.Fatalf("expected make build/test: %s", out)
	}
	if strings.Contains(out, "make deploy") {
		t.Fatalf("did not expect an undetected target: %s", out)
	}
}

func TestDetectVerificationCommandsProvidesStableStructuredSuite(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/x\n\ngo 1.22\n")
	writeFile(t, dir, "Makefile", "test:\n\tgo test ./...\n")
	found, commands := DetectVerificationCommands(dir)
	if len(found) != 2 || len(commands) != 4 {
		t.Fatalf("found=%v commands=%+v", found, commands)
	}
	if commands[0].Purpose != "build" || commands[0].Command != "go build ./..." || commands[2].Command != "go test ./..." || commands[3].Command != "make test" {
		t.Fatalf("unexpected structured order=%+v", commands)
	}
}

func TestDetectVerificationNoProjectFilesFound(t *testing.T) {
	dir := t.TempDir()
	out, err := DetectVerificationTool{Workspace: dir}.Execute(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No known project files") {
		t.Fatalf("expected no-project-files message: %s", out)
	}
}

func TestDetectVerificationReadOnlyRisk(t *testing.T) {
	action, err := DetectVerificationTool{Workspace: t.TempDir()}.Assess(nil)
	if err != nil {
		t.Fatal(err)
	}
	if action.Risk != RiskRead {
		t.Fatalf("expected read risk, got %s", action.Risk)
	}
}
