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

func TestSandboxAutoReportsUnavailableBackend(t *testing.T) {
	tool, err := NewRunCommandTool(t.TempDir(), nil, 4096)
	if err != nil {
		t.Fatal(err)
	}
	tool.SandboxMode = sandbox.ModeAuto
	tool.Backend = failingBackend{}
	out, err := tool.Execute(t.Context(), []byte(`{"command":"echo hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sandbox warning:") || !strings.Contains(out, "normal user privileges") {
		t.Fatalf("auto mode must visibly report degraded execution, got %q", out)
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

func TestResolvedWritableRootsAreWorkspaceRelativeAndExpanded(t *testing.T) {
	workspace := t.TempDir()
	external := t.TempDir()
	t.Setenv("COLLO_TEST_CACHE", external)
	tool, err := NewRunCommandTool(workspace, nil, 1024)
	if err != nil {
		t.Fatal(err)
	}
	tool.ExtraWritableRoots = []string{".cache/build", "${COLLO_TEST_CACHE}"}
	got := tool.resolvedWritableRoots()
	if len(got) != 2 {
		t.Fatalf("roots=%v", got)
	}
	if got[0] != filepath.Join(workspace, ".cache", "build") {
		t.Fatalf("relative root=%q", got[0])
	}
	canonicalExternal, err := filepath.EvalSymlinks(external)
	if err != nil {
		t.Fatal(err)
	}
	if got[1] != canonicalExternal {
		t.Fatalf("expanded root=%q, want %q", got[1], canonicalExternal)
	}
}

type failingBackend struct{}

func (failingBackend) Name() string                       { return "failing" }
func (failingBackend) Capabilities() sandbox.Capabilities { return sandbox.Capabilities{} }
func (failingBackend) Available() error                   { return fmt.Errorf("not available") }
func (failingBackend) Wrap([]string, sandbox.Policy) ([]string, error) {
	return nil, fmt.Errorf("not available")
}

func TestRunCommandPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty unsupported on windows")
	}
	tool, err := NewRunCommandTool(t.TempDir(), nil, 8*1024)
	if err != nil {
		t.Fatal(err)
	}
	// Under a pty, stdin/stdout are terminals; `test -t 0` succeeds only
	// when attached to one.
	out, err := tool.Execute(t.Context(), []byte(`{"command":"if [ -t 0 ]; then echo IS-A-TTY; else echo NOT-A-TTY; fi","pty":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "IS-A-TTY") {
		t.Fatalf("pty run should look like a terminal:\n%s", out)
	}
	// The same probe without pty must not see a terminal.
	out, err = tool.Execute(t.Context(), []byte(`{"command":"if [ -t 0 ]; then echo IS-A-TTY; else echo NOT-A-TTY; fi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "NOT-A-TTY") {
		t.Fatalf("plain run must not look like a terminal:\n%s", out)
	}
}

func TestRunCommandPTYTimeoutKillsGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty unsupported on windows")
	}
	tool, err := NewRunCommandTool(t.TempDir(), nil, 8*1024)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = tool.Execute(t.Context(), []byte(`{"command":"sleep 30","timeout_seconds":1,"pty":true}`))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("timeout took too long: %s", elapsed)
	}
}
