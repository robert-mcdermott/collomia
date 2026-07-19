package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/userconfig"
)

func isolateUserDirectories(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	legacy := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", legacy)
	t.Setenv("AppData", legacy)
}

func TestGlobalInstructionsUseDotCollomia(t *testing.T) {
	isolateUserDirectories(t)
	dir, err := userconfig.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(path, []byte("Prefer focused tests."), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := GlobalInstructions()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, path) || !strings.Contains(got, "Prefer focused tests.") {
		t.Fatalf("GlobalInstructions()=%q", got)
	}
}

func TestDiscoverUsesDotCollomiaSkills(t *testing.T) {
	isolateUserDirectories(t)
	dir, err := userconfig.Dir()
	if err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(dir, "skills", "reviewer")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Reviewer\nReview changes."), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := Discover(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Skills) != 1 || catalog.Skills[0].Name != "reviewer" {
		t.Fatalf("skills=%+v", catalog.Skills)
	}
}
