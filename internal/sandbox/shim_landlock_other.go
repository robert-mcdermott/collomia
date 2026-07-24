//go:build !linux

package sandbox

import "errors"

func runLandlockShim([]string) error {
	return errors.New("__landlock is only used on Linux")
}
