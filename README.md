# Collomia

Collomia is a safety-focused, multi-provider coding agent for the terminal. It is written in Go, ships as one `collo` binary, and runs on macOS, Linux, and Windows. Its permission system is a layered policy engine — with built-in OS sandbox backends on all three platforms — whose exact guarantees are documented in [docs/SECURITY.md](docs/SECURITY.md).

New users and advanced operators should start with the [complete Collomia user guide](docs/USER_GUIDE.md), which covers installation on every platform, configuration layering, every provider and authentication mode, permissions and sandboxes, LSP, MCP, hooks, skills, sub-agents, sessions, automation, and troubleshooting.

It combines a streaming agent loop with a polished Bubble Tea TUI, workspace-aware tools, human approval gates (down to individual diff hunks), a parallel multi-agent scheduler with git-worktree isolation, skills, MCP tools, background process management, code intelligence (a symbol index and real language-server diagnostics), and a verification loop that runs your project's own build/lint/test commands.

An up-to-date, generated list of exactly what is implemented, experimental, or unsupported lives in [docs/CAPABILITIES.md](docs/CAPABILITIES.md) (`collo capabilities`). The [roadmap](ROADMAP.md) tracks what's still ahead.

## Highlights

- Interactive TUI with Markdown and syntax-highlighted code rendering, Chat/Session/Help tabs, a filtering slash-command palette with argument completion, fuzzy pickers for models/themes/sessions, `@` file mentions, collapsible tool output, and a status bar with live context, task-progress, active-agent, and background-process gauges.
- Nineteen switchable themes (including Fred Hutch dark/light and the colorless `plain` mode) that also set the terminal background to match when color is enabled.
- Streaming OpenAI-compatible, Anthropic-compatible, Responses-style, and native Bedrock ConverseStream conversations with tool calling, capability-aware live model discovery (`/model` and `/models`), request preflight, and automatic retry with backoff on transient failures.
- Native AWS Bedrock Converse support and Bedrock Mantle Responses API support.
- Azure OpenAI, Microsoft Foundry OpenAI/v1, and Microsoft Foundry Anthropic endpoint support.
- Local Ollama, vLLM, LM Studio, Phlox-GW, and other OpenAI-compatible endpoints.
- Three autonomy levels: `ask`, `workspace`, and `autopilot`, refined by ordered scoped permission rules (`allow`/`prompt`/`deny` on tool, path, command, host, or MCP server).
- Conservative static command analysis: commands that cannot be fully read (substitutions, `eval`, inline interpreters) always require interactive approval, in every mode.
- Workspace containment, symlink escape checks, hard command denials, timeouts, output limits, and process-group termination of every command's descendants.
- OS sandbox enforcement: Seatbelt write/network containment on macOS, Landlock filesystem (and, on newer kernels, TCP) containment on Linux, both with `auto` and fail-closed `require` modes.
- Repository trust: when a project `.collomia.json` exists, that configuration and the project's MCP servers, skills, and instructions are quarantined until approved with `collo trust`.
- Persistent audit ledger of every permission decision and execution outcome, stored outside the workspace.
- Layered, schema-versioned configuration (defaults → user → project → environment) with `collo config validate` and `collo config show`.
- Diagnostics: `collo doctor`, redacted `--debug` logging, and a maintained [capability matrix](docs/CAPABILITIES.md).
- Schema-versioned JSONL event stream for automation (`collo run --jsonl`), with `--resume`/`--continue` for headless runs.
- Full-lifecycle skills: `SKILL.md` manifests with YAML front matter plus bundled `scripts/`, `references/`, and `assets/`, project and global scopes with deterministic precedence, on-demand loading, and `collo skills` management — plus hierarchical `AGENTS.md`/`COLLOMIA.md` instructions (user-level, then project).
- MCP `stdio` and Streamable HTTP clients using the official Go SDK.
- **Multi-agent delegation**: the `delegate` tool runs up to six sub-agent tasks concurrently (bounded to four at once). Read-only tasks share the workspace; write-capable tasks get their own isolated git worktree so parallel agents never race on the same files, with sibling-conflict detection across a batch. Optional named agent profiles (model, role instructions, tool allowlist) live in configuration.
- **Background processes**: `start_process`/`list_processes`/`process_output`/`stop_process` run dev servers, watchers, and long test runs without blocking the turn, with the same safety analysis as `run_command`; `/ps` manages them from the TUI, and everything is stopped at session exit.
- **Code intelligence**: `search_symbols` queries an incremental, ignore-aware definition index (Go, Python, JS/TS, Rust); `diagnostics` runs a real language server (gopls, pyright, typescript-language-server, rust-analyzer) and returns exact-position findings.
- **Verification loop**: `detect_verification` finds the project's real build/lint/test commands from its own files (`go.mod`, `package.json`, `Cargo.toml`, …); `collo verify`/`/verify` runs them and ties outcomes to the plan.
- Read-only planning mode with a structured, persisted plan artifact (`update_plan`, `/tasks`).
- Durable sessions: crash-safe persistence, `--resume`/`--continue`, forking, live in-TUI switching (`/sessions`), and automatic context compaction.
- Atomic multi-file patching (`apply_patch`), session-wide diff review (`/diff`), checkpointed undo (`/undo`), colorized diff previews at approval, and **hunk-level approval** — accept or reject individual hunks of a `write_file` change before it lands.
- Read-only git inspection tools (status, diff, log, blame) that never commit or push.
- `run_command` supports a pseudo-terminal (`pty: true`, Unix) for interactive-only or isatty-dependent programs.
- The agent can pause and ask you a typed question (`ask_user`) instead of guessing.
- Command output streams into the transcript live, for both foreground and background commands.
- Interactive and non-interactive operation from the same binary.
- Local browser terminal (`collo --web`) that runs the existing TUI in a real PTY and serves an embedded, authenticated xterm.js client on loopback (macOS/Linux).

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
collo init --global --with-reference
```

Once releases are published, macOS and Linux can install a checksum-verified binary without `sudo`:

```sh
curl --proto '=https' --tlsv1.2 -fsSL \
  https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.sh | sh
```

The installer writes to `$HOME/.local/bin` by default. Set `COLLO_INSTALL_DIR` or `COLLO_VERSION` to override the destination or pin a release. Windows users can download `collo-windows-amd64.exe` or `collo-windows-arm64.exe` and the checksum manifest from GitHub Releases.

## Configuration

Collomia supports both user-wide defaults and per-project overrides. You do not need both: use only the global configuration for the same behavior everywhere, only a project configuration for a self-contained workspace, or layer them when a project needs to differ from your normal setup.

### Global and project configuration

`collo init --global` creates the global user configuration in the user's home directory:

| Platform | Global configuration path |
| --- | --- |
| macOS and Linux | `~/.collomia/config.json` |
| Windows | `%USERPROFILE%\.collomia\config.json` |

The global file applies to every workspace for that user. The same `.collomia` directory is the single root for every persistent user-level Collomia file: the generated `config.example.jsonc` reference, optional `AGENTS.md`/`COLLOMIA.md` instructions, skills, sessions, logs, audit ledgers, repository trust decisions, and MCP server pins. Collomia does not search additional platform configuration or cache directories.

It is a good place for personal provider definitions, preferred models, permissions, and user-wide options. Collomia uses compatibility-friendly defaults: sandboxing is `off`, and command networking is `true`, so changing only `permissions.sandbox` to `auto` adds containment without blocking package downloads or online command-line tools at the network boundary. Set `sandbox_allow_network` to `false` when you intentionally want sandboxed commands offline. Package managers may also need a writable cache or environment-provided registry credentials, as described under [Permissions and safety](#permissions-and-safety). Store API keys in environment variables and refer to them with `api_key_env`; avoid putting secret values directly in either configuration file.

Running `collo init` without `--global` creates `.collomia.json` in the current workspace (or the directory selected by `--cwd`). This file applies only to that project. It is a good place for project-specific permission rules, sandbox policy, agents, MCP servers, language servers, and other settings that should travel with the repository.

Project configuration is quarantined until you review it and run `collo trust`. Trust is tied to the file contents and is invalidated whenever `.collomia.json` changes. You can safely inspect an untrusted file with `collo config validate --strict` before approving it.

### Precedence and inheritance

Collomia builds the effective configuration in this order:

1. Built-in defaults.
2. Global user configuration.
3. Trusted project `.collomia.json`.
4. `COLLO_PROVIDER` and `COLLO_MODEL` environment overrides.

Each later layer overrides settings supplied by an earlier layer. Settings omitted from a later file continue to inherit their earlier values, so project configuration should contain only intentional overrides. The generated global starter deliberately includes common safe defaults to make them easy to discover and edit. Object fields such as `permissions.mode` can be overridden independently. Lists are replaced when specified, except `permissions.denied_commands`: regex denials are additive, so built-in patterns are mandatory, global patterns extend them, and project patterns extend both. Collomia's structural catastrophic-command checks are built into the executable and cannot be disabled by configuration. A same-named entry in a named map such as `providers`, `mcp`, or `agents` should be treated as a complete replacement definition.

For example, a global file can define a personal OpenRouter setup:

```json
{
  "schema_version": 1,
  "default_provider": "openrouter",
  "providers": {
    "openrouter": {
      "type": "openai",
      "base_url": "https://openrouter.ai/api/v1",
      "api_key_env": "OR_API_KEY",
      "model": "z-ai/glm-5.2",
      "max_tokens": 128000,
      "context_window": 500000
    }
  },
  "permissions": {
    "mode": "ask",
    "allow_outside_workspace": false,
    "sandbox": "off",
    "sandbox_allow_network": true
  },
  "options": {
    "max_iterations": 24,
    "max_tool_output_bytes": 65536
  }
}
```

A project can then override only its permission mode:

```json
{
  "schema_version": 1,
  "permissions": {
    "mode": "workspace"
  }
}
```

In that project, the effective configuration uses the global OpenRouter provider and the project's `workspace` permission mode; every other setting comes from the global file or built-in defaults. Run `collo config show` inside a workspace to print the merged, secret-redacted configuration, the applied and quarantined layers, their file paths, and the settings contributed by each layer.

Active configuration files are strict JSON. `collo config reference` prints an exhaustive commented JSONC reference without changing any files. Add `--with-reference` to either form of `collo init` to save that documentation beside the active file as `.collomia.example.jsonc` or `config.example.jsonc`. These reference files are documentation only and are never loaded.

## Usage

```text
collo [flags] [initial prompt]      start the interactive TUI
collo --web [flags] [initial prompt]  open the interactive TUI in a local browser (macOS/Linux)
collo run [flags] <prompt>          run once, or read a prompt from stdin
collo init [--with-reference]       create project .collomia.json
collo init --global [--with-reference]  create ~/.collomia/config.json
collo config validate [--strict]    validate configuration with field-level errors
collo config show                   print the effective configuration and its layers
collo config reference              print every configuration option with annotations
collo trust [--status|--revoke]     review and trust this workspace's project config
collo doctor [--strict]             diagnose config, terminal, git, providers, MCP, sandbox
collo capabilities [--markdown]     print the product capability matrix
collo policy check <command…>       evaluate a command against permission rules, without running it
collo review [ref] [instructions…]  review pending changes ('-' = uncommitted) with optional focus, headlessly
collo verify [focus]                detect and run this project's build/lint/test commands, headlessly
collo sessions [list|show|fork|rename|archive|unarchive|delete]  manage saved sessions
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
--resume <id>                        resume a saved session
--continue                           resume the most recently updated session
--web                                serve the TUI in an authenticated local browser terminal
--web-port <port>                    choose its loopback port (default: random available port)
--no-open                            print the browser-terminal URL without opening a browser
--jsonl                              (run) emit schema-versioned JSONL events on stdout
--debug                              write a redacted debug log
--global                             (init) create the user-wide config instead of a project config
--with-reference                     (init) also write the non-loaded annotated JSONC reference
```

`COLLO_PROVIDER` and `COLLO_MODEL` override the configured selection without editing files.

Non-interactive execution does not have a UI in which to approve actions. Use `--autopilot` when you explicitly want it to make workspace changes:

```sh
collo run --plan "Inspect this repository and propose an implementation plan"
collo run --autopilot "Fix the failing Go tests and verify the result"
git diff | collo run --plan "Review this patch"
collo run --resume <session-id> "Continue where we left off"
collo run --jsonl --autopilot "Run the test suite and report failures" | jq .
```

With `--jsonl`, every event is one schema-versioned JSON line (secrets redacted), and the final line is always a `run.result` record — the machine-readable verdict, so automation never has to reassemble text deltas or scan for mid-stream errors. Provider streams use `text.delta`, `reasoning.delta`, `tool.call.delta` (id/name plus incremental `arguments_delta`, completed by `done`), `usage`, and `warning`; a tool is never executed from those fragments, only from the provider's complete validated call. Provider failures add a classified `provider` object to the preceding `error` event (for example, `{"kind":"rate_limit","status_code":429,"retryable":true,"retry_after_ms":3000,"request_id":"…"}`):

```json
{"schema":1,"kind":"run.result","result":{"status":"ok","answer":"…","session_id":"a1b2c3","changed_files":["main.go"],"duration_ms":8412},"usage":{"input_tokens":5210,"output_tokens":644}}
```

`status` is `ok`, `error`, or `cancelled` (interrupt); on non-`ok` the record carries the error and the process exits non-zero. Pull just the verdict with `... --jsonl | tail -1 | jq .result`.

## Browser terminal

On macOS and Linux, `--web` exposes the normal Collomia TUI through a local
browser without creating a second frontend or agent protocol:

```sh
# Bind 127.0.0.1 on a random available port and open the default browser.
collo --web

# Keep normal TUI options and an initial prompt.
collo --web --provider openrouter --autonomy workspace "Review this project"

# Choose a loopback port, or open the printed URL yourself.
collo --web --web-port 8765 --no-open
```

Collomia prints the access URL to stderr even when it opens the browser
successfully. The server launches the same binary as `collo tui` in a real
pseudo-terminal, preserving the selected working directory and environment.
ANSI rendering, raw keyboard input, interactive approvals, Ctrl+C, and browser
resize events therefore follow the regular TUI path. The xterm.js frontend and
fit addon are vendored and embedded in the executable; the page does not load
scripts from a CDN.

Web mode is intentionally local-only. It always binds to `127.0.0.1`, uses a
random port unless `--web-port` is supplied, generates a new 256-bit token, and
requires the exact local browser origin. The token is placed in the URL fragment
so it is not sent in HTTP requests, then sent as the first WebSocket message.
Only one authenticated browser can control the session. Closing that connection
ends the TUI and its child process group; refresh/reconnection is not supported
in this first version.

Treat the printed URL as a password: anyone who obtains it can control the TUI
and answer its approval prompts. Do not share, proxy, tunnel, or port-forward
the server. It has no TLS or remote-user authentication. Native Windows web
mode is not available until Collomia has a ConPTY backend; the command exits
with a clear error rather than running the TUI without terminal semantics. See
[the browser-terminal security boundary](docs/SECURITY.md#browser-terminal-boundary)
for the complete limitations.

## Slash commands

Inside the TUI:

| Command | Purpose |
| --- | --- |
| `/status` | Show workspace, provider, model, effective capabilities, plan, autonomy, and config status. |
| `/model [provider/model]` | Show or switch the active provider and model (opens a fuzzy picker with no argument). |
| `/models` | Show each provider's default model, effective capabilities, endpoint constraints, and live catalog availability when the adapter supports discovery. |
| `/context` | Break down exactly what the model sees: system prompt, instructions, skills, tool results, conversation, compaction summaries, and the usage gauge. |
| `/plan [on\|off]` | Toggle read-only planning mode. |
| `/tasks` | Show the structured task plan the agent maintains. |
| `/diff` | Show every file change the agent made this session, with syntax highlighting. |
| `/undo` | Revert the agent's most recent file change (refuses to clobber files you edited outside the agent). |
| `/review [ref] [instructions…]` | Read-only code review of uncommitted changes (`-` or no ref) or changes vs any ref; extra words become custom reviewer instructions. |
| `/verify [focus]` | Detect and run the project's real build/lint/test commands, tying each outcome to a plan step. |
| `/ps` | List background processes started this session; `/ps stop <id>` stops one. |
| `/sessions` | Fuzzy-pick a saved session and resume it in place — transcript, plan, and persistence all move over. |
| `/new` | Start a fresh session; the current one stays saved. |
| `/compact [focus]` | Summarize older context to free the model window. |
| `/autonomy <mode>` | Switch among `ask`, `workspace`, and `autopilot`. |
| `/theme [name]` | List color themes or switch to one (fuzzy picker with no argument). |
| `/skills [list]` | Fuzzy-pick a skill to use — choosing one pre-fills the prompt — or `list` to print them. |
| `/mcp [subcommand]` | Browse MCP servers with a fuzzy picker, or manage them at runtime: `list`/`status` (health, identity, negotiated capabilities), `ping`, `reconnect`, `enable`/`disable`, `add`, `remove`. |
| `/tools` | List the complete tool surface. |
| `/config` | Show the active configuration source. |
| `/clear` | Clear conversation history and usage. |
| `/help` | Show command help and keybindings. |

Informational commands (`/status`, `/context`, `/ps`, `/tasks`, `/models`, `/tools`, `/skills list`, `/mcp list`, `/config`, `/help`) render their output in a titled, theme-colored panel — the command's subject sits in the box border, body text is tinted with a readable shade derived from the theme (not the terminal's raw default color), and content wraps cleanly to the terminal width. Quick acknowledgements ("Theme switched…") stay as subtle one-line notes.

Typing `/` opens a command palette that filters as you type and completes argument values (`/theme dra…`, `/autonomy …`, `/model …`): ↑/↓ selects, `tab` completes, `enter` runs, `esc` dismisses. Typing `@` opens a fuzzy workspace-file picker that inserts the chosen path into your prompt. These fuzzy menus keep their compact position beside the composer. Approvals, hunk review, and questions use centered floating dialogs instead: they preserve the surrounding transcript, take keyboard focus while active, match the selected theme, and disappear as soon as the action is resolved.

`ctrl+t` cycles the Chat, Session, and Help tabs, and `ctrl+o` expands or collapses finished tool output. The Session tab shows the live task plan, changed files, active/finished delegated agents, and running background processes; the status bar carries live badges for all of them. Fenced code in assistant messages is syntax-highlighted with the language named after the opening fence (for example, a fence labeled `go`). Expanded `read_file` results select a lexer from the filename, and `git_diff` results receive diff highlighting, so source remains readable in the normal Chat transcript as well as in approval previews. Syntax colors follow the active theme; `plain`/`NO_COLOR` disables them.

When an approval or question is waiting, or a turn longer than ten seconds finishes, Collomia rings the terminal bell **and** posts a desktop notification through the terminal (the OSC 9 sequence — iTerm2, WezTerm, Ghostty, Kitty, and Windows Terminal support it; most only surface it while the window is unfocused, and unsupported terminals ignore it). Tune this with:

```json
{
  "options": { "notifications": "on" }
}
```

`"on"` (default) is bell plus desktop notification, `"bell"` is the bell only, `"off"` is silent.

### Approving changes

When the agent proposes a write, command, or other privileged action, a centered floating approval dialog shows the action and a colorized diff preview (for file changes) and waits for a decision. The dialog closes immediately after the choice:

| Key | Effect |
| --- | --- |
| `y` / `enter` | Approve this one action. |
| `a` | Approve, and auto-approve this tool for the rest of the session. |
| `n` / `esc` | Deny. |
| `h` | For a `write_file` change with two or more diff hunks: open **hunk review** — accept or reject each hunk independently instead of the whole file. |

Hunk review replaces the approval dialog with a focused preview of the current hunk. ↑/↓ (or `j`/`k`) navigate, `space` toggles the current hunk, `a` keeps all, `enter` applies only the selected hunks (composed against a fresh read of the file), and `esc` returns to the normal approval dialog. `edit_file` (a single atomic replacement) and `apply_patch` (a multi-file changeset) stay file-level for now. Questions from the agent and MCP elicitation use the same transient dialog treatment, with their answer editor inside the box.

## Themes

Collomia ships nineteen themes: `collomia` (default), `synthwave`, `outrun`, `blade-runner-2049`, `chaos-theory`, `cyberpunk-2077-blue`, `cyberpunk-2077-violet`, `catppuccin-mocha`, `gruvbox-dark`, `rose-pine-moon`, `kanagawa-wave`, `matrix`, `monokai`, `dracula`, `nord`, `tokyo-night`, `fredhutch-dark`, `fredhutch-light`, and `plain` — a fully colorless theme that relies on bold, reverse video, and borders (useful for limited terminals, screen readers, and transcripts). Setting the standard [`NO_COLOR`](https://no-color.org) environment variable selects `plain` automatically, overriding any configured theme. Switch at runtime with `/theme <name>`, or persist a choice in the configuration:

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

Collomia keeps an effective capability declaration for every provider/model selection. It distinguishes `supported`, `partial`, `unsupported`, and `unknown` for tool calling, streaming, reasoning, images, structured output, token usage, prompt caching, parallel tool calls, and model discovery; it also carries the configured context window and adapter-specific constraints. `/status`, `/model`, and `/models` expose this information. `/models` probes supported catalog endpoints concurrently and reports providers without a catalog as **unverified**, not incorrectly **unavailable**. Catalogs such as OpenAI-compatible `GET /models` often return names without feature metadata, so Collomia reports model-dependent facts as unknown instead of inferring capabilities from a model name.

The declaration describes what Collomia's current adapter can send and consume, which may be smaller than the vendor API's complete feature set. Before any provider request, Collomia rejects a known contradiction (for example, sending tools through an adapter declared not to support them, or configuring a maximum output larger than the context window). Unknown and partial capabilities remain visible but do not cause speculative failures.

Streaming adapters normalize upstream events before they reach the agent: text, provider-supplied reasoning text/summaries, incremental tool-call arguments, complete usage snapshots, warnings, and classified errors all follow one contract. Tool arguments may be incomplete JSON while they stream; Collomia assembles and validates the final document before approval or execution. An HTTP failure before streaming starts can use the normal retry policy. An error carried inside a stream is returned without replaying the request, preventing duplicate deltas and surprise repeat billing.

All built-in HTTP adapters use the same resilience policy. Network failures and HTTP 408, 429, 5xx, and 529 responses are retried up to three attempts with bounded exponential backoff, jitter, and `Retry-After`; authentication, permission, not-found, and other ordinary 4xx failures are not retried. A request is retried only when its body can be replayed. Failures are classified as `authentication`, `permission`, `rate_limit`, `invalid_request`, `not_found`, `timeout`, `unavailable`, `protocol`, `cancelled`, or `unknown`; status, retry timing, and request IDs are included when the provider supplies them. In `--jsonl` mode the same fields appear under `provider` on the `error` event.

OpenAI-protocol models do not all accept the same Chat Completions fields. Collomia sends the backward-compatible `max_tokens` field first. Only when an upstream HTTP 400 explicitly rejects that field and directs the caller to `max_completion_tokens` does Collomia rebuild and resend the request; a similarly explicit rejection of a configured `temperature` retries with the provider default and emits a warning. Those choices are remembered for the active provider/model client, so later turns avoid the failed probe. Successful requests, unrelated 400 responses, and providers that accept the original fields are never rewritten. The configured `max_tokens` remains Collomia's provider-neutral output budget; for reasoning models the upstream `max_completion_tokens` interpretation includes hidden reasoning tokens as well as visible output.

Three consecutive transient request failures open a 30-second circuit so a broken endpoint is not hammered; one recovery probe closes it. `/status` shows the active provider as `not checked yet`, `healthy`, `degraded`, `circuit open`, or `testing recovery`. Switching provider/model starts a fresh health state. Timeouts are configured per provider (values shown are the defaults):

```json
{
  "providers": {
    "example": {
      "type": "openai-compatible",
      "base_url": "http://127.0.0.1:11434/v1",
      "model": "qwen3-coder",
      "connect_timeout_seconds": 10,
      "request_timeout_seconds": 1800,
      "stream_idle_timeout_seconds": 300
    }
  }
}
```

`connect_timeout_seconds` bounds connection establishment, `request_timeout_seconds` bounds the complete request, and `stream_idle_timeout_seconds` bounds the silence between response chunks. Increase the idle timeout for models that legitimately spend more than five minutes without emitting data; do not disable these bounds for an unattended run.

### OpenAI-compatible services

This adapter works with OpenAI, Ollama, vLLM, LM Studio, Phlox-GW, and compatible gateways. It uses `/chat/completions`, streaming SSE, and function tools. Picking a provider in `/model` queries its live `GET /models` catalog when supported.

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

OpenRouter is also available in the generated global starter as an unselected provider example:

```json
{
  "providers": {
    "openrouter": {
      "type": "openai",
      "base_url": "https://openrouter.ai/api/v1",
      "api_key_env": "OR_API_KEY",
      "model": "z-ai/glm-5.2",
      "max_tokens": 128000,
      "context_window": 500000
    }
  }
}
```

Use type `openai` for the OpenAI API and compatible hosted APIs such as OpenRouter; use `openai-compatible` for local or custom Chat Completions implementations.

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

Native Bedrock uses the `ConverseStream` API and supports both AWS SigV4 credentials and the newer Amazon Bedrock bearer API keys. Text, tool arguments, provider-supplied reasoning, and token usage arrive through AWS event-stream framing; in-stream throttling/service/validation exceptions keep their provider classification and request ID. `auth` controls the credential family:

- `auto` (the default) uses `api_key`/`api_key_env` or `AWS_BEARER_TOKEN_BEDROCK` when present; otherwise it uses the AWS credential chain.
- `sigv4` requires the AWS credential chain even if a bearer token is present.
- `bearer` requires a short- or long-term Bedrock API key and never attempts SigV4.

For production and human development, prefer temporary SigV4 credentials obtained through an IAM role or IAM Identity Center. The SDK chain supports long-term IAM access/secret pairs, temporary access/secret/session-token credentials, shared profiles, SSO, assume-role/web-identity flows, and ECS/EKS/EC2 workload roles:

```json
{
  "providers": {
    "bedrock": {
      "type": "bedrock",
      "auth": "sigv4",
      "region": "us-west-2",
      "profile": "development",
      "model": "your-bedrock-model-id"
    }
  }
}
```

The profile is optional. With no profile, the SDK uses its default chain. Environment-based temporary credentials use all three standard values:

```bash
export AWS_ACCESS_KEY_ID="..."
export AWS_SECRET_ACCESS_KEY="..."
export AWS_SESSION_TOKEN="..." # required for STS/temporary credentials
export AWS_REGION="us-west-2"
```

For either a short-term or long-term Amazon Bedrock API key, use bearer mode. The key type is encoded by AWS; Collomia sends both forms identically and never writes the key into session data:

```bash
export AWS_BEARER_TOKEN_BEDROCK="..."
```

```json
{
  "providers": {
    "bedrock-key": {
      "type": "bedrock",
      "auth": "bearer",
      "api_key_env": "AWS_BEARER_TOKEN_BEDROCK",
      "region": "us-west-2",
      "model": "us.anthropic.claude-sonnet-4-6"
    }
  }
}
```

Collomia consumes an already-generated Bedrock API key; it does not currently mint or refresh short-term keys. Replace the environment value and restart Collomia before an expiring key becomes invalid. AWS recommends short-term keys for production and long-term keys only for exploration. Bearer keys are limited to supported Bedrock/Bedrock Runtime operations; they do not grant Agents or bidirectional-stream operations. `collo doctor` reports the selected authentication family and missing bearer-token variables without printing credential values.

Bedrock Mantle uses the OpenAI Responses API and a Bedrock API key. Collomia requests SSE and also accepts a synchronous JSON response from a compatible endpoint; incomplete Responses runs produce an explicit warning instead of being mistaken for a complete answer:

```json
{
  "providers": {
    "mantle": {
      "type": "bedrock-mantle",
      "base_url": "https://bedrock-mantle.us-west-2.api.aws/v1",
      "api_key_env": "AWS_BEARER_TOKEN_BEDROCK",
      "model": "openai.gpt-oss-120b"
    }
  }
}
```

### Azure OpenAI and Microsoft Foundry

Azure providers accept three explicit authentication modes:

- `api_key` (also the backward-compatible default when `auth` is omitted)
  reads `api_key`/`api_key_env` and sends the Azure `api-key` header.
- `bearer` sends a token from `api_key`/`api_key_env`. This compatibility mode
  cannot refresh the token; use it only when another process rotates the value
  and Collomia is restarted.
- `entra` uses the official Azure Identity SDK's `DefaultAzureCredential`.
  Tokens are held only in memory and refreshed proactively before the SDK's
  `RefreshOn` time or expiration. Do not configure an API key in this mode.

The Azure OpenAI adapter supports the deployment-scoped GA API. This keyless
example uses the documented Cognitive Services scope:

```json
{
  "providers": {
    "azure-openai": {
      "type": "azure-openai",
      "base_url": "https://my-resource.openai.azure.com",
      "auth": "entra",
      "deployment": "my-code-model",
      "api_version": "2024-10-21",
      "model": "my-code-model"
    }
  }
}
```

Microsoft Foundry's OpenAI/v1 and Claude Messages endpoints use the Azure AI
scope and are both supported by the same refreshable credential path:

```json
{
  "providers": {
    "foundry": {
      "type": "azure-foundry",
      "base_url": "https://my-resource.openai.azure.com/openai/v1",
      "auth": "entra",
      "model": "my-deployment"
    },
    "foundry-claude": {
      "type": "azure-foundry-anthropic",
      "base_url": "https://my-resource.services.ai.azure.com/anthropic",
      "auth": "entra",
      "model": "claude-sonnet-4-6"
    }
  }
}
```

`DefaultAzureCredential` checks environment service-principal credentials,
workload identity, managed identity, Azure CLI (`az login`), Azure Developer CLI
(`azd auth login`), and Azure PowerShell. For a service principal with a secret,
set `AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, and `AZURE_CLIENT_SECRET`; for a
user-assigned managed identity, set `AZURE_CLIENT_ID`. In deployed environments,
set `AZURE_TOKEN_CREDENTIALS=prod` to restrict the chain to environment,
workload, and managed identities. During local development, `dev` restricts it
to developer tools; an individual credential name such as
`AzureCLICredential` is also accepted.

The default scopes are:

| Provider type | Default `entra_scope` | Typical data-plane role |
| --- | --- | --- |
| `azure-openai` | `https://cognitiveservices.azure.com/.default` | Cognitive Services OpenAI User |
| `azure-foundry`, `azure-foundry-anthropic` | `https://ai.azure.com/.default` | Cognitive Services User |

RBAC assignments apply to the target resource and can take several minutes to
propagate. A 401/403 returned in `entra` mode includes the relevant role and
scope guidance without exposing a token. `collo doctor` reports the credential
chain selector, effective scope, tenant, authority, and required role, but never
acquires or prints a token.

Azure reasoning deployments such as GPT-5 require `max_completion_tokens`
instead of the legacy `max_tokens`, while some older Azure deployments reject
the newer field. Collomia therefore does not guess from a deployment name or
change every Azure request. It reacts only to the provider's structured 400,
retries before any stream data is emitted, and remembers the accepted shape for
the active model. If the model explicitly rejects a configured `temperature`,
Collomia also retries with the model default and reports that adjustment.

For a different tenant or a sovereign/private cloud, override only the values
your Azure environment documents:

```json
{
  "providers": {
    "foundry-government": {
      "type": "azure-foundry",
      "base_url": "https://your-private-or-sovereign-endpoint/openai/v1",
      "auth": "entra",
      "entra_tenant_id": "your-tenant-id",
      "entra_authority_host": "https://login.microsoftonline.us/",
      "entra_scope": "https://your-documented-audience/.default",
      "model": "your-deployment"
    }
  }
}
```

The authority must be an HTTPS origin and the scope must be an HTTPS `/.default`
audience. Collomia does not guess sovereign-cloud audiences. Private DNS
endpoints work through `base_url` as long as the host is reachable and its TLS
certificate is trusted by the operating system.

API-key mode remains available for either provider family:

```json
{
  "providers": {
    "foundry-key": {
      "type": "azure-foundry",
      "base_url": "https://my-resource.openai.azure.com/openai/v1",
      "auth": "api_key",
      "api_key_env": "AZURE_FOUNDRY_API_KEY",
      "model": "my-deployment"
    }
  }
}
```

Microsoft references: [Go credential
chains](https://learn.microsoft.com/azure/developer/go/sdk/authentication/credential-chains),
[Azure OpenAI keyless
authentication](https://learn.microsoft.com/azure/developer/ai/get-started-securing-your-ai-app),
[Foundry Models keyless
authentication](https://learn.microsoft.com/azure/foundry/foundry-models/how-to/configure-entra-id),
and [Claude on Foundry
authentication](https://learn.microsoft.com/azure/foundry/foundry-models/how-to/use-foundry-models-claude),
and [Azure OpenAI reasoning-model request
requirements](https://learn.microsoft.com/azure/foundry/openai/how-to/reasoning).

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
      "(?i)(^|[;&|]\\s*)rm(?:\\s+[^;&|\\s]+)*\\s+(--recursive|-[^;&|\\s]*r[^;&|\\s]*)($|\\s)"
    ],
    "sandbox": "auto",
    "sandbox_allow_network": true,
    "command_env": "minimal"
  }
}
```

`allowed_tools` is a persistent explicit grant. Interactive approval with `a` normally grants a tool for the remainder of the current process. The example `denied_commands` entry adds all direct recursive `rm` invocations to Collomia's mandatory protections. Global and trusted project patterns only add to the effective set; no subordinate configuration can remove an inherited command denial.

Shell safety has three outcomes. Routine scoped operations such as `rm -rf node_modules`, `rm -rf /tmp/example`, and formatting a workspace disk-image file follow the selected autonomy mode. Destructive but legitimate operations—such as `git reset --hard`, machine shutdown, bulk cloud/IaC deletion, or a recursive target that cannot be resolved statically—require a fresh one-time approval; allow rules, autopilot, and “always allow” cannot skip it. Catastrophic outcomes—such as recursively deleting `/`, the home or workspace root, `.git`, `~/.collomia`, Windows drive/system roots, or writing a physical disk—are refused and cannot be approved. Both structural checks and regex denials are repeated immediately before foreground or background execution. Use `collo policy check '<command>'` to inspect the result without running it. See the [security model](docs/SECURITY.md#command-safety-tiers) for the complete categories and limitations.

Path tools canonicalize paths and existing symlinks before checking containment. Outside access requires both `allow_outside_workspace: true` and an applicable permission decision. Tool output, file reads, and commands are size- and time-bounded, and every command runs in its own process group so cancellation and timeouts kill all descendants — including detached [background processes](#background-processes), which are additionally stopped at session exit regardless of how they were started.

**What these checks are — and are not.** Approval prompts, rules, and denial patterns are in-process policy checks, not an operating-system security boundary, unless the OS sandbox is enabled. An approved (or autopilot-approved) command runs with your normal user privileges. Shell commands are statically analyzed before approval; commands whose effect cannot be determined (substitutions, `eval`, inline interpreter payloads) always require interactive approval, in every mode.

`"permissions": {"sandbox": "auto"}` (or `"require"` to fail closed) enables real OS enforcement. Sandboxing remains `off` unless you select it, while `sandbox_allow_network` defaults to `true`; this compatibility-first combination means changing only `sandbox` to `auto` does not block package installation or online command-line tools at the network boundary. Set `sandbox_allow_network` to `false` when you want command traffic denied. A package manager may still need an explicit cache root or environment credentials as described below.

- **macOS**: Seatbelt (`sandbox-exec`) confines file writes to the workspace and denies network egress unless `sandbox_allow_network` is set.
- **Linux**: Landlock confines file writes to the workspace (kernel 5.13+); on kernel 6.7+ (Landlock ABI v4) it also denies TCP connect/bind unless `sandbox_allow_network` is set. UDP, including DNS, cannot be restricted by Landlock yet.
- **Windows 11**: the built-in AppContainer security boundary confines filesystem/registry/credential/process access, with a Job Object owning the descendant tree. The workspace, temp directory, and any `sandbox_writable_roots` are granted to a workspace-specific container. No Hyper-V feature, administrator setup, driver, service, or separate installation is required.

The network switch applies only to sandboxed `run_command`, PTY, and background-process traffic. Collomia's provider HTTP, remote MCP connections, hooks, and language servers are not routed through this command sandbox. If a build needs an external cache, add only that directory to `sandbox_writable_roots`. If it needs proxy variables or registry credentials, use `command_env: "full"` deliberately; sandboxed commands otherwise receive the minimal environment by default.

`auto` applies every protection the platform has and prints a warning when a requested capability is missing. `require` refuses the command instead. For example, Linux Landlock cannot deny UDP, so `require` plus `sandbox_allow_network: false` fails closed rather than claiming complete network isolation; use `auto` to accept the prominently reported TCP-only boundary. `collo doctor` and `/status` show the backend, effective command-network setting, and any missing protection.

Two more knobs narrow the blast radius further: `command_env: "minimal"` strips agent commands down to `PATH`/`HOME`/basics instead of inheriting your full environment, and `reviewer_command` runs an external program of your choosing before any non-read action is auto-approved — a non-zero exit or a `{"decision":"deny"}` reply escalates it to an interactive prompt instead of silently allowing it. The exact guarantees and limitations of every mode and backend are documented in [docs/SECURITY.md](docs/SECURITY.md).

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

A repository can ship `.collomia.json`, skills, and instruction files. When `.collomia.json` is present, the entire project layer — including project skills and instructions — stays quarantined until you review and approve the file with `collo trust`. Trust is bound to the project configuration's content hash and is automatically invalidated when the file changes. `collo trust --status` shows the current state; `collo trust --revoke` withdraws approval. A workspace with no `.collomia.json` has no configuration file to approve, so review repository-provided skills and instructions before use; the [user guide](docs/USER_GUIDE.md#repository-trust) explains this boundary.

## Instructions

Beyond the project's `.collomia.json`, Collomia layers plain-text instructions into every system prompt:

- A **user-level** `AGENTS.md` or `COLLOMIA.md` in your Collomia configuration directory (next to `config.json`) applies to every workspace you use.
- A **project-level** `AGENTS.md` or `COLLOMIA.md` in the workspace root applies to that repository when the runtime project-trust state permits it (a present `.collomia.json` must be trusted). Project instructions are layered after (and can refine or override) the user-level ones.

Use these for house style, testing conventions, deployment gotchas — anything you'd otherwise repeat in every prompt.

## Skills

A skill is a directory with a `SKILL.md` manifest and, optionally, supporting material the agent uses on demand:

```
release-check/
  SKILL.md          # required: front matter + instructions
  scripts/          # executable helpers the agent runs with run_command
  references/       # extra documentation the agent reads with read_file
  assets/           # templates and files used in the skill's output
```

Skills are discovered from two scopes with deterministic precedence — **project** skills (`.collomia/skills/<name>/` or `.agents/skills/<name>/`, active when the runtime project-trust state permits them; a present `.collomia.json` must be trusted) shadow **global** skills of the same name (`~/.collomia/skills/<name>/`, applying to every workspace). Legacy single-file `SKILLS.md`/`skills.md` manifests are still read. Shadowed duplicates are reported at startup rather than silently dropped.

`SKILL.md` starts with YAML front matter. `name` (lowercase letters, digits, hyphens; must match the directory) and `description` are required; the parser also understands folded/literal blocks, `license`, `allowed-tools`, and a nested `metadata` map:

```md
---
name: release-check
description: >-
  Verify release artifacts, checksums, and changelog conventions
  before publishing a new version.
allowed-tools: [read_file, run_command]
metadata:
  version: 1.0.0
---

# Release check

Full instructions that are loaded only when this skill is relevant.
Point the agent at references/ files and scripts/ helpers as needed.
```

Only names and descriptions enter the system prompt (`/skills` opens a fuzzy picker; choosing a skill pre-fills the prompt with `Use the "<name>" skill: ` so you just add the task). The model calls `load_skill` to pull the full instructions into context when relevant — the loaded result maps out the bundled scripts, references, and assets so the agent can use them without guessing paths. Unused skills cost nothing; bundled files cost nothing until read. Skill scripts execute through the same permission rules and sandbox as any other command — bundling a script grants it nothing.

The `collo skills` command manages the whole lifecycle:

```bash
collo skills list                    # every skill: scope, version, bundle size, validation warnings
collo skills show release-check      # full metadata, sha256, bundled file listing
collo skills new my-skill            # scaffold a project skill (.collomia/skills/my-skill/)
collo skills new my-skill --global   # scaffold in ~/.collomia/skills/ for every workspace
collo skills install ./some-skill    # validate and copy a skill directory in (--global for user scope)
collo skills update ./some-skill     # reinstall, replacing the existing version
collo skills disable my-skill        # keep it installed but hidden from the agent
collo skills enable my-skill
collo skills remove my-skill --yes
```

Validation problems (missing front matter, name/directory mismatch, oversized descriptions) are shown as warnings in `list` and `show` without breaking discovery, and installs refuse symlinks and oversized trees.

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

Remote tool names are exposed as `mcp_<server>_<tool>`. MCP tool annotations are never trusted to lower permissions: calls are classified as external and require approval unless that exact tool is allow-listed. `/mcp` opens a picker of connected servers; choosing one lists its tools with descriptions.

Servers are managed at runtime without restarting:

```
/mcp status                 every server: health, transport, protocol, server identity,
                            capabilities, live/pending catalogs, tool count, errors
/mcp ping docs              health-check one server (a failure is recorded as an error state)
/mcp refresh docs           reload tools in place without reconnecting
/mcp reconnect docs         tear down and re-establish the session, refreshing its tool catalog
/mcp disable docs           close the server and withdraw its tools for this session
/mcp enable docs            bring it back (cannot override missing trust)
/mcp add scratch npx -y @modelcontextprotocol/server-filesystem .
/mcp add remote --url https://example.com/mcp
/mcp remove scratch         disconnect and forget (configured servers return next start)
```

Untrusted, disabled, and failed servers stay visible in `/mcp status` with their exact initialization errors instead of silently disappearing, so a misconfigured server is diagnosable from inside the session. Servers added with `/mcp add` are session-scoped and user-initiated (the trust gate quarantines *repository-supplied* configuration, not your own commands); add them to the configuration file to keep them.

Beyond tools, Collomia uses two more MCP capabilities when a server negotiates them:

- **Resources** — `/mcp resources <server>` browses what the server publishes (URI, type, size, description) and `/mcp resource <server> <uri>` previews one in the transcript. The agent has matching tools, `list_mcp_resources` and `read_mcp_resource`, classified as external calls scoped to the named server so permission rules keep matching.
- **Prompts** — `/mcp prompts <server>` lists a server's prompt templates with their arguments; `/mcp prompt <server> <name> key=value …` expands one and places the result in the input box, so you review and edit the template output before anything is sent to the model.

Tool results keep their typed content instead of being flattened: text and structured output come through as-is, embedded resources contribute their text, images and audio become explicit `[image image/png, N bytes]` markers, and resource links keep their URI along with a hint that `read_mcp_resource` can follow them.

Three more protocol features are supported end to end:

- **Progress** — when an MCP tool reports progress during a long call, the updates stream live into the transcript exactly like command output (`progress: 3/10 — indexing…`).
- **Elicitation** — a server can pause a tool call to ask the user for input. Form-mode requests become typed questions in the TUI (enum fields offer their options, booleans offer true/false, esc declines the whole request — sensitive input never defaults to acceptance). URL-mode elicitation is declined outright, and headless runs never advertise the capability, so servers cannot fish for input when nobody is there.
- **Server pinning** — Collomia fingerprints each configured server's definition (transport, command, arguments, URL, and the *names* of env vars and headers — values are excluded so rotating a token is not a false alarm) and records the remote implementation's identity, per workspace, in the per-user state directory outside any repository. If a server's definition or its remote identity changes since last use, the session starts with an explicit warning naming the change — a tripwire for a swapped binary or a quietly edited server entry, layered on top of workspace trust (which already invalidates on any project-config change).

MCP catalogs also stay live. When a server advertises and sends a tools
`list_changed` notification, Collomia fetches and validates the complete new
list, then swaps registry entries atomically. A failed refresh leaves the
last-known-good tools callable and appears in `/mcp status`; `/mcp refresh
<server>` retries without reconnecting. Resource and prompt listings are read
live, so their notifications are shown as pending until the next successful
`/mcp resources` or `/mcp prompts` call. `/mcp status` reports the negotiated
protocol revision and exactly which catalogs advertised list-change support.

The supported protocol subset and fixture coverage are detailed in
[docs/MCP_PROTOCOL.md](docs/MCP_PROTOCOL.md). Experimental MCP tasks, resource
subscriptions, and standards-based OAuth/login are not yet implemented.

MCP configuration can launch processes or contact remote services. Servers are not started unless their entry explicitly sets `"trusted": true`; review a project-provided `.collomia.json` before granting that trust — this is exactly what `collo trust` gates.

## Hooks

Hooks run your own commands at lifecycle points, receiving a structured JSON payload on stdin — the raw material for custom notifications, audit pipelines, metrics, or policy tripwires that live outside Collomia:

```json
{
  "hooks": {
    "file_change": [
      { "command": "/usr/local/bin/my-audit", "args": ["--log"] }
    ],
    "tool_start": [
      { "command": "./scripts/guard.sh", "matcher": "run_command|apply_patch", "timeout_seconds": 5 }
    ]
  }
}
```

Eleven events cover the session lifecycle: `session_start`, `user_prompt`, `permission_decision`, `tool_start`, `tool_end`, `file_change`, `compaction`, `subagent_start`, `subagent_end`, `stop` (turn finished), and `session_end`. The payload always carries the event, workspace, and subject; tool events add the tool name, summary, arguments, and touched paths; permission events add the decision and its source.

Two events can gate: a `user_prompt` or `tool_start` hook blocks the action by exiting with status `2` or printing `{"decision":"block","reason":"…"}` — the reason is shown to the model (or the user) in place of the result. Hooks can only tighten: a hook cannot approve anything the permission engine would deny, and it cannot bypass the sandbox. Everything else about them is bounded — `matcher` (a regex on the tool or event name) scopes when they run, timeouts default to 10 seconds, output is capped, and a failing or timed-out hook becomes a logged warning rather than a broken session.

Hooks are trusted code, executed as ordinary subprocesses. Project-configured hooks are quarantined until `collo trust`, exactly like project MCP servers and skills.

## Sub-agents and multi-agent delegation

The `delegate` tool lets the agent fan out bounded work to sub-agents instead of doing everything serially in one context:

```
delegate({
  "tasks": [
    { "name": "investigate-auth", "task": "How does session expiry currently work?" },
    { "name": "add-retry-logic", "task": "Add exponential-backoff retry to the HTTP client.", "write": true },
    { "name": "security-pass", "task": "Look for injection risks in the new endpoint.", "agent": "reviewer" }
  ]
})
```

- Up to **6 tasks per call**, up to **4 running concurrently**. Each gets its own 10-minute timeout.
- **Read-only by default**: a task without `"write": true` shares the parent workspace and can only investigate — cheap, and safe to run alongside anything else.
- **Write-capable tasks are isolated**: `"write": true` gives that sub-agent its own `git worktree`, its own tool registry, its own permission manager, and its own audit ledger. Parallel writers can never race on the same files. Nothing is ever merged, committed, or pushed automatically — a worktree with real changes is left in place (path and branch reported back) for you to review and merge by hand; a worktree with no changes is cleaned up automatically. This requires the workspace to be a git repository.
- **Sibling conflict detection**: once a batch finishes, files touched by more than one write-capable sub-agent's worktree are called out with a warning, so overlapping work is surfaced rather than silently lost.
- **Named agent profiles**: define reusable roles in configuration and select one per task with `"agent": "<name>"`:

  ```json
  {
    "agents": {
      "reviewer": {
        "model": "gpt-5.1-mini",
        "instructions": "You are a security reviewer. Focus only on injection, auth, and secrets handling.",
        "tools": ["read_file", "search_files", "search_symbols", "git_diff"],
        "max_iterations": 12
      }
    }
  }
  ```

  Any field a profile omits falls back to the parent agent's own setting (same model, no extra restrictions, default iteration budget).
- The Session tab shows every delegated task's status, changed files, and worktree path; the status bar shows an `agents N` badge while any are running.

Sub-agents cannot recursively delegate.

## Background processes

For anything that shouldn't block the turn — a dev server, a file watcher, a long-running test suite — the agent has:

| Tool | Purpose |
| --- | --- |
| `start_process` | Launch a command detached from the turn; returns its id immediately. Same denied-pattern, shell-analysis, sandbox, and environment policy as `run_command`. |
| `list_processes` | Ids, commands, status, and uptime. |
| `process_output` | The process's retained output (last 64 KiB, optionally the last N lines). |
| `stop_process` | Stop one process — kills its whole process group so nothing lingers. |

From the TUI: `/ps` lists them, `/ps stop <id>` stops one, the Session tab shows them live, and the status bar carries a `procs N` badge while any are running. Every background process — including ones started by write-capable delegated sub-agents in their own worktrees — is stopped when the session ends, so nothing outlives Collomia.

## Code intelligence

Two tools give the agent real understanding of the codebase instead of guessing from text search:

- **`search_symbols`** queries an incremental, ignore-aware definition index (`.git`, `node_modules`, `vendor`, and similar build/dependency directories are skipped). It covers functions, methods, types, classes, interfaces, constants, and enums for **Go, Python, JavaScript/TypeScript, and Rust**, ranked exact-match first, then prefix, then substring. Only files that changed since the last query are re-parsed. Use `search_files` for references and arbitrary text.
- **`diagnostics`** runs the project's real language server against requested files (a minimal stdio JSON-RPC LSP client) and returns severity-ordered findings with exact files and lines — the same errors and warnings your editor would show, not a guess. `gopls`, `pyright-langserver`, `typescript-language-server`, and `rust-analyzer` are auto-detected on `PATH`; override or add servers per language:

  ```json
  {
    "lsp": {
      "go": ["gopls", "serve"],
      "python": ["pyright-langserver", "--stdio"]
    }
  }
  ```

  All files in one `diagnostics` call must share a language; the language server must be installed for the tool to do anything (it reports clearly when one isn't found).

## Verification loop

Rather than have the model guess at build/test commands, `detect_verification` inspects the workspace root for known project files — `go.mod`, `package.json` (reading its actual `scripts`, preferring `pnpm`/`yarn` when their lockfile is present), `Cargo.toml`, `pyproject.toml`/`requirements.txt`/`setup.py` (with a `ruff` suggestion when configured), and `Makefile` targets — and reports the real commands for that project.

`collo verify [focus]` and `/verify [focus]` run a canned loop on top of it: detect the commands, record each as a plan step, run it with the live-streamed `run_command`, and mark the step done or blocked with the command's own exact output as evidence — never claiming a pass the tool result didn't report. It only reports; it does not modify files.

## Sessions

Every conversation is a durable, crash-safe session (append-only JSONL, stored outside the workspace):

```sh
collo sessions list
collo sessions show <id>
collo sessions fork <id>
collo sessions rename <id> "auth refactor"
collo sessions archive <id>
collo sessions delete <id>
collo --resume <id>
collo --continue          # resume the most recently updated session
```

Inside the TUI, `/sessions` opens a fuzzy picker that switches the **live** conversation in place — transcript, plan, and persistence hooks all move over, no restart needed — and `/new` starts a fresh one while the current session stays saved. `collo sessions fork <id>` copies history into an independent session that shares the past but diverges from there. Loading tolerates a torn final write (a crash mid-append) and marks any tool call with no recorded result as interrupted rather than silently replaying it. The context window is managed automatically: usage-anchored estimates trigger compaction above 80% of the model's window, summarizing older messages while keeping recent ones (and the full durable transcript) intact; `/compact [focus]` compacts on demand.

## Architecture

```text
cmd/collo              command parsing and interactive/non-interactive entrypoint
internal/app            runtime wiring: providers, tools, permissions, sessions, plan
internal/tui             Bubble Tea interface, slash commands, approval broker, hunk review
internal/agent            provider-neutral tool loop, plan mode, delegate scheduler
internal/provider          OpenAI, Anthropic, Bedrock, Mantle, and Azure adapters
internal/tools               filesystem/search/edit/shell/process/diagnostics tool registry
internal/index                 incremental workspace symbol index
internal/lsp                    minimal stdio JSON-RPC LSP client
internal/diffmodel                unified diff, hunk parse/apply, checkpoint/undo tracking
internal/permission             autonomy policy, scoped rules, external reviewer hook
internal/sandbox                  OS sandbox backends (Seatbelt, Landlock)
internal/policy                    scoped allow/prompt/deny rule matching
internal/shell                      outcome-aware command safety analysis
internal/audit                       permission-decision and outcome ledger
internal/session                      durable session store, resume/fork, compaction
internal/plan                          structured plan artifact
internal/event                          schema-versioned runtime event model
internal/skills                          progressive skill discovery and loading
internal/mcp                              official SDK transport and tool bridge
internal/config                            cross-platform layered JSON configuration
internal/trust                              repository trust store
internal/redact                              secret redaction for logs/events/ledger
internal/logging                              redacted structured debug logging
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

These commands run the recorded provider contracts used by CI and never need
cloud credentials. Maintainers can additionally qualify real OpenAI,
Anthropic, Responses/Mantle, and Bedrock endpoints with the double-opt-in,
credential-safe [live provider contract suite](docs/LIVE_PROVIDER_CONTRACTS.md).
It makes two model requests per configured endpoint and is not enabled by the
ordinary test suite.

The implementation follows the MCP security recommendation to keep a human able to inspect and deny tool calls, and uses protocol-native JSON Schema tool definitions throughout.
