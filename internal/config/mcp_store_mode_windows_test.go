//go:build windows

package config

import (
	"os"
	"testing"
)

func assertConfigFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config file: %v", err)
	}
	// Go maps only the owner-write bit to Windows' read-only attribute; the
	// remaining Unix permission bits are not meaningful on Windows. Access is
	// otherwise governed by the ACL inherited from the containing directory.
	gotWritable := info.Mode().Perm()&0o200 != 0
	wantWritable := want.Perm()&0o200 != 0
	if gotWritable != wantWritable {
		t.Fatalf("config file writable=%v (mode %v), want writable=%v", gotWritable, info.Mode().Perm(), wantWritable)
	}
}
