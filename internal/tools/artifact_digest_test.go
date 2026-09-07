package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRecheckArtifactDigest(t *testing.T) {
	workspace := t.TempDir()
	guard, err := NewPathGuard(workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(guard.Workspace, "report.txt")
	if err := os.WriteFile(path, []byte("valid text"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := (ValidateArtifactTool{Guard: guard}).ExecuteResultStream(t.Context(), []byte(`{"path":"report.txt"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := RecheckArtifactDigest(t.Context(), path, path)
	if err != nil || digest != result.Evidence.Digest {
		t.Fatalf("digest=%q err=%v", digest, err)
	}
	if err := os.WriteFile(path, []byte("other text"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err = RecheckArtifactDigest(t.Context(), path, path)
	if err != nil || digest == result.Evidence.Digest {
		t.Fatal("changed bytes retained digest")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := RecheckArtifactDigest(ctx, path, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := RecheckArtifactDigest(t.Context(), path, path); err == nil {
		t.Fatal("missing file accepted")
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := RecheckArtifactDigest(t.Context(), path, path); err == nil {
		t.Fatal("directory accepted")
	}
}

func TestRecheckRejectsRetargetedAliasAndOversize(t *testing.T) {
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, second, alias := filepath.Join(workspace, "one"), filepath.Join(workspace, "two"), filepath.Join(workspace, "alias")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("same"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(first, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := RecheckArtifactDigest(t.Context(), alias, first); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := RecheckArtifactDigest(t.Context(), alias, first); err == nil {
		t.Fatal("same bytes at a different target accepted")
	}
	file, err := os.OpenFile(first, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxArtifactValidationBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := RecheckArtifactDigest(t.Context(), first, first); err == nil {
		t.Fatal("oversize file accepted")
	}
}

func TestRecheckRejectsReplacedParent(t *testing.T) {
	workspace := t.TempDir()
	parent := filepath.Join(workspace, "output")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "report.txt")
	if err := os.WriteFile(path, []byte("same"), 0o600); err != nil {
		t.Fatal(err)
	}
	guard, err := NewPathGuard(workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	result, err := (ValidateArtifactTool{Guard: guard}).ExecuteResultStream(t.Context(), []byte(`{"path":"output/report.txt"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parent, parent+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("same"), 0o600); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecheckArtifactDigest(t.Context(), path, canonical, result.Evidence.ArtifactRoot); err == nil {
		t.Fatal("replacement parent accepted")
	}
}
