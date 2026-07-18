//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain doubles as the sandboxed helper process: Landlock restricts the
// calling process, so the test re-executes itself, applies the policy in the
// child, and verifies enforcement there.
func TestMain(m *testing.M) {
	if os.Getenv("LANDLOCK_HELPER") == "1" {
		runHelper()
		return
	}
	os.Exit(m.Run())
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
	outside := filepath.Join(t.TempDir(), "escape.txt")
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
