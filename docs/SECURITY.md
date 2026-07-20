# Collomia security model

This document states what each control actually guarantees, what it does
not, and where the boundaries are. It is the "documentation truth pass"
required before advertising any unattended use.

## The honest summary

Collomia's permission prompts and rules are **in-process policy checks**, not
an operating-system security boundary, unless the OS sandbox is enabled. A
command approved by you — or auto-approved by autopilot mode — runs with
your normal user privileges. macOS and Linux can additionally enable OS-level
enforcement (`permissions.sandbox`); on Windows no sandbox backend exists
yet, and `collo doctor` says so.

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

## The OS sandbox (macOS and Linux)

`permissions.sandbox: "auto" | "require"` wraps every agent command —
including background processes started with `start_process`, and commands
run under a pseudo-terminal (`run_command` with `pty: true`) — in the
platform's containment mechanism.

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
- `require` fails closed on both platforms: if Landlock is unavailable
  (kernel too old or disabled), commands refuse to run rather than running
  unconfined. `auto` degrades with a visible doctor warning.

Shared limitations, stated plainly:

- Reads are not confined on either backend: a sandboxed command can still
  read any file your user can. Write (and, where supported, network)
  containment are the enforced properties.
- **Windows**: no sandbox backend exists yet (AppContainer is roadmap
  work). The sandbox setting can only be `off` (run unconfined after
  approval) or fail-closed `require` (refuse to run).

## Repository trust

A repository can ship `.collomia.json` (permissions, MCP servers) plus
skills and instruction files. All of it is quarantined until you run `collo
trust` after review. Trust is bound to the file's SHA-256; any change
re-quarantines. The trust database lives in your user configuration
directory, never in the workspace, so a repository cannot approve itself.

## Process control

Commands run in their own process group with a hard timeout; cancellation
and timeout kill the whole group (Unix `SIGKILL` to the group, Windows
`taskkill /T`). A command cannot leave grandchildren running past a timeout
— this is tested. Detached daemons that re-parent before the kill are the
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
