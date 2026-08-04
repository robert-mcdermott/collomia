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
| Runtime-owned goal-graph snapshots | `schema: 1` | OG-1 onward snapshots are carried by additive `goal_graph` session records. The complete graph is validated before restore; unsupported or structurally inconsistent snapshots are rejected rather than scheduled, and a saved TUI graph remains inert until explicit `/orchestrate resume`. OG-2B1 adds optional execution/read-envelope/worker-usage fields; a pre-fan-out snapshot restores omitted execution as serial `primary`, so an upgrade cannot create automatic workers. OG-2B2a adds optional pause request/reached/reason fields; omission restores as not paused. OG-2B2b1 adds aggregate accounting plus attempt iteration/cost-estimate fields; a legacy snapshot reconstructs only stored attempt usage and does not invent missing proposal or iteration history. OG-2B2b2 adds fixed aggregate limits, active-time state, and per-read cost/iteration bounds. OG-3A adds explicit narrow writer scopes, a fixed writer envelope, writer accounting, stable-base identity, retained-candidate and child-verification facts, and the `candidate` attempt state. OG-3A.1 raises the hard token ceiling for newly created graphs but does not rewrite a nonzero stored limit, so a graph created under the earlier 192,000-token envelope resumes with 192,000 rather than acquiring the new 1,000,000-token allowance. OG-3A.3 adds optional attempt `last_progress_iteration` and pending-action base workspace/generation fields. OG-3A.4 adds optional `completion_gap` and `completion_gap_iteration` attempt fields; omission means no previously recorded exact gap and grants no inferred remediation progress. Omission never invents prior exact progress; an active legacy attempt with successful tool evidence receives one fresh bounded progress lease on its next accounted provider cycle, while the stored aggregate envelope remains unchanged. Omitted limits restore to the current experimental defaults; legacy nodes remain serial, interrupted writers become blockers, restore freezes active time at the last durable update, and stored limits are rejected when implausible or inconsistent with the recorded envelope grant rather than for exceeding a build default (see the OG-3B4 note below). |
| Referenced tool-result artifacts | `schema_version: 1` | The stored object must match the supported version, ID, size, and quota checks before it is returned. |
| Support-bundle manifest | Versioned in the manifest | Intended for diagnostics, not restoration. Readers should tolerate additive fields and reject unsupported incompatible versions. |

OG-3A.6 does not change either schema version. Ending a graph's role as the
session's current resumable graph appends a `goal_graph` record with no graph
payload. Replay already applies the latest record, so that tombstone clears
only the live pointer while older snapshots remain in the append-only file.
Accepted-node handoffs use the existing `compaction` record and preserve the
full transcript under the established compaction contract.

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

### Model token limits

Two changes, both to configurations that were already legal.

**`collo config validate` now rejects a `max_tokens` at or above
`context_window`.** The output cap is spent out of the same budget as the
prompt, so no request could ever satisfy such a provider block; it previously
validated clean and surfaced mid-session as a provider error naming neither
field. Lower `max_tokens` or raise `context_window`. Nothing else about the two
fields is now required: an absent `context_window` still loads, because it
always has, and is reported by `collo doctor` rather than refused.

**A `max_tokens` larger than the model accepts is now retried rather than
failing the turn.** When a provider's HTTP 400 states its own ceiling, that
request is resent under the stated ceiling, a warning names both numbers, and
the ceiling is remembered for the active model for the life of the session. The
configuration file is never modified. A rejection with no recognizable ceiling
in it behaves exactly as before: the provider's error surfaces unchanged. This
applies to the OpenAI-protocol and Anthropic Messages routes.

`collo setup` also now always writes both fields where it previously wrote a
constant `context_window` and no `max_tokens` at all. Existing configurations
are untouched; re-run `collo setup --provider <name>` to have the numbers
resolved from the endpoint.

### Editor schema and the `$schema` key

Three changes, none of which alters how any existing configuration behaves.

**`$schema` is now a recognized configuration key.** It names the JSON Schema
an editor should use for the file and configures nothing. It was previously an
unknown field, which ordinary loading ignored and `collo config validate
--strict` rejected — so a file carrying it would have validated inconsistently
depending on the flag. Declaring it removes that split. A file without the key
is unaffected.

**`collo init` now writes a second file.** `collomia.schema.json` is created
beside the configuration, and the configuration's `$schema` points at it
relatively. The file is generated output, is never read by Collomia, and can be
deleted or gitignored; deleting it costs only the editor assistance, and
`collo doctor` will then report the reference as dangling. The path is printed
when it is created, so a project's copy does not appear in a repository
unannounced.

**`collo setup` adds `$schema` to a configuration it writes to, and refreshes
the sibling schema on every run.** It adds the key only when the file does not
already have one, so a `$schema` pointing at a shared or hosted contract is
left exactly as it was. No other key is touched, and the sparse-file guarantee
is unchanged: settings this build does not know about still survive untouched.

Regenerate the schema after upgrading Collomia — `collo schema config >
collomia.schema.json`, or simply re-run `collo setup`. A schema left over from
an older build describes fields the running one may not have, which
`collo doctor` reports as a warning rather than leaving to be discovered
through wrong completions.

`/config` output changed shape entirely: it previously printed the active
configuration path and a sentence about precedence, and now reports the layers
in order, the effective value and origin of each safety setting, and any
project weakening that was refused. There is no configuration for this and
nothing consumes it programmatically; scripts should use `collo config show`,
which is unchanged.

### Git write tools

Two new built-in tools, `git_commit` and `git_branch`. Both are visible to the
model by default, neither is available in planning mode, and
`options.disabled_tools` removes either. Nothing existing changes: the
read-only Git tools, `run_command`, and every permission setting behave exactly
as before, and a configuration written before this release needs no edit.

This is additive in capability terms rather than in permission terms, and the
distinction is worth stating. The agent could already commit — `run_command
"git commit -m …"` has always worked, and on a stock configuration
`collo policy check 'git commit -m test' --autonomy autopilot` reports
**allow**, because the safety classifier describes destruction and a commit
destroys nothing. What changes is that a commit made through `git_commit`
declares the files it will contain, so `permissions.protect_credentials` can
act on them: committing a tracked `.env` or a private key now prompts by
default and is refused under `deny`. The equivalent `run_command` invocation
still cannot be gated that way, because `git commit -a` names no path for the
shell analysis to classify.

The guarantee is exact: **a commit contains the files named in `paths` and
nothing else.** `paths` is required, and the commit runs as
`git commit -- <paths>`, so:

- unrelated changes in the working tree stay uncommitted, including the user's
  own edits in progress;
- anything the user staged by hand stays staged — the tool commits around a
  hand-built index rather than through it;
- untracked files are never swept in, so build output, scratch files, and a
  local `.env` cannot ride along. A new file is committed when it is named, and
  only then.

There is deliberately no "commit everything that changed" mode. It would decide
a commit's contents from whatever happened to be in the working tree, which is
the one thing this tool exists not to do.

`git_branch` only creates. Switching to an existing branch is refused, because
a checkout rewrites the working tree from outside Collomia's change tracking
and `/restore` verifies the workspace before reversing anything — a switch
would leave earlier turns unrecoverable. Use `run_command` when a real checkout
is wanted.

Pushing is unchanged and stays with `run_command` under
`permissions.publication`. There is no push tool.

## Sessions and upgrades

Durable session JSONL is append-only. Current releases:

- load legacy version-1 records that predate the explicit `schema_version`;
- tolerate unknown optional fields;
- discard only a malformed final line, treating it as a possible crash-torn
  write;
- reject an unsupported record version before opening the session for append;
- never execute a stored tool call during load, replay, fork, or rewind;
- restore an internal `goal_graph` record only when that programmatic mode is
  explicitly requested, then retry interrupted read-only work in a new attempt
  while converting any ambiguous non-replayable action into a blocker.

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

- select process behavior using `schema`, `kind`, and `run.result.status`, and
  use the additive `run.result.outcome` when goal-level
  done/blocked/cancelled/budget-exhausted behavior matters;
- tolerate unknown optional fields;
- avoid inferring success from streamed text or an intermediate error;
- require the final `run.result` for a complete run;
- fail clearly on an unsupported schema or event kind.

Adding a new event kind is not treated like adding an optional field: existing
strict replay clients reject unknown kinds, so the change requires an explicit
compatibility decision.

The OG-1/OG-2 decision is deliberately narrow: `goal.graph.update` remains an
additive schema-v1 kind. Standard CLI/TUI/headless streams never emit it. The
explicit TUI-only `/orchestrate` preview does emit it into that session's
durable activity record, but there is no headless activation flag and no
persisted setting can opt a process in. A consumer of an experimental graph
trace must use a binary/schema that knows the kind; an older strict replay
correctly rejects that trace. The established meanings and required payloads
of every pre-existing schema-v1 kind remain byte-compatible. OG-2B2b must revisit
event versioning before Orchestrated Goal is exposed to headless automation.

`plan.steps[].acceptance` is an additive optional session-plan field. Older
readers ignore it; ordinary plans may omit it. The explicit Orchestrated Goal
approval path requires at least one non-empty criterion per proposed node.
`plan.steps[].execution` is also additive and optional. Empty and `primary`
mean serial primary execution; only an explicitly approved `read_only` step is
eligible for the bounded automatic worker path. Goal snapshot `read_fanout`
and attempt worker/usage fields are internal additive schema-1 state, with
restore tests pinning the serial legacy default.

OG-3A additively permits `execution: isolated_write` only alongside explicit
narrow `plan.steps[].write_paths`. Graph node scopes, `writer_fanout`,
automatic-writer accounting, attempt base-commit identity, bounded retained
candidate facts, verification results/tokens, and attempt state `candidate`
remain internal schema-1 additions. A legacy snapshot contains no
`isolated_write` node and therefore cannot gain an automatic writer during
normalization. Missing writer bounds restore to the fixed two-concurrent,
two-start ceiling; stored values wider than this build are rejected. Running
writer attempts require a stable parent token/commit and recovery converts
them to non-replayable interruption blockers rather than scheduling new work.
OG-3A.4 changes approval/revision policy rather than the node encoding:
end-to-end proposals use `primary`, while `isolated_write` is accepted only in
candidate-only executable graphs whose writers are terminal leaves. Existing
schema-1 snapshots still restore under structural `ValidateSpec`; the stricter
rule applies when a new proposal is approved or a live graph is revised, so an
upgrade does not retroactively reinterpret persisted node state.
OG-3A.5 adds no persisted field. It refines how the existing completion-gap
watermark advances: a real observed workspace repair or novel failed-verifier
evidence can renew the bounded remediation window, while equivalent failures
cannot. Existing blocked attempts remain immutable; an eligible operator retry
can explicitly reattach the saved terminal graph and starts a fresh attempt
under the corrected behavior without changing the schema.
OG-3A.6 likewise keeps schema 1. A payload-free `goal_graph` record is a
session-current tombstone: older graph snapshots remain in the append-only log,
but replay leaves no graph eligible for resume. The deterministic accepted-
node handoff uses the existing compaction record, so the complete transcript
continues to survive while only active provider context is replaced.
OG-3A.7 changes no persisted shape. Proposal plan status/evidence fields keep
their existing meanings in ordinary plans, while Orchestrated Goal approval
continues to write a fresh pending plan and schema-1 graph rather than treating
those model-authored fields as execution history.
OG-3A.8 is additive within schema 1. Attempts gained `completion_gap_kinds` and
`evidence_pruned`; a snapshot written before them recovers its gap kinds from
the stored sentence once at restore, and clears a gap this build cannot
recognize rather than retaining one it could not enforce. The `awaiting_review`
node state and graph outcome are new values in existing fields: an older
snapshot that recorded a retained candidate as a blocked node is restored as
awaiting review, and `goal.graph.update` — internal-only by the OG-1
decision — adds `awaiting_review` to its outcome enumeration. The public
`run.result` outcome enumeration is deliberately unchanged, so automation
consumers see no new value. Evidence pruning removes only ordinary tool results
from the snapshot; the complete transcript remains in the append-only session
log, and the attempt records how many records were dropped.
OG-3B1 is additive within schema 1. Attempts gained optional `worktree` and
`branch` fields recording the isolated tree an attempt caused Git to create,
written durably before the child runs. The two are written and validated
together, so a snapshot carries either both or neither. Omission means only
that the snapshot predates the record: an older interrupted writer still
restores as a blocker, described in the earlier general terms rather than by
path. A writer that changed nothing has its worktree removed, and the fields
are cleared with it so a restored graph never points at a directory that is
gone.
OG-3B4 is additive within schema 1 and relaxes one validation rule
deliberately. The aggregate budget gained an optional `grant` object recording
the size of a single envelope; a snapshot without it derives the grant from its
stored maximum and extension count, so an older graph keeps exactly the
envelope it had. Stored limits are no longer rejected for exceeding the build
default — that check would now refuse a legitimately configured graph — and are
rejected instead when implausible or inconsistent with the recorded grant.
`options.orchestration_max_iterations`, `orchestration_max_tokens`,
`orchestration_max_cost_usd`, and `orchestration_max_active_wall_seconds` are
new optional configuration fields; omitted or zero keeps the previous fixed
values, so an existing configuration behaves exactly as before.
OG-3B3 is additive within schema 1. Accounting groups and attempts gained an
optional `cached_tokens` field, and the aggregate budget gained an optional
`extensions` count. A snapshot written before them restores with no cached
tokens recorded, so its stored history is charged in full exactly as it was
when written — omission never retroactively refunds allowance. The stored
aggregate limits are still rejected when wider than this build's ceiling; that
ceiling is now the fixed envelope times one plus the recorded extension count,
and the count is itself bounded and validated, so a snapshot cannot claim
allowance that no user granted.
OG-3B5 is additive within schema 1. Attempts gained optional
`worktree_disposition`, `worktree_disposition_detail`, and
`worktree_reconciled` fields recording what the runtime last observed about a
retained tree and when. Omission means the tree has never been observed, which
is the honest state for every snapshot written before this slice and is
rendered as unreconciled rather than as an assumption either way. Validation
rejects a disposition outside the vocabulary, one describing an attempt with no
retained worktree, one without a time of observation, and a time of observation
without a disposition — each of which would be a claim about nothing. A
discarded tree keeps its recorded identity, so a removed candidate stays
distinguishable from one that never existed.
The graduation review changes nothing shipped. It is a recorded decision:
the mode stays experimental, so every compatibility note below continues to
apply unchanged.

OG-5L changes nothing shipped: user-guide guidance and a test that enforces its
citations. No product code.

OG-5K changes one user-visible string and no persisted shape: a node blocked
because its candidate's base moved now names which of the parent workspace or
the Git base changed, and reports the retained candidate. Node reasons are prose
and were never a stable interface.

OG-5J changes nothing shipped: evaluation apparatus only, no product code.

OG-5I changes no persisted shape. The `awaiting_review` outcome reason and the
completion answer gain an account of nodes the graph never started; both are
prose and were never a stable interface. The unstarted account is derived from
existing node state, so every saved snapshot reports it correctly.

OG-5H changes one user-visible string and no persisted shape: the reason on a
node blocked by failed candidate verification now names the diagnosis and the
failing command instead of the child's raw exit-code error. Node reasons are
prose and were never a stable interface.

OG-5G changes nothing shipped: evaluation apparatus only, no product code.

OG-5F changes nothing shipped: it is evaluation apparatus only, with no
product code touched.

OG-5E changes no persisted shape. It derives the superseded-check account from
the workspace tokens already recorded on verification evidence, so no snapshot
field is added and every existing snapshot reports correctly. The `done`
outcome's `reason` gains a clause when such checks exist; that is prose and was
never a stable interface.

OG-5D adds an optional `retired_nodes` array to graph schema 1, each entry
naming a node a revision removed before it completed. It is additive: a
snapshot written before this slice has no such array and validates unchanged,
and a reader that ignores the field sees the graph exactly as it did. Snapshots
carrying one are rejected if it names a node the graph still contains, one that
had completed, or one without identity, reason, or time. The `done` outcome's
`reason` text changes when retirements exist, and the terminal-completion error
now carries that reason where it previously discarded it; both are prose fields
that were never a stable interface.

OG-5C changes no persisted or machine-readable shape, and no behaviour: it
records the publication half of the adversarial corpus against refusals that
already held.

OG-5B changes no persisted or machine-readable shape. It changes which
absolute path string `integrate_delegate` is authorized against: the resolved
one the write tools already use, rather than the configured workspace string
joined to the relative target. On a workspace not reached through a symlink
the two are identical. Where they differ, a path rule that previously failed
to match at integration now matches, which is the defect being fixed — an
existing configuration can therefore see an integration correctly refused
where it was previously allowed, and the refusal names the rule.

OG-5A adds one value to the `integration_checkpoint` record's `state`
vocabulary: `accepted`, meaning a person inspected an interrupted publication
and kept the workspace as it stood. It is additive and resolves the same
`pending` state that `restored` already resolves, so a reader that treats the
field as an opaque string is unaffected, and one that enumerates it sees a
resolved record it does not recognise rather than a pending one it does. No
graph snapshot, event, or `run.result` shape changes; a restart's scheduler
fidelity is a property of existing fields rather than a new one.

The OG-4 closure changes no persisted or machine-readable shape. It records the
milestone's exit-gate evidence and adds one evaluation.
OG-4D changes no persisted or machine-readable shape. Combined-workspace
verification and a user-authored waiver both record ordinary verification
evidence on the accepted attempt, distinguished by the evidence `status`
(`passed` versus `waived`) and by the tool name `verify_combined_workspace`,
both of which existing readers already tolerate as opaque strings.
OG-4C is additive within graph schema 1. It adds the `integrated` node state
and the `awaiting_verification` graph outcome. Neither appears in a snapshot
written before this release, so an older graph restores exactly as it did, and
the public `run.result` outcome enumeration is deliberately unchanged, so
automation consumers see no new value. A snapshot carrying either value is
rejected by an older build's state validation rather than misread, which is the
intended failure direction.
OG-4B adds an `integration_checkpoint` session record, additive within
`schema_version: 1`. It carries the delegate, workspace, state, and each target
path's prior content, mode, and existence, and is appended before a publication
changes the first byte and again with its outcome. A reader that does not know
the type ignores it, exactly as it ignores any unknown optional record; a
session written before this release simply has none, which restores as no
interrupted integrations rather than as an unknown state. Retained content is
bounded per file and per checkpoint, and a path whose content exceeded the
bound is recorded as unrestorable rather than omitted.
OG-4A is additive within schema 1. Durable delegate status records gained an
optional `graph_node` flag marking a candidate owned by an Orchestrated Goal
node. Omission means an ordinary delegate, which is what every record written
before the field existed was, so a resumed older session treats its delegates
exactly as it did — a graph candidate restored from such a session is not
recognised as one, and the graph's own snapshot remains the durable record of
which node owns it. The event schema accepts the field additively and older
readers ignore it.
OG-3C changes no persisted or machine-readable shape. It adds the
isolated-writer wave's product evaluations and records OG-3's exit-gate
evidence.
OG-3B6 changes no persisted or machine-readable shape. It is an adversarial
test campaign over the isolated-writer dispatch and acceptance gates, and it
confirmed rather than altered one classification worth recording: exhausting
the whole-graph writer starts bound ends the graph `budget_exhausted`, not
`blocked`, so it is extendable like every other resource bound.
OG-3B2 changes no persisted or machine-readable shape. Verification recognition
is a runtime decision about a command, and the refused-check diagnostic behind
a stalled node's blocker is in-memory attempt-scoped state that informs a
message rather than a gate.

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
