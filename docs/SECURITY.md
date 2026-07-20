# Collomia security model

This document states what each control actually guarantees, what it does
not, and where the boundaries are. It is the "documentation truth pass"
required before advertising any unattended use.

## The honest summary

Collomia's permission prompts and rules are **in-process policy checks**, not
an operating-system security boundary, unless the OS sandbox is enabled. A
command approved by you — or auto-approved by autopilot mode — runs with
your normal user privileges. macOS, Linux, and Windows 11 can additionally
enable OS-level enforcement (`permissions.sandbox`). The Windows backend uses
only inbox AppContainer and Job Object APIs; it does not require Windows
Sandbox, Hyper-V, an administrator-installed driver, or another runtime.

Do not point autopilot mode at untrusted code or untrusted instructions and
walk away, on any platform, without the sandbox in `require` mode — and even
then, understand the limitations listed below. This applies equally to
background processes started with `start_process`: they run under the same
policy and sandbox as `run_command`, just detached from the turn that
started them.

## Autonomy modes: exact properties

| Mode | Reads (workspace) | Writes (workspace) | Commands | Outside workspace | Network |
| --- | --- | --- | --- | --- | --- |
| `ask` | auto-allowed | prompt | prompt | prompt + `allow_outside_workspace` | uncontrolled once a command runs |
| `workspace` | auto-allowed | auto-allowed | prompt | prompt + `allow_outside_workspace` | uncontrolled once a command runs |
| `autopilot` | auto-allowed | auto-allowed | auto-allowed¹ | auto-allowed only with `allow_outside_workspace` | uncontrolled unless sandboxed |

¹ With two exceptions that hold in every mode:
- Commands matching `permissions.denied_commands` are always refused.
- Commands the static analyzer cannot fully read (substitutions, `eval`,
  inline interpreter payloads, variable commands) always require an
  interactive approval, and "always allow" never sticks for them.

MCP tool calls (`external` risk) always prompt unless a rule or session
grant allows them; they never ride along with autopilot.

### What the checks can and cannot stop

- **Path containment** (`internal/tools/path.go`) canonicalizes paths and
  resolves symlinks before comparing against the workspace root. It governs
  the built-in file tools only. A shell command is *not* path-checked — `cat
  /etc/passwd` runs if the command itself is approved. This is why command
  approval exists, and why the sandbox matters.
- **Command analysis** (`internal/shell`) is conservative by design: it
  proves what it can and prompts for the rest. It is a policy aid. It is not
  the boundary — a maliciously obfuscated but "inspectable-looking" command
  is still bounded only by what the OS allows your user to do.
- **Denied-command regexes** are defense in depth against catastrophic
  accidents (`rm -rf /`), nothing more. Regexes cannot enumerate harm.

## The OS sandbox

`permissions.sandbox: "auto" | "require"` wraps every agent command —
including background processes started with `start_process`, and commands
run under a pseudo-terminal (`run_command` with `pty: true`) — in the
platform's containment mechanism.

Sandboxing is currently opt-in (`off` is the runtime default), and
`sandbox_allow_network` defaults to `true`. Changing only `sandbox` from `off`
to `auto` therefore preserves the network access used by package installation
and online CLIs. Users who want network-denied commands set that value to
`false` explicitly.
This switch controls only `run_command`, PTY commands, and `start_process`.
Provider HTTP, remote MCP, hooks, and language servers run in the Collomia
process and are not blocked by command-sandbox networking.

**macOS: Seatbelt** (`sandbox-exec`):

- File writes are confined to the workspace, the temp directories, and
  `/dev`; everything else is deny-by-default (reads stay open).
- Network egress is denied unless `permissions.sandbox_allow_network` is
  true (loopback stays open for local model servers).
- `sandbox-exec` is deprecated by Apple but functional; treated as
  best-effort OS enforcement, tested in `internal/tools/command_test.go`.

**Linux: Landlock** (kernel 5.13+, via a hidden `collo __landlock`
re-exec shim — Landlock restricts the calling process, so the command is
re-executed through the shim, which applies the ruleset to itself and then
execs the real command):

- File writes are confined to the granted roots (the workspace, temp
  directories). Reads are not confined.
- On kernel 6.7+ (Landlock ABI v4), TCP connect/bind are also denied unless
  `permissions.sandbox_allow_network` is true. Below ABI v4, only the
  filesystem is confined and `collo doctor` reports that network is
  unenforced. **UDP — including DNS — cannot be restricted by Landlock at
  all**, on any kernel version; treat DNS-based exfiltration as always
  possible even with the sandbox enabled.
- `require` checks capabilities rather than merely checking that Landlock
  exists. With `sandbox_allow_network: false`, Linux fails closed because UDP
  cannot be denied completely. `auto` still applies write confinement and TCP
  denial while reporting the UDP limitation in `collo doctor`, `/status`, and
  command output.

**Windows 11: AppContainer + Job Object**:

- A workspace-specific AppContainer SID provides low-integrity filesystem,
  registry, credential, device, network, and cross-process isolation.
- Collomia grants that SID access to the workspace, the user temp directory,
  and explicit `permissions.sandbox_writable_roots`. User-local executable
  directories on `PATH` receive read/execute access, not write access. The
  normal user's existing access checks still apply as well.
- With `sandbox_allow_network: false`, no Internet or private-network
  capabilities are placed in the process token. With it set to `true`,
  Collomia grants the `internetClient` and `privateNetworkClientServer`
  capabilities. Windows still blocks AppContainer loopback to ordinary local
  processes by default; Collomia does not request an administrator-only
  loopback exemption. Use `sandbox: off` for a command that must connect to an
  unpackaged localhost development server.
- The initial process is created suspended, assigned to a Job Object with
  `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`, and then resumed. Descendants inherit
  the job so cancellation, timeout, or shim termination closes the job and
  kills the tree.
- Windows stores a small per-workspace AppContainer profile and an inheritable
  ACE naming that container SID on granted roots. The ACE gives no access to
  ordinary users or unrelated AppContainers and is reused on later commands.

Microsoft's [AppContainer launch documentation](https://learn.microsoft.com/en-us/windows/win32/secauthz/implementing-an-appcontainer)
describes the inbox profile, SID/DACL, capability, low-integrity, and process
attribute APIs used here. Its [loopback guidance](https://learn.microsoft.com/en-us/windows/apps/develop/communication/interprocess-communication)
documents why access to an unpackaged localhost process needs an administrator
exemption that Collomia deliberately does not create.

Shared limitations, stated plainly:

- Reads are not confined on macOS or Linux: a sandboxed command can still read
  any file your user can. AppContainer restricts Windows user-data reads while
  allowing the Windows runtime and explicitly granted locations.
- `auto` never silently equates partial enforcement with a complete sandbox:
  it emits an actionable degradation warning. `require` refuses a command if
  any requested write or network protection is unavailable.
- Network policy remains all-or-nothing for a command. Domain-scoped egress is
  roadmap work; environment-only proxy settings would not be a security
  boundary because a hostile command could bypass them.
- Package managers and build tools may need write access to caches outside the
  workspace. Prefer a narrow `sandbox_writable_roots` entry. `command_env:
  "minimal"` can also hide proxy variables or registry credentials; switch to
  `full` only when that tradeoff is intended.

## Repository trust

A repository can ship `.collomia.json` (permissions, MCP servers) plus
skills and instruction files. All of it is quarantined until you run `collo
trust` after review. Trust is bound to the file's SHA-256; any change
re-quarantines. The trust database lives in your user configuration
directory, never in the workspace, so a repository cannot approve itself.

The trust record is anchored to `.collomia.json`. A workspace without that
file has no project configuration to approve, and the runtime treats its
project-trust state as active. Review repository-provided instructions and
skills before use; add a project configuration when you want their activation
to be bound to an explicit `collo trust` decision.

## Process control

Commands run in their own process group with a hard timeout; cancellation
and timeout kill the whole group (Unix `SIGKILL` to the group, Windows
`taskkill /T`). Sandboxed Windows commands additionally live in a kill-on-close
Job Object. Ordinary descendants are terminated on timeout — this is tested.
Detached Unix daemons that re-parent before the process-group kill are the
known residual gap.

**Background processes** (`start_process`) are a deliberate exception to
the timeout: their lifetime is the session, not the tool call, so a
dev server started this way keeps running while the agent does other work.
They still run in their own process group and are killed — group-wide —
by `stop_process`, `/ps stop`, or automatically when the session ends
(including background processes started by a delegated write-agent inside
its own worktree, which are stopped when that sub-agent's task finishes).
Nothing started this way is expected to outlive the `collo` process.

**PTY commands** (`run_command` with `pty: true`, Unix only) run in their
own session (`setsid`) rather than merely a process group, because a
pseudo-terminal's child processes attach to the session leader; killing the
session on timeout or cancellation still reaches every descendant. Windows
has no PTY support yet and reports a clear error rather than silently
running without one.

## Browser-terminal boundary

`collo --web` is a terminal transport around the same TUI, not a separate
agent service. On macOS and Linux it starts a child `collo tui` in a real PTY,
so the child has the same workspace, environment, provider credentials,
configuration, tools, and permission policy as a normal terminal session.
The browser receives and sends terminal bytes; it cannot choose a different
executable, working directory, or environment through HTTP.

The initial implementation is deliberately local-only:

- The listener always binds to `127.0.0.1`; there is no remote-host option.
- The port is randomly assigned by default. `--web-port` selects only the
  loopback port and does not change the bind address.
- Every invocation generates a 256-bit random bearer token. The launcher puts
  it in the browser URL fragment (which HTTP requests do not transmit), the
  page removes the fragment from browser history, and JavaScript sends the
  token in the first WebSocket message before the PTY starts.
- The server requires the exact origin it served, applies restrictive browser
  security headers, and accepts only one authenticated controlling connection.
- Closing the controlling browser connection terminates the PTY session and
  its process group. Page refresh/reconnection and observer sessions are not
  supported yet.

Anyone who obtains the printed URL before it is used has the same interactive
control as the user at the terminal, including the ability to answer approval
prompts. Do not share it. Do not put this server behind a reverse proxy, port
forward, tunnel, or non-loopback listener: there is no TLS, account identity,
remote-access policy, or idle-session authentication. The server shuts down
with the TUI. Windows web-terminal mode is rejected until a real ConPTY backend
can preserve equivalent terminal and process-lifecycle behavior.

## Provider streams

Provider requests can be retried only before a response begins, using a
replayable request body and the bounded retry policy. Once any text,
reasoning, or tool-call fragment may have reached the runtime, an in-stream
exception is returned without replaying the request. This avoids duplicate
visible output and repeat billing. Streamed tool arguments are never trusted
as executable input: the adapter must receive, assemble, and validate the
complete JSON document before the normal permission pipeline can see the tool
call. Truncated Responses and Bedrock streams fail closed instead of accepting
their partial content as a completed model response.

Recorded provider contracts run in the ordinary credential-free CI suite.
Real-endpoint qualification is separately double-gated by
`COLLO_LIVE_PROVIDER_TESTS=1` and a manifest path. The live manifest rejects
literal API keys and embedded URL credentials, resolves keys and sensitive
headers from named environment variables, and redacts resolved values from
reported failures. The synthetic tool returned by a model is inspected but
never executed. See [Live provider contract tests](LIVE_PROVIDER_CONTRACTS.md).

## Secrets

Configured provider keys, MCP headers/env values, and common credential
shapes (OpenAI/Anthropic/AWS/GitHub/Slack keys, JWTs, bearer tokens) are
redacted from debug logs, JSONL events, and the audit ledger. Redaction is
best-effort defense in depth — it reduces accidental exposure and does not
defeat deliberate exfiltration.

For native Amazon Bedrock, `auth: "sigv4"` delegates credential discovery to
the AWS SDK chain (environment access/secret/session values, shared profiles,
IAM Identity Center, assumed/web-identity roles, and workload identity) and
signs each request without placing those credentials in Collomia's
configuration. `auth: "bearer"` sends a configured short- or long-term Bedrock
API key only in the HTTPS Authorization header. `auth: "auto"` prefers an
explicit `api_key`/`api_key_env` or `AWS_BEARER_TOKEN_BEDROCK`, then falls back
to SigV4. Set an explicit mode when both families exist and credential choice
must not vary with the environment.

The standard `AWS_BEARER_TOKEN_BEDROCK` value is registered with the redactor
when Bedrock is configured. Prefer `api_key_env` over literal `api_key`.
Collomia accepts already-generated short- and long-term Bedrock keys but does
not mint or refresh short-term bearer keys; replace an expiring token and
restart the process. AWS SDK-managed temporary SigV4 credentials retain the
SDK's normal refresh behavior.

### Microsoft Entra credentials

Azure OpenAI and Microsoft Foundry providers use Microsoft Entra only when the
configuration explicitly selects `auth: "entra"`. Collomia never treats an
ambient Azure CLI session, managed identity, or service-principal environment
as permission to replace an API key implicitly.

Entra mode constructs the official Azure Identity SDK's
`DefaultAzureCredential`. The resulting access token is kept in process memory,
never written to configuration, sessions, debug logs, or the audit ledger, and
is refreshed before the SDK's `RefreshOn` time or expiry. Concurrent requests
share one refresh. A token-acquisition failure is classified as authentication
and stops before provider HTTP; a partially obtained or invalid token is never
sent. Standard `AZURE_CLIENT_SECRET` and
`AZURE_CLIENT_CERTIFICATE_PASSWORD` values are registered with the redactor as
defense in depth.

The mode is intentionally deterministic at the configuration boundary:

- `auth: "api_key"` (or omitted) uses the `api-key` header.
- `auth: "bearer"` uses a caller-supplied static token and cannot refresh it.
- `auth: "entra"` rejects `api_key`, `api_key_env`, and custom authentication
  headers, then writes the current SDK token after all other custom headers.

Traditional Azure OpenAI and current Foundry endpoints use different default
audiences. `entra_scope` can override that choice, and
`entra_authority_host` can select a sovereign/private Entra authority. Both are
validated as HTTPS values; authority URLs cannot contain credentials, paths,
queries, or fragments, and scopes must end in `/.default`. Collomia does not
disable Entra instance discovery or infer sovereign audiences. Use
`AZURE_TOKEN_CREDENTIALS` to restrict `DefaultAzureCredential` to the intended
development or production credential set.

By default, agent commands (including background processes and PTY runs)
inherit your full environment, which may include unrelated secrets from
your shell. Set `permissions.command_env: "minimal"` to strip commands down
to `PATH`, `HOME`, and a short list of other basics — this is the default
automatically whenever the sandbox is enabled (`sandbox: "auto"` or
`"require"`), and can be set explicitly without the sandbox too.

## Optional external reviewer

`permissions.reviewer_command`, when set, runs before any non-read action
that the policy pipeline would otherwise auto-approve. It receives the
request (tool, summary, risk, normalized resources) as JSON on stdin; a
non-zero exit or a `{"decision":"deny"}` reply escalates the action to an
interactive prompt instead of silently allowing it. The reviewer can only
*tighten* decisions — it is never consulted for actions that would already
prompt, and a failing or misconfigured reviewer command fails closed
(escalates to a prompt), never open.

## Audit

Every privileged-action decision (tool, summary, normalized resources,
decision, source, matched rule) and every execution outcome is appended to
a per-workspace JSONL ledger under the user configuration directory —
outside the workspace, so agent-writable files cannot rewrite history.

## Prompt injection

Tool output, repository text, skills, and MCP responses are declared
untrusted data in the system prompt, but a sufficiently capable injection
can still steer the model. The controls that hold regardless of what the
model was told are: the permission pipeline, denied commands, uninspectable
command prompts, the trust quarantine, and (when enabled) the OS sandbox.

## Reporting

Security issues: open a GitHub issue marked security, or contact the
maintainer directly.
