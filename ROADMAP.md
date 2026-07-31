# Collomia Roadmap

**Status updated:** 2026-07-30

This document is the current product plan: what remains, why it matters, and
the dependency order. The detailed dated implementation record has moved to
[`docs/ROADMAP_HISTORY.md`](docs/ROADMAP_HISTORY.md). Completed work is
summarized here only when it affects the next decision.

## Product direction

Collomia is a cross-platform, provider-neutral terminal coding agent built
around explicit trust, enforceable permissions, durable recovery, structured
tool use, and a polished terminal experience.

The guiding principle remains:

> Make Collomia safe and recoverable before making it more autonomous.

Priorities:

- **P0 — foundation or safety gate:** required before safe unattended use can
  be advertised.
- **P1 — competitive core:** required for a strong daily-driver release.
- **P2 — differentiation:** ecosystem, scale, and advanced workflows.
- **P3 — expansion:** collaboration and hosted services after the local
  product is dependable.

## Current state

Phases 0–3 are P0-complete. Substantial production slices of phases 4–8 have
also shipped:

- layered validated configuration, project trust, diagnostics, stable events,
  and a generated capability matrix;
- macOS Seatbelt, Linux Landlock, and Windows 11 AppContainer/Job Object
  sandbox backends with compatibility-first `auto` enforcement by default,
  visible degradation, and capability-aware fail-closed `require` behavior;
- scoped permissions, conservative shell analysis, catastrophic-command
  denials, credential-store protection, publication/deployment protection,
  operation-scoped policy rules, macOS per-host brokered command egress, secret
  redaction, and an audit ledger;
- durable resumable sessions, crash recovery, compaction, bounded retained
  artifacts, rewind/fork, coupled conversation-plus-workspace checkpoint
  restore that fails closed on external edits, and fail-stop persistence
  handling;
- atomic patching, tracked diffs, hunk review, undo, Git inspection, planning,
  verification, LSP diagnostics/definitions/references/formatting, repository
  indexing, PTY commands, and background processes;
- normalized provider capabilities, streaming, retries, health, contracts,
  Azure Entra refresh, Bedrock SigV4/bearer authentication, optional macOS/
  Windows keychain credential storage, opt-in provider-safe reasoning controls,
  user-priced cost estimates, and image input;
- MCP lifecycle/resources/prompts/progress/elicitation, safe live catalogs,
  external-data framing, skills, and hooks;
- built-in configuration-free web search and page retrieval, confined to the
  public internet by a connect-time address guard and framed as external data;
- concurrent governed delegated agents with isolated Git worktrees, durable
  status, steering/cancellation, overlap-aware write-scope scheduling,
  freshness-bound three-way integration, and opt-in primary-reviewed
  publication;
- a responsive themed TUI with a growing composer, an optional context rail,
  per-tool-call status, transcript/activity/diff views, optional mouse control
  that can be handed back to the terminal mid-session, a loopback browser
  terminal, shell completion, notifications, and stable headless JSONL;
- deterministic replay/evaluations, cross-platform golden screens, bounded
  fuzzing, support bundles, verified installers, SBOMs, provenance, and gated
  draft releases.

Collomia is suitable for beta use with the documented limits. It should not
claim 1.0 or fully safe unattended execution until the remaining P0 security
and reliability gates are complete. Those now live entirely in Phase 8 —
independent review, sustained adversarial campaigns, and the reliability
campaigns — since the last P0 outside it was reclassified on the evidence that
the enforced all-or-nothing network boundary it was meant to add already
existed on all three platforms. The audit ledger those campaigns and that
review read from is now itself complete, attributable, bounded, and
inspectable, and pseudo-terminal execution reaches all three platforms. The
most recent wave closed the last known asymmetry in the risk classifier: the
safety taxonomy described destruction only, so publishing and deploying rode
along with autopilot while their deletion counterparts required a decision.

No wave is currently active. See
[Recommended next sequence](#recommended-next-sequence) for what the dependency
order argues for next.

## Completed wave — the actions that leave the machine

**Goal:** give the risk classifier a publication shape to match the destruction
shape it already had, and give the rule language enough resolution to say yes to
`npm install` and no to `npm publish` — so the control is one people leave on
rather than one they switch off.

- [x] Classify publication at all. `internal/shell/safety.go` was, end to end, a
  taxonomy of deletion, so under `autopilot` on a stock configuration
  `terraform destroy` required a decision and `terraform apply -auto-approve`
  did not; `kubectl delete` did and `kubectl apply` did not; `git push --force`
  did and `git push origin main` did not. `npm publish`, `cargo publish`,
  `docker push`, `gh pr create`, `gh release create`,
  `aws lambda update-function-code`, and `ssh prod "systemctl restart app"` were
  all approved silently. The asymmetry did not track reversibility — a published
  version is harder to take back than a deployment a controller recreates — and
  this roadmap's own deferred list already said these were not meant to be
  autonomous.
- [x] Fix the rule language first, because a tier with no expressible exception
  is a tier people disable. `rules[].command` was an *executable-name* glob
  while `denied_commands` in the same block was a full-command-line regex, so
  `{"action":"deny","command":"npm publish"}` matched nothing and validated
  clean — the third inert-matcher defect after the `host` matcher and the
  hand-built action in `collo policy check`. A pattern containing a space now
  matches an **operation**, one definition (`policy.NamesOperation`) decides
  which form a pattern is, and a pattern that could match neither fails
  validation.
- [x] Print the vocabulary rather than making people guess it. `collo policy
  check` gained an `operations:` line, because the discoverability failure is
  what made the inert rule dangerous rather than merely useless.
- [x] Model the setting on `protect_credentials`, not on tier 2. Tier 2 offers
  no durable answer at all, which is right for `npm publish` and wrong for
  `git push` on a feature branch; a control that is wrong half the time gets
  switched off. `permissions.publication` (off/prompt/deny, default prompt) is
  uncoverable by autopilot, a tool-wide "always", or an executable-only allow
  rule, while a rule naming the operation and one narrow session grant scoped to
  the exact operation both work.
- [x] Stay quiet during ordinary work. Read verbs (`gh pr view`, `kubectl get`,
  `terraform plan`, `aws s3 ls`), `npm install`, `docker pull`, a
  download-direction `rsync`, and every `--dry-run` rehearsal are unaffected —
  and `--dry-run=false` is an explicit request to act, so it is not.
- [x] Find the classifier's own blind spots by widening the probe. `ssh` passed
  because the publication check sat behind the operation lookup and ssh has no
  subcommand, which silently exempted every verbless tool. And the subcommand
  reader skipped unrecognized options without stopping, so
  `aws lambda update-function-code --function-name f` named its operation
  `aws lambda f` and `gh api -X POST` named it `gh api post` — plausible strings
  that matched nothing, which is why neither failed loudly.
- [x] State the rule/grant asymmetry instead of leaving it inferred. An
  operation-naming rule outranks `deny` because it is written down and survives
  review; an interactive grant never does. The credential gate had behaved this
  way since it shipped without saying so; a test written against the opposite
  assumption is what surfaced it.
- [x] Carry it on the preset ladder, clamp it monotonically, and report it in
  `collo doctor`, `collo policy check`, and the Session tab, with its own header
  in the approval dialog.
- [x] Test the property where it actually lives. Alongside the unit coverage, a
  symmetry test fails when a tool gains a destructive classification without its
  publishing counterpart, and three offline evaluations run a real autopilot
  turn — a unit test proves the string is recognized, not that the mode whose
  purpose is not asking actually stops.

**Behavior change:** two. Publishing, deploying, and pushing prompt by default,
including under `autopilot`, where a headless run fails closed;
`"publication": "off"` restores the earlier behavior exactly. And a `command`
rule pattern containing a space now matches an operation where it previously
matched nothing, so an upgrade may activate a denial its author believed was
already in force. See the
[compatibility note](docs/COMPATIBILITY.md#publication-protection).

**What it is not.** A policy layer, not enforcement — it reads what a command's
text says it will do, so a build script that uploads without naming the
operation is invisible to it, and it cannot tell `kubectl apply` against a local
cluster from the same command against production. The catalogue of publishing
tools is finite; `denied_commands` and `reviewer_command` remain the answer for
anything specific to one organization.

## Completed wave — a pseudo-terminal on the third platform

**Goal:** stop one missing backend from withholding two advertised capabilities
from a first-class platform.

- [x] Add `internal/conpty`, a shared Windows pseudoconsole implementation.
  Both `run_command` with `pty: true` and `collo --web` needed the same
  primitive, and the part that is easy to get wrong is handle lifetime rather
  than the API calls. Two copies would each have had to rediscover that the
  parent's copy of the console's output handle must be closed before anyone
  reads from it — otherwise the pipe still has a writer and a finished command
  never reaches end-of-file, which presents as "the PTY command hangs after it
  finishes".
- [x] Build the process here rather than on `os/exec`.
  `syscall.SysProcAttr` on Windows exposes no proc-thread attribute list, and a
  pseudoconsole is attached only through `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE`
  on a `STARTUPINFOEX`, so `os/exec` structurally cannot do it. Quoting still
  goes through the same `exec.LookPath` and `syscall.EscapeArg` path `os/exec`
  uses: a command quoted one way without a PTY and another way with one would
  be a far worse surprise than either quoting rule on its own.
- [x] Strengthen the cancellation contract instead of matching it. The child is
  created suspended, joined to a job object, and only then resumed, so there is
  no instant in which it could spawn a descendant outside the job — a window
  the ordinary `taskkill /T` path does have. The existing descendant walk is
  reused to wait for the kernel to finish the teardown, because returning
  earlier leaves processes holding the workspace directory open.
- [x] Say what Windows cannot do rather than implying parity. There is no
  SIGTERM: `GenerateConsoleCtrlEvent` requires the sender to share the target's
  console, which a pseudoconsole host by definition does not. The graceful step
  for an interactive session is closing the child's console input, documented
  as a request with a deadline rather than as an equivalent of the Unix path.
- [x] Un-skip the two PTY tests instead of leaving them skipped. They said
  "pty unsupported on windows", which was about to become false; the only
  genuinely platform-specific part was the shell, so they now run everywhere
  with a per-platform probe. Windows has no `test -t 0`, so the terminal
  assertion is that a pseudoconsole renders its client through a virtual
  terminal and a captured pipe does not — which is the difference programs
  actually key on when they decide whether to colorize.
- [x] Cover the mechanics with tests that fail informatively. A hang rather
  than a wrong value is the expected failure for handle-ordering mistakes, so
  the close and terminate tests carry explicit timeouts whose messages name the
  ordering error that would cause them.

**Behavior change:** `pty: true` is no longer refused on Windows and
`collo --web` no longer exits with a platform error there. Windows 10 1809 or
later is required for the pseudoconsole API; older releases report that rather
than running without terminal semantics.

## Completed wave — the record you can actually read

**Goal:** make the audit ledger the thing the README already claimed it was —
a complete, attributable, bounded, inspectable record of every privileged
action — because the independent review that gates 1.0 starts from that record
and it was the weakest durable stream in the project.

- [x] Stop losing entries silently. `Append` discarded every write error, so an
  unwritable ledger or a full disk produced a file that still read as
  complete — the worst possible failure for a record. A failure is now counted,
  reported to the session once (not once per authorized action, which a full
  disk would turn into a flood), and **declared in the file**: the next entry
  that reaches disk is preceded by a `gap` record naming how many entries were
  lost, since when, and why. The gap and the resumed entry are written as one
  buffer, because a gap marker that itself failed to persist would leave
  exactly the hole it exists to prevent.
- [x] Do not fail the turn over it. Refusing work the user already authorized
  because a record could not be filed is the wrong trade — which is why this is
  fail-visible rather than the session store's fail-stop. The distinction is
  now stated in `docs/SECURITY.md` instead of the blanket "best-effort
  diagnostic" that covered both the debug log and this.
- [x] Name who acted. Entries carried no session, agent, or task identity, so
  one workspace file receiving writes from the primary agent, several
  concurrently scheduled delegated agents, and any other `collo` process on the
  same directory interleaved into something no reader could separate. Every
  entry now carries the session id, the actor (`primary` or `agent:<profile>`),
  and the delegated task id. `collo audit --actor agent:reviewer` is the point
  of the field.
- [x] Stop dropping the child's ledger error. Both delegated-agent construction
  sites did `if ledger, err := audit.Open(...); err == nil`, so a child whose
  ledger could not be opened ran **completely unaudited** while the primary
  session's identical failure was reported. One `Agent.attachLedger` now owns
  redaction, the failure route, and identity together, and a source-scraping
  test fails on a third caller of `audit.Open` — the same guard shape that
  pinned single-site command-runner construction after the same defect.
- [x] Ship a reader, because nothing read the ledger back. `collo replay`
  excludes audit JSONL by documentation and support bundles exclude its
  content, so "reconstructable after the fact" meant locating
  `~/.collomia/audit/<name>-<hash>.jsonl` and parsing it by hand. `collo audit`
  filters by session, actor, tool, time window, and refusals, emits JSONL for
  external tooling, and prints a per-actor summary.
- [x] Lead with completeness, not with entries. `collo audit` states any
  declared gap, any unparsable line, and any generation discarded at rotation
  *before* it prints anything, because someone reconstructing an incident has
  to know the record has holes before drawing conclusions from what is in it.
  `collo doctor` gained the same check — it previously verified only that the
  directory could be created, which answers whether a ledger can be opened and
  not whether the one on disk is intact.
- [x] Bound the growth, and admit what the bound discards. The ledger grew
  forever while every other retained artifact in the project is bounded. It now
  rotates at 64 MiB keeping one previous generation, and the fresh file opens
  with a `rotation` entry that says when an older generation had to be removed —
  a record that quietly shortened itself would be the silent-hole defect again,
  in a slower form.
- [x] Latch the state where people look. A failure reported at startup has been
  dismissed by the time it matters, so the session latches its own count and the
  Session tab's Security block reports the record as recording or INCOMPLETE
  alongside the settings that produced it, marked like degraded sandboxing
  rather than as an ordinary row.
- [x] Test the package at all. `internal/audit` had **no test of its own** — one
  happy-path case written from `internal/permission` was the entire coverage,
  and the profile read 0.0%. It is now 83%, covering redaction of every field
  that can carry a secret, concurrent writers through separate handles,
  declared and detected incompleteness, rotation, filters, and the failure and
  recovery reports. The smoke test that followed found two more defects the
  unit tests had not: a zero `since` timestamp emitted on every ordinary entry
  because `omitempty` does not omit a zero `time.Time`, and ledger counts
  printed without pluralization.

**Behavior change:** none to permissions or execution. `collo audit` is new;
ledger entries gained additive `session`, `actor`, and `task` fields and two
new entry kinds (`gap`, `rotation`) that older readers can ignore; a ledger
larger than 64 MiB now rotates rather than growing without bound. See the
[compatibility note](docs/COMPATIBILITY.md).

## Completed wave — the bar that keeps its exit key

**Goal:** Stop the status bar from removing the user's way out in order to keep
showing session decoration on a narrow terminal.

- [x] Lay the bar out widest-first over tiered control hints instead of
  dropping the whole right-hand side when it does not fit. At 80 columns the
  bar previously showed no keyboard hints at all, and at 40 nothing but a
  truncated context gauge — so in a split pane there was no visible way to
  stop a running turn or answer a prompt. The controls now degrade through
  named forms down to a minimum that always survives.
- [x] Keep all three answers on an approval at every width. A prompt narrowed
  to show only how to approve is a prompt biased toward approving.
- [x] Make the left-hand badges droppable segments rather than one
  concatenated string. Two things on this bar are both promised to survive any
  width — the autonomy/containment mark and the exit key — and a string that
  gets truncated can only honor whichever one the ellipsis misses. Explicit
  drop ordering gives up the running spend before the model name.
- [x] Enforce "additive" by comparison rather than by a width guess. The named
  stance badge and the spend readout are laid out with and without, and kept
  only when the control hint lands on the same tier and no other badge was
  given up. The existing invariant test caught this: naming the stance had
  started costing the working indicator at 80 columns.
- [x] Pin the property with a test that sweeps every state across widths from
  40 to 200, rather than trusting golden screens. **The goldens had recorded
  the defect as correct** — `replay_chat_80x24` and `replay_chat_40x12` both
  captured a status bar with no controls on it. A golden proves the screen has
  not changed, not that it was ever right.

**Behavior change:** none configurable. Narrow terminals show fewer badges and
shorter control hints than before, and always show a way out.

## Completed wave — the running turn

**Goal:** Make the part of Collomia a user actually sits through — one turn,
eleven provider calls over the same growing prompt — cheap, fast, and
correctable, instead of expensive, slow, and all-or-nothing.

- [x] Stop reporting prompt-cache hits that were never requested. Both
  Anthropic adapters parsed `cache_read_input_tokens`, cost estimation priced
  it at `cached_input_per_million`, and the README advertised prompt caching as
  a tracked capability — but no request ever carried a `cache_control`
  breakpoint, and Anthropic caches only on explicit opt-in. The whole feature
  was display plumbing over a number that was structurally always zero. OpenAI
  caches implicitly above ~1024 tokens, which is why it went unnoticed.
- [x] Move the structured plan out of the system prompt. `update_plan`
  rewrote the system block, so the front of every request changed during
  exactly the multi-step work caching exists for. The plan now rides a trailing
  message regenerated per request and never retained in the conversation — the
  board stays the single source of truth, and no stale copy can accumulate in
  the history. This was the prerequisite, not a detail: a breakpoint in front
  of volatile content is invalidated by the agent's own progress tracking.
- [x] Send two breakpoints, not four. One on the system block, which also
  covers the tool definitions ahead of it in the prefix and is written once per
  session; one rolling breakpoint on the last non-volatile message, so the next
  call in the loop reads this one's history instead of paying for it again.
  `Message.Volatile` is what keeps the rolling breakpoint behind the trailing
  plan — a prefix that includes regenerated content is never read back, and a
  cache write costs more than ordinary input, so misplacing it is worse than
  not caching.
- [x] Normalize `Usage.InputTokens` to mean the whole prompt. Anthropic reports
  `input_tokens` net of both cache counters, so passing it through would have
  collapsed the context gauge to near-empty exactly when the context was
  fullest and priced most of the prompt at zero. Cache writes are tracked
  separately and priced by `cache_write_per_million`, because they are billed
  above the ordinary input rate rather than below it.
- [x] Take the five-minute cache lifetime deliberately rather than by default.
  The one-hour extension is requested through a beta header, and sending an
  unrecognized beta header to an arbitrary compatible endpoint is a
  compatibility risk taken for a saving nobody has measured yet.
- [x] Never lose a request to an optimization. Any 400 naming `cache_control`
  drops caching for the life of the client and retries once, so an endpoint
  that has not implemented caching costs one wasted round trip rather than one
  per call. Bedrock is deliberately untouched: `cachePoint` support varies by
  model *and* region and fails with a hard `ValidationException` rather than
  being ignored, so it stays honestly declared unsupported until it can be run
  against real Bedrock.
- [x] Say which of the three zeroes it is. A bare "0 cached" cannot distinguish
  a provider with no cache, a prefix not yet written, and reuse that is
  silently failing; `/context` and the Session tab now name the case.
- [x] Let the primary agent be steered. The iteration-boundary hook, the bounded
  drain-once queue, and the no-permission-grant framing all shipped with
  delegated agents; the primary session passed `TakeSteering: nil` and the
  composer refused mid-turn input with "Draft kept while the current turn
  runs." Watching a turn head the wrong way left two options — wait, or `esc`
  and lose it. Enter now steers, and the message says truthfully that it came
  from the user rather than from a parent.
- [x] Be exact about when guidance lands, and about what it does not do. It is
  delivered at the next iteration boundary, never inside an in-flight call, an
  executing tool, or a pending approval; the transcript marks it and says so.
  It grants no permissions — an action that needed approval before the text
  arrived still needs it after. Undelivered guidance is discarded when the turn
  ends and the discard is reported, because a cancelled turn is the common case
  and that text must not resurface against unrelated later work.
- [x] Keep the hint additive. The status bar advertises steering only when it
  does not push `esc cancel` off the bar: an indicator that hides the control
  which stops a turn has made the session worse to advertise one that nudges it.

**Behavior change:** two. Plain enter during a running turn now steers the
agent instead of holding the draft. And `input_tokens` in usage output and the
JSONL event stream now includes cached tokens on the Anthropic routes, where it
previously excluded them; `cache_write_tokens` is a new additive field on the
v1 event contract.

**Measured, not argued.** The fixed prefix is 13.3 KB (~3410 tokens): 11.5 KB
of tool schemas across 23 built-in tools plus a 1.8 KB system prompt. Across
one turn, the share of prompt bytes that are retransmission a warm cache serves
is 33% at one tool call, 71% at five, and 83% at ten. Against a live endpoint
(`azure-foundry-anthropic`, claude-sonnet-5) two identical requests reported
`cache_write=9627` then `cache_read=9627` — 100% of the second prompt served
from cache, with only two tokens falling outside the cached prefix, which
confirms the rolling conversation breakpoint is honored and not just the system
one. That run also confirmed the usage normalization is load-bearing: the
provider reported a raw `input_tokens` of 2 on both calls, so without summing
the three counters the second request would have shown a two-token prompt and
priced 9627 tokens at nothing. A write costs 1.25x and a read 0.1x, so the
one-time write premium is repaid by the first read — every turn that uses a
tool is already ahead.

## Completed wave — checkpoints that move the files too

**Goal:** Make an undo that actually undoes, by ending the split between a
conversation rewind that leaves the files alone and a file undo that knows
nothing about the conversation — without ever letting a recovery feature
destroy work the user did by hand.

- [x] Add `/restore [turn]`, which creates the same non-destructive
  conversation branch `/rewind` does *and* reverses every file mutation
  recorded after that turn. The two halves already existed and were
  independently solid; nothing connected them, so rewinding a turn left the
  files it wrote in place and the transcript describing a tree that no longer
  matched it.
- [x] Teach the change tracker turn boundaries from the runtime's existing
  event funnel rather than from the agent loop. Every surface — TUI, headless,
  browser terminal — reports events through that one site, so there is no
  second place a turn boundary could be missed, and the tracker never infers a
  boundary from elapsed time.
- [x] Verify the whole workspace before writing anything, and refuse the entire
  operation if any file changed outside Collomia — naming every affected file,
  not the first one found. Acting on one file and discovering a second
  afterwards is the same trap as a partial restore.
- [x] Verify before the conversation branches, not after. A drifted file found
  after the branch existed would leave a conversation that moved alone —
  precisely the split this wave exists to close. A test disables the pre-check
  and fails, so the ordering is pinned rather than incidental.
- [x] Collapse repeated mutations of one file into a single write, taking the
  newest recorded content as what disk must hold and the oldest as what to
  restore. Replaying twenty mutations backwards means twenty chances to stop
  halfway; one write per file means none.
- [x] Reverse a file the agent created (it is removed), restore one the agent
  deleted along with its original permission bits, and treat a file the user
  recreated where the agent deleted one as drift rather than as something to
  overwrite.
- [x] State the two real limits instead of papering over them. Change tracking
  is in memory, so a restore to a turn from a resumed session reports that no
  tracked changes needed reversing rather than implying it rewound writes it
  never observed — the tracker's turn numbering is aligned to the session's
  completed turns at every switch so the numbers still mean the same thing to
  both halves. External effects — commands, installs, network calls,
  deployments, MCP effects — are never reversed.
- [x] Say what a checkpoint costs before it is chosen: each picker entry carries
  how many changes across how many files restoring to it would reverse, because
  a turn number conveys none of that.
- [x] Leave `/rewind` exactly as it was, pointing at `/restore` for the coupled
  form. Changing what an existing recovery command does to the workspace is the
  last place to surprise someone.

**Behavior change:** none. `/restore` is new; `/rewind`, `/undo`, and
`collo sessions rewind` are unchanged.

## Completed wave — a Windows install that actually installs

**Goal:** Make the documented Windows path work on a stock machine, including
Windows 11 ARM64, without the user having to understand PowerShell's execution
policy first.

- [x] Stop detecting the CPU through
  `[Runtime.InteropServices.RuntimeInformation]::OSArchitecture`. Windows
  PowerShell 5.1 is a .NET Framework host with no native ARM64 build, and on
  Windows 11 ARM64 that property is missing, which `Set-StrictMode` turned into
  a hard `PropertyNotFoundStrict` failure before anything downloaded. The
  machine-scoped `PROCESSOR_ARCHITECTURE` registry value is read first instead:
  it reports the real hardware even when PowerShell itself is emulated.
  `-Architecture`/`COLLO_ARCH` is the escape hatch when every probe fails.
- [x] Document `irm ... | iex` as the install command. The execution policy
  governs script *files*, so evaluating the script from memory is unaffected by
  `Restricted` or `AllSigned` — no `Set-ExecutionPolicy`, no `Unblock-File`,
  no elevation. The saved-file path is still documented, with the bypass scoped
  to one invocation rather than changed machine-wide.
- [x] Keep the caller's session clean, because `iex` runs the script in it.
  `Set-StrictMode` and `$ErrorActionPreference` moved inside the installer
  function; a test asserts under Windows PowerShell 5.1 that neither leaks.
- [x] Update the user PATH by default, with `-NoPathUpdate` to opt out. Write
  the registry value directly, preserving `REG_EXPAND_SZ`, rather than using
  `[Environment]::SetEnvironmentVariable`, which rewrites PATH as `REG_SZ` and
  permanently breaks entries such as `%USERPROFILE%\bin`. A PATH failure warns
  instead of failing an install whose binary is already in place.
- [x] Silence the progress bar during download. Windows PowerShell renders it
  per buffer, which turns a 25 MB `Invoke-WebRequest` into minutes.
- [x] Stop staging the download under a file name containing "install". Release
  binaries carry no version resource, so Windows fell back to its UAC installer
  detection heuristic, decided `.collo.install.<guid>.exe` was an installer, and
  interposed an elevation consent dialog instead of running it. From PowerShell
  that is invisible — the call operator returns with no output, no error, and no
  exit code — so the version check failed on every standard-user machine.
  Administrators, including CI runners, never see the prompt, which is why this
  shipped. The staged and backup names are now asserted against the heuristic.
- [x] Make the post-download version check report what the binary actually did.
  It read `$LASTEXITCODE` bare, which is a global that a fresh interactive
  session has never set, so any invocation that did not set one failed with
  `VariableIsUndefined` instead of naming the real problem. CI never saw it
  because the fixture build sets `$LASTEXITCODE` first; the tests now clear it
  before installing. The binary's own output is the authoritative signal, the
  exit code is consulted only when one exists, stderr is captured so a chatty
  binary cannot trip `$ErrorActionPreference = 'Stop'`, and a failure quotes
  what the executable printed.

## Completed wave — built-in web search and fetch

**Goal:** Stop the agent from guessing about anything newer than its training
data, without making the capability an integration a user has to find, install,
trust, and pay for — and without giving a model-chosen URL a route into the
user's own network.

- [x] Ship `web_search` and `web_fetch` as built-ins with no API key, no
  account, and no configuration. DuckDuckGo's no-JavaScript endpoints are the
  backend because they are the only major search interface that answers a plain
  query with no key and no quota; there is no Go client worth depending on, and
  what exists wraps the same two endpoints. Adding `golang.org/x/net/html` cost
  no new module — it was already an indirect dependency.
- [x] Try both endpoints in order, and treat a 200 that parses to zero results
  as an engine failure rather than as "no results". Scraping breaks; the
  failure that matters is the silent one that tells a user the web has nothing
  on their question.
- [x] Reduce HTML structurally, not statistically: drop what is never content,
  prefer a `<main>`/`<article>` that actually holds the article, and keep
  headings, lists, code blocks, and tables. A readability score would let a
  page fall on the wrong side of a threshold and lose its own text.
- [x] Enforce a real address boundary rather than describing one. The check
  runs on the resolved IP at connect time through the dialer's `Control` hook,
  so it covers DNS rebinding, every redirect hop, and IPv4-mapped and NAT64
  spellings of a private address alike. Loopback, private, link-local (cloud
  metadata), CGNAT, multicast, benchmark, documentation, and reserved ranges
  are all refused, and no configuration key can turn that off — a switch to
  disable it is exactly what a prompt injection would ask a user to add.
- [x] Ignore inherited proxy variables, strip URL credentials, keep no cookie
  jar, and use a transport that is not shared with the provider client. Each
  of those is a way a model-chosen request could otherwise reach a host the
  guard never inspected or carry state that was never meant for it.
- [x] Report a redirect that leaves the requested site instead of following it.
  `web_fetch` declares the host of the URL it was given, so approving that host
  must not become approval for wherever a redirector points; moves within one
  site are followed normally. `web_search` symmetrically declares *every*
  endpoint it may fail over to, so a rule covering only the primary endpoint
  covers nothing.
- [x] Classify both as external risk, so autopilot never approves them
  silently and `permissions.network: "scoped"` governs them like any other
  network-bearing action — while a `host` rule or a session grant still makes
  ordinary use frictionless.
- [x] Collapse external-data framing into one implementation. MCP framing and
  web framing had to be the same code: a second source shipping a weaker copy
  of the first source's protection is the defect shape this repository keeps
  finding, and web pages are written by whoever the search ranked rather than
  by a server the user chose to trust.
- [x] Speak HTTP/1.1 and never negotiate HTTP/2. Go's HTTP/2 client sends a
  distinctive SETTINGS frame that bot-management products fingerprint: holding
  machine, address, and user agent constant, Stack Overflow returned 403 with
  `cf-mitigated: challenge` on every HTTP/2 request and 200 on every HTTP/1.1
  one, and Medium behaved the same. This — not the user-agent header — is what
  made those sites readable. Found by asking why a 403 persisted after the
  header change that was supposed to fix it.
- [x] Present one fixed browser identity — current desktop Chrome on Windows.
  A great many sites reject non-browser clients by default CDN rule, and a
  page the user can read but the agent cannot is the capability failing at its
  own premise. Deliberately not a rotating pool: rotation only defends against
  a blocklist naming one exact string, which no operator applies to mainstream
  Chrome, while turning any site that did refuse one entry into a failure that
  reproduces a fraction of the time. Desktop rather than mobile because mobile
  identities are served a smaller document. Nothing beyond the header — no TLS
  fingerprint forgery, no challenge solving, no address rotation, no retry of
  a refusal — and `docs/RELEASING.md` carries refreshing the version.
- [x] Name DuckDuckGo's rate limiting instead of echoing it. A throttled
  client gets HTTP 202 and a challenge page rather than a 429, which reported
  verbatim reads like a Collomia bug and sends the user looking for one. Found
  by measuring the user-agent pool against the live endpoints, which tripped
  the limit and produced exactly that unhelpful message.
- [x] Add an opt-in live suite (`COLLO_LIVE_WEB_TESTS=1`) that exercises each
  search endpoint alone, so a working fallback cannot hide a primary that has
  stopped parsing. The ordinary suite stays offline and credential-free.

**Behavior change:** two new built-in tools, visible to the model by default
and available in planning mode. They prompt like any other external action;
`options.disabled_tools` removes them.

## Completed wave — scoped egress on macOS

**Goal:** Make the sandbox's network boundary something a user will leave
switched on, by replacing an all-or-nothing switch with a per-host one — and
be exact about the two platforms where that is not possible.

- [x] Add `permissions.sandbox_egress` (`off`/`scoped`, default `off`). Under
  `scoped` the sandbox denies direct remote egress while keeping loopback
  reachable, and the command is routed through a Collomia-owned loopback
  CONNECT broker that dials only the hosts named by host-scoped `allow` rules.
  The allowlist is built from those rules rather than configured separately,
  so there is no second list to keep in step.
- [x] Take the destination from the proxy request itself and dial exactly that
  host, with no TLS interception — an approved tunnel is spliced byte for byte.
  This also removes any need for SNI inspection: a client cannot name one
  destination and reach another.
- [x] Ship no Linux backend, and say why. Landlock filters TCP by destination
  port and never by address, so reaching a loopback broker means allowing that
  port outright — which also allows every remote host on it, and the adversary
  this control targets chooses its own port. A control defeated by the thing it
  guards against is worse than an honest coarse one, exactly as with the
  credential store's rejected encrypted-file fallback.
- [x] Ship no Windows backend, and say why. AppContainer blocks loopback to
  unpackaged local services regardless of network capability SIDs, so a
  sandboxed command cannot reach the broker at all — no route rather than a
  weaker one. The documented escape needs administrator rights and persistent
  machine state, which the inbox-API-only Windows backend does not take.
- [x] Never inject proxy variables where they cannot work: under `require` the
  unsupported platforms fail closed, under `auto` they degrade visibly to
  `sandbox_allow_network`, and with `sandbox: "off"` no broker starts anywhere.
  A proxy the user believes is a boundary, which any program ignoring
  `HTTP_PROXY` walks past, is worse than no claim at all.
- [x] Drop inherited proxy variables before setting the broker's, so an
  ambient `NO_PROXY` cannot route a command around it and the result does not
  depend on how a library resolves duplicate environment keys.
- [x] Broker background processes on the same terms, with the broker's lifetime
  following the process rather than the tool call — otherwise `start_process`
  would have been a documented way around the control.
- [x] Collapse command-runner construction into one site. Delegated
  verification built its own runner, which is how a containment field ends up
  applied in the primary session and silently absent for delegated agents; a
  source-scraping test now fails on a second construction site.
- [x] Report the stance where people look: the effective posture and allowlist
  size in `collo doctor` and the Session tab, a per-endpoint forecast in
  `collo policy check`, and a refusal that names the host and the rule that
  would permit it. The generic sandbox hint is suppressed after an egress
  refusal, because pointing at `sandbox_allow_network` would send the user to
  the switch this feature exists to replace.
- [x] Keep no preset setting it. Scoped egress is enforceable on macOS only, so
  a cross-platform bundle selecting it would make one preset name mean
  different containment on different machines.

**Behavior change:** none by default — `sandbox_egress` defaults to `off` and
no preset selects it. Adopting `scoped` is a deliberate change; see the
[compatibility note](docs/COMPATIBILITY.md#scoped-egress).

## Completed wave — first run and code intelligence

**Goal:** Remove the two things that most often make a first session go badly —
a credential that has to live in a dotfile, and an agent that reads code by
grepping for names.

- [x] Store a provider API key in the macOS Keychain or Windows Credential
  Manager through `collo auth set|list|status|rm|import`, prompting without
  echo and never placing a secret in an argument or shell history. Nothing
  prints a stored value back.
- [x] Consult the store only after `api_key`, `api_key_env`, and a provider
  family's own variable, so an exported environment variable always wins and no
  existing configuration changes meaning. A machine that has never stored a
  credential makes no credential-manager call at all: a local name index is
  checked first, and its absence ends the lookup — which also means no keychain
  dialog for a user who does not use the feature.
- [x] Ship no Linux backend and no encrypted-file fallback, and say why:
  Secret Service needs a desktop session that headless hosts do not have, and a
  passphrase-protected file would only move the problem. `collo auth` and
  `collo doctor` state the absence rather than degrading quietly.
- [x] Report where each provider's credential came from in `collo auth status`
  and `collo doctor`, and mark an entry the operating system no longer holds as
  missing rather than implying it works.
- [x] Add `find_definition` and `find_references` on the existing
  language-server client, located by file, line, and the symbol's own text —
  the protocol counts columns in UTF-16 code units, and asking a model to count
  them buys confident answers about the wrong token.
- [x] Add `format_file`, applying the language server's own formatting as an
  ordinary tracked, undoable write, and refusing to write if the file changed
  while the server was formatting it.
- [x] Deliberately leave code actions unimplemented for now. Organize-imports
  and quick fixes need `codeAction/resolve` round trips and workspace edits
  that can span files, and a half-working mutation path is worse than an absent
  one. Phase 3 keeps the item open.
- [x] Say which capability a server lacks instead of relaying the protocol.
  Servers differ in what they implement — pyright, the auto-detected Python
  default, ships no formatter — so `format_file` used to fail with the raw
  string `Unhandled method textDocument/formatting`. A method-not-found answer
  is now a configuration answer that names the server, the missing capability,
  and the setting to change; every other protocol error passes through
  untouched.
- [x] Document the two Python facts a first test runs into: pyright navigates
  but cannot format, while `pylsp` does all three and type-checks less well;
  and a project `lsp` map does nothing until `collo trust`, because the
  quarantined layer silently falls back to the auto-detected default.
- [x] Account for the wait. A cold server indexing a large repository consumes
  most of a slow call, and a motionless spinner cannot be told from a hang, so
  the four language-server tools now stream `starting <server>…` and
  `<server> ready in <time> — <what it is doing>…`. Separating startup from
  the request is what distinguishes a slow index from a stall. The lines are
  display-only, never part of what the model reads, and the transcript
  replaces them with the result.
- [x] Make the first screen look composed. The identity line under the logo ran
  past a hundred columns on one line — version, commit, build timestamp,
  provider, model, theme — and because a centred block is centred by its widest
  line, that one line decided the whole header's offset and left the wordmark
  hanging to its left until the first prompt replaced the screen. The line is
  now two short centred ones, the wordmark is a five-row rendering with the
  blossom beside it, and the openers take the orientation card's indent rather
  than their own. The transcript header keeps the compact wordmark, left and
  top, over one line that carries only the version and the answering model;
  build detail moved to the Session tab and `collo version`.
- [x] Make modal dimming a preference. Dropping colour behind a dialog is right
  for using the tool and wrong for photographing it, and product documentation
  is made of screenshots, so `options.dim_background` turns the scrim off while
  defaulting to on. The cleared gutter is deliberately not part of the option:
  reading a modal must not depend on the dimming.
- [x] Wrap the transcript to the transcript. The context rail is composited
  over the body row by row, so a line wider than the body was cut at the rail's
  left edge rather than scrolled — answers, prompts, system and error lines,
  tool output, and panels all measured against the terminal instead. They now
  measure against the body width the tool-call header already used, prose
  word-wrapped and tool output hard-wrapped inside its gutter.

## Completed wave — terminal experience

**Goal:** Make the session's own surface — writing a prompt, seeing what the
agent is doing, and getting text back out — hold up under real use rather than
only in a screenshot.

- [x] Grow the composer with the draft instead of scrolling a one-line field,
  and extend rather than send a draft that is visibly unfinished — one ending
  in a backslash, or sitting inside an unclosed fence. Most users never
  discover a newline chord, so plain Enter has to be the common way to write a
  multi-line prompt. `alt+enter`/`ctrl+j` insert a newline everywhere;
  terminals speaking the Kitty protocol or `modifyOtherKeys` also get
  `shift+enter`/`ctrl+enter`, which arrive as CSI sequences Bubble Tea does not
  recognize and are intercepted ahead of the key switch.
- [x] Hand the draft to `$EDITOR` and take it back (`alt+e`), for the prompt
  that turned out to be three paragraphs.
- [x] Add an optional context rail (`alt+r`): workspace and branch, the plan,
  delegated agents, changed files, and background processes, beside the
  transcript rather than inside it. It appears on its own at 146 columns, is
  unavailable below 116, borrows columns from the transcript and never from the
  composer, and remembers a deliberate choice across a resize.
- [x] Replace the blank opening transcript with an orientation card, degrading
  to the banner and a single honest hint when the terminal is too narrow to
  show pairs that still read as pairs.
- [x] Mark each tool call in the transcript with its outcome and elapsed time,
  and leave both blank for a replayed session — the transcript records what a
  tool did, not how long it took, and inventing a duration is worse than
  omitting one.
- [x] Request mouse reporting by default (`options.mouse`) for wheel scrolling
  and click-to-select tabs, consuming only the wheel and a plain left click so
  drag, motion, and every modifier stay with the terminal — and add `alt+m` to
  release and reclaim the mouse mid-session, because mouse reporting and the
  terminal's own drag-selection are mutually exclusive by protocol and copying
  text should not require a restart.
- [x] Composite modals over a dimmed scrim with color dropped rather than
  blended, and clear a gutter around the dialog, so the frame is blank instead
  of mid-word transcript fragments that read as a corrupted redraw.
- [x] Let Chroma paint an approval diff's added/removed tint itself: emitting
  the background separately does not survive the SGR resets written between
  tokens, so the wash used to stop at the first keyword. Only previews that are
  actually diffs are tinted.
- [x] Give the context gauge eighth-block resolution, so ten cells no longer
  sit at zero for the first five percent of the window and then jump a whole
  cell.

## Completed wave — approval comfort

**Goal:** Make the controls added above livable, so they are read rather than
dismissed.

- [x] Fix an approval offer that did nothing: the dialog advertised a
  tool-wide "always" for a credential-reaching action, the permission layer
  declined to record it, and the next identical action prompted again. Whether
  an "always" is available is now one field the permission layer owns, the two
  stale copies in the TUI are gone, and a test fails on a third.
- [x] Offer one narrow session grant on a credential prompt, scoped to the
  exact target shown — never the tool, the directory, a sibling file, or
  anything past this process, and never offered under `deny`. A control whose
  only answer is "approve again" is a control people switch off.
- [x] Give a credential approval its own identity: its own header and accent,
  the file named first with the kind of secret after it, and a grant button
  short enough not to wrap the row.
- [x] Show the configuration rule that ends a recurring prompt, with the path
  or endpoint filled in — and deliberately not for an uninspectable command,
  where no rule would help.
- [x] Report the permission stance in `collo doctor` (preset, autonomy,
  postures, credential setting, rule count) and warn when a project's
  containment weakening was refused.
- [x] Group the Session tab's Security block into policy, enforcement, and
  session, and mark degraded sandboxing and refused project settings visibly
  rather than as ordinary rows.

## Completed wave — credential files as their own decision

**Goal:** Stop a broad approval from silently including a private key, without
adding configuration a user must understand before starting work.

- [x] Recognize the conventional credential locations — SSH and GPG private
  keys, cloud CLI token caches, registry authentication files, environment
  files — by path, with public keys, `known_hosts`, and example environment
  files excluded explicitly rather than by luck.
- [x] Report the credential stores a command's arguments name from shell
  analysis, keyed on the argument rather than on a table of reading programs,
  and derive the same for any tool that declares its paths.
- [x] Gate reaching one behind `permissions.protect_credentials`
  (`off`/`prompt`/`deny`, default `prompt`), placed so a blanket allow rule, a
  tool-wide session grant, the implicit in-workspace read path, and autopilot
  cannot cover it, while a rule naming the path still can.
- [x] Carry the setting on the preset ladder (frictionless off, standard
  prompt, hardened deny) and clamp it monotonically like every other
  containment field.
- [x] Redact PEM private key blocks and the remaining common provider token
  shapes, and state plainly in the package and in SECURITY.md that redaction
  does not sit between a tool result and the provider.
- [x] Show the setting in the Session tab's Security block and in
  `collo policy check`.
- [x] Build every command-shaped action in one constructor, with a test that
  fails on a second construction site. **This was not cosmetic:**
  `collo policy check` was reporting the wrong decision for a
  credential-reaching command because it assembled its own action and missed
  the field — the same defect shape that let the `host` matcher ship inert.

**Behavior change:** an action reaching a credential store now prompts by
default, including under `autopilot`, where a headless run fails closed. See
the [compatibility note](docs/COMPATIBILITY.md#credential-file-protection).

## Completed wave — host-scoped policy surface and per-capability grants

**Goal:** Make the documented `host` matcher real, and make an approval a
decision about what an action reaches rather than about a tool name — without
claiming enforcement the policy layer does not provide.

- [x] Derive the endpoints a command's text names (URL arguments, ssh-family
  destinations, Git remote URLs) and the endpoint of an HTTP-transport MCP
  server; normalize them to comparable bare hostnames.
- [x] Report an endpoint that resolves elsewhere — a named Git remote, a
  configured registry, a URL read from a file — as explicitly undetermined,
  and never as "no endpoints".
- [x] Populate the previously inert `Rule.Host` matcher from command, PTY,
  background-process, and MCP actions; block host-scoped `allow` rules from
  covering undetermined endpoints, mirroring the uninspectable-command rule.
- [x] Add the `permissions.network: scoped` and `permissions.commands:
  allowlist` postures: prompt-only escalation, defaulting to the earlier
  `open` behavior, monotonic across configuration layers, and not satisfiable
  by a tool-wide session grant.
- [x] Show an action's reach one dimension at a time in the approval dialog
  and add a session grant covering exactly the reach shown; grant nothing for
  an uninspectable command, a one-time confirmation, or an unreadable
  endpoint.
- [x] Treat an interpreter that reads its program from a pipe (`curl … | sh`)
  as uninspectable while still reporting the endpoint it fetches from.
- [x] Update starter/reference configuration, the capability matrix,
  `collo policy check`, and the README/security/user documentation to state
  that this is a policy layer and not egress enforcement.
- [x] Add host-extraction, policy-matching, posture, layering, and grant-UI
  regression coverage plus fuzz invariants that no unreadable endpoint is ever
  reported as a plain host.
- [x] Keep the growing security surface usable: add `permissions.preset`
  (`frictionless`/`standard`/`hardened`) as sugar over the existing fields —
  explicit fields win within a layer, and no preset sets autonomy mode — and
  make the effective stance always visible through a containment mark on the
  autonomy badge plus a consolidated Security block in the Session tab.
- [x] Replace the per-field precedence exceptions with one rule: a repository
  can tighten any containment setting but never weaken one, by explicit field
  or preset alike, with every refusal reported rather than applied silently.
  Document the complete precedence matrix. **Behavior change:** a project
  `"sandbox": "off"` (or any other project-level weakening) is now refused;
  the escape hatch lives in the global configuration only.

## Remaining work by phase

### Phase 1 — Safety boundary

- [x] **P0 — Complete separate capability controls:** executable allowlisting
  and a per-capability grant UI now ship alongside the independent filesystem,
  environment, network, and process controls.
- [x] **P0 — Credential-store protection:** reaching a conventional credential
  location is its own decision (`permissions.protect_credentials`, default
  `prompt`), not coverable by a blanket allow rule, a tool-wide grant, or
  autopilot. Recognition is by conventional path, so it is a usable default
  rather than secret detection; enforcing what a running process may read
  remains sandbox read confinement's job.
- [x] **P1 — Scoped egress on macOS:** `permissions.sandbox_egress: "scoped"`
  denies direct remote egress in the sandbox and routes commands through a
  Collomia-owned loopback CONNECT broker that dials only the hosts named by
  host-scoped `allow` rules, without TLS interception. Foreground commands,
  background processes, and delegated verification are brokered on the same
  terms through one configuration site.

  **This was reclassified from P0 during implementation.** The all-or-nothing
  `sandbox_allow_network` is enforced on all three platforms already, so the
  safety floor never depended on this; what was missing was a version of that
  floor people would leave switched on. It is a usability-of-security feature
  over an existing enforced control, not a hole — and with it landing, no P0
  remains outside Phase 8.
- [x] **P1 — State the per-platform egress limits rather than degrade
  vaguely:** the three backends do not sit on a gradient, so they are
  documented as three distinct claims. macOS Seatbelt denies remote traffic
  while keeping loopback, which is what makes the broker a boundary. Linux
  Landlock filters TCP by port and never by address, so allowing the broker's
  port would allow every remote host on it — an allowlist the adversary it
  targets picks its own port around, which is why no Linux backend is shipped
  rather than a weaker one. Windows AppContainer blocks loopback to unpackaged
  local services, so the design has no route at all there, and its
  all-or-nothing denial is the most complete of the three. Both refuse under
  `require` and degrade visibly under `auto`; with `sandbox: "off"` no broker
  starts anywhere, because a cooperative proxy is not presented as a boundary.
- [x] **P1 — Publication and deployment as their own decision:**
  `permissions.publication` (`off`/`prompt`/`deny`, default `prompt`) governs
  the actions that put something outside this machine — package and container
  registries, source remotes, code-forge writes, infrastructure applies, and
  commands run on another host. It is not coverable by autopilot, a tool-wide
  "always allow", or an allow rule naming only an executable; a rule naming the
  operation is, and one narrow session grant covers exactly the operation shown.

  **This was the last known asymmetry in the risk classifier.** The safety
  taxonomy described destruction only, so every deletion in these tools required
  a fresh decision even under autopilot while none of the publishing
  counterparts did — a gap an independent assessment would have found by running
  `collo policy check` for an afternoon. Like host rules it is a policy layer
  and not egress enforcement, and the catalogue of recognized tools is finite.
- [x] **P1 — Operation-scoped policy rules:** a `rules[].command` pattern
  containing a space matches the executable plus the words that decide what it
  does (`npm publish`, `gh pr create`, `ssh build-host`) rather than `argv[0]`.
  Before this, such a pattern matched nothing and validated clean — the third
  inert-matcher defect in this repository. An unmatchable pattern is now a
  validation error, and `collo policy check` prints the exact operation string a
  command produces so the vocabulary never has to be guessed.
- [ ] **P2 — Windows scoped egress:** only reachable through a
  `CheckNetIsolation` loopback exemption (administrator, persistent machine
  state, documented by Microsoft as a debugging aid) or WFP/firewall filters
  keyed on the AppContainer SID (administrator, address-based rather than
  host-based). Both conflict with the Windows backend's no-administrator,
  inbox-API-only commitment, so this stays deliberately unbuilt rather than
  half-built.
- [ ] **P0 — Independent review:** sustain the adversarial suite and obtain an
  independent security assessment before 1.0.

### Phase 2 — Sessions and context

- [x] **P1 — Coupled checkpoints:** `/restore [turn]` branches the conversation
  and reverses the tracked file mutations recorded after that turn as one
  operation. The workspace is verified before the conversation branches, so
  drift refuses both halves and names every file rather than half-applying.
  Shell, network, and other external side effects are still not reversed, and
  the process-local scope of change tracking is stated rather than implied.
- [ ] **P2 — Nested instructions:** evaluate directory-scoped instruction
  inheritance after precedence and trust UX are designed.

### Phase 3 — Coding loop

- [ ] **P1 — Complete LSP operations:** definitions, references, and
  formatting ship alongside diagnostics and lexical symbol search. Safe code
  actions remain: they need `codeAction/resolve` round trips and workspace
  edits that can span files, which is a mutation path worth doing properly or
  not at all.
- [ ] **P1 — Optional deeper review:** line-level pending-write selection and
  broader selective application for multi-file patches.
- [x] **P1 — Windows ConPTY:** `run_command` with `pty: true` and `collo --web`
  both work on Windows through a shared `internal/conpty` package. The child is
  created suspended and joined to a job object before it runs, so the
  process-tree cancellation contract is strengthened rather than weakened —
  there is no instant in which a descendant could be spawned outside the job.
  Command-line construction goes through the same `exec.LookPath` and
  `syscall.EscapeArg` path `os/exec` uses, so a command is quoted identically
  with and without a PTY. No new dependency: the whole API is already in
  `golang.org/x/sys/windows`.

### Phase 4 — Provider platform

- [x] **P1 — Secure credential lifecycle:** `collo auth` keeps provider API
  keys in the macOS Keychain or Windows Credential Manager, with set/list/
  status/rm/import flows, no project-file secrets, and no value ever printed
  back. Environment variables keep precedence and remain fully supported;
  Linux has no backend by design, so headless hosts use `api_key_env`. MCP
  server credentials are covered separately by Phase 5's OAuth item.
- [ ] **P1 — First-run setup and provider discovery:** make the configuration
  correct *before* it is written, by proving it with a real request — and take
  Azure deployment/project discovery and routing, tested sovereign presets, and
  clearer resolved AWS identity/model-access diagnostics as part of the same
  work rather than beside it.

  **`collo setup` has shipped**, covering local runtimes, hosted families, and
  form-configured Azure and Bedrock end to end: concurrent probing, catalog
  discovery, two-request verification, diagnosis, a writer that never puts a
  secret in a file, and `sts:GetCallerIdentity` reporting which AWS identity the
  credential chain actually resolved — the "resolved AWS identity diagnostics"
  half of this item. Re-running is a supported flow: it reads the file it writes
  rather than the merged configuration, shows the current default, marks a
  provider it would replace, and asks before repointing `default_provider`.

  What remains open is **discovery** proper. Azure OpenAI and Bedrock are
  configured by naming their fields, which is honest but not enumeration:
  deployment listing needs the ARM management plane and Bedrock's
  `ListFoundationModels` needs `aws-sdk-go-v2/service/bedrock`, neither of which
  is a current dependency. Foundry already gets model selection free, since its
  OpenAI v1 route publishes a catalog. Tested sovereign presets also remain.

  **Reclassified from P2, and merged with the former "Setup wizard" entry.**
  The two were one item seen from opposite sides: the wizard's Azure branch
  *is* deployment discovery, and its Bedrock branch *is* the model-access
  diagnostic, so kept separate the Azure probe gets written twice. The parts
  all exist — capability registry, model discovery, the four-state
  `ProviderAvailability` that already distinguishes *unverified* from
  *unavailable*, credential precedence, `credstore`, starter generation, and
  `config validate --strict` as the final gate — so this is assembly and one
  honest failure path rather than new machinery. It is P1 because the first
  item in the recommended next sequence is real beta feedback, and the current
  first-run path is: read the README, hand-write JSONC, set a credential, run
  `doctor`, and guess which of those four steps was wrong.

  **Nothing currently dials.** `collo init` writes a static starter file with
  `ollama`/`qwen3-coder`/`127.0.0.1:11434` as literal values, and
  `config.Defaults()` returns the same assumption for a machine that has never
  been configured at all — so an install with no Ollama on it is, as far as the
  configuration layer is concerned, correctly configured. `collo doctor` is
  thorough about everything visible from the configuration and the
  environment — which credential source won, the Entra scope and tenant,
  whether Bedrock resolved bearer or SigV4 — and makes **no network request**,
  so its best possible answer is `ok`. A dead port, a revoked key, a model the
  endpoint does not have, an Azure model name written where a deployment name
  belongs, a Bedrock region without model access, and a wrong `context` all
  survive every existing check and surface identically: the interface opens,
  the user types, and *then* it fails. That is the state in which someone
  stops rather than reports.

  - Probe, discover, then verify, in that order. Probe the default ports of the
    local runtimes already supported (Ollama 11434, LM Studio 1234, vLLM 8000),
    because "not installed" and "installed but not running" are different
    sentences. Discover through `Runtime.ListModels`, which already constructs
    the client, checks `CapabilitiesFor(...).ModelDiscovery`, and annotates
    every result with its capabilities — so the model is chosen from what the
    endpoint actually has rather than typed from memory.
  - **Verify with real requests, not with the catalog call.** These are
    different endpoints with different permissions: Azure lists *models* while
    requests address *deployments*, and Bedrock will list a model the account
    cannot invoke. A catalog response proves the host is reachable; only a
    generation proves the thing being written down will answer.
  - **Send two requests, the second carrying a tool definition.** The first
    version sent no tools, reasoning that one request isolates one cause. It
    does, and running it against a real Ollama showed what that costs:
    `gemma3:270m` verified cleanly and then failed the first real prompt with
    `does not support tools` — the exact failure the wizard exists to move
    earlier, and not an edge case, since every embedding, vision, and small chat
    model in a local catalog is in that position. Two requests keep the cause
    isolated *and* keep the promise. Nothing needs to *call* the probe tool;
    acceptance is the discriminator, because a capable model may reasonably
    answer a trivial prompt with text.
  - Write only fields that were verified — provider, type, base URL, and model —
    with `context` from the capability registry instead of the current
    hardcoded guess. **Always write a context window**, falling back to the
    value `collo init` already uses and labelling it as assumed on the
    confirmation screen. Leaving it out looks more honest and is worse:
    `Agent.shouldCompact` returns false on a zero window, so automatic
    compaction never runs and a long session ends at a provider
    context-length error with no recovery.
  - **Never write a secret into the file**, which is the rule `collo auth` was
    built on. Prefer an already-exported variable and record `api_key_env`
    pointing at it, so the wizard handles no secret at all; otherwise offer the
    keychain through `internal/credstore`; on Linux say plainly that there is no
    backend and write `api_key_env` to export. This also puts the macOS Keychain
    backend on a real code path for the first time — see the Phase 8 coverage
    item.
  - Spend the effort on the failure branch, because that is the whole value. A
    refused connection names the runtime and how to start it; a 404 on a model
    prints the catalog that was actually returned; an Azure 404 says that the
    field wants a deployment name. A wizard that only narrates success has
    moved the original failure later, not removed it.
  - Ship it as `collo setup`, its own verb, and have the interface offer it when
    no provider has ever been verified. `collo init` keeps exactly its current
    contract — write a starter file, refuse if one exists — because it is
    documented, guarded by tests, and used from scripts; probe-verify-write is a
    different operation and giving one verb two contracts is how a documented
    behavior quietly becomes two. `collo setup` is re-runnable, since adding a
    second provider is the next thing that happens after the first works.

  **Behavior change:** `config.Defaults()` stops naming a provider it has never
  contacted. An installation with no configuration reports that no provider is
  configured and points at `collo setup`, rather than asserting Ollama on
  localhost and failing at the first prompt. Anyone actually running Ollama on
  the default port is unaffected in practice — `collo setup` finds it and
  writes it down — but a machine that relied on the implicit default without a
  configuration file now needs one.
- [x] **P1 — Provider prompt caching:** the Anthropic Messages routes send two
  cache breakpoints — the stable tools/system prefix and a rolling
  conversation boundary held behind any volatile trailing content — and drop
  caching for the client after an explicit rejection. Usage is normalized so
  `input_tokens` means the whole prompt whatever split a provider reports, and
  cache writes are priced separately from cache reads. Bedrock `cachePoint`
  remains unbuilt: support varies by model and region and fails hard rather
  than degrading, so it stays declared unsupported until it can be verified
  against real Bedrock.
- [ ] **P1 — Modern API features:** general OpenAI/Azure Responses routing,
  structured output, richer thinking/content blocks, and additional media
  types. The one-hour cache TTL is open here: it needs a beta header whose
  current status must be confirmed, and it is worth taking only on measured
  evidence since the longer write is billed higher.
- [ ] **P1 — Explicit routing/fallback:** ordered capability/health/cost/local
  choices that never silently cross privacy or residency boundaries.
- [ ] **P1 — Usage and budgets:** normalized user-priced cost estimates and
  enforceable session/agent monetary budgets ship; an independently
  configurable per-turn dollar cap and richer provider billing caveats remain.

### Phase 5 — MCP and extension ecosystem

- [ ] **P1 — Standards-based MCP OAuth:** login/logout and credential storage
  outside project configuration.
- [ ] **P1 — Resource subscriptions and stable tasks:** implement only against
  stable protocol/SDK contracts; retain safe catalog refresh behavior.
- [ ] **P1 — Complete rich content:** audio and annotation passthrough without
  flattening typed content.
- [ ] **P1 — Argument-level MCP permissions:** bounded normalized resource
  matching that server-authored annotations cannot use to lower risk.
- [ ] **P2 — Extension packaging:** a versioned custom-tool/plugin package and
  SDK after trust and permission contracts are stable.

### Phase 6 — Multi-agent orchestration

- [x] **P0 — Finish agent definitions:** reasoning controls, monetary budgets,
  visibility, and named primary profiles.
- [x] **P0 — Conservative conflict handling:** serialize known overlapping
  assignments and offer explicit three-way reconciliation without silently
  overwriting parent or sibling work.
- [ ] **P1 — Plan graph execution:** assign dependency-ready nodes, propagate
  verified evidence, invalidate stale repository assumptions, and re-plan on
  failure. Keep this opt-in until cancellation and review behavior are proven.
- [ ] **P1 — Result synthesis:** build on the shipped freshness-bound child
  verification and comparison surface with explicit combined-parent
  verification and safe ranking criteria.
- [ ] **P1 — Reproducible recovery:** restore scheduler order and offer safe
  restart of pending read-only work without replaying completed mutations.
- [ ] **P2 — Optional team templates:** reviewer, researcher, test, security,
  and documentation profiles without hard-coding them into the core loop.

### Phase 7 — Terminal and automation experience

- [ ] **P1 — Finish artifact input:** raw clipboard image protocols and
  optional inline pixel previews where the terminal supports them.
- [x] **P1 — Correctable turns:** typing during a running turn steers the
  primary agent through the same iteration-boundary hook delegated children
  use, with a bounded drain-once queue, an explicit no-permission-grant
  framing, a transcript marker stating where the guidance will land, and a
  reported discard when a turn ends before delivery.
- [ ] **P1 — Workspace UI refinements:** the context rail now carries
  workspace, plan, agents, changed files, and background processes beside the
  transcript; automatically surfaced diagnostics and provider price/budget
  visibility remain.
- [ ] **P1 — Accessibility validation:** colour (`NO_COLOR` selects the plain
  theme), motion (`options.reduced_motion`), and narrow-width resize behavior
  are done, the last pinned by a width sweep rather than by golden screens.
  Broader terminal-emulator coverage remains — the Kitty/`modifyOtherKeys` key
  handling is the most emulator-sensitive code and is unverified across
  emulators.

  **Screen-reader support is deliberately not built.** Full-screen TUIs repaint
  with absolute cursor positioning and screen readers follow the terminal
  buffer, so the two are structurally opposed; of comparable tools only Claude
  Code ships a mode for it, added recently after sustained user pressure, and
  Codex/Crush/OpenCode have none. Collomia's non-interactive JSONL output is
  already a linear-text path and is documented as the answer, in the same
  idiom as Linux scoped egress. That keeps a future `--screen-reader` flag
  cheap — wiring and documentation rather than architecture — if a user asks.
- [ ] **P2 — Structured local service API:** authenticated stdio/socket or
  WebSocket access to the event/session/permission contracts. The current web
  terminal is a PTY transport, not this API.
- [ ] **P2 — Governed web/browser tools:** explicit enablement, domain policy,
  citations, download quarantine, and prompt-injection defenses.
- [ ] **P2/P3 — Remote and collaboration surfaces:** only after identity,
  residency, audit, and local durability boundaries are complete.

### Phase 8 — Quality and 1.0 readiness

- [ ] **P0 — Security program:** sustained fuzz/adversarial campaigns and an
  independent review.
- [ ] **P0 — Reliability campaigns:** host-level filesystem exhaustion,
  power-loss durability, native terminal loss, and longer cancellation stress.
  The diagnostic/audit half of this item is broken out below, because it turned
  out to be a feature rather than a policy decision.
- [x] **P0 — A trustworthy audit ledger:** every entry names the session and
  actor that wrote it; a write failure is counted, reported once, and declared
  in the file as a gap rather than leaving a hole that reads as complete; both
  delegated-agent paths route through one attachment site that no longer drops
  `audit.Open`'s error; `collo audit` reads the record back and leads with its
  integrity; growth is bounded by rotation that admits what it discards; and
  the package went from no test of its own to 83% coverage. What remains here
  is a boundary rather than a gap: this records what Collomia's permission
  layer decided, not what an approved process then did on its own, which is
  sandbox read/write confinement's job and is documented as such.
- [ ] **P2 — Decide whether a mandatory-audit posture should exist:**
  `docs/FEATURES.md` claimed audit writes could be made mandatory by policy,
  and listed "audit requirements" among the monotonically clamped containment
  fields. Neither has ever existed: there is no such key in
  `appconfig.Permissions` and `ContainmentFields()` does not include one. The
  documentation has been corrected rather than the gap papered over, and the
  claim is now blocked by `TestDocumentedPermissionSettingsAllExist`.

  The question the false claim raises is still a fair one. The audit wave chose
  fail-visible deliberately — refusing work the user already authorized because
  a record could not be filed is the wrong default — but a regulated deployment
  may genuinely want "no action proceeds unless it can be recorded" as an
  opt-in posture, and the wave left the mechanism in place for it
  (`Ledger.Degraded`, `OnFailure`, and the runtime's latched health). It would
  be a new clamped containment field with a fail-closed headless path. It stays
  unbuilt until someone wants it, because a posture nobody has asked for is how
  the original claim came to be written in the first place.
- [ ] **P1 — Cover the credential store's actual backend:** `internal/credstore`
  reads 40.8%, but the headline hides where the hole is — `backendGet`,
  `backendSet`, and `backendDelete` in `store_darwin.go` are at **0.0%**, as are
  `Delete` and `Verify`. The entire macOS Keychain path, and `collo auth rm`
  along with it, has no test of its own on a platform that can run one. This is
  the shape the audit wave already found once: a package at zero coverage whose
  first real tests immediately surfaced two defects that unit review had missed,
  in a package that holds provider API keys. Phase 4's first-run setup will put
  this path under a user's hands for the first time, so the coverage is worth
  having before that rather than after.
- [ ] **P1 — Performance budgets:** idle memory, token overhead, compaction
  quality, monorepo fixtures, and same-hardware regression thresholds. The
  prompt-cache wave established the measurement discipline and the live-endpoint
  harness this needs, so it is cheapest to take while that work is still warm.
- [ ] **P1 — Optional telemetry decision:** only opt-in, minimal, documented,
  locally inspectable/deletable, and fully disabled by offline mode.
- [ ] **P1 — Native release signing:** Apple signing/notarization, Windows
  Authenticode, and installer-enforced signature verification.
- [ ] **P1 — Package managers:** Homebrew and Scoop first, because neither
  needs a signed binary — a Homebrew *formula* over the release tarball and a
  Scoop manifest over the zip are both checksum-verified text, and the release
  workflow already produces the artifacts, checksums, SBOMs, and attestations
  they would reference. Publish both from `release.yml` so a manifest cannot
  drift from `VERSION`. A Homebrew **cask** and a Winget manifest are blocked
  on native release signing above and should not be started before it;
  discovering that halfway through is how a distribution slice stalls with
  nothing shipped. Selected Linux flows and clean-machine
  install/update/rollback/uninstall testing follow each channel that ships.

  Today the only routes to the binary are `curl | sh` and `irm | iex`. Both
  work, and neither is how most developers install a tool they have just heard
  about.
- [ ] **P1 — A reporting path that does not require assembling a report:**
  `collo feedback` opens a prefilled issue carrying `collo doctor`'s
  environment facts and the opaque `err-…` identifier, and nothing else,
  reusing the support bundle's existing privacy review rather than inventing a
  second one. `collo support bundle` already covers a reproducible failure; it
  is the wrong instrument for "this was confusing", which is most of what a
  beta needs to hear and none of what it currently receives.

  This is deliberately the zero-telemetry form of the optional telemetry
  decision above and does not wait on it: nothing is collected, nothing is
  transmitted in the background, and the user reads the whole report before it
  leaves the machine.

## Recommended next sequence

Several waves in a row were P1 or P2 while the items that actually gate 1.0 did
not move. The audit ledger — the cheapest of the remaining P0s, and the one an
independent assessment starts from — has been taken; Windows ConPTY closed the
last platform-parity gap; and the publication wave closed the finding that
assessment would most likely have opened with. The rest, in order:

1. Close the distance to a first successful session, because the beta feedback
   this list has opened with for several waves is blocked on it rather than on
   anyone's willingness. Three segments of the path from *hears about Collomia*
   to *has a working session* are unbuilt, and all three were already on this
   roadmap as deferred items: **getting the binary** (Phase 8 package
   managers — no Homebrew, Scoop, or Winget; `curl | sh` and `irm | iex` are
   the only routes), **trusting it** (Phase 8 native release signing — unsigned
   on macOS and Windows, provenance-attested only), and **configuring a
   provider** (Phase 4 first-run setup — `collo init` writes a static starter
   file, `config.Defaults()` asserts a provider it has never contacted, and
   `collo doctor` makes no network request, so nothing between installation and
   the first prompt ever dials the endpoint being configured). Add
   `collo feedback` beside them so that what comes back is cheap to send.

   Native signing is the one segment with an external dependency —
   certificates cost money and need CI secrets — so it is a decision to take
   rather than work to schedule, and the other three do not wait on it. Every
   wave since v0.1.7 has added depth to a product that is still meaningfully
   hard to start using; this is the same asymmetry the publication wave fixed,
   in a different place. A control nobody leaves on and a product nobody can
   easily install fail for the same reason.
2. Gather real beta feedback on named primary profiles, cost estimates,
   verified delegated results, scoped scheduling, three-way review, and the
   new postures — including scoped egress, whose allowlist ergonomics are best
   judged against real toolchains rather than predicted. This is the stated
   prerequisite for item 3 and has not happened yet.
3. Add opt-in plan-graph execution using verified results, write scopes,
   dependency readiness, and stale-state invalidation, then explicit
   combined-parent verification and conservative result-ranking criteria
   without turning a score into permission. `collo audit --actor` is now the
   surface that can say what each agent in such a graph was permitted to do.
4. Continue Phase 8 security/reliability campaigns in parallel with every
   feature wave, and take the performance budgets while the prompt-cache wave's
   measurement harness is still warm. The reliability half — host-level
   filesystem exhaustion, power-loss durability, native terminal loss, longer
   cancellation stress — is now the largest untouched P0 and produces
   confidence rather than a control, which is why it did not outrank the
   publication gap but does outrank the next feature.

Two small decisions can ride along with whichever wave ships next rather than
becoming waves of their own:

- The one-hour cache TTL. The mechanism is measured and confirmed working; what
  is unknown is only gap behavior, since the live run measured back-to-back
  requests rather than a session resumed after a pause. Instrument the real
  gap between turns before paying a write premium that is 2x rather than 1.25x.
- Git write tooling under approval. The agent reads Git but must drop to
  `run_command` to commit, and `/restore`'s change tracking is in memory so it
  does not survive a resume; a checkpoint commit would fix both. It was
  considered for this slot and deferred deliberately: it *adds* an
  outward-facing capability, and governing the outward-facing capability that
  already existed had to come first. It now lands on top of the publication
  tier rather than beside it.
- Deliberately **not** another terminal-surface wave. Four of the last six
  touched it, the width sweep now pins the property the golden screens had
  recorded wrong, and the returns there are visibly smaller than a security
  control with no tests.

## Exit gates

### Multi-agent automation gate

- Independent writers cannot race or silently overwrite one another.
- Child claims are distinguishable from machine-observed fresh verification.
- Publication always requires review/policy and combined-parent verification
  remains explicit.
- Cancellation and recovery never replay a completed mutating action.

### 1.0 gate

Collomia should not call itself 1.0 or advertise safe unattended execution
until all of these are true:

- The sandbox and permission model pass cross-platform adversarial tests and
  an independent review.
- Sessions survive interruption; mutating actions are idempotent or explicitly
  reconciled; direct edits remain reviewable and recoverable.
- Every advertised provider has maintained capabilities and passing contracts.
- Long-context and multi-agent work has bounded budgets, cancellation,
  observability, and regression evaluations.
- MCP, skills, hooks, and repository configuration share the documented trust
  model.
- Release artifacts are natively signed where applicable, reproducible or
  provenanced, and install/update/rollback paths are tested.
- No known P0 data-loss, sandbox-escape, secret-exposure, or duplicate-mutation
  defects remain open.

## Explicitly deferred

- Cloud-hosted execution and account synchronization.
- Shared real-time team workspaces.
- A public plugin marketplace.
- Autonomous Git commits, pushes, pull requests, deployments, or issue updates
  by default. This is now enforced rather than merely intended:
  `permissions.publication` defaults to `prompt` and is not coverable by
  autonomy mode or a tool-wide grant.
- Persistent semantic memory across unrelated repositories.
- Decorative features that do not improve coding safety, accessibility, or
  throughput.
