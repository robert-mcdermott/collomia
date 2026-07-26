// Package credstore keeps provider API keys in the operating system's own
// credential manager instead of in a configuration file or a shell profile.
//
// Three properties shape everything here:
//
// It is optional. Environment variables and `api_key`/`api_key_env` keep
// working exactly as they did, and a user who never runs `collo auth set` is
// never affected by this package — not even by a keychain lookup, because the
// index below is consulted first and an absent index means no operating-system
// call at all. That matters beyond speed: on macOS a keychain read can raise a
// dialog, and a tool that raises one for a user who stores nothing is a tool
// people disable.
//
// It is not a secret file. Only the operating system holds the secret. The
// index this package maintains records provider *names* so entries can be
// listed and so a lookup can be skipped cheaply; it contains no credential
// material and is not a fallback store. There is deliberately no
// file-backed backend: an encrypted-file store would need a passphrase
// somewhere, and an unencrypted one would be worse than the environment
// variable it replaced.
//
// It never reveals. Values go in and are read back only to authenticate a
// provider request. Nothing here prints a stored secret, which is the same
// stance the credential-protection policy takes toward files on disk.
package credstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/userconfig"
)

// IndexName is the file recording which provider names have a stored
// credential. It holds names only; the secrets live in the OS keychain.
const IndexName = "credentials.json"

// ErrUnsupported is returned by mutating operations on a platform with no
// supported credential manager. Callers report it rather than falling back to
// a file, so the absence is visible instead of quietly less safe.
var ErrUnsupported = errors.New("no supported OS credential store on this platform")

// Backend names the operating system credential manager in use, or "" when
// the platform has none.
func Backend() string { return backendName }

// Available reports whether credentials can be stored on this platform.
func Available() bool { return backendName != "" }

// ValidateName rejects provider names that cannot be round-tripped safely
// through a platform credential manager. The leading-character rule is not
// cosmetic: the macOS backend passes the name as a command-line argument, and
// a name beginning with "-" would be read as an option.
func ValidateName(name string) error {
	if strings.TrimSpace(name) != name || name == "" {
		return errors.New("provider name must not be empty or padded with spaces")
	}
	if len(name) > 128 {
		return errors.New("provider name is too long for a credential entry (max 128 characters)")
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case (r == '-' || r == '_' || r == '.') && i > 0:
		default:
			return fmt.Errorf("provider name %q cannot be stored: use letters, digits, and -_. after the first character", name)
		}
	}
	return nil
}

// Get returns the stored credential for a provider. The index is consulted
// first so an unstored provider — the common case — costs one small file read
// and never touches the credential manager.
func Get(provider string) (string, bool, error) {
	if !Available() {
		return "", false, nil
	}
	if ValidateName(provider) != nil {
		return "", false, nil
	}
	indexed, err := List()
	if err != nil || !contains(indexed, provider) {
		return "", false, err
	}
	secret, found, err := backendGet(provider)
	if err != nil {
		return "", false, err
	}
	return secret, found, nil
}

// Set stores or replaces a provider's credential and records the name in the
// index.
func Set(provider, secret string) error {
	if !Available() {
		return ErrUnsupported
	}
	if err := ValidateName(provider); err != nil {
		return err
	}
	if strings.TrimSpace(secret) == "" {
		return errors.New("credential must not be empty")
	}
	if strings.ContainsAny(secret, "\x00\n\r") {
		return errors.New("credential must not contain newlines or NUL bytes")
	}
	if err := backendSet(provider, secret); err != nil {
		return err
	}
	return addToIndex(provider)
}

// Delete removes a provider's credential. It reports whether an entry
// existed, and clears the index entry even when the credential manager had
// already lost it, so an externally deleted item cannot linger in the listing.
func Delete(provider string) (bool, error) {
	if !Available() {
		return false, ErrUnsupported
	}
	if err := ValidateName(provider); err != nil {
		return false, err
	}
	existed, err := backendDelete(provider)
	if err != nil {
		return false, err
	}
	indexed, indexErr := List()
	if indexErr == nil && contains(indexed, provider) {
		existed = true
		if err := writeIndex(remove(indexed, provider)); err != nil {
			return existed, err
		}
	}
	return existed, nil
}

// List returns the provider names with a recorded credential, sorted. The
// listing is what the index claims; Verify reports whether the credential
// manager still agrees.
func List() ([]string, error) {
	path, err := IndexPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var file indexFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("%s is not readable as a credential index: %w", path, err)
	}
	names := append([]string(nil), file.Providers...)
	sort.Strings(names)
	return names, nil
}

// Verify reports whether the credential manager still holds an entry the
// index lists. An entry deleted through Keychain Access or Credential Manager
// stays in the index until something looks, and a listing that quietly implied
// a working credential would be worse than one that says the entry is gone.
func Verify(provider string) (bool, error) {
	if !Available() {
		return false, nil
	}
	_, found, err := backendGet(provider)
	return found, err
}

// IndexPath is the location of the name index.
func IndexPath() (string, error) { return userconfig.Path(IndexName) }

type indexFile struct {
	Version   int      `json:"version"`
	Providers []string `json:"providers"`
}

func addToIndex(provider string) error {
	names, err := List()
	if err != nil {
		return err
	}
	if contains(names, provider) {
		return nil
	}
	return writeIndex(append(names, provider))
}

// writeIndex replaces the index atomically. The file carries no secret, but it
// is written 0600 anyway: which providers a user has configured is not
// something to publish to every account on a shared machine.
func writeIndex(names []string) error {
	path, err := IndexPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	sort.Strings(names)
	if names == nil {
		names = []string{}
	}
	data, err := json.MarshalIndent(indexFile{Version: 1, Providers: names}, "", "  ")
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func remove(list []string, value string) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		if item != value {
			out = append(out, item)
		}
	}
	return out
}
