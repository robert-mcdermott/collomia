//go:build !windows

package reliability

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// exhaustionEnv opts these tests in. They mount and unmount a real filesystem,
// which is not something an ordinary `go test ./...` should do to a developer's
// machine, and is why this follows the same idiom as COLLO_KEYCHAIN_TESTS and
// COLLO_LIVE_WEB_TESTS.
const exhaustionEnv = "COLLO_DISK_EXHAUSTION_TESTS"

// linuxDirEnv lets an operator supply the filesystem on platforms where
// creating one needs privileges this test will not take. On Linux a loop mount
// or a size-limited tmpfs both require root; rather than skipping the platform
// silently or asking for privileges, the test uses a directory the operator
// prepared:
//
//	sudo mount -t tmpfs -o size=4m tmpfs /mnt/collo-full
//	COLLO_DISK_EXHAUSTION_TESTS=1 COLLO_FULL_FS_DIR=/mnt/collo-full go test ./internal/reliability
const linuxDirEnv = "COLLO_FULL_FS_DIR"

// smallFilesystem is a mounted filesystem with very little space, which the
// test fills when it is ready to.
type smallFilesystem struct {
	dir    string
	device string
	image  string
}

// mountSmallFilesystem returns an empty filesystem of a few megabytes.
//
// Filling comes later, as its own step, because most of these tests need to
// create something while there is still room and then watch what happens when
// there is not. A harness that arrived already full could only test the
// first write.
func mountSmallFilesystem(t *testing.T) *smallFilesystem {
	t.Helper()
	if os.Getenv(exhaustionEnv) != "1" {
		t.Skipf("set %s=1 to run the filesystem exhaustion campaign", exhaustionEnv)
	}
	if dir := os.Getenv(linuxDirEnv); dir != "" {
		scratch := filepath.Join(dir, fmt.Sprintf("collo-%d", os.Getpid()))
		if err := os.MkdirAll(scratch, 0o755); err != nil {
			t.Fatalf("prepare %s: %v", scratch, err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(scratch) })
		return &smallFilesystem{dir: scratch}
	}
	if runtime.GOOS != "darwin" {
		t.Skipf("no unprivileged way to create a small filesystem on %s; set %s to a prepared mount point", runtime.GOOS, linuxDirEnv)
	}
	return mountDiskImage(t)
}

// mountDiskImage builds a disk image with hdiutil, which needs no privileges
// on macOS.
func mountDiskImage(t *testing.T) *smallFilesystem {
	t.Helper()
	image := filepath.Join(t.TempDir(), "small.dmg")
	create := exec.Command("hdiutil", "create", "-size", "8m", "-fs", "HFS+",
		"-volname", "colloexhaustion", "-quiet", image)
	if out, err := create.CombinedOutput(); err != nil {
		t.Skipf("hdiutil create failed, skipping: %v\n%s", err, out)
	}
	attach := exec.Command("hdiutil", "attach", image, "-nobrowse")
	out, err := attach.CombinedOutput()
	if err != nil {
		t.Skipf("hdiutil attach failed, skipping: %v\n%s", err, out)
	}
	// The mount point is the remainder of the line that names one, and it can
	// contain spaces: macOS appends " 1" when a volume of the same name is
	// already mounted, which a naive last-field parse silently truncates into
	// a path that does not exist.
	dir, device := "", ""
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 3)
		if device == "" && len(fields) > 0 && strings.HasPrefix(fields[0], "/dev/") {
			device = strings.TrimSpace(fields[0])
		}
		if len(fields) == 3 && strings.Contains(fields[2], "/Volumes/") {
			dir = strings.TrimSpace(fields[2])
		}
	}
	if dir == "" || device == "" {
		t.Fatalf("could not read a mount point from hdiutil output:\n%s", out)
	}
	fs := &smallFilesystem{dir: dir, device: device, image: image}
	t.Cleanup(fs.detach)
	return fs
}

func (fs *smallFilesystem) detach() {
	if fs.device == "" {
		return
	}
	if err := exec.Command("hdiutil", "detach", fs.device, "-quiet").Run(); err != nil {
		_ = exec.Command("hdiutil", "detach", fs.device, "-force", "-quiet").Run()
	}
}

// fill consumes the remaining space until even a single byte cannot be
// written.
//
// Writing one large file is not enough and produces a harness that lies. A
// filler that fails partway leaves the file allocated up to the point it
// failed, and a few kilobytes of slack behind it — enough that the next small
// write succeeds and the test believes it exercised an exhausted filesystem
// when it did not. Filling in shrinking steps until a one-byte write fails is
// what makes the state actually exhausted.
func (fs *smallFilesystem) fill(t *testing.T) {
	t.Helper()
	fs.consume(t, []int{4 << 20, 1 << 20, 64 << 10, 4 << 10, 512, 64})
	if err := os.WriteFile(filepath.Join(fs.dir, "probe"), []byte{'x'}, 0o600); err == nil {
		t.Fatalf("filesystem at %s still accepts writes after filling; the harness would prove nothing", fs.dir)
	}
}

// fillNearly leaves a little room — enough to create a file, not enough to
// write a large one.
//
// This is the state that actually exercises an atomic replacement, and the
// distinction was found by mutation rather than by reasoning. A test written
// against a completely full filesystem passed even when Replace was rewritten
// to truncate the destination in place, which is precisely the data loss the
// temporary-plus-rename design exists to prevent: on a disk with nothing left,
// creating the temporary file fails first, so the interesting code never runs
// and the test proves only that the early error is handled.
//
// A real disk fills up while a program is running, and the write that fails is
// the one that had somewhere to start and nowhere to finish.
func (fs *smallFilesystem) fillNearly(t *testing.T) {
	t.Helper()
	written := fs.consume(t, []int{4 << 20, 1 << 20, 64 << 10, 4 << 10, 512, 64})
	// Fill completely, then hand a known amount back. Stopping the fill early
	// does not work: the loop only stops when a size no longer fits, which by
	// then has consumed the slack it was supposed to leave. Deleting fillers
	// is the only way to arrive at a controlled small amount of free space.
	freed := 0
	for i := len(written) - 1; i >= 0 && freed < 32<<10; i-- {
		if err := os.Remove(written[i].name); err == nil {
			freed += written[i].size
		}
	}
	probe := filepath.Join(fs.dir, "probe")
	if err := os.WriteFile(probe, []byte{'x'}, 0o600); err != nil {
		t.Skipf("could not free usable slack on %s: %v", fs.dir, err)
	}
	if err := os.Remove(probe); err != nil {
		t.Fatalf("remove probe: %v", err)
	}
}

type filler struct {
	name string
	size int
}

// consume writes filler files in the given decreasing sizes until each size no
// longer fits, and reports what it wrote so a caller can give some back.
func (fs *smallFilesystem) consume(t *testing.T, sizes []int) []filler {
	t.Helper()
	var written []filler
	for _, size := range sizes {
		for index := 0; ; index++ {
			name := filepath.Join(fs.dir, fmt.Sprintf("filler-%d-%d", size, index))
			if err := os.WriteFile(name, make([]byte, size), 0o600); err != nil {
				// A failed write can still leave a partially allocated file.
				_ = os.Remove(name)
				break
			}
			written = append(written, filler{name: name, size: size})
		}
	}
	return written
}

// workspace returns a subdirectory created while there is still room.
func (fs *smallFilesystem) workspace(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(fs.dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}
	return dir
}

// temporaryFiles lists the leftover temporary files under dir. safefile writes
// through a private same-directory temporary, so a failure that leaves one
// behind litters the user's workspace with hidden files after every failed
// write.
func temporaryFiles(t *testing.T, dir string) []string {
	t.Helper()
	var found []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".collomia-") && strings.HasSuffix(entry.Name(), ".tmp") {
			found = append(found, entry.Name())
		}
	}
	return found
}
