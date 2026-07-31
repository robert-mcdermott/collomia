# Compatibility and migration policy

Collomia stores configuration, sessions, automation events, attachments, and
support metadata on the user's machine. This document defines which formats
are stable, how version changes are handled, and what to do before upgrading
or downgrading.

Collomia is currently a technical beta. The project still treats persisted
user data and the documented automation interface as compatibility contracts:
an upgrade must either read an existing supported format or reject it clearly
without rewriting it.

## Versioned formats

| Format | Current version | Compatibility behavior |
| --- | --- | --- |
| User/project configuration | `schema_version: 1` | A missing version is legacy version 1. Normal loading tolerates unknown fields; strict validation rejects them. A newer version is rejected before activation. |
| Headless runtime events | `schema: 1` | Optional additive fields are allowed. Unknown event kinds, incompatible required fields, and other schema versions are rejected by `collo replay`. |
| Durable session records | `schema_version: 1` | New records carry the version on every JSONL line. Legacy records without it are version 1. Unknown optional fields are ignored; a newer version is rejected without appending to the session. |
| Referenced tool-result artifacts | `schema_version: 1` | The stored object must match the supported version, ID, size, and quota checks before it is returned. |
| Support-bundle manifest | Versioned in the manifest | Intended for diagnostics, not restoration. Readers should tolerate additive fields and reject unsupported incompatible versions. |

Image attachment blobs do not contain an executable schema. Their session
references carry type, size, and SHA-256 metadata, all of which are rechecked
before provider delivery.

Trust records, MCP pins, debug logs, and generated configuration references are
internal operational data. They are not public extension APIs. When an internal
cache or pin can be safely reconstructed, a future release may require the user
to review or recreate it instead of attempting an unsafe migration.

The audit ledger sits between those two groups. It is not an extension API and
carries no schema version, but it is the record of what an agent was permitted
to do, so it is never rewritten or migrated in place, and its reader tolerates
entries written by any earlier release: fields added since — `session`,
`actor`, `task`, and the `gap` and `rotation` entry kinds — are additive, and
an older entry lacking them reads as unattributed rather than as an error.
`collo audit` reports an entry it cannot parse as an unreadable line rather
than discarding it silently.

## Additive and breaking changes

These changes normally remain within the current version:

- adding an optional JSON field;
- adding an optional configuration setting with a safe default;
- adding an enum value that old consumers are explicitly documented to ignore;
- adding human-readable diagnostic detail without changing the stable status
  or exit code.

These changes require a schema-version decision and normally a version bump:

- removing or renaming a field;
- changing a field's type, unit, required status, or meaning;
- changing the ordering or lifecycle guarantees of automation events;
- adding an event or record kind that older readers cannot safely ignore;
- changing how a persisted permission, mutation, or delegated task would be
  interpreted.

The Go structure alone does not decide compatibility. The observable meaning
and safety behavior do.

## Configuration upgrades

Validate before and after upgrading:

```sh
collo config validate --strict
collo config show
```

Collomia does not silently rewrite configuration files. Version 1 currently
needs no migration command. When a future configuration version requires a
change, the release notes and migration documentation must specify:

1. the oldest directly supported source version;
2. whether migration is automatic, previewable, or explicit;
3. the exact backup created before a write;
4. any setting whose default or security behavior changes;
5. how to validate and, where possible, roll back.

### Sandbox default transition

The current runtime default for an omitted `permissions.sandbox` field is
`auto`, replacing the earlier `off` default. This is an intentional security
behavior change within schema version 1:

- an existing *global* file that explicitly says `"sandbox": "off"` remains off;
- a *project* file that says `"sandbox": "off"` is refused and reported (see
  the containment-precedence change below), keeping the inherited mode;
- an existing file that omitted the field begins using `auto` after upgrade;
- new global starter files write `"sandbox": "auto"`;
- project starter files continue to omit the field and inherit the user or
  built-in setting;
- Collomia does not edit an existing file during the transition.

The compatibility-first companion defaults remain
`sandbox_allow_network: true` and
`sandbox_allow_read_outside_workspace: true`. Sandboxed commands use the
minimal environment when `command_env` is omitted. Before or after upgrading,
run `collo config show` and `collo doctor`; add narrow readable/writable roots
for SDKs and caches where needed. Add an explicit `"sandbox": "off"` only when
the prior unsandboxed behavior is deliberately required.

### Containment-precedence change

A repository can now tighten any containment setting but never weaken one.
This is an intentional security behavior change within schema version 1, and
it can change the effective policy of an unmodified project file:

- affected settings are `sandbox`, `sandbox_allow_network`,
  `sandbox_allow_read_outside_workspace`, `command_env`, `network`,
  `commands`, and `allow_outside_workspace`;
- it applies identically to an explicit field and to `permissions.preset`;
- a project file that weakens one of these is refused, not applied, and the
  refusal is printed by `collo config show` and `collo config validate`;
- your global configuration is unaffected — it may still select
  `"sandbox": "off"` or `"preset": "frictionless"`;
- no file is rewritten.

A project that relied on `.collomia.json` setting `"sandbox": "off"` (or
`"command_env": "full"`) must move that setting to the global configuration,
or select `"preset": "frictionless"` there. Run `collo config show` after
upgrading to see whether anything was refused.

### Credential-file protection

`permissions.protect_credentials` defaults to `prompt`, so an action reaching a
well-known credential store now always asks. This is an intentional security
behavior change within schema version 1 and can add approvals to a workflow
that previously ran unattended:

- an existing file that omits the field begins using `prompt` after upgrade;
- under `autopilot`, a command naming a credential file that used to run
  without asking now stops for approval, and a headless run fails closed
  because no approver is present;
- a tool-wide `allowed_tools` entry and a blanket `allow` rule no longer cover
  such an action; a rule naming the path still does;
- `"protect_credentials": "off"` restores the earlier behavior exactly, and
  `"preset": "frictionless"` selects it;
- the setting is clamped like every other containment field: a project may
  raise it but never lower it;
- Collomia does not edit an existing file during the transition.

The protected and exempt locations are listed in the
[user guide](USER_GUIDE.md#credential-files). If a scheduled automation reads
`.env` or a deploy key, either set `protect_credentials` to `off` for that
environment or add a rule naming the file, and verify with `collo policy check`
before relying on the run.

### Publication protection

`permissions.publication` is new and defaults to `prompt`, so an action that
puts something outside this machine now always asks. Like credential-file
protection this is an intentional security behavior change within schema
version 1, and it is the one most likely to interrupt an existing automation:

- an existing file that omits the field begins using `prompt` after upgrade;
- under `autopilot`, `npm publish`, `cargo publish`, `docker push`,
  `gh pr create`, `gh release create`, `kubectl apply`, `helm upgrade`,
  `terraform apply`, `aws lambda update-function-code`, `git push`, and
  `ssh host <command>` used to run without asking and now stop for approval; a
  headless run fails closed because no approver is present;
- a tool-wide `allowed_tools` entry, a session "always allow", and an `allow`
  rule naming only an executable no longer cover such an action; a rule naming
  the *operation* (`{"action": "allow", "command": "npm publish"}`) does;
- read verbs, rehearsals (`--dry-run`), `npm install`, `go mod tidy`,
  `docker pull`, `terraform plan`, and download-direction copies are
  unaffected;
- `"publication": "off"` restores the earlier behavior exactly, and
  `"preset": "frictionless"` selects it; `"preset": "hardened"` selects
  `deny`;
- the setting is clamped like every other containment field: a project may
  raise it but never lower it;
- Collomia does not edit an existing file during the transition.

The complete catalogue is in the
[user guide](USER_GUIDE.md#publishing-outside-this-machine). If a scheduled
automation publishes or deploys, either set `publication` to `off` for that
environment or add rules naming the operations it needs, and verify with
`collo policy check '<command>'` before relying on the run — it prints the
exact operation string a rule has to name.

### Operation-scoped command rules

A rule's `command` matcher now has two forms: without a space it remains an
executable-name glob, and with a space it matches an operation such as
`npm publish` or `gh pr create`.

Every existing rule keeps its meaning, because a space in that field previously
matched nothing at all. That is the compatibility note worth reading: a rule
written as `{"action": "deny", "command": "npm publish"}` used to be **silently
inert** and validated clean, so an upgrade may activate a denial an author
believed was already in force. Two changes make that visible:

- `collo config validate` now rejects a `command` pattern that could match
  neither form (leading, trailing, or repeated spaces);
- `collo policy check` prints an `operations:` line naming exactly what a
  command declares.

Review any existing `command` patterns containing a space before upgrading an
unattended deployment.

### Scoped egress

`permissions.sandbox_egress` is new and defaults to `off`, so no existing
configuration changes meaning: command networking continues to follow the
all-or-nothing `sandbox_allow_network`, and no preset selects the new value.

Adopting `"scoped"` is a deliberate behavior change worth planning:

- a sandboxed command reaches only the hosts named by `allow` rules carrying a
  `host`, so a build that fetches from a registry no rule names will fail until
  a rule is added — `collo policy check` reports which endpoints would be
  refused before you rely on a run;
- `sandbox_allow_network` no longer decides command egress while `scoped` is
  active; the OS-level denial is what makes the broker a boundary;
- on Linux and Windows the setting is refused under `"sandbox": "require"` and
  degrades visibly under `"auto"`, because neither platform can enforce it —
  see [SECURITY.md](SECURITY.md#scoped-egress-macos-only). A configuration
  shared across platforms should expect that asymmetry rather than assume
  uniform behavior;
- with `"sandbox": "off"` no broker starts at all;
- the setting is clamped like every other containment field: a project may
  raise it but never lower it.

A newer configuration is rejected with an instruction to upgrade the binary.
Normal loading's unknown-field tolerance supports forward-compatible optional
settings; use strict validation in CI and after manual edits to catch typos.

### Reported prompt token counts

Collomia now requests Anthropic prompt caching, and the usage it reports on
those routes changed meaning as a result. This affects anyone computing spend
or context pressure from a token count rather than from `cost_usd`.

The Anthropic Messages API reports `input_tokens` *net* of both cache counters:
a request whose entire prompt was served from cache reports an `input_tokens`
of roughly zero and the real figure under `cache_read_input_tokens`. Passing
that through unchanged would have understated the prompt by whatever the cache
served — a measured live request reported a raw `input_tokens` of 2 for a 9,629
token prompt — collapsing the context gauge exactly when the context was
fullest and pricing most of the prompt at nothing.

So on the `anthropic`, `anthropic-compatible`, and `azure-foundry-anthropic`
routes:

- `input_tokens` is now the whole prompt: uncached plus cache reads plus cache
  writes. It previously excluded the cached portion. Comparing this field
  across the upgrade will show an apparent increase that is a correction, not
  new consumption;
- `cached_tokens` is the portion served from cache. It was always present and
  was always zero before this release, because nothing ever requested a cache;
- `cache_write_tokens` is new and additive under schema v1, reporting tokens
  written to the cache. Consumers tolerating unknown optional fields, as
  [automation consumers](#automation-consumers) are asked to, need no change;
- `cost_usd` already accounts for all three at their separate rates and is the
  field to compare across this boundary. Cache reads bill below ordinary input
  and cache writes above it, so a spend figure recomputed from `input_tokens`
  alone will now overstate cost on a cache-warm session.

The OpenAI, Bedrock, and Bedrock Mantle routes are unaffected: OpenAI reports a
gross `input_tokens` and caches implicitly, and Bedrock is declared without
cache support rather than sending a cache point that varies by model and
region.

The event schema stays at v1 deliberately. The field's name, type, and required
status are unchanged and the new field is optional, so no existing reader
breaks; what changed is that a number that was quietly wrong is now right.
Bumping the schema would force every strict client to update in order to
receive a correction.

### Steering a running turn

Pressing `enter` on a non-empty draft while a turn is running now sends that
text to the agent as steering guidance. It previously held the draft in the
composer with a message explaining that it would be sent after the turn ended.

Nothing about permissions changed. Steering arrives at an iteration boundary as
an ordinary user message that grants nothing: an action that would have prompted
before the guidance arrived still prompts after it, and guidance never lands
inside an in-flight provider call, an executing tool, or a pending approval.
Guidance that was never delivered — because the turn ended or was cancelled
first — is discarded and reported rather than held against later work.

This changes an interactive surface only. Headless runs, `collo` in
non-interactive mode, and the JSONL event stream are unaffected.

## Sessions and upgrades

Durable session JSONL is append-only. Current releases:

- load legacy version-1 records that predate the explicit `schema_version`;
- tolerate unknown optional fields;
- discard only a malformed final line, treating it as a possible crash-torn
  write;
- reject an unsupported record version before opening the session for append;
- never execute a stored tool call during load, replay, fork, or rewind.

Before a major upgrade, back up `~/.collomia/` on macOS/Linux or
`%USERPROFILE%\.collomia\` on Windows if the sessions are important. Do not
edit live session JSONL by hand.

Downgrading is not guaranteed. An older binary may not understand data created
by a newer release even when the newer release can still read the older data.
Use the previous binary with a backup made before the upgrade rather than
pointing it at state already updated by a newer version.

## Automation consumers

Use `collo schema events` to retrieve the JSON Schema embedded in the exact
binary being run. Consumers should:

- select behavior using `schema`, `kind`, and `run.result.status`;
- tolerate unknown optional fields;
- avoid inferring success from streamed text or an intermediate error;
- require the final `run.result` for a complete run;
- fail clearly on an unsupported schema or event kind.

Adding a new event kind is not treated like adding an optional field: existing
strict replay clients reject unknown kinds, so the change requires an explicit
compatibility decision.

## Release and developer checklist

Any change to a persisted or machine-readable structure should include:

1. a compatibility classification: additive, breaking, or internal-only;
2. fixtures proving the oldest supported input still loads;
3. a rejection test for unsupported future versions;
4. an updated embedded schema or format constant when applicable;
5. migration and rollback documentation before release;
6. roadmap, capability, automation, and user-guide updates where behavior is
   user-visible.

Compatibility tests are credential-free and run on macOS, Linux, and Windows.
The release workflow repeats them against the exact tagged source.
