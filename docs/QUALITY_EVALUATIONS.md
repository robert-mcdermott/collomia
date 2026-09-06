# Real-model quality evaluations

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
