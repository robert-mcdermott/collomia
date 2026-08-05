package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ReferencePath returns the non-loaded annotated reference path adjacent to
// an active JSON configuration file.
func ReferencePath(configPath string) string {
	ext := filepath.Ext(configPath)
	return strings.TrimSuffix(configPath, ext) + ".example.jsonc"
}

// WriteStarter writes a scope-appropriate active configuration. Project
// starters only tighten the permission mode so other settings continue to
// inherit user and built-in defaults. Global starters expose commonly edited
// safe permission and runtime defaults, but deliberately name no provider: a
// static file cannot establish which endpoint, credential, or model works.
func WriteStarter(path string, global bool) error {
	type starterPermissions struct {
		Mode                             string   `json:"mode"`
		AllowOutsideWorkspace            *bool    `json:"allow_outside_workspace,omitempty"`
		Sandbox                          string   `json:"sandbox,omitempty"`
		SandboxAllowNetwork              *bool    `json:"sandbox_allow_network,omitempty"`
		SandboxAllowReadOutsideWorkspace *bool    `json:"sandbox_allow_read_outside_workspace,omitempty"`
		SandboxReadableRoots             []string `json:"sandbox_readable_roots,omitempty"`
		SandboxWritableRoots             []string `json:"sandbox_writable_roots,omitempty"`
	}
	type starterOptions struct {
		MaxIterations               int               `json:"max_iterations"`
		MaxToolOutputBytes          int               `json:"max_tool_output_bytes"`
		DelegateMaxConcurrency      int               `json:"delegate_max_concurrency"`
		DelegateProviderConcurrency map[string]int    `json:"delegate_provider_concurrency"`
		AgentIntegration            string            `json:"agent_integration"`
		AlternateScreen             bool              `json:"alternate_screen"`
		Mouse                       bool              `json:"mouse"`
		ReducedMotion               bool              `json:"reduced_motion"`
		DimBackground               bool              `json:"dim_background"`
		Keybindings                 map[string]string `json:"keybindings"`
	}
	type starterConfig struct {
		Schema          string              `json:"$schema"`
		SchemaVersion   int                 `json:"schema_version"`
		DefaultProvider string              `json:"default_provider,omitempty"`
		DefaultModel    string              `json:"default_model,omitempty"`
		Providers       map[string]Provider `json:"providers,omitempty"`
		Permissions     *starterPermissions `json:"permissions,omitempty"`
		Options         *starterOptions     `json:"options,omitempty"`
	}

	cfg := starterConfig{Schema: SchemaReference, SchemaVersion: CurrentSchemaVersion}
	if global {
		inactive := false
		commandNetwork := true
		broadReads := true
		cfg.Permissions = &starterPermissions{
			Mode:                             "ask",
			AllowOutsideWorkspace:            &inactive,
			Sandbox:                          "auto",
			SandboxAllowReadOutsideWorkspace: &broadReads,
			// Network stays available inside the default sandbox. Users who
			// prefer fail-closed command networking can set this to false
			// explicitly.
			SandboxAllowNetwork: &commandNetwork,
		}
		cfg.Options = &starterOptions{
			MaxIterations:               24,
			MaxToolOutputBytes:          64 * 1024,
			DelegateMaxConcurrency:      4,
			DelegateProviderConcurrency: map[string]int{},
			AgentIntegration:            "manual",
			AlternateScreen:             true,
			Mouse:                       true,
			ReducedMotion:               false,
			DimBackground:               true,
			Keybindings:                 DefaultKeybindings(),
		}
	} else {
		cfg.Permissions = &starterPermissions{Mode: "ask"}
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := writeGeneratedFile(path, append(data, '\n')); err != nil {
		return err
	}
	// The `$schema` key above points at a sibling, so the sibling has to exist
	// or the reference is a broken link that makes the editor silently offer
	// nothing — indistinguishable, from the user's side, from not having
	// shipped a schema at all.
	_, err = WriteSchema(path)
	return err
}

// WriteSchema places the generated configuration schema beside a configuration
// file and returns the path written.
//
// A sibling file rather than a hosted URL, because the schema describes the
// fields *this* build understands: a URL would describe whichever release
// published it, so an editor pointed at one would offer fields an older or
// locally built binary does not have. That is the drift a generated schema
// exists to remove, and putting it back in at the last step would be the same
// mistake in a new place.
func WriteSchema(configPath string) (string, error) {
	target := filepath.Join(filepath.Dir(configPath), SchemaFileName)
	if err := writeGeneratedFile(target, JSONSchema()); err != nil {
		return "", err
	}
	return target, nil
}

// WriteReference writes the exhaustive JSONC reference. It is intentionally
// not a recognized configuration filename and is never loaded by Collomia.
func WriteReference(path string) error {
	return writeGeneratedFile(path, []byte(ConfigReference()))
}

func writeGeneratedFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// ConfigReference returns an annotated, exhaustive configuration reference.
// Copy only settings you intend to override into the active strict-JSON file.
func ConfigReference() string {
	return strings.TrimSpace(configReferenceJSONC) + "\n"
}

const configReferenceJSONC = `
// Collomia configuration reference (JSONC documentation; this file is not loaded).
// Copy only the settings you intend to override into .collomia.json or the
// user-level config.json. Active configuration files remain strict JSON.
{
  // Editor contract for this file. 'collo schema config' generates it from the
  // build that will read the configuration, and 'collo init' / 'collo setup'
  // write it beside the file they create. With it, an editor offers completion,
  // hover documentation, and inline validation for every field below — which is
  // the point, since an active configuration file is strict JSON and cannot
  // carry the comments this reference is made of.
  "$schema": "./collomia.schema.json",

  // Current configuration schema. Files without this field are treated as v1.
  "schema_version": 1,

  // Provider selected when no CLI or environment override is present.
  "default_provider": "ollama",
  "default_model": "qwen3-coder",
  // Optional named profile used for the primary conversation. CLI --agent
  // and interactive /agent selection override it.
  "default_agent": "builder",

  // Keep only providers you use. Secrets should normally use api_key_env.
  //
  // Set both token limits on every provider, and prefer 'collo setup' to
  // writing them by hand — it asks the endpoint. They fail silently and in
  // opposite directions when left out:
  //   context_window  omitted -> automatic compaction never runs, and a long
  //                   session ends at a provider context-length error instead
  //                   of compacting.
  //   max_tokens      omitted -> every answer stops at 8192 tokens, with no
  //                   message. On a modern model that is a small fraction of
  //                   what it can produce.
  // 'collo doctor' reports both, and 'collo setup --provider NAME' rewrites
  // them for a provider that is already configured.
  "providers": {
    "ollama": {
      "type": "openai-compatible",
      "base_url": "http://127.0.0.1:11434/v1",
      "model": "qwen3-coder",
      // max_tokens is the output budget, spent from the same window as the
      // prompt: it must be below context_window, and validation refuses a
      // configuration where it is not.
      "max_tokens": 8192,
      "context_window": 32768
    },
    "openrouter": {
      "type": "openai",
      "base_url": "https://openrouter.ai/api/v1",
      "api_key_env": "OR_API_KEY",
      "model": "z-ai/glm-5.2",
      "max_tokens": 128000,
      "context_window": 500000,
      // Connection establishment, whole request, and time between response
      // chunks are bounded independently. Zero/omitted uses these defaults.
      "connect_timeout_seconds": 10,
      "request_timeout_seconds": 1800,
      "stream_idle_timeout_seconds": 300
    },
    "openai": {
      "type": "openai",
      "base_url": "https://api.openai.com/v1",
      "api_key_env": "OPENAI_API_KEY",
      "model": "gpt-5.1-codex-mini",
      "max_tokens": 8192,
      "context_window": 200000,
      "temperature": 0.2,
      // Entirely opt-in. Omit reasoning to preserve the provider/model
      // default. Unsupported explicit controls are never silently invented.
      "reasoning": {"effort": "medium"},
      // Optional user-maintained USD rates per one million tokens. Collomia
      // never ships model prices because providers and contracts change.
      // "cache_write_per_million" prices tokens written to the provider's
      // prompt cache, normally above the ordinary input rate; omit it to
      // price writes at "input_per_million".
      "pricing": {
        "input_per_million": 1.25,
        "output_per_million": 10.0,
        "cached_input_per_million": 0.125,
        "cache_write_per_million": 1.5625
      }
    },
    "anthropic": {
      "type": "anthropic",
      "base_url": "https://api.anthropic.com",
      "api_key_env": "ANTHROPIC_API_KEY",
      "model": "claude-sonnet-4-6",
      "max_tokens": 8192,
      "context_window": 200000
    },
    "custom-openai-compatible": {
      "type": "openai-compatible",
      "base_url": "https://example.com/v1",
      "api_key": "${CUSTOM_API_KEY}",
      "api_key_env": "CUSTOM_API_KEY",
      "auth": "bearer",
      "headers": {
        "X-Custom-Header": "${CUSTOM_HEADER}"
      },
      "model": "your-model-id",
      "max_tokens": 8192,
      "context_window": 128000,
      "temperature": 0.2
    },
    "bedrock": {
      "type": "bedrock",
      // auto (default) uses a configured/standard Bedrock bearer token when
      // present, otherwise the AWS SDK credential chain. Use sigv4 or bearer
      // to require one family explicitly.
      "auth": "sigv4",
      "region": "us-west-2",
      "profile": "development",
      "model": "your-bedrock-model-id",
      "max_tokens": 8192
    },
    "bedrock-api-key": {
      "type": "bedrock",
      "auth": "bearer",
      "api_key_env": "AWS_BEARER_TOKEN_BEDROCK",
      "region": "us-west-2",
      "model": "your-bedrock-model-id",
      "max_tokens": 8192
    },
    "bedrock-mantle": {
      "type": "bedrock-mantle",
      "base_url": "https://bedrock-mantle.us-west-2.api.aws/v1",
      "api_key_env": "AWS_BEARER_TOKEN_BEDROCK",
      "model": "openai.gpt-oss-120b",
      "max_tokens": 8192
    },
    "azure-openai": {
      "type": "azure-openai",
      "base_url": "https://your-resource.openai.azure.com",
      // entra uses DefaultAzureCredential and refreshes short-lived tokens.
      // It checks service-principal/workload/managed identity first, then
      // developer sign-ins such as az login and azd auth login.
      "auth": "entra",
      // These three settings are optional. The scope below is the default for
      // traditional Azure OpenAI; set tenant/authority for multi-tenant or
      // sovereign-cloud environments.
      "entra_scope": "https://cognitiveservices.azure.com/.default",
      "entra_tenant_id": "your-tenant-id",
      "entra_authority_host": "https://login.microsoftonline.com/",
      "deployment": "your-deployment",
      "api_version": "2024-10-21",
      "model": "your-deployment",
      "max_tokens": 8192
    },
    "azure-foundry": {
      "type": "azure-foundry",
      "base_url": "https://your-resource.openai.azure.com/openai/v1",
      "auth": "api_key",
      "api_key_env": "AZURE_FOUNDRY_API_KEY",
      "model": "your-deployment",
      "max_tokens": 8192
    },
    "azure-foundry-claude": {
      "type": "azure-foundry-anthropic",
      "base_url": "https://your-resource.services.ai.azure.com/anthropic",
      // Foundry OpenAI/v1 and Claude use https://ai.azure.com/.default by
      // default when auth=entra.
      "auth": "entra",
      "model": "your-claude-deployment",
      "max_tokens": 8192
    }
  },

  "permissions": {
    // frictionless | standard | hardened. A preset is a starting point, not a
    // mode: it only fills the containment fields below that you do not set
    // yourself, and every value it chooses is visible in "collo config show".
    // Omit it entirely and you get standard behavior.
    //
    //   standard      platform sandbox where available, command networking and
    //                 broad reads on. This is what you get with no preset.
    //   hardened      sandbox: require, reads confined to the workspace,
    //                 network: scoped, commands: allowlist, command_env:
    //                 minimal. Add "sandbox_allow_network": false for offline.
    //   frictionless  no OS sandbox, inherited environment. An explicit opt-out
    //                 for a toolchain that fights containment; policy prompts
    //                 and command-safety denials still apply.
    //
    // A preset never loosens a stricter layer above it, and never sets "mode":
    // autonomy stays a choice you make knowingly.
    "preset": "standard",
    // ask | workspace | autopilot
    "mode": "ask",
    "allow_outside_workspace": false,
    "allowed_tools": [],
    "denied_tools": [],
    // Additional regex hard denials. These append across scopes and are
    // separate from Collomia's non-configurable catastrophic-outcome checks.
    // An empty list adds nothing; lower layers cannot remove inherited rules.
    "denied_commands": [],
    // Rules are ordered; the first matching allow, prompt, or deny wins.
    // A host matches the endpoints a command's text names (a URL, an ssh
    // destination, a Git remote URL), the endpoint of an HTTP-transport MCP
    // server, and the endpoints web_search and web_fetch declare. An allow
    // rule never covers an endpoint Collomia could not read, such as a named
    // Git remote or a registry chosen by configuration.
    "rules": [
      {
        "action": "allow",
        "tool": "run_command",
        "command": "go",
        "reason": "project build tooling"
      },
      // web_search declares every endpoint it may fail over to, so a rule
      // naming only one of them would stop covering the search the moment the
      // primary endpoint failed. Match the whole family instead.
      {
        "action": "allow",
        "tool": "web_search",
        "host": "*.duckduckgo.com",
        "reason": "built-in web search needs no approval"
      },
      {
        "action": "allow",
        "tool": "web_fetch",
        "host": "pkg.go.dev",
        "reason": "language documentation"
      },
      {
        "action": "deny",
        "path": "/work/repo/secrets/**",
        "reason": "credentials"
      },
      {
        "action": "allow",
        "host": "proxy.example.com",
        "reason": "internal package mirror"
      },
      {
        "action": "prompt",
        "host": "*.example.com"
      },
      {
        "action": "prompt",
        "server": "docs"
      }
    ],
    // open | scoped. open (the default) matches earlier releases. scoped never
    // approves a network-bearing action automatically unless a rule or a
    // session grant covers every endpoint it declares. This is Collomia's own
    // policy posture, not OS-enforced egress confinement: a program that opens
    // a socket without saying so in its command line is bounded by the sandbox
    // below, not by this setting.
    "network": "open",
    // open | allowlist. allowlist never approves a command automatically
    // unless a rule or session grant covers every executable it runs.
    "commands": "open",
    // off | auto | require. auto is the default and uses the platform backend
    // when available; off is an explicit compatibility escape hatch that only
    // the global configuration may select. A project file can tighten any
    // containment setting but never weaken one.
    "sandbox": "auto",
    // The compatibility default is true. This reference shows the explicit
    // offline override: false denies sandboxed command network access. It does
    // not affect providers or remote MCP, which run in the Collomia process.
    "sandbox_allow_network": false,
    // off | scoped. off (the default) leaves sandbox_allow_network above as the
    // all-or-nothing egress control. scoped is the narrower alternative: the OS
    // sandbox denies direct remote traffic, and the command is pointed at a
    // loopback broker that dials only the hosts named by "allow" rules with a
    // "host" — the same rules the policy layer already matches, so there is no
    // second list to keep in step. A refused destination fails with a message
    // naming the host and this setting.
    //
    // macOS only, and deliberately so. Seatbelt can deny remote egress while
    // leaving loopback reachable, which is what makes this enforcement rather
    // than a convention. Linux Landlock filters TCP by port and never by
    // address, so an allowlist would be bypassable on the broker's own port;
    // Windows AppContainer blocks loopback to unpackaged services, so a
    // sandboxed command cannot reach the broker at all. On both, "scoped" is
    // refused under "sandbox": "require" and degrades visibly under "auto",
    // leaving sandbox_allow_network in charge. No preset sets this.
    "sandbox_egress": "off",
    // The compatibility default is true (broad command reads). Set false to
    // deny ordinary user-data reads outside the workspace while retaining the
    // runtime roots needed to launch normal system tools. Add only required
    // dependency/cache roots below.
    "sandbox_allow_read_outside_workspace": false,
    "sandbox_readable_roots": [],
    // Optional extra write locations for build/package caches. Relative paths
    // resolve from the workspace. Keep this list narrow.
    "sandbox_writable_roots": [],
    // full | minimal. Sandboxed commands default to minimal when omitted;
    // choose full deliberately when a command needs inherited credentials,
    // proxy settings, or other toolchain variables.
    "command_env": "minimal",
    // off | prompt | deny. Decides what happens when an action reaches a
    // well-known credential store: an SSH or GPG private key, a cloud CLI token
    // cache, a registry authentication file, a .env. prompt (the default) always
    // asks, deny refuses outright, off treats those files as ordinary.
    //
    // Under prompt this is stronger than the ask mode alone. A blanket allow
    // rule, a tool-wide "always allow", and autopilot all decline to cover a
    // credential store, so a broad approval cannot sweep a private key in as a
    // side effect. A rule naming the path is still honored, which keeps an
    // intentional exception possible and written down:
    //
    //   { "action": "allow", "path": "/work/repo/.env", "reason": "app config" }
    "protect_credentials": "prompt",
    // off | prompt | deny. Decides what happens when an action puts something
    // outside this machine: a package version, a container image, a pull
    // request or release, an infrastructure apply, a push to a Git remote, a
    // command run on another host. prompt (the default) always asks, deny
    // refuses outright, off treats those operations as ordinary commands.
    //
    // The rest of the safety classifier is a taxonomy of destruction, so
    // "terraform destroy" and "kubectl delete" always required a fresh
    // decision while "terraform apply" and "npm publish" did not — even though
    // a published version is harder to take back than a deployment a
    // controller recreates. This closes that asymmetry.
    //
    // Like protect_credentials it is not coverable by autopilot or a
    // tool-wide "always allow". A rule naming the operation is honored, which
    // is what makes "install freely, ask before publishing" expressible:
    //
    //   { "action": "allow", "command": "npm install" }
    //   { "action": "deny",  "command": "npm publish" }
    //
    // A command pattern containing a space matches an operation; one without
    // matches an executable name. Run "collo policy check <command>" to print
    // the exact operation string a command produces.
    "publication": "prompt",
    // Optional executable receiving approval requests as JSON on stdin.
    "reviewer_command": ""
  },

  // MCP examples remain inert until explicitly enabled and trusted.
  "mcp": {
    "example-stdio": {
      "transport": "stdio",
      "trusted": false,
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "."],
      "env": {
        "EXAMPLE_TOKEN": "${EXAMPLE_MCP_TOKEN}"
      },
      "disabled": true,
      "timeout_seconds": 30
    },
    "example-http": {
      "transport": "streamable-http",
      "trusted": false,
      "url": "https://example.com/mcp",
      "headers": {
        "Authorization": "Bearer ${EXAMPLE_MCP_TOKEN}"
      },
      "disabled": true,
      "timeout_seconds": 30
    }
  },

  "options": {
    "max_iterations": 24,
    // Active-context preview per returned tool string. Durable sessions may
    // retain a quota-bound larger copy for read_tool_result; ephemeral runs do not.
    "max_tool_output_bytes": 65536,
    // Session-wide delegated tasks. Provider entries may only tighten the
    // global limit; queue time counts against each profile timeout.
    "delegate_max_concurrency": 4,
    "delegate_provider_concurrency": {
      "openrouter": 2
    },
    // manual keeps delegated publication behind /agents apply; operators can
    // still /agents verify and compare retained candidates. reviewed also lets
    // the primary inspect exact child diffs, run repository-detected commands
    // under run_command policy, compare candidates, and selectively publish
    // accepted hunks. A child pass never grants permission or proves the
    // combined parent workspace. Nothing commits, merges, or pushes.
    "agent_integration": "manual",
    // The Orchestrated Goal execution envelope. Each bound is a speed bump,
    // not a wall: reaching one stops the graph, keeps every accepted node and
    // retained candidate, and asks you — /orchestrate extend grants another
    // envelope of the same size and continues. Zero uses the built-in default.
    // Tokens count new input plus output; prompt tokens served from the
    // provider cache are reported but not charged. Cost is enforced only when
    // the provider has complete pricing.
    "orchestration_max_iterations": 96,
    "orchestration_max_tokens": 1000000,
    "orchestration_max_cost_usd": 5,
    "orchestration_max_active_wall_seconds": 1800,
    "disabled_tools": [],
    "transcript_directory": "/path/to/transcripts",
    "theme": "collomia",
    // true uses a clean full-screen buffer. false keeps the final TUI frame
    // in the terminal's native scrollback; --no-alt-screen overrides it.
    "alternate_screen": true,
    // true lets the wheel scroll the transcript and a click select a tab.
    // While it is on the terminal routes drags here rather than to its own
    // selection; set false to keep native mouse selection everywhere. This is
    // only the starting state: alt+m releases and reclaims the mouse during a
    // session, so text stays selectable without giving up wheel scrolling.
    "mouse": true,
    // Optional. false preserves the animated working indicator. true uses a
    // static marker without changing input, cancellation, or other controls.
    "reduced_motion": false,
    // true drops colour from the screen behind an approval or a question so
    // the dialog is plainly the focused element. false leaves the transcript
    // at full saturation, which is what you want for a screenshot. The
    // cleared gutter around the dialog is kept either way.
    "dim_background": true,
    // Global TUI actions are remappable. Approval/question decision keys stay
    // fixed and are always shown in their dialog.
    "keybindings": {
      "agent_control": "alt+a",
      "next_tab": "ctrl+t",
      "toggle_mouse": "alt+m",
      "toggle_tool_output": "ctrl+o",
      "transcript_view": "ctrl+y",
      "diff_view": "ctrl+d",
      "session_picker": "alt+s",
      "context_rail": "alt+r",
      "compose_editor": "alt+e",
      "page_up": "pgup",
      "page_down": "pgdown",
      "scroll_top": "home",
      "scroll_bottom": "end"
    },
    "notifications": "on",
    // Optional external editor used by the e key in the diff viewer.
    // Collomia executes this argv directly (no shell). Supported placeholders
    // are {file}, {line}, and {column}; {file} is appended when omitted.
    "editor": {
      "command": "code",
      "args": ["--wait", "--goto", "{file}:{line}:{column}"]
    },
    "debug": false
  },

  // Named profiles for the primary agent and/or delegate tool. Empty fields
  // inherit the ordinary runtime setting. availability defaults to delegate
  // for backward compatibility; use primary or both to expose /agent.
  // Profile permissions only tighten: allow rules are rejected, denials are
  // additive, and the effective mode is the stricter of parent and child.
  "agents": {
    "builder": {
      "availability": "primary",
      "instructions": "Implement focused changes and verify them.",
      "reasoning": {"effort": "high"},
      "max_iterations": 24,
      // Cost budgets require pricing on the selected provider. They use
      // provider-reported tokens and survive /clear and session resume.
      "cost_budget_usd": 2.50
    },
    "reviewer": {
      "availability": "delegate",
      "model": "",
      "instructions": "Review for correctness, security, and missing tests.",
      "tools": ["read_file", "search_files"],
      "skills": ["security-review"],
      "max_iterations": 12,
      "token_budget": 50000,
      "cost_budget_usd": 1.00,
      "timeout_seconds": 600,
      "permissions": {
        "mode": "ask",
        "denied_tools": ["run_command"],
        "denied_commands": ["(?i)^example-destructive-command($|\\s)"],
        "rules": [
          {"action": "deny", "server": "production-*", "reason": "review agents cannot call production MCP servers"}
        ]
      }
    }
  },

  // Language-server commands keyed by language id; omit for auto-detection.
  "lsp": {
    "go": ["gopls"],
    "python": ["pyright-langserver", "--stdio"],
    "typescript": ["typescript-language-server", "--stdio"]
  },

  // Lifecycle hooks: trusted commands run at session events, receiving a
  // JSON payload on stdin. Events: session_start, user_prompt,
  // permission_decision, tool_start, tool_end, file_change, compaction,
  // subagent_start, subagent_end, stop, session_end. The gating events
  // (user_prompt, tool_start) may block by exiting 2 or printing
  // {"decision":"block","reason":"..."}; hooks only ever tighten — they
  // cannot approve what the permission engine denies. "matcher" is a
  // regular expression tested against the tool name (tool events) or event
  // name; "timeout_seconds" defaults to 10. Hook failures are logged
  // warnings, never fatal. Project-configured hooks require collo trust.
  "hooks": {
    "tool_end": [
      {
        "command": "/path/to/notify-script",
        "args": ["--quiet"],
        "matcher": "run_command|apply_patch",
        "timeout_seconds": 10
      }
    ]
  }
}
`
