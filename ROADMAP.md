# Collomia Roadmap

**Status updated:** 2026-08-02

This document is the current product plan: what remains, why it matters, and
the dependency order. The detailed dated implementation record has moved to
[`docs/ROADMAP_HISTORY.md`](docs/ROADMAP_HISTORY.md). Completed work is
summarized here only when it affects the next decision.

## Product direction

Collomia is a cross-platform, provider-neutral terminal coding agent built
around explicit trust, enforceable permissions, durable recovery, structured
tool use, and a polished terminal experience.

The guiding principle remains:

> Make Collomia safe and recoverable before making it more autonomous.

Priorities:

- **P0 — foundation or safety gate:** required before safe unattended use can
  be advertised.
- **P1 — competitive core:** required for a strong daily-driver release.
- **P2 — differentiation:** ecosystem, scale, and advanced workflows.
- **P3 — expansion:** collaboration and hosted services after the local
  product is dependable.

## Current state

Phases 0–3 are P0-complete. Substantial production slices of phases 4–8 have
also shipped:

- layered validated configuration, project trust, diagnostics, stable events,
  and a generated capability matrix;
- macOS Seatbelt, Linux Landlock, and Windows 11 AppContainer/Job Object
  sandbox backends with compatibility-first `auto` enforcement by default,
  visible degradation, and capability-aware fail-closed `require` behavior;
- scoped permissions, conservative shell analysis, catastrophic-command
  denials, credential-store protection, publication/deployment protection,
  operation-scoped policy rules, macOS per-host brokered command egress, secret
  redaction, and an audit ledger;
- durable resumable sessions, crash recovery, compaction, bounded retained
  artifacts, rewind/fork, coupled conversation-plus-workspace checkpoint
  restore that fails closed on external edits, and fail-stop persistence
  handling;
- atomic patching, tracked diffs, hunk review, undo, Git inspection, planning,
  verification, LSP diagnostics/definitions/references/formatting, repository
  indexing, PTY commands, and background processes;
- normalized provider capabilities, streaming, retries, health, contracts,
  Azure Entra refresh, Bedrock SigV4/bearer authentication, optional macOS/
  Windows keychain credential storage, opt-in provider-safe reasoning controls,
  user-priced cost estimates, and image input;
- MCP lifecycle/resources/prompts/progress/elicitation, safe live catalogs,
  external-data framing, skills, and hooks;
- built-in configuration-free web search and page retrieval, confined to the
  public internet by a connect-time address guard and framed as external data;
- concurrent governed delegated agents with isolated Git worktrees, durable
  status, steering/cancellation, overlap-aware write-scope scheduling,
  freshness-bound three-way integration, and opt-in primary-reviewed
  publication;
- a responsive themed TUI with a growing composer, an optional context rail,
  per-tool-call status, transcript/activity/diff views, optional mouse control
  that can be handed back to the terminal mid-session, a loopback browser
  terminal, shell completion, notifications, and stable headless JSONL;
- deterministic replay/evaluations, cross-platform golden screens, bounded
  fuzzing, support bundles, verified installers, SBOMs, provenance, and gated
  draft releases.

Collomia is suitable for beta use with the documented limits. It should not
claim 1.0 or fully safe unattended execution until the remaining P0 security
and reliability gates are complete. Those now live entirely in Phase 8 —
independent review, sustained adversarial campaigns, and the reliability
campaigns — since the last P0 outside it was reclassified on the evidence that
the enforced all-or-nothing network boundary it was meant to add already
existed on all three platforms. The audit ledger those campaigns and that
review read from is now itself complete, attributable, bounded, and
inspectable, and pseudo-terminal execution reaches all three platforms. An
earlier wave closed the last known asymmetry in the risk classifier: the safety
taxonomy described destruction only, so publishing and deploying rode along
with autopilot while their deletion counterparts required a decision.

The most recent wave took the first sustained beta report at its word. It was
not about a missing capability: it was about having to hand-write JSON, and in
particular about correcting `max_tokens` and `context_window` by hand because
the defaults were wrong for the models in use. Both fields turn out to have
failed silently and in opposite directions, and the product had never held any
knowledge of a model's limits to offer instead. They are now discovered, always
written, reported, validated, and — where the configured value is too large —
corrected from the provider's own rejection.

The wave after it finished the other half of the same report. The limits wave
answered "two numbers were wrong"; this one answered "I have to read extensive
documentation, or hand it to an AI to read, before I can write the file at
all". The documentation was never the problem — the active configuration file
is strict JSON and structurally cannot carry the comments the reference is made
of, so the documentation had nowhere to be at the moment it was needed. It now
reaches the editor as a generated schema.

The latest wave closed the gap between installation and a verified session.
A fresh process no longer asserts that Ollama and qwen are present: interactive
startup enters reusable provider setup when no provider is configured, then
continues directly into the session after the selected endpoint, model, and
tool-calling path have been proved. The same flow remains available later for
adding or changing providers, while headless use receives an actionable
configuration failure instead of a prompt or a dead localhost request.

The wave after it closed the first goal-completion gap. A tool-free model
response is no longer accepted merely because it contains the language of
completion: execution mode compares it with the active structured plan,
terminal-step evidence and reasons, tracked writes since recognized
verification, and unresolved tool failures. It can deterministically continue
the model twice, then ends with a goal-level done, blocked, cancelled, or
budget-exhausted outcome instead of looping without bound. Planning mode and
unrelated informational turns retain ordinary completion.

The reliability wave before it took the P0 by running the failures rather
than reasoning about them, and each one was a defect rather than a
confirmation: terminal loss orphaned every background process, the session was
never flushed to stable storage at all, and the first exhaustion harness passed
against an implementation that destroyed the file it was replacing.

The wave before it closed the last gap between what the agent could do and
what the permission layer could see it doing. Committing was never impossible —
`run_command "git commit"` worked and was approved silently under autopilot —
but it was invisible, because the safety taxonomy classifies destruction and a
commit destroys nothing. `git_commit` declares the files entering the commit, so
`protect_credentials` can act on them; both write tools are classified by the
same code that classifies the equivalent command string.

The completed OG-2 program gave Orchestrated Goal a fixed
runtime-owned whole-graph envelope of 96 provider iterations, now calibrated
to 1,000,000 tokens,
a $5 estimated-cost ceiling when pricing is complete, and 30 minutes of active
post-approval execution. Reached pauses and inert restart time do not consume
that active allowance. Credential-free comparisons retain bounded two-worker
fan-out for substantive independent read investigations: decomposable and
cross-layer scenarios produced the same grounded answer with lower controlled
elapsed time than Standard and primary-only graph runs, while their extra
model work remained visible. Trivial primary work launches no worker and
dependency-serial reads do not overlap. **OG-3A — One verified isolated-writer
candidate wave** adds explicitly
scoped pairwise-disjoint writers on a clean stable Git base, retained child
worktrees, and fresh child verification while stopping before parent
integration. Seven trial-driven follow-ups then calibrated the cumulative
token envelope and corrected the primary execution loop: `max_iterations` is
now a consecutive no-progress lease inside one immutable graph attempt rather
than an accidental lifetime cutoff, unchanged repository state no longer
makes valid verification stale merely because a process or network action ran,
verification-like shell compounds receive an exact reason and direct-command
correction, and graph-hidden tools are enforced before argument decoding.
An exact open completion gap now has its own four-cycle lease that renews only
when evidence can change that gate, redundant exact-workspace `cd ... &&` and
final `2>&1` verification wrappers are recognized without accepting status-
masking shell composition, and unschedulable isolated-writer graphs are
rejected before approval. End-to-end change graphs default to `primary`;
`isolated_write` remains a clean-base, candidate-only terminal preview until
reviewed integration exists.
The latest correction distinguishes repair progress from churn: a novel
machine-observed verification failure and an actual repository mutation renew
the short completion-gap window so the agent can add or fix a focused test,
while repeating the same failure does not. Proposal guidance now requires the
first mutating node to establish a focused test when the repository has no
applicable verification surface yet. The sixth correction makes terminal
graphs yield to a new wave in the same session and creates a zero-provider,
runtime-authored context handoff after every accepted node, preventing one
node's transcript and work from silently consuming the next node's budget.
The seventh correction keeps proposal progress on the model side of the
authority boundary: approval always initializes runtime nodes pending, and
both `/orchestrate cancel` and `/plan off` provide visible recovery from an
unapproved read-only proposal.
Standard evidence-gated execution remains the default.
**OG-3 — Isolated writer candidates is complete** as of 2026-08-03: OG-3B5
made every retained worktree observable and disposable by the user, OG-3B6
completed the adversarial campaign, and OG-3C added the product evaluations
that proved the exit gate. **OG-4 — Reviewed integration** is now the active
milestone through 2026-08-03, when it met its exit gate and closed. Its four
increments shipped: a graph candidate could be
published into the parent workspace through `/agents apply` while the graph
still reported that reviewed integration was required, and that path is now
closed; every publication into the parent now records durably what it replaced
before the first byte moves, so an interrupted one can be inspected and undone;
and a verified candidate can now be published into the workspace on an explicit
request and completed only when the repository's own checks pass against the
combined result — or when you record an explicit written waiver.
**OG-5 — Reproducible recovery and graduation** is now the active milestone,
and its recovery increment has shipped: a restart reproduces a multi-worker
schedule exactly, and no integration, verification, or waiver will proceed
while an earlier publication into the workspace never recorded an outcome —
you resolve that first by putting the prior bytes back or by recording that
you are keeping what was published.
See the
[Orchestrated Goal strategy](docs/ORCHESTRATION_STRATEGY.md) and
[Recommended next sequence](#recommended-next-sequence) for its contract and
exit gate.

## Completed wave — evidence-gated goal completion

**Goal:** accept a final answer only when the runtime's structured evidence
supports done or explicitly supports blocked, without weakening any existing
limit or turning a confused model into an unbounded loop.

- [x] Replace the unconditional “no tool calls means done” branch in primary
  execution mode with a deterministic controller. An active plan with pending or
  in-progress work causes a continuation notice rather than immediate
  completion; a terminal historical plan does not poison unrelated later
  turns, and read-only planning mode can still succeed by producing pending
  implementation steps.
- [x] Make terminal plan state meaningful. New plans require a non-empty goal
  and steps, known acyclic non-duplicated dependencies, dependency-ready active
  or done steps, evidence for `done`, and reasons for `blocked`/`skipped`.
  Restored older plans are assessed at completion even if they predate those
  write-time checks.
- [x] Make successful tracked writes stale the turn's verification evidence.
  A later successful direct conventional build/lint/test command clears the
  gate; shell compounds and success masking such as `go test ./... || true`
  never count. Where no meaningful automated check exists, the plan can record
  a specific `verification_note`, explicitly labelled model-authored rather
  than machine-observed.
- [x] Retain failed tools as unresolved evidence. The agent must use another
  tool to retry or take an alternative, or update the relevant step to blocked
  with an exact reason before a tool-free answer can terminate truthfully.
- [x] Bound controller intervention at two and spend every continuation from
  the existing iteration, token, and cost budgets. Cancellation, permission,
  persistence, and provider failures keep their existing boundaries; the
  ordinary iteration ceiling is now classified as budget exhaustion rather
  than an undifferentiated runtime stop.
- [x] Preserve the schema-v1 `ok`/`error`/`cancelled` process-status contract
  and add an optional `run.result.outcome` carrying `done`, `blocked`,
  `cancelled`, or `budget_exhausted`. Replay validates the pairing and accepts
  older schema-v1 traces that omit the additive field.

**What it is not.** This controller does not parse prose to decide whether a
claim sounds convincing, execute plan nodes automatically, or infer every
side effect of a shell/MCP/external tool. The stale-write gate covers
Collomia's tracked write actions, and verification recognition deliberately
accepts a conservative direct-command vocabulary. Those limits keep the first
slice deterministic and make automatic replanning and dependency-ready node
selection the next agentic work rather than hiding them inside heuristics.
Delegated agents remain governed by their isolated-worktree review and parent
verification flow; this slice gates the primary agent that owns the goal and
shared plan.

## Completed wave — launch to a verified session

**Goal:** make the first successful prompt the continuation of setup, not the
first time Collomia discovers whether its assumed provider exists.

- [x] Remove the synthetic Ollama/qwen provider from `config.Defaults()` and
  the generated global starter. An empty provider map is a valid settings state
  but not a session-ready state; stale provider/model selections remain invalid.
- [x] Have interactive startup recognize only that clean unconfigured state,
  enter provider setup, and continue into the session after a successful write.
  Cancellation writes nothing and does not create a partial runtime.
- [x] Keep headless behavior non-interactive. `collo run`, `review`, and
  `verify` return a configuration-class error that points to `collo setup`
  rather than assuming localhost or attempting to prompt.
- [x] Make provider setup visibly reusable: remove the “first-run” label, list
  already configured providers as actions, preserve the direct
  `--provider <name>` shortcut, and keep the existing verify-before-write and
  default-selection confirmation contracts.
- [x] Carry a credential entered on a platform without an OS credential store
  into only the immediately opened session. It is neither serialized nor left
  in the process environment; later sessions still use the explicitly recorded
  environment-variable contract.
- [x] Make `collo doctor` report the provider-free state and update installation,
  setup, feature, and capability documentation so none describes Ollama or a
  particular model as an installed default.

## Completed wave — the failures nobody had run

**Goal:** take the largest untouched P0 by running the failures the project had
only ever reasoned about — a terminal that goes away, a power cut, a full
disk — instead of adding another test that injects the error it expects.

- [x] Find that terminal loss was unhandled, and that it was not a cosmetic
  gap. `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)` appeared at
  three sites and none of them named SIGHUP, so a closed window or a dropped
  ssh connection hit the Go runtime's default disposition: the process died
  instantly and `defer runtime.Close()` never ran. Because `ProcessManager`
  gives every background process its own group with `Setpgid`, and a hangup
  reaches only the foreground group, **every background process the agent had
  started was orphaned** and kept running against the workspace. The session
  was never closed and the log never flushed. Confirmed by building a program
  with the same wiring, sending it a real SIGHUP, and watching the child
  survive its parent.
- [x] Fix it in one place rather than three. `internal/shutdown` owns the set,
  which is the same three-copies-of-one-vocabulary shape the completion table
  had — and here the drift had already cost the signal that mattered.
- [x] Discover that registering the signal alone would have made things
  **worse**, which is the finding that justified reading the dependency rather
  than trusting it. Bubble Tea installs its own handler for SIGINT and SIGTERM
  and quits on them, which is why those two always reached teardown; it does
  not handle SIGHUP, and it never sees Collomia's context. Registering SIGHUP
  without `tea.WithContext` would have captured the signal, suppressed the
  default disposition that at least ended the process, and left the interface
  running against a terminal that no longer exists. A hang is worse than a
  crash. The contract is now pinned by its own test so an upgrade cannot
  silently remove it, and a shutdown-caused exit is reported as a clean one
  rather than as a program failure.
- [x] Separate what survives a crash from what survives a power cut, because
  they had been one word. Process death loses nothing — the records are already
  in the page cache — and that is what the existing tests covered. Power loss
  discards whatever was not written back, and `Session.append` **never called
  Sync**, so a cut could take the whole session.
- [x] Choose the flush boundary by measuring it. An fsync costs about four
  milliseconds on a local SSD against tens of records in an ordinary turn, so
  per-record flushing buys a guarantee finer than anyone can act on — nobody
  resumes into the middle of a turn — at a cost paid on every tool call. The
  session flushes at turn boundaries and on close, supporting a claim a user can
  act on: a turn that finished is on disk. A failed flush is latched exactly as
  a failed write is, because an fsync error is not retryable — Linux may have
  already discarded the pages it could not write.
- [x] Flush the audit ledger per entry instead, and say why the two differ. A
  session is the user's own conversation; the ledger is the record of what an
  agent was permitted to do, read by someone reconstructing an incident and by
  the review that gates 1.0. A record that quietly loses its last entries during
  the event worth investigating is not that record.
- [x] Sweep recovery across every byte offset rather than at chosen record
  boundaries, since power loss can lose any suffix. That immediately found the
  boundary the design actually has: a final line whose newline never reached
  disk still loads, a partially written record is discarded rather than
  half-decoded, and a session whose metadata never reached disk is refused
  rather than resumed as an anonymous one. The first version of the sweep
  asserted "always loads" and was wrong — refusing is correct there.
- [x] Run the durable writers against a filesystem that is genuinely full.
  `internal/reliability` builds an 8 MiB image with `hdiutil`, which needs no
  privileges on macOS; Linux has no unprivileged equivalent, so the suite skips
  unless an operator supplies a prepared mount rather than pretending to cover
  the platform.
- [x] Discover that the first version of that harness proved nothing, which is
  the whole argument for running real failures. Filling the disk completely and
  then attempting a write **passed against a `safefile.Replace` rewritten to
  truncate the destination in place** — precisely the data loss
  temporary-plus-rename exists to prevent. With nothing left, creating the
  temporary file fails first, so the interesting code never runs. A real disk
  fills while a program is running, and the write that fails is the one that had
  somewhere to start and nowhere to finish. Filling completely and then handing
  a known amount back reproduces that, and the same mutation now destroys the
  file and fails both tests. Sizes come from `statfs` rather than from a
  constant, which had guessed wrong in both directions.
- [x] Raise the cancellation gate from five iterations to twenty and add the new
  reliability tests to it. A race that reproduces one run in ten passes a
  five-run gate most of the time, which is the same as not having one.

**Behavior change:** SIGHUP now begins an orderly shutdown instead of killing
the process, so background processes are stopped and the session is flushed and
closed. Nothing configurable changed.

**What it is not.** Not a claim that a power cut is free: an interrupted turn
may still be lost back to the previous boundary, and that is stated rather than
implied. The exhaustion suite covers the durable writers, not every path that
touches a file. And the reliability P0 is not finished — an independent security
assessment and the sustained adversarial campaigns remain, and those are the
items that actually gate 1.0.

## Completed wave — the commit that says what is in it

**Goal:** stop the agent from having to drop to `run_command` to commit, and in
doing so turn committing from something the permission layer cannot see into
something it can describe, preview, and gate.

- [x] Establish that this adds structure rather than capability, because that
  decides how much freedom the design has. `run_command "git commit"` has always
  worked, and measured against the built binary,
  `collo policy check 'git commit -m test' --autonomy autopilot` answers
  **allow (source: mode)** on a stock configuration. `internal/shell/safety.go`
  is a taxonomy of destruction and a commit destroys nothing, so nothing
  classified it. The deferred list has said since the beginning that autonomous
  Git commits are not the intent; what was missing was any surface on which to
  express that.
- [x] Add `shell.AnalyzeArgv` before adding a tool that would need it. A
  built-in that constructs its own argv has to be classified by the code that
  classifies the same command as text, or it is a second classification site —
  the shape that let the `host` matcher ship inert, made `collo policy check`
  report the wrong decision for a credential-reaching command, and gave
  delegated verification its own command runner. Here the consequence would have
  been worse than a wrong report: a structured Git tool that skipped
  `classifyGit` and `publicationLabel` would be a documented way around the
  confirmations and the publication tier that govern the identical command
  string. A test runs ten commands down both paths and compares.
- [x] Run only the passes that apply. Splitting, quote handling, command
  substitution, redirection, and the raw-string Windows scan all describe what a
  *shell* does with a string; an argv has already been split, and a `>` or a
  `` `rm -rf /` `` among its words is an argument. Running them anyway would
  invent findings, and the finding it would invent is a commit message quoting a
  command — which is exactly what commit messages do. Both directions are
  pinned: the message is data through `AnalyzeArgv`, and the same text through
  `Analyze` still defeats static analysis.
- [x] Ship `git_commit` declaring every file the commit will contain. This is
  the part `run_command` structurally cannot do — `git commit -a` names no path,
  so shell analysis has no argument to classify and a tracked `.env` is
  committed with nothing noticing. A declared path list runs through the
  permission layer's existing derivation from `Action.Paths`, so committing a
  credential file prompts under `protect_credentials` and is refused under
  `deny`, without this tool knowing what a credential is. That derivation was
  written on the promise that a tool added later would be covered by declaring
  what it touches; this is the first tool to collect on it.
- [x] Commit the named files and nothing else, which took two tries. The first
  version let `paths` be omitted and then committed every changed tracked file —
  `git commit -a` — and staged the union with whatever was already in the index,
  on the reasoning that a commit takes the whole index so the prompt had better
  say so. Both halves were wrong in the same way: they let *ambient working-tree
  state* decide the contents of a commit. In a repository where the user has an
  edit in progress, an agent committing "its" change would have carried that
  edit along, silently, under autopilot, with no prompt — nothing about an
  unrelated source file reaches a credential store. Found by being asked the
  plain question of whether that could happen, and answering it with a test
  rather than from memory.
- [x] Make `paths` required and restrict the commit to it with
  `git commit -- <paths>`. That is git's own exact semantic: those paths are
  committed, anything else staged stays staged, and every other working-tree
  change stays uncommitted. A hand-built index survives a commit made around it.
  `git add` still runs first, because `git commit -- new-file` matches nothing
  for a path git does not track yet. A tool whose stated purpose is to say what
  is in a commit cannot have a mode where the answer is "whatever was lying
  around", and the cost — the agent naming the files it just changed, with
  `git_status` there for when it cannot — is the whole price of the guarantee.
- [x] Let `git_branch` create and never switch. Creating a branch at HEAD leaves
  every file on disk untouched; checking out an existing one rewrites the
  working tree from outside Collomia's change tracking, and `/restore` verifies
  the workspace before it will reverse anything — so allowing the switch would
  silently disarm recovery for every turn that came before it. The refusal says
  that rather than reporting a generic error.
- [x] Ask git rather than restating it. A branch name is validated by
  `git check-ref-format`, and the author identity by `git var
  GIT_AUTHOR_IDENT` — not by `git config --get user.email`, which is only one of
  the places the identity comes from and would have refused commits git performs
  perfectly well from `GIT_AUTHOR_NAME` in CI. Found by writing the check the
  obvious way first and watching it fail against this package's own tests.
- [x] Ship no `git_push`. The publication tier already governs it through
  `run_command`, and a dedicated tool would be adding the outward-facing
  capability rather than governing the one that was already there — the
  distinction the roadmap drew when it deferred this work behind the publication
  wave. A test fails if either tool ever reports a publication target.
- [x] Keep both out of planning mode, pinned by a test, because a new `git_*`
  tool is exactly the kind of addition that gets waved into that list beside its
  read-only siblings on the strength of the name.

**Behavior change:** two new built-in tools, visible to the model by default and
absent from planning mode. Nothing existing changes: `run_command "git commit"`
behaves exactly as before, and `options.disabled_tools` removes the new tools.
See the [compatibility note](docs/COMPATIBILITY.md#git-write-tools).

**What it is not.** Not checkpoint commits, and not a fix for `/restore`'s
process-local change tracking — that remains the open item it was. It is also
not a claim that committing is now safe under autopilot: an ordinary source
commit is still approved by the mode, exactly as the command was, and what
changed is that the permission layer can finally see the file list well enough
for `protect_credentials` to act on it. The guarantee is about *which files* are
in a commit, not about whether their contents were the right change to make —
a wrong edit to a correctly-named file is still committed.

## Completed wave — documentation that reaches the file being edited

**Goal:** answer the half of the beta report the previous wave did not — that
configuring Collomia means reading extensive documentation, or handing it to an
AI to read, before a block can be written by hand — by putting the
documentation where the typing happens instead of writing more of it.

- [x] Find the structural reason first. The reference is a 600-line annotated
  JSONC file, and **it is never loaded**: there is no comment stripping
  anywhere in the loader, so `~/.collomia/config.json` is strict JSON and
  cannot hold a single line of the explanation that exists for it. The
  documentation was not missing; it was in a different file from the one being
  edited, which is why the working method became "ask an AI to read it".
- [x] Ship `collo schema config`, a JSON Schema 2020-12 contract generated from
  the structs, beside the existing `collo schema events`. Names and types come
  from reflection and cannot disagree with the loader; enumerated values come
  from the vocabularies below and cannot disagree with the validator;
  defaults are read out of `Defaults()` itself. Descriptions are the one part
  written by hand, and a test fails on a field that has none — an undocumented
  setting is a build failure rather than a documentation debt, since a field
  with a name and a type and nothing else is the state this wave exists to end.
- [x] Extract the enumerated vocabularies before the generator could copy them.
  They were inline `switch` literals, and `permissions.mode` already had two —
  one for the top-level setting and one for a delegated agent's. A schema
  hand-listing them would have been the third copy and the one nobody updates,
  which is the inert-matcher shape this repository keeps finding. One list per
  field now, read by both, with a test that drives the *loader* with every
  published value rather than comparing two lists to each other.
- [x] Give a delegated agent's rules their own definition. They are `[]Rule` in
  Go and may only prompt or deny, so a single shared definition would have had
  an editor offering `allow` in the one place the loader refuses it — an editor
  recommending a broken configuration being strictly worse than no editor
  support. The negative test that catches it is the delegated `allow`; the
  positive one is that a top-level `allow` stays valid.
- [x] Declare `$schema` as a real field rather than tolerating it.
  `LoadOptions.Strict` turns on `DisallowUnknownFields`, so the key that makes
  the file editable would have loaded fine normally and failed
  `collo config validate --strict` — a validator rejecting a file `collo init`
  had just written. It configures nothing and a test pins that.
- [x] Require nothing at the root, which is the finding generating a schema
  produced. **A configuration file is one layer of a merge, not the
  configuration.** `providers`, `default_provider`, and `permissions.mode` are
  required of the *merged* result and of no particular file, so a project
  `.collomia.json` setting two rules is correct — and the obvious derivation,
  "required means no `omitempty`", is wrong twice over, since a boolean
  defaulting to true omits the tag so that `false` still serializes. Deriving
  it that way would have underlined `options.mouse` and every project file in
  existence.
- [x] Write the schema as a sibling and point at it relatively, never a hosted
  URL. It describes the fields *this* build understands; a URL describes
  whichever release published it, which reintroduces at the last step exactly
  the drift generating it removed. `collo doctor` reports a reference that is
  dangling or was written by a different build, because both fail silently —
  an editor with a broken `$schema` offers nothing at all, which looks the same
  as never having had one.
- [x] Replace `/config`, which printed a path and a sentence about precedence.
  Reading the file answers what one layer asked for; it cannot answer which
  layer won, and it cannot answer what is in force for the settings no file
  mentions — which is most of them. It now reports the layers in order, the
  effective value and origin of every containment setting, and any project
  weakening that was refused, with `/config all` for the whole surface.
- [x] Redact by position, not by pattern, and find out why it mattered.
  `resolveProviderEnvironment` copies a resolved credential — from the
  environment or the macOS Keychain — into `Provider.APIKey`, so the merged
  configuration holds secrets that appear in no file the user could think to
  check. The first run against the author's own configuration confirmed it:
  three provider keys, sourced from the keychain, would have been printed into
  the session transcript. A pattern matcher has to recognize a secret to
  protect it; a positional rule protects a credential whose shape nobody has
  seen yet, which is the only kind that generalizes to an endpoint a user
  pointed Collomia at.
- [x] Build the stance display from the struct rather than from the serialized
  configuration, because the first version had the defect it exists to prevent.
  Reading the merged JSON meant `omitempty` removed any field at its zero
  value — so `sandbox_egress` and `command_env` vanished whenever unset, and
  `sandbox_allow_network` and `sandbox_allow_read_outside_workspace` vanished
  **precisely when they were turned off**. A containment display that hides a
  boundary at the moment it is switched on is worse than no display. It reads
  `ContainmentFields` so a new clamped setting appears without anyone
  remembering, and names the behavior of each unset field instead of showing a
  blank.
- [x] Validate the schema against real documents rather than trusting it. The
  generated contract is checked to be well-formed and byte-stable — `collo
  doctor` compares it on disk, so leaked map-iteration order would have made it
  perpetually stale — and it is run against both starters and the exhaustive
  reference, then against six mistakes people actually make. The checker lives
  in the test and understands only the keywords the generator emits, rather
  than adding a JSON Schema module for something no shipped code needs.

**Behavior change:** three, none affecting how an existing configuration
behaves. `$schema` is a recognized key rather than one that passed ordinary
loading and failed `--strict`; `collo init` writes a second file beside the
configuration and prints its path; and `collo setup` adds `$schema` when the
file has none, leaving an existing one alone. See the
[compatibility note](docs/COMPATIBILITY.md#editor-schema-and-the-schema-key).

**What it is not.** Not the interactive configuration surface the Phase 7 item
describes, and not a form over 120 fields. It is the tier of that item that
needed no wizard: the schema is generated, so it cannot drift, and it works in
every editor without Collomia running. The descriptions remain the one
hand-written part, and the exhaustive JSONC reference was deliberately left
hand-written rather than generated from the same table — its worked examples
are worth more than a single source would have been, and the completeness test
already fails on a field missing from either.

## Completed wave — the numbers nobody should have to guess

**Goal:** stop the two configuration fields that decide how much a session can
hold and how much a model may say from being decided invisibly, by a constant,
in a product whose first sustained beta report was about having to set them by
hand.

- [x] Find out what the two fields actually did when omitted, because the
  answer is what justified the wave. An absent `max_tokens` is normalized to
  **8192** at load with no warning, so every answer from a frontier model stops
  at a fraction of what it can emit and presents as a response that simply
  ends. An absent `context_window` stays zero, and `Agent.shouldCompact`
  returns false on a zero window for the life of the session — automatic
  compaction never runs, and a long session ends at a provider context-length
  error with no recovery. `ValidateFields` inspected neither, and no diagnostic
  had ever mentioned either.
- [x] Establish that Collomia held no knowledge of any model's limits anywhere.
  `CapabilitiesFor(providerType, model, contextWindow)` takes the window as an
  *argument* and echoes it back, so there was no registry to consult. Three
  consequences were all shipping: `setup.Build`'s "take the context window from
  the capability registry" branch **could never be reached**, since
  `capabilities.ContextWindow` is non-zero only when a non-zero window was
  passed in, so every locally discovered provider was written with the assumed
  32768 whatever model was chosen; `setup.Build` wrote no `max_tokens` at all;
  and the model picker annotated every catalog entry with `context N` taken
  from one echoed constant — a display that looked like per-model discovery and
  was the same number repeated down the list.
- [x] Read the limits the catalogs already publish and the adapter already
  discarded. `ListModels` parsed nothing but `id`, while OpenRouter-style
  catalogs carry `context_length` and `top_provider.max_completion_tokens`
  beside it. LM Studio's native catalog answers the whole list in one request,
  which is what makes annotating a picker affordable; Ollama's `/api/show`
  answers one model per request, so it is asked once, about the model actually
  chosen.
- [x] Prefer the window a runtime has **loaded** over the one its weights
  allow. A model loaded with 8k is serving 8k, and writing the documented
  number down would disable compaction exactly where it is needed soonest.
- [x] Add a published-limits table for the hosted families whose catalogs
  publish nothing at all, and make its epistemic status structural rather than
  documented. Endpoint-reported limits are authoritative and may contradict a
  configured value; the table is a floor that may only ever fill a gap. Every
  entry deliberately understates, because understating a window costs an early
  compaction while overstating one costs the session, and family floors carry
  models released after this build so a new Claude inherits a modern Claude's
  shape rather than the 32768 guess. A test fails on any entry whose output cap
  meets its window, and another on a duplicate prefix, since a shadowed entry
  is a maintenance trap: the numbers are there, the table appears to know the
  model, and the floor answers instead.
- [x] Make the table safe to ship by giving the provider the last word. A
  `max_tokens` above the model's real ceiling now retries at the ceiling named
  in the provider's own 400 — on the OpenAI route through the existing
  parameter-negotiation profile, on the Anthropic route beside the reasoning
  and caching retries — with a warning carrying both numbers and a pointer at
  the configuration. This is what makes an approximate table and a
  written-from-memory configuration both recoverable rather than fatal.
- [x] Match the rejection on the phrasing that carries the number, never on the
  digits in the message. A rejection routinely names the model, and
  `claude-sonnet-4-5-20250929` contributes 4, 5, and a date to any scan of the
  text — so "the smallest number in the message" would have silently learned a
  ceiling of four output tokens and remembered it for the session. That failure
  is worse than not recognizing the message at all, which is why an
  unrecognized rejection surfaces the provider's error untouched.
- [x] Report it where a configuration written before any of this gets read.
  `collo doctor` carries both numbers on each provider's own row and warns when
  either is missing, naming the consequence rather than the field; `/context`
  says what an unknown window costs instead of printing "unknown"; and
  `collo config validate` refuses a `max_tokens` at or above `context_window`,
  which no request can satisfy. The first run of the doctor check found a live
  defect in the author's own configuration: a Bedrock provider on Claude Opus
  with no `max_tokens`, capped at 8192 output tokens for however long it had
  been configured.
- [x] Add `collo setup --provider <name>`, which re-enters the same
  probe-verify-write path pointed at a provider the file already has. It is not
  a second mode: it skips the scan, opens the picker on the model already in
  use, and everything after that is the ordinary path. A `CredentialKeep` plan
  exists because the alternative would have been `CredentialStore` with no
  secret — which `Apply` would have written as an empty string, destroying the
  credential the run had just authenticated with.
- [x] Record in normalization that `max_tokens` was defaulted. Nothing
  downstream could otherwise tell a deliberate 8192 from a field the user never
  knew existed, and only the second is worth reporting.

**Behavior change:** two. `collo config validate` now rejects a `max_tokens` at
or above `context_window` — a combination no request could satisfy, which
previously validated clean and failed mid-session. And a `max_tokens` above the
model's ceiling is now corrected for that request instead of failing the turn;
the configuration file is never modified. See the
[compatibility note](docs/COMPATIBILITY.md#model-token-limits).

**What it is not.** The table is documentation, not measurement, and it cannot
be verified from inside a build — which is why it is confined to filling gaps,
labelled wherever it is shown, and backed by the provider's own correction. No
catalog anywhere publishes an output ceiling, so that number is the weakest one
in the system whatever this does; the negotiation is what makes it survivable
rather than what makes it right.

## Completed wave — the actions that leave the machine

**Goal:** give the risk classifier a publication shape to match the destruction
shape it already had, and give the rule language enough resolution to say yes to
`npm install` and no to `npm publish` — so the control is one people leave on
rather than one they switch off.

- [x] Classify publication at all. `internal/shell/safety.go` was, end to end, a
  taxonomy of deletion, so under `autopilot` on a stock configuration
  `terraform destroy` required a decision and `terraform apply -auto-approve`
  did not; `kubectl delete` did and `kubectl apply` did not; `git push --force`
  did and `git push origin main` did not. `npm publish`, `cargo publish`,
  `docker push`, `gh pr create`, `gh release create`,
  `aws lambda update-function-code`, and `ssh prod "systemctl restart app"` were
  all approved silently. The asymmetry did not track reversibility — a published
  version is harder to take back than a deployment a controller recreates — and
  this roadmap's own deferred list already said these were not meant to be
  autonomous.
- [x] Fix the rule language first, because a tier with no expressible exception
  is a tier people disable. `rules[].command` was an *executable-name* glob
  while `denied_commands` in the same block was a full-command-line regex, so
  `{"action":"deny","command":"npm publish"}` matched nothing and validated
  clean — the third inert-matcher defect after the `host` matcher and the
  hand-built action in `collo policy check`. A pattern containing a space now
  matches an **operation**, one definition (`policy.NamesOperation`) decides
  which form a pattern is, and a pattern that could match neither fails
  validation.
- [x] Print the vocabulary rather than making people guess it. `collo policy
  check` gained an `operations:` line, because the discoverability failure is
  what made the inert rule dangerous rather than merely useless.
- [x] Model the setting on `protect_credentials`, not on tier 2. Tier 2 offers
  no durable answer at all, which is right for `npm publish` and wrong for
  `git push` on a feature branch; a control that is wrong half the time gets
  switched off. `permissions.publication` (off/prompt/deny, default prompt) is
  uncoverable by autopilot, a tool-wide "always", or an executable-only allow
  rule, while a rule naming the operation and one narrow session grant scoped to
  the exact operation both work.
- [x] Stay quiet during ordinary work. Read verbs (`gh pr view`, `kubectl get`,
  `terraform plan`, `aws s3 ls`), `npm install`, `docker pull`, a
  download-direction `rsync`, and every `--dry-run` rehearsal are unaffected —
  and `--dry-run=false` is an explicit request to act, so it is not.
- [x] Find the classifier's own blind spots by widening the probe. `ssh` passed
  because the publication check sat behind the operation lookup and ssh has no
  subcommand, which silently exempted every verbless tool. And the subcommand
  reader skipped unrecognized options without stopping, so
  `aws lambda update-function-code --function-name f` named its operation
  `aws lambda f` and `gh api -X POST` named it `gh api post` — plausible strings
  that matched nothing, which is why neither failed loudly.
- [x] State the rule/grant asymmetry instead of leaving it inferred. An
  operation-naming rule outranks `deny` because it is written down and survives
  review; an interactive grant never does. The credential gate had behaved this
  way since it shipped without saying so; a test written against the opposite
  assumption is what surfaced it.
- [x] Carry it on the preset ladder, clamp it monotonically, and report it in
  `collo doctor`, `collo policy check`, and the Session tab, with its own header
  in the approval dialog.
- [x] Test the property where it actually lives. Alongside the unit coverage, a
  symmetry test fails when a tool gains a destructive classification without its
  publishing counterpart, and three offline evaluations run a real autopilot
  turn — a unit test proves the string is recognized, not that the mode whose
  purpose is not asking actually stops.

**Behavior change:** two. Publishing, deploying, and pushing prompt by default,
including under `autopilot`, where a headless run fails closed;
`"publication": "off"` restores the earlier behavior exactly. And a `command`
rule pattern containing a space now matches an operation where it previously
matched nothing, so an upgrade may activate a denial its author believed was
already in force. See the
[compatibility note](docs/COMPATIBILITY.md#publication-protection).

**What it is not.** A policy layer, not enforcement — it reads what a command's
text says it will do, so a build script that uploads without naming the
operation is invisible to it, and it cannot tell `kubectl apply` against a local
cluster from the same command against production. The catalogue of publishing
tools is finite; `denied_commands` and `reviewer_command` remain the answer for
anything specific to one organization.

## Completed wave — a pseudo-terminal on the third platform

**Goal:** stop one missing backend from withholding two advertised capabilities
from a first-class platform.

- [x] Add `internal/conpty`, a shared Windows pseudoconsole implementation.
  Both `run_command` with `pty: true` and `collo --web` needed the same
  primitive, and the part that is easy to get wrong is handle lifetime rather
  than the API calls. Two copies would each have had to rediscover that the
  parent's copy of the console's output handle must be closed before anyone
  reads from it — otherwise the pipe still has a writer and a finished command
  never reaches end-of-file, which presents as "the PTY command hangs after it
  finishes".
- [x] Build the process here rather than on `os/exec`.
  `syscall.SysProcAttr` on Windows exposes no proc-thread attribute list, and a
  pseudoconsole is attached only through `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE`
  on a `STARTUPINFOEX`, so `os/exec` structurally cannot do it. Quoting still
  goes through the same `exec.LookPath` and `syscall.EscapeArg` path `os/exec`
  uses: a command quoted one way without a PTY and another way with one would
  be a far worse surprise than either quoting rule on its own.
- [x] Strengthen the cancellation contract instead of matching it. The child is
  created suspended, joined to a job object, and only then resumed, so there is
  no instant in which it could spawn a descendant outside the job — a window
  the ordinary `taskkill /T` path does have. The existing descendant walk is
  reused to wait for the kernel to finish the teardown, because returning
  earlier leaves processes holding the workspace directory open.
- [x] Say what Windows cannot do rather than implying parity. There is no
  SIGTERM: `GenerateConsoleCtrlEvent` requires the sender to share the target's
  console, which a pseudoconsole host by definition does not. The graceful step
  for an interactive session is closing the child's console input, documented
  as a request with a deadline rather than as an equivalent of the Unix path.
- [x] Un-skip the two PTY tests instead of leaving them skipped. They said
  "pty unsupported on windows", which was about to become false; the only
  genuinely platform-specific part was the shell, so they now run everywhere
  with a per-platform probe. Windows has no `test -t 0`, so the terminal
  assertion is that a pseudoconsole renders its client through a virtual
  terminal and a captured pipe does not — which is the difference programs
  actually key on when they decide whether to colorize.
- [x] Cover the mechanics with tests that fail informatively. A hang rather
  than a wrong value is the expected failure for handle-ordering mistakes, so
  the close and terminate tests carry explicit timeouts whose messages name the
  ordering error that would cause them.

**Behavior change:** `pty: true` is no longer refused on Windows and
`collo --web` no longer exits with a platform error there. Windows 10 1809 or
later is required for the pseudoconsole API; older releases report that rather
than running without terminal semantics.

## Completed wave — the record you can actually read

**Goal:** make the audit ledger the thing the README already claimed it was —
a complete, attributable, bounded, inspectable record of every privileged
action — because the independent review that gates 1.0 starts from that record
and it was the weakest durable stream in the project.

- [x] Stop losing entries silently. `Append` discarded every write error, so an
  unwritable ledger or a full disk produced a file that still read as
  complete — the worst possible failure for a record. A failure is now counted,
  reported to the session once (not once per authorized action, which a full
  disk would turn into a flood), and **declared in the file**: the next entry
  that reaches disk is preceded by a `gap` record naming how many entries were
  lost, since when, and why. The gap and the resumed entry are written as one
  buffer, because a gap marker that itself failed to persist would leave
  exactly the hole it exists to prevent.
- [x] Do not fail the turn over it. Refusing work the user already authorized
  because a record could not be filed is the wrong trade — which is why this is
  fail-visible rather than the session store's fail-stop. The distinction is
  now stated in `docs/SECURITY.md` instead of the blanket "best-effort
  diagnostic" that covered both the debug log and this.
- [x] Name who acted. Entries carried no session, agent, or task identity, so
  one workspace file receiving writes from the primary agent, several
  concurrently scheduled delegated agents, and any other `collo` process on the
  same directory interleaved into something no reader could separate. Every
  entry now carries the session id, the actor (`primary` or `agent:<profile>`),
  and the delegated task id. `collo audit --actor agent:reviewer` is the point
  of the field.
- [x] Stop dropping the child's ledger error. Both delegated-agent construction
  sites did `if ledger, err := audit.Open(...); err == nil`, so a child whose
  ledger could not be opened ran **completely unaudited** while the primary
  session's identical failure was reported. One `Agent.attachLedger` now owns
  redaction, the failure route, and identity together, and a source-scraping
  test fails on a third caller of `audit.Open` — the same guard shape that
  pinned single-site command-runner construction after the same defect.
- [x] Ship a reader, because nothing read the ledger back. `collo replay`
  excludes audit JSONL by documentation and support bundles exclude its
  content, so "reconstructable after the fact" meant locating
  `~/.collomia/audit/<name>-<hash>.jsonl` and parsing it by hand. `collo audit`
  filters by session, actor, tool, time window, and refusals, emits JSONL for
  external tooling, and prints a per-actor summary.
- [x] Lead with completeness, not with entries. `collo audit` states any
  declared gap, any unparsable line, and any generation discarded at rotation
  *before* it prints anything, because someone reconstructing an incident has
  to know the record has holes before drawing conclusions from what is in it.
  `collo doctor` gained the same check — it previously verified only that the
  directory could be created, which answers whether a ledger can be opened and
  not whether the one on disk is intact.
- [x] Bound the growth, and admit what the bound discards. The ledger grew
  forever while every other retained artifact in the project is bounded. It now
  rotates at 64 MiB keeping one previous generation, and the fresh file opens
  with a `rotation` entry that says when an older generation had to be removed —
  a record that quietly shortened itself would be the silent-hole defect again,
  in a slower form.
- [x] Latch the state where people look. A failure reported at startup has been
  dismissed by the time it matters, so the session latches its own count and the
  Session tab's Security block reports the record as recording or INCOMPLETE
  alongside the settings that produced it, marked like degraded sandboxing
  rather than as an ordinary row.
- [x] Test the package at all. `internal/audit` had **no test of its own** — one
  happy-path case written from `internal/permission` was the entire coverage,
  and the profile read 0.0%. It is now 83%, covering redaction of every field
  that can carry a secret, concurrent writers through separate handles,
  declared and detected incompleteness, rotation, filters, and the failure and
  recovery reports. The smoke test that followed found two more defects the
  unit tests had not: a zero `since` timestamp emitted on every ordinary entry
  because `omitempty` does not omit a zero `time.Time`, and ledger counts
  printed without pluralization.

**Behavior change:** none to permissions or execution. `collo audit` is new;
ledger entries gained additive `session`, `actor`, and `task` fields and two
new entry kinds (`gap`, `rotation`) that older readers can ignore; a ledger
larger than 64 MiB now rotates rather than growing without bound. See the
[compatibility note](docs/COMPATIBILITY.md).

## Completed wave — the bar that keeps its exit key

**Goal:** Stop the status bar from removing the user's way out in order to keep
showing session decoration on a narrow terminal.

- [x] Lay the bar out widest-first over tiered control hints instead of
  dropping the whole right-hand side when it does not fit. At 80 columns the
  bar previously showed no keyboard hints at all, and at 40 nothing but a
  truncated context gauge — so in a split pane there was no visible way to
  stop a running turn or answer a prompt. The controls now degrade through
  named forms down to a minimum that always survives.
- [x] Keep all three answers on an approval at every width. A prompt narrowed
  to show only how to approve is a prompt biased toward approving.
- [x] Make the left-hand badges droppable segments rather than one
  concatenated string. Two things on this bar are both promised to survive any
  width — the autonomy/containment mark and the exit key — and a string that
  gets truncated can only honor whichever one the ellipsis misses. Explicit
  drop ordering gives up the running spend before the model name.
- [x] Enforce "additive" by comparison rather than by a width guess. The named
  stance badge and the spend readout are laid out with and without, and kept
  only when the control hint lands on the same tier and no other badge was
  given up. The existing invariant test caught this: naming the stance had
  started costing the working indicator at 80 columns.
- [x] Pin the property with a test that sweeps every state across widths from
  40 to 200, rather than trusting golden screens. **The goldens had recorded
  the defect as correct** — `replay_chat_80x24` and `replay_chat_40x12` both
  captured a status bar with no controls on it. A golden proves the screen has
  not changed, not that it was ever right.

**Behavior change:** none configurable. Narrow terminals show fewer badges and
shorter control hints than before, and always show a way out.

## Completed wave — the running turn

**Goal:** Make the part of Collomia a user actually sits through — one turn,
eleven provider calls over the same growing prompt — cheap, fast, and
correctable, instead of expensive, slow, and all-or-nothing.

- [x] Stop reporting prompt-cache hits that were never requested. Both
  Anthropic adapters parsed `cache_read_input_tokens`, cost estimation priced
  it at `cached_input_per_million`, and the README advertised prompt caching as
  a tracked capability — but no request ever carried a `cache_control`
  breakpoint, and Anthropic caches only on explicit opt-in. The whole feature
  was display plumbing over a number that was structurally always zero. OpenAI
  caches implicitly above ~1024 tokens, which is why it went unnoticed.
- [x] Move the structured plan out of the system prompt. `update_plan`
  rewrote the system block, so the front of every request changed during
  exactly the multi-step work caching exists for. The plan now rides a trailing
  message regenerated per request and never retained in the conversation — the
  board stays the single source of truth, and no stale copy can accumulate in
  the history. This was the prerequisite, not a detail: a breakpoint in front
  of volatile content is invalidated by the agent's own progress tracking.
- [x] Send two breakpoints, not four. One on the system block, which also
  covers the tool definitions ahead of it in the prefix and is written once per
  session; one rolling breakpoint on the last non-volatile message, so the next
  call in the loop reads this one's history instead of paying for it again.
  `Message.Volatile` is what keeps the rolling breakpoint behind the trailing
  plan — a prefix that includes regenerated content is never read back, and a
  cache write costs more than ordinary input, so misplacing it is worse than
  not caching.
- [x] Normalize `Usage.InputTokens` to mean the whole prompt. Anthropic reports
  `input_tokens` net of both cache counters, so passing it through would have
  collapsed the context gauge to near-empty exactly when the context was
  fullest and priced most of the prompt at zero. Cache writes are tracked
  separately and priced by `cache_write_per_million`, because they are billed
  above the ordinary input rate rather than below it.
- [x] Take the five-minute cache lifetime deliberately rather than by default.
  The one-hour extension is requested through a beta header, and sending an
  unrecognized beta header to an arbitrary compatible endpoint is a
  compatibility risk taken for a saving nobody has measured yet.
- [x] Never lose a request to an optimization. Any 400 naming `cache_control`
  drops caching for the life of the client and retries once, so an endpoint
  that has not implemented caching costs one wasted round trip rather than one
  per call. Bedrock is deliberately untouched: `cachePoint` support varies by
  model *and* region and fails with a hard `ValidationException` rather than
  being ignored, so it stays honestly declared unsupported until it can be run
  against real Bedrock.
- [x] Say which of the three zeroes it is. A bare "0 cached" cannot distinguish
  a provider with no cache, a prefix not yet written, and reuse that is
  silently failing; `/context` and the Session tab now name the case.
- [x] Let the primary agent be steered. The iteration-boundary hook, the bounded
  drain-once queue, and the no-permission-grant framing all shipped with
  delegated agents; the primary session passed `TakeSteering: nil` and the
  composer refused mid-turn input with "Draft kept while the current turn
  runs." Watching a turn head the wrong way left two options — wait, or `esc`
  and lose it. Enter now steers, and the message says truthfully that it came
  from the user rather than from a parent.
- [x] Be exact about when guidance lands, and about what it does not do. It is
  delivered at the next iteration boundary, never inside an in-flight call, an
  executing tool, or a pending approval; the transcript marks it and says so.
  It grants no permissions — an action that needed approval before the text
  arrived still needs it after. Undelivered guidance is discarded when the turn
  ends and the discard is reported, because a cancelled turn is the common case
  and that text must not resurface against unrelated later work.
- [x] Keep the hint additive. The status bar advertises steering only when it
  does not push `esc cancel` off the bar: an indicator that hides the control
  which stops a turn has made the session worse to advertise one that nudges it.

**Behavior change:** two. Plain enter during a running turn now steers the
agent instead of holding the draft. And `input_tokens` in usage output and the
JSONL event stream now includes cached tokens on the Anthropic routes, where it
previously excluded them; `cache_write_tokens` is a new additive field on the
v1 event contract.

**Measured, not argued.** The fixed prefix is 13.3 KB (~3410 tokens): 11.5 KB
of tool schemas across 23 built-in tools plus a 1.8 KB system prompt. Across
one turn, the share of prompt bytes that are retransmission a warm cache serves
is 33% at one tool call, 71% at five, and 83% at ten. Against a live endpoint
(`azure-foundry-anthropic`, claude-sonnet-5) two identical requests reported
`cache_write=9627` then `cache_read=9627` — 100% of the second prompt served
from cache, with only two tokens falling outside the cached prefix, which
confirms the rolling conversation breakpoint is honored and not just the system
one. That run also confirmed the usage normalization is load-bearing: the
provider reported a raw `input_tokens` of 2 on both calls, so without summing
the three counters the second request would have shown a two-token prompt and
priced 9627 tokens at nothing. A write costs 1.25x and a read 0.1x, so the
one-time write premium is repaid by the first read — every turn that uses a
tool is already ahead.

## Completed wave — checkpoints that move the files too

**Goal:** Make an undo that actually undoes, by ending the split between a
conversation rewind that leaves the files alone and a file undo that knows
nothing about the conversation — without ever letting a recovery feature
destroy work the user did by hand.

- [x] Add `/restore [turn]`, which creates the same non-destructive
  conversation branch `/rewind` does *and* reverses every file mutation
  recorded after that turn. The two halves already existed and were
  independently solid; nothing connected them, so rewinding a turn left the
  files it wrote in place and the transcript describing a tree that no longer
  matched it.
- [x] Teach the change tracker turn boundaries from the runtime's existing
  event funnel rather than from the agent loop. Every surface — TUI, headless,
  browser terminal — reports events through that one site, so there is no
  second place a turn boundary could be missed, and the tracker never infers a
  boundary from elapsed time.
- [x] Verify the whole workspace before writing anything, and refuse the entire
  operation if any file changed outside Collomia — naming every affected file,
  not the first one found. Acting on one file and discovering a second
  afterwards is the same trap as a partial restore.
- [x] Verify before the conversation branches, not after. A drifted file found
  after the branch existed would leave a conversation that moved alone —
  precisely the split this wave exists to close. A test disables the pre-check
  and fails, so the ordering is pinned rather than incidental.
- [x] Collapse repeated mutations of one file into a single write, taking the
  newest recorded content as what disk must hold and the oldest as what to
  restore. Replaying twenty mutations backwards means twenty chances to stop
  halfway; one write per file means none.
- [x] Reverse a file the agent created (it is removed), restore one the agent
  deleted along with its original permission bits, and treat a file the user
  recreated where the agent deleted one as drift rather than as something to
  overwrite.
- [x] State the two real limits instead of papering over them. Change tracking
  is in memory, so a restore to a turn from a resumed session reports that no
  tracked changes needed reversing rather than implying it rewound writes it
  never observed — the tracker's turn numbering is aligned to the session's
  completed turns at every switch so the numbers still mean the same thing to
  both halves. External effects — commands, installs, network calls,
  deployments, MCP effects — are never reversed.
- [x] Say what a checkpoint costs before it is chosen: each picker entry carries
  how many changes across how many files restoring to it would reverse, because
  a turn number conveys none of that.
- [x] Leave `/rewind` exactly as it was, pointing at `/restore` for the coupled
  form. Changing what an existing recovery command does to the workspace is the
  last place to surprise someone.

**Behavior change:** none. `/restore` is new; `/rewind`, `/undo`, and
`collo sessions rewind` are unchanged.

## Completed wave — a Windows install that actually installs

**Goal:** Make the documented Windows path work on a stock machine, including
Windows 11 ARM64, without the user having to understand PowerShell's execution
policy first.

- [x] Stop detecting the CPU through
  `[Runtime.InteropServices.RuntimeInformation]::OSArchitecture`. Windows
  PowerShell 5.1 is a .NET Framework host with no native ARM64 build, and on
  Windows 11 ARM64 that property is missing, which `Set-StrictMode` turned into
  a hard `PropertyNotFoundStrict` failure before anything downloaded. The
  machine-scoped `PROCESSOR_ARCHITECTURE` registry value is read first instead:
  it reports the real hardware even when PowerShell itself is emulated.
  `-Architecture`/`COLLO_ARCH` is the escape hatch when every probe fails.
- [x] Document `irm ... | iex` as the install command. The execution policy
  governs script *files*, so evaluating the script from memory is unaffected by
  `Restricted` or `AllSigned` — no `Set-ExecutionPolicy`, no `Unblock-File`,
  no elevation. The saved-file path is still documented, with the bypass scoped
  to one invocation rather than changed machine-wide.
- [x] Keep the caller's session clean, because `iex` runs the script in it.
  `Set-StrictMode` and `$ErrorActionPreference` moved inside the installer
  function; a test asserts under Windows PowerShell 5.1 that neither leaks.
- [x] Update the user PATH by default, with `-NoPathUpdate` to opt out. Write
  the registry value directly, preserving `REG_EXPAND_SZ`, rather than using
  `[Environment]::SetEnvironmentVariable`, which rewrites PATH as `REG_SZ` and
  permanently breaks entries such as `%USERPROFILE%\bin`. A PATH failure warns
  instead of failing an install whose binary is already in place.
- [x] Silence the progress bar during download. Windows PowerShell renders it
  per buffer, which turns a 25 MB `Invoke-WebRequest` into minutes.
- [x] Stop staging the download under a file name containing "install". Release
  binaries carry no version resource, so Windows fell back to its UAC installer
  detection heuristic, decided `.collo.install.<guid>.exe` was an installer, and
  interposed an elevation consent dialog instead of running it. From PowerShell
  that is invisible — the call operator returns with no output, no error, and no
  exit code — so the version check failed on every standard-user machine.
  Administrators, including CI runners, never see the prompt, which is why this
  shipped. The staged and backup names are now asserted against the heuristic.
- [x] Make the post-download version check report what the binary actually did.
  It read `$LASTEXITCODE` bare, which is a global that a fresh interactive
  session has never set, so any invocation that did not set one failed with
  `VariableIsUndefined` instead of naming the real problem. CI never saw it
  because the fixture build sets `$LASTEXITCODE` first; the tests now clear it
  before installing. The binary's own output is the authoritative signal, the
  exit code is consulted only when one exists, stderr is captured so a chatty
  binary cannot trip `$ErrorActionPreference = 'Stop'`, and a failure quotes
  what the executable printed.

## Completed wave — built-in web search and fetch

**Goal:** Stop the agent from guessing about anything newer than its training
data, without making the capability an integration a user has to find, install,
trust, and pay for — and without giving a model-chosen URL a route into the
user's own network.

- [x] Ship `web_search` and `web_fetch` as built-ins with no API key, no
  account, and no configuration. DuckDuckGo's no-JavaScript endpoints are the
  backend because they are the only major search interface that answers a plain
  query with no key and no quota; there is no Go client worth depending on, and
  what exists wraps the same two endpoints. Adding `golang.org/x/net/html` cost
  no new module — it was already an indirect dependency.
- [x] Try both endpoints in order, and treat a 200 that parses to zero results
  as an engine failure rather than as "no results". Scraping breaks; the
  failure that matters is the silent one that tells a user the web has nothing
  on their question.
- [x] Reduce HTML structurally, not statistically: drop what is never content,
  prefer a `<main>`/`<article>` that actually holds the article, and keep
  headings, lists, code blocks, and tables. A readability score would let a
  page fall on the wrong side of a threshold and lose its own text.
- [x] Enforce a real address boundary rather than describing one. The check
  runs on the resolved IP at connect time through the dialer's `Control` hook,
  so it covers DNS rebinding, every redirect hop, and IPv4-mapped and NAT64
  spellings of a private address alike. Loopback, private, link-local (cloud
  metadata), CGNAT, multicast, benchmark, documentation, and reserved ranges
  are all refused, and no configuration key can turn that off — a switch to
  disable it is exactly what a prompt injection would ask a user to add.
- [x] Ignore inherited proxy variables, strip URL credentials, keep no cookie
  jar, and use a transport that is not shared with the provider client. Each
  of those is a way a model-chosen request could otherwise reach a host the
  guard never inspected or carry state that was never meant for it.
- [x] Report a redirect that leaves the requested site instead of following it.
  `web_fetch` declares the host of the URL it was given, so approving that host
  must not become approval for wherever a redirector points; moves within one
  site are followed normally. `web_search` symmetrically declares *every*
  endpoint it may fail over to, so a rule covering only the primary endpoint
  covers nothing.
- [x] Classify both as external risk, so autopilot never approves them
  silently and `permissions.network: "scoped"` governs them like any other
  network-bearing action — while a `host` rule or a session grant still makes
  ordinary use frictionless.
- [x] Collapse external-data framing into one implementation. MCP framing and
  web framing had to be the same code: a second source shipping a weaker copy
  of the first source's protection is the defect shape this repository keeps
  finding, and web pages are written by whoever the search ranked rather than
  by a server the user chose to trust.
- [x] Speak HTTP/1.1 and never negotiate HTTP/2. Go's HTTP/2 client sends a
  distinctive SETTINGS frame that bot-management products fingerprint: holding
  machine, address, and user agent constant, Stack Overflow returned 403 with
  `cf-mitigated: challenge` on every HTTP/2 request and 200 on every HTTP/1.1
  one, and Medium behaved the same. This — not the user-agent header — is what
  made those sites readable. Found by asking why a 403 persisted after the
  header change that was supposed to fix it.
- [x] Present one fixed browser identity — current desktop Chrome on Windows.
  A great many sites reject non-browser clients by default CDN rule, and a
  page the user can read but the agent cannot is the capability failing at its
  own premise. Deliberately not a rotating pool: rotation only defends against
  a blocklist naming one exact string, which no operator applies to mainstream
  Chrome, while turning any site that did refuse one entry into a failure that
  reproduces a fraction of the time. Desktop rather than mobile because mobile
  identities are served a smaller document. Nothing beyond the header — no TLS
  fingerprint forgery, no challenge solving, no address rotation, no retry of
  a refusal — and `docs/RELEASING.md` carries refreshing the version.
- [x] Name DuckDuckGo's rate limiting instead of echoing it. A throttled
  client gets HTTP 202 and a challenge page rather than a 429, which reported
  verbatim reads like a Collomia bug and sends the user looking for one. Found
  by measuring the user-agent pool against the live endpoints, which tripped
  the limit and produced exactly that unhelpful message.
- [x] Add an opt-in live suite (`COLLO_LIVE_WEB_TESTS=1`) that exercises each
  search endpoint alone, so a working fallback cannot hide a primary that has
  stopped parsing. The ordinary suite stays offline and credential-free.

**Behavior change:** two new built-in tools, visible to the model by default
and available in planning mode. They prompt like any other external action;
`options.disabled_tools` removes them.

## Completed wave — scoped egress on macOS

**Goal:** Make the sandbox's network boundary something a user will leave
switched on, by replacing an all-or-nothing switch with a per-host one — and
be exact about the two platforms where that is not possible.

- [x] Add `permissions.sandbox_egress` (`off`/`scoped`, default `off`). Under
  `scoped` the sandbox denies direct remote egress while keeping loopback
  reachable, and the command is routed through a Collomia-owned loopback
  CONNECT broker that dials only the hosts named by host-scoped `allow` rules.
  The allowlist is built from those rules rather than configured separately,
  so there is no second list to keep in step.
- [x] Take the destination from the proxy request itself and dial exactly that
  host, with no TLS interception — an approved tunnel is spliced byte for byte.
  This also removes any need for SNI inspection: a client cannot name one
  destination and reach another.
- [x] Ship no Linux backend, and say why. Landlock filters TCP by destination
  port and never by address, so reaching a loopback broker means allowing that
  port outright — which also allows every remote host on it, and the adversary
  this control targets chooses its own port. A control defeated by the thing it
  guards against is worse than an honest coarse one, exactly as with the
  credential store's rejected encrypted-file fallback.
- [x] Ship no Windows backend, and say why. AppContainer blocks loopback to
  unpackaged local services regardless of network capability SIDs, so a
  sandboxed command cannot reach the broker at all — no route rather than a
  weaker one. The documented escape needs administrator rights and persistent
  machine state, which the inbox-API-only Windows backend does not take.
- [x] Never inject proxy variables where they cannot work: under `require` the
  unsupported platforms fail closed, under `auto` they degrade visibly to
  `sandbox_allow_network`, and with `sandbox: "off"` no broker starts anywhere.
  A proxy the user believes is a boundary, which any program ignoring
  `HTTP_PROXY` walks past, is worse than no claim at all.
- [x] Drop inherited proxy variables before setting the broker's, so an
  ambient `NO_PROXY` cannot route a command around it and the result does not
  depend on how a library resolves duplicate environment keys.
- [x] Broker background processes on the same terms, with the broker's lifetime
  following the process rather than the tool call — otherwise `start_process`
  would have been a documented way around the control.
- [x] Collapse command-runner construction into one site. Delegated
  verification built its own runner, which is how a containment field ends up
  applied in the primary session and silently absent for delegated agents; a
  source-scraping test now fails on a second construction site.
- [x] Report the stance where people look: the effective posture and allowlist
  size in `collo doctor` and the Session tab, a per-endpoint forecast in
  `collo policy check`, and a refusal that names the host and the rule that
  would permit it. The generic sandbox hint is suppressed after an egress
  refusal, because pointing at `sandbox_allow_network` would send the user to
  the switch this feature exists to replace.
- [x] Keep no preset setting it. Scoped egress is enforceable on macOS only, so
  a cross-platform bundle selecting it would make one preset name mean
  different containment on different machines.

**Behavior change:** none by default — `sandbox_egress` defaults to `off` and
no preset selects it. Adopting `scoped` is a deliberate change; see the
[compatibility note](docs/COMPATIBILITY.md#scoped-egress).

## Completed wave — first run and code intelligence

**Goal:** Remove the two things that most often make a first session go badly —
a credential that has to live in a dotfile, and an agent that reads code by
grepping for names.

- [x] Store a provider API key in the macOS Keychain or Windows Credential
  Manager through `collo auth set|list|status|rm|import`, prompting without
  echo and never placing a secret in an argument or shell history. Nothing
  prints a stored value back.
- [x] Consult the store only after `api_key`, `api_key_env`, and a provider
  family's own variable, so an exported environment variable always wins and no
  existing configuration changes meaning. A machine that has never stored a
  credential makes no credential-manager call at all: a local name index is
  checked first, and its absence ends the lookup — which also means no keychain
  dialog for a user who does not use the feature.
- [x] Ship no Linux backend and no encrypted-file fallback, and say why:
  Secret Service needs a desktop session that headless hosts do not have, and a
  passphrase-protected file would only move the problem. `collo auth` and
  `collo doctor` state the absence rather than degrading quietly.
- [x] Report where each provider's credential came from in `collo auth status`
  and `collo doctor`, and mark an entry the operating system no longer holds as
  missing rather than implying it works.
- [x] Add `find_definition` and `find_references` on the existing
  language-server client, located by file, line, and the symbol's own text —
  the protocol counts columns in UTF-16 code units, and asking a model to count
  them buys confident answers about the wrong token.
- [x] Add `format_file`, applying the language server's own formatting as an
  ordinary tracked, undoable write, and refusing to write if the file changed
  while the server was formatting it.
- [x] Deliberately leave code actions unimplemented for now. Organize-imports
  and quick fixes need `codeAction/resolve` round trips and workspace edits
  that can span files, and a half-working mutation path is worse than an absent
  one. Phase 3 keeps the item open.
- [x] Say which capability a server lacks instead of relaying the protocol.
  Servers differ in what they implement — pyright, the auto-detected Python
  default, ships no formatter — so `format_file` used to fail with the raw
  string `Unhandled method textDocument/formatting`. A method-not-found answer
  is now a configuration answer that names the server, the missing capability,
  and the setting to change; every other protocol error passes through
  untouched.
- [x] Document the two Python facts a first test runs into: pyright navigates
  but cannot format, while `pylsp` does all three and type-checks less well;
  and a project `lsp` map does nothing until `collo trust`, because the
  quarantined layer silently falls back to the auto-detected default.
- [x] Account for the wait. A cold server indexing a large repository consumes
  most of a slow call, and a motionless spinner cannot be told from a hang, so
  the four language-server tools now stream `starting <server>…` and
  `<server> ready in <time> — <what it is doing>…`. Separating startup from
  the request is what distinguishes a slow index from a stall. The lines are
  display-only, never part of what the model reads, and the transcript
  replaces them with the result.
- [x] Make the first screen look composed. The identity line under the logo ran
  past a hundred columns on one line — version, commit, build timestamp,
  provider, model, theme — and because a centred block is centred by its widest
  line, that one line decided the whole header's offset and left the wordmark
  hanging to its left until the first prompt replaced the screen. The line is
  now two short centred ones, the wordmark is a five-row rendering with the
  blossom beside it, and the openers take the orientation card's indent rather
  than their own. The transcript header keeps the compact wordmark, left and
  top, over one line that carries only the version and the answering model;
  build detail moved to the Session tab and `collo version`.
- [x] Make modal dimming a preference. Dropping colour behind a dialog is right
  for using the tool and wrong for photographing it, and product documentation
  is made of screenshots, so `options.dim_background` turns the scrim off while
  defaulting to on. The cleared gutter is deliberately not part of the option:
  reading a modal must not depend on the dimming.
- [x] Wrap the transcript to the transcript. The context rail is composited
  over the body row by row, so a line wider than the body was cut at the rail's
  left edge rather than scrolled — answers, prompts, system and error lines,
  tool output, and panels all measured against the terminal instead. They now
  measure against the body width the tool-call header already used, prose
  word-wrapped and tool output hard-wrapped inside its gutter.

## Completed wave — terminal experience

**Goal:** Make the session's own surface — writing a prompt, seeing what the
agent is doing, and getting text back out — hold up under real use rather than
only in a screenshot.

- [x] Grow the composer with the draft instead of scrolling a one-line field,
  and extend rather than send a draft that is visibly unfinished — one ending
  in a backslash, or sitting inside an unclosed fence. Most users never
  discover a newline chord, so plain Enter has to be the common way to write a
  multi-line prompt. `alt+enter`/`ctrl+j` insert a newline everywhere;
  terminals speaking the Kitty protocol or `modifyOtherKeys` also get
  `shift+enter`/`ctrl+enter`, which arrive as CSI sequences Bubble Tea does not
  recognize and are intercepted ahead of the key switch.
- [x] Hand the draft to `$EDITOR` and take it back (`alt+e`), for the prompt
  that turned out to be three paragraphs.
- [x] Add an optional context rail (`alt+r`): workspace and branch, the plan,
  delegated agents, changed files, and background processes, beside the
  transcript rather than inside it. It appears on its own at 146 columns, is
  unavailable below 116, borrows columns from the transcript and never from the
  composer, and remembers a deliberate choice across a resize.
- [x] Replace the blank opening transcript with an orientation card, degrading
  to the banner and a single honest hint when the terminal is too narrow to
  show pairs that still read as pairs.
- [x] Mark each tool call in the transcript with its outcome and elapsed time,
  and leave both blank for a replayed session — the transcript records what a
  tool did, not how long it took, and inventing a duration is worse than
  omitting one.
- [x] Request mouse reporting by default (`options.mouse`) for wheel scrolling
  and click-to-select tabs, consuming only the wheel and a plain left click so
  drag, motion, and every modifier stay with the terminal — and add `alt+m` to
  release and reclaim the mouse mid-session, because mouse reporting and the
  terminal's own drag-selection are mutually exclusive by protocol and copying
  text should not require a restart.
- [x] Composite modals over a dimmed scrim with color dropped rather than
  blended, and clear a gutter around the dialog, so the frame is blank instead
  of mid-word transcript fragments that read as a corrupted redraw.
- [x] Let Chroma paint an approval diff's added/removed tint itself: emitting
  the background separately does not survive the SGR resets written between
  tokens, so the wash used to stop at the first keyword. Only previews that are
  actually diffs are tinted.
- [x] Give the context gauge eighth-block resolution, so ten cells no longer
  sit at zero for the first five percent of the window and then jump a whole
  cell.

## Completed wave — approval comfort

**Goal:** Make the controls added above livable, so they are read rather than
dismissed.

- [x] Fix an approval offer that did nothing: the dialog advertised a
  tool-wide "always" for a credential-reaching action, the permission layer
  declined to record it, and the next identical action prompted again. Whether
  an "always" is available is now one field the permission layer owns, the two
  stale copies in the TUI are gone, and a test fails on a third.
- [x] Offer one narrow session grant on a credential prompt, scoped to the
  exact target shown — never the tool, the directory, a sibling file, or
  anything past this process, and never offered under `deny`. A control whose
  only answer is "approve again" is a control people switch off.
- [x] Give a credential approval its own identity: its own header and accent,
  the file named first with the kind of secret after it, and a grant button
  short enough not to wrap the row.
- [x] Show the configuration rule that ends a recurring prompt, with the path
  or endpoint filled in — and deliberately not for an uninspectable command,
  where no rule would help.
- [x] Report the permission stance in `collo doctor` (preset, autonomy,
  postures, credential setting, rule count) and warn when a project's
  containment weakening was refused.
- [x] Group the Session tab's Security block into policy, enforcement, and
  session, and mark degraded sandboxing and refused project settings visibly
  rather than as ordinary rows.

## Completed wave — credential files as their own decision

**Goal:** Stop a broad approval from silently including a private key, without
adding configuration a user must understand before starting work.

- [x] Recognize the conventional credential locations — SSH and GPG private
  keys, cloud CLI token caches, registry authentication files, environment
  files — by path, with public keys, `known_hosts`, and example environment
  files excluded explicitly rather than by luck.
- [x] Report the credential stores a command's arguments name from shell
  analysis, keyed on the argument rather than on a table of reading programs,
  and derive the same for any tool that declares its paths.
- [x] Gate reaching one behind `permissions.protect_credentials`
  (`off`/`prompt`/`deny`, default `prompt`), placed so a blanket allow rule, a
  tool-wide session grant, the implicit in-workspace read path, and autopilot
  cannot cover it, while a rule naming the path still can.
- [x] Carry the setting on the preset ladder (frictionless off, standard
  prompt, hardened deny) and clamp it monotonically like every other
  containment field.
- [x] Redact PEM private key blocks and the remaining common provider token
  shapes, and state plainly in the package and in SECURITY.md that redaction
  does not sit between a tool result and the provider.
- [x] Show the setting in the Session tab's Security block and in
  `collo policy check`.
- [x] Build every command-shaped action in one constructor, with a test that
  fails on a second construction site. **This was not cosmetic:**
  `collo policy check` was reporting the wrong decision for a
  credential-reaching command because it assembled its own action and missed
  the field — the same defect shape that let the `host` matcher ship inert.

**Behavior change:** an action reaching a credential store now prompts by
default, including under `autopilot`, where a headless run fails closed. See
the [compatibility note](docs/COMPATIBILITY.md#credential-file-protection).

## Completed wave — host-scoped policy surface and per-capability grants

**Goal:** Make the documented `host` matcher real, and make an approval a
decision about what an action reaches rather than about a tool name — without
claiming enforcement the policy layer does not provide.

- [x] Derive the endpoints a command's text names (URL arguments, ssh-family
  destinations, Git remote URLs) and the endpoint of an HTTP-transport MCP
  server; normalize them to comparable bare hostnames.
- [x] Report an endpoint that resolves elsewhere — a named Git remote, a
  configured registry, a URL read from a file — as explicitly undetermined,
  and never as "no endpoints".
- [x] Populate the previously inert `Rule.Host` matcher from command, PTY,
  background-process, and MCP actions; block host-scoped `allow` rules from
  covering undetermined endpoints, mirroring the uninspectable-command rule.
- [x] Add the `permissions.network: scoped` and `permissions.commands:
  allowlist` postures: prompt-only escalation, defaulting to the earlier
  `open` behavior, monotonic across configuration layers, and not satisfiable
  by a tool-wide session grant.
- [x] Show an action's reach one dimension at a time in the approval dialog
  and add a session grant covering exactly the reach shown; grant nothing for
  an uninspectable command, a one-time confirmation, or an unreadable
  endpoint.
- [x] Treat an interpreter that reads its program from a pipe (`curl … | sh`)
  as uninspectable while still reporting the endpoint it fetches from.
- [x] Update starter/reference configuration, the capability matrix,
  `collo policy check`, and the README/security/user documentation to state
  that this is a policy layer and not egress enforcement.
- [x] Add host-extraction, policy-matching, posture, layering, and grant-UI
  regression coverage plus fuzz invariants that no unreadable endpoint is ever
  reported as a plain host.
- [x] Keep the growing security surface usable: add `permissions.preset`
  (`frictionless`/`standard`/`hardened`) as sugar over the existing fields —
  explicit fields win within a layer, and no preset sets autonomy mode — and
  make the effective stance always visible through a containment mark on the
  autonomy badge plus a consolidated Security block in the Session tab.
- [x] Replace the per-field precedence exceptions with one rule: a repository
  can tighten any containment setting but never weaken one, by explicit field
  or preset alike, with every refusal reported rather than applied silently.
  Document the complete precedence matrix. **Behavior change:** a project
  `"sandbox": "off"` (or any other project-level weakening) is now refused;
  the escape hatch lives in the global configuration only.

## Remaining work by phase

### Phase 1 — Safety boundary

- [x] **P0 — Complete separate capability controls:** executable allowlisting
  and a per-capability grant UI now ship alongside the independent filesystem,
  environment, network, and process controls.
- [x] **P0 — Credential-store protection:** reaching a conventional credential
  location is its own decision (`permissions.protect_credentials`, default
  `prompt`), not coverable by a blanket allow rule, a tool-wide grant, or
  autopilot. Recognition is by conventional path, so it is a usable default
  rather than secret detection; enforcing what a running process may read
  remains sandbox read confinement's job.
- [x] **P1 — Scoped egress on macOS:** `permissions.sandbox_egress: "scoped"`
  denies direct remote egress in the sandbox and routes commands through a
  Collomia-owned loopback CONNECT broker that dials only the hosts named by
  host-scoped `allow` rules, without TLS interception. Foreground commands,
  background processes, and delegated verification are brokered on the same
  terms through one configuration site.

  **This was reclassified from P0 during implementation.** The all-or-nothing
  `sandbox_allow_network` is enforced on all three platforms already, so the
  safety floor never depended on this; what was missing was a version of that
  floor people would leave switched on. It is a usability-of-security feature
  over an existing enforced control, not a hole — and with it landing, no P0
  remains outside Phase 8.
- [x] **P1 — State the per-platform egress limits rather than degrade
  vaguely:** the three backends do not sit on a gradient, so they are
  documented as three distinct claims. macOS Seatbelt denies remote traffic
  while keeping loopback, which is what makes the broker a boundary. Linux
  Landlock filters TCP by port and never by address, so allowing the broker's
  port would allow every remote host on it — an allowlist the adversary it
  targets picks its own port around, which is why no Linux backend is shipped
  rather than a weaker one. Windows AppContainer blocks loopback to unpackaged
  local services, so the design has no route at all there, and its
  all-or-nothing denial is the most complete of the three. Both refuse under
  `require` and degrade visibly under `auto`; with `sandbox: "off"` no broker
  starts anywhere, because a cooperative proxy is not presented as a boundary.
- [x] **P1 — Publication and deployment as their own decision:**
  `permissions.publication` (`off`/`prompt`/`deny`, default `prompt`) governs
  the actions that put something outside this machine — package and container
  registries, source remotes, code-forge writes, infrastructure applies, and
  commands run on another host. It is not coverable by autopilot, a tool-wide
  "always allow", or an allow rule naming only an executable; a rule naming the
  operation is, and one narrow session grant covers exactly the operation shown.

  **This was the last known asymmetry in the risk classifier.** The safety
  taxonomy described destruction only, so every deletion in these tools required
  a fresh decision even under autopilot while none of the publishing
  counterparts did — a gap an independent assessment would have found by running
  `collo policy check` for an afternoon. Like host rules it is a policy layer
  and not egress enforcement, and the catalogue of recognized tools is finite.
- [x] **P1 — Operation-scoped policy rules:** a `rules[].command` pattern
  containing a space matches the executable plus the words that decide what it
  does (`npm publish`, `gh pr create`, `ssh build-host`) rather than `argv[0]`.
  Before this, such a pattern matched nothing and validated clean — the third
  inert-matcher defect in this repository. An unmatchable pattern is now a
  validation error, and `collo policy check` prints the exact operation string a
  command produces so the vocabulary never has to be guessed.
- [ ] **P2 — Windows scoped egress:** only reachable through a
  `CheckNetIsolation` loopback exemption (administrator, persistent machine
  state, documented by Microsoft as a debugging aid) or WFP/firewall filters
  keyed on the AppContainer SID (administrator, address-based rather than
  host-based). Both conflict with the Windows backend's no-administrator,
  inbox-API-only commitment, so this stays deliberately unbuilt rather than
  half-built.
- [ ] **P0 — Independent review:** sustain the adversarial suite and obtain an
  independent security assessment before 1.0.

### Phase 2 — Sessions and context

- [x] **P1 — Coupled checkpoints:** `/restore [turn]` branches the conversation
  and reverses the tracked file mutations recorded after that turn as one
  operation. The workspace is verified before the conversation branches, so
  drift refuses both halves and names every file rather than half-applying.
  Shell, network, and other external side effects are still not reversed, and
  the process-local scope of change tracking is stated rather than implied.
- [ ] **P2 — Nested instructions:** evaluate directory-scoped instruction
  inheritance after precedence and trust UX are designed.

### Phase 3 — Coding loop

- [ ] **P1 — Complete LSP operations:** definitions, references, and
  formatting ship alongside diagnostics and lexical symbol search. Safe code
  actions remain: they need `codeAction/resolve` round trips and workspace
  edits that can span files, which is a mutation path worth doing properly or
  not at all.
- [ ] **P1 — Optional deeper review:** line-level pending-write selection and
  broader selective application for multi-file patches.
- [x] **P1 — Git write tooling under approval:** `git_commit` stages the named
  files and commits, declaring every file the commit will contain — including
  changes already in the index — so the approval prompt previews the real diff
  and `protect_credentials` gates a credential file entering history.
  `run_command "git commit -a"` cannot be gated that way because it names no
  path, which is what made this structure rather than capability: the command
  was already allowed under autopilot. `paths` is required and the commit is
  restricted to it through `git commit -- <paths>`, so an unrelated edit in the
  working tree and anything the user staged by hand are both left where they
  were. `git_branch` creates a branch at HEAD and
  switches without touching the working tree, refusing an existing branch
  because a checkout would change files outside the tracking `/restore` verifies
  against. Both route through `shell.AnalyzeArgv`, so the classification is the
  command runner's own and a rule naming `git commit` covers either spelling.

  Pushing is deliberately not here. It stays with `run_command` under
  `permissions.publication`, because governing the outward-facing capability
  that already existed had to come first, and a dedicated tool would be adding
  one rather than governing it.
- [x] **P1 — Windows ConPTY:** `run_command` with `pty: true` and `collo --web`
  both work on Windows through a shared `internal/conpty` package. The child is
  created suspended and joined to a job object before it runs, so the
  process-tree cancellation contract is strengthened rather than weakened —
  there is no instant in which a descendant could be spawned outside the job.
  Command-line construction goes through the same `exec.LookPath` and
  `syscall.EscapeArg` path `os/exec` uses, so a command is quoted identically
  with and without a PTY. No new dependency: the whole API is already in
  `golang.org/x/sys/windows`.

### Phase 4 — Provider platform

- [x] **P1 — Secure credential lifecycle:** `collo auth` keeps provider API
  keys in the macOS Keychain or Windows Credential Manager, with set/list/
  status/rm/import flows, no project-file secrets, and no value ever printed
  back. Environment variables keep precedence and remain fully supported;
  Linux has no backend by design, so headless hosts use `api_key_env`. MCP
  server credentials are covered separately by Phase 5's OAuth item.
- [ ] **P1 — Provider discovery beyond verified setup:** enumerate Azure
  deployments/projects and Bedrock models, add tested sovereign presets, and
  deepen model-access diagnostics. The provider-configuration and startup
  portions of the former combined item have shipped.

  **`collo setup` has shipped**, covering local runtimes, hosted families, and
  form-configured Azure and Bedrock end to end: concurrent probing, catalog
  discovery, two-request verification, diagnosis, a writer that never puts a
  secret in a file, and `sts:GetCallerIdentity` reporting which AWS identity the
  credential chain actually resolved — the "resolved AWS identity diagnostics"
  half of this item. Re-running is a supported flow: it reads the file it writes
  rather than the merged configuration, shows the current default, marks a
  provider it would replace, and asks before repointing `default_provider`.

  What remains open is **discovery** proper. Azure OpenAI and Bedrock are
  configured by naming their fields, which is honest but not enumeration:
  deployment listing needs the ARM management plane and Bedrock's
  `ListFoundationModels` needs `aws-sdk-go-v2/service/bedrock`, neither of which
  is a current dependency. Foundry already gets model selection free, since its
  OpenAI v1 route publishes a catalog. Tested sovereign presets also remain.

  **Originally reclassified from P2 and merged with the former setup entry.**
  The two were one item seen from opposite sides: the wizard's Azure branch
  *is* deployment discovery, and its Bedrock branch *is* the model-access
  diagnostic, so kept separate the Azure probe gets written twice. The parts
  all exist — capability registry, model discovery, the four-state
  `ProviderAvailability` that already distinguishes *unverified* from
  *unavailable*, credential precedence, `credstore`, starter generation, and
  `config validate --strict` as the final gate — so this is assembly and one
  honest failure path rather than new machinery. It remains P1 because real
  cloud enumeration removes deployment/model guesses the current form-based
  configuration cannot make on the user's behalf.

  **The verified path now dials before it writes.** `config.Defaults()` and
  `collo init --global` name no provider; interactive startup enters the same
  reusable setup flow when the provider map is empty, and headless startup
  fails with an actionable configuration error. The remaining gap here is not
  verification but cloud enumeration: Azure and Bedrock still require the user
  to name deployment/project, region, and model fields before verification can
  prove them.

  - Probe, discover, then verify, in that order. Probe the default ports of the
    local runtimes already supported (Ollama 11434, LM Studio 1234, vLLM 8000),
    because "not installed" and "installed but not running" are different
    sentences. Discover through `Runtime.ListModels`, which already constructs
    the client, checks `CapabilitiesFor(...).ModelDiscovery`, and annotates
    every result with its capabilities — so the model is chosen from what the
    endpoint actually has rather than typed from memory.
  - **Verify with real requests, not with the catalog call.** These are
    different endpoints with different permissions: Azure lists *models* while
    requests address *deployments*, and Bedrock will list a model the account
    cannot invoke. A catalog response proves the host is reachable; only a
    generation proves the thing being written down will answer.
  - **Send two requests, the second carrying a tool definition.** The first
    version sent no tools, reasoning that one request isolates one cause. It
    does, and running it against a real Ollama showed what that costs:
    `gemma3:270m` verified cleanly and then failed the first real prompt with
    `does not support tools` — the exact failure the wizard exists to move
    earlier, and not an edge case, since every embedding, vision, and small chat
    model in a local catalog is in that position. Two requests keep the cause
    isolated *and* keep the promise. Nothing needs to *call* the probe tool;
    acceptance is the discriminator, because a capable model may reasonably
    answer a trivial prompt with text.
  - Write only fields that were verified — provider, type, base URL, and model —
    with `context` from the capability registry instead of the current
    hardcoded guess. **Always write a context window**, falling back to the
    value `collo init` already uses and labelling it as assumed on the
    confirmation screen. Leaving it out looks more honest and is worse:
    `Agent.shouldCompact` returns false on a zero window, so automatic
    compaction never runs and a long session ends at a provider
    context-length error with no recovery.
  - **Never write a secret into the file**, which is the rule `collo auth` was
    built on. Prefer an already-exported variable and record `api_key_env`
    pointing at it, so the wizard handles no secret at all; otherwise offer the
    keychain through `internal/credstore`; on Linux say plainly that there is no
    backend and write `api_key_env` to export. This also puts the macOS Keychain
    backend on a real code path for the first time — see the Phase 8 coverage
    item.
  - Spend the effort on the failure branch, because that is the whole value. A
    refused connection names the runtime and how to start it; a 404 on a model
    prints the catalog that was actually returned; an Azure 404 says that the
    field wants a deployment name. A wizard that only narrates success has
    moved the original failure later, not removed it.
  - Ship it as `collo setup`, its own verb, and have the interface offer it when
    no provider has ever been verified. `collo init` keeps exactly its current
    contract — write a starter file, refuse if one exists — because it is
    documented, guarded by tests, and used from scripts; probe-verify-write is a
    different operation and giving one verb two contracts is how a documented
    behavior quietly becomes two. `collo setup` is re-runnable, since adding a
    second provider is the next thing that happens after the first works.

  **Shipped behavior change:** `config.Defaults()` stops naming a provider it has never
  contacted. An installation with no configuration reports that no provider is
  configured and points at `collo setup`, rather than asserting Ollama on
  localhost and failing at the first prompt. Anyone actually running Ollama on
  the default port is unaffected in practice — `collo setup` finds it and
  writes it down — but a machine that relied on the implicit default without a
  configuration file now needs one.
- [x] **P1 — Model limits that are discovered rather than guessed:** both token
  limits are now resolved from the endpoint that knows them, then from a
  conservative published-limits table, then labelled as assumed — and both are
  always written. `collo doctor` reports them for every provider and warns when
  either is absent, naming the consequence rather than the field; a
  `max_tokens` at or above `context_window` is a validation error; and a
  `max_tokens` above the model's real ceiling is retried at the ceiling the
  provider's own rejection states rather than failing the turn.

  See the [completed wave](#completed-wave--the-numbers-nobody-should-have-to-guess)
  for what it found and what it deliberately does not do.
- [x] **P1 — Provider prompt caching:** the Anthropic Messages routes send two
  cache breakpoints — the stable tools/system prefix and a rolling
  conversation boundary held behind any volatile trailing content — and drop
  caching for the client after an explicit rejection. Usage is normalized so
  `input_tokens` means the whole prompt whatever split a provider reports, and
  cache writes are priced separately from cache reads. Bedrock `cachePoint`
  remains unbuilt: support varies by model and region and fails hard rather
  than degrading, so it stays declared unsupported until it can be verified
  against real Bedrock.
- [ ] **P1 — Modern API features:** general OpenAI/Azure Responses routing,
  structured output, richer thinking/content blocks, and additional media
  types. The one-hour cache TTL is open here: it needs a beta header whose
  current status must be confirmed, and it is worth taking only on measured
  evidence since the longer write is billed higher.
- [ ] **P1 — Explicit routing/fallback:** ordered capability/health/cost/local
  choices that never silently cross privacy or residency boundaries.
- [ ] **P1 — Usage and budgets:** normalized user-priced cost estimates and
  enforceable session/agent monetary budgets ship; an independently
  configurable per-turn dollar cap and richer provider billing caveats remain.

### Phase 5 — MCP and extension ecosystem

- [ ] **P1 — Standards-based MCP OAuth:** login/logout and credential storage
  outside project configuration.
- [ ] **P1 — Resource subscriptions and stable tasks:** implement only against
  stable protocol/SDK contracts; retain safe catalog refresh behavior.
- [ ] **P1 — Complete rich content:** audio and annotation passthrough without
  flattening typed content.
- [ ] **P1 — Argument-level MCP permissions:** bounded normalized resource
  matching that server-authored annotations cannot use to lower risk.
- [ ] **P2 — Extension packaging:** a versioned custom-tool/plugin package and
  SDK after trust and permission contracts are stable.

### Phase 6 — Multi-agent orchestration

The approved product and architecture contract for the remaining Phase 6 work
is [Orchestrated Goal](docs/ORCHESTRATION_STRATEGY.md), Collomia's experimental
**evidence-gated durable execution** mode. It is the canonical cross-session
handoff for the model/runtime authority boundary, evidence contract, recovery
guarantees, milestone order, non-goals, evaluation, and graduation. The
roadmap remains the source of priority and completion status.

- [x] **P0 — Finish agent definitions:** reasoning controls, monetary budgets,
  visibility, and named primary profiles.
- [x] **P0 — Conservative conflict handling:** serialize known overlapping
  assignments and offer explicit three-way reconciliation without silently
  overwriting parent or sibling work.
- [ ] **P1 — Plan graph execution:** deliver evidence-gated durable execution
  by assigning dependency-ready nodes, propagating machine-observed evidence,
  invalidating stale repository assumptions, and re-planning on failure. Keep
  this opt-in until cancellation and review behavior are proven.
  Deliver it through the strategy's ordered milestones rather than as one
  large autonomy jump:
  - [x] **OG-0 — Strategy and continuity:** record the durable charter,
    cross-agent handoff, milestone order, and safety/graduation contract.
  - [x] **OG-1 — Runtime-owned primary graph controller:** add the durable
    node/attempt state machine, dependency-ready primary execution, bounded
    replanning, conservative invalidation, and combined-workspace completion
    gates without adding automatic actors. *(Completed 2026-08-01 through an
    internal programmatic evaluation path; no user-facing mode.)*
  - [x] **OG-2 — Experimental Orchestrated Goal:** require explicit
    per-session opt-in and graph approval; add bounded automatic read-only
    fan-out while keeping one serial primary write lane.
    - [x] **OG-2A — Explicit primary-only preview:** add a TUI-only fresh
      proposal, concrete acceptance criteria, one-time approval,
      status/cancel/explicit-resume controls, visible experimental graph state,
      and inert persisted state without automatic actors. *(Completed
      2026-08-01.)*
    - [x] **OG-2B — Bounded read-only fan-out:** automatically schedule at
      most two useful dependency-ready read-only delegates while retaining one
      serial primary write lane and the OG-2A consent/authority boundary.
      - [x] **OG-2B1 — Runtime-selected read fan-out kernel:** add explicit
        approved `read_only` nodes, stable dependency-ready claims, at most two
        automatic read workers, bounded evidence/usage ingestion, and no
        unnecessary child for primary-only or serial graphs. *(Completed
        2026-08-02.)*
      - [x] **OG-2B2 — Controls and comparative evidence:** complete operator
        control, aggregate presentation, and proof of a useful quality or
        elapsed-time gain over Standard and primary-only execution before
        completing OG-2B.
        - [x] **OG-2B2a — Cooperative pause and safe retry:** durably pause at
          a scheduling boundary, explicitly resume without discarding an
          in-process attempt, and retry only an eligible blocked node while
          rejecting ambiguous mutation replay. Keep whole-graph cancellation
          immediate and defer node cancellation until optional branches exist.
          *(Completed 2026-08-02.)*
        - [x] **OG-2B2b — Aggregate presentation and comparative evidence:**
          make primary-plus-worker work visible and bounded, compare suitable
          and unsuitable workloads, and finish the event/automation decision
          before any headless activation surface.
          - [x] **OG-2B2b1 — Durable aggregate accounting and presentation:**
            count the explicit proposal, primary work, automatic reads, and
            failed provider iterations exactly once; persist per-lane
            input/output tokens and honest price availability; and show total
            elapsed work in graph status. *(Completed 2026-08-02.)*
          - [x] **OG-2B2b2 — Aggregate bounds and comparative evidence:**
            enforce a whole-graph token/cost/iteration/active-wall envelope,
            run the comparative scenario matrix, and retain fan-out only if
            quality or elapsed-time evidence justifies its added work.
            *(Completed 2026-08-02.)*
  - [x] **OG-3 — Isolated writer candidates:** dispatch only ready,
    disjoint-scope writers on a stable base; require child verification and
    stop at reviewable candidates. *(Exit gate met 2026-08-03.)*
    - [x] **OG-3A — One verified isolated-writer candidate wave:** add
      explicit narrow `isolated_write` scopes, claim at most two pairwise-
      disjoint writers from one clean stable commit, reuse ordinary delegate
      permission/hooks and isolated worktrees, require fresh detected child
      verification, retain candidates, and stop before selection or parent
      integration. Interrupted writers are blocked and never replayed.
      *(Completed 2026-08-02.)*
    - [x] **OG-3A.1 — Aggregate-budget usability correction:** recalibrate
      new graphs from the initial 192,000-token experimental ceiling to a
      fixed 1,000,000-token ceiling, preserve the stored ceiling on older
      graphs, compact once at approval and proactively under cumulative-budget
      pressure, and show per-request context beside whole-graph consumption.
      *(Completed 2026-08-02.)*
    - [x] **OG-3A.2 — Primary-loop budget and evidence diagnostics:** renew
      the configured primary iteration slice only when the runtime starts a
      new immutable node attempt, retain the fixed 96-iteration graph
      envelope as the outer bound, explain rejected verification-like shell
      commands with a safe direct form, and enforce graph-hidden tools before
      their arguments can reach an implementation. *(Completed 2026-08-02.)*
    - [x] **OG-3A.3 — Progress-aware primary control and workspace evidence:**
      reinterpret the configured primary slice as a durable consecutive
      no-progress lease that renews on novel machine evidence; retain 96
      aggregate iterations as the hard graph bound; distinguish conservative
      write-ahead mutation history from observed repository-state changes;
      return positive verification receipts; recognize direct Python module
      verification; and prefer coherent 4–6-node proposals rather than
      spending graph overhead on file-by-file serial nodes. *(Completed
      2026-08-02.)*
    - [x] **OG-3A.4 — Completion-gap and schedulability correction:** bind
      post-proposal remediation to the exact unmet gate rather than novel-
      looking output; recognize only safe redundant workspace/status wrappers
      around verification; default end-to-end graphs to the primary lane;
      reject mixed or dependency-producing retained-candidate topologies; and
      preflight candidate-only graphs against a clean stable Git base with
      actionable dirty-path diagnostics. *(Completed 2026-08-02.)*
    - [x] **OG-3A.5 — Repair-progress and verifier-bootstrap correction:**
      preserve a bounded remediation window after a novel verification
      failure or a real workspace repair, refuse renewal for identical failed
      output, retain full failed-verifier evidence, and require proposals to
      establish a focused smoke test before a mutating node relies on a test
      runner with no collected tests; allow an explicit safe retry to reattach
      its saved blocked graph without a separate resume step. *(Completed
      2026-08-02.)*
    - [x] **OG-3A.6 — Multi-wave lifecycle and node-boundary efficiency:**
      let a new `/orchestrate <goal>` archive a terminal attached or saved
      graph without leaving the session, make cancel on an already-terminal
      graph explicitly release it, retain prior graph snapshots and evidence
      in the append-only log, scale proposals to 1–3 nodes for scoped changes,
      stop models at the current node's verification boundary, replace the
      accepted node's active transcript with a zero-provider runtime handoff,
      and exclude generated/dependency/cache trees from ordinary file
      discovery. *(Completed 2026-08-03.)*
    - [x] **OG-3A.7 — Proposal-state authority and escape paths:** treat
      proposal-time status/evidence as model-authored annotations rather than
      runtime completion, normalize every approved node to pending with no
      imported evidence, discourage graph nodes that only repeat inspection
      already performed during proposal design, retain direct
      `/orchestrate cancel`, and make `/plan off` explicitly cancel an
      unapproved proposal and restore execution mode. *(Completed 2026-08-03.)*
    - [x] **OG-3A.8 — Review-readiness corrections:** recognize the
      conventional verifiers of the ecosystems the mode actually meets
      (environment-manager wrappers, R, Ruby, Elixir, PHP, Swift, CMake, Deno,
      Haskell, Bazel, task runners, tox/nox, Java) and the runner a Python
      project really uses; stop accepting `git diff --check` as proof of a
      change; make completion gaps and read-node groundedness typed runtime
      state rather than matched prose; report a verified candidate wave as
      `awaiting_review` instead of a blocker; retain candidate worktree
      identity when a wave crosses the aggregate budget; withhold
      `git_commit`/`git_branch` from automatic writers structurally; bound
      retained per-attempt evidence so durable snapshots stop growing
      quadratically; keep mid-graph steering across the node handoff; and give
      `internal/writescope` direct tests with opposite-direction
      overlap/violation rules. *(Completed 2026-08-03.)*
    - [ ] **OG-3B — Adversarial and recovery closure:** finish cancellation,
      provider-failure, scope/drift, retained-worktree, restore, and operator-
      inspection campaigns; resolve any candidate-state/recovery gaps before
      declaring OG-3 complete.
      - [x] **OG-3B1 — Retained-worktree accountability closure:** make every
        directory the runtime causes Git to create attributable to a plan node
        and attempt however the wave ends — record what a cancelled wave left
        on disk, record identity before usage accounting rather than after,
        and bind an isolated worktree to its attempt durably at creation so
        recovery after a process boundary names the exact orphaned path and
        branch instead of describing something to go find; list every retained
        tree in `/orchestrate status` for live and saved graphs, marking an
        unexamined one as unreconciled; and add adversarial application-level
        coverage for cancellation, delegate-permission refusal, child
        verification failure, out-of-scope writes, and a provider failure that
        must not discard a verified sibling. *(Completed 2026-08-03.)*
      - [x] **OG-3B2 — Verification-composition correction:** accept a check
        whose exit status the shell provably reports — an `&&` chain ending in
        a recognized verifier, so preparation such as a sandbox-required cache
        redirect or a virtualenv activation no longer makes verification
        impossible — while continuing to refuse `||`, `;`, pipelines,
        backgrounding, redirection, a verifier that is not last, a final
        command assembled by substitution, and a leading segment that
        relocates the verifier out of the workspace the evidence is bound to;
        name the direct command wherever the verifier sits in a refused
        composition; and make a node that stalls after refused checks report
        them in its blocker. *(Completed 2026-08-03.)*
      - [x] **OG-3B3 — Budget-accounting correction:** charge the aggregate
        token envelope for new work rather than for prompt tokens the provider
        served from cache, so a long node's re-sent context no longer exhausts
        the ceiling while cost, iterations, and wall clock all have headroom;
        stop compaction from spiralling by requiring real context growth since
        the last one before paying for another; and make exhaustion cost a
        decision instead of the graph by letting a person grant up to two more
        bounded envelopes with `/orchestrate extend`, persisted and validated
        so nothing but an explicit user action can widen the ceiling.
        *(Completed 2026-08-03.)*
      - [x] **OG-3B4 — User-owned execution envelope:** make the four
        whole-graph bounds configuration with the current values as defaults
        (`options.orchestration_max_iterations`, `_max_tokens`,
        `_max_cost_usd`, `_max_active_wall_seconds`), refusing only implausible
        values; stop capping how many times a person may grant another
        envelope, and size each grant from the graph's own configured envelope;
        and record in the strategy that a resource bound is a speed bump
        requiring human interaction rather than a wall that discards completed
        work — repository text, skills, hooks, and the model still cannot widen
        it, and the permission, verification, scope, and publication gates are
        unaffected. *(Completed 2026-08-03.)*
      - [x] **OG-3B5 — Retained-worktree reconciliation:** observe each
        retained worktree and record a typed disposition durably
        (`present`, `empty`, `missing`, `orphaned`, `base_unreachable`,
        `discarded`) through `/orchestrate reconcile`; add `/orchestrate
        discard <node-id> [confirm]`, reachable only by the user, refusing an
        unreconciled tree, requiring confirmation for one holding changes, and
        refusing outright a directory Git no longer registers; and refuse to
        archive a graph that is still the only record of an unobserved tree.
        *(Completed 2026-08-03.)*
      - [x] **OG-3B6 — Adversarial campaign:** prove the dispatch and
        acceptance gates fail closed through application-level paths — hook
        refusal, post-claim parent drift, verification spanning a changing
        tree, case-folded sibling scopes, every writer in a wave failing, and
        a writer beyond the wave's starts bound, which stops the graph as
        `budget_exhausted` rather than as a failure. *(Completed 2026-08-03.)*
      - [x] **OG-3C — Writer-wave product evaluation and OG-3 sign-off:** add
        the isolated-writer wave's missing evaluation-matrix cases on a full
        runtime with real worktrees and the application's own child
        verification, each asserting the parent repository is byte-for-byte
        unchanged, and record the exit-gate evidence that closes OG-3.
        *(Completed 2026-08-03.)*
  - [x] **OG-4 — Reviewed integration:** add recoverable publication,
    conservative candidate synthesis, and fresh combined-parent verification
    before a logical node can finish. *(Exit gate met 2026-08-03; candidate
    ranking deferred into OG-5.)*
    - [x] **OG-4A — No unaccounted publication of a graph candidate:** mark a
      candidate graph-owned from dispatch through the durable delegate record
      and a resumed session, and refuse publication at the single funnel every
      apply path shares — operator, primary-agent reviewed, and model tool —
      while keeping review available. Closes a path that changed the parent
      workspace while the node still reported that reviewed integration was
      required. *(Completed 2026-08-03.)*
    - [x] **OG-4B — Recoverable pre-integration checkpoint:** append and flush
      a durable record of every target path's prior content, mode, and
      existence before publication changes the first byte, mark the outcome
      applied or reverted afterwards, report an unresolved checkpoint at
      startup and through `/restore integration`, and restore the recorded
      prior state only on explicit request — never re-publishing, because
      completing a half-finished integration would repeat a mutation whose
      effect is unknown. Retained content is bounded, and a path past the
      bound is named as unrestorable rather than dropped.
      *(Completed 2026-08-03.)*
    - [x] **OG-4C — Graph-owned candidate integration:** publish one node's
      whole verified candidate into the parent workspace through an explicit
      user-only `/orchestrate integrate <node-id>`, under the OG-4B checkpoint
      and ordinary integration permission. A candidate goes whole or not at
      all, since the child verified its entire tree; a conflict in any file
      refuses the whole integration. The node moves to a new `integrated`
      state and the graph to `awaiting_verification` — never `done`, because a
      child's pass says nothing about the parent it was merged into — and every
      previously accepted node is staled. *(Completed 2026-08-03.)*
    - [x] **OG-4D — Combined-workspace verification and node acceptance:** run
      the repository's detected checks against the merged parent through
      ordinary `run_command` permission with `/orchestrate verify`, require
      every one to pass against a workspace that did not move while they ran,
      and only then complete the integrated nodes. A failure leaves the node
      integrated and unfinished. `/orchestrate waive <reason>` accepts a node
      on a person's written judgement where no automated check applies, and is
      labelled a user-authored waiver rather than verification everywhere it
      appears. *(Completed 2026-08-03.)*
    - [x] **OG-4 closure:** record the four exit-gate clauses against the tests
      and commands that proved each, close the last untested clause with an
      integration-denial evaluation, and write down the two deliverable bullets
      not delivered literally — hunk-level application superseded by
      whole-candidate publication, and candidate ranking deferred into OG-5's
      graduation review. *(Completed 2026-08-03.)*
  - [ ] **OG-5 — Reproducible recovery and graduation:** restore scheduler
    state without replaying mutations, finish adversarial/performance
    evaluations, and make an evidence-based graduation decision.
    - [x] **OG-5A — restart fidelity and unresolved parent publications:**
      pin multi-worker scheduler restoration as exact — same nodes, same
      order, no attempt resumed in place, and the spent envelope carried
      across so a restart is charged for the starts it re-spends — and stop
      every step that reasons about the combined parent workspace while an
      earlier publication into it never recorded an outcome, with
      `/restore integration <id> keep` added as the resolution that keeps
      what was published instead of undoing it. *(Completed 2026-08-03.)*
    - [x] **OG-5B — permission-decision equivalence at the parent-workspace
      boundary:** close the graduation gate's permission clause, and with it a
      real bypass the test found — a path `deny` rule that correctly stopped
      `write_file` did not match at integration on any workspace reached
      through a symlink, so publishing a delegate's candidate got around it.
      Both paths now resolve targets through the same guard, and one
      evaluation runs the identical rule through Standard and Orchestrated
      mode. *(Completed 2026-08-03.)*
    - [x] **OG-5C — the adversarial publication corpus:** record the
      publication half of the graduation gate's no-silent-overwrite,
      no-duplicated-mutation clause. Three cases were already covered; five
      were not — a symlink standing in for the target file, a symlinked
      directory component, both sides creating the same path, the parent
      deleting a file the candidate modified, and publishing the same
      candidate twice. Each now asserts its refusal reason, and the escape
      cases assert a canary outside the workspace is untouched.
      *(Completed 2026-08-03.)*
- [ ] **P1 — Result synthesis:** build on the shipped freshness-bound child
  verification and comparison surface with explicit combined-parent
  verification and safe ranking criteria.
- [ ] **P1 — Reproducible recovery:** restore scheduler order and offer safe
  restart of pending read-only work without replaying completed mutations.
- [ ] **P2 — Optional team templates:** reviewer, researcher, test, security,
  and documentation profiles without hard-coding them into the core loop.

### Phase 7 — Terminal and automation experience

- [ ] **P1 — Finish artifact input:** raw clipboard image protocols and
  optional inline pixel previews where the terminal supports them.
- [x] **P1 — Correctable turns:** typing during a running turn steers the
  primary agent through the same iteration-boundary hook delegated children
  use, with a bounded drain-once queue, an explicit no-permission-grant
  framing, a transcript marker stating where the guidance will land, and a
  reported discard when a turn ends before delivery.
- [ ] **P1 — Workspace UI refinements:** the context rail now carries
  workspace, plan, agents, changed files, and background processes beside the
  transcript; automatically surfaced diagnostics and provider price/budget
  visibility remain.
- [ ] **P1 — Accessibility validation:** colour (`NO_COLOR` selects the plain
  theme), motion (`options.reduced_motion`), and narrow-width resize behavior
  are done, the last pinned by a width sweep rather than by golden screens.
  Broader terminal-emulator coverage remains — the Kitty/`modifyOtherKeys` key
  handling is the most emulator-sensitive code and is unverified across
  emulators.

  **Screen-reader support is deliberately not built.** Full-screen TUIs repaint
  with absolute cursor positioning and screen readers follow the terminal
  buffer, so the two are structurally opposed; of comparable tools only Claude
  Code ships a mode for it, added recently after sustained user pressure, and
  Codex/Crush/OpenCode have none. Collomia's non-interactive JSONL output is
  already a linear-text path and is documented as the answer, in the same
  idiom as Linux scoped egress. That keeps a future `--screen-reader` flag
  cheap — wiring and documentation rather than architecture — if a user asks.
- [ ] **P2 — A configuration surface that is not a hand-edited JSON file:**
  the reported beta experience of configuring Collomia is reading extensive
  documentation — or asking an AI to read it — and then hand-writing JSON,
  including numbers like `max_tokens` and `context_window` whose defaults were
  wrong for the model in use.

  **Two of the three tiers below have now shipped.** The limits wave took the
  discovery tier, and the schema wave took the third: `collo schema config`
  generates the contract from the structs, `collo init`/`collo setup` write it
  beside the file with a `$schema` key, and any editor supplies completion,
  hover documentation, and inline validation for the file being typed into.
  `/config` no longer prints a path — it reports what each layer resolved to.
  What remains open is the middle tier alone: changing a safety posture from
  inside a session rather than reading it.

  **Deliberately not one wizard over the whole file.** There are ~120
  configuration fields, and a form asking 120 questions is worse than the file
  it replaces. Three tiers, of which two mostly exist:

  - *Needs discovery or verification* — providers, models, limits,
    credentials. This is where the reported pain is, it is the only tier a
    wizard is the right instrument for, and `collo setup` plus Phase 4's
    per-provider re-run covers it.
  - *Safety postures* — already sugar-coated by `permissions.preset` and
    reachable through `/autonomy`. What is missing is seeing and changing the
    effective stance from inside a session rather than reading it in the
    Session tab. Any such editor must respect the monotonic clamp, which means
    it can offer a project a tightening and must refuse it a weakening in the
    same words the loader already uses.
  - *Everything else* — **shipped.** It stays JSON and got a generated schema
    instead of a form: `collo schema config`, beside the existing
    `collo schema events`, with `$schema` written into what `collo init` and
    `collo setup` produce. Generated from the structs, so it cannot drift from
    them the way `docs/FEATURES.md` drifted into claiming a mandatory-audit
    posture that never existed — and enumerated values are read from the
    validator's own vocabularies, which had to be extracted from duplicated
    inline `switch` literals before the schema could safely consult them.

  The schema was the cheapest part and answered most of the complaint, exactly
  as predicted, and it needed no interactive surface. What is left is the
  posture editor, which is worth taking only if reading the stance turns out
  not to be enough.
- [ ] **P2 — Structured local service API:** authenticated stdio/socket or
  WebSocket access to the event/session/permission contracts. The current web
  terminal is a PTY transport, not this API.
- [ ] **P2 — Governed web/browser tools:** explicit enablement, domain policy,
  citations, download quarantine, and prompt-injection defenses.
- [ ] **P2/P3 — Remote and collaboration surfaces:** only after identity,
  residency, audit, and local durability boundaries are complete.

### Phase 8 — Quality and 1.0 readiness

- [ ] **P0 — Security program:** sustained fuzz/adversarial campaigns and an
  independent review.
- [x] **P0 — Reliability campaigns:** all four are now run rather than
  reasoned about. Native terminal loss was a live defect — SIGHUP was unhandled
  at three signal sites, so a closed window killed the process before teardown
  and orphaned every background process, each of which sits in its own process
  group and never sees a hangup. Power-loss durability had no flush at all: the
  session now flushes at turn boundaries and on close, the audit ledger flushes
  per entry, and recovery is swept across every byte offset of a real log.
  Filesystem exhaustion runs the durable writers against a genuinely full
  filesystem through `internal/reliability`. Cancellation stress went from five
  iterations to twenty.

  **What remains under this heading is confidence built by others, not code.**
  The suite proves the failures it reproduces; an independent assessment and
  the sustained adversarial campaigns above are what actually close the 1.0
  gate, and neither is something this project can perform on itself. See the
  [completed wave](#completed-wave--the-failures-nobody-had-run) for what
  running the failures found that reasoning about them had not.
- [x] **P0 — A trustworthy audit ledger:** every entry names the session and
  actor that wrote it; a write failure is counted, reported once, and declared
  in the file as a gap rather than leaving a hole that reads as complete; both
  delegated-agent paths route through one attachment site that no longer drops
  `audit.Open`'s error; `collo audit` reads the record back and leads with its
  integrity; growth is bounded by rotation that admits what it discards; and
  the package went from no test of its own to 83% coverage. What remains here
  is a boundary rather than a gap: this records what Collomia's permission
  layer decided, not what an approved process then did on its own, which is
  sandbox read/write confinement's job and is documented as such.
- [ ] **P2 — Decide whether a mandatory-audit posture should exist:**
  `docs/FEATURES.md` claimed audit writes could be made mandatory by policy,
  and listed "audit requirements" among the monotonically clamped containment
  fields. Neither has ever existed: there is no such key in
  `appconfig.Permissions` and `ContainmentFields()` does not include one. The
  documentation has been corrected rather than the gap papered over, and the
  claim is now blocked by `TestDocumentedPermissionSettingsAllExist`.

  The question the false claim raises is still a fair one. The audit wave chose
  fail-visible deliberately — refusing work the user already authorized because
  a record could not be filed is the wrong default — but a regulated deployment
  may genuinely want "no action proceeds unless it can be recorded" as an
  opt-in posture, and the wave left the mechanism in place for it
  (`Ledger.Degraded`, `OnFailure`, and the runtime's latched health). It would
  be a new clamped containment field with a fail-closed headless path. It stays
  unbuilt until someone wants it, because a posture nobody has asked for is how
  the original claim came to be written in the first place.
- [x] **P1 — Cover the credential store's actual backend:** `internal/credstore`
  read 40.8% with `backendGet`, `backendSet`, `backendDelete`, `Delete`, and
  `Verify` in `store_darwin.go` all at **0.0%** — the entire macOS Keychain
  path, and `collo auth rm` along with it, untested on a platform that can run
  it, in a package that holds provider API keys. It is now 51.4%, with the
  Keychain backend exercised against a real temporary keychain behind
  `COLLO_KEYCHAIN_TESTS=1`; the suite is opt-in because unlike every other test
  in the package it touches the user's own operating-system credential store,
  and skips cleanly when `security(1)` is unavailable.

  The remaining uncovered statements in `store_darwin.go` are the branches that
  need a keychain that fails in a specific way — a locked store, a denied
  authorization — which cannot be provoked from inside a test without leaving
  state behind on the developer's machine. That is a stated limit rather than
  an outstanding task.
- [ ] **P1 — Performance budgets:** idle memory, token overhead, compaction
  quality, monorepo fixtures, and same-hardware regression thresholds. The
  prompt-cache wave established the measurement discipline and the live-endpoint
  harness this needs, so it is cheapest to take while that work is still warm.
- [ ] **P1 — Optional telemetry decision:** only opt-in, minimal, documented,
  locally inspectable/deletable, and fully disabled by offline mode.
- [ ] **P1 — Native release signing:** Apple signing/notarization, Windows
  Authenticode, and installer-enforced signature verification.
- [ ] **P1 — Package managers:** Homebrew and Scoop first, because neither
  needs a signed binary — a Homebrew *formula* over the release tarball and a
  Scoop manifest over the zip are both checksum-verified text, and the release
  workflow already produces the artifacts, checksums, SBOMs, and attestations
  they would reference. Publish both from `release.yml` so a manifest cannot
  drift from `VERSION`. A Homebrew **cask** and a Winget manifest are blocked
  on native release signing above and should not be started before it;
  discovering that halfway through is how a distribution slice stalls with
  nothing shipped. Selected Linux flows and clean-machine
  install/update/rollback/uninstall testing follow each channel that ships.

  Today the only routes to the binary are `curl | sh` and `irm | iex`. Both
  work, and neither is how most developers install a tool they have just heard
  about.
- [ ] **P1 — A reporting path that does not require assembling a report:**
  `collo feedback` opens a prefilled issue carrying `collo doctor`'s
  environment facts and the opaque `err-…` identifier, and nothing else,
  reusing the support bundle's existing privacy review rather than inventing a
  second one. `collo support bundle` already covers a reproducible failure; it
  is the wrong instrument for "this was confusing", which is most of what a
  beta needs to hear and none of what it currently receives.

  This is deliberately the zero-telemetry form of the optional telemetry
  decision above and does not wait on it: nothing is collected, nothing is
  transmitted in the background, and the user reads the whole report before it
  leaves the machine.

## Recommended next sequence

The setup journey, first completion controller, OG-1 runtime-owned primary
graph, the complete OG-2 experimental read-only orchestration program, and
OG-3A's first verified isolated-writer candidate wave, its seven trial-driven
controller corrections, OG-3A.8's audit-driven review-readiness corrections, and
OG-3B1–B6's retained-worktree accountability closure, verification-composition
correction, budget-accounting correction, user-owned execution envelope,
retained-worktree reconciliation, and adversarial campaign, and OG-3C's product
evaluations and exit-gate sign-off are now complete. OG-3 is closed.
Together, the graph milestones are the shipped experimental foundation for
evidence-gated durable execution. The
[Orchestrated Goal strategy](docs/ORCHESTRATION_STRATEGY.md) is the durable
contract and explicitly separates current evidence/recovery guarantees from
future graduation claims. The next orchestration slice is **the second OG-5
increment**. An Orchestrated Goal now runs end to end and survives a restart:
OG-5A pinned multi-worker scheduler restoration as exact and closed the case
where an interrupted publication left the parent workspace in a state no
later step could honestly reason about. What is left is measurement rather
than mechanism — the security, reliability, compatibility, and performance
campaigns, the Standard-versus-Orchestrated comparison, and the graduation
decision. OG-5B started that measurement by auditing the graduation gate's
permission clause and found a real bypass rather than confirming a property,
which is the argument for working through the remaining clauses the same way.
OG-5C then took the adversarial-corpus clause and found the opposite — the
mechanism sound, the evidence missing — which is reported as such rather than
dressed up as a fix.

1. Gather real-session evidence from the Standard completion gate: how often
   each rule intervenes, which verification commands are still missed by the
   recognizer after OG-3A.8's ecosystem breadth and OG-3B2's composition rule,
   and whether two interventions is the right bound. Keep this local and
   inspectable rather than adding telemetry by default.
2. Continue **OG-4 — reviewed integration and combined verification**. OG-4A
   closed the one path that could publish a graph candidate without the graph
   knowing, OG-4B made every publication into the parent recoverable by
   recording what it replaced before the first byte moves, and OG-4C published
   the first graph candidate into the workspace under that checkpoint, and
   OG-4D completes an integrated node only on checks that pass against the
   combined workspace, or on an explicit user-authored waiver. **OG-4 met its
   exit gate and closed on 2026-08-03.** Candidate ranking is deferred into
   OG-5's graduation review rather than built: it presupposes competing
   candidates for one node, which the runtime cannot produce, and a
   recommendation may rank only deterministic facts because a score never
   grants permission.
3. Then take OG-4 and OG-5: verified/recoverable combined-parent integration
   and durable graph recovery. A score, child test, or plan approval never
   grants permission. `collo audit --actor` remains the surface that can say
   what each participant was permitted to do.
4. Continue Phase 8 security campaigns in parallel with every feature wave,
   and take the performance budgets while the prompt-cache wave's measurement
   harness is still warm. **The reliability half has now shipped** — terminal
   loss, power-loss durability, filesystem exhaustion, and a cancellation gate
   raised from five iterations to twenty — so what is left under Phase 8's P0s
   is the part no amount of self-testing closes: sustained adversarial
   campaigns and an independent security assessment. That is now the only P0
   between here and 1.0, and it is a decision to commission rather than work to
   schedule.

Two small decisions can ride along with whichever wave ships next rather than
becoming waves of their own:

- The one-hour cache TTL. **The instrumentation has now shipped**, so this is
  waiting on data rather than on work. Collomia records the gap between
  consecutive provider requests and reports, in `/context` and the Session tab,
  how many of them exceeded the five-minute lifetime and how many of *those*
  were under an hour — the second number being the only evidence for the longer
  lifetime, since a gap beyond an hour is cold under either setting. A session
  that never paused reports nothing rather than a reassuring zero. Decide once
  a few real sessions have been read, not before: the write premium is 2x
  rather than 1.25x, and the five-minute lifetime refreshes on every read, so a
  chain of short gaps stays warm however long the session runs.
- Git write tooling under approval — **shipped**, as its own wave rather than a
  ride-along, because the classifier work it needed turned out to be the larger
  half. What remains of the original note is the checkpoint-commit idea:
  `/restore`'s change tracking is still in memory and still does not survive a
  resume, and a commit made at each turn boundary would fix that. `git_commit`
  is the mechanism such a feature would use; it is not that feature.
- Deliberately **not** another terminal-surface wave. Four of the last six
  touched it, the width sweep now pins the property the golden screens had
  recorded wrong, and the returns there are visibly smaller than a security
  control with no tests.

## Exit gates

### Multi-agent automation gate

- Independent writers cannot race or silently overwrite one another.
- Child claims are distinguishable from machine-observed fresh verification.
- Publication always requires review/policy and combined-parent verification
  remains explicit.
- Cancellation and recovery never replay a completed mutating action.

### 1.0 gate

Collomia should not call itself 1.0 or advertise safe unattended execution
until all of these are true:

- The sandbox and permission model pass cross-platform adversarial tests and
  an independent review.
- Sessions survive interruption; mutating actions are idempotent or explicitly
  reconciled; direct edits remain reviewable and recoverable.
- Every advertised provider has maintained capabilities and passing contracts.
- Long-context and multi-agent work has bounded budgets, cancellation,
  observability, and regression evaluations.
- MCP, skills, hooks, and repository configuration share the documented trust
  model.
- Release artifacts are natively signed where applicable, reproducible or
  provenanced, and install/update/rollback paths are tested.
- No known P0 data-loss, sandbox-escape, secret-exposure, or duplicate-mutation
  defects remain open.

## Explicitly deferred

- Cloud-hosted execution and account synchronization.
- Shared real-time team workspaces.
- A public plugin marketplace.
- Autonomous Git commits, pushes, pull requests, deployments, or issue updates
  by default. This is now enforced rather than merely intended:
  `permissions.publication` defaults to `prompt` and is not coverable by
  autonomy mode or a tool-wide grant.
- Persistent semantic memory across unrelated repositories.
- Decorative features that do not improve coding safety, accessibility, or
  throughput.
