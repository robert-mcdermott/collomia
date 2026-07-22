//go:build darwin

package sandbox

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("SEATBELT_NETWORK_HELPER") == "1" {
		tryTCP := func(address string) string {
			conn, _ := net.DialTimeout("tcp", address, 500*time.Millisecond)
			if conn != nil {
				_ = conn.Close()
				return "ok"
			}
			return "blocked"
		}
		tryUDP := func(address string) string {
			conn, err := net.DialTimeout("udp", address, 500*time.Millisecond)
			if err == nil {
				_, err = conn.Write([]byte("collomia network policy test"))
				_ = conn.Close()
			}
			if err == nil {
				return "ok"
			}
			return "blocked"
		}
		bind := "blocked"
		if listener, err := net.Listen("tcp4", "127.0.0.1:0"); err == nil {
			bind = "ok"
			_ = listener.Close()
		}
		fmt.Printf("loopback=%s bind=%s remote=%s\n", tryTCP(os.Getenv("SEATBELT_LOOPBACK")), bind, tryUDP(os.Getenv("SEATBELT_REMOTE")))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestDarwinProfileReadConfinementIsExplicit(t *testing.T) {
	workspace := t.TempDir()
	broad, err := Profile(Policy{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(broad, "(deny file-read-data") {
		t.Fatalf("broad-read policy unexpectedly denied reads:\n%s", broad)
	}

	confined, err := Profile(Policy{WorkspaceRoot: workspace, ConstrainReads: true, ExtraReadableRoots: []string{"/read-only-sdk"}})
	if err != nil {
		t.Fatal(err)
	}
	canonicalWorkspace, ok := normalizedRoot(workspace)
	if !ok {
		t.Fatal("could not normalize workspace")
	}
	for _, want := range []string{"(deny file-read-data", sbplString(canonicalWorkspace), sbplString("/read-only-sdk"), sbplString("/System")} {
		if !strings.Contains(confined, want) {
			t.Fatalf("confined profile missing %q:\n%s", want, confined)
		}
	}
}

func TestDarwinBackendReportsReadIsolation(t *testing.T) {
	if !ForPlatform().Capabilities().ReadIsolation {
		t.Fatal("Seatbelt backend must advertise its read-confinement support")
	}
}

func TestSeatbeltDeniesRemoteNetworkAndKeepsLoopback(t *testing.T) {
	backend := ForPlatform()
	if err := backend.Available(); err != nil {
		t.Skipf("Seatbelt unavailable: %v", err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	loopback := listener.Addr().String()
	remote := "192.0.2.1:9"
	// A connected UDP write needs no reply. Prove the host can issue it before
	// Seatbelt, so the child's failure measures policy rather than routing.
	baseline, err := net.DialTimeout("udp", remote, time.Second)
	if err != nil {
		t.Skipf("no route for non-loopback UDP fixture: %v", err)
	}
	if _, err := baseline.Write([]byte("collomia network policy baseline")); err != nil {
		_ = baseline.Close()
		t.Skipf("non-loopback UDP baseline unavailable: %v", err)
	}
	_ = baseline.Close()
	wrapped, err := backend.Wrap([]string{os.Args[0], "-test.run=IGNORED"}, Policy{WorkspaceRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(wrapped[0], wrapped[1:]...)
	cmd.Env = append(os.Environ(), "SEATBELT_NETWORK_HELPER=1", "SEATBELT_LOOPBACK="+loopback, "SEATBELT_REMOTE="+remote)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Seatbelt network helper failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "loopback=ok bind=ok remote=blocked" {
		t.Fatalf("Seatbelt network enforcement=%q", got)
	}
}
