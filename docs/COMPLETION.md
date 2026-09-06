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
regular files, at most 64 MiB each, and a purpose of 1–512 bytes. File scope is
checked through the normal file-read and command permission paths. The runtime
reads each authorized file's digest before the command and after a successful
exit. Only matching bytes, path targets, and parent identities yield a scoped
receipt. Checks that modify their own scoped outputs must be separated into a
generation step followed by a check of the final files.

Run the check directly. Shell forms that can hide its status, such as
`check || true`, pipelines, or a trailing command, are refused as explicit
verification before execution. Quoted multiline arguments and literal shell
punctuation within quotes are accepted for Standard scoped checks. Put complex
assertions in a script. The script's
own logic still determines what a zero exit means; the harness cannot prove
that an arbitrary test is comprehensive or even useful.

The event records `scoped_verification`, the command, the proposed purpose,
and a `files` map of paths to SHA-256 digests. `execution` and `file_freshness`
are `passed`; `coverage` is `not_assessed`. These are point-in-time observations,
not file locks, full-workspace snapshots, or a guarantee of semantic correctness.
Include relevant inputs in the scope when their freshness matters.

## File completion without duplicate checks

Standard Developer and Work accept either a current scoped command receipt or a current
`validate_artifact` receipt for a file deliverable. Do not run both merely to
close the same file gap. `validate_artifact` remains useful for format parsing,
size, and required-content checks; HTML/source text checks do not execute code.

Scoped command and artifact receipts cover only their listed files and cannot
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
retains obligations, never passing receipts or process-local identities.
An unrelated successful command, a changed plan, or a prose claim cannot satisfy
a missing declared deliverable. A specific validation/verification note remains
a disclosed judgment for eligible work with no meaningful machine check; it
cannot waive a missing or stale declared file receipt. Verification-only gaps
end as `needs_verification`, not successful verification.

## Recovery without unnecessary bookkeeping

The runtime automatically resolves:

- Successful retries of the same tool operation. Command timeout changes alone
  do not create a different operation; command, PTY behavior, and scope still match.
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

## What a final answer should say

Describe the result and the checks actually run. For example: “Created the
game; browser checks passed for loading, movement, shooting, and restart.” If
only text was inspected, say so and identify browser testing as outstanding.
Receipts substantiate the checks; they never certify all acceptance criteria.
