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
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestMain also handles the backend's hidden re-exec shim so CI exercises the
// same dispatcher as the real collo binary.
func TestMain(m *testing.M) {
	if handled, err := DispatchReexec(os.Args[1:]); handled {
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
	if os.Getenv("COLLO_APPCONTAINER_NULL_CHILD") == "1" {
		nullDevice := "denied"
		if file, err := os.Open(os.DevNull); err == nil {
			nullDevice = "ok"
			_ = file.Close()
		}
		fmt.Printf("child_null_device=%s\n", nullDevice)
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
	nullDevice := "denied"
	if file, err := os.Open(os.DevNull); err == nil {
		nullDevice = "ok"
		_ = file.Close()
	}
	fmt.Printf("null_device=%s\n", nullDevice)

	childNullDevice := "denied"
	child := exec.Command(os.Args[0], "-test.run=TestWindowsAppContainerWorker")
	child.Env = append(os.Environ(), "COLLO_APPCONTAINER_NULL_CHILD=1")
	if output, err := child.CombinedOutput(); err == nil && strings.Contains(strings.ReplaceAll(string(output), "\r\n", "\n"), "child_null_device=ok\n") {
		childNullDevice = "ok"
	}
	fmt.Printf("descendant_null_device=%s\n", childNullDevice)
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
	// Exercise the hidden shim with the same non-secret Windows essentials
	// available in production's minimal command environment. In particular,
	// CreateProcess needs LOCALAPPDATA to construct the AppContainer profile
	// environment, and RunAppContainer uses USERPROFILE when granting
	// read/execute access to user-local PATH entries.
	cmd.Env = append(windowsAppContainerTestEnv(t),
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
	nullDeviceMarkerFound := false
	descendantNullDeviceMarkerFound := false
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
		if strings.TrimSpace(line) == "null_device=ok" {
			nullDeviceMarkerFound = true
		}
		if strings.TrimSpace(line) == "descendant_null_device=ok" {
			descendantNullDeviceMarkerFound = true
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
	if !nullDeviceMarkerFound {
		t.Fatalf("null-device compatibility mismatch: %q", strings.TrimSpace(string(out)))
	}
	if !descendantNullDeviceMarkerFound {
		t.Fatalf("descendant null-device compatibility mismatch: %q", strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("outside file exists despite AppContainer confinement")
	}
}

func windowsAppContainerTestEnv(t *testing.T) []string {
	t.Helper()
	keys := []string{"PATH", "TEMP", "TMP", "SYSTEMROOT", "COMSPEC", "PATHEXT", "USERPROFILE", "LOCALAPPDATA"}
	required := map[string]bool{"USERPROFILE": true, "LOCALAPPDATA": true}
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
			delete(required, key)
		}
	}
	if len(required) > 0 {
		t.Fatalf("Windows AppContainer test requires environment variables: %v", required)
	}
	return env
}

func TestWindowsBackendReportsCompleteIsolation(t *testing.T) {
	caps := ForPlatform().Capabilities()
	if !caps.WriteIsolation || !caps.ReadIsolation || !caps.ReadIsolationAlways || caps.NetworkIsolation != NetworkFull || !caps.ProcessIsolation {
		t.Fatalf("capabilities=%+v", caps)
	}
}

func TestProcessDeviceMapSetInformationUsesHandleSizedABI(t *testing.T) {
	got := unsafe.Sizeof(processDeviceMapSetInformation{})
	want := unsafe.Sizeof(windows.Handle(0))
	if got != want {
		t.Fatalf("ProcessDeviceMap set information size=%d, want native handle size %d", got, want)
	}
}

func TestDebugEventLayoutMatchesWindowsABI(t *testing.T) {
	wantInfoOffset := uintptr(12)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		wantInfoOffset = 16
	}
	if got := unsafe.Offsetof(debugEvent{}.Info); got != wantInfoOffset {
		t.Fatalf("DEBUG_EVENT union offset=%d, want %d", got, wantInfoOffset)
	}
	if got := unsafe.Offsetof(createProcessDebugInfo{}.Process); got != unsafe.Sizeof(windows.Handle(0)) {
		t.Fatalf("CREATE_PROCESS_DEBUG_INFO process offset=%d, want one native handle", got)
	}
	// EXCEPTION_DEBUG_INFO is the largest union member (160 bytes on the
	// supported 64-bit Windows targets), so DEBUG_EVENT must provide at least
	// 176 bytes including its aligned header.
	if unsafe.Sizeof(uintptr(0)) == 8 && unsafe.Sizeof(debugEvent{}) < 176 {
		t.Fatalf("DEBUG_EVENT size=%d, want at least 176", unsafe.Sizeof(debugEvent{}))
	}
}
