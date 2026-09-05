# Agent improvement plan

Updated: 2026-09-04. Source: [project review](PROJECT_REVIEW_2026-09-04.md).

This is the durable implementation and user-testing plan for the September
review. It supplements the [roadmap](../ROADMAP.md). The existing
[orchestration strategy](ORCHESTRATION_STRATEGY.md) remains authoritative for
Orchestrated Goal, its supported/experimental distinctions, authority
boundaries, and non-goals. These waves do not reopen completed milestones.

## Current handoff — read this first

- **Active wave:** W5 — measured agent quality.
- **Status:** ready for user testing. W1, W3, and W4 are accepted; W4 was committed as
  `ab9d5ca`. The user selected a balanced coding/Work task set. W2 and W6–W9
  remain planned.
- **Next action:** obtain the user's W5 acceptance after reviewing the completed
  baseline below. `dist/quality-baseline` passed 24/24 machine checks across
  12 tasks and two trials; all traces validate. Prose review identified issues
  that machine checks missed. No further baseline run is required for this gate.
  Stop at the W5 user gate before W7 or W6.
- **Test build:** `dist/collo-wave5`, version `v0.4.2-wave5.1`, base commit
  `ab9d5ca-wave5.1-dirty`. The installed binary and `VERSION` are unchanged.
- **User acceptance:** W1 accepted on 2026-09-04. The user reported it “worked
  great” with GLM-5.3-flash from Ollama and confirmed visible reasoning.
  W3 accepted on 2026-09-04 after successful pagination and follow-up manual
  tests. The optional low-output-token check was not separately confirmed.
  W4 accepted on 2026-09-04 after all manual tests passed.
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
2026-09-04 the user chose W3 ahead of W2. W1, W3, and W4 are accepted;
W5 is active; W2 and W6–W9 remain planned. Revisit the next priority at each gate.
After accepting W3, the user explicitly selected W4 on 2026-09-04.
After W4, the user approved W5 next, with W7 then W6 proposed after its gate.

| Wave | Deliverable | Status | User gate |
| --- | --- | --- | --- |
| W1 | Separate, bounded live thinking summaries in the TUI | Accepted | User confirmed successful reasoning display with GLM-5.3-flash from Ollama |
| W2 | Reasoning configuration, provider-state continuity, and summary replay | Planned | Reasoning/tool conversations work on the user's actual providers, including reopen |
| W3 | Correct completion outcomes, exact failure recovery, large-file pagination | Accepted | User confirmed pagination and the follow-up manual tests passed |
| W4 | Final deliverable identity and observed-effect checks | Accepted | User confirmed all manual tests passed and explicitly marked the wave done |
| W5 | Real-model evaluation baseline | Ready for user testing | Representative tasks and quality/cost metrics reflect the user's work |
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

- [x] Introduce observed effects independently of permission risk, including
  unknown command effects and stable operation/subject identity.
- [x] Recheck declared deliverable digests at completion, including shell writes;
  invalidate receipts when the final bytes differ.
- [x] Separate requested deliverables from scratch/helper files in a lightweight
  task brief; keep direct Q&A free of synthetic plans or artifact requirements.
- [x] Specify failure/uncertainty behavior for external receipts and read-back;
  preserve existing authorization boundaries and never blindly replay writes.
- [x] Test script mutation after validation, unchanged reads, partial failures,
  unknown effects, missing outputs, and artifact-specific acceptance notices.
- [x] **User accepts W4:** on 2026-09-04, the user confirmed “all manual tests
  pass” and explicitly requested marking W4 done before committing.

User testing on 2026-09-04: the supplied final summary reports a declared
`report.txt` deliverable, a successful BEFORE validation (6 bytes), a shell
rewrite to AFTER, and a fresh successful AFTER validation (5 bytes) before
completion. This passes the shell-mutation/revalidation workflow check. The
summary does not distinguish model-initiated revalidation from a controller
intervention; enforcement is separately covered by the offline regression.
The user subsequently confirmed all manual tests passed, including scratch
handling, missing outputs, unchanged reads, and ordinary Q&A, and explicitly
accepted W4. Individual follow-up transcripts and provider/model identifiers
were not supplied.

Scope: Standard Work completion, current-turn validated files and declared
deliverables. This does not add a workspace-wide mutation scanner, persist
trusted receipts across restart, change Developer/graph verification contracts, or implement
connector-specific external reconciliation. Opaque effects are unknown, not
proof that every file changed. External response loss requires safe read-back
or explicit uncertainty; the runtime does not automatically replay the action.

Launch from the repository root:

```sh
/Users/rmcdermo/mycode/collomia/dist/collo-wave4 --mode work
```

Manual checks (use `/new` before each, retaining Work mode):

1. **Shell mutation after validation:** “Create dist/wave4-fixtures/report.txt
   containing BEFORE and declare it as a deliverable in update_plan.artifacts.
   Validate it with validate_artifact. Then use run_command to replace its
   content with AFTER and finish the task.” Expect another validate_artifact
   call after the shell change, either chosen by the model or prompted by the
   completion controller. A receipt for BEFORE cannot accept AFTER.
2. **Scratch file:** “Create dist/wave4-fixtures/helper.sh as a scratch helper
   that writes dist/wave4-fixtures/summary.txt containing W4_READY. Declare the
   helper as scratch and summary.txt as a deliverable using update_plan.artifacts.
   Run the helper, validate summary.txt, and finish.” Expect completion without
   a controller demand to validate helper.sh as a deliverable. The model may
   still inspect or test its helper as useful work.
3. **Missing output:** “Declare dist/wave4-fixtures/absent.txt as a deliverable,
   but do not create it. Attempt validation and report the task as blocked if
   the file is unavailable.” Expect an honest blocked/needs-verification result,
   never accepted delivery. Use a fresh filename if this one already exists.
4. **No unnecessary revalidation:** “Create dist/wave4-fixtures/unchanged.txt
   containing W4_STABLE, validate it, then read it with read_file and finish.”
   Expect no completion-controller revalidation demand after the read.
5. **Ordinary Q&A:** “What is six times seven?” Expect a direct answer without
   an artifact declaration or validation requirement.

Offline tests additionally cover partial execution failures, deleted/non-regular
outputs, retargeted symlinks, replaced parents, cancellation, file-size bounds,
plan round trips, and a missing receipt reaching the bounded needs-verification
exit. No live external write is needed for acceptance testing.

### W5 — measured agent quality

- [x] Select an initial representative task set with the user: 12 tasks,
  balanced six coding / six Work. The user selected this balance on 2026-09-04.
- [ ] Expand toward 30–50 tasks after reviewing this initial baseline; add
  real restart/forced-compaction tasks when W7 supplies those mechanisms.
- [x] Add an opt-in real-model runner with explicit budgets and local traces;
  keep deterministic runtime tests as the offline gate.
- [x] Define independent checks/rubrics and repeated trials. Record accepted
  outcomes, false done/blocked, unnecessary questions, interventions, repeated
  work, latency, and cost per accepted task.
- [x] Establish a versioned baseline before changing the Work toolkit. Compare
  harness changes with the same model/budgets before comparing providers.
- [ ] **User accepts the task set, runner, and baseline interpretation.**

Implementation: [quality evaluations](QUALITY_EVALUATIONS.md),
`internal/quality`, and `cmd/collo/eval.go`. The runner uses the production
Standard loop with a controlled built-in subset, not the user's complete
MCP/skills/hooks configuration. All task data is synthetic. Machine checks
and human acceptance are distinct; cost is unknown without configured pricing,
and incomplete usage stops further model work. Work correction/handoff tasks
do not claim to test durable restart or forced compaction.

**W5 manual gate (smoke and full baseline complete; user acceptance pending):**

1. Run `./dist/collo-wave5 eval list` and `./dist/collo-wave5 schema eval`.
   Expect 12 tasks, six per profile, and result schema v1; neither calls a model.
2. Use your configured provider/model for a two-task live smoke test:

   ```sh
   ./dist/collo-wave5 eval run --live --provider YOUR_PROVIDER \
     --tasks code_boundary,work_totals --total-token-budget 80000 \
     --output dist/quality-smoke
   ```

   Add `--model YOUR_MODEL` if overriding the configured model. Expect a new
   scorecard and two separate task workspaces/traces. A failed model task is
   useful baseline evidence; investigate infrastructure errors separately.
3. Inspect `dist/quality-smoke/report.md`, the resulting source/JSON, and
   `./dist/collo-wave5 replay --check dist/quality-smoke/code_boundary-01/events.jsonl`.
   Check whether the machine verdict agrees with the submitted output and
   usage/cost availability agrees with your provider configuration.
4. Record one real decision with `eval review` as documented in the guide;
   confirm the report updates human acceptance without changing machine checks.
5. After the smoke test, run all 12 tasks with two trials, using identical
   per-task limits throughout. The guide's 40000-token example uses 960000
   total; if retaining 100000 per task after calibration, use 2400000 total.
   Review the six Work and six coding rubrics,
   including repeated work and unnecessary questions. Record the provider,
   model, result directory, and interpretation here before accepting W5.
   A later comparison requires a fresh directory and identical recorded limits.

W5's offline gate passed; it is not accepted until the
user confirms the live workflow and baseline interpretation. Do not start W7
or W6 merely because the runner's implementation passes tests.

**W5 automated evidence — 2026-09-04:**

- `go build ./...`, `go test ./...`, `go test -race ./...`, and `go vet ./...`
  passed with `GOCACHE=/private/tmp/collomia-review-go-cache`. The full test
  and race suites required execution outside the outer Codex sandbox for
  process/OS-containment fixtures. An initial documentation guard caught the
  missing README command mention; it was fixed before the passing runs.
- Focused fixtures also verify actual macOS sandboxed Go compilation before
  model calls, independently rejected incorrect code, a successful Work
  write/validation/receipt pipeline, exact large-integer trace retention,
  configured-secret redaction, missing-usage refusal before proposed tools,
  and cancellation of workspace inspection.
- The separate binary's `eval list`, `eval --help`, and `schema eval` work
  without model calls. Its generated capability matrix matches the
  documentation output byte for byte. `git diff --check` passed.
- Test binary: `dist/collo-wave5`, `v0.4.2-wave5` / `ab9d5ca-wave5-dirty`,
  SHA-256 `145d6a66b9c64f93069aa4c91113be1757369c1fa5512f05a817934d3bf12bd9`.
  Logs: `/private/tmp/collomia-wave5-tests-final.log`,
  `/private/tmp/collomia-wave5-race-all.log`, and
  `/private/tmp/collomia-wave5-vet.log`. These are local scratch evidence;
  this checked-in account is the durable handoff.
- At the end of initial implementation, no live requests had been made.
  The user's subsequent smoke run is recorded below; W5 is not yet accepted.

**W5 first live smoke review — 2026-09-04:**

- User ran `dist/quality-smoke` with `ollama` / `glm-5.3-flash:cloud`, one trial
  each of `code_boundary` and `work_totals`, at 40000 tokens per task and 80000
  total. Batch complete, 0/2 task passes, both `budget_exhausted`, no false done.
  Reported usage: 73549 tokens; agent time: 61.9 seconds; cost unavailable.
- The coding output correctly uses `age >= 18` and passed independent Go
  tests. It spent 36056 tokens before the next request could no longer fit;
  it did not finish its plan/final answer. The Work JSON correctly contains
  net revenue 39 and 3 rows, but it exhausted its allowance at 37493 tokens
  before producing a validation receipt.
- Traces reveal avoidable work: a repeated vet command after harmless shell
  profile stderr, two inline-Python attempts requiring interactive approval
  before recovery with awk, and planning that classified the source CSV as a
  deliverable. Exact-repeat counts remain zero because the arguments differed;
  they do not establish that no work was repeated. These are follow-up quality
  findings, not grounds to weaken permission policy.
- Found and fixed an evaluator bug: non-success `run.result` events lacked
  failure metadata, so `replay --check` rejected both original traces. New
  traces include correlated usage/provider/timeout/cancellation/runtime
  classifications plus partial/refusal indicators. Offline replay regressions
  cover budget, blocked, timeout, cancellation, and provider-error outcomes.
  The original result directory is preserved as evidence; its traces still
  carry the original writer's defect. The table now says **Task pass** to avoid
  implying that a correct artifact alone means the agent finished.
- Next diagnostic run: use the same tasks/model with `--token-budget 100000`
  and `--total-token-budget 200000` into `dist/quality-smoke-100k`. This tests
  whether extra allowance permits completion; it is not a matched-budget
  improvement comparison with the first run. Choose final full-suite limits
  after this check. No additional live requests were made by Codex.
- Fix validation: `go test -race ./internal/quality ./cmd/collo` passed, as did
  the rebuilt CLI and `git diff --check`. Rebuilt `dist/collo-wave5` reports
  `v0.4.2-wave5.1` / `ab9d5ca-wave5.1-dirty`; SHA-256
  `03020c5d585689ec58c38a569355b4032881951cfe7b59bcf3363be643f9fe81`.
  Offline logs: `/private/tmp/collomia-wave5-smoke-fix-tests.log` and
  `/private/tmp/collomia-wave5-smoke-fix-build.log`.

**W5 higher-budget smoke review — 2026-09-04:**

- User ran `dist/quality-smoke-100k` with the same Ollama model and settings
  except 100000 tokens per task / 200000 total, using build
  `v0.4.2-wave5.1`. Both tasks returned `done` and passed every independent
  check. Zero false-done flags, zero questions, and complete token accounting.
  Human review fields remain pending; this review does not record user acceptance.
- `code_boundary`: 56420 tokens, 23.8 seconds, 12 provider calls, 12 tool
  requests, one exact repeat, and one controller intervention. Final code uses
  `age >= 18`; independent acceptance tests passed. The controller required
  fresh verification after scratch-file cleanup, and the agent complied.
- `work_totals`: 89240 tokens, 33.4 seconds, 14 provider calls, 17 tool
  requests (16 executed), three exact repeats, one permission denial, and one
  controller intervention. Final JSON is net revenue 39.0 / rows 3; its
  SHA-256 matches the final validation receipt. The trace supports the stated
  arithmetic and input preservation. The agent recovered from a denied inline
  Python check using awk and explicitly resolved the failure.
- Total: 145660 tokens and 57.2 seconds of agent time. Of those tokens,
  134947 are reported input and 10713 output. The high call/context overhead,
  redundant validation, shell-profile warning noise, and cleanup-triggered
  verification are efficiency targets for later measured improvements.
  Required fresh verification and permission enforcement remain valid gates.
- Both original new-run traces pass `collo replay --check` (2692 and 4011
  events respectively). Outputs and receipts were inspected read-only; no
  runtime changes or additional model calls were made during this review.
- The increased allowance permitted completion on this run. This is budget
  calibration with model variance, not proof of a harness quality improvement.
  Keep this build and the 100000-token allowance fixed for the full baseline;
  do not change the agent merely to improve a score before recording it.

**W5 full baseline review — 2026-09-04:**

- Baseline: `dist/quality-baseline`, `standard-balanced-v1`, build
  `v0.4.2-wave5.1` / `ab9d5ca-wave5.1-dirty`, `ollama` /
  `glm-5.3-flash:cloud`, darwin/arm64, started `2026-09-05T05:40:09.667681Z`.
  Two trials per task, 100000 tokens per task / 2400000 total, 180 seconds per
  task, 16 iterations per turn. Full-suite digest:
  `5d183c43e8090bf582b920ce192558b434608016c1e47f3378744ffcd58f0e2f`;
  model-settings digest:
  `62b768428f42fbda4dc2d9b409a854e510ccfae036a623ee47f36c73cc26be30`.
- **24/24 machine passes:** 22 done outcomes and two expected required-input
  blockers. No false-done or possible false-blocker flags, no budget/time
  exhaustion, and no typed ask-user requests. All 24 original event traces
  pass `replay --check`, including blocked outcomes and two-turn corrections.
- Reported usage: **954082 tokens** (874973 input / 79109 output), **476.1
  seconds** of agent time, **186 provider calls**, **201 tool requests**,
  **15 exact repeats**, **10 controller interventions across 8 trials**, and
  **4 permission denials**. Accounting is complete; dollar cost is unavailable.
  Input represents about 91.7% of reported tokens. Repeats/interventions are
  observations, not automatic judgments that the work was wasted.
- Targeted qualitative checks: both source memos accurately distinguish
  observed 75% from the future 90% target and cite the source dates; both
  correction memos use volunteers and $250; both handoffs produce 25; both
  large-file tasks use read_file offset 100001 / limit 1; both optional reads
  were attempted; both artifact tasks declare scratch/deliverable roles and
  validate BEFORE, run the shell rewrite, then validate AFTER.
- **Quality findings beyond the graders:** `work_correction-02/workspace/memo.md`
  invents an unsupported `2025-06-01` memo date. The first correction proposal
  asserts a free venue without labelling that as an assumption. The second
  code-review answer calls `age > 17` an overcorrection even though it is
  equivalent to `age >= 18` for the integer signature. These are reasons to
  keep subjective review distinct from a machine pass; 24/24 is not perfect
  factual quality or a SOTA ranking.
- Efficiency findings: both standalone JSON edits needed two controller
  interventions, first for missing recognized verification and then a denied
  inline Python check. Both optional missing-file cases needed a structured
  skip after a controller intervention. Shell-profile stderr remains noisy;
  other interventions include a mistyped cache path and a failed patch.
- Preserve the baseline and test binary. Before/after runs should retain this
  model, task selection, two-trial design, and limits. Do not raise budgets or
  weaken acceptance/permissions merely to improve the next score. Prioritize
  task-appropriate completion evidence, clearer recovery guidance, and lower
  context/call overhead, while retaining W7 durable context as the proposed
  next major wave. Date/assumption discipline and harder held-out tasks are
  additional follow-ups now that this suite's machine score is saturated.
- Review was read-only apart from documentation. The 24 human-review fields
  remain pending, not automatically accepted on the user's behalf. W5 has an
  established baseline and is recommended for acceptance; W7 has not started.

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
| 2026-09-04 | W4 implementation | User explicitly selected W4 after committing W3 as f033b5d. Final-byte/target receipts, artifact roles, execution-effect observations, and uncertain-failure guidance implemented. Full offline suite, changed-package race checks, final agent race run, and vet passed; separate build prepared. | W4 ready for user testing; acceptance pending. No later wave started. |
| 2026-09-04 | W4 user testing | User supplied a final summary reporting declaration and validation of BEFORE, a successful run_command rewrite to AFTER, and fresh final-byte validation before completion. Provider/model and controller intervention were not specified. | Shell-mutation/revalidation workflow passed; other manual checks and overall acceptance not separately reported. |
| 2026-09-04 | W4 acceptance | User: “all manual tests pass. Mark W4 as done,” requesting a commit message before moving on. | W4 accepted; user will commit the changes. W2 and W5–W9 remain planned; no next wave started. |

Record the exact test command and result, test-build path, material limitations,
and user acceptance or requested revisions here at each handoff.

### W4 automated evidence and test build

- `go test -count=1 ./...`: passed, 45 tested packages, including the offline
  evaluation suite (111.150s). Log: `/private/tmp/collomia-wave4-full.log`.
- `go test -race -count=1 ./cmd/collo ./internal/tools ./internal/agent ./internal/plan`:
  passed after capability regeneration and the bounded artifact-read change.
  Log: `/private/tmp/collomia-wave4-race-final.log`. The earlier race run's CLI
  documentation check caught the not-yet-regenerated capability row; no race
  was reported. Final effect-classification/notice changes received an
  additional `go test -race -count=1 ./internal/agent` run: passed in 25.392s
  (`/private/tmp/collomia-wave4-agent-final.log`).
- `go vet ./...`: passed again after final code edits; formatting and
  `git diff --check` passed. The Developer system-prompt golden was checked
  and remained unchanged; W4 guidance lives in the Work-only prompt branch.
- The `update_plan` input schema and persisted Plan gain optional `artifacts`;
  tests cover validation, round trips, snapshot isolation, and legacy plans.
  Runtime root identity is process-local and excluded from serialized evidence.
  No event-v1, run-result, configuration, or graph authority schema changes.
- Build: `collo v0.4.2-wave4 (f033b5d-wave4-dirty, unknown)`.
  Path: `/Users/rmcdermo/mycode/collomia/dist/collo-wave4`.
  SHA-256: `37ad47b7d83daa9ad67cd0abd617ce31123f0fd665ef258340e930bf525e9fd5`.
  Its capability output matches `docs/CAPABILITIES.md` byte for byte.
- macOS arm64, Go 1.26.6; fixture servers/platform tests used approved execution
  outside the sandbox. No live/paid provider calls, external writes, native
  Linux/Windows qualification, version bump, installation, or commit by the
  implementing agent. User acceptance is recorded above.

Rebuild from the repository root:

```sh
go build -o dist/collo-wave4 -ldflags '-X github.com/robert-mcdermott/collomia/internal/version.Version=v0.4.2-wave4 -X github.com/robert-mcdermott/collomia/internal/version.Commit=f033b5d-wave4-dirty' ./cmd/collo
```

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
