//go:build !linux

package main

import "errors"

func runLandlockShim([]string) error {
	return errors.New("__landlock is only used on Linux")
}
