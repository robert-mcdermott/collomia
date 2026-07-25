package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// These tests keep the documentation honest about the surfaces users script
// against. They exist because a documented-but-absent control (a `host` rule
// no tool populated) and a documented-but-nonexistent install version both
// shipped unnoticed: prose review does not catch that class of error, and a
// diff against the source does.

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("repository root not available: %v", err)
	}
	return root
}

func docFiles(t *testing.T) map[string]string {
	t.Helper()
	root := repoRoot(t)
	out := map[string]string{}
	for _, name := range []string{"README.md", "ROADMAP.md", "SECURITY.md"} {
		if data, err := os.ReadFile(filepath.Join(root, name)); err == nil {
			out[name] = string(data)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "docs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".md") {
			data, err := os.ReadFile(filepath.Join(root, "docs", entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			out["docs/"+entry.Name()] = string(data)
		}
	}
	return out
}

var markdownLink = regexp.MustCompile(`\[[^\]]*\]\(([^)#\s]+)(#[^)\s]*)?\)`)

// A relative link that does not resolve is a broken document, and the docs
// cross-reference each other heavily.
func TestDocumentationLinksResolve(t *testing.T) {
	root := repoRoot(t)
	for name, text := range docFiles(t) {
		for _, match := range markdownLink.FindAllStringSubmatch(text, -1) {
			target := match[1]
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			resolved := filepath.Join(root, filepath.Dir(name), target)
			if _, err := os.Stat(resolved); err != nil {
				t.Errorf("%s links to %q, which does not exist", name, target)
			}
		}
	}
}

// Headless consumers switch on event kinds, so every kind the binary can emit
// must be described somewhere a reader can find it.
func TestEveryEventKindIsDocumented(t *testing.T) {
	root := repoRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "internal", "event", "event.go"))
	if err != nil {
		t.Fatal(err)
	}
	kinds := regexp.MustCompile(`Kind\s*=\s*"([a-z.]+)"`).FindAllStringSubmatch(string(source), -1)
	if len(kinds) < 15 {
		t.Fatalf("found only %d event kinds; the declaration shape changed and this guard needs updating", len(kinds))
	}
	docs := docFiles(t)
	corpus := docs["README.md"] + docs["docs/AUTOMATION.md"] + docs["docs/USER_GUIDE.md"]
	for _, kind := range kinds {
		if !strings.Contains(corpus, kind[1]) {
			t.Errorf("event kind %q is emitted but never documented", kind[1])
		}
	}
}

// A tool the model can call should be findable in the documentation.
func TestEveryToolNameIsDocumented(t *testing.T) {
	root := repoRoot(t)
	names := map[string]bool{}
	err := filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, m := range regexp.MustCompile(`ToolDefinition\{Name: "([a-z_]+)"`).FindAllStringSubmatch(string(data), -1) {
			names[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) < 15 {
		t.Fatalf("found only %d tools; the registration shape changed and this guard needs updating", len(names))
	}
	docs := docFiles(t)
	corpus := docs["README.md"] + docs["docs/USER_GUIDE.md"] + docs["docs/AUTOMATION.md"] + docs["docs/CAPABILITIES.md"]
	for name := range names {
		if !strings.Contains(corpus, name) {
			t.Errorf("tool %q is registered but never documented", name)
		}
	}
}

// Every slash command the TUI accepts should be discoverable, either in the
// command palette or in the documentation. `/resume` was neither.
func TestEverySlashCommandIsDiscoverable(t *testing.T) {
	root := repoRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "internal", "tui", "slash.go"))
	if err != nil {
		t.Fatal(err)
	}
	commands := map[string]bool{}
	for _, m := range regexp.MustCompile(`"(/[a-z]+)"`).FindAllStringSubmatch(string(source), -1) {
		commands[m[1]] = true
	}
	if len(commands) < 20 {
		t.Fatalf("found only %d slash commands; the dispatch shape changed and this guard needs updating", len(commands))
	}
	palette, err := os.ReadFile(filepath.Join(root, "internal", "tui", "palette.go"))
	if err != nil {
		t.Fatal(err)
	}
	docs := docFiles(t)
	corpus := string(palette) + docs["README.md"] + docs["docs/USER_GUIDE.md"]
	for command := range commands {
		if !strings.Contains(corpus, command) {
			t.Errorf("slash command %q is accepted but appears in neither the palette nor the documentation", command)
		}
	}
}

// Install instructions are copy-pasted. A version that was never released
// fails with a confusing download error, so every version literal in the
// install guide must be a tag this repository actually has.
func TestInstallGuideCitesReleasedVersionsOnly(t *testing.T) {
	root := repoRoot(t)
	guide, err := os.ReadFile(filepath.Join(root, "docs", "INSTALLING.md"))
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	released := map[string]bool{strings.TrimSpace(string(current)): true}
	// Tags are not available in every checkout (shallow clones, archives), so
	// treat the VERSION file as authoritative and accept anything at or below
	// it that the guide cites as an explicit older-version example.
	tags, err := os.ReadDir(filepath.Join(root, ".git", "refs", "tags"))
	if err == nil {
		for _, tag := range tags {
			released[tag.Name()] = true
		}
	} else {
		t.Skip("no tag information in this checkout")
	}
	for _, m := range regexp.MustCompile(`v\d+\.\d+\.\d+(?:-[a-z0-9.]+)?`).FindAllString(string(guide), -1) {
		if !released[m] {
			t.Errorf("docs/INSTALLING.md cites %s, which is not a released version (have: %v)", m, keys(released))
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The exact contents of the minimal environment and the read-confinement
// roots decide whether a user's build works, so the guide lists them
// verbatim. These guards read the source as text rather than calling the
// functions, because the sandbox roots live in build-tagged platform files
// that do not compile on every host.

func literalsAfter(t *testing.T, relPath, declaration string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), relPath))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	start := strings.Index(text, declaration)
	if start < 0 {
		t.Fatalf("%s no longer contains %q; this guard needs updating", relPath, declaration)
	}
	body := text[start:]
	// Read only to the end of the slice literal, so surrounding code (a
	// key+"="+value concatenation, a comment) contributes no false entries.
	if end := strings.Index(body, "}"); end > 0 {
		body = body[:end]
	}
	var out []string
	for _, m := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestGuideListsEveryMinimalEnvironmentVariable(t *testing.T) {
	kept := literalsAfter(t, filepath.Join("internal", "tools", "command.go"), "keep := []string{")
	if len(kept) < 15 {
		t.Fatalf("found only %d kept variables; minimalEnv's shape changed and this guard needs updating", len(kept))
	}
	guide := docFiles(t)["docs/USER_GUIDE.md"]
	for _, name := range kept {
		if !strings.Contains(guide, "`"+name+"`") {
			t.Errorf("minimal environment keeps %s but the user guide does not list it", name)
		}
	}
}

func TestGuideListsEveryReadableSystemRoot(t *testing.T) {
	guide := docFiles(t)["docs/USER_GUIDE.md"]
	for _, source := range []struct{ path, decl string }{
		{filepath.Join("internal", "sandbox", "sandbox_darwin.go"), "func darwinSystemReadableRoots() []string {\n\t// These roots"},
		{filepath.Join("internal", "sandbox", "sandbox_linux.go"), "func linuxSystemReadableRoots() []string {\n\t// Keep system"},
	} {
		roots := literalsAfter(t, source.path, source.decl)
		if len(roots) < 8 {
			t.Fatalf("%s yielded only %d roots; its shape changed and this guard needs updating", source.decl, len(roots))
		}
		for _, root := range roots {
			if !strings.Contains(guide, "`"+root+"`") {
				t.Errorf("%s permits reads under %s but the user guide does not list it", source.decl, root)
			}
		}
	}
}
