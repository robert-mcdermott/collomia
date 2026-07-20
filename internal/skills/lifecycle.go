package skills

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/userconfig"
)

const maxInstallFiles = 2000

// ProjectSkillsDir is where project-scoped skills live inside a workspace.
func ProjectSkillsDir(workspace string) string {
	return filepath.Join(workspace, ".collomia", "skills")
}

// UserSkillsDir is where global skills live (~/.collomia/skills).
func UserSkillsDir() (string, error) {
	dir, err := userconfig.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "skills"), nil
}

// Roots returns every skill root that lifecycle commands operate on, project
// scope first (matching discovery precedence).
func Roots(workspace string) []string {
	roots := []string{ProjectSkillsDir(workspace), filepath.Join(workspace, ".agents", "skills")}
	if dir, err := userconfig.Dir(); err == nil {
		roots = append(roots, filepath.Join(dir, "skills"))
	}
	return roots
}

// FindDir locates an installed skill's directory by name across all roots,
// regardless of trust or disabled state — lifecycle commands are explicit
// user file operations, not capability activation.
func FindDir(workspace, name string) (string, error) {
	if !nameRE.MatchString(name) {
		return "", fmt.Errorf("invalid skill name %q", name)
	}
	for _, root := range Roots(workspace) {
		dir := filepath.Join(root, name)
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err == nil {
			return dir, nil
		}
	}
	return "", fmt.Errorf("no installed skill named %q (project and user skill directories checked)", name)
}

const scaffoldTemplate = `---
name: %[1]s
description: One sentence saying what this skill does and when the agent should use it.
metadata:
  version: 0.1.0
---

# %[1]s

## When to use this skill

Describe the tasks this skill applies to. The agent only sees the
description above until it loads the skill, so make the description count.

## Instructions

Step-by-step guidance for the agent.

## Bundled files (optional)

Create these directories beside SKILL.md as needed:

- scripts/    — executable helpers the agent can run with run_command
- references/ — extra documentation the agent reads on demand
- assets/     — templates and files used in the skill's output
`

// Scaffold creates root/<name>/SKILL.md from the starter template and
// returns the created directory.
func Scaffold(root, name string) (string, error) {
	if !nameRE.MatchString(name) || len(name) > maxNameLength {
		return "", fmt.Errorf("skill name must be lowercase letters, digits, and hyphens (max %d characters), got %q", maxNameLength, name)
	}
	dir := filepath.Join(root, name)
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("skill directory already exists: %s", dir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(fmt.Sprintf(scaffoldTemplate, name)), 0o644); err != nil {
		return "", err
	}
	return dir, nil
}

// Install validates the skill at src (a directory containing SKILL.md) and
// copies it into root under its skill name. With replace, an existing
// installation of the same name is overwritten atomically-enough for a local
// file tree: the old directory is removed after the copy fully succeeds into
// a staging path.
func Install(src, root string, replace bool) (Skill, error) {
	src, err := filepath.Abs(src)
	if err != nil {
		return Skill{}, err
	}
	manifest := filepath.Join(src, "SKILL.md")
	if _, err := os.Stat(manifest); err != nil {
		return Skill{}, fmt.Errorf("%s is not a skill directory (no SKILL.md): %w", src, err)
	}
	skill, err := inspect(manifest, src, "")
	if err != nil {
		return Skill{}, err
	}
	if !nameRE.MatchString(skill.Name) || len(skill.Name) > maxNameLength {
		return Skill{}, fmt.Errorf("skill name %q is invalid; fix the front matter before installing", skill.Name)
	}
	dest := filepath.Join(root, skill.Name)
	if same, sameErr := samePath(src, dest); sameErr == nil && same {
		return Skill{}, fmt.Errorf("%s is already installed at %s", skill.Name, dest)
	}
	if _, err := os.Stat(dest); err == nil {
		if !replace {
			return Skill{}, fmt.Errorf("skill %q is already installed at %s; pass --yes to replace it", skill.Name, dest)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Skill{}, err
	}
	staging := dest + ".installing"
	_ = os.RemoveAll(staging)
	if err := copySkillTree(src, staging); err != nil {
		_ = os.RemoveAll(staging)
		return Skill{}, err
	}
	if err := os.RemoveAll(dest); err != nil {
		_ = os.RemoveAll(staging)
		return Skill{}, err
	}
	if err := os.Rename(staging, dest); err != nil {
		_ = os.RemoveAll(staging)
		return Skill{}, err
	}
	return inspect(filepath.Join(dest, "SKILL.md"), dest, "")
}

// Remove deletes an installed skill directory after confirming it actually
// is one.
func Remove(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		return fmt.Errorf("%s does not look like a skill directory: %w", dir, err)
	}
	return os.RemoveAll(dir)
}

// SetDisabled switches a skill off (or back on) by managing its marker file.
func SetDisabled(dir string, disabled bool) error {
	marker := filepath.Join(dir, DisabledMarker)
	if disabled {
		return os.WriteFile(marker, []byte("disabled by collo skills disable\n"), 0o644)
	}
	err := os.Remove(marker)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func samePath(a, b string) (bool, error) {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false, err
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false, err
	}
	return ra == rb, nil
}

// copySkillTree copies regular files (preserving the executable bit) and
// directories, skipping VCS metadata and refusing symlinks and oversized
// trees so an install cannot smuggle links outside the tree.
func copySkillTree(src, dest string) error {
	count := 0
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() && (name == ".git" || name == "node_modules" || name == "__pycache__") {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("refusing to install non-regular file %s (symlinks are not allowed in skills)", path)
		}
		if count++; count > maxInstallFiles {
			return fmt.Errorf("skill has more than %d files; refusing to install", maxInstallFiles)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dest string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	mode := os.FileMode(0o644)
	if info.Mode()&0o111 != 0 {
		mode = 0o755
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Describe renders one skill for `collo skills show`.
func Describe(s Skill) string {
	var b strings.Builder
	fmt.Fprintf(&b, "name:        %s\n", s.Name)
	fmt.Fprintf(&b, "description: %s\n", s.Description)
	fmt.Fprintf(&b, "source:      %s\n", orUnknown(s.Source))
	if s.Version != "" {
		fmt.Fprintf(&b, "version:     %s\n", s.Version)
	}
	if s.License != "" {
		fmt.Fprintf(&b, "license:     %s\n", s.License)
	}
	if len(s.AllowedTools) > 0 {
		fmt.Fprintf(&b, "allowed-tools: %s\n", strings.Join(s.AllowedTools, ", "))
	}
	for key, value := range s.Metadata {
		if key != "version" {
			fmt.Fprintf(&b, "metadata.%s: %s\n", key, value)
		}
	}
	fmt.Fprintf(&b, "path:        %s\n", s.Path)
	fmt.Fprintf(&b, "sha256:      %s\n", s.Hash)
	if s.Disabled {
		b.WriteString("status:      disabled (collo skills enable " + s.Name + " to re-enable)\n")
	}
	for _, group := range []struct {
		label string
		files []string
	}{{"scripts", s.Scripts}, {"references", s.References}, {"assets", s.Assets}} {
		if len(group.files) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s:\n", group.label)
		for _, f := range group.files {
			fmt.Fprintf(&b, "  %s\n", f)
		}
	}
	for _, issue := range s.Issues {
		fmt.Fprintf(&b, "warning:     %s\n", issue)
	}
	return b.String()
}

func orUnknown(s string) string {
	if s == "" {
		return "unpackaged"
	}
	return s
}
