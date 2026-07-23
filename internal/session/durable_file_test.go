package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type injectedDurableFile struct {
	file      *os.File
	writeErr  error
	syncErr   error
	closeErr  error
	wroteOnce bool
}

func (f *injectedDurableFile) Write(data []byte) (int, error) {
	if f.writeErr != nil && !f.wroteOnce {
		f.wroteOnce = true
		n, err := f.file.Write(data[:max(1, len(data)/2)])
		if err != nil {
			return n, err
		}
		return n, f.writeErr
	}
	return f.file.Write(data)
}

func (f *injectedDurableFile) Sync() error {
	if f.syncErr != nil {
		return f.syncErr
	}
	return f.file.Sync()
}

func (f *injectedDurableFile) Close() error {
	err := f.file.Close()
	if err == nil {
		err = f.closeErr
	}
	return err
}

func faultOpener(writeErr, syncErr, closeErr error) durableFileOpener {
	return func(path string, flag int, mode os.FileMode) (durableFile, error) {
		file, err := os.OpenFile(path, flag, mode)
		if err != nil {
			return nil, err
		}
		return &injectedDurableFile{file: file, writeErr: writeErr, syncErr: syncErr, closeErr: closeErr}, nil
	}
}

func TestExclusiveDurableBlobFailuresRemovePartialFile(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		writeErr, syncErr, closeErr error
	}{
		{name: "write", writeErr: errors.New("injected ENOSPC")},
		{name: "sync", syncErr: errors.New("injected sync failure")},
		{name: "close", closeErr: errors.New("injected close failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "blob")
			err := writeExclusiveDurable(path, []byte("complete durable blob"), 0o600, faultOpener(tc.writeErr, tc.syncErr, tc.closeErr))
			if err == nil {
				t.Fatal("faulted durable write unexpectedly succeeded")
			}
			if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("partial durable blob survived failure: %v", statErr)
			}
		})
	}
}

func TestArtifactAndAttachmentManagersPropagateStorageFailures(t *testing.T) {
	store, err := OpenAt(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.New("fixture", "model")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	artifactErr := errors.New("injected artifact sync failure")
	artifacts := NewArtifactManager()
	artifacts.Use(sess)
	artifacts.openFile = faultOpener(nil, artifactErr, nil)
	if _, err := artifacts.SaveArtifact("run_command", "oversized output"); !errors.Is(err, artifactErr) {
		t.Fatalf("artifact error=%v", err)
	}
	if entries, err := os.ReadDir(store.artifactDir(sess.Meta.ID)); err != nil || len(entries) != 0 {
		t.Fatalf("failed artifact left files: entries=%v err=%v", entries, err)
	}

	attachmentErr := errors.New("injected attachment ENOSPC")
	attachments := NewAttachmentManager()
	attachments.Use(sess)
	attachments.openFile = faultOpener(attachmentErr, nil, nil)
	if _, err := attachments.SaveBytes("screen.png", "image/png", fixturePNG()); !errors.Is(err, attachmentErr) {
		t.Fatalf("attachment error=%v", err)
	}
	if entries, err := os.ReadDir(store.attachmentDir(sess.Meta.ID)); err != nil || len(entries) != 0 {
		t.Fatalf("failed attachment left files: entries=%v err=%v", entries, err)
	}
}
