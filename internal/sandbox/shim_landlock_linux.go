//go:build linux

package sandbox

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// runLandlockShim applies the encoded Landlock policy to this process and
// replaces it with the requested command.
func runLandlockShim(args []string) error {
	if len(args) < 3 || args[1] != "--" {
		return errors.New("usage: collo __landlock <policy> -- <command…>")
	}
	policy, err := DecodePolicy(args[0])
	if err != nil {
		return err
	}
	if err := ApplyLandlock(policy); err != nil {
		return err
	}
	argv := args[2:]
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(path, argv, os.Environ())
}
