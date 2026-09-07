//go:build windows

package sandbox

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Distinguish executable lookup from access to the explicitly authorized SDK.
// Hosted Windows runners may put PATH on a C: junction to a D: tool cache.
// This uses the production AppContainer and minimal environment, without
// widening the policy or substituting an unsandboxed verification command.
func TestAppContainerGoSDKDiscovery(t *testing.T) {
	sdk := runtime.GOROOT()
	resolved, err := finalAppContainerPath(sdk)
	if err != nil {
		t.Fatal(err)
	}
	lookup, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Go SDK=%q resolved SDK=%q parent lookup=%q", sdk, resolved, lookup)
	for _, tc := range []struct {
		name        string
		argv        []string
		explicitSDK bool
	}{
		{"resolved SDK executable", []string{filepath.Join(resolved, "bin", "go.exe"), "version"}, true},
		{"shell PATH lookup", []string{"cmd.exe", "/d", "/s", "/c", "where.exe go & go version"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			argv, err := platformBackend().Wrap(tc.argv, Policy{WorkspaceRoot: workspace, ConstrainReads: true, ExtraReadableRoots: []string{sdk}})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
			cmd.Dir = workspace
			cmd.Env = windowsAppContainerTestEnv(t)
			if tc.explicitSDK {
				// This baseline tests access to the authorized SDK, independent
				// of a trimmed Go binary's root auto-discovery. The shell case
				// above still tests normal PATH lookup without this override.
				cmd.Env = append(cmd.Env, "GOROOT="+sdk)
			}
			out, err := cmd.CombinedOutput()
			t.Logf("sandbox output:\n%s", out)
			if err != nil || !strings.Contains(string(out), "go version go") {
				t.Fatalf("sandbox SDK discovery failed: %v", err)
			}
		})
	}
}
