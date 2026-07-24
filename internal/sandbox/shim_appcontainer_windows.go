//go:build windows

package sandbox

import "errors"

// runAppContainerShim launches the requested command under the encoded
// AppContainer policy.
func runAppContainerShim(args []string) error {
	if len(args) < 3 || args[1] != "--" {
		return errors.New("usage: collo __appcontainer <policy> -- <command…>")
	}
	policy, err := DecodePolicy(args[0])
	if err != nil {
		return err
	}
	return RunAppContainer(policy, args[2:])
}
