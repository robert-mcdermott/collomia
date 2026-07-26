//go:build darwin

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/egress"
	"github.com/robert-mcdermott/collomia/internal/sandbox"
)

// These fixtures exercise the brokered path natively rather than in isolation.
//
// They deliberately prove only half of the claim. That a sandboxed command
// cannot open a remote socket directly is proven by
// TestSeatbeltDeniesRemoteNetworkAndKeepsLoopback in internal/sandbox, which
// establishes that Seatbelt denies remote traffic while keeping loopback
// reachable. What is left to show is the other half: that through the broker
// an allowlisted destination is reachable from inside the sandbox and an
// unlisted one is refused. Together those two facts are the whole property —
// the broker is the only way out, and the broker filters.
//
// The destination server is on loopback, which is why these tests are not
// themselves evidence of remote denial and say so.
func TestScopedEgressReachesAllowedDestinationInsideSandbox(t *testing.T) {
	requireSeatbelt(t)
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "destination reached")
	}))
	defer destination.Close()

	out, err := runScoped(t, destination.URL, "127.0.0.1")
	if err != nil {
		t.Fatalf("allowed destination should be reachable through the broker: %v\n%s", err, out)
	}
	if !strings.Contains(out, "destination reached") {
		t.Fatalf("output %q does not contain the destination's response", out)
	}
}

func TestScopedEgressRefusesUnlistedDestinationInsideSandbox(t *testing.T) {
	requireSeatbelt(t)
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("an unlisted destination must never be reached")
	}))
	defer destination.Close()

	// The allowlist names a different host, so the destination is not covered.
	out, _ := runScoped(t, destination.URL, "proxy.golang.org")
	if strings.Contains(out, "destination reached") {
		t.Fatal("the command reached a destination no allow rule covers")
	}
	// The refusal has to be legible: the tool layer reports which host was
	// refused so a failing build names what it would need.
	if !strings.Contains(out, "scoped egress refused") || !strings.Contains(out, "127.0.0.1") {
		t.Fatalf("output does not report the refused host:\n%s", out)
	}
}

// AllowNetwork is deliberately true here: brokering must override it, because
// the OS-level denial is what makes the broker a boundary rather than a
// suggestion a command can decline.
func runScoped(t *testing.T, target, allowedHost string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is required for the native egress fixture")
	}
	command, err := NewRunCommandTool(t.TempDir(), nil, 64*1024)
	if err != nil {
		t.Fatal(err)
	}
	command.SandboxMode = sandbox.ModeAuto
	command.AllowNetwork = true
	command.AllowReadOutsideWorkspace = true
	command.EgressScoped = true
	command.EgressAllowlist = egress.NewAllowlist([]string{allowedHost})
	raw, _ := json.Marshal(map[string]any{
		"command":         "curl -sS --max-time 10 " + target,
		"timeout_seconds": 30,
	})
	return command.Execute(context.Background(), raw)
}

func requireSeatbelt(t *testing.T) {
	t.Helper()
	if err := sandbox.ForPlatform().Available(); err != nil {
		t.Skipf("Seatbelt unavailable: %v", err)
	}
}
