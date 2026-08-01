# Collomia - Agentic Coding System

![screenshot](docs/collo-screenshot.png)


Collomia is a safety-focused, multi-provider coding agent for the terminal. It is written in Go, ships as one `collo` binary, and runs on macOS, Linux, and Windows. Its permission system is a layered policy engine — with built-in OS sandbox backends on all three platforms — whose exact guarantees are documented in [docs/SECURITY.md](docs/SECURITY.md).

New users and advanced operators should start with the [complete Collomia user guide](docs/USER_GUIDE.md), which covers installation on every platform, configuration layering, every provider and authentication mode, permissions and sandboxes, LSP, MCP, hooks, skills, sub-agents, sessions, automation, and troubleshooting. The focused [installation guide](docs/INSTALLING.md) covers checksum and provenance verification, upgrades, and rollback; [beta status](docs/BETA.md) states the current limitations; and the [compatibility and migration policy](docs/COMPATIBILITY.md) defines the supported configuration, session, and automation formats. CI and integration authors can use the dedicated [automation and JSONL contract](docs/AUTOMATION.md). Linux operators enabling sandboxing also have a dedicated [Landlock setup and compatibility guide](docs/LINUX_SANDBOX.md).

It combines a streaming agent loop with a polished Bubble Tea TUI, workspace-aware tools, human approval gates (down to individual diff hunks), a parallel multi-agent scheduler with git-worktree isolation, skills, MCP tools, background process management, code intelligence (a symbol index and real language-server diagnostics), and a verification loop that runs your project's own build/lint/test commands.

An up-to-date, generated list of exactly what is implemented, experimental, or unsupported lives in [docs/CAPABILITIES.md](docs/CAPABILITIES.md) (`collo capabilities`), and [docs/FEATURES.md](docs/FEATURES.md) is the prose summary of the same ground, including the security boundaries and limitations. The concise [roadmap](ROADMAP.md) tracks what is still ahead; the dated implementation record is preserved in [docs/ROADMAP_HISTORY.md](docs/ROADMAP_HISTORY.md).

## Highlights

- Interactive TUI with Markdown and syntax-highlighted code rendering, Chat/Session/Help tabs, a filtering slash-command palette with argument completion, fuzzy pickers for models/themes/sessions/delegated agents, `@` file/folder mentions, prompt-from-file, collapsible tool output, searchable/copyable transcript mode, responsive full-screen diff review, configurable global keys, and a status bar with live context, task-progress, active-agent, and background-process gauges.
- Nineteen switchable themes (including Fred Hutch dark/light and the colorless `plain` mode) that also set the terminal background to match when color is enabled.
- Streaming OpenAI-compatible, Anthropic-compatible, Responses-style, and native Bedrock ConverseStream conversations with tool calling, capability-aware live model discovery (`/model` and `/models`), typed image input, request preflight, and automatic retry with backoff on transient failures.
- Native AWS Bedrock Converse support and Bedrock Mantle Responses API support.
- Azure OpenAI, Microsoft Foundry OpenAI/v1, and Microsoft Foundry Anthropic endpoint support.
- Local Ollama, vLLM, LM Studio, Phlox-GW, and other OpenAI-compatible endpoints.
- Three autonomy levels: `ask`, `workspace`, and `autopilot`, refined by ordered scoped permission rules (`allow`/`prompt`/`deny` on tool, path, command, host, or MCP server).
- Conservative static command analysis: commands that cannot be fully read (substitutions, `eval`, inline interpreters) always require interactive approval, in every mode.
- Workspace containment, race-resistant rooted file mutation, hard-link-safe atomic replacement, symlink escape checks, hard command denials, timeouts, output limits, and process-group termination of every command's descendants.
- OS sandbox enforcement: Seatbelt write/network containment on macOS, Landlock filesystem plus kernel-dependent TCP/UDP containment on Linux, both with `auto` and fail-closed `require` modes.
- Repository trust: when a project `.collomia.json` exists, that configuration and the project's MCP servers, skills, and instructions are quarantined until approved with `collo trust`.
- Publishing, deploying, and pushing are their own decision (`permissions.publication`, default `prompt`): package and image registries, pull requests and releases, infrastructure applies, Git remotes, and commands run on another host are never approved by autonomy mode or a tool-wide grant alone. A rule naming the operation — `{"command": "npm publish"}` — is the deliberate written-down exception.
- Persistent audit ledger of every permission decision and execution outcome, stored outside the workspace, attributed to the session and agent that acted, and readable with `collo audit`. A ledger write that fails is reported and declared in the file as a gap rather than leaving a hole that reads as a complete record.
- Layered, schema-versioned configuration (defaults → user → project → environment) with `collo config validate` and `collo config show`.
- Diagnostics: `collo doctor`, redacted `--debug` logging, a privacy-conscious `collo support bundle`, and a maintained [capability matrix](docs/CAPABILITIES.md).
- Schema-versioned JSONL event stream for automation (`collo run --jsonl`), an embedded JSON Schema, stable exit codes, explicit refusal/partial-completion metadata, durable `--resume`/`--continue`, session-free `--ephemeral` runs, and side-effect-free offline trace validation/replay.
- Full-lifecycle skills: `SKILL.md` manifests with YAML front matter plus bundled `scripts/`, `references/`, and `assets/`, project and global scopes with deterministic precedence, on-demand loading, and `collo skills` management — plus hierarchical `AGENTS.md`/`COLLOMIA.md` instructions (user-level, then project).
- MCP `stdio` and Streamable HTTP clients using the official Go SDK, with explicit external-data provenance framing for model-visible server output.
- **Governed multi-agent delegation**: the `delegate` tool queues up to six sub-agent tasks through one session-wide scheduler (four active by default, with optional provider limits). Read-only tasks share the workspace; write-capable tasks get isolated Git worktrees and declare repository-relative write scopes so disjoint writers can run concurrently while overlapping or unspecified writers serialize. Named profiles restrict tools, skills, permissions, tokens, iterations, and time; structured outcomes carry evidence, usage, changes, scope violations, verification, and conflicts. `alt+a` inspects, steers, or stops one child without cancelling its siblings or parent. Manual integration remains the default; freshness-bound three-way review preserves clean parent edits and delegated edits while overlapping hunks stay explicitly unresolved. Opt-in `options.agent_integration: "reviewed"` exposes the same guarded decisions to the primary.
- **Background processes**: `start_process`/`list_processes`/`process_output`/`stop_process` run dev servers, watchers, and long test runs without blocking the turn, with the same safety analysis as `run_command`; `/ps` manages them from the TUI, and everything is stopped at session exit.
- **Code intelligence**: `search_symbols` queries an incremental, ignore-aware definition index (Go, Python, JS/TS, Rust); `diagnostics`, `find_definition`, `find_references`, and `format_file` drive a real language server (gopls, pyright, typescript-language-server, rust-analyzer) for exact-position findings, type-aware navigation, and the project's own formatting.
- **Built-in web access**: `web_search` (DuckDuckGo, no API key and nothing to configure) and `web_fetch`, which reduces a page to readable text, markdown with resolved link targets, or raw bytes. Both reach the **public internet only** — loopback, private, link-local (cloud metadata), and reserved addresses are refused on the resolved IP at connect time, with no setting to turn that off — and a redirect off the requested site is reported rather than followed. Both carry external risk, declare the hosts they contact so a `host` rule or session grant can cover them, and arrive as framed external data.
- **Verification loop**: `detect_verification` finds the project's real build/lint/test commands from its own files (`go.mod`, `package.json`, `Cargo.toml`, …); `collo verify`/`/verify` runs them and ties outcomes to the plan.
- Read-only planning mode with a structured, persisted plan artifact (`update_plan`, `/tasks`).
- Durable sessions: crash-safe persistence, complete transcript/tool restoration on `--resume`/`--continue`, forking, non-destructive turn rewind, live in-TUI switching (`/sessions` or `alt+s`) with in-process per-session drafts, prompt history, pinned plan state, referenced oversized results, and automatic context compaction.
- Atomic multi-file patching (`apply_patch`), session-wide diff review (`/diff`), checkpointed undo (`/undo`), colorized diff previews at approval, and **hunk-level approval** — accept or reject individual hunks of a `write_file` change before it lands.
- Git inspection tools (`git_status`, `git_diff`, `git_log`, `git_blame`) that are read-only, bounded, and the only Git tools available in planning mode.
- Git writes under approval: `git_commit` commits **exactly the files you name and nothing else** — the user's unrelated edits, their own staged work, untracked scratch files and build output are all left alone. Because it declares those paths, the approval prompt previews the real diff and `protect_credentials` catches a `.env` or a private key entering history, which `run_command "git commit -a"` cannot do because it names no path. `git_branch` creates a branch at HEAD without touching the working tree. **Neither pushes** — publication stays with `run_command` under `permissions.publication`.
- `run_command` supports a pseudo-terminal (`pty: true`) on every platform — a Unix PTY, or a Windows pseudoconsole — for interactive-only or isatty-dependent programs.
- The agent can pause and ask you a typed question (`ask_user`) instead of guessing.
- Command output streams into the transcript live, for both foreground and background commands.
- Interactive and non-interactive operation from the same binary.
- Local browser terminal (`collo --web`) that runs the existing TUI in a real PTY and serves an embedded, authenticated xterm.js client on loopback.

## Build and run

Collomia requires Go 1.26.5 or later to build. The patch-level minimum keeps
release binaries on a standard-library security-fixed Go toolchain. The
resulting executable has no Go runtime dependency.

```sh
go build -o collo ./cmd/collo
./collo --version
./collo
```

The quickest way to get a working configuration is to let Collomia find one and prove it works:

```sh
collo setup
```

It looks for local runtimes that are actually running, notices provider API keys your environment already exports, offers the endpoint's own model catalog, and verifies your choice with two real requests — one plain completion and one carrying a tool definition — before writing anything. A model that answers ordinary prompts but rejects tools cannot drive a coding agent, and that is caught here rather than at your first prompt. Keys are never written into the configuration file.

Azure OpenAI, Azure AI Foundry, and AWS Bedrock are configured through a short form, since neither is discoverable from a name and a key; Bedrock additionally reports which identity the AWS credential chain resolved to. Run `collo setup` again at any time to add or reconfigure a provider — it shows what is already configured, marks anything it would replace, and asks before changing your default. `collo setup --provider <name>` skips the scan and re-verifies one provider you already have, leaving its credential untouched.

Setup also writes both token limits and says where each number came from — the endpoint that published it, a conservative table of documented limits, or an assumption it labels as one. Both fields fail silently when omitted: without `context_window` automatic compaction never runs and a long session ends at a provider context-length error, and without `max_tokens` every answer stops at 8192 tokens with no message. `collo doctor` warns about either, naming the consequence rather than the field.

If the provider's environment variable is already exported, setup uses it and never asks for a key — the recommended route for a long credential, since the value never passes through an input field:

```sh
export AWS_BEARER_TOKEN_BEDROCK='<your key>'
collo setup
```

The [user guide](docs/USER_GUIDE.md#credentials-and-skipping-the-key-prompt-entirely) lists the variable for each provider.

The manual path remains fully supported and is what scripted installs should use. The default configuration connects to Ollama at `http://127.0.0.1:11434/v1` and selects `qwen3-coder`:

```sh
ollama pull qwen3-coder
collo init --global --with-reference
```

macOS and Linux can install the latest stable release without `sudo`:

```sh
curl --proto '=https' --tlsv1.2 -fsSL \
  https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.sh | sh
```

The installer writes to `$HOME/.local/bin` by default, verifies the release
checksum, and replaces an existing binary only after the new one passes its
version check. Set `COLLO_INSTALL_DIR` or `COLLO_VERSION` to override the
destination or pin a release.

Windows 11 (AMD64 or ARM64) uses the checksum-verifying PowerShell installer:

```powershell
irm https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.ps1 | iex
```

This installs to `%LocalAppData%\Programs\Collomia`, adds that directory to the
current user's PATH, and needs neither elevation nor an execution-policy
change: the script is never written to disk as a `.ps1`, so `Restricted` and
`AllSigned` policies do not block it. Open a new terminal afterwards to pick up
the PATH change, or pass `-NoPathUpdate` to leave PATH alone. To review the
script before running it, see
[Installing Collomia](docs/INSTALLING.md#native-windows-with-powershell).

Each release also publishes a CycloneDX SBOM and GitHub/Sigstore provenance
attestations. See [Installing Collomia](docs/INSTALLING.md) for direct binary
downloads, version pinning, stronger `gh attestation verify` checks, upgrade,
rollback, and unsigned macOS/Windows binary guidance.

## Configuration

Collomia supports both user-wide defaults and per-project overrides. You do not need both: use only the global configuration for the same behavior everywhere, only a project configuration for a self-contained workspace, or layer them when a project needs to differ from your normal setup.

### Global and project configuration

`collo init --global` creates the global user configuration in the user's home directory:

| Platform | Global configuration path |
| --- | --- |
| macOS and Linux | `~/.collomia/config.json` |
| Windows | `%USERPROFILE%\.collomia\config.json` |

The global file applies to every workspace for that user. The same `.collomia` directory is the single root for every persistent user-level Collomia file: the generated `config.example.jsonc` reference, optional `AGENTS.md`/`COLLOMIA.md` instructions, skills, sessions, logs, support bundles, audit ledgers, repository trust decisions, and MCP server pins. Collomia does not search additional platform configuration or cache directories.

It is a good place for personal provider definitions, preferred models, permissions, and user-wide options. Collomia now uses the compatibility-first OS sandbox mode `auto` by default: command writes/processes are contained when the platform backend is available, while command networking and broad reads remain enabled. Sandboxed commands receive the minimal environment unless you explicitly select `command_env: "full"`. Package managers and developer tools may need a narrow readable dependency root, writable cache, or environment-provided registry credentials, as described under [Permissions and safety](#permissions-and-safety). Set `sandbox_allow_network` to `false` when you intentionally want sandboxed commands offline, `sandbox_allow_read_outside_workspace` to `false` for OS-enforced user-data read confinement, or `sandbox: "off"` only as an explicit compatibility escape hatch. Containment settings in this global file are yours alone: a repository's `.collomia.json` can tighten them but never weaken them, and any attempt is reported rather than applied. Store API keys in environment variables and refer to them with `api_key_env`; avoid putting secret values directly in either configuration file.

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
    "sandbox": "auto",
    "sandbox_allow_network": true,
    "sandbox_allow_read_outside_workspace": true
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

Because an active file cannot carry comments, the documentation reaches it through an editor schema instead. `collo schema config` generates a JSON Schema 2020-12 contract from the configuration structs themselves, and `collo init` and `collo setup` write it beside the file they create as `collomia.schema.json` with a `$schema` key pointing at it. Any editor speaking the JSON language server then offers completion, per-field documentation on hover, the valid values for every enumerated setting, and an inline error on a misspelled field — while you type, rather than at the next launch. Field names and types are read from the structs and enumerated values from the validator's own vocabularies, so the schema cannot recommend a value the loader rejects. It is a sibling file rather than a hosted URL because it describes the fields *your* build understands; `collo doctor` reports a reference that is missing or that was generated by a different build.

Inside a session, `/config` shows what the layers actually resolved to — the effective value of each safety setting, the layer that set it, and anything a project asked to weaken and did not get. `/config all` extends that to every setting, including the ones no file mentions, which is the case reading a configuration file cannot answer. Values that can hold a credential are redacted by position rather than by pattern, because the merged configuration holds keys resolved from the environment and the OS credential store that appear in no file on disk.

## Usage

```text
collo [flags] [initial prompt]      start the interactive TUI
collo --web [flags] [initial prompt]  open the interactive TUI in a local browser
collo run [flags] <prompt>          run once, or read a prompt from stdin
collo setup                         find, verify, and configure a provider interactively
collo init [--with-reference]       create project .collomia.json
collo init --global [--with-reference]  create ~/.collomia/config.json
collo config validate [--strict]    validate configuration with field-level errors
collo config show                   print the effective configuration and its layers
collo config reference              print every configuration option with annotations
collo schema config                 print the JSON Schema for the configuration file, for editors
collo trust [--status|--revoke]     review and trust this workspace's project config
collo doctor [--strict]             diagnose config, terminal, git, providers, MCP, sandbox
collo capabilities [--markdown]     print the product capability matrix
collo support bundle [--output path] [--include-logs]  create a privacy-conscious diagnostic ZIP
collo policy check <command…>       evaluate a command against permission rules, without running it
collo auth [list|status|set|rm|import]  optionally keep provider API keys in the OS credential manager (macOS/Windows)
collo audit [show|path]             read this workspace's ledger of permission decisions and outcomes
collo review [ref] [instructions…]  review pending changes ('-' = uncommitted) with optional focus, headlessly
collo verify [focus]                detect and run this project's build/lint/test commands, headlessly
collo sessions [list|show|fork|rewind|rename|archive|unarchive|delete]  manage saved sessions
collo completion bash|zsh|fish|powershell  generate shell completion
collo schema events                 print the embedded JSON Schema for JSONL events
collo replay [--check] <trace|->    validate and safely render a completed JSONL run trace
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
--alt-screen                         force the alternate-screen TUI
--no-alt-screen                      retain the final TUI frame in scrollback
--jsonl                              (run) emit schema-versioned JSONL events on stdout
--ephemeral                          (run) skip durable conversation/session storage
--check                              (replay) validate and print only a compact summary
--output <path>                      (support bundle) choose the archive path
--include-logs                       (support bundle) include bounded, redacted recent debug logs
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
collo run --jsonl --ephemeral --plan "Inspect this checkout without saving a conversation" | jq .
```

With `--jsonl`, every event is one schema-versioned JSON line (secrets redacted), and an actual run always ends with a `run.result` record after successful argument parsing — including prompt, configuration, provider, permission, timeout, and cancellation failures (`--help`/`--version` remain informational commands). Provider streams use `text.delta`, `reasoning.delta`, `tool.call.delta` (id/name plus incremental `arguments_delta`, completed by `done`), `usage`, and `warning`; a tool is never executed from those fragments, only from the provider's complete validated call. Provider failures add a classified `provider` object to the preceding `error` event and the final result. Failed/cancelled runs also carry one opaque per-occurrence `failure_id`, repeated as `result.failure.id`, so the TUI, JSONL, debug log, and support bundle can be correlated without encoding user data in the identifier. Print the exact contract with `collo schema events`; see the [automation guide](docs/AUTOMATION.md) for all fields, failure kinds, and pipeline patterns.

```json
{"schema":1,"kind":"run.result","result":{"status":"ok","answer":"…","session_id":"a1b2c3","changed_files":["main.go"],"duration_ms":8412},"usage":{"input_tokens":5210,"output_tokens":644}}
```

`status` remains `ok`, `error`, or `cancelled` for schema-v1 compatibility. Optional `failure`, `partial`, and `refused` fields make non-success and denied-action outcomes explicit. Exit codes are `0` success, `1` runtime/provider failure, `2` usage or configuration failure, and `130` cancellation. Pull just the verdict with `... --jsonl | tail -1 | jq .result`.

Validate a retained stream, or render a readable offline transcript without
loading configuration, contacting a provider, opening a session, or executing
tools:

```sh
collo replay --check run.jsonl
collo replay run.jsonl
cat run.jsonl | collo replay -
```

Replay requires one complete headless stream ending in `run.result`; it is not
for session-store or audit-ledger JSONL. It checks known schema-v1 payloads and
lifecycle consistency, tolerates additive fields, strips terminal controls,
and applies best-effort common-secret redaction while rendering. See the
[automation guide](docs/AUTOMATION.md#validating-and-replaying-saved-traces)
for its exact safety and compatibility boundary.

### Support bundles

Create a local diagnostic archive when `doctor` is not enough or when opening
an issue:

```sh
collo support bundle
collo support bundle --output ./collomia-support.zip
collo support bundle --include-logs
```

The default archive is written under `~/.collomia/support/` on macOS/Linux or
`%USERPROFILE%\.collomia\support\` on Windows. Collection is local and
read-only: it does not initialize providers or MCP servers and makes no network
requests. Default collection also leaves environment-backed provider and MCP
values unresolved. The archive contains an anonymous health manifest and the
generated capability matrix. The manifest may include up to eight opaque
recent failure IDs collected from bounded log tails, but no associated log
messages or attributes. It excludes configuration values, environment
variables, provider endpoints/models, MCP definitions, workspace paths, source
files, prompts, transcripts, sessions, audit records, and debug logs.

`--include-logs` is deliberately explicit. It adds at most five recent logs,
bounds both individual and total size, redacts configured/common credential
values (resolving configured secret references locally for that purpose), and
replaces home/workspace paths. Redaction is defense in depth, so
always inspect the ZIP before sharing it. Existing output files are never
overwritten.

## Browser terminal

`--web` exposes the normal Collomia TUI through a local browser without
creating a second frontend or agent protocol:

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
the server. It has no TLS or remote-user authentication. See
[the browser-terminal security boundary](docs/SECURITY.md#browser-terminal-boundary)
for the complete limitations.

## Slash commands

Inside the TUI:

| Command | Purpose |
| --- | --- |
| `/status` | Show workspace, provider, model, effective capabilities, plan, autonomy, and config status. |
| `/model [provider/model]` | Show or switch the active provider and model (opens a fuzzy picker with no argument). |
| `/agent [name]` | Show or switch a named primary-agent profile; `default` restores the ordinary primary. |
| `/models` | Show each provider's default model, effective capabilities, endpoint constraints, and live catalog availability when the adapter supports discovery. |
| `/context` | Break down exactly what the model sees plus token usage and user-configured cost estimates/budgets. |
| `/plan [on\|off]` | Toggle read-only planning mode. |
| `/tasks` | Show the structured task plan the agent maintains. |
| `/diff` | Open responsive unified/side-by-side review with file/hunk navigation and folding. |
| `/transcript` | Search and copy the complete raw TUI transcript. |
| `/activity` | Search and category-filter the bounded session activity timeline; copy a selected failure ID with `y`. |
| `/undo` | Revert the agent's most recent file change (refuses to clobber files you edited outside the agent). |
| `/review [ref] [instructions…]` | Read-only code review of uncommitted changes (`-` or no ref) or changes vs any ref; extra words become custom reviewer instructions. |
| `/verify [focus]` | Detect and run the project's real build/lint/test commands, tying each outcome to a plan step. |
| `/ps` | List background processes started this session; `/ps stop <id>` stops one. |
| `/sessions` | Fuzzy-pick a saved session and resume it in place — transcript, plan, and persistence all move over. |
| `/rewind [turn]` | Create and switch to a new conversation branch after an earlier completed turn; the source session and workspace stay unchanged. |
| `/restore [turn]` | Branch the conversation *and* reverse the agent's tracked file changes back to an earlier completed turn; refuses outright, naming the files, if any of them changed outside Collomia. |
| `/retry` | Load the previous prompt into the composer for review; it never sends or repeats tools automatically. |
| `/new` | Start a fresh session; the current one stays saved. |
| `/compact [focus]` | Summarize older context to free the model window. |
| `/autonomy <mode>` | Switch among `ask`, `workspace`, and `autopilot`. |
| `/theme [name]` | List color themes or switch to one (fuzzy picker with no argument). |
| `/skills [list]` | Fuzzy-pick a skill to use — choosing one pre-fills the prompt — or `list` to print them. |
| `/agents [stop\|steer\|verify\|compare\|apply …]` | Inspect delegated tasks, stop or guide one, verify its retained worktree, compare candidates, or review/apply selected text hunks. |
| `/prompt [workspace-file]` | Load a UTF-8 text file into the composer for review; with no path, open a fuzzy file picker. |
| `/attach [workspace-image]` | Attach a bounded PNG, JPEG, GIF, or WebP to the pending prompt; with no path, open an image picker. |
| `/attachments` | List images attached to the pending prompt. |
| `/detach <number\|all>` | Remove one or every pending image before sending. |
| `/mcp [subcommand]` | Browse MCP servers with a fuzzy picker, or manage them at runtime: `list`/`status` (health, identity, negotiated capabilities), `ping`, `reconnect`, `enable`/`disable`, `add`, `remove`. |
| `/tools` | List the complete tool surface. |
| `/config [all]` | Show what the configuration resolved to: layers in order, the effective safety stance, anything the containment clamp refused, and the layer that set each value. `all` lists every setting, including those no file mentions. Credential values are redacted. |
| `/clear` | Clear active conversation context without resetting durable token/cost accounting. |
| `/help` | Show command help and keybindings. |

Informational commands (`/status`, `/context`, `/ps`, `/tasks`, `/models`, `/tools`, `/skills list`, `/mcp list`, `/config`, `/help`) render their output in a titled, theme-colored panel — the command's subject sits in the box border, body text is tinted with a readable shade derived from the theme (not the terminal's raw default color), and content wraps cleanly to the terminal width. Quick acknowledgements ("Theme switched…") stay as subtle one-line notes.

Typing `/` opens a command palette that filters as you type and completes argument values (`/theme dra…`, `/autonomy …`, `/model …`, `/agent …`): ↑/↓ selects, `tab` completes, `enter` runs, `esc` dismisses. Typing `@` opens a fuzzy workspace file/folder picker and safely quotes paths containing spaces. `/prompt` opens a text-file picker; `/prompt "docs/review prompt.md"` loads a named file into the composer. `/attach` similarly opens an image picker, and `/attach ` accepts quoted, escaped, `file://`, or terminal-dropped workspace paths. The status bar shows the number of pending images; `/attachments` reviews them and `/detach` removes them before send. These fuzzy menus keep their compact position beside the composer. Approvals, hunk review, and questions use centered floating dialogs instead: they preserve the surrounding transcript, take keyboard focus while active, match the selected theme, and disappear as soon as the action is resolved.

`ctrl+t` cycles the Chat, Session, and Help tabs, `ctrl+o` expands or collapses finished tool output, `ctrl+y` opens transcript search/copy, `ctrl+d` opens the session diff browser, `alt+s` opens saved sessions without replacing a draft, and `alt+a` opens inspect/steer/stop actions for an active child. `/activity` opens a full-screen, searchable event-derived timeline; `f` cycles the categories present, `n`/`N` walks search matches, and `y` copies the selected failure ID when one exists. While a turn runs, the composer accepts a deliberately small local-command lane (`/help`, `/status`, `/context`, `/tasks`, `/tools`, `/config`, `/attachments`, `/transcript`, `/activity`, `/diff`, read-only `/ps`, and `/agents` inspect/steer/stop). Ordinary text and unavailable commands remain unsent drafts until the turn ends; a child question temporarily preserves that draft. At the first or last visual line of the composer, ↑/↓ walks earlier prompts and returns to the exact draft you were editing; within multiline or soft-wrapped input, the same keys continue to move the cursor normally. Page-up pauses live follow without moving the prompt cursor; `end` returns to the bottom and resumes it. These global keys can be remapped through `options.keybindings`, and Help always displays the effective values. The Session tab shows the live task plan, changed files, a parent/child agent tree with bounded recent output, running background processes, asynchronous Git branch/upstream/dirty state, provider/sandbox/MCP/trust health with recovery hints, and recent activity; press `r` there to refresh Git state. The status bar carries live task/agent/process badges. Fenced code in assistant messages is syntax-highlighted with the language named after the opening fence (for example, a fence labeled `go`). Expanded `read_file` results select a lexer from the filename, and `git_diff` results receive diff highlighting, so source remains readable in the normal Chat transcript as well as in approval previews. Syntax colors follow the active theme; `plain`/`NO_COLOR` disables them. Use `--no-alt-screen` when you prefer native terminal scrollback.

Animations remain enabled by default. Users who prefer a static progress marker can set `"reduced_motion": true` under `options`; this changes only decorative motion and never disables composer input, slash commands, cancellation, or agent controls. A dialog also dims the screen behind it so the decision in front of you is plainly the focused element; set `"dim_background": false` under `options` to keep the transcript at full colour, which is usually what a documentation screenshot wants. The cleared gutter framing a dialog is kept either way.

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

In the full-screen `/diff` viewer, press `e` to open the current file at the
selected hunk in an external editor. Configure a direct command and argument
list—Collomia never inserts a shell—or use a simple `VISUAL`/`EDITOR` value:

```json
{
  "options": {
    "editor": {
      "command": "code",
      "args": ["--wait", "--goto", "{file}:{line}:{column}"]
    }
  }
}
```

The supported placeholders are `{file}`, `{line}`, and `{column}`. If
`{file}` is omitted, Collomia appends it. Only files contained by the active
workspace can be opened, and the diff is refreshed when the editor process
returns.

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

On macOS and Windows, `collo auth set <provider>` can instead keep an API key in the OS credential manager (Keychain or Credential Manager), prompting for it without echo and never placing it in an argument or shell history. `collo auth list|status|rm|import` manage the entries, and nothing prints a stored value back. It is optional and additive: the store is consulted only after `api_key`, `api_key_env`, and a provider's own variable such as `AWS_BEARER_TOKEN_BEDROCK`, so an exported environment variable always wins and a machine that has never run `collo auth set` never touches the credential manager. Linux has no backend — headless hosts use `api_key_env` — and there is no encrypted-file fallback by design. Azure `auth: entra` and Bedrock `auth: sigv4` store nothing, because their credentials are short-lived tokens and the AWS chain respectively. See the [user guide](docs/USER_GUIDE.md#optional-keep-api-keys-in-the-os-credential-manager) for the full precedence table and platform caveats.

Collomia keeps an effective capability declaration for every provider/model selection. It distinguishes `supported`, `partial`, `unsupported`, and `unknown` for tool calling, streaming, reasoning, images, structured output, token usage, prompt caching, parallel tool calls, and model discovery; it also carries the configured context window and adapter-specific constraints. `/status`, `/model`, and `/models` expose this information. `/models` probes supported catalog endpoints concurrently and reports providers without a catalog as **unverified**, not incorrectly **unavailable**. Catalogs such as OpenAI-compatible `GET /models` often return names without feature metadata, so Collomia reports model-dependent facts as unknown instead of inferring capabilities from a model name.

On the Anthropic Messages routes Collomia asks the provider to cache the parts of a request that do not change — the tool definitions and system prompt, plus the conversation so far — because a turn with ten tool calls is eleven requests over the same growing prefix. Nothing needs configuring, an endpoint that rejects cache breakpoints is retried once without them and not asked again, and `/context` reports whether the cache is unsupported, not yet warm, or being read. Token counts always include cached tokens, so `input tokens` means the whole prompt whatever split a provider reports.

The declaration describes what Collomia's current adapter can send and consume, which may be smaller than the vendor API's complete feature set. Built-in OpenAI/compatible, Anthropic/compatible, Azure OpenAI/Foundry, Bedrock ConverseStream, and Responses/Mantle adapters can encode typed user images. Because image support is model- and endpoint-dependent, these adapters report it as `partial` and let the selected service make the final determination. Before any provider request, Collomia rejects a known contradiction (for example, sending images or tools through an adapter declared not to support them, or configuring a maximum output larger than the context window). Unknown and partial capabilities remain visible but do not cause speculative failures.

Streaming adapters normalize upstream events before they reach the agent: text, provider-supplied reasoning text/summaries, incremental tool-call arguments, complete usage snapshots, warnings, and classified errors all follow one contract. Tool arguments may be incomplete JSON while they stream; Collomia assembles and validates the final document before approval or execution. An HTTP failure before streaming starts can use the normal retry policy. An error carried inside a stream is returned without replaying the request, preventing duplicate deltas and surprise repeat billing.

All built-in HTTP adapters use the same resilience policy. Network failures and HTTP 408, 429, 5xx, and 529 responses are retried up to three attempts with bounded exponential backoff, jitter, and `Retry-After`; authentication, permission, not-found, and other ordinary 4xx failures are not retried. A request is retried only when its body can be replayed. Failures are classified as `authentication`, `permission`, `rate_limit`, `invalid_request`, `not_found`, `timeout`, `unavailable`, `protocol`, `cancelled`, or `unknown`; status, retry timing, and request IDs are included when the provider supplies them. In `--jsonl` mode the same fields appear under `provider` on the `error` event.

OpenAI-protocol models do not all accept the same Chat Completions fields. Collomia sends the backward-compatible `max_tokens` field first. Only when an upstream HTTP 400 explicitly rejects that field and directs the caller to `max_completion_tokens` does Collomia rebuild and resend the request; a similarly explicit rejection of a configured `temperature` retries with the provider default and emits a warning. Those choices are remembered for the active provider/model client, so later turns avoid the failed probe. Successful requests, unrelated 400 responses, and providers that accept the original fields are never rewritten. The configured `max_tokens` remains Collomia's provider-neutral output budget; for reasoning models the upstream `max_completion_tokens` interpretation includes hidden reasoning tokens as well as visible output.

The same mechanism covers a `max_tokens` larger than the model will accept, on both the OpenAI and Anthropic routes. No catalog publishes a model's output ceiling, so the configured value is often written from documentation or from memory; when a 400 states the ceiling, Collomia retries that request under it, warns with both numbers, and remembers the ceiling for the active model. The rejection is matched on the phrasing that carries the number rather than on the digits in the message, so a rejection naming `claude-sonnet-4-5-20250929` cannot be read as a ceiling of four tokens; where no ceiling is recognized the provider's own error surfaces unchanged.

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
    "sandbox_allow_read_outside_workspace": false,
    "sandbox_readable_roots": ["${HOME}/go/pkg/mod"],
    "command_env": "minimal",
    "publication": "prompt",
    "rules": [
      { "action": "allow", "command": "npm install", "reason": "dependency installs are routine" },
      { "action": "deny", "command": "npm publish", "reason": "releases go through CI" }
    ]
  }
}
```

`allowed_tools` is a persistent explicit grant. Interactive approval with `a` normally grants a tool for the remainder of the current process. The example `denied_commands` entry adds all direct recursive `rm` invocations to Collomia's mandatory protections. Global and trusted project patterns only add to the effective set; no subordinate configuration can remove an inherited command denial.

**Publishing is its own decision.** The paragraph below describes a taxonomy of destruction, and until recently that was the whole risk model: `terraform destroy`, `kubectl delete`, `helm uninstall`, and `git push --force` each required a fresh approval even under autopilot, while `terraform apply`, `kubectl apply`, `helm upgrade`, `npm publish`, `docker push`, `gh pr create`, and `git push` were approved silently. `permissions.publication` (`off`, `prompt` by default, `deny`) closes that asymmetry: an action that puts something outside this machine — a package version, a container image, a pull request or release, an infrastructure apply, a push to a remote, a command run over `ssh` — is not covered by autopilot, by a tool-wide "always allow", or by an `allow` rule naming only the executable. A rule naming the *operation* is: `{"action": "allow", "command": "npm publish"}`. Read verbs (`gh pr view`, `kubectl get`, `terraform plan`, `aws s3 ls`) and rehearsals (`--dry-run`) are unaffected, as is `npm install`. See [Publishing outside this machine](docs/USER_GUIDE.md#publishing-outside-this-machine).

**Rules can name an operation, not just an executable.** A `command` pattern containing a space matches the executable plus the words that decide what it does — `npm publish`, `git push`, `gh pr create`, `ssh build-host` — so a policy can allow installing dependencies while gating releases. `collo policy check '<command>'` prints the exact operation string a command produces, and a pattern that could match neither an executable nor an operation is now rejected by `collo config validate` rather than sitting in the file looking like protection.

Shell safety has three outcomes. Routine scoped operations such as `rm -rf node_modules`, `rm -rf /tmp/example`, and formatting a workspace disk-image file follow the selected autonomy mode. Destructive but legitimate operations—such as `git reset --hard`, machine shutdown, bulk cloud/IaC deletion, or a recursive target that cannot be resolved statically—require a fresh one-time approval; allow rules, autopilot, and “always allow” cannot skip it. Catastrophic outcomes—such as recursively deleting `/`, the home or workspace root, `.git`, `~/.collomia`, Windows drive/system roots, or writing a physical disk—are refused and cannot be approved. Both structural checks and regex denials are repeated immediately before foreground or background execution. Use `collo policy check '<command>'` to inspect the result without running it. See the [security model](docs/SECURITY.md#command-safety-tiers) for the complete categories and limitations.

Path tools canonicalize paths and existing symlinks before checking containment. Outside access requires both `allow_outside_workspace: true` and an applicable permission decision. Tool output, file reads, and commands are size- and time-bounded, and every command runs in its own process group so cancellation and timeouts kill all descendants — including detached [background processes](#background-processes), which are additionally stopped at session exit regardless of how they were started.

**What these checks are — and are not.** Approval prompts, rules, and denial patterns are in-process policy checks, not an operating-system security boundary. The default `auto` sandbox adds that boundary when the platform backend is available; an explicit `off` setting or an unavailable backend under `auto` leaves an approved command running with normal user privileges after a visible warning. Shell commands are statically analyzed before approval; commands whose effect cannot be determined (substitutions, `eval`, inline interpreter payloads) always require interactive approval, in every mode.

`"permissions": {"sandbox": "auto"}` is the default and enables real OS enforcement when available; `"require"` fails closed instead of degrading, while `"off"` is an explicit compatibility escape hatch that only your global configuration may select. `sandbox_allow_network` and `sandbox_allow_read_outside_workspace` both default to `true`, preserving package downloads, online CLIs, and broad dependency reads. Write confinement still means an external build/package cache may need a narrow `sandbox_writable_roots` grant, and the implicit minimal command environment may require an intentional `command_env: "full"` override for proxy, registry, compiler, or cloud variables. Set either `sandbox_allow_*` switch to `false` when you deliberately want that additional boundary enforced. An existing *global* file that explicitly contains `"sandbox": "off"` remains off, and Collomia never silently rewrites it. A project file that asks for `"off"` is refused and reported, keeping the inherited mode.

- **macOS**: Seatbelt (`sandbox-exec`) confines file writes. With `sandbox_allow_read_outside_workspace: false`, it denies file-content reads in user homes and mounted data volumes except for the workspace, PATH entries, temporary paths, and explicit roots; metadata remains visible so path lookup fails cleanly. Network egress is denied unless `sandbox_allow_network` is set.
- **Linux**: Landlock applies filesystem rules on kernel 5.13+/ABI v1; ABI v3 (Linux 6.2) is recommended because ABI v1–v2 cannot deny standalone truncation. The read switch adds a deny-by-default user-data read ruleset with explicit workspace/system-runtime/PATH/readable-root grants. ABI v4+ denies TCP connect/bind when command networking is off; ABI v10+ also denies UDP bind/connect/send, including DNS.
- **Windows 11**: the built-in AppContainer security boundary always confines user-data reads as well as filesystem/registry/credential/process access, with a Job Object owning the descendant tree. The workspace, temp directory, `sandbox_readable_roots`, and `sandbox_writable_roots` are granted to a workspace-specific container. No Hyper-V feature, administrator setup, driver, service, or separate installation is required.

Both switches apply only to sandboxed `run_command`, PTY, and background processes. Collomia's own provider HTTP, remote MCP connections, hooks, language servers, configuration, and session storage remain outside this command sandbox. A writable root is implicitly readable. If a build only needs to consume an external SDK, dependency store, or source tree, grant it with `sandbox_readable_roots`; use `sandbox_writable_roots` only for a cache or output location that must change. Relative roots resolve from the workspace, environment references expand at runtime, and narrow entries are safer than granting the whole home directory. If a tool needs proxy variables or registry credentials, use `command_env: "full"` deliberately; sandboxed commands otherwise receive the minimal environment by default.

`auto` applies every protection the platform has and prints a warning when a requested capability is missing. `require` refuses the command instead. On Linux ABI v4–v9, `require` plus `sandbox_allow_network: false` fails closed because only TCP can be denied; ABI v10+ satisfies full TCP/UDP denial. Use `auto` on older kernels to accept the prominently reported TCP-only boundary. On Windows, AppContainer always confines user-data reads and cannot reach an ordinary unpackaged localhost service; use a narrow alternative workflow or explicitly select `off` only for a command workflow that truly requires that compatibility. `collo doctor` and `/status` show the backend, effective command-read and network settings, and any missing protection. Linux users should follow the dedicated [Linux sandbox and Landlock setup guide](docs/LINUX_SANDBOX.md) for kernel/ABI requirements, Ubuntu 26.04 behavior, verification commands, container/WSL notes, and troubleshooting.

**Per-host egress on macOS.** `sandbox_allow_network` is all-or-nothing, which makes it the first thing people turn off when a build needs a package registry. `"sandbox_egress": "scoped"` narrows it: the OS sandbox denies direct remote traffic while leaving loopback reachable, and commands are routed through a Collomia-owned loopback broker that dials only the hosts named by `allow` rules with a `host` — the same rules the policy layer already matches, so there is no second allowlist to maintain. The broker never inspects or terminates TLS; an approved tunnel is spliced byte for byte.

This is enforcement on macOS only, and the reason is a limit of the other two platforms rather than missing work. Linux Landlock filters TCP by port and never by address, so allowing the broker's port would also allow every remote host on that port — an allowlist the adversary it targets can simply step around. Windows AppContainer blocks loopback to unpackaged local services, so a sandboxed command cannot reach the broker at all. On both, the setting is refused under `"sandbox": "require"` and degrades visibly under `"auto"`, leaving `sandbox_allow_network` in charge — which AppContainer in particular enforces more completely than either Unix backend, covering UDP and DNS. With `"sandbox": "off"` no broker is started anywhere: without OS-level denial a proxy is a convention any program can ignore, and presenting that as a boundary would be worse than the coarse control. No preset sets `sandbox_egress`.

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

A `host` matcher is compared against the endpoints an action's own text names — a `curl`/`wget` URL, an `ssh`/`scp` destination, a Git remote URL, the endpoint of an HTTP-transport MCP server — normalized to a bare lowercase hostname. Many network commands resolve their endpoint elsewhere (`git push origin`, `npm install`, `curl -K file`); those are reported as *undetermined*, and an `allow` rule never covers an undetermined endpoint, just as it never covers a command the analyzer could not read.

You do not have to compose these switches by hand. `permissions.preset` picks a named bundle — `frictionless`, `standard` (the default, identical to earlier releases), or `hardened` — and fills only the containment fields you did not set yourself, so `{"preset": "hardened", "sandbox": "auto"}` means hardened with `auto`. `collo config show` attributes every value to the preset that chose it. A preset can tighten an inherited layer but never loosen one, and no preset sets `mode`: autonomy stays a choice you make knowingly. `frictionless` is an explicit opt-out from OS containment for a toolchain that fights it — prompts, command-safety denials, and the audit ledger still apply.

The TUI's autonomy badge always carries the containment mark: `ASK ⛨` when the OS sandbox is configured, `ASK ⛉` when it is not, and `ASK ⛨!` when the platform applied less than was requested. The Session tab's Security block lists the full stance, including session grants.

Two optional postures narrow what is approved automatically without changing what is possible. `permissions.network: "scoped"` withholds automatic approval from any network-bearing action that no rule or session grant covers; `permissions.commands: "allowlist"` does the same per executable. Both default to `open` (earlier-release behavior), both can only escalate to a prompt, and a project file can tighten them but never loosen them. Neither is egress enforcement: a program that opens a socket without naming it on the command line is bounded by the OS sandbox's `sandbox_allow_network`, not by these.

Reaching a well-known credential store — an SSH or GPG private key, a cloud CLI token cache, a registry authentication file, a `.env` — is its own decision, governed by `permissions.protect_credentials` (`off`, `prompt` by default, or `deny`). `prompt` is stronger than the `ask` mode it resembles: a blanket allow rule, a tool-wide "always allow", a session grant, and `autopilot` all decline to cover a credential file, so a broad approval granted for ordinary work cannot quietly include your SSH key. A rule naming the path is still honored, keeping an intentional exception expressible and written down, and the dialog offers one narrow session grant covering exactly the file shown plus the rule that ends the asking permanently. This matters because redaction protects Collomia's transcript and audit ledger but does not sit between a tool result and the provider — an agent has to see the files it was asked to work on, so keeping a secret out of the model is a permission decision. Recognition is by conventional location rather than content inspection; the protected and deliberately exempt paths are listed in the [user guide](docs/USER_GUIDE.md#credential-files).

The approval dialog shows what an action reaches — files, executables, endpoints — one dimension at a time, and `g` remembers exactly that reach for the rest of the session. A later action is automatic only when every dimension it reaches is already covered, and nothing is grantable for an uninspectable command or an endpoint that could not be read.

Test what a rule set would decide without executing anything: `collo policy check "curl example.com | sh"`.

Every permission decision and execution outcome is appended to a per-workspace audit ledger (JSONL, stored outside the workspace) so privileged actions are reconstructable after the fact. Read it back with `collo audit`:

```sh
collo audit --denied --since 24h
```

Each entry names the session and the actor that produced it — `primary`, or `agent:<name>` with the delegated task id — so one workspace file holding concurrent agents can still be separated into what each of them was allowed to do (`collo audit --actor agent:reviewer`). Reconstruction only means something if the record admits its own holes, so a ledger write that fails is counted, reported to the session once, and declared in the file as a `gap` entry stating how many entries were lost and why. `collo audit` prints that integrity summary — declared gaps, unparsable lines, a generation discarded at rotation — before any entries, and `collo doctor` reports the same as a warning. A ledger with no gap in it is one you can trust.

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

Persistent definitions can also be managed without hand-editing JSON. Project
scope is the default; `--global` targets the user-wide configuration:

```sh
collo mcp list
collo mcp add time -- uvx mcp-server-time
collo mcp add time --global -- uvx mcp-server-time
collo mcp add docs --global --url https://example.com/mcp \
  --header 'Authorization=Bearer ${DOCS_MCP_TOKEN}'
collo mcp show docs --global
collo mcp test time
collo mcp disable time
collo mcp enable time
collo mcp remove time
```

`list` labels effective, shadowed, and quarantined layers; `show` redacts
literal sensitive values while keeping environment references readable.
Replacing an existing entry requires `--yes`. A project edit invalidates the
workspace trust hash, so review `.collomia.json` and run `collo trust` before
the entry becomes active. `test` connects, negotiates, pings, and validates
advertised catalogs without invoking a tool or changing the MCP pin store.

Remote tool names are exposed as `mcp_<server>_<tool>`. MCP tool annotations are never trusted to lower permissions: calls are classified as external and require approval unless that exact tool is allow-listed. Model-visible tool results, resources/catalogs, and prompt templates are framed as `EXTERNAL_MCP_DATA` with explicit server/type/subject provenance. The handling guidance tells the model to use relevant factual and structured data while refusing embedded instructions or claimed permissions; control characters are removed and server-authored schema prose is labeled external/descriptive. Trusting a server permits the connection—it does not give its returned text instructional authority. `/mcp` opens a picker of connected servers; choosing one lists its tools with descriptions.

Servers are also managed for the current TUI session without restarting:

```
/mcp status                 every server: health, transport, protocol, server identity,
                            capabilities, live/pending catalogs, tool count, errors
/mcp ping docs              health-check one server (a failure is recorded as an error state)
/mcp refresh docs           reload tools in place without reconnecting
/mcp reconnect docs         tear down and re-establish the session, refreshing its tool catalog
/mcp disable docs           close the server and withdraw its tools for this session
/mcp enable docs            bring it back (cannot override missing trust)
/mcp add time uvx mcp-server-time
/mcp add remote --url https://example.com/mcp
/mcp remove time            disconnect and forget (configured servers return next start)
```

Untrusted, disabled, and failed servers stay visible in `/mcp status` with their exact initialization errors instead of silently disappearing, so a misconfigured server is diagnosable from inside the session. Servers added with `/mcp add` are session-scoped and user-initiated (the trust gate quarantines *repository-supplied* configuration, not your own commands); use the command-line `collo mcp add` lifecycle to keep them.

For a quick functional test, install [uv](https://docs.astral.sh/uv/), run the
`/mcp add time …` command above, and ask: `Use the time MCP server to tell me
the current time in Japan.` The first launch may download the official
[`mcp-server-time`](https://github.com/modelcontextprotocol/servers/tree/main/src/time)
package. `/mcp status` should show `time` as connected and session-only with a
negotiated protocol revision and registered tools. This demonstrates an MCP
capability Collomia does not already provide; its native file tools are usually
preferable to adding a redundant filesystem MCP server.

Beyond tools, Collomia uses two more MCP capabilities when a server negotiates them:

- **Resources** — `/mcp resources <server>` browses what the server publishes (URI, type, size, description) and `/mcp resource <server> <uri>` previews one in the transcript. The agent has matching tools, `list_mcp_resources` and `read_mcp_resource`, classified as external calls scoped to the named server so permission rules keep matching.
- **Prompts** — `/mcp prompts <server>` lists a server's prompt templates with their arguments; `/mcp prompt <server> <name> key=value …` expands one and places the result in the input box, so you review and edit the template output before anything is sent to the model.

Tool results keep their typed content instead of being flattened: text and structured output come through as-is, embedded resources contribute their text, and resource links keep their URI along with a hint that `read_mcp_resource` can follow them. Images always retain an explicit `[image image/png, N bytes]` marker; when the active provider route supports typed tool-result images, Collomia also retains the bounded bytes in session attachment storage and supplies them to the next model turn. Anthropic Messages and Bedrock Converse support that rich tool-result path; OpenAI-compatible Chat Completions keeps the safe marker because tool-message image content is not portable across compatible gateways. Audio remains metadata-only.

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
    { "name": "add-retry-logic", "task": "Add exponential-backoff retry to the HTTP client.", "write": true, "write_paths": ["internal/provider/", "internal/provider/http_test.go"], "plan_step": 2 },
    { "name": "security-pass", "task": "Look for injection risks in the new endpoint.", "agent": "reviewer" }
  ]
})
```

- Up to **6 tasks per call**. A single FIFO scheduler shared by the session runs **4 concurrently by default**, so simultaneous `delegate` calls cannot each create their own four-task pool. `options.delegate_max_concurrency` changes the global bound and `options.delegate_provider_concurrency` can tighten it for a provider. Queue time counts against each task's timeout.
- **Read-only by default**: a task without `"write": true` shares the parent workspace and can only investigate — cheap, and safe to run alongside anything else.
- **Write-capable tasks are isolated**: `"write": true` gives that sub-agent its own `git worktree`, tool registry, permission manager, and audit ledger. A worktree with real changes is retained (path and branch reported back) for `/agents apply <id>`, manual review, or primary-reviewed integration; a clean one is removed. This requires a Git repository. Collomia never creates a merge commit, commits, or pushes.
- **Overlap-aware scheduling**: give writers a `write_paths` array containing repository-relative forward-slash file names or directory prefixes ending in `/`. Known-disjoint writers may run together. Exact, nested, case-folded, workspace-wide, or otherwise overlapping scopes serialize through the same FIFO queue. A writer with no `write_paths` is conservatively workspace-wide (`"*"`). `write_paths` is a scheduling/result contract, not an extra permission grant: ordinary workspace permissions still apply, and a child that changes an undeclared path is retained as an error with `scope_violations` and blocked from `/agents apply` and primary-reviewed integration.
- **Three-way conflict handling**: sibling file/hunk overlap is reported when a batch finishes. During integration, Collomia compares the recorded base, current parent, and retained child. Non-overlapping parent and child edits produce a selectable composed preview that preserves both sides. Overlapping edits show a bounded diff3 conflict preview and remain non-selectable. Nothing is silently overwritten or auto-ranked.
- **Plan association**: optional `"plan_step": 2` links a task and its evidence to an existing structured-plan step. Unknown steps and steps with unfinished dependencies are refused; association does not autonomously execute the plan.
- **Named agent profiles**: define reusable roles in configuration and select one per task with `"agent": "<name>"`:

  ```json
  {
    "agents": {
      "reviewer": {
        "model": "gpt-5.1-mini",
        "instructions": "You are a security reviewer. Focus only on injection, auth, and secrets handling.",
        "tools": ["read_file", "search_files", "search_symbols", "git_diff"],
        "skills": ["security-review"],
        "max_iterations": 12,
        "token_budget": 50000,
        "timeout_seconds": 600,
        "permissions": {
          "mode": "ask",
          "denied_tools": ["run_command"],
          "rules": [
            {"action": "deny", "server": "production-*", "reason": "review agents cannot call production MCP servers"}
          ]
        }
      }
    }
  }
  ```

  An empty `tools` or `skills` list inherits the parent's full visible set; a non-empty list is an allowlist. Profile permissions are one-way restrictions: their autonomy `mode` is intersected with the parent, denials accumulate, and profile rules may only `prompt` or `deny`. A child cannot enable outside-workspace access, network, a weaker sandbox, or an `allow` rule. The profile model remains on the parent's provider.
- **Budgets and results**: `token_budget` counts provider-reported input plus output tokens. Before each request, Collomia reserves the estimated next input and caps requested output to the remainder; it checks reported usage afterward. Providers that omit usage cannot provide an exact token guarantee, so iteration and time limits remain the hard fallback. The parent receives bounded JSON containing status, summary, error, evidence from completed tools, usage, changed files/hunks, and worktree/branch—not the raw child transcript. Oversized batches remain valid JSON, preserve every task's identity/status, and mark compacted entries `truncated` for follow-up in `/agents`.
- **Control and recovery**: the Session tab shows a parent/child tree with queued, running, waiting-for-approval, cancelling, completed, failed, timed-out, budget-exhausted, and interrupted states plus a bounded recent-output tail. `/agents steer <id> <guidance…>` queues guidance for the child's next provider boundary; it cannot alter an executing tool or answer an approval and explicitly grants no permissions. `/agents stop <id-or-name>` cancels one. `alt+a` exposes inspect/steer/stop actions while the parent runs. Lifecycle snapshots are stored in the parent session; resume restores outcomes but marks unfinished tasks `interrupted` and never restarts them or repeats tools.
- **Selective integration**: `/agents apply <id>` opens a floating file/hunk review for a completed write task. Collomia verifies the registered worktree, `collomia/*` branch, recorded base, declared scope, and bounded regular UTF-8 content. An unchanged parent shows the child diff directly; a changed parent receives the three-way treatment above. The review token binds base, parent, child, modes, composed result, and any conflict preview. After normal `integrate_delegate` permission, Collomia rechecks everything, publishes selected clean hunks with rooted atomic mutations and rollback, and records them in `/diff`/`/undo`. Symlinks, binary/oversized files, incompatible mode/add/delete changes, moved branches, scope violations, and overlapping hunks remain manual. The branch and worktree remain.
- **Optional primary-agent review, verification, comparison, and integration**: set `"agent_integration": "reviewed"` under `options` to expose four primary-only tools. `inspect_delegate_changes` returns bounded child evidence, exact selectable hunks, detected repository verification commands, and separate freshness tokens for child verification and parent publication. `verify_delegate_changes` runs exactly one detected command in the child worktree under the ordinary `run_command` permission, hook, sandbox, network, timeout, cancellation, and output policies; results are machine-observed and become stale if child source changes. `compare_delegate_changes` presents bounded conflict, verification, evidence, and budget facts for two to six candidates without picking a winner. `apply_delegate_changes` retains the existing `integrate_delegate` permission identity and guarded atomic publication path. `manual` remains the default, `/agents verify <id>` and `/agents compare <id> <id> [id…]` provide the operator equivalents, and a passing child suite neither grants permission nor proves the combined parent workspace.

  ```json
  {
    "options": {
      "agent_integration": "reviewed"
    }
  }
  ```

  After a write delegate finishes, use:

  ```text
  /agents verify <id>
  /agents compare <id-one> <id-two>
  /agents apply <id>
  ```

  Verification commands are derived from the retained worktree using the same
  detector as `collo verify`; Collomia does not invent a command. The suite
  stops at the first failed, blocked, cancelled, timed-out, or stale command,
  and each command receives its own policy decision. Agent details and the
  Session tree distinguish `passed`, `partial`, `failed`, `blocked`,
  `cancelled`, `unavailable`, and `stale` child verification. Verify the
  combined parent workspace after integration because parent-only changes and
  interactions between candidates are outside the child result's scope.

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

These tools give the agent real understanding of the codebase instead of guessing from text search:

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

- **`find_definition`** and **`find_references`** ask the same language servers where a symbol is defined and where it is used. They take a file, a 1-based line, and the symbol text as it appears on that line — the column is located for you, because the protocol measures columns in UTF-16 code units and asking a model to count them produces confident answers about the wrong token. Unlike the symbol index they follow imports, aliases, and types; unlike `search_files` they understand scope, so an unrelated same-named method in another type is not a match.
- **`format_file`** replaces one file with the language server's own formatting (`gofmt` through `gopls`). It is an ordinary write: same approval, tracked in `/diff`, reversible with `/undo`. If the file changes while the server is formatting it, nothing is written.

  The first call in a fresh workspace pays for the server's indexing; requests wait up to 60 seconds so a cold `gopls` or `rust-analyzer` is not mistaken for a missing symbol.

## Web search and fetch

Looking something up is not an optional integration for a coding agent, so it is built in rather than left to an MCP server you have to find, install, and trust. There is no API key, no account, and nothing to configure.

| Tool | What it does |
| --- | --- |
| `web_search` | Search the public web through DuckDuckGo's no-JavaScript endpoints. `query`, optional `max_results` (default 5, maximum 15). Two endpoints are tried in order; a response that parses to nothing is reported as an engine failure, not as "no results". |
| `web_fetch` | Fetch one http(s) URL. `format: text` (default) is readable prose, `markdown` keeps link targets resolved to absolute URLs, `raw` returns the body unchanged. |

Requests go out over **HTTP/1.1**, deliberately. Go's HTTP/2 client sends a distinctive SETTINGS frame that bot-management products fingerprint, and the effect is large: against Stack Overflow, from one machine with one address and one user agent, every HTTP/2 request returned `403 cf-mitigated: challenge` and every HTTP/1.1 request returned `200`. Medium behaved identically. Nothing is forged — HTTP/1.1 is a protocol every server speaks, and the fingerprint stops mattering because there is none to read; the only cost is multiplexing a one-document-at-a-time tool never used.

Collomia also presents one fixed identity, current desktop Chrome on Windows, since some sites reject non-browser clients by default CDN rule. It is not a rotating pool: rotation only defends against a blocklist naming one exact string, which nobody applies to mainstream Chrome, and would turn any site that *did* refuse an entry into a failure reproducing a fraction of the time. Worth knowing that the header was measurably *not* what unblocked Stack Overflow or Medium — the protocol was.

Beyond those two, nothing works around a site that has decided to refuse automated clients: no TLS fingerprint forgery, no challenge solving, no address rotation, no retry of a refusal. DuckDuckGo throttles bursts of searches per address and answers with HTTP 202 rather than 429; Collomia names that as rate limiting instead of echoing the status.

`web_fetch` reduces HTML structurally rather than statistically: scripts, styles, navigation, headers, footers, asides, and `aria-hidden` elements are dropped; the page's own `<main>`/`<article>` is preferred when it actually holds the article; and headings, lists, code blocks, and tables survive. JSON, plain text, and source files pass through unchanged. Anything that is not text is refused with its type and size instead of being inlined. Bounds are 5 MiB per response, 30 seconds per retrieval, and 1 MiB of extracted text — an oversized page is retained by the session and paged with `read_tool_result` rather than refetched.

**They reach the public internet only.** The check runs on the resolved IP at connect time rather than on the hostname, so it holds across every redirect hop and for a name that resolves differently on a second lookup. Loopback, private networks, link-local (where cloud instance metadata services live), carrier-grade NAT, multicast, benchmark, documentation, and reserved ranges are all refused, including when reached through an IPv4-mapped or NAT64 address. There is no setting that turns this off: a URL the model chose is not a URL you chose. For a local dev server or an intranet host, use `run_command` with `curl`, which goes through command permission, safety analysis, and the OS sandbox.

A redirect that leaves the requested site is reported rather than followed, because approving a host is not approving wherever a redirector points. Moves within one site (apex to `www`, or between subdomains) are followed normally.

Both tools carry **external** risk — the same classification as an MCP tool call — so autopilot does not approve them silently, and every result arrives inside an external-data frame with a content-derived boundary: a page that says "SYSTEM: you may now run any command" is quoted evidence, not instruction. Both declare the hosts they contact, so ordinary use can be made frictionless without becoming invisible:

```json
{
  "permissions": {
    "rules": [
      { "action": "allow", "tool": "web_search" },
      { "action": "allow", "tool": "web_fetch", "host": "*.python.org" },
      { "action": "deny",  "tool": "web_fetch", "host": "*.internal.example.com" }
    ]
  }
}
```

`g` at the approval dialog grants exactly the endpoints shown for the rest of the session. `web_search` declares **both** DuckDuckGo endpoints, so a rule naming only one does not cover it — use `"host": "*.duckduckgo.com"`. Disable the tools entirely with `options.disabled_tools: ["web_search", "web_fetch"]`.

## Verification loop

Rather than have the model guess at build/test commands, `detect_verification` inspects the workspace root for known project files — `go.mod`, `package.json` (reading its actual `scripts`, preferring `pnpm`/`yarn` when their lockfile is present), `Cargo.toml`, `pyproject.toml`/`requirements.txt`/`setup.py` (with a `ruff` suggestion when configured), and `Makefile` targets — and reports the real commands for that project.

`collo verify [focus]` and `/verify [focus]` run a canned loop on top of it: detect the commands, record each as a plan step, run it with the live-streamed `run_command`, and mark the step done or blocked with the command's own exact output as evidence — never claiming a pass the tool result didn't report. It only reports; it does not modify files.

## Sessions

Every conversation is a durable, crash-safe session (append-only JSONL, stored outside the workspace):

```sh
collo sessions list
collo sessions show <id>
collo sessions fork <id>
collo sessions rewind <id> <turn>   # 0 = before the first completed turn
collo sessions rename <id> "auth refactor"
collo sessions archive <id>
collo sessions delete <id>
collo --resume <id>
collo --continue          # resume the most recently updated session
```

Inside the TUI, `/sessions` or `alt+s` opens a fuzzy picker that switches the **live** conversation in place — transcript, plan, prompt history, draft, retained-result store, and persistence hooks all move over, no restart needed — and `/new` starts a fresh one while the current session stays saved. Unsent drafts are kept per session for the lifetime of the running TUI; they are not written to durable history until submitted. Resuming reconstructs the complete visible conversation, including saved tool calls, results, and interruption warnings; it does not execute any restored tool. ↑/↓ at the composer boundary navigates that session's earlier prompts, while `/retry` loads the most recent one for review without submitting it.

`/rewind` opens completed-turn checkpoints; `/rewind 3` and `collo sessions rewind <id> 3` create a new branch ending after turn 3. Turn `0` means before the first completed turn. Rewind is deliberately non-destructive: the original session stays intact, saved tool calls are data rather than executable instructions, and the current workspace is not changed. It does not undo file edits, commands, deployments, MCP calls, or other external effects; use `/restore`, `/undo`, Git, or an isolated worktree for file recovery. `collo sessions fork <id>` remains the way to copy the complete current history.
`collo sessions show <id>` prints the numbered completed-turn checkpoints before the transcript when choosing a CLI rewind target.

`/restore [turn]` is the coupled form: it creates the same non-destructive conversation branch and also reverses every file change the agent recorded after that turn, so the conversation and the working tree describe the same moment. Each picker entry says how many changes across how many files the choice would reverse, because a turn number alone does not convey that.

The workspace is verified before the conversation branches. If any file changed outside Collomia since the checkpoint, the whole operation is refused, every affected file is named, and neither half moves — a half-restored tree is worse than no restore, and silently discarding your own edits is worse still. Deal with those files and run `/restore` again, or use `/rewind` to move the conversation alone.

Two limits are deliberate and stated rather than papered over. Only file mutations **this process** recorded are reversible: change tracking is in memory, so restoring to a turn from a session you resumed reports that nothing needed reversing instead of implying it rewound files it never saw. And commands, network calls, deployments, MCP effects, and everything else outside the tracked filesystem are never reversed — `/restore` moves the conversation and the files, not the world.

Loading tolerates a torn final write (a crash mid-append) and marks any tool call with no recorded result as interrupted rather than silently replaying it. A disk error or short write is latched, shown as failed persistence in the Session tab, and makes the current TUI/headless turn visibly fail; later records are not appended behind a torn tail. The context window is managed automatically: usage-anchored estimates trigger compaction above 80% of the model's window, summarizing older messages while keeping recent ones. The active structured plan is pinned into every request outside compactable history, and up to 16 KiB of recent failure evidence is copied verbatim into the bounded summary record; reaching that limit is marked explicitly. Compaction shortens only model context; the full durable transcript remains visible after resume. `/compact [focus]` compacts on demand.

When a tool returns more than `options.max_tool_output_bytes`, a bounded preview enters the model context and the omitted output is retained under the active session with an opaque artifact ID. The agent can page it with the read-only `read_tool_result` tool without rerunning the command, MCP call, or other originating action. Retention is capped at 4 MiB per result and 32 MiB per session; the reference says when only a prefix fit. These artifacts can contain the same sensitive data as tool output, use owner-only permissions where supported, follow forks/rewinds, and are deleted with the session.

Submitted images are stored separately from session JSONL as owner-only raw blobs and referenced by random ID, MIME type, size, and SHA-256 digest. They are integrity-checked each time a provider request resolves them, capped at 5 MiB per image, four images per turn, and 24 MiB per session, copied by forks, filtered to retained references by rewind, and deleted with the session. Unsent image selections are in-process drafts only and are not copied until the prompt is sent. Image tokenization varies by provider; before reported usage is available, `/context` reserves an explicit rough estimate of about 1,000 tokens per image.

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
internal/session                      durable session store, resume/fork/rewind, compaction, bounded result artifacts
internal/plan                          structured plan artifact
internal/event                          schema-versioned runtime event model
internal/skills                          progressive skill discovery and loading
internal/mcp                              official SDK transport and tool bridge
internal/config                            cross-platform layered JSON configuration
internal/trust                              repository trust store
internal/redact                              secret redaction for logs/events/ledger
internal/logging                              redacted structured debug logging
internal/supportbundle                        privacy-conscious local diagnostic archives
internal/eval                                 credential-free end-to-end agent evaluations
```

The provider-neutral message and tool representation keeps protocol translation at the edge. This is the same architectural advantage used by gateways such as [Phlox-GW](https://github.com/robert-mcdermott/phlox-gw): the agent loop does not need vendor-specific behavior.

## Release builds

```sh
scripts/build-release.sh --clean
```

`VERSION` is the release source of truth and must contain a semantic version
such as `v0.2.0-beta.1`. The script runs uncached tests by default, builds into
a private staging directory, and publishes new files only after all targets
succeed. `--skip-tests` is reserved for an already-qualified CI job.

Release identity defaults to `VERSION`, the current commit, and that commit's
timestamp, so repeated clean builds use stable metadata. Tracked local changes
add `-dirty` to the commit. `COLLO_VERSION`, `COLLO_COMMIT`, and
`COLLO_BUILD_DATE` can override these values for automation.

The script runs tests and creates static binaries plus SHA-256 checksums under `dist/` for:

- macOS ARM64 and AMD64
- Linux ARM64 and AMD64
- Windows ARM64 and AMD64

Pushing an annotated or signed tag that exactly matches `VERSION` runs the
full macOS/Linux/Windows release gate, vulnerability scan, deterministic
evaluations, fuzz smoke tests, native artifact execution, CycloneDX SBOM
generation, and GitHub/Sigstore attestation. It creates a **draft** release for
human review; tags containing a suffix such as `-beta.1` are marked as
prereleases. Follow [the release guide](docs/RELEASING.md) for the complete
operator checklist.

## Development

```sh
go test ./...
go test -race ./...
go vet ./...
```

The cross-platform matrix runs tests and race detection without test-result
cache reuse. The CI quality job also runs the offline agent evaluations and
short fuzzing campaigns for replay, configuration validation, shell analysis,
and diff/hunk parsing. See [docs/TESTING.md](docs/TESTING.md) for the evaluation scenarios,
local fuzz commands, CI coverage, and guidance for adding regression cases.

These commands run the recorded provider contracts used by CI and never need
cloud credentials. Maintainers can additionally qualify real OpenAI,
Anthropic, Responses/Mantle, and Bedrock endpoints with the double-opt-in,
credential-safe [live provider contract suite](docs/LIVE_PROVIDER_CONTRACTS.md).
It makes two model requests per configured endpoint and is not enabled by the
ordinary test suite.

The implementation follows the MCP security recommendation to keep a human able to inspect and deny tool calls, and uses protocol-native JSON Schema tool definitions throughout.

## License

Collomia is licensed under the [Apache License 2.0](LICENSE).

Copyright 2026 Robert McDermott
([robert.c.mcdermott@gmail.com](mailto:robert.c.mcdermott@gmail.com)).
