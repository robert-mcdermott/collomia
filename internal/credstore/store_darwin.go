//go:build darwin

package credstore

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// backendName is what the user is told holds their credential.
const backendName = "macOS Keychain"

// service is the keychain service every Collomia entry shares; the account is
// the provider name.
const service = "collomia"

// securityPath is Apple's keychain command-line tool. Collomia drives it
// rather than linking Security.framework for two reasons: linking would
// require cgo and give up the single self-contained binary, and the keychain
// grants access per application identity — an unsigned build would be a new
// identity on every rebuild, so the user would re-authorize each upgrade. The
// trusted caller here is Apple's own signed binary, which is stable.
const securityPath = "/usr/bin/security"

// itemNotFound is the exit status security(1) uses for a missing item.
const itemNotFound = 44

func backendGet(account string) (string, bool, error) {
	cmd := exec.Command(securityPath, "find-generic-password", "-s", service, "-a", account, "-w")
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if code, ok := exitCode(err); ok && code == itemNotFound {
			return "", false, nil
		}
		return "", false, securityError("read", account, stderr.String(), err)
	}
	// -w prints the password followed by a newline.
	return strings.TrimRight(out.String(), "\r\n"), true, nil
}

// backendSet writes the entry with -U so an existing one is replaced.
//
// The secret is passed as a command-line argument, which is how security(1)
// accepts it — it has no option to read a password from standard input. On
// macOS another user cannot read this process's arguments (the kernel
// restricts them to the owning user and root), so the exposure is to root and
// to the user's own session, which already holds the unlocked keychain. This
// is stated in the security documentation rather than left for a reader to
// discover.
func backendSet(account, secret string) error {
	cmd := exec.Command(securityPath, "add-generic-password", "-U", "-s", service, "-a", account,
		"-l", "Collomia: "+account, "-D", "Collomia provider credential", "-w", secret)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return securityError("store", account, stderr.String(), err)
	}
	return nil
}

func backendDelete(account string) (bool, error) {
	cmd := exec.Command(securityPath, "delete-generic-password", "-s", service, "-a", account)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if code, ok := exitCode(err); ok && code == itemNotFound {
			return false, nil
		}
		return false, securityError("delete", account, stderr.String(), err)
	}
	return true, nil
}

func exitCode(err error) (int, bool) {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), true
	}
	return 0, false
}

func securityError(verb, account, stderr string, err error) error {
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = err.Error()
	}
	// The keychain can only ask permission where there is a window server to
	// ask through. Over SSH, in a launch daemon, or in CI the request comes
	// back as a cancellation, which reads as a mysterious refusal unless the
	// cause is named.
	if strings.Contains(detail, "authorization was canceled") || strings.Contains(detail, "User interaction is not allowed") {
		return fmt.Errorf("could not %s the keychain entry for %q: the keychain could not prompt for access (%s). "+
			"This happens in a session with no graphical login — over SSH or in CI. Unlock the keychain in a desktop session, or use api_key_env there", verb, account, detail)
	}
	return fmt.Errorf("could not %s the keychain entry for %q: %s", verb, account, detail)
}
