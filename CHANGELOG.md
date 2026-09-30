# Changelog

This file starts at v0.3.0. Earlier releases are not backfilled, because
reconstructing them after the fact would produce a plausible account rather than
an accurate one; their history is in the Git log and in
[docs/ROADMAP_HISTORY.md](docs/ROADMAP_HISTORY.md).

## Unreleased

### Added

- **Setup shows both token limits and lets you change them.** After
  verification, a Token limits screen shows the context window and max output
  with where each came from. You can accept either or type your own; typed
  values are labelled `configured`, and thousands separators are accepted.
  - When nothing established a value, the field opens empty and focused rather
    than 32768/8192 being written silently. A blank answer accepts the stated
    assumption, which stays labelled "assumed".
  - Pairs the loader would refuse are refused on the screen, in the same words.
  - Re-running setup on the same model opens on the file's values, with the
    detected ones beside them. A different model starts from its own detected
    limits.
  - `l` on the confirmation returns to the screen.
- **More limits are discovered.**
  - Anthropic's model catalog (`max_input_tokens` and `max_tokens`).
  - vLLM's `max_model_len`, with LoRA adapters inheriting their base model's
    window.
  - For Ollama, the context the loaded model is actually serving (`/api/ps`),
    which setup prefers over the trained maximum. When the runtime serves less
    than the model supports, the screen says so and names
    `OLLAMA_CONTEXT_LENGTH`.
  - The built-in table now covers gpt-oss, Llama 4, and Gemini.

- **Per-model settings.** A provider's `models` map holds `max_tokens`,
  `context_window`, `reasoning`, and `pricing` for individual models, each
  overriding the provider-level field for that model only.
  - They apply at startup, with `--model`, on `/model`, and when an agent
    profile selects a model. An agent profile's reasoning still wins.
  - Setup maintains them. Changing a provider's model moves the old model's
    limits into its own entry, so `/model` back to it keeps them.
  - `collo doctor` lists models with their own settings, and
    `collo config validate` checks every entry.
  - Existing files load unchanged.

- **Claude keeps its reasoning across tool calls, and shows it.** This applies
  on the Anthropic, Azure Foundry-Claude, and Bedrock-Claude routes.
  - Each response's signed thinking blocks are replayed verbatim to the
    endpoint or region that issued them, in their original order around the
    text and tool calls, so the model continues its reasoning after each tool
    call instead of starting again.
  - Claude models that think by default are asked for summarized display, so
    their thinking is visible. Before this, Claude 5.x showed none.
  - While such a provider is selected, the live plan is recorded into the
    conversation once per change, because Anthropic rejects replayed thinking
    if anything before it changed. Compaction drops replayed thinking, and
    recorded plan snapshots are hidden from the transcript, prompt history,
    and rewind labels.
  - Safety net: a refused replay or display request is retried without it,
    warns once, and is not attempted again for the session, so the worst case
    is the previous behavior.
  - Verified live on Bedrock with `us.anthropic.claude-opus-5-5`, with and
    without an active plan. Other providers are unchanged.
- **Thinking survives reopening a session.** Each response's readable thinking
  is kept with the answer it preceded (64 KiB per response), so a resumed or
  reopened session shows it collapsed above that answer. Kept thinking is
  display text only and is never sent back to a provider.
  - The synchronous fallbacks extract thinking too: OpenAI-compatible
    `reasoning_content`/`reasoning`, Anthropic `thinking` blocks, Responses
    reasoning summaries, and Bedrock `reasoningContent`.
  - Sessions no longer store thinking as one record per streamed chunk.
  - `/models` now reports reasoning effort, readable summaries, and continuity
    between requests as separate capabilities. Continuity is unsupported on
    every adapter today.
- **A menu for each configured provider in `/providers`.** It offers these
  actions:
  - Re-verify.
  - Make default.
  - Switch model without re-verifying.
  - Add a verified model as its own entry, without changing the provider's
    model.
  - Edit the connection (with re-verification, keeping settings).
  - Edit temperature and headers (credential-looking values are never shown).
  - Rename (a stored key moves with it).
  - Remove (refused for the default and the active provider).

  After a provider rejects `max_tokens` and states its ceiling, the session
  offers to save it with `/providers save-ceiling`. `collo setup --provider`
  keeps its direct re-verification.
- **`/providers` runs setup inside a session.** Add a provider, or change one's
  model, limits, or effort, without leaving the conversation (alias `/setup`,
  or `/providers <name>`).
  - The change is saved to your user configuration and applied from the next
    turn, without a restart.
  - Only providers and defaults are reloaded; permissions and other settings
    are untouched.
  - The session says whether it switched, kept another model, or was
    overridden by a project configuration.
  - It is unavailable while a turn is running, and ctrl+c closes the flow, not
    Collomia.
- **Reasoning effort in setup and in the session.**
  - After the token limits, setup offers the efforts the chosen model accepts.
    The levels come from Anthropic's catalog, Ollama's advertised thinking
    levels, or a published OpenAI/Anthropic/gpt-oss table.
  - A chosen level is checked with one request, and a refusal is shown rather
    than saved. The choice is written to that model's own entry.
  - "Model default" writes nothing, and a model without effort control is not
    asked. `e` on the confirmation returns to the screen.
  - `/effort [level|default|reset]` shows the effective effort, which setting
    decided it, and the accepted levels, or changes it for the rest of the
    session. It outranks configuration and agent profiles until
    `/effort reset` and is never saved.
  - The effort vocabulary adds `none` and `minimal`, matching OpenAI's
    current values.

### Fixed

- **`/model` no longer runs a model with another model's limits.** Switching
  models within a provider kept the context window and output cap written for
  the provider's own model, so a small model could inherit a million-token
  window and never compact.
  - A model without its own limits now uses what the endpoint's catalog
    reported for it, then its published limits.
  - The inherited value is used only when nothing is known about the model.
  - The `/model` picker now reports each model's own window.
- **Re-running setup no longer deletes provider settings it never asked
  about.**
  - Reconfiguring a provider used to rewrite its whole entry, silently dropping
    `headers`, `temperature`, `reasoning`, `pricing`, timeouts, and unknown
    keys. It also replaced a `${VAR}` reference in `base_url` with its expanded
    value.
  - Setup now updates only the fields it verifies, keeps everything else, and
    names the kept settings on the confirmation.
  - A name that now points at a different endpoint is still replaced whole, so
    headers that may carry a gateway credential never follow it to a new host.
    The confirmation names what is dropped.

### Changed

- **The Go baseline moves from 1.26.6 to 1.26.8, with a dependency refresh.**
  `govulncheck` reported no reachable findings before or after; the refresh
  clears 17 unreachable advisories in `golang.org/x/crypto` and
  `golang.org/x/net` (one remaining `x/crypto` advisory has no upstream fix).
  Updated direct dependencies include the AWS and Azure SDKs, chroma, and the
  terminal width libraries. CI and release builds consume the version from
  `go.mod`. The MCP Go SDK stays at v1.6.1: v1.7.0 and later negotiate MCP
  protocol `2026-07-28` and fail the elicitation and conformance tests, so
  adopting that revision is tracked as its own roadmap item.

### Release process

- The release workflow rejects a tag whose source commit lacks a verified
  GitHub signature before qualification begins, and the
  [release guide](docs/RELEASING.md) requires that check before tagging. The
  published v0.5.1 release is unchanged.

## v0.5.1

The v0.5.0 tag failed Windows release qualification and was never published.
v0.5.1 is the first published 0.5 release: it contains everything listed under
v0.5.0 below plus these fixes.

### Fixed

- **Unix PTY commands no longer lose their final output.** The terminal master
  was closed after the command exited but before the output reader finished,
  discarding unread bytes. Output is now drained before close; if a descendant
  keeps the terminal open, the drain is limited to five seconds, remains
  cancellable, and reports incomplete output rather than presenting the
  captured prefix as the full result.
- **Windows sandbox grants no longer overwrite each other.** Concurrent
  AppContainer shims read, merged, and wrote ACLs on shared SDK and temp
  directories without coordination, so one workspace's grant could erase
  another's. A per-user named mutex now serializes only the ACL update;
  commands still run concurrently, and a 30-second lock wait fails closed. See
  [Security](docs/SECURITY.md).

### Release process

- PR, main, and release workflows share one composite qualification action,
  so release builds inherit CI's test timeouts, serialized Windows race-test
  packages, and fuzz smoke settings.

## v0.5.0

### Added

- Live, collapsible thinking summaries for reasoning events emitted by provider
  adapters. Provider-side enablement and reasoning display in reopened sessions
  remain separate follow-up work.
- Retained task notes, original-request previews and session evidence retrieval
  across compaction/resume. See [Task context](docs/TASK_CONTEXT.md).
- Durable Standard completion obligations, bounded workspace checkpoints and
  `/recovery` inspection/reconciliation. Interrupted actions require inspection;
  external effects cannot be rolled back or automatically replayed. See
  [Recovery](docs/RECOVERY.md).
- Opt-in `collo eval` with 12 balanced coding/Work tasks, contained workspaces,
  budgets, traces, independent checks, human review and matched comparisons.
  An initial 24-trial baseline is recorded separately from prose-quality review.
  See [Quality evaluations](docs/QUALITY_EVALUATIONS.md).
- Task-specific `run_command.verification` in Standard Developer and Work.
  Supported plain project builds automatically capture input scope when explicit
  verification metadata is absent. Explicit scopes remain authoritative, and
  passing checks certify only their recorded scope. See [Completion](docs/COMPLETION.md).
- General-purpose local PNG/JPEG/GIF inspection with `view_image`, and bounded
  XLSX/Open XML structural validation. Pixel delivery depends on model support;
  structural checks do not prove calculation or visual correctness.
- Separate total-turn and no-progress limits, `--max-turns`, `--max-no-progress`
  and live `/limits` controls. Standard execution defaults to 256 total turns.

### Fixed

- Standard completion distinguishes requested deliverables from disposable
  helpers, accepts scoped project evidence, and preserves receipts across
  unrelated edits. Final bytes, target identity and input freshness remain
  checked. HTML/source validation checks text rather than claiming execution.
- Verification-only gaps report “Verification incomplete”; final success text
  is held until accepted. Diagnostics identify uncovered paths and narrower
  receipts, and routine verification details are collapsed in the TUI.
- Observed ordinary native shell exits allow diagnosis and repair without manual
  recovery acknowledgement. Actual failed work still needs resolution;
  interruptions and opaque external-tool failures retain uncertainty guards.
- Housekeeping errors and native input rejected before execution no longer
  create task-failure obligations. Malformed file replacements are rejected
  before mutation, including patch-batch preflight.
- Recovery uses exact operation arguments or supported alternative evidence.
  Verification guidance preserves working directories and environment context;
  non-signal exit statuses such as npm's 254 are classified correctly.
- Output/context limits allow up to two bounded continuations per turn; incomplete
  tool calls are discarded without execution. Persistent limits pause resumably.
  Empty completed responses receive bounded retries. Refused or failed responses
  cannot become successful completion, and incomplete compaction cannot replace
  conversation context.
- Native command execution inherits the caller's PATH through a non-login shell;
  executable discovery and Orchestrated Goal scheduling, wall accounting and
  explicit budget continuation are repaired.
- Partial plan updates preserve omitted steps. Terminal output stalls and UI
  teardown are bounded; real-PTY startup and resize regressions are covered.
  Context reporting distinguishes current input occupancy from cumulative usage.
- `read_file` pagination reaches beyond the first MiB with bounded pages,
  EOF/continuation metadata and guidance for oversized lines.
- Evaluation failure traces include classification/correlation metadata, and
  scorecards distinguish task passes from individual artifact checks.

### Documentation and acceptance

- Work remains general-purpose: no bundled reporting workflow or example-project
  initializer is required. Tools and user-installed skills support model-selected
  methods. The proposed prescriptive workflow was withdrawn.
- Updated usage, completion, recovery and evaluation guides, and consolidated
  current release status separately from historical candidate records.
- Standard Developer acceptance was verified through Kanban30 on September 6;
  Work acceptance was reported by the user on September 7. Orchestrated Goal's
  continuation follow-up passed manual testing through Kanban21. These results
  do not establish universal model/platform reliability; exact-tag release CI
  remains required. See the [improvement plan](docs/IMPROVEMENT_PLAN.md).

## v0.4.1

### Fixed

- **The minimum Go toolchain now includes the current standard-library
  security fixes.** The build baseline moves from Go 1.26.5 to Go 1.26.6,
  resolving six reachable `govulncheck` findings in `net/url`, `crypto/tls`,
  `net/http`, `encoding/xml`, and `encoding/asn1`. CI and release builds both
  consume the version from `go.mod`, so the quality gate and shipped binaries
  now use the corrected standard library.

- **Windows AppContainer commands can execute junction-backed toolchains.**
  GitHub Actions exposes Go on a `C:` directory junction whose bytes live on
  `D:`. The sandbox correctly granted the resolved SDK target but left the
  child `PATH` on the inaccessible alias, so `cmd.exe` reported that `go` did
  not exist and every command-backed evaluation failed downstream. The
  AppContainer shim now resolves absolute `PATH` entries before launch, making
  executable lookup use the same spelling as the existing read-only ACL
  without granting any additional root.

- **Work completion notices now name the artifact that is actually still
  outstanding.** A transcript changed an analysis script and a Markdown
  report, successfully validated the report, then received only the generic
  warning that "one or more artifacts" still needed evidence. Because the
  notice hid the remaining script path, the model revalidated the already
  accepted report and spent both interventions before a validation note closed
  the gap. Work notices now list a bounded, deterministic set of
  workspace-relative paths still in the dirty ledger, explicitly omit paths
  with current accepted receipts, and preserve a separate fail-closed warning
  when a mutating tool did not report paths.

- **Developer and Work completion recovery no longer loops on web-result
  provenance IDs.** A real Developer transcript copied an opaque
  `COLLOMIA_EXTERNAL_WEB_DATA` marker into `recovery_tool_call_id` instead of
  the successful `web_fetch` call's provider-envelope ID. The completion
  notice now lists bounded, exact successful current-turn receipt candidates,
  distinguishes those IDs from identifiers printed inside tool output, and
  points a mistaken marker back to the actual successful call. Changing one
  invalid guessed ID into another no longer resets the two-intervention bound;
  the allowance renews only when the number of real completion gaps reaches a
  new low. A metadata-only correction still releases the already-generated
  answer without another provider call.

## v0.4.0

### Added

- **Work mode makes non-software outcomes first-class.** `--mode work` and
  `/mode work` select a persisted task profile for direct Q&A, research,
  analysis, automation, knowledge retrieval, document/data artifacts, and
  governed external actions in ordinary non-Git folders. Developer remains the
  default, and switching profiles changes neither provider nor permissions.
  Work matches evidence to the result instead of demanding a development test:
  the new `validate_artifact` tool emits a typed path- and SHA-256-bound receipt
  for bounded text, Markdown, JSON, CSV/TSV, DOCX, PPTX, PDF, and binary
  structure/content checks; calculations, sources, external receipts/read-back,
  and an explicitly model-authored `validation_note` cover other outcomes.
  Structural validation does not claim factual correctness or visual polish.
  The initial profile uses Standard execution and refuses Orchestrated Goal and
  write-capable delegation while their state/isolation contracts remain
  Git-backed. See [the Work contract](docs/WORK_MODE.md).

- **`/orchestrate done` ends a goal that has finished.** A terminal graph stays
  attached so it remains inspectable, which also means it keeps owning the
  session until something releases it. Until now the only command that did so
  was `/orchestrate cancel`, which on a terminal graph had always archived
  rather than cancelled — so finishing successfully and abandoning work in
  flight shared one word, and the word was the wrong one. `/orchestrate done`
  (also spelled `release`) returns the session to Standard mode and deletes
  nothing: the transcript, evidence, and graph snapshots stay in the session
  log.

  Mostly you will not type it. A graph that finished with nothing left to run —
  `done`, or one you cancelled — is released by your next ordinary prompt,
  which then runs. `/orchestrate cancel` on a terminal graph still releases it,
  so nothing that worked before stops working.

  Release is narrower than cancellation on purpose. It refuses a running graph
  (`/orchestrate pause` or `/orchestrate cancel`), refuses one stopped at
  `awaiting_review` or `awaiting_verification` because each is holding a
  decision that is yours to make, and still refuses any graph that is the only
  record of a worktree nobody has reconciled.

### Fixed

- **Work no longer reports a completed answer as blocked because an abandoned
  tool call used a different recovery mechanism.** Failed calls now carry
  controller-visible IDs, and `update_plan.resolved_failures` binds a retry or
  alternative to its exact successful tool-call receipt while retaining
  distinct unnecessary-skipped and genuinely-blocked dispositions. The
  controller no longer guesses cross-tool recovery from permission-risk
  labels, counts interventions against unchanged gaps, and reuses an answer
  intercepted solely for metadata repair instead of paying the provider to
  repeat it. Sandboxed `uv` failures now receive the concrete
  `UV_CACHE_DIR="$PWD/.uv-cache"` recovery form, and file-tool errors explain
  why command access to `/tmp` does not grant `edit_file` access there.

- **Standard mode no longer burns its remediation attempts guessing why a
  passing check did not count.** Once the completion controller has named a
  verification gap, an ineligible heredoc, shell compound, or unrecognized
  check explains the exact reason in its tool result and points to detected
  direct verifiers or a fresh `verification_note`. A recognized verifier emits
  a positive receipt for the current tracked-write state. If verification is
  the only remaining gap after both bounded interventions, the result is now
  `needs_verification` instead of the false claim `blocked`.
- **Productive Standard turns are no longer cut off at cycle 24.**
  `max_iterations` now measures consecutive provider cycles without novel
  progress in Standard mode, matching its Orchestrated Goal meaning. Repeated
  equivalent evidence still exhausts the lease, and a hard envelope at twice
  the configured value bounds continuous write churn.

- **A finished run could report itself as blocked.** The completion
  controller's notice offered one way to record an unfinished step — mark it
  `blocked` — and any blocked step ends the turn as blocked. A run that built
  and verified its deliverable therefore came back as a failure because the
  agent had abandoned a side attempt (a reference it turned out not to need, a
  tool call it replaced with a better one) and had only that word to record it
  with. The notice and the system prompt now distinguish `skipped` — the action
  proved unnecessary or was accomplished another way — from `blocked`, which is
  for work that genuinely cannot be completed, and say that blocked ends the
  turn.
- **A skill's own reference files could not be read.** `load_skill` tells the
  model that a skill's references are read with `read_file` and lists their
  paths, but the read was denied for being outside the workspace. An active
  skill's directory is now readable by `read_file`, `list_files`, and
  `search_files` regardless of `allow_outside_workspace`. Reads only: writes
  still resolve against the workspace, so no tool can modify a skill bundle;
  symlinks are contained against the resolved path; and disabled or
  untrusted-project skills are excluded, so the project-trust quarantine is
  unchanged.
- **Orchestrated Goal could not verify a Node project that had no
  `package.json`.** `node` was missing from the verification recognizer, so
  `node --test` and `node tests/smoke.js` were not accepted as proof — while
  the proposal contract requires the first mutating node to create a focused
  test exactly when a project has no test surface. A node could therefore be
  required to write a test and then refused every way of running it, blocking
  with its own passing suite recorded in evidence. Node's entry points are now
  recognized, by entry point rather than by interpreter: `node --test` and a
  script in a conventional test location qualify, `node index.js` and
  `node -e "..."` do not.
- **A passing command the recognizer does not cover is no longer silent.**
  While a node is waiting on verification, a declined command's tool result now
  names it and gives either the project's detected verification commands or, if
  the project has no recognized manifest, what declaring a test entry point
  would achieve. The blocker names it too, instead of reporting that no
  verification exists directly beneath a check you watched pass.

### Changed

- Terminal Orchestrated Goal messages name the commands that apply to them. A
  completed graph pointed at `/new`, which ends the whole session, instead of
  the command that ends the goal; blocked and budget-exhausted graphs named no
  exit at all. They now name `/orchestrate status`, `retry`, `extend`, and the
  release as applicable.

## v0.3.0

A minor rather than a patch release. Most of it is one new opt-in mode, but one
change reaches people who never touch that mode — see **Changed** first if you
are deciding whether to upgrade.

### Changed

- **Delegate integration is now authorized against the same resolved path the
  write tools are judged against.** This affects `/agents apply` and the
  delegate integration tool, not only the new mode. On a workspace reached
  through a symlink — which on macOS includes anything under `/tmp` or `/var`,
  and generally any symlinked checkout — a scoped `deny` rule written in the
  resolved form the configuration documents stopped `write_file` but did not
  match at integration. Publishing a delegate's candidate was therefore a way
  around a rule that had already been obeyed.

  If you have such a rule *and* a symlinked workspace, an integration that
  previously succeeded is now refused, and the refusal names the rule. That is
  the defect being fixed. On a workspace with no symlink in its path, nothing
  changes.
- The capability matrix reports Orchestrated Goal as two rows rather than one —
  end-to-end graphs with governed read fan-out as implemented, isolated-writer
  candidate waves as experimental. A consumer keying on the previous single row
  title will not find it.

### Added

- **Orchestrated Goal**, an opt-in TUI-only execution mode in which the model
  proposes a bounded dependency graph and the runtime owns readiness, attempts,
  evidence freshness, recovery, budgets, and the terminal outcome. Standard mode
  remains the default and always will be; this is selected per session, never
  entered on your behalf.

  Two shapes, with different maturity:
  - *End-to-end graphs with governed read fan-out* — supported. At most two
    read-only workers run for independently ready nodes before the serial
    primary lane.
  - *Isolated-writer candidate waves* — still experimental. Writers work in
    separate Git worktrees, each candidate is verified in its own tree, and the
    run stops with your workspace unchanged until you publish a candidate
    yourself.

  New commands: `/orchestrate [goal | approve | status [node] | pause | resume |
  retry <node> | extend | integrate <node> | verify | waive <reason> |
  reconcile | discard <node> [confirm] | cancel]`.
- `/restore integration [<id> [keep]]` — inspect an integration that never
  recorded an outcome, put the prior bytes back, or record that you are keeping
  the workspace as it stands.
- **Evidence-gated goal completion** in Standard mode: a tool-free response is
  checked against the active plan, terminal-step evidence, successful
  conventional verification after tracked writes, and unresolved tool failures.
- Guidance on when to reach for Orchestrated Goal and when not to, in the user
  guide. Every case it names cites the evaluation that measured it.
- First launch continues into a verified session rather than ending at setup.

### Fixed

Most of these are ways a run could have told you something untrue about its own
result. They are listed because a completion message is the thing people act on.

- A graph that had integrated a candidate could not be reopened after a
  restart: two evidence statuses were missing from the snapshot validator, so
  resuming or archiving such a session reported it as structurally false. Any
  session left in that state can now be read again with no action from you.
- A completed graph no longer claims every required node passed its acceptance
  gates when that is not what happened. It now says so when a revision retired
  an unfinished node, when nodes finished on your written waiver rather than
  machine-observed verification, and when an earlier node's passing checks were
  superseded by later work that changed the workspace.
- A candidate wave that could not take every approved node reports which ones it
  never started, says they are waiting on your review rather than blocked, and
  warns that releasing the graph would abandon them. It previously reported
  itself as finished.
- A node blocked by a failing candidate names the check that failed rather than
  reporting an exit code, and distinguishes a failed check from absent
  verification and from verification not bound to one settled state.
- A candidate rejected because the parent workspace or its Git base moved names
  which of the two changed, and says the candidate survived and where it is
  retained — it previously read as though the work had been lost.
- Integration refused because its retained worktree no longer exists says so,
  rather than reporting the wording used for a genuine path mismatch.
- An integration, combined verification, or waiver is refused while an earlier
  publication into the workspace never recorded an outcome, naming the
  checkpoint and both ways to resolve it.

### Notes

- Orchestrated Goal persists under graph schema 1 with additive fields only.
  Sessions written by earlier versions load unchanged.
- The isolated-writer wave runs the repository's detected verification set once
  per candidate worktree and again over the combined result — three rounds
  against one. It suits a change you would rather not have land if it turns out
  wrong, and suits work whose steps touch the same files badly. The user guide
  covers this.
