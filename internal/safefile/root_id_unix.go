//go:build darwin || linux

package safefile

import (
	"fmt"
	"os"
	"syscall"
)

// PersistentRootID binds a durable checkpoint to the directory, not its name.
func PersistentRootID(path string) (string, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("directory identity unavailable")
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}
