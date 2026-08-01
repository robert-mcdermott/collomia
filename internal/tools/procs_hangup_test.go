//go:build !windows

package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/shutdown"
)

// The end-to-end proof that terminal loss is survivable.
//
// The unit tests in internal/shutdown assert that SIGHUP is in a slice, which
// says nothing about whether anything happens when one arrives. This starts a
// real process that wires itself the way Collomia does — the shutdown context,
// a background process started through the real ProcessManager, StopAll on
// cancellation — then sends it a real SIGHUP and looks at what is left running.
//
// Before SIGHUP was handled this failed at the last assertion: the helper died
// instantly under the runtime's default disposition, no teardown ran, and the
// background process survived because ProcessManager gives each one its own
// process group and a hangup reaches only the foreground group.
func TestHangupStopsBackgroundProcessesInsteadOfOrphaningThem(t *testing.T) {
	helper := exec.Command(os.Args[0], "-test.run=TestHangupHelperProcess", "-test.timeout=60s")
	helper.Env = append(os.Environ(), "COLLO_HANGUP_HELPER=1")
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	helper.Stderr = os.Stderr
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}

	lines := bufio.NewScanner(stdout)
	type helperResult struct {
		teardown bool
		scanErr  error
		waitErr  error
	}
	ready := make(chan int, 1)
	finished := make(chan helperResult, 1)
	go func() {
		result := helperResult{}
		for lines.Scan() {
			line := strings.TrimSpace(lines.Text())
			switch {
			case strings.HasPrefix(line, "BACKGROUND_PID="):
				pid, err := strconv.Atoi(strings.TrimPrefix(line, "BACKGROUND_PID="))
				if err != nil {
					result.scanErr = fmt.Errorf("parse helper output %q: %w", line, err)
					continue
				}
				ready <- pid
			case line == "TEARDOWN_RAN":
				result.teardown = true
			}
		}
		if err := lines.Err(); err != nil && result.scanErr == nil {
			result.scanErr = fmt.Errorf("read helper output: %w", err)
		}
		// StdoutPipe requires all reads to finish before Wait. Keeping both in
		// this goroutine also makes delivery of the final TEARDOWN_RAN line
		// happen-before the test inspects result.teardown.
		result.waitErr = helper.Wait()
		finished <- result
	}()
	processWaited := false
	defer func() {
		if processWaited {
			return
		}
		_ = helper.Process.Kill()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
		}
	}()

	backgroundPID := 0
	select {
	case backgroundPID = <-ready:
	case result := <-finished:
		processWaited = true
		if result.scanErr != nil {
			t.Fatalf("helper output failed before reporting a background process: %v", result.scanErr)
		}
		t.Fatalf("helper exited before reporting a background process: %v", result.waitErr)
	case <-time.After(30 * time.Second):
		t.Fatal("helper never reported a background process")
	}
	if backgroundPID <= 0 {
		t.Fatalf("helper reported an unusable background pid %d", backgroundPID)
	}
	if !processAlive(backgroundPID) {
		t.Fatalf("background process %d was not running before the hangup", backgroundPID)
	}

	if err := helper.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatalf("send SIGHUP: %v", err)
	}
	var result helperResult
	select {
	case result = <-finished:
		processWaited = true
	case <-time.After(30 * time.Second):
		// The failure mode a swallowed signal produces: the process neither
		// exits nor tears down, which is worse than the crash it replaced.
		t.Fatal("helper did not exit after SIGHUP; the signal was captured but nothing acted on it")
	}
	if result.scanErr != nil {
		t.Fatalf("read helper output: %v", result.scanErr)
	}
	if result.waitErr != nil {
		t.Errorf("helper exited unsuccessfully after SIGHUP: %v", result.waitErr)
	}
	if !result.teardown {
		t.Error("teardown did not run on SIGHUP, so nothing would have closed the session or stopped processes")
	}
	if err := waitForExit(backgroundPID, 15*time.Second); err != nil {
		t.Fatalf("background process %d survived the hangup: %v", backgroundPID, err)
	}
}

// TestHangupHelperProcess is not a test. It is the child half of the test
// above, selected by -test.run and gated on an environment variable so an
// ordinary `go test` run skips it.
func TestHangupHelperProcess(t *testing.T) {
	if os.Getenv("COLLO_HANGUP_HELPER") != "1" {
		t.Skip("helper process for TestHangupStopsBackgroundProcessesInsteadOfOrphaningThem")
	}
	workspace := t.TempDir()
	runner, err := ConfiguredRunCommandTool(workspace, appconfig.Defaults(), 64*1024)
	if err != nil {
		fmt.Println("HELPER_ERROR=" + err.Error())
		return
	}
	manager := NewProcessManager()

	// The same wiring cmd/collo uses: a context cancelled by a shutdown signal.
	ctx, stop := shutdown.NotifyContext(context.Background())
	defer stop()

	// The process reports its own pid rather than the manager exposing one.
	// `exec` replaces the shell, so the recorded pid is the surviving process
	// itself and not a parent that would exit on its own anyway.
	pidFile := filepath.Join(workspace, "background.pid")
	command := fmt.Sprintf("echo $$ > %s; exec sleep 120", pidFile)
	start := StartProcessTool{Manager: manager, Runner: runner}
	args, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		fmt.Println("HELPER_ERROR=" + err.Error())
		return
	}
	if _, err := start.Execute(context.Background(), args); err != nil {
		fmt.Println("HELPER_ERROR=" + err.Error())
		return
	}
	pid, err := readPIDFile(pidFile, 20*time.Second)
	if err != nil {
		fmt.Println("HELPER_ERROR=" + err.Error())
		return
	}
	fmt.Printf("BACKGROUND_PID=%d\n", pid)
	os.Stdout.Sync()

	<-ctx.Done()
	// Runtime.Close does exactly this, among other things.
	manager.StopAll()
	fmt.Println("TEARDOWN_RAN")
	os.Stdout.Sync()
}

// readPIDFile waits for the background process to record its own pid.
func readPIDFile(path string, within time.Duration) (int, error) {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 {
				return pid, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return 0, fmt.Errorf("background process never wrote %s", path)
}

func processAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

func waitForExit(pid int, within time.Duration) error {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("still alive after %s", within)
}
