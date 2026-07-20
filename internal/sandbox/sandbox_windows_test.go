//go:build windows

package sandbox

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain also handles the backend's hidden re-exec shim. The real collo
// binary dispatches this in cmd/collo; the test binary mirrors that small
// piece so CI can exercise AppContainer enforcement on windows-latest.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "__appcontainer" {
		if len(os.Args) < 5 || os.Args[3] != "--" {
			fmt.Fprintln(os.Stderr, "invalid AppContainer test shim arguments")
			os.Exit(2)
		}
		policy, err := DecodePolicy(os.Args[2])
		if err == nil {
			err = RunAppContainer(policy, os.Args[4:])
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestWindowsAppContainerWorker(t *testing.T) {
	if os.Getenv("COLLO_APPCONTAINER_WORKER") != "1" {
		return
	}
	inside := "denied"
	if os.WriteFile(filepath.Join(os.Getenv("COLLO_APPCONTAINER_WORKSPACE"), "inside.txt"), []byte("ok"), 0o600) == nil {
		inside = "ok"
	}
	outside := "denied"
	if os.WriteFile(os.Getenv("COLLO_APPCONTAINER_OUTSIDE"), []byte("escape"), 0o600) == nil {
		outside = "ok"
	}
	fmt.Printf("inside=%s outside=%s\n", inside, outside)
}

func TestWindowsAppContainerConfinesWrites(t *testing.T) {
	backend := ForPlatform()
	if err := backend.Available(); err != nil {
		t.Fatalf("Windows 11 must provide the built-in AppContainer APIs: %v", err)
	}
	workspace := t.TempDir()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(home, fmt.Sprintf(".collomia-appcontainer-escape-%d.txt", os.Getpid()))
	t.Cleanup(func() { _ = os.Remove(outside) })

	// Put the worker executable in the granted workspace so an AppContainer
	// can read and execute it without broadening access to the Go build cache.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	worker := filepath.Join(workspace, "sandbox-worker.exe")
	src, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := os.Create(worker)
	if err != nil {
		_ = src.Close()
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = src.Close()
		_ = dst.Close()
		t.Fatal(err)
	}
	_ = src.Close()
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}

	wrapped, err := backend.Wrap([]string{worker, "-test.run=TestWindowsAppContainerWorker"}, Policy{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(wrapped[0], wrapped[1:]...)
	cmd.Env = append(os.Environ(),
		"COLLO_APPCONTAINER_WORKER=1",
		"COLLO_APPCONTAINER_WORKSPACE="+workspace,
		"COLLO_APPCONTAINER_OUTSIDE="+outside,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("AppContainer helper failed: %v\n%s", err, out)
	}
	markerFound := false
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "inside=ok outside=denied" {
			markerFound = true
			break
		}
	}
	if !markerFound {
		t.Fatalf("enforcement mismatch: %q", strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("outside file exists despite AppContainer confinement")
	}
}

func TestWindowsBackendReportsCompleteIsolation(t *testing.T) {
	caps := ForPlatform().Capabilities()
	if !caps.WriteIsolation || !caps.ReadIsolation || caps.NetworkIsolation != NetworkFull || !caps.ProcessIsolation {
		t.Fatalf("capabilities=%+v", caps)
	}
}
