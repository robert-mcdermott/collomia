//go:build darwin

package credstore

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Live macOS Keychain coverage.
//
// The rest of this package's tests exercise the index, the validation, and the
// precedence rules — everything except the part that actually holds the
// secret. `backendGet`, `backendSet`, and `backendDelete` read 0.0%, which
// means `collo auth set` and `collo auth rm` have never been executed by a
// test on the one platform that can run them.
//
// Skipped unless explicitly requested, because unlike every other test here it
// touches a resource shared with the developer's own account:
//
//	COLLO_KEYCHAIN_TESTS=1 go test ./internal/credstore -run Keychain -v
//
// # Why this cannot reach a real credential
//
// Three independent protections, any one of which would be sufficient:
//
//  1. **A keychain of its own.** Each test creates a keychain file in its
//     temporary directory and points the backend at it explicitly, so
//     security(1) never resolves a default. The login keychain is not named,
//     is not in the search list for these operations, and is not modified. The
//     file is deleted when the test ends.
//
//  2. **The index is redirected.** Every index operation resolves through
//     userconfig.Path → os.UserHomeDir → HOME, so isolating HOME points
//     List/Set/Delete at a temporary credentials.json.
//     TestKeychainLeavesTheRealCredentialIndexUntouched asserts the real
//     ~/.collomia/credentials.json is byte-identical afterwards.
//
//  3. **Account names cannot collide.** security(1) is invoked as
//     `-s collomia -a <account>`, and delete-generic-password removes only an
//     item matching *both*; it has no wildcard and no bulk form. Every account
//     here carries testAccountPrefix and 8 bytes of randomness, and
//     testAccount refuses to return a name without that prefix — so a later
//     edit cannot accidentally name a real provider either.
//
// # Why the first protection exists
//
// The obvious isolation is HOME alone, and it is actively unsafe. macOS
// resolves the login keychain through $HOME/Library/Keychains, so with HOME
// moved, security(1) found no keychain to write to and raised a modal dialog —
// "A keychain cannot be found to store …" — whose buttons were Cancel and
// **Reset To Defaults**. No credential was ever at risk of being read, but a
// test run that offers to reset the user's keychain is a worse outcome than
// the missing coverage it was closing. Naming the keychain explicitly removes
// the default-resolution path, and with it the dialog.
const keychainTestsEnv = "COLLO_KEYCHAIN_TESTS"

// testAccountPrefix marks every keychain entry these tests may touch.
const testAccountPrefix = "collo-selftest-"

func requireKeychainTests(t *testing.T) {
	t.Helper()
	if os.Getenv(keychainTestsEnv) != "1" {
		t.Skip("set COLLO_KEYCHAIN_TESTS=1 to exercise the real macOS Keychain")
	}
	if _, err := os.Stat(securityPath); err != nil {
		t.Skipf("%s is not present: %v", securityPath, err)
	}
}

// testAccount returns a keychain account name that provably names no real
// provider, and registers its removal.
//
// The prefix check is not decoration. It is the invariant that makes these
// tests safe to run on a machine holding live credentials, so it is enforced
// in code rather than left to whoever edits the file next.
func testAccount(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("generating a unique account name: %v", err)
	}
	account := fmt.Sprintf("%s%d-%s", testAccountPrefix, os.Getpid(), hex.EncodeToString(buf))
	assertSafeAccount(t, account)
	t.Cleanup(func() {
		assertSafeAccount(t, account)
		if _, err := backendDelete(account); err != nil {
			t.Errorf("cleaning up keychain entry %q: %v", account, err)
		}
	})
	return account
}

func assertSafeAccount(t *testing.T, account string) {
	t.Helper()
	if !strings.HasPrefix(account, testAccountPrefix) {
		t.Fatalf("refusing to touch keychain account %q: these tests may only use names beginning %q", account, testAccountPrefix)
	}
}

// sandbox gives one test its own keychain and its own credential index, and
// returns the real index's contents so a test can prove it was left alone.
//
// Order matters: the keychain is created before HOME moves, because
// create-keychain on a path under a temporary HOME is fine but reading the
// user's real environment is not something the rest of the test should depend
// on. Both are torn down by t.Cleanup in reverse.
func sandbox(t *testing.T) []byte {
	t.Helper()
	useTemporaryKeychain(t)
	return isolateIndex(t)
}

// useTemporaryKeychain creates a keychain for this test alone and points the
// backend at it. It is never added to the search list, so nothing outside this
// process can see it and the user's default keychain is untouched.
func useTemporaryKeychain(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "collo-selftest.keychain")
	out, err := exec.Command(securityPath, "create-keychain", "-p", "collo-selftest", path).CombinedOutput()
	if err != nil {
		t.Skipf("cannot create a temporary keychain (%v): %s", err, out)
	}
	previous := keychainFile
	keychainFile = path
	t.Cleanup(func() {
		keychainFile = previous
		if out, err := exec.Command(securityPath, "delete-keychain", path).CombinedOutput(); err != nil {
			t.Errorf("deleting the temporary keychain %s: %v\n%s", path, err, out)
		}
	})

	// The login keychain must not have been disturbed by any of this. A test
	// that quietly changed the search list would be a far worse defect than
	// the one it exists to prevent.
	list, err := exec.Command(securityPath, "list-keychains").Output()
	if err == nil && strings.Contains(string(list), path) {
		t.Fatalf("the temporary keychain entered the search list; refusing to continue:\n%s", list)
	}
}

// isolateIndex points the credential index at a temporary directory and
// returns the real index's contents so a test can prove it was left alone.
func isolateIndex(t *testing.T) []byte {
	t.Helper()
	realPath, err := IndexPath()
	if err != nil {
		t.Fatalf("resolving the real index path: %v", err)
	}
	before, err := os.ReadFile(realPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reading the real index: %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	isolated, err := IndexPath()
	if err != nil {
		t.Fatalf("resolving the isolated index path: %v", err)
	}
	if isolated == realPath {
		t.Fatal("HOME isolation did not move the credential index; refusing to run against the real one")
	}
	if filepath.Dir(isolated) == filepath.Dir(realPath) {
		t.Fatalf("isolated index %q shares a directory with the real one %q", isolated, realPath)
	}
	return before
}

func TestKeychainStoresReadsAndDeletesACredential(t *testing.T) {
	requireKeychainTests(t)
	sandbox(t)
	account := testAccount(t)
	const secret = "sk-selftest-value-0123456789"

	// Nothing is there to begin with, and asking must not be an error.
	if _, found, err := backendGet(account); err != nil || found {
		t.Fatalf("backendGet on a fresh account = (found %v, err %v), want (false, nil)", found, err)
	}

	if err := backendSet(account, secret); err != nil {
		t.Fatalf("backendSet: %v", err)
	}
	got, found, err := backendGet(account)
	if err != nil {
		t.Fatalf("backendGet: %v", err)
	}
	if !found {
		t.Fatal("a credential that was just stored must be found")
	}
	if got != secret {
		// The -w output is newline-terminated; a backend that forgot to trim
		// would authenticate with a credential carrying a trailing \n, which
		// providers reject with an opaque 401.
		t.Errorf("read back %q, want %q", got, secret)
	}

	existed, err := backendDelete(account)
	if err != nil {
		t.Fatalf("backendDelete: %v", err)
	}
	if !existed {
		t.Error("deleting a stored credential must report that it existed")
	}
	if _, found, err = backendGet(account); err != nil || found {
		t.Errorf("after deletion backendGet = (found %v, err %v), want (false, nil)", found, err)
	}
}

func TestKeychainReplacesRatherThanDuplicatingAnEntry(t *testing.T) {
	// backendSet passes -U. Without it security(1) fails on an existing item,
	// which would make `collo auth set` a one-shot operation and rotating a key
	// impossible without deleting first.
	requireKeychainTests(t)
	sandbox(t)
	account := testAccount(t)

	if err := backendSet(account, "first-value"); err != nil {
		t.Fatalf("first backendSet: %v", err)
	}
	if err := backendSet(account, "second-value"); err != nil {
		t.Fatalf("replacing an existing entry: %v", err)
	}
	got, found, err := backendGet(account)
	if err != nil || !found {
		t.Fatalf("backendGet = (%q, %v, %v)", got, found, err)
	}
	if got != "second-value" {
		t.Errorf("read back %q, want the replacement", got)
	}
	// One delete must remove it completely. If -U had created a second item,
	// the first would survive and keep authenticating with a stale key.
	if _, err := backendDelete(account); err != nil {
		t.Fatalf("backendDelete: %v", err)
	}
	if _, found, _ = backendGet(account); found {
		t.Error("a replaced entry left a duplicate behind; the old credential is still live")
	}
}

func TestKeychainDeleteOfAMissingEntryIsNotAnError(t *testing.T) {
	// security(1) exits 44 for a missing item. Treating that as a failure would
	// make `collo auth rm` report an error for the idempotent case, and would
	// stop Delete from clearing a stale index entry.
	requireKeychainTests(t)
	sandbox(t)
	account := testAccount(t)

	existed, err := backendDelete(account)
	if err != nil {
		t.Fatalf("deleting a missing entry must not error: %v", err)
	}
	if existed {
		t.Error("deleting a missing entry must report that it did not exist")
	}
}

func TestKeychainRoundTripsThroughThePublicAPI(t *testing.T) {
	// The backend tests above call security(1) directly. This one goes through
	// Set/Get/Verify/List/Delete, which is what `collo auth` actually invokes,
	// and proves the index and the keychain stay in step.
	requireKeychainTests(t)
	sandbox(t)
	account := testAccount(t)
	const secret = "sk-selftest-public-api"

	if err := Set(account, secret); err != nil {
		t.Fatalf("Set: %v", err)
	}
	names, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !contains(names, account) {
		t.Errorf("List = %v, want it to include the stored provider", names)
	}
	got, found, err := Get(account)
	if err != nil || !found || got != secret {
		t.Errorf("Get = (%q, %v, %v), want the stored secret", got, found, err)
	}
	present, err := Verify(account)
	if err != nil || !present {
		t.Errorf("Verify = (%v, %v), want (true, nil)", present, err)
	}

	existed, err := Delete(account)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !existed {
		t.Error("Delete must report that the entry existed")
	}
	if names, err = List(); err != nil || contains(names, account) {
		t.Errorf("after Delete, List = %v (err %v); the index must not keep a removed provider", names, err)
	}
	if present, _ = Verify(account); present {
		t.Error("Verify must report a deleted credential as absent")
	}
}

func TestKeychainDeleteClearsAnIndexEntryTheKeychainNoLongerHas(t *testing.T) {
	// The documented case: a user removed the item in Keychain Access. The
	// index still lists it, and a listing implying a working credential is
	// worse than one that says it is gone.
	requireKeychainTests(t)
	sandbox(t)
	account := testAccount(t)

	if err := Set(account, "sk-selftest-orphan"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Remove it behind the package's back, as Keychain Access would.
	if _, err := backendDelete(account); err != nil {
		t.Fatalf("backendDelete: %v", err)
	}

	existed, err := Delete(account)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !existed {
		t.Error("Delete must report the stale index entry as having existed")
	}
	names, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if contains(names, account) {
		t.Errorf("List = %v, want the orphaned entry cleared", names)
	}
}

func TestKeychainVerifyDistinguishesAbsentFromFailed(t *testing.T) {
	requireKeychainTests(t)
	sandbox(t)
	account := testAccount(t)

	present, err := Verify(account)
	if err != nil {
		t.Fatalf("Verify on a missing entry must not error: %v", err)
	}
	if present {
		t.Error("Verify on a missing entry must report absent")
	}
}

func TestKeychainLeavesTheRealCredentialIndexUntouched(t *testing.T) {
	// The belt to the braces above. Every other test in this file isolates
	// HOME; this one records the real index, exercises the full write path,
	// and proves the real file is byte-identical afterwards.
	requireKeychainTests(t)
	realPath, err := IndexPath()
	if err != nil {
		t.Fatalf("resolving the real index path: %v", err)
	}
	before := sandbox(t)

	account := testAccount(t)
	if err := Set(account, "sk-selftest-isolation"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := Delete(account); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	after, err := os.ReadFile(realPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("re-reading the real index: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("the real credential index at %s changed:\n before: %s\n after:  %s", realPath, before, after)
	}
}

func TestKeychainTestAccountsAreRejectedWithoutTheGuardPrefix(t *testing.T) {
	// The safety invariant itself, checked rather than trusted. If someone
	// later replaces testAccount with a fixed name, this states what breaks.
	requireKeychainTests(t)
	if !strings.HasPrefix(testAccount(t), testAccountPrefix) {
		t.Fatal("testAccount must always return a guarded name")
	}
}

func TestKeychainErrorNamesTheAccountAndTheCause(t *testing.T) {
	// securityError builds the message a user sees when the keychain refuses.
	// The headless case in particular has to name itself, because "authorization
	// was canceled" over SSH reads as a Collomia bug.
	err := securityError("read", "openrouter", "User interaction is not allowed.", errors.New("exit status 36"))
	message := err.Error()
	for _, want := range []string{"openrouter", "no graphical login", "api_key_env"} {
		if !strings.Contains(message, want) {
			t.Errorf("headless keychain error must mention %q: %s", want, message)
		}
	}
	plain := securityError("store", "bedrock", "", errors.New("exit status 1")).Error()
	if !strings.Contains(plain, "bedrock") || !strings.Contains(plain, "exit status 1") {
		t.Errorf("a plain failure must carry the account and the cause: %s", plain)
	}
}

func TestExitCodeReadsAnExitStatusAndIgnoresOtherErrors(t *testing.T) {
	// itemNotFound detection depends on this. A non-ExitError — security(1)
	// missing, or the context cancelled — must not be read as some exit code.
	cmd := exec.Command(securityPath, "not-a-real-subcommand")
	runErr := cmd.Run()
	if runErr == nil {
		t.Skip("security(1) accepted an invalid subcommand")
	}
	if code, ok := exitCode(runErr); !ok || code == 0 {
		t.Errorf("exitCode = (%d, %v), want a non-zero status", code, ok)
	}
	if _, ok := exitCode(errors.New("not an exit error")); ok {
		t.Error("a non-ExitError must not report an exit code")
	}
}

func TestBackendRefusesAnEnvironmentWithNoResolvableKeychain(t *testing.T) {
	// This reproduces the exact conditions that raised the "Reset To Defaults"
	// dialog: a HOME with no Library/Keychains. The assertion is that all three
	// operations now fail with an explanation *before* security(1) is invoked,
	// because once it is invoked the dialog is out of Collomia's hands.
	//
	// It needs no opt-in switch and touches no keychain — that is the point. The
	// guard's whole purpose is that this path never reaches security(1).
	if _, err := os.Stat(securityPath); err != nil {
		t.Skipf("%s is not present", securityPath)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if err := ensureKeychainResolvable(); err == nil {
		t.Fatal("a HOME with no Library/Keychains must be refused")
	} else if !strings.Contains(err.Error(), "api_key_env") {
		t.Errorf("the refusal must name the way forward: %v", err)
	}

	if err := backendSet("collo-selftest-guard", "value"); err == nil {
		t.Error("backendSet must refuse rather than let security(1) raise a dialog")
	}
	if _, _, err := backendGet("collo-selftest-guard"); err == nil {
		t.Error("backendGet must refuse in an environment with no keychain")
	}
	if _, err := backendDelete("collo-selftest-guard"); err == nil {
		t.Error("backendDelete must refuse in an environment with no keychain")
	}
}

func TestAnExplicitKeychainBypassesTheResolvabilityGuard(t *testing.T) {
	// The guard must not block the tests' own temporary keychain, which never
	// goes through default resolution and so can never raise the dialog.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	previous := keychainFile
	keychainFile = filepath.Join(home, "explicit.keychain")
	t.Cleanup(func() { keychainFile = previous })

	if err := ensureKeychainResolvable(); err != nil {
		t.Errorf("an explicitly named keychain needs no default to resolve: %v", err)
	}
}
