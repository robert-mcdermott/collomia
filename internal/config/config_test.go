package config

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/trust"
)

func TestDefaultRegexDenialsRemainNarrowBackstops(t *testing.T) {
	patterns := Defaults().Permissions.DeniedCommands
	matches := func(command string) bool {
		for _, pattern := range patterns {
			if regexp.MustCompile(pattern).MatchString(command) {
				return true
			}
		}
		return false
	}
	for _, command := range []string{"rm -rf /", `del /s /q C:\`} {
		if !matches(command) {
			t.Errorf("default regex backstop should match %q", command)
		}
	}
	for _, command := range []string{"git reset --hard", "shutdown now", "mkfs.ext4 ./test-disk.img", "rm -rf /tmp/example"} {
		if matches(command) {
			t.Errorf("%q should be handled by outcome classification, not an unconditional regex denial", command)
		}
	}
}

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "collomia-config-test-*")
	if err != nil {
		panic(err)
	}
	// Keep tests hermetic on every supported OS. Otherwise LoadWithOptions
	// can pick up the developer's real global configuration and change the
	// effective values and origin assertions.
	for key, value := range map[string]string{
		"HOME":        root,
		"USERPROFILE": root,
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
	if p.ConnectTimeoutSeconds != 10 || p.RequestTimeoutSeconds != 1800 || p.StreamIdleTimeoutSeconds != 300 {
		t.Fatalf("provider timeout defaults=%+v", p)
	}
	name, _, model, err := cfg.Selected("custom", "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "custom" || model != "preferred" {
		t.Fatalf("selected %s/%s", name, model)
	}
}

func TestLoadCanSkipEnvironmentExpansionForDiagnostics(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TEST_COLLO_SECRET", "resolved-secret")
	writeProject(t, dir, `{
  "default_provider":"custom",
  "default_model":"model",
  "providers":{"custom":{"type":"openai","base_url":"https://example.invalid/${TEST_COLLO_SECRET}/","api_key_env":"TEST_COLLO_SECRET","headers":{"X-Test":"${TEST_COLLO_SECRET}"},"model":"model"}},
  "mcp":{"fixture":{"transport":"stdio","command":"fixture","env":{"TOKEN":"${TEST_COLLO_SECRET}"}}}
}`)
	cfg, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustAll, SkipEnvironmentExpansion: true})
	if err != nil {
		t.Fatal(err)
	}
	provider := cfg.Providers["custom"]
	if provider.APIKey != "" || provider.BaseURL != "https://example.invalid/${TEST_COLLO_SECRET}" || provider.Headers["X-Test"] != "${TEST_COLLO_SECRET}" {
		t.Fatalf("provider environment unexpectedly resolved: %+v", provider)
	}
	if got := cfg.MCP["fixture"].Env["TOKEN"]; got != "${TEST_COLLO_SECRET}" {
		t.Fatalf("MCP environment unexpectedly resolved: %q", got)
	}
}

func TestValidateProviderTimeouts(t *testing.T) {
	cfg := Defaults()
	p := cfg.Providers["ollama"]
	p.ConnectTimeoutSeconds = -1
	p.RequestTimeoutSeconds = -2
	p.StreamIdleTimeoutSeconds = -3
	cfg.Providers["ollama"] = p
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected negative provider timeouts to fail validation")
	}
	for _, field := range []string{"connect_timeout_seconds", "request_timeout_seconds", "stream_idle_timeout_seconds"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("error %q missing %q", err, field)
		}
	}
}

func TestValidateSandboxWritableRoots(t *testing.T) {
	cfg := Defaults()
	cfg.Permissions.SandboxWritableRoots = []string{".cache", "  "}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "permissions.sandbox_writable_roots.1") {
		t.Fatalf("expected field-specific writable-root error, got %v", err)
	}
}

func TestValidateSandboxReadableRoots(t *testing.T) {
	cfg := Defaults()
	cfg.Permissions.SandboxReadableRoots = []string{".deps", "  "}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "permissions.sandbox_readable_roots.1") {
		t.Fatalf("expected field-specific readable-root error, got %v", err)
	}
}

func TestDefaultsKeepSandboxCommandNetworkAvailable(t *testing.T) {
	if !Defaults().Permissions.SandboxAllowNetwork {
		t.Fatal("compatibility default must keep sandboxed command networking available until explicitly disabled")
	}
}

func TestDefaultsEnableSandboxAuto(t *testing.T) {
	if got := Defaults().Permissions.Sandbox; got != "auto" {
		t.Fatalf("default sandbox=%q, want auto", got)
	}
}

func TestNormalizeMissingSandboxToAuto(t *testing.T) {
	var cfg Config
	cfg.normalize()
	if cfg.Permissions.Sandbox != "auto" {
		t.Fatalf("normalized sandbox=%q, want auto", cfg.Permissions.Sandbox)
	}
}

func TestDefaultsKeepSandboxCommandReadsBroad(t *testing.T) {
	if !Defaults().Permissions.SandboxAllowReadOutsideWorkspace {
		t.Fatal("compatibility default must keep sandboxed command reads broad until explicitly confined")
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
	if cfg.Permissions.Sandbox != "auto" {
		t.Fatalf("omitted project sandbox=%q, want inherited auto", cfg.Permissions.Sandbox)
	}
	if !cfg.Permissions.SandboxAllowReadOutsideWorkspace {
		t.Fatal("omitted project read policy must inherit the broad-read compatibility default")
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

func TestProjectCanOptIntoSandboxReadConfinement(t *testing.T) {
	dir := t.TempDir()
	writeProject(t, dir, `{"permissions":{"sandbox":"require","sandbox_allow_read_outside_workspace":false,"sandbox_readable_roots":["vendor-sdk"]}}`)
	cfg, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Permissions.SandboxAllowReadOutsideWorkspace {
		t.Fatal("explicit false read policy was not applied")
	}
	if len(cfg.Permissions.SandboxReadableRoots) != 1 || cfg.Permissions.SandboxReadableRoots[0] != "vendor-sdk" {
		t.Fatalf("readable roots=%v", cfg.Permissions.SandboxReadableRoots)
	}
}

// A repository cannot disable the sandbox, even by writing the field out
// explicitly and even once the workspace is trusted. Turning containment off
// is the machine owner's decision, made in their own configuration.
func TestProjectCannotDisableTheInheritedSandbox(t *testing.T) {
	dir := t.TempDir()
	writeProject(t, dir, `{"permissions":{"sandbox":"off"}}`)
	cfg, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Permissions.Sandbox != "auto" {
		t.Fatalf("project weakened the sandbox to %q", cfg.Permissions.Sandbox)
	}
	// Ignoring the setting silently would read as a bug, so the refusal is
	// reported.
	if len(cfg.Clamped) != 1 || cfg.Clamped[0].Field != "sandbox" {
		t.Fatalf("refusal not reported: %+v", cfg.Clamped)
	}
	if !strings.Contains(cfg.LayerReport(), "Refused project containment changes") {
		t.Fatalf("layer report does not explain the refusal:\n%s", cfg.LayerReport())
	}
}

// The global layer remains free to opt out: a built-in default is not a
// choice the user made, so it does not clamp their own configuration.
func TestGlobalLayerCanStillDisableTheSandbox(t *testing.T) {
	cfg := loadWithGlobal(t, `{"permissions":{"sandbox":"off"}}`, "")
	if cfg.Permissions.Sandbox != "off" {
		t.Fatalf("global sandbox=%q, want off", cfg.Permissions.Sandbox)
	}
	if len(cfg.Clamped) != 0 {
		t.Fatalf("global choice should not be clamped: %+v", cfg.Clamped)
	}
}

// Every containment switch follows the same rule, so the model is one
// sentence rather than a table of exceptions.
func TestProjectCannotWeakenAnyContainmentSetting(t *testing.T) {
	cfg := loadWithGlobal(t,
		`{"permissions":{"preset":"hardened","command_env":"minimal"}}`,
		`{"permissions":{"sandbox":"auto","command_env":"full","network":"open","commands":"open","sandbox_allow_read_outside_workspace":true,"allow_outside_workspace":true}}`)
	p := cfg.Permissions
	if p.Sandbox != "require" || p.CommandEnv != "minimal" || p.Network != "scoped" || p.Commands != "allowlist" {
		t.Fatalf("project weakened containment: %+v", p)
	}
	if p.SandboxAllowReadOutsideWorkspace || p.AllowOutsideWorkspace {
		t.Fatalf("project widened reads: %+v", p)
	}
	if len(cfg.Clamped) != 6 {
		t.Fatalf("expected every refusal reported, got %d: %+v", len(cfg.Clamped), cfg.Clamped)
	}
}

// Tightening from a project file stays available and is not reported as a
// refusal.
func TestProjectCanStillTightenContainment(t *testing.T) {
	cfg := loadWithGlobal(t, `{"permissions":{"sandbox":"auto"}}`, `{"permissions":{"sandbox":"require","network":"scoped"}}`)
	if cfg.Permissions.Sandbox != "require" || cfg.Permissions.Network != "scoped" {
		t.Fatalf("project could not tighten: %+v", cfg.Permissions)
	}
	if len(cfg.Clamped) != 0 {
		t.Fatalf("tightening should not be reported as refused: %+v", cfg.Clamped)
	}
}

func TestProjectOmissionPreservesExplicitGlobalSandboxOff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	global, err := GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(global), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(global, []byte(`{"permissions":{"sandbox":"off"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeProject(t, dir, `{"permissions":{"mode":"ask"}}`)
	cfg, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Permissions.Sandbox != "off" {
		t.Fatalf("inherited sandbox=%q, want explicit global off", cfg.Permissions.Sandbox)
	}
	if cfg.Origins["permissions.sandbox"] != "user" {
		t.Fatalf("sandbox origin=%q, want user", cfg.Origins["permissions.sandbox"])
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

func TestLoadAppliesGlobalPath(t *testing.T) {
	path, err := GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	data := `{"schema_version":1,"default_provider":"global","providers":{"global":{"type":"openai-compatible","base_url":"http://localhost:1234/v1","model":"test"}}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithOptions(t.TempDir(), LoadOptions{TrustStatus: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultProvider != "global" || cfg.Source != path {
		t.Fatalf("global config not applied: provider=%q source=%q", cfg.DefaultProvider, cfg.Source)
	}
}

func TestDeniedCommandsAreAdditiveAcrossConfigurationLayers(t *testing.T) {
	globalPath, err := GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(globalPath) })
	if err := os.WriteFile(globalPath, []byte(`{"permissions":{"denied_commands":["global-only","shared"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeProject(t, workspace, `{"permissions":{"denied_commands":["shared","project-only"]}}`)

	cfg, err := LoadWithOptions(workspace, LoadOptions{TrustStatus: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string(nil), Defaults().Permissions.DeniedCommands...)
	want = append(want, "global-only", "shared", "project-only")
	if !slices.Equal(cfg.Permissions.DeniedCommands, want) {
		t.Fatalf("denied commands=%q\nwant=%q", cfg.Permissions.DeniedCommands, want)
	}
}

func TestEmptyOrNullDeniedCommandsCannotRemoveInheritedDenials(t *testing.T) {
	globalPath, err := GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(globalPath) })
	if err := os.WriteFile(globalPath, []byte(`{"permissions":{"denied_commands":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeProject(t, workspace, `{"permissions":{"denied_commands":null}}`)

	cfg, err := LoadWithOptions(workspace, LoadOptions{TrustStatus: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	if want := Defaults().Permissions.DeniedCommands; !slices.Equal(cfg.Permissions.DeniedCommands, want) {
		t.Fatalf("empty subordinate lists removed defaults: got=%q want=%q", cfg.Permissions.DeniedCommands, want)
	}
}

func TestSchemaVersionTooNewIsRejected(t *testing.T) {
	dir := t.TempDir()
	writeProject(t, dir, `{"schema_version":99}`)
	if _, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustAll}); err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("expected schema_version error, got %v", err)
	}
}

func TestLegacyV1ConfigWithoutSchemaKeepsLenientCompatibility(t *testing.T) {
	dir := t.TempDir()
	writeProject(t, dir, `{
	  "default_model": "legacy-model",
	  "future_optional_field": {"ignored": true}
	}`)
	cfg, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchemaVersion != CurrentSchemaVersion || cfg.DefaultModel != "legacy-model" {
		t.Fatalf("legacy config=%+v", cfg)
	}
	if _, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustAll, Strict: true}); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("strict compatibility check error=%v", err)
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

func TestValidateAzureAuthenticationModes(t *testing.T) {
	for _, providerType := range []string{"azure-openai", "azure-foundry", "azure-foundry-anthropic"} {
		for _, auth := range []string{"", "api_key", "bearer", "entra"} {
			cfg := Defaults()
			cfg.Providers["azure"] = Provider{Type: providerType, Auth: auth, BaseURL: "https://example.services.ai.azure.com", Model: "deployment"}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("type=%s auth=%q should validate: %v", providerType, auth, err)
			}
		}
	}

	tests := []struct {
		name     string
		provider Provider
		field    string
	}{
		{"unknown auth", Provider{Type: "azure-openai", Auth: "managed", BaseURL: "https://example.openai.azure.com"}, "providers.azure.auth"},
		{"Entra with key", Provider{Type: "azure-openai", Auth: "entra", BaseURL: "https://example.openai.azure.com", APIKeyEnv: "AZURE_OPENAI_API_KEY"}, "providers.azure.api_key_env"},
		{"Entra with auth header", Provider{Type: "azure-foundry", Auth: "entra", BaseURL: "https://example.services.ai.azure.com", Headers: map[string]string{"Authorization": "Bearer value"}}, "providers.azure.headers.Authorization"},
		{"invalid scope", Provider{Type: "azure-foundry", Auth: "entra", BaseURL: "https://example.services.ai.azure.com", EntraScope: "https://ai.azure.com/token"}, "providers.azure.entra_scope"},
		{"invalid authority", Provider{Type: "azure-foundry", Auth: "entra", BaseURL: "https://example.services.ai.azure.com", EntraAuthorityHost: "http://login.example"}, "providers.azure.entra_authority_host"},
		{"unused Entra setting", Provider{Type: "azure-openai", BaseURL: "https://example.openai.azure.com", EntraTenantID: "tenant"}, "providers.azure.auth"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Providers["azure"] = test.provider
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("validation error=%v, want field %s", err, test.field)
			}
		})
	}
}

func TestNormalizeAzureEntraSettings(t *testing.T) {
	t.Setenv("COLLO_TEST_ENTRA_SCOPE", "https://ai.azure.com/.default")
	cfg := Defaults()
	cfg.Providers["azure"] = Provider{
		Type: " AZURE-FOUNDRY ", Auth: " EnTrA ", BaseURL: "https://example.services.ai.azure.com",
		EntraScope: " ${COLLO_TEST_ENTRA_SCOPE} ", EntraTenantID: " tenant-id ", EntraAuthorityHost: " https://login.microsoftonline.us/ ",
	}
	cfg.normalize()
	provider := cfg.Providers["azure"]
	if provider.Type != "azure-foundry" || provider.Auth != "entra" || provider.EntraScope != "https://ai.azure.com/.default" || provider.EntraTenantID != "tenant-id" || provider.EntraAuthorityHost != "https://login.microsoftonline.us/" {
		t.Fatalf("provider=%+v", provider)
	}
}

func TestValidateBedrockAuthenticationModes(t *testing.T) {
	for _, auth := range []string{"", "auto", "sigv4", "bearer"} {
		cfg := Defaults()
		cfg.Providers["bedrock"] = Provider{Type: "bedrock", Auth: auth, Model: "model"}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("auth %q should validate: %v", auth, err)
		}
	}

	cfg := Defaults()
	cfg.Providers["bedrock"] = Provider{Type: "bedrock", Auth: "basic", Model: "model"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "providers.bedrock.auth") {
		t.Fatalf("invalid auth error=%v", err)
	}

	cfg.Providers["bedrock"] = Provider{Type: "bedrock", Auth: "sigv4", APIKeyEnv: "AWS_BEARER_TOKEN_BEDROCK", Model: "model"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "providers.bedrock.api_key_env") {
		t.Fatalf("ignored bearer setting error=%v", err)
	}

	cfg.Providers["bedrock"] = Provider{Type: "bedrock", Auth: "bearer", Profile: "development", Model: "model"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "providers.bedrock.profile") {
		t.Fatalf("ignored profile error=%v", err)
	}
}

func TestNormalizeBedrockAuthenticationMode(t *testing.T) {
	cfg := Defaults()
	cfg.Providers["bedrock"] = Provider{Type: " BEDROCK ", Auth: " BeArEr ", Model: "model"}
	cfg.normalize()
	provider := cfg.Providers["bedrock"]
	if provider.Type != "bedrock" || provider.Auth != "bearer" {
		t.Fatalf("provider=%+v", provider)
	}
}

func TestDefaultsValidate(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateDelegatedAgentGovernance(t *testing.T) {
	cfg := Defaults()
	cfg.Agents["reviewer"] = AgentDefinition{
		TokenBudget: 1000, TimeoutSeconds: 30, Skills: []string{"security-review"},
		Permissions: AgentPermissions{Mode: "ask", Rules: []Rule{{Action: "deny", Tool: "run_command"}}},
	}
	cfg.Options.DelegateMaxConcurrency = 2
	cfg.Options.DelegateProviderConcurrency = map[string]int{"ollama": 1}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid delegated-agent governance: %v", err)
	}

	profile := cfg.Agents["reviewer"]
	profile.Permissions.Rules = []Rule{{Action: "allow", Tool: "run_command"}}
	cfg.Agents["reviewer"] = profile
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "may only prompt or deny") {
		t.Fatalf("profile allow rule should be rejected: %v", err)
	}
	profile.Permissions.Rules = nil
	profile.Permissions.DeniedCommands = []string{"["}
	cfg.Agents["reviewer"] = profile
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "denied_commands") {
		t.Fatalf("bad profile command regex should be rejected: %v", err)
	}
}

func TestValidateNotifications(t *testing.T) {
	cfg := Defaults()
	for _, ok := range []string{"", "on", "bell", "off", "Bell"} {
		cfg.Options.Notifications = ok
		if err := cfg.Validate(); err != nil {
			t.Fatalf("notifications %q should validate: %v", ok, err)
		}
	}
	cfg.Options.Notifications = "loud"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected options.notifications error")
	}
}

func TestValidateAgentIntegration(t *testing.T) {
	cfg := Defaults()
	for _, mode := range []string{"manual", "reviewed"} {
		cfg.Options.AgentIntegration = mode
		if err := cfg.Validate(); err != nil {
			t.Fatalf("agent integration %q should validate: %v", mode, err)
		}
	}
	cfg.Options.AgentIntegration = "automatic"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "options.agent_integration") {
		t.Fatal("expected options.agent_integration error")
	}
}

func TestValidateExternalEditor(t *testing.T) {
	cfg := Defaults()
	cfg.Options.Editor = EditorOptions{Command: "code", Args: []string{"--goto", "{file}:{line}:{column}"}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid editor: %v", err)
	}
	cfg.Options.Editor = EditorOptions{Args: []string{"{file}"}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "options.editor.command") {
		t.Fatalf("missing command error=%v", err)
	}
}

func TestValidatePrimaryAgentReasoningPricingAndBudget(t *testing.T) {
	cfg := Defaults()
	cached := 0.25
	provider := cfg.Providers["ollama"]
	provider.Reasoning = &Reasoning{Effort: "high"}
	provider.Pricing = &Pricing{InputPerMillion: 1, OutputPerMillion: 4, CachedInputPerMillion: &cached}
	cfg.Providers["ollama"] = provider
	cfg.Agents["builder"] = AgentDefinition{
		Availability: "both", Reasoning: &Reasoning{Effort: "medium"}, CostBudgetUSD: 2.5,
	}
	cfg.DefaultAgent = "builder"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid primary profile: %v", err)
	}
	if _, err := cfg.PrimaryAgent("builder"); err != nil {
		t.Fatal(err)
	}
	if profile, err := cfg.PrimaryAgent("default"); err != nil || profile.Model != "" {
		t.Fatalf("default alias should restore the unprofiled primary: profile=%+v err=%v", profile, err)
	}
	if profile, err := cfg.PrimaryAgent("none"); err != nil || profile.Model != "" {
		t.Fatalf("none alias should restore the unprofiled primary: profile=%+v err=%v", profile, err)
	}

	bad := cfg
	bad.DefaultAgent = "missing"
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "default_agent") {
		t.Fatalf("missing default agent error=%v", err)
	}
	provider = cfg.Providers["ollama"]
	provider.Reasoning = &Reasoning{Effort: "turbo"}
	cfg.Providers["ollama"] = provider
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "reasoning.effort") {
		t.Fatalf("invalid reasoning error=%v", err)
	}
}

func TestExistingAgentProfilesRemainDelegateOnly(t *testing.T) {
	profile := AgentDefinition{}
	if !AgentAvailableFor(profile, "delegate") || AgentAvailableFor(profile, "primary") {
		t.Fatal("empty availability should retain delegate-only behavior")
	}
}

func TestValidateKeybindings(t *testing.T) {
	cfg := Defaults()
	cfg.Options.Keybindings["next_tab"] = "alt+t"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid keybinding override: %v", err)
	}

	cfg = Defaults()
	cfg.Options.Keybindings["unknown_action"] = "ctrl+u"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "options.keybindings.unknown_action") {
		t.Fatalf("unknown action error=%v", err)
	}

	cfg = Defaults()
	cfg.Options.Keybindings["next_tab"] = cfg.Options.Keybindings["diff_view"]
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "already assigned") {
		t.Fatalf("duplicate key error=%v", err)
	}

	cfg = Defaults()
	cfg.Options.Keybindings["next_tab"] = "shift+space"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported key") {
		t.Fatalf("unsupported key error=%v", err)
	}
}

func TestPartialKeybindingOverrideInheritsDefaults(t *testing.T) {
	dir := t.TempDir()
	writeProject(t, dir, `{"options":{"alternate_screen":false,"reduced_motion":true,"keybindings":{"next_tab":"alt+t"}}}`)
	cfg, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Options.AlternateScreen {
		t.Fatal("project alternate_screen=false was not applied")
	}
	if !cfg.Options.ReducedMotion {
		t.Fatal("project reduced_motion=true was not applied")
	}
	if got := cfg.Options.Keybindings["next_tab"]; got != "alt+t" {
		t.Fatalf("next_tab=%q", got)
	}
	if got := cfg.Options.Keybindings["diff_view"]; got != "ctrl+d" {
		t.Fatalf("omitted keybinding did not inherit default: %q", got)
	}
	if got := cfg.Options.Keybindings["session_picker"]; got != "alt+s" {
		t.Fatalf("new session-picker binding did not inherit default: %q", got)
	}
}

// An omitted posture must keep the behavior every earlier release had, so
// loading an existing configuration with this build changes nothing.
func TestPosturesDefaultToOpen(t *testing.T) {
	dir := t.TempDir()
	writeProject(t, dir, `{"permissions":{"mode":"workspace"}}`)
	cfg, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Permissions.Network != "open" || cfg.Permissions.Commands != "open" {
		t.Fatalf("postures = %q/%q", cfg.Permissions.Network, cfg.Permissions.Commands)
	}
}

func TestPosturesAreValidated(t *testing.T) {
	cfg := Defaults()
	cfg.Permissions.Network = "enforced"
	cfg.Permissions.Commands = "strict"
	errs := cfg.ValidateFields()
	fields := map[string]bool{}
	for _, err := range errs {
		fields[err.Field] = true
	}
	if !fields["permissions.network"] || !fields["permissions.commands"] {
		t.Fatalf("errors=%+v", errs)
	}
}

// Postures are monotonic across layers like denied commands: a project file
// may tighten what the user configured, never loosen it.
func TestProjectLayerCannotLoosenAPosture(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	global, err := GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(global), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(global, []byte(`{"permissions":{"network":"scoped","commands":"allowlist"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeProject(t, dir, `{"permissions":{"network":"open","commands":"open"}}`)
	cfg, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Permissions.Network != "scoped" || cfg.Permissions.Commands != "allowlist" {
		t.Fatalf("project layer loosened the postures: %q/%q", cfg.Permissions.Network, cfg.Permissions.Commands)
	}
}

// A lower layer may still tighten, and an omitted value inherits.
func TestProjectLayerCanTightenAPosture(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := t.TempDir()
	writeProject(t, dir, `{"permissions":{"network":"scoped"}}`)
	cfg, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Permissions.Network != "scoped" || cfg.Permissions.Commands != "open" {
		t.Fatalf("postures = %q/%q", cfg.Permissions.Network, cfg.Permissions.Commands)
	}
}

func loadWithGlobal(t *testing.T, global, project string) Config {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if global != "" {
		path, err := GlobalPath()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(global), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	if project != "" {
		writeProject(t, dir, project)
	}
	cfg, err := LoadWithOptions(dir, LoadOptions{TrustStatus: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestHardenedPresetExpandsToContainmentFields(t *testing.T) {
	cfg := loadWithGlobal(t, `{"permissions":{"preset":"hardened"}}`, "")
	p := cfg.Permissions
	if p.Sandbox != "require" || p.SandboxAllowReadOutsideWorkspace || p.Network != "scoped" || p.Commands != "allowlist" || p.CommandEnv != "minimal" {
		t.Fatalf("hardened expansion = %+v", p)
	}
	// Command networking stays on: denying it breaks package installs, so it
	// remains a deliberate extra line rather than part of the bundle.
	if !p.SandboxAllowNetwork {
		t.Fatal("hardened must not silently deny command networking")
	}
	// A preset never chooses autonomy for the user.
	if p.Mode != "ask" {
		t.Fatalf("preset changed autonomy mode to %q", p.Mode)
	}
	if origin := cfg.Origins["permissions.network"]; origin != "user (preset hardened)" {
		t.Fatalf("expansion is not attributable: %q", origin)
	}
}

func TestFrictionlessPresetIsAnExplicitOptOut(t *testing.T) {
	cfg := loadWithGlobal(t, `{"permissions":{"preset":"frictionless"}}`, "")
	p := cfg.Permissions
	if p.Sandbox != "off" || p.CommandEnv != "full" || p.Network != "open" || p.Commands != "open" {
		t.Fatalf("frictionless expansion = %+v", p)
	}
	// Reduced containment is not reduced safety policy: prompts still apply.
	if p.Mode != "ask" {
		t.Fatalf("frictionless changed autonomy mode to %q", p.Mode)
	}
}

// A preset is a starting point. Anything the same layer states explicitly
// must win, or a user could not adjust one field without abandoning it.
func TestExplicitFieldsWinOverThePreset(t *testing.T) {
	cfg := loadWithGlobal(t, `{"permissions":{"preset":"hardened","sandbox":"auto","commands":"open"}}`, "")
	p := cfg.Permissions
	if p.Sandbox != "auto" || p.Commands != "open" {
		t.Fatalf("explicit fields lost to the preset: %+v", p)
	}
	if p.Network != "scoped" {
		t.Fatalf("undeclared field should still come from the preset: %+v", p)
	}
	if origin := cfg.Origins["permissions.sandbox"]; origin != "user" {
		t.Fatalf("explicit field origin = %q", origin)
	}
}

// A weaker preset in a lower layer must not undo containment established
// above it. An explicit field remains the documented escape hatch.
func TestPresetCannotLoosenAStricterLayer(t *testing.T) {
	cfg := loadWithGlobal(t,
		`{"permissions":{"preset":"hardened"}}`,
		`{"permissions":{"preset":"frictionless"}}`)
	p := cfg.Permissions
	if p.Sandbox != "require" {
		t.Fatalf("project preset weakened the sandbox to %q", p.Sandbox)
	}
	if p.Network != "scoped" || p.Commands != "allowlist" {
		t.Fatalf("project preset weakened the postures: %+v", p)
	}
	if p.CommandEnv != "minimal" {
		t.Fatalf("project preset restored the inherited environment: %q", p.CommandEnv)
	}
}

func TestPresetCanTightenALooserLayer(t *testing.T) {
	cfg := loadWithGlobal(t, `{"permissions":{"preset":"standard"}}`, `{"permissions":{"preset":"hardened"}}`)
	if cfg.Permissions.Sandbox != "require" || cfg.Permissions.Network != "scoped" {
		t.Fatalf("project preset could not tighten: %+v", cfg.Permissions)
	}
}

func TestUnknownPresetIsRejected(t *testing.T) {
	cfg := Defaults()
	cfg.Permissions.Preset = "paranoid"
	errs := cfg.ValidateFields()
	found := false
	for _, err := range errs {
		if err.Field == "permissions.preset" {
			found = true
		}
	}
	if !found {
		t.Fatalf("errors=%+v", errs)
	}
}

// An omitted preset must change nothing at all.
func TestOmittedPresetKeepsExistingBehavior(t *testing.T) {
	cfg := loadWithGlobal(t, `{"permissions":{"mode":"workspace"}}`, "")
	p := cfg.Permissions
	if p.Sandbox != "auto" || !p.SandboxAllowNetwork || !p.SandboxAllowReadOutsideWorkspace {
		t.Fatalf("defaults changed: %+v", p)
	}
	if p.Network != "open" || p.Commands != "open" {
		t.Fatalf("postures changed: %+v", p)
	}
}

// A preset must never be a value whose source the user cannot see.
func TestLayerReportAttributesPresetExpansion(t *testing.T) {
	cfg := loadWithGlobal(t, `{"permissions":{"preset":"hardened","sandbox":"auto"}}`, "")
	report := cfg.LayerReport()
	if !strings.Contains(report, "Expanded by a preset") {
		t.Fatalf("report does not attribute the expansion:\n%s", report)
	}
	if !strings.Contains(report, "permissions.network") || !strings.Contains(report, "user (preset hardened)") {
		t.Fatalf("expanded field missing from the report:\n%s", report)
	}
	// An explicitly set field belongs to its layer, not to the preset.
	for _, line := range strings.Split(report, "\n") {
		if strings.Contains(line, "permissions.sandbox ") && strings.Contains(line, "preset") {
			t.Fatalf("explicit field attributed to the preset: %q", line)
		}
	}
}

func TestLayerReportOmitsPresetSectionWithoutAPreset(t *testing.T) {
	cfg := loadWithGlobal(t, `{"permissions":{"mode":"workspace"}}`, "")
	if strings.Contains(cfg.LayerReport(), "Expanded by a preset") {
		t.Fatalf("preset section shown without a preset:\n%s", cfg.LayerReport())
	}
}

func TestProtectCredentialsDefaultsToPrompt(t *testing.T) {
	cfg := loadWithGlobal(t, "", "")
	if got := cfg.Permissions.ProtectCredentials; got != ProtectCredentialsPrompt {
		t.Fatalf("protect_credentials = %q, want %q", got, ProtectCredentialsPrompt)
	}
}

func TestPresetsCarryTheirCredentialSetting(t *testing.T) {
	cases := map[string]string{
		PresetFrictionless: ProtectCredentialsOff,
		PresetStandard:     ProtectCredentialsPrompt,
		PresetHardened:     ProtectCredentialsDeny,
	}
	for name, want := range cases {
		cfg := loadWithGlobal(t, `{"permissions":{"preset":"`+name+`"}}`, "")
		if got := cfg.Permissions.ProtectCredentials; got != want {
			t.Errorf("preset %s: protect_credentials = %q, want %q", name, got, want)
		}
	}
}

// An explicit field beats the preset that accompanies it, matching every other
// containment setting.
func TestExplicitCredentialSettingWinsOverThePreset(t *testing.T) {
	cfg := loadWithGlobal(t, `{"permissions":{"preset":"hardened","protect_credentials":"prompt"}}`, "")
	if got := cfg.Permissions.ProtectCredentials; got != ProtectCredentialsPrompt {
		t.Fatalf("protect_credentials = %q, want prompt", got)
	}
}

func TestProjectCanTightenCredentialProtection(t *testing.T) {
	cfg := loadWithGlobal(t,
		`{"permissions":{"protect_credentials":"off"}}`,
		`{"permissions":{"protect_credentials":"deny"}}`)
	if got := cfg.Permissions.ProtectCredentials; got != ProtectCredentialsDeny {
		t.Fatalf("protect_credentials = %q, want deny", got)
	}
	if len(cfg.Clamped) != 0 {
		t.Fatalf("tightening should not be clamped: %v", cfg.Clamped)
	}
}

// The escape hatch lives in the global configuration only: a repository must
// not be able to switch off a protection the user chose.
func TestProjectCannotWeakenCredentialProtection(t *testing.T) {
	cfg := loadWithGlobal(t,
		`{"permissions":{"protect_credentials":"deny"}}`,
		`{"permissions":{"protect_credentials":"off"}}`)
	if got := cfg.Permissions.ProtectCredentials; got != ProtectCredentialsDeny {
		t.Fatalf("protect_credentials = %q, want deny", got)
	}
	found := false
	for _, note := range cfg.Clamped {
		if note.Field == "protect_credentials" {
			found = true
		}
	}
	if !found {
		t.Fatalf("refusal was not reported: %v", cfg.Clamped)
	}
}

// A project preset is clamped the same way an explicit field is.
func TestProjectPresetCannotWeakenCredentialProtection(t *testing.T) {
	cfg := loadWithGlobal(t,
		`{"permissions":{"protect_credentials":"deny"}}`,
		`{"permissions":{"preset":"frictionless"}}`)
	if got := cfg.Permissions.ProtectCredentials; got != ProtectCredentialsDeny {
		t.Fatalf("protect_credentials = %q, want deny", got)
	}
}

func TestUnknownCredentialSettingIsRejected(t *testing.T) {
	cfg := Defaults()
	cfg.Permissions.ProtectCredentials = "sometimes"
	found := false
	for _, e := range cfg.ValidateFields() {
		if e.Field == "permissions.protect_credentials" {
			found = true
		}
	}
	if !found {
		t.Fatal("invalid setting was accepted")
	}
}

// A project cannot turn scoped egress back off, exactly as it cannot weaken
// any other containment setting.
func TestProjectLayerCannotWeakenSandboxEgress(t *testing.T) {
	cfg := loadWithGlobal(t, `{"permissions":{"sandbox_egress":"scoped"}}`, `{"permissions":{"sandbox_egress":"off"}}`)
	if cfg.Permissions.SandboxEgress != SandboxEgressScoped {
		t.Fatalf("sandbox_egress = %q, want scoped", cfg.Permissions.SandboxEgress)
	}
	found := false
	for _, clamped := range cfg.Clamped {
		if clamped.Field == "sandbox_egress" {
			found = true
		}
	}
	if !found {
		t.Error("a refused weakening must be reported rather than applied silently")
	}
}

func TestProjectLayerCanTightenSandboxEgress(t *testing.T) {
	cfg := loadWithGlobal(t, "", `{"permissions":{"sandbox_egress":"scoped"}}`)
	if cfg.Permissions.SandboxEgress != SandboxEgressScoped {
		t.Fatalf("sandbox_egress = %q, want scoped", cfg.Permissions.SandboxEgress)
	}
}

func TestSandboxEgressRejectsUnknownValue(t *testing.T) {
	cfg := Defaults()
	cfg.Permissions.SandboxEgress = "enforced"
	found := false
	for _, err := range cfg.ValidateFields() {
		if err.Field == "permissions.sandbox_egress" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a validation error for an unknown sandbox_egress posture")
	}
}

// Scoped egress is enforceable on macOS only, so folding it into a
// cross-platform bundle would make one preset name mean different containment
// on different machines.
func TestNoPresetEnablesSandboxEgress(t *testing.T) {
	for _, name := range PresetNames() {
		cfg := loadWithGlobal(t, "", `{"permissions":{"preset":"`+name+`"}}`)
		if cfg.Permissions.SandboxEgress == SandboxEgressScoped {
			t.Errorf("preset %q enabled scoped egress; it must stay an explicit opt-in", name)
		}
	}
}

func TestPublicationDefaultsToPrompt(t *testing.T) {
	cfg := loadWithGlobal(t, "", "")
	if got := cfg.Permissions.Publication; got != PublicationPrompt {
		t.Fatalf("publication = %q, want %q", got, PublicationPrompt)
	}
}

func TestPresetsCarryTheirPublicationSetting(t *testing.T) {
	cases := map[string]string{
		PresetFrictionless: PublicationOff,
		PresetStandard:     PublicationPrompt,
		PresetHardened:     PublicationDeny,
	}
	for name, want := range cases {
		cfg := loadWithGlobal(t, `{"permissions":{"preset":"`+name+`"}}`, "")
		if got := cfg.Permissions.Publication; got != want {
			t.Errorf("preset %s: publication = %q, want %q", name, got, want)
		}
	}
}

func TestExplicitPublicationSettingWinsOverThePreset(t *testing.T) {
	cfg := loadWithGlobal(t, `{"permissions":{"preset":"hardened","publication":"prompt"}}`, "")
	if got := cfg.Permissions.Publication; got != PublicationPrompt {
		t.Fatalf("publication = %q, want prompt", got)
	}
}

func TestProjectCanTightenPublicationButNotWeakenIt(t *testing.T) {
	tightened := loadWithGlobal(t,
		`{"permissions":{"publication":"off"}}`,
		`{"permissions":{"publication":"deny"}}`)
	if got := tightened.Permissions.Publication; got != PublicationDeny {
		t.Fatalf("tightened publication = %q, want deny", got)
	}
	if len(tightened.Clamped) != 0 {
		t.Fatalf("tightening should not be clamped: %v", tightened.Clamped)
	}
	weakened := loadWithGlobal(t,
		`{"permissions":{"publication":"deny"}}`,
		`{"permissions":{"publication":"off"}}`)
	if got := weakened.Permissions.Publication; got != PublicationDeny {
		t.Fatalf("weakened publication = %q, want deny", got)
	}
	found := false
	for _, note := range weakened.Clamped {
		if note.Field == "publication" {
			found = true
		}
	}
	if !found {
		t.Fatalf("refusal was not reported: %v", weakened.Clamped)
	}
}

func TestPublicationIsAClampedContainmentField(t *testing.T) {
	for _, field := range ContainmentFields() {
		if field == "publication" {
			return
		}
	}
	t.Fatal("publication is not listed as a containment field, so documentation checks will not cover it")
}

func TestInvalidPublicationSettingIsRejected(t *testing.T) {
	cfg := Defaults()
	cfg.Permissions.Publication = "sometimes"
	errs := cfg.ValidateFields()
	found := false
	for _, err := range errs {
		if err.Field == "permissions.publication" {
			found = true
		}
	}
	if !found {
		t.Fatalf("invalid publication setting was accepted: %v", errs)
	}
}

// A command pattern is matched against an executable name or, when it contains
// a space, against an operation. Both forms are single-spaced and untrimmed,
// so a pattern that can match neither is a rule that reads as protection and
// does nothing — the shape that let the host matcher ship inert.
func TestUnmatchableCommandPatternIsRejected(t *testing.T) {
	for _, pattern := range []string{"npm  publish", " npm publish", "npm publish "} {
		cfg := Defaults()
		cfg.Permissions.Rules = []Rule{{Action: "deny", Command: pattern}}
		found := false
		for _, err := range cfg.ValidateFields() {
			if err.Field == "permissions.rules[0].command" {
				found = true
			}
		}
		if !found {
			t.Errorf("pattern %q was accepted but can never match", pattern)
		}
	}
	for _, pattern := range []string{"npm", "npm publish", "gh pr *", "g*"} {
		cfg := Defaults()
		cfg.Permissions.Rules = []Rule{{Action: "deny", Command: pattern}}
		for _, err := range cfg.ValidateFields() {
			if err.Field == "permissions.rules[0].command" {
				t.Errorf("usable pattern %q was rejected: %v", pattern, err)
			}
		}
	}
}
