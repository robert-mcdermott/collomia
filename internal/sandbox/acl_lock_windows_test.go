//go:build windows

package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestAppContainerACLGrantWorker(t *testing.T) {
	root := os.Getenv("COLLO_TEST_ACL_ROOT")
	if root == "" {
		return
	}
	sid, err := windows.StringToSid(os.Getenv("COLLO_TEST_ACL_SID"))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("ready")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, filepath.Join(root, "bin")} {
		if err := grantAppContainerAccess(path, sid, windows.GENERIC_READ|windows.GENERIC_EXECUTE); err != nil {
			t.Fatal(err)
		}
	}
}

// Actual independent processes, like go test's package binaries and Collo's
// sandbox shims, must retain every workspace's grants on a shared SDK tree.
func TestAppContainerConcurrentACLGrants(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	type worker struct {
		sid    string
		done   chan error
		output *bytes.Buffer
	}
	var workers []worker
	unlock, err := lockAppContainerACL()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	for i := range 8 {
		// Valid distinct synthetic package SIDs; no persistent profiles needed.
		sid := fmt.Sprintf("S-1-15-2-100-200-300-400-500-600-%d", i+1)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAppContainerACLGrantWorker$")
		cmd.Env = append(os.Environ(), "COLLO_TEST_ACL_ROOT="+root, "COLLO_TEST_ACL_SID="+sid)
		input, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		output, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		reader := bufio.NewReader(output)
		if line, err := reader.ReadString('\n'); err != nil || strings.TrimSpace(line) != "ready" {
			_ = input.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("worker readiness: %q %v: %s", line, err, &stderr)
		}
		w := worker{sid: sid, done: make(chan error, 1), output: new(bytes.Buffer)}
		workers = append(workers, w)
		go func() {
			_, _ = fmt.Fprintln(input, "grant")
			_ = input.Close()
			// Drain stdout before Wait closes the pipe.
			_, _ = reader.WriteTo(w.output)
			err := cmd.Wait()
			_, _ = w.output.Write(stderr.Bytes())
			w.done <- err
		}()
	}
	// Each child is ready, but none may finish a grant while the parent holds
	// the cross-process lock. This also rejects a process-local mutex fix.
	select {
	case err := <-workers[0].done:
		t.Fatalf("grant bypassed the parent lock: %v: %s", err, workers[0].output)
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	unlock = nil
	for _, w := range workers {
		if err := <-w.done; err != nil {
			t.Fatalf("grant %s: %v: %s", w.sid, err, w.output)
		}
	}
	for _, path := range []string{root, filepath.Join(root, "bin")} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range workers {
			if !strings.Contains(sd.String(), ";"+w.sid+")") {
				t.Errorf("%s lost grant for %s: %s", path, w.sid, sd.String())
			}
		}
	}
}
