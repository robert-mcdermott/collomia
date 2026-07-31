//go:build !windows

package setup

import (
	"os"
	"testing"
)

// assertOwnerOnly checks that the written configuration is readable only by
// its owner. `collo setup` never puts a secret in this file, but it does name
// endpoints, deployments, regions, and the environment variables a credential
// is read from, which is not material to widen to every local account.
func assertOwnerOnly(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat configuration: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("configuration must be owner-only, got %o", perm)
	}
}
