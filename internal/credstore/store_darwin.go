//go:build darwin

package credstore

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// keychainFile names an explicit keychain for security(1) to operate on.
//
// It is empty in every shipping path: Collomia uses the user's default
// keychain, which is what "stored in the macOS Keychain" means to the person
// who ran `collo auth set`. It exists so this package's own tests can run
// against a keychain they create and destroy, and the seam is here rather than
// in the tests because the alternative was worse in a way that took a real
// scare to discover.
//
// Redirecting HOME is the obvious way to isolate a test, and it is wrong here.
// macOS resolves the login keychain through $HOME/Library/Keychains, so a test
// that moved HOME left security(1) with no keychain to write to — which it
// reports not as an error but as a modal dialog offering to **reset the
// keychain to defaults**. An isolation measure that puts a destructive button
// in front of the user is not isolation. Naming the keychain explicitly avoids
// the default-resolution path entirely, so HOME can be redirected for the
// index without touching how the keychain is found.
var keychainFile string

// withKeychain appends the explicit keychain, which security(1) takes as a
// trailing operand on all three of these subcommands.
func withKeychain(args []string) []string {
	if keychainFile == "" {
		return args
	}
	return append(args, keychainFile)
}

// errNoKeychainDirectory reports an environment in which security(1) cannot
// resolve a keychain, checked before it is asked to.
//
// This is not a theoretical guard. When security(1) is invoked to store an item
// and no keychain resolves, it does not fail — it raises a modal dialog reading
// "A keychain cannot be found to store …" whose two buttons are Cancel and
// **Reset To Defaults**. A user who does not recognize that dialog is one click
// from resetting their keychain, and Collomia raised it. That happened here in a
// test with a redirected HOME, but a test is not the only way to get there: a
// launchd job, a container, `sudo -H`, or any context with a HOME that has no
// Library/Keychains reaches the same place through `collo auth set`.
//
// The check is the directory, not a specific file, on purpose. A user may set a
// custom default keychain living anywhere, so testing for login.keychain-db
// would refuse working configurations; every real macOS account has the
// directory, and its absence is the precise signal that HOME is not a home.
func ensureKeychainResolvable() error {
	if keychainFile != "" {
		// An explicitly named keychain does not go through default resolution,
		// so there is nothing to raise a dialog about.
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot use the macOS Keychain: no home directory is set (%w). "+
			"Use api_key_env in this environment", err)
	}
	dir := filepath.Join(home, "Library", "Keychains")
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("cannot use the macOS Keychain: %s does not exist, so no keychain can be resolved for HOME=%s. "+
			"This happens in a service, container, or sudo context rather than a desktop login session. Use api_key_env there", dir, home)
	}
	return nil
}

func backendGet(account string) (string, bool, error) {
	if err := ensureKeychainResolvable(); err != nil {
		return "", false, err
	}
	cmd := exec.Command(securityPath, withKeychain([]string{"find-generic-password", "-s", service, "-a", account, "-w"})...)
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
	if err := ensureKeychainResolvable(); err != nil {
		return err
	}
	cmd := exec.Command(securityPath, withKeychain([]string{"add-generic-password", "-U", "-s", service, "-a", account,
		"-l", "Collomia: " + account, "-D", "Collomia provider credential", "-w", secret})...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return securityError("store", account, stderr.String(), err)
	}
	return nil
}

func backendDelete(account string) (bool, error) {
	if err := ensureKeychainResolvable(); err != nil {
		return false, err
	}
	cmd := exec.Command(securityPath, withKeychain([]string{"delete-generic-password", "-s", service, "-a", account})...)
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
