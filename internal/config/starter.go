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
		Mode                  string `json:"mode"`
		AllowOutsideWorkspace *bool  `json:"allow_outside_workspace,omitempty"`
		Sandbox               string `json:"sandbox,omitempty"`
		SandboxAllowNetwork   *bool  `json:"sandbox_allow_network,omitempty"`
	}
	type starterOptions struct {
		MaxIterations      int `json:"max_iterations"`
		MaxToolOutputBytes int `json:"max_tool_output_bytes"`
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
			},
			"openrouter": {
				Type: "openai", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "OR_API_KEY",
				Model: "z-ai/glm-5.2", MaxTokens: 128000, Context: 500000,
			},
		}
		inactive := false
		cfg.Permissions = &starterPermissions{
			Mode:                  "ask",
			AllowOutsideWorkspace: &inactive,
			Sandbox:               "off",
			SandboxAllowNetwork:   &inactive,
		}
		cfg.Options = &starterOptions{
			MaxIterations:      24,
			MaxToolOutputBytes: 64 * 1024,
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
      "context_window": 500000
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
      "api_key_env": "AZURE_OPENAI_API_KEY",
      "deployment": "your-deployment",
      "api_version": "2024-10-21",
      "model": "your-deployment",
      "max_tokens": 8192
    },
    "azure-foundry": {
      "type": "azure-foundry",
      "base_url": "https://your-resource.services.ai.azure.com/openai/v1",
      "api_key_env": "AZURE_FOUNDRY_API_KEY",
      "model": "your-deployment",
      "max_tokens": 8192
    },
    "azure-foundry-claude": {
      "type": "azure-foundry-anthropic",
      "base_url": "https://your-resource.services.ai.azure.com/anthropic",
      "api_key_env": "AZURE_FOUNDRY_API_KEY",
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
    // Omit denied_commands to inherit built-in safety patterns.
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
    // false denies TCP network access where the sandbox backend supports it.
    "sandbox_allow_network": false,
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
    "max_tool_output_bytes": 65536,
    "disabled_tools": [],
    "transcript_directory": "/path/to/transcripts",
    "theme": "collomia",
    "notifications": "on",
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
