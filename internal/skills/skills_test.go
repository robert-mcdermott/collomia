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
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
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

// ReadableDirs decides which bundles read_file may open, so its boundaries are
// the quarantine's boundaries. An active skill's own directory is readable
// because load_skill tells the model to read its references; a disabled skill
// and an untrusted project skill are not, since neither is supposed to reach
// the model at all.
func TestReadableDirsCoverActiveSkillsOnly(t *testing.T) {
	isolateUserDirectories(t)
	dir, err := userconfig.Dir()
	if err != nil {
		t.Fatal(err)
	}
	write := func(root, name, body string) string {
		skillDir := filepath.Join(root, name)
		if err := os.MkdirAll(skillDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return skillDir
	}
	userRoot := filepath.Join(dir, "skills")
	active := write(userRoot, "branding", "# Branding\nUse the palette.")
	off := write(userRoot, "retired", "# Retired\nOld guidance.")
	if err := os.WriteFile(filepath.Join(off, DisabledMarker), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	project := write(ProjectSkillsDir(workspace), "repo-local", "# Repo local\nProject guidance.")

	untrusted, err := Discover(workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	dirs := untrusted.ReadableDirs()
	if len(dirs) != 1 || dirs[0] != active {
		t.Fatalf("readable dirs with an untrusted project=%v, want only %s", dirs, active)
	}

	trusted, err := Discover(workspace, true)
	if err != nil {
		t.Fatal(err)
	}
	readable := map[string]bool{}
	for _, d := range trusted.ReadableDirs() {
		readable[d] = true
	}
	if !readable[active] || !readable[project] {
		t.Fatalf("trusted readable dirs=%v, want both %s and %s", trusted.ReadableDirs(), active, project)
	}
	if readable[off] {
		t.Fatalf("a disabled skill's bundle became readable: %v", trusted.ReadableDirs())
	}
}
