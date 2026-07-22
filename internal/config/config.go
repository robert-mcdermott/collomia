package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/robert-mcdermott/collomia/internal/trust"
	"github.com/robert-mcdermott/collomia/internal/userconfig"
)

const ProjectFile = ".collomia.json"

// CurrentSchemaVersion is the configuration schema this build reads and
// writes. Older files without schema_version are treated as version 1.
const CurrentSchemaVersion = 1

type Config struct {
	SchemaVersion   int                  `json:"schema_version,omitempty"`
	DefaultProvider string               `json:"default_provider"`
	DefaultModel    string               `json:"default_model"`
	Providers       map[string]Provider  `json:"providers"`
	Permissions     Permissions          `json:"permissions"`
	MCP             map[string]MCPServer `json:"mcp,omitempty"`
	Options         Options              `json:"options,omitempty"`
	// Agents defines named sub-agent profiles the delegate tool can select
	// by name, each with its own model, role instructions, tool allowlist,
	// and iteration budget.
	Agents map[string]AgentDefinition `json:"agents,omitempty"`
	// LSP maps a language id (e.g. "go", "python", "typescript") to the
	// command that starts its language server (e.g. ["gopls"]). Common
	// servers found on PATH are auto-detected when unset.
	LSP map[string][]string `json:"lsp,omitempty"`
	// Hooks maps a lifecycle event name to the commands that observe it.
	// Hooks are trusted code: project-configured hooks require `collo trust`
	// like every other project capability.
	Hooks map[string][]Hook `json:"hooks,omitempty"`

	// Source names the highest-precedence file layer for display.
	Source string `json:"-"`
	// Layers records every configuration layer in application order.
	Layers []Layer `json:"-"`
	// Origins maps dotted key paths to the layer that last set them.
	Origins map[string]string `json:"-"`
	// ProjectTrusted is false when a project configuration exists but was
	// quarantined because the workspace is not trusted.
	ProjectTrusted bool `json:"-"`
	// Quarantined lists project-provided capabilities that were ignored.
	Quarantined []string `json:"-"`
	// EnvProvider/EnvModel hold COLLO_PROVIDER / COLLO_MODEL overrides.
	EnvProvider string `json:"-"`
	EnvModel    string `json:"-"`
}

// Layer describes one source of configuration and what it contributed.
type Layer struct {
	Name    string // defaults, user, project, env
	Path    string // backing file, when there is one
	Applied bool
	Note    string
	Keys    []string // dotted key paths this layer set
}

type Provider struct {
	Type       string `json:"type"`
	BaseURL    string `json:"base_url,omitempty"`
	APIKey     string `json:"api_key,omitempty"`
	APIKeyEnv  string `json:"api_key_env,omitempty"`
	Model      string `json:"model,omitempty"`
	Region     string `json:"region,omitempty"`
	Profile    string `json:"profile,omitempty"`
	Deployment string `json:"deployment,omitempty"`
	APIVersion string `json:"api_version,omitempty"`
	Auth       string `json:"auth,omitempty"`
	// EntraScope overrides the provider-family token audience when auth=entra.
	// Traditional Azure OpenAI and current Foundry endpoints use different
	// documented defaults.
	EntraScope string `json:"entra_scope,omitempty"`
	// EntraTenantID selects the tenant for developer CLI and workload identity
	// credentials in DefaultAzureCredential. EnvironmentCredential continues to
	// honor the standard AZURE_TENANT_ID variable.
	EntraTenantID string `json:"entra_tenant_id,omitempty"`
	// EntraAuthorityHost selects a sovereign/private Microsoft Entra authority.
	// The empty value uses Azure Public Cloud.
	EntraAuthorityHost       string            `json:"entra_authority_host,omitempty"`
	Headers                  map[string]string `json:"headers,omitempty"`
	MaxTokens                int               `json:"max_tokens,omitempty"`
	Context                  int               `json:"context_window,omitempty"`
	Temperature              *float64          `json:"temperature,omitempty"`
	ConnectTimeoutSeconds    int               `json:"connect_timeout_seconds,omitempty"`
	RequestTimeoutSeconds    int               `json:"request_timeout_seconds,omitempty"`
	StreamIdleTimeoutSeconds int               `json:"stream_idle_timeout_seconds,omitempty"`
}

type Permissions struct {
	Mode                  string   `json:"mode"`
	AllowOutsideWorkspace bool     `json:"allow_outside_workspace"`
	AllowedTools          []string `json:"allowed_tools,omitempty"`
	DeniedTools           []string `json:"denied_tools,omitempty"`
	// DeniedCommands is monotonic across configuration layers: each layer can
	// add patterns, but cannot remove built-in or higher-scope denials.
	DeniedCommands []string `json:"denied_commands,omitempty"`
	// Rules are ordered allow/prompt/deny decisions matched before the
	// autonomy mode's defaults. See internal/policy.
	Rules []Rule `json:"rules,omitempty"`
	// Sandbox selects OS sandbox enforcement for commands: off, auto, require.
	Sandbox string `json:"sandbox,omitempty"`
	// SandboxAllowNetwork permits network egress inside the sandbox.
	SandboxAllowNetwork bool `json:"sandbox_allow_network,omitempty"`
	// SandboxAllowReadOutsideWorkspace keeps the compatibility default of
	// broad filesystem reads inside the command sandbox. Set it to false to
	// confine reads to the workspace, system runtime paths, temporary
	// directories, and SandboxReadableRoots.
	SandboxAllowReadOutsideWorkspace bool `json:"sandbox_allow_read_outside_workspace,omitempty"`
	// SandboxReadableRoots grants sandboxed commands read access to additional
	// explicit roots when outside-workspace reads are confined. Relative paths
	// are resolved from the workspace. Writable roots are always readable.
	SandboxReadableRoots []string `json:"sandbox_readable_roots,omitempty"`
	// SandboxWritableRoots grants sandboxed commands write access to
	// additional explicit roots (for example a package-manager cache). Relative
	// paths are resolved from the workspace.
	SandboxWritableRoots []string `json:"sandbox_writable_roots,omitempty"`
	// CommandEnv controls the environment passed to agent commands: "full"
	// (inherit everything, the default) or "minimal" (PATH, HOME, and other
	// basics only, keeping parent secrets out of child processes).
	CommandEnv string `json:"command_env,omitempty"`
	// ReviewerCommand, when set, runs before any non-read action is
	// auto-approved. It receives the request as JSON on stdin; replying
	// {"decision":"deny"} (or exiting non-zero) escalates the action to an
	// interactive prompt instead of silently allowing it.
	ReviewerCommand string `json:"reviewer_command,omitempty"`
}

// Rule is one scoped approval rule. Empty match fields match anything; all
// populated fields must match. Patterns use filepath.Match globs.
type Rule struct {
	Action  string `json:"action"`            // allow, prompt, deny
	Tool    string `json:"tool,omitempty"`    // tool name glob, e.g. run_command or mcp_*
	Path    string `json:"path,omitempty"`    // resolved path glob
	Command string `json:"command,omitempty"` // command executable glob, e.g. go or git
	Host    string `json:"host,omitempty"`    // host/domain glob for network-bearing tools
	Server  string `json:"server,omitempty"`  // MCP server name glob
	Reason  string `json:"reason,omitempty"`  // shown to the user when the rule fires
}

type MCPServer struct {
	Transport string            `json:"transport"`
	Trusted   bool              `json:"trusted,omitempty"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	URL       string            `json:"url,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Disabled  bool              `json:"disabled,omitempty"`
	Timeout   int               `json:"timeout_seconds,omitempty"`
}

// AgentDefinition names a reusable sub-agent profile. Any field left empty
// falls back to the parent agent's own setting.
type AgentDefinition struct {
	// Model overrides the model used for this agent, on the same provider
	// as the parent (a different reasoning tier or a cheaper/faster model).
	Model string `json:"model,omitempty"`
	// Instructions is prepended to the sub-agent's system prompt to give it
	// a fixed role (e.g. "You are a security reviewer. Focus only on...").
	Instructions string `json:"instructions,omitempty"`
	// Tools restricts the sub-agent to this allowlist of tool names. Empty
	// means it may use every tool the parent has enabled.
	Tools []string `json:"tools,omitempty"`
	// Skills restricts the sub-agent's model-visible skill catalog. Empty
	// inherits every skill visible to the parent.
	Skills []string `json:"skills,omitempty"`
	// MaxIterations overrides the default sub-agent iteration budget.
	MaxIterations int `json:"max_iterations,omitempty"`
	// TokenBudget bounds provider-reported input plus output tokens across the
	// delegated task. Zero leaves the task bounded by iterations and timeout.
	TokenBudget int `json:"token_budget,omitempty"`
	// TimeoutSeconds bounds queueing plus execution. Zero uses ten minutes.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// Permissions can only tighten the parent's effective policy. It cannot
	// enable outside access, alter sandboxing, or add allow rules.
	Permissions AgentPermissions `json:"permissions,omitempty"`
}

// AgentPermissions is the deliberately restrictive permission surface for a
// named delegated-agent profile. Denials are additive, rules may only prompt
// or deny, and Mode is intersected with the parent's effective autonomy.
type AgentPermissions struct {
	Mode           string   `json:"mode,omitempty"`
	DeniedTools    []string `json:"denied_tools,omitempty"`
	DeniedCommands []string `json:"denied_commands,omitempty"`
	Rules          []Rule   `json:"rules,omitempty"`
}

// Hook is one lifecycle-hook command. The event it observes is the key of
// Config.Hooks. Gating events (user_prompt, tool_start) may block the action
// by exiting 2 or printing {"decision":"block"}; hooks can only tighten —
// they never bypass the permission engine or sandbox.
type Hook struct {
	// Command and Args form the argv; no shell is involved.
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	// Matcher is an optional regular expression tested against the event's
	// subject (the tool name for tool events, the event name otherwise).
	// Empty matches everything.
	Matcher string `json:"matcher,omitempty"`
	// TimeoutSeconds bounds the hook run (default 10).
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// HookEvents are the recognized lifecycle events, in firing order across a
// session.
var HookEvents = []string{"session_start", "user_prompt", "permission_decision", "tool_start", "tool_end", "file_change", "compaction", "subagent_start", "subagent_end", "stop", "session_end"}

type Options struct {
	MaxIterations      int `json:"max_iterations,omitempty"`
	MaxToolOutputBytes int `json:"max_tool_output_bytes,omitempty"`
	// DelegateMaxConcurrency is the session-wide delegated-task limit. It
	// defaults to four and is capped by the six-task delegate request limit.
	DelegateMaxConcurrency int `json:"delegate_max_concurrency,omitempty"`
	// DelegateProviderConcurrency optionally applies a tighter limit to tasks
	// using a named provider. Omitted providers inherit the session-wide limit.
	DelegateProviderConcurrency map[string]int `json:"delegate_provider_concurrency,omitempty"`
	DisabledTools               []string       `json:"disabled_tools,omitempty"`
	TranscriptDirectory         string         `json:"transcript_directory,omitempty"`
	Theme                       string         `json:"theme,omitempty"`
	// AlternateScreen controls whether the interactive TUI uses the terminal's
	// alternate screen buffer. It defaults to true; disabling it keeps the
	// final screen in native terminal scrollback.
	AlternateScreen bool `json:"alternate_screen"`
	// Keybindings overrides named global TUI actions. Modal safety decisions
	// retain fixed, visible keys so a local remap cannot make an approval
	// ambiguous.
	Keybindings map[string]string `json:"keybindings,omitempty"`
	// Notifications controls how the TUI gets the user's attention for
	// approvals, questions, and finished long turns: "on" (bell + terminal
	// desktop notification, the default), "bell" (bell only), or "off".
	Notifications string `json:"notifications,omitempty"`
	// Editor configures the user-initiated external-editor action in diff
	// review. Command and Args are executed directly without a shell. Args may
	// use {file}, {line}, and {column} placeholders.
	Editor EditorOptions `json:"editor,omitempty"`
	Debug  bool          `json:"debug,omitempty"`
}

type EditorOptions struct {
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
}

func Defaults() Config {
	return Config{
		SchemaVersion:   CurrentSchemaVersion,
		DefaultProvider: "ollama",
		DefaultModel:    "qwen3-coder",
		Providers: map[string]Provider{
			"ollama": {
				Type: "openai-compatible", BaseURL: "http://127.0.0.1:11434/v1",
				Model: "qwen3-coder", Context: 32768, MaxTokens: 8192,
			},
		},
		Permissions: Permissions{
			Mode:                             "ask",
			SandboxAllowNetwork:              true,
			SandboxAllowReadOutsideWorkspace: true,
			DeniedCommands: []string{
				`(?i)(^|[;&|]\s*)(rm\s+-[^\s]*(rf|fr)[^\s]*|rmdir\s+/s)\s+([/~]|\.{1,2}|\*|[a-z]:\\)($|\s)`,
				`(?i)(^|[;&|]\s*)(del|erase)\s+(?:/[^\s]+\s+)*[a-z]:\\(?:\*|\.\*)?($|\s)`,
			},
		},
		MCP:    map[string]MCPServer{},
		Agents: map[string]AgentDefinition{},
		Options: Options{
			MaxIterations:      24,
			MaxToolOutputBytes: 64 * 1024,
			AlternateScreen:    true,
			Keybindings:        DefaultKeybindings(),
		},
	}
}

func GlobalPath() (string, error) {
	return userconfig.ConfigPath()
}

type LoadOptions struct {
	// Strict rejects unknown fields and treats warnings as errors.
	Strict bool
	// SkipEnvironmentExpansion keeps ${VAR}, api_key_env, and MCP environment
	// references unresolved. It is intended for privacy-conscious local
	// diagnostics that need configuration shape but do not initialize a
	// runtime or consume credentials.
	SkipEnvironmentExpansion bool
	// TrustStatus overrides the trust lookup for the project layer; used by
	// tests. nil consults the default trust store.
	TrustStatus func(workspace string, configData []byte) trust.Status
}

// Load merges defaults, the user configuration, the (trusted) project
// configuration, and environment overrides, in that precedence order.
func Load(workspace string) (Config, error) { return LoadWithOptions(workspace, LoadOptions{}) }

func LoadWithOptions(workspace string, opts LoadOptions) (Config, error) {
	cfg := Defaults()
	cfg.Origins = map[string]string{}
	for _, key := range flattenJSON(mustJSON(cfg)) {
		cfg.Origins[key] = "defaults"
	}
	cfg.Layers = []Layer{{Name: "defaults", Applied: true}}
	cfg.Source = "defaults"
	cfg.ProjectTrusted = true

	if global, err := GlobalPath(); err == nil {
		if err := cfg.applyFile(global, "user", opts.Strict); err != nil {
			return cfg, err
		}
	}

	projectPath := filepath.Join(workspace, ProjectFile)
	data, err := os.ReadFile(projectPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// no project layer
	case err != nil:
		return cfg, fmt.Errorf("read config %s: %w", projectPath, err)
	default:
		status := trust.StatusTrusted
		if opts.TrustStatus != nil {
			status = opts.TrustStatus(workspace, data)
		} else if store, storeErr := trust.Load(); storeErr == nil {
			status = store.Check(workspace, data)
		}
		if status == trust.StatusTrusted {
			if err := cfg.applyFile(projectPath, "project", opts.Strict); err != nil {
				return cfg, err
			}
		} else {
			cfg.ProjectTrusted = false
			note := "workspace is not trusted"
			if status == trust.StatusChanged {
				note = "project configuration changed since it was trusted"
			}
			cfg.Layers = append(cfg.Layers, Layer{Name: "project", Path: projectPath, Applied: false, Note: note + "; run `collo trust` to approve it"})
			cfg.Quarantined = append(cfg.Quarantined, "project configuration "+projectPath)
		}
	}

	cfg.applyEnv()
	cfg.normalizeWithOptions(opts.SkipEnvironmentExpansion)
	if errs := cfg.ValidateFields(); len(errs) > 0 {
		return cfg, ValidationError{Errors: errs}
	}
	return cfg, nil
}

func (c *Config) applyFile(path, layer string, strict bool) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	var probe struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	if probe.SchemaVersion > CurrentSchemaVersion {
		return fmt.Errorf("config %s: schema_version %d is newer than this build supports (%d); upgrade collo", path, probe.SchemaVersion, CurrentSchemaVersion)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if strict {
		decoder.DisallowUnknownFields()
	}
	inheritedDeniedCommands := append([]string(nil), c.Permissions.DeniedCommands...)
	if err := decoder.Decode(c); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	c.Permissions.DeniedCommands = additiveStrings(inheritedDeniedCommands, c.Permissions.DeniedCommands)
	keys := flattenJSON(data)
	for _, key := range keys {
		c.Origins[key] = layer
	}
	c.Layers = append(c.Layers, Layer{Name: layer, Path: path, Applied: true, Keys: keys})
	c.Source = path
	return nil
}

// additiveStrings preserves inherited safety policy while allowing a lower
// configuration layer to add entries. Exact duplicates are retained once in
// first-seen order so built-ins always precede user and project additions.
func additiveStrings(inherited, additions []string) []string {
	merged := make([]string, 0, len(inherited)+len(additions))
	seen := make(map[string]struct{}, len(inherited)+len(additions))
	for _, values := range [][]string{inherited, additions} {
		for _, value := range values {
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			merged = append(merged, value)
		}
	}
	return merged
}

func (c *Config) applyEnv() {
	var keys []string
	if v := os.Getenv("COLLO_PROVIDER"); v != "" {
		c.EnvProvider = v
		c.Origins["default_provider"] = "env"
		keys = append(keys, "default_provider (COLLO_PROVIDER)")
	}
	if v := os.Getenv("COLLO_MODEL"); v != "" {
		c.EnvModel = v
		c.Origins["default_model"] = "env"
		keys = append(keys, "default_model (COLLO_MODEL)")
	}
	if len(keys) > 0 {
		c.Layers = append(c.Layers, Layer{Name: "env", Applied: true, Keys: keys})
	}
}

func (c *Config) normalize() {
	c.normalizeWithOptions(false)
}

func (c *Config) normalizeWithOptions(skipEnvironmentExpansion bool) {
	if c.SchemaVersion == 0 {
		c.SchemaVersion = CurrentSchemaVersion
	}
	if c.Providers == nil {
		c.Providers = map[string]Provider{}
	}
	if c.MCP == nil {
		c.MCP = map[string]MCPServer{}
	}
	if c.Agents == nil {
		c.Agents = map[string]AgentDefinition{}
	}
	if c.Permissions.Mode == "" {
		c.Permissions.Mode = "ask"
	}
	if c.Permissions.Sandbox == "" {
		c.Permissions.Sandbox = "off"
	}
	if c.Options.MaxIterations <= 0 {
		c.Options.MaxIterations = 24
	}
	if c.Options.MaxToolOutputBytes <= 0 {
		c.Options.MaxToolOutputBytes = 64 * 1024
	}
	if c.Options.DelegateMaxConcurrency <= 0 {
		c.Options.DelegateMaxConcurrency = 4
	}
	if c.Options.DelegateProviderConcurrency == nil {
		c.Options.DelegateProviderConcurrency = map[string]int{}
	}
	if c.Options.Keybindings == nil {
		c.Options.Keybindings = DefaultKeybindings()
	}
	for action, key := range c.Options.Keybindings {
		c.Options.Keybindings[action] = strings.ToLower(strings.TrimSpace(key))
	}
	c.Options.Editor.Command = strings.TrimSpace(c.Options.Editor.Command)
	for name, p := range c.Providers {
		p.Type = strings.ToLower(strings.TrimSpace(p.Type))
		p.Auth = strings.ToLower(strings.TrimSpace(p.Auth))
		if skipEnvironmentExpansion {
			p.BaseURL = strings.TrimRight(p.BaseURL, "/")
			p.EntraScope = strings.TrimSpace(p.EntraScope)
			p.EntraTenantID = strings.TrimSpace(p.EntraTenantID)
			p.EntraAuthorityHost = strings.TrimSpace(p.EntraAuthorityHost)
		} else {
			p.BaseURL = strings.TrimRight(expandEnv(p.BaseURL), "/")
			p.APIKey = expandEnv(p.APIKey)
			if p.APIKey == "" && p.APIKeyEnv != "" {
				p.APIKey = os.Getenv(p.APIKeyEnv)
			}
			for key, value := range p.Headers {
				p.Headers[key] = expandEnv(value)
			}
			p.EntraScope = strings.TrimSpace(expandEnv(p.EntraScope))
			p.EntraTenantID = strings.TrimSpace(expandEnv(p.EntraTenantID))
			p.EntraAuthorityHost = strings.TrimSpace(expandEnv(p.EntraAuthorityHost))
		}
		if p.MaxTokens <= 0 {
			p.MaxTokens = 8192
		}
		if p.ConnectTimeoutSeconds == 0 {
			p.ConnectTimeoutSeconds = 10
		}
		if p.RequestTimeoutSeconds == 0 {
			p.RequestTimeoutSeconds = 30 * 60
		}
		if p.StreamIdleTimeoutSeconds == 0 {
			p.StreamIdleTimeoutSeconds = 5 * 60
		}
		c.Providers[name] = p
	}
	for name, server := range c.MCP {
		c.MCP[name] = normalizeMCPServer(server, !skipEnvironmentExpansion)
	}
}

// ResolveMCPServer expands environment references and applies runtime
// defaults to one MCP definition. It is exported for scoped diagnostics such
// as `collo mcp test --global`, which deliberately inspect one configuration
// layer rather than loading the merged project configuration.
func ResolveMCPServer(server MCPServer) MCPServer {
	return normalizeMCPServer(server, true)
}

func normalizeMCPServer(server MCPServer, expandEnvironment bool) MCPServer {
	server.Transport = strings.ToLower(strings.TrimSpace(server.Transport))
	if expandEnvironment {
		server.URL = expandEnv(server.URL)
		for key, value := range server.Env {
			server.Env[key] = expandEnv(value)
		}
		for key, value := range server.Headers {
			server.Headers[key] = expandEnv(value)
		}
	}
	if server.Timeout == 0 {
		server.Timeout = 30
	}
	return server
}

// FieldError ties a validation failure to the configuration key that caused it.
type FieldError struct {
	Field   string
	Message string
}

func (e FieldError) Error() string { return e.Field + ": " + e.Message }

type ValidationError struct{ Errors []FieldError }

func (e ValidationError) Error() string {
	parts := make([]string, len(e.Errors))
	for i, fe := range e.Errors {
		parts[i] = fe.Error()
	}
	return "invalid configuration:\n  - " + strings.Join(parts, "\n  - ")
}

// ValidateFields returns every problem found, each tied to its field path.
func (c Config) ValidateFields() []FieldError {
	var errs []FieldError
	if len(c.Providers) == 0 {
		errs = append(errs, FieldError{"providers", "at least one provider is required"})
		return errs
	}
	providerName := c.DefaultProvider
	if c.EnvProvider != "" {
		providerName = c.EnvProvider
	}
	provider, ok := c.Providers[providerName]
	if !ok {
		errs = append(errs, FieldError{"default_provider", fmt.Sprintf("provider %q is not configured (configured: %s)", providerName, strings.Join(c.ProviderNames(), ", "))})
	} else if c.DefaultModel == "" && c.EnvModel == "" && provider.Model == "" {
		errs = append(errs, FieldError{"default_model", "default_model or provider.model is required"})
	}
	switch c.Permissions.Mode {
	case "ask", "workspace", "autopilot":
	default:
		errs = append(errs, FieldError{"permissions.mode", fmt.Sprintf("must be ask, workspace, or autopilot (got %q)", c.Permissions.Mode)})
	}
	switch c.Permissions.Sandbox {
	case "", "off", "auto", "require":
	default:
		errs = append(errs, FieldError{"permissions.sandbox", fmt.Sprintf("must be off, auto, or require (got %q)", c.Permissions.Sandbox)})
	}
	switch c.Permissions.CommandEnv {
	case "", "full", "minimal":
	default:
		errs = append(errs, FieldError{"permissions.command_env", fmt.Sprintf("must be full or minimal (got %q)", c.Permissions.CommandEnv)})
	}
	for i, root := range c.Permissions.SandboxWritableRoots {
		if strings.TrimSpace(root) == "" {
			errs = append(errs, FieldError{fmt.Sprintf("permissions.sandbox_writable_roots.%d", i), "must not be empty"})
		}
	}
	for i, root := range c.Permissions.SandboxReadableRoots {
		if strings.TrimSpace(root) == "" {
			errs = append(errs, FieldError{fmt.Sprintf("permissions.sandbox_readable_roots.%d", i), "must not be empty"})
		}
	}
	for name, provider := range c.Providers {
		field := "providers." + name
		switch provider.Type {
		case "openai", "openai-compatible", "anthropic", "anthropic-compatible", "bedrock", "bedrock-mantle", "azure-openai", "azure-foundry", "azure-foundry-anthropic":
		default:
			errs = append(errs, FieldError{field + ".type", fmt.Sprintf("unsupported type %q", provider.Type)})
		}
		if provider.Type != "bedrock" && provider.BaseURL == "" {
			errs = append(errs, FieldError{field + ".base_url", "required for this provider type"})
		}
		if provider.Type == "bedrock" {
			switch provider.Auth {
			case "", "auto", "sigv4", "bearer":
			default:
				errs = append(errs, FieldError{field + ".auth", fmt.Sprintf("must be auto, sigv4, or bearer for Bedrock (got %q)", provider.Auth)})
			}
			if provider.Auth == "sigv4" && (provider.APIKey != "" || provider.APIKeyEnv != "") {
				errs = append(errs, FieldError{field + ".api_key_env", "is not used with auth=sigv4; use the AWS credential chain or change auth to bearer/auto"})
			}
			if provider.Auth == "bearer" && provider.Profile != "" {
				errs = append(errs, FieldError{field + ".profile", "is not used with auth=bearer; remove it or change auth to sigv4/auto"})
			}
		}
		if provider.Type == "azure-openai" || provider.Type == "azure-foundry" || provider.Type == "azure-foundry-anthropic" {
			switch provider.Auth {
			case "", "api_key", "bearer", "entra":
			default:
				errs = append(errs, FieldError{field + ".auth", fmt.Sprintf("must be api_key, bearer, or entra for Azure providers (got %q)", provider.Auth)})
			}
			if provider.Auth == "entra" {
				if provider.APIKey != "" || provider.APIKeyEnv != "" {
					errs = append(errs, FieldError{field + ".api_key_env", "must be omitted with auth=entra; DefaultAzureCredential supplies short-lived tokens"})
				}
				for header := range provider.Headers {
					switch strings.ToLower(strings.TrimSpace(header)) {
					case "authorization", "api-key", "x-api-key":
						errs = append(errs, FieldError{field + ".headers." + header, "conflicts with auth=entra"})
					}
				}
				if provider.EntraScope != "" {
					if err := validateEntraScope(provider.EntraScope); err != nil {
						errs = append(errs, FieldError{field + ".entra_scope", err.Error()})
					}
				}
				if provider.EntraAuthorityHost != "" {
					if err := validateEntraAuthorityHost(provider.EntraAuthorityHost); err != nil {
						errs = append(errs, FieldError{field + ".entra_authority_host", err.Error()})
					}
				}
			} else if provider.EntraScope != "" || provider.EntraTenantID != "" || provider.EntraAuthorityHost != "" {
				errs = append(errs, FieldError{field + ".auth", "must be entra when entra_scope, entra_tenant_id, or entra_authority_host is configured"})
			}
		}
		for _, timeout := range []struct {
			key   string
			value int
		}{
			{"connect_timeout_seconds", provider.ConnectTimeoutSeconds},
			{"request_timeout_seconds", provider.RequestTimeoutSeconds},
			{"stream_idle_timeout_seconds", provider.StreamIdleTimeoutSeconds},
		} {
			if timeout.value < 0 {
				errs = append(errs, FieldError{field + "." + timeout.key, "must not be negative"})
			}
		}
	}
	for i, pattern := range c.Permissions.DeniedCommands {
		if _, err := regexp.Compile(pattern); err != nil {
			errs = append(errs, FieldError{fmt.Sprintf("permissions.denied_commands[%d]", i), err.Error()})
		}
	}
	for i, rule := range c.Permissions.Rules {
		field := fmt.Sprintf("permissions.rules[%d]", i)
		switch rule.Action {
		case "allow", "prompt", "deny":
		default:
			errs = append(errs, FieldError{field + ".action", fmt.Sprintf("must be allow, prompt, or deny (got %q)", rule.Action)})
		}
		if rule.Tool == "" && rule.Path == "" && rule.Command == "" && rule.Host == "" && rule.Server == "" {
			errs = append(errs, FieldError{field, "must match on at least one of tool, path, command, host, or server"})
		}
		for _, glob := range []string{rule.Tool, rule.Path, rule.Command, rule.Host, rule.Server} {
			if glob == "" {
				continue
			}
			if _, err := filepath.Match(glob, ""); err != nil {
				errs = append(errs, FieldError{field, fmt.Sprintf("invalid pattern %q: %v", glob, err)})
			}
		}
	}
	for name, server := range c.MCP {
		errs = append(errs, ValidateMCPServer(name, server)...)
	}
	for name, a := range c.Agents {
		field := "agents." + name
		if a.MaxIterations < 0 {
			errs = append(errs, FieldError{field + ".max_iterations", "must not be negative"})
		}
		if a.TokenBudget < 0 {
			errs = append(errs, FieldError{field + ".token_budget", "must not be negative"})
		}
		if a.TimeoutSeconds < 0 || a.TimeoutSeconds > 3600 {
			errs = append(errs, FieldError{field + ".timeout_seconds", "must be between 0 and 3600"})
		}
		switch a.Permissions.Mode {
		case "", "ask", "workspace", "autopilot":
		default:
			errs = append(errs, FieldError{field + ".permissions.mode", fmt.Sprintf("must be ask, workspace, or autopilot (got %q)", a.Permissions.Mode)})
		}
		for i, pattern := range a.Permissions.DeniedCommands {
			if _, err := regexp.Compile(pattern); err != nil {
				errs = append(errs, FieldError{fmt.Sprintf("%s.permissions.denied_commands[%d]", field, i), err.Error()})
			}
		}
		for i, rule := range a.Permissions.Rules {
			ruleField := fmt.Sprintf("%s.permissions.rules[%d]", field, i)
			if rule.Action != "prompt" && rule.Action != "deny" {
				errs = append(errs, FieldError{ruleField + ".action", "delegated-agent rules may only prompt or deny"})
			}
			if rule.Tool == "" && rule.Path == "" && rule.Command == "" && rule.Host == "" && rule.Server == "" {
				errs = append(errs, FieldError{ruleField, "must match on at least one of tool, path, command, host, or server"})
			}
			for _, glob := range []string{rule.Tool, rule.Path, rule.Command, rule.Host, rule.Server} {
				if glob == "" {
					continue
				}
				if _, err := filepath.Match(glob, ""); err != nil {
					errs = append(errs, FieldError{ruleField, fmt.Sprintf("invalid pattern %q: %v", glob, err)})
				}
			}
		}
	}
	if c.Options.DelegateMaxConcurrency < 0 || c.Options.DelegateMaxConcurrency > 6 {
		errs = append(errs, FieldError{"options.delegate_max_concurrency", "must be zero (default) or between 1 and 6"})
	}
	for name, limit := range c.Options.DelegateProviderConcurrency {
		if strings.TrimSpace(name) == "" {
			errs = append(errs, FieldError{"options.delegate_provider_concurrency", "provider names must not be empty"})
		}
		if limit < 1 || limit > 6 {
			errs = append(errs, FieldError{"options.delegate_provider_concurrency." + name, "must be between 1 and 6"})
		}
	}
	switch strings.ToLower(c.Options.Notifications) {
	case "", "on", "bell", "off":
	default:
		errs = append(errs, FieldError{"options.notifications", fmt.Sprintf("must be on, bell, or off (got %q)", c.Options.Notifications)})
	}
	if c.Options.Editor.Command == "" && len(c.Options.Editor.Args) > 0 {
		errs = append(errs, FieldError{"options.editor.command", "required when editor args are configured"})
	}
	if strings.ContainsRune(c.Options.Editor.Command, '\x00') {
		errs = append(errs, FieldError{"options.editor.command", "must not contain NUL"})
	}
	for i, arg := range c.Options.Editor.Args {
		if strings.ContainsRune(arg, '\x00') {
			errs = append(errs, FieldError{fmt.Sprintf("options.editor.args[%d]", i), "must not contain NUL"})
		}
	}
	for eventName, hooksForEvent := range c.Hooks {
		if !slices.Contains(HookEvents, eventName) {
			errs = append(errs, FieldError{"hooks." + eventName, fmt.Sprintf("unknown event (known: %s)", strings.Join(HookEvents, ", "))})
		}
		for i, hook := range hooksForEvent {
			field := fmt.Sprintf("hooks.%s[%d]", eventName, i)
			if hook.Command == "" {
				errs = append(errs, FieldError{field + ".command", "required"})
			}
			if hook.TimeoutSeconds < 0 {
				errs = append(errs, FieldError{field + ".timeout_seconds", "must not be negative"})
			}
			if hook.Matcher != "" {
				if _, err := regexp.Compile(hook.Matcher); err != nil {
					errs = append(errs, FieldError{field + ".matcher", err.Error()})
				}
			}
		}
	}
	errs = append(errs, ValidateKeybindings(c.Options.Keybindings)...)
	return errs
}

// Validate keeps the aggregate error form used by callers.
func (c Config) Validate() error {
	if errs := c.ValidateFields(); len(errs) > 0 {
		return ValidationError{Errors: errs}
	}
	return nil
}

func (c Config) Selected(providerName, modelOverride string) (string, Provider, string, error) {
	if providerName == "" {
		providerName = c.EnvProvider
	}
	if providerName == "" {
		providerName = c.DefaultProvider
	}
	p, ok := c.Providers[providerName]
	if !ok {
		return "", Provider{}, "", fmt.Errorf("unknown provider %q", providerName)
	}
	model := modelOverride
	if model == "" {
		model = c.EnvModel
	}
	if model == "" {
		model = p.Model
	}
	if model == "" {
		model = c.DefaultModel
	}
	if model == "" {
		return "", Provider{}, "", fmt.Errorf("provider %q has no model selected", providerName)
	}
	return providerName, p, model, nil
}

func (c Config) ProviderNames() []string {
	names := make([]string, 0, len(c.Providers))
	for name := range c.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// LayerReport renders the applied layers and key origins for inspection.
func (c Config) LayerReport() string {
	var b strings.Builder
	b.WriteString("Configuration layers (later layers override earlier ones):\n")
	for _, layer := range c.Layers {
		status := "applied"
		if !layer.Applied {
			status = "IGNORED"
		}
		fmt.Fprintf(&b, "  %-8s  %-7s  %s", layer.Name, status, layer.Path)
		if layer.Note != "" {
			fmt.Fprintf(&b, "  (%s)", layer.Note)
		}
		b.WriteString("\n")
		if layer.Applied && layer.Name != "defaults" {
			for _, key := range layer.Keys {
				fmt.Fprintf(&b, "            sets %s\n", key)
			}
		}
	}
	return b.String()
}

func expandEnv(value string) string {
	if value == "" {
		return ""
	}
	return os.Expand(value, func(key string) string { return os.Getenv(key) })
}

func validateEntraScope(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("must be an absolute HTTPS scope without credentials, query, or fragment")
	}
	if !strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/.default") {
		return errors.New("must end in /.default")
	}
	return nil
}

func validateEntraAuthorityHost(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("must be an absolute HTTPS origin without credentials, path, query, or fragment")
	}
	return nil
}

// flattenJSON lists the dotted key paths present in a JSON object, so layer
// precedence can be reported per key.
func flattenJSON(data []byte) []string {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil
	}
	var keys []string
	var walk func(prefix string, value any)
	walk = func(prefix string, value any) {
		object, ok := value.(map[string]any)
		if !ok || len(object) == 0 {
			keys = append(keys, prefix)
			return
		}
		for key, child := range object {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			walk(path, child)
		}
	}
	walk("", root)
	sort.Strings(keys)
	return keys
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}

func RuntimeSummary() string {
	return fmt.Sprintf("%s/%s · Go %s · %s", runtime.GOOS, runtime.GOARCH, runtime.Version(), time.Now().Format("2006-01-02"))
}
