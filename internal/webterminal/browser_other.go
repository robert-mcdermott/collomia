//go:build !darwin && !linux && !windows

package webterminal

import "errors"

func openBrowser(string) error {
	return errors.New("automatic browser opening is not supported on this platform")
}
