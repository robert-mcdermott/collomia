//go:build windows

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestAppContainerPathsResolveRealDirectoryJunction(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "SDK target")
	alias := filepath.Join(root, "SDK alias")
	if err := os.MkdirAll(filepath.Join(target, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "bin", "fixture.exe"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	// Junction creation does not require the symlink privilege. Exercise the
	// real kernel resolver rather than a mocked C:-to-D: string substitution.
	// cmd.exe does not use Go's default CommandLineToArgvW quoting.
	// Supply its /s /c outer quotes explicitly so the embedded path quotes
	// reach mklink intact, including the deliberate spaces in these paths.
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: fmt.Sprintf(`cmd.exe /d /s /c "mklink /J "%s" "%s""`, alias, target)}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("create junction: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = os.Remove(alias) })
	for _, suffix := range []string{".", "bin", filepath.Join("bin", "fixture.exe")} {
		got, err := finalAppContainerPath(filepath.Join(alias, suffix))
		if err != nil {
			t.Fatal(err)
		}
		want, err := finalAppContainerPath(filepath.Join(target, suffix))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.EqualFold(got, want) {
			t.Fatalf("final junction path=%q, want %q", got, want)
		}
	}
	path := filepath.Join(alias, "bin") + string(os.PathListSeparator) + `C:\missing-tool\bin`
	t.Setenv("PATH", path)
	t.Setenv("GOROOT", alias)
	restore, err := canonicalizeAppContainerPathEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	wantRoot, err := finalAppContainerPath(target)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(wantRoot, "bin") + string(os.PathListSeparator) + `C:\missing-tool\bin`
	if !strings.EqualFold(os.Getenv("PATH"), wantPath) || !strings.EqualFold(os.Getenv("GOROOT"), wantRoot) {
		t.Fatalf("canonical environment PATH=%q GOROOT=%q", os.Getenv("PATH"), os.Getenv("GOROOT"))
	}
	restore()
	if os.Getenv("PATH") != path || os.Getenv("GOROOT") != alias {
		t.Fatal("original environment not restored")
	}
}
