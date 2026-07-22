package session

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

func fixturePNG() []byte { return append([]byte("\x89PNG\r\n\x1a\n"), []byte("collomia fixture")...) }

func TestImageAttachmentsAreBoundedIntegrityCheckedAndOwnerOnly(t *testing.T) {
	store, err := OpenAt(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.New("fixture", "vision")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	manager := NewAttachmentManager()
	manager.Use(sess)
	part, err := manager.SaveBytes("screen.png", "image/png", fixturePNG())
	if err != nil {
		t.Fatal(err)
	}
	if part.AttachmentID == "" || part.MediaType != "image/png" || part.Size != len(fixturePNG()) || len(part.SHA256) != 64 || len(part.Data) != 0 {
		t.Fatalf("part=%+v", part)
	}
	resolved, err := manager.Resolve(part)
	if err != nil || string(resolved) != string(fixturePNG()) {
		t.Fatalf("resolved=%q err=%v", resolved, err)
	}
	path := filepath.Join(store.attachmentDir(sess.Meta.ID), part.AttachmentID+".bin")
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(path)
		if statErr != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("mode=%v err=%v", info.Mode(), statErr)
		}
	}
	if err := os.WriteFile(path, append(fixturePNG(), 'x'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Resolve(part); err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("expected tamper detection, got %v", err)
	}
}

func TestImageAttachmentLifecycleFollowsForkRewindAndDelete(t *testing.T) {
	store, err := OpenAt(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.New("fixture", "vision")
	if err != nil {
		t.Fatal(err)
	}
	manager := NewAttachmentManager()
	manager.Use(sess)
	first, err := manager.SaveBytes("first.png", "image/png", fixturePNG())
	if err != nil {
		t.Fatal(err)
	}
	sess.AppendMessage(provider.Message{Role: "user", Content: "first", Parts: []provider.ContentPart{first}})
	sess.AppendMessage(provider.Message{Role: "assistant", Content: "done"})
	sess.AppendEvent(event.New(event.KindTurnEnd))
	secondData := append(fixturePNG(), []byte("second")...)
	second, err := manager.SaveBytes("second.png", "image/png", secondData)
	if err != nil {
		t.Fatal(err)
	}
	sess.AppendMessage(provider.Message{Role: "user", Content: "second", Parts: []provider.ContentPart{second}})
	sess.AppendMessage(provider.Message{Role: "assistant", Content: "done"})
	sess.AppendEvent(event.New(event.KindTurnEnd))
	id := sess.Meta.ID
	sess.Close()

	forked, err := store.Fork(id)
	if err != nil {
		t.Fatal(err)
	}
	manager.Use(forked)
	if _, err := manager.Resolve(first); err != nil {
		t.Fatalf("fork lost first attachment: %v", err)
	}
	if _, err := manager.Resolve(second); err != nil {
		t.Fatalf("fork lost second attachment: %v", err)
	}
	forkID := forked.Meta.ID
	forked.Close()

	rewound, err := store.Rewind(id, 1)
	if err != nil {
		t.Fatal(err)
	}
	manager.Use(rewound)
	if _, err := manager.Resolve(first); err != nil {
		t.Fatalf("rewind lost retained attachment: %v", err)
	}
	if _, err := manager.Resolve(second); err == nil {
		t.Fatal("rewind copied attachment from discarded future")
	}
	rewindID := rewound.Meta.ID
	rewound.Close()

	for _, deleteID := range []string{forkID, rewindID} {
		if err := store.Delete(deleteID); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(store.attachmentDir(deleteID)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("attachment directory survived delete: %v", err)
		}
	}
}

func TestInspectImageRejectsUnsupportedAndOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	textPath := filepath.Join(dir, "not-image.txt")
	if err := os.WriteFile(textPath, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectImage(textPath); err == nil {
		t.Fatal("text file accepted as an image")
	}
	largePath := filepath.Join(dir, "large.png")
	f, err := os.Create(largePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(AttachmentFileLimit + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if _, err := InspectImage(largePath); err == nil {
		t.Fatal("oversized image accepted")
	}
}

func TestReadWorkspaceImageCannotFollowSymlinkOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, fixturePNG(), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workspace, "linked.png")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if _, err := ReadWorkspaceImage(workspace, link); err == nil {
		t.Fatal("workspace-rooted image read followed a symbolic link outside the workspace")
	}
	inside := filepath.Join(workspace, "inside.png")
	if err := os.WriteFile(inside, fixturePNG(), 0o600); err != nil {
		t.Fatal(err)
	}
	part, err := ReadWorkspaceImage(workspace, inside)
	if err != nil {
		t.Fatal(err)
	}
	if part.MediaType != "image/png" || len(part.Data) != len(fixturePNG()) {
		t.Fatalf("part=%+v", part)
	}
}
