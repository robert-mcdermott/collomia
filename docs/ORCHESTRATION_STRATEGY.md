# Orchestrated Goal strategy

**Status:** approved product and architecture strategy; product implementation
has not started  
**Roadmap owner:** Phase 6 — Multi-agent orchestration  
**Last updated:** 2026-08-01  
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

The user-facing promise is:

> Give Collomia an outcome. It builds an inspectable dependency graph,
> executes only ready work, preserves evidence and repository freshness,
> recovers from bounded failures, and stops only when the integrated workspace
> is verified or the goal is explicitly blocked.

The working product name is **Orchestrated Goal**. Internally it is plan-graph
execution. The user-facing name emphasizes the outcome rather than the
implementation mechanism and avoids confusion with `/plan`, which is a
read-only planning surface.

This is a good direction for Collomia, but only as a staged, explicit opt-in.
It should not become a generic agent swarm, a larger permission mode, or a
default path for ordinary tasks.

## Product decision

Collomia will retain two distinct execution modes:

| Mode | Contract |
| --- | --- |
| Standard | The current model-directed tool loop with evidence-gated completion. It remains the default. |
| Orchestrated Goal — experimental | A runtime-owned dependency graph coordinates primary work, bounded delegates, integration, recovery, and verification. |

Planning mode remains separate. `/plan` means read-only analysis and must not
implicitly start execution or delegation. An orchestrated run begins with an
explicit user action and a visible graph proposal.

The recommended initial interaction is one of:

```text
/orchestrate Build a kanban application with tests and documentation
```

```text
collo --orchestrate "Migrate this project to the new API and make all tests pass"
```

The exact command spelling remains an implementation decision. The required
experience is:

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
| Delegation | Bounded batches, named profiles, plan-step association, per-agent budgets, steering, cancellation, durable results, and no recursive delegation. |
| Scheduling | Session-wide FIFO admission, global/provider limits, declared write scopes, concurrent disjoint writers, and serialization of overlapping or workspace-wide writers. |
| Isolation | Write delegates operate in separate Git worktrees and cannot directly mutate the parent workspace. |
| Evidence | Child summaries, changed paths/hunks, scope violations, usage, machine-observed child verification, and state tokens are durable and inspectable. |
| Integration | Freshness-bound review, conservative three-way composition, selective rooted publication, ordinary permission checks, and no automatic commit, merge, push, or worktree deletion. |
| Operations | Stable events, replay, audit attribution, token/cost/time/iteration bounds, cancellation, fail-stop persistence, and operator inspection. |

The remaining prerequisites are not cosmetic:

- a runtime-owned node lifecycle and attempt ledger;
- deterministic dependency-ready node selection for the primary agent;
- bounded graph revision and replanning after classified failures;
- structured evidence references that the model cannot manufacture or replace
  with prose;
- conservative repository-assumption invalidation;
- explicit verification of the combined parent workspace;
- mutation-safe graph resume and scheduler recovery;
- graph-specific UI, event contracts, evaluations, and compatibility rules.

## Authority model

Responsibility must remain divided:

| Participant | Owns |
| --- | --- |
| Model | Decomposing the outcome, proposing dependencies and acceptance criteria, interpreting results, selecting a suitable profile, and proposing bounded replans. |
| Runtime | Readiness, claims, attempts, locks, scheduling, staleness, evidence identity, budgets, cancellation, recovery, and whether structural completion is possible. |
| User | Starting orchestration, granting authority, resolving material ambiguity or conflicts, approving protected operations, and granting any verification waiver. |

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

## Runtime-inserted control gates

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
  verification or a user-authored waiver exists;
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

If meaningful automated verification does not exist, Orchestrated Goal must
pause for a user-authored waiver. A model-authored `verification_note` can
explain the situation but cannot waive a graph's write-completion gate.

Candidate synthesis has two stages:

1. Deterministic eligibility removes candidates with scope violations, stale
   state, unresolved conflicts, failed required verification, or a mismatched
   task contract.
2. The coordinator may compare eligible candidates and recommend one with a
   visible rationale based on exact hunks, task fit, evidence, and cost.

A score or recommendation never authorizes application. The first writer
slice stops at a reviewable candidate and does not automatically choose among
competing results.

## Recovery contract

Resume must distinguish safe recomputation from dangerous replay:

- queued or interrupted read-only work may be restarted explicitly;
- completed read-only evidence may be reused only while its state token remains
  fresh;
- an interrupted mutation is never replayed automatically;
- a dirty retained worktree remains a candidate awaiting inspection;
- an integration is not repeated because its final event was interrupted;
- scheduler order, graph revision, attempts, bounds, and terminal states are
  durable;
- resume converts ambiguous mutation state into a blocker requiring
  reconciliation rather than guessing;
- cancellation can target a node, a branch of dependents, or the whole graph,
  and no new action begins after cancellation is acknowledged.

This contract is a prerequisite for calling the mode suitable for unattended
work.

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

Required controls are pause, resume, cancel graph, cancel node, inspect node,
retry eligible node, and review a proposed graph revision. A paused graph holds
state and starts no new work; it does not suspend an already running OS process
without using the existing cancellation contract.

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

**Status: not started. This is the next implementation slice.**

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

Exit gate:

- a primary-only graph survives success, recoverable failure, permission
  denial, cancellation, stale workspace state, budget exhaustion, and resume;
- no open/stale/unverified required node can produce `done`;
- no mutation is duplicated during recovery;
- Standard mode behavior and compatibility remain unchanged.

### OG-2 — Experimental Orchestrated Goal with read fan-out

**Status: blocked on OG-1.**

- Add the explicit per-session opt-in and graph proposal approval.
- Allow at most two automatically selected concurrent read-only delegates by
  default, even if the manual delegate ceiling is higher.
- Keep one serial primary write lane in the parent workspace.
- Start with a guideline of at most 12 logical nodes, two graph revisions, and
  two attempts per node; measure before making these configuration surface.
- Enforce aggregate graph token, cost, iteration, and wall-clock budgets.
- Add pause/resume/cancel/inspect controls and a visible experimental badge.
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

- Restore scheduler order and graph bounds exactly.
- Restart only safe pending read-only work.
- Reconcile interrupted writer and integration states without replay.
- Complete security, reliability, compatibility, and performance campaigns.
- Compare Standard and Orchestrated modes on the evaluation matrix below.
- Decide whether to graduate, revise, or retain the mode as experimental.

Graduation does not imply universal automatic use. Even a graduated graph
engine should be selected only for work that benefits from decomposition.

## Evaluation and graduation

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

- Last completed milestone: **OG-0 — Strategy and continuity**.
- Next milestone: **OG-1 — Runtime-owned primary graph controller**.
- Active implementation branch or partial patch: **none recorded**.
- Shipped experimental mode: **none**.
- Current default behavior: Standard model-directed execution with
  evidence-gated goal completion.
- First implementation constraint: OG-1 adds no automatic delegated actors.
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

## Open implementation decisions

These are deliberately unresolved and should be decided with code and
evaluation evidence during OG-1 or OG-2:

- final CLI and slash-command spelling;
- the persisted execution-graph record version and whether it is embedded in
  session records or referenced as its own artifact;
- exact node/attempt event vocabulary and compatibility rollout;
- the graph-revision tool schema;
- the representation of user-authored verification waivers;
- state-token granularity for built-in file reads and conservative external
  reads;
- whether the initial guideline limits become user configuration after
  measurement;
- the precise targeted-versus-full combined verification policy by repository
  type.

An implementation may resolve these questions, but must record the decision
and its evidence here before declaring the relevant milestone complete.
