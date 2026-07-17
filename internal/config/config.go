package config

import (
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
)

const ProjectFile = ".collomia.json"

type Config struct {
	DefaultProvider string               `json:"default_provider"`
	DefaultModel    string               `json:"default_model"`
	Providers       map[string]Provider  `json:"providers"`
	Permissions     Permissions          `json:"permissions"`
	MCP             map[string]MCPServer `json:"mcp,omitempty"`
	Options         Options              `json:"options,omitempty"`
	Source          string               `json:"-"`
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

type Options struct {
	MaxIterations       int      `json:"max_iterations,omitempty"`
	MaxToolOutputBytes  int      `json:"max_tool_output_bytes,omitempty"`
	DisabledTools       []string `json:"disabled_tools,omitempty"`
	TranscriptDirectory string   `json:"transcript_directory,omitempty"`
}

func Defaults() Config {
	model := envOr("COLLO_MODEL", "qwen3-coder")
	return Config{
		DefaultProvider: "ollama",
		DefaultModel:    model,
		Providers: map[string]Provider{
			"ollama": {
				Type: "openai-compatible", BaseURL: "http://127.0.0.1:11434/v1",
				Model: model, Context: 32768, MaxTokens: 8192,
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

// Load checks the project configuration first, then the user configuration.
// Environment references are expanded only in credential-bearing string fields;
// shell expressions are deliberately never evaluated.
func Load(workspace string) (Config, error) {
	candidates := []string{filepath.Join(workspace, ProjectFile)}
	if global, err := GlobalPath(); err == nil {
		candidates = append(candidates, global)
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Config{}, fmt.Errorf("read config %s: %w", path, err)
		}
		cfg := Defaults()
		if err := json.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
		cfg.Source = path
		cfg.normalize()
		if err := cfg.Validate(); err != nil {
			return Config{}, fmt.Errorf("config %s: %w", path, err)
		}
		return cfg, nil
	}
	cfg := Defaults()
	cfg.Source = "defaults"
	cfg.normalize()
	return cfg, nil
}

func (c *Config) normalize() {
	if c.Providers == nil {
		c.Providers = map[string]Provider{}
	}
	if c.MCP == nil {
		c.MCP = map[string]MCPServer{}
	}
	if c.Permissions.Mode == "" {
		c.Permissions.Mode = "ask"
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

func (c Config) Validate() error {
	if len(c.Providers) == 0 {
		return errors.New("at least one provider is required")
	}
	provider, ok := c.Providers[c.DefaultProvider]
	if !ok {
		return fmt.Errorf("default provider %q is not configured", c.DefaultProvider)
	}
	if c.DefaultModel == "" && provider.Model == "" {
		return errors.New("default_model or provider.model is required")
	}
	switch c.Permissions.Mode {
	case "ask", "workspace", "autopilot":
	default:
		return fmt.Errorf("permission mode must be ask, workspace, or autopilot (got %q)", c.Permissions.Mode)
	}
	for name, provider := range c.Providers {
		switch provider.Type {
		case "openai", "openai-compatible", "anthropic", "anthropic-compatible", "bedrock", "bedrock-mantle", "azure-openai", "azure-foundry", "azure-foundry-anthropic":
		default:
			return fmt.Errorf("provider %q has unsupported type %q", name, provider.Type)
		}
		if provider.Type != "bedrock" && provider.BaseURL == "" {
			return fmt.Errorf("provider %q requires base_url", name)
		}
	}
	for _, pattern := range c.Permissions.DeniedCommands {
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("invalid denied_commands pattern %q: %w", pattern, err)
		}
	}
	return nil
}

func (c Config) Selected(providerName, modelOverride string) (string, Provider, string, error) {
	if providerName == "" {
		providerName = c.DefaultProvider
	}
	p, ok := c.Providers[providerName]
	if !ok {
		return "", Provider{}, "", fmt.Errorf("unknown provider %q", providerName)
	}
	model := modelOverride
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

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func RuntimeSummary() string {
	return fmt.Sprintf("%s/%s · Go %s · %s", runtime.GOOS, runtime.GOARCH, runtime.Version(), time.Now().Format("2006-01-02"))
}
