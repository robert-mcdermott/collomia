# Orchestrated Goal strategy

**Status:** approved product and architecture strategy; evidence-gated durable
execution is available experimentally through completed OG-2, OG-3A's
verified isolated-writer candidate wave, OG-3A.1–.8 trial- and audit-driven
controller corrections, OG-3B1–B6's retained-worktree accountability closure,
verification-composition correction, budget-accounting correction, user-owned
execution envelope, retained-worktree reconciliation, and adversarial
campaign, and OG-3C's product evaluations and sign-off; OG-3 is complete and
OG-4 reviewed integration is next
**Roadmap owner:** Phase 6 — Multi-agent orchestration  
**Last updated:** 2026-08-03
**Canonical roadmap:** [`../ROADMAP.md`](../ROADMAP.md#phase-6--multi-agent-orchestration)

This document is the durable implementation charter for Collomia's next
agentic capability. It records the product decision, architectural boundaries,
delivery order, safety gates, evaluation plan, and current handoff state so the
work can continue across sessions and across Claude Code, Codex, Collomia, or a
human contributor without reconstructing the strategy from chat history.

The roadmap owns priority and completion status. This document owns the design
and execution contract. `docs/ROADMAP_HISTORY.md` records decisions and shipped
slices after they happen.

## Objective

**Orchestrated Goal is Collomia's experimental evidence-gated durable
execution mode.** The product name describes the user experience; the phrase
describes its runtime contract.

The user-facing promise is:

> Give Collomia an outcome. The model proposes an inspectable dependency
> graph, while the runtime executes only ready work, records evidence against
> repository state, recovers conservatively from interruption, and stops only
> when the applicable completion gates pass or the goal is explicitly blocked.

The working product name is **Orchestrated Goal**. Internally it is plan-graph
execution. The user-facing name emphasizes the outcome rather than the
implementation mechanism and avoids confusion with `/plan`, which is a
read-only planning surface.

This is a good direction for Collomia, but only as a staged, explicit opt-in.
It should not become a generic agent swarm, a larger permission mode, or a
default path for ordinary tasks.

## Evidence-gated durable execution

This term is a compact description of three properties, not a claim that the
model is infallible:

- **Evidence-gated:** a model or worker may propose that work is complete, but
  it cannot write the proof record or accept its own claim. The runtime applies
  a typed gate appropriate to the work and rejects missing, failed, or stale
  evidence.
- **Durable:** graph generations, attempts, failures, evidence, bounds, and
  terminal state are stored with the session. Resume starts no work by itself,
  and recovery distinguishes safe recomputation from an action that may have
  mutated files or caused an external effect.
- **Execution:** this is the operational state machine beneath a proposed
  logical plan. It owns readiness and transition order; it does not grant new
  tools, permissions, paths, network access, or publication authority.

The shipped OG-1 through OG-3 boundary supports one serial primary lane, at
most two governed automatic read-only workers, and—only in a candidate-only
graph—one bounded wave of at most two pairwise-disjoint terminal isolated
writers from a clean stable Git commit. It provides durable graph
truth, fresh machine-observed evidence, conservative invalidation, bounded
retry/revision, cooperative pause and resume, safe retry of eligible blocked
nodes, non-replay of ambiguous mutations, durable primary-plus-worker
accounting, user-owned whole-graph aggregate enforcement that stops the graph
for a person's decision rather than ending it, observed and reconcilable
retained worktrees, and a bounded comparative case for substantive independent
read fan-out. Writer results remain in their
own worktrees, require fresh detected-command verification tied to child state,
and stop the graph in a distinct `awaiting_review` outcome rather than reporting
their success as a blocker. It does **not** select or integrate a candidate,
cancel an optional branch or node, or reproduce a multi-worker scheduler
exactly after restart. Those are remaining OG-3 through OG-5 work and must not
be described as current behavior.

## Product decision

Collomia will retain two distinct execution modes:

| Mode | Contract |
| --- | --- |
| Standard | The current model-directed tool loop with evidence-gated completion. It remains the default. |
| Orchestrated Goal — experimental | Evidence-gated durable execution: the model proposes a dependency graph; the runtime owns readiness, attempts, evidence freshness, recovery decisions, and terminal state. Current automatic work includes bounded reads plus one retained isolated-writer candidate wave; candidate selection and parent integration remain staged milestones. |

Planning mode remains separate. `/plan` means read-only analysis and must not
implicitly start execution or delegation. An orchestrated run begins with an
explicit user action and a visible graph proposal.

The OG-2A preview resolves the initial interactive spelling as:

```text
/orchestrate Build a kanban application with tests and documentation
```

There is intentionally no headless or configuration activation surface yet.
Whether a future milestone adds `collo --orchestrate` remains an implementation
decision. The required experience is:

1. Collomia creates a proposed logical graph in a read-only design phase.
2. The UI shows dependencies, expected parallelism, declared write scopes,
   verification expectations, and graph budgets.
3. The user explicitly chooses to run it once.
4. The runtime, rather than a model message, transitions the session to
   execution.
5. Execution pauses for permissions, material ambiguity, scope expansion,
   conflicts, unavailable verification, or an exhausted bound.

Neither project configuration, project instructions, skills, hooks, nor the
model may enable experimental orchestration. The first release must require an
explicit per-session user action. A future release may let a user-level setting
remember that choice after the feature graduates; a repository must never be
able to opt the user in on its own.

## Strategic position

Collomia should compete on **trustworthy orchestration**, not on the maximum
number of agents:

- provider-neutral execution;
- a typed and inspectable graph rather than opaque agent conversation;
- runtime-owned readiness, evidence, freshness, budgets, and terminal state;
- isolated writers and guarded publication;
- deterministic recovery that never replays a mutation merely because a
  session resumed;
- permission checks on every actual action, with no authority gained from a
  plan, worker result, score, or passing test;
- useful single-agent behavior for work that does not benefit from a graph.

The competitor lessons are specific:

- Claude Code separates model-led subagents, shared-task agent teams, and
  script-led dynamic workflows. The useful ideas are dependency-ready work,
  explicit ownership of the plan, bounded fan-out, and repeatable verification.
  Collomia should not initially copy arbitrary model-generated JavaScript,
  peer mailboxes, shared-checkout writers, or unattended large swarms.
- Codex Goal mode reinforces outcome, constraints, completion criteria, and
  verification. Its subagent workflow reinforces a supervisor that receives
  bounded summaries and keeps the main context focused.
- OpenCode's Build/Plan and primary/subagent split reinforces that a planning
  permission surface and an orchestration engine are different features.

These products validate the direction, but Collomia's opportunity is a smaller,
typed, recoverable engine whose state transitions are part of the product
contract.

## Existing foundation

The difficult prerequisites are substantially present:

| Foundation | Current capability |
| --- | --- |
| Structured plan | Goals, stable step IDs, acyclic known dependencies, terminal reasons/evidence, persistence, and revisions. |
| Goal completion | Tool-free answers are checked against open plan state, stale verification after tracked writes, and unresolved tool failures. Outcomes are `done`, `blocked`, `cancelled`, or `budget_exhausted`. |
| Primary graph controller | OG-1 durably owns required-node readiness, immutable attempts/evidence, typed retry/revision, conservative Git freshness, verification, terminal state, and non-replaying recovery through an internal programmatic evaluation seam. |
| Delegation | Bounded batches, named profiles, plan-step association, per-agent budgets, steering, cancellation, durable results, and no recursive delegation. |
| Scheduling | Session-wide FIFO admission, global/provider limits, declared write scopes, concurrent disjoint writers, and serialization of overlapping or workspace-wide writers. |
| Isolation | Write delegates operate in separate Git worktrees and cannot directly mutate the parent workspace. |
| Evidence | Child summaries, changed paths/hunks, scope violations, usage, machine-observed child verification, and state tokens are durable and inspectable. |
| Integration | Freshness-bound review, conservative three-way composition, selective rooted publication, ordinary permission checks, and no automatic commit, merge, push, or worktree deletion. |
| Operations | Stable events, replay, audit attribution, token/cost/time/iteration bounds, cancellation, fail-stop persistence, and operator inspection. |

The current experiment is still intentionally incomplete:

- optional-branch semantics that would make node-level cancellation useful;
- an explicit user-owned verification-waiver interaction when meaningful
  automated verification is unavailable;
- later isolated-writer eligibility, guarded integration, fresh combined-parent
  verification, and reproducible multi-worker scheduler recovery; and
- a final event/automation compatibility decision before any headless
  activation surface.

## Authority model

The authority boundary is the central design constraint:

| Participant | Proposes or decides | Cannot establish by itself |
| --- | --- | --- |
| Model | Proposes decomposition, dependencies, acceptance criteria, execution class, result interpretation, suitable profiles, and bounded replans. | Node readiness, a successful machine result, evidence freshness, permission, or terminal completion. |
| Runtime | Decides readiness, claims, attempts, locks, scheduling, staleness, evidence identity, budgets, cancellation, recovery treatment, and whether structural completion is possible. | User intent, protected-operation approval, broader authority, or a verification waiver. |
| User | Decides whether to start orchestration, grants ordinary authority, resolves material ambiguity or conflicts, approves protected operations, sizes the execution envelope and grants more of it when one is reached, and may eventually grant an explicit verification waiver. | A graph approval does not manufacture machine evidence or make stale evidence fresh, and no envelope makes an unverified change acceptable. |

The model is never authoritative about machine state. It may explain that a
test passed, but only a recorded successful command bound to the current state
is machine evidence. It may propose that a node is complete, but only the
runtime may move an execution node through its acceptance gates.

Graph execution changes scheduling, not authority:

- every tool call passes through the existing registry, permission manager,
  hooks, sandbox, network, audit, timeout, cancellation, and output bounds;
- the runtime enforces the model-visible tool set again before decoding tool
  arguments; hiding `update_plan` or model-directed delegation after graph
  approval is an authority boundary, not merely prompt guidance;
- a denied operation blocks the node or asks the user; the coordinator may not
  delegate the same operation to bypass the denial;
- child profiles can only tighten the parent's effective permissions;
- plan approval, child verification, comparison, or ranking never grants
  publication permission;
- no graph mode automatically authorizes commit, push, deployment, release,
  issue changes, external messages, or another externally visible side effect.

The execution envelope belongs to the user, and this is deliberate. A resource
bound is not a safety property: exceeding one means the job is bigger than
expected, not that the work is unsafe. Every bound is therefore a speed bump —
it stops the graph, keeps every accepted node and retained candidate, and hands
the decision to a person, who may size the envelope in configuration up front
or grant another one at the moment it is reached. Making a bound terminal for
the *graph* rather than for the *turn* was the mistake it replaced: a
conservative ceiling then cost the user all completed work, which is the
opposite of conservative. Repository text, skills, hooks, and the model still
cannot widen it, because none of those is the user. The gates that *are* safety
properties — permission, verification, scope, publication — are unaffected by
any envelope and are not user-tunable in this way.

## Two-layer state model

Do not turn the current `plan.Plan` into a concurrent scheduler database. Keep
intent and execution related but separate.

### Logical plan

This is model- and user-visible intent:

- goal;
- logical steps and dependencies;
- acceptance criteria;
- expected read and write scope;
- suggested agent profile;
- whether work appears safely parallelizable.

The current compact plan remains useful in Standard mode. Orchestrated mode may
add a revision-aware proposal or patch tool instead of allowing a whole-plan
replacement to overwrite live execution state.

### Runtime execution graph

This is runtime-owned operational truth:

- plan revision and graph generation;
- logical node ID plus immutable attempt IDs;
- readiness and terminal state;
- queued/running worker and worktree identity;
- base commit and repository-state tokens;
- declared and observed scopes;
- machine evidence and artifact references;
- permission, verification, review, and integration outcomes;
- token, cost, iteration, retry, and elapsed-time use;
- invalidation, recovery, and replan history.

A useful internal lifecycle is:

```text
proposed
  -> ready
  -> running
  -> candidate
  -> child_verified
  -> awaiting_review
  -> integrated
  -> parent_verified
  -> done
```

Alternative transitions include `failed_retryable`, `stale`, `blocked`,
`cancelled`, and `budget_exhausted`. Read-only and primary-executed nodes can
skip stages that do not apply, but a write candidate cannot skip its
runtime-inserted gates.

Model-authored plan revisions use optimistic concurrency. A proposal names the
revision it was based on. The runtime rejects a stale proposal, preserves
completed attempts as history, validates the new acyclic graph, and invalidates
affected downstream nodes. A model cannot delete an attempt, rewrite machine
evidence, or silently expand a running node's scope.

## Evidence contract and runtime-inserted control gates

The evidence contract records what the runtime observed, which attempt
produced it, and which workspace state it describes. It is deliberately
different from a transcript claim: model-authored text such as “tests passed”
or “the files are correct” is useful interpretation, but never a substitute
for the corresponding record.

| Completion claim | Evidence the runtime accepts | Failure prevented |
| --- | --- | --- |
| Automatic read-only node | A non-empty bounded result from the claimed worker, at least one successful read tool result, and the same Git workspace token when the claim was state-bound. | Ungrounded summaries, failed-only investigations, or research accepted after the repository changed. |
| Primary node with no mutation | At least one successful bounded tool result on the active attempt, no unresolved failure, and a tool-free completion proposal for the runtime to assess. | A final-sounding answer closing an attempt with no machine-observed work or an unresolved failure. |
| Primary node after a potential mutation | Successful tool evidence plus a recognized successful verification command recorded after the mutation against the current Git token and mutation generation; a successful structured write must also have changed that token. | A no-op write, a test from before the edit, a success-masked command, or model-authored “tests passed” prose being used as proof. |
| Future delegated writer | Declared-scope compliance, fresh child verification, reviewed integration under ordinary permission, and fresh combined-parent verification. | A child's isolated pass being substituted for proof of the integrated workspace. |
| Blocked node | A typed failure or exact structured reason tied to the node and attempt. | Vague abandonment being reported as either success or an actionable blocker. |

Machine-observed evidence does not always mean command output. Current
automatic read workers intentionally cannot run commands, so successful read
tool results are their evidence. Command-backed verification becomes mandatory
after the graph observes a potential workspace mutation. Evidence is retained
for inspection, but acceptance expires conservatively when the current Git
workspace token no longer matches the state it proved.

Write work expands into system-controlled stages:

```text
Implement candidate
        |
Validate declared scope
        |
Verify child worktree
        |
Review and integrate
        |
Verify combined parent workspace
        |
Accept logical node
```

These gates are not ordinary model-authored plan steps and cannot be removed by
replanning.

A dependency unlocks only when the prerequisite is **accepted**, not merely
when a worker returned text. Acceptance means:

- read-only node: a successful bounded result is attached to the expected
  graph revision and remains fresh enough for its consumers;
- primary write node: tracked changes are present and fresh combined-workspace
  verification exists, or a user-authored waiver exists after that interaction
  is implemented; the current experiment blocks when verification is
  unavailable;
- delegated write node: scope is valid, child verification is fresh,
  acceptable changes are integrated under ordinary permission, and combined
  parent verification is fresh;
- verification node: the required machine command passed against the exact
  state token;
- blocked/skipped node: a structured reason identifies why the outcome cannot
  or need not be produced.

## Scheduling policy

The scheduler selects dependency-ready nodes deterministically. Parallelism is
an optimization, not the definition of orchestration.

Initial rules:

- preserve stable plan order among equally ready nodes;
- automatically parallelize only work declared independent;
- cap automatic fan-out below the existing absolute delegate limit;
- permit multiple read-only nodes when they do not consume changing workspace
  assumptions;
- run one parent-workspace writer at a time;
- allow isolated writer candidates concurrently only with disjoint declared
  scopes and a common stable base;
- serialize unknown, overlapping, nested, or workspace-wide write scopes;
- retain the current star topology: workers report to the coordinator and do
  not message or spawn each other.

The graph may decide that a goal should run serially. Opting into orchestration
does not promise multiple agents.

## Freshness and invalidation

Precise dependency invalidation is impossible when an arbitrary shell command
or external tool can read broadly. The first implementation must be
conservative rather than pretending to know every read.

- Every result is tied to a graph generation, base commit, and relevant state
  token.
- A parent write invalidates earlier combined-workspace verification.
- Child source drift invalidates child verification and review tokens, as it
  does today.
- Parent drift invalidates integration previews and any downstream assumption
  that consumed the prior bytes.
- Where read footprints are unknown, a repository mutation stales dependent
  research rather than silently reusing it.
- The initial scheduler should prefer finishing independent investigation
  before integrating writes, reducing avoidable invalidation.
- Later work may record rooted file-read footprints for built-in tools. Shell,
  MCP, and external-tool reads remain conservative unless their contract makes
  a narrower claim enforceable.

Stale is not failed. A stale node can be rerun, replaced by a replan, or made a
structured blocker after its retry bound is exhausted.

## Failure and replanning policy

Replanning is model judgment constrained by a runtime state machine.

Classify failures before deciding the next action:

- transient provider or tool failure: retry within the same attempt policy and
  existing provider resilience bounds;
- deterministic verification failure: create a bounded repair attempt or
  propose a graph revision;
- stale base or conflict: invalidate the candidate and replan or ask the user;
- permission denial: block or ask the user, never route around the decision;
- scope violation: retain the isolated candidate and block automatic
  integration;
- material ambiguity: ask the user instead of multiplying speculative work;
- exhausted token, cost, iteration, time, retry, or replan bound: finish
  `budget_exhausted` or `blocked` with exact evidence.

Each node and graph has a small retry/replan bound. Repeating the same failed
operation without a changed assumption does not count as progress. A graph
revision that expands write scope, external side effects, or material budget
requires renewed user confirmation.

## Verification and result synthesis

Verification must be proportionate and state-bound:

- verify an isolated writer in its child worktree before it is eligible for
  integration;
- after integration, run relevant verification against the combined parent;
- before terminal completion, run the repository's detected project-level
  verification when meaningful;
- any later tracked write stales the evidence;
- preserve exact command, exit status, bounded output, state token, and time;
- never interpret a success-masked compound command as passing evidence.

If meaningful automated verification does not exist, the current experiment
blocks honestly. A later milestone may pause for a user-authored waiver. A
model-authored `verification_note` can explain the situation but cannot waive
a graph's write-completion gate.

Candidate synthesis has two stages:

1. Deterministic eligibility removes candidates with scope violations, stale
   state, unresolved conflicts, failed required verification, or a mismatched
   task contract.
2. The coordinator may compare eligible candidates and recommend one with a
   visible rationale based on exact hunks, task fit, evidence, and cost.

A score or recommendation never authorizes application. The first writer
slice stops at a reviewable candidate and does not automatically choose among
competing results.

## Recovery guarantees and target contract

The current experimental mode makes these recovery guarantees:

- graph generation, logical nodes, immutable attempts, evidence, failures,
  bounds, and terminal state are stored in the durable session;
- a restored non-terminal graph is inert until the user explicitly runs
  `/orchestrate resume`;
- a cooperative pause request is durable, starts no new graph work, and becomes
  fully paused at the next provider/scheduler boundary after the current
  iteration finishes; an in-process resume retains the same active attempt;
- an interrupted replay-safe read is recorded as interrupted and may be
  recomputed only as a fresh bounded attempt;
- a potentially mutating or external action crosses a durable write-ahead
  transition before execution; if persistence fails it does not start, and if
  its outcome is ambiguous after interruption it becomes a reconciliation
  blocker and is never automatically replayed;
- completed evidence is reusable only while its workspace token remains fresh;
  later Git workspace drift invalidates it conservatively; and
- unsupported or structurally false saved graph state fails closed rather than
  being scheduled.

These guarantees prevent the most damaging recovery failure: repeating a
mutation because the runtime cannot tell whether it already happened. Resume
may repeat safe observation work, but never guesses that an ambiguous side
effect is safe to run again.

The full target contract additionally retains isolated writer candidates for
inspection, prevents repeated integration, restores reproducible multi-worker
scheduler order and aggregate bounds, and adds optional-branch/node
cancellation. Those guarantees belong to OG-2B2b through OG-5. Until they ship
and pass the recovery campaign, the current experiment must not be described
as fully resumable or suitable for unattended work.

## Operator experience

Reuse the existing context rail, Session tab, `/activity`, `/agents`, busy-safe
command lane, and event model rather than building a separate dashboard.

The operator must be able to see:

- goal, graph generation, and aggregate budget;
- ready, queued, running, awaiting-review, stale, blocked, and done nodes;
- why a node is serialized or ready;
- active worker/profile/provider, current action, time, and usage;
- changed files, declared scope, scope violations, and worktree;
- child and parent verification state;
- the exact cause and consequences of a replan or invalidation;
- what decision is required from the user.

The current preview provides status and node inspection, whole-graph cancel,
cooperative pause/resume, and safe retry of an eligible blocked node. Pause is
not an OS-process suspension: the current provider/tool/read iteration may
finish, then the runtime records the safe boundary and starts no new work.
Whole-graph cancel remains the immediate-stop control. Retry creates a new
bounded attempt while preserving the blocked attempt and its evidence; it is
rejected after attempt exhaustion or when an interrupted mutation may already
have happened. Node cancellation remains a target only after the graph can
express an optional branch; today every node is required, so cancelling one is
equivalent to cancelling the graph. Review of a proposed graph revision also
remains target work.

## Delivery plan

Milestone identifiers are durable and should be used in commits, pull requests,
roadmap updates, and future agent handoffs.

### OG-0 — Strategy and continuity

**Status: complete.**

- Record this charter.
- Link it from the roadmap and repository entry points.
- Add cross-agent instructions that identify the canonical files and update
  protocol.
- Record the decision in roadmap history without claiming implementation.

### OG-1 — Runtime-owned primary graph controller

**Status: complete (2026-08-01).**

Do not increase the number of automatic actors.

- Add a durable execution-graph and attempt state model separate from the
  logical plan.
- Select the next dependency-ready logical node deterministically.
- Drive that node through the existing primary agent and ordinary tool path.
- Add bounded failure classification, retry, graph revision, and downstream
  invalidation.
- Insert and enforce combined-workspace verification gates for primary writes.
- Emit stable graph lifecycle/activity events and expose state in the existing
  TUI/headless surfaces.
- Preserve current permission, cancellation, persistence, and budget behavior.
- Add product evaluations before exposing an experimental user mode.

Implemented contract and refinements:

- OG-1 is one milestone delivered through two internal increments: first the
  pure durable graph/state contract, then its primary-agent execution,
  recovery, visibility, and evaluation path. Neither increment is a shipped
  product mode on its own.
- The graph is exercised programmatically by offline product evaluations in
  OG-1. CLI/slash-command opt-in and graph approval remain OG-2 work.
- Potentially mutating actions require a durable write-ahead graph transition
  before execution. Resume may retry interrupted read-only work in a new
  attempt, but an ambiguous mutation becomes a reconciliation blocker and is
  never replayed.
- Initial primary-write state tokens are Git-backed. Read-only graphs can be
  repository-agnostic; a write-bearing graph whose combined workspace cannot
  be state-bound blocks rather than claiming verified completion. Standard
  mode retains its existing non-Git behavior.
- A model-authored verification note is not a graph waiver. Until OG-2 adds an
  explicit user-owned waiver interaction, unavailable meaningful verification
  blocks the graph honestly.
- The graph is bounded to 12 required nodes, two attempts per node, two graph
  revisions, eight acceptance criteria per node, and two completion
  interventions. A material blocker ends the required graph immediately.
- `propose_goal_graph_revision` uses optimistic graph-generation concurrency
  and cannot rewrite immutable attempts or evidence. `block_goal_node` records
  an exact blocker. Both change scheduling state only, bypass restrictive
  profile tool allowlists for controller availability, and grant no action.
- Schema-1 graph snapshots are embedded as additive `goal_graph` session
  records. Mutating or external actions cross a durable write-ahead transition;
  active sessions cannot switch, rewind, or reset underneath the controller.
- `goal.graph.update` is a bounded schema-v1 lifecycle event projected into the
  activity timeline and headless progress. This is an explicit internal-only
  compatibility addition: Standard event streams never emit it, and OG-2 must
  revisit the decision before exposing the mode to automation consumers.
- The Git combined-workspace token covers HEAD, index and working-tree binary
  diffs, and non-ignored untracked regular-file paths, modes, and bytes. A
  structured write that leaves the token unchanged is not accepted. Successful
  verification records bounded output, exact command, status, token, mutation
  generation, and time.
- Normalized retryable provider failures and recoverable tool failures consume
  a fresh immutable node attempt. Permission, hook, unavailable-state, and
  ambiguous-action failures block instead of being routed around. Cancellation
  and all existing token, cost, and iteration limits retain their terminal
  outcomes.

Exit gate:

- a primary-only graph survives success, recoverable failure, permission
  denial, cancellation, stale workspace state, budget exhaustion, and resume;
- no open/stale/unverified required node can produce `done`;
- no mutation is duplicated during recovery;
- Standard mode behavior and compatibility remain unchanged.

Exit-gate evidence:

- `go test -count=1 ./...`
- `go test -race -count=1 ./...`
- `go vet ./...`
- `go build ./...`
- The credential-free OG-1 product evaluations drive real primary
  read/edit/command tools, permission decisions, Git state tokens, repository
  tests, recoverable failure, and denial. App/session tests prove an ambiguous
  write-ahead action restores as blocked; state-machine tests prove stable
  readiness, stale invalidation, bounded revision/retry, cancellation, budget
  exhaustion, corrupt-snapshot rejection, and no mutation replay.

### OG-2 — Experimental Orchestrated Goal with read fan-out

**Status: complete through two bounded increments (2026-08-02).**

#### OG-2A — Explicit primary-only preview

**Status: complete (2026-08-01).**

This increment exposes OG-1 for real operator use without increasing the
number of actors. It resolves the first user-facing command spelling as a TUI
slash-command family:

- `/orchestrate <goal>` starts a read-only proposal turn and cannot execute
  work;
- `/orchestrate approve` is the one-time per-session opt-in that converts the
  newly proposed logical plan into runtime graph state and begins execution;
- `/orchestrate status [node-id]` shows the proposal or durable node, attempt,
  failure, evidence, readiness, and terminal state;
- `/orchestrate cancel` cancels a pending proposal or the active graph; and
- `/orchestrate resume` explicitly reattaches a saved non-terminal graph after
  process or session resume. Persisted graph bytes alone never activate it.

An approvable proposal must be newer than the state present when proposal mode
began, contain only pending steps, and give each step at least one concrete
acceptance criterion. The preview shows dependencies, the serial primary-only
execution lane, fixed node/attempt/revision bounds, ordinary permission and
publication posture, and the fresh combined-workspace verification rule.
Project configuration, instructions, skills, hooks, model output, and a saved
session still cannot opt the user in. Planning mode remains read-only.

The preview adds an experimental status badge and projects graph nodes into the
existing context rail and activity view. It does not add automatic delegates,
pause, node-level cancellation/retry controls, verification waivers, new
permissions, or a headless activation flag. Those omissions are visible rather
than implied.

Exit gate:

- a user can propose, inspect, approve, execute, cancel, and explicitly resume
  a primary-only graph in the TUI;
- approval cannot consume a stale/restored ordinary plan or a plan without
  concrete acceptance criteria;
- no project-controlled input or persisted graph activates the mode;
- Standard and read-only planning behavior remain unchanged.

Completion evidence:

- runtime/app/TUI tests prove fresh-proposal consent, concrete acceptance
  criteria, proposal cancellation and planning-mode restoration, inert saved
  graphs, explicit resume, terminal-session isolation, visible status/rail
  state, and the prohibition on carrying proposal consent across rewind;
- the credential-free product evaluation drives a real read-only proposal,
  explicit approval, dependency-ready primary execution, real repository read,
  and terminal evidence while asserting that proposal tools cannot mutate and
  execution cannot delegate; and
- `go test -count=1 ./...`, `go test -race -count=1 ./...`, `go vet ./...`,
  `go build ./...`, formatting checks, and documentation-contract tests passed.

#### OG-2B — Bounded read-only fan-out

**Status: complete (2026-08-02).**

##### OG-2B1 — Runtime-selected read fan-out kernel

**Status: complete (2026-08-02).**

- Extend the approved logical proposal with an explicit `read_only` execution
  class; omitted execution remains `primary` and therefore serial.
- Let the runtime claim only dependency-ready, user-approved `read_only`
  nodes, in stable plan order, with at most two live automatic workers.
- Run those workers through the existing read-only delegated-agent boundary:
  planning-mode tools, inherited-or-tighter permissions, no nested delegation,
  no shared-plan mutation, and no write worktree.
- Persist worker identity, bounded result evidence, freshness, usage, and the
  scheduler reason in the runtime graph. A child sentence cannot directly mark
  a logical node done.
- Add fixed experimental aggregate read-work bounds and expose them before and
  during execution. Trivial or primary-only graphs must start no child.
- Keep primary execution and every parent-workspace write serial; automatic
  read workers finish before the primary lane advances.

Exit gate:

- two independent approved read nodes run concurrently and their grounded,
  bounded results unlock a dependent primary node;
- stable ordering, concurrency, aggregate-work, cancellation, freshness, and
  read-only authority limits are enforced by runtime tests;
- a primary-only or dependency-serial graph launches no unnecessary worker;
- Standard mode and OG-2A activation/approval behavior remain unchanged.

Completion evidence:

- the graph unit suite proves stable two-worker claims, primary-only and
  dependency-serial fallback, aggregate start/token/wall bounds, freshness
  retry, cancellation, and additive restoration of pre-fan-out snapshots as
  serial `primary` work;
- the credential-free product evaluation drives a real application runtime
  with two concurrent read-only workers, asserts their no-write/no-command/
  no-recursion/no-graph-control surface, ingests grounded Git-fresh summaries,
  and unlocks the dependent primary only after both workers finish;
- a second product evaluation cancels both in-flight workers and proves the
  runtime records a terminal `cancelled` graph rather than converting the
  interruption into a blocker; and
- focused plan, graph, agent, app, and evaluation suites plus the full
  verification commands recorded in `docs/ROADMAP_HISTORY.md` passed.

##### OG-2B2 — Operator controls and comparative evidence

**Status: complete (2026-08-02).**

###### OG-2B2a — Cooperative pause and safe retry

**Status: complete (2026-08-02).**

- Add `/orchestrate pause` as a cooperative runtime control. A durable pause
  request prevents new scheduling, lets the current provider/tool/read
  iteration reach its boundary, then records the graph as paused.
- Add `/orchestrate resume` for an attached paused graph. In-process resume
  clears only the pause state and preserves the active attempt, evidence, and
  bounds. A restored graph remains inert and requires the same explicit
  command before it can run.
- Add `/orchestrate retry <node-id>` only for a safely retryable blocked node
  with remaining attempt budget. Preserve its blocked attempt and evidence,
  clear the terminal blocked outcome, and let ordinary dependency readiness
  create the next attempt.
- Reject retry when a non-replayable action is unresolved or an interrupted
  mutation may already have happened. Do not manufacture a successful attempt
  or replay an ambiguous side effect.
- Keep `/orchestrate cancel` as the immediate whole-graph stop. Do not add a
  misleading node-cancel surface while every graph node is required.
- Persist pause state additively in graph schema 1 and keep the new lifecycle
  states inside the existing internal-only `goal.graph.update` event contract.

Exit gate:

- an active run can be asked to pause without cancelling its current attempt,
  reaches a visible safe boundary, and resumes without losing in-process graph
  state;
- a safe blocked node can enter a new bounded attempt while the prior attempt
  remains inspectable;
- ambiguous mutation and exhausted-attempt retries fail closed; and
- Standard mode, activation consent, permissions, automatic-read bounds, and
  inert restore behavior remain unchanged.

Completion evidence:

- graph, agent, app, and TUI tests cover durable pause boundaries, preservation
  of an active attempt, explicit attached and restored resume, safe retry,
  ambiguous-mutation rejection, exhausted-attempt rejection, busy-command
  admission, lifecycle events, and visible pausing/paused state; and
- the full verification commands recorded in `docs/ROADMAP_HISTORY.md` pass.

###### OG-2B2b — Aggregate presentation and comparative evidence

**Status: complete (2026-08-02).**

**OG-2B2b1 — Durable aggregate accounting and presentation**

**Status: complete (2026-08-02).**

- Persist separate primary and automatic-read counters for provider
  iterations, input/output tokens, cost availability, estimated cost, and the
  start of the explicit proposal turn.
- Include the proposal call in the primary lane so the extra work required to
  create an Orchestrated Goal graph is not hidden from later comparisons.
- Record primary provider failures and compaction calls as iterations even
  when they report no tokens. Retain the same counters on an active node
  attempt when one exists.
- Record automatic-worker iterations with their existing tokens/cost on the
  immutable read attempt and in the graph aggregate.
- Show total, primary, and automatic-read work plus elapsed time in
  `/orchestrate status`. If any token-bearing lane lacks configured pricing,
  report cost as unavailable rather than `$0`.
- Keep the additive record in graph schema 1. A pre-accounting snapshot
  reconstructs only the attempt usage it actually stored; missing proposal and
  provider-iteration history remain zero instead of being guessed.

Exit gate:

- the explicit proposal, serial primary work, automatic reads, provider
  failures, and retries contribute exactly once to durable counters;
- aggregate and per-lane status is inspectable before, during, and after graph
  execution without granting authority or activating a saved graph;
- incomplete pricing is unmistakably unavailable; and
- Standard mode, existing primary/profile bounds, and the fixed automatic-read
  envelope remain unchanged.

Completion evidence:

- graph and agent tests prove durable accumulation, per-attempt attribution,
  unavailable-cost handling, failure iteration accounting, elapsed-time
  presentation, invalid snapshot rejection, and conservative legacy restore;
- credential-free application evaluations prove the proposal is counted and a
  two-worker fan-out plus primary synthesis produces the exact six-iteration
  primary/read split expected from recorded provider calls; and
- the full verification commands recorded in `docs/ROADMAP_HISTORY.md` pass.

**OG-2B2b2 — Aggregate bounds and comparative evidence**

**Status: complete (2026-08-02).**

- Enforce whole-graph aggregate token, cost, iteration, and active wall-clock
  bounds using the durable accounting record without weakening tighter
  primary/profile/worker bounds.
- Compare decomposable, cross-layer, trivial, and inherently serial scenarios
  against Standard and primary-only Orchestrated execution; retain fan-out
  only where the measured quality or elapsed-time benefit justifies its cost.
- Finish the event/automation compatibility decision required before any
  headless activation surface is considered.

Implemented contract:

- Persist fixed, non-configurable experimental ceilings of 96 aggregate
  provider iterations, 1,000,000 aggregate input/output tokens, $5 estimated
  cost when every token-bearing contribution has configured pricing, and 30
  minutes of active execution after approval.
- Apply the limit at provider/scheduler admission and again after recorded
  usage. An exact limit prevents another request; a response that crosses it
  terminates the graph `budget_exhausted`. Tighter primary/profile/read limits
  continue to win. If one result in a completed parallel read wave crosses the
  limit, the runtime still records its already-finished siblings before
  returning the terminal outcome.
- Divide the remaining token, iteration, cost (when enforceable), and active
  wall allowance among an automatic read wave and retain those bounds on each
  immutable attempt.
- Stop the active clock at a reached cooperative pause, terminal transition,
  and durable process boundary. Restarted bytes are inert; only explicit
  resume restarts the clock. Proposal usage is included in aggregate work, but
  user review, pause, and downtime do not consume the post-approval active
  execution allowance.
- Keep unpriced work visibly `cost unavailable`. The runtime cannot prove a
  dollar total without pricing, so it enforces tokens, iterations, and active
  wall rather than pretending the $5 cost gate was observed.
- Keep aggregate accounting in the internal graph snapshot and TUI status.
  Do not add event kinds or `goal.graph.update` usage fields before a headless
  activation surface has an actual compatibility consumer.
- Compact proposal history once at the approval-to-execution boundary when
  enough history exists. During execution, compact again when one estimated
  prompt reaches one-eighth of the remaining cumulative token allowance. The
  summary request remains ordinary recorded primary-lane provider work.
- Preserve the immutable stored ceiling on older graphs, including graphs
  created under the initial 192,000-token preview. A software upgrade may
  raise the ceiling for a newly approved graph but never widen saved authority.

- Build on OG-2A's explicit per-session opt-in, graph approval, status,
  cancellation, and inert-resume contract without weakening it.
- Allow at most two automatically selected concurrent read-only delegates by
  default, even if the manual delegate ceiling is higher.
- Keep one serial primary write lane in the parent workspace.
- Start with a guideline of at most 12 logical nodes, two graph revisions, and
  two attempts per node; measure before making these configuration surface.
- Locally record why the scheduler delegated, serialized, retried, replanned,
  invalidated, blocked, or finished. Do not add default telemetry.

Exit gate:

- decomposable read-heavy and cross-layer evaluations demonstrate a useful
  elapsed-time improvement over Standard mode at equal grounding (a quality
  improvement remains unmeasured while the harness scripts equal answers);
- cost and extra model work are visible and bounded;
- trivial or inherently serial work remains serial;
- project content cannot enable the mode or widen authority.

Completion evidence and retained decision:

- state/agent tests cover exact-bound admission, post-response token/cost
  overage, unpriced-cost behavior, active pause/resume time, stored-bound
  validation, narrowed automatic claims, and the graph-level terminal result;
- credential-free comparisons run equal grounded scenarios through Standard,
  primary-only graph, and two-worker graph execution. For decomposable facts
  and cross-layer source/test investigation with equal substantive read
  latency, fan-out completes faster because its expensive reads share one
  critical-path wave; all three produce the same grounded answer. The measured
  claim is elapsed time only: answer quality is held equal by the scripted
  harness rather than observed, so no quality improvement has been
  demonstrated and none should be claimed from this evidence;
- the comparison also proves fan-out spends six visible provider iterations
  and more tokens than Standard. The result is therefore narrow: retain it for
  independently ready, substantive read investigations, not as a general
  claim that parallel execution is cheaper or always faster; and
- primary-only trivial work launches no worker, while dependency-serial reads
  have at most one active worker and gain no parallelism.

### OG-3 — Isolated writer candidates

**Status: complete (2026-08-03). OG-3A plus corrections through OG-3A.8, OG-3B1–B6, and OG-3C's product evaluations and exit-gate sign-off all shipped.**

- Automatically dispatch only dependency-ready writers with declared disjoint
  scopes and a stable base.
- Keep writers isolated, non-recursive, bounded, and attributable.
- Require observed-scope validation and fresh child verification.
- Produce reviewable candidates and deterministic eligibility facts.
- Do not automatically select or integrate a competing candidate.
- Retain conflicting, stale, failed, or out-of-scope worktrees for inspection.

OG-3A implemented contract:

- `execution: isolated_write` is valid only with an explicit normalized scope
  narrower than the whole workspace. The runtime, not the model, selects at
  most two dependency-ready nodes in stable order and excludes every scope
  that overlaps an earlier claim.
- Every claim in the wave is durably bound to the same clean parent-workspace
  state token and exact Git commit. Worktree creation names that commit rather
  than resolving a later `HEAD`; parent drift makes the candidate ineligible.
- Dispatch reuses the ordinary `delegate` write permission decision,
  `permission_decision`/`tool_start` hooks, shared scheduler, non-recursive
  child runtime, declared-scope enforcement, bounded provider accounting, and
  retained `collomia/…` worktree identity.
- The application redetects repository-standard verification commands inside
  each retained child worktree. Every command receives its ordinary
  `run_command` permission and hook decision. A candidate is eligible only
  when all detected commands pass against one unchanged child-state token,
  changed files remain in scope, the child base matches, and the parent token
  is unchanged.
- An eligible result becomes immutable attempt state `candidate` with bounded
  worker/worktree/branch/base, changed-file, scope, verification-command, and
  state-token facts. Its logical node becomes `blocked` with a review-required
  reason; dependents do not unlock and the parent workspace is untouched.
- A process boundary while a writer is running becomes an interrupted-action
  blocker and cannot be retried automatically or through the safe-retry path.
  OG-3B1 adds exact association of the orphaned in-flight worktree, and OG-3B5
  adds observing what is actually left in it and discarding it on the user's
  explicit instruction. Deciding what may be *reused* remains OG-4 selection
  and OG-5 recovery work.

OG-3A.1 aggregate-budget usability correction:

- A real eleven-node application proposal consumed 67,149 tokens before
  approval and another 119,977 tokens in seven primary execution calls. The
  controller correctly refused a 17,793-token next request with only 4,874
  tokens left, but the initial 192,000-token envelope made the preview
  impractical before it reached an isolated writer.
- New graphs therefore use a fixed 1,000,000-token ceiling. The 96-iteration,
  conditionally enforceable $5 cost, and 30-minute active-wall limits remain
  unchanged, as do tighter provider/profile/worker bounds.
- `/context` and the Session tab distinguish the estimated prompt for one
  request from cumulative proposal-plus-execution consumption. Proposal status
  shows the exact usage that approval will seed and the remaining allowance
  before the approval-boundary summary request.
- This is calibration, not added authority: at the time, configuration,
  repository text, skills, hooks, and the model could none of them widen the
  maximum, and compaction never deletes the durable transcript or escapes
  accounting. OG-3B4 later opened the envelope to *user* configuration and to
  an explicit user grant, on the reasoning recorded there; repository text,
  skills, hooks, and the model still cannot widen it.

OG-3A.2 primary-loop budget and evidence-diagnostic correction:

- A real primary execution used eight provider iterations in a recoverable
  first attempt and sixteen in its replacement, then hit the ordinary
  24-iteration turn counter even though the graph had used only 31 of its 96
  aggregate iterations. `max_iterations` now bounds one immutable primary
  attempt and renews only at a runtime-recorded accepted or retry boundary;
  the stored 96-iteration whole-graph envelope remains the outer ceiling.
- The same run repeatedly invoked passing pytest commands through
  `2>&1; echo "EXIT_CODE=$?"`. Rejecting that compound as proof was correct—the
  final `echo` can mask pytest's status—but returning only a later generic
  completion gap caused an avoidable loop. Verification-like rejected
  commands now return the exact reason and a direct-command suggestion;
  leading environment assignments and virtual-environment executable paths
  remain eligible when the command is otherwise direct.
- Once a graph is approved, model-directed `update_plan` and delegation tools
  are absent by design. The execution path now enforces the same availability
  decision before argument assessment, so a remembered or fabricated hidden
  call cannot reach its decoder or implementation and does not poison the
  active work node.
- This is a bounded-loop correction, not a larger autonomy grant: each attempt
  retains the configured primary limit, retries and graph attempts remain
  finite, and token, cost, active-wall, permission, cancellation, and
  persistence gates are unchanged.

OG-3A.3 progress-aware primary control and workspace-evidence correction:

- A third fresh-project trial reached 24 provider cycles in one productive
  primary attempt. The attempt had created application and test files, repaired
  dependency and import failures, passed five tests, and completed a live
  health check. Its final allowed action produced fresh recognized pytest
  evidence, but the lifetime attempt counter stopped before the model could
  submit the completion proposal. The fixed number 24 came from the ordinary
  `options.max_iterations` default; it was not a tool-call limit and did not
  describe lack of progress.
- In Orchestrated Goal, `max_iterations` now measures consecutive provider
  cycles since the last novel durable successful tool evidence. A new result,
  resolved failure outcome, changed workspace token, or verification bound to
  a new workspace generation renews that lease inside the same immutable
  attempt. Repeating equivalent evidence does not. Standard mode still uses
  `max_iterations` as its total turn bound, while the graph's fixed 96
  provider-iteration, token, conditional-cost, and active-wall envelope
  remains the non-renewable outer bound.
- Write-ahead safety and evidence freshness now use separate generations.
  Every potentially mutating or external action still advances the global
  durable generation before execution, preserving the rule that an
  interrupted non-replayable action is ambiguous and never replayed. When the
  action returns with the same machine-observed Git workspace token it began
  with, the active attempt retains its prior observed workspace generation.
  Starting a server, reading its output, or issuing a smoke-check request can
  therefore no longer stale passing tests when repository bytes did not
  change. A changed or unavailable post-action token remains conservative and
  still requires fresh verification.
- Successful direct verification returns a positive receipt to the model that
  it was recorded against the post-command workspace state. Direct
  `python -m pytest`, virtual-environment Python module forms, and `uv run
  python -m pytest` join the recognized commands; compound shell status
  masking remains ineligible.
- Proposal guidance now prefers four to six substantive outcome nodes and
  treats twelve as a hard maximum rather than a target. Serial changes sharing
  a write scope and verification surface should be coalesced. This reduces
  provider/context overhead without weakening dependency, isolation,
  permission, or acceptance boundaries.

OG-3A.4 completion-gap and executable-topology correction:

- A fourth clean-project trial exposed two different failures after the
  ordinary progress lease was repaired. The first scaffold node consumed 54
  provider iterations and 900,582 tokens while repeatedly varying passing
  pytest commands. Only the final direct `uv run pytest -q` qualified, even
  though earlier commands were the equivalent verifier wrapped in an exact
  workspace `cd`, an environment assignment, and `2>&1`. The controller then
  accepted that node but immediately blocked the next isolated writer because
  primary scaffolding had necessarily left the parent workspace dirty.
- Safe verification canonicalization now removes only an exact redundant
  `cd` to the current workspace (or `.`) followed by `&&`, plus a final literal
  `2>&1`. Those wrappers preserve the verifier's exit status. Other
  directories, pipes, semicolons, `||`, and status-masking composition remain
  ineligible and receive direct-command guidance.
- A durable completion-gap fingerprint and provider-iteration watermark now
  bound remediation. Once the runtime records an unmet gate, only evidence
  capable of changing that gate renews its four-cycle lease: recognized
  verification for a verification gap, an observed state token for a missing-
  state gap, a real workspace change for a no-op-write gap, or the first
  successful bounded result where none existed. Different command strings,
  repeated passing output, and unrelated reads remain inspectable evidence but
  no longer buy an unbounded sequence of retries. An exhausted gap ends
  truthfully `blocked`; aggregate, permission, cancellation, and non-replay
  bounds are unchanged.
- Approval now validates the current controller's actual schedulability.
  End-to-end build/change proposals must use `primary`. Because OG-3A retains
  candidates without selecting or integrating them, `isolated_write` is valid
  only in a candidate-only graph (optionally after `read_only` prerequisites),
  every writer must be a terminal leaf, and no node may depend on it. Bounded
  graph revisions obey the same rule.
- Candidate-only approval observes the same stable Git base as dispatch and
  refuses a dirty, missing, or changing base before durable execution state is
  created. Dirty failures list the observed paths and tell the operator to
  commit or reconcile them. The proposal remains available after correction
  instead of becoming an immediately blocked graph.

OG-3A.5 repair-progress and verifier-bootstrap correction:

- The next fresh Kanban trial confirmed that OG-3A.4 stopped churn but made
  the lease too literal. Node 1 installed dependencies, imported the FastAPI
  app, started Uvicorn, and received HTTP 200 from both `/` and `/health`,
  satisfying its model-authored acceptance criteria. The generic mutation gate
  still required a conventional detected verifier. Because pytest had been
  installed but no test existed yet, the final correctly formed
  `uv run pytest -q` returned exit 5 (“no tests collected”). The controller
  then blocked at the provider boundary before the model could see that useful
  failure and create the missing smoke test.
- The completion-gap watermark now advances on three bounded machine facts: a
  successful result that directly closes a gate, an actual Git workspace-state
  change that constitutes repair work, or a novel recognized verification
  failure whose output differs from prior failure evidence. The failed command
  spelling is deliberately excluded from equivalence, so running the same
  failure through another wrapper or executable does not renew the lease.
  Repeated identical output, unrelated reads, and rejected command forms still
  stop after four provider iterations; the aggregate envelope remains
  unchanged.
- Failed verification evidence now retains its bounded tool output as well as
  the typed failure, allowing the runtime to distinguish “no tests collected”
  from a later assertion, import, or syntax failure. A later successful run of
  the same governed command resolves the recoverable failure normally.
- Proposal guidance now requires every mutating primary node to name a direct
  build/lint/test verification surface. If none exists yet, the first mutating
  node must create a focused smoke test before proposing completion. The
  controller notice gives the same instruction when a detected runner reports
  no tests, rather than encouraging another command wrapper.
- Saved graph bytes remain inert after reopening a conversation, but an
  explicit `/orchestrate retry <node-id>` can now restore a terminal blocked
  graph and perform the already bounded safe-retry transition in one action.
  Ordinary nonterminal continuation still uses `/orchestrate resume`.

OG-3A.6 multi-wave lifecycle and node-boundary efficiency correction:

- The first complete application wave proved the mode can deliver an
  end-to-end project, but its follow-up SQLite wave showed that terminal graph
  attachment was incorrectly session-long and that a model could continue
  implementing future logical nodes inside the current runtime attempt.
- A terminal attached or inert saved graph now yields automatically when the
  user starts `/orchestrate <new-goal>`. The runtime appends a durable
  `goal_graph` tombstone and detaches graph-only controls while leaving every
  prior snapshot, transcript message, and evidence record auditable.
  `/orchestrate cancel` against an already-terminal graph performs the same
  explicit archive operation. Active graphs still cannot be displaced.
- Successful current-state verification now returns an explicit node-boundary
  instruction: if the running node's criteria are satisfied, the next model
  response must be tool-free; work assigned to later nodes must wait for the
  runtime. The authoritative pinned graph repeats this contract every request.
- After the runtime accepts any non-final node, it performs a deterministic,
  zero-provider handoff compaction. The next request contains a bounded
  runtime-authored acceptance/next-node notice plus the pinned graph, not the
  previous node's growing tool transcript. The graph retains bounded accepted
  dependency summaries needed for later synthesis. The complete transcript survives
  unchanged in durable session history, so auditability does not require
  paying to resend it.
- Proposal size follows scope: one to three coherent nodes for a scoped change,
  four to six only for broad work, and twelve remains the fixed maximum.
  Serial work sharing state and verification is coalesced. Built-in file
  listing/search also skips dependency, cache, virtual-environment, and build
  trees so generated data does not dominate inspection context.
- This correction deliberately does not widen the one-million-token ceiling.
  The observed exhaustion happened because one logical attempt crossed five
  node boundaries and resent a growing uncached prompt, not because the fixed
  envelope was too small for the resulting code. Recalibration remains an
  evidence-based option after another clean trial.

OG-3A.7 proposal-state authority and escape-path correction:

- A successful Kanban6 application wave and same-session SQLite follow-up
  validated OG-3A.6. A third drag-and-drop wave then exposed a distinct
  proposal problem: the model marked its proposal-time read investigation
  `done`, and the approval validator rejected that annotation before the
  runtime could perform its existing fresh-pending normalization.
- Proposal plan status and evidence are model-authored design annotations, not
  operational state. Approval imports only the goal, node identity/topology,
  execution class, write scope, and acceptance criteria. It initializes every
  runtime node pending, clears proposal evidence, and creates no attempt until
  the runtime scheduler selects one. This is the same authority boundary used
  for completion claims.
- Proposal instructions now distinguish investigation needed to formulate the
  graph from a post-approval `read_only` dependency. Work already performed to
  understand the request should not become a graph node merely so it can be
  repeated; a fresh pending read node remains available when later work
  genuinely depends on runtime-recorded investigation.
- `/orchestrate cancel` remains the direct proposal escape. `/plan off` now
  safely aliases “cancel the unapproved proposal and return to execution mode”
  rather than leaving the user in a read-only dead end. It does not approve the
  plan, expose execution tools within the proposal, or import plan evidence.

OG-3A.8 review-readiness correction (implementation audit, not a trial):

- A full audit of the shipped implementation against this charter found three
  classes of problem: the evidence gate was narrower than the design language
  implied, two acceptance decisions were made by matching English strings, and a
  successful candidate wave was reported to the operator as a failure. None of
  the corrections widen authority.
- Recognized verification is no longer what decides which languages the mode
  can finish work in. The recognizer unwraps environment-manager prefixes
  recursively (`uv`, `poetry`, `pipenv`, `pdm`, `hatch`, `rye`, `pixi`,
  `conda`/`mamba`/`micromamba` with an environment selector, `bundle exec`,
  `npx`) and recognizes R, Ruby, Elixir, PHP, Swift, CMake/`ctest`, Deno,
  Haskell, Bazel, `just`/`task`, `tox`/`nox`, and Java build tools beside the
  original Go, Rust, Node, and Python forms. Detection gained matching markers
  and reports the runner a Python repository actually uses. A wrapper qualifies
  only when what it wraps is itself a recognized check, and each new ecosystem
  contributes one test entry point so candidate suites stay short.
- `git diff --check` is no longer recognized. A whitespace linter passes on
  nearly any tree, so it let a mutating node close its verification gate
  without checking the change it had just made.
- Completion gaps are typed runtime state (`no_tool_evidence`,
  `no_state_token`, `no_op_write`, `no_fresh_verification`), persisted
  additively, with the operator-facing sentence derived from the kind. Matching
  prose to decide whether the model made progress is what made OG-3A.2 through
  OG-3A.5 a sequence of similar corrections. Pre-typing snapshots recover their
  kinds once at restore, and an unrecognizable legacy sentence clears the gap
  rather than leaving one that cannot be enforced. Read-node groundedness uses
  a machine-counted successful-tool total rather than a phrase found in the
  worker's rendered evidence lines.
- A node holding a retained verified candidate is `awaiting_review`, and a
  graph whose remaining nodes are all done or awaiting review reduces to the
  `awaiting_review` outcome. The turn ends with an answer naming the review
  step; retry refuses with a reason that names the candidate; aggregate
  exhaustion does not overwrite the node. The state is additive to graph schema
  1 and to the internal-only `goal.graph.update` event, and the public
  `run.result` outcome enumeration is deliberately unchanged.
- Retained candidate facts become durable before the aggregate budget is
  enforced. A wave that crossed the ceiling previously terminated the graph
  before any candidate was attached, leaving real worktrees on disk that the
  graph could no longer point at. No further child verification runs after the
  ceiling; the recorded worktree identity is what makes review possible.
- Automatic writers no longer hold `git_commit` or `git_branch`. Rebuilding the
  child registry for a worktree restored every builtin, leaving an explicit
  non-goal enforced by prompt text alone, and a child commit would move the ref
  the candidate's diff is measured against. Both the registry removal and the
  availability check apply, matching the treatment of graph meta-tools.
- An attempt retains at most forty ordinary tool results, never pruning
  verification or node-result evidence, and records how many it dropped. Every
  transition rewrites the complete snapshot, so unbounded evidence made durable
  cost grow with the square of a node's tool calls. The full transcript remains
  in the durable session log.
- Mid-graph user steering is retained across the accepted-node handoff, which
  otherwise replaced the whole active context including guidance the user was
  told applies to the remaining task.
- `Overlap` and `Violations` now err in opposite directions on purpose:
  `Overlap` folds case because over-serializing only costs parallelism, while
  `Violations` is case-exact because folding `src/` into `SRC/` would accept an
  undeclared write on a case-sensitive filesystem. `internal/writescope` has
  direct tests for the first time.

OG-3B1 retained-worktree accountability closure:

- Every directory the runtime causes Git to create is now attributable to a
  plan node and attempt, which is one half of OG-3B's exit gate. Three ways of
  ending a wave previously failed that: a cancelled wave, a wave whose usage
  accounting errored, and a process boundary mid-flight.
- A cancelled wave records what its writers left on disk. The cancellation
  outcome is written first so the graph states the real reason, and the
  retained facts follow. `FinishWriter` therefore treats a `cancelled` graph
  the same way OG-3A.8 made it treat an exhausted budget: identity is still
  recorded, and no scheduling state changes. It accepts only the attempt
  states a terminal transition itself produced, so a late result can add
  identity but never revive a finished wave.
- Worktree identity is recorded before usage accounting rather than after. A
  graph that cannot say where a retained tree is cannot honour its promise to
  retain it, and that must not depend on whether a child's counters were well
  formed.
- An isolated worktree is bound to its attempt durably at creation, before the
  child can change anything in it, through additive optional attempt
  `worktree`/`branch` fields. Recovery after a process boundary now names the
  exact orphaned path and branch instead of describing something to go find,
  which is what makes the interrupted-writer blocker actionable. A writer that
  changed nothing has its tree removed and the record cleared with it. This is
  the association half of the orphan problem; deciding what to do with an
  orphaned tree is still OG-5 reconciliation work.
- `/orchestrate status` lists every retained tree for the whole graph, on live
  and saved graphs alike, marking one whose contents the runtime never examined
  as unreconciled rather than omitting it. Review previously required guessing
  node identifiers. The model-facing render is deliberately unchanged: the
  model has no selection or integration authority, so naming candidates to it
  would only invite work it is not permitted to do.
- The isolated-writer wave has adversarial application-level coverage for the
  first time: cancellation, delegate-permission refusal before any dispatch,
  child verification failure, an out-of-scope write, and a provider failure in
  one of two concurrent writers that must not discard the verified sibling.

OG-3B2 verification-composition correction:

- A real session (`kanban9`) built a FastAPI/SQLite backend, wrote eleven
  passing tests, ran them, and blocked four iterations later reporting that
  "potentially mutating work has no successful recognized verification". The
  suite had passed, against an unchanged workspace token, and was recorded only
  as an ordinary tool result.
- The command was
  `export UV_CACHE_DIR="$(pwd)/.uv-cache" && uv run pytest -q`. The repository's
  own `AGENT.md` told the model to redirect package caches into the project
  folder because the sandbox denies writes elsewhere, so following the project's
  instructions is what made every verification command ineligible.
- The gate protects one property: an observed zero status must prove the
  verifier ran and exited zero. `A && B` satisfies it — the shell reports B's
  status when A succeeded and A's non-zero status otherwise — whatever A is.
  The rule that rejected this rejected `A && verifier` for the same reason it
  rejects `verifier && echo ok`, which was a real conflation. Recognition now
  splits on `&&`, requires the final segment to be a recognized verifier, and
  refuses everything that can decouple status from it: `||`, `;`, pipelines,
  backgrounding, redirection, and a verifier that is not last. The special-case
  workspace `cd` wrapper is subsumed.
- A leading segment that relocates the verifier is still refused, for a
  different reason that is now stated separately: the result would describe a
  tree other than the one whose state token the evidence is bound to. A final
  segment assembled by command substitution is refused too, because the
  recognizer classifies commands by their literal words.
- The refusal notice now searches the whole command rather than its leading
  segment, so a trailing verifier is named. In the failing session the model
  received no notice at all — the leading segment was `export …` — and had no
  way to learn which part of a command it had watched pass was unacceptable.
- A node that exhausts its remediation window after refused checks now names
  them in the blocker and gives the direct command to run. The operator
  previously read that no verification existed directly beneath a passing test
  suite they had just watched run, with nothing to act on.

OG-3B3 budget-accounting correction:

- A real session (`kanban10`) finished node 1, was midway through node 2, and
  died on the aggregate token ceiling: 998,806 of 1,000,000. Nothing else was
  close — estimated cost was $1.50 of $5, iterations 49 of 96, active wall 11
  minutes of 30. Only the token bound fired, and it fired on the wrong number.
- Of 937,617 input tokens, 681,717 were served from the provider cache. An
  agentic node resends its whole active prompt on every iteration, so charging
  cache reads at full weight makes the ceiling a function of context length
  times iteration count: it grows with the square of a node's tool calls while
  the new content grows linearly. The envelope now charges
  `input − cached + output`. The same session bills 317,089 — 32% of the
  envelope — and would have continued. A provider that reports no cache
  counters charges everything, exactly as before.
- The same session compacted six times in its final two minutes. Compaction
  under budget pressure triggers at a fraction of the *remaining* allowance, so
  as the allowance shrinks the threshold falls, and each summary is itself an
  accounted provider request: a spiral that spends the remaining allowance on
  summaries of a context already reduced to a summary. Compaction now records
  the context size it achieved and requires real growth beyond it before paying
  for another, in both modes.
- Exhaustion previously cost the whole graph. Accepted nodes and retained
  candidates survived in the snapshot, but there was no way to continue, so the
  only path was a new goal that redid the completed work — which is what made
  a ceiling that fired early expensive rather than merely conservative.
  `/orchestrate extend` grants one more fixed envelope, at most twice, and the
  exhaustion message names it.
- The grant is a user decision. The model, repository text, hooks, and skills
  cannot widen the ceiling by any route. It is deliberately not a resume: every
  attempt the exhaustion ended stays immutable and terminal, each unfinished
  node starts a new attempt, and a node that already spent its attempt bound
  stays blocked rather than being made ready by adding tokens.

OG-3B4 user-owned execution envelope:

- The four whole-graph bounds are now configuration with the previous fixed
  values as defaults: `options.orchestration_max_iterations` (96),
  `orchestration_max_tokens` (1,000,000), `orchestration_max_cost_usd` (5), and
  `orchestration_max_active_wall_seconds` (1800). A zero or omitted field keeps
  its default. Validation refuses only implausible values — 10,000 iterations,
  100,000,000 tokens, $1,000, 24 hours — because those are integrity bounds,
  not policy.
- `/orchestrate extend` is no longer capped at two grants. A person deciding
  to continue is the whole mechanism; a fixed number of decisions they are
  allowed to make is arbitrary. Each grant adds one envelope of the size the
  graph was configured with, recorded in the snapshot so a session configured
  for more work extends by that larger amount rather than by a build constant.
- Snapshot validation changed accordingly. It previously rejected any stored
  limit wider than the build default, which would now reject a legitimately
  configured graph. It rejects implausible values instead, and separately
  checks that the recorded single-envelope grant is consistent with the stored
  maximum, so a hand-edited snapshot still cannot manufacture allowance.
- This supersedes OG-3A.1's "configuration cannot widen the hard maximum" for
  the user's own configuration only. The reasoning is in the authority model
  above: a resource bound is not a safety property, and making one terminal for
  the graph rather than for the turn cost the user completed work every time a
  ceiling turned out to be miscalibrated. Repository text, skills, hooks, and
  the model still cannot widen the envelope, and permission, verification,
  scope, and publication gates are untouched by it.

OG-3B5 retained-worktree reconciliation:

- OG-3B1 made every retained directory attributable to a node and attempt, and
  then never looked at one again. Naming a path is not knowing whether it is
  still there. Isolated writer worktrees live under the system temp directory,
  so between one session and the next the operating system may have swept one,
  a person may have removed one by hand, or it may be sitting there full of
  unreviewed changes — and `/orchestrate status` printed the remembered path
  identically in all three cases. Every decision a person can make about a
  candidate depends on which case it is in.
- `/orchestrate reconcile` observes each retained tree and records a typed
  disposition durably on the attempt: `present` (registered with Git and still
  holding changes), `empty` (registered and intact but holding none),
  `missing` (the directory is gone), `orphaned` (the directory exists but Git
  no longer registers it), `base_unreachable` (intact, but the commit its
  claim recorded is no longer in the parent, so it cannot be diffed against
  it), and `discarded`. An absent disposition means never observed, which is
  the honest state for every graph written before this slice.
- Observation is read-only and lives in the application layer: it removes
  nothing, reuses nothing, and reopens no attempt, and the graph owns only the
  durable record of the answer. Path comparison resolves symlinks, without
  which every tree on macOS reports orphaned — the system temp directory is
  itself a symlink, so Git's spelling of the path and the graph's never match.
- `/orchestrate discard <node-id>` removes a tree a person no longer wants. It
  is not a tool, so the model cannot call it; it is not covered by an autonomy
  mode, because what it deletes is unreviewed work only a person can judge
  worthless. It refuses a tree nobody has reconciled, so the decision is made
  against contents rather than a path; a tree still holding changes needs the
  request repeated with `confirm` after the changed-file count has been shown;
  and it refuses an `orphaned` directory outright, because removing a path Git
  does not own means recursively deleting a location read out of a durable
  record. Removal is recorded in the audit ledger, and the node and attempt
  keep the identity of what was removed.
- Archiving a terminal graph is refused while it still points at a tree nobody
  has observed. Archiving stops the session advertising the graph, and the
  graph is the only thing that knows the directory exists, so releasing it
  first would recreate precisely the orphan OG-3B1 removed. The gate asks for
  the observation, not for the tree to be gone: a reconciled tree full of
  changes archives fine.
- Reuse remains out of scope. Selecting, integrating, or resuming work from a
  retained candidate is OG-4, and the exact scheduler-state recovery this
  reconciliation feeds is OG-5. This slice decides only what is *there*.

OG-3B6 adversarial campaign:

- The campaign is complete. Every dispatch and acceptance gate on the writer
  path now has an application-level test that proves it fails closed, which is
  the remaining half of OG-3B's exit gate.
- Hook refusal of a wave is covered through a real configured hook rather than
  a stub, because the gate it exercises is a user's own script. A refusal never
  becomes an automatic in-graph retry — `FailureHook` blocks immediately — but
  an explicit user retry is permitted and should be: the hook is external
  policy the person may have just changed, and a new attempt consults it again
  rather than assuming either answer. That is precisely what distinguishes a
  hook refusal from an interrupted action, which stays unsafe however often it
  is asked.
- Parent drift between the durable claim and dispatch refuses the whole wave
  before any worktree exists. The claim records the exact parent state writers
  may branch from, so a moved parent would otherwise produce candidates built
  on a base that no longer describes the workspace.
- Verification spanning a changing tree is rejected. Evidence is bound to one
  state of one tree, and the team layer discards results bound to a superseded
  state, so a suite whose commands passed against different states cannot
  aggregate to `passed`. The graph's own mixed-token check behind it is
  defence in depth rather than the only barrier.
- Scopes differing only in case are serialized, exercising the deliberate
  asymmetry OG-3A.8 introduced: `Overlap` folds case because over-detecting a
  collision only costs parallelism, while missing one would let two writers
  claim the same directory from one commit.
- Every writer in a wave failing ends the graph honestly, blocks both nodes,
  and leaves the parent workspace untouched.
- **The earlier contract item "complete the provider-failure campaign beyond
  the single-wave case" was not implementable as written and has been
  withdrawn.** `defaultMaxWriterStarts` and `defaultMaxWriterConcurrency` are
  both 2, so two starts *is* one wave and a second wave cannot occur. What the
  item can usefully mean was covered instead: every writer failing rather than
  one, and the fate of a writer node beyond the starts bound. That node is
  never silently finished, and once an explicit retry reopens the graph the
  spent starts budget stops it as `budget_exhausted` rather than as a failure —
  the same speed-bump treatment every other resource bound receives under
  OG-3B4, and therefore extendable rather than fatal.

OG-3C writer-wave product evaluation and OG-3 sign-off:

- The evaluation matrix had no isolated-writer case at all. Every OG-3
  guarantee was proven by focused agent, graph, and application tests, while
  the matrix that covers OG-1 and OG-2 stopped before the writer wave existed.
  Signing off OG-3 on that basis would have made it the first milestone
  accepted to a lower standard than its predecessors, and the handoff protocol
  below explicitly forbids marking a milestone complete from code inspection
  alone.
- `internal/eval/orchestrated_writer_test.go` adds three cases on a full
  `app.New` runtime with a Git fixture whose tests pass at the base commit and
  whose packages are genuinely disjoint. The child verification is the
  application's real one, so each candidate's evidence is a detected `go`
  command actually executed inside that worktree and bound to its state token —
  the focused tests stub that step, which is precisely why the evaluation is
  worth having.
- Each case compares the parent repository before and after by HEAD, porcelain
  status including untracked files, and the full file list. That comparison is
  itself verified to notice both a new untracked file and edited tracked
  content, so the central exit-gate assertion cannot pass vacuously.

Exit gate — **met on 2026-08-03**:

- *adversarial overlap, scope, drift, cancellation, and provider-failure tests
  produce no parent mutation before reviewed integration* — overlap and
  case-folded scopes, out-of-scope writes, post-claim parent drift,
  verification spanning a changing tree, cancellation, hook refusal, delegate
  denial, one writer failing, and every writer failing are covered across
  `internal/goalgraph/graph_test.go`, `internal/agent/goalwrite_test.go`, and
  the three product evaluations, which assert parent immutability directly.
- *no two writers can overwrite each other or the parent* — a wave admits only
  pairwise-disjoint scopes from one clean commit, scopes differing only in case
  are serialized, observed changed files are validated against the declared
  scope, and writers never hold the parent workspace.
- *every candidate remains attributable to its plan node and attempt* —
  identity is recorded durably at creation before the child runs, retained
  across cancellation, accounting failure, and process boundary, surfaced for
  the whole graph live or saved, and OG-3B5 adds the observed disposition of
  each tree so attribution survives the directory itself.

Proved by: `go build ./...`, `gofmt -l`, `go test -count=1 ./...`, `go test
-race -count=1 ./internal/agent/ ./internal/goalgraph/ ./internal/app/
./internal/tui/ ./internal/config/`, and `go test -count=1 -timeout 600s -run
Orchestrated ./internal/eval/`.

**OG-3 is complete.** What it does not include remains explicit: no candidate
is selected, integrated, or reused, and no multi-worker scheduler order is
reproduced exactly after restart. Those are OG-4 and OG-5.

### OG-4 — Reviewed integration and combined verification

**Status: in progress. OG-3 met its exit gate on 2026-08-03; OG-4A closed the
unaccounted publication path, OG-4B added the recoverable pre-integration
checkpoint, and OG-4C published the first graph candidate into the parent.**

OG-4A no unaccounted publication of a graph candidate:

- A graph writer is an ordinary delegate to the Team, and the delegate
  integration path had no idea the graph existed. `/agents apply` on a
  retained candidate published its hunks into the parent workspace and
  succeeded. The node stayed `awaiting_review` still reporting that reviewed
  integration was required, the graph's recorded evidence and workspace token
  described a parent that no longer existed, and no combined-workspace
  verification ran at all. This was reproduced against a real graph before it
  was fixed, not reasoned about.
- It also contradicted what the product claimed. The capability matrix and
  every user-facing document said no candidate is selected or integrated,
  which was true of what the runtime does automatically and false of what a
  user could reach in two commands.
- A candidate is now marked graph-owned from dispatch, through the durable
  delegate record, and across a resumed session. Publication is refused at
  `prepareDelegateIntegrationMutations`, the single funnel every apply path
  runs through — operator, primary-agent reviewed, and model tool — so no
  surface is left open. The primary agent's route matters as much as the
  human's: it holds no authority to publish a node's candidate either.
- Reviewing is deliberately still allowed. The retained worktree exists to be
  looked at, and OG-3B5's whole argument was that an operator decides against
  contents rather than against a path. The TUI refuses before opening the
  selection UI rather than after the user has chosen hunks.
- This restores the documented boundary rather than removing a capability;
  publishing a candidate with combined-workspace verification is the rest of
  OG-4.

OG-4B recoverable pre-integration checkpoint:

- Publication writes candidate bytes into the user's own workspace, and the
  only record of what those bytes replaced lived in memory. The in-process
  rollback is sound while the process exists; a process that stopped between
  the first file and the last left a workspace that was neither the parent it
  had been nor the candidate it was becoming, with nothing on disk able to say
  which files had already moved or what they had held. `/restore`'s change
  tracking is in-memory and does not survive a resume, so it could not answer
  either.
- A durable checkpoint is now appended and flushed *before* the first byte
  moves, recording each target path's prior content, mode, and whether it
  existed at all — an absent file and an empty one restore differently. The
  outcome is appended afterwards as `applied`, or as `reverted` when the
  in-process rollback ran. The evidence of an interruption is therefore the
  absence of a recorded outcome rather than an inference from file contents,
  which is the same write-ahead shape the graph already uses for ambiguous
  mutations.
- Recovery reports an unresolved checkpoint at startup and through
  `/restore integration`, and never resolves it automatically. It restores the
  recorded prior state on explicit request and deliberately cannot re-publish:
  completing a half-finished integration would repeat a mutation whose effect
  is unknown, which is the replay this program refuses everywhere else.
- Retained content is bounded per file and per checkpoint. Past the bound the
  path is still named and marked unrestorable rather than being dropped or the
  integration refused: telling a person exactly which file changed and that it
  cannot be put back automatically is more useful than either alternative.
- This lands on the delegate publication path that already writes to the parent
  today, so it is proven against a real mutation rather than built ahead of one.
  Graph-owned publication, which OG-4A closed, will reuse it.

OG-4C graph-owned candidate integration:

- The first time the runtime writes a candidate into the user's own workspace.
  It is reachable only by a person through `/orchestrate integrate <node-id>`:
  it is not a tool, so the model cannot call it, and no autonomy mode reaches
  it, because this is the point at which unreviewed work becomes the user's
  files.
- A candidate is published whole or not at all. The child's verification passed
  against its entire tree, so publishing selected hunks would put bytes into
  the parent that no verification ever covered. A conflict in any file refuses
  the whole integration for the same reason: applying the part that still fits
  would produce a combined workspace that is neither what the child verified
  nor what the user had.
- The node moves to a new `integrated` state and the graph to a new
  `awaiting_verification` outcome. It deliberately does not become `done`: the
  child's pass says nothing about the parent it has just been merged into, and
  treating a child pass as a combined pass is exactly what OG-4's exit gate
  forbids. Nothing in the runtime can mark an integrated node done — the
  acceptance path for it is OG-4D.
- Publication advances the mutation generation and stales every previously
  accepted node, because the workspace those nodes were judged against no
  longer exists. This is the same treatment an external edit receives.
- It runs under OG-4B's checkpoint and ordinary integration permission, and the
  node's reason names the checkpoint that can undo it. A failure at any point
  leaves the graph untouched, so the node stays awaiting review rather than
  claiming a state the workspace is not in.
- `integrated` and `awaiting_verification` are additive within graph schema 1.

- Add explicit combined-parent verification to the graph acceptance path.
- Add a recoverable pre-integration checkpoint and safe post-failure
  reconciliation.
- Allow the coordinator to recommend among deterministically eligible
  candidates with a visible rationale.
- Apply only freshness-bound selected hunks through ordinary integration
  permission.
- Re-verify the combined workspace and invalidate all earlier affected
  evidence.
- Require a user-authored waiver when meaningful automated verification does
  not exist.

Exit gate:

- stale or conflicting bytes never publish;
- a child pass never substitutes for combined-parent verification;
- integration denial or failure leaves a recoverable and inspectable state;
- terminal `done` is tied to fresh combined-workspace evidence.

### OG-5 — Reproducible graph recovery and graduation decision

**Status: blocked on OG-4.**

- Extend OG-1's exact primary-graph restoration to multi-worker scheduler
  order, claims, and aggregate bounds.
- Restart only safe pending read-only work.
- Reconcile interrupted writer and integration states without replay.
- Complete security, reliability, compatibility, and performance campaigns.
- Compare Standard and Orchestrated modes on the evaluation matrix below.
- Decide whether to graduate, revise, or retain the mode as experimental.

Graduation does not imply universal automatic use. Even a graduated graph
engine should be selected only for work that benefits from decomposition.

## Evaluation and graduation

OG-1 establishes the internal primary-only baseline: real product evaluations
now cover dependency-ready success with a repository repair and fresh tests,
recoverable tool failure in a new attempt, permission denial, and no delegated
events. Focused state/app coverage adds provider retry, cancellation, budget
exhaustion, stale workspace state, graph revision, durable restart, corrupt
snapshot rejection, and ambiguous-mutation non-replay. OG-2B1 adds the
first user-facing multi-actor path: at most two runtime-selected read-only
workers can run before the serial primary lane. It proves authority,
freshness, cancellation, and result-ingestion semantics. OG-2B2a adds
cooperative pause/resume and safe blocked-node retry without weakening the
non-replay guarantee. OG-2B2b1 makes proposal, primary, and worker model work
durably visible. OG-2B2b2 bounds that aggregate and establishes the narrow
comparative case for substantive independent reads while preserving Standard
as the default and serializing unsuitable work. OG-3C adds the isolated-writer
wave's own product evaluations, which had been the gap: every OG-3 guarantee
was proven by focused tests while the evaluation matrix stopped at OG-2. Three
cases now drive a complete runtime — real delegate permission, real Git
worktrees, and the application's own child verification running the
repository's actual `go test` inside each candidate tree, rather than the
stubbed verifier the focused tests use. They cover a verified two-writer
disjoint wave reaching `awaiting_review`, a wave in which one writer fails
while its sibling's verified candidate survives, and the operator's whole
recovery path from retained tree through reconcile, refused discard, confirmed
discard, and release. Each asserts the parent repository is byte-for-byte
unchanged, comparing HEAD, porcelain status, and the full untracked file list
before and after.

Compare Standard and Orchestrated modes on:

- cross-layer feature implementation;
- ambiguous bug diagnosis with competing hypotheses;
- large migrations;
- independent research followed by implementation;
- same-file work that should remain serial;
- recoverable provider and tool failures;
- verification failure and repair;
- permission denial;
- parent and child drift;
- cancellation in queue, provider, approval, verification, and integration;
- crash and resume;
- token, cost, iteration, and wall-clock exhaustion.

Measure:

- task acceptance and repository test pass rate;
- false `done`, false `blocked`, and unnecessary replan rate;
- elapsed time and critical-path utilization;
- total and per-node input/output tokens and estimated cost;
- user interventions and permission decisions;
- stale invalidations and repeated work;
- conflicts, scope violations, and rejected candidates;
- duplicate or post-cancellation actions, which must remain zero.

The mode remains experimental until all of these hold:

- no silent overwrite or duplicated mutation in the adversarial corpus;
- no `done` with an open, stale, or unverified required node;
- permission decisions are equivalent to the same actions in Standard mode;
- resume is mutation-safe;
- decomposable tasks show a meaningful quality or elapsed-time improvement;
- the improvement justifies visible token/cost overhead;
- replan, serialization, invalidation, and blocking explanations are useful to
  an operator;
- the Phase 8 security and cancellation campaigns pass.

Do not make it default merely because demonstrations look impressive.

## Explicit non-goals for the initial program

- Arbitrary model-generated orchestration code.
- Peer-to-peer worker messaging or a shared agent mailbox.
- Recursive or nested delegation.
- Self-claiming write agents in a shared checkout.
- Automatic reconciliation of overlapping changes.
- Automatic selection among competing write candidates.
- Automatic commit, merge, push, pull request, publish, deploy, release, issue
  change, or external message.
- Repository-controlled opt-in.
- Persistent teams across unrelated goals.
- Treating agent count as a success metric.

These can be reconsidered only after the typed graph proves a concrete need;
none is required for a capable Orchestrated Goal mode.

## Cross-session handoff protocol

Every agent or contributor continuing this program must:

1. Read the Phase 6 and Recommended next sequence sections of `ROADMAP.md` and
   this entire document before changing orchestration behavior.
2. Treat the milestone status in this document and the roadmap checkboxes as
   the source of truth, not a prior chat transcript or a model's summary.
3. Inspect the current implementation and tests before assuming a prerequisite
   is present. The capability matrix describes shipped behavior; this document
   describes intended behavior.
4. Work on the earliest unblocked milestone unless the user explicitly changes
   priority.
5. Preserve the authority model, non-goals, and exit gate. If a proposed change
   crosses one, update the strategy and record the decision before coding.
6. On every completed slice, update together:
   - milestone status and evidence in this document;
   - Phase 6 and Recommended next sequence in `ROADMAP.md`;
   - `docs/ROADMAP_HISTORY.md` with what shipped and why;
   - `docs/CAPABILITIES.md`, `docs/BETA.md`, security/automation/user guidance,
     and schemas when observable behavior changes;
   - the offline evaluation matrix and compatibility policy when state or
     event contracts change.
7. Never mark a milestone complete from code inspection alone. Record the
   commands and evaluations that proved its exit gate.

### Current handoff

- Last completed slice: **OG-4C — Graph-owned candidate integration**,
  following OG-4B's recoverable pre-integration checkpoint, OG-4A's closure of
  the unaccounted publication path, OG-3C's
  writer-wave product evaluation and OG-3 sign-off, OG-3B6's adversarial
  campaign, OG-3B5's
  retained-worktree reconciliation, OG-3B1–B4's retained-worktree
  accountability closure, verification-composition correction,
  budget-accounting correction, and user-owned execution envelope, OG-3A's
  verified isolated-writer candidate wave, and its eight trial- and
  audit-driven corrections.
- Active milestone: **OG-4 — Reviewed integration and combined verification.**
  OG-3 met its exit gate on 2026-08-03 and is complete; OG-4A and OG-4B have
  shipped.
- Next unblocked slice: **OG-4D — combined-workspace verification and node
  acceptance.** An integrated node currently has no path to `done` at all,
  which is the correct fail-closed state but not the finished one: the graph
  needs to run the repository's verification against the combined parent,
  accept the node only on a fresh pass bound to the current workspace token,
  and represent a user-authored waiver where no meaningful automated check
  exists. Deterministic eligibility among competing candidates with a visible
  rationale follows.
- Active implementation branch: **`wave36`. OG-2B2b2 is `e3b1dd9`, OG-3A.4 is
  `57c1c26`, OG-3A.6–7 are `364d845`, OG-3A.8 is `350178c`, OG-3B1–B4 are
  `fd1c2bc`, OG-3B5 is `205bff2`, OG-3B6 is `d88ac11`, and OG-3C, OG-4A, plus
  OG-4B,
  plus OG-4C are the working tree on top of them.**
- Shipped experimental mode: **TUI-only, explicit per-session Orchestrated
  Goal with one serial primary lane, at most two automatic read-only workers
  for independently ready approved nodes, and one bounded verified retained-
  candidate wave for pairwise-disjoint isolated writers ending in
  `awaiting_review`, plus cooperative pause/resume, safe retry of eligible
  blocked nodes, durable aggregate accounting, bounded retained evidence,
  attributable retained worktrees across every way a wave can end, explicit
  observation and user-decided disposal of those worktrees, and a
  user-configurable whole-graph execution envelope a person can extend**.
- Current default behavior: Standard model-directed execution with
  evidence-gated goal completion.
- Preserved implementation constraint: only approved `read_only` and narrowly
  scoped `isolated_write` nodes may be automatically delegated; writers never
  touch the parent workspace, no candidate is selected or integrated, primary
  parent writes stay serial, workers cannot recurse or control the graph, and
  a saved graph remains inert until explicit resume.
- Parallel program requirement: continue the remaining Phase 8 security and
  reliability campaigns alongside every orchestration wave.

## Decision log

### 2026-08-01

- Proceed with plan-graph execution as a staged experimental program.
- Use **Orchestrated Goal** as the working product name.
- Keep Standard mode as the default.
- Require explicit per-session user opt-in initially.
- Keep `/plan` read-only and separate from orchestration.
- Separate logical plan intent from runtime execution truth.
- Make readiness, attempts, evidence, staleness, budgets, recovery, and
  structural completion runtime-owned.
- Prove primary-only scheduling and replanning before automatic fan-out.
- Add read-only fan-out before delegated writers.
- Require isolated writers, declared scopes, child verification, guarded
  integration, and fresh combined-parent verification.
- Keep star-topology workers and prohibit recursive delegation.
- Do not run arbitrary model-generated orchestration code in the initial
  program.
- Do not add authority or external publication side effects to graph mode.
- Persist graph schema 1 inside additive durable-session `goal_graph` records;
  reject unsupported or structurally false snapshots before scheduling.
- Emit bounded `goal.graph.update` lifecycle events under the internal-only
  schema-v1 compatibility decision documented above.
- Use `propose_goal_graph_revision` and `block_goal_node` as the only model
  graph-control tools; both are scheduling-only meta tools.
- Use one conservative whole-Git-workspace token for OG-1 rather than claiming
  precise read footprints. Treat a no-op structured write as unproven work.
- End a required graph on the first material node blocker rather than spending
  more authority on independent nodes that cannot make the goal complete.
- Resolve the first operator surface as a TUI-only `/orchestrate` command
  family. Require a newly generated pending plan with concrete acceptance
  criteria, a separate explicit approval, and explicit resume of inert saved
  state. Defer headless activation and automatic actors.

### 2026-08-02

- Use **evidence-gated durable execution** as the architectural description of
  Orchestrated Goal, not as a separate mode name or an unsupported marketing
  claim. The model proposes intent and interpretation; the runtime owns
  operational truth and accepts completion only through the applicable typed,
  fresh evidence gate.
- State the shipped recovery guarantees separately from the OG-2B2 through
  OG-5 target. Current resume can recompute interrupted replay-safe reads in a
  fresh attempt and will not replay an ambiguous mutation; exact multi-worker
  scheduler recovery, isolated automatic writers, and guarded integration are
  not current capabilities.
- Split OG-2B into a fan-out kernel (OG-2B1) and operator/comparative work
  (OG-2B2), so concurrency safety can ship without claiming unmeasured product
  benefit or incomplete aggregate presentation.
- Make `execution` explicit logical intent: omitted/`primary` is serial;
  `read_only` is eligible only after the graph has been explicitly approved.
- Reuse the governed read delegate boundary and the session-wide scheduler;
  do not create a second child runtime or permission path for orchestration.
- Persist a fixed experimental read envelope of two concurrent workers, eight
  starts, 64,000 tokens, and fifteen minutes total wall time. Separately cap
  each read at five minutes, two attempts per node, and eight child iterations.
  Tighter provider/profile/scheduler limits still win.
- Require a non-empty bounded summary, recorded successful tool evidence, and
  a matching Git workspace token when a base token exists before a delegated
  read node can become done.
- Finish a complete read wave before advancing the serial primary lane. Keep
  manual model-directed delegation hidden during an approved graph.
- Treat graph meta-tools as parent-only authority even when a child fabricates
  a hidden call by name.
- Retain operator controls, complete primary-plus-worker aggregate
  presentation, comparative usefulness measurements, and the event/headless
  decision for OG-2B2.
- Implement pause as a cooperative scheduling boundary, not an OS-process
  suspension: durably request it, allow the current iteration to finish, then
  start no new graph work. Keep whole-graph cancellation as immediate stop.
- Resume an in-process pause by clearing only pause state. Keep restored graph
  state inert until the user explicitly resumes it, after which ordinary
  conservative recovery rules still apply.
- Permit retry only for a blocked node with remaining attempt budget and no
  unresolved non-replayable action or ambiguous interrupted mutation. Preserve
  prior attempts and evidence rather than rewriting history.
- Do not add node cancellation while every node is required; it would be only
  an alias for whole-graph cancellation. Revisit it with optional branch
  semantics.
- Keep additive schema-1 pause fields and `pause_requested`, `paused`,
  `resumed`, and `retry_requested` lifecycle states under the existing
  internal-only event decision. Split remaining OG-2B2 work into OG-2B2b.
- Split OG-2B2b into measurement infrastructure (OG-2B2b1) and the bounds plus
  comparative decision (OG-2B2b2), so instrumentation cannot be mistaken for
  evidence that fan-out improves the product.
- Count the explicit proposal in the primary lane; count every completed
  primary/worker provider request as an iteration, including failures that
  report no tokens. Persist input/output tokens and cost only as reported.
- Treat aggregate cost as available only when every token-bearing contribution
  had configured pricing. Never render missing price evidence as zero cost.
- Preserve accounting additively in graph schema 1. Legacy restore may rebuild
  stored attempt usage but must not invent proposal work or iteration history
  that older snapshots never recorded.
- Initially store fixed whole-graph ceilings of 96 provider iterations,
  192,000 tokens, $5 estimated cost when pricing is complete, and 30 minutes
  active execution. After the first realistic eleven-node trial demonstrated
  that repeated full-context input exhausted the token ceiling before one node
  completed, recalibrate newly approved graphs to 1,000,000 tokens while
  preserving older graphs' stored 192,000-token ceiling. Project/config/model
  content cannot widen the current maximum; tighter existing limits win.
  Superseded by OG-3B4 for the user's own configuration: `options.orchestration_*`
  sets the envelope, repository text/skills/hooks/model still cannot, and a
  restored graph keeps the envelope it was created with.
- Compact once after approval and later when the current prompt reaches one-
  eighth of the remaining aggregate allowance. Count every summary request in
  the primary lane; compaction changes future context, not past usage.
- Count active time only while approved execution is attached and runnable.
  Stop it at a reached pause, terminal state, or process boundary, and restart
  it only through explicit resume. Do not count user review or downtime.
- Retain two-worker fan-out for independently ready, substantive reads because
  equal-grounding decomposable and cross-layer comparisons reduced controlled
  elapsed time. Do not generalize this result to trivial, serial, cheaper, or
  lower-token work; the evaluation records visible overhead.
- Keep `goal.graph.update` unchanged and internal-only. Aggregate snapshot/TUI
  presentation has no headless event consumer yet, so additive event fields
  would create compatibility surface without product value.
- Split OG-3 into OG-3A's first bounded candidate wave and OG-3B's adversarial
  and recovery closure. Shipping the worktree path is not enough evidence to
  declare the writer milestone complete.
- Require automatic writers to start from one clean, twice-observed stable Git
  state and exact commit. Do not copy uncommitted parent bytes into a child or
  let a later `HEAD` silently change the durable claim's base.
- Treat `isolated_write` scopes as approved logical intent, not permission.
  Require a narrow explicit scope, select pairwise-disjoint ready writers in
  stable order, then obtain the ordinary delegate write decision and hooks.
- Reuse the reviewed-delegate detected verification suite for automatic
  candidates. Each child command keeps its ordinary `run_command` decision;
  a passing child proves only that retained worktree state and never grants
  integration.
- End OG-3A at a review-required blocked node with the candidate retained.
  Do not unlock dependents, choose a winner, apply a hunk, commit, merge, push,
  or publish. Those authority and combined-evidence transitions belong to
  OG-4.
- Treat an interrupted isolated writer as an ambiguous mutation even though
  the parent is unchanged: block and preserve history rather than starting a
  second writer that could duplicate work or abandon an unknown worktree.

### 2026-08-03

- Treat recognized-verification breadth as a safety property of the mode, not a
  convenience. Because a mutating node blocks without recognized verification
  and no waiver exists before OG-4, a missing ecosystem means the mode cannot
  finish honest work there. Unwrap environment managers recursively rather than
  enumerating every wrapper-and-verifier pair, and require that what a wrapper
  wraps is itself recognized.
- Remove `git diff --check` from recognized verification. Evidence that passes
  on nearly any tree is not evidence about the change that was just made.
- Make acceptance-gate state typed rather than rendered prose, in both the
  completion gap and read-node groundedness. Presentation may be derived from
  state; state may never be recovered from presentation.
- Add `awaiting_review` as a node state and graph outcome so a verified
  candidate wave is distinguishable from a failure. Keep it additive to graph
  schema 1 and to the internal-only `goal.graph.update` event, and keep the
  public `run.result` outcome enumeration unchanged until a headless activation
  surface exists.
- Record retained candidate facts before enforcing the aggregate budget. A
  ceiling reached after the work happened must not erase the runtime's only
  pointer to worktrees that exist on disk.
- Withhold `git_commit` and `git_branch` from automatically dispatched graph
  writers structurally, in the registry and again at availability. A non-goal
  enforced only by prompt text is not enforced.
- Bound retained per-attempt tool evidence, since every transition rewrites the
  complete snapshot durably. Never prune verification or node-result evidence,
  and record the number dropped rather than silently shrinking the record.
- Retain mid-graph user steering across the accepted-node handoff. A context
  optimization must not discard an instruction the user was told applies to the
  remaining task.
- Let `Overlap` fold case and `Violations` match case exactly. The two
  comparisons have opposite safe directions, so they should not share one rule.
- State the read fan-out comparison as an elapsed-time result only. The harness
  scripts equal answers, so it cannot demonstrate a quality improvement and the
  exit gate should not imply that it did.
- Treat "every candidate remains attributable to its plan node and attempt" as
  a property of every way a wave can end, not only of the successful path. The
  budget correction above fixed one ending; cancellation, a failure while
  recording usage, and a process boundary were three more that dropped the
  runtime's only pointer to a real directory.
- Bind an isolated worktree to its attempt durably at creation, before the
  child runs. Recording identity after the child returns can only describe
  trees whose children returned, which excludes exactly the case — an
  interrupted writer — where the operator most needs the path.
- Keep retained candidates on the operator surface and off the model surface.
  The runtime holds no selection or integration authority in OG-3, so naming
  candidates to the model would advertise work it is not permitted to do, while
  an operator cannot review what the status output will not name.
- Report a worktree the runtime never examined as unreconciled rather than
  omitting it or implying verification. An unexamined tree is precisely the one
  a person has to deal with by hand.
- Judge a composed verification command by whether the shell's status is
  provably the verifier's, not by whether the command is a single word. An
  `&&` chain ending in the verifier has that property; preparation before a
  check is not evidence tampering, and refusing it made repositories that need
  setup — a sandbox redirecting a package cache, a virtualenv activation —
  unable to verify anything.
- Keep the relocation refusal separate from the masking refusal and say which
  one applies. They protect different things: one that the status is the
  verifier's, the other that the verified tree is the one the evidence is bound
  to.
- When the runtime refuses evidence, name the direct command wherever the
  verifier sits in what was refused, and repeat it in the blocker if the node
  dies. A gate that silently declines something the user watched pass is
  indistinguishable from a bug.
- Charge the aggregate token envelope for new work, not for context the graph
  already paid for. Counting cache reads at par measured conversation length
  times iteration count rather than work done, and made the token bound fire
  while cost, iterations, and wall clock all had ample headroom.
- Require compaction to reclaim more than it costs. A threshold expressed as a
  fraction of a shrinking allowance falls as the allowance falls, so under
  pressure it re-summarizes a context already reduced to a summary and spends
  the remainder on doing so.
- Let a person grant an exhausted graph another bounded envelope, and say so
  where the exhaustion is reported. Making exhaustion terminal for the graph
  rather than for the turn meant a conservative ceiling cost every accepted
  node, which is the opposite of conservative.
- Treat every resource bound as a speed bump that requires human interaction,
  not as a wall that ends the job. Exceeding one means the work is bigger than
  expected, not that it is unsafe, so the correct response is to stop and ask —
  never to discard progress. This is what distinguishes a resource bound from
  the permission, verification, scope, and publication gates, which are safety
  properties and stay closed to the same user tuning.
- Make the envelope user configuration with the current values as defaults, and
  stop capping the number of grants. A fixed allowance of decisions a person is
  permitted to make has no principle behind it once the decision itself is the
  control. Repository text, skills, hooks, and the model still cannot widen it,
  because none of them is the user.
- Separate "where is the retained tree" from "is it still there". The first is
  identity and the runtime can record it; the second is an observation that
  goes stale the moment the session ends, and printing a remembered path as
  though it were current is a claim the runtime has no basis for. Give the
  observation its own typed vocabulary rather than folding it into prose.
- Keep worktree observation read-only and worktree removal explicit and
  user-only. Removal deletes work that was never reviewed, so it is not a tool,
  is not covered by an autonomy mode, and requires that a person has been shown
  what is in the tree first. Automatic cleanup of unreviewed candidates would
  be the same defect as automatic selection, arrived at from the other side.
- Refuse to remove a directory Git no longer registers. Through Git, removal is
  bounded by what Git owns; without Git it is a recursive delete of a path read
  out of a durable record, which is the one case where being wrong is
  unrecoverable and a person should act themselves.
- Refuse to archive a graph that is still the only record of a directory nobody
  has looked at. Archiving is not destructive to the transcript, but it does
  end the session's pointer to a real tree. Require the observation, not the
  removal: a reconciled tree full of changes may be archived.
- Resolve symlinks before comparing worktree paths. The system temp directory
  is itself a symlink on macOS, so Git's spelling and the graph's never match
  literally, and a naive comparison reports every retained tree orphaned —
  the failure mode that would have made the whole surface untrustworthy.
- Withdraw a contract item rather than satisfy it dishonestly. "Beyond the
  single-wave case" could not be implemented while two starts is one wave, and
  writing a test that appeared to cover it would have made the exit gate a
  claim about the fixture rather than about the runtime. State what the bound
  actually is and test what is actually reachable.
- Treat the writer starts bound as budget exhaustion rather than as a failure,
  consistent with every other resource bound. A graph that ran out of writer
  slots has not done anything wrong, so it stops for a decision and stays
  extendable.
- Let an explicit user retry reopen a hook-refused node, while automatic retry
  stays closed. A hook is external policy that a person can change between
  attempts, so re-running consults it again; the runtime is not entitled to
  assume either that the refusal stands or that it has been lifted.
- Hold a milestone's sign-off to the same evidence standard as its
  predecessors. OG-3 had thorough focused tests and no product evaluation,
  which is a real gap rather than a formality: the evaluation runs the
  application's own verifier against a real repository, and that is where a
  stubbed step would have hidden a difference between what the tests assert and
  what the product does.
- Assert parent immutability by comparing the whole observable repository
  state, not by looking for the files a writer happened to create. The gate is
  that *nothing* changed, and a check written around the expected change cannot
  see an unexpected one.
- Refuse publication of a graph-owned candidate at the shared funnel rather
  than at each caller. Operator, primary-agent reviewed, and model-tool apply
  all reach the same preparation step, and a refusal placed at one surface
  would have read as a fix while leaving the others open.
- Refuse publication but keep review. The retained worktree exists to be
  inspected, and OG-3B5's argument was that a person decides against contents
  rather than against a path; a refusal that also hid the diff would have made
  the candidate less reviewable in the name of safety.
- Publish a graph candidate whole or not at all. The unit the child verified
  is its entire tree, so hunk-level selection — correct for an ordinary
  delegate, where the user is the only judge — would publish bytes no
  verification covered. The conservative choice is the simpler one here.
- Give an integrated node its own state rather than reusing done or blocked.
  Done would assert a combined result nothing checked; blocked would report a
  successful publication as a failure.
- Leave an integrated node with no path to done in this slice. A fail-closed
  gap is a better intermediate state than an acceptance path that has nothing
  to verify against, and it makes the next increment's contract obvious.
- Write the integration checkpoint before the first byte and its outcome
  after, so an interruption is evidenced by a missing outcome rather than
  inferred from file contents. A file that matches its recorded prior bytes may
  never have been written, or may have been written and edited back; the
  contents cannot distinguish those and the record can.
- Restore an interrupted integration, never complete it. Finishing a
  half-applied publication would repeat a mutation whose effect is unknown,
  which is the replay refused everywhere else in this program.
- Bound retained prior content and name what was dropped. Refusing the
  integration to protect the record would trade a working capability for a
  guarantee, and silently omitting the file would leave a change nobody knows
  about; saying "this changed and I cannot put it back" is the honest third
  option.
- Land the checkpoint on the publication path that already writes to the
  parent rather than building it ahead of graph integration. Infrastructure
  validated only by the feature that has not shipped yet is a guess.
- Treat a capability the documentation denies as a defect in the code, not in
  the documentation. The matrix said no candidate is integrated, which was true
  of the runtime's automatic behaviour and false of what a user could reach in
  two commands; closing the path restored the claim rather than weakening it.

## Open implementation decisions

These remain unresolved for OG-3 or later:

- whether and how a headless CLI surface should be added;
- the representation of user-authored verification waivers;
- whether the initial guideline limits become user configuration after
  measurement;
- the precise targeted-versus-full combined verification policy by repository
  type;
- whether later built-in reads earn narrower freshness footprints than OG-1's
  conservative whole-workspace token;
- the final event-version decision if real automation users can opt in.

An implementation may resolve these questions, but must record the decision
and its evidence here before declaring the relevant milestone complete.
