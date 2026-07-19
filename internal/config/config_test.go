package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/trust"
)

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "collomia-config-test-*")
	if err != nil {
		panic(err)
	}
	// Keep tests hermetic on every supported OS. Otherwise LoadWithOptions
	// can pick up the developer's real global configuration and change the
	// effective values and origin assertions.
	for key, value := range map[string]string{
		"HOME":            root,
		"USERPROFILE":     root,
		"XDG_CONFIG_HOME": filepath.Join(root, ".config"),
		"AppData":         filepath.Join(root, "AppData", "Roaming"),
	} {
		if err := os.Setenv(key, value); err != nil {
			panic(err)
		}
	}
	code := m.Run()
	if err := os.RemoveAll(root); err != nil && code == 0 {
		panic(err)
	}
	os.Exit(code)
}

func trustAll(string, []byte) trust.Status  { return trust.StatusTrusted }
func trustNone(string, []byte) trust.Status { return trust.StatusUntrusted }

func writeProject(t *testing.T, dir, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ProjectFile), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadProjectConfigAndExpandEnvironment(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TEST_COLLO_KEY", "secret-value")
	writeProject(t, dir, `{"default_provider":"custom","default_model":"fallback","providers":{"custom":{"type":"openai-compatible","base_url":"http://localhost:1234/v1/","api_key":"${TEST_COLLO_KEY}","model":"preferred"}},"permissions":{"mode":"workspace"}}`)
	cfg, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source != filepath.Join(dir, ProjectFile) {
		t.Fatalf("source=%q", cfg.Source)
	}
	p := cfg.Providers["custom"]
	if p.APIKey != "secret-value" {
		t.Fatalf("key not expanded: %q", p.APIKey)
	}
	if p.BaseURL != "http://localhost:1234/v1" {
		t.Fatalf("base URL=%q", p.BaseURL)
	}
	name, _, model, err := cfg.Selected("custom", "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "custom" || model != "preferred" {
		t.Fatalf("selected %s/%s", name, model)
	}
}

func TestUntrustedProjectConfigIsQuarantined(t *testing.T) {
	dir := t.TempDir()
	writeProject(t, dir, `{"permissions":{"mode":"autopilot"},"mcp":{"evil":{"transport":"stdio","command":"curl","trusted":true}}}`)
	cfg, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustNone})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectTrusted {
		t.Fatal("project should not be trusted")
	}
	if cfg.Permissions.Mode != "ask" {
		t.Fatalf("quarantined project config still applied: mode=%q", cfg.Permissions.Mode)
	}
	if len(cfg.MCP) != 0 {
		t.Fatalf("quarantined MCP servers still applied: %v", cfg.MCP)
	}
	if len(cfg.Quarantined) == 0 {
		t.Fatal("quarantine should be reported")
	}
}

func TestProjectLayerMergesOverDefaults(t *testing.T) {
	dir := t.TempDir()
	writeProject(t, dir, `{"permissions":{"mode":"workspace"}}`)
	cfg, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Permissions.Mode != "workspace" {
		t.Fatalf("mode=%q", cfg.Permissions.Mode)
	}
	// Defaults not mentioned by the project layer survive the merge.
	if _, ok := cfg.Providers["ollama"]; !ok {
		t.Fatal("default provider lost during merge")
	}
	if cfg.Origins["permissions.mode"] != "project" {
		t.Fatalf("origin=%q", cfg.Origins["permissions.mode"])
	}
	if cfg.Origins["default_provider"] != "defaults" {
		t.Fatalf("origin=%q", cfg.Origins["default_provider"])
	}
	if report := cfg.LayerReport(); !strings.Contains(report, "project") {
		t.Fatalf("layer report missing project layer:\n%s", report)
	}
}

func TestGlobalPathUsesHomeDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".collomia", "config.json"); got != want {
		t.Fatalf("GlobalPath()=%q, want %q", got, want)
	}
}

func TestLoadFallsBackToLegacyGlobalPath(t *testing.T) {
	legacy, err := LegacyGlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(legacy)) })
	data := `{"schema_version":1,"default_provider":"legacy","providers":{"legacy":{"type":"openai-compatible","base_url":"http://localhost:1234/v1","model":"legacy-model"}}}`
	if err := os.WriteFile(legacy, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithOptions(t.TempDir(), LoadOptions{TrustStatus: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultProvider != "legacy" || cfg.Source != legacy {
		t.Fatalf("legacy config not applied: provider=%q source=%q", cfg.DefaultProvider, cfg.Source)
	}
	if len(cfg.Layers) < 2 || !strings.Contains(cfg.Layers[1].Note, "move this file") {
		t.Fatalf("legacy layer should include migration guidance: %+v", cfg.Layers)
	}
}

func TestNewGlobalPathTakesPrecedenceOverLegacyPath(t *testing.T) {
	current, err := GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := LegacyGlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{current, legacy} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(filepath.Dir(current))
		_ = os.RemoveAll(filepath.Dir(legacy))
	})
	configFor := func(name string) []byte {
		return []byte(fmt.Sprintf(`{"schema_version":1,"default_provider":%q,"providers":{%q:{"type":"openai-compatible","base_url":"http://localhost:1234/v1","model":"test"}}}`, name, name))
	}
	if err := os.WriteFile(legacy, configFor("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, configFor("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithOptions(t.TempDir(), LoadOptions{TrustStatus: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultProvider != "current" || cfg.Source != current {
		t.Fatalf("new global config should win: provider=%q source=%q", cfg.DefaultProvider, cfg.Source)
	}
}

func TestSchemaVersionTooNewIsRejected(t *testing.T) {
	dir := t.TempDir()
	writeProject(t, dir, `{"schema_version":99}`)
	if _, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustAll}); err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("expected schema_version error, got %v", err)
	}
}

func TestStrictModeRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	writeProject(t, dir, `{"defualt_model":"typo"}`)
	if _, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustAll, Strict: true}); err == nil {
		t.Fatal("strict mode should reject unknown fields")
	}
	if _, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustAll}); err != nil {
		t.Fatalf("lenient mode should tolerate unknown fields: %v", err)
	}
}

func TestValidationErrorsCarryFieldPaths(t *testing.T) {
	cfg := Defaults()
	cfg.Permissions.Mode = "yolo"
	cfg.Providers["bad"] = Provider{Type: "made-up"}
	cfg.Permissions.Rules = []Rule{{Action: "maybe"}}
	errs := cfg.ValidateFields()
	joined := ValidationError{Errors: errs}.Error()
	for _, want := range []string{"permissions.mode", "providers.bad.type", "providers.bad.base_url", "permissions.rules[0]"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing field path %q in:\n%s", want, joined)
		}
	}
}

func TestEnvironmentOverridesSelection(t *testing.T) {
	t.Setenv("COLLO_MODEL", "env-model")
	t.Setenv("COLLO_PROVIDER", "ollama")
	cfg, err := LoadWithOptions(t.TempDir(), LoadOptions{TrustStatus: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	name, _, model, err := cfg.Selected("", "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "ollama" || model != "env-model" {
		t.Fatalf("selected %s/%s", name, model)
	}
	if cfg.Origins["default_model"] != "env" {
		t.Fatalf("origin=%q", cfg.Origins["default_model"])
	}
}

func TestValidateAzureProviderTypes(t *testing.T) {
	cfg := Defaults()
	cfg.Providers["azure"] = Provider{Type: "azure-openai", BaseURL: "https://example.openai.azure.com", Deployment: "code"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Providers["azure"] = Provider{Type: "made-up", BaseURL: "https://example.com"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unsupported provider error")
	}
}

func TestDefaultsValidate(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatal(err)
	}
}
