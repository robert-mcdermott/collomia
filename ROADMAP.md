# Collomia Roadmap

**Status updated:** 2026-07-23

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
  sandbox backends with capability-aware fail-closed behavior;
- scoped permissions, conservative shell analysis, catastrophic-command
  denials, secret redaction, and an audit ledger;
- durable resumable sessions, crash recovery, compaction, bounded retained
  artifacts, rewind/fork, and fail-stop persistence handling;
- atomic patching, tracked diffs, hunk review, undo, Git inspection, planning,
  verification, LSP diagnostics, repository indexing, PTY commands, and
  background processes;
- normalized provider capabilities, streaming, retries, health, contracts,
  Azure Entra refresh, Bedrock SigV4/bearer authentication, and image input;
- MCP lifecycle/resources/prompts/progress/elicitation, safe live catalogs,
  external-data framing, skills, and hooks;
- concurrent governed delegated agents with isolated Git worktrees, durable
  status, steering/cancellation, manual selective integration, and opt-in
  primary-reviewed integration;
- a responsive themed TUI, transcript/activity/diff views, a loopback browser
  terminal, shell completion, notifications, and stable headless JSONL;
- deterministic replay/evaluations, cross-platform golden screens, bounded
  fuzzing, support bundles, verified installers, SBOMs, provenance, and gated
  draft releases.

Collomia is suitable for beta use with the documented limits. It should not
claim 1.0 or fully safe unattended execution until the remaining P0 security
and reliability gates are complete.

## Active wave — verified delegated results

**Goal:** Make primary-agent integration decisions rely on machine-observed,
freshness-bound evidence rather than child-authored claims.

- [x] Run one repository-detected verification command at a time in the
  retained child worktree through the ordinary `run_command` permission,
  hook, sandbox, network, timeout, cancellation, and output policies.
- [x] Bind every result to the exact child branch/base/file state; retain
  passed, failed, cancelled, blocked, and stale outcomes durably without
  executing anything during restore.
- [x] Expose primary-only `verify_delegate_changes` and
  `compare_delegate_changes` tools when reviewed integration is enabled.
- [x] Add `/agents verify <id>` and `/agents compare <id> <id> [id…]`, with
  child-verification state in agent detail and Session views.
- [x] Keep verification independent from publication: a pass grants no
  permission, never auto-applies changes, and does not claim the combined
  parent workspace passed.
- [x] Cover success, failure, cancellation, permission identity, child drift,
  candidate comparison, durable restoration, and cross-platform Git behavior.
- [x] Update configuration, user, security, automation, testing, capability,
  and roadmap documentation.

## Remaining work by phase

### Phase 1 — Safety boundary

- [ ] **P0 — Complete separate capability controls:** executable allowlisting
  and a clear per-capability grant UI remain after independent filesystem,
  environment, network, and process controls.
- [ ] **P0 — Domain/endpoint network policy:** add explicit endpoint-scoped
  grants with understandable DNS/proxy behavior and Windows loopback
  ergonomics. Preserve the current compatibility-first default until the
  install-time policy is deliberately changed.
- [ ] **P0 — Independent review:** sustain the adversarial suite and obtain an
  independent security assessment before 1.0.

### Phase 2 — Sessions and context

- [ ] **P1 — Coupled checkpoints:** conversation rewind and file undo exist;
  add an explicit conversation-plus-workspace checkpoint while continuing to
  state that shell and external side effects cannot be reversed automatically.
- [ ] **P2 — Nested instructions:** evaluate directory-scoped instruction
  inheritance after precedence and trust UX are designed.

### Phase 3 — Coding loop

- [ ] **P1 — Complete LSP operations:** definitions, references, formatting,
  and safe code actions. Diagnostics and lexical symbol search already ship.
- [ ] **P1 — Optional deeper review:** line-level pending-write selection and
  broader selective application for multi-file patches.
- [ ] **P1 — Windows ConPTY:** add native PTY execution without weakening the
  existing process-tree cancellation contract.

### Phase 4 — Provider platform

- [ ] **P1 — Secure credential lifecycle:** operating-system keychain storage
  where practical, with login/logout/status flows and no project-file secrets.
- [ ] **P1 — Provider discovery refinements:** Azure deployment/project
  discovery and routing, tested sovereign presets, and clearer resolved AWS
  identity/model-access diagnostics.
- [ ] **P1 — Modern API features:** general OpenAI/Azure Responses routing,
  structured output, provider caching, richer thinking/content blocks, and
  additional media types.
- [ ] **P1 — Explicit routing/fallback:** ordered capability/health/cost/local
  choices that never silently cross privacy or residency boundaries.
- [ ] **P1 — Usage and budgets:** normalized cost estimates and enforceable
  turn/session/agent monetary budgets with provider caveats.
- [ ] **P2 — Setup wizard:** discover local runtimes, validate endpoints and
  credentials, test deployments, and write a minimal user provider profile.

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

- [ ] **P0 — Finish agent definitions:** reasoning controls, monetary budgets,
  visibility, and named primary profiles.
- [ ] **P0 — Conservative conflict handling:** serialize known overlapping
  assignments and offer explicit three-way reconciliation without silently
  overwriting parent or sibling work.
- [ ] **P1 — Plan graph execution:** assign dependency-ready nodes, propagate
  verified evidence, invalidate stale repository assumptions, and re-plan on
  failure. Keep this opt-in until cancellation and review behavior are proven.
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
- [ ] **P1 — Workspace UI refinements:** automatically surfaced diagnostics
  plus provider price/budget visibility without crowding the transcript.
- [ ] **P1 — Accessibility validation:** native screen-reader, colored theme,
  resize, and broader terminal-emulator coverage.
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
- [ ] **P0 — Reliability campaigns:** host-level filesystem exhaustion,
  power-loss durability, native terminal loss, remaining diagnostic/audit
  fail-stop policy, and longer cancellation stress.
- [ ] **P1 — Performance budgets:** idle memory, token overhead, compaction
  quality, monorepo fixtures, and same-hardware regression thresholds.
- [ ] **P1 — Optional telemetry decision:** only opt-in, minimal, documented,
  locally inspectable/deletable, and fully disabled by offline mode.
- [ ] **P1 — Native release signing:** Apple signing/notarization, Windows
  Authenticode, and installer-enforced signature verification.
- [ ] **P1 — Package managers:** Homebrew, Scoop/Winget, selected Linux flows,
  and clean-machine install/update/rollback/uninstall testing.

## Recommended next sequence

1. Gather real beta feedback on the completed verified delegated-result wave.
2. Add conservative overlap-aware scheduling and explicit conflict
   reconciliation.
3. Add opt-in plan-graph execution using verified results and stale-state
   invalidation.
4. Return to the P0 endpoint-scoped network-policy design before broadening
   unattended autonomy.
5. Continue Phase 8 security/reliability campaigns in parallel with every
   feature wave.

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
  by default.
- Persistent semantic memory across unrelated repositories.
- Decorative features that do not improve coding safety, accessibility, or
  throughput.
