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

¹ With safety constraints that hold in every mode:

- Commands matching `permissions.denied_commands` are always refused.
- Commands with a classified catastrophic outcome are always refused.
- Destructive commands classified for one-time confirmation always prompt.
- Commands the static analyzer cannot fully read (substitutions, `eval`,
  inline interpreter payloads, variable commands) always require an
  interactive approval.

Allow rules, tool/session grants, autopilot, and an interactive "always allow"
choice cannot bypass these constraints. "Always allow" never sticks for an
uninspectable or one-time-confirmation command.

Command denials are monotonic across configuration scopes. Built-in
catastrophic-command patterns are mandatory, global patterns append to them,
and trusted project patterns append to the combined set. A lower scope cannot
remove inherited patterns, including by specifying an empty list.

MCP tool calls (`external` risk) always prompt unless a rule or session
grant allows them; they never ride along with autopilot.

### What the checks can and cannot stop

- **Path containment** (`internal/tools/path.go`) canonicalizes paths and
  resolves symlinks before comparing against the workspace root. It governs
  the built-in file tools only. A shell command is *not* path-checked — `cat
  /etc/passwd` runs if the command itself is approved. This is why command
  approval exists, and why the sandbox matters.
- **Rooted file mutation** re-checks the approved target through an
  operating-system directory root when `write_file`, `edit_file`,
  `apply_patch`, or `/undo` performs its final operation. Parent traversal and
  a parent symlink swapped during the operation cannot redirect that mutation
  outside the authorized root. Replacements are written to a private file in
  the same directory, synced, and atomically renamed; Collomia does not
  truncate the old inode, so an existing hard link cannot turn a workspace
  edit into an edit of another name. Deletes remove only the rooted directory
  entry. The original permission mode is preserved across edits and undo where
  the operating system exposes POSIX-style mode bits.
- **Command analysis** (`internal/shell`) is conservative by design: it
  identifies common catastrophic outcomes, requires confirmation for known
  destructive operations, and prompts when it cannot resolve an effect. It is
  an accident-prevention policy aid, not a shell security boundary. A novel or
  maliciously obfuscated command is still bounded only by what the OS allows
  your user to do.
- **Denied-command regexes** are an additive defense-in-depth layer for local
  policy. Regexes cannot enumerate harm; the built-in structural checks do not
  depend on users maintaining regex syntax.

Rooted mutation protects Collomia's structured file tools; it does not make an
approved shell command use those primitives. `apply_patch` validates the whole
change set before publishing and attempts rooted rollback if a later publish
fails. It is not a filesystem transaction against unrelated concurrent
writers: another program changing an approved file at the same time can still
win before or after one atomic replacement. Keep important work in version
control, inspect `/diff`, and use the OS sandbox when commands themselves are
untrusted. Atomic publication deliberately creates a new inode: it preserves
content and portable permission bits, but breaks hard-link identity and may not
preserve platform-specific ACLs, extended attributes, or special ownership.
Use a reviewed command or a metadata-aware external tool when those attributes
are part of the file's contract.

## Command safety tiers

The same analyzer and execution-time check cover `run_command`, PTY commands,
and `start_process`. Transparent wrappers such as `sudo`, `doas`, `env`,
`command`, `timeout`, and `nohup` are unwrapped. Literal `sh -c`, `cmd /c`, and
PowerShell `-Command` payloads are inspected recursively. Relative paths are
resolved from the workspace while tracking straightforward `cd` segments.

### Tier 1: non-overridable catastrophic denials

These are refused without showing an approval dialog:

- Recursive deletion or recursive permission/ownership changes aimed at a
  protected root: filesystem/drive roots, the user's home, the workspace
  root, the repository `.git` state, `~/.collomia`, important OS roots, and
  detected mount/volume roots. Broad forms such as `/*`, `$HOME/*`, and a
  workspace-root `*` are included.
- Direct destruction or overwrite of Collomia safety configuration,
  repository metadata, critical account files, `/proc/sysrq-trigger`, or raw
  kernel-memory targets.
- Filesystem creation, wiping, partitioning, raw copying, redirection, or
  similar destructive writes aimed at physical block devices, macOS disk
  identifiers, or Windows physical drives/volumes. Ordinary disk-image files
  inside the workspace are not classified as physical devices.
- Any command matching an effective `permissions.denied_commands` regex.

There is intentionally no approval flag or lower-scope override for this tier.
When a user genuinely intends to administer a physical disk or destroy a
protected root, they must run that operation themselves outside Collomia.

### Tier 2: mandatory one-time confirmation

These can be legitimate, so they remain available, but every invocation needs
a new human decision—even in autopilot and even when an allow rule matches:

- Destructive Git operations such as hard reset/restore/clean, forced push or
  worktree removal, history rewriting, stash/reflog deletion, and aggressive
  pruning. Dry-run Git cleanup remains routine.
- Shutdown/reboot and similar machine-lifecycle operations.
- Bulk or high-impact Terraform, Pulumi, Kubernetes, Helm, Docker/Podman,
  AWS, Azure, Google Cloud, SQL database, and logical-storage deletions.
- Recursive deletion whose target contains unresolved variables or other
  dynamic path syntax, plus `find`-driven dynamic deletion.
- Commands whose complete effect the analyzer cannot inspect.

The approval dialog does not offer a persistent grant for this tier. In a
headless run, the operation fails closed because no human approver is present.

### Tier 3: normal permission flow

Scoped cleanup and ordinary development operations continue through the
configured `ask`, `workspace`, or `autopilot` flow. Examples include:

```sh
rm -r directory
rm -rf node_modules
rm -rf /tmp/example
cd build && rm -rf -- *
git clean --dry-run
mkfs.ext4 ./test-disk.img
dd if=/dev/zero of=./test-disk.img count=1
```

The `*` in the `build` directory is recognized as scoped because Collomia
tracks the preceding `cd`. Test a decision without executing it:

```sh
collo policy check 'rm -rf /*'          # deny, source: safety
collo policy check 'git reset --hard'   # prompt, source: safety
collo --autonomy autopilot policy check 'rm -rf node_modules'  # allow
```

### Configuration denials remain additive

`permissions.denied_commands` augments the classifier with organization- or
project-specific regular expressions. Built-in regexes are always present;
global patterns append to them, and trusted project patterns append to that
combined set. Empty or `null` lists cannot clear inherited denials, and exact
duplicates are collapsed. A project can therefore tighten global policy but
cannot weaken it.

## The OS sandbox

`permissions.sandbox: "auto" | "require"` wraps every agent command —
including background processes started with `start_process`, and commands
run under a pseudo-terminal (`run_command` with `pty: true`) — in the
platform's containment mechanism.

Sandboxing is currently opt-in (`off` is the runtime default).
`sandbox_allow_network` and `sandbox_allow_read_outside_workspace` both
default to `true`. Changing only `sandbox` from `off` to `auto` therefore
preserves the network and dependency reads used by package installation and
developer toolchains. Users opt into network denial or user-data read
confinement by setting the corresponding value to `false` explicitly.
These switches control only `run_command`, PTY commands, and `start_process`.
Provider HTTP, remote MCP, hooks, and language servers run in the Collomia
process and are not blocked by command-sandbox read/network policy.

**macOS: Seatbelt** (`sandbox-exec`):

- File writes are confined to the workspace, the temp directories, and
  `/dev`; everything else is deny-by-default.
- With `sandbox_allow_read_outside_workspace: false`, file-content reads from
  user homes and mounted volumes are denied except for the workspace,
  temporary directories, executable directories from `PATH`, explicit
  `sandbox_readable_roots`, and writable roots. Public operating-system
  runtime paths remain readable. File metadata remains visible so a shell can
  resolve paths and report a normal permission failure without leaking file
  contents. This is a user-data boundary, not an attempt to hide public system
  configuration.
- Network egress is denied unless `permissions.sandbox_allow_network` is
  true. Loopback connect, bind, and inbound operations stay open for local
  model and development servers; those exceptions are operation-specific so
  matching a local ephemeral address cannot accidentally reopen remote egress.
- `sandbox-exec` is deprecated by Apple but functional; treated as
  best-effort OS enforcement, tested in `internal/tools/command_test.go`.

**Linux: Landlock** (kernel 5.13+, via a hidden `collo __landlock`
re-exec shim — Landlock restricts the calling process, so the command is
re-executed through the shim, which applies the ruleset to itself and then
execs the real command):

- File write rules are available on ABI v1, but ABI v1–v2 cannot deny a
  standalone `truncate(2)` operation; ABI v3 (Linux 6.2) is the recommended
  minimum for robust write confinement. Other writes are confined to the
  granted roots (the workspace and temp directories). With
  `sandbox_allow_read_outside_workspace: false`, Landlock
  also handles read/execute access and grants it only to the workspace,
  temporary/writable roots, conventional system runtime/configuration roots,
  executable directories from `PATH`, and explicit
  `sandbox_readable_roots`. System roots such as `/usr`, `/lib`, and `/etc`
  stay readable so normal dynamically linked tools, TLS, identity lookup, and
  package clients continue to work; ungranted user data does not.
- Landlock ABI v4+ denies TCP connect/bind unless
  `permissions.sandbox_allow_network` is true. ABI v10+ additionally handles
  UDP bind/connect/send, including DNS. Below ABI v4, only the filesystem is
  confined; on ABI v4–v9, UDP remains reachable and `collo doctor` reports
  TCP-only isolation.
- `require` checks capabilities rather than merely checking that Landlock
  exists. With `sandbox_allow_network: false`, ABI v4–v9 fails closed because
  UDP cannot be denied completely; ABI v10+ satisfies the full network-denial
  request. `auto` still applies filesystem confinement and the available
  network controls while reporting any limitation in `collo doctor`, `/status`,
  and command output.

The Linux kernel's [Landlock userspace API documentation](https://docs.kernel.org/userspace-api/landlock.html)
defines the read/execute rights, TCP and ABI v10 UDP rights, ruleset layering,
and special-filesystem limitations used for this capability reporting. The
project's [Linux sandbox setup and Landlock compatibility guide](LINUX_SANDBOX.md)
provides the kernel/ABI matrix, Ubuntu 26.04 behavior, configuration recipes,
host verification, custom-kernel requirements, and container/WSL
troubleshooting.

**Windows 11: AppContainer + Job Object**:

- A workspace-specific AppContainer SID provides low-integrity filesystem,
  registry, credential, device, network, and cross-process isolation.
- Collomia grants that SID access to the workspace, the user temp directory,
  explicit `permissions.sandbox_readable_roots` (read/execute), and explicit
  `permissions.sandbox_writable_roots` (read/write). User-local executable
  directories on `PATH` receive read/execute access, not write access. The
  normal user's existing access checks still apply as well. AppContainer
  always restricts user-data reads even though the compatibility read switch
  defaults to broad reads on macOS/Linux; granting the whole user profile is
  deliberately not used to weaken the Windows boundary.
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

- Read confinement is opt-in on macOS/Linux and always present for Windows
  AppContainer. It protects ordinary user data, not public operating-system
  files, executable PATH entries, temp paths, or explicit grants. On macOS,
  metadata remains visible and the content boundary targets user homes and
  mounted volumes because a global Seatbelt content denial prevents stable
  process startup on current macOS. Linux Landlock provides the stricter
  allowlisted filesystem view, but pseudo-filesystems have kernel-specific
  mediation limits. Do not describe either mode as a secret vault.
- `auto` never silently equates partial enforcement with a complete sandbox:
  it emits an actionable degradation warning. `require` refuses a command if
  any requested write, read, or network protection is unavailable.
- Network policy remains all-or-nothing for a command. Domain-scoped egress is
  roadmap work; environment-only proxy settings would not be a security
  boundary because a hostile command could bypass them.
- Package managers and build tools may need dependencies or caches outside the
  workspace. Prefer a narrow `sandbox_readable_roots` entry for immutable
  inputs and `sandbox_writable_roots` only for paths that must change. Writable
  roots are implicitly readable. `command_env: "minimal"` can also hide proxy
  variables or registry credentials; switch to `full` only when that tradeoff
  is intended.

## Repository trust

A repository can ship `.collomia.json` (permissions, MCP servers) plus
skills and instruction files. All of it is quarantined until you run `collo
trust` after review. Trust is bound to the file's SHA-256; any change
re-quarantines. The trust database lives at `~/.collomia/trust.json` on
macOS/Linux or `%USERPROFILE%\.collomia\trust.json` on Windows, never in the
workspace, so a repository cannot approve itself.

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

## Delegated-agent boundary

Delegated agents use the same security boundary as the parent and can only be
made more restrictive. An `agents.<name>` profile may choose a smaller tool or
skill allowlist, add denied tools or command regexes, add `prompt`/`deny`
permission rules, and lower the autonomy mode. Configuration validation rejects
an agent-level `allow` rule. Parent and built-in denials remain additive, and a
child cannot enable outside-workspace access, network access, user-data reads,
or a weaker sandbox policy that the parent did not have. Disabled tools are
checked again at execution time, not merely hidden from the model's tool list.

Every child gets a distinct permission manager and audit ledger. A child
approval is shown through the normal themed approval dialog with the delegated
task name and ID, and approval affects only that proposed action. Write-capable
children get independent Git worktrees; Collomia records their changed files
and common-base hunk ranges but never commits, merges, or chooses between
sibling changes automatically. Retained worktrees may contain user-reviewable
changes after a task ends.

The session-wide FIFO scheduler bounds total and per-provider concurrency.
Each task also has a queue-inclusive timeout, maximum iteration count, and
optional token budget. Token enforcement uses provider-reported usage when the
adapter supplies it and estimates the next request before sending; providers
may report usage only after a response, so a final response can exceed the
configured token target. Timeouts and iteration limits remain the hard fallback.
These controls limit agent work, not operating-system CPU or memory consumption.

`/agents stop <id-or-name>` and `alt+a` cancel one queued or active child.
Closing Collomia requests cancellation for every child and stops background
processes owned by write agents. Durable sessions keep bounded status, summary,
evidence, usage, and change manifests—not raw child transcripts. On resume,
recorded completed outcomes remain visible and any nonterminal task becomes
`interrupted`; it is never automatically scheduled again. This avoids duplicate
mutations but means the user or parent must explicitly start replacement work.

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

### Durable conversation and retained tool output

Session transcripts are operational history, not a secret vault. They can
contain source text, prompts, tool arguments/results, command errors, and data
returned by external services. When a returned string exceeds
`options.max_tool_output_bytes`, a durable session may additionally retain up
to 4 MiB of that result under an opaque ID; total retained-result storage is
capped at 32 MiB per session. `read_tool_result` reads bounded ranges from this
local copy and never reruns the originating action.

Session JSONL and result-artifact files live under the workspace-specific
directory in `~/.collomia/sessions/` (or `%USERPROFILE%\.collomia\sessions\`
on Windows), outside the repository, with owner-only modes where the platform
supports them. Artifacts remain outside model context until explicitly read,
are framed as untrusted content, follow forks, and follow rewind branches only
when referenced by the retained conversation prefix. They are removed with
their session and excluded
from support bundles. None of these properties redact arbitrary stored tool
output or encrypt it at rest; protect the user account and delete sensitive
sessions when they are no longer needed.

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

## Support-bundle privacy boundary

`collo support bundle` performs local, read-only inspection without creating
the application runtime, contacting providers or MCP servers, opening
sessions, executing tools, or making network requests. Its default manifest
uses aggregate counts and anonymous configuration keys. It excludes
configuration values, environment variable names and values, credential
references, provider and MCP names/definitions, endpoint/model/deployment
details, workspace paths and files, prompts, transcripts, sessions, audit
records, and logs. Configuration validation failures are represented by a
generic status because detailed validator errors can echo user-defined names,
paths, patterns, or values. Default collection also suppresses environment
expansion, so `api_key_env` and MCP environment references are not fetched.

Logs require the explicit `--include-logs` flag and are bounded to five files,
1 MiB per file, and 3 MiB total. Configured/common credential values are
redacted, exact home/workspace paths are normalized, and terminal controls are
removed. This explicit mode resolves configured secret references locally only
to register their values with the redactor; it never writes those values to the
manifest. Those transforms are defense in depth: arbitrary source/tool output
can contain sensitive material no pattern matcher recognizes. Inspect every
archive before sharing it. Bundles are created with owner-only permissions
where the operating system supports them, use same-directory atomic publish,
and refuse to overwrite an existing path even if it appears during creation.

## Prompt injection

Tool output, repository text, skills, and MCP responses are external data. A
sufficiently capable injection can still steer the model, so prose is not the
security boundary.

Every model-visible MCP tool result, resource, resource catalog, and expanded
prompt template is wrapped in an `EXTERNAL_MCP_DATA` content-derived boundary
that identifies its server, content type, subject, and byte count. Its handling
guidance explicitly permits using relevant factual and structured data while
refusing instructions, claimed authority, or claimed permissions embedded in
the payload. Terminal control characters are removed. Server-supplied
tool-schema descriptions/titles are labeled external and descriptive and are
bounded; schema comments and examples are discarded. Catalog and elicitation
metadata is likewise control-safe and bounded. A server's `trusted` setting
authorizes Collomia to connect to and run that server; it does not give the
server's returned text instructional authority.

These frames make provenance clear to both users and models, but they are not
an instruction-following guarantee. The controls that hold regardless of what
the model was told are the permission pipeline (MCP calls remain external
risk), denied commands, uninspectable-command prompts, repository/server trust
gates, rooted structured-file mutations, and—when enabled—the OS sandbox. A
credential-free adversarial evaluation verifies that an allowed MCP-like read
containing a forged permission grant still cannot authorize a workspace write.

## Image attachment storage

User-selected and supported MCP-returned images are copied into the active
session's per-user storage, never into the repository. Durable session JSONL
contains only a random attachment ID, display name, MIME type, size, and
SHA-256 digest; provider requests resolve the owner-only raw blob and verify its
regular-file status, size, detected type, and digest immediately before send.
Limits are 5 MiB per image, four images per turn/tool batch, and 24 MiB per
session. Only PNG, JPEG, GIF, and WebP are accepted; active SVG and arbitrary
binary files are refused. Fork copies attachments, rewind copies only IDs
reachable from retained messages, and delete removes the attachment directory.

Attaching an image is an explicit data disclosure to the selected provider.
The submit-time read is anchored to the workspace directory, so changing a
path component or symbolic link after selection cannot redirect it elsewhere
in the user account. It does not redact pixels, strip EXIF or other embedded
metadata, or determine whether a screenshot contains credentials. Inspect and
sanitize images before sending them to a hosted model. Unsent selections are
kept only in the running TUI; user images are copied only after the
`user_prompt` hook accepts the submission, so a blocked turn leaves no blob.
MCP images retain external-data provenance and never authorize a later action.

## Reporting

Security issues: open a GitHub issue marked security, or contact the
maintainer directly.
