//go:build windows

package main

import (
	"errors"

	"github.com/robert-mcdermott/collomia/internal/sandbox"
)

// runAppContainerShim implements the hidden
// `collo __appcontainer <policy> -- cmd…` Windows re-exec step.
func runAppContainerShim(args []string) error {
	if len(args) < 3 || args[1] != "--" {
		return errors.New("usage: collo __appcontainer <policy> -- <command…>")
	}
	policy, err := sandbox.DecodePolicy(args[0])
	if err != nil {
		return err
	}
	return sandbox.RunAppContainer(policy, args[2:])
}
