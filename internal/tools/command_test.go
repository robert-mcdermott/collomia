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

func TestRunCommandBuiltInCatastrophicDenial(t *testing.T) {
	workspace := t.TempDir()
	tool, err := NewRunCommandTool(workspace, nil, 1024)
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"command":"rm -rf ."}`)
	action, err := tool.Assess(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(action.HardDenyReasons) == 0 {
		t.Fatalf("assessment did not report catastrophic target: %+v", action)
	}
	if _, err := tool.Execute(t.Context(), raw); err == nil || !strings.Contains(err.Error(), "catastrophic-command protection") {
		t.Fatalf("execution must repeat built-in protection, got %v", err)
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

func TestSandboxCanConfineReadsOutsideWorkspace(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox backend is darwin-only")
	}
	backend := sandbox.ForPlatform()
	if backend.Available() != nil {
		t.Skip("sandbox-exec unavailable")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "inside.txt"), []byte("inside-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	outsideDir, err := os.MkdirTemp(home, ".collomia-sandbox-read-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outsideDir) })
	outside := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outside, []byte("outside-secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	tool, err := NewRunCommandTool(workspace, nil, 64*1024)
	if err != nil {
		t.Fatal(err)
	}
	tool.SandboxMode = sandbox.ModeRequire
	tool.AllowReadOutsideWorkspace = false
	if out, err := tool.Execute(t.Context(), []byte(`{"command":"cat inside.txt"}`)); err != nil || !strings.Contains(out, "inside-value") {
		t.Fatalf("workspace read should succeed: out=%q err=%v", out, err)
	}
	readOutside, err := json.Marshal(map[string]string{"command": "cat " + outside})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := tool.Execute(t.Context(), readOutside); err == nil || strings.Contains(out, "outside-secret") {
		t.Fatalf("ungranted outside read should fail without leaking content: out=%q err=%v", out, err)
	}
	tool.ExtraReadableRoots = []string{outsideDir}
	if out, err := tool.Execute(t.Context(), readOutside); err != nil || !strings.Contains(out, "outside-secret") {
		t.Fatalf("explicit readable root should succeed: out=%q err=%v", out, err)
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

func TestLimitedBufferCanRetainMoreThanItStreams(t *testing.T) {
	var streamed strings.Builder
	buffer := &limitedBuffer{limit: 12, streamLimit: 5, onChunk: func(chunk string) { streamed.WriteString(chunk) }}
	if n, err := buffer.Write([]byte("abcdefgh")); err != nil || n != 8 {
		t.Fatalf("first write n=%d err=%v", n, err)
	}
	if n, err := buffer.Write([]byte("ijklmnop")); err != nil || n != 8 {
		t.Fatalf("second write n=%d err=%v", n, err)
	}
	if got := streamed.String(); got != "abcde" {
		t.Fatalf("streamed=%q", got)
	}
	if got := buffer.String(); !strings.HasPrefix(got, "abcdefghijkl") || !strings.Contains(got, "output truncated") {
		t.Fatalf("retained=%q", got)
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

func TestMinimalEnvKeepsGoBuildCacheWithoutLeakingSecrets(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "go-build")
	t.Setenv("GOCACHE", cache)
	t.Setenv("SUPER_SECRET_TOKEN", "leakme")

	env := minimalEnv()
	if !containsEnv(env, "GOCACHE", cache) {
		t.Fatalf("minimal env did not preserve GOCACHE: %v", env)
	}
	if containsEnv(env, "SUPER_SECRET_TOKEN", "leakme") {
		t.Fatalf("minimal env leaked a parent secret: %v", env)
	}
}

func TestMinimalEnvKeepsWindowsAppContainerPaths(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "profile")
	localAppData := filepath.Join(profile, "AppData", "Local")
	t.Setenv("USERPROFILE", profile)
	t.Setenv("LOCALAPPDATA", localAppData)

	env := minimalEnv()
	if !containsEnv(env, "USERPROFILE", profile) {
		t.Fatalf("minimal env did not preserve USERPROFILE: %v", env)
	}
	if !containsEnv(env, "LOCALAPPDATA", localAppData) {
		t.Fatalf("minimal env did not preserve LOCALAPPDATA: %v", env)
	}
}

func containsEnv(env []string, key, value string) bool {
	want := key + "=" + value
	for _, entry := range env {
		if entry == want {
			return true
		}
	}
	return false
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

func TestResolvedReadableRootsAreWorkspaceRelativeAndExpanded(t *testing.T) {
	workspace := t.TempDir()
	external := t.TempDir()
	t.Setenv("COLLO_TEST_SDK", external)
	tool, err := NewRunCommandTool(workspace, nil, 1024)
	if err != nil {
		t.Fatal(err)
	}
	tool.ExtraReadableRoots = []string{"vendor-sdk", "${COLLO_TEST_SDK}"}
	got := tool.resolvedReadableRoots()
	if len(got) != 2 || got[0] != filepath.Join(workspace, "vendor-sdk") {
		t.Fatalf("roots=%v", got)
	}
	canonicalExternal, err := filepath.EvalSymlinks(external)
	if err != nil {
		t.Fatal(err)
	}
	if got[1] != canonicalExternal {
		t.Fatalf("expanded root=%q, want %q", got[1], canonicalExternal)
	}
}

func TestRunCommandPropagatesIndependentSandboxReadPolicy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture command uses a Unix shell")
	}
	workspace := t.TempDir()
	readable := t.TempDir()
	backend := &recordingBackend{}
	tool, err := NewRunCommandTool(workspace, nil, 1024)
	if err != nil {
		t.Fatal(err)
	}
	tool.SandboxMode = sandbox.ModeRequire
	tool.Backend = backend
	tool.AllowNetwork = true
	tool.AllowReadOutsideWorkspace = false
	tool.ExtraReadableRoots = []string{readable}
	if _, err := tool.Execute(t.Context(), []byte(`{"command":"echo ok"}`)); err != nil {
		t.Fatal(err)
	}
	if !backend.policy.ConstrainReads || !backend.policy.AllowNetwork {
		t.Fatalf("policy=%+v", backend.policy)
	}
	canonicalReadable, err := filepath.EvalSymlinks(readable)
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.policy.ExtraReadableRoots) != 1 || backend.policy.ExtraReadableRoots[0] != canonicalReadable {
		t.Fatalf("readable roots=%v", backend.policy.ExtraReadableRoots)
	}
}

type recordingBackend struct{ policy sandbox.Policy }

func (*recordingBackend) Name() string { return "recording" }
func (*recordingBackend) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{WriteIsolation: true, ReadIsolation: true, NetworkIsolation: sandbox.NetworkFull}
}
func (*recordingBackend) Available() error { return nil }
func (b *recordingBackend) Wrap(argv []string, policy sandbox.Policy) ([]string, error) {
	b.policy = policy
	return argv, nil
}

type failingBackend struct{}

func (failingBackend) Name() string                       { return "failing" }
func (failingBackend) Capabilities() sandbox.Capabilities { return sandbox.Capabilities{} }
func (failingBackend) Available() error                   { return fmt.Errorf("not available") }
func (failingBackend) Wrap([]string, sandbox.Policy) ([]string, error) {
	return nil, fmt.Errorf("not available")
}

// ttyProbe returns a command that reports whether it owns a terminal, and the
// text it prints when it does. These used to skip on Windows because there was
// no ConPTY backend; the only thing that is still platform-specific is the
// shell, so the tests now run everywhere.
//
// The probes differ because the platforms answer the question differently. A
// POSIX shell can ask directly with `test -t 0`. cmd.exe cannot, but a
// pseudoconsole renders its client's output through a virtual terminal, so the
// stream carries escape sequences that a plain captured pipe never does —
// which is the observable difference that matters to a program choosing
// whether to colorize or paginate.
func ttyProbe() (command string, marker string) {
	if runtime.GOOS == "windows" {
		return "echo PROBE-RAN", "PROBE-RAN"
	}
	return "if [ -t 0 ]; then echo IS-A-TTY; else echo NOT-A-TTY; fi", "IS-A-TTY"
}

func TestRunCommandPTY(t *testing.T) {
	tool, err := NewRunCommandTool(t.TempDir(), nil, 8*1024)
	if err != nil {
		t.Fatal(err)
	}
	command, marker := ttyProbe()
	request, err := json.Marshal(map[string]any{"command": command, "pty": true})
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, marker) {
		t.Fatalf("pty run should look like a terminal:\n%s", out)
	}
	plainRequest, err := json.Marshal(map[string]any{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := tool.Execute(t.Context(), plainRequest)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// The pseudoconsole is a terminal and the pipe is not, so only one of
		// them carries VT escapes.
		if !strings.ContainsRune(out, 0x1b) {
			t.Errorf("a pseudoconsole must render through a virtual terminal, but no escape sequence reached the output:\n%q", out)
		}
		if strings.ContainsRune(plain, 0x1b) {
			t.Errorf("a plain captured pipe must not carry terminal escapes:\n%q", plain)
		}
		return
	}
	if !strings.Contains(plain, "NOT-A-TTY") {
		t.Fatalf("plain run must not look like a terminal:\n%s", plain)
	}
}

func TestRunCommandPTYTimeoutKillsGroup(t *testing.T) {
	tool, err := NewRunCommandTool(t.TempDir(), nil, 8*1024)
	if err != nil {
		t.Fatal(err)
	}
	// Something that outlives the timeout without needing a shell builtin that
	// differs between platforms.
	sleeper := "sleep 30"
	if runtime.GOOS == "windows" {
		sleeper = "ping -n 31 127.0.0.1"
	}
	request, err := json.Marshal(map[string]any{"command": sleeper, "timeout_seconds": 1, "pty": true})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = tool.Execute(t.Context(), request)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("timeout took too long: %s", elapsed)
	}
}
