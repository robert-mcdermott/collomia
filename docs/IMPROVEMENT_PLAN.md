# Agent improvement plan

Updated: 2026-09-06. Source: [project review](PROJECT_REVIEW_2026-09-04.md).

This is the durable implementation and user-testing plan for the September
review. It supplements the [roadmap](../ROADMAP.md). The existing
[orchestration strategy](ORCHESTRATION_STRATEGY.md) remains authoritative for
Orchestrated Goal, its supported/experimental distinctions, authority
boundaries, and non-goals. These waves do not reopen completed milestones.

## Current handoff — read this first

- **Current work:** Larger-project Standard completion and continuation reliability (Developer and Work).
- **Status:** implementation, automated verification, and the candidate build
  are complete; live user acceptance is pending. The small Developer rerun remains accepted as a small-task
  result; it did not establish larger-project reliability. Overall live acceptance
  and the Work-mode rerun remain pending. W1, W3, W4, W5, and full W7 remain
  accepted; W2, W8, and W9 remain planned. The withdrawn reporting workflow stays
  withdrawn.
- **Next action:** obtain user acceptance on a larger Developer task and a Work
  task using the candidate and manual checks below.
  Do not begin another feature wave by default.
- **Test build:** `dist/collo-standard-reliability`, version
  `v0.4.3-standard-reliability.1`, based on `b7abf6d`.
  Prior binaries and user sessions are preserved. The installed binary and
  user's `VERSION` are unchanged.
- **User acceptance:** W1 accepted on 2026-09-04. The user reported it “worked
  great” with GLM-5.3-flash from Ollama and confirmed visible reasoning.
  W3 accepted on 2026-09-04 after successful pagination and follow-up manual
  tests. The optional low-output-token check was not separately confirmed.
  W4 accepted on 2026-09-04 after all manual tests passed.
  W7a accepted on 2026-09-04: user reported “manual testing passes.”
  W7b and full W7 accepted on 2026-09-05: user reported manual testing was
  complete and successful. Provider/model was not specified.
- **Deferred W2 work:** W1 only displays readable reasoning events already
  emitted by an adapter. It does not enable thinking at the API, add synchronous
  reasoning extraction, or restore thinking in reopened chat transcripts.
  Those are W2 work. A silent summary area does not prove absent computation.

## Larger-project candidate — September 6

Implementation checklist:

- [x] Directory-scoped project checks cover included source files and directory
  declarations, including declarations retained by older sessions.
- [x] Snapshot freshness detects edits, additions, deletions, file-mode changes,
  symlink retargeting, and directory replacement; child read policy/hooks apply.
  Dependency/cache/build outputs are excluded, never silently accepted as
  standalone deliverables.
- [x] Command execution identity ignores timeout and verification metadata.
  Successful corrected calls recover pre-execution assessment rejections of the
  same tool; permission denials and uncertain effects keep their safeguards.
- [x] Retain up to 64 bounded historical successful-operation facts across a
  pause/restart for explicit recovery, without restoring fresh validation or
  permissions.
- [x] Independent Standard total limit (256 by default), startup
  `--max-turns` / `--max-no-progress`, and live `/limits`.
- [x] At most two counted retries for completed-but-empty responses; no tool
  replay, no retry of refusal/truncation/partial tool payloads, clear provider
  unavailability label.
- [x] Kanban-shaped native-tool regression in Developer and Work: over 40 nested
  project files, malformed patch correction, failing check repaired, retry with
  added scope, budget pause, new agent instance, two empty responses, a fresh
  project check, and explicit recovery using a retained earlier success.
- [x] Full suite, race checks, vet, candidate build, and cross-build results
  recorded below.
- [ ] User acceptance of the larger Developer retest.
- [ ] User acceptance of the Work-mode retest.

Manual checks for this candidate:

1. Start a normal multi-file Developer task with the candidate. You do not need
   special verification wording in the task prompt. Confirm that normal project
   tests/builds can finish with directory scopes and no per-source-file validation
   loop. Inspect the summary for the checks actually performed.
2. Enter `/limits`, then `/limits 500 24` while it runs; verify that the displayed
   limits change. These are provider response cycles, not tool calls. A deliberate
   small total (for example `/limits 3` on a task requiring more work) should pause
   as budget exhausted, retain work, and allow `/limits 500` followed by
   `continue`. Lowering a running total below cycles already spent stops at the
   next boundary.
3. Repeat a meaningful Work task involving inputs, a disposable helper, and a
   requested output. Confirm that the output gets a relevant check and scratch
   helpers do not create independent completion obligations.
4. Optional existing Kanban recovery: resume with the candidate and request a
   fresh backend test and frontend build, followed by resolution of remaining
   historical failures. Older sessions did not save historical success facts,
   so they need fresh recovery operations. No old transcript text is imported
   as proof. If the proxy still returns empty responses, expect two retries and
   a clear provider-unavailable stop; switch/check the provider before continuing.

Candidate invocation from this repository:

```sh
./dist/collo-standard-reliability --cwd /path/to/project --mode developer --max-turns 500
```

For the existing Kanban session:

```sh
./dist/collo-standard-reliability --cwd /Users/rmcdermo/mycode/kanban20 --resume 20260906-201718-711ba7 --max-turns 500
```

Automated evidence on the final candidate source:

- `go test ./...` passed, including the existing agent, application, evaluation,
  provider, tool, and graph regression suites.
- Targeted `go test -race` passed across agent/app/TUI/config/safefile/CLI:
  project and file scopes, pause/resume, recovery ordering, child-path denial,
  symlink boundaries, live limits, and empty-response handling.
- `go vet ./...` passed.
- Native macOS candidate built; Linux and Windows amd64 cross-builds passed.
  Cross-builds do not establish native runtime testing on those systems.
- Generated capabilities match the candidate. Markdown link targets and code
  fences were checked across the README, roadmap, and 23 docs pages;
  `git diff --check` passed.
- Local verification logs: `/private/tmp/collo-reliability-verified.log`,
  `/private/tmp/collo-reliability-race-final.log`, and
  `/private/tmp/collo-reliability-vet-final.log`.

The Kanban-shaped regression finishes without completion interventions in both
profiles after a pause and a new agent instance. Negative cases retain genuine
failure/verification gaps. Historical recovery facts are ordered: an old pass
cannot erase a newer failure, including when a provider reuses an ID.
Scripted success does not establish live provider behavior or replace user
acceptance. No user session, application output, installed binary, or provider
configuration was modified to obtain these results.

## Active follow-up — Task-scoped completion reliability

Authorized September 6 after `worktest2-c7261c0ccb1c` produced and checked a
report but exhausted completion interventions on supporting scripts. This fixes
Standard execution only; it does not reopen graph milestones or change their
verification authority.

- [x] Honor scratch/deliverable roles in both modes and use `.collomia-tmp/`
  as the default disposable-helper directory. Explicit deliverables take
  precedence; aliases outside scratch and failed commands remain tracked.
- [x] Accept native artifact checks in Developer as well as Work, alongside
  scoped commands; preserve unrelated source obligations and final-file freshness.
- [x] Parse quoted multiline scoped checks correctly without accepting outer
  shell pipelines, fallback chains, redirection, or substitutions as proof.
- [x] Recover native preflight-only rejections through fresh scoped replacements
  with matching intent and covered paths, including after resume.
- [x] Hold Standard final-answer text until completion accepts it; give
  verification-only gaps a truthful UI label and name actual uncovered paths.
- [x] Add native-tool regressions for dashboard completion in both modes,
  existing helper classification, scratch boundaries, stale files, recovery,
  shell quoting, and final-answer display.
- [x] Complete broad offline checks and candidate build verification.
- [x] **Developer live rerun accepted:** user reported success; transcript verified
  September 6 (details below).
- [ ] **Work live rerun accepted.** Do not begin the next
  major wave before this gate. Scripted checks cannot establish model judgment,
  latency, or visual quality.

### Task-scoped completion checks

Run the same ordinary task in fresh folders in each mode, using the candidate:

```sh
./dist/collo-task-completion --cwd /path/to/developer-test --mode developer
./dist/collo-task-completion --cwd /path/to/work-test --mode work
```

Put a copy of the sunspot CSV in each folder first. Suggested prompt:

> Analyze the sunspot CSV in this folder using its first two columns and create
> an HTML dashboard reporting your findings. Check the result appropriately.

Do not prescribe tool arguments, scratch roles, or recovery IDs in the prompt:
the agent should handle those details. Confirm that the requested output is
created, suitable checks pass with truthful coverage descriptions, and the turn
finishes with one accepted final answer and no false “Blocked” status. Disposable
helpers should be under `.collomia-tmp/` or declared scratch by the agent.
No redundant check should be required merely to validate a checking script.
If an actual check fails, normal diagnosis and repair are expected.

Automated negative cases cover unrelated project changes, failed assertions,
explicit deliverables inside scratch, stale bytes, and symlink escapes; users
need not reproduce those manually. Existing sessions can retain root-level
helpers: the agent can classify them as scratch through its plan without
moving files, and obtain fresh evidence where restart requires it.

### Live evidence — Developer, September 6

- User reported “it worked that time.” Verified session
  `worktest3-ac3792d381a1/20260906-164751-6176d3`: Developer mode, Ollama
  `deepseek-v4-flash:0731-cloud`; the task ran from 16:48:10 to 16:49:28 UTC
  (about 78 seconds).
- Analysis and build helpers used `.collomia-tmp/`. A native artifact check
  supplied the dashboard receipt without demanding independent helper checks.
  The final file digest still matched the recorded receipt when inspected.
- Two real tool mistakes occurred: running analysis from the wrong directory
  and a malformed plan update. Both recovered. One completion intervention
  requested disposition of the earlier executed analysis failure; the agent
  supplied an alternative receipt, and completion cleared all durable obligations
  and ended normally without a terminal error or user “continue.” This was a
  successful recovery run, not a zero-intervention run.
- Evidence establishes completion/recovery behavior. Checks were text/content
  checks, not browser execution or an independent audit of analysis correctness.
- A second session file in that directory (`20260906-164723-c3085e`) contained
  only initialization records and no attempted user turn.

### Live evidence — larger Developer project, September 6 (not accepted)

Session `kanban20-2edc407ef8c0/20260906-201718-711ba7` exposed remaining defects:

- The first user turn hit the 48-provider-request hard limit despite progress.
  The tested build defaulted `options.max_iterations` to 24 and also set the
  hard limit to twice that value, without independent CLI/live controls.
- Application checks had already reported 47 passing pytest tests, a successful
  Vite production build, and 26 passing API/static-serving smoke checks.
- Plan declarations retained directories (`app`, `frontend`, `tests`, later
  `frontend/src`) as deliverables, but scoped verification accepts regular files
  only. The directories remained impossible-to-satisfy receipt obligations even
  after later file scopes were checked. The 16-file limit caused extra splitting
  and duplicate verification. Project verification needs a distinct contract
  from standalone file artifacts; invalid declarations need correction/migration.
- Pre-execution patch argument errors and changed-command retries remained
  failures after successful alternatives. After the iteration stop, dispositions
  referring to previous-turn receipts were rejected as not current-turn proof.
- During the next ~14 minutes, recorded file mutations were confined to two
  scratch verification scripts. The agent was mainly satisfying controller
  requirements and repairing assertions in its own extra checks.
- The final failure and two additional `continue` attempts each produced
  `fh-kiro` protocol errors: stop `stop`, zero reported output tokens, and no
  usable assistant text/tool calls. No output-limit stop or refusal was recorded.
  The transcript cannot distinguish upstream empty output from proxy/adapter
  loss. Diagnostics and bounded safe recovery are needed; empty output is not
  evidence that the task failed or completed.
- Priorities identified during diagnosis (implemented in the candidate above,
  awaiting live acceptance): project-level verification scope and directory
  correction; recovery identities that separate never-executed validation errors
  from actual failed operations and preserve evidence across budget pauses;
  independent total/no-progress limits with live controls; actionable empty-response
  diagnostics and bounded provider recovery. Preserve fresh evidence for changed
  files and do not replay uncertain external effects.

This does not revoke the smaller dashboard result. It shows that the larger
application workflow has not passed acceptance. No session, project output,
provider configuration, or runtime code was changed during this diagnosis.

### Automated evidence — task-scoped follow-up

- Focused native-tool regressions passed, including full agent runs with zero
  completion interventions in Developer and Work.
- Race checks passed for agent, TUI, plan, prompts, and session packages:
  `/private/tmp/collo-task-race.log`.
- `go test ./...` passed, including the offline evaluations and graph boundary
  regressions: `/private/tmp/collo-task-full.log`. Localhost/native sandbox
  integration fixtures required approved execution outside the tool sandbox.
- Final agent/TUI/prompt/plan race checks passed:
  `/private/tmp/collo-task-final-race.log`. The final scratch-receipt refinement
  passed the complete agent race suite again:
  `/private/tmp/collo-task-scratch-race.log`.
- `go vet ./...` passed: `/private/tmp/collo-task-vet.log`.
- Native macOS candidate build and Linux/Windows amd64 cross-builds passed.
  Cross-builds establish compilation, not native execution on those platforms.
- Checked relative file links and fences in 26 Markdown documents; no broken
  file targets or unclosed fences. Generated capabilities match the candidate;
  `git diff --check` passed. TUI golden changes reflect the larger prompt's
  context-usage percentage; the status-label behavior has a dedicated test.
- Candidate: `v0.4.3-task-completion.1`,
  `d39ee78-task-completion.1-dirty`, built September 6. The installed binary,
  user configuration, session transcripts, original test projects, and `VERSION`
  were not modified. No live model request or commit was made.

## Previous follow-up — Standard completion simplification

This candidate was not accepted: subsequent live tests exposed excessive
reasoning in one run and a false Developer verification block in another.
The task-scoped follow-up above fixes the latter; adaptive generation budgets
and truncation recovery remain separate future work.

Authorized September 6 after clarifying that evidence-based completion remains
part of the product contract in Standard as well as Orchestrated Goal.
This supersedes the earlier proposal to replace completion gates with prompt
instructions alone. It does not remove runtime-owned evidence or open a new
orchestration milestone.

- [x] Add explicit `run_command.verification` scope for task-specific checks in
  Standard Developer and Work. Require observed success and unchanged authorized
  file bytes/target/parent before recording evidence.
- [x] Accept scoped command evidence in place of a duplicate Work artifact check.
  Keep declared outputs, stale bytes, failed checks, and unrelated paths guarded.
- [x] Recognize native file repairs only after a successful replacement/edit and
  current verification; retain explicit recovery for semantic alternatives.
- [x] Preserve command retry identity across timeout changes, including compatibility
  with older retained exact hashes. Preserve interruption and external-action guards.
- [x] Update event evidence and the quality grader for scoped file receipts.
- [x] Audit the `/docs` inventory; add [a documentation guide](README.md) and
  [the completion contract](COMPLETION.md). Mark the original review as historical.
- [x] Finish full offline tests, race checks, vet, cross-builds, and generated docs.
- [x] Deliver `dist/collo-work-general` as `v0.4.3-work-general.3`.
- [ ] **User accepts this follow-up.** Live comparison remains pending; do not
  claim less model latency or a quality improvement from scripted fixtures alone.

### Automated evidence — September 6 completion follow-up

- `go test ./...`: passed, including the offline evaluation suite.
  Log: `/private/tmp/collomia-adaptive-full.log`.
- `go test -race ./internal/agent ./internal/tools ./internal/event ./internal/quality ./internal/plan ./internal/prompts ./internal/app ./internal/session ./cmd/collo`:
  passed. Log: `/private/tmp/collomia-adaptive-race.log`. Final scoped-check
  parsing/guidance refinements passed targeted tools/agent race regressions again
  (`/private/tmp/collomia-adaptive-final-scope.log`).
- `go vet ./...`: passed (`/private/tmp/collomia-adaptive-vet.log`).
- Final CLI/documentation, plan, prompt, event-schema and quality suites passed
  (`/private/tmp/collomia-adaptive-final-docs.log`). The native quality fixture
  accepts scoped receipts with zero controller interventions; a graph regression
  rejects the same custom scope as graph verification.
- Linux amd64 and Windows amd64 cross-builds passed. This is compilation evidence,
  not native platform execution. Tests ran on macOS arm64; suites needing loopback
  or native sandbox fixtures used approved execution after restricted attempts
  encountered environment denials.
- Relative links and anchors checked across 26 Markdown documents: no unresolved
  targets or unclosed fenced blocks. Generated capabilities match the candidate.
- Candidate: `dist/collo-work-general`, `v0.4.3-work-general.3`,
  `304d7af-work-general.3-dirty`. Previous candidate retained as
  `dist/collo-work-general.2`. The user's `VERSION` bump is preserved. No installed
  binary update, commit, live-provider request, or user session/game edit occurred.
- This establishes runtime behavior under controlled fixtures. Browser gameplay,
  real analysis quality, provider performance, and live efficiency gains remain
  the user's manual/matched-evaluation gate.

### Completion simplification checks

Use the separate test binary in a fresh, disposable folder. No example project
or Work setup command is needed. The installed binary stays unchanged; preserve the user's `VERSION` bump.

```sh
/path/to/collomia/dist/collo-work-general --cwd /path/to/test-folder --mode work
```

1. **Game:** ask for a self-contained HTML game and proportionate verification.
   If browser tooling is available, Collo should exercise the game; otherwise it
   must say what it checked and that gameplay was not tested. A successful scoped
   check should not be followed by `validate_artifact` solely to satisfy completion.
   Open the game yourself. Repeat with `--mode developer` in a separate folder.
2. **Analysis:** supply a small CSV and ask for totals and a brief report. Collo
   should choose tools/skills, verify calculations, and finish with current file
   evidence. No prescribed reporting toolkit or template should appear.
3. **Recoverable check:** ask Collo to create `result.txt` containing `BEFORE` and
   a check script that requires `AFTER`; run it once to observe failure, then fix
   the output and rerun the same check with `verification.paths: ["result.txt"]`.
   The nonzero local exit should permit repair, and the successful retry should
   recover automatically. No `/recovery acknowledge` or failure-ID plan entry
   should be needed for that settled exact-operation retry.
4. **Freshness:** ask it to verify a file, rewrite it through a shell command,
   and finish. It must obtain new evidence on the final bytes. An unchanged
   receipt or a prose claim must not satisfy the old check after the rewrite.
5. **Direct answer:** ask a simple explanatory question. No synthetic file,
   artifact plan, or verification command should be created.

Automatic tests additionally cover failed-edit/replacement recovery, missing
proof after restart, masked exits, changed paths and parent identity, narrower
checks, permissions, and unchanged graph gates. Retain actual outputs and the
session path if live behavior differs; provider/model should accompany feedback.

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
W5 and full W7 are accepted. W6 was withdrawn/re-scoped; W2, W8, and W9 remain planned.
Revisit the next priority at each gate.
After accepting W3, the user explicitly selected W4 on 2026-09-04.
After W4, the user approved W5 next, with W7 then W6 proposed after its gate.

| Wave | Deliverable | Status | User gate |
| --- | --- | --- | --- |
| W1 | Separate, bounded live thinking summaries in the TUI | Accepted | User confirmed successful reasoning display with GLM-5.3-flash from Ollama |
| W2 | Reasoning configuration, provider-state continuity, and summary replay | Planned | Reasoning/tool conversations work on the user's actual providers, including reopen |
| W3 | Correct completion outcomes, exact failure recovery, large-file pagination | Accepted | User confirmed pagination and the follow-up manual tests passed |
| W4 | Final deliverable identity and observed-effect checks | Accepted | User confirmed all manual tests passed and explicitly marked the wave done |
| W5 | Real-model evaluation baseline | Accepted | Representative tasks and quality/cost metrics reflect the user's work |
| W6 | Bundled artifact workflow withdrawn; general primitives retained | Cleanup / retained-capability testing | Broad Work behavior without a prescribed workflow |
| W7 | Durable task context, retrievable evidence, and restart checkpoints | Accepted (W7a and W7b) | User confirmed successful manual testing of both slices |
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
- [x] **User accepts the task set, runner, and baseline interpretation.** Explicit
  continuation after commit `0c72f04`, 2026-09-04.

Implementation: [quality evaluations](QUALITY_EVALUATIONS.md),
`internal/quality`, and `cmd/collo/eval.go`. The runner uses the production
Standard loop with a controlled built-in subset, not the user's complete
MCP/skills/hooks configuration. All task data is synthetic. Machine checks
and human acceptance are distinct; cost is unknown without configured pricing,
and incomplete usage stops further model work. Work correction/handoff tasks
do not claim to test durable restart or forced compaction.

**W5 manual gate (completed; accepted by explicit continuation on 2026-09-04):**

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

W5 is accepted following the live baseline review and explicit continuation.
Individual `eval review` decisions remain pending unless entered by the user.

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
  established baseline. The subsequent user instruction to proceed accepted
  the wave; it did not assign individual artifact review decisions.

### W6 — withdrawn workflow; retained general improvements

**Decision — 2026-09-06:** the user wants Work mode to handle system maintenance,
data analysis, research, Q&A, automation, and other non-development tasks through
a capable model, general tools, and user-installed skills. The bundled reporting
workflow was too prescriptive. Initial example testing passed, but W6 was never
accepted; the user authorized selective removal after reviewing the tradeoffs.

- [x] Remove the embedded reporting skill, dedicated Python/Office toolkit,
  hidden runtime preparation/diagnostics commands, and workflow-specific prompt.
- [x] Restore ordinary user/project skill discovery and the general Work profile.
- [x] Retain ordinary command-failure repair, including the native distinction
  between observed nonzero exits and interrupted/known-external failures.
- [x] Retain generic `view_image` and explicit notices when images cannot reach
  the model; remove document-specific directions from the tool description.
- [x] Retain XLSX structure/relationship validation, bounded Open XML package
  checks, and explicit evidence scopes without a generation workflow.
- [x] Preserve user sessions, existing workspaces, baseline results and test
  outputs. Archive withdrawn sources and the pre-cleanup diff in ignored
  `dist/_withdrawn-w6-20260906-37310-4lqxh4`; this is historical local material,
  not shipped code or an installed skill. Earlier W6 binaries remain historical.
- [x] Validate the reduced implementation and prepare the separate test build.
- [ ] User confirms retained primitives and representative general Work behavior.

The first sample-kit and subsequent built-in-skill experiments, their offline
qualification, and the command-failure correction remain recorded in
[roadmap history](ROADMAP_HISTORY.md). Their live Office acceptance and matched
W5 comparison were never completed. They are superseded, not implied passes.

**Cleanup evidence — 2026-09-06:** full `go test ./...` and `go vet ./...`
passed. Agent/tools/app/skills/TUI/CLI/event race checks passed, as did Linux
amd64 and Windows amd64 Go cross-builds. Documentation links, generated
capabilities, and whitespace checks passed. The native test binary reports
`v0.4.2-work-general.1`; help and capabilities expose the retained general tools
without the removed workflow setup. Existing offline Work, skill-discovery,
resume, and recovery fixtures ran against the reduced code. No provider calls
were made; this does not establish live-model task quality or user acceptance.

#### Retained-capability checks

Use a fresh session to avoid instructions from the withdrawn skill in old
conversation history. No initialization or preinstalled reporting skill is needed:

```sh
./dist/collo-work-general --cwd /path/to/your-folder --mode work
```

1. **General Work:** ask a direct question, request a read-only system inventory,
   analyze an existing data file, or request source-backed research. Collo should
   select an appropriate approach and avoid inventing a report or artifact for
   tasks that do not need one. Existing permission/network controls still apply.
2. **Skills:** use a skill you installed through the existing skill mechanism.
   It should be discoverable/loadable as before; there is no shipped reporting
   skill or automatic artifact-runtime preparation.
3. **Local failure repair:** ask Collo to fix and rerun a failing local script
   or test. An observed ordinary nonzero exit should permit repair without
   `/recovery acknowledge`; unrelated successful work must not erase the failure.
4. **Images:** ask Collo to inspect a local screenshot, diagram, or chart using
   `view_image`. A capable model should receive the pixels; unsupported models
   should receive an explicit limitation rather than claim visual inspection.
5. **Validation:** ask for `validate_artifact` on an existing XLSX file. It should
   check structure and worksheet relationships while leaving calculation and
   visual quality unassessed. No Python or LibreOffice setup is required by the
   native tool. Generated deliverables still need fresh validation after edits.
6. **HTML and corrected checks:** request a self-contained HTML file or validate
   an existing one. `auto` should infer text; `html` is an explicit text alias.
   Corrected validations that preserve required text, minimum size, and format
   checks should recover without an administrative plan update. A check that
   drops a missing required string must not clear the failure.
7. **Quiet completion status:** if a genuine completion gap remains, the TUI
   should show “Checking remaining work.” Expand with `ctrl+o` to see diagnostics;
   transcript search/copy and logs retain them. Actual blocks remain visible.

**Validation follow-up — 2026-09-06:** the user reported a working Asteroids
game but excessive validation bookkeeping, using DeepSeek V4 Flash through
Ollama. The recorded attempts used unsupported `html`, then `auto` (previously
binary), then a successful `text` check with all six strings preserved. Exact
argument matching kept the first two failures open. The user authorized fixing
format inference, native validation recovery, and notice presentation.
The revised implementation retains bounded hashed validation requirements across
resume, prevents lowered text/size/parser requirements from auto-recovering a
failure, and leaves command/external retry identity unchanged. Full diagnostics
still reach the model and event log; this is not a hidden relaxation of gates.
This earlier build was `v0.4.2-work-general.2`; the active candidate and combined
manual gate are in the current handoff above.

Automated evidence: full `go test ./...`, affected tools/agent/TUI/activity/event/
app/session race checks, and vet passed. Native Linux/Windows Go cross-builds
passed. Regressions cover the format-correction sequence with zero interventions,
resume without restored receipts, text/size/parser downgrade rejection, bounded
requirement retention, and collapsed/live/restored diagnostic presentation.
The full event text and model guidance remain available; evaluation intervention
counting is unchanged. No live provider calls or edits to the user's game/session
were made. The previous general test binary is preserved as
`dist/collo-work-general.1`; installed Collo and `VERSION` remain unchanged.

Older sessions can still resume, but their history may contain loaded skill
instructions or old helper paths. Existing uncertain markers require inspection
and one acknowledgement as described in [recovery](RECOVERY.md); cleanup does not
rewrite logs or silently clear obligations. User-created files remain available.

**Next direction:** evaluate a broader mix of Work tasks before choosing another
feature wave. Keep specialized procedures in optional user-installed skills.
The preserved W5 suite can measure general regressions, but it does not cover
all system maintenance, research, or automation behavior. Do not expand the suite
or make live provider calls as part of this cleanup without a separate request.

### W7 — durable context and recovery

W7 is split into two user-testable slices. W7a adds retained context; W7b
changes recovery behavior only after the first slice is accepted.

**W7a — retained context and evidence (accepted):**

- [x] Maintain an 8 KiB revisioned task record: objective, corrections,
  constraints, decisions, sources, artifacts, evidence references, gaps, next action.
- [x] Pin bounded genuine user-request previews separately from fallible model notes.
- [x] Add session-scoped search/read with stable message IDs across compaction,
  resume, and fork; rewind exposes only the retained prefix.
- [x] Add `/context task` inspection and `/context clear` for notes.
- [x] Exercise repeated real compaction and restart with a scripted provider,
  stale revisions, storage errors, redaction, missing evidence, bounded paging,
  Unicode, cancellation, session isolation, fork, and rewind offline.
- [x] Complete broad automated checks and deliver a separate test build.
- [x] **User accepts W7a**: “manual testing passes,” 2026-09-04, following
  [the manual guide](TASK_CONTEXT.md#manual-acceptance-checks).

**W7b — durable obligations and recovery (accepted):**

- [x] Persist Standard completion obligations and bounded recoverable workspace
  checkpoints across restart, including non-Git Work folders.
- [x] Test cancellation/restart, external edits, and uncertain mutations;
  do not replay ambiguous external effects or reuse stale validation receipts.
- [x] **User accepts W7b / full W7:** manual testing complete and successful,
  2026-09-05. Workspace memory is optional future scope,
  with provenance/edit/delete controls; unrelated-project semantic memory is deferred.

**W7b implementation — 2026-09-05:**

- Runtime completion state persists dirty paths, non-waivable deliverables,
  unresolved failures, and a synced pre-execution uncertainty marker. Fresh
  turns restore obligations, never old validation receipts, waivers, or grants.
- Tracked file checkpoints retain bounded binary bytes/existence/modes with a
  directory identity and coverage floor. Append-only deltas avoid duplicating
  unchanged file entries. Resume/fork/rewind follow session history; new/switch
  replace the live tracker rather than leaking another session's changes.
- `/restore` and `/undo` journal their own application; interrupted restores
  block continuation and further restore until explicit inspection/keep.
  `/recovery` inspects state; user-only acknowledge/keep commands retain current
  files and discard incomplete checkpoint coverage without validating work.
- Command/external effects are never rolled back or automatically replayed.
  Crash gaps and retention limits remain explicit. This is Standard recovery;
  Orchestrated Goal authority and integration recovery are unchanged.
- Focused offline tests pass, including cancellation/restart, original binary
  bytes, mode/drift guards, fresh receipts, plan-drop resistance, failed-tool
  identity collisions, pre-effect sync failure, bounded retention, replaced
  roots, delta replay, interrupted restore reconciliation, task-mode bypass
  rejection, and permitted read-only inspection while effects remain uncertain.
- `go test ./...` and `go vet ./...` passed. Affected agent/app/session/diffmodel/
  TUI/safefile/CLI packages passed with `-race`; final affected-package race
  checks also passed after inspection and coverage refinements.
- Built the separate `dist/collo-wave7b` binary: `v0.4.2-wave7b`
  (`0f618aa-wave7b-dirty`). Its generated capability output matched the matrix
  before the acceptance status update. `git diff --check` passed. Linux amd64 and Windows amd64 cross-builds
  passed; this is compile evidence, not native runtime qualification.
- No live provider calls were made by the implementation/test work. The user
  subsequently confirmed successful manual testing on 2026-09-05, accepting
  W7b and full W7; provider/model was not specified. The W5 sessionless quality harness does
  not measure this new recovery behavior or its persistence overhead.
- See [Standard recovery](RECOVERY.md) for exact controls, limits, compatibility,
  and manual checks retained for regression testing. Full W7 is accepted.

W7a uses additive session records (`task_context` payload schema 1 and explicit
`user_request` references). Its working notes are claims, never permission or
runtime completion receipts. Original evidence can be stale and must be checked
against current files before claiming current validity. Legacy role=user messages
are not retroactively asserted to be genuine user requests. Ephemeral runs omit
the four context tools. No background continuation or workspace restore changes.

**W7a validation — 2026-09-04:**

- `go test ./...`, `go test -race ./...`, and `go vet ./...` passed.
- After final prompt/provenance refinements, affected session, app, agent,
  prompts, TUI, and CLI packages passed again with `-race`.
- Built `dist/collo-wave7`; `--version` reports `v0.4.2-wave7a`
  (`0c72f04-wave7a-dirty`). Its generated capability output matches the checked-in
  matrix. `git diff --check` passed. `VERSION` and the installed binary are unchanged.
- Session/app/TUI checks cover repeated compaction/restart, stale revisions,
  append/sync failures, redaction, UTF-8 paging, bounded 2000-message search,
  explicit user/runtime provenance, cancellation, missing evidence, isolation,
  fork/rewind, ephemeral omission, and inspect/clear with a busy-turn guard.
- No live provider requests were made during implementation. The user then
  reported “manual testing passes” on 2026-09-04, accepting W7a on the prepared
  test build. Provider/model was not specified for this gate. The checks are in
  [Task context](TASK_CONTEXT.md#manual-acceptance-checks).
- The controlled W5 quality runner has no durable session, so its tasks do not
  measure this capability. W7a uses production-app scripted compaction/restart
  tests plus the manual gate; its benefit and prompt overhead need live evaluation.

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
| 2026-09-04 | W5 acceptance / W7a implementation | User directed the next wave after commit `0c72f04`. Added retained context and evidence retrieval; offline test/race/vet gates passed and separate `v0.4.2-wave7a` build produced. | W5 accepted; W7a ready for manual testing. W7b remains planned behind that gate. |
| 2026-09-04 | W7a acceptance | User reported “manual testing passes” after receiving the W7a test build and manual guide. Provider/model was not specified. | W7a accepted; W7b unblocked but not started. Full W7 remains incomplete. |
| 2026-09-05 | W7b implementation | User explicitly selected W7b after W7a commit `0f618aa`. Durable Standard obligations and bounded checkpoint recovery passed offline tests, affected-package race checks, vet, and cross-build checks. Separate `v0.4.2-wave7b` binary and manual guide prepared. | W7b ready for user testing; full W7 acceptance and later waves remain pending. |
| 2026-09-05 | W7b / full W7 acceptance | User confirmed manual testing was complete and successful and requested that W7b be marked complete. Provider/model was not specified. | W7b and full W7 accepted; ready to commit. W6 remains proposed, not started. |
| 2026-09-05 | W6 implementation | User selected the Work artifact workflow after W7b commit `304d7af`. Exported locked toolkit, native image/XLSX evidence support, real creation/revision/rendering checks, and separate test build delivered. Offline, race, vet, cross-build, and fresh virtual-environment gates passed. | W6 ready for user testing; live workflow acceptance and matched W5 comparison pending. Later waves remain planned. |
| 2026-09-05 | W6 UX revision | User reported the initial workflow worked but requested ordinary Work-mode tasks without a pre-populated example project. Added a built-in skill, hidden runtime preparation through setup, and read-only diagnostics; removed the unreleased work-init command. Full offline, race, vet, cross-build, and required-sandbox operational checks passed. | Revised build ready for natural-request testing in an existing folder; W6 acceptance and matched W5 comparison pending. |

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
