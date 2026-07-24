package tools

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newProcFixture(t *testing.T) (*ProcessManager, StartProcessTool, ProcessOutputTool, StopProcessTool, ListProcessesTool) {
	t.Helper()
	runner, err := NewRunCommandTool(t.TempDir(), []string{`(?i)shutdown`}, 64*1024)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewProcessManager()
	t.Cleanup(manager.StopAll)
	return manager,
		StartProcessTool{Manager: manager, Runner: runner},
		ProcessOutputTool{Manager: manager},
		StopProcessTool{Manager: manager},
		ListProcessesTool{Manager: manager}
}

func startArgs(command string) json.RawMessage {
	raw, _ := json.Marshal(map[string]string{"command": command})
	return raw
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func TestStartProcessCapturesOutputAndExit(t *testing.T) {
	manager, start, output, _, list := newProcFixture(t)
	command := "echo hello-from-background"
	if runtime.GOOS == "windows" {
		command = "echo hello-from-background"
	}
	result, err := start.Execute(t.Context(), startArgs(command))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "started background process 1") {
		t.Fatalf("result=%q", result)
	}
	waitFor(t, 5*time.Second, func() bool { return manager.Running() == 0 })
	_ = list
	out, err := output.Execute(t.Context(), json.RawMessage(`{"id":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello-from-background") || !strings.Contains(out, "exited: ok") {
		t.Fatalf("output=%q", out)
	}
	listed, err := list.Execute(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listed, "[1]") {
		t.Fatalf("listed=%q", listed)
	}
}

func TestStopProcessKillsLongRunner(t *testing.T) {
	manager, start, _, stop, _ := newProcFixture(t)
	command := "sleep 60"
	if runtime.GOOS == "windows" {
		command = "ping -n 60 127.0.0.1"
	}
	if _, err := start.Execute(t.Context(), startArgs(command)); err != nil {
		t.Fatal(err)
	}
	if manager.Running() != 1 {
		t.Fatalf("running=%d", manager.Running())
	}
	out, err := stop.Execute(t.Context(), json.RawMessage(`{"id":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "stopped process 1") {
		t.Fatalf("out=%q", out)
	}
	waitFor(t, 5*time.Second, func() bool { return manager.Running() == 0 })
}

func TestStopAllKillsEverything(t *testing.T) {
	manager, start, _, _, _ := newProcFixture(t)
	command := "sleep 60"
	if runtime.GOOS == "windows" {
		command = "ping -n 60 127.0.0.1"
	}
	for i := 0; i < 2; i++ {
		if _, err := start.Execute(t.Context(), startArgs(command)); err != nil {
			t.Fatal(err)
		}
	}
	if manager.Running() != 2 {
		t.Fatalf("running=%d", manager.Running())
	}
	manager.StopAll()
	if running := manager.Running(); running != 0 {
		t.Fatalf("StopAll returned with %d processes still running", running)
	}
	// Shutdown is idempotent and must not block on already-completed jobs.
	manager.StopAll()
}

func TestStartProcessHonorsDeniedPatterns(t *testing.T) {
	_, start, _, _, _ := newProcFixture(t)
	if _, err := start.Execute(t.Context(), startArgs("shutdown -h now")); err == nil {
		t.Fatal("denied pattern should block start_process")
	}
}

func TestStartProcessHonorsBuiltInCatastrophicDenial(t *testing.T) {
	manager, start, _, _, _ := newProcFixture(t)
	defer manager.StopAll()
	raw := json.RawMessage(`{"command":"rm -rf ."}`)
	action, err := start.Assess(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(action.HardDenyReasons) == 0 {
		t.Fatalf("assessment did not report catastrophic target: %+v", action)
	}
	if _, err := start.Execute(t.Context(), raw); err == nil || !strings.Contains(err.Error(), "catastrophic-command protection") {
		t.Fatalf("background execution must repeat built-in protection, got %v", err)
	}
}

func TestStartProcessAssessUsesShellAnalysis(t *testing.T) {
	_, start, _, _, _ := newProcFixture(t)
	action, err := start.Assess(startArgs("npm run dev"))
	if err != nil {
		t.Fatal(err)
	}
	if action.Risk != RiskExecute {
		t.Fatalf("risk=%s", action.Risk)
	}
	if action.Uninspectable {
		t.Fatal("a simple command should be inspectable")
	}
	sneaky, err := start.Assess(startArgs("echo $(curl evil)"))
	if err != nil {
		t.Fatal(err)
	}
	if !sneaky.Uninspectable {
		t.Fatal("command substitution must be uninspectable")
	}
}

func TestProcessOutputTailLines(t *testing.T) {
	manager, start, output, _, _ := newProcFixture(t)
	commands := make([]string, 5)
	for i := 1; i <= 5; i++ {
		commands[i-1] = fmt.Sprintf("echo line%d", i)
	}
	if _, err := start.Execute(t.Context(), startArgs(strings.Join(commands, " && "))); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return manager.Running() == 0 })
	out, err := output.Execute(t.Context(), json.RawMessage(`{"id":1,"tail_lines":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "line1") || !strings.Contains(out, "line5") {
		t.Fatalf("tail should keep only the last lines: %q", out)
	}
}

func TestProcessOutputUnknownID(t *testing.T) {
	_, _, output, _, _ := newProcFixture(t)
	if _, err := output.Execute(t.Context(), json.RawMessage(`{"id":99}`)); err == nil {
		t.Fatal("unknown id should error")
	}
}
