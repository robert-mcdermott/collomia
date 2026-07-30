package main

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/secrets"
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

// A guard that matches source text across a line boundary must not depend on
// how the checkout was configured: .gitattributes pins LF, but a working copy
// predating it, or a file written by a Windows editor, still carries CRLF.
func normalizeNewlines(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}

func docFiles(t *testing.T) map[string]string {
	t.Helper()
	root := repoRoot(t)
	out := map[string]string{}
	for _, name := range []string{"README.md", "ROADMAP.md", "SECURITY.md"} {
		if data, err := os.ReadFile(filepath.Join(root, name)); err == nil {
			out[name] = normalizeNewlines(string(data))
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
			out["docs/"+entry.Name()] = normalizeNewlines(string(data))
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
		// The declaration is matched with flexible whitespace: a tool whose
		// description is long enough to wrap its literal onto several lines is
		// still a tool, and a guard that only saw the one-line form would let
		// exactly the biggest tools go undocumented.
		for _, m := range regexp.MustCompile(`ToolDefinition\{\s*Name:\s*"([a-z_]+)"`).FindAllStringSubmatch(string(data), -1) {
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

// Install instructions are copy-pasted, so a version literal in them is a
// maintenance obligation: it names a real download that has to keep existing,
// and it goes stale the moment VERSION moves.
//
// This guard used to accept any literal that matched a tag in the checkout.
// That made it pass on a developer machine, where every tag is present, and
// fail only in the release pipeline, whose `qualify` job checks out a single
// tag shallowly — so the allowed set collapsed to VERSION alone and a
// correct-looking guide failed the release. It was a release-time surprise
// twice.
//
// The rule is therefore the stronger and tag-independent one: the install
// guide carries no concrete versions at all, only `vX.Y.Z` placeholders the
// reader substitutes. Nothing can go stale, and this fails on an ordinary
// branch push rather than during a release.
func TestInstallGuideCitesNoConcreteVersions(t *testing.T) {
	root := repoRoot(t)
	guide, err := os.ReadFile(filepath.Join(root, "docs", "INSTALLING.md"))
	if err != nil {
		t.Fatal(err)
	}
	found := regexp.MustCompile(`v\d+\.\d+\.\d+(?:-[a-z0-9.]+)?`).FindAllString(string(guide), -1)
	if len(found) > 0 {
		t.Errorf("docs/INSTALLING.md cites concrete version(s) %v; use the vX.Y.Z placeholder instead so install examples cannot go stale when VERSION moves", unique(found))
	}
	// A guide with neither a literal nor a placeholder would satisfy the check
	// above by having lost its pinning instructions entirely.
	if !strings.Contains(string(guide), "vX.Y.Z") {
		t.Error("docs/INSTALLING.md no longer shows how to pin a version; the vX.Y.Z placeholder examples are missing")
	}
}

func unique(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
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
	text := normalizeNewlines(string(data))
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

// Unlike the sandbox roots above, internal/secrets compiles on every platform,
// so this guard calls the implementation instead of scraping its source.
func TestGuideListsEveryProtectedCredentialLocation(t *testing.T) {
	guide := docFiles(t)["docs/USER_GUIDE.md"]
	locations := secrets.Locations()
	if len(locations) < 30 {
		t.Fatalf("found only %d credential locations; the tables shrank and this guard needs updating", len(locations))
	}
	for _, location := range locations {
		if !strings.Contains(guide, "`"+location+"`") {
			t.Errorf("credential protection covers %s but the user guide does not list it", location)
		}
	}
	exempt := secrets.ExemptLocations()
	if len(exempt) < 8 {
		t.Fatalf("found only %d exempt locations; the exclusions shrank and this guard needs updating", len(exempt))
	}
	for _, location := range exempt {
		if !strings.Contains(guide, "`"+location+"`") {
			t.Errorf("%s is exempt from credential protection but the user guide does not say so", location)
		}
	}
}

// The reverse of TestEveryContainmentSettingIsIntroducedInTheReadme: no
// document may name a `permissions.<field>` that configuration validation
// would reject.
//
// The forward guard only checks that every real setting is mentioned
// somewhere, which cannot catch a setting that was written about but never
// built. docs/FEATURES.md claimed audit writes could be made mandatory by
// policy and listed "audit requirements" among the monotonically clamped
// containment fields; neither has ever existed. That is the same shape as the
// `host` matcher that shipped inert and the install guide that cited a
// nonexistent version — a documented control a reader would rely on.
func TestDocumentedPermissionSettingsAllExist(t *testing.T) {
	real := map[string]bool{}
	permissions := reflect.TypeOf(appconfig.Permissions{})
	for i := range permissions.NumField() {
		tag := permissions.Field(i).Tag.Get("json")
		if name, _, _ := strings.Cut(tag, ","); name != "" && name != "-" {
			real[name] = true
		}
	}
	if len(real) < 10 {
		t.Fatalf("found only %d permission fields; the struct shape changed and this guard needs updating", len(real))
	}
	reference := regexp.MustCompile(`permissions\.([a-z_]+)`)
	for name, text := range docFiles(t) {
		for _, m := range reference.FindAllStringSubmatch(text, -1) {
			if !real[m[1]] {
				t.Errorf("%s refers to permissions.%s, which is not a real configuration field", name, m[1])
			}
		}
	}
}

// The audit ledger's `source` field is what answers "why was this allowed?",
// so the guide's table of its values must be complete.
//
// This guard exists because the first version of that table was written from
// the stale comment on audit.Entry and listed six of the thirteen values the
// permission layer actually emits — a documented-but-wrong enumeration, which
// is the same class of defect as a documented-but-absent control.
func TestGuideDocumentsEveryAuditDecisionSource(t *testing.T) {
	root := repoRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "internal", "permission", "permission.go"))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, m := range regexp.MustCompile(`Source: *"([a-z-]+)"`).FindAllStringSubmatch(string(source), -1) {
		found[m[1]] = true
	}
	if len(found) < 10 {
		t.Fatalf("found only %d decision sources; the permission layer's shape changed and this guard needs updating", len(found))
	}
	guide := docFiles(t)["docs/USER_GUIDE.md"]
	for value := range found {
		if !strings.Contains(guide, "| `"+value+"` |") {
			t.Errorf("the permission layer records source %q but the user guide's audit table does not list it", value)
		}
	}
}

// Every selectable setting must be documented, so a new one cannot ship
// unexplained.
func TestGuideDocumentsEveryCredentialSetting(t *testing.T) {
	guide := docFiles(t)["docs/USER_GUIDE.md"]
	for _, setting := range appconfig.ProtectCredentialsSettings() {
		if !strings.Contains(guide, "`"+setting+"`") {
			t.Errorf("permissions.protect_credentials accepts %q but the user guide does not document it", setting)
		}
	}
}

// Command-shaped actions must be built in exactly one place.
//
// This guard exists because the bug it prevents has now happened twice: the
// `host` matcher shipped documented, validated, and never populated, and
// `collo policy check` later reported the wrong decision for a
// credential-reaching command because it assembled its own tools.Action and
// silently missed a field. Both were second construction sites. A new field on
// shell.Analysis should require editing one function.
func TestCommandActionsAreBuiltInOnePlace(t *testing.T) {
	root := repoRoot(t)
	var offenders []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); name == ".git" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		// The single sanctioned constructor.
		if filepath.ToSlash(strings.TrimPrefix(path, root)) == "/internal/tools/command.go" {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, marker := range []string{"Risk: RiskExecute", "Risk: tools.RiskExecute", "Risk:              RiskExecute"} {
			if strings.Contains(string(body), marker) {
				offenders = append(offenders, filepath.ToSlash(strings.TrimPrefix(path, root)))
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("these files build a command action by hand instead of calling tools.ActionFromAnalysis: %s", strings.Join(offenders, ", "))
	}
}

// Every containment setting must be explained where a user actually looks.
//
// This guard exists because permissions.protect_credentials shipped fully
// documented in the user guide, security model, compatibility policy, starter
// reference, and capability matrix — and was missed in the README, which is
// where the rest of the containment surface is introduced. No existing check
// covered that file for configuration coverage.
func TestEveryContainmentSettingIsIntroducedInTheReadme(t *testing.T) {
	docs := docFiles(t)
	fields := appconfig.ContainmentFields()
	if len(fields) < 7 {
		t.Fatalf("found only %d containment fields; the set shrank and this guard needs updating", len(fields))
	}
	for _, source := range []string{"README.md", "docs/USER_GUIDE.md"} {
		for _, field := range fields {
			if !strings.Contains(docs[source], field) {
				t.Errorf("%s does not mention the containment setting %q", source, field)
			}
		}
	}
}

// Whether a tool-wide "always allow" is available must be decided in one
// place.
//
// The approval dialog and the key handler each carried their own copy of this
// rule. When credential stores were added, both copies went stale: the dialog
// offered "Always" for a private key and the permission layer then declined to
// record it, so the user pressed a button that did nothing and was asked again
// on the next identical action. Request.AllowsAlways is now the only answer.
func TestAlwaysAvailabilityIsDecidedInOnePlace(t *testing.T) {
	root := repoRoot(t)
	var offenders []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); name == ".git" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		// The permission package owns the rule and is allowed to state it.
		if strings.HasPrefix(filepath.ToSlash(strings.TrimPrefix(path, root)), "/internal/permission/") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		// Re-deriving the rule means reading these fields together outside the
		// permission package; asking Request.AllowsAlways needs neither.
		text := string(body)
		if strings.Contains(text, "ConfirmReasons) > 0") && strings.Contains(text, "Uninspectable") {
			offenders = append(offenders, filepath.ToSlash(strings.TrimPrefix(path, root)))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("these files re-derive whether a persistent grant is available instead of reading permission.Request.AllowsAlways: %s", strings.Join(offenders, ", "))
	}
}

// The capability matrix is generated from capabilityMatrix(), but nothing
// stopped the generated file from going stale: the build-identity detail added
// to `replay --check` never reached docs/CAPABILITIES.md, and the whole suite
// stayed green for a release window with the published matrix understating what
// the binary did. This is the same defect shape the RunResult-versus-schema
// sync test exists for, so it gets the same treatment.
//
// Two rows describe what the host platform can actually enforce and therefore
// differ by GOOS on purpose — a matrix claiming Seatbelt on Linux would be the
// over-claim their per-platform design exists to avoid. Those rows are checked
// for presence and status rather than byte equality, so the guard runs
// everywhere while still catching a row that was added, removed, renamed, or
// silently reworded.
func TestCapabilityMatrixDocIsRegenerated(t *testing.T) {
	root := repoRoot(t)
	published, err := os.ReadFile(filepath.Join(root, "docs", "CAPABILITIES.md"))
	if err != nil {
		t.Fatal(err)
	}
	platformDependent := map[string]bool{"OS sandbox": true, "scoped egress broker": true}

	rowKey := func(line string) (string, bool) {
		fields := strings.Split(strings.Trim(line, "|"), " | ")
		if len(fields) != 4 || strings.TrimSpace(fields[0]) == "Area" || strings.HasPrefix(strings.TrimSpace(fields[0]), "---") {
			return "", false
		}
		return strings.TrimSpace(fields[1]), true
	}
	index := func(text string) map[string]string {
		rows := map[string]string{}
		for _, line := range strings.Split(normalizeNewlines(text), "\n") {
			if !strings.HasPrefix(line, "| ") {
				continue
			}
			if key, ok := rowKey(line); ok {
				rows[key] = line
			}
		}
		return rows
	}

	want, got := index(capabilityMarkdown()), index(string(published))
	if len(want) < 30 {
		t.Fatalf("parsed only %d generated rows; the matrix shape changed and this guard needs updating", len(want))
	}
	for capability, wantRow := range want {
		gotRow, ok := got[capability]
		if !ok {
			t.Errorf("capability %q is in the matrix but missing from docs/CAPABILITIES.md; run `collo capabilities --markdown > docs/CAPABILITIES.md`", capability)
			continue
		}
		if platformDependent[capability] {
			// Compare the status column only; the notes name this GOOS.
			wantStatus := strings.Split(wantRow, " | ")[2]
			gotStatus := strings.Split(gotRow, " | ")[2]
			if wantStatus != gotStatus && gotStatus != "experimental" && gotStatus != "unsupported" {
				t.Errorf("capability %q has status %q, which is not a value this platform-dependent row may take", capability, gotStatus)
			}
			continue
		}
		if wantRow != gotRow {
			t.Errorf("docs/CAPABILITIES.md is stale for %q; run `collo capabilities --markdown > docs/CAPABILITIES.md`\n published: %s\n generated: %s", capability, gotRow, wantRow)
		}
	}
	for capability := range got {
		if _, ok := want[capability]; !ok {
			t.Errorf("docs/CAPABILITIES.md carries %q, which the matrix no longer produces", capability)
		}
	}
}
