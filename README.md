# Collomia

Collomia is a safety-focused, multi-provider coding agent for the terminal. It is written in Go, ships as one `collo` binary, and runs on macOS, Linux, and Windows. Its permission system is a layered policy engine — with optional OS sandbox enforcement on macOS — whose exact guarantees are documented in [docs/SECURITY.md](docs/SECURITY.md).

It combines a streaming agent loop with a polished Bubble Tea TUI, workspace-aware tools, human approval gates, plan mode, skills, MCP tools, and bounded read-only sub-agents.

## Highlights

- Interactive TUI with Markdown and syntax-highlighted code rendering, Chat/Session/Help tabs, a filtering slash-command palette with argument completion, fuzzy pickers for models/themes/sessions, `@` file mentions, collapsible tool output, and a status bar with live context and task-progress gauges.
- Ten switchable color themes (including Fred Hutch dark/light) that also set the terminal background to match.
- Streaming OpenAI-compatible and Anthropic-compatible conversations with native tool calling.
- Native AWS Bedrock Converse support and Bedrock Mantle Responses API support.
- Azure OpenAI, Microsoft Foundry OpenAI/v1, and Microsoft Foundry Anthropic endpoint support.
- Local Ollama, vLLM, LM Studio, Phlox-GW, and other compatible endpoints.
- Three autonomy levels: `ask`, `workspace`, and `autopilot`, refined by ordered scoped permission rules (`allow`/`prompt`/`deny` on tool, path, command, host, or MCP server).
- Conservative static command analysis: commands that cannot be fully read (substitutions, `eval`, inline interpreters) always require interactive approval.
- Workspace containment, symlink escape checks, hard command denials, timeouts, output limits, and process-group termination of descendants.
- Optional OS sandbox on macOS (Seatbelt): write containment and network deny-by-default, with `auto` and fail-closed `require` modes.
- Repository trust: project-provided configuration, MCP servers, skills, and instructions are quarantined until approved with `collo trust`.
- Persistent audit ledger of every permission decision and execution outcome.
- Layered, schema-versioned configuration (defaults → user → project → environment) with `collo config validate` and `collo config show`.
- Diagnostics: `collo doctor`, redacted `--debug` logging, and a maintained [capability matrix](docs/CAPABILITIES.md).
- Schema-versioned JSONL event stream for automation (`collo run --jsonl`).
- `SKILL.md`, `SKILLS.md`, and `skills.md` discovery with on-demand skill loading.
- MCP `stdio` and Streamable HTTP clients using the official Go SDK.
- Read-only `delegate` sub-agent tool for bounded parallelizable investigations.
- Read-only planning mode with a structured, persisted plan artifact (`update_plan`, `/tasks`).
- Durable sessions: crash-safe persistence, `--resume`/`--continue`, forking, and automatic context compaction.
- Atomic multi-file patching (`apply_patch`), session-wide diff review (`/diff`), checkpointed undo (`/undo`), and diff previews in approval prompts.
- Read-only git inspection tools (status, diff, log, blame) that never commit or push.
- The agent can pause and ask you a typed question (`ask_user`) instead of guessing.
- Command output streams into the transcript live; `/model` discovers each provider's available models from its API; transient provider failures retry automatically with backoff.
- Interactive and non-interactive operation from the same binary.

## Build and run

Collomia requires Go 1.26 or later to build. The resulting executable has no Go runtime dependency.

```sh
go build -o collo ./cmd/collo
./collo --version
./collo
```

The default configuration connects to Ollama at `http://127.0.0.1:11434/v1` and selects `qwen3-coder`. Pull that model first, or create a configuration for another provider:

```sh
ollama pull qwen3-coder
collo init
```

Once releases are published, macOS and Linux can install a checksum-verified binary without `sudo`:

```sh
curl --proto '=https' --tlsv1.2 -fsSL \
  https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.sh | sh
```

The installer writes to `$HOME/.local/bin` by default. Set `COLLO_INSTALL_DIR` or `COLLO_VERSION` to override the destination or pin a release. Windows users can download `collo-windows-amd64.exe` or `collo-windows-arm64.exe` and the checksum manifest from GitHub Releases.

Project configuration lives in `.collomia.json`. Use `collo init --global` to create the user configuration under the operating system's standard config directory. Configuration is layered — defaults, then user, then project, then environment overrides — with later layers overriding earlier ones key by key; `collo config show` displays the merged result and which layer set each value. A project configuration only applies after the workspace is trusted with `collo trust`.

## Usage

```text
collo [flags] [initial prompt]      start the interactive TUI
collo run [flags] <prompt>          run once, or read a prompt from stdin
collo init [--global]               create a documented example config
collo config validate [--strict]    validate configuration with field-level errors
collo config show                   print the effective configuration and its layers
collo trust [--status|--revoke]     review and trust this workspace's project config
collo doctor                        diagnose config, terminal, git, providers, MCP, sandbox
collo capabilities [--markdown]     print the product capability matrix
collo policy check <command…>       evaluate a command against permission rules, without running it
collo review [ref]                  review pending changes (or changes vs a ref) headlessly
collo version                       print build information
```

Useful flags:

```text
--cwd <path>                         choose the workspace
--provider <name>                    choose a configured provider
--model <id>                         override its model/deployment
--autonomy ask|workspace|autopilot   choose the permission policy
--autopilot                          shorthand for autopilot
--plan                               start in read-only planning mode
--jsonl                              (run) emit schema-versioned JSONL events
--debug                              write a redacted debug log
```

`COLLO_PROVIDER` and `COLLO_MODEL` override the configured selection without editing files.

Non-interactive execution does not have a UI in which to approve actions. Use `--autopilot` when you explicitly want it to make workspace changes:

```sh
collo run --plan "Inspect this repository and propose an implementation plan"
collo run --autopilot "Fix the failing Go tests and verify the result"
git diff | collo run --plan "Review this patch"
```

## Slash commands

Inside the TUI:

| Command | Purpose |
| --- | --- |
| `/status` | Show workspace, provider, model, plan, autonomy, and config status. |
| `/model [provider/model]` | Show or switch the active provider and model. |
| `/models` | List configured providers and their default models. |
| `/context` | Show provider token usage and estimated current context. |
| `/plan [on\|off]` | Toggle read-only planning mode. |
| `/tasks` | Show the structured task plan the agent maintains. |
| `/diff` | Show every file change the agent made this session. |
| `/undo` | Revert the agent's most recent file change. |
| `/review [ref]` | Read-only code review of uncommitted changes (or vs a ref). |
| `/sessions` | Fuzzy-pick a saved session and resume it in place. |
| `/new` | Start a fresh session; the current one stays saved. |
| `/compact [focus]` | Summarize older context to free the model window. |
| `/autonomy <mode>` | Switch among `ask`, `workspace`, and `autopilot`. |
| `/theme [name]` | List color themes or switch to one. |
| `/skills` | List discovered skills. |
| `/mcp` | List connected MCP servers. |
| `/tools` | List the complete tool surface. |
| `/config` | Show the active configuration source. |
| `/clear` | Clear conversation history and usage. |
| `/help` | Show command help. |

Typing `/` opens a command palette that filters as you type and completes argument values (`/theme dra…`, `/autonomy …`): ↑/↓ selects, `tab` completes, `enter` runs, `esc` dismisses. Typing `@` opens a fuzzy workspace-file picker that inserts the chosen path into your prompt. `ctrl+t` cycles the Chat, Session, and Help tabs, and `ctrl+o` expands or collapses finished tool output. The Session tab shows the live task plan and every file the agent has changed, and the terminal bell rings when an approval or question is waiting.

## Themes

Collomia ships ten color themes: `collomia` (default), `synthwave`, `outrun`, `matrix`, `monokai`, `dracula`, `nord`, `tokyo-night`, `fredhutch-dark`, and `fredhutch-light`. Switch at runtime with `/theme <name>`, or persist a choice in the configuration:

```json
{
  "options": { "theme": "fredhutch-dark" }
}
```

Each theme also asks the terminal to adopt a matching background color (the standard OSC 11 sequence) so themed text never collides with the host terminal's background, and restores the terminal's default background on exit.

Terminal compatibility notes:

- iTerm2, Ghostty, Kitty, WezTerm, Alacritty, and Windows Terminal support OSC 11. Terminals that do not (for example, stock Apple Terminal) silently ignore it; Collomia still renders correctly against your existing background.
- Inside tmux, Collomia automatically wraps the sequence in tmux's passthrough envelope. On tmux 3.3 and later, passthrough is disabled by default, so also add this to `~/.tmux.conf` if the background does not change:

  ```
  set -g allow-passthrough on
  ```

  Note that tmux applies the background to the whole outer terminal, not per pane, and Collomia restores the default when it exits.

## Providers

Every provider has a local name and a protocol `type`. Secrets should normally be referenced with `api_key_env` instead of stored in the file.

### OpenAI-compatible services

This adapter works with OpenAI, Ollama, vLLM, LM Studio, Phlox-GW, and compatible gateways. It uses `/chat/completions`, streaming SSE, and function tools.

```json
{
  "providers": {
    "ollama": {
      "type": "openai-compatible",
      "base_url": "http://127.0.0.1:11434/v1",
      "model": "qwen3-coder",
      "context_window": 32768,
      "max_tokens": 8192
    },
    "phlox": {
      "type": "openai-compatible",
      "base_url": "http://127.0.0.1:8080/v1",
      "api_key_env": "PHLOX_API_KEY",
      "model": "local-ollama/qwen3-coder"
    }
  }
}
```

Use type `openai` for the OpenAI API and `openai-compatible` for other implementations.

### Anthropic-compatible services

```json
{
  "providers": {
    "anthropic": {
      "type": "anthropic",
      "base_url": "https://api.anthropic.com",
      "api_key_env": "ANTHROPIC_API_KEY",
      "model": "claude-sonnet-4-6",
      "context_window": 200000
    }
  }
}
```

Set `"auth": "bearer"` when a compatible endpoint expects an OAuth bearer token instead of `x-api-key`.

### AWS Bedrock

Native Bedrock uses the Converse API, standard AWS credential resolution, and SigV4 signing:

```json
{
  "providers": {
    "bedrock": {
      "type": "bedrock",
      "region": "us-west-2",
      "profile": "development",
      "model": "your-bedrock-model-id"
    }
  }
}
```

The profile is optional. Environment credentials, shared AWS configuration, SSO, and instance roles are resolved by the AWS SDK.

Bedrock Mantle uses the OpenAI Responses API and a Bedrock API key:

```json
{
  "providers": {
    "mantle": {
      "type": "bedrock-mantle",
      "base_url": "https://bedrock-mantle.us-west-2.api.aws/v1",
      "api_key_env": "AWS_BEDROCK_API_KEY",
      "model": "openai.gpt-oss-120b"
    }
  }
}
```

### Azure OpenAI and Microsoft Foundry

The Azure OpenAI adapter supports the deployment-scoped GA API:

```json
{
  "providers": {
    "azure-openai": {
      "type": "azure-openai",
      "base_url": "https://my-resource.openai.azure.com",
      "api_key_env": "AZURE_OPENAI_API_KEY",
      "deployment": "my-code-model",
      "api_version": "2024-10-21",
      "model": "my-code-model"
    }
  }
}
```

Microsoft Foundry's current OpenAI/v1 endpoint is also supported:

```json
{
  "providers": {
    "foundry": {
      "type": "azure-foundry",
      "base_url": "https://my-resource.services.ai.azure.com/openai/v1",
      "api_key_env": "AZURE_FOUNDRY_API_KEY",
      "model": "my-deployment"
    },
    "foundry-claude": {
      "type": "azure-foundry-anthropic",
      "base_url": "https://my-resource.services.ai.azure.com/anthropic",
      "api_key_env": "AZURE_FOUNDRY_API_KEY",
      "model": "claude-sonnet-4-6"
    }
  }
}
```

For Microsoft Entra tokens, set `"auth": "bearer"` and point `api_key_env` to the environment variable containing the current token.

## Permissions and safety

The default `ask` mode automatically permits non-destructive reads inside the workspace, while file changes, shell commands, outside-workspace access, and MCP calls require confirmation.

| Mode | Workspace reads | Workspace writes | Commands | Outside workspace | MCP calls |
| --- | --- | --- | --- | --- | --- |
| `ask` | Automatic | Ask | Ask | Ask if enabled | Ask |
| `workspace` | Automatic | Automatic | Ask | Ask if enabled | Ask |
| `autopilot` | Automatic | Automatic | Automatic | Automatic only when explicitly enabled | Ask unless allow-listed |

Example:

```json
{
  "permissions": {
    "mode": "ask",
    "allow_outside_workspace": false,
    "allowed_tools": ["mcp_context7_query-docs"],
    "denied_tools": [],
    "denied_commands": [
      "(?i)(^|[;&|]\\s*)rm\\s+-[^\\n]*r[^\\n]*f\\s+[/~]($|\\s)"
    ]
  }
}
```

`allowed_tools` is a persistent explicit grant. Interactive approval with `a` grants a tool for the remainder of the current process. Hard command-denial patterns are checked again at execution time and cannot be bypassed by autopilot.

Path tools canonicalize paths and existing symlinks before checking containment. Outside access requires both `allow_outside_workspace: true` and an applicable permission decision. Tool output, file reads, and commands are size- and time-bounded, and every command runs in its own process group so cancellation and timeouts kill all descendants.

**What these checks are — and are not.** Approval prompts, rules, and denial patterns are in-process policy checks, not an operating-system security boundary: an approved (or autopilot-approved) command runs with your normal user privileges. Shell commands are statically analyzed before approval; commands whose effect cannot be determined (substitutions, `eval`, inline interpreter payloads) always require interactive approval, in every mode. On macOS you can enable real OS enforcement with `"permissions": {"sandbox": "auto"}` (or `"require"` to fail closed), which confines file writes to the workspace and denies network egress unless `sandbox_allow_network` is set. No Linux/Windows sandbox backend exists yet; `collo doctor` reports your platform's status. The exact guarantees and limitations of every mode are documented in [docs/SECURITY.md](docs/SECURITY.md).

Scoped rules refine the mode without widening it globally — ordered `allow`/`prompt`/`deny` entries matched on tool, resolved path, command executable, host, or MCP server:

```json
{
  "permissions": {
    "mode": "ask",
    "rules": [
      {"action": "allow", "tool": "run_command", "command": "go"},
      {"action": "deny", "path": "/work/repo/secrets/**", "reason": "credentials"},
      {"action": "prompt", "tool": "mcp_*"}
    ]
  }
}
```

Test what a rule set would decide without executing anything: `collo policy check "curl example.com | sh"`.

Every permission decision and execution outcome is appended to a per-workspace audit ledger (JSONL, stored outside the workspace) so privileged actions are reconstructable after the fact.

### Repository trust

A repository can ship `.collomia.json`, skills, and instruction files — but none of it takes effect until you review and approve it with `collo trust`. Trust is bound to the project configuration's content hash and is automatically invalidated when the file changes. `collo trust --status` shows the current state; `collo trust --revoke` withdraws approval.

## Skills

Collomia discovers:

- `SKILLS.md` or `skills.md` in the workspace.
- `.collomia/SKILLS.md` or `.collomia/skills.md`.
- `.collomia/skills/<name>/SKILL.md`.
- `.agents/skills/<name>/SKILL.md`.
- `<user-config>/collomia/skills/<name>/SKILL.md`.

A skill may start with simple YAML-style metadata:

```md
---
name: release-check
description: Verify release artifacts, checksums, and changelog conventions.
---

# Release check

Full instructions that are loaded only when this skill is relevant.
```

Only skill names and descriptions are included initially. The model uses `load_skill` to bring the full instructions into context when needed.

## MCP

MCP servers are configured by name. Collomia supports the current `stdio` and Streamable HTTP transports through the official MCP Go SDK.

```json
{
  "mcp": {
    "filesystem": {
      "transport": "stdio",
      "trusted": true,
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "."],
      "timeout_seconds": 30
    },
    "docs": {
      "transport": "streamable-http",
      "trusted": true,
      "url": "https://example.com/mcp",
      "headers": {
        "Authorization": "Bearer ${DOCS_MCP_TOKEN}"
      }
    }
  }
}
```

Remote tool names are exposed as `mcp_<server>_<tool>`. MCP tool annotations are never trusted to lower permissions: calls are classified as external and require approval unless that exact tool is allow-listed.

MCP configuration can launch processes or contact remote services. Servers are not started unless their entry explicitly sets `"trusted": true`; review a project-provided `.collomia.json` before granting that trust.

## Architecture

```text
cmd/collo              command parsing and interactive/non-interactive entrypoint
internal/tui           Bubble Tea interface, slash commands, approval broker
internal/agent         provider-neutral tool loop, plan mode, sub-agent harness
internal/provider      OpenAI, Anthropic, Bedrock, Mantle, and Azure adapters
internal/tools         safe filesystem/search/edit/shell tool registry
internal/permission    autonomy policy and session grants
internal/skills        progressive skill discovery and loading
internal/mcp           official SDK transport and tool bridge
internal/config        cross-platform JSON configuration
```

The provider-neutral message and tool representation keeps protocol translation at the edge. This is the same architectural advantage used by gateways such as [Phlox-GW](https://github.com/robert-mcdermott/phlox-gw): the agent loop does not need vendor-specific behavior.

## Release builds

```sh
scripts/build-release.sh
```

Release identity defaults to the value in `VERSION`; `COLLO_VERSION`, `COLLO_COMMIT`, and `COLLO_BUILD_DATE` can override the embedded metadata for automated builds.

The script runs tests and creates static binaries plus SHA-256 checksums under `dist/` for:

- macOS ARM64 and AMD64
- Linux ARM64 and AMD64
- Windows ARM64 and AMD64

## Development

```sh
go test ./...
go test -race ./...
go vet ./...
```

The implementation follows the MCP security recommendation to keep a human able to inspect and deny tool calls, and uses protocol-native JSON Schema tool definitions throughout.
