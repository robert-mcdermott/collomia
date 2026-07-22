package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/userconfig"
)

const (
	maxSkillBytes = 512 * 1024
	// DisabledMarker is the file whose presence in a skill directory keeps
	// the skill out of the model-visible catalog without deleting it.
	DisabledMarker = ".disabled"
	maxBundleFiles = 200
	maxNameLength  = 64
	maxDescription = 1024
)

// Skill is one discovered skill: the SKILL.md instructions plus any bundled
// scripts, references, and assets in its directory.
type Skill struct {
	Name        string
	Description string
	// Path is the SKILL.md (or legacy SKILLS.md) file.
	Path string
	// Dir is the skill's directory; empty for legacy single-file skills.
	Dir string
	// Source is "project" or "user".
	Source string
	// Version, License, and Metadata come from the YAML front matter
	// (version may live at the top level or under metadata.version).
	Version  string
	License  string
	Metadata map[string]string
	// AllowedTools is the skill author's declared tool expectation
	// (advisory: surfaced to the model, enforced by the permission engine
	// like any other tool use).
	AllowedTools []string
	// Scripts, References, and Assets are slash-separated paths relative to
	// Dir, discovered from the standard bundle directories.
	Scripts    []string
	References []string
	Assets     []string
	// Hash is the SHA-256 of SKILL.md, for change detection and inspection.
	Hash string
	// Disabled marks a skill excluded from the catalog by its marker file.
	Disabled bool
	// Issues lists validation problems; the skill still loads, but list and
	// show surface them.
	Issues []string

	frontmatterSeen bool
}

// HasFrontmatter reports whether SKILL.md carried YAML front matter.
func (s Skill) HasFrontmatter() bool { return s.frontmatterSeen }

type Catalog struct {
	// Skills are the active skills in deterministic name order.
	Skills []Skill
	// Disabled are skills present on disk but switched off.
	Disabled []Skill
	// Issues are catalog-level problems: shadowed duplicates and skills that
	// could not be inspected at all.
	Issues []string
}

var nameRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Discover finds skills with deterministic precedence: trusted project skills
// (.collomia/skills, .agents/skills, then legacy SKILLS.md files) shadow
// user-level skills (~/.collomia/skills) of the same name. Results are sorted
// by name.
func Discover(workspace string, includeProject bool) (Catalog, error) {
	type candidate struct {
		path, dir, source string
	}
	var candidates []candidate
	var issues []string
	addRoot := func(root, source string) {
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil {
			issues = append(issues, fmt.Sprintf("skills directory %s: %v", root, err))
			return
		}
		for _, entry := range entries {
			if entry.IsDir() {
				dir := filepath.Join(root, entry.Name())
				candidates = append(candidates, candidate{path: filepath.Join(dir, "SKILL.md"), dir: dir, source: source})
			}
		}
	}
	if includeProject {
		addRoot(filepath.Join(workspace, ".collomia", "skills"), "project")
		addRoot(filepath.Join(workspace, ".agents", "skills"), "project")
		for _, name := range []string{"SKILLS.md", "skills.md"} {
			candidates = append(candidates, candidate{path: filepath.Join(workspace, name), source: "project"})
			candidates = append(candidates, candidate{path: filepath.Join(workspace, ".collomia", name), source: "project"})
		}
	}
	if dir, err := userconfig.Dir(); err == nil {
		addRoot(filepath.Join(dir, "skills"), "user")
	}
	seenPath := map[string]bool{}
	byName := map[string]string{}
	var cat Catalog
	cat.Issues = issues
	for _, c := range candidates {
		abs, _ := filepath.Abs(c.path)
		if seenPath[abs] {
			continue
		}
		seenPath[abs] = true
		skill, err := inspect(c.path, c.dir, c.source)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			cat.Issues = append(cat.Issues, fmt.Sprintf("skill %s: %v", c.path, err))
			continue
		}
		if prior, taken := byName[skill.Name]; taken {
			cat.Issues = append(cat.Issues, fmt.Sprintf("skill %q from %s is shadowed by %s", skill.Name, skill.Path, prior))
			continue
		}
		byName[skill.Name] = skill.Path
		if skill.Disabled {
			cat.Disabled = append(cat.Disabled, skill)
			continue
		}
		cat.Skills = append(cat.Skills, skill)
	}
	sort.Slice(cat.Skills, func(i, j int) bool { return cat.Skills[i].Name < cat.Skills[j].Name })
	sort.Slice(cat.Disabled, func(i, j int) bool { return cat.Disabled[i].Name < cat.Disabled[j].Name })
	return cat, nil
}

// Inspect parses one skill file for lifecycle commands and installs; dir may
// be empty for legacy single-file skills.
func Inspect(path, dir, source string) (Skill, error) { return inspect(path, dir, source) }

func inspect(path, dir, source string) (Skill, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Skill{}, err
	}
	if info.Size() > maxSkillBytes {
		return Skill{}, fmt.Errorf("SKILL.md is %d bytes; the limit is %d", info.Size(), maxSkillBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	skill := Skill{Path: path, Dir: dir, Source: source}
	if abs, absErr := filepath.Abs(path); absErr == nil {
		skill.Path = abs
	}
	if dir != "" {
		if abs, absErr := filepath.Abs(dir); absErr == nil {
			skill.Dir = abs
		}
	}
	sum := sha256.Sum256(data)
	skill.Hash = hex.EncodeToString(sum[:])
	dirName := ""
	if dir != "" {
		dirName = filepath.Base(dir)
	}
	fmLines, body, hasFM := splitFrontmatter(string(data))
	skill.frontmatterSeen = hasFM
	if hasFM {
		fm := parseFrontmatter(fmLines)
		skill.Name = fm.scalars["name"]
		skill.Description = fm.scalars["description"]
		skill.License = fm.scalars["license"]
		skill.Version = fm.scalars["version"]
		skill.Metadata = fm.maps["metadata"]
		if skill.Version == "" && skill.Metadata != nil {
			skill.Version = skill.Metadata["version"]
		}
		skill.AllowedTools = fm.lists["allowed-tools"]
		if len(skill.AllowedTools) == 0 {
			if raw := fm.scalars["allowed-tools"]; raw != "" {
				skill.AllowedTools = strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' })
			}
		}
	} else {
		skill.Issues = append(skill.Issues, "SKILL.md has no YAML front matter; add a `name` and `description` block")
		skill.Description = firstHeading(body)
	}
	if skill.Name == "" {
		switch {
		case strings.EqualFold(filepath.Base(path), "skills.md"):
			skill.Name = "workspace"
		case dirName != "":
			skill.Name = dirName
		default:
			skill.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		if hasFM {
			skill.Issues = append(skill.Issues, "front matter is missing `name`; using "+skill.Name)
		}
	}
	if skill.Description == "" {
		if hasFM {
			skill.Issues = append(skill.Issues, "front matter is missing `description`; the model cannot judge when to use this skill")
		}
		skill.Description = "Reusable instructions from " + filepath.Base(path)
	}
	if !nameRE.MatchString(skill.Name) {
		skill.Issues = append(skill.Issues, fmt.Sprintf("name %q should be lowercase letters, digits, and hyphens", skill.Name))
	}
	if len(skill.Name) > maxNameLength {
		skill.Issues = append(skill.Issues, fmt.Sprintf("name is %d characters; the limit is %d", len(skill.Name), maxNameLength))
	}
	if len(skill.Description) > maxDescription {
		skill.Issues = append(skill.Issues, fmt.Sprintf("description is %d characters; the limit is %d — long descriptions bloat every system prompt", len(skill.Description), maxDescription))
	}
	if dirName != "" && hasFM && skill.Name != dirName {
		skill.Issues = append(skill.Issues, fmt.Sprintf("name %q does not match its directory %q", skill.Name, dirName))
	}
	if dir != "" {
		if _, statErr := os.Stat(filepath.Join(dir, DisabledMarker)); statErr == nil {
			skill.Disabled = true
		}
		skill.Scripts = bundleFiles(dir, "scripts")
		skill.References = bundleFiles(dir, "references")
		skill.Assets = bundleFiles(dir, "assets")
	}
	return skill, nil
}

func firstHeading(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return strings.TrimSpace(strings.TrimLeft(trimmed, "# "))
		}
	}
	return ""
}

// bundleFiles lists the files under one standard bundle directory, as
// slash-separated paths relative to the skill directory, capped for safety.
func bundleFiles(dir, sub string) []string {
	root := filepath.Join(dir, sub)
	var out []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		if len(out) >= maxBundleFiles {
			return errors.New("capped")
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// BundleCount is the total number of bundled supporting files.
func (s Skill) BundleCount() int { return len(s.Scripts) + len(s.References) + len(s.Assets) }

func (c Catalog) Summary() string {
	if len(c.Skills) == 0 {
		return "No skills discovered."
	}
	var b strings.Builder
	b.WriteString("Available skills (use load_skill only when a description matches the task; loaded skills may bundle scripts and reference files):\n")
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

// Restrict returns a catalog containing only the named active skills. An
// empty allowlist preserves the original catalog. The returned Issues list is
// retained so profile filtering never hides discovery/trust diagnostics from
// operators, while disabled skills remain unavailable.
func (c Catalog) Restrict(names []string) Catalog {
	if len(names) == 0 {
		return c
	}
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = true
	}
	filtered := Catalog{Issues: append([]string(nil), c.Issues...)}
	for _, skill := range c.Skills {
		if allowed[skill.Name] {
			filtered.Skills = append(filtered.Skills, skill)
		}
	}
	return filtered
}

// Find returns an active skill by name.
func (c Catalog) Find(name string) (Skill, bool) {
	for _, s := range c.Skills {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

// Load returns a skill and its full SKILL.md content.
func (c Catalog) Load(name string) (Skill, string, error) {
	skill, ok := c.Find(name)
	if !ok {
		for _, s := range c.Disabled {
			if s.Name == name {
				return Skill{}, "", fmt.Errorf("skill %q is disabled; enable it with `collo skills enable %s`", name, name)
			}
		}
		return Skill{}, "", fmt.Errorf("unknown skill %q", name)
	}
	data, err := os.ReadFile(skill.Path)
	if err != nil {
		return Skill{}, "", err
	}
	if len(data) > maxSkillBytes {
		return Skill{}, "", fmt.Errorf("skill %s exceeds %d bytes", name, maxSkillBytes)
	}
	return skill, string(data), nil
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
	dir, err := userconfig.Dir()
	if err != nil {
		return "", err
	}
	for _, name := range []string{"AGENTS.md", "COLLOMIA.md"} {
		path := filepath.Join(dir, name)
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
