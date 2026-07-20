//go:build !windows

package main

import "errors"

func runAppContainerShim([]string) error {
	return errors.New("__appcontainer is only used on Windows")
}
