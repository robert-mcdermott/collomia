# Real-model quality evaluations

Kanban29 regressions in `internal/agent/automatic_verification_test.go` run native
shell checks through the agent loop in Standard Developer and Work. A plain
project build, later documentation writes, malformed verification metadata,
rejected piped check, valid docs check, narrower build scope and late directory
declaration finish without interventions or terminal errors. Counterexamples
retain incomplete outcomes for failed builds, source/lockfile drift, changes
during a build, new inputs, denied reads, masked statuses and explicitly narrow
coverage. They test harness behavior, not full application correctness or a
live-provider success rate. Candidate `v0.4.4-recovery-reliability.5` passes the
full offline suite, agent/tools/session/goalgraph/TUI/CLI race checks, vet,
six-platform builds/checksums and Developer/Work native PTY startup, resize,
status and quit checks. Legacy preflight-failure recovery remains covered
separately. Standard Developer user acceptance passed on 2026-09-06 through
Kanban30 (`20260907-053039-c1665a`, Ollama `deepseek-v4-flash:0731-cloud`):
9 backend tests, passing frontend build, 10/10 HTTP smoke checks and launcher/docs/
ignore-rule checks. The run repaired a real cascade-delete bug, recovered from
an output-limit continuation and corrected verification input/scope issues.
One completion intervention was resolved autonomously; there were no terminal
error events and the final recovery state was empty. Its frontend build used
explicit project scope, so automatic inference is covered by offline tests only.
Work-mode live acceptance was reported by the user on 2026-09-07; no transcript
or provider/model was supplied for that check. See [the improvement plan](IMPROVEMENT_PLAN.md).
These accepted runs are not a measured cross-model success rate. Exact-tag
release qualification remains separate from candidate checks.

## Historical candidate evidence

The following records preserve qualification at the time of each candidate.
Older candidates were not all separately accepted; their pending live gates are
superseded by the integrated acceptance above.

Kanban28's `internal/agent/command_recovery_test.go` runs actual loopback HTTP
and native shell tools through `Agent.Run`: a failing HTTP health check,
shell diagnosis, directory repair, one deliberate retry, a file write, fresh
verification and final answer. Developer/Work Standard and primary graph
fixtures assert zero terminal errors, zero completion interventions and exactly
two HTTP requests (no hidden automatic retry). Recovery/restart tests distinguish
normal shell exits, including network/publication labels, from interrupted
commands and failed opaque external tools. These are scripted harness tests,
not a measured live-model success rate. The internal Work/graph fixture does
not enable that public combination. Candidate `v0.4.4-recovery-reliability.4`
passes the full offline suite, agent/tools/session/goalgraph/TUI/CLI race checks,
vet, six-platform builds/checksums and native Developer/Work real-PTY startup,
resize, status and quit checks. Only expected context-bar snapshot changes
needed updating. Live acceptance is pending in
[the improvement plan](IMPROVEMENT_PLAN.md).

Kanban27's offline regressions in `internal/agent/bookkeeping_completion_test.go`
exercise native note tools, writes, a failed check, repair, fresh verification,
and a final stale-note conflict through `Agent.Run`. They assert termination
with zero completion interventions, zero terminal errors and no extra model
requests in Developer/Work Standard and graph-controller fixtures. Legacy-state
migration, metadata progress churn, unfinished plans, stale evidence, invalid
recovery proofs and persistence failures have counterexamples. This tests the
completion contract, not a live-model success rate. The internal Work/graph
fixture does not enable that public combination. Candidate
`v0.4.4-recovery-reliability.3` passes the full offline suite, agent/tools/session/
goalgraph/TUI/CLI race checks, vet, six-platform builds/checksums and native
Developer/Work real-PTY startup, resize, status and quit smoke checks. Live
acceptance is pending.

Kanban26 has native regressions in `internal/agent/verification_command_test.go`
and `internal/tools/command_test.go`: a successful piped frontend build is
converted into a direct check preserving cwd, `$PWD`, exports and quoted spaces;
its scoped receipt completes Standard Developer/Work work. High non-signal exits
(including npm's 254) allow repair across restart and a recorded successful
alternative can complete. Pipes and PTYs preserve native exit classification;
interruption counterexamples remain uncertain. These are offline harness tests,
not live-provider quality measurements. Candidate
`v0.4.4-recovery-reliability.2` passes the full offline suite, agent/tools/shell/
TUI/CLI race checks, vet, six-platform builds/checksums and native real-PTY
startup/resize/status/quit checks. User acceptance remains pending.

Kanban25 has an offline native-tool regression in
`internal/agent/dependency_recovery_test.go`: malformed patch rejection, ordinary
dependency-command failure, read-back, repair, retry and verification complete
without terminal errors or completion interventions. It covers Developer/Work
Standard execution, graph-controller execution and Standard restart; it does
not make Work/Orchestrated Goal a supported public combination. File argument
and shell classification tests include intentional empty replacements, atomic
batch rejection and retained interruption guards. The original dependency
allowlist and network-command exclusions are superseded by Kanban28 above. These tests prove the correction path, not a measured
live-model success rate. Acceptance is tracked in the improvement plan.

Kanban23 has offline reproductions for a real PTY whose reader stops consuming
output, bounded renderer teardown, cancellation with a full UI queue, and partial
plan updates retaining seven steps. See `internal/tui/output*_test.go`,
`cmd/collo/shutdown_test.go` and `internal/plan/update_test.go`. These establish
the harness behavior under those failures, not a fix to a third-party terminal
bridge or a live-model quality improvement.

The first terminal-output candidate exposed a startup regression that the
stopped-reader test did not cover. `TestGuardedTerminalDeliversStartupAndResize`
now runs the actual TUI on a sized PTY and relies on Bubble Tea's terminal
detection for both initial and changed dimensions. It failed on the incomplete
output wrapper and passes with the complete `term.File` contract. Developer and
Work smoke checks of candidate `.2` also passed startup, resize, `/status` and
`/quit` using temporary configuration/workspaces and no live model calls.

Kanban22 response-limit recovery is covered offline by
`internal/agent/response_limit_test.go` and the updated termination/graph tests.
They check bounded continuation across modes, no execution of rejected tool
calls, charged usage, cancellation and extendable resource stops. This is
regression evidence, not a live-model quality score; acceptance remains tracked
in the improvement plan.

The September 6 orchestration reliability maintenance has separate offline
regressions in `internal/goalgraph/continuation_test.go`,
`internal/agent/worker_budget_test.go`, and
`internal/app/graph_extension_test.go`. They cover repeated budget grants,
provider-request leases including compaction, and saved-session continuation.
Runtime discovery and PATH preservation also have native-tool and application
tests across Developer, Work, planning and graph execution. These passing tests
do not constitute a live model quality score; user acceptance is tracked in the
[improvement plan](IMPROVEMENT_PLAN.md#kanban21-reliability-candidate--september-6).

The receipt grader accepts both native artifact validation and Standard
`scoped_verification.files` evidence and compares the recorded digest with final
bytes. This does not grade browser behavior or calculation methodology by itself;
the independent task checks and human rubric still apply. Existing saved
baselines remain historical; a matched live rerun is needed to measure this
completion change's effect on quality, interventions, latency, and cost.

W5 adds an opt-in scorecard for the production Standard agent loop. The initial
`standard-balanced-v1` suite has six Developer and six Work tasks. Its prompts,
synthetic inputs, and independent checks are versioned in
[`internal/quality/suite.go`](../internal/quality/suite.go); every run records a
digest of the selected definitions. The ordinary Go tests stay offline.

This measures a controlled tool configuration, not every feature of a user's
installed setup. It uses built-in file, command, plan, artifact-validation, and
clarification tools. User hooks, MCP servers, skills, project instructions,
agent profiles, and graph/delegate execution are not loaded. The provider
configuration is loaded through ordinary trust and credential resolution.
Provider keys and headers are never written into result metadata.

## Start with two tasks

List tasks without making model requests:

```sh
collo eval list
```

Choose an existing configured provider name. `--model` is optional when that
provider already has a model selected. The output directory must be new:

```sh
collo eval run --live --provider YOUR_PROVIDER --model YOUR_MODEL \
  --tasks code_boundary,work_totals --output dist/quality-smoke
```

Only `run --live` makes provider requests. `list`, `report`, `compare`, `review`,
and `schema eval` do not. This command uses the selected model and can incur
provider charges. It does not modify the provider's saved settings.

Each trial gets a fresh synthetic workspace inside its result directory.
Command execution requires an available OS sandbox with read/write confinement
and full network denial; it fails closed when those protections are unavailable.
Model API requests run outside that command sandbox. Go tasks require a local
Go 1.26+ toolchain; a trusted compilation preflight runs before any model call.
Go checks use a workspace-local cache and `GOTOOLCHAIN=local`, with no dependency
downloads. Existing configured hooks or connected services are not exercised.

## Tasks and interpretation

| Task | Profile | What the checks establish |
| --- | --- | --- |
| `code_boundary` | Developer | Independent Go tests exercise the age-18 boundary and nearby values. |
| `code_normalize` | Developer | Independent Go tests exercise trimming, case normalization, and empty input. |
| `code_config` | Developer | Parsed JSON exactly matches requested edits while preserving other settings. |
| `code_review` | Developer | Required references appear and files stay unchanged; review quality needs human judgment. |
| `required_input` | Developer | A required missing file remains absent and the task reports blocked. |
| `optional_input` | Developer | The summary covers the available input without inventing the optional file. |
| `work_totals` | Work | Parsed output has the correct net revenue and row count, with a current artifact receipt. |
| `work_sources` | Work | The memo includes source references and key values, with a receipt; claim/citation accuracy needs human review. |
| `work_deliverable` | Work | Final bytes match AFTER and a receipt matches those bytes; review the requested tool order and scratch roles. |
| `work_large_input` | Work | The answer includes the late-file marker and EOF; review the read offset in the trace. |
| `work_correction` | Work | A second-turn correction reaches the final memo; this does not force compaction or restart. |
| `work_handoff` | Work | The agent continues a supplied handoff and produces the correct result; this does not test runtime restart. |

Protected fixture inputs are checked for changes after execution. Only the
task's named editable inputs are exempt; additional files are allowed except
in the read-only review task. Independent Go
tests are supplied by the grader after the agent returns and run through the
same required command sandbox, not an unrestricted host shell.

A passing fixture is not proof of factual quality, broad autonomy, or SOTA
performance. Each task includes a human-review rubric. In particular, keyword
checks on prose are deliberately narrow and do not replace reading the output.
W5 acceptance means the runner and scorecard are useful and honest; it does not
require a model to pass every task.

Batch state `complete` means every scheduled trial was recorded, including
budget-exhausted or failed trials. The table's task-pass verdict requires both
the expected runtime outcome and passing independent checks; the per-task
checklist can therefore pass while the overall task fails to finish.

## Budgets and repeated trials

Defaults are one trial, 40,000 cumulative input-plus-output tokens per task,
480,000 batch tokens, 180 seconds of agent time per task, and 16 iterations per
turn. A task with two prompts shares one cumulative token/cost/time allowance.
Independent grading can add up to 60 seconds per task and is excluded from the
reported agent duration. The Go preflight can add up to 65 seconds once per run.

Use identical limits for before/after comparisons:

```sh
collo eval run --live --provider YOUR_PROVIDER --model YOUR_MODEL \
  --trials 2 --token-budget 40000 --total-token-budget 960000 \
  --timeout 180 --max-iterations 16 --output dist/quality-baseline
```

The runner reserves enough remaining batch budget for a complete task allowance
before starting it. If less remains, it stops and retains the partial scorecard.
The agent also bounds each request based on estimated input and reported usage;
provider tokenization differences can overshoot these estimates. A response
without usage stops the agent before its proposed tools or another model
request, then stops the batch. A provider failure may leave incomplete usage;
the scorecard does not call that zero spend. Adapter-internal HTTP retries are
not separate logical provider-call counts, and billing cannot be reconciled to
an invoice from these traces alone.

Optional `--cost-budget 0.25` caps estimated spend per task and requires positive
configured input/output pricing. This is an estimate, not a provider billing
limit. Without pricing, cost is unavailable. The maximum planned batch cost
allowance is the per-task cap times the number of scheduled trials; the token
and time bounds still apply. Cancelling retains completed rows and any available
current-trial trace. A stopped run can be inspected but is not resumed in place;
start a fresh directory to rerun it.

## Inspect and review

Each run writes `results.json` and `report.md`. Each task/trial directory contains
`workspace/`, `events.jsonl`, and `messages.jsonl`. Event traces use the existing
event-v1 shape; messages retain model tool arguments for inspection. Logs are
bounded to 16 MiB per task and scrub configured secrets from string values.
Trace-write failures stop execution. Keep traces local and inspect before sharing.

```sh
collo eval report dist/quality-baseline
collo replay --check dist/quality-baseline/code_boundary-01/events.jsonl
collo schema eval
```

The result schema is [results-v1.schema.json](../internal/quality/results-v1.schema.json).
Machine passes, false-done results, and possible false blockers are separate
from human acceptance. A machine pass requires every check and the expected
runtime outcome. False done means the runtime returned `done` despite a failed
check or an expected blocker. A possible false blocker means checks passed for
an expected-done task but the runtime returned `blocked`. These narrow flags
cannot detect every mistaken prose claim; human review can reject a machine
pass. False blockers are candidates for review and may reflect
policy or infrastructure constraints. Tool-call repetition counts exact
canonical arguments; retries and revalidation can legitimately repeat them.
Ask-user counts cover the typed clarification tool. Prose questions require
human inspection. Controller interventions and permission denials are observed
counts, not judgments about whether they were necessary.

Record an explicit review after the run finishes or stops:

```sh
collo eval review dist/quality-baseline --task work_sources --trial 1 \
  --accepted true --note "Checked the source dates, observed 75%, and future 90% target" \
  --unnecessary-questions 0 --wasted-repeats 0
```

Review flags annotate observed work; they never execute tools. Repeating a
review replaces that row's current decision and timestamp. Unreviewed values
remain unknown. Cost per accepted trial is shown only when all attempted trials
have acceptance decisions and complete cost estimates, including failed-attempt
cost in the numerator.

## Compare a later build

Run the same command into a new directory, then:

```sh
collo eval compare dist/quality-baseline dist/quality-after
```

Comparison requires two completed batches with identical suite/task digests,
task order, trial count, provider/model, recorded model-setting digest, platform,
and budgets. It reports paired machine outcomes, time, and token differences.
It does not claim statistical significance from a few trials. Different backend
deployments behind the same provider/model name or unrecorded infrastructure
changes can still affect results. Model comparisons should be separate analyses,
not silently mixed into a before/after harness comparison.
