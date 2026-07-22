//go:build windows

package sandbox

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if readInside := os.Getenv("COLLO_APPCONTAINER_READ_INSIDE"); readInside != "" {
		insideRead := "denied"
		if data, err := os.ReadFile(readInside); err == nil && string(data) == "inside-value" {
			insideRead = "ok"
		}
		outsideRead := "denied"
		if data, err := os.ReadFile(os.Getenv("COLLO_APPCONTAINER_READ_OUTSIDE")); err == nil && string(data) == "outside-secret" {
			outsideRead = "ok"
		}
		extraRead := "denied"
		if data, err := os.ReadFile(os.Getenv("COLLO_APPCONTAINER_READ_EXTRA")); err == nil && string(data) == "extra-value" {
			extraRead = "ok"
		}
		fmt.Printf("inside_read=%s outside_read=%s extra_read=%s\n", insideRead, outsideRead, extraRead)
	}
	if address := os.Getenv("COLLO_APPCONTAINER_NETWORK_TARGET"); address != "" {
		network := "denied"
		if conn, err := net.DialTimeout("tcp", address, time.Second); err == nil {
			network = "ok"
			_ = conn.Close()
		}
		fmt.Printf("network=%s\n", network)
	}
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
	secret := filepath.Join(home, fmt.Sprintf(".collomia-appcontainer-secret-%d.txt", os.Getpid()))
	extra := filepath.Join(home, fmt.Sprintf(".collomia-appcontainer-readable-%d.txt", os.Getpid()))
	t.Cleanup(func() {
		_ = os.Remove(outside)
		_ = os.Remove(secret)
		_ = os.Remove(extra)
	})
	if err := os.WriteFile(secret, []byte("outside-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extra, []byte("extra-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	insideRead := filepath.Join(workspace, "readable.txt")
	if err := os.WriteFile(insideRead, []byte("inside-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

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

	wrapped, err := backend.Wrap([]string{worker, "-test.run=TestWindowsAppContainerWorker"}, Policy{WorkspaceRoot: workspace, ConstrainReads: true, ExtraReadableRoots: []string{extra}})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(wrapped[0], wrapped[1:]...)
	cmd.Env = append(os.Environ(),
		"COLLO_APPCONTAINER_WORKER=1",
		"COLLO_APPCONTAINER_WORKSPACE="+workspace,
		"COLLO_APPCONTAINER_OUTSIDE="+outside,
		"COLLO_APPCONTAINER_READ_INSIDE="+insideRead,
		"COLLO_APPCONTAINER_READ_OUTSIDE="+secret,
		"COLLO_APPCONTAINER_READ_EXTRA="+extra,
		"COLLO_APPCONTAINER_NETWORK_TARGET="+listener.Addr().String(),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("AppContainer helper failed: %v\n%s", err, out)
	}
	markerFound := false
	readMarkerFound := false
	networkMarkerFound := false
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "inside=ok outside=denied" {
			markerFound = true
		}
		if strings.TrimSpace(line) == "inside_read=ok outside_read=denied extra_read=ok" {
			readMarkerFound = true
		}
		if strings.TrimSpace(line) == "network=denied" {
			networkMarkerFound = true
		}
	}
	if !markerFound {
		t.Fatalf("enforcement mismatch: %q", strings.TrimSpace(string(out)))
	}
	if !readMarkerFound {
		t.Fatalf("read enforcement mismatch: %q", strings.TrimSpace(string(out)))
	}
	if !networkMarkerFound {
		t.Fatalf("network enforcement mismatch: %q", strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("outside file exists despite AppContainer confinement")
	}
}

func TestWindowsBackendReportsCompleteIsolation(t *testing.T) {
	caps := ForPlatform().Capabilities()
	if !caps.WriteIsolation || !caps.ReadIsolation || !caps.ReadIsolationAlways || caps.NetworkIsolation != NetworkFull || !caps.ProcessIsolation {
		t.Fatalf("capabilities=%+v", caps)
	}
}
