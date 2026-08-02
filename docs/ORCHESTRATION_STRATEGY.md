# Orchestrated Goal strategy

**Status:** approved product and architecture strategy; evidence-gated durable
execution is available experimentally through OG-2B2a, and OG-2B2b is next
**Roadmap owner:** Phase 6 — Multi-agent orchestration  
**Last updated:** 2026-08-02
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

The shipped OG-1 through OG-2B2a boundary supports one serial primary lane and
at most two governed automatic read-only workers. It provides durable graph
truth, fresh machine-observed evidence, conservative invalidation, bounded
retry/revision, cooperative pause and resume, safe retry of eligible blocked
nodes, and non-replay of ambiguous mutations. It does **not** yet automatically
dispatch isolated writers, integrate their changes, cancel an optional branch
or node, or reproduce a multi-worker scheduler exactly after restart. Those
are OG-2B2b through OG-5 work and must not be described as current behavior.

## Product decision

Collomia will retain two distinct execution modes:

| Mode | Contract |
| --- | --- |
| Standard | The current model-directed tool loop with evidence-gated completion. It remains the default. |
| Orchestrated Goal — experimental | Evidence-gated durable execution: the model proposes a dependency graph; the runtime owns readiness, attempts, evidence freshness, recovery decisions, and terminal state. Current automatic fan-out is read-only; writers and integration remain staged milestones. |

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

- complete aggregate graph time/token/cost/iteration presentation across the
  primary and automatic workers;
- comparative evidence that bounded read fan-out improves suitable tasks;
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
| User | Decides whether to start orchestration, grants ordinary authority, resolves material ambiguity or conflicts, approves protected operations, and may eventually grant an explicit verification waiver. | A graph approval does not manufacture machine evidence or make stale evidence fresh. |

The model is never authoritative about machine state. It may explain that a
test passed, but only a recorded successful command bound to the current state
is machine evidence. It may propose that a node is complete, but only the
runtime may move an execution node through its acceptance gates.

Graph execution changes scheduling, not authority:

- every tool call passes through the existing registry, permission manager,
  hooks, sandbox, network, audit, timeout, cancellation, and output bounds;
- a denied operation blocks the node or asks the user; the coordinator may not
  delegate the same operation to bypass the denial;
- child profiles can only tighten the parent's effective permissions;
- plan approval, child verification, comparison, or ranking never grants
  publication permission;
- no graph mode automatically authorizes commit, push, deployment, release,
  issue changes, external messages, or another externally visible side effect.

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

**Status: in progress through two bounded increments.**

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

**Status: in progress through two bounded increments.**

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

**Status: in progress through two bounded increments.**

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

**Status: next and unblocked.**

- Complete aggregate token/cost/iteration/wall-clock presentation across the
  primary and automatic workers.
- Compare decomposable, cross-layer, trivial, and inherently serial scenarios
  against Standard and primary-only Orchestrated execution; retain fan-out
  only where the measured quality or elapsed-time benefit justifies its cost.
- Finish the event/automation compatibility decision required before any
  headless activation surface is considered.

- Build on OG-2A's explicit per-session opt-in, graph approval, status,
  cancellation, and inert-resume contract without weakening it.
- Allow at most two automatically selected concurrent read-only delegates by
  default, even if the manual delegate ceiling is higher.
- Keep one serial primary write lane in the parent workspace.
- Start with a guideline of at most 12 logical nodes, two graph revisions, and
  two attempts per node; measure before making these configuration surface.
- Enforce aggregate graph token, cost, iteration, and wall-clock budgets.
- Locally record why the scheduler delegated, serialized, retried, replanned,
  invalidated, blocked, or finished. Do not add default telemetry.

Exit gate:

- decomposable read-heavy and cross-layer evaluations demonstrate a useful
  quality or elapsed-time improvement over Standard mode;
- cost and extra model work are visible and bounded;
- trivial or inherently serial work remains serial;
- project content cannot enable the mode or widen authority.

### OG-3 — Isolated writer candidates

**Status: blocked on OG-2.**

- Automatically dispatch only dependency-ready writers with declared disjoint
  scopes and a stable base.
- Keep writers isolated, non-recursive, bounded, and attributable.
- Require observed-scope validation and fresh child verification.
- Produce reviewable candidates and deterministic eligibility facts.
- Do not automatically select or integrate a competing candidate.
- Retain conflicting, stale, failed, or out-of-scope worktrees for inspection.

Exit gate:

- adversarial overlap, scope, drift, cancellation, and provider-failure tests
  produce no parent mutation before reviewed integration;
- no two writers can overwrite each other or the parent;
- every candidate remains attributable to its plan node and attempt.

### OG-4 — Reviewed integration and combined verification

**Status: blocked on OG-3.**

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
non-replay guarantee. Neither increment makes a Standard-versus-Orchestrated
quality, cost, or performance claim. That comparative decision belongs to
OG-2B2b.

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

- Last completed milestone: **OG-2B2a — Cooperative pause and safe retry**.
- Active milestone: **none**.
- Next unblocked milestone: **OG-2B2b — Aggregate presentation and comparative
  evidence**.
- Active implementation branch or partial patch: **OG-2B2a is implemented as
  an uncommitted patch on `wave36`**.
- Shipped experimental mode: **TUI-only, explicit per-session Orchestrated
  Goal with one serial primary lane and at most two automatic read-only
  workers for independently ready approved nodes, plus cooperative
  pause/resume and safe retry of eligible blocked nodes**.
- Current default behavior: Standard model-directed execution with
  evidence-gated goal completion.
- Preserved implementation constraint: only approved `read_only` nodes may be
  automatically delegated; primary work and every parent-workspace write stay
  serial, workers cannot recurse or control the graph, and a saved graph
  remains inert until explicit resume.
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

## Open implementation decisions

These remain unresolved for OG-2B2b or later:

- whether and how a headless CLI surface should be added;
- the representation of user-authored verification waivers;
- whether the initial guideline limits become user configuration after
  measurement;
- the precise targeted-versus-full combined verification policy by repository
  type;
- whether later built-in reads earn narrower freshness footprints than OG-1's
  conservative whole-workspace token;
- the final event-version decision when real automation users can opt in.

An implementation may resolve these questions, but must record the decision
and its evidence here before declaring the relevant milestone complete.
