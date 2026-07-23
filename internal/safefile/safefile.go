// Package safefile provides race-resistant, atomic mutations beneath an
// already-authorized directory root. It is intentionally small: callers keep
// responsibility for policy and approval, while this package makes the final
// filesystem operation match the approved rooted path.
package safefile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Target is one path anchored beneath an os.Root. Root operations reject
// absolute paths, parent traversal, and symlinks that escape the root, and are
// race-resistant on Collomia's supported operating systems.
type Target struct {
	root *os.Root
	name string
	abs  string
	// Per-target fault seams keep interruption tests deterministic without
	// introducing mutable process-global hooks. Nil uses the real OS path.
	writeTemp   func(*os.File, []byte) error
	publishTemp func(*os.Root, string, string) error
}

// RootIdentity is a stable, opaque identity for one directory. Capture uses an
// already-open directory handle rather than retaining an os.Stat result. This
// distinction matters on Windows, where os.Stat may defer loading the volume
// and file IDs until os.SameFile is first called; resolving that lazy identity
// after a path is replaced would identify the replacement instead.
type RootIdentity struct {
	info os.FileInfo
}

// CaptureRootIdentity records the directory currently named by rootPath. The
// returned identity remains tied to that directory after the handle is closed,
// so callers can detect a later rename-and-replacement of the root path.
func CaptureRootIdentity(rootPath string) (RootIdentity, error) {
	rootAbs, err := filepath.Abs(rootPath)
	if err != nil {
		return RootIdentity{}, err
	}
	root, err := os.OpenRoot(rootAbs)
	if err != nil {
		return RootIdentity{}, err
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil {
		return RootIdentity{}, err
	}
	if !info.IsDir() {
		return RootIdentity{}, fmt.Errorf("mutation root %s is not a directory", rootAbs)
	}
	return RootIdentity{info: info}, nil
}

func (id RootIdentity) Valid() bool { return id.info != nil }

// Same reports whether two captured identities refer to the same directory.
func (id RootIdentity) Same(other RootIdentity) bool {
	return id.info != nil && other.info != nil && os.SameFile(id.info, other.info)
}

// Open anchors path beneath rootPath. Both paths are made absolute before the
// lexical containment check; symlink containment is enforced by os.Root when
// an operation is performed.
func Open(rootPath, path string) (*Target, error) {
	rootAbs, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, err
	}
	expectedRoot, err := os.Stat(rootAbs)
	if err != nil {
		return nil, err
	}
	if !expectedRoot.IsDir() {
		return nil, fmt.Errorf("mutation root %s is not a directory", rootAbs)
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return nil, err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, fmt.Errorf("path %q is not a file beneath mutation root %s", path, rootAbs)
	}
	root, err := os.OpenRoot(rootAbs)
	if err != nil {
		return nil, err
	}
	openedRoot, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	if !os.SameFile(expectedRoot, openedRoot) {
		_ = root.Close()
		return nil, fmt.Errorf("mutation root %s changed while it was being opened", rootAbs)
	}
	return &Target{root: root, name: rel, abs: filepath.Clean(pathAbs)}, nil
}

// OpenParent anchors an explicitly authorized path to its nearest existing
// parent. It is used only when policy permits mutation outside the workspace;
// selecting an existing ancestor preserves create-with-parents behavior while
// Root still binds every intermediate lookup to that directory handle.
func OpenParent(path string) (*Target, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	parent := filepath.Dir(abs)
	for {
		info, statErr := os.Stat(parent)
		if statErr == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("mutation parent %s is not a directory", parent)
			}
			return Open(parent, abs)
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return nil, statErr
		}
		next := filepath.Dir(parent)
		if next == parent {
			return nil, statErr
		}
		parent = next
	}
}

func (t *Target) Path() string { return t.abs }

func (t *Target) Close() error {
	if t == nil || t.root == nil {
		return nil
	}
	err := t.root.Close()
	t.root = nil
	return err
}

func (t *Target) ReadFile() ([]byte, error)   { return t.root.ReadFile(t.name) }
func (t *Target) OpenFile() (*os.File, error) { return t.root.Open(t.name) }
func (t *Target) Stat() (os.FileInfo, error)  { return t.root.Stat(t.name) }
func (t *Target) Lstat() (os.FileInfo, error) { return t.root.Lstat(t.name) }
func (t *Target) RootIdentity() (RootIdentity, error) {
	if t == nil || t.root == nil {
		return RootIdentity{}, errors.New("safe file target is closed")
	}
	info, err := t.root.Stat(".")
	if err != nil {
		return RootIdentity{}, err
	}
	return RootIdentity{info: info}, nil
}

// Replace writes data to a private same-directory temporary file, syncs and
// closes it, then atomically publishes it over the destination. Replacing the
// directory entry instead of truncating an existing inode prevents a workspace
// hard link from modifying another name for that inode.
func (t *Target) Replace(data []byte, mode os.FileMode) error {
	if t == nil || t.root == nil {
		return errors.New("safe file target is closed")
	}
	mode &= os.ModePerm
	if mode == 0 {
		mode = 0o644
	}
	parent := filepath.Dir(t.name)
	if parent == "." {
		parent = ""
	}
	if parent != "" {
		if err := t.root.MkdirAll(parent, 0o755); err != nil {
			return fmt.Errorf("create parent directories: %w", err)
		}
	}
	exists := false
	if info, err := t.root.Lstat(t.name); err == nil {
		exists = true
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to replace symbolic link %s", t.abs)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular file %s", t.abs)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	createMode := os.FileMode(0o600)
	if !exists {
		// Let the process umask participate in the mode of a newly created
		// destination, matching os.WriteFile behavior. The empty temporary is
		// immediately made private before any content is written.
		createMode = mode
	}
	tempName, file, err := t.createTemp(parent, filepath.Base(t.name), createMode)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		_ = file.Close()
		if cleanup {
			_ = t.root.Remove(tempName)
		}
	}()
	publishMode := mode
	if !exists {
		if info, statErr := file.Stat(); statErr != nil {
			return fmt.Errorf("inspect temporary file mode: %w", statErr)
		} else {
			publishMode = info.Mode().Perm()
		}
		if err := file.Chmod(0o600); err != nil {
			return fmt.Errorf("make temporary file private: %w", err)
		}
	}
	writeTemp := t.writeTemp
	if writeTemp == nil {
		writeTemp = func(file *os.File, data []byte) error { return writeAll(file, data) }
	}
	if err := writeTemp(file, data); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := file.Chmod(publishMode); err != nil {
		return fmt.Errorf("set temporary file mode: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	publishTemp := t.publishTemp
	if publishTemp == nil {
		publishTemp = func(root *os.Root, oldName, newName string) error {
			return root.Rename(oldName, newName)
		}
	}
	if err := publishTemp(t.root, tempName, t.name); err != nil {
		return fmt.Errorf("publish atomic replacement: %w", err)
	}
	cleanup = false
	// Directory sync is not uniformly supported (notably on Windows). The
	// content itself has already been synced; best-effort directory sync gives
	// stronger crash durability where the platform provides it.
	dirName := parent
	if dirName == "" {
		dirName = "."
	}
	if dir, err := t.root.Open(dirName); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

// Remove deletes the rooted directory entry. It refuses directories; deleting
// a hard link removes only this name and cannot change the other linked file.
func (t *Target) Remove() error {
	if t == nil || t.root == nil {
		return errors.New("safe file target is closed")
	}
	info, err := t.root.Lstat(t.name)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("refusing to remove directory %s", t.abs)
	}
	return t.root.Remove(t.name)
}

func (t *Target) createTemp(parent, base string, mode os.FileMode) (string, *os.File, error) {
	for attempt := 0; attempt < 16; attempt++ {
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, fmt.Errorf("generate temporary name: %w", err)
		}
		name := "." + base + ".collomia-" + hex.EncodeToString(random[:]) + ".tmp"
		if parent != "" {
			name = filepath.Join(parent, name)
		}
		file, err := t.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err == nil {
			return name, file, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", nil, fmt.Errorf("create temporary file: %w", err)
		}
	}
	return "", nil, errors.New("could not allocate a unique temporary file")
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := w.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}
