# Collomia Roadmap History

This archive preserves the detailed assessment, dated implementation log, and
historical phase annotations that previously lived in `ROADMAP.md`. It is a
record of what changed and why, not the current task queue.

For current priorities, remaining deliverables, and sequencing, see
[`../ROADMAP.md`](../ROADMAP.md).

**Assessment date:** 2026-07-17 · **History split:** 2026-07-23

**Scope:** Current repository compared with the original product requirements,
plus a feature and architecture benchmark against current terminal coding
agents, provider platforms, and the Model Context Protocol specification.

## Executive summary

*Original 2026-07-17 assessment:* Collomia was a credible **vertical-slice MVP** — one Go binary, a polished Bubble Tea interface, a provider-neutral tool-calling loop, approval prompts, workspace-aware tools, slash commands, skills, MCP tools, planning mode, and a bounded subagent — but not yet a production-grade agentic terminal.

*Updated 2026-07-23:* Phases 0–3 are P0-complete and substantial slices of phases 4, 5, 6, 7, and 8 have shipped. Collomia now has a schema-versioned event model; layered, validated configuration with repository trust; a scoped permission-rules engine with conservative command analysis, an audit ledger, and OS sandbox enforcement on macOS (Seatbelt), Linux (Landlock), and Windows 11 (AppContainer/Job Object); durable crash-safe sessions with resume/fork/non-destructive completed-turn rewind, automatic compaction, complete visible transcript/tool restoration, dynamically pinned plan state, bounded referenced oversized results, exact recent-failure retention, session-scoped prompt history and drafts, visible fail-stop handling for short/disk writes that blocks later provider/tool boundaries, and explicitly versioned backward-compatible session records; an atomic patch tool, diff tracking with checkpointed undo, git inspection tools, a structured plan artifact, and a user-question primitive; live-streamed command output, fuzzy pickers (including delegated agents), `@` file/folder mentions, bounded prompt-from-file input, model discovery, a review workflow, provider retry/error/timeout/circuit health, normalized provider streaming, MCP live-catalog refresh/conformance fixtures, a concurrent worktree-isolated delegate scheduler with manual and opt-in primary-reviewed guarded integration, a loopback-only browser terminal (`collo --web`), desktop notifications, a colorless `plain`/`NO_COLOR` mode, optional reduced motion, a searchable/copyable transcript browser, a bounded searchable event-derived activity center, responsive interactive diff review with safe external-editor handoff, an asynchronous workspace/health dashboard with recovery hints, validated keybindings/shell completion, machine-readable `run.result` output for automation, offline deterministic trace validation/replay, a complete representative credential-free agent evaluation matrix, diagnostic performance baselines, parser fuzz smoke tests, privacy-conscious local support bundles with opaque failure correlation, and platform-neutral TUI golden screens exercised on all CI operating systems. The remaining large gaps, roughly in dependency order:

1. **Enforced endpoint-scoped egress and independent security review** (Phase 1's enumerated adversarial corpus now covers rooted symlink races, hard links, MCP prompt injection, and native network denial in addition to the existing command/process/read/write cases; declared endpoints, host-scoped rules, scoped/allowlist postures, and per-capability grants shipped 2026-07-24, and credential-store protection shipped 2026-07-25, but OS-level endpoint-scoped egress confinement remains open).
2. **Provider platform hardening**: Azure deployment/project discovery, general Responses routing, and explicit routing/fallback (Phase 4 — optional macOS/Windows keychain credential storage shipped 2026-07-25 — capability declarations/discovery/preflight, normalized cross-adapter streaming including Bedrock `ConverseStream`, resilience, recorded/live protocol contracts, and refreshable Azure Entra authentication shipped 2026-07-19).
3. **MCP ecosystem remainder**: OAuth authentication, experimental tasks, resource subscriptions, audio/annotation passthrough, and argument-level permission scoping (Phase 5 — skills, hooks, lifecycle/resources/prompts/elicitation/progress/pinning, bounded image passthrough, external-data provenance framing, safe list-change refresh, and conformance fixtures have shipped).
4. **Multi-agent orchestration polish**: the user-approved Orchestrated Goal
   evidence-gated durable execution mode, automatic writer candidates,
   combined-parent verification/ranking, exact multi-worker recovery, and
   fuller transcript audit (Phase 6 now has the OG-1 durable controller,
   explicit TUI-only OG-2A preview, OG-2B1 bounded automatic read fan-out, and
   OG-2B2a cooperative pause/resume plus safe blocked-node retry, OG-2B2b1
   durable primary-plus-worker accounting/status presentation, and OG-2B2b2
   fixed aggregate enforcement plus comparative read-fan-out evidence,
   in addition to named primary/delegated profiles, portable reasoning,
   durable token/cost budgets, restrictive permissions,
   scheduling/isolation, declared-scope serialization, structured results,
   plan-step association, durable outcomes, a live parent/child tree,
   boundary-safe steering, machine-observed child verification, and
   freshness-bound three-way text integration have shipped).
5. **Deep coding-loop tooling**: Phase 3 is now functionally complete (diagnostics, definitions, references, formatting, indexing, PTY, background jobs, hunk approval); remaining refinements are safe LSP code actions, line-level approval, and Windows ConPTY.
6. **Release and QA engineering**: native platform signing/notarization, package-manager distribution, deeper native-terminal/accessibility coverage, independent review, and sustained security/reliability campaigns (Phase 8 now includes deterministic replay, cross-platform TUI goldens, a complete representative offline evaluation matrix, diagnostic startup/index/session/TUI performance baselines, explicit persisted-format compatibility rules, bounded fuzz CI, session short-write handling, support bundles, checksum-verifying atomic installers, reachable-dependency scanning, CycloneDX SBOMs, GitHub/Sigstore attestations, and gated draft releases).

*Updated 2026-07-25:* three further waves shipped. Reaching a conventional credential location is now its own permission decision that no blanket rule, tool-wide grant, or autonomy mode can absorb; the approval surface around it was made livable rather than dismissible, and the effective stance is reportable through `collo doctor` and grouped in the Session tab. The terminal surface gained a draft-sized composer with an external-editor handoff, an optional context rail, per-tool-call outcome and timing, an opening orientation card, mouse wheel/tab-click support that can be handed back to the terminal mid-session, and readability fixes to modals, approval diffs, and the context gauge. None of this changes the standing gaps below.

The guiding principle is unchanged: make Collomia **safe and recoverable before making it more autonomous**. Phases below are dependency ordered, not calendar estimates.

## Recent updates

### 2026-08-03 — OG-3A.8 review-readiness corrections from an implementation audit

- **A full review of the shipped Orchestrated Goal implementation, rather than
  another trial, drove this slice.** The audit read the strategy against the
  code and found that the evidence gate was narrower than the design language
  implied, that two acceptance decisions were made by matching English strings,
  and that a successful candidate wave was reported to the operator as a
  failure. Nothing here widens authority; every change either lets honest work
  finish or removes a way the runtime could be wrong about its own state.
- **The verification recognizer no longer decides which languages the mode
  works in.** A mutating primary node cannot complete without recognized
  verification, so an ecosystem missing from the recognizer was an ecosystem in
  which every honest change blocked, with no waiver available before OG-4. The
  recognizer now unwraps environment managers recursively (`uv`, `poetry`,
  `pipenv`, `pdm`, `hatch`, `rye`, `pixi`, `conda`/`mamba`/`micromamba` with an
  environment selector, `bundle exec`, `npx`) and recognizes R, Ruby, Elixir,
  PHP, Swift, CMake/`ctest`, Deno, Haskell, Bazel, `just`/`task`, `tox`/`nox`,
  and Java build tools alongside the original Go, Rust, Node, and Python forms.
  Detection gained the matching markers and now reports the runner a Python
  repository actually uses, so a Poetry or tox project is not told to run a
  bare `pytest` that cannot work there. Breadth is bounded: a wrapper qualifies
  only when what it wraps is itself a recognized check, and each new ecosystem
  contributes its test entry point alone so candidate verification suites stay
  short.
- **`git diff --check` is no longer accepted as verification.** A whitespace
  linter passes on nearly any tree, so accepting it let a mutating node close
  its verification gate without checking the change it had just made. That was
  the one recognized command that proved nothing about the work.
- **Completion gaps are typed state instead of prose.** The remediation lease
  previously renewed by matching sentence fragments against text the runtime
  had rendered for the model, so rewording one message could silently strand a
  productive attempt — the failure class OG-3A.2 through OG-3A.5 each had to
  correct. Gaps are now `no_tool_evidence`, `no_state_token`, `no_op_write`,
  and `no_fresh_verification`, persisted additively; the sentence is derived
  from the kind, and pre-typing snapshots recover their kinds once at restore.
  A legacy sentence this build cannot recognize clears the gap rather than
  leaving an unenforceable one. Read-node groundedness likewise now uses a
  machine-counted successful-tool total from the worker instead of counting
  `": completed —"` occurrences in its rendered evidence lines.
- **A verified candidate wave is reported as success.** Nodes holding retained
  candidates enter the new `awaiting_review` state and reduce to an
  `awaiting_review` graph outcome, so the writer path working is no longer
  indistinguishable from it failing. The turn ends with an answer naming the
  review step instead of a `goal blocked` error, retry refuses with a reason
  that names the candidate, and budget exhaustion no longer overwrites a
  retained candidate's node. The state is additive to graph schema 1 and to the
  internal-only `goal.graph.update` event; the public `run.result` outcome
  enumeration is unchanged.
- **A wave that crosses the aggregate budget keeps its candidates.** Usage was
  previously recorded for the whole wave before any result was interpreted, so
  a crossing terminated the graph before a single `WriterCandidate` was
  attached — leaving real `collomia/…` worktrees on disk with nothing in the
  graph pointing at them. Candidate facts are now durable before the aggregate
  limit is enforced, and an over-budget wave still records where each worktree
  is. No further child verification runs after the ceiling.
- **Automatic writers can no longer commit or branch.** Rebuilding the child
  registry for a worktree restored every builtin, including `git_commit` and
  `git_branch`, leaving an explicit non-goal enforced only by prompt text — and
  in `workspace` or `autopilot` mode those are write-risk actions that need no
  prompt. A commit there would also move the ref the retained candidate's diff
  is measured against. They are now removed from the registry and refused
  again at availability, the same two-layer treatment the graph meta-tools get.
- **Durable graph state is bounded.** Every transition rewrites the complete
  snapshot into the session log, so an attempt that retained every tool result
  made persistence cost grow with the square of a node's tool calls — precisely
  on the long nodes that reach that point. Attempts now retain at most forty
  ordinary tool results, never pruning verification or node-result evidence,
  and record how many were dropped; the complete transcript remains in the
  durable session log.
- **A node boundary no longer discards the user's steering.** The zero-provider
  handoff replaced the entire active context, including guidance the user had
  been told applies to the remaining task. Mid-graph steering is now retained
  and reattached after each accepted node, bounded to the steering queue's own
  depth.
- **`internal/writescope` has direct tests, and its two comparisons now err in
  opposite directions on purpose.** The package deciding writer disjointness
  and scope violations previously had no test of its own. `Overlap` still folds
  case, because over-detecting a collision only costs parallelism, while
  `Violations` is now case-exact, because on a case-sensitive filesystem
  folding `src/` into `SRC/` would silently accept an undeclared write.
- **Verification:** `go build ./...`, `go vet ./...`, `gofmt -l internal`,
  `go test -count=1 ./...`, and `go test -race -count=1 ./internal/goalgraph
  ./internal/agent ./internal/writescope` pass. New coverage: ecosystem
  recognition and wrapper rejection, Python runner detection, typed-gap
  renewal and legacy recovery, budget-crossing candidate retention, legacy
  blocked-candidate restore, evidence pruning under a long attempt, graph-writer
  tool denial, steering retention across a node boundary, and the writescope
  table tests.

### 2026-08-03 — OG-3A.7 proposal-state authority and escape paths corrected

- **The third successful Kanban6 wave exposed a proposal-state mismatch, not
  an execution failure.** The earlier application and SQLite waves completed.
  For the drag-and-drop wave, the model used ordinary plan semantics during
  read-only proposal design: it inspected the frontend, marked that
  investigation node `done` with evidence, and left the implementation node
  pending. Approval then rejected the otherwise valid topology with `proposal
  step 1 must be pending`, leaving the session correctly read-only but without
  the intuitive transition the user expected.
- **Model-authored plan progress no longer becomes runtime graph truth.** The
  approval path already rebuilt accepted plans with every status pending and
  evidence empty, but an earlier validator contradicted that boundary by
  rejecting non-pending annotations first. That rejection is removed. A
  `done` or `in_progress` proposal step can describe what the planning model
  believes, but approval imports only topology, execution class, dependencies,
  scopes, and acceptance criteria; the runtime starts every node fresh and
  unproven.
- **Proposal design avoids repeating its own inspection.** The proposal prompt
  now tells the model that investigation used to formulate the graph is
  grounding, not a completed graph node. It should include a pending
  `read_only` node only when a fresh post-approval investigation is a real
  dependency.
- **Both escape paths are explicit and tested.** `/orchestrate cancel` still
  cancels an unapproved proposal. `/plan off` now means “cancel this proposal
  and restore execution mode” instead of refusing and pointing elsewhere; its
  completion description says so while a proposal is active. Neither path
  approves or executes the saved plan, and ordinary permission boundaries are
  unchanged.
- **Regression evidence** recreates a `done` read-only proposal node followed
  by an `in_progress` primary node, proves approval normalizes both to pending
  with no evidence or attempts, and exercises both TUI cancellation paths.

### 2026-08-03 — OG-3A.6 multi-wave lifecycle and node-boundary efficiency corrected

- **The first successful end-to-end trial exposed two lifecycle and context
  defects on its follow-up wave.** A completed graph remained attached and
  refused a new `/orchestrate <goal>`; cancelling it was a terminal no-op, so
  starting the SQLite wave required leaving Collomia. In that second wave,
  runtime node 1 consumed 47 provider responses, 854,663 input tokens, 39,710
  output tokens, and 57 successful tools. The model implemented work assigned
  to all five nodes and passed 25 tests while the runtime still correctly
  showed only node 1 accepted. Starting node 2 then failed aggregate admission:
  the next approximately 9,683-token prompt could not fit the remaining
  5,011-token allowance. Total graph usage was 56 responses and 994,989 tokens.
- **Terminal graphs now yield without destroying history.** Starting a new
  `/orchestrate <goal>` automatically appends a durable tombstone for an
  attached or saved terminal graph, detaches its controls, and enters a fresh
  proposal in the same session. `/orchestrate cancel` on an already-terminal
  graph performs the same explicit archive action. Every earlier graph
  snapshot, transcript message, and evidence record remains in the append-only
  session log; only the pointer identifying the graph eligible for resume is
  cleared. A nonterminal saved or attached graph still requires resume or
  cancellation and cannot be displaced silently.
- **Accepted nodes now have a hard context boundary.** A passing verifier tells
  the model to return a tool-free completion proposal and forbids later-node
  work until runtime selection. The pinned graph repeats that instruction.
  Once the runtime accepts a nonterminal node, it replaces the active model
  context with a small runtime-authored handoff before starting the next
  provider request. The pinned graph retains bounded accepted dependency
  summaries needed by downstream nodes. This handoff costs no provider call, preserves the full
  durable transcript, and prevents the next node from repeatedly paying for
  the previous node's tool loop.
- **Graph and inspection overhead are smaller by construction.** Proposal
  guidance now prefers one to three coherent nodes for a scoped change and
  four to six only for broad work, batches related operations, and requires an
  immediate tool-free proposal after final verification. `list_files` and
  `search_files` omit VCS metadata, dependency trees, build output, caches, and
  virtual environments while retaining hidden source files.
- **Regression evidence** covers terminal attached and saved graphs beginning
  another wave, explicit terminal cancel/archive, append-only graph tombstones,
  zero-provider node-boundary handoff with the prior tool transcript absent
  from the next request, explicit verifier/node-boundary receipts, scaled
  proposal guidance, and generated-tree filtering.

### 2026-08-02 — OG-3A.5 repair progress and verifier bootstrap corrected

- **Kanban5 exposed an overcorrection at the exact blocking boundary.** The
  all-primary six-node graph was schedulable, and node 1 did useful work:
  FastAPI/Uvicorn dependencies installed, the app imported, Uvicorn reached
  startup, and machine HTTP checks returned 200 for `/` and `/health`. At 26
  total graph iterations and 291,940 tokens, the runtime required its generic
  conventional verification gate. The correctly formed pytest command then
  returned exit 5 because no tests existed, but the four-cycle lease blocked
  before the model could react to that new diagnostic.
- **Repair evidence now renews remediation without admitting churn.** A novel
  recognized verification failure retains its bounded output and advances the
  gap watermark. A real Git workspace mutation—such as adding the missing
  focused test—also advances it. A passing current verifier still closes the
  gate. Repeating identical failure output through the same or differently
  spelled command does not renew anything, nor do unrelated reads or rejected
  wrappers.
- **New projects establish verification before depending on it.** The proposal
  prompt now requires each mutating primary node to name a direct build, lint,
  or test command and requires the first mutating node to create a focused
  smoke test when no applicable test surface exists. The completion notice
  gives the same repair instruction after “no tests collected.”
- **Regression evidence** reproduces the exact boundary: three non-progress
  responses followed by a novel failed verifier, a workspace repair, a passing
  verifier, and truthful completion. A companion test proves repeated
  identical verifier failures still block after the bounded window.
- **Saved blocked graphs have a one-step retry path.** Persisted graphs remain
  inert after reopening a conversation, but `/orchestrate retry <node-id>` is
  itself an explicit activation decision and now safely reattaches that saved
  blocker before opening the fresh bounded attempt. `/orchestrate resume`
  remains available for ordinary nonterminal continuation.

### 2026-08-02 — OG-3A.4 completion gaps and executable graph shape corrected

- **The fourth clean-project trial found a gate loop, not useful durable
  progress.** The scaffold node used 54 provider responses and 900,582 tokens,
  including 22 pytest-bearing commands, while trying superficial variants of
  already passing verification. The common
  `cd <workspace> && UV_CACHE_DIR=.uv-cache uv run pytest 2>&1` form was not
  recognized, so novel-looking command results repeatedly renewed the broad
  progress lease even though the same verification gap remained open.
- **Completion remediation is now tied to the unmet gate.** The runtime
  persists the exact completion-gap fingerprint and its last gate-changing
  iteration. Once open, a four-provider-cycle lease renews only when evidence
  can change that gap; unrelated reads, different commands, and repeated
  passing output cannot extend it. Exhaustion ends `blocked` with the exact
  gap instead of spending the aggregate budget.
- **Safe verification wrappers qualify without weakening exit-status
  integrity.** An exact redundant `cd` to the current workspace (or `.`) plus
  `&&`, and a final literal `2>&1`, are canonicalized before classification.
  Other directories, pipes, semicolons, `||`, and status-masking shell forms
  remain ineligible. The command tool also states that it already starts in
  the workspace and asks for direct verification.
- **Impossible retained-candidate graphs fail before execution.** The trial's
  primary scaffold necessarily dirtied the parent, after which a dependent
  `isolated_write` node could neither inherit those uncommitted bytes nor
  unlock its own dependents because OG-3A stops candidates for review. The
  proposal/revision validator now defaults end-to-end changes to `primary` and
  permits `isolated_write` only in candidate-only graphs whose writers are
  terminal leaves. Candidate approval preflights the stable Git base, names
  dirty paths, preserves the proposal after failure, and succeeds once the
  base is corrected.
- **Regression evidence** covers exact workspace/redirection wrappers,
  continued rejection of masking forms, a repeated-gap loop with superficially
  novel evidence, candidate-only topology rules, recoverable dirty-base
  approval, scheduler diagnostics, snapshot validation, and the existing
  aggregate/recovery/permission controls.

### 2026-08-02 — OG-3A.3 progress-aware control and workspace evidence corrected

- **The third realistic trial was productive when its local counter stopped.**
  One primary node reached 24 provider responses after scaffolding the
  application, repairing dependency/import failures, passing five tests,
  completing a live health check, and producing fresh recognized pytest
  evidence on its final action. Durable aggregate accounting was only 53 of
  96 iterations and 713,278 of 1,000,000 tokens. The runtime denied the one
  subsequent response needed to propose completion because 24 was still a
  lifetime attempt counter.
- **The primary bound is now a no-progress lease.** In Orchestrated Goal,
  `max_iterations` measures consecutive provider cycles without novel durable
  successful tool evidence. New evidence or a resolved recoverable failure
  renews the lease in the same immutable attempt; equivalent repeated evidence
  does not. Standard turns retain their total iteration ceiling, and the fixed
  96-iteration/token/conditional-cost/active-wall graph envelope remains the
  hard outer bound.
- **Potential effects no longer masquerade as repository changes.** The
  runtime still advances and durably persists a conservative write-ahead
  generation before potentially mutating or external work, so interrupted
  effects remain non-replayable. After a completed action, an unchanged
  machine-observed workspace token preserves the attempt's observed mutation
  generation. Starting/stopping a server or performing a smoke request no
  longer invalidates passing tests unless repository state actually changed;
  a changed or unavailable state token still requires fresh proof.
- **Verification feedback and graph shape are more efficient.** Passing direct
  verification returns an explicit state-bound receipt to the model. Direct
  Python module forms such as `.venv/bin/python -m pytest` and `uv run python
  -m pytest` are recognized. Proposal guidance prefers four to six substantive
  outcome nodes, coalesces serial same-scope work, and treats twelve as the
  maximum rather than the target.
- **Focused regression evidence** covers productive attempts exceeding their
  configured lease, equivalent-evidence exhaustion, unchanged external effects
  preserving proof, real workspace changes staling proof, Python command
  variants, positive receipts, proposal granularity, snapshot validation, and
  the pre-existing recovery/aggregate controls.

### 2026-08-02 — OG-3A.2 primary-loop budget and evidence diagnostics corrected

- **The second realistic trial exposed a control-loop composition bug.** The
  primary lane used eight iterations in a recoverable first attempt and
  sixteen in its replacement, then exhausted the ordinary 24-iteration turn
  counter even though durable graph accounting showed only 31 of 96 aggregate
  iterations and 396,331 of 1,000,000 tokens. The count was provider/model
  response cycles, not tool calls; the same 24 responses contained 33 tool
  calls.
- **Primary bounds now follow immutable attempt boundaries.** Standard mode
  still applies `max_iterations` to one turn. Orchestrated Goal applies it to
  one primary node attempt and renews the same bounded slice only after the
  runtime accepts an attempt or starts a recorded retry. The fixed aggregate
  graph iteration, token, conditional-cost, and active-wall limits remain the
  cross-attempt outer envelope.
- **Rejected verification is actionable.** The trial repeatedly passed pytest
  through `2>&1; echo "EXIT_CODE=$?"`; this correctly could not become evidence
  because the shell tail can mask pytest's failure, but the model received
  only a generic later gap. A verification-like rejected command now explains
  the status-masking risk and supplies the safe direct form. Direct commands
  with leading environment assignments or a virtual-environment executable
  path are recognized.
- **Hidden tools are now an enforced boundary.** The graph omitted
  `update_plan`, but a model-emitted remembered call still reached its JSON
  decoder and produced the reported unmarshal error. Provider definition
  filtering and pre-decode execution admission now share one decision; a
  graph-hidden call is blocked without executing and without recording a false
  work-node failure.
- **Focused regression coverage** proves hidden tools cannot be assessed or
  executed, two primary nodes can each consume their bounded iteration slice,
  a one-attempt ceiling still terminates truthfully, direct Python verification
  variants qualify, and success-masked verification returns an exact
  correction.

### 2026-08-02 — OG-3A.1 aggregate-budget usability corrected

- **A realistic run exposed a calibration failure, not an accounting defect.**
  An eleven-node application proposal used 67,149 input/output tokens across
  nine calls. Seven primary execution calls used another 119,977, leaving
  4,874 of the original 192,000-token envelope while the next request required
  an estimated 17,793-token prompt. Every one of the 187,126 recorded tokens
  matched provider usage exactly, but the graph exhausted before reaching an
  isolated writer.
- **New graphs have a usable but still fixed envelope.** The token ceiling is
  now 1,000,000. The 96 provider-iteration, conditionally enforced $5
  estimated-cost, and 30-minute active-wall ceilings remain unchanged, and
  tighter provider/profile/worker bounds still win. Configuration,
  repository content, hooks, skills, and model output cannot widen the limit.
- **Compaction now understands cumulative work.** A newly approved graph
  compacts proposal history once before its first node. Later it compacts when
  one estimated prompt reaches one-eighth of the remaining graph allowance,
  even if the prompt is far below 80% of the provider context window. The
  summary request remains recorded primary-lane usage, while the complete
  transcript remains durable.
- **The two token concepts are visible together.** Proposal status shows the
  exact work that approval will seed and its remaining allowance. `/context`
  and the Session tab label the current prompt as a per-request value and show
  cumulative Orchestrated Goal consumption separately.
- **Stored authority does not grow during upgrade.** A graph created with the
  earlier 192,000-token ceiling restores with that exact ceiling. Only a newly
  approved graph receives 1,000,000 tokens; snapshots above this build's hard
  maximum remain invalid.
- **Focused regression coverage** proves approval-boundary accounting,
  cumulative-pressure compaction below the context-window trigger, proposal
  and active budget presentation, the new default, and conservative restore
  of the earlier ceiling.
- **Verification:** `go test -count=1 ./...`, `go test -race -count=1 ./...`,
  `go vet ./...`, `go build ./...`, and `git diff --check` pass on the combined
  OG-3A and OG-3A.1 worktree, including native macOS sandbox and loopback
  coverage.

### 2026-08-02 — OG-3A verified isolated-writer candidate wave completed

- **The first automatic writer stops before publication.** An approved plan
  may declare `execution: isolated_write` only with explicit narrow
  repository-relative `write_paths`. The graph selects at most two ready,
  pairwise-disjoint writers in stable order and binds the complete wave to one
  clean parent state token and exact Git commit.
- **Existing authority is reused.** The wave obtains the ordinary `delegate`
  write permission and hooks, then uses the existing shared scheduler,
  non-recursive child runtime, exact-base `collomia/…` worktrees, inherited-or-
  tighter child permissions, declared-scope result checks, and bounded
  accounting. Approval of the graph or scope does not approve either dispatch
  or a child command.
- **Child completion is machine-observed.** The application redetects standard
  verification commands inside each retained worktree and sends every command
  through the ordinary `run_command` permission/hook path. Passing results are
  bound to one child-state token; parent drift, base drift, missing commands,
  failed/stale verification, or an out-of-scope file makes the result
  ineligible.
- **The durable handoff is inspectable and intentionally incomplete.** A valid
  result stores bounded node/attempt/worker/worktree/branch/base, changed-file,
  scope, command, and state-token facts as a `candidate` attempt. The node and
  graph stop `blocked` with a reviewed-integration reason. No dependent node
  unlocks and no candidate is selected, applied, committed, merged, pushed, or
  published.
- **Recovery does not replay writers.** A process boundary with a running
  isolated writer records an interrupted-action blocker. Safe retry refuses
  it, because the retained worktree may already contain a mutation even though
  the parent workspace is unchanged.
- **Compatibility remains additive and conservative.** `write_paths`,
  `isolated_write`, the fixed two-start writer envelope, automatic-writer
  accounting, stable-base identity, and bounded candidate facts remain
  internal additive schema-1 state. Older graphs normalize to no writer nodes;
  a new writer cannot appear unless a newly proposed graph explicitly contains
  one and the user approves it.
- **OG-3 remains open.** Focused graph, plan, agent, and application tests prove
  stable/disjoint selection, dirty-base refusal, stale/scope rejection, no
  parent mutation, candidate verification/retention, accounting, approval
  conversion, and non-replay recovery. OG-3B retains the broader cancellation,
  provider-failure, corrupted-state, drift, and operator-inspection campaign
  before OG-3 can be marked complete.
- **Verification:** `go test -count=1 ./...`, `go test -race -count=1 ./...`,
  `go vet ./...`, `go build ./...`, and `git diff --check` pass on the completed
  slice, including the native macOS sandbox and loopback-listener tests.

### 2026-08-02 — OG-2B2b2 aggregate enforcement and comparison completed

- **The graph now owns a whole-run envelope.** Durable schema-1 state stores
  fixed experimental ceilings of 96 provider iterations, 192,000 input/output
  tokens, $5 estimated cost when all token-bearing work has configured
  pricing, and 30 minutes of active execution after approval. Project
  configuration, instructions, skills, hooks, persisted content, and model
  output cannot widen them; tighter primary/profile/read limits still win.
- **Admission and observation both enforce it.** Reaching an exact bound
  prevents another provider or scheduler admission; a response that crosses a
  bound records its usage and ends `budget_exhausted`. Automatic read claims
  receive a durable share of the remaining token, iteration, priced-cost, and
  active-wall allowance. A completed parallel wave retains every sibling's
  already-spent usage even when the first recorded result crosses a bound.
- **Active time has conservative recovery semantics.** A reached pause,
  terminal transition, and process boundary freeze the clock. Saved state is
  inert, and only explicit resume starts it again, so user review, pause, and
  downtime do not consume the post-approval active execution allowance.
- **Missing pricing remains an explicit limitation, not fake enforcement.** If
  any token-bearing contribution is unpriced, cost remains unavailable and the
  runtime relies on its always-enforceable token, iteration, and active-wall
  ceilings instead of claiming it proved a dollar total.
- **The comparative conclusion is narrow and favorable where intended.**
  Credential-free decomposable-fact and cross-layer source/test scenarios run
  through Standard, primary-only graph, and two-worker graph paths. All produce
  the same grounded answer. With equal substantive investigation latency, the
  two independent workers share one expensive critical-path wave and complete
  faster, while six provider iterations and higher tokens remain visible.
  Primary-only trivial work starts no worker, and dependency-serial reads never
  overlap. This supports retaining bounded read fan-out, not making it default
  or claiming it is universally cheaper.
- **No event surface was added without a consumer.** Aggregate state remains in
  the durable graph snapshot and TUI status. `goal.graph.update` remains the
  same internal transition payload; the headless/event compatibility decision
  is deferred until a real headless activation design needs it.
- **Verification:** focused envelope, recovery, controller, application, and
  comparison evaluations pass. Full test/race/vet/build and documentation
  checks passed before handoff.

### 2026-08-02 — OG-2B2b1 durable aggregate accounting completed

- **The comparison inputs are now runtime-owned.** Graph schema 1 stores
  separate primary and automatic-read provider iterations, input/output
  tokens, cost availability, estimated cost, and the start of the explicit
  proposal turn. The proposal is part of the primary lane, so Orchestrated
  Goal cannot look cheaper by hiding its design call.
- **Failures and retries remain visible work.** Every completed primary or
  child provider request contributes one iteration even when a failure reports
  no tokens. Active primary and read attempts retain their own counters while
  the graph keeps the aggregate.
- **Status distinguishes facts from missing price data.** `/orchestrate status`
  shows total, proposal-plus-primary, and automatic-read work with elapsed
  time. Cost appears only when every token-bearing contribution had configured
  pricing; otherwise the status says `cost unavailable` rather than `$0`.
- **Compatibility does not invent history.** The accounting object and attempt
  iteration/cost-estimate fields are additive schema-1 data. A pre-accounting
  snapshot reconstructs only token/cost facts already present on immutable
  attempts and leaves unavailable proposal/iteration history at zero.
- **The product path proves the split.** State and agent tests cover durable
  accumulation, provider failures, retries, per-attempt attribution,
  unavailable pricing, elapsed time, corruption rejection, and legacy restore.
  Credential-free evaluations prove an explicit two-call proposal is retained
  and a two-worker investigation plus primary synthesis records exactly four
  read iterations and two primary iterations. Full test/race/vet/build and
  documentation checks passed.
- **Instrumentation is not a favorable verdict.** OG-2B2b2 still owns
  whole-graph aggregate enforcement, Standard/primary-only/fan-out comparative
  scenarios, the usefulness decision, and the event/headless compatibility
  decision.

### 2026-08-02 — OG-2B2a cooperative pause and safe retry completed

- **Pause is a durable scheduling request, not pretend process suspension.**
  `/orchestrate pause` is admitted while a turn is running, prevents new graph
  scheduling, lets the current provider/tool/read iteration finish, and then
  records the safe boundary. The TUI distinguishes `pausing` from `paused`,
  and `/orchestrate cancel` remains the immediate whole-graph stop.
- **Resume preserves runtime truth.** An attached paused graph clears only its
  pause state, retaining the active attempt, evidence, and bounds. A graph
  restored after a process/session boundary remains inert until the user
  explicitly runs `/orchestrate resume`; conservative interrupted-action
  recovery still applies.
- **Retry is narrow and history-preserving.** `/orchestrate retry <node-id>`
  accepts only a blocked node with attempt budget remaining and no unresolved
  non-replayable action. It preserves the blocked attempt and evidence, clears
  the graph's blocked outcome, and lets ordinary dependency readiness create a
  fresh attempt. Exhausted attempts and ambiguous interrupted mutations are
  rejected.
- **Node cancellation is intentionally absent.** Every current graph node is
  required, so cancelling a node would only alias whole-graph cancellation.
  Per-node cancellation stays deferred until optional branch semantics make it
  truthful.
- **Compatibility remains additive and internal.** Pause request/reached/reason
  fields stay in graph schema 1, and `pause_requested`, `paused`, `resumed`,
  and `retry_requested` use the existing internal-only `goal.graph.update`
  lifecycle contract. Standard mode, project opt-in boundaries, permissions,
  and the automatic-read envelope are unchanged.
- **The control path is exercised end to end.** Graph, agent, app, and TUI
  tests cover boundary arrival, active-attempt preservation, attached and
  restored resume, safe retry, unsafe/exhausted rejection, event states, busy
  command admission, and status presentation. Full test/race/vet/build and
  documentation checks passed.

### 2026-08-02 — Evidence-gated durable execution contract documented

- **The phrase names an implemented architectural boundary.** Orchestrated
  Goal is now described consistently as Collomia's experimental
  evidence-gated durable execution mode: the model proposes graph intent and
  interprets results, while the runtime owns readiness, attempts, evidence
  freshness, recovery treatment, and terminal state.
- **The evidence contract states what each proof prevents.** Automatic reads
  require a successful tool result and a matching state token when claimed;
  mutations require recognized successful verification against the current
  Git token and mutation generation. Model prose never substitutes for either
  record, and later workspace drift invalidates evidence conservatively.
- **Current guarantees are separated from later milestones.** Durable graph
  state, explicit inert resume, fresh-attempt recomputation of replay-safe
  reads, and non-replay of ambiguous mutations have shipped. Automatic
  isolated writers, reviewed integration, pause/node controls, and exact
  multi-worker scheduler recovery remain OG-2B2 through OG-5 work.

### 2026-08-02 — OG-2B1 runtime-selected read fan-out completed

- **The first automatic actor remains a read-only optimization.** A graph-owned
  execution class now claims at most two independently
  ready, explicitly approved read nodes in stable order. The primary workspace
  write lane remains serial, and primary-only or dependency-serial graphs do
  not gain a child merely because Orchestrated Goal was selected.
- **Existing delegate safety is reused, not bypassed.** Automatic workers use
  the same planning-mode registry, inherited-or-tighter permissions,
  non-recursive topology, bounded evidence inbox, audit identity, cancellation,
  and provider limits as manual read delegation.
- **Completion is evidence- and freshness-gated.** A worker result needs a
  bounded summary, successful tool evidence, and the same Git workspace token
  as its durable claim. The graph stores worker identity, attempt usage, a
  `delegate_read` evidence record, and the reason it delegated; child prose
  alone cannot mark a node done.
- **The aggregate envelope is fixed and persisted.** The experimental
  controller allows two live reads, eight starts, 64,000 aggregate read tokens,
  and fifteen minutes of read wall time. Each child is additionally capped at
  five minutes and eight iterations, and every node retains the existing
  two-attempt bound. Scheduler, provider, profile, cancellation, and permission
  limits can only make those bounds tighter.
- **Compatibility fails toward serial execution.** The additive plan/node
  `execution`, graph read envelope, and attempt worker/usage fields stay in
  schema 1. Snapshots written before fan-out restore omitted execution as
  `primary` and cannot gain automatic children from an upgrade.
- **The product path was exercised, not inferred.** One credential-free
  evaluation proves two governed workers overlap and unlock the dependent
  primary only after both grounded results arrive; another cancels both
  workers and proves the graph ends `cancelled`. Unit coverage proves stable
  claims, serial fallback, bounds, freshness retry, meta-tool isolation, and
  legacy restore. Full test/race/vet/build and documentation checks passed.
- **OG-2B is not complete yet.** OG-2B1 owns the runtime fan-out kernel and its
  authority/bound tests. OG-2B2 retains pause/node controls and the comparative
  quality, elapsed-time, and cost evidence required by the milestone exit gate.

### 2026-08-01 — OG-2A explicit primary-only preview completed

- **The user surface and the autonomy increase are separate increments.**
  OG-2A exposes the completed OG-1 primary controller through an explicit TUI
  proposal/approval/status/cancel/resume flow. OG-2B remains responsible for
  the first automatic read-only fan-out after the operator contract is proven.
- **A saved plan or graph is not consent.** `/orchestrate <goal>` must produce a
  new pending proposal with concrete acceptance criteria, and only
  `/orchestrate approve` may activate it for the current session. Explicit
  `/orchestrate resume` is required after a process/session boundary.
- **The preview remains primary-only.** It adds no automatic delegates,
  publication authority, configuration switch, headless flag, repository
  opt-in, verification waiver, or recursive orchestration.
- **Runtime truth is visible and session-safe.** The TUI carries an
  experimental goal badge, live graph nodes in the Session/context-rail views,
  and bounded whole-graph or per-node inspection. Active graphs cannot switch
  or rewind sessions, terminal graphs reject unrelated prompts until `/new`,
  and proposal consent cannot cross a rewind boundary.
- **The exit gate was executed.** Focused app, controller, TUI, planning, and
  credential-free product-evaluation tests cover the proposal-to-terminal
  path and its negative authority cases. `go test -count=1 ./...`,
  `go test -race -count=1 ./...`, `go vet ./...`, and `go build ./...` passed.
  OG-2B bounded read-only fan-out is now unblocked but has not started.

### 2026-08-01 — OG-1 runtime-owned primary graph controller completed

- **Execution truth moved into a bounded state machine.** A separate
  `internal/goalgraph` package owns at most twelve required logical nodes,
  stable dependency-order readiness, immutable attempt IDs, typed failures,
  machine evidence, two retries/revisions, conservative staleness, and the
  terminal `done`, `blocked`, `cancelled`, or `budget_exhausted` outcome. The
  model can propose an optimistic-concurrency revision or exact blocker but
  cannot replace attempts, evidence, permissions, or runtime state with prose.
- **Primary execution stayed on the existing authority path.** OG-1 schedules
  only the primary agent, filters whole-plan/delegate tools, and uses the same
  registry, permission manager, hooks, sandbox, audit identity, and usage/
  cancellation limits as Standard mode. Controller meta tools change only
  graph scheduling and remain available under restrictive profile tool lists;
  no automatic actor, CLI flag, slash command, configuration field, or
  repository-controlled opt-in was added.
- **Completion is state- and mutation-bound.** Git-backed tokens cover HEAD,
  binary index/worktree diffs, and non-ignored untracked paths, modes, and
  bytes. A successful structured write that leaves the combined token unchanged
  is not work, and any potential mutation requires a recognized passing
  command recorded after it against the current token and mutation generation.
  Read-only graphs remain usable outside Git; write-bearing ones block there.
- **Recovery cannot guess.** Full schema-1 graph snapshots are appended to the
  session. A non-replayable action is fsynced as pending before execution; a
  crash after that boundary restores an explicit reconciliation blocker rather
  than repeating the action. Interrupted reads can use a fresh attempt. Graph
  sessions cannot switch, reset, or rewind beneath the controller.
- **Visibility and compatibility are explicit.** Bounded
  `goal.graph.update` events project into Activity and headless progress and are
  validated by the embedded schema/replay contract. The schema-v1 addition is
  internal-only: Standard streams remain unchanged, older strict replay rejects
  an internal graph trace, and OG-2 must revisit event versioning before real
  automation users can opt in.
- **The exit gate was run, not inferred.** Credential-free product evaluations
  drive a real dependency-ordered read/repair/test, recover from a failed read,
  and prove a denied write never starts. App/session tests restore an ambiguous
  action as blocked; unit tests cover stable selection, provider/tool retry,
  revision/invalidation, no-op/unverified writes, persistence failure before
  mutation, cancellation, budgets, corrupt state, and read/mutation recovery.
  `go test -count=1 ./...`, `go test -race -count=1 ./...`, `go vet ./...`, and
  `go build ./...` passed. OG-2 is unblocked but not started.

### 2026-08-01 — OG-1 implementation wave started

- **The next agentic wave is primary-only.** Work began on a durable runtime
  graph and immutable attempt ledger, deterministic dependency-ready selection,
  state-bound evidence, bounded repair/replanning, conservative invalidation,
  and mutation-safe resume. It adds no automatic actors and exposes no
  `/orchestrate` or CLI mode.
- **Durability is part of the controller rather than an afterthought.** A
  potentially mutating graph action must be recorded and flushed before it is
  executed, so an interrupted action resumes as an explicit reconciliation
  blocker instead of being repeated because its last result record was lost.
- **The user-facing experiment remains OG-2.** OG-1 will be exercised through
  programmatic offline product evaluations, with Standard mode held unchanged,
  before per-session opt-in, graph approval, or read-only fan-out is exposed.

### 2026-08-01 — Orchestrated Goal strategy recorded

- **This records a decision, not a shipped capability.** Phase 6 plan-graph
  execution will proceed as an explicit experimental program named
  Orchestrated Goal. Standard evidence-gated execution stays the default, and
  `/plan` remains a separate read-only surface.
- **The runtime will own execution truth.** The approved design separates the
  user/model-visible logical plan from durable node attempts, readiness,
  evidence identity, freshness, budgets, cancellation, recovery, and terminal
  acceptance. Model-authored prose, a child pass, a candidate score, or plan
  approval cannot grant permission or substitute for combined-parent
  verification.
- **The delivery order is intentionally conservative.** First prove
  dependency-ready primary-only execution and bounded replanning; then expose
  opt-in read-only fan-out; then isolated writer candidates; then reviewed
  integration with fresh combined-parent verification; finally prove
  mutation-safe recovery and decide whether the mode graduates.
- **The strategy is a cross-agent handoff.** The complete authority model,
  state design, non-goals, milestones `OG-0` through `OG-5`, exit gates,
  evaluations, current handoff, and update protocol live in
  [`ORCHESTRATION_STRATEGY.md`](ORCHESTRATION_STRATEGY.md). `OG-0` is complete;
  implementation begins with `OG-1`, and no graph execution is claimed yet.

### 2026-08-01 — Evidence-gated goal completion

- **A final-sounding sentence is no longer a completion signal.** In primary
  execution mode, a tool-free response is checked against the plan active in this turn,
  terminal-step evidence/reasons, tracked writes newer than recognized
  verification, and unresolved tool failures. Informational turns without
  those signals still finish normally; planning mode can still return pending
  execution steps, and a terminal plan from an older turn remains history
  unless updated.
  Delegated work keeps its isolated-worktree review and parent-verification
  contract; the goal-owning primary agent is the controller boundary in this
  first slice.
- **The plan now has completion semantics rather than only display shape.** New
  plan writes require non-empty goals/steps, known acyclic dependencies,
  dependency-ready active/done steps, evidence for done steps, and reasons for
  blocked/skipped steps. Completion assessment also covers restored older plans
  that predate those validators.
- **Verification freshness is runtime state.** Every successful tracked write
  invalidates earlier verification in the turn. A later successful direct
  conventional build/lint/test command restores it; compound/success-masked
  shell commands do not. `verification_note` records the exceptional case
  where no meaningful automated check applies and is labelled model-authored,
  not machine evidence.
- **Recovery is required but bounded.** A failed tool must be followed by a
  successful tool path or an explicitly blocked plan step. The controller
  injects at most two deterministic continuation notices, each charged to the
  existing iteration/token/cost limits, before ending blocked. The ordinary
  iteration ceiling is now a budget-exhausted outcome.
- **Automation can distinguish process status from goal status.** Schema-v1
  `run.result.status` remains `ok`/`error`/`cancelled`; the additive `outcome`
  field reports `done`/`blocked`/`cancelled`/`budget_exhausted`. Replay validates
  their pairing and remains compatible with older traces that omit `outcome`.

### 2026-08-01 — Launch to a verified session

- **A fresh installation is now honestly unconfigured.** `config.Defaults()`
  and `collo init --global` no longer name Ollama, localhost, qwen, or any other
  provider/model combination that has not been observed. Settings without a
  provider remain loadable for setup and diagnostics; a session request is the
  boundary that requires one.
- **Interactive startup now completes the setup journey.** Running `collo` with
  no configured provider enters the existing probe/discover/two-request verify
  flow and, after the verified configuration is written, continues directly
  into the session. Cancellation writes nothing. Headless commands never
  prompt and instead return a configuration-class error pointing to
  `collo setup`.
- **Setup is a reusable provider configuration surface.** The interface is
  labelled “provider setup,” configured providers appear as actions for model
  changes and re-verification, and `collo setup --provider <name>` remains the
  direct shortcut. Adding a provider still cannot silently steal the default.
- **The immediate Linux session does not lose the key just verified.** Where no
  OS credential store exists, a manually entered key is passed directly into
  only the runtime opened after setup. It is not serialized and is not placed
  in the process environment; the recorded environment variable remains the
  contract for later sessions.
- **Diagnostics and documentation now agree with the runtime.** `collo doctor`
  reports a provider-free configuration, and installation, feature, capability,
  and provider documentation no longer describe the old Ollama/qwen assumption
  as an installed default.

### 2026-07-31 — Phase 4 first-run setup, and verification that actually verifies

- **The first-run path was four manual steps and a guess.** Read the README,
  hand-write JSONC, set a credential, run `doctor`, and work out which of the
  four was wrong. `collo init` wrote a *static* starter naming
  `ollama`/`qwen3-coder`/`127.0.0.1:11434` as literal values, and
  `config.Defaults()` returned the same assumption for a machine that had never
  run Ollama. Nothing dialled anything. `collo setup` now probes the runtimes
  that are actually listening, reads each endpoint's own catalog, and offers
  Azure and Bedrock as forms, because neither is discoverable from a name and a
  key: Azure addresses a *deployment* inside a resource you name, and Bedrock
  resolves an identity through the AWS credential chain and grants model access
  per region. Both go through the same verification.
- **Verifying without tools verified the wrong thing.** The first design sent
  one request, reasoning that a single request isolates a single cause. It does,
  and it also let `gemma3:270m` verify cleanly and then fail the user's first
  real prompt with "does not support tools" — the exact failure the package
  exists to move earlier. Found by running the wizard against a real Ollama and
  then running a real session, not by a fixture. Verification is now two
  requests: one plain completion, and one carrying a tool definition. The cause
  stays isolated *and* the promise holds. Requiring an actual tool *call* would
  have been worse — a capable model may answer a trivial prompt with text.
- **An omitted context window silently disables compaction.** The design said to
  leave `context_window` out when the endpoint does not publish one. There is no
  adapter default, and `Agent.shouldCompact` returns false on a zero window, so
  the omission would have written a configuration whose auto-compaction never
  ran. It now assumes 32768 and labels the assumption on the confirmation screen
  rather than presenting a guess as a measurement.
- **A silent input truncation was diagnosed as a permissions problem.** A
  reported Bedrock failure — "Missing required parameters in the API Key", with
  a correct key, region, and model — traced to `CharLimit = 512` on the wizard's
  own text input, which truncates on both `SetValue` and paste. In a field that
  deliberately does not echo, a cut-off key is indistinguishable from a whole
  one. Alongside it, the 403 branch asserted "the credential is valid, but not
  allowed to use this model" while printing AWS's contradicting message directly
  beneath it. Both fixed: no character limit, `SanitizeSecret` for whitespace
  and quotes acquired in copying, a visible character count (a length is not a
  secret), and a 403 that reads the endpoint's own message before deciding which
  of the two quite different causes to name.
- **SigV4 failures were answered with API-key advice.** Bedrock's two credential
  families fail for unrelated reasons, and a chain that resolves nothing never
  reaches AWS at all — so "the endpoint rejected the credential" was wrong about
  where the failure happened, and there is no key to check in that mode. The
  diagnosis now names IAM variables, profiles, `aws sso login` for an expired
  Identity Center session, and `aws sts get-caller-identity`. `auto` with no
  token counts as the chain, since the user never typed the word "sigv4" and
  would not connect the failure to it.
- **The documentation guards were passing vacuously.** Writing a new guard
  exposed that it passed against deliberately broken documentation, because the
  token it searched for appeared incidentally elsewhere in the file. Six
  existing guards had the same defect. All are now scoped to the smallest
  enclosing heading section and matched on whole words, and each was verified by
  mutation — breaking the documentation and confirming the guard fails. The
  recipe is written down in `docs/TESTING.md`, because a guard nobody has seen
  fail is not evidence of anything. Section anchors are unique headings rather
  than body strings: the first attempt anchored on `~/.aws/credentials`, which a
  later section legitimately mentioned, and the guard then failed against
  correct documentation.
- **A 32-token verification budget failed every reasoning model.** Reported
  against LM Studio serving `qwen/qwen3.5-9b`: the model spent the whole budget
  thinking, returned an empty visible answer, and the wizard reported a model
  LM Studio was actively serving as "not actually served behind" the gateway. It
  needs about 170 tokens to reach the word "ok". The budget is now 1024, which
  costs nothing on a model that answers directly, and an empty reply is read
  rather than assumed: reasoning present means the route is proven, truncation
  at the ceiling is stated as a limit rather than blamed on the model, and only
  a complete, genuinely empty 200 keeps the original diagnosis.
- **One parameterless tool broke every request against LM Studio.**
  `git_status` declared `{"type":"object","additionalProperties":false}` — valid
  and complete JSON Schema. LM Studio requires `properties` regardless and
  rejects the whole request rather than the one tool, so a single tool made the
  entire session unusable, identified only by a numeric index. The declaration
  is fixed and a guard covers every builtin, but the durable fix is in the
  adapters: tools arriving over MCP are written by somebody else and cannot be
  corrected at the source, so an object schema is normalized before it goes on
  the wire. The normalization only ever adds an absent key, never modifies a
  declared one, and a cross-adapter test asserts an ordinary tool reaches every
  provider byte-identical to what it declared — the guard against a fix for one
  strict server damaging the others.

### 2026-07-30 — Phase 1 publication and deployment as their own decision

- **The risk model was destruction-shaped.** `internal/shell/safety.go` is, end
  to end, a taxonomy of things that delete: `classifyRM`, the Git
  reset/clean/reflog/gc branches, `classifyAWS`'s `s3 rm`, and the
  Terraform/Kubernetes/Helm destruction branches. Nothing in it classified an
  action that *puts something out into the world*. Measured against the built
  binary at `4abdb9a` with `collo policy check --autonomy autopilot`, on a stock
  configuration: `terraform destroy` → confirm but `terraform apply
  -auto-approve` → **allow**; `kubectl delete` → confirm but `kubectl apply` →
  **allow**; `helm uninstall` → confirm but `helm upgrade` → **allow**;
  `git push --force` → confirm but `git push origin main` → **allow**. Also
  allowed silently: `npm publish`, `cargo publish`, `twine upload`,
  `docker push`, `gh pr create`, `gh pr merge`, `gh release create`,
  `aws lambda update-function-code`, and `ssh prod "systemctl restart app"`.
- **The asymmetry did not track reversibility.** A published package version is
  harder to take back than a Kubernetes deployment a controller will recreate.
  And `ROADMAP.md`'s own "Explicitly deferred" list already said "Autonomous Git
  commits, pushes, pull requests, deployments, or issue updates by default" —
  the documented intent and the binary disagreed, which is the same shape as the
  audit ledger the previous wave fixed.
- **The analyzer already knew.** `npm publish` reported `endpoints:
  UNDETERMINED (npm publish contacts endpoints chosen by configuration)`;
  `publish` and `push` sat in `fetchingSubcommands` beside `install`. The
  knowledge was being spent on endpoint reporting rather than on risk.
- **Nothing a user could adopt closed it.** `permissions.network: "scoped"`
  does catch every case — and also prompts on `npm install`, `pip install`, and
  `go mod tidy`, with no rule able to buy those back. That is the same
  all-or-nothing ergonomics problem the scoped-egress wave was written to escape
  one layer down.
- **The rule language could not express the exception, and lied about it.**
  `permissions.rules[].command` is an *executable-name* glob while
  `permissions.denied_commands` in the same block is a full-command-line regex.
  `{"action":"deny","command":"npm publish"}` therefore matched **nothing**,
  and `collo config validate --strict` reported "Configuration is valid" — the
  third instance of the inert-matcher defect, after the `host` matcher and the
  hand-built action in `collo policy check`.
- **Operations came first, because a tier with no expressible exception is a
  tier people switch off.** A `command` pattern containing a space is now
  matched against the operation — the executable plus the leading words that
  decide what it does. `internal/shell/publication.go` derives one operation per
  recognized invocation for the subcommand-driven tools, plus `<executable>
  <host>` for the ssh family so a rule can name one build host rather than every
  host. `policy.NamesOperation` is the single definition of which form a pattern
  is, read by both the matcher and the permission layer. A pattern that could
  match neither form now fails validation.
- **`collo policy check` prints the vocabulary.** An `operations:` line was
  added because the discoverability failure is what made the inert rule
  dangerous: a user who has to guess the pattern writes one that silently
  matches nothing.
- **`permissions.publication` (off/prompt/deny, default prompt)** classifies six
  categories — package registry, container registry, source remote, code forge,
  infrastructure, remote host. It is modeled on `protect_credentials` rather
  than on tier 2: straight tier 2 offers no durable answer at all, which is
  right for `npm publish` and wrong for `git push` on a feature branch, and a
  control that is wrong half the time gets disabled. Autopilot, a tool-wide
  "always allow", and an executable-only allow rule never cover it; a rule
  naming the operation does, and one narrow session grant covers exactly the
  operation shown.
- **Read verbs and rehearsals stay ordinary.** `gh pr view`, `kubectl get`,
  `terraform plan`, `aws s3 ls`, `docker pull`, `npm install`, and a
  download-direction `rsync` or `aws s3 sync` are not publications;
  `--dry-run`/`--what-if`/`--noop` suppress the classification and
  `--dry-run=false` does not. The control is only worth having if it is quiet
  during the work an agent does all day.
- **Two defects the first implementation shipped with, both found by widening
  the probe rather than by reading the code.** `ssh prod "systemctl restart
  app"` passed because the publication check sat *behind* the operation lookup,
  and ssh has no subcommand — so every tool without a verb was silently exempt.
  And `operationWords` skipped unrecognized options without stopping, so
  `aws lambda update-function-code --function-name f` read the flag's value as
  the verb and produced the operation `aws lambda f`, while `gh api -X POST`
  produced `gh api post`. Both yielded plausible-looking operation strings that
  matched nothing, which is why neither failed loudly. The subcommand path now
  ends at the first unrecognized option, and `kubectl -n prod apply` no longer
  reports its verb as `prod`.
- **Rules and grants are deliberately not equivalent, and that was already
  true.** An operation-naming `allow` rule outranks `publication: "deny"`,
  because it is written down, inspectable, and survives review; an interactive
  grant never does. The credential gate has behaved this way since it shipped
  and it was undocumented — a test written against the opposite assumption is
  what surfaced it. Both are now stated in `docs/SECURITY.md` rather than
  inferred.
- **Carried on the preset ladder and clamped like every other containment
  field:** frictionless off, standard prompt, hardened deny; a project may raise
  it and never lower it, and a refusal is reported. Reported in `collo doctor`,
  `collo policy check`, and the Session tab's Security block, with its own
  header and accent in the approval dialog.
- **Tested where the property actually lives.** 8 classifier tests including a
  symmetry check that fails when a tool gains a destructive classification
  without its publishing counterpart; 6 policy tests pinning that an operation
  pattern never falls back to an executable; 12 permission tests; 5 config
  tests; and 3 offline evaluations, because a unit test proves the string is
  recognized while only an end-to-end autopilot turn proves the mode whose
  purpose is not asking actually stops.

**Behavior change:** two, both documented in
[COMPATIBILITY.md](COMPATIBILITY.md). Publishing, deploying, and pushing now
prompt by default including under `autopilot`, where a headless run fails
closed; `"publication": "off"` restores the earlier behavior exactly. And a
`command` rule pattern containing a space, which previously matched nothing,
now matches an operation — so an upgrade may activate a denial its author
believed was already in force.

### 2026-07-30 — Phase 3/7 Windows pseudoconsole

- **One backend was withholding two advertised capabilities.** `ptySupported`
  was false in `internal/tools/command_pty_windows.go`, so `run_command` with
  `pty: true` was refused; the same absence in
  `internal/webterminal/session_windows.go` made `collo --web` unavailable on
  Windows entirely. The roadmap had listed this once, under Phase 3, as though
  it were a single tool flag.
- **os/exec structurally cannot do it.** `syscall.SysProcAttr` on Windows
  exposes `CreationFlags`, `Token`, `AdditionalInheritedHandles`, and
  `ParentProcess` — but no proc-thread attribute list, and a pseudoconsole is
  attached only through `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE` on a
  `STARTUPINFOEX`. `creack/pty`, already a dependency, returns `ErrUnsupported`
  from its `start_windows.go`. So process creation, waiting, cancellation, and
  job assignment all had to be written directly against
  `golang.org/x/sys/windows`, which already exports the entire API —
  `CreatePseudoConsole`, `ResizePseudoConsole`, `ClosePseudoConsole`, and the
  attribute constant — so no new module was added.
- **Shared rather than duplicated, because the risk is handle lifetime.** Both
  callers needed the same primitive, and the API calls are the easy part.
  `internal/conpty` owns the ordering that two copies would each have had to
  rediscover: the parent's copies of the console's input-read and output-write
  handles must be closed immediately after `CreatePseudoConsole` duplicates
  them, or the output pipe still has a writer and a finished command never
  reaches end-of-file — which presents as a PTY command that hangs after it
  has already printed everything.
- **The cancellation contract came out stronger than the one it matched.** The
  child is created with `CREATE_SUSPENDED`, assigned to a job object carrying
  `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`, and only then resumed, so no descendant
  can be spawned before the job exists. The ordinary `taskkill /T` path does
  have that window. The existing `openDescendants`/`waitAllExited` walk is
  reused unchanged to wait out the teardown, because returning before the
  kernel has released those processes leaves them holding the workspace
  directory open — the defect that walk was written for.
- **Windows has no SIGTERM, and the docs say so instead of implying parity.**
  `GenerateConsoleCtrlEvent` requires the sender to share the target's console,
  which a pseudoconsole host by definition does not, so there is no signal to
  send. The graceful step for an interactive session is closing the child's
  console input; a program blocked on input can act on it and one that ignores
  input cannot. It is documented as a request with a deadline.
- **`go vet` shaped one line of the implementation, and the comment says which
  part is presentation.** `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE` takes the
  console handle by value in the `lpValue` slot — Microsoft's own sample passes
  `hPC`, not `&hPC` — so the natural spelling is a `uintptr`-to-
  `unsafe.Pointer` conversion, which vet rejects and CI vets on the Windows
  runner. The bits move through the handle's own address instead. That is
  presentation, not extra safety: the result is still an `unsafe.Pointer`
  holding a non-pointer, sound only because a HANDLE is a small integer the
  collector will never mistake for a heap address.
- **Two tests were skipped for a reason that was about to become false.**
  `TestRunCommandPTY` and `TestRunCommandPTYTimeoutKillsGroup` said "pty
  unsupported on windows"; the only genuinely platform-specific part was the
  shell. They now run everywhere. Windows has no `test -t 0`, so the terminal
  assertion is that a pseudoconsole renders its client through a virtual
  terminal while a captured pipe does not — the difference programs actually
  key on when deciding whether to colorize. That was the assertion least
  certain to hold without a Windows machine, and it passed on first run.
- **Verified on Windows 11, not only in CI.** The eleven `internal/conpty`
  integration tests and both tool-level PTY tests passed on a real VM. The
  close and terminate tests carry explicit timeouts whose failure messages name
  the ordering mistake that would cause them, because a hang rather than a
  wrong value is how handle-lifetime errors present; both finished in
  hundredths of a second.

### 2026-07-30 — Phase 8 a trustworthy audit ledger

- **A write-only file with no reader, no test, no bound, and no failure
  signal.** `internal/audit` was 5 functions and 116 lines carrying the claim
  the README leads with — "every permission decision and execution outcome…
  so privileged actions are reconstructable" — and the coverage profile read
  0.0%. Its only test was one happy-path case written from
  `internal/permission`. Nothing in the product read the file back: `collo
  replay` excludes audit JSONL by documentation, support bundles exclude its
  content, and there was no `collo audit`. Reconstruction meant locating
  `~/.collomia/audit/<name>-<12-hex>.jsonl` and parsing JSONL by hand.
- **The silent hole was the defect that mattered.** `Append` ended with
  `_, _ = file.Write(...)` after an `if err != nil { return }` on the open, so
  an unwritable ledger, a full disk, or a short write produced a file that
  still read as complete — the worst failure mode a record has, because a
  reader cannot distinguish it from nothing having happened. The fix is not
  fail-stop: refusing work the user already authorized because a record could
  not be filed is the wrong trade, and the session store's guarantee is not the
  right one here. It is fail-visible. Failures are counted; the first is
  reported to the session once (not once per authorized action, which a full
  disk would turn into a flood of identical warnings); and the next entry that
  reaches disk is preceded by a `gap` record naming how many entries were lost,
  when the loss began, and why. The gap and the resumed entry are written as
  one buffer — a gap marker that itself failed to persist would leave exactly
  the hole it exists to prevent. `docs/SECURITY.md` previously grouped this
  with the debug log as a "best-effort diagnostic"; the two are now described
  separately, because they no longer behave the same way.
- **Nobody could tell which agent acted.** Entries carried no session, actor,
  or task field. One workspace ledger receives writes from the primary agent,
  from every concurrently scheduled delegated agent, and from any other `collo`
  process open on that directory, so those streams interleaved into something
  no reader could separate again. The approval dialog did prefix "delegated
  agent X (id)" — but onto a *display copy* of the request, so what the ledger
  recorded was the unprefixed summary, indistinguishable from the primary's.
  Identity now lives on the `Ledger` (`Identify(session, actor, task)`) rather
  than on each `Append` call, so the two `Append` sites in the permission layer
  did not have to change and cannot forget it.
- **A child could run completely unaudited.** Both delegated-agent construction
  sites did `if ledger, ledgerErr := audit.Open(workspace); ledgerErr == nil`
  and dropped the error, while the primary path at `app.go` turned the same
  failure into a startup warning. `Agent.attachLedger` is now the single site
  owning redaction, the failure route, and the three identity fields together,
  and `TestAuditLedgerHasOneAttachmentSite` fails on a third caller of
  `audit.Open` — the same source-scraping guard shape that pinned single-site
  command-runner construction after the same defect produced a containment
  field present in the primary session and silently absent for delegates.
- **Completeness is printed before the entries, not after.** `collo audit`
  states any declared gap, any line that would not parse, and any generation
  discarded at rotation ahead of the first entry, because someone
  reconstructing an incident has to know the record has holes before drawing
  conclusions from what is in it. `collo doctor` gained the matching check: it
  previously verified only that the ledger *directory* could be created, which
  answers whether a ledger can be opened and not whether the one already on
  disk is intact. The session latches its own failure count on the runtime and
  the Session tab's Security block reports it, marked like degraded sandboxing
  rather than as an ordinary row — a startup warning has been dismissed by the
  time the question is asked.
- **Bounded, and honest about the bound.** The ledger grew without limit while
  every other retained artifact in the project is bounded. It now rotates at
  64 MiB keeping exactly one previous generation, and the new file opens with a
  `rotation` entry that says when an older generation had to be discarded. A
  record that quietly shortened itself would be the silent-hole defect again in
  a slower form; `Report.Complete()` is false when a generation was dropped.
- **From 0.0% to 83%, and the smoke test still found two more.** The new suite
  covers redaction of every field that can carry a secret (and that redaction
  no longer rewrites the caller's `Resources` slice, which the approval dialog
  shares), four concurrent writers through separate handles producing zero torn
  lines, declared and detected incompleteness, rotation, the filters, and the
  failure and recovery reports. Running the real binary against a fixture
  afterwards found two defects the unit tests had not: `omitempty` does not
  omit a zero `time.Time`, so every ordinary entry was emitting
  `"since":"0001-01-01T00:00:00Z"` (the field is now `*time.Time`), and the
  integrity summary printed "1 declared gaps".

### 2026-07-29 — Phase 2 coupled conversation-plus-workspace checkpoints

- **Two halves that never met:** `session.Store.Rewind` branched the
  conversation and documented that the workspace was untouched;
  `diffmodel.Tracker.Undo` reversed exactly one file mutation and knew nothing
  about turns. Both were solid, and the gap between them was the feature: after
  a rewind the transcript described a tree that no longer matched it, and the
  only route back was `/undo` pressed an unknown number of times. `/restore
  [turn]` now does both as one operation.
- **The tracker learns turns from the funnel that already exists:**
  `Runtime.LogEvent` is the single site every surface — TUI, headless runner,
  browser terminal — reports events through, and the session already counts
  turns from the `turn.end` it carries. The tracker's `CompleteTurn` hangs off
  the same event rather than off the agent loop, so there is no second place a
  boundary could be missed, and each recorded `Snapshot` carries the turn it was
  made during. Position in history would not have done: a resumed session's
  numbering starts above zero.
- **Verification runs before the conversation branches, and that ordering is
  the design.** `Tracker.VerifyRestore` is a dry run; `RestoreCheckpoint` calls
  it first and only then creates the branch. A drifted file discovered *after*
  the branch existed would leave a conversation that had moved alone — exactly
  the split the wave exists to close. This is pinned by an experiment rather
  than by assertion: disabling the pre-check makes
  `TestRestoreRefusedByDriftLeavesBothHalvesUntouched` fail on the branched
  session, so the guard is demonstrably load-bearing.
- **A refusal names every file, not the first one.** `DriftError` carries the
  complete sorted list, and the TUI renders it as a titled panel with the paths
  workspace-relative. Stopping at the first drifted file would send the user to
  deal with it and then hit the second — the same trap as a partial restore,
  arrived at more slowly.
- **One write per file, not one per mutation.** The plan collapses every
  mutation of a path into a single reversal: the newest recorded `After` is what
  disk must hold, the oldest `Before` is what gets written. Replaying twenty
  mutations backwards would be twenty chances to stop halfway, and the
  intermediate states are not states anyone asked to see. A file the agent
  created is removed; one it deleted comes back with its original permission
  bits; a file the *user* recreated where the agent deleted one reads as drift
  rather than as something to overwrite.
- **The limits are reported, not hidden.** Change tracking is in memory, so a
  restore to a turn belonging to a resumed session says "No tracked file changes
  needed reversing" instead of implying it rewound writes it never observed —
  and `alignChangeTurns` points the tracker's numbering at the session's
  completed turns on startup, resume, switch, and rewind so a turn number means
  the same thing to both halves. External effects — commands, installs, network
  calls, deployments, MCP effects — are never reversed, and every success
  message says so.
- **The picker says what a choice costs:** `PendingSince` reports files and
  mutations without touching disk, so each entry reads "reverses 3 changes
  across 2 files". A turn number conveys none of that, and this is a decision
  people should be able to make from the list.
- **`/rewind` is unchanged**, and now points at `/restore` for the coupled form.
  Altering what an existing recovery command does to the workspace is the last
  place to surprise anyone. No behavior change ships in this wave.
- **Sixteen new tests**, split between the tracker (turn attribution, collapsed
  reversal, create/delete/mode round trips, external deletion and recreation as
  drift, refusal writing nothing and staying retryable, turn alignment with an
  empty history, and a session diff that afterwards shows only what survived) and the coupled surface (both halves moving together, both
  halves staying put on drift, the picker's cost text, the resumed-session
  honesty, and `/rewind` keeping its own limits). No CLI `collo sessions
  restore` was added: the tracker is process-local, so a store-only command
  could restore the conversation alone while appearing to do both.

### 2026-07-26 — Phase 7 first screen, modal dimming, and rail-aware wrapping

- **The logo was never centred, and the reason was one long line:** the identity
  under the wordmark was a single string — version, commit, build timestamp,
  provider, model, theme — that ran past a hundred columns. `centerBlock`
  centres a block by its widest line, so that line set the indent for the whole
  header and the twenty-two-column wordmark sat flush against its left edge.
  The first prompt then replaced the screen with the compact left-aligned
  banner, which is why the logo appeared to jump. It is now two short lines,
  each centred against the other, and nothing on the first screen is wide enough
  to drag the art off centre.
- **The first screen has a title rather than a caption:** a five-row wordmark in
  the same double rule as the compact banner, with a four-petal blossom set two
  columns to its left — a mark before the name, the way `✿ collo` and
  `✿ COLLOMIA` are already written elsewhere in the interface, and without the
  six extra rows that stacking it above would have pushed onto the card and the
  openers. The gradient is raked across the assembled block rather than restarted
  for each piece, which is why the art is padded to a rectangle first: `gradient`
  blends per line, so ragged lines finish the blend at different columns and the
  block comes out striped. Below 59 columns the blossom drops, below 47 the
  compact wordmark stands in, and below 56 the whole card gives way to the
  banner and one hint as before.
- **The openers hang off the card:** they took the card's indent instead of
  being centred on their own narrower width, which had them floating a few
  columns to its right.
- **The transcript header is one short line again:** the compact wordmark, left
  and top as before, over `✿ collo <version> · <provider>/<model>`. The commit,
  the build date, and the theme name left it — a header read at a glance should
  not be the widest thing on screen — and are now on the first screen, on the
  Session tab's new `build` row, and in `collo version`. Two golden screens moved
  by exactly that one line.
- **Regression tests, not screenshots:** one asserts the wordmark and the
  identity line carry equal margins on both sides, which is the defect itself;
  another asserts no banner line exceeds the body width and that build detail
  stays out of it.
- **Modal dimming is now a preference:** a dialog drops colour from everything
  behind it so the decision in front of the user is plainly the focused element,
  which is right for using the tool and wrong for photographing it — the
  screenshots that go into product documentation are exactly the frames where
  the interface should be at full saturation. `options.dim_background` defaults
  to true and turns the scrim off when set false. The cleared gutter around a
  dialog is deliberately not part of the option: it is what keeps the border
  from sitting against mid-word transcript fragments, so nothing about reading
  a modal depends on the dimming being on. Tested at both settings.
- **The transcript now wraps to the transcript, not to the terminal:** the
  context rail is composited over the body row by row, so a line wider than the
  body was not scrolled off — it was cut at the rail's left edge and the tail
  was gone. Glamour was being told to wrap answers at the terminal width, and
  prompts, system lines, error lines, tool output, and `/status`-style panels
  were not folded at all. All of them now measure against `bodyWidth()`, which
  the tool-call header already used: prose is word-wrapped with a hard break for
  a word longer than the measure (a pasted path or URL), tool output is
  hard-wrapped inside its four-column gutter, because a reflowed column of
  command output misleads in a way a break at the right margin does not. This
  was never only a rail defect — a long single-line prompt was cut at the
  terminal's edge with no rail in sight — but the rail is what made it constant.
  The regression test renders one long line as all six block kinds, asserts no
  line exceeds the body, and asserts every word survives the fold in all six.

### 2026-07-26 — Phase 3 language-server capability reporting

- **A missing capability is a configuration answer, not a protocol string:**
  language servers differ in what they implement, and pyright — the
  auto-detected Python default — deliberately ships no formatter. `format_file`
  against it failed with the server's own words, `Unhandled method
  textDocument/formatting`, which tells a user nothing about what to do. The
  client now returns a typed `lsp.ProtocolError` carrying the JSON-RPC code, and
  a method-not-found answer becomes a sentence naming the server, the capability
  it lacks, and the `lsp.<language>` setting to change. A test pins both halves:
  the explanation, and that every other protocol error passes through unrewritten
  — a server that is merely broken is not a server that lacks the capability.
- **Verified against real Python servers, not from memory:** `pylsp` answers
  definition, references, and formatting; `pyright` answers definition, and
  reported an unresolved import that `pylsp` did not. Neither is strictly
  better, so the default is unchanged and the trade-off is documented instead.
- **The wait is now accounted for:** `find_definition`, `find_references`,
  `format_file`, and `diagnostics` stream `starting <server>…` followed by
  `<server> ready in <time> — <what it is doing>…`. Reporting server startup
  separately from the request is the point: a warm server answers in a second,
  while a cold gopls or rust-analyzer indexing a large repository can consume
  most of the 60-second budget, and a motionless spinner is indistinguishable
  from a hang. The lines go through the existing streaming path used for live
  command output, so they are display-only and never enter the string returned
  to the model — a regression test asserts both halves — and the transcript
  replaces the streamed view with the real result, leaving nothing behind.
- **Two first-test traps are now written down:** a project `.collomia.json`
  carrying an `lsp` map does nothing until `collo trust`, because the
  quarantined layer falls back to the auto-detected default and looks like a
  configuration being ignored; and the `uv` recipe for a Python server that can
  do all three (`python-lsp-server` with a formatting plugin) is spelled out.

### 2026-07-25 — Phase 4/3 first-run and code-intelligence wave

- **A key no longer has to live in a dotfile:** `collo auth set|list|status|rm|
  import` keeps a provider API key in the macOS Keychain or the Windows
  Credential Manager. The value is prompted for without echo, or read from
  standard input when that is not a terminal, so it never becomes a
  command-line argument or a shell-history entry, and nothing prints a stored
  value back to a user, a log, or a tool result.
- **Nothing about an existing setup changes:** the store is consulted only
  after `api_key`, `api_key_env`, and a provider family's own variable such as
  `AWS_BEARER_TOKEN_BEDROCK`, so an exported environment variable always wins
  and a stored key cannot silently shadow one. Delegated authentication modes
  are excluded outright — Entra issues short-lived tokens and SigV4 draws on
  the AWS chain, so there is nothing static to store — while Bedrock bearer
  keys and Azure `api_key` are ordinary keys and can be. A configuration test
  pins the whole precedence table, and a cross-package test fails if the
  Bedrock variable name drifts between the two packages that both name it.
- **A user who does not use the feature never pays for it:** a local name index
  is checked before any platform call, so a machine that has never run
  `collo auth set` performs no credential-manager call at all. On macOS that is
  the difference between silence and a keychain dialog. The index records names
  only, is written 0600, and is explicitly not a fallback store.
- **No Linux backend, and no encrypted-file substitute:** Secret Service needs
  a D-Bus session with gnome-keyring or kwallet, normal on a desktop and absent
  on the headless hosts an agent most often runs on. A passphrase-protected
  file would only move the secret; an unencrypted one would be weaker than the
  environment variable it replaced. `collo auth` and `collo doctor` state the
  absence and point at `api_key_env` instead of degrading quietly. macOS uses
  Apple's signed `/usr/bin/security` rather than linking Security.framework, so
  the binary stays cgo-free and keychain authorization is not re-requested for
  every unsigned build; Windows uses `CredWriteW` through the already-present
  `golang.org/x/sys`. No dependency was added for any of it.
- **Where a credential came from is now answerable:** `collo auth status` names
  the winning source per provider, `collo doctor` reports the store and repeats
  the source on each provider line, and an entry the operating system no longer
  holds is marked missing rather than listed as if it worked.
- **Type-aware navigation:** `find_definition` and `find_references` join
  `diagnostics` on the existing language-server client. They take a file, a
  1-based line, and the symbol text as it appears on that line; the column is
  derived. That is the ergonomic difference between this and a raw LSP call —
  the protocol counts columns in UTF-16 code units, and a model asked to count
  them returns a confident answer about the wrong token. A symbol absent from
  the named line is an error, not a guess.
- **The project's own formatter:** `format_file` applies the language server's
  formatting as an ordinary write — same approval, recorded by the diff
  tracker, visible in `/diff`, reversed by `/undo` — and refuses to write if
  the file changed while the server was formatting it. The approval carries no
  diff preview, deliberately: producing one means running the server twice per
  approval and the second result can still differ.
- **Position arithmetic is tested where it breaks:** UTF-16/byte conversion
  round-trips through non-ASCII and astral-plane characters, edits apply from
  the end backwards, overlapping edits are rejected rather than mangling a
  file, end-of-document appends are legal, and CRLF line counting survives. A
  scripted fake server asserts the client sends the position it meant to;
  `gopls`, when installed, exercises definition, references, and formatting end
  to end.
- **Code actions were left out on purpose.** Organize-imports and quick fixes
  need `codeAction/resolve` round trips and workspace edits that can span
  files. A half-working mutation path is worse than an absent one, so the
  Phase 3 item stays open rather than being marked complete.

### 2026-07-25 — Phase 7 terminal experience wave

- **The composer grows with the draft:** a single-line prompt still occupies a
  single line, and the editor expands only once there is something to show,
  with the transcript's height re-derived on every keystroke that adds a row.
  A draft that is visibly unfinished — one ending in a backslash, or sitting
  inside an unclosed fence — extends on Enter instead of sending, because most
  users never discover a newline chord and the common way to write a
  multi-line prompt has to be the obvious key. `alt+enter` and `ctrl+j` insert
  a newline everywhere (`ctrl+j` is a literal line feed and survives terminals
  that swallow `alt`); terminals speaking the Kitty keyboard protocol or
  xterm's `modifyOtherKeys` additionally get `shift+enter`/`ctrl+enter`, which
  arrive as CSI sequences Bubble Tea 1.x does not decode into a `KeyMsg` and
  are therefore intercepted ahead of the type switch. `alt+e` hands the draft
  to `$EDITOR` and takes it back.
- **An optional context rail:** `alt+r` draws a persistent panel carrying the
  workspace and branch, the plan with per-step status, delegated agents,
  changed files, and background processes — everything the runtime already
  tracks, placed where reading it does not mean leaving the transcript. It
  appears on its own at 146 columns, is unavailable below 116, and borrows its
  columns from the transcript only: narrowing the composer would punish the
  user for opening a reference panel by shrinking the thing they type into. A
  deliberate toggle is remembered so a later resize does not overrule it, the
  workspace block is always present so an idle rail does not read as a panel
  that failed to load, and content is trimmed rather than allowed to push the
  composer off screen — the Session tab remains the complete record.
- **An opening screen instead of a blank one:** an orientation card replaces
  the empty transcript, degrading to the banner and one honest hint below the
  width at which its label and value columns stop reading as pairs.
- **Tool calls carry their outcome:** transcript tool headers gained status and
  elapsed time as the turn runs. A session replayed from disk leaves both zero
  — the transcript records what a tool did but not how long it took, and
  inventing a duration there would be worse than omitting one.
- **Mouse reporting, and a way to give it back:** `options.mouse` (default
  `true`) requests wheel scrolling and click-to-select tabs, consuming only the
  wheel and a plain left click so drag, motion, and every modifier are left to
  the terminal's own selection. Because mouse reporting and terminal
  drag-selection are mutually exclusive by protocol, `alt+m` releases and
  reclaims the mouse mid-session with a status notice and a `SELECT`/`MOUSE`
  badge, so copying arbitrary text never requires editing configuration or
  restarting. Documented alongside the shift-drag and macOS option-drag
  escapes.
- **Modals stop looking like a corrupted redraw:** the base layer is dimmed by
  dropping color entirely rather than blending — syntax highlighting, diff
  greens and reds, and status accents at full saturation kept pulling the eye
  back to content that was not actionable — and a gutter of blanked cells is
  cleared around the dialog, with a narrow leftover strip cleared as well
  rather than left as one orphaned character per row.
- **Approval diffs are tinted by the highlighter itself:** Chroma paints the
  added/removed wash as part of the style, because a background emitted
  separately does not survive the SGR resets Chroma writes between tokens and
  the tint used to stop at the first keyword. Only previews containing a hunk
  header are treated as diffs, so a command preview whose line happens to start
  with a hyphen is not tinted.
- **Gauge resolution:** partial block glyphs give the context bar eight times
  the resolution of its width, which matters at the ten cells the status bar
  can spare — without them the gauge sat at zero for the first five percent of
  the window and then jumped a whole cell.
- **Configuration, keys, and goldens:** `options.mouse` joins the starter and
  reference configuration; `compose_editor`, `context_rail`, and `toggle_mouse`
  join the validated named keybindings, deliberately bound to `alt+e`/`alt+r`/
  `alt+m` because the mnemonic `ctrl` letters are emacs motions the composer's
  textarea already binds and a global handler would shadow them. The user guide
  documents every new key and the selection trade-off, and the cross-platform
  golden screens were regenerated for the new chrome.

### 2026-07-25 — Phase 1 credential-file protection wave

- **A blanket approval no longer quietly includes a private key:**
  conventional credential locations — SSH and GPG private keys, cloud CLI token
  caches, registry authentication files, environment files — are recognized by
  path, with public keys, `known_hosts`, and example environment files excluded
  explicitly rather than by luck. Recognition is by convention, not secret
  detection, which makes it a usable default; enforcing what a running process
  may read remains sandbox read confinement's job.
- **Detection is keyed on the argument, not the program:** shell analysis
  reports the credential stores a command's arguments name, so it does not
  depend on a table of known reading programs, and any tool that declares its
  paths derives the same.
- **Its own decision:** `permissions.protect_credentials`
  (`off`/`prompt`/`deny`, default `prompt`) is evaluated where a blanket allow
  rule, a tool-wide session grant, the implicit in-workspace read path, and
  autopilot cannot cover it, while a rule naming the path still can. It rides
  the preset ladder (frictionless off, standard prompt, hardened deny) and
  clamps monotonically like every other containment field, and it is reported
  in the Session tab's Security block and in `collo policy check`.
- **Redaction caught up:** PEM private key blocks and the remaining common
  provider token shapes are redacted, and the package documentation and
  SECURITY.md now state plainly that redaction does not sit between a tool
  result and the provider.
- **One constructor for command-shaped actions, with a test that fails on a
  second construction site.** This was not cosmetic: `collo policy check` was
  reporting the wrong decision for a credential-reaching command because it
  assembled its own action and missed the new field — the same defect shape
  that let the `host` matcher ship inert for as long as it did.
- **Behavior change:** an action reaching a credential store prompts by
  default, including under `autopilot`, where a headless run fails closed. See
  the [compatibility note](COMPATIBILITY.md#credential-file-protection).

### 2026-07-25 — Phase 1 approval-comfort wave

- **An offer that did nothing:** the approval dialog advertised a tool-wide
  "always" for a credential-reaching action, the permission layer declined to
  record it, and the next identical action prompted again. Whether an "always"
  is available is now one field the permission layer owns, the two stale copies
  in the TUI are gone, and a test fails on a third.
- **One narrow grant instead of none:** a credential prompt offers a session
  grant scoped to the exact target shown — never the tool, the directory, a
  sibling file, or anything past this process, and never under `deny`. A
  control whose only answer is "approve again" is a control people switch off.
- **A credential approval looks like one:** its own header and accent, the file
  named first with the kind of secret after it, and a grant button short enough
  not to wrap the row.
- **The way out is on screen:** the prompt shows the configuration rule that
  would end a recurring approval with the path or endpoint already filled in —
  and deliberately does not for an uninspectable command, where no rule would
  help.
- **Stance is reportable and grouped:** `collo doctor` reports preset,
  autonomy, postures, credential setting, and rule count, and warns when a
  project's containment weakening was refused. The Session tab's Security block
  is grouped into policy, enforcement, and session, with degraded sandboxing
  and refused project settings marked visibly rather than rendered as ordinary
  rows.

### 2026-07-24 — Phase 1 host-scoped policy and per-capability grants

- **A documented control became a real one:** `permissions.rules` has always
  accepted a `host` matcher, and the starter configuration and README have
  always advertised it, but no tool ever populated the request's host set — so
  `matchSet` returned false and every host rule silently never fired. Command,
  PTY, background-process, and HTTP-transport MCP actions now declare their
  endpoints, and the matcher works.
- **Endpoints come from the command's own text:** URL arguments to
  curl/wget-family tools, ssh/sftp/scp/rsync destinations, and Git remote URLs,
  normalized to a bare lowercase hostname without scheme, credentials, port, or
  path. Commands outside a curated network-bearing table contribute nothing:
  any binary can open a socket, which is the sandbox's job, not the analyzer's.
- **Unreadable endpoints are named as such:** `git push origin`, `npm install`,
  `curl -K file`, and a URL containing an unexpanded variable are reported as
  *undetermined* rather than as "no endpoints". A host-scoped `allow` rule
  never covers an undetermined endpoint — the same rule that already stopped
  an allow rule from vouching for an uninspectable command — while `deny` and
  `prompt` still fire on whatever was readable. Fuzz invariants assert that no
  reported host ever carries a scheme, credential, port, path, or variable.
- **Two optional postures:** `permissions.network: "scoped"` withholds
  automatic approval from any network-bearing action no rule or grant covers;
  `permissions.commands: "allowlist"` does the same per executable. Both
  default to `open`, so no existing configuration changes meaning. Both can
  only escalate to a prompt, are evaluated before the tool-wide session grant
  and the autonomy mode so neither can hand out the withheld access, and are
  monotonic across configuration layers like `denied_commands`.
- **Approval is now about reach:** the dialog lists filesystem, executable,
  network, and server dimensions separately, marks a dimension it could not
  read, and offers one grant covering exactly the reach shown. A later action
  is automatic only when every dimension it reaches is covered, so a grant for
  `curl` to `api.example.com` covers neither `wget` nor another host. Nothing
  is grantable for an uninspectable command, a mandatory one-time
  confirmation, or an undetermined endpoint, and a posture-gated prompt does
  not offer the tool-wide "always" that would not satisfy it.
- **`curl … | sh` is no longer inspectable:** an interpreter that takes its
  program from a pipe cannot be analyzed, because the code that will run is
  not in the command text. It now always requires a human, in every mode,
  while the endpoint it fetches from is still declared for deny rules.
- **Stated limits:** this is a policy layer, not egress enforcement. It
  describes what a command says it will contact; a program that opens a socket
  without naming it is bounded only by `sandbox_allow_network`, which remains
  all-or-nothing. Endpoint-scoped OS-level confinement remains open as the
  next P0.
- **Containment presets keep the surface usable:** `permissions` had grown to
  four independent security dimensions and roughly fifteen fields, which is a
  coherent engine but not a coherent decision. `permissions.preset` selects
  `frictionless`, `standard` (the default, identical to earlier behavior), or
  `hardened`, filling only the containment fields the same layer did not state
  itself. It is sugar over ordinary fields, not a mode: `collo config show`
  attributes every expanded value to the preset that chose it, an explicit
  field always wins, a preset can tighten an inherited layer but never loosen
  one, and no preset sets autonomy mode. `frictionless` is an explicit opt-out
  from OS containment — never a default, never reached by inheritance — and
  leaves prompts, command-safety denials, and the audit ledger untouched. The
  sandbox command-failure hint now names it, so the escape hatch is offered at
  the moment of friction rather than buried in a 358-line reference.
- **One precedence rule instead of five (behavior change):** containment
  precedence had become per-field folklore — presets could not weaken an
  inherited layer, the `network`/`commands` postures could not be weakened by
  any means, but an explicit project-level `"sandbox": "off"` still won,
  because wave 20 had documented it as an escape hatch preserved at every
  layer. That asymmetry was defensible field by field and impossible to
  predict as a whole. It is now one rule: **a repository can tighten any
  containment setting but never weaken one**, applied identically to
  `sandbox`, `sandbox_allow_network`,
  `sandbox_allow_read_outside_workspace`, `command_env`, `network`,
  `commands`, and `allow_outside_workspace`, and identically to explicit
  fields and presets. The machine owner's global configuration is unrestricted,
  because a built-in default is not a choice they made — that is where the
  escape hatch now lives. Refusals are recorded and printed by
  `collo config show`/`validate` rather than applied silently, so an ignored
  project setting never reads as a bug. A project file that relied on
  `"sandbox": "off"` must move that setting to the global configuration or use
  `"preset": "frictionless"` there. The complete precedence matrix is now
  documented in the user guide instead of being derivable only from three
  separated paragraphs.
- **The stance is always on screen:** the autonomy badge carries `⛨` when OS
  containment is configured, `⛉` when it is not, and `⛨!` when the platform
  applied less than was asked for. It rides inside the existing badge for two
  columns so it cannot be squeezed out; a named badge spells an unusual stance
  out only when it does not displace the run controls, verified by a
  comparative test at four widths. The Session tab gained a consolidated
  Security block — stance, autonomy, sandbox reality, command environment,
  both postures, rule counts, and the session grants handed out so far.
  Cross-platform golden screens were regenerated for the two-column change,
  and `COLLO_UPDATE_GOLDEN=1` now regenerates them deliberately.

### 2026-07-23 — Phase 1 default-on command-sandbox wave

- **Containment is now ordinary:** the built-in runtime default and new global
  starter select capability-aware `sandbox: "auto"` instead of `off`.
  Supported macOS, Linux, and Windows 11 hosts therefore apply their existing
  write/process boundary to foreground, PTY, delegated-verification, and
  background commands without requiring an opt-in edit.
- **Compatibility remains deliberate:** command networking and broad
  macOS/Linux dependency reads still default to enabled. Sandboxed commands
  retain the existing implicit minimal environment, and documentation calls
  out narrow readable/writable grants for SDKs and external caches. Windows
  AppContainer's always-confined user-data reads and unpackaged-loopback
  limitation remain explicit.
- **No silent migration:** an existing global or trusted project setting of
  `sandbox: "off"` stays authoritative, existing files are never rewritten,
  and minimal project starters continue to inherit the earlier layer. `auto`
  visibly degrades when the backend or a requested protection is unavailable;
  `require` remains the fail-closed choice.
- **Operator-visible rollout:** command failures now point to environment as
  well as read/write/network remedies and to `collo doctor`. Starter/reference
  configuration, generated capabilities, README, beta/compatibility/security/
  Linux/exhaustive user guidance, and the active roadmap describe the new
  default and upgrade path. Regression tests lock implicit `auto`, inherited
  compatibility switches, the new global starter, and explicit `off`; the
  native cross-platform enforcement suite remains the boundary evidence.

### 2026-07-23 — Phase 6 governed primary profiles and cost controls wave

- **Named primary agents:** `agents.<name>.availability` now explicitly selects
  `delegate`, `primary`, or `both`, with omitted values retaining the historical
  delegate-only behavior. `default_agent`, `--agent`, and fuzzy `/agent`
  selection activate a visible primary profile without hiding execution or
  discarding the current conversation.
- **Enforced specialization:** primary profiles apply role instructions,
  same-provider model overrides, skill/tool allowlists, iteration/token/cost
  bounds, and permission restrictions at execution. Profile autonomy can only
  tighten; tool/command denials and prompt/deny rules remain additive to
  built-in, user, and project policy. Returning to `default` removes only the
  profile layer.
- **Portable reasoning:** providers and profiles accept optional
  `reasoning.effort`. Omission keeps prior request bodies unchanged. OpenAI,
  Anthropic, Responses, and recognized Bedrock Claude routes translate their
  native fields; explicit unsupported-field responses warn and retry without
  the optional setting where safe, while non-Claude native Bedrock models never
  receive a guessed Claude request shape and retain their model default.
- **No stale price catalog:** optional provider `pricing` contains
  user-verified input/output/cached-input USD rates per million tokens.
  Collomia estimates only from reported usage, treats an omitted cache rate
  conservatively, shows that estimates are not invoices, and ships no model
  prices or silent network lookup.
- **Durable bounded spend:** `cost_budget_usd` joins token/iteration/time limits
  for primary sessions and delegated tasks. Estimated input reserves output
  headroom before a call; missing accounting fails closed and post-response
  overshoot stops before tools or another provider request. Usage/cost rebuild
  from append-only events across resume/fork/rewind and cannot be reset with
  `/clear` or profile switching; `/new` creates fresh accounting.
- **Visible and tested:** status, `/context`, the Session tab, delegated-agent
  details, JSONL, and schema-v1 additive events expose profile/reasoning/cost
  state without crowding the transcript. Tests cover validation,
  backward-compatible request omission, adapter translation/fallback, additive
  primary restrictions, budget exhaustion, and accounting beyond the bounded
  recent-event projection; user/security/automation/testing/capability
  documentation was updated.

### 2026-07-23 — Phase 6 scoped scheduling and three-way reconciliation wave

- **Declared write contracts:** `delegate.tasks[].write_paths` accepts up to 64 validated repository-relative files or directory prefixes. Omitted writer scopes normalize to workspace-wide; read-only tasks cannot declare them. Exact, nested, case-folded, workspace-wide, and otherwise overlapping writers serialize through the existing FIFO/global/provider admission controller, while known-disjoint writers retain useful concurrency and queue time remains inside cancellation/time budgets.
- **Observed-scope enforcement:** every writer is explicitly instructed to remain within its declaration and its final Git change manifest is checked. An out-of-scope path records durable `scope_violations`, makes the task result an error, retains the isolated worktree, and blocks guarded integration. The declaration never widens inherited permissions or replaces the OS sandbox.
- **Fresh three-way review:** integration now compares the recorded Git base, current parent, and retained child. Unchanged parents keep the ordinary selective child diff. Clean non-overlapping text edits become an explicitly labeled parent-to-composed hunk preview preserving both sides. Overlapping edits expose a bounded diff3 conflict preview and remain non-selectable; incompatible add/delete or mode changes stay manual.
- **One publication boundary:** manual `/agents apply` and primary-reviewed `inspect_delegate_changes`/`apply_delegate_changes` share the same token binding, normal `integrate_delegate` permission, post-approval recomputation, rooted atomic writes, rollback, hooks, `/diff`, and `/undo`. No path auto-ranks a candidate, chooses an overlapping resolution, commits, pushes, creates a merge commit, or removes the retained worktree.
- **Durable and cross-platform contract:** schema-v1 delegate updates, restored sessions, the Session/Agents views, hooks, parent inbox, generated capabilities, and user/security/automation/testing documentation carry normalized scopes and violations. Tests cover validation, FIFO overlap admission, workspace-wide serialization, durable round trips, clean/conflicting/stale integration, scope-violation refusal, Windows-style Git configuration, race behavior, and macOS/Linux/Windows compilation.

### 2026-07-23 — Phase 6 verified delegated-results wave

- **Machine-observed child verification:** retained write worktrees expose their repository-detected build/lint/test commands. The primary-only `verify_delegate_changes` tool and operator `/agents verify <id>` path run one command per ordinary `run_command` permission decision with the same hook, sandbox, network, environment, timeout, cancellation, denial, audit, and output policies.
- **Freshness-bound evidence:** bounded redacted results retain purpose, exact command, outcome, timestamps, and an opaque token derived from the registered child worktree/branch/base plus exact changed paths, modes, and bytes. Complete suites are `passed` only when every detected command succeeds against one token; later or in-command source drift preserves the evidence as `stale`.
- **Read-only candidate comparison:** primary-only `compare_delegate_changes` and `/agents compare` expose selectable file/hunk counts, conflicts, fresh verification, bounded evidence, and usage for two to six candidates without choosing, reconciling, or publishing one.
- **Permission remains separate from quality:** passing verification never grants integration permission and covers only the retained child state. Publication still uses the existing guarded review path, and the combined parent workspace remains explicitly unverified until checked after integration. Durable events, agent detail/Session views, capability output, and user/security/automation/testing guidance preserve this distinction.
- **Planning/history split:** the active roadmap now contains current state, remaining deliverables, dependency order, and exit gates; this document retains the full dated work record and historical assessment.

### 2026-07-23 — Phase 6 reviewed delegated-integration wave

- **Manual default, opt-in primary review:** `options.agent_integration` defaults to `manual`, preserving `/agents apply <id>` as the only publication path. `reviewed` exposes `inspect_delegate_changes` and `apply_delegate_changes` only to the primary agent; delegated children cannot see or invoke them.
- **Freshness-bound decisions:** inspection returns bounded child evidence, conflicts, and numbered text hunks plus a SHA-256 review token derived from the registered worktree/branch/base and exact parent/child states. Apply refuses a missing or stale token before authorization, then the normal permission engine owns the single write decision.
- **One guarded publication core:** primary-driven and TUI-driven integration share Git/worktree/base validation, regular UTF-8/size restrictions, parent/child drift checks, rooted atomic mutation, multi-file rollback, change tracking, hooks, `/diff`, and `/undo`. The worktree/branch remain; no path commits, Git-merges, pushes, deletes recovery artifacts, or silently reconciles conflicts.
- **Durable operator visibility:** additive delegate status records and the Session/Agents views retain reviewed, conflict, integrated, partial, blocked, and rejected dispositions. Configuration/reference, README, exhaustive user/security/automation/testing guidance, capability output, event schema, and focused stale-review/single-authorization/cross-platform Git tests describe and enforce the contract.

### 2026-07-23 — Phase 8 interruption, storage, and teardown wave

- **Fail-stop durable sessions:** a latched session write failure is now checked synchronously after persistence-sensitive message/event boundaries and before every later provider request or tool execution. Parent and delegated agents stop rather than creating additional external or mutating effects behind a torn log; an already-started action remains explicitly uncertain and is never replayed during recovery.
- **Real process-death recovery evidence:** subprocess fixtures terminate without cleanup after a dangling tool call and immediately before/after rooted atomic publication. Session loading preserves the accepted prefix and synthesizes one inert interruption result; file destinations are always the complete old or complete new bytes. Catchable temporary-write/publication failures still clean up completely, while documentation names the private orphan-temporary possibility after an uncatchable pre-rename kill.
- **Broader storage fault injection:** immutable session attachments and retained oversized results share a per-manager write/sync/close fault seam. Partial blobs are removed and storage errors propagate; session-record disk failures remain latched so no later line can convert a recoverable tail into middle-of-log corruption.
- **Deterministic cancellation and teardown:** a barrier-driven repeated race proves late delegated-agent updates cannot revive a cancelling child. Runtime/background-process shutdown now waits on command completion after requesting group/tree termination instead of returning after only sending cancellation. Concurrent write delegates serialize only Git's shared worktree-administration setup, preventing `.git/worktrees` creation races while retaining parallel child execution. CI and tagged-release quality gates repeat the focused interruption/cancellation corpus five times without timing assertions.

### 2026-07-23 — Phase 8 beta stabilization and compatibility wave

- **Complete representative offline evaluation matrix:** credential-free scripted-provider scenarios now cover repository search, bug fixing, behavior-preserving refactoring through the atomic patch path, boundary-focused test generation followed by real execution, grounded read-only review with an exact file/line finding, permission refusal, external MCP prompt injection, long-context decision/failure retention, non-replaying recovery/rewind, governed parallel delegation, and selective worktree integration. Assertions remain outcome-based rather than prose- or timing-sensitive.
- **Interruption and teardown evidence:** per-target fault seams prove failures while writing or publishing a private replacement leave the accepted destination unchanged and remove temporary files. Provider cancellation stops before any tool begins, the busy TUI preserves its draft and controls while cancellation is pending, and runtime teardown cancels active delegated work. Existing session short-write/torn-tail and delegated queue/provider/approval cancellation coverage remains in place.
- **Versioned durable compatibility:** newly appended session records carry `schema_version: 1`; legacy records without the field and unknown additive fields continue to load, while a newer version is rejected before append. A focused compatibility and migration policy now defines additive versus breaking changes, configuration/session/event behavior, upgrade backups, downgrade limits, and the release checklist. Configuration and event tests lock the corresponding version-1 rules.
- **Non-flaky performance visibility:** benchmarks now cover runtime construction, 10,000-event activity projection, 2,000-file index query/warm refresh, 2,000-message session restoration, maximum activity rendering, and a 500-block syntax-highlighted transcript. Quality and tagged-release CI execute one smoke iteration and report time/allocations without comparing heterogeneous runner timings; deterministic structural bounds remain ordinary test assertions.

### 2026-07-22 — Phase 8 beta release-readiness wave

- **Gated draft releases:** a version tag must be valid semantic versioning, match the repository `VERSION`, and point to a commit contained in `main`. The exact tag repeats build, uncached test, race, vet, installer, deterministic evaluation, module-integrity, reachable-vulnerability, and bounded fuzz gates before any artifact is assembled. All six cross-built binaries are downloaded from one immutable Actions artifact, and the native AMD64/ARM64 match available on Linux, macOS, and Windows is executed and checked for the complete tagged commit identity. Only then does automation create a human-reviewed draft; suffixed tags are prereleases and never replace the stable `latest` installer target.
- **Reachable-dependency remediation:** enabling the pinned `govulncheck` gate found reachable advisories in the resolved `golang.org/x/text` and Goldmark versions; both were raised to their published fixed versions and the scan now reports zero reachable vulnerabilities. The gate distinguishes called vulnerable symbols from advisories in unused dependency paths while remaining a release blocker for the former.
- **Verifiable, deterministic artifacts:** release metadata defaults to the Git commit timestamp rather than the build clock, and the build script stages every target before replacing `dist/`, marks tracked dirty builds, validates `VERSION`, and runs tests unless an already-qualified job explicitly skips them. Releases include six raw standalone binaries, a complete SHA-256 manifest, a deterministic CycloneDX module SBOM, SLSA provenance attestations, and an SBOM attestation signed with GitHub's short-lived Sigstore workflow identity. Official action dependencies are immutable-SHA pinned.
- **Supported installers:** the macOS/Linux `curl | sh` flow now uses strict HTTPS/redirect policy, retry/timeout bounds, exact checksum matching, a pre-installation binary identity check, and same-directory atomic replacement. A repository-owned PowerShell installer provides the equivalent AMD64/ARM64 checksum and version checks, preserves an existing installation on failure, and changes user PATH only with explicit `-AddToPath`. Credential-free tests cover URL/version selection, strict manifest parsing, bad/duplicate checksums, and failed-upgrade preservation.
- **Beta governance:** focused installation, release, beta-limitations, and private vulnerability-reporting documents cover checksum versus attestation guarantees, upgrade/rollback, draft review, unsigned/notarization status, and the remaining unattended-use/security boundaries. README, exhaustive user/security/testing guidance, generated capabilities, and this roadmap reflect the release contract.

### 2026-07-22 — Phase 7 activity, health, and accessibility wave

- **Event-derived activity center:** `/activity` presents a searchable, category-filtered operator timeline for turns, completed tool activity, permission decisions, file changes, plans, delegated agents, compaction, warnings, and failures. The presentation-neutral projection consumes the established runtime event contract, retains a fixed newest 500-item UI window, restores from bounded recent durable events without replay, redacts displayed content, and lets users copy a selected opaque failure ID. It remains available through the busy-safe local command lane and does not contact providers, execute tools, or change permissions.
- **Actionable, color-independent health:** the Session tab now summarizes recent activity with explicit textual states and adds concise recovery actions for quarantined project configuration, unhealthy providers, degraded sandboxes, MCP problems, and session-persistence failure. `/activity` keeps the detail out of the main transcript and permanent tab bar.
- **Optional motion control with animated defaults:** `options.reduced_motion` replaces only the decorative working spinner with a static marker when explicitly enabled. Animations remain on by default, and composer input, local commands, cancellation, approvals, questions, and agent controls are unchanged. Plain/NO_COLOR regression coverage verifies that states remain understandable without color.
- **Bounded performance evidence:** projection and maximum-size activity-render benchmarks accompany structural retention limits, resume/search/filter/resize tests, and a cross-platform 80x24 plain golden. README, exhaustive user/security/testing guidance, generated capabilities, starter/reference configuration, and this roadmap describe the behavior.

### 2026-07-22 — Phase 8 cross-platform reliability and diagnostics wave

- **Correlated failures without embedded user data:** significant runtime failures receive a random opaque `err-…` identifier that preserves normal `errors.Is`/`errors.As` behavior. The same ID appears on the runtime error event, terminal `run.result` (`failure_id` and `result.failure.id`), TUI diagnostic, durable delegated-agent outcome, and structured debug log; its additive schema-v1 fields do not change established status, classification, or exit-code contracts. Recovery-interrupted and approval/provider-cancelled child outcomes also retain an ID.
- **Privacy-conscious support correlation:** default support bundles collect at most eight valid IDs from bounded recent debug-log tails without copying log messages or attributes. The manifest remains free of paths, prompts, sessions, provider identities, configuration values, and credentials; complete bounded redacted logs still require explicit `--include-logs`.
- **Cross-platform orchestration evaluations:** credential-free product tests now run a plan-associated read specialist and isolated writer concurrently, then exercise guarded selective integration under an inherited Windows-style `core.autocrlf=true`, a nested mixed-case path, two separated hunks, real repository verification, and retained child worktree/branch evidence. Cancellation coverage now includes queue admission, in-flight provider calls, and delegated approval waits, proving a cancelled proposal cannot mutate the parent.
- The macOS/Linux/Windows CI matrix disables test-result reuse for ordinary and race runs. README, testing, automation, security, exhaustive user guidance, generated capabilities, and this roadmap describe the correlation and reliability contracts.

### 2026-07-21 — Phase 6 delegation control and integration wave

- **Busy-safe operator lane:** the composer remains editable while the parent turn runs. A deliberately small set of local inspection and child-control slash commands executes immediately; ordinary text and unavailable commands remain unsent drafts, and question dialogs preserve the draft around their temporary answer field.
- **Hierarchical live control:** the Session tab renders Collomia and its children as a tree with plan association, current action, budget/state, and a bounded recent-output tail. `alt+a` opens an explicit inspect/steer/stop action menu rather than making selection destructive. `/agents steer <id> <guidance…>` persists bounded guidance and delivers it exactly once at the next child provider boundary; it cannot alter an in-flight action or grant permission.
- **Selective, guarded integration:** write delegates record their base commit. `/agents apply <id>` provides themed file/hunk selection and copies approved regular UTF-8 text through rooted atomic mutations only after validating Git worktree/branch identity, unchanged child base, unchanged parent file state, normal permission policy, and a second post-approval byte check. Parent drift, symlinks, binaries, oversize/mode-only changes, and moved branches fail closed; multi-file mutation rolls back on failure, while the child branch/worktree remains intact and nothing is committed or Git-merged.
- **Plan and recovery metadata:** delegate tasks may name an existing dependency-ready `plan_step`; durable status now retains association, steering history/pending count, recent output, base commit, and integrated files. Stored events remain inert on replay/resume.
- Focused tests cover boundary steering, plan dependency validation, bounded output/status round trips, busy-composer behavior, explicit stop selection, selective integration, parent drift, approval races, change tracking, and retained worktrees. README, user/security/automation/testing guides, capability matrix, and this roadmap describe the workflow and limits.

### 2026-07-21 — Phase 6 governed delegation wave

- **Least-privilege agent profiles:** `agents.<name>` now supports tool and skill allowlists, maximum iterations, token and queue-inclusive time budgets, and child permission modes/denials/rules. Profiles can only tighten the parent: built-in and inherited denials remain additive, child `allow` rules are invalid, sandbox/network/outside-workspace policy cannot widen, and hidden tools are enforced again at execution.
- **Session-wide scheduling and control:** all `delegate` calls share one FIFO admission controller with configurable global and per-provider concurrency. Queued work is cancellable and counts against its task timeout; `/agents stop <id-or-name>` and `alt+a` stop one child without cancelling siblings, approval dialogs identify the requesting child, and runtime shutdown cancels every remaining child.
- **Bounded parent inbox and conflict evidence:** delegate results are structured JSON containing stable identity/status, bounded summary/evidence, usage and budgets, changed files/hunks, and retained worktree/branch details. Same-file sibling edits are compared as common-base zero-context hunks and labeled overlapping or disjoint; Collomia still never merges or commits automatically.
- **Durable, non-replaying visibility:** every meaningful delegate state is stored as a bounded `delegate.update` session event. `/agents` and the Session tab show queued, running, waiting-approval, cancelling, budget-exhausted, timed-out, interrupted, and terminal states plus current action and usage. Resume keeps completed outcomes and converts unfinished work to inert `interrupted` state rather than duplicating it.
- Configuration reference/starter comments, README, user/security/automation/testing guides, the capability matrix, and focused scheduler/governance/persistence/TUI tests now cover the shipped behavior and its limits.

### 2026-07-21 — Phase 4/5/7 multimodal image-input wave

- **Typed, durable user images:** provider-neutral messages now carry additive typed content without changing the wire shape of text-only requests. `/attach [workspace-image]` accepts PNG/JPEG/GIF/WebP through a fuzzy picker or quoted/escaped/file-URL/terminal-dropped path; `/attachments` and `/detach` manage the pending session-scoped draft, and the status bar shows its count. Workspace containment, regular-file/type checks, 5 MiB/image, four-image/turn, and 24 MiB/session bounds apply before send.
- **Reference-only session storage:** submitted image bytes live in owner-only session blob files rather than base64 JSONL. Durable messages retain random IDs plus MIME/size/SHA-256 metadata; every provider request re-verifies file type, size, and digest. The send-time read is anchored beneath the workspace to resist path/symlink swaps, and storage occurs only after the `user_prompt` hook accepts the turn. Fork copies attachments, rewind copies only references reachable from retained turns, and deletion removes them. Unsent or hook-blocked selections are not stored. `/context` exposes image count and an explicit rough pre-usage estimate.
- **Provider and MCP delivery:** OpenAI/compatible Chat Completions, Azure OpenAI/Foundry OpenAI, Anthropic/Foundry Claude, Bedrock ConverseStream, and Responses/Mantle encode typed user images while retaining model/endpoint uncertainty as a `partial` capability. MCP image results preserve bytes through the typed tool boundary and are session-retained for capable Anthropic/Bedrock tool-result turns; OpenAI-compatible tool messages keep their portable external-data type/size marker. Audio remains metadata-only.
- Contract/unit coverage verifies unchanged text-only shapes, all four image encodings, rooted symlink-escape refusal, storage integrity/modes/lifecycle, hook-block cleanup, TUI session draft behavior, and typed MCP image retention. README, exhaustive user/security/testing/MCP documentation, capability matrix, and this roadmap describe the workflow and its disclosure, metadata, compaction, and provider limitations.

### 2026-07-21 — Phase 1 trust-boundary hardening wave

- **Race-resistant structured file mutations:** `write_file`, `edit_file`, `apply_patch`, and `/undo` now perform their final read/write/delete through a Go directory root anchored beneath the approved workspace or explicit outside parent. Parent traversal and adversarial parent-symlink swaps cannot redirect the operation outside that root. Replacements use private same-directory files, full writes, sync, and atomic rename rather than truncating existing inodes; hard-linked names outside the workspace therefore remain unchanged. Safe deletion removes only the approved entry, edits/undo preserve permission bits, and patch rollback uses the same rooted primitives.
- **MCP provenance and prompt-injection reduction:** every model-visible MCP tool result, resource/catalog, and expanded prompt template has a deterministic content-derived `EXTERNAL_MCP_DATA` frame naming the source server, content kind, subject, and size. The frame explicitly tells models to use relevant facts and structured data while refusing embedded instructions and claimed permissions. Terminal controls are removed; schema descriptions/titles are visibly marked external/descriptive and bounded, schema comments/examples are omitted, and catalog/elicitation metadata is bounded. Server trust authorizes connection and execution, never textual authority or permission.
- **Adversarial and native-network evidence:** focused tests cover hard-link replacement/deletion, final and parent symlink escape, a concurrent parent-symlink swap, workspace-root replacement, rooted undo and mode preservation, malicious MCP delimiter/control payloads, schema sanitization, and a full agent run in which allowed external content forges a permission grant but still cannot cause a workspace write. Native fixtures now verify Seatbelt remote denial with its documented loopback allowance, Landlock ABI-dependent TCP/UDP denial, and AppContainer network denial without a loopback exemption. The Seatbelt fixture exposed and fixed an overly broad local-address exception: loopback connect/bind/inbound grants are now operation-specific and cannot reopen outbound egress.
- Security/user/testing documentation, README, generated capability matrix, and the Phase 1/5/8 status below now describe both the guarantees and limitations. Structured file safety does not wrap approved shell commands; new-inode publication intentionally breaks hard-link identity and may not retain platform-specific ACLs/xattrs; multi-file publication is not a lock against unrelated concurrent writers; content framing cannot guarantee model obedience; and network grants remain all-or-nothing.

### 2026-07-21 — Phase 2 long-session context and recovery wave

- **Pinned authoritative plan state:** the current structured plan is rendered dynamically into every ordinary provider request outside compactable message history. Updates, resume, in-process session switches, compaction, and rewind therefore expose the same current plan to the model; `/context` reports its contribution separately.
- **Referenced oversized results:** when a returned string exceeds `options.max_tool_output_bytes`, durable sessions keep the normal bounded preview plus an opaque artifact ID. The read-only `read_tool_result` tool pages up to 64 KiB at a time from an owner-only session-local copy without rerunning the originating command, MCP call, or other action. Fixed 4 MiB/result and 32 MiB/session quotas prevent unbounded growth; incomplete retention is explicit, forks copy all artifacts, rewinds copy only references reachable from the retained prefix, deletion removes them, and ephemeral runs retain nothing.
- **Failure-aware compaction:** up to three recent tool failures, bounded to 16 KiB total, are copied verbatim into the compacted context instead of depending on a provider-authored paraphrase; hitting the bound is explicit. The complete durable transcript remains unchanged.
- **Non-destructive conversation rewind:** `collo sessions rewind <id> <turn>` and `/rewind [turn]` create a new session branch ending at a durable completed-turn boundary; turn zero means before the first turn. The source session is never truncated, recorded tools are never executed, and workspace/external state is explicitly left unchanged. Users continue to use `/undo`, Git, or worktrees for file recovery.
- Credential-free evaluations now cover long-context plan/failure retention and inert rewind of a recorded mutation. Unit and TUI coverage additionally exercise artifact quotas/range reads/branch continuity/deletion, dynamic plan refresh, completed-turn selection, source preservation, and workspace preservation. README, exhaustive user guide, security model, testing guide, capability matrix, CLI/TUI help, and this roadmap describe the behavior and boundaries.

### 2026-07-21 — Phase 8 reliability and supportability wave

- **Privacy-conscious support archive:** `collo support bundle [--output path] [--include-logs]` performs local read-only inspection without constructing the runtime, connecting providers/MCP servers, opening sessions, executing tools, or making network requests. Its schema-versioned manifest contains anonymous configuration-layer/strict-validation status, provider type/auth counts, aggregate MCP state, Git availability, sandbox capabilities, platform/build data, and the generated capability matrix. Configuration values, environment names/values, endpoints/models/deployments, user-defined provider/MCP names, workspace paths, source, prompts, transcripts, sessions, audits, and logs are excluded by default, and default diagnostic loading does not resolve environment-backed secrets. Explicit log inclusion is capped at five files/1 MiB each/3 MiB total, locally resolves configured secret references only for redaction, applies common-secret redaction and home/workspace normalization, and remains clearly labeled defense in depth. Archives use owner-only modes where supported, are atomically created under the global root by default, and never overwrite an existing file.
- **Credential-free product evaluations:** a new offline suite drives the real agent, permission manager, change tracker, session recovery, and built-in tools through grounded repository inspection, a bug fix followed by its fixture's actual Go tests, headless permission refusal, and interrupted-mutation recovery. Scripted providers make outcomes deterministic without credentials, network access, provider cost, or brittle prose comparisons.
- **Persistence failure honesty:** the durable session writer now detects and latches both operating-system errors and short writes. Once a record may be torn, later appends stop so recovery can safely discard only the final fragment; the TUI Session dashboard reports persistence health, and TUI/headless turns fail visibly rather than claiming undurable success. A fault-injected test proves the torn tail remains loadable and no partial record enters accepted history.
- **Bounded fuzz and CI quality gate:** replay JSONL, configuration validation, shell analysis, and diff/hunk parsing have bounded Go fuzz targets. A credential-free Linux quality job runs short campaigns plus the evaluation corpus, while release builds now wait for both that job and the existing macOS/Linux/Windows build/test/race/vet matrix. A dedicated testing guide documents the scenarios, local commands, fault coverage, live-provider separation, and safe regression-fixture practices.
- README, exhaustive user guide, CLI help/completion, capability matrix, and this roadmap describe the new command, privacy boundary, durability behavior, and remaining Phase 8 gaps.

### 2026-07-21 — Phase 7 workspace-awareness and review-handoff wave

- **Live workspace snapshot:** the Session tab now shows Git branch, upstream, ahead/behind, and staged/modified/untracked/conflict counts. Inspection runs directly through Git without a shell, is bounded by a short timeout, handles non-repositories and missing Git explicitly, refreshes after turns and on Session-tab entry, and supports a manual `r` refresh. Generation checks discard stale asynchronous responses.
- **Runtime health without transcript noise:** the same tab summarizes active-provider circuit health, effective sandbox/read/network behavior, MCP connected/error/disabled/untrusted counts, and whether project configuration is absent, trusted, or quarantined. A bounded newest-first activity list retains current-process permission decisions and tool failures without turning the chat transcript into a dashboard.
- **Safe external-editor handoff:** `e` in `/diff` opens the current workspace-contained file at the selected hunk. `options.editor` supplies a direct command/argv with `{file}`, `{line}`, and `{column}` placeholders; `VISUAL`/`EDITOR` provide a simple fallback. No shell is inserted, paths are checked after symlink resolution, Bubble Tea suspends/restores the terminal around the child process, and the diff is rebuilt from disk when it returns.
- Cross-platform parser tests cover porcelain-v2 Git status including CRLF; TUI tests cover stale async snapshots, health/activity rendering, editor argv/path containment, hunk line targeting, and the updated diff golden. README, the exhaustive user guide/config reference, capability matrix, and this roadmap describe the new behavior and its safety boundaries.

### 2026-07-21 — Phase 7 session-continuity and composer wave

- **Complete, side-effect-free resume rendering:** initial `--resume`/`--continue` and live session switches now reconstruct the visible chat from the complete durable transcript rather than the compacted model context. User and assistant messages, saved tool calls/results, and synthetic interrupted-call warnings return to the TUI; restoration never executes a recorded tool. Compaction therefore remains a model-context optimization and no longer makes accepted evidence disappear from the resumed screen.
- **Session-scoped composer state:** up/down at the first/last visual composer line navigates prior user prompts while preserving normal cursor movement inside multiline and soft-wrapped input. Moving past the newest entry restores the exact draft. The configurable `session_picker` action (`alt+s` by default) opens saved sessions without replacing the composer, and drafts are retained independently when switching away and back during the running TUI; unsent text is intentionally not persisted as conversation history.
- **Deliberately safe retry:** `/retry` loads the active session's previous prompt into the composer with an explicit nothing-sent notice. It never submits automatically, so a retry cannot silently repeat commands, writes, external calls, or other tool side effects.
- Focused tests cover full-history restoration after model compaction, restored source-tool rendering, interrupted writes remaining unexecuted, multiline history boundaries, draft round trips across sessions, and the review-only retry contract. A plain 80x24 resumed-session golden joins the cross-platform TUI fixtures. README, exhaustive user guide, configuration reference, capability matrix, Help/slash surfaces, and this roadmap describe the behavior.

### 2026-07-21 — Phase 8 deterministic replay and regression wave

- **Side-effect-free trace validation:** `collo replay [--check] <trace|->` consumes completed schema-v1 headless JSONL without constructing the application runtime. It therefore does not load configuration or trust state, open sessions, contact providers/MCP servers, or execute recorded tools. The validator reports exact source lines for malformed JSON, unsupported schemas/kinds, missing known payload fields, impossible turn/tool ordering, inconsistent final status/failure metadata, events after completion, and absent `run.result`; additive schema-v1 fields remain forward-compatible.
- **Safe, useful offline inspection:** default replay renders a deterministic transcript with text/reasoning, permissions, tools and retained partial output, warnings/errors, usage, changes, and the terminal verdict. Terminal controls are removed, identifiers are forced onto one line, untrusted payloads are visibly framed, CRLF is normalized, individual rendered payloads are bounded, and common credential shapes receive a second redaction pass; the original evidence file is never rewritten. Cancelled/error traces may retain an interrupted turn/tool, while successful traces must close cleanly.
- **Cross-platform regression fixtures:** credential-free success and mid-tool cancellation traces cover replay, refusal/additive-field fixtures cover compatibility, and corruption cases cover missing/late verdicts and invalid payloads. Plain-mode golden screens now lock the event-driven chat transcript at 80x24 and 40x12, the floating question dialog, and the responsive side-by-side diff. They run under the existing macOS/Linux/Windows CI matrix with CRLF, ANSI, and terminal-padding normalization.
- README, exhaustive user guide, automation contract, CLI help/completion, capability matrix, and this roadmap document the command and its boundaries. Deeper native terminal-emulator, colored-rendering, and accessibility goldens remain open.

### 2026-07-21 — Phase 7 headless automation contract wave

- **Published, binary-matched contract:** schema-v1 JSONL now has a forward-compatible draft 2020-12 JSON Schema committed with the source, embedded into the single executable, and printed by `collo schema events`. Contract tests keep the published event-kind registry and additive final-result fields aligned with Go's wire types.
- **Reliable terminal verdicts:** after valid argument parsing, actual JSONL runs (excluding informational help/version requests) establish their writer before prompt/config/provider startup and emit exactly one final `run.result` for success, usage/configuration failure, runtime/provider failure, timeout, or cancellation. The established `ok`/`error`/`cancelled` status vocabulary stays compatible while optional `failure`, `partial`, `refused`, and provider metadata let consumers distinguish failure classes, partial work, and denied actions without parsing prose.
- **Stable process behavior:** exit codes are documented as 0 success, 1 execution/provider failure, 2 usage or validated-configuration failure, and 130 cancellation. Stdout remains JSONL-only while human diagnostics and live non-JSON tool progress use stderr; shell/PowerShell pipeline examples show how to preserve both verdict and exit status.
- **Explicitly ephemeral conversations:** `collo run --ephemeral` skips creation/resume/update of durable conversation sessions and marks the final result accordingly. It deliberately does not weaken audit retention, suppress requested debug logs, undo workspace changes, or imply read-only mode; `--resume`/`--continue` are rejected as contradictory. Focused persistence tests prove the session directory stays absent while audit infrastructure remains available.
- README, the exhaustive user guide, generated capability matrix, CLI help/completion, and a dedicated automation-contract guide now document the exact behavior and limitations.

### 2026-07-21 — Phase 7 workspace-input and agent-discovery wave

- **Prompt from file, reviewed before send:** `/prompt` opens a fuzzy workspace-file picker, while `/prompt <path>` accepts quoted, backslash-escaped, local `file://`, and terminal drag-and-drop path forms. A regular UTF-8 text file up to 256 KiB is loaded into the composer with source context; nothing is sent until the user reviews and presses enter. Symlink-aware containment refuses outside-workspace files, and binary/media input fails explicitly because current provider adapters are text-only.
- **File and folder mentions:** the `@` picker now includes folders as well as files, labels each kind, appends `/` to folders, and quotes paths containing whitespace or quote characters so the reference remains unambiguous in the prompt. Large files stay cheap until the agent chooses bounded read tools.
- **Searchable delegated-agent outcomes:** `/agents` completes the command-palette picker set. It fuzzy-searches every delegate retained by the current session and opens a detail panel with live/done/error state, read/write isolation mode, task, duration, redacted outcome, changed files, and retained worktree/branch details. Empty sessions explain when delegates appear rather than showing a blank picker.
- Focused tests cover folder/path quoting, quoted/escaped/URL path parsing, picker-driven and explicit prompt loading, binary/outside-workspace refusal, empty and populated agent browsers, and secret-safe detail rendering. README, exhaustive user guide, capability matrix, slash help, and this roadmap describe the workflow and its current text-only boundary.

### 2026-07-21 — Phase 7 terminal UX and review wave

- **Transcript navigation without fighting the composer:** `ctrl+y` and `/transcript` open a full-screen raw transcript browser with message-by-message selection, line/page navigation, case-insensitive search and next/previous matches. `y` requests a bounded single-message OSC 52 copy and `Y` requests the complete transcript; no platform clipboard helper is required, and `--no-alt-screen` is the documented native-selection fallback when a terminal disables clipboard writes.
- **Stable scroll and responsive layout:** paging up pauses chat auto-follow while streamed events continue, `end` resumes it, and content refresh/resize preserves a manual position. Narrow layouts compact the header and truncate the status row instead of wrapping over the composer. Floating question editors now reserve the modal padding/border exactly once, preventing their nested input frame from wrapping at wide, resized, or narrow terminal widths. Automated tests exercise normal views at 80x24 and a smaller 40x12 fallback, long unbroken content, question dialogs, resizing, and full-screen transcript/diff bounds.
- **Interactive session diff review:** `/diff` and `ctrl+d` now open a changed-file browser rather than appending a large diff to Chat. It selects unified view on narrow terminals and a line-numbered side-by-side view at 108+ columns, falls back safely during resize, folds unchanged regions, and supports file, hunk, line, page, top, and bottom navigation. It is deliberately read-only; selective pending writes continue through the existing permission/hunk overlay, audit, tracker, and undo path.
- **Terminal control and discovery:** `options.alternate_screen` plus `--alt-screen`/`--no-alt-screen` control native scrollback. Nine named global actions have validated, collision-checked keybindings whose effective values appear in Help; modal safety decisions stay fixed. `collo completion bash|zsh|fish|powershell` emits dependency-free completion scripts. The global starter, exhaustive JSONC reference, README, user guide, capability matrix, and CLI help document every setting and workflow.

### 2026-07-20 — Phase 1 command read-confinement wave

- **Independent, compatibility-first read policy:** sandboxed commands now have a separate `sandbox_allow_read_outside_workspace` switch and `sandbox_readable_roots`. Broad command reads remain the default so enabling `sandbox: auto` alone does not break existing toolchains; setting the switch to false requests OS-enforced workspace-scoped user-data reads. Writable roots are implicitly readable, relative roots resolve from the workspace, and environment references expand at execution time.
- **Honest native enforcement:** macOS Seatbelt denies file-content reads from user homes and mounted data volumes except for workspace/temp/PATH/explicit roots while leaving metadata and public system runtime data visible for stable process startup. Linux Landlock handles read/execute access in the same ruleset and allows only workspace/temp/writable roots, conventional system runtime/configuration paths, PATH entries, and explicit read grants. Windows AppContainer keeps its stronger always-on user-data read boundary and now grants explicit readable roots read/execute ACLs without write access.
- **Capability-aware failure and visibility:** backend capability reporting now distinguishes user-data read confinement from write, network, and process isolation. `require` refuses a requested read boundary that a backend cannot enforce; `auto` applies partial protection with a visible warning. `collo doctor`, `/status`, command failure guidance, the starter/reference configuration, README, exhaustive user guide, security model, capability matrix, and roadmap expose the effective read policy and recovery knobs.
- **Adversarial enforcement coverage and current Linux networking:** real macOS integration coverage proves workspace read, outside-secret denial without content leakage, and explicit-root recovery. Linux re-exec and Windows AppContainer fixtures assert the same inside/denied/explicit-grant matrix, while cross-platform builds keep platform-specific policy code type-checked. Landlock ABI v10's stable UDP rights are now recognized: ABI v4–v9 reports TCP-only denial, while ABI v10+ can satisfy full TCP/UDP (including DNS) denial.
- **Linux operator runbook:** a dedicated Landlock guide now documents the kernel/ABI feature matrix, Ubuntu 26.04's ABI v8 behavior, compatibility-first and fail-closed recipes, host verification, custom-kernel/boot requirements, container and WSL caveats, troubleshooting, and the ABI v1–v2 standalone-truncation limitation.

### 2026-07-20 — Phase 5 persistent MCP lifecycle wave

- **Two-scope CLI management:** `collo mcp list|show|add|remove|enable|disable|test` now manages persistent project definitions by default and the user-wide file with `--global`. Layered listings expose effective, project-shadowed, and trust-quarantined entries instead of hiding precedence.
- **Safe config mutation:** MCP edits use same-directory atomic replacement, preserve permissions and unrelated/unknown JSON fields, reject newer schemas, validate names/transports/URLs/env/header fields, and require `--yes` before replacing an existing entry. Explicit adds set the server trust bit, while project edits deliberately invalidate repository trust until the user reviews and runs `collo trust`.
- **Connection-only diagnostics:** `collo mcp test` resolves the effective (or explicitly global) entry, negotiates and pings it, validates its tool catalog, lists advertised resource/prompt catalogs, then closes it without invoking a tool or reading/updating persistent server pins. Literal credential warnings and redacted `show` output encourage environment references.
- **Clear lifecycle split:** documentation now distinguishes persistent CLI operations from session-only `/mcp` slash commands and includes copy-ready stdio/HTTP examples. OAuth-backed `login`/`logout` remains coupled to the standards-based OAuth deliverable rather than a static-token shortcut.

### 2026-07-20 — Phase 5 MCP live catalogs and conformance wave

- **Safe dynamic catalogs:** standard tool/resource/prompt `list_changed` handlers are installed on every connection. Tool notifications fetch and validate a complete replacement before one atomic registry swap; malformed or failed refreshes preserve the last-known-good tools. Bursts serialize/coalesce, and callbacks from disabled, removed, or superseded connections are ignored by generation.
- **No stale resource/prompt cache:** resource and prompt lists remain live reads. Their notifications create visible pending markers that clear only after the corresponding list succeeds. Tool errors and pending catalogs appear in `/mcp status`; `/mcp refresh <server>` retries tools without tearing down a healthy transport.
- **Protocol truth:** `/mcp status` reports the negotiated revision and the exact catalogs that advertised list changes. Capability-specific in-memory fixtures now assert MCP 2025-11-25 negotiation, tools/resources/prompts, dynamic notifications, atomic hot replacement, last-known-good preservation, rich content, progress, elicitation, cancellation, lifecycle, and pinning. [The protocol support guide](MCP_PROTOCOL.md) declares the tested subset and older revisions supported through the official SDK.
- **Experimental boundary:** MCP tasks remain unsupported rather than gaining a private implementation while the standard and Go SDK task surface evolve. OAuth/login, resource subscriptions, audio/annotation passthrough, and argument-level permission scoping remain separate Phase 5 work; image passthrough and prompt-injection framing shipped in later 2026-07-21 waves above.

### 2026-07-20 — Phase 1 Windows sandbox and capability-truth wave

- **No-install Windows 11 enforcement**: sandboxed commands re-exec through a hidden AppContainer launcher built from inbox Windows APIs — no Windows Sandbox/Hyper-V feature, administrator-installed driver, service, or third-party runtime. A workspace-derived AppContainer SID receives access only to the workspace, temp, configured `sandbox_writable_roots`, and read/execute access for user-local PATH directories; a kill-on-close Job Object owns the complete descendant tree. Internet/private-network capabilities are present only when `sandbox_allow_network` is true.
- **Capability-aware fail-closed behavior**: backends now report write confinement, read confinement, network isolation (`none`/TCP/full), process isolation, and platform limitations independently. `auto` applies partial protection but surfaces the exact degradation in command output, `collo doctor`, and `/status`; `require` refuses a policy the backend cannot fully enforce. In particular, Linux no longer presents Landlock's TCP-only control as complete network denial when UDP remains reachable.
- **Compatibility-first networking**: sandboxing remains opt-in, and `sandbox_allow_network` defaults to true, so switching only `sandbox` to `auto` preserves package installation and online CLI traffic at the network boundary. Users can explicitly set it false for command network denial; provider HTTP, remote MCP, hooks, and LSP networking remain outside the command sandbox. Narrow additional cache roots can be granted with `sandbox_writable_roots`.
- **Cross-platform verification and documentation**: shared policy-preparation tests cover fail-closed and visible-degradation behavior, Windows CI now runs a real AppContainer write-confinement fixture, and Windows packages are cross-compiled locally. README, security model, exhaustive user guide/reference, capability matrix, doctor/status output, and this roadmap describe the controls and platform limitations, including Windows' default AppContainer loopback restriction.

### 2026-07-19 — Phase 4 reasoning-model request compatibility

- **Provider-driven Chat Completions negotiation**: OpenAI-protocol adapters preserve the existing `max_tokens` and optional `temperature` request shape unless an upstream HTTP 400 explicitly names one as unsupported. A `max_tokens` rejection that directs the caller to `max_completion_tokens` is retried with the same numeric output budget; an explicit default-only/deprecated `temperature` rejection is retried without the field and surfaced as a warning.
- **No model-name guessing or provider regressions**: the learned request shape is scoped to the active provider/model client and protected for concurrent calls. Successful requests, unrelated client errors, older Azure deployments, and compatible services such as OpenRouter, Ollama, vLLM, and LM Studio remain unchanged; adjusted attempts occur before stream parsing, so no text/tool delta can be duplicated.
- **Authentication and resilience parity**: every adjusted request is rebuilt through the normal API-key/static-bearer/refreshable-Entra authentication path, while replay-safe network/408/429/5xx retries remain a separate policy. Bounded unit and contract coverage verifies sequential `max_tokens`/`temperature` negotiation, in-memory reuse, precise error matching, current-token injection, accepted legacy fields, unrelated 400 behavior, streaming, and concurrent learning.

### 2026-07-19 — Phase 4 Azure Entra authentication and refresh wave

- **Keyless Azure provider coverage**: `azure-openai`, `azure-foundry`, and `azure-foundry-anthropic` now accept explicit `auth: "entra"`. The official Azure Identity SDK's `DefaultAzureCredential` covers environment service principals/certificates, workload identity, managed identity, Azure CLI, Azure Developer CLI, and Azure PowerShell; `AZURE_TOKEN_CREDENTIALS` can restrict the chain to `prod`, `dev`, or one credential type.
- **Safe automatic refresh**: access tokens remain in memory, are shared across concurrent requests, and refresh proactively at the SDK's `RefreshOn` time or two minutes before expiry. Each provider request obtains the current token after custom headers. Empty/control-character tokens and acquisition failures stop before provider HTTP with a non-retryable authentication error; the compatibility `bearer` mode remains static and doctor warns that it cannot refresh.
- **Correct audiences and cloud controls**: traditional Azure OpenAI defaults to `https://cognitiveservices.azure.com/.default`; Foundry OpenAI/v1 and Claude default to `https://ai.azure.com/.default`. `entra_scope`, `entra_tenant_id`, and validated `entra_authority_host` overrides cover multi-tenant and sovereign/private endpoint configurations without guessing an audience.
- **Deterministic auth and actionable diagnostics**: API-key behavior stays backward compatible (`auth: "api_key"` or omitted), while Entra mode rejects API-key fields and custom authentication headers so ambient identity never silently changes a configured mode. `collo doctor` reports chain selection, scope, tenant, authority, token-refresh behavior, and the relevant data-plane role without requesting a token; Azure 401/403 errors add scope/RBAC and propagation guidance.
- Unit and contract tests cover cache/refresh timing, concurrent refresh serialization, current-token injection for OpenAI and Anthropic protocols, pre-HTTP acquisition failure, provider-family scope defaults/overrides, RBAC hints, validation conflicts, diagnostics, and redaction of Azure environment secrets. README, annotated JSONC reference, security guide, capability matrix, and this roadmap now document the feature.

### 2026-07-19 — Phase 4 provider contract qualification wave

- **Credential-free CI contracts**: ordinary `go test ./...` continues to run deterministic request/stream fixtures for OpenAI Chat Completions, Anthropic Messages, Responses/Mantle, and native Bedrock ConverseStream. Coverage includes tool definitions and completed calls, normalized text/reasoning/tool/usage signals, HTTP and in-stream failures, retry/truncation behavior, and now cancellation after a partial delta for every family.
- **Consistent cancellation**: a table-driven cross-adapter fixture proves that cancelling a live read promptly returns the shared non-retryable `cancelled` classification, closes the response body, and never replays a request whose response already began.
- **Double-opt-in live qualification**: `TestLiveProviderContracts` reads a strict, secret-free manifest only when both `COLLO_LIVE_PROVIDER_TESTS=1` and `COLLO_LIVE_PROVIDER_CONFIG` are set. Each configured endpoint must stream a synthetic tool call and usage, accept its supplied tool result, then stream a final text answer and usage; the harness never executes the tool. `required_families` can enforce OpenAI, Anthropic, Responses, and Bedrock coverage for release runs.
- **Credential safeguards and operating guide**: literal API keys, embedded URL credentials, and literal sensitive headers are rejected; referenced environment variables are resolved only in memory and known values are removed from reported failures. [The live-contract guide](LIVE_PROVIDER_CONTRACTS.md) documents cost, credential, selective-development, and full-qualification behavior; the capability matrix now marks the suite implemented.

### 2026-07-19 — Phase 4 normalized provider streaming wave

- **One streaming vocabulary**: provider deltas now distinguish text, provider-supplied reasoning, incremental tool-call identity/argument fragments, complete usage snapshots, and warnings. The runtime maps them to `text.delta`, `reasoning.delta`, `tool.call.delta`, `usage`, and `warning` JSONL events; streamed tool JSON is informational until the adapter assembles and validates the complete call, so no partial request can execute.
- **Native Bedrock streaming**: the AWS adapter now calls `ConverseStream` with either SigV4 or Bedrock bearer authentication and decodes CRC-checked AWS event-stream frames for text, parallel tool calls, reasoning text, stop reason, cache-aware usage, and in-band exceptions. Throttling, service, validation, and protocol failures retain their classification and AWS request ID; a stream that ends without `messageStop` fails instead of accepting a truncated answer.
- **Responses SSE with compatibility fallback**: Responses/Mantle requests negotiate SSE, assemble function-call fragments, surface reasoning summaries and incomplete-run warnings, reconcile the terminal response with emitted text, and reject missing terminal events. Compatible endpoints that return one synchronous JSON envelope continue to work.
- **No unsafe stream replay**: status/network failures can retry before a response body starts, but an exception carried inside a stream is never automatically replayed after deltas may have reached the user. Recorded fixtures now cover normalized signals, parallel tool assembly, usage, synchronous fallback, in-band errors, and truncation for the provider families.

### 2026-07-19 — Phase 4 provider resilience wave

- **One failure vocabulary**: built-in adapters now return a provider-neutral classification (`authentication`, `permission`, `rate_limit`, `invalid_request`, `not_found`, `timeout`, `unavailable`, `protocol`, `cancelled`, or `unknown`) with HTTP status, retryability, `Retry-After`, and provider request ID when present. Human-readable messages are bounded/control-character-safe, while JSONL `error` events carry the same fields in a structured `provider` object for automation.
- **Retry parity and safe bounds**: OpenAI-compatible, Anthropic, Responses/Mantle, and native Bedrock requests now share the existing three-attempt exponential-backoff/jitter/`Retry-After` policy for network failures and 408/429/5xx/529. Ordinary 4xx failures do not retry, and a request with a non-replayable body is attempted only once. Provider config exposes independent `connect_timeout_seconds` (10), `request_timeout_seconds` (1800), and `stream_idle_timeout_seconds` (300) defaults; the idle timer resets on every response chunk and interrupts a silent stream.
- **Health and circuit state**: the active client tracks `unknown`/`healthy`/`degraded`/`circuit_open`/`half_open`. Three complete transient request failures open a 30-second circuit, concurrent recovery probes are suppressed, and one successful probe restores health. `/status` exposes the state; changing provider/model resets it. The breaker wraps complete model requests, not tool execution, so it cannot replay a mutating tool.
- Focused tests cover taxonomy/rate-limit metadata, retryability, Responses and Bedrock retry paths, replay safety, idle timeout interruption, circuit open/recovery/non-transient behavior, timeout config validation/defaults, and JSONL provider-error classification. README, exhaustive JSONC reference, capability matrix, and this roadmap document the behavior.

### 2026-07-19 — Native Bedrock authentication coverage

- **All Bedrock credential families**: native Converse requests now support `auth: auto|sigv4|bearer`. SigV4 retains the AWS SDK chain — long-term access/secret keys, temporary access/secret/session credentials, shared profiles, IAM Identity Center, assumed/web-identity roles, and ECS/EKS/EC2 workload roles. Bearer accepts both short- and long-term Amazon Bedrock API keys from `api_key_env`, literal `api_key` (discouraged), or the standard `AWS_BEARER_TOKEN_BEDROCK` environment variable.
- **Deterministic precedence**: `auto` prefers an explicitly configured or standard Bedrock bearer token and otherwise falls back to SigV4; explicit `sigv4` ignores an ambient bearer token, and explicit `bearer` never falls back to unrelated AWS credentials. A named but missing bearer environment fails before HTTP instead of silently changing identity. Configuration validation rejects ignored/contradictory profile or API-key settings.
- **Credential-safe diagnostics**: `collo doctor` reports SigV4 vs bearer and the selected profile/environment source without credential values. The standard Bedrock bearer variable joins configured secrets in the redactor. Collomia consumes but does not mint/refresh short-term bearer keys; SDK-managed SigV4 credentials keep normal refresh behavior.
- Contract tests assert bearer-only headers, automatic standard-token discovery, SigV4 signing with `AWS_SESSION_TOKEN`, explicit-mode precedence, missing-token fail-fast behavior, and header-injection rejection. README, exhaustive JSONC reference, security documentation, and capability matrix now include both authentication paths.

### 2026-07-19 — Phase 4 provider capability foundation

- **Honest capability declarations**: every supported provider/model selection now carries a normalized `supported`/`partial`/`unsupported`/`unknown` declaration for tools, streaming, reasoning, images, structured output, token usage, prompt caching, parallel tool calls, and model discovery, plus configured context size and endpoint constraints. Declarations describe Collomia's implemented adapter surface, not every vendor feature, and model-dependent facts remain unknown when catalogs expose only names.
- **Capability-aware selection and health**: `/status` and both `/model` picker stages show compact effective capabilities. `/models` immediately renders the configured declarations, then probes supported catalogs concurrently and updates the same panel with available/unavailable state and model count; providers without a catalog are explicitly *unverified*, not incorrectly marked down. Errors are redacted before display.
- **Preflight before network I/O**: requests that contradict a known declaration (currently unsupported tool calling or a maximum output larger than the configured context window) fail locally with an actionable error. Unknown and partial capabilities remain visible without becoming speculative failures.
- **Recorded protocol contracts**: request/response fixtures exercise tool definitions/calls, content streaming, and usage normalization across OpenAI Chat Completions, Anthropic Messages, Responses/Mantle, and Bedrock ConverseStream. The opt-in live harness and cross-family cancellation fixtures shipped in the qualification wave above; the health/error taxonomy shipped in the resilience wave.

### 2026-07-19 — Phase 5 lifecycle hooks

- **Eleven hook events**: `hooks.<event>` in configuration runs trusted commands at `session_start`, `user_prompt`, `permission_decision`, `tool_start`, `tool_end`, `file_change`, `compaction`, `subagent_start`, `subagent_end`, `stop`, and `session_end` — each receiving a structured JSON payload on stdin (event, workspace, subject, tool/summary/args/paths, decision detail) plus `COLLO_HOOK_EVENT`/`COLLO_WORKSPACE` in the environment.
- **Gating that only tightens**: `user_prompt` and `tool_start` hooks may block the action (exit 2, or print `{"decision":"block","reason":…}`); the reason lands in the transcript/tool result. Hooks cannot approve anything the permission engine denies and cannot bypass the sandbox; all other events are observational.
- **Bounded by construction**: regex `matcher` scoping, per-hook `timeout_seconds` (default 10) with a kill wait-delay so a hook's orphaned children cannot stall the turn, 8 KiB output caps, and fail-open-with-warning semantics for broken hooks (the permission engine remains the enforcement boundary). Config validation rejects unknown events, empty commands, bad regexes, and negative timeouts; project hooks sit behind `collo trust`.
- 8 runner tests (nil-safety, both block forms, bounded failure, matcher, stdin payload, timeout, output cap) plus agent-level tests proving a blocked tool never executes and a blocked prompt never reaches the provider; README section, config reference entry, and capability row added.

### 2026-07-19 — Phase 5 MCP security pinning, elicitation, and progress

- **Server pinning**: every configured server's definition fingerprint (SHA-256 over transport, command, args, URL, and the *names* of env vars and headers — values excluded so token rotation is not a false alarm) and the remote implementation's name/version are pinned per workspace in `mcp-pins.json` beside the trust database, outside any repository. A definition or identity mismatch at connect produces an explicit session warning naming the change — a tripwire for swapped binaries or quietly edited server entries, layered on top of workspace trust. Session-scoped `/mcp add` servers are not pinned.
- **Elicitation**: a server can now pause a tool call to ask the user for input. Form-mode requests become typed questions through the TUI's existing ask flow — field titles and descriptions, enum options, boolean choices, required-field enforcement, number/integer/boolean coercion — and the answers return to the server without entering model context. Esc or an invalid answer declines the whole request (sensitive input never defaults to acceptance), URL-mode elicitation is declined outright, and headless runs never advertise the capability so unattended sessions cannot be fished.
- **Progress**: MCP tools that report progress stream it live into the transcript during the call (`progress: 3/10 — indexing…`), through the same path as live command output — implemented with a per-call progress token and a general `RunStream` hook on `tools.Function`.
- 6 new tests: pin fingerprint and identity-swap detection (quiet on unchanged reconnects), runtime-server pin exemption, live progress streaming (and silence without a stream sink), elicitation accept with typed answers, decline on user cancel, and no-asker refusal.

### 2026-07-19 — Phase 5 MCP resources, prompts, and rich content

- **Resources**: servers that negotiate the resources capability are browsable from the TUI (`/mcp resources <server>` lists URI/type/size/description; `/mcp resource <server> <uri>` previews one, capped at 4 KB with a note) and readable by the agent through two new tools — `list_mcp_resources` (optionally per server) and `read_mcp_resource` — registered whenever MCP is in play. Both are classified external, and their permission assessment names the target server dynamically from the arguments so server-scoped policy rules keep matching.
- **Prompts as user templates**: `/mcp prompts <server>` lists a server's prompt templates with argument names, descriptions, and required markers; `/mcp prompt <server> <name> key=value …` expands the template server-side and places the result in the input box — the user reviews and edits before anything reaches the model. The server picker's detail panel advertises both features when the server negotiated them.
- **Rich tool content**: MCP tool results now render from the SDK's typed content blocks (previously a lossy JSON re-parse that kept only text): structured output and embedded-resource text pass through, images and audio become explicit `[image image/png, N bytes]` markers instead of disappearing, and resource links keep their URI with a hint that `read_mcp_resource` can follow them.
- 5 new tests against the in-memory fixture server cover resource list/read (text and binary), prompt list/expand, capability-missing errors, the agent-facing resource tools (including the server-scoped permission assessment), and mixed-content preservation.

### 2026-07-19 — Phase 5 MCP lifecycle: runtime server management with health and capabilities

- **Stateful server manager**: `internal/mcp` now retains every configured server — connected, failed, disabled, or untrusted — with its status, exact initialization error, connection time, negotiated capabilities (tools/resources/prompts/logging/completions), remote server name/version, and contributed tool list, instead of forgetting everything but successful connections.
- **Runtime operations**: `/mcp status` reports the fleet with health glyphs; `/mcp ping <name>` health-checks a session (failures are recorded, not just printed); `/mcp reconnect <name>` re-establishes the session and refreshes its tool catalog; `/mcp enable|disable <name>` toggle a server for the session — disabling withdraws its tools from the registry immediately (a new `Registry.Remove`), and enabling cannot override missing trust; `/mcp add <name> <command…>` (or `--url <endpoint>`) connects a session-scoped server the user defines inline; `/mcp remove <name>` disconnects and forgets it.
- **Trust model unchanged**: repository-supplied servers stay quarantined until `collo trust`; `/mcp add` servers are user-initiated and session-only, and never persist to configuration.
- 7 lifecycle tests run against a real in-process MCP server (SDK in-memory transports through a dial seam): status/capability reporting, disable/enable tool withdrawal and restoration, trust enforcement, catalog refresh on reconnect, runtime add/remove, ping failure recording, and failed-connect repair.

### 2026-07-19 — Phase 5 skills overhaul: standard-compliant skill packages with a full lifecycle

- **Agent Skills-standard packages**: a skill is now a directory — `SKILL.md` plus optional `scripts/` (executable helpers), `references/` (docs read on demand), and `assets/` (output templates). `load_skill` returns the instructions *and* a map of the bundled files with usage guidance (references via `read_file`, scripts via `run_command` under the normal permission rules), so the agent never guesses paths and unused material costs no context.
- **Robust front matter**: a hand-rolled YAML-subset parser (no new dependency) handles plain/quoted scalars, folded (`>-`) and literal (`|`) blocks, indented continuations, block and inline lists, and one-level nested maps — covering `name`, `description`, `license`, `allowed-tools` (surfaced to the model as the author's tool expectation), and `metadata.version`.
- **Two scopes, deterministic precedence**: project skills (`.collomia/skills/`, `.agents/skills/`; trusted workspaces only) shadow global skills (`~/.collomia/skills/`) of the same name, with shadowed duplicates reported as startup warnings instead of silently dropped. Legacy single-file `SKILLS.md` manifests still work.
- **Validation without breakage**: name format/length, name-vs-directory match, description length, missing front matter, and oversized files produce warnings in `collo skills list`/`show` (only unreadable/oversized skills are excluded), so existing simple skills keep working while authors are nudged toward the standard.
- **Full lifecycle CLI**: `collo skills list|show|new|install|update|remove|enable|disable` — scaffolding with a front-matter template, SHA-256 inspection, validated symlink-refusing installs into either scope, `.disabled` markers that hide a skill from the agent without deleting it, and `--yes`-guarded removal. Bundled scripts gain no execution rights from installation; the permission engine and sandbox govern them like any other command.
- 11 new tests cover the parser, validation, precedence/shadowing, disabled markers, bundle rendering, the install/scaffold/remove round trip, and symlink/oversize refusals; README, capability matrix, and TUI surfaces updated to match.

### 2026-07-18 — Phase 7 experience batch: pickers everywhere, notifications, plain mode, structured run results

- **Skills and MCP pickers**: `/skills` now opens the fuzzy picker — choosing a skill pre-fills the prompt with `Use the "<name>" skill: ` so the user only adds the task — and `/mcp` opens a server picker whose selection prints that server's tools with descriptions. `list` on either prints the old plain listing. Every picker surface named in the Phase 7 palette deliverable except agents is now done.
- **Desktop notifications**: pending approvals, agent questions, failed turns, and turns longer than ten seconds now post a desktop notification through the hosting terminal (OSC 9, with the tmux passthrough envelope) in addition to the existing bell. `options.notifications` selects `on` (default), `bell`, or `off`; control characters are stripped and messages bounded before they enter the escape sequence.
- **Plain mode / NO_COLOR**: an eleventh theme, `plain`, renders entirely without color — structure comes from bold, reverse video, and borders — and skips the OSC 11 background takeover. The standard `NO_COLOR` environment variable selects it automatically over any configured theme (glamour markdown drops to `notty` too). This closes the no-color item of the terminal-fundamentals deliverable.
- **Titled output panels**: informational slash commands (`/status`, `/context`, `/ps`, `/tasks`, `/models`, `/tools`, `/skills list`, `/mcp list`, `/config`, `/help`, and the MCP server-tools view) render in a rounded, theme-colored box with the command's subject spliced into the top border and content word-wrapped (long paths hard-wrap; frame alignment is asserted by test), replacing untitled muted text. Body text is tinted with a theme-derived color — `Theme.panelText()` blends `Muted` 45% toward white (dark themes) or black (light themes) via `BlendLuv` — rather than the terminal's raw default foreground, so the box reads as one themed unit; `plain` stays uncolored. Quick acknowledgements stay as subtle one-line notes.
- **Structured final output**: `collo run --jsonl` now always ends with one `run.result` line — status (`ok`/`error`/`cancelled`), the final answer, error text, session id, changed files, duration, and usage — so automation reads a stable verdict instead of reassembling text deltas or scanning for mid-stream error events. Additive to event schema v1; round-trip tested.

### 2026-07-18 — Browser terminal (`collo --web`)

- A PTY-backed web terminal: `collo --web` starts the normal TUI in a real PTY and serves it to a local browser over an embedded xterm.js page — same workspace, credentials, permission policy, and approval prompts as the terminal session, because it *is* the terminal session.
- Deliberately local-only: loopback bind, per-invocation 256-bit bearer token carried in the URL fragment, exact-origin check, one controlling connection, PTY session killed when the browser disconnects. macOS/Linux only until a ConPTY backend exists. Full properties in [docs/SECURITY.md](SECURITY.md).
- Alongside it, config generation was reworked: `collo init [--global]` writes a starter file, `collo config reference` prints an exhaustive annotated JSONC reference (with `--with-reference` to save it beside the active file), and the global config moved to `~/.collomia/` — with a completeness test asserting the reference documents every JSON field in the schema.

### 2026-07-18 — Phase 3 completion: LSP diagnostics, symbol indexing, PTY, review instructions

- **LSP diagnostics**: a new minimal Language Server Protocol client (`internal/lsp`: stdio JSON-RPC, initialize/didOpen/publishDiagnostics, single reader goroutine, server-request auto-acks) powers the `diagnostics` tool — it runs the project's real language server over requested files and returns severity-ordered findings with exact files and lines. gopls, pyright, typescript-language-server, and rust-analyzer are auto-detected on PATH; an `lsp` config map overrides the command per language. Tested against a scripted fake server and verified live against gopls (caught an undefined function at its exact line in ~2s).
- **Repository symbol indexing**: `internal/index` keeps an incremental, ignore-aware definition index (mtime/size cache — unchanged trees re-parse nothing; deletions drop out) for Go, Python, JS/TS(X), and Rust. The `search_symbols` tool queries it with exact→prefix→substring ranking and kind filters; `search_files` remains for references and arbitrary text.
- **PTY execution**: `run_command` accepts `pty: true` on Unix — the command runs attached to a real pseudo-terminal (creack/pty) for interactive-only CLIs and isatty-dependent output, in its own session with whole-group kill on timeout, verified by tests (isatty probe true under pty and false without; 1-second timeout kills a 30-second sleep). Windows reports a clear unsupported error; ConPTY is future work.
- **Custom review instructions**: `collo review [ref] [instructions…]` and `/review` now accept trailing words as reviewer instructions (`-` reviews uncommitted changes when instructions are wanted without a ref).
- With these, every Phase 3 deliverable is checked or explicitly reduced to named refinements (LSP definitions/references/formatting, line-level approval, Windows ConPTY, semantic symbol extraction). New test coverage: 5 index tests, 2 LSP client tests, 4 diagnostics-tool tests (one against real gopls, skipped where absent), 2 PTY tests.

### 2026-07-18 — Background process management (Phase 3 P1)

- **Detached background jobs**: `start_process` launches a long-running command (dev server, file watcher, long test run) and returns its id immediately instead of blocking the turn until timeout. The process lifetime belongs to the session, not the tool call — cancelling a turn no longer kills a deliberately-started server.
- **Same safety envelope as `run_command`**: denied-command patterns, conservative shell analysis (uninspectable commands still require interactive approval), OS sandbox wrapping, minimal child environment, and own-process-group execution so stops kill every descendant.
- **Observability and control**: `list_processes` (status + uptime), `process_output` (bounded 64 KiB retention with optional `tail_lines`), and `stop_process` for the agent; `/ps` and `/ps stop <id>` for the user; a live "Background processes" section in the Session tab and a `procs N` status-bar badge.
- **Nothing outlives Collomia**: every background process is killed at session exit, and processes started by delegated write-agents in worktrees are stopped when their task finishes.
- Seven new tests cover output capture, group kill, stop-all, denied patterns, shell-analysis assessment, tail behavior, and unknown ids — all passing under `-race` (concurrent writer/reader on the retained output). `docs/CAPABILITIES.md` and the Phase 3 live-execution entry were updated; only PTY support remains there.

### 2026-07-18 — Hunk-level diff approval (Phase 3 P0 follow-up)

- **Selective hunk apply**: `internal/diffmodel` gained `ParseHunks`/`ApplyHunks`, which split a unified diff into its individual hunks and reconstruct file content keeping only a chosen subset — the same selection semantics as `git add -p` — verified by round-trip tests (keep-all reproduces the proposal exactly, keep-none reproduces the original exactly, and mixed selections apply only the chosen hunks).
- **`h` to review hunks**: when a pending `write_file` approval's diff has two or more hunks, the approval overlay now offers `h` alongside the existing y/a/n choices. It opens a hunk list — ↑/↓ to navigate, space to toggle, `a` to keep all, enter to apply the selection, esc to go back — and shows the selected hunk's colorized lines inline.
- **Plumbing, not a new write path**: the composed content flows through the existing approval → execution pipeline via `permission.Decision.Content` / `Grant.ContentOverride`; `agent.executeTool` rewrites the `write_file` call's `content` argument before executing, so tracking, undo, and the audit ledger all see it as a normal write. Scoped to `write_file` for now — `edit_file` is already a single atomic replacement and `apply_patch` changesets remain file-level.
- `docs/CAPABILITIES.md` and this roadmap's Phase 3 diff-model/exit-gate and Phase 7 diff-ergonomics entries were updated; new tests cover the diff-model math, the permission/agent plumbing, and the content-override edge cases.

### 2026-07-18 — Verification loop (Phase 3 P1)

- **`detect_verification` tool**: a deterministic, read-only inspection of the workspace root that recognizes Go (`go.mod`), Node (`package.json`, preferring `pnpm`/`yarn` when their lockfile is present and reading actual `scripts` before suggesting a fallback), Rust (`Cargo.toml`), Python (`pyproject.toml`/`requirements.txt`/`setup.py`, with a `ruff` suggestion when configured), and `Makefile` targets — and reports the real build/lint/test commands for that project instead of the model guessing.
- **`collo verify [focus]` and `/verify [focus]`**: a canned prompt, mirroring `collo review`, that has the agent call `detect_verification`, record each command as a step via `update_plan` before running it, execute it with the existing live-streamed `run_command`, and mark the step done or blocked with the command's own exact output as evidence — never claiming a pass the tool result didn't report. Ends with a one-paragraph pass/fail summary. It does not modify files.
- The agent's default system prompt now points at `detect_verification` for its own end-of-task verification, not just the explicit `/verify` flow.
- `docs/CAPABILITIES.md` and this roadmap's Phase 3 P1 entry were updated to match; eight new tests cover each detected ecosystem plus the empty-workspace case.

### 2026-07-18 — Named agent profiles and conflict detection (Phase 6 P0 follow-up)

- **Named agent profiles**: a project or user config can now define `agents.<name>` with its own `model`, `instructions` (a fixed role prepended to the sub-agent's system prompt), `tools` (an allowlist that disables everything else), and `max_iterations`. `delegate` tasks pick one by name via a new `agent` field; the tool's own description lists the profiles currently configured so the model can discover them. Any field a profile omits falls back to the parent's own setting.
- **Sibling conflict detection**: once every task in a `delegate` batch finishes, Collomia scans the changed-file lists across all write-capable sub-agents' worktrees and appends a warning naming any file touched by more than one of them, so overlapping work is surfaced instead of silently left for the user to discover later. Worktree isolation still means they never raced on disk; this only flags overlap for manual reconciliation before merging.
- Config schema gained `agents` (validated: `max_iterations` must not be negative) and `docs/CAPABILITIES.md`/this roadmap were updated to match.

### 2026-07-18 — Parallel multi-agent delegation slice (Phase 6 P0)

- **Concurrent scheduler**: `delegate` now takes a batch of up to 6 named tasks and runs up to 4 of them at once over a bounded worker pool, instead of one synchronous read-only child at a time; each task keeps its own 10-minute timeout and inherits the parent's cancellation.
- **Worktree isolation for write-capable agents**: a task marked `write: true` gets its own `git worktree add` checkout, a freshly built tool registry and permission manager rooted there, and its own audit ledger — so parallel writers can never race on the same files. Read-only tasks stay cheap, sharing the parent workspace as before.
- **No silent merges**: a write task's worktree and branch are left in place for the user to review/merge by hand whenever it produced changes; clean (no-op) worktrees are torn down automatically. Collomia still never commits, merges, or pushes on its own.
- **Parent inbox**: results come back as one structured `[name] …` block per task (status, changed-file list, worktree/branch) rather than raw child transcripts. The TUI Session tab gained an **Agents** section (status glyph, read/write, changed files, worktree path) and the status bar shows an `agents N` badge while tasks are running.
- `docs/CAPABILITIES.md` and this roadmap's Phase 6 entries were updated to match.

### 2026-07-18 — Live execution, discovery, review, and resilience slice

- **Live command output**: `run_command` streams stdout/stderr into the transcript as it happens (and to stderr in headless runs) via new `tool.output` events; long builds and test runs are watchable. Fixing this uncovered and closed an `io.Copy`/`ReadFrom` bypass of the tool output cap.
- **Model discovery**: picking a provider in `/model` now queries its live catalog (OpenAI-compatible `GET /models` — Ollama, vLLM, LM Studio — and Anthropic `GET /v1/models`) and offers the discovered models in a second fuzzy picker.
- **Review workflow**: `collo review [ref]` and `/review [ref]` run a read-only, severity-ordered review of uncommitted changes or changes vs a ref, findings tied to files and lines.
- **Context inspector**: `/context` breaks down exactly what the model sees — system prompt, instructions, skills, tool results, conversation, and compaction summaries.
- **Hierarchical instructions**: a user-level `AGENTS.md` in the collomia config directory now applies everywhere, with project instructions layered on top.
- **Provider resilience**: OpenAI-compatible and Anthropic requests retry transient failures (three attempts, exponential backoff with jitter, `Retry-After`, no 4xx retries), verified with fault-injection tests.

### 2026-07-18 — Phase 7 interactive-experience slice

- **Fuzzy pickers everywhere**: one scored-subsequence picker component drives `/model` (providers), `/theme` (themes), and `/sessions` (saved sessions) — type to filter, ↑/↓, enter.
- **Live session switching**: `/sessions` resumes any saved conversation *in place* (transcript, plan, and persistence hooks all move over) and `/new` starts a fresh one; no restart needed.
- **`@` file mentions**: typing `@` at a word boundary opens a workspace-file picker and inserts the chosen relative path into the prompt.
- **Argument completion**: the slash palette now completes values (`/theme dra…`, `/autonomy …`, `/model …`, `/plan on|off`) and enter runs the completed line.
- **Workspace visibility**: the Session tab gained the live plan (status glyphs + evidence), changed-files list, session identity, and cached-token usage; the status bar shows tasks progress (e.g. `tasks 2/5`).
- **Attention bell**: approvals, agent questions, and long-turn completion ring the terminal bell so background sessions surface.

### 2026-07-18 — Phase 3 professional coding loop (P0 slice)

- **Atomic patching**: `apply_patch` multi-file change sets with validate-then-apply semantics, rollback, and machine-readable changesets.
- **Diff model + checkpoints**: every agent mutation is tracked with before/after content (`internal/diffmodel`); `/diff` shows the session diff, `/undo` reverts the last change (refusing to clobber external edits), and approval prompts now display a colorized unified diff preview.
- **Git-native inspection**: bounded read-only `git_status` / `git_diff` / `git_log` / `git_blame` tools; no commits/pushes ever.
- **Structured planning**: `update_plan` maintains a validated steps/dependencies/evidence artifact, viewable with `/tasks` and persisted with the session.
- **User questions**: `ask_user` pauses a run for a typed answer in the TUI without ending the turn.
- Remaining Phase 3 P1s (streaming PTY commands, LSP diagnostics, verification loop, `collo review`, repository indexing) and hunk-level approval are open.

### 2026-07-18 — Phase 2 durable sessions and context engine

- **Session store** (`internal/session`): append-only JSONL per session (metadata, full transcript, events, compaction markers) under the user configuration directory; atomic appends; loading tolerates a torn tail and marks dangling tool calls interrupted instead of replaying them.
- **Lifecycle**: `collo sessions list|show|fork|rename|archive|unarchive|delete`, `--resume <id>`, `--continue`, and `/sessions`; forks share history but diverge independently.
- **Context engine**: usage-anchored context estimates, cached/reasoning token parsing (OpenAI + Anthropic), automatic compaction above 80% of the window, and `/compact [focus]` — the stored transcript is never rewritten. Exit-gate fixtures cover kill/restart/resume, long-task compaction, fork independence, and no-replay recovery.
- **Phase 1 completions in the same push**: Linux Landlock sandbox backend (fs write confinement, TCP deny on ABI v4+) via a `collo __landlock` re-exec shim with a CI enforcement test; `permissions.command_env` minimal-environment mode (default when sandboxed); and `permissions.reviewer_command`, a fail-closed external reviewer whose veto escalates to the human.

### 2026-07-18 — Phase 0 baseline and Phase 1 safety slice

Phase 0 shipped in full, along with the core of Phase 1 (the "trusted beta"
and "safe command" delivery slices):

- **Stable event model** (`internal/event`): schema-versioned typed events for turns, text deltas, tool lifecycle, permission decisions, usage, warnings, and errors; consumed by the TUI, debug logging, and `collo run --jsonl`. An end-to-end fixture asserts a full tool-using run is representable by the schema.
- **Versioned, layered configuration**: `schema_version`, field-path validation errors, strict/lenient modes, and defaults → user → project → environment merging with per-key origin tracking; `collo config validate [--strict]` and `collo config show`.
- **Repository trust** (`internal/trust`): project configuration, MCP servers, skills, and instructions are quarantined until `collo trust`; trust binds to the config's SHA-256 and self-invalidates on change; the store lives outside the workspace.
- **Diagnostics**: `collo doctor` (config/layers, trust, terminal, git, providers, MCP, sandbox readiness, logs), redacted structured `--debug` logging (`internal/logging`), and a generated capability matrix (`collo capabilities`, `docs/CAPABILITIES.md`).
- **Safe command analysis** (`internal/shell`): conservative tokenizer covering quotes, operators, pipelines, redirections, substitutions, wrappers, and inline-interpreter payloads; uninspectable commands always require interactive approval — in every autonomy mode — and "always allow" never sticks for them.
- **Scoped approval rules** (`internal/policy`): ordered `allow`/`prompt`/`deny` rules matched on tool, resolved path, command executable, host, and MCP server, evaluated ahead of mode defaults; `collo policy check` tests decisions without executing.
- **Process control**: commands run in their own process group; timeout/cancellation kills all descendants on Unix (`SIGKILL` to group) and Windows (`taskkill /T`), verified by test.
- **Secret hygiene** (`internal/redact`): configured credentials plus common key shapes scrubbed from logs, JSONL events, and the audit ledger.
- **Audit ledger** (`internal/audit`): per-workspace JSONL of every permission decision (action, resources, source, matched rule) and execution outcome, stored outside the workspace.
- **Sandbox interface + macOS backend at this checkpoint** (`internal/sandbox`): Seatbelt (`sandbox-exec`) write containment and network controls with `off`/`auto`/fail-closed `require` modes; Linux/Windows still reported degraded/unavailable here and were completed by the later 2026-07-18 and 2026-07-20 waves. Enforcement was integration-tested on macOS.
- **Documentation truth pass**: `docs/SECURITY.md` states the exact guarantees and limits of every autonomy mode and the sandbox; README updated to match. CI now runs build, tests, `-race`, and vet on all three OSes.

Still open at that checkpoint: Linux (Landlock/seccomp) and Windows (AppContainer) sandbox backends, domain-scoped network grants, environment narrowing for child processes, the optional automated reviewer, and the remainder of the adversarial suite (symlink races, hard links beyond current path-guard tests). Later entries above record the Linux, Windows, environment, and reviewer completions.

### 2026-07-17 — TUI demo polish (Phase 7 pull-forward)

Selected Phase 7 interface work was pulled forward ahead of the MVP demo:

- [x] **Theme system:** ten built-in color themes (`collomia`, `synthwave`, `outrun`, `matrix`, `monokai`, `dracula`, `nord`, `tokyo-night`, `fredhutch-dark`, `fredhutch-light`), switchable at runtime with `/theme <name>` and persistable through `options.theme` in the configuration file. Each theme also sets the terminal background color to match (OSC 11, restored on exit) so theme text is never lost against the host terminal's background.
- [x] **Slash-command palette:** typing `/` opens a popup listing available commands with descriptions and filters it live as the user types; ↑/↓ selects, tab completes, enter runs the selection, esc dismisses. Prefix matches rank above substring matches.
- [x] **Status bar:** a persistent bottom bar showing provider/model, a color-coded autonomy badge (ask/workspace/autopilot), a planning badge, a live context gauge (percentage of the model window consumed, with green/yellow/red thresholds), and a spinner with elapsed time while a turn runs.
- [x] **Tabs:** `ctrl+t` cycles Chat, Session (workspace, provider, context usage, providers, tools, skills, MCP servers, and theme swatches), and Help views.
- [x] **Visual refresh:** gradient ASCII banner, badge-styled message roles, themed tool/result/error rendering, a themed permission-approval overlay, and theme-aware Glamour markdown (dark/light).
- [x] **First TUI unit tests:** palette filtering, tab cycling, theme switching, and status-bar composition are covered in `internal/tui`.

At that checkpoint, fuzzy pickers, argument completion, `@` mentions, diff/review UI, transcript copy/navigation, plain mode, keybindings, and accessibility work were still outstanding. Later entries above record those flows and bounded path-based multimodal image attachment UX as shipped or mostly complete; raw clipboard image protocols and the deeper accessibility/cross-terminal audit remain.

## Status against the PRD

Status meanings:

- **Implemented:** the core requirement works in the current product.
- **Partial:** a useful MVP version exists, but material behavior needed by the PRD or a best-in-class product is missing.
- **Outstanding:** no meaningful product implementation exists yet.

| PRD requirement | Status | Current implementation | Material gap |
| --- | --- | --- | --- |
| Go, single executable | Implemented | Go application builds to a standalone `collo` binary; tagged workflows publish SHA-256 manifests, a deterministic CycloneDX SBOM, and GitHub/Sigstore provenance/SBOM attestations. | Native platform code signing/notarization, self-update, and broader package-manager distribution. |
| macOS, Linux, Windows | Partial | Six cross-built binaries are tested on all three operating systems; checksum-verifying shell and PowerShell installers stage and validate replacements while preserving an existing binary on failure. | Homebrew/Scoop/Winget/Linux packages, native signing/notarization, and broader clean-machine update/rollback campaigns. |
| Beautiful interactive TUI | Implemented | Bubble Tea/Lip Gloss/Glamour rendering, nineteen themes (including colorless `plain` with automatic `NO_COLOR` support) with terminal-background matching, palette with argument completion, fuzzy pickers (models/themes/sessions/files/folders/agents/skills/MCP servers/images), bounded prompt-from-file, `@` mentions, bounded typed image attachments with pending list/detach/status, live streamed command output, diff previews and hunk-level pending-write/delegated-worktree selection, a responsive unified/side-by-side session diff browser with safe external-editor handoff, searchable/copyable raw transcript view, complete resumed transcript/tool rendering, prompt history and per-session drafts, a busy-safe local-command lane, configurable keybindings, floating approval/question/integration dialogs, syntax-highlighted transcript code, an asynchronous Git/runtime-health/session-activity dashboard, a parent/child agent tree with recent output and controls, tasks badge, bell + OSC 9 desktop notifications, a loopback-only browser terminal (`collo --web`), and a growing TUI test suite. | Raw clipboard image protocols/inline pixel previews, line-level ordinary pending-write selection, and deeper accessibility/cross-terminal passes. |
| Permission prompts | Implemented | Ordered allow/prompt/deny rules on tool/path/executable/host/server, conservative command analysis (uninspectable commands always prompt), diff previews at approval, an external reviewer hook, a persistent audit ledger, repository trust, and OS sandbox backends on macOS/Linux/Windows with capability-aware degradation/fail-closed reporting. | Domain-scoped network grants and completing the remaining adversarial suite. |
| Workspace autopilot and explicit outside access | Partial | `ask`/`workspace`/`autopilot` modes with canonical path and symlink containment; outside access opt-in; OS sandboxes confine writes on every platform and can confine user-data reads, minimal child environments are available, and process groups/Windows Job Objects kill descendants. | Network grants are all-or-nothing; read confinement is opt-in on macOS/Linux with documented system/metadata limits; Linux network denial is TCP-only before Landlock ABI v10. |
| Slash commands | Implemented | Status, model (with capability-aware live discovery), models (capabilities and availability), context (inspector), plan, tasks, agents, prompt-from-file, ps (background processes), autonomy, theme, skills, MCP lifecycle/resources/prompts, tools, interactive diff, transcript search/copy, undo, review, verify, sessions, new, compact, config, clear, help, quit — plus CLI: config, trust, doctor, capabilities, policy, sessions, skills, persistent MCP management/testing, shell completion, review, verify, and offline JSONL replay/validation. | Provider/MCP login and update await their underlying features. |
| Skills / `skills.md` | Implemented | Agent Skills-standard directories (SKILL.md front matter + bundled scripts/references/assets), project and global scopes with deterministic precedence and shadow reporting, validation warnings, on-demand `load_skill` with a bundle map, and the full `collo skills` lifecycle (list/show/new/install/update/remove/enable/disable) with hash/version metadata. | Remote skill registries/marketplaces, automatic refresh notifications, and per-skill trust prompts beyond workspace trust. |
| MCP | Partial | Official Go SDK with trusted stdio and Streamable HTTP servers; runtime health/add/remove/enable/disable/refresh/reconnect; tools, resources, prompts, typed rich-content preservation including bounded image passthrough for capable Anthropic/Bedrock turns, elicitation, progress, safe tool/resource/prompt list-change handling, explicit `EXTERNAL_MCP_DATA` prompt-injection/provenance framing, protocol/subset conformance fixtures, and server identity/definition pinning. | OAuth/login, experimental tasks, resource subscriptions, audio passthrough, annotations, and argument-level permission scoping. |
| Subagents | Partial | `delegate` fans up to 6 tasks through a session-wide FIFO scheduler with configurable global/per-provider limits; named profiles set role/model, tool/skill allowlists, iteration/token/time budgets, and permissions that only tighten the parent; write agents use isolated worktrees and separate tools/permissions/audits; bounded structured results carry status, evidence, usage, files/hunks, and worktree details; plan-step association validates dependencies; common-base hunk analysis flags overlap; durable parent/child TUI state supports boundary-safe steering, individual stop, manual selective integration, opt-in freshness-bound primary review/application, and non-replaying resume. | Reasoning/cost budgets, automatic plan-node execution/replanning, full child-transcript audit, conflict serialization/reconciliation, and safe pending-work restart. |
| Planning mode | Implemented | Read-only planning mode plus a structured, validated plan artifact (`update_plan`: steps, status, dependencies, evidence) persisted with the session, shown in `/tasks` and the Session tab; `ask_user` provides the clarification loop. | User-editable/approvable plan steps and automatic re-planning. |
| Local Ollama, vLLM, LM Studio | Partial | OpenAI-compatible adapter with live model discovery (`GET /models`), declared adapter/model capabilities and configured context metadata in `/model`/`/models`, endpoint availability through the live catalog probe, and active-request circuit health in `/status`; `collo doctor` checks endpoint configuration. | Richer model metadata where runtimes expose it, dedicated runtime lifecycle guidance, and an explicit lightweight health endpoint where available. |
| OpenAI-compatible endpoints | Partial | Streaming Chat Completions, function tools, typed user-image content, normalized provider-supplied reasoning/tool/usage deltas, capability-aware model discovery/preflight, cached/reasoning usage parsing, provider-driven `max_tokens`/`max_completion_tokens` and default-temperature negotiation, classified errors, configurable timeouts, shared retry/circuit health, and recorded protocol contracts. | General OpenAI Responses API selection, structured output, richer multimodal content, and opt-in live compatibility contracts. |
| Anthropic-compatible endpoints | Partial | Streaming Messages API, tool use, normalized provider reasoning/tool/usage deltas, capability-aware model discovery/preflight, cache-read usage parsing, classified errors, configurable timeouts, shared retry/circuit health, and recorded protocol contracts. | Signed thinking-block round-trip, prompt cache creation, richer content blocks, and opt-in live compatibility contracts. |
| AWS Bedrock | Partial | Native `ConverseStream` with text/tool/reasoning/usage events and classified in-stream errors; `auto`/`sigv4`/`bearer` authentication covers the AWS SDK credential chain (including temporary session credentials, SSO, roles, and workload identity) plus short- and long-term Bedrock API keys; auth-aware doctor output, redaction, capability preflight, retry/circuit health, and recorded contracts. | Bedrock API-key mint/refresh/login lifecycle, model discovery, guardrails/trace fields, richer reasoning round-trip, and live contracts. |
| AWS Bedrock Mantle | Partial | Responses-style SSE (plus synchronous JSON fallback), normalized tool/reasoning/usage/warning events, capability preflight, classified failure/retry/circuit behavior, and recorded success/error/truncation contracts. | Richer Responses features and live contracts. |
| Azure OpenAI | Partial | Deployment-scoped Chat Completions route, API version, API key/static bearer/refreshable `DefaultAzureCredential` auth, documented scope plus tenant/authority overrides, RBAC diagnostics, capability preflight, reasoning-model request-parameter negotiation, and shared failure/retry/circuit behavior. | OpenAI v1/Responses support, deployment discovery, cloud-specific tested presets, and live endpoint qualification. |
| Azure AI Foundry | Partial | OpenAI/v1 and Anthropic endpoint adapters with API key/static bearer/refreshable `DefaultAzureCredential` auth, Foundry scope plus tenant/authority overrides, RBAC diagnostics, capability preflight, reasoning-model request-parameter negotiation on the OpenAI route, catalog discovery where supported, and classified failure/retry/circuit behavior. | Foundry deployment discovery, project/resource endpoint variants, Responses API, governance/trace integration, cloud-specific tested presets, and live endpoint qualification. |

## What “best in class” means for Collomia

Feature count alone is not the target. A best-in-class terminal agent should meet these product properties:

| Property | Required product behavior |
| --- | --- |
| Safe by construction | Model-generated code runs inside a real platform sandbox. Filesystem, network, process, and secret access are least-privilege capabilities with visible, auditable grants. |
| Durable and recoverable | Every session can survive a crash, resume, fork, compact, rewind, and explain what changed. User-approved edits have a recovery path. |
| Effective on real repositories | The agent can search, patch, compile, test, inspect diagnostics, manage long-running processes, and review diffs without losing state or flooding context. |
| Context efficient | Exact or provider-reported usage, automatic compaction, pinned context, hierarchical repository instructions, and bounded tool results support long tasks. |
| Reliably multi-provider | Providers expose declared capabilities, consistent events, robust auth, retries/backoff, model discovery, and actionable diagnostics rather than merely sharing an HTTP shape. |
| Genuinely multi-agent | Parallel specialist agents can work in isolated contexts and worktrees with explicit budgets, progress, cancellation, and deterministic handoff. |
| Extensible without becoming unsafe | MCP, skills, hooks, and custom tools have lifecycle management, trust, validation, permissions, and observable execution. |
| Excellent interactively and headlessly | The TUI is fast and legible; automation gets stable JSONL events, schemas, exit codes, and resumable sessions. |
| Measurably good | Security, reliability, coding success, latency, token efficiency, and regression behavior are continuously evaluated. |

## Priorities

- **P0 — foundation or safety gate:** required before advertising unattended/autopilot use as safe.
- **P1 — competitive core:** required for a strong daily-driver beta.
- **P2 — best-in-class differentiation:** raises usability, scale, and ecosystem quality.
- **P3 — expansion:** valuable after the local terminal product is dependable.

## Phase 0 — Establish a trustworthy beta baseline

**Goal:** Make the current MVP explicit, diagnosable, and safe to evolve.

### Deliverables

- [x] **P0 — Product capability matrix:** `collo capabilities [--markdown]` and the generated [docs/CAPABILITIES.md](CAPABILITIES.md) distinguish implemented, experimental, and unsupported behavior. *(2026-07-18)*
- [x] **P0 — Stable runtime event model:** `internal/event` defines schema-versioned typed events for turns, text deltas, tool lifecycle, permission decisions, usage, warnings, and errors; used by the TUI, debug logging, and `collo run --jsonl`. Reasoning deltas, file changes, and plan updates are defined in the schema and will be emitted when those features land. *(2026-07-18)*
- [x] **P0 — Versioned configuration:** `schema_version`, field-level validation errors, strict/lenient modes, and `collo config validate [--strict]`. Migrations become relevant at the first schema bump. *(2026-07-18)*
- [x] **P0 — Configuration layering:** Defaults, user config, project config, and environment merge with per-key origin tracking, inspectable via `collo config show`. Profiles remain future work. *(2026-07-18)*
- [x] **P0 — Repository trust:** Project config, MCP servers, skills, and instructions are quarantined until `collo trust`; trust is hash-bound and self-invalidates on change. *(2026-07-18)*
- [x] **P0 — Diagnostics:** Redacted structured `--debug` logs and `collo doctor` covering config, trust, terminal, Git, providers, MCP, sandbox readiness, and log storage. *(2026-07-18)*
- [x] **P0 — Test seams:** Provider clients are injectable end to end; an app-level fixture harness drives a scripted provider through the real registry/permission/audit pipeline and asserts the event stream. Clock/filesystem seams remain ad hoc where not yet needed. *(2026-07-18)*
- [x] **P1 — Documentation truth pass:** [docs/SECURITY.md](SECURITY.md) states that approval checks are in-process policy, the exact properties of each autonomy mode, and the sandbox's guarantees and limits; README updated to match. *(2026-07-18)*

### Exit gate

- Configuration errors are actionable and precedence is inspectable.
- A recorded run can be represented entirely by the stable event schema.
- Project-controlled executable configuration is disabled until trust is established.
- CI passes unit tests, race tests, static analysis, and at least one end-to-end fixture on all three operating systems.

## Phase 1 — Build the real safety boundary

**Goal:** Make unattended work safe by enforcement, not prompt wording or regular expressions.

### Deliverables

- [x] **P0 — Cross-platform sandbox interface:** macOS Seatbelt, Linux Landlock, and Windows 11 AppContainer + Job Object backends all implement the shared interface. Backends declare their actual write/read/network/process capabilities; `auto` visibly degrades and `require` rejects any requested protection the active backend cannot enforce. Windows uses only inbox APIs and has native CI write- and network-confinement fixtures. *(macOS/Linux 2026-07-18; Windows and capability-aware enforcement 2026-07-20; expanded native network fixtures 2026-07-21.)*
- [x] **P0 — Separate capabilities:** Independently control filesystem reads, filesystem writes, executable launch, child processes, network egress, environment variables/secrets, and additional readable/writable roots. *(Completed 2026-07-24: write confinement, opt-in user-data read confinement, all-or-nothing network, explicit read-only/read-write roots, minimal/full environment, and process isolation are independently configured and reported; executable allowlisting (`permissions.commands: allowlist`) and the per-capability grant UI shipped with the host-scoped policy wave. Windows AppContainer always confines user-data reads; macOS/Linux preserve broad reads until the user opts in.)*
- [x] **P0 — Scoped approval rules:** Ordered `allow`/`prompt`/`deny` rules matched on tool, resolved path, command executable, host, and MCP server (`permissions.rules`), evaluated before mode defaults; deny rules cannot be overridden by session grants. *(2026-07-18)*
- [x] **P0 — Safe command analysis:** `internal/shell` conservatively parses quotes, pipelines, redirections, subshells, wrappers, and inline-interpreter payloads; uninspectable forms require interactive approval in every mode and never receive persistent grants. Hard denials retained as defense in depth. *(2026-07-18)*
- [ ] **P0 — Network policy:** Default agent-generated commands and external tools to no network, with domain- or endpoint-scoped grants and visible proxy/DNS behavior. *(Partial 2026-07-20: all three sandbox backends honor all-or-nothing `sandbox_allow_network`; capability reporting distinguishes no isolation, TCP-only Landlock ABI v4–v9, and full TCP/UDP Landlock ABI v10+/macOS/Windows denial. For usability, sandboxing remains opt-in and command networking currently defaults to enabled when the user first selects `auto`. Domain-scoped grants, proxy/DNS policy, Windows loopback ergonomics, and a deliberate decision on the eventual install-time default remain.)*
- [x] **P0 — Process control:** Commands run in owned process groups with timeouts and output caps; cancellation and timeout kill all descendants on Unix and Windows, verified by test. Resource (CPU/memory) limits remain future work. *(2026-07-18)*
- [x] **P0 — Secret hygiene:** Configured credentials and common key shapes are redacted from debug logs, JSONL events, permission summaries, and the audit ledger. `permissions.command_env: minimal` strips the child-process environment to basics, and is the default whenever the sandbox is enabled. *(2026-07-18; extended 2026-07-25: PEM private key blocks — `RSA`, `EC`, `OPENSSH`, `ENCRYPTED`, PKCS#8, PGP — are removed whole while public keys and certificates are deliberately preserved, GitLab/Google/npm/Stripe and the remaining GitHub token shapes were added, and the two limits redaction cannot cover are now stated in the package and in SECURITY.md: it does not sit between a tool result and the provider, and it is applied to bounded chunks rather than an unbounded stream. Reaching a credential store is therefore governed by `permissions.protect_credentials` rather than by redaction.)*
- [x] **P0 — Credential-store protection:** Reaching a conventional credential location — SSH and GPG private keys, cloud CLI token caches, registry authentication files, `.env` files, Collomia's own provider configuration — is its own permission decision, governed by `permissions.protect_credentials` (`off`/`prompt`/`deny`, default `prompt`) and carried on the preset ladder with the same monotonic clamping as every other containment field. The gate sits after rule evaluation and before the implicit in-workspace read path, so a `deny` rule still wins and a rule naming the path is honored, while a blanket `allow` rule, a bare `**` path glob, a tool-wide session grant, and `autopilot` all decline to cover it. One narrow session grant is offered, scoped to the exact credential target shown: it never covers the tool, the directory, a sibling file that classifies the same way, or anything past this process, and it is not offered at all under `deny`. Recognition is by conventional path rather than content inspection, and describes what a command's text names rather than what a process opens — enforcing the latter remains sandbox read confinement's job. Public keys, `known_hosts`, `authorized_keys`, and example environment files are excluded explicitly, and a documentation guard fails if the published location lists and the implementation diverge. *(2026-07-25. The wave also consolidated command-shaped `tools.Action` construction into one function after `collo policy check` was found reporting the wrong decision for a credential-reaching command because it assembled its own action and missed the new field — the same defect shape that let `Rule.Host` ship inert. A test now fails on any second construction site.)*
- [x] **P1 — Approval comfort:** The approval dialog advertised a tool-wide "always" for a credential-reaching action while the permission layer declined to record it, so the button did nothing and the next identical action prompted again — the rule had been copied into the dialog and the key handler and both copies went stale when credential stores were added. Availability is now a single `permission.Request.AllowsAlways` field, and a test fails on any file outside the permission package that re-derives it. A credential prompt gained one narrow session grant scoped to the exact target shown (never the tool, the directory, a sibling file that classifies the same way, or anything past the process; not offered under `deny`, and invalidated by raising the setting to `deny` mid-session), its own header/accent with the file named ahead of the kind of secret, and a printed configuration rule that ends the asking permanently — omitted for uninspectable commands, where no rule would help. `collo doctor` now reports the permission stance and warns when a project's containment weakening was refused; the Session tab's Security block is grouped into policy/enforcement/session with degraded sandboxing and refused settings marked rather than listed as ordinary rows. *(2026-07-25)*
- [x] **P0 — Permission audit ledger:** Per-workspace JSONL ledger (outside the workspace) records requested action, normalized resources, decision source, matched rule, timestamp, and execution outcome. *(2026-07-18)*
- [x] **P1 — Policy tester:** `collo policy check <command…>` explains the analysis, matched rule, and decision without executing. *(2026-07-18)*
- [x] **P1 — Optional automated review:** `permissions.reviewer_command` receives each would-be auto-approved non-read action as JSON; a deny verdict (or reviewer failure — fail-closed) escalates to an interactive prompt, preserving the human override; the reviewer can only tighten decisions. *(2026-07-18)*
- [x] **P1 — Threat model and adversarial suite:** Traversal, concurrent parent-symlink swaps, final symlinks, hard-link writes/deletes, shell expansion, interpreters, encoded commands, nested shells, subprocess escape, environment leakage, prompt-injected MCP output, and native network denial are covered. Structured file mutations use rooted atomic publication; MCP model-visible content carries explicit external-data provenance; a credential-free agent evaluation proves forged external permission text cannot widen file-write authorization. Seatbelt, Landlock, and AppContainer network fixtures exercise their documented platform semantics. *(Completed 2026-07-21; sustained fuzzing and independent review remain Phase 8 security-program work.)*

### Exit gate

- With the corresponding sandbox capabilities requested, hostile model output cannot read ordinary user data or write outside granted roots, or reach the network, even after the user enables workspace autopilot; documented system-runtime, metadata, and platform-network limitations remain explicit.
- Cancellation and timeout terminate descendant processes on macOS, Linux, and Windows.
- Every privileged action is reconstructable from the audit ledger.
- The security documentation names the guarantees and known limitations of every platform backend.

## Phase 2 — Durable sessions and a context engine

**Goal:** Support long, resumable work without silently losing history or overflowing the model context.

### Deliverables

- [x] **P0 — Durable event store:** `internal/session` persists metadata, the full message transcript, runtime events, and compaction markers as an append-only JSONL log per session (atomic appends, torn-tail tolerant), stored outside the workspace. JSONL was chosen over SQLite to keep the binary dependency-free; the record schema is versioned via the event schema. *(2026-07-18)*
- [x] **P0 — Session lifecycle:** `collo sessions list|show|fork|rename|archive|unarchive|delete`, `collo --resume <id>`, `collo --continue`, and `/sessions`/`alt+s` in the TUI. Initial resume and live switches reconstruct the complete visible transcript including saved tool results without executing them; session-scoped prompt history and unsent drafts follow the active conversation. *(2026-07-18; TUI continuity completed 2026-07-21.)*
- [x] **P0 — Crash recovery:** Loading discards a torn final line only; a tool call with no recorded result is marked interrupted with an explicit "may or may not have run — verify before repeating" note, and is never replayed. Covered by tests. *(2026-07-18)*
- [x] **P0 — Context accounting:** Context estimates combine the provider-reported input size of the last request with a character estimate of messages added since; cached and reasoning token counts are parsed from OpenAI and Anthropic responses and shown in `/context`. Cost display awaits pricing data. *(2026-07-18)*
- [x] **P0 — Automatic and manual compaction:** The loop compacts automatically above 80% of the model window, keeping the most recent messages verbatim and never splitting a tool call from its results; `/compact [focus]` compacts on demand. The full transcript survives in the session log. Verified by a fixture that completes a long task through a tiny window. *(2026-07-18)*
- [x] **P0 — Context policy:** System/repository instructions and the current structured plan are pinned outside compactable message history; old tool output is summarized while recent failures retain exact bounded evidence; oversized returned strings become quota-bound session references readable in ranges without replay. `/context` reports pinned state and referenced-result storage. *(Completed 2026-07-21.)*
- [ ] **P1 — Targeted rewind/checkpoints:** Let users rewind conversation, code edits, or both. State clearly that shell/external changes may require Git or an isolated worktree to recover. *(Substantially complete 2026-07-21: `collo sessions rewind <id> <turn>` and `/rewind [turn]` create a non-destructive branch at a completed conversational turn; the original session, workspace, and external state remain untouched, and no recorded tool executes. Direct file edits already have external-edit-safe `/undo`; a coupled conversation+workspace checkpoint remains.)*
- [x] **P1 — Branching and side investigations:** `collo sessions fork <id>` copies history into an independent session (shared immutable past, divergent future), verified by test. In-TUI side-question scoping remains. *(2026-07-18)*
- [x] **P1 — Hierarchical instructions:** A user-level `AGENTS.md`/`COLLOMIA.md` in the collomia config directory now applies to every workspace, layered before (trusted) project instructions with documented precedence and the existing size limits. Nested per-directory files within a repository remain future work. *(2026-07-18)*
- [x] **P1 — Context inspector:** `/context` now breaks down the model-visible context: system prompt, project instructions, skills summary, tool-result volume, conversation counts, and active compaction summaries, alongside usage and the window gauge. *(2026-07-18)*

### Exit gate

- A session can be killed, restarted, and resumed without losing accepted conversation or corrupting state.
- A long fixture exceeding a model context window completes through automatic compaction while retaining critical constraints and plan state.
- Forked sessions have independent active context and shared immutable history.
- Tool mutations are never silently duplicated during recovery.

## Phase 3 — Complete the professional coding loop

**Goal:** Make editing, testing, reviewing, and recovery fast enough for daily repository work.

### Deliverables

- [x] **P0 — Atomic patch tool:** `apply_patch` applies multi-file change sets (update/create/delete) atomically: every operation validates against current content first, failures name the exact operation and suggest re-reading, partial applies roll back, and the result is a machine-readable changeset. *(2026-07-18)*
- [ ] **P0 — Unified diff model:** Track every agent file change independent of rendering; support file, hunk, and line-level review and approval. *(Partial 2026-07-18: `internal/diffmodel` records every mutation's before/after, `/diff` renders the session diff, and approval prompts show a colorized unified diff preview for write/edit/patch. A pending `write_file` approval with 2+ hunks now offers hunk-level selective approval — press `h` to accept/reject each hunk independently before the write lands, computed via `diffmodel.ParseHunks`/`ApplyHunks` against a fresh read of the file. `edit_file` and `apply_patch` stay file-level (a single-hunk find-replace and a multi-file changeset respectively); line-level review remains.)*
- [x] **P0 — Checkpoint and undo:** Every write/edit/patch snapshots prior content; `/undo` reverts the most recent change and refuses to clobber files modified outside the agent since. Works without Git and regardless of tree cleanliness. *(2026-07-18)*
- [x] **P0 — Git-native inspection:** `git_status`, `git_diff` (worktree/staged/ref, per-path, stat), `git_log`, and `git_blame` are read-only, output-bounded, flag-injection-guarded tools; the agent never commits, branches, or pushes. Changed-file selection UI remains with the diff-review work. *(2026-07-18)*
- [x] **P0 — Structured planning:** The `update_plan` tool maintains a validated plan artifact (goal, steps with status/dependencies/evidence) shown via `/tasks` and persisted/restored with the session; planning mode still enforces read-only tools while allowing plan updates. *(2026-07-18)*
- [x] **P0 — User-question primitive:** The `ask_user` tool pauses the run for a typed answer or option choice in the TUI (esc declines explicitly); headless runs simply don't expose the tool. *(2026-07-18)*
- [x] **P1 — Live command execution:** Stream stdout/stderr, support PTY programs where safe, manage background jobs, show process status, and allow targeted stop/restart. *(2026-07-18: stdout/stderr stream live into the TUI transcript and headless stderr via `tool.output` events — this also fixed an io.Copy/ReadFrom bypass of the output cap. Background jobs: `start_process` launches a detached command (dev server, watcher, long test run) under the same denied-pattern/shell-analysis/sandbox/minimal-env policy as `run_command`, with `list_processes`, `process_output` (bounded 64 KiB retention, optional tail), and `stop_process` (kills the whole process group); the TUI shows them in the Session tab and a `procs N` status badge, `/ps` lists and `/ps stop <id>` stops one, and every background process is killed at session exit — including those started by delegated sub-agents in worktrees. PTY: `run_command` accepts `pty: true` on Unix (creack/pty; setsid session, group kill on timeout, isatty-faithful), verified by test; Windows ConPTY remains future work and reports a clear error.)*
- [ ] **P1 — Diagnostics and LSP:** Expose project diagnostics, definitions/references, symbols, formatting, and code actions through available language servers with bounded context output. *(Partial 2026-07-18: the `diagnostics` tool runs a real language server over requested files via a minimal stdio JSON-RPC LSP client (`internal/lsp`: initialize, didOpen, publishDiagnostics) and returns severity-ordered findings with exact files/lines; gopls, pyright, typescript-language-server, and rust-analyzer are auto-detected on PATH, overridable per language via the `lsp` config map; verified against a scripted fake server and live gopls. Extended 2026-07-25: `find_definition`, `find_references`, and `format_file` share the same client, locating a position from a file, a 1-based line, and the symbol's own text rather than a UTF-16 column; formatting is an ordinary tracked, undoable write that refuses to publish if the file changed while the server was formatting it, and the client's position arithmetic, edit application, overlap rejection, and CRLF handling are covered by platform-independent tests plus a live gopls round trip. Extended 2026-07-26: a server that does not implement a request is reported as a named configuration answer rather than the raw protocol string, verified against pyright (no formatter) and python-lsp-server (all three). Safe code actions remain: they need `codeAction/resolve` round trips and workspace edits that can span files.)*
- [x] **P1 — Verification loop:** Detect relevant build/test/lint commands, propose them, show live progress, summarize failures, and associate evidence with plan steps. *(2026-07-18: `detect_verification` inspects go.mod/package.json/Cargo.toml/pyproject.toml or requirements.txt/Makefile and reports the real build/lint/test commands for that project — no guessing; `collo verify [focus]` and `/verify [focus]` run a canned loop that records each command as a plan step with `update_plan`, executes it with the existing live-streamed `run_command`, and marks the step done or blocked with the exact command outcome as evidence.)*
- [x] **P1 — Review workflow:** Add `collo review` and `/review` for uncommitted changes, a commit, a branch comparison, or custom instructions, with findings linked to exact files/lines. *(2026-07-18: `collo review [ref] [instructions…]` and `/review [ref] [instructions…]` run a read-only review of uncommitted changes (`-` or empty ref) or changes vs any ref, findings ordered by severity and tied to files/lines; trailing words become custom reviewer instructions layered onto the standard checks.)*
- [x] **P1 — Repository indexing:** Introduce ignore-aware, incremental symbol/text indexing for large repositories while retaining fast `rg`-style direct search. *(2026-07-18: `internal/index` maintains an mtime/size-cached definition index — only changed files re-parse on refresh, deletions drop out, and `.git`/`node_modules`/`vendor`/build dirs are skipped; line-regex extraction covers Go, Python, JS/TS(X), and Rust functions, methods, types, classes, interfaces, constants, and enums. The `search_symbols` tool queries it with exact-then-prefix-then-substring ranking and optional kind filters, while `search_files` remains for references and arbitrary text. Semantic (parser-accurate) extraction and reference indexing are future refinements.)*

### Exit gate

- Users can inspect and accept/reject agent changes at hunk granularity (currently `write_file`; `edit_file`/`apply_patch` remain file-level) and undo direct edits.
- Interrupted and background commands have correct status and can be cancelled without orphaning descendants.
- Plans are persistent state, not only prose in a chat message.
- A representative multi-language fixture can be searched, edited, formatted, tested, diagnosed, and reviewed entirely through structured tools.

## Phase 4 — Production-grade provider platform

**Goal:** Turn protocol adapters into a reliable provider layer with consistent product behavior.

### Deliverables

- [x] **P0 — Capability registry:** Every configured provider/model selection carries a normalized four-state declaration for tools, streaming, reasoning, images, structured output, context size, token counting, prompt caching, parallel tool calls, model discovery, and endpoint constraints. It describes Collomia's effective adapter support and retains `unknown` when an upstream catalog omits model-level metadata; known contradictions fail before provider network I/O. *(2026-07-19)*
- [x] **P0 — Model/deployment discovery:** OpenAI-compatible (`GET /models` — including Ollama, vLLM, LM Studio) and Anthropic (`GET /v1/models`) catalogs merge with configured defaults/context declarations; `/model` exposes compact capabilities and `/models` reports detailed capabilities plus live available/unavailable/unverified state. Adapters without a catalog remain explicitly unverified. Azure/AWS-specific deployment discovery remains a provider-specific P1 refinement. *(2026-07-19)*
- [x] **P0 — Consistent streaming events:** OpenAI Chat Completions, Anthropic Messages, Responses/Mantle, and native Bedrock `ConverseStream` normalize text, provider-supplied reasoning, incremental tool-call arguments, usage snapshots, warnings, and classified errors. JSONL exposes `tool.call.delta` without ever executing incomplete JSON; Bedrock decodes CRC-checked event frames and requires `messageStop`, while Responses requires a terminal event and retains synchronous JSON compatibility. In-stream failures are not replayed after deltas may have escaped. *(2026-07-19)*
- [x] **P0 — Resilience:** Built-in HTTP adapters return a shared classified error with retryability/status/`Retry-After`/request-ID metadata (also structured on JSONL error events), retry replayable network and 408/429/5xx/529 failures up to three attempts with bounded backoff+jitter while leaving ordinary 4xx alone, and enforce configurable connect/whole-request/stream-idle timeouts. Responses/Mantle and Bedrock have retry parity with OpenAI/Anthropic. Three complete transient failures open a credential-safe 30-second health circuit shown in `/status`; a single half-open probe restores it, and the breaker never wraps tool execution. *(2026-07-19)*
- [x] **P0 — Provider contract suite:** Credential-free CI fixtures cover tool definitions/calls, normalized streaming signals, synchronous Responses fallback, usage, HTTP/in-stream failures, retry/truncation, and post-delta cancellation for OpenAI Chat Completions, Anthropic Messages, Responses/Mantle, and Bedrock ConverseStream. A strict, double-opt-in live harness exercises a synthetic streamed tool call, tool-result round trip, text, and usage against configured real endpoints without executing the tool or storing credentials; release manifests can require all four families. *(2026-07-19)*
- [x] **P1 — Secure credential lifecycle:** Use the operating-system keychain where practical; support login/logout/status flows; never require long-lived secrets in project files. *(Completed for provider credentials 2026-07-25: `collo auth set|list|status|rm|import` stores keys in the macOS Keychain (via Apple's signed `/usr/bin/security`, keeping the binary cgo-free) or the Windows Credential Manager (`CredWriteW` through the already-present `golang.org/x/sys`), with no new dependency. The store is optional and is consulted only after `api_key`, `api_key_env`, and a provider family's own variable, so an environment variable always wins; a name index is checked before any platform call, so a machine that has stored nothing never raises a keychain dialog; Entra and SigV4 are excluded because they hold no static secret; and no value is ever printed back. Linux has no backend and no encrypted-file fallback by design — the absence is reported by `collo auth` and `collo doctor` rather than degraded around. MCP server credentials remain Phase 5 OAuth work.)*
- [ ] **P1 — Azure identity:** Support `DefaultAzureCredential`-style Microsoft Entra authentication and token refresh, the documented Azure OpenAI/Foundry scopes, deployment discovery, resource/project endpoint variants, private/sovereign endpoints, and actionable RBAC diagnostics. *(Partial 2026-07-19: explicit Entra mode now covers Azure OpenAI, Foundry OpenAI/v1, and Foundry Claude with SDK credential chaining, in-memory proactive refresh, documented provider-family scopes, tenant/custom-authority overrides, private `base_url` support, credential-safe doctor output, RBAC hints, and validation/redaction safeguards. Deployment/project discovery, project endpoint routing, and tested sovereign-cloud presets remain.)*
- [ ] **P1 — AWS identity diagnostics:** Report the selected profile/region/credential source safely, support SSO refresh behavior through the SDK, and surface model access/capability errors clearly. *(Partial 2026-07-19: `collo doctor` reports native Bedrock's selected SigV4/bearer family and configured profile or bearer environment without values; SigV4 uses the SDK chain and its normal temporary-credential/SSO refresh behavior. Exact resolved SDK credential-source reporting, API-key generation/refresh, model-access diagnostics, and live validation remain.)*
- [ ] **P1 — Modern API features:** Add OpenAI/Azure Responses API support, richer Anthropic content/thinking blocks, provider-native caching, structured output, and multimodal input where capabilities permit. *(Partial 2026-07-21: the provider-neutral message model and existing OpenAI Chat Completions, Azure OpenAI/Foundry OpenAI, Anthropic/Foundry Claude, Bedrock ConverseStream, and Responses/Mantle adapters now carry bounded typed user images. General OpenAI/Azure Responses routing, non-image media, structured output, caching, and richer thinking round-trip remain.)*
- [ ] **P1 — Routing and fallback:** Allow explicit ordered fallbacks by capability, health, cost, or locality. Never switch provider/model silently when privacy, residency, or model behavior could change.
- [ ] **P1 — Usage and budgets:** Normalize input/output/cached/reasoning tokens and cost estimates; enforce per-turn/session/agent budgets with provider-specific caveats.
- [ ] **P2 — Setup wizard:** Discover local runtimes, validate endpoints and credentials, test a deployment, and write a minimal user-level provider profile.

### Exit gate

- Each advertised provider passes the same tool-call, streaming-or-declared-nonstreaming, cancellation, usage, retry, and redaction contract.
- Azure OpenAI and Azure AI Foundry work with API keys and automatically refreshed Entra credentials; the active resource/project/deployment is diagnosable without exposing secrets.
- The runtime rejects an unsupported model capability before sending an invalid request or offers an explicit alternative.
- Provider failure does not corrupt session state or duplicate mutating tool calls.

## Phase 5 — Complete MCP, skills, and extension lifecycle

**Goal:** Make Collomia safely extensible and compatible with the wider agent-tool ecosystem.

### Deliverables

- [ ] **P0 — MCP lifecycle:** Add runtime list/add/remove/enable/disable/reconnect/login/logout operations plus detailed health, negotiated capabilities, and initialization errors. *(Mostly complete 2026-07-20: the manager tracks every configured server — including untrusted, disabled, and failed ones — with per-server status, initialization errors, negotiated capabilities, and remote server identity; `/mcp status|ping|reconnect|enable|disable|add|remove` manage the live session, while `collo mcp list|show|add|remove|enable|disable|test` safely manages persistent project/global definitions and connection-only diagnostics. login/logout await the OAuth deliverable below.)*
- [ ] **P1 — MCP resources and prompts:** Discover, browse, subscribe to, and attach resources; expose server prompts as explicit user-selectable commands/templates. *(Mostly complete 2026-07-19: `/mcp resources <server>` browses published resources and `/mcp resource` previews one; the agent lists and reads them through `list_mcp_resources`/`read_mcp_resource` — external-risk tools whose permission assessment is scoped to the server named in the arguments; `/mcp prompts <server>` lists templates with their arguments and `/mcp prompt <server> <name> key=value…` expands one into the input box for user review before sending. Capability checks produce actionable errors when a server didn't negotiate the feature. Resource subscriptions/change notifications remain with the tasks-and-notifications deliverable.)*
- [x] **P1 — MCP elicitation and progress:** Form-mode elicitation renders as typed user questions through the TUI's ask flow (field titles/descriptions, enum options, boolean choices, required-field enforcement, type coercion for number/integer/boolean; esc or any invalid answer declines the whole request rather than defaulting to acceptance) — the user's answers go straight back to the server without entering model context. URL-mode elicitation is declined outright, and headless runs never advertise the capability. Progress notifications stream live into the transcript during tool calls via a per-call progress token routed through the existing tool-output streaming path; cancellation rides the existing per-call context/timeout. *(2026-07-19)*
- [ ] **P1 — Rich MCP content:** Preserve text, images, audio, embedded resources, resource links, structured content, and annotations without flattening everything into an untyped string. *(Partial 2026-07-21: text/structured/embedded/link content remains typed; image bytes now survive the tool boundary, enter bounded integrity-checked session attachment storage, and pass to capable Anthropic/Bedrock tool-result turns while retaining a visible type/size marker. OpenAI-compatible tool messages remain marker-only for portability. Audio passthrough and annotation handling remain.)*
- [ ] **P1 — MCP tasks and notifications:** Support negotiated task/progress capabilities and list-change notifications; refresh catalogs safely. *(Partial 2026-07-20: standard tool/resource/prompt list-change handlers now track catalog revisions; tools hot-refresh through a complete validate-then-atomic-swap path that preserves the last-known-good registry on failure, while resource/prompt notifications remain pending until their next live listing. Stale-session callbacks are rejected and refresh bursts are serialized/coalesced. Progress already streams end to end. Experimental task execution/status remains unsupported pending a stable SDK surface.)*
- [ ] **P1 — MCP authentication/transports:** Add standards-based OAuth and any compatibility transport still intentionally supported, with credentials stored outside project config.
- [x] **P1 — MCP provenance and prompt-injection boundary:** Fingerprint trusted definitions/remote identities, require review on material changes, keep server-authored content external, and guard against tool-output prompt injection. Tool results, resources/catalogs, and expanded prompts carry explicit content-derived `EXTERNAL_MCP_DATA` frames that permit factual use while refusing embedded instructions; terminal controls are stripped, schema prose is labeled/bounded, and an end-to-end evaluation proves injected permission text cannot authorize a write. *(Shipped 2026-07-21.)*
- [ ] **P1 — MCP argument-level permissions:** Extend existing server/tool scoped `permissions.rules` to match bounded, normalized argument resources without allowing server-authored annotations to lower risk.
- [x] **P1 — MCP conformance:** Capability-specific in-memory fixture servers cover initialization/identity, negotiated MCP 2025-11-25 revision and subset, tools/resources/prompts, list changes, rich content, progress, elicitation, cancellation, lifecycle, and pinning. `/mcp status` reports each connection's actual negotiated revision; [docs/MCP_PROTOCOL.md](MCP_PROTOCOL.md) declares compatibility revisions, implemented behavior, exclusions, and the test boundary. *(2026-07-20)*
- [x] **P1 — Skills specification:** Robust YAML front matter (plain/quoted/folded/literal scalars, block and inline lists, nested `metadata` map — hand-rolled subset, no new dependency), `allowed-tools` metadata surfaced to the model, bundled `scripts/`/`references/`/`assets/` directories mapped into every `load_skill` result, project-over-global precedence with shadow reporting, per-field validation warnings (name format/length, directory match, description length, missing front matter), size limits, and deterministic name-sorted discovery. *(2026-07-19)*
- [x] **P1 — Skill trust and lifecycle:** `collo skills list|show|new|install|update|remove|enable|disable` with SHA-256 hashes, version/license/source metadata, shadow-conflict reporting, `.disabled` markers, and symlink-refusing size-capped installs. Executable assets carry no inherent grants: project skills stay quarantined until `collo trust`, and bundled scripts execute only through the normal permission engine and sandbox. A dedicated per-skill trust prompt (beyond workspace trust) remains future hardening. *(2026-07-19)*
- [x] **P1 — Hooks:** Eleven lifecycle events (`session_start`, `user_prompt`, `permission_decision`, `tool_start`, `tool_end`, `file_change`, `compaction`, `subagent_start`, `subagent_end`, `stop`, `session_end`) run configured commands (`hooks` config map, validated event names/matchers/timeouts) with a structured JSON payload on stdin, regex matchers on the tool/event subject, default 10-second timeouts with a wait-delay so orphaned grandchildren cannot stall the session, and capped output. `user_prompt` and `tool_start` can gate — exit code 2 or `{"decision":"block","reason":…}` blocks the action with the reason shown to the model — and hooks only ever tighten: they cannot approve what the permission engine denies or bypass the sandbox. Failures and timeouts are bounded, logged warnings, never fatal. Project-configured hooks require `collo trust`. *(2026-07-19)*
- [ ] **P2 — Extension packaging:** Define a versioned plugin/custom-tool package and SDK only after the event, permission, and trust contracts are stable.

### Exit gate

- A conformance fixture exercises MCP tools, resources, prompts, elicitation, progress, rich content, cancellation, reconnect, and change notifications.
- Changing a trusted MCP command, URL, skill executable, or hook invalidates trust and requires review.
- Skills have deterministic precedence and can bundle supporting material without injecting all of it into every prompt.
- Hook or extension failure is bounded, visible, and cannot bypass the sandbox or permission engine.

## Phase 6 — True multi-agent orchestration

**Goal:** Let specialist agents work concurrently without sharing uncontrolled state or confusing the user.

### Deliverables

- [ ] **P0 — Agent definitions:** Configure named primary and subagents with their own instructions, model, reasoning level, tools, permissions, skills, maximum turns, token/cost budget, and visibility. *(Substantially complete 2026-07-21: `agents.<name>` selects role/model, tool and skill allowlists, iteration/token/time budgets, and restrictive permission mode/denials/rules. Permissions only tighten inherited policy and tool allowlists are enforced at execution. Reasoning level, monetary cost budget, and primary-agent profiles remain.)*
- [x] **P0 — Scheduler:** Run independent tasks concurrently with global and per-provider concurrency limits, fair queuing, cancellation, timeout, and maximum delegation depth. *(Completed 2026-07-21: every `delegate` call in a session shares one configurable FIFO admission controller with global and per-provider limits; queue time counts toward the task deadline, any one task can be cancelled, parent/runtime cancellation propagates, and sub-agents cannot recurse.)*
- [x] **P0 — Isolation:** Give write-capable agents independent Git worktrees or explicit scratch workspaces; keep read-only investigation cheap and shared where safe. *(2026-07-18: a task with `write: true` gets its own `git worktree add` checkout, its own tool registry and permission manager rooted there, and its own audit ledger; read-only tasks stay cheap and shared in the parent workspace. Git's short shared administrative setup is serialized per parent so concurrent adds cannot race, while child work remains parallel. Requires the workspace to be a git repository.)*
- [x] **P0 — Parent inbox:** Deliver structured progress, questions, permission requests, failures, and final results to the parent without forcing full child transcripts into parent context. *(Completed 2026-07-21: each child returns bounded structured identity/status, summary, evidence, usage/budget, file/hunk manifest, and worktree details. Live state/current action and named child approvals stay outside parent context in the TUI; no raw child transcript is injected.)*
- [x] **P0 — Conflict handling:** Detect overlapping files/hunks, serialize or reconcile conflicting work, and never silently overwrite user or sibling changes. *(Completed 2026-07-23: validated repository-relative writer scopes preserve concurrency for known-disjoint work and FIFO-serialize overlapping, nested, case-folded, unspecified, or workspace-wide assignments. Actual Git changes are checked against the declaration and violations remain isolated. Integration performs a freshness-bound base/parent/child comparison: clean non-overlapping text edits become a selectable composed preview, while overlapping diff3 conflicts and incompatible entries remain non-selectable. The existing permission, post-approval revalidation, rooted publication, rollback, `/diff`, and `/undo` boundary remains; no commit, push, merge commit, conflict winner, or worktree deletion is automatic.)*
- [ ] **P1 — Plan-to-agent execution:** Assign approved plan nodes to named agents, track dependencies and evidence, and re-plan on failure or changed repository state. *(Partial 2026-07-21: optional `plan_step` associates a delegate/result with an existing step and rejects unknown IDs or unfinished dependencies. Automatic assignment, status propagation, graph execution, repository-change invalidation, and replanning remain.)*
- [ ] **P1 — Result synthesis:** Require evidence and changed-artifact manifests from children; let the parent compare, validate, and selectively integrate results. *(Substantially complete 2026-07-23: bounded evidence/file/hunk manifests are required, same-base sibling overlap is compared, users can selectively integrate guarded text hunks, and opt-in reviewed mode lets the primary inspect exact evidence/hunks and publish a freshness-bound selection through the same guarded path. Mandatory verification/ranking and automatic conflict reconciliation remain.)*
- [x] **P1 — Multi-agent TUI:** Show an agent tree, state, model, budget, current action, pending approval, recent output, and stop/steer controls. *(Completed 2026-07-21: `/agents`, a parent/child Session tree, status badges, and `alt+a` show persisted identity/state/profile/model/action/approval/usage/budget/outcome/change details plus bounded recent output. The explicit action menu inspects, prepares boundary-safe steering, or stops one child; the busy composer also accepts `/agents` inspect/steer/stop without sending a second prompt.)*
- [ ] **P1 — Reproducible orchestration:** Persist delegation and scheduler events so interrupted runs resume without duplicating completed work. *(Partial 2026-07-21: bounded latest child snapshots persist in `delegate.update` events; completed outcomes restore, and nonterminal work becomes inert `interrupted` state so mutations are never replayed. Resuming safe pending work and reconstructing scheduler order remain.)*
- [ ] **P2 — Team patterns:** Ship optional reviewer, researcher, test, security, and documentation agent templates without hard-coding them into the core loop.

### Exit gate

- Multiple agents can investigate in parallel and write in isolated worktrees without data races or accidental overwrites.
- Users can inspect, stop, steer, and approve any active child from the TUI.
- Parent context receives concise structured results while full child transcripts remain available for audit.
- Crash recovery does not re-run completed mutations or lose pending approvals.

## Phase 7 — Best-in-class terminal and automation experience

**Goal:** Make advanced capability discoverable, fast, accessible, and scriptable.

### Deliverables

- [x] **P1 — Command palette:** Add fuzzy slash-command selection, argument completion, searchable models/sessions/agents/skills/MCP servers, and contextual help. *(Completed 2026-07-21: scored fuzzy selection and contextual descriptions cover slash commands plus model, theme, session, skill, MCP, workspace-path, prompt-file, and delegated-agent surfaces. `/agents` searches retained current-session delegates and opens detailed status/outcome panels.)*
- [ ] **P1 — File and artifact input:** Support `@` file/folder mentions, quoted paths, pasted/dragged images, prompt-from-file, and capability-aware attachment previews. *(Mostly complete 2026-07-21: text reference/prompt flows are complete; `/attach` fuzzy-picks or accepts quoted/escaped/file-URL/terminal-dropped workspace PNG/JPEG/GIF/WebP paths, shows pending name/type/size and a status badge, supports list/detach, checks adapter capability, and persists submitted bytes by safe session reference. Raw clipboard image protocols and inline pixel previews remain.)*
- [ ] **P1 — Workspace UI:** Add persistent plan/tasks, file changes, Git state, diagnostics, token/cost/budget, active agents, background processes, and pending approvals without overwhelming the transcript. *(Substantially complete 2026-07-22: the Session tab shows the live plan, changed files, session identity, token/cache usage, a live agent tree/current action/recent output, background processes, asynchronous Git branch/upstream/ahead-behind/dirty state, and provider/sandbox/MCP/trust/persistence health with actionable recovery hints. `/activity` moves the bounded searchable/filterable event timeline and copyable failure IDs into an on-demand full-screen view; child approvals remain labeled floating dialogs. `r` refreshes Git state and status-bar badges cover tasks/agents/procs. An optional context rail (`alt+r`, automatic at 146 columns and unavailable below 116) carries workspace/branch, plan, delegated agents, changed files, and background processes beside the transcript, trimming rather than displacing the composer, and tool calls in the transcript carry outcome and elapsed time for live turns. Automatically surfaced diagnostics and provider pricing/budgets remain.)*
- [x] **P1 — Diff and review ergonomics:** Provide syntax-highlighted side-by-side/unified views, folding, hunk actions, keyboard navigation, and links to external editors. *(Completed 2026-07-21: approval prompts retain colorized previews and selective multi-hunk `write_file` apply/reject; `/diff`/`ctrl+d` provides responsive unified/side-by-side layouts, unchanged-region folding, old/new line numbers, counts, and file/hunk/line/page navigation. `e` safely suspends the TUI and opens the current workspace-contained file at the selected hunk through a direct configured argv, then refreshes the diff on return. Line-level pending-write selection remains a deeper optional refinement rather than part of this deliverable.)*
- [ ] **P1 — Terminal fundamentals:** Raw scrollback/copy mode, robust resize behavior, no-color/plain mode, alternate-screen toggle, shell completion, configurable keybindings/themes, and screen-reader-conscious status output. *(Mostly complete 2026-07-22: in addition to selectable themes and `plain`/`NO_COLOR`, `ctrl+y`/`/transcript` provides raw transcript navigation/search and bounded OSC 52 copy; manual chat scroll survives streaming/resize until `end` resumes follow; complete session transcripts/tools restore without replay; boundary-aware up/down history and per-session drafts improve composer continuity; 80x24/narrow layouts and the activity view are bounded by cross-platform plain goldens; `options.alternate_screen` and CLI overrides control native scrollback; named global keys are validated/collision-checked and reflected in Help; animations remain default while opt-in `options.reduced_motion` retains textual working state and every control; and Bash/Zsh/Fish/PowerShell completion is generated by the binary. Extended 2026-07-25: the composer grows with the draft, extends instead of sending an unfinished one, accepts `alt+enter`/`ctrl+j` everywhere plus Kitty/`modifyOtherKeys` `shift+enter`/`ctrl+enter`, and hands the draft to `$EDITOR` under `alt+e`; `options.mouse` requests wheel scrolling and tab clicks while `alt+m` releases and reclaims the mouse mid-session so terminal drag-selection stays available without a restart; modals dim by dropping color and clear a gutter; approval diffs are tinted by the highlighter itself; and the context gauge gained eighth-block resolution. Remaining: a deeper native screen-reader/accessibility audit and broader terminal-emulator coverage.)*
- [x] **P1 — Machine-readable execution:** Add stable JSONL events, structured final output via JSON Schema, meaningful exit codes, stdout/stderr separation, ephemeral mode, and resume by session ID. *(Completed 2026-07-21: schema-v1 events and the exactly-once terminal `run.result` have an embedded/published draft 2020-12 schema printed by `collo schema events`; structured failure/provider, partial-completion, and refusal metadata preserve the existing ok/error/cancelled statuses; exit codes are stable; stdout/stderr are separated; durable resume/continue and session-free `collo run --ephemeral` are documented and tested.)*
- [x] **P1 — Notifications:** Cross-platform notification hooks for permission, question, failure, and completion events. *(2026-07-18: pending approvals, agent questions, failed turns, and long-turn completion ring the terminal bell and post an OSC 9 desktop notification through the hosting terminal (tmux passthrough handled; unsupported terminals ignore it); `options.notifications` selects on/bell/off. Direct native notifier integration (notify-send, osascript) is deliberately not used — the terminal owns focus-awareness and user preferences.)*
- [ ] **P2 — Local service API:** Expose the same event/session/permission contracts through an authenticated local stdio/socket/WebSocket service so IDEs and other frontends do not reimplement the agent. *(Related 2026-07-18: `collo --web` serves the full TUI over an authenticated loopback WebSocket-to-PTY bridge — a terminal transport, not the structured service API, which remains open.)*
- [ ] **P2 — Web and browser tools:** Add explicitly enabled search/fetch/browser capabilities with domain policy, citation metadata, download quarantine, and prompt-injection defenses.
- [ ] **P2 — Remote execution:** Allow an explicit remote/CI worker while keeping identity, workspace, permission, data-residency, and audit boundaries visible.
- [ ] **P3 — Collaboration surfaces:** Add GitHub/GitLab review workflows and optional team session sharing only after local session security and durability are mature.

### Exit gate

- Core workflows are usable at 80x24, without color or mouse input. *(Automated 80x24 and 40x12 plain rendering plus event-driven chat/question/diff golden screens run across the macOS/Linux/Windows CI matrix; native terminal-emulator and colored/accessibility goldens remain.)*
- Headless consumers can reconstruct a run from versioned JSONL and reliably distinguish success, refusal, cancellation, provider failure, and partial completion. *(Automated contract complete 2026-07-21; uncatchable process/stdout failure limits are documented.)*
- TUI and headless modes consume the same runtime events and produce the same permission decisions.
- Accessibility, resize, keyboard, long-output, and Windows-terminal golden tests run in CI. *(Partial 2026-07-21: deterministic normalized golden screens now run on Windows/macOS/Linux alongside resize, keyboard, modal, and long-output unit coverage. Native Windows Terminal/emulator and deeper screen-reader/color passes remain.)*

## Phase 8 — Quality, evaluation, and 1.0 readiness

**Goal:** Prove that Collomia is dependable, secure, maintainable, and better over time.

### Deliverables

- [x] **P0 — Agent evaluation suite:** Maintain representative repository tasks for search, bug fix, refactor, test creation, review, long context, recovery, permission refusal, and multi-agent integration. *(Completed 2026-07-23: the offline deterministic suite drives the real agent/permission/tool/session pipeline through grounded repository inspection, bug fix and behavior-preserving refactor followed by actual fixture tests, generated boundary tests that are executed, exact-file/line read-only review, headless permission refusal, external MCP injection refusal, interrupted-mutation recovery, plan/decision/failure-aware compaction, non-replaying conversation rewind, governed concurrent read/write delegation, and guarded selective worktree integration under Windows-style Git settings followed by real verification.)*
- [ ] **P0 — Security program:** Keep the threat model current; run sandbox/adversarial tests in CI; add dependency scanning, fuzzing, coordinated disclosure guidance, and independent security review before 1.0. *(Partial 2026-07-22: the cross-platform sandbox/adversarial corpus includes concurrent symlink swaps, hard links, rooted mutations/undo, injected MCP output, environment/process escape, and native network denial; bounded replay/config/shell/diff fuzz targets and pinned reachable-dependency scanning run in CI; a root private-reporting/coordinated-disclosure policy has shipped. Sustained fuzzing and independent review remain.)*
- [ ] **P0 — Reliability tests:** Add provider fault injection, truncated streams, malformed tool calls, rate limits, MCP disconnects, crashes during mutation, full disks, terminal loss, and cancellation races. *(Partial 2026-07-23: provider fault/retry/truncation/malformed-stream and post-delta cancellation contracts exist; deterministic replay covers success, refusal, corruption, missing/late verdict, and mid-tool cancellation; MCP fixtures cover disconnect/reconnect and failed/superseded catalog refresh; the session store fault-injects short/disk writes, a real subprocess death leaves a recoverable torn tail/dangling tool, and the fail-stop guard blocks subsequent provider/tool boundaries in parent and child agents; immutable attachments/results remove partial files across write/sync/close failures; atomic file tests inject failures and kill a subprocess before/after publication to prove the accepted destination is wholly old or new; provider/TUI/runtime teardown cancellation is covered alongside delegation cancellation while queued, calling a provider, or waiting for approval; tracked background-process shutdown waits for completion; and Git fixtures exercise inherited autocrlf, portable mode semantics, parent drift, and approval races. Real host-level filesystem exhaustion, power-loss durability, native terminal loss, diagnostic/audit-stream fail-stop policy, and longer sustained cancellation campaigns remain.)*
- [ ] **P1 — Performance budgets:** Track startup time, idle memory, TUI render latency, search latency, event-store growth, token overhead, and compaction quality on small and monorepo fixtures. *(Partial 2026-07-23: structurally bounded activity projection plus diagnostic benchmarks cover runtime startup, 10,000-event projection, 2,000-file index query/warm refresh, 2,000-message session restoration, maximum activity rendering, and a 500-block syntax-highlighted transcript. CI smoke-runs them without comparing runner timings. Idle memory, token overhead, measured compaction cost/quality, representative monorepo fixtures, and same-hardware regression thresholds remain.)*
- [x] **P1 — Deterministic replay:** `collo replay [--check] <trace|->` validates completed schema-v1 headless traces, enforces lifecycle/final-verdict consistency, tolerates additive fields, and renders an offline control-safe transcript without creating a runtime or executing anything. Generated traces are already configuration-aware redacted; replay applies common-pattern redaction again, and docs state why this remains defense in depth. Credential-free recorded fixtures plus event-driven normalized TUI goldens exercise success, refusal, cancellation/partial tool output, malformed input, 80x24/narrow chat, floating questions, and side-by-side diff across all CI platforms. *(2026-07-21)*
- [ ] **P1 — Optional telemetry:** Collect only opt-in, documented, minimal operational metrics; support a fully offline mode and local inspection/deletion.
- [x] **P1 — Support tooling:** Produce a redacted support bundle and expose versions, feature flags, active config layers, provider/MCP health, and recent failure IDs. *(`collo support bundle` atomically creates a schema-versioned local-only ZIP with build/platform/capability data, anonymous layer and strict-validation status, provider type/auth counts, aggregate MCP state, Git/sandbox health, explicit default exclusions, optional bounded redacted logs, and up to eight opaque recent failure IDs without copying log content. It does not construct the runtime or contact anything. Completed 2026-07-22.)*
- [ ] **P1 — Release security:** Sign binaries and checksums, publish SBOM and provenance attestations, notarize where applicable, and verify artifacts in installers/updaters. *(Substantially complete for beta 2026-07-22: the tag-gated draft workflow publishes a deterministic CycloneDX SBOM plus GitHub/Sigstore SLSA provenance and SBOM attestations covering every checksummed release subject; installers strictly verify the SHA-256 manifest and documentation provides repository/workflow-bound `gh attestation verify`. Apple signing/notarization, Windows Authenticode, and installer-enforced signature verification remain.)*
- [ ] **P1 — Distribution:** Add supported Homebrew, Scoop/Winget, and selected Linux package flows; test install, update, rollback, and uninstall. *(Partial 2026-07-22: supported macOS/Linux and native Windows user-local installers detect architecture, verify exact checksums and binary identity, publish atomically, preserve an existing binary on failure, support stable/prerelease pinning, and have credential-free regression plus native artifact smoke coverage. Package-manager publication and broader clean-machine update/rollback/uninstall campaigns remain.)*
- [x] **P1 — Documentation:** Publish a security model, provider setup guides including Azure and AWS identity, MCP/skills/hooks authoring, automation schema, troubleshooting, and migration policy. *(Completed 2026-07-23: the exhaustive user guide and focused security, Linux sandbox, automation/schema, MCP protocol, live-provider, testing, installation, beta, release, private vulnerability-reporting, and compatibility/migration guides cover the implemented surface. Release-by-release accuracy remains an ongoing maintenance requirement rather than an unshipped deliverable.)*

### 1.0 exit gate

Collomia should not call itself 1.0 or advertise safe unattended execution until all of these are true:

- The sandbox and permission model pass the cross-platform adversarial suite and an independent review.
- Sessions survive interruption; mutating actions are idempotent or explicitly reconciled; direct edits are reviewable and recoverable.
- Every advertised provider has a maintained capability declaration and passing contract tests.
- Long-context and multi-agent tasks have bounded budgets, cancellation, observability, and regression evaluations.
- MCP, skills, hooks, and repository configuration share a documented trust model.
- Release artifacts are signed, reproducible/provenanced, and install/update paths are tested.
- No known P0 data-loss, sandbox-escape, secret-exposure, or duplicate-mutation defects remain open.

## Recommended delivery slices

The phases are deliberately large enough to describe outcomes. Implement them as thin vertical slices:

1. ~~**Trusted beta slice:** event schema + config layering/validation + repository trust + doctor.~~ *(Done 2026-07-18.)*
2. ~~**Safe command slice:** one platform sandbox end to end + scoped filesystem/network grants + audit ledger, followed immediately by the other platforms.~~ *(macOS and Linux shipped 2026-07-18; the no-install Windows AppContainer/Job Object backend and capability-aware enforcement shipped 2026-07-20. Domain-scoped egress remains a separate follow-up.)*
3. ~~**Resumable session slice:** event store + resume + crash-safe tool states + minimal context accounting.~~ *(Done 2026-07-18.)*
4. ~~**Long-task slice:** compaction + structured plan + atomic patch/diff + checkpoint undo.~~ *(Core shipped 2026-07-18, including hunk-level diff approval for `write_file`; completed context-policy follow-up shipped 2026-07-21 with pinned plan state, exact bounded failure retention, referenced oversized results, and non-destructive conversation rewind.)*
5. ~~**Provider quality slice:** capability registry + normalized streaming + error taxonomy/retry + live contract harness + Azure Entra refresh.~~ *(Done 2026-07-19: capability declarations/preflight and discovery, normalized streaming including Bedrock ConverseStream and Responses SSE, recorded/opt-in live contract suites, complete error/retry/timeout/circuit health, and refreshable DefaultAzureCredential authentication for Azure OpenAI and Foundry.)*
6. ~~**Extension slice:** dynamic MCP lifecycle + resources/prompts + skill trust/validation + hooks + safe catalog refresh/conformance.~~ *(Done 2026-07-20. OAuth/login, experimental tasks, resource subscriptions, and content hardening remain focused follow-up waves.)*
7. **Parallel-agent slice:** two read-only specialists, then isolated write agents/worktrees, then full scheduler UI. *(The governed operator loop is substantially complete as of 2026-07-23: isolation, least-privilege profiles, shared global/provider/write-scope scheduling, token/time budgets, structured evidence/change/scope results, plan-step association, durable state, a full supported-depth parent/child tree, steering/stop controls, machine-observed child verification, manual selective three-way integration, and opt-in primary-reviewed freshness-bound integration. Automatic plan-graph execution/replanning, reasoning/cost controls, combined-parent ranking/verification, safe pending restart, and fuller transcript audit remain.)*
8. **1.0 slice:** headless JSONL/schema + complete cross-platform QA + signed distribution + eval/security gates. *(The beta foundation is substantially complete as of 2026-07-23: embedded automation schema/verdicts, offline replay, opaque failure correlation, a complete representative credential-free evaluation matrix, cross-platform TUI goldens, diagnostic performance baselines, explicit persisted-format compatibility rules, bounded fuzzing, fail-stop short/disk-write handling, subprocess crash recovery around session/atomic-publication boundaries, bounded cancellation stress, support bundles, strict atomic installers, reachable-dependency scanning, deterministic SBOMs, GitHub/Sigstore provenance, and exact-tag cross-platform draft-release gates. Native platform signing/notarization, package managers, native-terminal QA depth, independent review, and sustained security/reliability campaigns remain.)*

## Explicitly deferred until the foundations are ready

These are attractive, but building them before Phases 0–3 would increase risk and rework:

- Cloud-hosted task execution and account synchronization.
- Shared team workspaces and real-time collaboration.
- A public plugin marketplace.
- Autonomous Git commits, pushes, pull requests, deployments, or issue updates by default.
- Persistent semantic memory across unrelated repositories.
- Voice or decorative TUI features that do not improve coding safety or throughput.

## Assessment notes

- The roadmap is based on source inspection, not only README claims. Status annotations dated 2026-07-18 were verified against the code and its tests at the time of marking.
- `go test -race ./...` passes on the annotated revision, including sandbox enforcement, crash-recovery, compaction, retry fault-injection, and TUI interaction tests. This still does not substitute for the provider-conformance, security-audit, or cross-platform golden suites listed in phases 4 and 8.
