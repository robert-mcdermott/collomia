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
// safe permission and runtime defaults alongside provider examples.
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
		MaxIterations      int               `json:"max_iterations"`
		MaxToolOutputBytes int               `json:"max_tool_output_bytes"`
		AlternateScreen    bool              `json:"alternate_screen"`
		Keybindings        map[string]string `json:"keybindings"`
	}
	type starterConfig struct {
		SchemaVersion   int                 `json:"schema_version"`
		DefaultProvider string              `json:"default_provider,omitempty"`
		DefaultModel    string              `json:"default_model,omitempty"`
		Providers       map[string]Provider `json:"providers,omitempty"`
		Permissions     *starterPermissions `json:"permissions,omitempty"`
		Options         *starterOptions     `json:"options,omitempty"`
	}

	cfg := starterConfig{SchemaVersion: CurrentSchemaVersion}
	if global {
		cfg.DefaultProvider = "ollama"
		cfg.DefaultModel = "qwen3-coder"
		cfg.Providers = map[string]Provider{
			"ollama": {
				Type: "openai-compatible", BaseURL: "http://127.0.0.1:11434/v1",
				Model: "qwen3-coder", Context: 32768, MaxTokens: 8192,
				ConnectTimeoutSeconds: 10, RequestTimeoutSeconds: 1800, StreamIdleTimeoutSeconds: 300,
			},
			"openrouter": {
				Type: "openai", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "OR_API_KEY",
				Model: "z-ai/glm-5.2", MaxTokens: 128000, Context: 500000,
				ConnectTimeoutSeconds: 10, RequestTimeoutSeconds: 1800, StreamIdleTimeoutSeconds: 300,
			},
		}
		inactive := false
		commandNetwork := true
		broadReads := true
		cfg.Permissions = &starterPermissions{
			Mode:                             "ask",
			AllowOutsideWorkspace:            &inactive,
			Sandbox:                          "off",
			SandboxAllowReadOutsideWorkspace: &broadReads,
			// Network stays available if the user later enables the sandbox by
			// changing only sandbox=auto. Users who prefer fail-closed command
			// networking can set this to false explicitly.
			SandboxAllowNetwork: &commandNetwork,
		}
		cfg.Options = &starterOptions{
			MaxIterations:      24,
			MaxToolOutputBytes: 64 * 1024,
			AlternateScreen:    true,
			Keybindings:        DefaultKeybindings(),
		}
	} else {
		cfg.Permissions = &starterPermissions{Mode: "ask"}
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return writeGeneratedFile(path, append(data, '\n'))
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
  // Current configuration schema. Files without this field are treated as v1.
  "schema_version": 1,

  // Provider selected when no CLI or environment override is present.
  "default_provider": "ollama",
  "default_model": "qwen3-coder",

  // Keep only providers you use. Secrets should normally use api_key_env.
  "providers": {
    "ollama": {
      "type": "openai-compatible",
      "base_url": "http://127.0.0.1:11434/v1",
      "model": "qwen3-coder",
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
      "temperature": 0.2
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
    "rules": [
      {
        "action": "allow",
        "tool": "run_command",
        "command": "go",
        "reason": "project build tooling"
      },
      {
        "action": "deny",
        "path": "/work/repo/secrets/**",
        "reason": "credentials"
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
    // off | auto | require. auto uses the platform backend when available.
    "sandbox": "off",
    // The compatibility default is true. This reference shows the explicit
    // offline override: false denies sandboxed command network access. It does
    // not affect providers or remote MCP, which run in the Collomia process.
    "sandbox_allow_network": false,
    // The compatibility default is true (broad command reads). Set false to
    // deny ordinary user-data reads outside the workspace while retaining the
    // runtime roots needed to launch normal system tools. Add only required
    // dependency/cache roots below.
    "sandbox_allow_read_outside_workspace": false,
    "sandbox_readable_roots": [],
    // Optional extra write locations for build/package caches. Relative paths
    // resolve from the workspace. Keep this list narrow.
    "sandbox_writable_roots": [],
    // full | minimal. minimal keeps secrets out of child command environments.
    "command_env": "full",
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
    "disabled_tools": [],
    "transcript_directory": "/path/to/transcripts",
    "theme": "collomia",
    // true uses a clean full-screen buffer. false keeps the final TUI frame
    // in the terminal's native scrollback; --no-alt-screen overrides it.
    "alternate_screen": true,
    // Global TUI actions are remappable. Approval/question decision keys stay
    // fixed and are always shown in their dialog.
    "keybindings": {
      "next_tab": "ctrl+t",
      "toggle_tool_output": "ctrl+o",
      "transcript_view": "ctrl+y",
      "diff_view": "ctrl+d",
      "session_picker": "alt+s",
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

  // Named profiles for the delegate tool. Empty fields inherit the parent.
  "agents": {
    "reviewer": {
      "model": "",
      "instructions": "Review for correctness, security, and missing tests.",
      "tools": ["read_file", "search_files"],
      "max_iterations": 12
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
