//go:build linux

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"github.com/robert-mcdermott/collomia/internal/sandbox"
)

// runLandlockShim implements the hidden `collo __landlock <policy> -- cmd…`
// re-exec step: it applies the Landlock ruleset to this process (which is
// irreversible) and then replaces itself with the target command.
func runLandlockShim(args []string) error {
	if len(args) < 3 || args[1] != "--" {
		return errors.New("usage: collo __landlock <policy> -- <command…>")
	}
	policy, err := sandbox.DecodePolicy(args[0])
	if err != nil {
		return err
	}
	if err := sandbox.ApplyLandlock(policy); err != nil {
		return err
	}
	argv := args[2:]
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(path, argv, os.Environ())
}
