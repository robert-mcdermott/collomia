//go:build !windows

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
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("config file mode=%v, want %v", got, want)
	}
}
