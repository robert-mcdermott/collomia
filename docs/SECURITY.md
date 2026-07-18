# Collomia security model

This document states what each control actually guarantees, what it does
not, and where the boundaries are. It is the "documentation truth pass"
required before advertising any unattended use.

## The honest summary

Collomia's permission prompts and rules are **in-process policy checks**, not
an operating-system security boundary, unless the OS sandbox is enabled. A
command approved by you — or auto-approved by autopilot mode — runs with
your normal user privileges. On macOS you can additionally enable OS-level
enforcement (`permissions.sandbox`); on Linux and Windows no sandbox backend
exists yet, and `collo doctor` says so.

Do not point autopilot mode at untrusted code or untrusted instructions and
walk away, on any platform, without the sandbox in `require` mode — and even
then, understand the limitations listed below.

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

## The OS sandbox (macOS today)

`permissions.sandbox: "auto" | "require"` wraps every agent command in a
Seatbelt profile via `sandbox-exec`:

- File writes are confined to the workspace, the temp directories, and
  `/dev`; everything else is deny-by-default (reads stay open).
- Network egress is denied unless `permissions.sandbox_allow_network` is
  true (loopback stays open for local model servers).
- `require` fails closed: if the backend is unavailable, commands refuse to
  run. `auto` degrades with a visible doctor warning.

Known limitations, stated plainly:

- `sandbox-exec` is deprecated by Apple but functional; we treat it as
  best-effort OS enforcement, tested in `internal/tools/command_test.go`.
- Reads are not confined: a sandboxed command can still read any file your
  user can. Write and network containment are the enforced properties.
- Linux (Landlock/seccomp) and Windows (AppContainer) backends are roadmap
  work. On those platforms the sandbox setting can only be `off` (run
  unconfined after approval) or `require` (refuse to run).

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

## Secrets

Configured provider keys, MCP headers/env values, and common credential
shapes (OpenAI/Anthropic/AWS/GitHub/Slack keys, JWTs, bearer tokens) are
redacted from debug logs, JSONL events, and the audit ledger. Redaction is
best-effort defense in depth — it reduces accidental exposure and does not
defeat deliberate exfiltration. The parent environment is currently passed
to commands; narrowing it is roadmap work.

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
