package skills

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxSkillBytes = 512 * 1024

type Skill struct {
	Name        string
	Description string
	Path        string
}

type Catalog struct {
	Skills []Skill
}

// Discover finds skills. Project-provided skills are only included when the
// workspace is trusted (includeProject); user-level skills always load.
func Discover(workspace string, includeProject bool) (Catalog, error) {
	var paths []string
	var roots []string
	if includeProject {
		paths = []string{
			filepath.Join(workspace, "SKILLS.md"), filepath.Join(workspace, "skills.md"),
			filepath.Join(workspace, ".collomia", "SKILLS.md"), filepath.Join(workspace, ".collomia", "skills.md"),
		}
		roots = []string{filepath.Join(workspace, ".collomia", "skills"), filepath.Join(workspace, ".agents", "skills")}
	}
	if dir, err := os.UserConfigDir(); err == nil {
		roots = append(roots, filepath.Join(dir, "collomia", "skills"))
	}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Catalog{}, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				paths = append(paths, filepath.Join(root, entry.Name(), "SKILL.md"))
			}
		}
	}
	seen := map[string]bool{}
	var found []Skill
	for _, path := range paths {
		abs, _ := filepath.Abs(path)
		if seen[abs] {
			continue
		}
		seen[abs] = true
		skill, err := inspect(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Catalog{}, fmt.Errorf("inspect skill %s: %w", path, err)
		}
		found = append(found, skill)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	return Catalog{Skills: found}, nil
}

func inspect(path string) (Skill, error) {
	f, err := os.Open(path)
	if err != nil {
		return Skill{}, err
	}
	defer f.Close()
	reader := bufio.NewReader(io.LimitReader(f, 16*1024))
	name := strings.TrimSuffix(filepath.Base(filepath.Dir(path)), filepath.Ext(filepath.Base(filepath.Dir(path))))
	if strings.EqualFold(filepath.Base(path), "skills.md") {
		name = "workspace"
	}
	description := ""
	first, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return Skill{}, err
	}
	if strings.TrimSpace(first) == "---" {
		for {
			line, e := reader.ReadString('\n')
			trimmed := strings.TrimSpace(line)
			if trimmed == "---" || e == io.EOF {
				break
			}
			key, value, ok := strings.Cut(trimmed, ":")
			if ok {
				switch strings.TrimSpace(strings.ToLower(key)) {
				case "name":
					name = strings.Trim(strings.TrimSpace(value), `"'`)
				case "description":
					description = strings.Trim(strings.TrimSpace(value), `"'`)
				}
			}
		}
	} else {
		description = strings.TrimSpace(strings.TrimLeft(first, "# "))
	}
	if name == "" {
		name = filepath.Base(filepath.Dir(path))
	}
	if description == "" {
		description = "Reusable instructions from " + filepath.Base(path)
	}
	abs, _ := filepath.Abs(path)
	return Skill{Name: name, Description: description, Path: abs}, nil
}

func (c Catalog) Summary() string {
	if len(c.Skills) == 0 {
		return "No skills discovered."
	}
	var b strings.Builder
	b.WriteString("Available skills (load only when relevant):\n")
	for _, s := range c.Skills {
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Description)
	}
	return b.String()
}
func (c Catalog) Names() []string {
	names := make([]string, len(c.Skills))
	for i, s := range c.Skills {
		names[i] = s.Name
	}
	return names
}
func (c Catalog) Load(name string) (string, error) {
	for _, s := range c.Skills {
		if s.Name == name {
			data, err := os.ReadFile(s.Path)
			if err != nil {
				return "", err
			}
			if len(data) > maxSkillBytes {
				return "", fmt.Errorf("skill %s exceeds %d bytes", name, maxSkillBytes)
			}
			return string(data), nil
		}
	}
	return "", fmt.Errorf("unknown skill %q", name)
}

func ProjectInstructions(workspace string) (string, error) {
	names := []string{"AGENTS.md", "COLLOMIA.md"}
	var sections []string
	for _, name := range names {
		path := filepath.Join(workspace, name)
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if len(data) > maxSkillBytes {
			return "", fmt.Errorf("%s exceeds %d bytes", path, maxSkillBytes)
		}
		sections = append(sections, fmt.Sprintf("# Project instructions from %s\n%s", name, string(data)))
	}
	return strings.Join(sections, "\n\n"), nil
}

// GlobalInstructions reads the user-level AGENTS.md (or COLLOMIA.md) from
// the collomia configuration directory. It applies to every workspace and
// precedes project instructions, so a project can refine or override it.
func GlobalInstructions() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", nil
	}
	for _, name := range []string{"AGENTS.md", "COLLOMIA.md"} {
		path := filepath.Join(dir, "collomia", name)
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if len(data) > maxSkillBytes {
			return "", fmt.Errorf("%s exceeds %d bytes", path, maxSkillBytes)
		}
		return fmt.Sprintf("# Global user instructions from %s\n(Project instructions, when present, take precedence over these.)\n%s", path, string(data)), nil
	}
	return "", nil
}
