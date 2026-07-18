package trust

import (
	"path/filepath"
	"testing"
)

func TestTrustLifecycle(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadFrom(filepath.Join(dir, "trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(dir, "repo")
	config := []byte(`{"permissions":{"mode":"workspace"}}`)

	if got := store.Check(workspace, nil); got != StatusTrusted {
		t.Fatalf("no project config should not require trust, got %s", got)
	}
	if got := store.Check(workspace, config); got != StatusUntrusted {
		t.Fatalf("unknown workspace should be untrusted, got %s", got)
	}
	if err := store.Trust(workspace, config); err != nil {
		t.Fatal(err)
	}
	if got := store.Check(workspace, config); got != StatusTrusted {
		t.Fatalf("trusted workspace should verify, got %s", got)
	}
	if got := store.Check(workspace, []byte(`{"changed":true}`)); got != StatusChanged {
		t.Fatalf("changed config should invalidate trust, got %s", got)
	}

	// Trust persists across reloads.
	reloaded, err := LoadFrom(filepath.Join(dir, "trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Check(workspace, config); got != StatusTrusted {
		t.Fatalf("trust should persist, got %s", got)
	}
	if err := reloaded.Revoke(workspace); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Check(workspace, config); got != StatusUntrusted {
		t.Fatalf("revoked workspace should be untrusted, got %s", got)
	}
}
