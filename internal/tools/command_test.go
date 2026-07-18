package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/robert-mcdermott/collomia/internal/sandbox"
)

func TestRunCommandHardDenial(t *testing.T) {
	tool, err := NewRunCommandTool(t.TempDir(), []string{`(?i)rm\s+-rf\s+/`}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tool.Execute(t.Context(), []byte(`{"command":"rm -rf /"}`)); err == nil {
		t.Fatal("expected dangerous command to be denied")
	}
}

func TestAssessCarriesCommandAnalysis(t *testing.T) {
	tool, err := NewRunCommandTool(t.TempDir(), nil, 1024)
	if err != nil {
		t.Fatal(err)
	}
	action, err := tool.Assess(json.RawMessage(`{"command":"go test ./... | tee out.log"}`))
	if err != nil {
		t.Fatal(err)
	}
	if action.Uninspectable {
		t.Fatalf("simple pipeline should be inspectable: %+v", action)
	}
	joined := strings.Join(action.Executables, ",")
	if !strings.Contains(joined, "go") || !strings.Contains(joined, "tee") {
		t.Fatalf("executables=%v", action.Executables)
	}
	action, err = tool.Assess(json.RawMessage(`{"command":"echo $(whoami)"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !action.Uninspectable {
		t.Fatal("substitution should be uninspectable")
	}
}

func TestTimeoutKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix process-group test")
	}
	tool, err := NewRunCommandTool(t.TempDir(), nil, 64*1024)
	if err != nil {
		t.Fatal(err)
	}
	marker := fmt.Sprintf("29%d", os.Getpid()%1000+1000)
	start := time.Now()
	_, err = tool.Execute(t.Context(), []byte(fmt.Sprintf(`{"command":"sleep %s & sleep %s","timeout_seconds":1}`, marker, marker)))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("timeout took %s; descendants were not killed promptly", elapsed)
	}
	// The backgrounded sleep must be dead too.
	out, _ := exec.Command("pgrep", "-f", "sleep "+marker).Output()
	if strings.TrimSpace(string(out)) != "" {
		exec.Command("pkill", "-f", "sleep "+marker).Run()
		t.Fatalf("background child survived the timeout: pids %s", out)
	}
}

func TestSandboxBlocksWritesOutsideWorkspace(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox backend is darwin-only")
	}
	backend := sandbox.ForPlatform()
	if backend.Available() != nil {
		t.Skip("sandbox-exec unavailable")
	}
	workspace := t.TempDir()
	// t.TempDir lives under the user temp root, which the sandbox profile
	// legitimately allows; the escape attempt must target somewhere the
	// profile does not list, like the home directory.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	outside := filepath.Join(home, fmt.Sprintf(".collomia-sandbox-escape-%d.txt", os.Getpid()))
	t.Cleanup(func() { os.Remove(outside) })
	tool, err := NewRunCommandTool(workspace, nil, 64*1024)
	if err != nil {
		t.Fatal(err)
	}
	tool.SandboxMode = sandbox.ModeRequire

	if _, err := tool.Execute(t.Context(), []byte(fmt.Sprintf(`{"command":"echo pwned > %s"}`, outside))); err == nil {
		t.Fatal("write outside the workspace should fail inside the sandbox")
	}
	if _, statErr := os.Stat(outside); statErr == nil {
		t.Fatal("outside file was created despite the sandbox")
	}
	if _, err := tool.Execute(t.Context(), []byte(`{"command":"echo ok > inside.txt"}`)); err != nil {
		t.Fatalf("workspace write should succeed inside the sandbox: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, "inside.txt")); statErr != nil {
		t.Fatalf("workspace file missing: %v", statErr)
	}
}

func TestSandboxRequireFailsClosedWhenUnavailable(t *testing.T) {
	tool, err := NewRunCommandTool(t.TempDir(), nil, 1024)
	if err != nil {
		t.Fatal(err)
	}
	tool.SandboxMode = sandbox.ModeRequire
	tool.Backend = failingBackend{}
	if _, err := tool.Execute(t.Context(), []byte(`{"command":"echo hi"}`)); err == nil || !strings.Contains(err.Error(), "sandbox required") {
		t.Fatalf("require mode must fail closed, got %v", err)
	}
}

func TestExecuteStreamDeliversLiveChunks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell test")
	}
	tool, err := NewRunCommandTool(t.TempDir(), nil, 64*1024)
	if err != nil {
		t.Fatal(err)
	}
	var chunks []string
	out, err := tool.ExecuteStream(t.Context(), []byte(`{"command":"echo first; echo second"}`), func(chunk string) {
		chunks = append(chunks, chunk)
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(chunks, "")
	if !strings.Contains(joined, "first") || !strings.Contains(joined, "second") {
		t.Fatalf("streamed chunks missing output: %q", joined)
	}
	if !strings.Contains(out, "first") {
		t.Fatalf("final result missing output: %q", out)
	}
}

func TestMinimalEnvStripsSecrets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix env test")
	}
	t.Setenv("SUPER_SECRET_TOKEN", "leakme")
	tool, err := NewRunCommandTool(t.TempDir(), nil, 8*1024)
	if err != nil {
		t.Fatal(err)
	}
	tool.MinimalEnv = true
	out, err := tool.Execute(t.Context(), []byte(`{"command":"env"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "SUPER_SECRET_TOKEN") {
		t.Fatal("minimal env leaked a parent secret")
	}
	if !strings.Contains(out, "PATH=") {
		t.Fatalf("minimal env must keep PATH:\n%s", out)
	}
}

type failingBackend struct{}

func (failingBackend) Name() string     { return "failing" }
func (failingBackend) Available() error { return fmt.Errorf("not available") }
func (failingBackend) Wrap([]string, sandbox.Policy) ([]string, error) {
	return nil, fmt.Errorf("not available")
}
