# Collomia user guide

This guide is the complete user-facing manual for installing, configuring,
operating, extending, and troubleshooting Collomia. It is written for both a
first-time user who wants a safe working setup and an advanced user integrating
Collomia with hosted models, language servers, MCP servers, hooks, automation,
and unattended workflows.

For the exact security boundary, read [Security model](SECURITY.md). For a
generated statement of what is implemented today, read the [capability
matrix](CAPABILITIES.md). Installation, verification, upgrades, and rollback
also have a focused [installation guide](INSTALLING.md); maintainers should use
the [release guide](RELEASING.md).

## Contents

- [Installation](#installation)
- [Five-minute setup](#five-minute-setup)
- [How configuration works](#how-configuration-works)
- [Complete configuration reference](#complete-configuration-reference)
- [Provider setup](#provider-setup)
- [Permissions and safety](#permissions-and-safety)
- [Using the terminal interface](#using-the-terminal-interface)
- [Headless and automated use](#headless-and-automated-use)
- [Tools and coding workflows](#tools-and-coding-workflows)
- [Language-server support](#language-server-support)
- [Instructions and skills](#instructions-and-skills)
- [MCP servers](#mcp-servers)
- [Lifecycle hooks](#lifecycle-hooks)
- [Sub-agents](#sub-agents)
- [Sessions and context](#sessions-and-context)
- [Browser terminal](#browser-terminal)
- [Files, state, logs, and privacy](#files-state-logs-and-privacy)
- [Support bundles and problem reports](#support-bundles-and-problem-reports)
- [Troubleshooting](#troubleshooting)
- [Uninstalling](#uninstalling)

## Installation

Collomia is distributed as one statically linked `collo` executable. It does
not require Node.js, npm, Python, or a Go runtime when installed from a release.
The browser terminal's xterm.js files are vendored and embedded in that same
executable.

Release assets are named:

| Platform | AMD64/x86-64 | ARM64 |
| --- | --- | --- |
| macOS | `collo-darwin-amd64` | `collo-darwin-arm64` |
| Linux | `collo-linux-amd64` | `collo-linux-arm64` |
| Windows | `collo-windows-amd64.exe` | `collo-windows-arm64.exe` |

Every release also includes `checksums.txt` with SHA-256 digests and
`collomia.cdx.json`, a CycloneDX dependency SBOM. The release workflow publishes
GitHub/Sigstore provenance and SBOM attestations for stronger verification.

### macOS and Linux: install with curl and sh

The repository installer detects the operating system and CPU, downloads the
matching release binary and checksum manifest, requires exactly one matching
SHA-256 entry, tests the downloaded executable, and atomically installs `collo`
without `sudo`:

```sh
curl --proto '=https' --tlsv1.2 -fsSL \
  https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.sh | sh
```

The installer does not modify `PATH`, create application data, or start
Collomia. The default destination is `$HOME/.local/bin/collo`. Make sure that
directory is on `PATH`:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Add that line to `~/.zshrc`, `~/.bashrc`, or your shell's equivalent if it is
not already present. Open a new terminal, then verify the installation:

```sh
collo --version
collo doctor
```

Installer overrides:

```sh
# Install a particular release tag.
curl --proto '=https' --tlsv1.2 -fsSL \
  https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.sh |
  COLLO_VERSION=v0.1.3 sh

# Install somewhere else.
curl --proto '=https' --tlsv1.2 -fsSL \
  https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.sh |
  COLLO_INSTALL_DIR="$HOME/bin" sh
```

`COLLO_REPOSITORY` can point the installer at a fork using `owner/repository`
syntax. For a production install, pinning `COLLO_VERSION` makes the downloaded
artifact reproducible.

If your security policy does not allow piping a network response to a shell,
download and inspect `install.sh` first:

```sh
curl --proto '=https' --tlsv1.2 -fsSLO \
  https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.sh
less install.sh
sh install.sh
```

### Windows: install with PowerShell

Download and inspect the repository-owned PowerShell installer, then run it.
It detects AMD64 or ARM64, downloads the binary and checksum manifest, requires
exactly one valid SHA-256 entry, tests the downloaded executable, and only then
replaces the installed `collo.exe`. It does not require elevation. PATH changes
are explicit through `-AddToPath`.

```powershell
$Installer = Join-Path $env:TEMP 'install-collo.ps1'
[Net.ServicePointManager]::SecurityProtocol = `
  [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
Invoke-WebRequest -UseBasicParsing `
  'https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.ps1' `
  -OutFile $Installer
Get-Content $Installer
Unblock-File $Installer
& $Installer -AddToPath
```

The default executable location is
`$env:LOCALAPPDATA\Programs\Collomia\collo.exe`. Install a pinned release or
choose another directory with:

```powershell
& $Installer -Version v0.2.0-beta.1 -InstallDir "$HOME\bin" -AddToPath
```

`COLLO_VERSION`, `COLLO_INSTALL_DIR`, and `COLLO_REPOSITORY` provide equivalent
defaults. Omit `-AddToPath` when shell configuration must remain unchanged.
Close a running Collomia process before upgrading because Windows may refuse to
replace an executable in use. The focused [installation guide](INSTALLING.md)
also provides a direct `Invoke-WebRequest` binary workflow for organizations
that prohibit downloaded PowerShell scripts.

### Manual binary installation

You can install any release without the scripts:

1. Download the binary for your platform, `checksums.txt`, and optionally the
   CycloneDX `collomia.cdx.json` SBOM from the same GitHub release.
2. Verify SHA-256 with `sha256sum`, `shasum -a 256`, or PowerShell
   `Get-FileHash -Algorithm SHA256`.
3. On macOS/Linux, make it executable with `chmod 0755` and rename it to
   `collo`. On Windows, rename it to `collo.exe`.
4. Move it into a directory on `PATH`.
5. Run `collo --version` and `collo doctor`.

Checksums detect corruption but do not protect against replacement of both the
binary and manifest. Release artifacts also carry GitHub/Sigstore provenance:

```sh
gh attestation verify collo-linux-amd64 \
  --repo robert-mcdermott/collomia \
  --signer-workflow robert-mcdermott/collomia/.github/workflows/release.yml
```

Raw macOS and Windows beta binaries are not yet platform code-signed or Apple
notarized. Review [beta status](BETA.md) and the complete verification and
rollback guidance in [Installing Collomia](INSTALLING.md).

### Build from source

Building requires Go 1.26.5 or later. Collomia pins the patch-level minimum
because Go standard-library security fixes are shipped in patch releases:

```sh
git clone https://github.com/robert-mcdermott/collomia.git
cd collomia
go build -o collo ./cmd/collo
./collo --version
```

The release build script validates `VERSION`, runs uncached tests, and builds
all six platform/architecture targets through a private staging directory so a
failed build cannot publish a partial replacement:

```sh
scripts/build-release.sh --clean
```

Its default commit-derived timestamp makes repeated clean builds from the same
commit deterministic. The tag-triggered release workflow additionally runs
the complete cross-platform gates, generates the SBOM and attestations, runs
the generated artifacts natively, and creates a draft release. Maintainers
should follow [Releasing Collomia](RELEASING.md).

## Five-minute setup

### 1. Check the installation

Run diagnostics from the project you intend to use:

```sh
cd /path/to/project
collo doctor
```

`doctor` checks the Collomia build, platform, merged configuration, repository
trust, TTY, Git, provider credentials, MCP entries, sandbox availability, and
debug-log directory. Warnings are actionable but do not necessarily prevent an
interactive session. A failed check makes `doctor` exit non-zero.

### 2. Create a user-wide starter configuration

```sh
collo init --global --with-reference
```

This creates:

- `~/.collomia/config.json` on macOS/Linux, or
  `%USERPROFILE%\.collomia\config.json` on Windows: active strict-JSON
  configuration.
- `config.example.jsonc` beside it: commented documentation only; Collomia
  never loads this file.

The global starter includes Ollama, an unselected OpenRouter example, safe
permission defaults, and common runtime options. `init` never overwrites an
existing file.

If you use the default Ollama setup:

```sh
ollama pull qwen3-coder
collo
```

If you use OpenRouter, set the key in your shell and select the existing
`openrouter` entry in `~/.collomia/config.json`:

```sh
export OR_API_KEY='your-key'       # macOS/Linux
$env:OR_API_KEY = 'your-key'       # PowerShell, current window
```

Change `default_provider` to `openrouter`, then validate:

```sh
collo config validate --strict
collo config show
collo doctor
```

### 3. Optionally create project overrides

From a repository root:

```sh
collo init --with-reference
```

This creates `.collomia.json` plus the optional `.collomia.example.jsonc`.
The project starter contains only a permission-mode override so that providers
and other user defaults continue to inherit from the global file.

Project configuration is quarantined until approved:

```sh
collo config validate --strict
collo trust
collo trust --status
```

Review the displayed file before answering yes. A change to `.collomia.json`
invalidates its content-bound trust and requires review again.

### 4. Start Collomia

```sh
collo
collo "Explain this repository and identify the main entry points"
collo --plan "Propose a safe migration plan"
```

The first form opens an empty interactive TUI. The second starts the TUI with
an initial prompt. `--plan` exposes read-only discovery tools and the structured
plan tool, making it useful before authorizing edits.

## How configuration works

### Active files and generated reference files

| Scope | Active file | Applies to |
| --- | --- | --- |
| User/global | `~/.collomia/config.json` on macOS/Linux; `%USERPROFILE%\.collomia\config.json` on Windows | Every workspace for that operating-system user |
| Project | `<workspace>/.collomia.json` | Only that workspace, after trust when the file exists |
| User reference | `~/.collomia/config.example.jsonc` | Documentation only; never loaded |
| Project reference | `<workspace>/.collomia.example.jsonc` | Documentation only; never loaded |

Active files are strict JSON: no comments, trailing commas, or unquoted keys.
The `.example.jsonc` files may contain comments because they are not read as
configuration. Generate or print the exhaustive current reference at any time:

```sh
collo config reference
collo init --global --with-reference
collo init --with-reference
```

Copy settings you intend to change from the JSONC reference into the active
JSON file. Do not rename the reference to an active filename without first
removing its comments.

### Precedence

The effective configuration is assembled from lowest to highest precedence:

1. Built-in defaults.
2. Global user configuration.
3. Trusted project `.collomia.json`.
4. `COLLO_PROVIDER` and `COLLO_MODEL` environment selections.
5. `--provider` and `--model` for the current invocation.

`--autonomy` overrides `permissions.mode` for the current invocation. `/model`,
`/autonomy`, `/plan`, and `/theme` can make session/runtime changes, but they do
not rewrite configuration files.

Later layers override only values they supply. Scalar fields nested in an
object inherit independently. Named maps such as `providers`, `mcp`, and
`agents` keep differently named entries from earlier layers, but a same-named
entry should be treated as a complete replacement. Lists such as
`permissions.rules`, `allowed_tools`, and `options.disabled_tools` are replaced
when a later file specifies them.

`permissions.denied_commands` is the deliberate list-merging exception: it is
additive and cannot be weakened by a later layer. Built-in regex patterns are
always retained, global configuration can add user-wide patterns, and trusted
project configuration can add project patterns to that combined set. Exact
duplicates are retained once. An empty list adds nothing and cannot clear
inherited denials. Separate structural catastrophic-command checks are compiled
into Collomia and cannot be disabled by any configuration scope.

Example global configuration:

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
    "sandbox": "auto",
    "sandbox_allow_network": true,
    "sandbox_allow_read_outside_workspace": true
  }
}
```

Example project override:

```json
{
  "schema_version": 1,
  "permissions": {
    "mode": "workspace"
  },
  "lsp": {
    "go": ["gopls", "serve"]
  }
}
```

The project inherits the global provider and sandbox settings, changes the
permission mode, and adds a Go language-server command.

Inspect the result rather than reasoning about layers manually:

```sh
collo config show
```

It prints the merged configuration with secrets redacted, followed by every
applied or quarantined layer and the keys each layer contributed.

### Environment expansion and secrets

Provider `base_url`, literal `api_key`, custom header values, Entra settings,
and MCP URL/env/header values support `$NAME` and `${NAME}` expansion. The
preferred secret form is `api_key_env`, which names an environment variable
without placing its value in JSON:

```json
{
  "providers": {
    "example": {
      "type": "openai-compatible",
      "base_url": "${MODEL_GATEWAY_URL}/v1",
      "api_key_env": "MODEL_GATEWAY_KEY",
      "headers": {
        "X-Tenant": "${MODEL_GATEWAY_TENANT}"
      },
      "model": "coding-model"
    }
  }
}
```

Set variables for the current shell:

```sh
export MODEL_GATEWAY_URL='https://gateway.example.com'
export MODEL_GATEWAY_KEY='secret'
export MODEL_GATEWAY_TENANT='engineering'
```

```powershell
$env:MODEL_GATEWAY_URL = 'https://gateway.example.com'
$env:MODEL_GATEWAY_KEY = 'secret'
$env:MODEL_GATEWAY_TENANT = 'engineering'
```

To persist a Windows variable for future terminals:

```powershell
[Environment]::SetEnvironmentVariable('MODEL_GATEWAY_KEY', 'secret', 'User')
```

Collomia redacts configured secrets and common credential shapes from debug
logs, JSONL events, and audit records on a best-effort basis. This is defense
in depth, not protection against a malicious command or model-directed
exfiltration. Prefer `permissions.command_env: "minimal"`, an OS sandbox, and
short-lived credentials for higher-risk work.

### One global directory

Collomia uses one global root for every persistent user-level file:

- `~/.collomia/` on macOS and Linux.
- `%USERPROFILE%\.collomia\` on Windows.

Configuration, generated references, user instructions, skills, sessions,
logs, audit ledgers, repository trust decisions, and MCP pins all live below
that root. Collomia does not search any additional platform configuration or
cache directory. This makes the complete user state easy to inspect, back up,
or remove as one unit.

### Validation and schema versions

Use strict validation after every edit:

```sh
collo config validate --strict
```

Normal loading tolerates unknown fields for forward compatibility; `--strict`
rejects them, which catches misspellings. Validation checks provider types,
authentication combinations, required endpoints/models, timeouts, modes,
globs, regular expressions, hook events, hook matchers, keybinding action
names, supported key forms, and global key collisions. It parses the
project file for validation even before trust, but validation alone does not
activate that file.

`schema_version` is currently `1`. A missing version is treated as version 1.
A file declaring a schema newer than the running binary is rejected with an
instruction to upgrade Collomia.

## Complete configuration reference

### Top-level fields

| Field | Type | Meaning |
| --- | --- | --- |
| `schema_version` | integer | Configuration schema; currently `1`. |
| `default_provider` | string | Local name of the selected provider when no environment or CLI override exists. |
| `default_model` | string | Fallback model when the selected provider has no `model`. |
| `providers` | object/map | Named provider definitions. At least one is required after merging. |
| `permissions` | object | Autonomy, rules, sandbox, and command-environment controls. |
| `mcp` | object/map | Named MCP server definitions. |
| `options` | object | Agent/TUI/runtime options. |
| `agents` | object/map | Named sub-agent profiles used by `delegate`. |
| `lsp` | object/map | Language ID to language-server argv. |
| `hooks` | object/map | Lifecycle event to a list of hook commands. |

### Provider fields

Every value under `providers` has the following common shape. Not every field
applies to every provider type.

| Field | Type | Default/meaning |
| --- | --- | --- |
| `type` | string | Required protocol adapter; see [Provider setup](#provider-setup). |
| `base_url` | string | Required except for native `bedrock`; trailing `/` is removed. |
| `api_key` | string | Literal credential or expanded value; avoid when `api_key_env` works. |
| `api_key_env` | string | Name of the environment variable holding the credential. |
| `model` | string | Provider model ID or Azure deployment name. |
| `region` | string | Native Bedrock region; Collomia uses `us-east-1` when omitted. |
| `profile` | string | AWS shared configuration/profile name for Bedrock SigV4. |
| `deployment` | string | Azure OpenAI deployment; falls back to selected model. |
| `api_version` | string | Azure OpenAI deployment-route API version; defaults to `2024-10-21`. |
| `auth` | string | Provider-specific authentication family. |
| `entra_scope` | string | Optional HTTPS `/.default` scope override for Azure Entra auth. |
| `entra_tenant_id` | string | Optional tenant selector for Azure developer/workload credentials. |
| `entra_authority_host` | string | Optional HTTPS authority origin for sovereign/private Entra clouds. |
| `headers` | object/map | Additional HTTP headers; values support environment expansion. |
| `max_tokens` | integer | Provider-neutral output budget; defaults to `8192`. |
| `context_window` | integer | Configured model context size, used for status, preflight, and compaction. |
| `temperature` | number | Optional sampling temperature. Omit for the provider/model default. |
| `connect_timeout_seconds` | integer | Connection setup timeout; defaults to `10`. |
| `request_timeout_seconds` | integer | Whole request timeout; defaults to `1800`. |
| `stream_idle_timeout_seconds` | integer | Maximum silence between stream chunks; defaults to `300`. |

`max_tokens` remains the same Collomia setting across protocols. The OpenAI
adapter sends `max_tokens` first and changes to `max_completion_tokens` only
after an explicit provider rejection requiring it. The Responses adapter uses
`max_output_tokens`; Anthropic and Bedrock translate the budget to their native
request shapes.

### Permission fields

| Field | Type | Meaning |
| --- | --- | --- |
| `mode` | string | `ask`, `workspace`, or `autopilot`; default `ask`. |
| `allow_outside_workspace` | boolean | Allows built-in path tools to resolve outside the workspace; permission checks still apply. |
| `allowed_tools` | string list | Persistent session-start allowlist by exact tool name. |
| `denied_tools` | string list | Exact tool names that are always disabled by the permission manager. |
| `denied_commands` | regex list | Additional hard command denials checked again at execution. Built-in, global, and project patterns accumulate and cannot be removed by a lower layer; structural catastrophic checks are separate and always active. |
| `rules` | rule list | Ordered scoped policy rules; first match wins. |
| `sandbox` | string | `off`, `auto`, or `require`; default `off`. |
| `sandbox_allow_network` | boolean | Allows network inside sandboxed shell/background commands. Defaults to `true` for package-manager compatibility; provider and MCP networking is separate. |
| `sandbox_allow_read_outside_workspace` | boolean | Allows broad user-data reads inside sandboxed commands. Defaults to `true` for toolchain compatibility; set `false` to request OS-enforced workspace-scoped user-data reads. Windows AppContainer remains read-confined either way. |
| `sandbox_readable_roots` | string list | Additional narrowly scoped read/execute roots used when reads are confined, resolved from the workspace when relative. Useful for dependency stores and read-only SDKs. |
| `sandbox_writable_roots` | string list | Additional narrowly scoped read/write roots for sandboxed commands, resolved from the workspace when relative. Every writable root is implicitly readable. |
| `command_env` | string | `full` or `minimal`; if omitted while sandboxing is enabled, minimal is used. |
| `reviewer_command` | string | Optional external policy reviewer for otherwise auto-approved non-read actions. |

Each rule supports:

| Field | Meaning |
| --- | --- |
| `action` | Required: `allow`, `prompt`, or `deny`. |
| `tool` | Tool-name glob, for example `run_command` or `mcp_*`. |
| `path` | Glob matched against resolved native paths; a suffix of `/**` includes the directory and descendants. |
| `command` | Executable-name glob such as `go`, `git`, or `npm`. |
| `host` | Network host/domain glob for tools that declare hosts. |
| `server` | MCP server-name glob. |
| `reason` | Human-readable explanation shown with the rule decision. |

At least one matcher must be present. All populated matcher categories must
match. For an `allow` rule, every resource in the request must be covered;
`deny` and `prompt` match when any resource in that category matches. An allow
rule never vouches for a shell command the static analyzer could not fully
inspect.

### MCP server fields

| Field | Meaning |
| --- | --- |
| `transport` | `stdio` (default if empty), `http`, or `streamable-http`. |
| `trusted` | Must be `true` before a configured server is started. |
| `command` | Executable for `stdio`. No shell is inserted. |
| `args` | Argument list for the stdio command. |
| `url` | Endpoint for Streamable HTTP. |
| `env` | Extra environment values for a stdio process; supports expansion. |
| `headers` | HTTP headers for a remote server; supports expansion. |
| `disabled` | Keeps the definition visible but does not connect it at startup. |
| `timeout_seconds` | Connect, operation, and HTTP timeout; defaults to `30`. |

### Runtime option fields

| Field | Meaning |
| --- | --- |
| `max_iterations` | Maximum model/tool iterations in one turn; defaults to `24`. |
| `max_tool_output_bytes` | Per-result preview cap used by shell output and active model context; defaults to `65536`. Larger returned strings use bounded session artifacts when durable sessions are available. |
| `delegate_max_concurrency` | Session-wide delegated-task limit, `1`–`6`; defaults to `4`. It applies across simultaneous `delegate` calls. |
| `delegate_provider_concurrency` | Optional map of provider name to a tighter `1`–`6` task limit. Omitted providers use the global limit. |
| `disabled_tools` | Tool names hidden from the model. This is separate from permission denial. |
| `transcript_directory` | Reserved configuration field. The current durable session store does not use it; sessions remain under the global `.collomia/sessions` directory. |
| `theme` | Persistent TUI theme name; defaults to `collomia`. |
| `alternate_screen` | Whether the TUI uses the terminal's clean alternate buffer; defaults to `true`. Set `false` to keep the final frame in native terminal scrollback. |
| `reduced_motion` | Optional static working indicator. Defaults to `false`, so animations remain enabled; it never changes input, commands, cancellation, or other controls. |
| `keybindings` | Named global TUI action-to-key overrides. Omitted actions inherit defaults; approval and question decision keys are intentionally fixed. |
| `notifications` | `on` (bell + OSC 9), `bell`, or `off`; empty behaves as `on`. |
| `editor` | Optional direct external-editor command and argument list used by `e` in `/diff`. Arguments support `{file}`, `{line}`, and `{column}`. |
| `debug` | Enables redacted structured debug logging for every run. |

### Named agent fields

| Field | Meaning |
| --- | --- |
| `model` | Model override on the same provider as the parent. |
| `instructions` | Role instructions prepended to the sub-agent prompt. |
| `tools` | Tool-name allowlist; empty inherits the parent tool surface. |
| `skills` | Skill-name allowlist; empty inherits the parent catalog. |
| `max_iterations` | Per-agent iteration override; zero inherits the normal budget. |
| `token_budget` | Maximum provider-reported input plus output tokens for the task; zero disables this additional limit. |
| `timeout_seconds` | Queue plus execution deadline; zero means `600`, maximum `3600`. |
| `permissions` | Child-only restrictions: optional stricter `mode`, additive `denied_tools`/`denied_commands`, and `prompt`/`deny` rules. `allow` rules are rejected. |

Profile permissions cannot weaken their parent. The effective autonomy is the
stricter mode; profile denials are added to inherited denials; profile rules
are evaluated as an independent restriction layer and may only prompt or deny,
so neither layer can mask the other's denial. Sandbox, networking,
outside-workspace access, command environment, reviewer, catastrophic denials,
and all other parent protections remain inherited unchanged.

### LSP field

`lsp` maps an LSP language ID to an argv array. The first item is the
executable; remaining items are arguments. Collomia executes it directly:

```json
{
  "lsp": {
    "go": ["gopls", "serve"],
    "python": ["pyright-langserver", "--stdio"],
    "typescript": ["typescript-language-server", "--stdio"]
  }
}
```

### Hook fields

`hooks` maps one of eleven event names to an array of definitions:

| Field | Meaning |
| --- | --- |
| `command` | Required executable path/name. It is run directly, without a shell. |
| `args` | Argument list. |
| `matcher` | Optional Go regular expression tested against the tool name for tool events, otherwise the event name. |
| `timeout_seconds` | Per-hook timeout; defaults to `10`. |

The complete hook behavior and payload are in [Lifecycle hooks](#lifecycle-hooks).

## Provider setup

A provider name is your local alias (`openrouter`, `work-azure`, `bedrock`, and
so on). Its `type` selects Collomia's protocol adapter. You can configure many
providers and switch among them without restarting the TUI.

| Provider `type` | Protocol/route | Authentication |
| --- | --- | --- |
| `openai` | OpenAI Chat Completions + `GET /models` | Bearer API key |
| `openai-compatible` | OpenAI-compatible Chat Completions + optional model catalog | Optional bearer API key |
| `anthropic` | Anthropic Messages + model catalog | `x-api-key`, or bearer when selected |
| `anthropic-compatible` | Anthropic-compatible Messages | `x-api-key`, or bearer when selected |
| `bedrock` | Native AWS Bedrock `ConverseStream` | AWS SigV4 chain or Bedrock bearer API key |
| `bedrock-mantle` | OpenAI Responses-style `/responses` | Bearer Bedrock API key |
| `azure-openai` | Azure deployment-scoped Chat Completions, or OpenAI/v1 route | Azure API key, static bearer, or Microsoft Entra |
| `azure-foundry` | Microsoft Foundry OpenAI/v1 Chat Completions | Azure API key, static bearer, or Microsoft Entra |
| `azure-foundry-anthropic` | Microsoft Foundry Claude Messages | Azure API key, static bearer, or Microsoft Entra |

The provider/model selection order is command-line override, environment
override, selected provider's `model`, then `default_model`:

```sh
collo --provider openrouter --model vendor/model-id
COLLO_PROVIDER=openrouter COLLO_MODEL=vendor/model-id collo
```

Inside the TUI, `/model` opens a fuzzy provider/model picker, `/model
provider/model` switches directly, and `/models` shows configured defaults,
effective capabilities, endpoint constraints, health, and live catalog status
where discovery is supported.

### Ollama

Ollama is the built-in default. Its OpenAI-compatible endpoint normally needs
no API key:

```json
{
  "schema_version": 1,
  "default_provider": "ollama",
  "providers": {
    "ollama": {
      "type": "openai-compatible",
      "base_url": "http://127.0.0.1:11434/v1",
      "model": "qwen3-coder",
      "max_tokens": 8192,
      "context_window": 32768
    }
  }
}
```

Start Ollama and pull the configured model before launching Collomia:

```sh
ollama pull qwen3-coder
ollama list
collo doctor
collo
```

If Ollama is listening elsewhere, change `base_url`. When sandbox network is
disabled on macOS, loopback remains available specifically for services such
as Ollama. Linux Landlock network behavior depends on the kernel as described
under [OS sandboxing](#os-sandboxing).

### OpenAI

```sh
export OPENAI_API_KEY='your-key'
```

```json
{
  "schema_version": 1,
  "default_provider": "openai",
  "providers": {
    "openai": {
      "type": "openai",
      "base_url": "https://api.openai.com/v1",
      "api_key_env": "OPENAI_API_KEY",
      "model": "your-model-id",
      "max_tokens": 8192,
      "context_window": 200000
    }
  }
}
```

Use a model that supports tool calling. Collomia's agent loop depends on
function tools for file inspection, edits, shell commands, planning, and other
features. `/models` can list the catalog, but many APIs do not return reliable
per-model feature metadata; `unknown` is not the same as unsupported.

### OpenRouter

The generated global starter includes this provider but leaves Ollama selected:

```sh
export OR_API_KEY='your-key'
```

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
  }
}
```

Model IDs and limits are provider-specific. Set `context_window` and
`max_tokens` to the values appropriate for the selected model and your account,
not merely the largest numbers accepted by the schema.

### Other OpenAI-compatible services

Ollama, vLLM, LM Studio, Phlox-GW, and many gateways expose the same basic
route. Collomia appends `/chat/completions` to `base_url` and queries `/models`
for discovery:

```json
{
  "providers": {
    "local-vllm": {
      "type": "openai-compatible",
      "base_url": "http://127.0.0.1:8000/v1",
      "model": "your-served-model",
      "max_tokens": 8192,
      "context_window": 131072
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

Custom headers are available for routing or tenancy:

```json
{
  "providers": {
    "gateway": {
      "type": "openai-compatible",
      "base_url": "https://gateway.example.com/v1",
      "api_key_env": "GATEWAY_KEY",
      "headers": {
        "X-Organization": "${GATEWAY_ORG}"
      },
      "model": "coding-model"
    }
  }
}
```

### Anthropic and compatible endpoints

```sh
export ANTHROPIC_API_KEY='your-key'
```

```json
{
  "schema_version": 1,
  "default_provider": "anthropic",
  "providers": {
    "anthropic": {
      "type": "anthropic",
      "base_url": "https://api.anthropic.com",
      "api_key_env": "ANTHROPIC_API_KEY",
      "model": "your-claude-model-id",
      "max_tokens": 8192,
      "context_window": 200000
    }
  }
}
```

The adapter adds `/v1` when the base does not already end with it, sends
Messages requests to `/v1/messages`, and uses `x-api-key` by default. For a
compatible endpoint expecting OAuth-style authorization, add:

```json
{
  "auth": "bearer"
}
```

### Native Amazon Bedrock

The native adapter calls the regional Bedrock Runtime `ConverseStream`
endpoint and supports model tool use, streaming text, provider reasoning,
usage, and correctly paired tool-result messages.

Always set the intended region explicitly. If omitted, Collomia uses
`us-east-1` for the endpoint. The model ID may be a foundation model ID,
inference profile ID, or other identifier accepted by Bedrock Runtime for your
account and region.

#### SigV4 and the AWS credential chain

Recommended configuration:

```json
{
  "providers": {
    "bedrock": {
      "type": "bedrock",
      "auth": "sigv4",
      "region": "us-east-1",
      "model": "your-bedrock-model-id",
      "max_tokens": 8192,
      "context_window": 200000
    }
  }
}
```

The AWS SDK credential chain supports:

- `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`.
- Temporary credentials with `AWS_SESSION_TOKEN` as the required third value.
- Shared AWS configuration and credentials files.
- IAM Identity Center/SSO profiles.
- Assume-role and web-identity flows.
- ECS, EKS, and EC2 workload roles.

Temporary environment credentials:

```sh
export AWS_ACCESS_KEY_ID='...'
export AWS_SECRET_ACCESS_KEY='...'
export AWS_SESSION_TOKEN='...'
```

To force a named shared profile:

```json
{
  "providers": {
    "bedrock-sso": {
      "type": "bedrock",
      "auth": "sigv4",
      "region": "us-west-2",
      "profile": "development",
      "model": "your-bedrock-model-id"
    }
  }
}
```

For an SSO profile, authenticate it before starting Collomia:

```sh
aws sso login --profile development
```

SDK-managed temporary credentials retain the SDK's normal refresh behavior.
The IAM principal needs permission to invoke the chosen model, and the model
must be available/enabled in that region.

#### Bedrock API keys (bearer authentication)

Collomia accepts already-created short- or long-term Bedrock API keys:

```sh
export AWS_BEARER_TOKEN_BEDROCK='...'
```

```json
{
  "providers": {
    "bedrock-key": {
      "type": "bedrock",
      "auth": "bearer",
      "api_key_env": "AWS_BEARER_TOKEN_BEDROCK",
      "region": "us-west-2",
      "model": "your-bedrock-model-id",
      "max_tokens": 8192
    }
  }
}
```

Collomia sends either Bedrock key form identically as an HTTPS bearer token.
It does not mint or refresh expiring Bedrock API keys. Replace an expiring
value and restart Collomia. A bearer configuration does not use `profile`.

With `auth: "auto"` (or omitted), Collomia chooses bearer when `api_key`,
`api_key_env`, or `AWS_BEARER_TOKEN_BEDROCK` is present; otherwise it chooses
SigV4. Prefer explicit `sigv4` or `bearer` when both credential families exist
and deterministic selection matters.

#### Bedrock Mantle / Responses

This adapter is separate from native ConverseStream. It posts an OpenAI
Responses-style request to `<base_url>/responses`, requests SSE, and also
accepts synchronous JSON from a compatible endpoint:

```json
{
  "providers": {
    "mantle": {
      "type": "bedrock-mantle",
      "base_url": "https://bedrock-mantle.us-west-2.api.aws/v1",
      "api_key_env": "AWS_BEARER_TOKEN_BEDROCK",
      "model": "openai.gpt-oss-120b",
      "max_tokens": 8192
    }
  }
}
```

### Azure OpenAI and Microsoft Foundry

Azure provider types support three authentication modes:

- `api_key`: the default when `auth` is omitted; sends the key in `api-key`.
- `bearer`: sends a caller-supplied bearer token from `api_key` or
  `api_key_env`. Collomia cannot refresh it.
- `entra`: uses the Azure Identity SDK's `DefaultAzureCredential`, caches the
  token only in memory, and refreshes it proactively. Do not configure an API
  key or authentication header in this mode.

#### Azure OpenAI with an API key

```sh
export AZURE_OPENAI_API_KEY='...'
```

```json
{
  "providers": {
    "azure-openai": {
      "type": "azure-openai",
      "base_url": "https://your-resource.openai.azure.com",
      "auth": "api_key",
      "api_key_env": "AZURE_OPENAI_API_KEY",
      "deployment": "gpt-deployment",
      "api_version": "2025-04-01-preview",
      "model": "gpt-deployment",
      "max_tokens": 8192,
      "context_window": 200000
    }
  }
}
```

The adapter constructs:

```text
<base_url>/openai/deployments/<deployment>/chat/completions?api-version=<api_version>
```

If `deployment` is absent, the selected model is used. If `api_version` is
absent, the code default is `2024-10-21`; set an explicit version supported by
your deployment when using preview or reasoning models.

#### Azure OpenAI with Microsoft Entra

For local Azure CLI authentication:

```sh
az login
```

```json
{
  "providers": {
    "azure-openai-entra": {
      "type": "azure-openai",
      "base_url": "https://your-resource.openai.azure.com",
      "auth": "entra",
      "deployment": "gpt-deployment",
      "api_version": "2025-04-01-preview",
      "model": "gpt-deployment"
    }
  }
}
```

The default Entra scope is
`https://cognitiveservices.azure.com/.default`. The identity normally needs
the **Cognitive Services OpenAI User** data-plane role on the Azure OpenAI
resource. Role assignments can take several minutes to propagate.

For a service principal with a client secret:

```sh
export AZURE_CLIENT_ID='...'
export AZURE_TENANT_ID='...'
export AZURE_CLIENT_SECRET='...'
```

For a user-assigned managed identity, set its `AZURE_CLIENT_ID`. Workload
identity, managed identity, Azure CLI, Azure Developer CLI, and Azure
PowerShell are also available through `DefaultAzureCredential`. Set
`AZURE_TOKEN_CREDENTIALS=prod` to restrict the chain to production-style
credentials, `dev` for developer credentials, or a supported credential name
when deterministic selection is important.

#### Microsoft Foundry OpenAI/v1

```sh
export AZURE_FOUNDRY_API_KEY='...'
```

```json
{
  "providers": {
    "foundry-gpt": {
      "type": "azure-foundry",
      "base_url": "https://your-resource.services.ai.azure.com/openai/v1",
      "auth": "api_key",
      "api_key_env": "AZURE_FOUNDRY_API_KEY",
      "model": "your-deployment",
      "max_tokens": 8192,
      "context_window": 200000
    }
  }
}
```

If `base_url` does not contain `/openai/v1`, Collomia appends it. The adapter
then posts to `/chat/completions`.

The same endpoint can use Entra:

```json
{
  "providers": {
    "foundry-gpt": {
      "type": "azure-foundry",
      "base_url": "https://your-resource.services.ai.azure.com/openai/v1",
      "auth": "entra",
      "model": "your-deployment"
    }
  }
}
```

The Foundry default Entra scope is `https://ai.azure.com/.default`; the
typical data-plane role is **Cognitive Services User**.

#### Microsoft Foundry Claude

```sh
export AZURE_FOUNDRY_CLAUDE_KEY='...'
```

```json
{
  "providers": {
    "foundry-claude": {
      "type": "azure-foundry-anthropic",
      "base_url": "https://your-resource.services.ai.azure.com/anthropic",
      "auth": "api_key",
      "api_key_env": "AZURE_FOUNDRY_CLAUDE_KEY",
      "model": "your-claude-deployment",
      "max_tokens": 8192,
      "context_window": 200000
    }
  }
}
```

Collomia ensures the base ends in `/anthropic`, then uses the Anthropic
Messages route beneath it. `auth: "entra"` is also supported with the Foundry
scope and refresh behavior described above.

#### Tenant, authority, and scope overrides

For multi-tenant or sovereign/private Azure environments:

```json
{
  "providers": {
    "foundry-sovereign": {
      "type": "azure-foundry",
      "base_url": "https://your-private-endpoint/openai/v1",
      "auth": "entra",
      "entra_tenant_id": "your-tenant-id",
      "entra_authority_host": "https://login.microsoftonline.us/",
      "entra_scope": "https://your-documented-audience/.default",
      "model": "your-deployment"
    }
  }
}
```

The authority must be an HTTPS origin without path/query/credentials. The
scope must be an absolute HTTPS URL ending in `/.default`. Collomia does not
guess sovereign-cloud audiences. Private endpoints work when DNS, routing,
and the OS TLS trust store are configured correctly.

#### Azure reasoning-model request compatibility

Some Azure/OpenAI reasoning deployments reject `max_tokens` and require
`max_completion_tokens`; some also reject non-default `temperature`. Older and
compatible services may require the original fields. Collomia therefore does
not guess from the model name. It sends the compatible request first and only
rebuilds/retries after an explicit HTTP 400 names the unsupported parameter and
required replacement. The accepted shape is remembered for that active
provider/model client. Unrelated 400 responses are returned unchanged.

### Provider resilience and errors

All built-in HTTP adapters share these behaviors:

- Network failures and HTTP 408, 429, 5xx, and 529 are retried up to three
  attempts using bounded exponential backoff, jitter, and `Retry-After`.
- Authentication, permission, not-found, and ordinary invalid-request 4xx
  failures are not retried.
- A request is retried only while its body can be replayed and before stream
  data has been accepted. In-stream failures are never replayed, avoiding
  duplicated output, tool calls, or billing.
- Failures are classified as `authentication`, `permission`, `rate_limit`,
  `invalid_request`, `not_found`, `timeout`, `unavailable`, `protocol`,
  `cancelled`, or `unknown`, with request IDs when supplied.
- Three consecutive transient failures open a 30-second circuit. One recovery
  probe can close it. `/status` displays the current provider health.

Timeouts are separate so a slow generation does not need an unlimited network
connection:

```json
{
  "providers": {
    "slow-local": {
      "type": "openai-compatible",
      "base_url": "http://127.0.0.1:8000/v1",
      "model": "large-model",
      "connect_timeout_seconds": 10,
      "request_timeout_seconds": 1800,
      "stream_idle_timeout_seconds": 600
    }
  }
}
```

Increase idle time only when a model legitimately emits nothing for longer
than five minutes. Use `collo doctor`, `/models`, and `--debug` to distinguish
credentials, endpoint construction, timeouts, and model capability problems.

## Permissions and safety

Collomia evaluates every tool action as read, write, execute, or external.
The permission engine is an in-process policy layer. Only an enabled OS sandbox
adds an operating-system boundary around shell/background commands.

### Autonomy modes

| Mode | Workspace reads | Workspace writes | Shell/background commands | Outside workspace | MCP/external calls |
| --- | --- | --- | --- | --- | --- |
| `ask` | Automatic | Prompt | Prompt | Prompt, only if outside access is enabled | Prompt |
| `workspace` | Automatic | Automatic | Prompt | Prompt, only if outside access is enabled | Prompt |
| `autopilot` | Automatic | Automatic | Automatic, subject to hard checks | Automatic only when outside access is enabled | Prompt unless explicitly allowed |

Select a persistent default:

```json
{
  "permissions": {
    "mode": "ask"
  }
}
```

Override it for one process:

```sh
collo --autonomy workspace
collo --autopilot
```

Or switch the running TUI with `/autonomy ask`, `/autonomy workspace`, or
`/autonomy autopilot`.

The following constraints hold in every mode:

- A command matching `denied_commands` is refused at execution.
- A command classified with a catastrophic outcome is refused without an
  approval option.
- A destructive command classified for one-time confirmation always prompts;
  autopilot, allow rules, and session grants cannot skip it.
- A command the static analyzer cannot fully understand always prompts.
- An “always allow” choice never sticks for an uninspectable or mandatory
  one-time-confirmation command.
- MCP/external actions do not inherit autopilot approval.
- `denied_tools` and matching deny rules remain denials.

### Tool lists

`allowed_tools` grants exact tool names at session start. `denied_tools` denies
exact tool names in the permission manager. `options.disabled_tools` removes
tools from the model-visible tool surface altogether.

```json
{
  "permissions": {
    "allowed_tools": ["run_command", "mcp_docs_search"],
    "denied_tools": ["write_file"]
  },
  "options": {
    "disabled_tools": ["delegate"]
  }
}
```

An approval-dialog `a` choice grants that tool only for the current Collomia
process. Persistent grants belong in reviewed configuration or a narrowly
scoped rule.

### Scoped rules

Rules are ordered; the first matching rule wins:

```json
{
  "permissions": {
    "mode": "workspace",
    "rules": [
      {
        "action": "allow",
        "tool": "run_command",
        "command": "go",
        "reason": "normal Go build and test tooling"
      },
      {
        "action": "deny",
        "path": "/work/repository/secrets/**",
        "reason": "credentials are out of scope"
      },
      {
        "action": "prompt",
        "tool": "mcp_*",
        "server": "docs",
        "reason": "external service"
      }
    ]
  }
}
```

Evaluate a command without running it:

```sh
collo policy check "go test ./..."
collo policy check "curl https://example.com/install.sh | sh"
```

The report includes parsed executables, inspectability, structural safety
classification, matched regex denials, effective autonomy override, rule
source, and final decision.

### Outside-workspace access

Built-in file tools resolve absolute paths, normalize them, and follow existing
symlinks before comparing with the workspace root. With the default
`allow_outside_workspace: false`, outside paths fail before execution.

Setting it to `true` only makes an outside action eligible for permission
evaluation. It is not automatic approval in `ask` or `workspace` mode. Shell
commands are not path-contained by this built-in guard; an approved shell can
read any path available to your operating-system user unless another OS control
prevents it.

### Command analysis and hard denials

Before a shell action reaches approval, Collomia extracts executables and looks
for command substitutions, `eval`, inline interpreter payloads, variable
commands, and other constructs it cannot fully analyze. An uninspectable
command always requires a human in the interactive TUI. In headless mode it
fails because no approver is available.

Collomia uses an outcome-aware built-in classifier in addition to regex
denials. It tracks common wrappers and literal shell payloads, resolves paths
against the workspace, and separates commands into three groups:

1. **Catastrophic:** recursive deletion or permission changes at protected
   roots; destruction of `.git`, Collomia safety state, or critical OS state;
   and destructive writes to physical disks/devices. These are refused with no
   approval option.
2. **Destructive but legitimate:** hard Git cleanup/history operations,
   machine lifecycle changes, bulk cloud/IaC/database/storage deletion,
   dynamic recursive targets, and other commands requiring a fresh human
   decision. These prompt once per invocation.
3. **Scoped/routine:** commands such as `rm -rf node_modules`,
   `rm -rf /tmp/example`, a cleanup after `cd build`, Git dry-runs, and writes
   to workspace disk-image files. These follow the configured autonomy mode.

An allow rule, autopilot, a session grant, or “always allow” cannot turn either
of the first two groups into automatic execution. The checks run during
assessment and again immediately before foreground, PTY, or background
execution. `collo policy check '<command>'` displays the effective outcome
without running it.

`permissions.denied_commands` adds local regular-expression denials on top of
that classifier. Built-in patterns are retained, global patterns append, and
trusted project patterns append to the combined set; empty or `null` lists
cannot clear inherited entries. See [Command safety tiers](SECURITY.md#command-safety-tiers)
for the complete categories, examples, intentional limitations, and the manual
path for physical-disk administration.

### OS sandboxing

Enable compatibility-first containment while preserving package installation,
online documentation CLIs, command networking, and dependency reads outside
the workspace:

```json
{
  "permissions": {
    "sandbox": "auto",
    "sandbox_allow_network": true,
    "sandbox_allow_read_outside_workspace": true,
    "command_env": "minimal"
  }
}
```

Sandboxing is `off` unless configured. `sandbox_allow_network` and
`sandbox_allow_read_outside_workspace` default to `true`. Changing only
`sandbox` to `auto` therefore adds write/process containment without blocking
package downloads or dependencies stored in a user cache. Set either switch
to `false` to deliberately request network denial or user-data read
confinement. Package managers can still require a readable dependency store,
writable cache, or environment-provided credentials; the examples below cover
all three cases.

Use `"require"` for fail-closed operation: if the backend is unavailable or
cannot enforce every requested write/read/network protection, it refuses to run.
`"auto"` applies all available protections and emits a visible degradation
warning in command output, `collo doctor`, and `/status` when a protection is
missing.

The sandbox applies to `run_command`, `start_process`, and PTY commands,
including those invoked from a skill. It does not wrap Collomia's own provider
HTTP, configured hooks, MCP processes/connections, or configured language
servers. Those are separately controlled through configuration trust,
`trusted` MCP flags, and the permission model for MCP tools.

Platform behavior:

- **macOS/Seatbelt:** writes are limited to the workspace, temporary
  directories, and `/dev`. When outside-workspace reads are disabled,
  file-content reads in user homes and mounted volumes are denied except for
  the workspace, temp/writable roots, PATH entries, and explicit readable
  roots. Public system runtime files and path metadata remain visible. With
  network disabled, remote egress is denied but loopback remains available.
- **Linux/Landlock:** kernel 5.13+/ABI v1 applies filesystem rules, but ABI v3
  (Linux 6.2) is recommended because ABI v1–v2 cannot deny standalone
  truncation. Writes are otherwise confined to the workspace and
  temporary/device helper paths. Read confinement adds a deny-by-default
  Landlock view with grants for the workspace, temp/writable roots,
  conventional system runtime/configuration paths, PATH entries, and explicit
  readable roots. ABI v4+ can deny TCP connect/bind; ABI v10+ can also deny UDP
  bind/connect/send, including DNS. Older kernels cannot enforce the network
  setting, and ABI v4–v9 remains TCP-only. Consequently, `require` plus network
  denial fails closed on ABI v4–v9 but succeeds with full TCP/UDP isolation on
  ABI v10+; `auto` applies and clearly reports the capability available. See
  [Linux sandbox setup and Landlock compatibility](LINUX_SANDBOX.md) for the
  ABI feature matrix, Ubuntu 26.04 specifics, verification and custom-kernel
  steps, configuration recipes, and container/WSL troubleshooting.
- **Windows 11/AppContainer:** a workspace-specific, low-integrity
  AppContainer restricts filesystem, registry, credentials, devices, network,
  and access to other processes. A kill-on-close Job Object owns the complete
  child tree. The workspace, temp directory, and `sandbox_writable_roots` are
  writable; `sandbox_readable_roots` and user-local PATH directories are
  read/execute only. AppContainer always confines user-data reads even though
  the compatibility switch defaults to broad reads on macOS/Linux. This uses
  inbox Windows APIs and requires no Windows Sandbox feature, Hyper-V, driver,
  service, administrator setup, or additional installation.

The network setting is deliberately command-specific. It does not block model
providers, remote MCP servers, hooks, or LSP processes. On Windows, allowing
network adds Internet and private-network AppContainer capabilities, but
Windows still blocks AppContainer loopback to an ordinary unpackaged localhost
server without an administrator-created exemption. Collomia does not request
that exemption; use `sandbox: "off"` for a command that must access such a
local development service.

Dependency stores and build/package caches outside the workspace may need
narrow explicit grants:

```json
{
  "permissions": {
    "sandbox": "auto",
    "sandbox_allow_network": true,
    "sandbox_allow_read_outside_workspace": false,
    "sandbox_readable_roots": ["${HOME}/go/pkg/mod"],
    "sandbox_writable_roots": [".collomia-cache", "${HOME}/.cache/my-build"]
  }
}
```

Relative entries resolve from the workspace and environment references expand
at command time. Writable roots are automatically readable. Prefer an
immutable dependency root or tool-specific cache over granting an entire home
directory. Read [Security model](SECURITY.md) for the precise platform
boundary—including macOS metadata/system-runtime visibility and Linux
pseudo-filesystem limits—before using autopilot with untrusted repositories or
instructions.

### Command environment

`command_env: "full"` passes the parent environment to shell and background
commands. `"minimal"` keeps only basics such as `PATH`, `HOME`, user/shell/temp,
locale, terminal, and essential Windows command variables. When `command_env`
is omitted and sandboxing is `auto` or `require`, Collomia automatically uses
the minimal environment.

Minimal mode can cause builds that require proxy variables, package-registry
tokens, compiler flags, or cloud credentials to fail. Prefer adding a narrow
purpose-built wrapper or explicitly opting into `full` only when needed.

### External reviewer

`reviewer_command` runs before any non-read action that would otherwise be
auto-approved. It receives JSON on stdin:

```json
{
  "tool": "run_command",
  "summary": "run: go test ./...",
  "risk": "execute",
  "resources": ["exec:go"],
  "uninspectable": false
}
```

A successful response of `{"decision":"deny","reason":"..."}` or a
reviewer failure/non-zero exit escalates the action to the interactive prompt.
The reviewer never turns a prompt or denial into an approval. The reviewer
command is interpreted through `/bin/sh -c` on Unix or `cmd.exe /c` on Windows
and has a 30-second bound.

### Repository trust

A `.collomia.json` file can introduce providers, looser permissions, hook
executables, MCP servers, LSP processes, and agent profiles. Collomia does not
apply it until its exact contents have been reviewed and approved:

```sh
collo config validate --strict
collo trust
collo trust --status
collo trust --revoke
```

Trust is stored outside the repository and bound to the SHA-256 of the project
configuration. Editing the file changes its hash and quarantines the entire
project layer again. While quarantined, the global configuration remains
active and startup reports which project capabilities were ignored.

Project instructions and project skills are loaded only when the runtime's
project-trust state is active. A workspace with no `.collomia.json` has no
configuration file to approve, so `collo trust --status` reports that trust is
not required. If a repository includes project instructions or skills and you
want an explicit trust boundary for them, include and review a project
`.collomia.json` before use.

### Audit ledger

Every privileged permission decision and execution outcome is appended as
redacted JSONL to a per-workspace ledger outside the repository. Records include
the tool, risk, summary, normalized resources, allow/deny source, matched rule,
and eventual result. Audit failures do not stop the agent loop; `collo doctor`
helps identify state-directory problems.

## Using the terminal interface

Run `collo` from the repository root. The chosen working directory is the
workspace boundary used by file tools, Git inspection, configuration, trust,
skills, sessions, and hooks. Select a different directory without changing
your shell's location:

```sh
collo --cwd /path/to/repository
```

### Main interface

The interactive UI has Chat, Session, and Help tabs. Chat contains the streamed
conversation and tool results. Session shows the structured plan, changed
files, a parent/child delegated-agent tree with bounded recent output,
background processes, Git branch/upstream and working-tree counts,
provider/sandbox/MCP/trust health with concise recovery actions, and bounded
recent activity. Git inspection is read-only,
runs asynchronously with a short timeout, and reports non-Git workspaces
normally; press `r` in the Session tab to refresh it. `/agents` provides a
searchable view of each retained delegated outcome, and `alt+a` opens explicit
inspect, steer, and stop actions for one active child while its parent turn is
still running. Help lists commands,
providers, tools, skills, MCP servers, themes, and keybindings.

Markdown is rendered in the active theme. Fenced source code, expanded
`read_file` output, Git diffs, and approval previews are syntax-highlighted.
Tool output is initially compact and can be expanded without leaving the
conversation.

`/activity` opens the bounded in-memory operator timeline retained for the
current session: turns, completed tool events, permission decisions, file
changes, plan updates, delegated-agent state, context compaction, warnings,
and failures. It is projected from the same durable runtime events used by
headless output and session recovery; opening it does not execute a tool,
contact a provider, or replay prior work. The UI retains at most the newest
500 projected entries, while the append-only session JSONL remains the durable
record. Press `f` to cycle only categories present, `/` to search, `n`/`N` to
move between matches, arrow/page keys to navigate, and `y` to copy the selected
failure ID (or the selected activity text when no failure ID exists).

The layout adapts to narrow terminals: below 44 columns the header shows only
the active tab, status content is truncated rather than wrapping into the
composer, and full-screen transcript/activity/diff views use the available
rows. The 80x24 layout is a supported baseline. Resizing preserves a manually scrolled
chat position; new streaming output no longer pulls you to the bottom. Press
`end` to resume live follow.

### Keyboard reference

| Key | Action |
| --- | --- |
| `enter` | Send the prompt or run the selected palette item. |
| `alt+enter` | Insert a newline in the prompt. |
| `/` | Open/filter the slash-command palette. |
| `@` | Fuzzy-pick a workspace file or folder and insert its safely quoted path. |
| `up` / `down` | Move in palettes/pickers; at the first or last composer line, navigate this session's prompt history. Multiline input retains normal cursor movement. |
| `tab` | Complete the selected command/palette value. |
| `ctrl+t` | Cycle Chat, Session, and Help. |
| `alt+s` | Open the saved-session picker without replacing the current draft. |
| `alt+a` | Inspect an active delegated agent, prepare steering guidance, or explicitly stop it without stopping siblings or the parent. |
| `ctrl+o` | Expand or collapse finished tool output. |
| `ctrl+y` | Open the full-screen transcript search/copy view. |
| `ctrl+d` | Open the interactive session diff viewer. |
| `f` in Activity | Cycle the activity categories present in this session. |
| `/`, then `n` / `N` in Activity | Search activity and move between matches. |
| `y` in Activity | Copy the selected failure ID, or the activity text when no ID is present. |
| `r` in Session | Refresh the asynchronous Git workspace summary. |
| `page up` / `page down` | Scroll the transcript. |
| `home` / `end` | Jump to the top or bottom; `end` resumes live follow. |
| `esc` | Dismiss a palette/picker or cancel the active turn. |
| `ctrl+c` | Cancel the active turn; press again to quit. |

Typing `/` filters commands by prefix and substring. Known first arguments for
`/theme`, `/autonomy`, `/plan`, and `/model` are completed fuzzily. These menus
remain beside the composer; approvals and questions open as centered,
theme-aware transient dialogs.

While a provider turn is running, the composer remains available for a small
local-control command lane: `/help`, `/status`, `/context`, `/tasks`, `/tools`,
`/config`, `/attachments`, `/transcript`, `/activity`, `/diff`, read-only
`/ps`, and `/agents` inspect/steer/stop. Free-form text and unavailable commands remain in
the composer as unsent drafts; they are not queued to the model or executed
concurrently. If the agent asks a question, Collomia preserves and restores the
draft around the question dialog.

The global actions in this table are configurable. The Help tab always shows
the effective bindings after defaults, user configuration, and project
configuration are merged. See [Terminal behavior and keybindings](#terminal-behavior-and-keybindings).

### Slash commands

| Command | Purpose |
| --- | --- |
| `/help` | Show slash commands and keybindings. |
| `/status` | Show workspace, provider/model, capabilities, health, context, plan, autonomy, and configuration/trust state. |
| `/model [provider[/model]]` | Pick or switch the provider/model. A bare provider selects its configured model. |
| `/models` | Inspect configured provider defaults, capabilities, constraints, and live catalog availability. |
| `/context` | Show token usage, estimated active context, message counts, pinned plan state, summaries, retained-result storage, and context composition. |
| `/plan [on\|off]` | Toggle the read-only plan tool surface. |
| `/tasks` | Show the structured plan. |
| `/autonomy [mode]` | Show or set `ask`, `workspace`, or `autopilot`. |
| `/theme [name]` | Pick or switch themes for this process. |
| `/skills` | Pick a skill and prefill a prompt that asks the agent to use it. |
| `/skills list` | List active and disabled skills. |
| `/agents` | Search and inspect current or persisted delegated tasks. |
| `/agents stop <id-or-name>` | Cancel one queued or active child without cancelling siblings or the parent. |
| `/agents steer <id> <guidance...>` | Queue bounded guidance for the child's next model boundary. It never answers an approval or grants permission. |
| `/agents apply <id>` | Review files/hunks from a retained write worktree and integrate selected safe text changes after permission and drift checks. Run only while the parent is idle. |
| `/prompt [workspace-file]` | Load a UTF-8 text file into the composer for review; omit the path for a fuzzy file picker. |
| `/attach [workspace-image]` | Attach a PNG, JPEG, GIF, or WebP to the pending prompt; omit the path for a fuzzy image picker. |
| `/attachments` | List images attached to the pending prompt. |
| `/detach <number\|all>` | Remove one or every pending image attachment. |
| `/mcp ...` | Browse/manage MCP servers, resources, and prompts. |
| `/tools` | List every tool currently registered. |
| `/review [ref] [instructions...]` | Review uncommitted changes or changes relative to a ref, with an optional focus. |
| `/verify [focus]` | Detect and run project verification commands, recording plan results. |
| `/diff` | Open the interactive session diff viewer. |
| `/transcript` | Open the complete raw transcript for search, navigation, and copy. |
| `/activity` | Search and category-filter the bounded event-derived session timeline; `y` copies a selected failure ID when present. |
| `/undo` | Revert the most recent tracked agent file change when the file has not diverged externally. |
| `/ps` | List background processes. |
| `/ps stop <id>` | Stop one background process and its descendants. |
| `/sessions` | Fuzzy-pick and switch to another durable session in place. |
| `/rewind [turn]` | Branch safely from an earlier completed turn; omit the turn for a picker. The source conversation and workspace remain unchanged. |
| `/retry` | Load the previous prompt into the composer for review. It does not submit the prompt or repeat tools. |
| `/new` | Start a new session while preserving the current one. |
| `/compact [focus]` | Summarize older active context while preserving the durable transcript. |
| `/config` | Show the active configuration source. |
| `/clear` | Clear active conversation context. It does not delete the durable session file. |
| `/quit` or `/exit` | Exit. |

### Workspace paths and prompt files

Typing `@` at a word boundary opens one fuzzy picker containing workspace
files and folders. Selecting a path inserts it into the prompt; paths with
spaces or quotes are quoted automatically, and folders end in `/`. This is a
reference for the model, not an eager attachment: the agent reads only the
files it needs through normal workspace tools, with their output bounds and
permission policy.

`/prompt` is for a text file whose contents should become the prompt itself.
With no argument it opens a file picker. With an argument it accepts a
workspace-relative or absolute-within-workspace path, including the common
forms pasted by terminals:

```text
/prompt prompts/review.md
/prompt "prompt files/release review.md"
/prompt prompt\ files/release\ review.md
/prompt file:///workspace/prompt%20files/release%20review.md
```

You can also type `/prompt ` and drag a workspace file into a terminal that
pastes dropped paths. Collomia resolves symlinks and refuses paths outside the
active workspace. It reads only regular UTF-8 text files, refuses terminal
control characters, strips an optional UTF-8 byte-order mark, and caps input
at 256 KiB. The composer receives a
source header plus the file contents; review or edit both before pressing
enter. For a larger source file, mention it with `@` and let the agent inspect
bounded portions instead.

### Image attachments

Use `/attach` when the model should receive an image itself rather than a text
reference to a workspace path:

```text
/attach screenshots/failure.png
/attach "design references/dialog state.webp"
/attach file:///workspace/screenshots/failure.png
```

With no path, `/attach` opens a fuzzy picker containing PNG, JPEG, GIF, and
WebP files. You can also type `/attach ` and drag an image into terminals that
paste the dropped path. Paths are parsed directly—no shell evaluates them—and
may be quoted or use backslash-escaped spaces. Images must resolve to regular
files inside the active workspace; symlink escapes and outside paths are
refused.

The status bar shows an `images N` badge while a prompt has pending images.
Run `/attachments` to review their names, types, and sizes, `/detach 2` to
remove one, or `/detach all` to remove them all. Pending images follow the
unsent draft when you switch sessions inside the running TUI. Like the text
draft, an unsent selection is not durable across application restarts.

Collomia accepts at most four images per prompt, 5 MiB per image, and 24 MiB
of retained images per session. The selected file is inspected when attached,
then reopened through a workspace-rooted handle and copied into owner-only
session storage only after the prompt is sent and its `user_prompt` hook
accepts the turn. A path or symlink swap cannot redirect that final read
outside the workspace, and a hook-blocked prompt leaves no attachment blob. The
session record stores a random reference, media type, size, and SHA-256 digest
rather than base64 bytes; Collomia rechecks size, type, and digest before every
provider request. Fork copies the attachments, rewind copies only references
reachable from retained turns, and session deletion removes them.

Image capability is adapter- and model-dependent. OpenAI/compatible Chat
Completions, Azure OpenAI/Foundry OpenAI, Anthropic/Foundry Claude, Bedrock
ConverseStream, and Responses/Mantle adapters encode user images, but their
capability remains `partial` because a selected model or compatible gateway
may still be text-only. A known text-only adapter is rejected before network
I/O; a partial endpoint can return its normal provider error. GIF and WebP are
accepted by Collomia, though individual models may support a smaller format
set. Audio, video, PDFs, SVG, raw clipboard image protocols, and image
generation are not part of this workflow.

Provider-reported usage replaces Collomia's approximation after a request.
Until then, `/context` visibly reserves roughly 1,000 tokens per image; actual
image token accounting varies by provider, model, resolution, and detail mode.

### Approval dialogs

Writes, commands, outside-workspace paths, MCP calls, and other privileged
actions prompt according to the active policy. The dialog shows the tool,
summary, reason, normalized resources, and a colored diff when available.

| Key | Approval action |
| --- | --- |
| `y` or `enter` | Allow once. |
| `a` | Allow and auto-approve this exact tool for the remainder of the process. |
| `n` or `esc` | Deny. |
| `h` | For a `write_file` proposal with at least two hunks, enter hunk review. |

In hunk review, `up`/`down` or `j`/`k` navigate, `space` includes/excludes the
current hunk, `a` includes all, `enter` applies selected hunks, and `esc`
returns to the whole-file approval. Hunk selection currently applies only to
`write_file`; `edit_file` and atomic multi-file `apply_patch` remain file-level.

Question dialogs let the agent or an MCP server pause a tool call for typed
input. Review the question and any enumerated choices. Escape declines instead
of inventing an answer.

### Transcript search and copy

Press `ctrl+y` (or run `/transcript`) to open a full-screen,
selection-friendly view of every user message, assistant response, tool
call/result, error, and informational panel in the current TUI transcript. It
uses raw Markdown and the full retained tool-result block rather than the
collapsed Chat rendering.

| Transcript key | Action |
| --- | --- |
| `[` / `]` or left/right | Select the previous/next transcript block. |
| up/down or `j`/`k` | Scroll one line. |
| configured page/top/bottom keys | Scroll a page or jump to an edge. |
| `/` | Enter a case-insensitive search; `enter` runs it. |
| `n` / `N` | Move to the next/previous matching block. |
| `y` | Copy the selected block's content. |
| `Y` | Copy the complete transcript. |
| `esc` or `q` | Return to Chat. |

Copy uses the standard OSC 52 terminal clipboard sequence and is capped at 100
KiB. It requires no platform helper, but the hosting terminal may disable
clipboard writes or ask for confirmation; terminals do not acknowledge the
request. In tmux 3.3+, enable `allow-passthrough` when OSC 52 does not reach the
outer terminal. If clipboard integration is unavailable, start with
`--no-alt-screen` and use normal terminal selection/scrollback instead.

### Interactive diff review

`/diff` or `ctrl+d` opens a full-screen browser over files changed by the agent
in this session. It is a read-only review surface: approving or selectively
applying a pending write still happens through the permission dialog and its
existing `h` hunk-review action, so the diff viewer cannot bypass policy,
auditing, change tracking, or undo.

The viewer starts side-by-side at 108 columns or wider and uses unified diff at
narrower widths. A resize below that threshold switches safely to unified
mode. Unchanged regions are folded with three context lines by default.

| Diff key | Action |
| --- | --- |
| `[` / `]` or left/right | Previous/next changed file. |
| `n` / `N` | Next/previous change hunk. |
| up/down or `j`/`k` | Scroll one line. |
| configured page/top/bottom keys | Scroll a page or jump to an edge. |
| `u` | Use unified view. |
| `s` | Use side-by-side view when at least 108 columns are available. |
| `f` | Fold/unfold unchanged regions. |
| `e` | Open the current file at the selected hunk in the configured external editor. |
| `esc` or `q` | Close the viewer. |

Headers show the relative file path, file position, additions/deletions, and
active layout. Both layouts use theme-aware addition/deletion colors; unified
view shows hunk headers, while side-by-side view shows old/new line numbers.

#### External editor handoff

The `e` action suspends the terminal UI, runs an editor directly without a
shell, and restores Collomia when that process exits. It refuses any target
whose resolved path is outside the active workspace. The diff is read again on
return, so changes made in the editor appear immediately; if the editor restores
the file to its original state, the empty review closes normally.

Configure an editor under `options`. `{file}`, `{line}`, and `{column}` are
replaced in individual arguments. When no argument contains `{file}`, Collomia
appends the absolute file path:

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

For a terminal editor:

```json
{
  "options": {
    "editor": {
      "command": "nvim",
      "args": ["+{line}", "{file}"]
    }
  }
}
```

When `options.editor.command` is omitted, Collomia tries `VISUAL`, then
`EDITOR`, as a whitespace-separated executable and argument list. Shell
operators are not interpreted. Use the JSON argument form for executable paths
or arguments containing spaces. GUI editor commands that return immediately
also return immediately to Collomia; add the editor's wait flag when you want
the TUI to remain suspended until the file is closed.

### Terminal behavior and keybindings

Collomia uses the alternate screen by default, leaving the terminal exactly as
it was when the TUI exits. To retain the final frame in native scrollback:

```sh
collo --no-alt-screen
```

Persist the preference, or force the default for one invocation with
`--alt-screen`:

```json
{
  "options": {
    "alternate_screen": false
  }
}
```

Animations also remain enabled by default. To replace the decorative working
spinner with a static marker:

```json
{
  "options": {
    "reduced_motion": true
  }
}
```

This setting affects only decorative progress motion. The composer remains
editable, the busy-safe slash-command lane remains available, and cancellation,
approvals, questions, and agent controls behave exactly as they do with the
animated indicator.

Global navigation keys can be remapped by action. Each omitted action inherits
its earlier/default binding, so a project may override just one user binding.
Supported values are `ctrl+letter`, `alt+letter`, `f1` through `f12`, `pgup`,
`pgdown`, `home`, and `end`. Duplicate global bindings fail configuration
validation. Approval `y`/`a`/`n`, question `enter`/`esc`, and keys shown inside
transcript/diff modes remain fixed so safety decisions and modal help stay
unambiguous.

```json
{
  "options": {
    "keybindings": {
      "agent_control": "alt+a",
      "next_tab": "alt+t",
      "toggle_tool_output": "ctrl+o",
      "transcript_view": "ctrl+y",
      "diff_view": "ctrl+d",
      "session_picker": "alt+s",
      "page_up": "pgup",
      "page_down": "pgdown",
      "scroll_top": "home",
      "scroll_bottom": "end"
    }
  }
}
```

Validate changes with `collo config validate --strict`, then inspect the Help
tab for the effective bindings.

### Shell completion

`collo completion` generates completion without requiring a shell plugin. For
the current shell session:

```sh
source <(collo completion bash)   # Bash
source <(collo completion zsh)    # Zsh
collo completion fish | source    # Fish
```

For persistent installation, save the generated script in the shell's normal
completion directory:

```sh
# Bash (make sure ~/.local/share/bash-completion/completions is loaded).
mkdir -p ~/.local/share/bash-completion/completions
collo completion bash > ~/.local/share/bash-completion/completions/collo

# Zsh: put this directory in fpath before compinit.
mkdir -p ~/.zfunc
collo completion zsh > ~/.zfunc/_collo

# Fish automatically loads this location.
mkdir -p ~/.config/fish/completions
collo completion fish > ~/.config/fish/completions/collo.fish
```

PowerShell:

```powershell
$Completion = Join-Path $HOME '.collomia\collo-completion.ps1'
collo completion powershell | Set-Content $Completion
. $Completion
# Add the preceding dot-source line to $PROFILE to load it in future shells.
```

### Themes and color

Available themes:

```text
collomia                 synthwave               outrun
blade-runner-2049        chaos-theory            cyberpunk-2077-blue
cyberpunk-2077-violet    catppuccin-mocha        gruvbox-dark
rose-pine-moon           kanagawa-wave           matrix
monokai                  dracula                 nord
tokyo-night              fredhutch-dark          fredhutch-light
plain
```

Persist one:

```json
{
  "options": {
    "theme": "chaos-theory"
  }
}
```

`plain` avoids color and uses borders, bold, and reverse video. Setting the
standard `NO_COLOR` environment variable forces `plain`, overriding config.

Color themes send OSC 11 so a capable terminal adopts a matching background;
Collomia restores the terminal default on exit. iTerm2, Ghostty, Kitty,
WezTerm, Alacritty, and Windows Terminal support it. Other terminals can ignore
it safely. For tmux 3.3+, enable passthrough when background switching is
desired:

```tmux
set -g allow-passthrough on
```

### Notifications

Approval prompts, questions, and turns lasting more than ten seconds can get
your attention with a bell and terminal desktop notification:

```json
{
  "options": {
    "notifications": "on"
  }
}
```

- `on`: terminal bell plus OSC 9 notification.
- `bell`: bell only.
- `off`: silent.

Notification visibility depends on the terminal and operating-system settings.
Unsupported terminals ignore OSC 9.

## Headless and automated use

### One-shot runs

Pass a prompt as arguments:

```sh
collo run "Explain the dependency graph"
```

Or pipe up to 4 MiB of prompt text on stdin:

```sh
git diff | collo run --plan "Review this patch"
```

Headless mode has no approval UI. Any action whose policy result is `prompt`
fails. Use planning mode for inspection, a narrowly scoped allow rule for a
known command/tool, or explicit autopilot when you genuinely intend writes and
execution:

```sh
collo run --plan "Inspect the repository and propose a plan"
collo run --autopilot "Fix the failing tests and verify the result"
```

Uninspectable commands still require interactive approval even in autopilot,
so they fail headlessly. This is intentional.

### JSONL event stream

`--jsonl` emits one schema-versioned JSON object per line on stdout:

```sh
collo run --jsonl --plan "Summarize the architecture" | jq .
```

The complete, integration-focused contract—including every field, stable exit
codes, failure classifications, ephemeral semantics, Bash/PowerShell pipeline
patterns, and compatibility rules—is in the [automation guide](AUTOMATION.md).
Print the exact schema embedded in your installed binary with:

```sh
collo schema events
```

Current one-shot runs emit these kinds as applicable:

```text
turn.start             text.delta             reasoning.delta
tool.call.delta        tool.start             tool.output
tool.result            permission.decision    usage
context.compaction     warning                error
turn.end               run.result
```

Schema v1 also reserves `session.start`, `permission.request`, `file.change`,
and `plan.update` kinds for consumers that share the event model; do not assume
those reserved kinds appear in every current CLI stream.

`tool.call.delta.arguments_delta` may be incomplete JSON while streaming.
Collomia does not execute it until the provider completes and the adapter
validates the final tool call.

The last line is always `run.result`:

```json
{
  "schema": 1,
  "kind": "run.result",
  "result": {
    "status": "ok",
    "answer": "...",
    "session_id": "20260719-120000-a1b2c3",
    "changed_files": ["main.go"],
    "duration_ms": 8412
  },
  "usage": {
    "input_tokens": 5210,
    "output_tokens": 644
  }
}
```

`status` is `ok`, `error`, or `cancelled`. Schema v1 adds optional structured
`failure`, `partial`, and `refused` fields without changing those established
status values. Error results make `collo` exit non-zero after writing the final
record. Exit codes are 0 for success, 1 for execution/provider failure, 2 for
usage/configuration failure, and 130 for cancellation. In shell pipelines,
enable `pipefail` if the pipeline's exit code must reflect Collomia rather than
the final parser:

```sh
set -o pipefail
collo run --jsonl --autopilot "Run tests" | tee run.jsonl | jq .
```

Retrieve only the final verdict from a saved stream:

```sh
tail -n 1 run.jsonl | jq '.result, .usage'
```

Failed and cancelled runs also carry one opaque correlation value at the event
level (`failure_id`) and inside `result.failure.id`. The same ID is shown by the
TUI and written to debug logs. It identifies one occurrence, not a category:
reproducing the same problem generates a new ID. The ID contains no prompt,
path, provider, session, or credential data and should not be parsed beyond
matching it across diagnostics.

Provider-originated error events include a `provider` object with kind, HTTP
status, retryability, retry delay, operation, and request ID when available.
Secret redaction is applied before lines leave the process.

Use `collo run --ephemeral` for a one-shot run that must not create or append a
durable conversation session. It cannot be combined with `--resume` or
`--continue`. Ephemeral runs still make permitted workspace changes and retain
the normal audit ledger; `--debug` still writes its explicitly requested log.
Combine `--ephemeral` with `--plan` when the task should also be read-only.

### Offline trace validation and replay

Validate a saved, completed headless stream before using it for support or
regression analysis:

```sh
collo replay --check run.jsonl
```

Render the same stream as a readable transcript, or read it from stdin:

```sh
collo replay run.jsonl
cat run.jsonl | collo replay -
```

Replay is observational. It does not load global or project configuration,
open a workspace session, connect to a provider or MCP server, or execute any
recorded tool. It requires exactly one terminal `run.result` as the final
event, validates known schema-v1 payloads plus turn/tool/result consistency,
and reports malformed records with their JSONL line number. Additive fields
are accepted; unsupported schema versions and event kinds are rejected rather
than guessed at.

Human rendering removes terminal control characters, keeps identifiers on one
line, visibly frames untrusted payload text, normalizes Windows line endings,
caps an individual rendered payload at 64 Ki characters, and applies
best-effort common-secret redaction. Normal `collo run --jsonl` output was
already scrubbed using configured secrets as well, but replay does not load
configuration and therefore cannot recognize arbitrary custom credentials in
an imported file. Inspect any trace before sharing it.

This command is for completed headless run streams, not the differently shaped
session-store or audit-ledger JSONL files. Full validation rules, exit behavior,
and limitations are documented in the [automation guide](AUTOMATION.md#validating-and-replaying-saved-traces).

### CI and scheduled automation examples

The [automation guide](AUTOMATION.md#complete-automation-examples) includes two
copy-ready, defensive examples:

- a GitHub Actions pull-request review that installs Collomia, runs a
  read-only ephemeral agent, preserves stdout/stderr separately, validates the
  final `run.result`, and fails CI on errors, partial work, or denied actions;
- a weekly cron maintenance report that defines the minimal cron environment,
  loads provider credentials from a mode-0600 file, retains JSONL and stderr,
  and writes the final answer and execution metadata to Markdown.

Both use `--plan` because unattended examples should not modify a checkout by
default. A CI job may deliberately use `--autopilot` in a disposable checkout,
but must still use narrow permission rules and retain the resulting diff for
human review. `--ephemeral` suppresses conversation persistence only; it does
not roll back changes or suppress the audit ledger.

### Code review

Review uncommitted changes:

```sh
collo review -
collo review - "Focus on concurrency and cancellation"
```

Review changes relative to a ref:

```sh
collo review main "Look for backward compatibility problems"
```

The review workflow instructs the agent to use read-only Git/file tools and
report findings by severity with exact paths and lines. Add `--plan` when you
want the tool surface itself restricted to planning/read-only tools:

```sh
collo review --plan main "Focus on security"
```

### Verification

`collo verify` detects commands from repository files, adds each command as a
plan step, runs it, and records the exact outcome. Because execution normally
prompts, headless verification needs an applicable allow rule/tool grant or an
explicit mode that auto-allows commands:

```sh
collo verify --autopilot
collo verify --autopilot "Only unit tests and lint"
```

Review detected commands and your permission configuration before using
autopilot in automation.

### Useful command-line reference

```text
collo [flags] [initial prompt]       interactive TUI
collo --web [flags] [initial prompt] browser-hosted local TUI
collo run [flags] <prompt>           one-shot/headless run
collo init [--with-reference]        create project config
collo init --global [--with-reference]
collo config show|validate|reference
collo trust [--status|--revoke]
collo doctor [--strict]
collo capabilities [--markdown]
collo support bundle [--output path] [--include-logs]
collo policy check <command...>
collo review [ref] [instructions...]
collo verify [focus]
collo sessions list|show|fork|rewind|rename|archive|unarchive|delete
collo skills list|show|new|install|update|remove|enable|disable
collo mcp list|show|add|remove|enable|disable|test
collo completion bash|zsh|fish|powershell
collo schema events
collo replay [--check] <trace|->
collo version
```

Common flags:

```text
--cwd <path>                         workspace
--provider <name>                    configured provider alias
--model <id>                         model/deployment override
--autonomy ask|workspace|autopilot   permission policy override
--autopilot                          shorthand for autopilot
--workspace                          shorthand for workspace mode
--plan                               read-only plan tool surface
--resume <id>                        resume a saved session
--continue                           resume the most recently updated session
--web                                local browser terminal (macOS/Linux)
--web-port <0..65535>                loopback port; 0/random by default
--no-open                            print web URL without opening a browser
--alt-screen                         force the alternate-screen TUI
--no-alt-screen                      retain the final TUI frame in scrollback
--jsonl                              JSONL output for `run`
--ephemeral                          skip durable session storage for `run`
--check                              validate and summarize a `replay` trace
--output <path>                      support-bundle archive path
--include-logs                       include bounded, redacted logs in a support bundle
--debug                              redacted debug log
--strict                             strict config/doctor validation
--global                             user scope for init/new/install/update
--with-reference                     write adjacent JSONC reference on init
--yes                                confirm destructive session/skill actions
```

## Tools and coding workflows

The model sees JSON Schema definitions for tools allowed by the current mode.
`/tools` reports the complete registered registry; `options.disabled_tools`,
planning mode, sub-agent restrictions, and the absence of an interactive
question broker can make the model-visible subset smaller.

### Built-in tools

| Tool | Purpose and important bounds |
| --- | --- |
| `read_file` | UTF-8 text with line numbers; defaults to 400 lines, maximum 5,000; files over 1 MiB must be read in chunks. |
| `list_files` | Directory tree including hidden files; skips `.git` and session data; depth 1-8; maximum 5,000 entries. |
| `search_files` | Go-regular-expression search with path/glob and result limits. |
| `write_file` | Create/replace text with rooted, same-directory atomic publication, diff preview, change tracking, hunk review, and undo support. |
| `edit_file` | Replace one exact unique fragment with rooted atomic publication; refuses missing or ambiguous matches. |
| `apply_patch` | Validate related create/update/delete operations before applying them through rooted atomic replacement and safe deletion, with rollback on a later publish failure. |
| `run_command` | Shell command in workspace; default timeout 120 seconds, maximum 1,800; bounded/live output; optional Unix PTY. |
| `git_status` | Read-only branch/ahead/behind/change status. |
| `git_diff` | Read-only unstaged/staged/ref diff or stat, optionally one path. |
| `git_log` | Read-only recent history, default 20 and maximum 100 commits. |
| `git_blame` | Read-only attribution, optionally line-bounded. |
| `detect_verification` | Detect real build/lint/test commands from project files. |
| `start_process` | Start a session-lifetime background command under command safety/sandbox policy. |
| `list_processes` | List background process IDs, command, status, and uptime. |
| `process_output` | Read the retained last 64 KiB, optionally the last N lines. |
| `read_tool_result` | Page a bounded range from an oversized result retained by the active durable session; never reruns the source tool. |
| `stop_process` | Stop one background process and its process group. |
| `search_symbols` | Incremental definition search for Go, Python, JS/TS, and Rust. |
| `diagnostics` | Run a configured/auto-detected language server over up to 20 same-language files. |
| `update_plan` | Maintain a structured plan persisted with the session. |
| `load_skill` | Load a relevant skill's full manifest and bundle map on demand. |
| `delegate` | Run bounded parallel sub-agent tasks; omitted inside sub-agents. |
| `ask_user` | Pause for a typed answer; interactive TUI only. |
| `list_mcp_resources` / `read_mcp_resource` | Browse/read negotiated MCP resources when MCP is connected. |
| `mcp_<server>_<tool>` | Dynamically registered MCP tool. |

`options.max_tool_output_bytes` caps the preview returned directly to the
agent even when a tool has a larger internal display cap. During a durable
session, an oversized returned string includes an opaque reference that
`read_tool_result` can inspect in bounded ranges; in ephemeral mode it uses an
ordinary truncation marker. In either case, omitted output is not evidence
that an unseen operation succeeded—narrow the request or inspect the reference.

### File editing, diff, and undo

For any proposed write that needs approval, review the path and diff in the
floating dialog. After changes:

```text
/diff     interactively browse every change tracked in this session
/undo     revert the most recent tracked operation
```

Undo checks current content and refuses to overwrite a file changed outside the
agent since its checkpoint. Direct writes, edits, patches, and undo anchor the
final operation beneath the approved directory root. Replacements use a
private same-directory file plus atomic rename instead of truncating an
existing inode; parent-symlink swaps cannot redirect the rooted operation, an
existing hard link's other name is not modified, and file permission bits are
preserved where the platform exposes them. Deletes remove only the approved
directory entry.

These properties apply to Collomia's structured file tools, not an approved
`run_command`. A multi-file patch is validated before it starts and rolls back
rootedly on a later publication error, but it is not a transaction that locks
out unrelated editors. Atomic publication creates a new inode, so hard-link
identity is intentionally broken and platform-specific ACLs, extended
attributes, or special ownership may not survive even though content and
portable mode bits do. Use a reviewed metadata-aware command for files where
those attributes matter. Undo remains a local safety net, not version control.
Keep work in Git and inspect `git diff` before committing.

### Shell commands and PTY

`run_command` uses `/bin/sh -lc` on Unix and `cmd.exe /d /s /c` on Windows.
Commands run from the workspace in their own process group. Timeout,
cancellation, or session cleanup terminates descendants; a deliberately
re-parented daemon remains a known residual risk.

Unix commands that require a terminal can request `pty: true`. PTY execution
is unavailable on native Windows. Use `start_process` for a server/watcher that
should remain live while the agent continues; do not use shell backgrounding
as a substitute.

### Background processes

Background processes share the same denied-command patterns, static analysis,
sandbox, workspace, and minimal/full environment as `run_command`. They are
listed in the Session tab and `/ps`. Their output retains the most recent 64
KiB. `stop_process`, `/ps stop`, sub-agent completion, or Collomia shutdown
kills the process group so session-owned processes do not intentionally
outlive Collomia.

### Git inspection

Git tools invoke Git directly, never through a shell, reject user-supplied
values beginning with `-`, cap output, and time out after 30 seconds. They do
not add, commit, branch, merge, rebase, reset, or push. If you ask the agent to
perform those operations, it must use `run_command` and pass the normal command
approval/sandbox policy.

### Verification detection

The detector recognizes:

- `go.mod`: `go build ./...`, `go vet ./...`, `go test ./...`.
- `package.json`: actual `build`, `lint`, and `test` scripts, preferring pnpm
  or Yarn when their lockfiles exist.
- `Cargo.toml`: Cargo build, Clippy, and test.
- `pyproject.toml`, `requirements.txt`, or `setup.py`: pytest, plus Ruff when
  configured.
- `Makefile`: common `build`, `test`, `lint`, `vet`, and `check` targets.

Detection is advisory. The verification loop trusts only the exact command
outcomes returned by `run_command` and does not edit files.

## Language-server support

The `diagnostics` tool provides editor-quality diagnostics after an edit.
Collomia auto-detects these commands on `PATH`:

| Files | Language ID | Default command |
| --- | --- | --- |
| `.go` | `go` | `gopls serve` |
| `.py` | `python` | `pyright-langserver --stdio` |
| `.ts` | `typescript` | `typescript-language-server --stdio` |
| `.tsx` | `typescriptreact` | `typescript-language-server --stdio` |
| `.js` | `javascript` | `typescript-language-server --stdio` |
| `.jsx` | `javascriptreact` | `typescript-language-server --stdio` |
| `.rs` | `rust` | `rust-analyzer` |

Install the relevant language server separately and confirm its executable is
on `PATH`. Examples (installation mechanisms vary by platform):

```sh
go install golang.org/x/tools/gopls@latest
npm install --global pyright typescript typescript-language-server
rustup component add rust-analyzer
```

Override commands when your environment needs wrappers or extra arguments:

```json
{
  "lsp": {
    "go": ["gopls", "serve"],
    "python": ["pyright-langserver", "--stdio"],
    "typescript": ["typescript-language-server", "--stdio"],
    "typescriptreact": ["typescript-language-server", "--stdio"],
    "javascript": ["typescript-language-server", "--stdio"],
    "javascriptreact": ["typescript-language-server", "--stdio"],
    "rust": ["rust-analyzer"]
  }
}
```

All paths in one `diagnostics` call must map to the same exact language ID;
split TypeScript and TSX, for example. Files must be inside the workspace. The
client opens the requested current text, waits up to 25 seconds for published
diagnostics, sorts error/warning/info/hint findings, and closes the server.

Configured language servers are trusted subprocesses started directly by
Collomia. They do not pass through `run_command` permission, command
environment, or OS sandbox settings. Put project LSP overrides in a reviewed
`.collomia.json` so repository trust governs their activation.

### Symbol index

`search_symbols` is independent of LSP installation. It uses a fast,
line-oriented definition index for Go, Python, JavaScript/TypeScript, and Rust.
Exact names rank above prefixes and substrings; a kind filter can narrow
results. Files over 1 MiB and dependency/build/hidden directories such as
`.git`, `node_modules`, `vendor`, `dist`, `target`, and `.venv` are skipped.
Only files whose size or modification time changed are re-parsed. Use
`search_files` for references and arbitrary text.

## Instructions and skills

### Persistent instructions

Instructions let you establish coding standards and repository-specific
knowledge without repeating it in every prompt.

User-wide instructions live beside the global configuration:

```text
~/.collomia/AGENTS.md
~/.collomia/COLLOMIA.md
```

Collomia uses the first user instruction file found, checking `AGENTS.md`
before `COLLOMIA.md` in the global `.collomia` directory. It applies to every
workspace.

Project instructions live at the workspace root:

```text
<workspace>/AGENTS.md
<workspace>/COLLOMIA.md
```

When the project trust state is active, Collomia reads both project files that
exist, in that order, after user instructions. Later project guidance can
refine user-wide conventions. Each instruction file is limited to 512 KiB.

Good instructions are stable and testable:

```md
# Repository conventions

- Use `uv` for Python dependency and script execution.
- Run `go test ./...` and `go vet ./...` after Go changes.
- Do not edit generated files under `internal/generated`.
- Preserve backward compatibility for the public JSON schema.
```

Repository text is still untrusted model input. Instructions do not bypass the
permission engine, hard denials, or sandbox.

### Skill package layout

A skill is reusable, progressively loaded guidance:

```text
release-check/
  SKILL.md          required manifest and instructions
  scripts/          optional executable helpers
  references/       optional supporting documentation
  assets/           optional templates/output assets
```

Locations, in precedence order:

1. `<workspace>/.collomia/skills/<name>/`
2. `<workspace>/.agents/skills/<name>/`
3. `~/.collomia/skills/<name>/`

Project skills of the same name shadow user skills; shadowing is reported.
Legacy project `SKILLS.md`/`skills.md` files at the root or under `.collomia`
are still discovered.

`SKILL.md` uses YAML front matter:

```md
---
name: release-check
description: >-
  Verify release binaries, checksums, changelog entries, and version metadata
  before publishing a Collomia release.
license: MIT
allowed-tools: [read_file, run_command]
metadata:
  version: 1.0.0
---

# Release check

1. Read the release checklist in `references/checklist.md`.
2. Run `scripts/verify-release.sh` through the normal command tool.
3. Report every artifact and checksum checked.
```

Requirements and limits:

- Names should contain lowercase letters, digits, and hyphens, maximum 64
  characters, and match the directory name.
- A concise description (maximum 1,024 characters) determines when the model
  should load the skill.
- `SKILL.md` is limited to 512 KiB.
- Up to 200 discovered bundled files are surfaced per skill.
- `allowed-tools` is advisory metadata describing expected tools; normal
  runtime permissions remain authoritative.
- A `.disabled` marker hides the skill without deleting it.

Only name and description are placed in every system prompt. The agent calls
`load_skill` to load the full instructions and bundle listing when relevant.
References/assets cost context only when read. Scripts are not privileged:
they run through `run_command` with its approval, sandbox, environment, timeout,
and output rules.

### Managing skills

```sh
collo skills list
collo skills show release-check

# Create a project skill.
collo skills new release-check

# Create a user-wide skill.
collo skills new release-check --global

# Copy an existing skill package into project or user scope.
collo skills install /path/to/release-check
collo skills install /path/to/release-check --global

# Replace the installed package with a source directory.
collo skills update /path/to/release-check

collo skills disable release-check
collo skills enable release-check
collo skills remove release-check --yes
```

Install validates and copies regular files, preserves executable bits, skips
common dependency/VCS directories, refuses symlinks, and refuses trees over
2,000 files. Installing over an existing skill requires `--yes`; `update`
implies replacement. `remove`, `enable`, and `disable` resolve an installed
name project-first across all roots, so use `skills show` to confirm its path
when duplicates exist.

Lifecycle commands are explicit user file operations and can inspect/manage
project skills before repository trust. The agent-visible catalog still follows
the runtime project-trust state.

## MCP servers

Collomia uses the official MCP Go SDK and supports stdio and Streamable HTTP.
MCP can add remote tools, resources, prompt templates, progress notifications,
and typed elicitation questions to a session.

### Configure a stdio server

```json
{
  "mcp": {
    "filesystem": {
      "transport": "stdio",
      "trusted": true,
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "."],
      "timeout_seconds": 30
    }
  }
}
```

The command is executed directly with the configured argv. It inherits
Collomia's full parent environment and then receives configured `env` values:

```json
{
  "mcp": {
    "database": {
      "transport": "stdio",
      "trusted": true,
      "command": "database-mcp",
      "args": ["--read-only"],
      "env": {
        "DATABASE_TOKEN": "${DATABASE_MCP_TOKEN}"
      }
    }
  }
}
```

MCP stdio processes do not use `permissions.command_env` or the shell-command
sandbox. Treat `command`, `args`, and the server package as trusted code. A
project MCP definition additionally remains quarantined until repository trust
is active.

### Configure a Streamable HTTP server

```json
{
  "mcp": {
    "docs": {
      "transport": "streamable-http",
      "trusted": true,
      "url": "https://docs.example.com/mcp",
      "headers": {
        "Authorization": "Bearer ${DOCS_MCP_TOKEN}"
      },
      "timeout_seconds": 30
    }
  }
}
```

`http` and `streamable-http` select the same current transport. Custom headers
are added to each request. OAuth negotiation is not implemented; supply any
token through environment-expanded headers and manage its lifecycle outside
Collomia.

### Two trust gates

A configured MCP server starts only when:

1. Its own definition has `"trusted": true` and is not disabled.
2. If defined by project `.collomia.json`, that project configuration is also
   trusted.

These gates are separate. A trusted project does not make an MCP entry with
`trusted: false` executable.

### Persistent MCP management from the command line

`collo mcp` edits MCP definitions without requiring users to hand-copy JSON.
Project scope is the default; add `--global` to target
`~/.collomia/config.json` on macOS/Linux or
`%USERPROFILE%\.collomia\config.json` on Windows:

```text
collo mcp list
collo mcp list --global
collo mcp show time
collo mcp show time --global

# Persistent stdio server for this project.
collo mcp add time -- uvx mcp-server-time

# Persistent stdio server for every workspace.
collo mcp add time --global -- uvx mcp-server-time

# Persistent Streamable HTTP server. Quote ${...} so your shell does not
# expand it while the definition is being written.
collo mcp add docs --global \
  --url https://docs.example.com/mcp \
  --header 'Authorization=Bearer ${DOCS_MCP_TOKEN}' \
  --timeout 30

# Stdio environment values are repeatable too.
collo mcp add database \
  --env 'DATABASE_TOKEN=${DATABASE_MCP_TOKEN}' \
  -- database-mcp --read-only

collo mcp test time
collo mcp disable time
collo mcp enable time
collo mcp remove time
```

`add` creates a reviewed, enabled definition with `trusted: true`. If the same
name already exists in the selected scope, Collomia refuses to overwrite it;
inspect it with `show`, then add `--yes` only when replacement is intentional.
`enable`, `disable`, and `remove` also modify only the selected scope. For
example, removing a project entry may reveal a same-named global entry that it
previously shadowed.

Without `--global`, `list` shows both layers and labels each entry `effective`,
`shadowed by project`, or `quarantined`. A project entry shadows the global
entry of the same name only after project trust is active. `list --global` and
`show --global` inspect only the user-wide file.

Every project-config edit changes the file's trust hash. Review the updated
`.collomia.json` and run `collo trust` before Collomia will apply any project
entry. Global entries do not need repository trust, but the per-server
`trusted` field still applies.

`show` leaves environment references visible but redacts literal values in
sensitive environment variables and headers. `add` warns when a likely secret
is literal; prefer `${NAME}` references so credentials remain in the process
environment rather than the JSON file.

`collo mcp test <name>` tests the effective entry; `--global` tests the exact
global entry even if a project entry shadows it. It connects, negotiates the
protocol, pings the server, validates the tool catalog, and lists advertised
resource/prompt catalogs. It closes the connection afterward, invokes no MCP
tool, and does not read or update the persistent MCP pin store.

Configuration writes use an atomic same-directory replacement, preserve the
file's permissions, and retain unrelated configuration plus unknown fields.
The active configuration is strict JSON, so comments belong in the non-loaded
`.example.jsonc` reference rather than `config.json` or `.collomia.json`.

### Runtime MCP management

Inside the TUI:

```text
/mcp                         pick a connected server and inspect its tools
/mcp status                  status, transport, identity, capabilities, tools, uptime, errors
/mcp ping docs               protocol health check
/mcp refresh docs            reload the live tool catalog without reconnecting
/mcp reconnect docs          reconnect and refresh the tool catalog
/mcp disable docs            disconnect for this session and withdraw tools
/mcp enable docs             reconnect a trusted configured server
/mcp add time uvx mcp-server-time
/mcp add remote --url https://example.com/mcp
/mcp remove time
```

Runtime-added servers are an explicit user action, session-scoped, and not
written to configuration. Use `collo mcp add` outside the TUI to persist one.
Removing or disabling a configured server with the slash command lasts only
until the next Collomia start; the command-line lifecycle edits configuration.

Untrusted, disabled, and failed definitions remain visible in `/mcp status`
with their error instead of disappearing.

Status also shows the negotiated MCP protocol revision, catalogs that promised
`list_changed` notifications, notification count, pending catalog reads, and
the last catalog error. `refresh` is less disruptive than `reconnect`: it keeps
the current transport/session and reloads only the tool definitions.

### Quick MCP test: current time

The official time server is a useful first MCP test because it adds timezone
functionality that Collomia's built-in workspace tools do not already provide.
It requires [`uvx`](https://docs.astral.sh/uv/), which is installed with `uv`.

From inside the Collomia TUI, run:

```text
/mcp add time uvx mcp-server-time
```

On Windows, a normal `uv` installation makes `uvx.exe` directly available, so
the same command should work. If `uvx` is available only through the command
shell on that machine, use:

```text
/mcp add time cmd /c uvx mcp-server-time
```

The first run may use the network to download the official
[`mcp-server-time`](https://github.com/modelcontextprotocol/servers/tree/main/src/time)
package. Check the connection:

```text
/mcp status
```

A healthy entry looks conceptually like this; exact versions and tool counts
may change with the server release:

```text
● time — connected (stdio, session-only)
    server: mcp-time <version>
    protocol: <negotiated revision>
    capabilities: tools
    tools: <count> registered
```

Now ask Collomia:

```text
Use the time MCP server to tell me the current time in Japan.
```

The transcript should show a tool such as `mcp_time_get_current_time` before
the answer. The call is external-risk, so it may require approval under your
permission policy. Remove the test server when finished:

```text
/mcp remove time
```

Because `/mcp add` is session-scoped, exiting Collomia also removes it. To make
the server available in future sessions, exit the TUI and run either:

```text
collo mcp add time -- uvx mcp-server-time
collo mcp add time --global -- uvx mcp-server-time
```

The first command writes the project configuration and requires a subsequent
`collo trust`; the second writes the user-wide configuration. Their equivalent
JSON is:

```json
{
  "mcp": {
    "time": {
      "transport": "stdio",
      "trusted": true,
      "command": "uvx",
      "args": ["mcp-server-time"],
      "timeout_seconds": 30
    }
  }
}
```

For this smoke test, prefer the time server over the filesystem server.
Collomia already has native workspace-aware file tools, so adding a filesystem
MCP server usually duplicates capabilities while adding another process and
trust boundary. Filesystem MCP remains useful for MCP interoperability testing
or deliberately exposing a separately constrained directory.

### MCP tools and permissions

Remote tools are registered as:

```text
mcp_<sanitized-server-name>_<sanitized-tool-name>
```

Names are reduced to letters, digits, underscore/hyphen and capped at 64
characters. Calls are classified as external risk and identify their MCP
server for `permissions.rules`. MCP tool annotations never lower Collomia's
permission requirement. Autopilot does not automatically approve them.

Example scoped approval:

```json
{
  "permissions": {
    "rules": [
      {
        "action": "allow",
        "tool": "mcp_docs_*",
        "server": "docs",
        "reason": "approved read-only documentation service"
      }
    ]
  }
}
```

Verify the remote server's behavior before granting a wildcard.

Trust and permission do not make server content authoritative. Every MCP tool
result, agent-read resource/catalog, and expanded prompt template is placed in
an explicit `EXTERNAL_MCP_DATA` provenance frame naming its server, kind, and
subject. Its handling guidance tells the model to use relevant factual and
structured content while refusing embedded instructions or claimed
permissions. Control characters are removed, descriptive schema/catalog
metadata is bounded, and server-authored tool descriptions are labeled
external/descriptive. Expanded prompts therefore include their provenance
frame in the composer; review and edit the complete framed text before pressing
Enter. The frame is a provenance signal, not a guarantee that a model cannot be
influenced, so keep write, command, and additional external permissions scoped
to what the server truly needs.

### Resources and prompts

```text
/mcp resources docs
/mcp resource docs doc://guide
/mcp prompts docs
/mcp prompt docs summarize uri=doc://guide style=short
```

Resource listing shows URI, name, MIME type, and description; resource preview
is capped in the TUI. The agent can call `list_mcp_resources` and
`read_mcp_resource`, both external-risk and server-scoped.

Prompt expansion puts a provenance-framed server-generated prompt into the
composer. Review and edit it before pressing Enter; expansion does not send it
automatically and does not grant any tool permission.

Tool content keeps structured/text and embedded resource data. Images always
retain an explicit type-and-size marker. When the active route supports rich
tool-result images, bounded image bytes are also retained in session attachment
storage and supplied to the next model turn; Anthropic Messages and Bedrock
Converse support that path. OpenAI-compatible Chat Completions keeps the marker
because multimodal tool-message content is not portable across gateways. Audio
remains metadata-only, and resource links retain a URI that the resource tool
can follow. Image content remains inside the same `EXTERNAL_MCP_DATA`
provenance context and cannot grant permission.

### Progress, elicitation, and pinning

- MCP progress notifications stream into the current tool output.
- In the TUI, form-mode elicitation becomes a typed question; enum and boolean
  choices are shown. Escape declines. URL-mode elicitation is declined.
- Headless sessions do not advertise an elicitation handler, so a server cannot
  solicit unattended input.
- Collomia pins each configured server's definition and the connected server
  name/version per workspace outside the repository. A changed command, args,
  URL, env/header names, or remote identity generates a warning. Secret values
  are excluded from the fingerprint so ordinary rotation does not cause noise.

### Live catalog changes and protocol support

If a server advertises list-change support, Collomia installs handlers for the
standard tool, resource, and prompt catalog notifications:

- **Tools:** Collomia lists and validates the complete replacement catalog,
  then swaps all tools from that server into the registry atomically. The model
  never sees a half-refreshed catalog. If listing or validation fails, the
  previous tools stay registered and `/mcp status` shows the error and pending
  `tools` marker. Use `/mcp refresh <server>` to retry without reconnecting.
- **Resources and prompts:** these lists are never held as a long-lived cache;
  `/mcp resources` and `/mcp prompts` read the live server. A notification sets
  a pending marker, cleared only after the corresponding list succeeds.
- Notifications from a stale connection are ignored after disable, remove, or
  reconnect. Bursts of tool changes are coalesced and serialized.

Collomia reports the actually negotiated protocol revision per server. The
current official SDK negotiates MCP 2025-11-25 and retains compatibility with
2025-06-18, 2025-03-26, and 2024-11-05. The complete implemented subset and
test boundary are in [MCP_PROTOCOL.md](MCP_PROTOCOL.md).

Experimental MCP tasks, resource subscriptions, and standards-based OAuth are
currently unsupported. Header tokens remain the supported authenticated HTTP
configuration until the OAuth/login wave lands; check the
[capability matrix](CAPABILITIES.md) for current status.

## Lifecycle hooks

Hooks run trusted local programs at session events. They can feed an external
audit/metrics system, notify another process, or block selected prompts/tools.

```json
{
  "hooks": {
    "file_change": [
      {
        "command": "/usr/local/bin/collomia-audit",
        "args": ["--record"],
        "timeout_seconds": 5
      }
    ],
    "tool_start": [
      {
        "command": "./scripts/tool-policy",
        "matcher": "run_command|apply_patch",
        "timeout_seconds": 5
      }
    ]
  }
}
```

### Events

| Event | When it fires | Notable payload |
| --- | --- | --- |
| `session_start` | Runtime created | session ID, provider, model in `detail` |
| `user_prompt` | Before the user prompt enters model history | `prompt`; can block |
| `permission_decision` | After allow/deny evaluation | tool, summary, `allowed`, risk/source/rule |
| `tool_start` | After permission, before execution | tool, summary, raw JSON args, paths; can block |
| `tool_end` | After execution | tool, summary, error, output byte count |
| `file_change` | After a successful write-risk action | tool and affected paths |
| `compaction` | After active context is summarized | replaced-message count |
| `subagent_start` | Before a delegated task | name as subject; task/write/profile in detail |
| `subagent_end` | After a delegated task | name and changed paths |
| `stop` | A turn completes normally | iteration count |
| `session_end` | Runtime closes | session ID |

Every hook receives one compact JSON object on stdin:

```json
{
  "event": "tool_start",
  "workspace": "/work/repository",
  "subject": "run_command",
  "tool": "run_command",
  "summary": "run: go test ./...",
  "args": {"command": "go test ./..."},
  "paths": []
}
```

Possible fields are `event`, `workspace`, `subject`, `tool`, `summary`, `args`,
`prompt`, `paths`, `error`, `allowed`, and event-specific `detail`. Hooks also
receive `COLLO_HOOK_EVENT` and `COLLO_WORKSPACE` environment variables, inherit
the parent environment, and run with the workspace as current directory.

`command` and `args` form a direct argv; there is no shell. Use a wrapper
script when shell composition is intentional. Hook stdout/stderr is capped at
8 KiB each, and timeout defaults to 10 seconds.

### Blocking

Only `user_prompt` and `tool_start` are gating events. A matching hook blocks by:

- Exiting with status `2`; its stdout (or stderr if stdout is empty) is the
  reason.
- Exiting successfully and printing
  `{"decision":"block","reason":"explanation"}`.

The first block wins. Hooks can only tighten behavior: they cannot approve a
permission denial, bypass an approval, or weaken a sandbox. A timeout, missing
executable, unreadable response, or non-block failure becomes a logged warning
and does not brick the session; the ordinary permission boundary remains.
Block-shaped output on observational events has no control effect.

Hook programs are not run through `run_command`, `command_env`, or the OS
sandbox. They are trusted code. Keep them in global config or a reviewed,
trusted project configuration.

## Sub-agents

The `delegate` tool can submit up to six bounded tasks per call. One FIFO
scheduler is shared by the session, with four active tasks by default, so two
simultaneous delegate calls cannot create eight active children. It is useful
for independent investigation, parallel reviews, or isolated implementation.

Conceptual tool request:

```json
{
  "tasks": [
    {
      "name": "auth-analysis",
      "task": "Trace the authentication flow and report expiry behavior."
    },
    {
      "name": "retry-change",
      "task": "Implement bounded retry in the HTTP client and test it.",
      "write": true,
      "plan_step": 2
    },
    {
      "name": "security-review",
      "task": "Review the proposed change for credential leakage.",
      "agent": "security-reviewer"
    }
  ]
}
```

Read-only tasks share the workspace and run in planning mode. Write tasks
require a Git repository and create a branch named `collomia/<task-id>` in a
worktree under the OS temporary directory. Each has its own built-in tool
registry, permission manager, audit ledger, background processes, and normal
sandbox policy.

No sub-agent result is committed, merged, or pushed automatically. A clean
write worktree is removed. A worktree with changes is left in place and its
path/branch is reported for human review. When siblings modify the same path,
the parent reports a conflict rather than attempting reconciliation. Because
all siblings start at the same `HEAD`, Collomia also compares zero-context
hunks against that common base and labels known overlap or disjoint ranges.
This analysis is advisory; disjoint worktrees are still never auto-merged.

An optional `plan_step` associates a child and its returned evidence with an
existing structured-plan step. Collomia refuses an unknown step or one whose
declared dependencies are unfinished. This is metadata for coordination; it
does not create an autonomous plan scheduler or mark the plan complete by
itself.

Queueing plus execution has a 10-minute default timeout, and each child has at
most 16 model/tool iterations (or a lower configured/profile limit). Sub-agents
do not receive the `delegate` tool, so delegation is not recursive.

Named profiles specialize a sub-agent without defining another provider:

```json
{
  "agents": {
    "security-reviewer": {
      "model": "a-compatible-model-on-the-current-provider",
      "instructions": "Focus on authentication, authorization, injection, and secret handling.",
      "tools": [
        "read_file",
        "list_files",
        "search_files",
        "search_symbols",
        "git_diff"
      ],
      "skills": ["security-review"],
      "max_iterations": 12,
      "token_budget": 50000,
      "timeout_seconds": 600,
      "permissions": {
        "mode": "ask",
        "denied_tools": ["run_command"],
        "denied_commands": ["(?i)^example-destructive-command($|\\s)"],
        "rules": [
          {
            "action": "deny",
            "server": "production-*",
            "reason": "review agents cannot call production MCP servers"
          }
        ]
      }
    }
  }
}
```

The model override stays on the parent's provider. Empty `tools` and `skills`
inherit the parent's visible surface; non-empty lists are allowlists. Hidden
tools are enforced at execution too, so a model cannot call one merely by
inventing its name.

Profile permissions are deliberately one-way. Their `mode` is intersected
with the parent's effective mode; `denied_tools` and `denied_commands` are
additive; profile rules are a separate restriction layer and may only `prompt`
or `deny`, so a prompt cannot mask a parent denial and a parent allow cannot
mask a child denial. Configuration validation rejects `allow`. A profile cannot enable
outside-workspace access, weaken sandboxing, enable command networking, expose
more environment, or bypass parent/global/built-in denials.

`token_budget` counts provider-reported input plus output tokens. Before each
request Collomia reserves the estimated next input and reduces the requested
output maximum to the remaining allowance, then checks the provider's reported
usage afterward. Tokenizers and images are provider-specific, and a provider
that omits usage cannot provide an exact token guarantee; iteration and timeout
bounds still apply. `timeout_seconds` includes scheduler queue time and accepts
up to 3600 seconds.

Configure scheduler limits independently of profiles:

```json
{
  "options": {
    "delegate_max_concurrency": 4,
    "delegate_provider_concurrency": {
      "openrouter": 2,
      "bedrock": 1
    }
  }
}
```

Each parent result is bounded structured JSON containing the child's terminal
status, summary/error, up to eight bounded tool-evidence entries, provider
usage, token budget, changed file/hunk manifest, and retained worktree/branch.
The raw child transcript is not injected wholesale into parent context. If the
complete batch would exceed `max_tool_output_bytes`, Collomia compacts details
while preserving valid JSON and every task's identity/status, and sets
`truncated: true`; `/agents` retains the bounded per-task outcome for review.

The Session tab shows the Collomia parent and its children as a tree, including
queued, running, waiting-for-approval, cancelling, completed, failed,
cancelled, timed-out, budget-exhausted, and interrupted states. Active entries
include their current action and a bounded recent-output tail. `/agents`
searches the snapshots and opens full bounded details.

`/agents steer <id> <guidance...>` queues up to eight bounded steering updates.
The child receives them as explicit parent guidance immediately before its next
provider request. Steering cannot alter a provider call or tool already in
flight, cannot answer a permission dialog, and contains an explicit reminder
that it grants no permission. If the child finishes before another boundary,
the undelivered count remains visible. `/agents stop <id-or-name>` cancels one.
`alt+a` stays active during a busy turn and opens a second, deliberate action
menu for inspect, steer, or stop. Approval dialogs name the requesting child.

For a retained write worktree, `/agents apply <id>` opens a themed floating
review. Use `[`/`]` (or left/right) to change files, up/down to change hunks,
space to include/exclude one hunk, `x` to toggle a file, and enter to request
integration. Before showing or applying anything, Collomia requires all of the
following:

- the saved path is still a worktree registered to the current repository;
- its `collomia/*` branch still points at the recorded delegation base;
- the parent copy of every selectable file still matches that base;
- source and destination are regular UTF-8 text files, at most 1 MiB each and
  4 MiB total for one review; and
- no symlink, binary, oversized, mode-only, or otherwise unsupported entry is
  selected.

Integration uses the normal `integrate_delegate` permission decision, then
rechecks parent and child bytes after the approval dialog. Selected text is
published through the same rooted atomic file primitive as built-in writes;
multi-file failure rolls back earlier entries. Integrated changes enter the
ordinary session change tracker, so `/diff` and `/undo` can review or revert
them. Collomia does not commit, Git-merge, push, delete the branch, or remove
the worktree. Parent drift and other conflicts remain for explicit manual
reconciliation.

Delegation lifecycle snapshots and completed outcomes are persisted in the
parent session. On resume, terminal outcomes remain inspectable; any recorded
queued/running/approval state becomes `interrupted`. Collomia never re-enqueues
it or repeats its provider/tool calls. Dirty worktrees remain available for
manual inspection.

## Sessions and context

Every run creates or resumes a durable per-workspace session. The append-only
JSONL file includes metadata, full messages, runtime events, compaction
markers, structured plan updates, and bounded delegated-agent lifecycle/outcome
snapshots. These agent records are observational and are never replayed as work.

### Session commands

```sh
collo sessions list
collo sessions show <id>
collo sessions fork <id>
collo sessions rewind <id> <turn>
collo sessions rename <id> "provider retry investigation"
collo sessions archive <id>
collo sessions unarchive <id>
collo sessions delete <id> --yes

collo --resume <id>
collo --continue
collo run --resume <id> "Continue with the next step"
```

`--continue` resumes the most recently updated unarchived session. `fork`
copies the complete history into an independent session that can diverge.
`rewind` creates a new session containing a prefix through the requested
completed turn; `0` creates a branch before the first turn. The target must be
earlier than the source session's current completed-turn count. Archive hides
a session from `--continue` selection but does not delete it. Delete is
permanent and requires `--yes`. `collo sessions show <id>` prints numbered
completed-turn checkpoints before the transcript so scripts and terminal-only
users can choose a rewind target deliberately.

Within the TUI, `/sessions` or the configurable `alt+s` shortcut switches the
transcript, plan, prompt history, unsent draft, and persistence hooks in place.
The shortcut is useful when a draft is already in the composer because opening
the picker does not replace it. `/new` creates a fresh session while leaving
the current one saved. Drafts are retained per session only while this TUI
process remains open; unsent text is not added to durable history.

`/rewind` opens a fuzzy list of durable completed-turn boundaries and switches
to the new branch immediately; `/rewind 3` selects one directly. This is
conversation rewind, not environment rollback. The source log is never
truncated, restoration does not execute recorded tools, and Collomia does not
change the workspace while creating the branch. Files changed by earlier or
later turns, shell commands, package installs, deployments, remote MCP effects,
and other external state remain as they are now. Use `/undo` for a compatible
most-recent direct file edit, or use Git/worktrees for broader source recovery.

On initial `--resume`/`--continue` and in-TUI switches, Collomia reconstructs
the complete visible conversation from the durable transcript, including tool
calls, tool results, and interruption warnings. This is presentation only:
restoration never executes a saved tool. Context compaction can shorten what
the model receives without hiding the complete transcript from the user.

At the first or last visual line of the composer, up/down navigates the active
session's prior user prompts and restores the exact in-progress draft when you
move past the newest entry. Inside multiline or soft-wrapped input, up/down
continues to move the cursor normally. `/retry` is the explicit equivalent for
the most recent prompt: it loads editable text into the composer and never
submits anything automatically.

Session loading tolerates a torn final JSONL line after a crash. A tool call
without a recorded result is marked interrupted and is not replayed
automatically, preventing duplicate writes or commands. If the operating
system returns a disk error or short write, Collomia latches the first
persistence failure and stops appending records behind the torn tail. The
current turn fails visibly in the TUI or headless result, and the Session tab
shows persistence as failed. Resolve the storage problem before continuing;
accepted history up to the final torn line remains recoverable.

### Context estimation and compaction

`context_window` tells Collomia the model's usable context size. Provider token
usage anchors estimates; when no fresh usage is available, the UI estimates at
roughly four characters per token. `/context` breaks down system prompt,
instructions, pinned session state, skill summary, tool results,
user/assistant messages, compaction summaries, image attachments, and
retained-result storage. Before provider-reported usage is available, each
image reserves a visible rough estimate of about 1,000 tokens.

The current structured plan is rendered into a pinned session-state section
on every provider request. It is refreshed after `update_plan`, resume,
in-process session switching, compaction, and rewind. Because it is outside the
message history, compaction cannot remove it. `/tasks` shows the same plan the
model receives.

When estimated active context exceeds 80% of a known window and enough messages
exist, Collomia asks the active provider to summarize older history. It keeps
the six most recent messages verbatim and never splits a tool call from its
results. Up to three recent failure results, bounded to 16 KiB in total, are
copied verbatim into the summary record rather than trusting the provider to
paraphrase them correctly. If that bound is reached, the retained evidence has
an explicit truncation marker. `/compact [focus]` requests the same operation
manually.

Compaction changes only the model's active context. The full durable transcript
is retained in the session JSONL file. Compaction itself consumes a provider
request and tokens.

### Session image storage

Submitted image bytes are stored as owner-only raw blobs beside the session,
not base64-encoded into its append-only JSONL. Message records keep only a
random attachment ID, name, MIME type, size, and SHA-256 digest. Every provider
request reopens the regular file and verifies its size, detected type, and
digest before attaching the bytes. A missing, replaced, or corrupted blob
fails the turn visibly rather than silently sending different content.

The fixed limits are 5 MiB per image, four images in one user/tool-result
batch, and 24 MiB per session. Fork copies all referenced images; rewind copies
only images reachable from its retained conversation prefix; delete removes
the session attachment directory. Older image turns can be compacted: the
durable transcript and blob remain, while the compacted summary records image
metadata and normally relies on the surrounding assistant analysis for the
visual conclusions. Unsent selections are in-process composer state and are
not stored until an accepted submission; a prompt rejected by a `user_prompt`
hook is not retained.

### Oversized tool-result artifacts

`options.max_tool_output_bytes` controls how much of one returned string can
enter active model context. When a result exceeds that limit during a durable
session, Collomia stores a bounded session-local copy and adds an opaque ID to
the preview. The model can call `read_tool_result` with that ID, a byte
`offset`, and a `limit` up to 65536 to inspect another range. This operation
only reads stored bytes; it never reruns the command, MCP tool, or other action
that produced them.

Retention has fixed safety bounds:

- At most 4 MiB is retained from one result.
- At most 32 MiB is retained for one session.
- A returned reference reports returned-string and retained sizes and whether the
  copy is complete.
- Artifacts follow session forks; rewind copies only artifacts referenced by
  its retained conversation prefix, so those references remain usable without
  carrying result data from discarded future turns. Deleting a session deletes
  its artifact directory.
- Ephemeral runs do not create artifacts; oversized results use the ordinary
  truncation marker.

Artifact contents are framed as untrusted tool output when read. They remain
outside the prompt until requested and `/context` reports their count and disk
size. This is context management, not secret redaction: arbitrary tool output
can contain source, credentials, or personal data, so session storage must be
protected like the transcript itself.

## Browser terminal

On macOS and Linux, browser mode runs the same Bubble Tea TUI inside a real PTY
and serves an embedded xterm.js client:

```sh
collo --web
collo --web --provider openrouter --autonomy workspace "Review this project"
collo --web --web-port 8765 --no-open
```

The server:

- Binds only to `127.0.0.1`.
- Uses a random available port unless one is selected.
- Generates a new 256-bit token per launch.
- Places the token in the URL fragment, then authenticates the WebSocket with
  the first message.
- Requires the exact served browser origin.
- Allows one controlling connection.
- Terminates the PTY and child process group when the connection closes.

The printed URL is a password with the power to control the TUI and answer
approval prompts. Do not share it, expose it through a reverse proxy, tunnel,
or port forward, or treat it as a remote multi-user service. It has no TLS,
remote identity, reconnect, or observer sessions. Refreshing the page ends the
current first-generation browser session.

All web assets are vendored and embedded; npm is not required to build or run
the existing interface. Native Windows browser mode is unavailable until a
ConPTY backend can preserve equivalent terminal and process semantics.

## Files, state, logs, and privacy

### User-editable files

In this table, `<global-root>` means `~/.collomia` on macOS/Linux and
`%USERPROFILE%\.collomia` on Windows.

| File/directory | Purpose |
| --- | --- |
| `<global-root>/config.json` | Global active configuration. |
| `<global-root>/config.example.jsonc` | Generated commented reference; never loaded. |
| `<global-root>/AGENTS.md` or `COLLOMIA.md` | User-wide instructions. |
| `<global-root>/skills/` | User-wide skills. |
| `<workspace>/.collomia.json` | Project configuration, content-trusted when present. |
| `<workspace>/.collomia.example.jsonc` | Project reference only. |
| `<workspace>/AGENTS.md`, `COLLOMIA.md` | Project instructions. |
| `<workspace>/.collomia/skills/`, `.agents/skills/` | Project skills. |

User-editable configuration files are created with owner-only permissions when
the platform supports Unix modes. Do not commit literal secrets in project
configuration; use environment variable names.

### Internal state

Collomia keeps internal mutable state outside the repository but inside the
same global root as the configuration. The root is `~/.collomia/` on
macOS/Linux and `%USERPROFILE%\.collomia\` on Windows:

| State | Relative location under the global root |
| --- | --- |
| Sessions | `sessions/<workspace-name-and-hash>/*.jsonl` |
| Oversized result artifacts | `sessions/<workspace-name-and-hash>/<session-id>.artifacts/*.json` |
| Audit ledger | `audit/<workspace-name-and-hash>.jsonl` |
| Trust database | `trust.json` |
| MCP pins | `mcp-pins.json` |
| Debug logs | `logs/*.log` |
| Support bundles | `support/collomia-support-*.zip` |

`options.transcript_directory` is currently reserved and does not relocate
sessions. Run `collo doctor` to print the exact log directory for the current
machine.

### Debug logging

Enable for one run:

```sh
collo --debug
collo run --debug --plan "Diagnose provider selection"
```

Or persist it:

```json
{
  "options": {
    "debug": true
  }
}
```

Logs are structured JSON and redacted using configured provider keys, MCP
env/header values, common credential patterns, Bedrock bearer tokens, and Azure
client secrets. Redaction is best effort. Inspect a log before attaching it to
an issue, and never assume arbitrary repository/tool output is secret-free.

## Support bundles and problem reports

When `collo doctor` does not explain a problem, create a diagnostic ZIP from
the affected workspace:

```sh
collo support bundle
```

The default destination is:

- `~/.collomia/support/collomia-support-<timestamp>.zip` on macOS/Linux.
- `%USERPROFILE%\.collomia\support\collomia-support-<timestamp>.zip` on
  Windows.

Use `--output` when you want a different location:

```sh
collo support bundle --output ./collomia-support.zip
```

Collection is local and read-only. The command does not construct the agent
runtime, initialize providers, connect MCP servers, open sessions, execute
tools, or make network requests. It loads configuration structure without
expanding environment-backed provider or MCP values. The default bundle contains:

- A schema-versioned manifest with Collomia version, OS, architecture, and
  terminal status.
- Anonymous configuration-layer and strict-validation status. User-defined
  provider, MCP, agent, LSP, and hook names are removed from setting keys.
  Failed validation is reported generically because the detailed local error
  can contain user-defined names, paths, patterns, or values; run
  `collo config validate --strict` for the local detail.
- Provider type/auth-mode counts without provider aliases, models,
  deployments, endpoints, profiles, or credential references.
- Aggregate MCP enabled/disabled/trusted counts without server definitions.
- Git availability/repository status and effective sandbox capability status.
- Up to eight recent opaque failure IDs for correlating the report with local
  diagnostics. The bundle reads only bounded debug-log tails for these values;
  it does not copy their messages or attributes unless logs are explicitly
  requested.
- The capability matrix generated by the same binary.

The default bundle excludes configuration values, all environment variable
names and values, credentials, provider endpoints/models, MCP names/URLs/
commands/arguments, the workspace path, source files, prompts, transcripts,
sessions, audit records, and debug logs.

Logs are opt-in because arbitrary repository and tool output can contain data
that pattern matching cannot recognize as secret:

```sh
collo support bundle --include-logs
```

This includes at most five recent debug logs, caps each at 1 MiB and the total
at 3 MiB, applies configured-secret and common-credential redaction, removes
terminal control characters, and replaces exact home/workspace paths. For this
explicit mode only, configured secret references are resolved locally so their
values can be registered with the redactor; they are not added to the manifest.
These protections are defense in depth, not a guarantee. Always open and
review the archive before sharing it. The command refuses to overwrite an
existing output file.

## Troubleshooting

Start with these four commands from the affected workspace:

```sh
collo --version
collo config validate --strict
collo config show
collo doctor --strict
```

Then reproduce with `--debug` if the failure is not explained.

When the TUI reports `Failure ID: err-…`, include that ID in the issue. A
headless JSONL run exposes the same value as `failure_id` and
`result.failure.id`. A default support bundle includes up to eight recent IDs,
which lets maintainers match a report to a separately reviewed debug log
without placing error text in the default archive.

For a problem report, prefer a reviewed `collo support bundle` over copying
whole configuration, session, audit, or environment files. Add
`--include-logs` only after reproducing with debug logging and reviewing the
resulting archive.

### `collo` is not found

- Open a new terminal after installation.
- On macOS/Linux, add `$HOME/.local/bin` to `PATH`.
- On Windows, confirm `%LocalAppData%\Programs\Collomia` is on the user
  `PATH`, or invoke the full `collo.exe` path.
- Run the downloaded binary's `--version` to distinguish a `PATH` issue from
  an incompatible/corrupt executable.

### Configuration parse or unknown-field errors

- Active files are strict JSON, not JSONC.
- Remove comments and trailing commas.
- Check braces and string escaping, especially Windows paths and regexes.
- Use `collo config reference` for the installed binary's exact field names.
- `--strict` exposes misspellings that normal forward-compatible loading may
  tolerate.

### Project configuration appears ignored

Run:

```sh
collo trust --status
collo config show
```

If the file is untrusted or changed, validate and run `collo trust` again. The
entire project layer is quarantined, so a project provider, LSP, hook, agent,
permission, or MCP definition will all appear absent until approved.

### Provider key is reported missing

- Confirm `api_key_env` contains the variable name, not the key.
- Confirm the variable exists in the same shell/process that starts Collomia.
- Open a new Windows terminal after setting a persistent user variable.
- With a GUI terminal/editor, verify its environment was refreshed after login.
- `collo doctor` reports the missing variable name but never its value.

### Provider returns 401 or 403

- Confirm endpoint, credential family, key scope, and selected resource.
- Azure Entra: check the effective scope and tenant in `doctor`, assign the
  correct data-plane role, and allow RBAC propagation time.
- Azure `bearer`: remember that Collomia cannot refresh a static token; restart
  with a new token or switch to `entra`.
- Bedrock SigV4: verify the current AWS identity/profile, region, model access,
  and `bedrock:InvokeModelWithResponseStream`-equivalent permissions.
- Bedrock bearer: confirm the API key supports the selected Bedrock Runtime
  operation and has not expired.

### Azure reports unsupported `max_tokens` or `temperature`

Use a current Collomia build. It reacts to an explicit structured 400 by
retrying with `max_completion_tokens` or omitting rejected temperature, then
remembers that requirement for the active model. If the same error persists,
capture a redacted debug log: gateways sometimes return a nonstandard error
shape that does not safely identify the rejected field.

### Bedrock chats but fails after a tool call

Use a current build and verify the configured model supports tool use through
ConverseStream. Collomia groups each assistant tool request with the matching
user `toolResult` blocks. A Bedrock error naming missing tool-use IDs usually
indicates an old client, truncated/restored conversation produced elsewhere,
or a model/gateway protocol incompatibility. Start a new session to separate a
persisted-history issue from the provider configuration.

### Provider is slow or times out

- Determine whether connection, whole request, or stream idle timeout fired.
- Increase only the relevant provider setting.
- Check `/status` for a degraded/open circuit and wait for the recovery window.
- For local models, confirm the server is loaded and not swapping or queued.
- A stream that already emitted output is not automatically retried.

### Sandbox-required commands refuse to run

Run `collo doctor`:

- macOS must have `sandbox-exec` available.
- Linux needs enabled Landlock (kernel 5.13+). Network denial requires ABI v4+
  for TCP and ABI v10+ for UDP. On ABI v4–v9, `require` with command networking
  disabled intentionally fails closed; use `auto` to accept the reported
  TCP-only boundary. ABI v3 (Linux 6.2) is the recommended minimum for robust
  filesystem confinement because earlier ABIs cannot mediate standalone
  truncation. The complete Linux setup procedure is in
  [Linux sandbox setup and Landlock compatibility](LINUX_SANDBOX.md).
- Windows 11 must expose the built-in AppContainer APIs. No optional Windows
  feature or third-party installation is required.

If `auto` cannot apply all requested protections, the command result,
`collo doctor`, and `/status` name the exact degradation. Do not mistake an
approval prompt for sandbox enforcement.

### A build works in the shell but fails in Collomia

If sandboxing is enabled, reads/writes outside the granted roots or remote
network access may be denied. Set `sandbox_allow_network: true` when the
command genuinely needs package registries or online documentation. When read
confinement is enabled, add immutable dependencies or SDKs to
`sandbox_readable_roots`; add only caches that must change to
`sandbox_writable_roots`. Avoid granting the whole home directory.
If `command_env` is minimal, proxy, registry, compiler, cloud, or package
credentials may be absent; select `full` deliberately when those values are
required. Windows AppContainer also blocks loopback to ordinary unpackaged
local servers. The command result mentions sandboxing when a sandboxed failure
occurs. Prefer a narrow fix over globally disabling controls.

### Language-server diagnostics are unavailable

- Install the default executable and put it on `PATH`.
- Configure the exact mapped language ID (`typescriptreact` differs from
  `typescript`, for example).
- Request files of one language per diagnostics call.
- Check project trust if the LSP override lives in `.collomia.json`.
- Run the language server manually with its stdio flag to catch installation
  or runtime problems.

### MCP server does not start

- Check both project trust and the server's `trusted: true` flag.
- Inspect `/mcp status` for the initialization error.
- Confirm `command` exists on `PATH`, args are correct, and needed environment
  variables are present.
- For HTTP, confirm the URL, headers, TLS trust, and timeout.
- If identity/definition pinning warns, investigate before reconnecting or
  granting tool calls.

### TUI colors, notifications, or rendering look wrong

- Set `NO_COLOR=1` or choose `/theme plain` for limited terminals.
- Set `options.reduced_motion` to `true` if you prefer a static progress
  marker; omit it or set it to `false` to retain the normal animations.
- Ensure stdout is a real TTY; use `collo run` for redirected output.
- For tmux background changes, enable `allow-passthrough`.
- Terminal OSC support controls background and desktop notifications; missing
  OSC support does not affect core TUI operation.
- If the terminal is too narrow, use 80x24 or larger for the complete status
  display; the core workflow remains usable below that with compact headers.
- If scrolling jumps unexpectedly, press `end` to resume live follow, or page
  up to pause it while output continues.
- If OSC 52 copy is blocked, enable terminal clipboard access/tmux passthrough
  or run `collo --no-alt-screen` and use native selection.

### Browser terminal does not open

- Browser mode is available only on macOS/Linux.
- With `--no-open`, copy the complete printed URL including its fragment.
- Confirm the selected loopback port is free, or omit `--web-port`.
- Do not refresh: the current implementation ends when the controlling
  connection closes.
- Treat an authentication/origin rejection as a security check, not a reason
  to expose the listener differently.

### A headless run says interactive approval is required

Headless mode has no approver. Use `--plan` for read-only work, add a narrowly
scoped allow rule, or explicitly select autopilot. If the command is
uninspectable, redesign it into a direct inspectable command; autopilot does
not bypass that prompt.

### Reporting a problem

Include:

- A reviewed `collo support bundle` when practical; its manifest already
  contains the Collomia version, OS/architecture, anonymous configuration
  layering, and local health state.
- The relevant provider type (not credentials)
- Exact error and provider request ID
- A reviewed, redacted `--debug` excerpt only when the default bundle is not
  enough, or recreate the bundle with `--include-logs`

Do not attach raw keys, tokens, full environments, or unreviewed session/audit
files.

## Uninstalling

Remove only the installed executable to uninstall while preserving settings and
history:

- macOS/Linux default: `$HOME/.local/bin/collo`
- Windows default from this guide:
  `%LocalAppData%\Programs\Collomia\collo.exe`

On Windows, also remove `%LocalAppData%\Programs\Collomia` from the user `PATH`
if you added it during installation.

Configuration and history are deliberately not removed with the binary. If you
want a complete data removal, first inspect and then delete the exact locations
listed under [Files, state, logs, and privacy](#files-state-logs-and-privacy):

- The home-directory `.collomia` directory, which contains all global
  configuration, skills, instructions, sessions, logs, audit history, trust
  decisions, and MCP pins.
- Any retained dirty sub-agent worktrees under the OS temporary
  `collomia-worktrees` directory and their `collomia/*` Git branches.

Deleting those locations is irreversible and removes provider definitions,
skills, instructions, sessions, audit history, trust decisions, MCP pins, and
logs. Project-owned `.collomia.json`, `.collomia.example.jsonc`, instruction
files, and project skills remain in each repository until removed there.
