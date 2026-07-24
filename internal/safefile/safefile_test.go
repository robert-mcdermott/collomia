package safefile

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReplaceIsAtomicAndBreaksHardLinks(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("shared original"), 0o640); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "inside.txt")
	if err := os.Link(outside, inside); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("hard links unavailable in this Windows environment: %v", err)
		}
		t.Fatal(err)
	}
	target, err := Open(root, inside)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := target.Replace([]byte("workspace replacement"), 0o640); err != nil {
		t.Fatal(err)
	}
	insideData, err := os.ReadFile(inside)
	if err != nil {
		t.Fatal(err)
	}
	outsideData, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(insideData) != "workspace replacement" || string(outsideData) != "shared original" {
		t.Fatalf("inside=%q outside=%q", insideData, outsideData)
	}
	info, err := os.Stat(inside)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Fatalf("mode=%v", info.Mode().Perm())
	}
}

func TestReplaceWriteFailurePreservesOriginalAndCleansTemporary(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, []byte("accepted"), 0o600); err != nil {
		t.Fatal(err)
	}
	target, err := Open(root, path)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	target.writeTemp = func(file *os.File, data []byte) error {
		if _, err := file.Write(data[:len(data)/2]); err != nil {
			return err
		}
		return io.ErrUnexpectedEOF
	}
	if err := target.Replace([]byte("interrupted replacement"), 0o600); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("replace error=%v", err)
	}
	assertOriginalAndNoTemps(t, root, path, "accepted")
}

func TestReplacePublishFailurePreservesOriginalAndCleansTemporary(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, []byte("accepted"), 0o600); err != nil {
		t.Fatal(err)
	}
	target, err := Open(root, path)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	publishErr := errors.New("injected publish interruption")
	target.publishTemp = func(*os.Root, string, string) error { return publishErr }
	if err := target.Replace([]byte("unpublished replacement"), 0o600); !errors.Is(err, publishErr) {
		t.Fatalf("replace error=%v", err)
	}
	assertOriginalAndNoTemps(t, root, path, "accepted")
}

func TestAtomicReplacementSurvivesAbruptProcessDeath(t *testing.T) {
	for _, tc := range []struct {
		stage, want string
	}{
		{stage: "before-publish", want: "accepted"},
		{stage: "after-publish", want: "complete replacement"},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "important.txt")
			if err := os.WriteFile(path, []byte("accepted"), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestAtomicReplacementCrashHelper$")
			cmd.Env = append(os.Environ(),
				"COLLOMIA_SAFEFILE_CRASH_HELPER=1",
				"COLLOMIA_SAFEFILE_CRASH_ROOT="+root,
				"COLLOMIA_SAFEFILE_CRASH_STAGE="+tc.stage,
			)
			output, err := cmd.CombinedOutput()
			exitErr := new(exec.ExitError)
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 87 {
				t.Fatalf("crash helper error=%v output=%s", err, output)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != tc.want {
				t.Fatalf("published bytes=%q err=%v, want complete state %q", data, err, tc.want)
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if !strings.Contains(entry.Name(), ".collomia-") || !strings.HasSuffix(entry.Name(), ".tmp") {
					continue
				}
				info, err := entry.Info()
				if err != nil {
					t.Fatal(err)
				}
				if !info.Mode().IsRegular() {
					t.Fatalf("crash temporary is not a regular file: %s", entry.Name())
				}
				if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
					t.Fatalf("crash temporary mode=%v", info.Mode().Perm())
				}
				if err := os.Remove(filepath.Join(root, entry.Name())); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestAtomicReplacementCrashHelper(t *testing.T) {
	if os.Getenv("COLLOMIA_SAFEFILE_CRASH_HELPER") != "1" {
		return
	}
	root := os.Getenv("COLLOMIA_SAFEFILE_CRASH_ROOT")
	target, err := Open(root, filepath.Join(root, "important.txt"))
	if err != nil {
		t.Fatal(err)
	}
	switch os.Getenv("COLLOMIA_SAFEFILE_CRASH_STAGE") {
	case "before-publish":
		target.writeTemp = func(file *os.File, data []byte) error {
			if _, err := file.Write(data); err != nil {
				return err
			}
			if err := file.Sync(); err != nil {
				return err
			}
			os.Exit(87)
			return nil
		}
	case "after-publish":
		target.publishTemp = func(root *os.Root, oldName, newName string) error {
			if err := root.Rename(oldName, newName); err != nil {
				return err
			}
			os.Exit(87)
			return nil
		}
	default:
		t.Fatal("unknown crash stage")
	}
	if err := target.Replace([]byte("complete replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash helper returned without exiting")
}

func assertOriginalAndNoTemps(t *testing.T, root, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("published file=%q err=%v, want %q", data, err, want)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".collomia-") && strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("interrupted replacement left temporary file %q", entry.Name())
		}
	}
}

func TestRootRefusesSymlinkEscapeForMissingDescendant(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ordinary Windows CI users cannot reliably create symbolic links")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	target, err := Open(root, filepath.Join(root, "escape", "nested", "secret.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := target.Replace([]byte("must stay contained"), 0o600); err == nil {
		t.Fatal("replacement through an escaping parent symlink unexpectedly succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "nested", "secret.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside path was created: %v", err)
	}
}

func TestReplaceRefusesFinalSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ordinary Windows CI users cannot reliably create symbolic links")
	}
	root := t.TempDir()
	real := filepath.Join(root, "real.txt")
	link := filepath.Join(root, "link.txt")
	if err := os.WriteFile(real, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.txt", link); err != nil {
		t.Fatal(err)
	}
	target, err := Open(root, link)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := target.Replace([]byte("replacement"), 0o600); err == nil {
		t.Fatal("final symlink unexpectedly replaced")
	}
	data, err := os.ReadFile(real)
	if err != nil || string(data) != "original" {
		t.Fatalf("symlink target changed: %q err=%v", data, err)
	}
}

func TestRemoveHardLinkLeavesOtherNameUnchanged(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	inside := filepath.Join(root, "inside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, inside); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	target, err := Open(root, inside)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := target.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(inside); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace name still exists: %v", err)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "keep" {
		t.Fatalf("outside hard link changed: %q err=%v", data, err)
	}
}

func TestOpenParentCreatesMissingAuthorizedAncestors(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "missing", "nested", "file.txt")
	target, err := OpenParent(path)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := target.Replace([]byte("created"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "created" {
		t.Fatalf("data=%q err=%v", data, err)
	}
}

func TestRootIdentityDetectsReplacement(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "workspace")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	original, err := CaptureRootIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, filepath.Join(base, "original-workspace")); err != nil {
		t.Skipf("cannot replace directory root on this platform: %v", err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	replacement, err := CaptureRootIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if original.Same(replacement) {
		t.Fatal("replacement directory retained the original root identity")
	}
}

func TestConcurrentParentSymlinkSwapCannotEscapeRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ordinary Windows CI users cannot reliably create symbolic links")
	}
	root := t.TempDir()
	outside := t.TempDir()
	slot := filepath.Join(root, "slot")
	if err := os.Mkdir(slot, 0o700); err != nil {
		t.Fatal(err)
	}
	var stop atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		for !stop.Load() {
			_ = os.RemoveAll(slot)
			_ = os.Symlink(outside, slot)
			_ = os.Remove(slot)
			_ = os.Mkdir(slot, 0o700)
		}
	}()
	for attempt := 0; attempt < 250; attempt++ {
		target, err := Open(root, filepath.Join(slot, "nested", "payload.txt"))
		if err != nil {
			continue
		}
		_ = target.Replace([]byte("must remain in workspace"), 0o600)
		_ = target.Close()
	}
	stop.Store(true)
	<-done
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("symlink race wrote outside root: %v", entries)
	}
}
