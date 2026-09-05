# Work mode

**Status:** implemented in the current development tree · **Profile name:**
`work` · **Default:** `developer`

Work mode expands Collomia from an agent that assumes a software repository
and code outcome into a general-purpose local agent for research, analysis,
automation, knowledge retrieval, document authoring, external actions, direct
questions, and other tasks whose result may be an artifact or an observed
outcome rather than source code.

The name is deliberately **Work**, not Assistant. “Assistant” describes the
agent's personality and says little about the task contract. Work is a task
profile with concrete evidence behavior, and it can coexist with planning,
permission autonomy, and—after a future non-Git state contract—other execution
strategies.

## Product model

Task profile and execution strategy are separate axes:

| Axis | Choices | What it controls |
| --- | --- | --- |
| Task profile | Developer, Work | Assumed outcome, preferred tools, and completion evidence. |
| Execution strategy | Standard, Orchestrated Goal | Who owns scheduling, attempts, recovery, and aggregate bounds. |
| Turn state | Plan, Execute | Whether mutation and commands are allowed in the current turn. |
| Permission autonomy | ask, workspace, autopilot | Which otherwise-permitted actions require user approval. |

Developer remains the backward-compatible default. Selecting Work changes
none of the permission, sandbox, trust, hook, secret-redaction, or audit
boundaries.

The initial Work release uses Standard execution only. Orchestrated Goal and
write-capable delegates still depend on Git state tokens and isolated Git
worktrees, so Work refuses those combinations instead of silently applying a
repository contract to a non-repository folder. Read-only tools and ordinary
Standard execution do not require Git.

## User requirements

- A directory used for Work may be an ordinary folder with no `.git`
  directory. Collomia must not initialize Git, require a commit, or treat the
  lack of repository metadata as a task failure.
- The profile is selected with `--mode work` or `/mode work`, is visible in
  status surfaces, and is stored in session metadata. Resume, session switching,
  and `/new` preserve the session's choice. Legacy sessions load as Developer.
- `/mode developer` restores the software-oriented profile without changing
  provider, model, conversation, usage, planning state, or permission autonomy.
- Ordinary Q&A that performs no mutation and has no unresolved tool failure
  completes directly. It must not manufacture a plan, artifact, test, or
  verification ritual merely to satisfy the profile.
- Multi-step work keeps the existing structured plan contract. `done` steps
  require observed evidence; `blocked` is reserved for a genuine impasse;
  `skipped` records an unnecessary or superseded action.
- Failed tool calls named by the completion controller carry stable
  current-turn IDs. A successful retry with the same tool and complete arguments
  clears that operation automatically, without a recovery-only plan update.
  A different file, command, or argument does not clear it just because the tool
  name matches. `update_plan.resolved_failures` binds each remaining relevant ID to
  a terminal step and, for a retry or alternative, the exact successful tool
  call that recovered it. A skipped unnecessary attempt and a genuine blocker
  remain distinct structured dispositions; plan prose alone cannot erase a
  failure.
- An interrupted or ambiguous external mutation is never retried blindly.
  Collomia first reads back the destination when possible or asks the user,
  preventing a duplicate submission from being mistaken for recovery.

## Evidence contract

The provider must also report a usable completed answer or tool request.
Truncation, filtering/refusal, and incomplete/failed output stop with retained
partial text and usage, without executing that response's proposed tools or
claiming `done`. This is shared runtime behavior, including direct Q&A; no
synthetic plan is required to detect it. Rejected responses are not automatically
continued. Existing workspace changes remain for inspection and deliberate
continuation.

Evidence is matched to the requested outcome rather than forced through a
software test framework:

| Outcome | Preferred evidence |
| --- | --- |
| Text, Markdown, JSON, CSV/TSV, DOCX, PPTX, PDF, or other file | `validate_artifact` after the final write, bound to the exact path and SHA-256 digest. |
| Analysis or data work | Identified inputs plus reproducible calculations, reconciliations, invariants, and data-quality caveats. |
| Research or retrieval | Sources actually consulted, citations where available, explicit separation of sourced fact from inference, and material uncertainty. |
| External action | Returned receipt/identifier and a safe read-back or typed postcondition when the service provides one. |
| Direct answer with no side effect | Grounded answer; no synthetic mutation or completion ceremony. |

`validate_artifact` is intentionally narrow and bounded to 64 MiB. It proves
that a regular non-empty file exists at a particular digest and performs the
following format checks:

- valid UTF-8 and basic structure for text and Markdown;
- a single parseable JSON value;
- parseable CSV or TSV records;
- required Open XML package parts and parseable XML for DOCX and PPTX, with
  inspectable text extraction;
- PDF header and end marker presence;
- size and digest only for an unknown binary format.

Callers may require exact text in formats whose content can be inspected. A
successful validation emits a typed `artifact_validated` receipt on the
`tool.result` event. Validation is path-specific: writing one artifact cannot
invalidate or satisfy another. At completion, the runtime rehashes validated
deliverables and checks the original path target and parent identity. Script
mutations, deletion, external edits, and retargeted symlinks cannot leave a
stale receipt accepted. Rewriting identical bytes at the same target preserves
the receipt. If a completion attempt still has dirty tracked
artifacts, the controller lists only those remaining paths relative to the
workspace and omits paths with accepted current receipts, so remediation does
not repeat validation of an already-cleared deliverable. Mutations whose tools
did not report paths remain an explicit unknown-path gap. Conventional
build/lint/test commands remain evidence when Work produces code, but do not
replace a file deliverable's artifact receipt.

For file-producing tasks, the optional `artifacts` field in `update_plan`
serves as a small task brief:

```json
"artifacts": [
  {"path": "report.md", "role": "deliverable"},
  {"path": "analysis.sh", "role": "scratch"}
]
```

Declare requested outputs, including files created by shell commands, and keep
the declarations in subsequent complete plan updates. Declared deliverables
require current successful `validate_artifact` receipts even if no file-write
tool observed their creation. Scratch files do not need deliverable acceptance.
Without a declaration, tracked writes retain the previous validation behavior;
validating an unclassified file makes it an implicit deliverable for this turn.
A declared deliverable cannot be silently removed or demoted during the turn.
Roles express model-authored intent, grant no permissions, and do not prove that
every user-requested output was identified. Direct Q&A needs no task brief.

Execution effects are tracked separately from permission risk. Known file
operations identify possible affected paths, including partial failures;
commands and opaque tools have unknown effect scope. Unknown scope does not
mean a command failed or every file changed: final digest checks determine
whether registered artifacts remain current. This is not a workspace-wide
mutation scanner. Failed opaque executions include a read-back/uncertainty
notice. External receipt IDs and command success do not prove remote state;
use safe read-back where available, and disclose uncertainty or block when it
cannot be resolved. No external action is automatically replayed by this gate.

Structural validation does **not** establish factual correctness, source
quality, accessibility, visual polish, or fitness for a human decision. When
no meaningful machine validation applies, a fresh `validation_note` records
what was checked and what remains subjective. It is visibly labelled as
model-authored disclosure, not runtime proof.
It cannot waive a missing declared deliverable or a stale digest receipt.

When the controller intercepts a completed answer solely for unresolved
tool-failure bookkeeping, Collomia retains that answer. A valid metadata-only
resolution releases the original response without another provider request;
substantive follow-up tools invalidate reuse so the final answer can reflect
their results. Repeated intervention counting follows the unchanged gap, so
unrelated tool activity neither clears a failure nor purchases more retries.

## Durable and automation contracts

- Session metadata adds optional `task_mode`; omission means Developer.
- Session plans and `update_plan` accept optional `artifacts` entries with
  `path` and `role` (`deliverable` or `scratch`). Older plans remain readable.
  Roles persist; runtime digest receipts are current-turn evidence and are not
  trusted across restart. The first resumed turn of an open plan must validate
  its deliverables again. Completed historical plans do not gate unrelated Q&A.
- Headless `run.result` adds optional `mode` (`developer` or `work`).
- A successful evidence-producing tool result may add `tool.evidence` with a
  narrow `kind`, `subject`, optional `digest`, and optional `detail`.
- Tracked file tools and `/undo` emit `file.change` path-manifest events. These
  are observations for audit/recovery consumers; replay never performs them.
- Schema-v1 consumers must continue tolerating these additive fields.

## Initial boundaries and future work

- Work mode is a task profile, not a promise that Collomia has a connector for
  every external service. Available tools, MCP servers, and skills determine
  which actions are possible.
- Structural validation is not a full Office/PDF renderer. Format-specific
  skills or tools should render and visually inspect artifacts when layout is
  part of acceptance.
- A shell command may modify files that do not appear in a statically known
  path list. The same conservative completion and permission rules continue to
  apply, but a complete command-side file manifest is future work.
- Work plus Orchestrated Goal requires a future non-Git workspace-state token,
  non-Git isolated-write substrate, and recovery/publication contract. It must
  not be enabled by weakening the existing Git-backed authority boundary.

## Acceptance evidence

The shipped slice is guarded by unit and credential-free product evaluations
covering:

- Developer defaulting, parsing, prompts, and session persistence;
- CLI, TUI, resume, new-session inheritance, and explicit mode switching;
- refusal of the unsupported Work/Orchestrated combination;
- non-Git direct Q&A without ceremony;
- non-Git Markdown production completed by one path-bound artifact validation,
  with a typed digest receipt and no controller retry;
- stale/path-specific artifact evidence and macOS path-alias normalization;
- Markdown, JSON, CSV, DOCX, PPTX, PDF, binary bounds, and malformed artifacts;
- additive event/schema fields and durable file-change manifests.
