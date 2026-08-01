//go:build !windows

package reliability

import (
	"syscall"
	"testing"
)

// freeBytes reports the space still available at dir.
//
// The tests size their writes against this rather than against a constant.
// A fixed "surely too big" size is a guess about a filesystem the test did not
// create, and the first version of these tests guessed wrong in both
// directions: 512 KiB was far more than a completely full disk would accept
// (so the interesting code never ran) and comfortably less than the slack left
// after a partial fill (so the write simply succeeded and the test skipped).
// Asking the filesystem removes the guess.
func freeBytes(t *testing.T, dir string) uint64 {
	t.Helper()
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		t.Fatalf("statfs %s: %v", dir, err)
	}
	return stat.Bavail * uint64(stat.Bsize)
}
