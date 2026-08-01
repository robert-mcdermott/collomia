package reliability

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/audit"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/safefile"
	"github.com/robert-mcdermott/collomia/internal/session"
)

// The property every file mutation in Collomia rests on: a write that cannot
// complete must leave the previous contents exactly as they were.
//
// This is what the temporary-plus-rename design is for, and it is worth
// checking against a real exhausted filesystem rather than an injected error,
// because the injected version chooses where the failure lands and the real one
// does not.
func TestFullDiskLeavesAnExistingFileIntact(t *testing.T) {
	fs := mountSmallFilesystem(t)
	workspace := fs.workspace(t, "workspace")
	path := filepath.Join(workspace, "important.txt")
	original := []byte("the contents that must survive\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	fs.fillNearly(t)

	target, err := safefile.Open(workspace, path)
	if err != nil {
		t.Fatalf("open target on a full filesystem: %v", err)
	}
	defer target.Close()

	// Larger than what is left, by asking rather than by guessing.
	oversized := make([]byte, freeBytes(t, workspace)+(1<<20))
	if target.Replace(oversized, 0o644) == nil {
		t.Fatalf("a %d-byte write succeeded on a filesystem with less than that free", len(oversized))
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the original file is unreadable after a failed replace: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("a failed replace changed the file:\n got: %q\nwant: %q", after, original)
	}
	if leftover := temporaryFiles(t, workspace); len(leftover) > 0 {
		t.Errorf("a failed replace left temporary files behind: %v", leftover)
	}
}

// A create that fails must leave no file at all, rather than an empty or
// partial one that later reads as a real but truncated document.
func TestFullDiskCreatesNoPartialFile(t *testing.T) {
	fs := mountSmallFilesystem(t)
	workspace := fs.workspace(t, "workspace")
	fs.fillNearly(t)

	path := filepath.Join(workspace, "new.txt")
	target, err := safefile.Open(workspace, path)
	if err != nil {
		t.Fatalf("open target on a full filesystem: %v", err)
	}
	defer target.Close()
	oversized := make([]byte, freeBytes(t, workspace)+(1<<20))
	if target.Replace(oversized, 0o644) == nil {
		t.Fatalf("a %d-byte write succeeded on a filesystem with less than that free", len(oversized))
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a failed create left %s behind (stat error: %v)", path, err)
	}
	if leftover := temporaryFiles(t, workspace); len(leftover) > 0 {
		t.Errorf("a failed create left temporary files behind: %v", leftover)
	}
}

// The durable session must fail visibly rather than continue and report a turn
// as persisted. The fail-stop behavior has injected-fault coverage; this checks
// it against the real thing, including the flush that only a real filesystem
// can refuse.
func TestFullDiskLatchesTheDurableSession(t *testing.T) {
	fs := mountSmallFilesystem(t)
	sessions := fs.workspace(t, "sessions")
	artifacts := fs.workspace(t, "artifacts")

	store, err := session.OpenAt(sessions, artifacts)
	if err != nil {
		t.Fatalf("open session store: %v", err)
	}
	sess, err := store.New("fixture", "model")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer sess.Close()
	fs.fill(t)

	// Enough content that the filesystem cannot absorb it in whatever slack
	// remains after filling.
	large := make([]byte, 256<<10)
	for i := range large {
		large[i] = 'x'
	}
	for i := 0; i < 8 && sess.Err() == nil; i++ {
		sess.AppendMessage(provider.Message{Role: "user", Content: string(large)})
	}
	if err := sess.Err(); err == nil {
		t.Skip("the filesystem absorbed every append; nothing to assert")
	}
	// Once latched it must stay latched: a later append that appeared to
	// succeed would put a record after a torn one, turning a recoverable tail
	// into corruption in the middle of the file.
	first := sess.Err()
	sess.AppendMessage(provider.Message{Role: "user", Content: "short"})
	if sess.Err() != first {
		t.Errorf("the latched error changed after a later append: %v then %v", first, sess.Err())
	}
}

// The audit ledger is fail-visible rather than fail-stop: work the user already
// authorized must not be refused because a record could not be filed. What it
// must never do is lose entries silently.
func TestFullDiskMakesTheAuditLedgerDeclareItsGap(t *testing.T) {
	fs := mountSmallFilesystem(t)
	dir := fs.workspace(t, "audit")

	ledger := audit.OpenAt(filepath.Join(dir, "ledger.jsonl"), "workspace")
	reported := 0
	ledger.OnFailure = func(error) { reported++ }
	fs.fill(t)

	padding := make([]byte, 128<<10)
	for i := range padding {
		padding[i] = 'x'
	}
	for i := 0; i < 8; i++ {
		ledger.Append(audit.Entry{Kind: "decision", Tool: "run_command", Summary: string(padding), Decision: "allow"})
	}
	dropped, _, _ := ledger.Degraded()
	if dropped == 0 {
		t.Skip("the filesystem absorbed every entry; nothing to assert")
	}
	// Once, not once per entry: a full disk would otherwise turn a report into
	// a flood during exactly the incident someone is trying to read about.
	if reported != 1 {
		t.Errorf("a full disk reported the ledger failure %d times, want exactly 1", reported)
	}
}
