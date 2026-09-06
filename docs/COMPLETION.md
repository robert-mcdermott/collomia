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

For an application, scope source trees instead of enumerating every file:

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
`.ruff_cache`, `.uv-cache`, `.npm`, `.cache`, `dist`, `build`,
`target`, `coverage`, `.next`, and `.nuxt`.
Scope an excluded output explicitly when it is a requested deliverable.
A source check can generate a bundle in `dist` without making its own source
receipt stale. It does not validate that bundle independently. A check that
rewrites included source inputs must still be rerun after the final source change.

Passing tree checks cover retained directory declarations and their included
descendants, including obligations saved by older Collo versions. Fresh project
checks supersede older receipts for covered inputs. Excluded outputs and unrelated
paths retain their own obligations.

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

The runtime automatically resolves:

- Successful retries of the same executed operation. Command timeout and
  verification metadata changes do not change execution identity; the command,
  PTY behavior, and other execution arguments still match. Fresh evidence
  requirements remain separate.
- A successful corrected call of the same tool after native argument/preflight
  assessment rejected an earlier call before execution. Permission and hook
  denials are separate and do not qualify.
- A native scoped-check preflight rejection followed by a successful scoped
  replacement with the same stated purpose covering all originally requested
  paths. This only clears an attempt that never executed, not a failed assertion,
  a permission/hook denial, or an uncertain action. The bounded identity survives
  resume; a fresh passing receipt is still required.
- Corrected native artifact checks on the same file that preserve or strengthen
  the original format, minimum-size, and required-text checks.
- An executed native file-edit failure after a successful native replacement or
  edit covers **every affected path**, followed by current file verification.
  Validation alone cannot erase a failed edit to unchanged old contents.

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

## What a final answer should say

Describe the result and the checks actually run. For example: “Created the
game; browser checks passed for loading, movement, shooting, and restart.” If
only text was inspected, say so and identify browser testing as outstanding.
Receipts substantiate the checks; they never certify all acceptance criteria.
