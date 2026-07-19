//go:build linux

package webterminal

import (
	"errors"
	"os/exec"
)

func openBrowser(target string) error {
	for _, candidate := range [][]string{{"xdg-open", target}, {"gio", "open", target}} {
		if _, err := exec.LookPath(candidate[0]); err != nil {
			continue
		}
		cmd := exec.Command(candidate[0], candidate[1:]...)
		if err := cmd.Start(); err != nil {
			continue
		}
		go func() { _ = cmd.Wait() }()
		return nil
	}
	return errors.New("neither xdg-open nor gio is available")
}
