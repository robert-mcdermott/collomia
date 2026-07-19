//go:build darwin

package webterminal

import "os/exec"

func openBrowser(target string) error {
	cmd := exec.Command("open", target)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
