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

// DefaultMaxTokens is the output cap applied to a provider that names none.
//
// It is small for a modern model on purpose — it predates most of them — and
// the number is not the problem. The problem is that it used to be applied
// invisibly: a provider block with no max_tokens produced answers that stopped
// at 8192 tokens with no message, no diagnostic, and no field in the file to
// point at. It is named here so `collo doctor` can report where it is in force
// and `collo setup` can write it down rather than let it apply silently.
const DefaultMaxTokens = 8192

type Config struct {
	// Schema is the `$schema` key an editor reads to find the contract for
	// this file. It is declared here rather than tolerated, because
	// LoadOptions.Strict turns on DisallowUnknownFields: without a field to
	// decode into, a file carrying the key that makes editing bearable would
	// load fine normally and fail `collo config validate --strict`. Nothing
	// downstream reads it — it describes the file, it does not configure
	// anything.
	Schema          string               `json:"$schema,omitempty"`
	SchemaVersion   int                  `json:"schema_version,omitempty"`
	DefaultProvider string               `json:"default_provider"`
	DefaultModel    string               `json:"default_model"`
	DefaultAgent    string               `json:"default_agent,omitempty"`
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
	// Clamped lists containment settings a trusted project asked to weaken
	// and did not get. They are reported rather than applied silently.
	Clamped []ClampedField `json:"-"`
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
	EntraAuthorityHost string            `json:"entra_authority_host,omitempty"`
	Headers            map[string]string `json:"headers,omitempty"`
	MaxTokens          int               `json:"max_tokens,omitempty"`
	Context            int               `json:"context_window,omitempty"`
	// MaxTokensDefaulted records that max_tokens was absent and normalization
	// supplied one. Without it nothing downstream can tell a deliberate 8192
	// from a field the user never knew existed — and the second case is the one
	// worth reporting, because it silently truncates long answers from a model
	// that could produce far more. Diagnostics only, never serialized.
	MaxTokensDefaulted bool     `json:"-"`
	Temperature        *float64 `json:"temperature,omitempty"`
	// Reasoning is opt-in. When omitted, Collomia sends no reasoning-specific
	// request field and preserves each provider/model's own default.
	Reasoning *Reasoning `json:"reasoning,omitempty"`
	// Pricing is user-supplied because model prices change and may differ by
	// account, region, gateway, or deployment. Collomia never hardcodes it.
	Pricing                  *Pricing `json:"pricing,omitempty"`
	ConnectTimeoutSeconds    int      `json:"connect_timeout_seconds,omitempty"`
	RequestTimeoutSeconds    int      `json:"request_timeout_seconds,omitempty"`
	StreamIdleTimeoutSeconds int      `json:"stream_idle_timeout_seconds,omitempty"`
	// CredentialSource names where APIKey came from, for diagnostics only. It
	// is never serialized: it is derived on load, and writing it to a file
	// would turn a description of the environment into configuration.
	CredentialSource string `json:"-"`
}

// Reasoning is the provider-neutral subset of model reasoning controls.
// Adapters translate Effort only when it is explicitly configured.
type Reasoning struct {
	Effort string `json:"effort"`
}

// Pricing estimates provider spend from reported token usage. Rates are USD
// per one million tokens. Cached input defaults to the ordinary input rate
// when CachedInputPerMillion is omitted, which is conservative.
type Pricing struct {
	InputPerMillion       float64  `json:"input_per_million"`
	OutputPerMillion      float64  `json:"output_per_million"`
	CachedInputPerMillion *float64 `json:"cached_input_per_million,omitempty"`
	// CacheWritePerMillion prices tokens written to the provider's prompt
	// cache, which is normally charged above the ordinary input rate. When
	// unset, writes are priced at InputPerMillion: an estimate that is
	// slightly low is preferable to inventing a vendor's multiplier for an
	// endpoint the user may have pointed anywhere.
	CacheWritePerMillion *float64 `json:"cache_write_per_million,omitempty"`
}

type Permissions struct {
	// Preset selects a named containment bundle so a working configuration
	// does not require composing every switch below by hand: standard (the
	// default behavior), hardened, or frictionless. It is sugar over the
	// same fields — any field this layer sets explicitly wins over the
	// preset, and a preset can never loosen a stricter inherited layer.
	// Autonomy mode is deliberately not part of any preset.
	Preset                string   `json:"preset,omitempty"`
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
	// Network selects the posture for actions that reach the network: open
	// (the default, matching earlier releases) or scoped. Under scoped, a
	// network-bearing action is never approved automatically unless a rule or
	// a session grant covers every endpoint it declares. This is a policy
	// posture evaluated by Collomia, not OS-enforced egress confinement.
	Network string `json:"network,omitempty"`
	// Commands selects the posture for command execution: open (the default)
	// or allowlist. Under allowlist, a command is never approved
	// automatically unless a rule or a session grant covers every executable
	// it runs.
	Commands string `json:"commands,omitempty"`
	// Sandbox selects OS sandbox enforcement for commands: off, auto (the
	// compatibility-first default), or require.
	Sandbox string `json:"sandbox,omitempty"`
	// SandboxAllowNetwork permits network egress inside the sandbox.
	SandboxAllowNetwork bool `json:"sandbox_allow_network,omitempty"`
	// SandboxEgress selects how a sandboxed command reaches the network:
	// "off" (the default) leaves SandboxAllowNetwork as the all-or-nothing
	// control, while "scoped" denies direct remote egress at the OS level and
	// routes the command through a loopback broker that dials only the hosts
	// named by host-scoped allow rules.
	//
	// This is enforcement only where the sandbox backend can deny direct
	// remote traffic while leaving loopback reachable, which today means macOS
	// alone. Linux Landlock filters TCP by port and never by address, and
	// Windows AppContainer blocks loopback to unpackaged services outright, so
	// on those platforms "scoped" is refused under sandbox=require and
	// visibly degrades to SandboxAllowNetwork under auto. See internal/egress.
	SandboxEgress string `json:"sandbox_egress,omitempty"`
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
	// ProtectCredentials decides what happens when an action reaches a
	// well-known credential store — an SSH or GPG private key, a cloud CLI
	// token cache, a registry authentication file, an environment file:
	// "prompt" (the default) always asks, "deny" refuses, and "off" restores
	// the earlier behavior of treating those files as ordinary.
	//
	// Under "prompt" this is not merely the default autonomy mode reasserting
	// itself. Reaching a credential store is deliberately not coverable by a
	// blanket allow rule, a tool-wide session grant, or autopilot, because the
	// point of the control is to stop a broad approval from silently including
	// a private key. A rule naming the path is still honored, so an
	// intentional exception stays possible and stays written down.
	ProtectCredentials string `json:"protect_credentials,omitempty"`
	// Publication decides what happens when an action puts something outside
	// this machine — a package version, a container image, a pull request, a
	// release, an infrastructure apply, a push to a Git remote, a command run
	// on another host: "prompt" (the default) always asks, "deny" refuses, and
	// "off" treats those operations as ordinary commands.
	//
	// It exists for the same reason protect_credentials does. The safety
	// classifier is a taxonomy of destruction, so every deletion in these
	// tools required a fresh decision while the publishing counterpart did
	// not — and publishing a package version is less reversible than deleting
	// a deployment a controller will recreate.
	//
	// Under "prompt" it is deliberately not coverable by autopilot or a
	// tool-wide session grant. A rule that names the operation
	// (`{"command": "npm publish"}`) is honored, because an intentional
	// exception should stay possible and stay written down; a rule that names
	// only the executable (`{"command": "npm"}`) is not, because allowing a
	// package manager is not a decision to publish with it.
	Publication string `json:"publication,omitempty"`
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

// AgentDefinition names a reusable primary and/or delegated agent profile.
// Any field left empty falls back to the ordinary runtime setting.
type AgentDefinition struct {
	// Availability controls where the profile is selectable: delegate
	// (default, preserving existing configurations), primary, or both.
	Availability string `json:"availability,omitempty"`
	// Model overrides the model used for this agent, on the same provider
	// as the parent (a different reasoning tier or a cheaper/faster model).
	Model string `json:"model,omitempty"`
	// Reasoning overrides the provider's opt-in reasoning setting.
	Reasoning *Reasoning `json:"reasoning,omitempty"`
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
	// CostBudgetUSD bounds estimated provider spend using the selected
	// provider's explicitly configured pricing. Zero disables this bound.
	CostBudgetUSD float64 `json:"cost_budget_usd,omitempty"`
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
	// AgentIntegration controls who may publish retained delegated-worktree
	// changes into the parent workspace. "manual" (the default) exposes only
	// /agents apply; "reviewed" additionally gives the primary agent bounded
	// inspect/apply tools backed by the same guarded integration path.
	AgentIntegration    string   `json:"agent_integration,omitempty"`
	DisabledTools       []string `json:"disabled_tools,omitempty"`
	TranscriptDirectory string   `json:"transcript_directory,omitempty"`
	Theme               string   `json:"theme,omitempty"`
	// AlternateScreen controls whether the interactive TUI uses the terminal's
	// alternate screen buffer. It defaults to true; disabling it keeps the
	// final screen in native terminal scrollback.
	AlternateScreen bool `json:"alternate_screen"`
	// Mouse enables wheel scrolling in the transcript and click-to-select on
	// the tab bar. It defaults to true. Turning mouse reporting on means the
	// terminal routes drags to Collomia instead of its own selection, so a
	// user who copies text with the mouse more than they scroll can set this
	// to false. Most terminals still offer native selection under shift-drag.
	Mouse bool `json:"mouse"`
	// Keybindings overrides named global TUI actions. Modal safety decisions
	// retain fixed, visible keys so a local remap cannot make an approval
	// ambiguous.
	Keybindings map[string]string `json:"keybindings,omitempty"`
	// Notifications controls how the TUI gets the user's attention for
	// approvals, questions, and finished long turns: "on" (bell + terminal
	// desktop notification, the default), "bell" (bell only), or "off".
	Notifications string `json:"notifications,omitempty"`
	// ReducedMotion replaces decorative progress animation with a static
	// marker. It is opt-in and never changes input, cancellation, or controls.
	ReducedMotion bool `json:"reduced_motion,omitempty"`
	// DimBackground drops colour from the screen behind an approval, a
	// question, or another modal so the dialog is plainly the focused element.
	// It defaults to true. Setting it false keeps the transcript at full
	// saturation — for a documentation screenshot, or simply for taste. The
	// cleared gutter around the dialog stays either way; it is what keeps the
	// border from sitting against mid-word transcript fragments.
	DimBackground bool `json:"dim_background"`
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
			Network:                          "open",
			Commands:                         "open",
			Sandbox:                          "auto",
			SandboxAllowNetwork:              true,
			SandboxAllowReadOutsideWorkspace: true,
			ProtectCredentials:               ProtectCredentialsPrompt,
			Publication:                      PublicationPrompt,
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
			AgentIntegration:   "manual",
			AlternateScreen:    true,
			Mouse:              true,
			DimBackground:      true,
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
	inheritedPermissions := c.Permissions
	if err := decoder.Decode(c); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	c.Permissions.DeniedCommands = additiveStrings(inheritedDeniedCommands, c.Permissions.DeniedCommands)
	keys := flattenJSON(data)
	for _, key := range keys {
		c.Origins[key] = layer
	}
	// A preset fills only what this layer did not state itself, so an explicit
	// field always wins over the bundle that accompanies it.
	declared := make(map[string]bool, len(keys))
	for _, key := range keys {
		declared[key] = true
	}
	if declared["permissions.preset"] {
		c.applyPreset(strings.ToLower(strings.TrimSpace(c.Permissions.Preset)), layer, declared)
	}
	// A repository can tighten containment but never weaken it, by any means:
	// an explicit field, a preset, or both. The machine owner's own
	// configuration is not restricted this way — a built-in default is not a
	// choice they made, so the global layer remains free to select `off` or
	// the frictionless preset.
	if layer == "project" {
		tightened, clamped := tightenContainment(inheritedPermissions, c.Permissions)
		c.Permissions = tightened
		for _, note := range clamped {
			// Ignoring a setting silently would read as a bug. Say so.
			c.Clamped = append(c.Clamped, note)
			c.Origins["permissions."+note.Field] = "user (project request refused)"
		}
	}
	c.Layers = append(c.Layers, Layer{Name: layer, Path: path, Applied: true, Keys: keys})
	c.Source = path
	return nil
}

// Preset names. An empty preset means standard, so an existing configuration
// keeps its meaning.
const (
	PresetStandard     = "standard"
	PresetHardened     = "hardened"
	PresetFrictionless = "frictionless"
)

// Settings for permissions.protect_credentials, from weakest to strongest.
const (
	ProtectCredentialsOff    = "off"
	ProtectCredentialsPrompt = "prompt"
	ProtectCredentialsDeny   = "deny"
)

// Sandbox egress postures.
const (
	// SandboxEgressOff keeps sandbox_allow_network as the only egress control.
	SandboxEgressOff = "off"
	// SandboxEgressScoped denies direct remote egress and brokers the rest.
	SandboxEgressScoped = "scoped"
)

// Settings for permissions.publication, from weakest to strongest.
const (
	PublicationOff    = "off"
	PublicationPrompt = "prompt"
	PublicationDeny   = "deny"
)

// ProtectCredentialsSettings lists the selectable settings in increasing
// strictness.
func ProtectCredentialsSettings() []string {
	return []string{ProtectCredentialsOff, ProtectCredentialsPrompt, ProtectCredentialsDeny}
}

// PublicationSettings lists the selectable settings in increasing strictness.
func PublicationSettings() []string {
	return []string{PublicationOff, PublicationPrompt, PublicationDeny}
}

// preset is one named containment bundle. Every field it sets is an ordinary
// configuration field: a preset is a starting point a user can read and
// override, never a hidden mode.
type preset struct {
	Summary                          string
	Sandbox                          string
	SandboxAllowNetwork              bool
	SandboxAllowReadOutsideWorkspace bool
	Network                          string
	Commands                         string
	CommandEnv                       string
	ProtectCredentials               string
	Publication                      string
	AllowOutsideWorkspace            bool
}

// presets deliberately omit permissions.mode. Autonomy is the one choice a
// user should make knowingly; a bundle that quietly selected autopilot would
// be exactly the surprise these presets exist to avoid.
//
// They also omit permissions.sandbox_egress, for the same reason hardened does
// not fold in "sandbox_allow_network": false. Scoped egress is enforceable on
// macOS only, so putting it in a cross-platform bundle would make one preset
// name mean genuinely different containment on different machines — and it
// would break any build whose registries are not yet named by an allow rule.
// It stays a deliberate extra line the user writes and can read back.
var presets = map[string]preset{
	PresetStandard: {
		Summary:                          "platform sandbox where available, command networking and broad reads on",
		Sandbox:                          "auto",
		SandboxAllowNetwork:              true,
		SandboxAllowReadOutsideWorkspace: true,
		Network:                          "open",
		Commands:                         "open",
		CommandEnv:                       "minimal",
		ProtectCredentials:               ProtectCredentialsPrompt,
		Publication:                      PublicationPrompt,
	},
	PresetHardened: {
		// The strongest bundle an ordinary toolchain can still work under.
		// Denying command egress outright stays a deliberate extra line
		// (`"sandbox_allow_network": false`) because it breaks package
		// installs, so it is not folded in here.
		Summary:                          "fail-closed sandbox, confined reads, scoped endpoints and commands",
		Sandbox:                          "require",
		SandboxAllowNetwork:              true,
		SandboxAllowReadOutsideWorkspace: false,
		Network:                          "scoped",
		Commands:                         "allowlist",
		CommandEnv:                       "minimal",
		ProtectCredentials:               ProtectCredentialsDeny,
		Publication:                      PublicationDeny,
	},
	PresetFrictionless: {
		// An explicit opt-out for users whose toolchain fights containment.
		// It is never a default and never reached by inheritance.
		Summary:                          "no OS sandbox, inherited environment; policy prompts still apply",
		Sandbox:                          "off",
		SandboxAllowNetwork:              true,
		SandboxAllowReadOutsideWorkspace: true,
		Network:                          "open",
		Commands:                         "open",
		CommandEnv:                       "full",
		ProtectCredentials:               ProtectCredentialsOff,
		Publication:                      PublicationOff,
	},
}

// PresetNames lists the selectable presets in increasing strictness.
func PresetNames() []string {
	return []string{PresetFrictionless, PresetStandard, PresetHardened}
}

// PresetSummary describes one preset for diagnostics and error messages.
func PresetSummary(name string) string { return presets[name].Summary }

// applyPreset expands a named bundle into ordinary fields. It only fills
// fields this layer did not declare itself, so an explicit setting always
// wins over the preset that accompanies it. Monotonicity is not this
// function's job: tightenContainment clamps the project layer afterwards,
// whether a value came from a preset or was written out by hand.
func (c *Config) applyPreset(name, layer string, declared map[string]bool) {
	bundle, ok := presets[name]
	if !ok {
		return
	}
	origin := layer + " (preset " + name + ")"
	set := func(field string, apply func()) {
		if declared["permissions."+field] {
			return
		}
		apply()
		c.Origins["permissions."+field] = origin
	}
	set("sandbox", func() { c.Permissions.Sandbox = bundle.Sandbox })
	set("sandbox_allow_network", func() { c.Permissions.SandboxAllowNetwork = bundle.SandboxAllowNetwork })
	set("sandbox_allow_read_outside_workspace", func() {
		c.Permissions.SandboxAllowReadOutsideWorkspace = bundle.SandboxAllowReadOutsideWorkspace
	})
	set("allow_outside_workspace", func() { c.Permissions.AllowOutsideWorkspace = bundle.AllowOutsideWorkspace })
	set("network", func() { c.Permissions.Network = bundle.Network })
	set("commands", func() { c.Permissions.Commands = bundle.Commands })
	set("command_env", func() { c.Permissions.CommandEnv = bundle.CommandEnv })
	set("protect_credentials", func() { c.Permissions.ProtectCredentials = bundle.ProtectCredentials })
	set("publication", func() { c.Permissions.Publication = bundle.Publication })
}

// ClampedField records a containment setting a repository asked for and did
// not get, so the refusal is visible instead of looking like a bug.
type ClampedField struct {
	Field     string
	Requested string
	Effective string
}

func (c ClampedField) String() string {
	return fmt.Sprintf("permissions.%s: project asked for %s; kept %s (a repository can tighten containment but never weaken it)", c.Field, c.Requested, c.Effective)
}

// containmentRank orders each containment setting from weakest to strongest.
// Only these fields are clamped: they decide what an approved action can
// reach. Autonomy mode, rules, and denied commands have their own rules.
var containmentRank = map[string]map[string]int{
	"sandbox":             {"off": 0, "auto": 1, "require": 2},
	"command_env":         {"full": 0, "": 1, "minimal": 2},
	"network":             {"open": 0, "scoped": 1},
	"commands":            {"open": 0, "allowlist": 1},
	"sandbox_egress":      {"": 0, SandboxEgressOff: 0, SandboxEgressScoped: 1},
	"protect_credentials": {"off": 0, "": 1, ProtectCredentialsPrompt: 1, ProtectCredentialsDeny: 2},
	"publication":         {"off": 0, "": 1, PublicationPrompt: 1, PublicationDeny: 2},
}

// ContainmentFields lists the settings subject to monotonic clamping, sorted.
// These are the settings that decide what an approved action can reach, so
// documentation is checked against this list rather than against a hand-kept
// copy of it.
func ContainmentFields() []string {
	fields := make([]string, 0, len(containmentRank)+3)
	for field := range containmentRank {
		fields = append(fields, field)
	}
	// The boolean switches are clamped too, but carry no rank table.
	fields = append(fields, "sandbox_allow_network", "sandbox_allow_read_outside_workspace", "allow_outside_workspace")
	sort.Strings(fields)
	return fields
}

// tightenContainment keeps the stronger of what was inherited and what this
// layer asked for, field by field, and reports every setting it refused.
func tightenContainment(inherited, declared Permissions) (Permissions, []ClampedField) {
	result := declared
	var clamped []ClampedField
	enum := func(field, inheritedValue, declaredValue string, assign func(string)) {
		ranks := containmentRank[field]
		normalize := func(v string) string { return strings.ToLower(strings.TrimSpace(v)) }
		inheritedValue, declaredValue = normalize(inheritedValue), normalize(declaredValue)
		if ranks[declaredValue] >= ranks[inheritedValue] {
			return
		}
		assign(inheritedValue)
		clamped = append(clamped, ClampedField{Field: field, Requested: declaredValue, Effective: inheritedValue})
	}
	boolean := func(field string, inheritedValue, declaredValue bool, assign func(bool)) {
		// For these switches false is the stronger setting.
		if !declaredValue || !inheritedValue {
			assign(inheritedValue && declaredValue)
		}
		if declaredValue && !inheritedValue {
			clamped = append(clamped, ClampedField{Field: field, Requested: "true", Effective: "false"})
		}
	}
	enum("sandbox", inherited.Sandbox, declared.Sandbox, func(v string) { result.Sandbox = v })
	enum("command_env", inherited.CommandEnv, declared.CommandEnv, func(v string) { result.CommandEnv = v })
	enum("network", inherited.Network, declared.Network, func(v string) { result.Network = v })
	enum("commands", inherited.Commands, declared.Commands, func(v string) { result.Commands = v })
	enum("sandbox_egress", inherited.SandboxEgress, declared.SandboxEgress, func(v string) { result.SandboxEgress = v })
	enum("protect_credentials", inherited.ProtectCredentials, declared.ProtectCredentials, func(v string) {
		result.ProtectCredentials = v
	})
	enum("publication", inherited.Publication, declared.Publication, func(v string) { result.Publication = v })
	boolean("sandbox_allow_network", inherited.SandboxAllowNetwork, declared.SandboxAllowNetwork, func(v bool) { result.SandboxAllowNetwork = v })
	boolean("sandbox_allow_read_outside_workspace", inherited.SandboxAllowReadOutsideWorkspace, declared.SandboxAllowReadOutsideWorkspace, func(v bool) {
		result.SandboxAllowReadOutsideWorkspace = v
	})
	boolean("allow_outside_workspace", inherited.AllowOutsideWorkspace, declared.AllowOutsideWorkspace, func(v bool) { result.AllowOutsideWorkspace = v })
	return result, clamped
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
		c.Permissions.Sandbox = "auto"
	}
	// An omitted posture keeps the behavior every earlier release had. Only
	// an explicit choice tightens it, so no existing configuration changes
	// meaning when it is loaded by this version.
	c.Permissions.Network = strings.ToLower(strings.TrimSpace(c.Permissions.Network))
	if c.Permissions.Network == "" {
		c.Permissions.Network = "open"
	}
	c.Permissions.Commands = strings.ToLower(strings.TrimSpace(c.Permissions.Commands))
	if c.Permissions.Commands == "" {
		c.Permissions.Commands = "open"
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
	if c.Options.AgentIntegration == "" {
		c.Options.AgentIntegration = "manual"
	}
	c.Options.AgentIntegration = strings.ToLower(strings.TrimSpace(c.Options.AgentIntegration))
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
		if p.Reasoning != nil {
			p.Reasoning.Effort = strings.ToLower(strings.TrimSpace(p.Reasoning.Effort))
		}
		if skipEnvironmentExpansion {
			p.BaseURL = strings.TrimRight(p.BaseURL, "/")
			p.EntraScope = strings.TrimSpace(p.EntraScope)
			p.EntraTenantID = strings.TrimSpace(p.EntraTenantID)
			p.EntraAuthorityHost = strings.TrimSpace(p.EntraAuthorityHost)
		} else {
			p = resolveProviderEnvironment(name, p)
		}
		if p.MaxTokens <= 0 {
			p.MaxTokens, p.MaxTokensDefaulted = DefaultMaxTokens, true
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
	c.DefaultAgent = strings.TrimSpace(c.DefaultAgent)
	for name, profile := range c.Agents {
		profile.Availability = strings.ToLower(strings.TrimSpace(profile.Availability))
		if profile.Reasoning != nil {
			profile.Reasoning.Effort = strings.ToLower(strings.TrimSpace(profile.Reasoning.Effort))
		}
		c.Agents[name] = profile
	}
	for name, server := range c.MCP {
		c.MCP[name] = normalizeMCPServer(server, !skipEnvironmentExpansion)
	}
}

// ResolveProvider expands environment references and resolves the credential
// for one provider definition, exactly as loading the merged configuration
// would.
//
// It exists for callers that deliberately work on a single configuration layer
// — `collo setup --provider`, which re-verifies a provider recorded in the
// global file — and still need the credential a session would actually use.
// Re-implementing the api_key → api_key_env → provider-family variable →
// credential-store precedence at such a call site is how the two orders drift
// apart, and a wizard that authenticates differently from the session it is
// configuring would verify the wrong thing.
func ResolveProvider(name string, p Provider) Provider {
	return resolveProviderEnvironment(name, p)
}

func resolveProviderEnvironment(name string, p Provider) Provider {
	p.BaseURL = strings.TrimRight(expandEnv(p.BaseURL), "/")
	p.APIKey = expandEnv(p.APIKey)
	if p.APIKey != "" {
		p.CredentialSource = "api_key"
	}
	if p.APIKey == "" && p.APIKeyEnv != "" {
		if p.APIKey = os.Getenv(p.APIKeyEnv); p.APIKey != "" {
			p.CredentialSource = "environment " + p.APIKeyEnv
		}
	}
	if p.APIKey == "" && usesStoredCredential(p) {
		if secret, ok, err := lookupStoredCredential(name); err == nil && ok {
			p.APIKey = secret
			p.CredentialSource = "credential store"
		}
	}
	for key, value := range p.Headers {
		p.Headers[key] = expandEnv(value)
	}
	p.EntraScope = strings.TrimSpace(expandEnv(p.EntraScope))
	p.EntraTenantID = strings.TrimSpace(expandEnv(p.EntraTenantID))
	p.EntraAuthorityHost = strings.TrimSpace(expandEnv(p.EntraAuthorityHost))
	return p
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
	errs = appendEnumErrors(errs,
		enumField{field: "permissions.mode", value: c.Permissions.Mode, allowed: AutonomyModes()},
		enumField{field: "permissions.sandbox", value: c.Permissions.Sandbox, allowed: SandboxModes(), optional: true},
		enumField{field: "permissions.preset", value: c.Permissions.Preset, allowed: PresetNames(), optional: true, fold: true},
		enumField{field: "permissions.network", value: c.Permissions.Network, allowed: NetworkPostures(), optional: true},
		enumField{field: "permissions.commands", value: c.Permissions.Commands, allowed: CommandPostures(), optional: true},
		enumField{field: "permissions.sandbox_egress", value: c.Permissions.SandboxEgress, allowed: SandboxEgressModes(), optional: true, fold: true},
		enumField{field: "permissions.command_env", value: c.Permissions.CommandEnv, allowed: CommandEnvModes(), optional: true},
		enumField{field: "permissions.protect_credentials", value: c.Permissions.ProtectCredentials, allowed: ProtectCredentialsSettings(), optional: true},
		enumField{field: "permissions.publication", value: c.Permissions.Publication, allowed: PublicationSettings(), optional: true},
	)
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
		if !slices.Contains(ProviderTypes(), provider.Type) {
			errs = append(errs, FieldError{field + ".type", fmt.Sprintf("unsupported type %q", provider.Type)})
		}
		if provider.Type != "bedrock" && provider.BaseURL == "" {
			errs = append(errs, FieldError{field + ".base_url", "required for this provider type"})
		}
		if provider.Type == "bedrock" {
			errs = appendEnumErrors(errs, enumField{
				field: field + ".auth", value: provider.Auth,
				allowed: BedrockAuthModes(), optional: true, suffix: " for Bedrock",
			})
			if provider.Auth == "sigv4" && (provider.APIKey != "" || provider.APIKeyEnv != "") {
				errs = append(errs, FieldError{field + ".api_key_env", "is not used with auth=sigv4; use the AWS credential chain or change auth to bearer/auto"})
			}
			if provider.Auth == "bearer" && provider.Profile != "" {
				errs = append(errs, FieldError{field + ".profile", "is not used with auth=bearer; remove it or change auth to sigv4/auto"})
			}
		}
		if provider.Type == "azure-openai" || provider.Type == "azure-foundry" || provider.Type == "azure-foundry-anthropic" {
			errs = appendEnumErrors(errs, enumField{
				field: field + ".auth", value: provider.Auth,
				allowed: AzureAuthModes(), optional: true, suffix: " for Azure providers",
			})
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
		// Token limits. Only the unsatisfiable combination is an error here;
		// an absent field is a warning, reported by `collo doctor`, because
		// refusing to start over a field that has always been optional would
		// break every configuration written before it was validated.
		if provider.Context < 0 {
			errs = append(errs, FieldError{field + ".context_window", "must not be negative"})
		}
		if provider.MaxTokens < 0 {
			errs = append(errs, FieldError{field + ".max_tokens", "must not be negative"})
		}
		if provider.Context > 0 && provider.MaxTokens >= provider.Context {
			// The output cap is spent out of the same budget as the prompt, so
			// this configuration cannot be satisfied by any request: the
			// provider rejects it, and ValidateRequest already refuses it
			// before the network. Catching it here names the two fields
			// instead of surfacing as a provider error mid-session.
			errs = append(errs, FieldError{field + ".max_tokens", fmt.Sprintf(
				"%d is at or above context_window %d; the output cap is spent from the same budget as the prompt, so no request can satisfy this",
				provider.MaxTokens, provider.Context)})
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
		if provider.Reasoning != nil {
			if err := validateReasoningEffort(provider.Reasoning.Effort); err != nil {
				errs = append(errs, FieldError{field + ".reasoning.effort", err.Error()})
			}
		}
		if provider.Pricing != nil {
			if provider.Pricing.InputPerMillion <= 0 {
				errs = append(errs, FieldError{field + ".pricing.input_per_million", "must be greater than zero"})
			}
			if provider.Pricing.OutputPerMillion <= 0 {
				errs = append(errs, FieldError{field + ".pricing.output_per_million", "must be greater than zero"})
			}
			if provider.Pricing.CachedInputPerMillion != nil && *provider.Pricing.CachedInputPerMillion < 0 {
				errs = append(errs, FieldError{field + ".pricing.cached_input_per_million", "must not be negative"})
			}
			if provider.Pricing.CacheWritePerMillion != nil && *provider.Pricing.CacheWritePerMillion < 0 {
				errs = append(errs, FieldError{field + ".pricing.cache_write_per_million", "must not be negative"})
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
		errs = appendEnumErrors(errs, enumField{
			field: field + ".action", value: rule.Action, allowed: RuleActions(),
		})
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
		// A command pattern is matched either against an executable name or,
		// when it contains a space, against an operation such as
		// "npm publish". Both forms are single-spaced and untrimmed patterns
		// can match neither, so a rule that could never fire is reported here
		// rather than sitting in the file looking like protection.
		if trimmed := strings.TrimSpace(rule.Command); rule.Command != "" && (trimmed != rule.Command || strings.Contains(trimmed, "  ")) {
			errs = append(errs, FieldError{field + ".command", fmt.Sprintf("pattern %q cannot match: use an executable such as \"npm\" or a single-spaced operation such as \"npm publish\"", rule.Command)})
		}
	}
	for name, server := range c.MCP {
		errs = append(errs, ValidateMCPServer(name, server)...)
	}
	for name, a := range c.Agents {
		field := "agents." + name
		if (name == "default" || name == "none") && AgentAvailableFor(a, "primary") {
			errs = append(errs, FieldError{field + ".availability", "primary profiles cannot use the reserved names default or none"})
		}
		errs = appendEnumErrors(errs, enumField{
			field: field + ".availability", value: a.Availability,
			allowed: AgentAvailabilities(), optional: true,
		})
		if a.Reasoning != nil {
			if err := validateReasoningEffort(a.Reasoning.Effort); err != nil {
				errs = append(errs, FieldError{field + ".reasoning.effort", err.Error()})
			}
		}
		if a.MaxIterations < 0 {
			errs = append(errs, FieldError{field + ".max_iterations", "must not be negative"})
		}
		if a.TokenBudget < 0 {
			errs = append(errs, FieldError{field + ".token_budget", "must not be negative"})
		}
		if a.CostBudgetUSD < 0 {
			errs = append(errs, FieldError{field + ".cost_budget_usd", "must not be negative"})
		}
		if a.TimeoutSeconds < 0 || a.TimeoutSeconds > 3600 {
			errs = append(errs, FieldError{field + ".timeout_seconds", "must be between 0 and 3600"})
		}
		errs = appendEnumErrors(errs, enumField{
			field: field + ".permissions.mode", value: a.Permissions.Mode,
			allowed: AgentAutonomyModes(), optional: true,
		})
		for i, pattern := range a.Permissions.DeniedCommands {
			if _, err := regexp.Compile(pattern); err != nil {
				errs = append(errs, FieldError{fmt.Sprintf("%s.permissions.denied_commands[%d]", field, i), err.Error()})
			}
		}
		for i, rule := range a.Permissions.Rules {
			ruleField := fmt.Sprintf("%s.permissions.rules[%d]", field, i)
			if !slices.Contains(AgentRuleActions(), rule.Action) {
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
	if c.DefaultAgent != "" {
		profile, ok := c.Agents[c.DefaultAgent]
		if !ok {
			errs = append(errs, FieldError{"default_agent", fmt.Sprintf("agent %q is not configured", c.DefaultAgent)})
		} else if !AgentAvailableFor(profile, "primary") {
			errs = append(errs, FieldError{"default_agent", fmt.Sprintf("agent %q is not available to the primary agent", c.DefaultAgent)})
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
	errs = appendEnumErrors(errs,
		enumField{field: "options.agent_integration", value: c.Options.AgentIntegration, allowed: AgentIntegrationModes(), optional: true},
		enumField{field: "options.notifications", value: c.Options.Notifications, allowed: NotificationModes(), optional: true, fold: true},
	)
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

func validateReasoningEffort(effort string) error {
	if slices.Contains(ReasoningEfforts(), effort) {
		return nil
	}
	return fmt.Errorf("must be %s (got %q)", englishList(ReasoningEfforts()), effort)
}

// AgentAvailableFor reports whether a named profile may be used in the
// requested role. Empty availability retains the historical delegate-only
// meaning so existing agent configurations do not unexpectedly affect the
// primary conversation.
func AgentAvailableFor(profile AgentDefinition, role string) bool {
	availability := strings.ToLower(strings.TrimSpace(profile.Availability))
	if availability == "" {
		availability = "delegate"
	}
	return availability == "both" || availability == role
}

// PrimaryAgent resolves a primary profile by name.
func (c Config) PrimaryAgent(name string) (AgentDefinition, error) {
	if name == "default" || name == "none" {
		name = ""
	}
	if name == "" {
		return AgentDefinition{}, nil
	}
	profile, ok := c.Agents[name]
	if !ok {
		return AgentDefinition{}, fmt.Errorf("unknown agent profile %q", name)
	}
	if !AgentAvailableFor(profile, "primary") {
		return AgentDefinition{}, fmt.Errorf("agent profile %q is not available to the primary agent", name)
	}
	return profile, nil
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
	b.WriteString(c.presetReport())
	if len(c.Clamped) > 0 {
		b.WriteString("\nRefused project containment changes:\n")
		for _, clamped := range c.Clamped {
			fmt.Fprintf(&b, "  %s\n", clamped)
		}
	}
	return b.String()
}

// presetReport lists the fields a preset filled in, so a bundle is never a
// value whose source the user cannot see. Fields the user set themselves are
// attributed to their layer and do not appear here.
func (c Config) presetReport() string {
	var expanded []string
	for key, origin := range c.Origins {
		if strings.Contains(origin, "(preset ") {
			expanded = append(expanded, fmt.Sprintf("  %-44s %s", key, origin))
		}
	}
	if len(expanded) == 0 {
		return ""
	}
	sort.Strings(expanded)
	return "\nExpanded by a preset (an explicit field always wins over these):\n" + strings.Join(expanded, "\n") + "\n"
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
