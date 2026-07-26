package credstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The index is the only part of this package that behaves identically on every
// platform, and it is the part that decides whether the operating system is
// consulted at all. These tests cover it directly; the platform backends are
// exercised by the integration test below, which skips where there is no store.

func TestValidateNameRejectsUnstorableNames(t *testing.T) {
	for _, name := range []string{"", " padded", "padded ", "-leading-dash", "has space", "quote\"", "semi;colon", strings.Repeat("x", 129)} {
		if err := ValidateName(name); err == nil {
			t.Errorf("ValidateName(%q) accepted a name that cannot be stored safely", name)
		}
	}
	for _, name := range []string{"openai", "work-azure", "local_ollama", "a.b.c", "x1"} {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", name, err)
		}
	}
}

// A leading dash would be read as an option by the macOS backend's command
// line. This is the specific reason the first character is restricted, so it
// gets its own test rather than living only in the table above.
func TestValidateNameRejectsOptionLookalike(t *testing.T) {
	if err := ValidateName("-w"); err == nil {
		t.Fatal("a name starting with - must be rejected: it would be read as a command-line option")
	}
}

func TestGetWithoutIndexNeverConsultsTheOperatingSystem(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// No index file exists, so this must resolve to "not found" without any
	// platform call. On macOS a platform call could raise a keychain dialog;
	// a user who has stored nothing must never see one.
	secret, found, err := Get("openai")
	if err != nil || found || secret != "" {
		t.Fatalf("Get with no index = (%q, %v, %v), want empty/false/nil", secret, found, err)
	}
}

func TestIndexRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if names, err := List(); err != nil || len(names) != 0 {
		t.Fatalf("List on a fresh home = (%v, %v), want empty", names, err)
	}
	if err := addToIndex("zeta"); err != nil {
		t.Fatal(err)
	}
	if err := addToIndex("alpha"); err != nil {
		t.Fatal(err)
	}
	if err := addToIndex("alpha"); err != nil {
		t.Fatal(err)
	}
	names, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "alpha" || names[1] != "zeta" {
		t.Fatalf("List = %v, want sorted [alpha zeta] with no duplicate", names)
	}
	path, err := IndexPath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeSupportsModes() && info.Mode().Perm() != 0o600 {
		t.Fatalf("index mode = %v, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The index must never become a place a secret could hide.
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "key") {
		t.Fatalf("index contains unexpected content: %s", data)
	}
}

func TestListRejectsACorruptIndexInsteadOfIgnoringIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path, err := IndexPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := List(); err == nil {
		t.Fatal("a corrupt index must be reported, not silently treated as empty")
	}
}

// TestStoreRoundTrip exercises the real platform backend against the user's
// real credential manager, so it runs only when explicitly asked for:
//
//	COLLO_CREDSTORE_TEST=1 go test ./internal/credstore/
//
// Two reasons it cannot be part of an ordinary run. It writes to the login
// keychain or Credential Manager of whoever runs the suite, which a unit test
// has no business doing. And it must not redirect HOME to isolate the index,
// because /usr/bin/security resolves the login keychain through HOME: pointing
// it at an empty directory makes macOS report that no keychain exists and
// offer to create one, which is an alarming dialog to raise from `go test`.
func TestStoreRoundTrip(t *testing.T) {
	if os.Getenv("COLLO_CREDSTORE_TEST") == "" {
		t.Skip("set COLLO_CREDSTORE_TEST=1 to exercise the real OS credential store")
	}
	if !Available() {
		t.Skipf("no credential store on this platform")
	}

	const name = "collomia-selftest-delete-me"
	const secret = "sk-test-value-0123456789"
	t.Cleanup(func() { _, _ = Delete(name) })

	if err := Set(name, secret); err != nil {
		t.Skipf("credential store is unavailable in this environment: %v", err)
	}
	got, found, err := Get(name)
	if err != nil || !found {
		t.Fatalf("Get after Set = (%q, %v, %v), want the stored secret", got, found, err)
	}
	if got != secret {
		t.Fatalf("Get = %q, want %q", got, secret)
	}
	if names, err := List(); err != nil || !contains(names, name) {
		t.Fatalf("List after Set = (%v, %v), want the stored name", names, err)
	}

	// Replacing must not create a second entry.
	if err := Set(name, secret+"-updated"); err != nil {
		t.Fatal(err)
	}
	got, _, err = Get(name)
	if err != nil || got != secret+"-updated" {
		t.Fatalf("Get after replace = (%q, %v), want the updated secret", got, err)
	}

	existed, err := Delete(name)
	if err != nil || !existed {
		t.Fatalf("Delete = (%v, %v), want (true, nil)", existed, err)
	}
	if _, found, _ := Get(name); found {
		t.Fatal("Get found a credential after Delete")
	}
	if names, _ := List(); contains(names, name) {
		t.Fatal("the index still lists a deleted credential")
	}
}

// Validation runs before any platform call, so this test never reaches the
// credential manager and never redirects HOME.
func TestSetRejectsEmptyAndMultilineSecrets(t *testing.T) {
	if !Available() {
		t.Skipf("no credential store on this platform")
	}
	if err := Set("collomia-selftest", "   "); err == nil {
		t.Error("an empty credential must be rejected")
	}
	if err := Set("collomia-selftest", "line\nline"); err == nil {
		t.Error("a multi-line credential must be rejected")
	}
}

func runtimeSupportsModes() bool { return os.PathSeparator == '/' }
