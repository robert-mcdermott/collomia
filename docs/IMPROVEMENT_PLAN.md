# Agent improvement plan

Updated: 2026-09-04. Source: [project review](PROJECT_REVIEW_2026-09-04.md).

This is the durable implementation and user-testing plan for the September
review. It supplements the [roadmap](../ROADMAP.md). The existing
[orchestration strategy](ORCHESTRATION_STRATEGY.md) remains authoritative for
Orchestrated Goal, its supported/experimental distinctions, authority
boundaries, and non-goals. These waves do not reopen completed milestones.

## Current handoff — read this first

- **Active wave:** none; W3 — truthful completion and usable inputs — accepted.
- **Status:** W1 and W3 accepted. The user confirmed the remaining manual tests
  passed on 2026-09-04. W2 and later waves remain planned.
- **Next action:** select the next wave with the user. No next wave has started.
- **Test build:** `dist/collo-wave3` (macOS arm64), version `v0.4.2-wave3`,
  base `05f698b` plus the current uncommitted W3 changes. Build details below.
- **User acceptance:** W1 accepted on 2026-09-04. The user reported it “worked
  great” with GLM-5.3-flash from Ollama and confirmed visible reasoning.
  W3 accepted on 2026-09-04 after successful pagination and follow-up manual
  tests. The optional low-output-token check was not separately confirmed.
- **Deferred W2 work:** W1 only displays readable reasoning events already
  emitted by an adapter. It does not enable thinking at the API, add synchronous
  reasoning extraction, or restore thinking in reopened chat transcripts.
  Those are W2 work. A silent summary area does not prove absent computation.

## How to use this plan across sessions

1. Read the handoff, active wave, and latest validation/feedback below before
   editing. Check the working tree; preserve unrelated user changes.
2. Work only on the active wave. Each wave moves through **planned →
   implementing → ready for user testing → accepted**. Record failures as
   **needs revision** and repair that wave before advancing.
3. Automated tests permit “ready for user testing,” not “accepted.” The user
   decides when a wave is accepted and may change its priority or scope.
   Do not infer acceptance from elapsed time, silence, or a passing test suite.
4. Deliver a runnable build, exact manual checks, automated evidence, and known
   limitations. Stop before the next wave until the user accepts or explicitly
   directs continuation. Routine fixes within the active wave need no new gate.
5. Update this file's checklist and handoff in the same change as the code.
   Update the user guide, changelog, and generated capability source/output
   when behavior changes. Update schemas only when their contract changes.
   Orchestration changes additionally follow the strategy's documentation and
   exit-gate requirements.
6. Record acceptance with date, user feedback, and revision/commit when known.
   Keep pending ideas below visible; don't silently expand an active wave.

## Wave sequence and acceptance gates

Wave numbers are stable identifiers, not mandatory execution order. On
2026-09-04 the user chose W3 ahead of W2. W1 and W3 are accepted;
W2 and the later waves remain pending. Revisit the next priority at each gate.

| Wave | Deliverable | Status | User gate |
| --- | --- | --- | --- |
| W1 | Separate, bounded live thinking summaries in the TUI | Accepted | User confirmed successful reasoning display with GLM-5.3-flash from Ollama |
| W2 | Reasoning configuration, provider-state continuity, and summary replay | Planned | Reasoning/tool conversations work on the user's actual providers, including reopen |
| W3 | Correct completion outcomes, exact failure recovery, large-file pagination | Accepted | User confirmed pagination and the follow-up manual tests passed |
| W4 | Final deliverable identity and observed-effect checks | Planned | Script-created/modified artifacts cannot retain stale acceptance |
| W5 | Real-model evaluation baseline | Planned | Representative tasks and quality/cost metrics reflect the user's work |
| W6 | One complete Work workflow: data → workbook → cited memo | Planned | Fresh setup can create, inspect, revise, and deliver useful artifacts |
| W7 | Durable task context, retrievable evidence, and restart checkpoints | Planned | Long work survives compaction/restart without losing requirements |
| W8 | Lazy tool discovery and measured independent-read concurrency | Planned | Connected tools are discoverable and parallel reads improve latency |
| W9 | Scoped autonomy mandates and durable delayed continuation | Planned; design gate required | Authorized work resumes predictably with clear wait/cancel controls |

### W1 — thinking visibility

Small UI-only slice. No new permissions, provider settings, or persisted schema.

- [x] Render `reasoning.delta` in its own “Thinking summary” block.
- [x] Merge adjacent chunks while preserving thinking/tool/answer order.
- [x] Show at most six wrapped preview lines; allow expansion through the
  existing `toggle_tool_output` binding (default `ctrl+o`).
- [x] Cap retained display text at 64 KiB per contiguous summary, preserve
  UTF-8 boundaries, and label truncation in both chat and transcript copy.
- [x] Keep summaries searchable/copyable in the current transcript; collapse
  them when output moves on or the turn ends.
- [x] Cover absent summaries, multiple chunks, tool transitions, turn end,
  truncation, narrow screens, and custom bindings with regressions.
- [x] Review live/finished terminal snapshots; run TUI tests and CLI/doc checks.
- [x] Document remaining provider/reopen limitations and prepare a test build.
- [x] **User accepts W1:** positive test feedback on 2026-09-04 with
  GLM-5.3-flash from Ollama; reasoning was visible. Individual manual checks
  beyond that observation were not separately reported.

Manual checks (using a provider that emits readable reasoning):

1. Start the test build in an ordinary workspace. Ask it to inspect a file and
   explain a comparison requiring some analysis. Observe “THINKING SUMMARY”
   before the answer if the provider emits it.
2. Check that the live preview remains readable and collapses as tools/answer
   arrive. Press `ctrl+o` twice to expand and collapse; tool output uses the same
   control. Repeat with a narrow terminal.
3. Open `/transcript` (or `ctrl+y`), search for summary text, and check the
   separate summary/answer labels. Copying uses the existing terminal support.
4. Try an ordinary short question and cancel one longer turn. There should be
   no invented thinking text, broken answer, or disappearing received summary
   in the current transcript.
5. If no provider shows a summary, record provider type/model and whether
   headless output contains `reasoning.delta`. This is input to W2, not grounds
   to enable provider settings silently. The deterministic W1 tests exercise
   rendering without a model account or paid request.

### W2 — provider reasoning and continuity

- [ ] Separate enablement, effort, readable summary availability, and opaque
  continuation state in capability declarations and diagnostics.
- [ ] Define ordered provider content/state and persistence migration rules;
  keep signed/opaque state provider-bound and out of rendered text.
- [ ] Add an explicit OpenAI Responses route and supported summary opt-in;
  implement model-appropriate Anthropic thinking/display configuration.
- [ ] Preserve reasoning items/signatures through tool results, fallbacks,
  retries, compaction boundaries, and close/reopen. Define provider-switch
  behavior explicitly. Keep old sessions readable.
- [ ] Extract supported readable reasoning in synchronous fallback and restore
  summaries in the TUI from durable, ordered data.
- [ ] Verify current official provider contracts before implementation. Test
  request/response fixtures plus user-selected live provider/model checks;
  report effective fallback settings rather than requested effort alone.
- [ ] **User accepts W2.** If provider coverage is too large for one testable
  increment, split into W2a/W2b with separate user gates before implementation.

### W3 — truthful completion and usable inputs

Chosen ahead of W2 by the user on 2026-09-04. Scope is the three confirmed
reliability defects and their directly related boundary cases. Rejected provider
responses stop explicitly; automatic continuation is deliberately excluded from
this slice to avoid repeating effects or resetting budgets. The completion
interface improvement is incremental: automatic exact recovery and clearer
notices/prompt guidance, preserving the existing typed alternative/skip/block
dispositions. No new completion tool or plan schema is introduced.

- [x] Normalize stop reasons; truncated, refused, and failed output must not
  silently become successful completion. Bound any continuation without
  duplicating tool effects or resetting budgets.
- [x] Match recovery to the failed operation and subject. An unrelated success
  from the same tool must not clear it; preserve explicit alternative recovery.
- [x] Simplify the completion interface incrementally: expose exact gaps and
  evidence while keeping plans focused on work, not bookkeeping.
- [x] Fix `read_file` pagination beyond the first MiB and return honest
  continuation/truncation information.
- [x] Convert the review probes to permanent regressions; check Developer and
  Work, failed then recovered work, harmless exploration, and budget stops.
- [x] **User accepts W3:** on 2026-09-04, after the pagination transcript and
  follow-up manual instructions, the user reported “the tests passed.”

User testing on 2026-09-04 confirmed that `read_file` with offset `100001` and
limit `1` returned `100001 W3_TAIL_MARKER` followed by `[read_file: EOF]`; the
agent correctly reported the exact line and EOF. User feedback: “it works.”
The user subsequently reported “the tests passed” after receiving concrete
checks for normal operation in both modes, page continuation, required missing
inputs despite unrelated success, exact retry within one turn, and optional
missing inputs. W3 is accepted. No individual follow-up transcripts or model
identifiers were supplied; the optional low-output-token test was not separately
confirmed and remains covered by automated fixtures.

Manual checks for the W3 test build:

1. Repeat a normal GLM/Ollama task in Work and Developer. Thinking and ordinary
   answers should work as in W1; no reasoning support is required.
2. From the repository workspace, ask: “Use read_file with offset 100001 and
   limit 1 on dist/wave3-fixtures/large.txt. Report the exact line.” The fixture
   is 1,100,015 bytes; the result should be `W3_TAIL_MARKER` and EOF, not
   `(no lines)`. Try a shorter page near the beginning and inspect its next-offset
   hint in the tool output.
3. Ask for two required inputs with one deliberately absent, then provide the
   missing file and continue. Reading an unrelated file or running a different
   successful command must not silently satisfy the missing input. A successful
   retry of the same read should clear that failure without a recovery-only
   plan update within the turn. Existing completion obligations are per-turn;
   durable cross-turn obligations remain W7 scope.
4. Try an optional exploratory lookup that can be skipped when absent. The
   model should distinguish an unnecessary attempt from a genuinely required
   missing input; the existing structured skipped disposition remains usable.
5. Optional: with a temporary provider configuration using a small supported
   `max_tokens`, request a longer answer. Expect retained partial output and a
   clear stop, with no successful `done` result or automatic continuation.
   Inspect before deliberately continuing. Do not change the usual provider
   configuration just to exercise this: the offline termination fixtures cover
   truncation, refusal, missing stream endings, and partial tool JSON.

The prepared binary uses ordinary Collomia configuration/sessions; it is separate
from the installed `collo`. Launch from the repository root for the fixture:

```sh
/Users/rmcdermo/mycode/collomia/dist/collo-wave3 --mode work
```

Rebuild the binary from the repository root if needed:

```sh
go build -o dist/collo-wave3 -ldflags '-X github.com/robert-mcdermott/collomia/internal/version.Version=v0.4.2-wave3 -X github.com/robert-mcdermott/collomia/internal/version.Commit=05f698b-wave3-dirty' ./cmd/collo
```

Recreate the ignored pagination fixture if needed:

```sh
mkdir -p dist/wave3-fixtures
awk 'BEGIN { for (i=1; i<=100000; i++) print "1234567890"; print "W3_TAIL_MARKER" }' > dist/wave3-fixtures/large.txt
```

### W4 — effects and deliverable acceptance

- [ ] Introduce observed effects independently of permission risk, including
  unknown command effects and stable operation/subject identity.
- [ ] Recheck declared deliverable digests at completion, including shell writes;
  invalidate receipts when the final bytes differ.
- [ ] Separate requested deliverables from scratch/helper files in a lightweight
  task brief; keep direct Q&A free of synthetic plans or artifact requirements.
- [ ] Specify failure/uncertainty behavior for external receipts and read-back;
  preserve existing authorization boundaries and never blindly replay writes.
- [ ] Test script mutation after validation, unchanged reads, partial failures,
  unknown effects, missing outputs, and artifact-specific acceptance notices.
- [ ] **User accepts W4.**

### W5 — measured agent quality

- [ ] Select an initial representative task set with the user; expand toward
  30–50 tasks across coding, Work, recovery, and long-task behavior.
- [ ] Add an opt-in real-model runner with explicit budgets and local traces;
  keep deterministic runtime tests as the offline gate.
- [ ] Define independent checks/rubrics and repeated trials. Record accepted
  outcomes, false done/blocked, unnecessary questions, interventions, repeated
  work, latency, and cost per accepted task.
- [ ] Establish a versioned baseline before changing the Work toolkit. Compare
  harness changes with the same model/budgets before comparing providers.
- [ ] **User accepts the task set, runner, and baseline interpretation.**

### W6 — a useful Work vertical slice

- [ ] Ship a maintained dependency/setup path and input extraction for one
  concrete workflow using CSV/XLSX inputs and a DOCX/PDF memo deliverable.
- [ ] Add workbook/formula/reconciliation checks, document generation,
  rendering, and model-accessible image inspection using tested libraries.
- [ ] Retain cited source records and make validation receipts distinguish
  structure, calculations, content/source checks, and visual inspection.
- [ ] Run fresh-setup and edit-existing-artifact tasks through creation,
  inspection, revision, and handoff; compare against W5 baseline.
- [ ] **User accepts W6** before expanding to presentations, additional formats,
  optional browser support, or alternative search backends.

### W7 — durable context and recovery

- [ ] Maintain a bounded task record of objectives, corrections, constraints,
  decisions, sources, artifacts, accepted evidence, gaps, and next action.
- [ ] Add model-accessible search/read of earlier session evidence, with source
  references that survive repeated compaction.
- [ ] Persist Standard completion obligations and bounded recoverable workspace
  checkpoints across restart, including non-Git Work folders.
- [ ] Test repeated compaction, cancellation/restart, missing evidence, external
  edits, and uncertain mutations; don't replay ambiguous external effects.
- [ ] **User accepts W7.** Workspace memory is optional future scope, with
  provenance/edit/delete controls; unrelated-project semantic memory remains
  deferred under the existing contract.

### W8 — discovery and safe concurrency

- [ ] Add catalog search/lazy schemas; keep discovery separate from authorization.
- [ ] Add stable invocation IDs across event start/output/result and bounded
  result retrieval before changing scheduling.
- [ ] Run independent, explicitly trusted read-only operations concurrently
  within limits; preserve mutation ordering and deterministic result association.
- [ ] Enable useful read-only connected-source delegation through local policy,
  never by treating remote annotations as permission grants.
- [ ] Compare accepted outcomes, context overhead, and latency against serial
  execution; include cancellation and partial-failure tests.
- [ ] **User accepts W8.** This does not make graph execution the default or
  authorize automatic integration of writer candidates.

### W9 — scoped autonomy and delayed work

- [ ] First agree a new product/authority contract for visible, revocable task
  mandates: scope, destinations, tools, budget, duration, and stopping conditions.
- [ ] Evaluate concise execution guidance encouraging completion, reversible
  assumptions, useful progress, and ordinary error recovery using W5 metrics.
- [ ] Under the agreed contract, add durable queue/wakeups, leases,
  deduplication, cancellation, waiting-for-input, and restart reconciliation.
- [ ] Test duplicate wakeups, expired mandates, revoked access, interruptions,
  and response loss after an external action. Preserve publication decisions.
- [ ] **User accepts W9.** Broader unattended/graph capabilities require their
  own strategy milestone and evidence; this plan grants no new runtime authority.

## Maintenance carried through the waves

- Split large modules along the boundaries being changed; retain one governed
  tool execution path and avoid a wholesale rewrite.
- Keep this current plan concise and move completed detailed evidence to a
  linked history when it grows. Separately simplify the old roadmap/strategy
  into current contracts plus historical decisions without changing authority.
- Revisit instruction precedence and nested instruction provenance as a
  separately tested slice: explicit user constraints over project defaults,
  scoped trusted guidance, and ordinary retrieved text as data.
- Carry user feedback from each gate into later priorities. More orchestration,
  browser integrations, extra artifact formats, and persistent semantic memory
  are candidates, not automatic additions to the current wave.

## Validation and feedback log

| Date | Wave | Evidence or feedback | Decision |
| --- | --- | --- | --- |
| 2026-09-04 | Planning | User requested tracked implementation waves with personal testing between major items. Review contains five reproduced gaps and passing baseline suites. | W1 active; W2–W9 pending. |
| 2026-09-04 | W1 | Seven offline reasoning tests (including live/finished golden snapshots) passed. TUI and CLI suites passed with race detection; targeted vet passed. Review links converted to the reviewed commit's permalinks after the documentation checker caught app-local paths. | Ready for user testing; acceptance pending. |
| 2026-09-04 | W1 user testing | User: “worked great with GLM-5.3-flash from Ollama,” with visible reasoning. Asked about models without reasoning; W1 requires no reasoning events and its absent-summary regression covers ordinary answers. No provider request settings were changed. | W1 accepted; W2 next, not started. |
| 2026-09-04 | W3 priority | User explicitly selected W3 ahead of W2 after discussing the waves' dependencies. | W3 active; W2 deferred. |
| 2026-09-04 | W3 implementation | Terminal-state, refusal, partial-tool-JSON, compaction, cumulative-budget, exact-retry/alternative, empty-refusal continuation, and large-file regressions passed. Full offline suite: 45 tested packages passed, including evaluations. Changed-package race checks and vet passed; final empty-response guard received an additional agent race run. | Ready for user testing; W3 acceptance pending. |
| 2026-09-04 | W3 user testing | User reported “it works” and supplied a live transcript: reading the large fixture at offset 100001, limit 1 returned W3_TAIL_MARKER and EOF, both correctly explained by the agent. Provider/model was not specified for this check. | Large-file pagination check passed; remaining completion/recovery checks and overall acceptance not separately reported. Next wave remains pending. |
| 2026-09-04 | W3 acceptance | After receiving the remaining manual checks and concrete prompts, the user reported “the tests passed.” Checks covered normal operation in both modes, page continuation, required missing input despite unrelated success, exact retry within a turn, and optional missing input. The optional low-output-token check was not separately confirmed. | W3 accepted on the existing test build; W2 and later waves remain planned. No next wave started. |

Record the exact test command and result, test-build path, material limitations,
and user acceptance or requested revisions here at each handoff.

### W3 automated evidence and test build

- `go test -count=1 ./...`: passed, 45 tested packages. The offline evaluation
  suite passed in 229.967s while other validation ran concurrently; this timing
  is test-run evidence, not a performance comparison.
- `go test -race -count=1 ./internal/provider ./internal/agent ./internal/tools ./cmd/collo`:
  passed. After the final empty-refusal history guard, the full agent package
  was rerun with `go test -race -count=1 ./internal/agent` and passed (56.342s).
- `go vet ./...`: passed; `go vet ./internal/agent` reran after that final guard
  and passed. `git diff --check` and formatting checks passed.
- Provider fixtures cover all four adapter families, including truncated tool
  arguments and Responses refusal/incomplete state. Two older assertions were
  corrected to supply exact retry identity and to reject tool calls from an
  incomplete response while retaining its diagnostics/usage.
- Capability Markdown regenerated and compared with the built binary; prompt
  golden updated deliberately. No persisted event/session/plan or configuration
  schema changes. Existing graph authority and graduation status are unchanged.
- Build smoke check: `collo v0.4.2-wave3 (05f698b-wave3-dirty, unknown)`.
  Binary: `/Users/rmcdermo/mycode/collomia/dist/collo-wave3`.
  SHA-256: `bb79ef33b178a7448982393063b0774db222af672e559fed90ece605c5d9773e`.
- Pagination fixture: `dist/wave3-fixtures/large.txt`, 100,001 lines and
  1,100,015 bytes; final line `W3_TAIL_MARKER`.
- Validation ran on macOS arm64 with Go 1.26.6 and a temporary build cache.
  HTTP/platform fixtures used approved execution outside the sandbox. No paid
  or live model calls, native Windows/Linux qualification, or release/version
  bump was performed by the implementing agent for W3. User live testing and
  acceptance are recorded above and in the feedback log.

### W1 automated evidence and test build

- `go test -count=1 ./internal/tui -run '^TestReasoning'`: passed; two new golden
  snapshots generated deliberately and visually inspected as terminal text.
- `go test -race -count=1 ./internal/tui ./cmd/collo`: passed; TUI 4.269s,
  CLI 3.535s. Existing terminal snapshots also passed unchanged.
- `go vet ./internal/tui ./cmd/collo`: passed.
- `git diff --check`: passed.
- Capability Markdown regenerated from its source; only the new live-thinking
  row changed. No event, provider, configuration, or session schema changed.
- Tests ran on macOS arm64 with Go 1.26.6 and an isolated temporary Go build
  cache. CLI fixture tests used approved execution outside the sandbox to allow
  loopback listeners. No live model calls, full-repository qualification, or
  native Linux/Windows qualification was performed for this UI-only wave.

The local test binary is at
`/Users/rmcdermo/mycode/collomia/dist/collo-wave1`. Launch that path from the
workspace you want to test, optionally with `--mode work`. It uses your ordinary
Collomia configuration and sessions. The build is separate from the installed
`collo`; it is not a release or an automatic installation.

```sh
/Users/rmcdermo/mycode/collomia/dist/collo-wave1 --mode work
```

Rebuild from the repository root if the ignored `dist` directory is removed:

```sh
go build -o dist/collo-wave1 -ldflags '-X github.com/robert-mcdermott/collomia/internal/version.Version=v0.4.1-wave1 -X github.com/robert-mcdermott/collomia/internal/version.Commit=f523696-wave1-dirty' ./cmd/collo
```

SHA-256 of the prepared binary:
`9a0ac6a6903cf80b1d02e50ae978f4ac5207865d4558b237f7374838a6c787bb`.
