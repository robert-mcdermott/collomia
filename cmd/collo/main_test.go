package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

func TestParseFlagsBeforeSubcommandAndTerminator(t *testing.T) {
	opts, err := parse([]string{"--cwd", "/tmp/work", "run", "--autopilot", "--", "prompt", "-with-dash"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.command != "run" || opts.cwd != "/tmp/work" || opts.autonomy != "autopilot" {
		t.Fatalf("options=%+v", opts)
	}
	if len(opts.args) != 2 || opts.args[1] != "-with-dash" {
		t.Fatalf("args=%v", opts.args)
	}
}

func TestParseInitWithReference(t *testing.T) {
	opts, err := parse([]string{"init", "--global", "--with-reference"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.command != "init" || !opts.global || !opts.withReference {
		t.Fatalf("options=%+v", opts)
	}
}

func TestParseWebTerminalFlags(t *testing.T) {
	opts, err := parse([]string{"tui", "--web", "--web-port", "8765", "--no-open", "--provider", "openrouter", "--", "start", "here"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.command != "tui" || !opts.web || opts.webPort != 8765 || !opts.noOpen || opts.provider != "openrouter" {
		t.Fatalf("options=%+v", opts)
	}
	if !slices.Equal(opts.args, []string{"start", "here"}) {
		t.Fatalf("prompt args=%v", opts.args)
	}
}

func TestWebTerminalFlagsRequireWebTUI(t *testing.T) {
	for _, args := range [][]string{
		{"run", "--web", "prompt"},
		{"--web-port", "8765"},
		{"--no-open"},
		{"--web", "--web-port", "70000"},
		{"--web", "--web-port=not-a-port"},
	} {
		if _, err := parse(args); err == nil {
			t.Errorf("parse(%v) unexpectedly succeeded", args)
		}
	}
}

func TestTUIChildArgsPreserveTUIOptionsWithoutRecursing(t *testing.T) {
	opts := options{
		cwd:      "/tmp/work",
		provider: "openrouter",
		model:    "example/model",
		autonomy: "workspace",
		plan:     true,
		resume:   "session-1",
		debug:    true,
		web:      true,
		webPort:  8765,
		noOpen:   true,
		args:     []string{"initial", "prompt"},
	}
	want := []string{"tui", "--cwd", "/tmp/work", "--provider", "openrouter", "--model", "example/model", "--autonomy", "workspace", "--plan", "--resume", "session-1", "--debug", "--", "initial", "prompt"}
	if got := tuiChildArgs(opts); !slices.Equal(got, want) {
		t.Fatalf("child args=%v\nwant=%v", got, want)
	}
}

func TestGlobalInitReportsLegacyConfiguration(t *testing.T) {
	home := t.TempDir()
	legacyBase := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", legacyBase)
	t.Setenv("AppData", legacyBase)
	legacy, err := appconfig.LegacyGlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err = run([]string{"init", "--global", "--cwd", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "former location") || !strings.Contains(err.Error(), filepath.Join(home, ".collomia", "config.json")) {
		t.Fatalf("expected migration guidance, got %v", err)
	}
}

func TestConfigValidateInspectsUntrustedProject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, appconfig.ProjectFile)
	if err := os.WriteFile(path, []byte(`{"unknown_setting":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runConfigCommand(options{cwd: dir, command: "config", strict: true, args: []string{"validate"}})
	if err == nil || !strings.Contains(err.Error(), "unknown_setting") {
		t.Fatalf("strict validation should inspect the untrusted project file, got %v", err)
	}
}

func TestProviderDiagnosticExplainsBedrockAuthentication(t *testing.T) {
	t.Setenv(provider.BedrockBearerTokenEnv, "")
	status, detail := providerDiagnostic(appconfig.Provider{Type: "bedrock", Auth: "sigv4", Profile: "development"})
	if status != "ok" || !strings.Contains(detail, "SigV4") || !strings.Contains(detail, "profile development") || !strings.Contains(detail, "session") {
		t.Fatalf("sigv4 diagnostic=%q %q", status, detail)
	}

	status, detail = providerDiagnostic(appconfig.Provider{Type: "bedrock", Auth: "bearer", APIKeyEnv: "MISSING_BEDROCK_KEY"})
	if status != "warn" || !strings.Contains(detail, "MISSING_BEDROCK_KEY") {
		t.Fatalf("missing bearer diagnostic=%q %q", status, detail)
	}

	t.Setenv(provider.BedrockBearerTokenEnv, "bedrock-api-key")
	status, detail = providerDiagnostic(appconfig.Provider{Type: "bedrock"})
	if status != "ok" || !strings.Contains(detail, "bearer") || !strings.Contains(detail, provider.BedrockBearerTokenEnv) {
		t.Fatalf("auto bearer diagnostic=%q %q", status, detail)
	}
}

func TestProviderDiagnosticShowsTimeoutPolicy(t *testing.T) {
	status, detail := providerDiagnostic(appconfig.Provider{
		Type: "openai", APIKey: "resolved",
		ConnectTimeoutSeconds: 4, RequestTimeoutSeconds: 90, StreamIdleTimeoutSeconds: 15,
	})
	if status != "ok" || !strings.Contains(detail, "connect=4s") || !strings.Contains(detail, "request=90s") || !strings.Contains(detail, "idle=15s") {
		t.Fatalf("diagnostic=%q %q", status, detail)
	}
}

func TestProviderDiagnosticExplainsAzureEntraAuthentication(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "dev")
	t.Setenv("AZURE_TENANT_ID", "environment-tenant")
	status, detail := providerDiagnostic(appconfig.Provider{Type: "azure-openai", Auth: "entra"})
	for _, want := range []string{"DefaultAzureCredential", "AZURE_TOKEN_CREDENTIALS=dev", provider.AzureOpenAIEntraScope, "environment-tenant", "automatically", "Cognitive Services OpenAI User"} {
		if status != "ok" || !strings.Contains(detail, want) {
			t.Fatalf("Azure OpenAI diagnostic=%q %q; missing %q", status, detail, want)
		}
	}

	status, detail = providerDiagnostic(appconfig.Provider{
		Type: "azure-foundry-anthropic", Auth: "entra",
		EntraScope: "https://custom.example/.default", EntraTenantID: "configured-tenant", EntraAuthorityHost: "https://login.microsoftonline.us/",
	})
	for _, want := range []string{"https://custom.example/.default", "configured-tenant", "login.microsoftonline.us", "Cognitive Services User"} {
		if status != "ok" || !strings.Contains(detail, want) {
			t.Fatalf("Foundry diagnostic=%q %q; missing %q", status, detail, want)
		}
	}
}

func TestProviderDiagnosticWarnsThatStaticAzureBearerDoesNotRefresh(t *testing.T) {
	status, detail := providerDiagnostic(appconfig.Provider{Type: "azure-foundry", Auth: "bearer", APIKey: "resolved-token"})
	if status != "ok" || !strings.Contains(detail, "cannot refresh") || !strings.Contains(detail, "auth=entra") {
		t.Fatalf("static bearer diagnostic=%q %q", status, detail)
	}
}
