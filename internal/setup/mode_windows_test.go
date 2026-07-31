//go:build windows

package setup

import (
	"os"
	"testing"
)

// assertOwnerOnly is deliberately a different assertion on Windows.
//
// Go maps only the owner-write bit onto Windows' read-only attribute; the
// remaining Unix permission bits are not meaningful there, so `os.WriteFile`
// with 0600 produces a file that reports 0666 and the Unix assertion fails on a
// correctly written file. Access is governed by the ACL inherited from the
// containing directory instead. What is still worth asserting is that the file
// was created writable rather than read-only, since setup must be re-runnable.
//
// This mirrors internal/config's assertConfigFileMode, which exists for the
// same reason and protects the same artifact.
func assertOwnerOnly(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat configuration: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o200 == 0 {
		t.Errorf("configuration must remain writable so setup can be re-run, got %o", perm)
	}
}
