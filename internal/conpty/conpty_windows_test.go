//go:build windows

package conpty

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// consoleOutput begins draining the child's console immediately, because a
// child that fills the output pipe blocks until someone reads it. The stream
// ends when Close releases the pseudoconsole.
func consoleOutput(process *Process) <-chan string {
	out := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(process)
		out <- string(data)
	}()
	return out
}

// awaitOutput collects the drained output on the test's own goroutine, which
// is where t.Fatal must be called from. Reaching the timeout means the read
// never saw end-of-file — the signature of the parent still holding its copy
// of the console's write handle.
func awaitOutput(t *testing.T, out <-chan string) string {
	t.Helper()
	select {
	case data := <-out:
		return data
	case <-time.After(30 * time.Second):
		t.Fatal("console output never reached end-of-file; the parent's copy of the console write handle is probably still open")
		return ""
	}
}

func TestChildOutputReachesTheParentAndEnds(t *testing.T) {
	process, err := Start(Config{Argv: []string{"cmd.exe", "/d", "/s", "/c", "echo CONPTY-HELLO"}})
	if err != nil {
		t.Fatal(err)
	}
	collected := consoleOutput(process)

	code, waitErr := process.Wait()
	_ = process.Close()
	out := awaitOutput(t, collected)

	if waitErr != nil {
		t.Fatalf("echo should succeed: %v", waitErr)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out, "CONPTY-HELLO") {
		t.Fatalf("child output did not reach the parent:\n%q", out)
	}
}

// The pseudoconsole is a terminal, so its client's output is rendered through
// a virtual terminal. This is the property programs actually key on when
// deciding whether to colorize, and the reason pty: true exists.
func TestConsoleOutputIsRenderedThroughAVirtualTerminal(t *testing.T) {
	process, err := Start(Config{Argv: []string{"cmd.exe", "/d", "/s", "/c", "echo VT-PROBE"}})
	if err != nil {
		t.Fatal(err)
	}
	collected := consoleOutput(process)
	_, _ = process.Wait()
	_ = process.Close()
	out := awaitOutput(t, collected)
	if !strings.ContainsRune(out, 0x1b) {
		t.Errorf("no escape sequence in pseudoconsole output; it is not behaving as a terminal:\n%q", out)
	}
}

func TestExitCodeIsReported(t *testing.T) {
	process, err := Start(Config{Argv: []string{"cmd.exe", "/d", "/s", "/c", "exit 3"}})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, process) }()
	code, waitErr := process.Wait()
	_ = process.Close()
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
	// The message shape matches exec.ExitError so a PTY failure reads the same
	// as an ordinary command failure everywhere it is shown.
	if waitErr == nil || !strings.Contains(waitErr.Error(), "exit status 3") {
		t.Errorf("wait error = %v, want it to name exit status 3", waitErr)
	}
}

func TestWaitIsRepeatableAndAgrees(t *testing.T) {
	process, err := Start(Config{Argv: []string{"cmd.exe", "/d", "/s", "/c", "exit 7"}})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, process) }()
	first, firstErr := process.Wait()
	second, secondErr := process.Wait()
	_ = process.Close()
	if first != second || (firstErr == nil) != (secondErr == nil) {
		t.Fatalf("Wait disagreed with itself: (%d, %v) then (%d, %v)", first, firstErr, second, secondErr)
	}
}

func TestWorkingDirectoryAndEnvironmentAreApplied(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker.txt")
	if err := os.WriteFile(marker, []byte("present"), 0o600); err != nil {
		t.Fatal(err)
	}
	process, err := Start(Config{
		Argv: []string{"cmd.exe", "/d", "/s", "/c", "dir /b & echo VALUE=%CONPTY_PROBE%"},
		Dir:  dir,
		// A non-nil Env replaces the parent's. SystemRoot is kept because
		// cmd.exe does not start without it.
		Env: []string{"SystemRoot=" + os.Getenv("SystemRoot"), "CONPTY_PROBE=applied"},
	})
	if err != nil {
		t.Fatal(err)
	}
	collected := consoleOutput(process)
	_, _ = process.Wait()
	_ = process.Close()
	out := awaitOutput(t, collected)
	if !strings.Contains(out, "marker.txt") {
		t.Errorf("child did not run in the requested directory:\n%q", out)
	}
	if !strings.Contains(out, "VALUE=applied") {
		t.Errorf("child did not receive the requested environment:\n%q", out)
	}
}

// Terminating must kill descendants, not only the process that was started.
// This is the contract the ordinary command path already keeps, and the reason
// the child is created suspended and joined to a job object before it runs:
// a child resumed first could spawn a grandchild outside the job.
func TestTerminateKillsTheWholeTree(t *testing.T) {
	// cmd.exe starts a child ping that outlives its parent unless the whole
	// job is terminated.
	process, err := Start(Config{Argv: []string{"cmd.exe", "/d", "/s", "/c", "start /b ping -n 120 127.0.0.1 & ping -n 120 127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, process) }()
	// Give the tree a moment to exist before killing it.
	time.Sleep(500 * time.Millisecond)
	descendants := descendantsOf(uint32(process.Pid()))
	if err := process.Terminate(); err != nil {
		t.Fatal(err)
	}
	_, _ = process.Wait()
	_ = process.Close()

	deadline := time.Now().Add(10 * time.Second)
	for _, pid := range descendants {
		for time.Now().Before(deadline) {
			if !processAlive(pid) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if processAlive(pid) {
			t.Fatalf("descendant %d survived termination of the job", pid)
		}
	}
}

func TestResizeIsAcceptedWhileRunning(t *testing.T) {
	process, err := Start(Config{Argv: []string{"cmd.exe", "/d", "/s", "/c", "ping -n 5 127.0.0.1"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, process) }()
	if err := process.Resize(132, 43); err != nil {
		t.Errorf("resize: %v", err)
	}
	// A degenerate size is ignored rather than passed to an API that rejects it.
	if err := process.Resize(0, 0); err != nil {
		t.Errorf("a zero resize should be a no-op, got %v", err)
	}
	_ = process.Terminate()
	_, _ = process.Wait()
	_ = process.Close()
}

func TestCloseIsIdempotentAndKillsARunningChild(t *testing.T) {
	process, err := Start(Config{Argv: []string{"cmd.exe", "/d", "/s", "/c", "ping -n 120 127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, process) }()
	pid := uint32(process.Pid())
	// Close on a live child must not hang: ClosePseudoConsole waits for its
	// client to detach, so Close kills the job first.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		_ = process.Close()
		_ = process.Close()
	}()
	select {
	case <-closed:
	case <-time.After(20 * time.Second):
		t.Fatal("Close blocked on a running child; the pseudoconsole is being closed before the job is terminated")
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && processAlive(pid) {
		time.Sleep(100 * time.Millisecond)
	}
	if processAlive(pid) {
		t.Fatal("Close left the child running")
	}
}

func TestStartRejectsAnUnknownExecutable(t *testing.T) {
	if _, err := Start(Config{Argv: []string{"collomia-no-such-program.exe"}}); err == nil {
		t.Fatal("starting a nonexistent executable must fail")
	}
	if _, err := Start(Config{}); err == nil {
		t.Fatal("starting with no command must fail")
	}
}

func TestCommandLineMatchesOsExecQuoting(t *testing.T) {
	// pty: true and pty: false must quote a command identically; differing
	// quoting between the two would be a far worse surprise than either rule.
	got := commandLine([]string{"cmd.exe", "/d", "/s", "/c", `echo "a b"`})
	want := `cmd.exe /d /s /c "echo \"a b\""`
	if got != want {
		t.Errorf("commandLine = %q, want %q", got, want)
	}
}

func TestEnvironmentBlockShapes(t *testing.T) {
	if block, err := environmentBlock(nil); err != nil || block != nil {
		t.Errorf("a nil environment must inherit the parent's, got %v %v", block, err)
	}
	block, err := environmentBlock([]string{})
	if err != nil {
		t.Fatal(err)
	}
	if block == nil {
		t.Fatal("an empty environment must still be a valid block")
	}
	if _, err := environmentBlock([]string{"BAD\x00KEY=value"}); err == nil {
		t.Error("a NUL inside an entry would truncate the block and must be refused")
	}
}

func descendantsOf(root uint32) []uint32 {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snapshot)
	children := map[uint32][]uint32{}
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for iterErr := windows.Process32First(snapshot, &entry); iterErr == nil; iterErr = windows.Process32Next(snapshot, &entry) {
		children[entry.ParentProcessID] = append(children[entry.ParentProcessID], entry.ProcessID)
	}
	var found []uint32
	visited := map[uint32]bool{root: true}
	queue := []uint32{root}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, child := range children[pid] {
			if visited[child] {
				continue
			}
			visited[child] = true
			found = append(found, child)
			queue = append(queue, child)
		}
	}
	return found
}

func processAlive(pid uint32) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	event, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && event == uint32(windows.WAIT_TIMEOUT)
}
