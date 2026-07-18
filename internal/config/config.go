package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/robert-mcdermott/collomia/internal/trust"
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
	Type        string            `json:"type"`
	BaseURL     string            `json:"base_url,omitempty"`
	APIKey      string            `json:"api_key,omitempty"`
	APIKeyEnv   string            `json:"api_key_env,omitempty"`
	Model       string            `json:"model,omitempty"`
	Region      string            `json:"region,omitempty"`
	Profile     string            `json:"profile,omitempty"`
	Deployment  string            `json:"deployment,omitempty"`
	APIVersion  string            `json:"api_version,omitempty"`
	Auth        string            `json:"auth,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	MaxTokens   int               `json:"max_tokens,omitempty"`
	Context     int               `json:"context_window,omitempty"`
	Temperature *float64          `json:"temperature,omitempty"`
}

type Permissions struct {
	Mode                  string   `json:"mode"`
	AllowOutsideWorkspace bool     `json:"allow_outside_workspace"`
	AllowedTools          []string `json:"allowed_tools,omitempty"`
	DeniedTools           []string `json:"denied_tools,omitempty"`
	DeniedCommands        []string `json:"denied_commands,omitempty"`
	// Rules are ordered allow/prompt/deny decisions matched before the
	// autonomy mode's defaults. See internal/policy.
	Rules []Rule `json:"rules,omitempty"`
	// Sandbox selects OS sandbox enforcement for commands: off, auto, require.
	Sandbox string `json:"sandbox,omitempty"`
	// SandboxAllowNetwork permits network egress inside the sandbox.
	SandboxAllowNetwork bool `json:"sandbox_allow_network,omitempty"`
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
	// MaxIterations overrides the default sub-agent iteration budget.
	MaxIterations int `json:"max_iterations,omitempty"`
}

type Options struct {
	MaxIterations       int      `json:"max_iterations,omitempty"`
	MaxToolOutputBytes  int      `json:"max_tool_output_bytes,omitempty"`
	DisabledTools       []string `json:"disabled_tools,omitempty"`
	TranscriptDirectory string   `json:"transcript_directory,omitempty"`
	Theme               string   `json:"theme,omitempty"`
	Debug               bool     `json:"debug,omitempty"`
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
			Mode: "ask",
			DeniedCommands: []string{
				`(?i)(^|[;&|]\s*)(rm\s+-[^\s]*(rf|fr)[^\s]*|rmdir\s+/s)\s+([/~]|\.{1,2}|\*|[a-z]:\\)($|\s)`,
				`(?i)(^|[;&|]\s*)git\s+(reset\s+--hard|clean\s+-[^\s]*f[^\s]*)($|\s)`,
				`(?i)(^|[;&|]\s*)(del|erase)\s+/[^\s]*[sq][^\s]*\s+[a-z]:\\($|\s)`,
				`(?i)(^|[;&|]\s*)(shutdown|reboot|mkfs|diskpart)(\s|$)`,
			},
		},
		MCP:     map[string]MCPServer{},
		Options: Options{MaxIterations: 24, MaxToolOutputBytes: 64 * 1024},
	}
}

func GlobalPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "collomia", "config.json"), nil
}

type LoadOptions struct {
	// Strict rejects unknown fields and treats warnings as errors.
	Strict bool
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
	cfg.normalize()
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
	if err := decoder.Decode(c); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	keys := flattenJSON(data)
	for _, key := range keys {
		c.Origins[key] = layer
	}
	c.Layers = append(c.Layers, Layer{Name: layer, Path: path, Applied: true, Keys: keys})
	c.Source = path
	return nil
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
	for name, p := range c.Providers {
		p.Type = strings.ToLower(strings.TrimSpace(p.Type))
		p.BaseURL = strings.TrimRight(expandEnv(p.BaseURL), "/")
		p.APIKey = expandEnv(p.APIKey)
		if p.APIKey == "" && p.APIKeyEnv != "" {
			p.APIKey = os.Getenv(p.APIKeyEnv)
		}
		for key, value := range p.Headers {
			p.Headers[key] = expandEnv(value)
		}
		if p.MaxTokens <= 0 {
			p.MaxTokens = 8192
		}
		c.Providers[name] = p
	}
	for name, server := range c.MCP {
		server.URL = expandEnv(server.URL)
		for key, value := range server.Env {
			server.Env[key] = expandEnv(value)
		}
		for key, value := range server.Headers {
			server.Headers[key] = expandEnv(value)
		}
		if server.Timeout <= 0 {
			server.Timeout = 30
		}
		c.MCP[name] = server
	}
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
	for name, a := range c.Agents {
		if a.MaxIterations < 0 {
			errs = append(errs, FieldError{"agents." + name + ".max_iterations", "must not be negative"})
		}
	}
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

func WriteExample(path string) error {
	cfg := Defaults()
	cfg.Providers["openai"] = Provider{
		Type: "openai", BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY",
		Model: "gpt-5.1-codex-mini", Context: 200000, MaxTokens: 8192,
	}
	cfg.Providers["anthropic"] = Provider{
		Type: "anthropic", BaseURL: "https://api.anthropic.com", APIKeyEnv: "ANTHROPIC_API_KEY",
		Model: "claude-sonnet-4-6", Context: 200000, MaxTokens: 8192,
	}
	cfg.Providers["phlox"] = Provider{
		Type: "openai-compatible", BaseURL: "http://127.0.0.1:8080/v1", APIKeyEnv: "PHLOX_API_KEY",
		Model: "your-route-id", Context: 128000, MaxTokens: 8192,
	}
	cfg.Providers["bedrock"] = Provider{
		Type: "bedrock", Region: "us-west-2", Model: "your-bedrock-model-id", MaxTokens: 8192,
	}
	cfg.Providers["bedrock-mantle"] = Provider{
		Type: "bedrock-mantle", BaseURL: "https://bedrock-mantle.us-west-2.api.aws/v1",
		APIKeyEnv: "AWS_BEDROCK_API_KEY", Model: "openai.gpt-oss-120b", MaxTokens: 8192,
	}
	cfg.Providers["azure-openai"] = Provider{
		Type: "azure-openai", BaseURL: "https://your-resource.openai.azure.com", APIKeyEnv: "AZURE_OPENAI_API_KEY",
		Deployment: "your-deployment", APIVersion: "2024-10-21", Model: "your-deployment", MaxTokens: 8192,
	}
	cfg.Providers["azure-foundry"] = Provider{
		Type: "azure-foundry", BaseURL: "https://your-resource.services.ai.azure.com/openai/v1",
		APIKeyEnv: "AZURE_FOUNDRY_API_KEY", Model: "your-deployment", MaxTokens: 8192,
	}
	cfg.Providers["azure-foundry-claude"] = Provider{
		Type: "azure-foundry-anthropic", BaseURL: "https://your-resource.services.ai.azure.com/anthropic",
		APIKeyEnv: "AZURE_FOUNDRY_API_KEY", Model: "your-claude-deployment", MaxTokens: 8192,
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func expandEnv(value string) string {
	if value == "" {
		return ""
	}
	return os.Expand(value, func(key string) string { return os.Getenv(key) })
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
