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

func TestParsePersistentMCPAddOptionsAndCommandTerminator(t *testing.T) {
	opts, err := parse([]string{"mcp", "add", "time", "--global", "--timeout", "12", "--env", "TOKEN=${MCP_TOKEN}", "--", "uvx", "mcp-server-time", "--local-timezone", "America/Los_Angeles"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.command != "mcp" || !opts.global || opts.mcpTimeout != 12 || !opts.mcpTimeoutSet {
		t.Fatalf("options=%+v", opts)
	}
	if !slices.Equal(opts.mcpEnv, []string{"TOKEN=${MCP_TOKEN}"}) || !slices.Equal(opts.args, []string{"add", "time", "uvx", "mcp-server-time", "--local-timezone", "America/Los_Angeles"}) {
		t.Fatalf("mcp env=%v args=%v", opts.mcpEnv, opts.args)
	}
	server, warnings, err := mcpServerFromOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	if server.Command != "uvx" || !slices.Equal(server.Args, []string{"mcp-server-time", "--local-timezone", "America/Los_Angeles"}) || server.Env["TOKEN"] != "${MCP_TOKEN}" || len(warnings) != 0 {
		t.Fatalf("server=%+v warnings=%v", server, warnings)
	}
}

func TestParsePersistentMCPHTTPOptions(t *testing.T) {
	opts, err := parse([]string{"mcp", "add", "docs", "--url=https://example.com/mcp", "--header", "Authorization=Bearer ${MCP_TOKEN}"})
	if err != nil {
		t.Fatal(err)
	}
	server, warnings, err := mcpServerFromOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	if server.Transport != "streamable-http" || server.URL != "https://example.com/mcp" || server.Headers["Authorization"] != "Bearer ${MCP_TOKEN}" || len(warnings) != 0 {
		t.Fatalf("server=%+v warnings=%v", server, warnings)
	}
	if _, err := parse([]string{"run", "--url", "https://example.com/mcp", "prompt"}); err == nil {
		t.Fatal("MCP-only option accepted for run")
	}
}

func TestMCPAddWarnsAboutLiteralSecretsAndRedactsShowValues(t *testing.T) {
	opts, err := parse([]string{"mcp", "add", "docs", "--url", "https://example.com/mcp?api_key=literal-query-secret", "--header", "Authorization=Bearer literal-secret"})
	if err != nil {
		t.Fatal(err)
	}
	server, warnings, err := mcpServerFromOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 2 || !strings.Contains(strings.Join(warnings, "\n"), "URL query parameter") {
		t.Fatalf("warnings=%v", warnings)
	}
	redacted := redactMCPServer(server)
	if redacted.Headers["Authorization"] != "[redacted]" || !strings.Contains(redacted.URL, "%5Bredacted%5D") || server.Headers["Authorization"] == "[redacted]" || !strings.Contains(server.URL, "literal-query-secret") {
		t.Fatalf("redacted=%+v original=%+v", redacted, server)
	}
}

func TestPersistentMCPCLIProjectLifecycle(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := run([]string{"mcp", "add", "time", "--cwd", workspace, "--", "uvx", "mcp-server-time"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, appconfig.ProjectFile)
	entries, exists, err := appconfig.ReadMCPFile(path)
	if err != nil || !exists {
		t.Fatalf("exists=%v err=%v", exists, err)
	}
	server := entries["time"]
	if server.Command != "uvx" || !server.Trusted || !slices.Equal(server.Args, []string{"mcp-server-time"}) {
		t.Fatalf("server=%+v", server)
	}
	if err := run([]string{"mcp", "add", "time", "--cwd", workspace, "--", "other"}); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("duplicate error=%v", err)
	}
	if err := run([]string{"mcp", "disable", "time", "--cwd", workspace}); err != nil {
		t.Fatal(err)
	}
	entries, _, _ = appconfig.ReadMCPFile(path)
	if !entries["time"].Disabled {
		t.Fatal("disable was not persisted")
	}
	if err := run([]string{"mcp", "enable", "time", "--cwd", workspace}); err != nil {
		t.Fatal(err)
	}
	entries, _, _ = appconfig.ReadMCPFile(path)
	if entries["time"].Disabled {
		t.Fatal("enable was not persisted")
	}
	if err := run([]string{"mcp", "remove", "time", "--cwd", workspace}); err != nil {
		t.Fatal(err)
	}
	entries, _, _ = appconfig.ReadMCPFile(path)
	if _, ok := entries["time"]; ok {
		t.Fatal("remove was not persisted")
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

func TestGlobalInitWritesHomeDirectoryConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	err := run([]string{"init", "--global", "--cwd", t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".collomia", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"schema_version": 1`) {
		t.Fatalf("unexpected starter at %s: %s", path, data)
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
