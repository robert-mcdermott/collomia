//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestMain doubles as the sandboxed helper process: Landlock restricts the
// calling process, so the test re-executes itself, applies the policy in the
// child, and verifies enforcement there.
func TestMain(m *testing.M) {
	if os.Getenv("LANDLOCK_HELPER") == "1" {
		runHelper()
		return
	}
	if os.Getenv("LANDLOCK_READ_HELPER") == "1" {
		runReadHelper()
		return
	}
	if os.Getenv("LANDLOCK_NETWORK_HELPER") == "1" {
		runNetworkHelper()
		return
	}
	os.Exit(m.Run())
}

func runNetworkHelper() {
	if err := ApplyLandlock(Policy{WorkspaceRoot: os.Getenv("LANDLOCK_WS")}); err != nil {
		fmt.Println("apply:", err)
		os.Exit(2)
	}
	try := func(network string) string {
		conn, err := net.DialTimeout(network, "127.0.0.1:9", 250*time.Millisecond)
		if conn != nil {
			_ = conn.Close()
		}
		if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) {
			return "denied"
		}
		return "available"
	}
	fmt.Printf("tcp=%s udp=%s\n", try("tcp"), try("udp"))
	os.Exit(0)
}

func runReadHelper() {
	policy := Policy{WorkspaceRoot: os.Getenv("LANDLOCK_WS"), ConstrainReads: true}
	if extra := os.Getenv("LANDLOCK_READ_EXTRA"); extra != "" {
		policy.ExtraReadableRoots = []string{extra}
	}
	if err := ApplyLandlock(policy); err != nil {
		fmt.Println("apply:", err)
		os.Exit(2)
	}
	inside := "denied"
	if data, err := os.ReadFile(os.Getenv("LANDLOCK_READ_INSIDE")); err == nil && string(data) == "inside-value" {
		inside = "ok"
	}
	outside := "denied"
	if data, err := os.ReadFile(os.Getenv("LANDLOCK_READ_OUTSIDE")); err == nil && string(data) == "outside-secret" {
		outside = "ok"
	}
	fmt.Printf("inside=%s outside=%s\n", inside, outside)
	os.Exit(0)
}

func runHelper() {
	policy := Policy{WorkspaceRoot: os.Getenv("LANDLOCK_WS")}
	if err := ApplyLandlock(policy); err != nil {
		fmt.Println("apply:", err)
		os.Exit(2)
	}
	inside := "denied"
	if os.WriteFile(filepath.Join(policy.WorkspaceRoot, "inside.txt"), []byte("ok"), 0o644) == nil {
		inside = "ok"
	}
	outside := "denied"
	if os.WriteFile(os.Getenv("LANDLOCK_OUT"), []byte("pwned"), 0o644) == nil {
		outside = "ok"
	}
	fmt.Printf("inside=%s outside=%s\n", inside, outside)
	os.Exit(0)
}

func TestLandlockConfinesWrites(t *testing.T) {
	backend := ForPlatform()
	if err := backend.Available(); err != nil {
		t.Skipf("landlock unavailable: %v", err)
	}
	workspace := t.TempDir()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	outsideDir, err := os.MkdirTemp(home, ".collomia-landlock-outside-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outsideDir) })
	outside := filepath.Join(outsideDir, "escape.txt")
	cmd := exec.Command(os.Args[0], "-test.run=IGNORED")
	cmd.Env = append(os.Environ(), "LANDLOCK_HELPER=1", "LANDLOCK_WS="+workspace, "LANDLOCK_OUT="+outside)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper failed: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if got != "inside=ok outside=denied" {
		t.Fatalf("enforcement mismatch: %s", got)
	}
	if _, statErr := os.Stat(outside); statErr == nil {
		t.Fatal("outside file exists despite landlock")
	}
}

func TestLandlockConfinesReadsAndHonorsExplicitGrant(t *testing.T) {
	backend := ForPlatform()
	if err := backend.Available(); err != nil {
		t.Skipf("landlock unavailable: %v", err)
	}
	workspace := t.TempDir()
	inside := filepath.Join(workspace, "inside.txt")
	if err := os.WriteFile(inside, []byte("inside-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	outsideDir, err := os.MkdirTemp(home, ".collomia-landlock-read-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outsideDir) })
	outside := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outside, []byte("outside-secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(extra string) string {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=IGNORED")
		cmd.Env = append(os.Environ(),
			"LANDLOCK_READ_HELPER=1",
			"LANDLOCK_WS="+workspace,
			"LANDLOCK_READ_INSIDE="+inside,
			"LANDLOCK_READ_OUTSIDE="+outside,
			"LANDLOCK_READ_EXTRA="+extra,
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("helper failed: %v\n%s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if got := run(""); got != "inside=ok outside=denied" {
		t.Fatalf("read enforcement mismatch: %s", got)
	}
	if got := run(outsideDir); got != "inside=ok outside=ok" {
		t.Fatalf("explicit readable grant mismatch: %s", got)
	}
}

func TestWrapUsesShim(t *testing.T) {
	backend := ForPlatform()
	if err := backend.Available(); err != nil {
		t.Skipf("landlock unavailable: %v", err)
	}
	argv, err := backend.Wrap([]string{"/bin/sh", "-c", "true"}, Policy{WorkspaceRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(argv) < 5 || argv[1] != "__landlock" || argv[3] != "--" {
		t.Fatalf("argv=%v", argv)
	}
	policy, err := DecodePolicy(argv[2])
	if err != nil || policy.WorkspaceRoot == "" {
		t.Fatalf("policy roundtrip failed: %+v %v", policy, err)
	}
}

func TestLinuxNetworkCapabilityTracksLandlockABI(t *testing.T) {
	abi := landlockABI()
	got := ForPlatform().Capabilities().NetworkIsolation
	switch {
	case abi >= 10 && got != NetworkFull:
		t.Fatalf("ABI %d network isolation=%s, want full", abi, got)
	case abi >= 4 && abi < 10 && got != NetworkTCP:
		t.Fatalf("ABI %d network isolation=%s, want tcp", abi, got)
	case abi < 4 && got != NetworkNone:
		t.Fatalf("ABI %d network isolation=%s, want none", abi, got)
	}
}

func TestLandlockNetworkAccessByABI(t *testing.T) {
	tcp := uint64(unix.LANDLOCK_ACCESS_NET_BIND_TCP | unix.LANDLOCK_ACCESS_NET_CONNECT_TCP)
	for _, tc := range []struct {
		abi       int
		isolation NetworkIsolation
		want      uint64
	}{
		{3, NetworkNone, 0},
		{4, NetworkTCP, tcp},
		{9, NetworkTCP, tcp},
		{10, NetworkFull, tcp | landlockAccessNetBindUDP | landlockAccessNetConnectSendUDP},
	} {
		if got := landlockNetworkIsolation(tc.abi); got != tc.isolation {
			t.Errorf("ABI %d isolation=%s, want %s", tc.abi, got, tc.isolation)
		}
		if got := landlockHandledNetwork(tc.abi, false); got != tc.want {
			t.Errorf("ABI %d handled network=%#x, want %#x", tc.abi, got, tc.want)
		}
		if got := landlockHandledNetwork(tc.abi, true); got != 0 {
			t.Errorf("ABI %d allowed network mask=%#x, want zero", tc.abi, got)
		}
	}
}

func TestLandlockNetworkEnforcementMatchesABI(t *testing.T) {
	abi := landlockABI()
	if abi < 4 {
		t.Skipf("Landlock ABI %d has no network rights", abi)
	}
	cmd := exec.Command(os.Args[0], "-test.run=IGNORED")
	cmd.Env = append(os.Environ(), "LANDLOCK_NETWORK_HELPER=1", "LANDLOCK_WS="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("network helper failed: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	want := "tcp=denied udp=available"
	if abi >= 10 {
		want = "tcp=denied udp=denied"
	}
	if got != want {
		t.Fatalf("ABI %d network enforcement=%q, want %q", abi, got, want)
	}
}
