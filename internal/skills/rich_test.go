package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const richSkill = `---
name: pdf-processing
description: >-
  Extracts text and tables from PDF files,
  fills forms, and merges documents.
license: MIT
allowed-tools: [read_file, run_command]
metadata:
  version: 1.2.0
  author: "Robert"
---

# PDF processing

Use the bundled scripts.
`

func TestFrontmatterRichParsing(t *testing.T) {
	home := t.TempDir()
	isolateUserDirectories(t)
	dir := writeSkill(t, home, "pdf-processing", richSkill)
	skill, err := inspect(filepath.Join(dir, "SKILL.md"), dir, "user")
	if err != nil {
		t.Fatal(err)
	}
	if skill.Name != "pdf-processing" {
		t.Fatalf("name=%q", skill.Name)
	}
	if want := "Extracts text and tables from PDF files, fills forms, and merges documents."; skill.Description != want {
		t.Fatalf("folded description=%q, want %q", skill.Description, want)
	}
	if skill.License != "MIT" || skill.Version != "1.2.0" {
		t.Fatalf("license=%q version=%q", skill.License, skill.Version)
	}
	if len(skill.AllowedTools) != 2 || skill.AllowedTools[0] != "read_file" || skill.AllowedTools[1] != "run_command" {
		t.Fatalf("allowed-tools=%v", skill.AllowedTools)
	}
	if skill.Metadata["author"] != "Robert" {
		t.Fatalf("metadata=%v", skill.Metadata)
	}
	if len(skill.Issues) != 0 {
		t.Fatalf("unexpected issues: %v", skill.Issues)
	}
	if !skill.HasFrontmatter() {
		t.Fatal("front matter should be detected")
	}
	if skill.Hash == "" {
		t.Fatal("hash missing")
	}
}

func TestFrontmatterBlockListAndLiteral(t *testing.T) {
	content := "---\nname: literal-skill\ndescription: |\n  Line one.\n  Line two.\nallowed-tools:\n  - read_file\n  - search_files\n---\nBody.\n"
	dir := writeSkill(t, t.TempDir(), "literal-skill", content)
	skill, err := inspect(filepath.Join(dir, "SKILL.md"), dir, "user")
	if err != nil {
		t.Fatal(err)
	}
	if skill.Description != "Line one.\nLine two." {
		t.Fatalf("literal block=%q", skill.Description)
	}
	if len(skill.AllowedTools) != 2 || skill.AllowedTools[1] != "search_files" {
		t.Fatalf("block list=%v", skill.AllowedTools)
	}
}

func TestFrontmatterPlainContinuation(t *testing.T) {
	content := "---\nname: contd\ndescription: A long description\n  that wraps onto the next line, with triggers like: build, test.\n---\nBody.\n"
	dir := writeSkill(t, t.TempDir(), "contd", content)
	skill, err := inspect(filepath.Join(dir, "SKILL.md"), dir, "user")
	if err != nil {
		t.Fatal(err)
	}
	if want := "A long description that wraps onto the next line, with triggers like: build, test."; skill.Description != want {
		t.Fatalf("continuation=%q", skill.Description)
	}
}

func TestValidationIssues(t *testing.T) {
	dir := writeSkill(t, t.TempDir(), "mismatch", "---\nname: Other_Name\ndescription: Something.\n---\nBody.\n")
	skill, err := inspect(filepath.Join(dir, "SKILL.md"), dir, "project")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(skill.Issues, "; ")
	if !strings.Contains(joined, "lowercase") {
		t.Fatalf("expected name-format issue, got %v", skill.Issues)
	}
	if !strings.Contains(joined, "does not match its directory") {
		t.Fatalf("expected mismatch issue, got %v", skill.Issues)
	}
	bare := writeSkill(t, t.TempDir(), "bare", "# Bare\nNo front matter here.\n")
	plain, err := inspect(filepath.Join(bare, "SKILL.md"), bare, "user")
	if err != nil {
		t.Fatal(err)
	}
	if len(plain.Issues) == 0 || !strings.Contains(plain.Issues[0], "front matter") {
		t.Fatalf("expected front-matter advisory, got %v", plain.Issues)
	}
	if plain.Name != "bare" || plain.Description != "Bare" {
		t.Fatalf("fallbacks: name=%q desc=%q", plain.Name, plain.Description)
	}
}

func TestProjectShadowsUserSkill(t *testing.T) {
	isolateUserDirectories(t)
	userDir, err := UserSkillsDir()
	if err != nil {
		t.Fatal(err)
	}
	writeSkill(t, userDir, "deploy", "---\nname: deploy\ndescription: User-level deploy skill.\n---\nUser body.\n")
	workspace := t.TempDir()
	writeSkill(t, ProjectSkillsDir(workspace), "deploy", "---\nname: deploy\ndescription: Project deploy skill.\n---\nProject body.\n")
	catalog, err := Discover(workspace, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Skills) != 1 {
		t.Fatalf("skills=%+v", catalog.Skills)
	}
	if catalog.Skills[0].Source != "project" || catalog.Skills[0].Description != "Project deploy skill." {
		t.Fatalf("project should win: %+v", catalog.Skills[0])
	}
	if len(catalog.Issues) != 1 || !strings.Contains(catalog.Issues[0], "shadowed") {
		t.Fatalf("expected shadow report, got %v", catalog.Issues)
	}
	// Untrusted: only the user skill remains.
	catalog, err = Discover(workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Skills) != 1 || catalog.Skills[0].Source != "user" {
		t.Fatalf("untrusted discovery=%+v", catalog.Skills)
	}
}

func TestDisabledMarker(t *testing.T) {
	isolateUserDirectories(t)
	userDir, err := UserSkillsDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSkill(t, userDir, "muted", "---\nname: muted\ndescription: A disabled skill.\n---\nBody.\n")
	if err := SetDisabled(dir, true); err != nil {
		t.Fatal(err)
	}
	catalog, err := Discover(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Skills) != 0 || len(catalog.Disabled) != 1 {
		t.Fatalf("active=%v disabled=%v", catalog.Skills, catalog.Disabled)
	}
	if _, _, err := catalog.Load("muted"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("load of disabled skill should explain itself, got %v", err)
	}
	if err := SetDisabled(dir, false); err != nil {
		t.Fatal(err)
	}
	catalog, err = Discover(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Skills) != 1 {
		t.Fatalf("re-enabled catalog=%+v", catalog)
	}
}

func TestBundleDiscoveryAndLoadedRendering(t *testing.T) {
	isolateUserDirectories(t)
	userDir, err := UserSkillsDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSkill(t, userDir, "bundled", richSkillNamed("bundled"))
	for _, path := range []string{"scripts/extract.py", "references/formats.md", "assets/template.html"} {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := Discover(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	skill, ok := catalog.Find("bundled")
	if !ok {
		t.Fatalf("catalog=%+v", catalog)
	}
	if len(skill.Scripts) != 1 || skill.Scripts[0] != "scripts/extract.py" {
		t.Fatalf("scripts=%v", skill.Scripts)
	}
	if skill.BundleCount() != 3 {
		t.Fatalf("bundle count=%d", skill.BundleCount())
	}
	loaded, content, err := catalog.Load("bundled")
	if err != nil {
		t.Fatal(err)
	}
	out := renderLoaded(loaded, content)
	for _, want := range []string{skill.Dir, "scripts/extract.py", "references/formats.md", "assets/template.html", "run_command", "read_file", "only these tools"} {
		if !strings.Contains(out, want) {
			t.Fatalf("loaded output missing %q:\n%s", want, out)
		}
	}
}

func richSkillNamed(name string) string {
	return "---\nname: " + name + "\ndescription: Bundled test skill.\nallowed-tools: [read_file, run_command]\n---\n\nUse the bundle.\n"
}

func TestLifecycleScaffoldInstallRemove(t *testing.T) {
	isolateUserDirectories(t)
	workspace := t.TempDir()
	root := ProjectSkillsDir(workspace)
	dir, err := Scaffold(root, "release-notes")
	if err != nil {
		t.Fatal(err)
	}
	skill, err := Inspect(filepath.Join(dir, "SKILL.md"), dir, "project")
	if err != nil {
		t.Fatal(err)
	}
	if skill.Name != "release-notes" || skill.Version != "0.1.0" {
		t.Fatalf("scaffolded skill=%+v", skill)
	}
	if _, err := Scaffold(root, "release-notes"); err == nil {
		t.Fatal("second scaffold should refuse to overwrite")
	}
	if _, err := Scaffold(root, "Bad Name"); err == nil {
		t.Fatal("invalid name should be rejected")
	}
	// Install the scaffolded skill into the user scope.
	userRoot, err := UserSkillsDir()
	if err != nil {
		t.Fatal(err)
	}
	installed, err := Install(dir, userRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	if installed.Dir != filepath.Join(userRoot, "release-notes") {
		t.Fatalf("installed at %q", installed.Dir)
	}
	if _, err := Install(dir, userRoot, false); err == nil || !strings.Contains(err.Error(), "already installed") {
		t.Fatalf("duplicate install should require replace, got %v", err)
	}
	if _, err := Install(dir, userRoot, true); err != nil {
		t.Fatalf("replace install failed: %v", err)
	}
	found, err := FindDir(workspace, "release-notes")
	if err != nil {
		t.Fatal(err)
	}
	if found != dir {
		t.Fatalf("FindDir should prefer the project copy, got %q want %q", found, dir)
	}
	if err := Remove(found); err != nil {
		t.Fatal(err)
	}
	found, err = FindDir(workspace, "release-notes")
	if err != nil {
		t.Fatal(err)
	}
	if found != filepath.Join(userRoot, "release-notes") {
		t.Fatalf("after project removal FindDir should fall back to user copy, got %q", found)
	}
}

func TestInstallRefusesSymlinks(t *testing.T) {
	src := writeSkill(t, t.TempDir(), "sneaky", "---\nname: sneaky\ndescription: Contains a symlink.\n---\nBody.\n")
	if err := os.Symlink("/etc/hosts", filepath.Join(src, "link")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if _, err := Install(src, t.TempDir(), false); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink refusal, got %v", err)
	}
}

func TestOversizedSkillReported(t *testing.T) {
	isolateUserDirectories(t)
	userDir, err := UserSkillsDir()
	if err != nil {
		t.Fatal(err)
	}
	big := "---\nname: big\ndescription: Too big.\n---\n" + strings.Repeat("x", maxSkillBytes)
	writeSkill(t, userDir, "big", big)
	catalog, err := Discover(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Skills) != 0 {
		t.Fatalf("oversized skill should be excluded, got %+v", catalog.Skills)
	}
	if len(catalog.Issues) != 1 || !strings.Contains(catalog.Issues[0], "limit") {
		t.Fatalf("issues=%v", catalog.Issues)
	}
}
