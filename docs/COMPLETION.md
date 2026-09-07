# Evidence-based completion

Standard execution is Collo's permanent default. The model chooses a method and
appropriate checks; the runtime records actual tool outcomes and checks the
applicable completion requirements. A model saying “done” cannot manufacture a
passing test or make stale evidence current. Developer and Work use this same
principle. [Orchestrated Goal](ORCHESTRATION_STRATEGY.md) additionally owns graph
readiness, attempts, scheduling, recovery, budgets, and terminal transitions.

## Choose evidence that fits the task

For development, use the project's relevant build, lint, and test commands.
For an HTML game, a browser check can test loading and interactions; reading
HTML as text cannot establish that JavaScript executes or gameplay works.
For analysis, run calculation assertions or reconciliations. For research,
cite the sources actually consulted and distinguish conclusions from facts.
Skills and installed tools can supply domain-specific methods. There is no
required Work template, example project, reporting toolkit, or bundled browser.

Ordinary users can describe the desired result. The following is the tool
interface the agent uses, not a required prompt format or CLI command:

```json
{
  "command": "node check-game.mjs",
  "verification": {
    "paths": ["asteroids.html"],
    "purpose": "Check game loading, controls, scoring, and restart in a browser"
  }
}
```

The check script must actually implement those assertions and exit nonzero on
failure. Its description is intent, not proof. Use an existing check when one
fits, or create a small task-specific script. Browser checks need an available
browser automation tool/runtime; Collo does not install one automatically.

In **Standard execution**, `run_command.verification` accepts 1–16 existing
file or project-directory paths and a purpose of 1–512 bytes. Regular files
are limited to 64 MiB each. File scope is
checked through the normal file-read and command permission paths. The runtime
reads each authorized file's digest before the command and after a successful
exit. Only matching bytes, path targets, and parent identities yield a scoped
receipt. Checks that modify their own scoped outputs must be separated into a
generation step followed by a check of the final files.

Standard Developer and Work now capture project-input evidence automatically
for plain `npm run build`, `pnpm build`/`pnpm run build`, `yarn build`/`yarn run
build`, `bun run build`, `go build ./...`, and `cargo build` when `verification`
is omitted and the matching manifest exists in the command's directory. Literal
workspace-contained `cd ... &&` prefixes are supported. Flags, selectors,
wrappers, environment prefixes, pipelines and other preparation require explicit
scope. Explicit verification fields are honored unchanged.

Scope is established before execution and passes the same file guard, command
permissions, child-read permissions and hooks as a supplied scope. Source,
configuration, manifests and lockfiles participate in freshness; common generated
and dependency directories do not. No prior command is retroactively converted
into evidence. A directory declaration with only narrower receipts now names the
current scopes and requests the whole directory; it does not guess at unread
files or claim a build failed. Malformed verification metadata and native shell
syntax rejected before execution are correction feedback, not failed task actions.
Actual command failures, filesystem errors, permissions and changed inputs remain
enforced. Orchestrated Goal's verification authority is unchanged.

For other application checks, scope source trees instead of enumerating every file:

```json
{
  "command": "uv run pytest",
  "verification": {
    "paths": ["app", "tests", "pyproject.toml"],
    "purpose": "Run backend regression tests"
  }
}
```

A separate frontend build can scope `frontend`. Directory scopes fingerprint
file contents, file permissions, directory membership, and symlink destinations.
They detect edits, additions, deletions, and directory replacement. Descendant
input files pass command read policy and hooks before hashing. Nested symlinks
are recorded without following their targets or claiming those targets as covered.
Each tree is bounded to 10,000 entries and 256 MiB total (64 MiB per file).

Directory checks exclude these **directories by name**:
`.git`, `.hg`, `.svn`, `.collomia`, `.collomia-tmp`, `node_modules`,
`.venv`, `venv`, `__pycache__`, `.pytest_cache`, `.mypy_cache`,
`.ruff_cache`, `.uv-cache`, `.npm-cache`, `.npm`, `.cache`, `dist`, `build`,
`target`, `coverage`, `.next`, and `.nuxt`.
Scope an excluded output explicitly when it is a requested deliverable.
A source check can generate a bundle in `dist` without making its own source
receipt stale. It does not validate that bundle independently. A check that
rewrites included source inputs must still be rerun after the final source change.

Passing tree checks cover retained directory declarations and their included
descendants, including obligations saved by older Collo versions. Fresh project
checks supersede older receipts for covered inputs. Excluded outputs and unrelated
paths retain their own obligations.

For a nested project, use `cd frontend && npm run build`; literal directory
changes that remain inside the workspace are accepted. Scope paths are still
relative to the workspace (for example `frontend/src`, not `src`). Outside,
dynamic and unresolved directory targets cannot establish workspace evidence.
When suggesting a direct retry of a piped check, Collo preserves directory and
environment setup and quoted arguments. It does not extract a later command
across a semicolon or prescribe replaying effectful preparation. Complex setup
can be put in a check script with the correct exit behavior.

Run the check directly. Shell forms that can hide its status, such as
`check || true`, pipelines, or a trailing command, are refused as explicit
verification before execution. Quoted multiline arguments and literal shell
punctuation within quotes are accepted for Standard scoped checks. Put complex
assertions in a script. The script's
own logic still determines what a zero exit means; the harness cannot prove
that an arbitrary test is comprehensive or even useful.

The event records `scoped_verification`, the command, the proposed purpose,
and a `files` map of paths to SHA-256 digests (directory entries contain
project-input snapshot digests). `execution` and `file_freshness`
are `passed`; `coverage` is `not_assessed`. These are point-in-time observations,
not file locks, full-workspace snapshots, or a guarantee of semantic correctness.
Include relevant inputs in the scope when their freshness matters.

## File completion without duplicate checks

Standard Developer and Work accept either a current scoped command receipt or a current
`validate_artifact` receipt for a file deliverable. Do not run both merely to
close the same file gap. `validate_artifact` remains useful for format parsing,
size, and required-content checks; HTML/source text checks do not execute code.

Scoped command receipts cover listed files or included project inputs; artifact
receipts cover individual files. They cannot
clear unrelated project changes. Disposable helpers are excluded as described below.
Conventional unscoped Developer build/lint/test recognition retains its existing
tracked-write semantics. An unscoped command does not satisfy Work's final-file
gate. Scoped commands do not substitute for Orchestrated Goal's detected checks
or combined-parent verification; its authority and acceptance rules are unchanged.

Multi-step Standard tasks in either mode can declare deliverables and scratch files in
`update_plan.artifacts`. Simple tasks need no artificial plan solely to obtain
file evidence: successful validation makes an unclassified file an implicit
deliverable. A later plan cannot remove a retained deliverable to evade a gap.
Scratch helpers do not require deliverable acceptance. Scope is model-selected
intent and cannot prove that every requested output was identified.

Use the workspace's `.collomia-tmp/` directory for disposable analysis, generation,
and verification helpers. The agent chooses this location automatically from its
instructions; users need not mention it in their prompts. Helpers elsewhere can
be declared `scratch` in the plan in either mode. Requested scripts and reusable
application code belong in their appropriate project locations and still need
relevant verification. No automatic deletion or Git ignore change is performed.

Scratch files do not independently create completion obligations. An explicit
`deliverable` declaration overrides the scratch directory convention, and a
retained deliverable cannot be demoted to evade verification. Canonical path
checks prevent a symlink inside scratch from hiding a write outside it. A failing
check, denied action, or uncertain execution remains tracked regardless of its
file location. The harness cannot infer that every script is temporary or that
all model-authored role declarations reflect user intent.

The runtime rechecks receipt freshness at completion. Edits, deletions, path
retargeting, and replaced parent directories require fresh evidence. Restart
retains obligations and bounded historical recovery facts; it never restores
fresh validation or process-local identities.
An unrelated successful command, a changed plan, or a prose claim cannot satisfy
a missing declared deliverable. A specific validation/verification note remains
a disclosed judgment for eligible work with no meaningful machine check; it
cannot waive a missing or stale declared file receipt. Verification-only gaps
end as `needs_verification`, not successful verification.

## Recovery without unnecessary bookkeeping

Native file-tool argument errors are returned for correction before execution.
Missing `content`/`new_text`, contradictory patch fields and rejected patch
preconditions do not become failed-action obligations in Standard or Orchestrated
Goal execution. They cannot change files, grant permission, mark a plan complete
or substitute for required verification. Deliberate empty strings are accepted.

Every observed ordinary nonzero native `run_command` exit allows diagnosis,
repair and a deliberate retry, including commands containing HTTP calls or
publication operations. Execution has finished; its failure and possibly partial
local/remote effects still need assessment. No automatic replay occurs and each
next action retains normal permission checks. The failed operation remains
unresolved until recovered. Interruptions, timeouts, signal outcomes and failed
external/MCP tools with unknown effects retain uncertainty protection.
See [Standard recovery](RECOVERY.md).

Planning, working-note and session-history tools are **housekeeping**, not task
outcomes: `update_plan`, `detect_verification`, `update_task_context`,
`read_task_context`, `read_session`, and `search_session`. Their tool errors
remain feedback but never become task-failure obligations. A malformed note or
stale note revision cannot invalidate a finished application. Successful notes
likewise cannot prove a task passed. Repeated notes/searches do not renew the
progress allowance. Actual unfinished plan steps, stale file receipts, failed
task operations, uncertain effects and persistence failures remain enforced.

The runtime automatically resolves:

- Successful retries of the same executed operation. Command timeout and
  verification metadata changes do not change execution identity; the command,
  PTY behavior, and other execution arguments still match. Fresh evidence
  requirements remain separate.
- A successful corrected call of the same tool after native argument/preflight
  assessment rejected an earlier call before execution. Permission and hook
  denials are separate and do not qualify.
- A retained native scoped-check preflight failure (including legacy sessions)
  followed by a successful scoped
  replacement with the same stated purpose covering all originally requested
  paths. This only clears an attempt that never executed, not a failed assertion,
  a permission/hook denial, or an uncertain action. The bounded identity survives
  resume; a fresh passing receipt is still required.
- Corrected native artifact checks on the same file that preserve or strengthen
  the original format, minimum-size, and required-text checks.
- An executed native file-edit failure after a successful native replacement or
  edit covers **every affected path**, followed by current file verification.
  Validation alone cannot erase a failed edit to unchanged old contents.

For an explicit recovery, both `recovered_by_retry` and
`recovered_by_alternative` require a later successful non-metadata receipt,
a terminal step and stated recovery intent. A changed command is an alternative
regardless of the supplied label; that label mismatch alone does not block
completion. This does not make an unrelated success automatic recovery.

Up to 64 successful tool-call facts survive a budget pause, provider interruption,
or restart. These retain bounded IDs, operation hashes, tool names, risk, and
summaries, so a later explicit recovery can refer to a real earlier success.
Recovery ordering is retained: an earlier pass cannot clear a later failure,
and a reused failed call ID cannot revive its old success. These facts do not
restore permission, replay any action, or attest current file bytes.
New turns still obtain fresh checks for retained deliverables. Older sessions
without these facts require a fresh successful alternative; IDs found only
inside transcript text are not promoted to runtime receipts.

Other semantic alternatives still use `update_plan.resolved_failures` with
observed recovery evidence. An unrelated pass cannot erase a failed required
check. Permission and hook denials, opaque command effects, and failed external
actions are not treated as verified file repairs. Ordinary local nonzero command
exits allow diagnosis and repair; interrupted or ambiguous actions retain the
[recovery safeguards](RECOVERY.md). Nothing here grants permission or authorizes
replaying an uncertain external action.

Routine completion notices are collapsed as “Checking remaining work.” Expand
with `ctrl+o`; details remain in the transcript and event log. When no failed
calls remain, notices omit failure-resolution instructions. Real blocked
outcomes remain visible. Verification-only gaps appear as **Verification
incomplete**, not **Blocked**, and list remaining paths rather than claiming that
files changed after a check when they were simply outside its scope. Standard
final-answer text is held until the controller accepts completion; reasoning and
tool events still stream. Candidate responses remain in the durable transcript
for diagnosis, so an older replay may show those drafts.

## Long tasks and provider interruptions

Standard uses two independent response-cycle limits:

- `options.max_iterations`: consecutive cycles without novel progress, default 24.
- `options.max_turn_iterations`: total provider responses per user turn, default
  256; 0 uses the default and values above 10,000 are rejected.

A response may contain several tool calls. Productive work renews the no-progress
lease but never the total ceiling. `--max-turns 500` and
`--max-no-progress 24` override startup settings. In the TUI, `/limits` displays
effective limits; `/limits 500` changes the total and `/limits 500 30` changes both,
including during a running turn. Runtime overrides last until a profile switch
or restart; edit configuration for persistent defaults. Agent profiles can
override the no-progress value. Orchestrated Goal and delegated-task budgets
retain their existing controls; these flags do not extend their authority.
Token and cost budgets still apply. After a budget pause, send another message
to continue the saved task.

A Standard response with a completed stop status but no answer or tool payload
gets at most two retries. Every retry counts toward iteration/token/cost budgets.
Completed tools are not replayed. Refusals, truncation, unknown statuses, and
partial streamed tool payloads do not use this retry path. Persistent empty
responses appear as **Provider response unavailable**, with work retained and
a suggestion to check the provider/proxy or switch providers before continuing.
The machine-readable result remains a provider failure; an unusable response
is never reported as task success.

Output/context-limit responses have a separate bounded continuation path across
Developer, Work, planning and graph execution. At most two additional requests
per turn ask for a smaller next step; successful responses do not refill this
allowance. Usage and iterations count normally. The runtime discards rejected
tool calls instead of executing or persisting them as pending work. Persistent
limits display **Paused at model response limit** and return `budget_exhausted`,
retaining work for a normal Standard continuation or `/orchestrate extend` in a
graph. Graph workers propagate the same resource-stop status. No model setting
is widened, no rejected response completes a task, and no incomplete compaction
summary replaces the original context.

## What a final answer should say

Describe the result and the checks actually run. For example: “Created the
game; browser checks passed for loading, movement, shooting, and restart.” If
only text was inspected, say so and identify browser testing as outstanding.
Receipts substantiate the checks; they never certify all acceptance criteria.
