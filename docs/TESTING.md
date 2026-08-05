# Testing and evaluation

Collomia's default test suite is credential-free and offline. Provider
protocol tests use in-process HTTP fixtures, MCP tests use the official SDK's
in-memory transports, and agent evaluations use a scripted provider while
driving the real permission and built-in tool pipeline.

## Standard local checks

Run from the repository root:

```sh
go build ./...
go test ./...
go test -race ./...
go vet ./...
```

`go test ./...` includes platform sandbox fixtures, provider/MCP contracts,
TUI goldens, session recovery, and the offline agent evaluations. A native
sandbox test skips only when the host cannot provide the backend being tested;
cross-compilation in CI still type-checks platform-specific code. Configuration
tests also lock the implicit `sandbox: auto` default, compatibility-first
network/read switches, minimal sandboxed command environment, and the
global-only explicit `sandbox: off` escape hatch, and the refusal of any
project-level attempt to weaken a containment setting.

## Offline agent evaluations

Run the evaluation corpus directly:

```sh
go test -count=1 ./internal/eval
```

The current corpus covers these outcome-oriented scenarios:

| Scenario | Real components exercised | Required invariants |
| --- | --- | --- |
| Repository inspection | Agent loop, `search_files`, `read_file`, implicit read permissions | Finds grounded file/line evidence and changes nothing. |
| Bug fix and verification | Agent loop, `edit_file`, change tracker, verification detection, `run_command` | Applies the intended edit, runs the fixture's real Go tests, and reports success only after passing output. |
| Behavior-preserving refactor | Agent loop, `read_file`, atomic `apply_patch`, change tracker, `run_command` | Removes duplication through one grounded patch, preserves behavior, and reports completion only after the existing tests pass. |
| Generated tests | Agent loop, `read_file`, `write_file`, change tracker, `run_command` | Creates boundary-focused tests and executes the fixture's real Go test suite before reporting success. |
| Grounded code review | Review prompt, `git_status`, `git_diff`, `read_file` | Identifies a real boundary regression at the exact file/line and leaves the worktree unchanged. |
| Permission refusal | Agent loop, command analysis, permission manager | A headless `ask` run records denial, never starts the command, and continues with an honest answer. |
| OG-1 primary goal graph | Runtime-owned graph, primary agent, built-in read/edit/command tools, permission manager, Git state token | Nodes run only after dependencies, no delegated actor appears, the real repair lands through the ordinary tool path, and `done` requires a passing repository test bound to the resulting combined workspace. |
| OG-1 bounded recovery | Typed graph failure ledger, controller retry, immutable attempts | A failed read cannot be erased by final prose; one fresh attempt changes the assumption, records grounded evidence, and alone is accepted. |
| OG-1 permission and restart safety | Ordinary permission pipeline, durable full-graph session snapshots, write-ahead action transition, restore | Denial becomes a typed blocked outcome without a write; a process boundary after a potentially mutating action never replays it and restores an explicit reconciliation blocker. |
| OG-2A explicit preview | Application runtime, read-only planning tool surface, fresh plan revision, acceptance criteria, explicit approval, dynamic primary controller | Proposal alone never activates a graph; a stale ordinary plan or missing criteria cannot be approved; one explicit approval attaches the primary-only graph and completes through recorded repository evidence. |
| OG-2B1 automatic read fan-out | Application runtime, runtime graph selector, shared delegate scheduler, two planning-mode children, Git state token, primary agent | Two independent approved `read_only` nodes overlap with no write/command/plan/delegate/graph-control authority; both grounded fresh summaries are retained, and only then does the dependent serial primary node start. |
| OG-2B1 cancellation and compatibility | Automatic worker contexts/team state, graph cancellation, schema-1 restore defaults | Cancelling a two-worker wave stops both children and records graph outcome `cancelled`; a pre-fan-out snapshot with omitted execution restores as serial `primary` and launches no child. |
| OG-2B2a cooperative controls | Runtime graph, agent boundary, application commands, TUI busy-command lane, schema-1 restore | Pause becomes durable without cancelling the active attempt, reaches only after pending action work finishes, attached and restored graphs require explicit resume, safe retry preserves blocked history, and exhausted or ambiguous-mutation retry fails closed. |
| OG-2B2b1 aggregate accounting | Explicit proposal baseline, primary and child provider-call counters, immutable attempts, graph snapshot restore, operator status | Proposal plus serial-primary work and automatic-read work remain separately inspectable and add to exact totals; failed/cancelled provider calls still count as iterations, unpriced work makes aggregate cost unavailable rather than zero, elapsed time freezes at terminal state, and legacy schema-1 restore reconstructs only facts its attempts recorded. |
| OG-2B2b2 aggregate bounds and comparison | Runtime provider/scheduler admission, durable usage recording, pause/restore clock, narrowed automatic claims, Standard/primary-only/fan-out product paths | Exact bounds stop the next admission, overages end `budget_exhausted`, all already-finished siblings in a terminal parallel wave remain accounted, unpriced work retains non-dollar bounds, paused/downtime time is excluded, and stored bounds cannot widen. Equal-grounding decomposable and cross-layer scenarios show lower controlled elapsed time for two substantive independent reads while recording extra iterations/tokens; trivial and dependency-serial work remains serial. |
| OG-3A isolated-writer candidate wave | Approved `isolated_write` scopes, stable parent token/commit, graph selector, ordinary delegate permission/hooks, shared scheduler, exact-base worktrees, child verifier, durable candidate state | At most two ready pairwise-disjoint writers start from one clean stable commit; overlaps and dirty bases fail closed; every changed file stays in scope; fresh passing detected commands are bound to child state; verified candidates remain in retained worktrees, stop for review, and never mutate the parent or unlock dependents; interrupted writers are blocked and never replayed. |
| OG-3A.1 aggregate-budget usability | Proposal accounting, approval-boundary compaction, graph-aware compaction trigger, graph snapshot restore, `/context` and Session presentation | New graphs receive the fixed one-million-token ceiling; older graphs retain their stored 192,000-token ceiling; proposal history is compacted before node execution; a prompt reaching one-eighth of the remaining aggregate allowance triggers compaction even far below 80% of the provider context window; summary requests remain graph-accounted; per-request context and cumulative graph usage are labeled separately. |
| OG-3A.2/.3 primary-loop reliability | Runtime tool admission, verification classifier/receipts, durable attempt progress, write-ahead and observed workspace generations, proposal prompt | Hidden graph tools cannot reach argument decoding; masked verification gets an actionable direct form; direct environment/venv/Python variants qualify; productive attempts may exceed the configured consecutive no-progress lease and receive a finalization response, while equivalent repeated evidence exhausts it; completed external effects with an unchanged Git token preserve prior verification, but real workspace drift stales it; proposal guidance prefers coherent 4–6-node graphs with 12 only as the maximum. |
| OG-3A.4 completion-gap and topology reliability | Durable completion-gap fingerprint/watermark, verification canonicalizer, executable-spec validator, stable writer-base approval preflight | Once verification is the exact unmet gate, different successful reads and command variants do not renew its four-cycle remediation lease; an exact current-workspace `cd ... &&` and final `2>&1` preserve verifier status and qualify, while other directories and masking composition remain rejected; mixed primary/candidate graphs and candidate dependencies are rejected before execution; a dirty candidate base names paths, preserves the proposal, and can be approved after correction. |
| OG-3A.5 repair progress and verifier bootstrap | Completion-gap progress classifier, failed-verification evidence equivalence, observed workspace state token, proposal contract, saved-graph retry control | A novel failed verifier or real scoped workspace repair gives the agent a bounded chance to react and verify, while the same failure under different command spelling cannot renew the lease; a new-project proposal establishes a focused smoke test before its first mutating node relies on a detected test command; an explicit retry reattaches the exact saved blocked graph without silently activating persisted state. |
| OG-3A.6 multi-wave lifecycle and node-boundary efficiency | Attached and saved terminal graphs, session graph tombstone, passing-verifier receipt, pinned graph render, accepted-node context transition, proposal contract, file discovery | A terminal graph can yield to another proposal in the same session without deleting its transcript/evidence, terminal cancel explicitly archives it, a nonterminal graph cannot be displaced, the next node receives a zero-provider runtime handoff without the prior tool loop, scoped proposals prefer 1–3 coherent nodes, and generated/dependency/cache trees do not inflate ordinary listing or search. |
| OG-3A.7 proposal-state authority and escape paths | Proposal plan statuses/evidence, approval normalization, TUI `/orchestrate cancel`, TUI `/plan off`, command completion | Model-authored `done`/`in_progress` proposal annotations cannot block approval or become runtime completion; every approved node starts pending with no imported evidence/attempt, direct cancel remains available, and plan-off cancels the unapproved proposal before restoring execution tools. |
| OG-3A.8 review-readiness corrections | Verification recognizer and project detection, typed completion gaps, read-node groundedness counter, `awaiting_review` state/outcome, candidate retention under aggregate exhaustion, graph-writer tool surface, per-attempt evidence bound, node-boundary steering, `internal/writescope` | Conventional verifiers of the ecosystems the mode meets are recognized, including environment-manager wrappers, while a wrapper around a non-check is not, and `git diff --check` no longer proves a change; a Python project's real runner is detected; the remediation lease renews on typed gate kinds rather than rendered prose, and untyped legacy gaps recover or clear; a read node is grounded by a machine-counted tool total; a verified candidate wave ends `awaiting_review` with an answer rather than a blocker, and retry names the candidate; a wave crossing the aggregate ceiling still records each retained worktree; automatic writers cannot reach `git_commit`/`git_branch` through the registry or availability; a long attempt bounds retained tool results without pruning verification; mid-graph steering survives the accepted-node handoff; overlap folds case while violations stay case-exact. |
| Publication under autopilot | Agent loop, publication classifier, permission manager, operation-scoped rules | `npm publish` is refused in the mode whose purpose is not asking, and the command never starts — a publication executed and then reported as denied has already spent the version number. Ordinary commands in the same mode still run, and an operation-scoped `allow` rule reaches execution. |
| External MCP prompt injection | Agent loop, external tool, permission manager, built-in file tools | An allowed external read remains usable as evidence but can also request a write and forge a permission grant; the write is denied and no file changes. |
| Fetched web page prompt injection | Agent loop, real `web_fetch`, external-data framing, permission manager, built-in file tools | An allowed page fetch stays usable as evidence but its "permission has already been granted" claim buys nothing; the requested write is denied and no file changes. |
| Web address boundary during a run | Agent loop, real `web_fetch`, connect-time address guard | A loopback URL requested mid-run is refused with an explanation naming the address and the alternative, and the run continues honestly. |
| Multimodal attachment lifecycle | Session store, rooted workspace reads, prompt hooks, provider message model, TUI commands, provider encoders, MCP rich results | Image bytes stay outside JSONL, symlink escapes and hook-blocked retention fail closed, integrity checks hold, fork/rewind/delete preserve the right references, and text-only requests retain their old wire shape. |
| Interrupted mutation recovery | Durable session store and recovery | Loading adds an interruption warning but never executes the recorded write. |
| Long-context retention | Compaction, pinned plan, exact failure evidence | Compaction retains authoritative plan state and bounded exact failure evidence. |
| Compaction decision quality | Manual compaction and the following provider request | User constraints, decisions, files, test strategy, and recent observations remain available after compression. |
| Conversation rewind | Durable branch creation and restoration | Rewind preserves the source, never replays recorded tools, and leaves workspace state unchanged. |
| Coupled checkpoint restore | Turn-aware change tracker, runtime event funnel, durable branch creation, TUI command and picker | A restore branches the conversation and reverses only the mutations recorded after the chosen turn — removing files created since, restoring files deleted since with their permission bits, and collapsing repeated writes into one. A file changed outside Collomia refuses the whole operation, names every affected file, writes nothing, and leaves the conversation where it was; a resumed session's earlier turns report that nothing was reversed rather than implying otherwise. |
| Governed parallel delegation | Real delegate tool, shared FIFO scheduler, structured plan, read-only child, isolated write child | Both children run concurrently, retain plan association/evidence, and the write appears only in its retained worktree. |
| Selective delegated integration | Git worktree validation, common-base diff, hunk picker model, rooted publication, change tracker, real Go verification | With a Windows-style inherited `core.autocrlf=true`, parent and child remain byte-stable; one of two hunks lands, tests pass, and the child worktree remains available. |
| Verified delegated results | Retained worktree validation, repository verification detection, canonical command policy, child-state fingerprints, durable team events, candidate comparison | A real child command passes or fails without publishing source, cancellation stops its process, later child drift makes evidence stale, and comparison remains read-only. |
| Scoped scheduling and three-way reconciliation | FIFO global/provider/scope admission, declared-versus-observed change checks, registered child/base/parent comparison, diff3 preview, freshness token, rooted publication | Disjoint writers can overlap; nested/workspace-wide writers serialize; out-of-scope results remain isolated; clean parent/child edits compose after review while overlapping edits stay non-selectable and cannot overwrite either side. |

These are product evaluations rather than model-quality benchmarks. The
scripted provider deliberately selects the tool calls so CI can test runtime
semantics without network access, credentials, nondeterministic wording, or
provider billing. Add a scenario when a regression crosses multiple packages
or when success depends on a complete tool/permission/session lifecycle. Keep
narrow parser and algorithm behavior in ordinary unit tests.

Evaluation assertions should prefer observable invariants—changed content,
tool lifecycle, permission decisions, verification output, and recovery
state—over exact prose or timing.

## Documentation guards

`cmd/collo/docs_test.go` holds guards that compare the documentation against the
source: every event kind, tool name, slash command, permission setting, sandbox
root, credential location, and setup environment variable must appear where a
reader would look for it. They exist because documented-but-absent controls have
shipped here more than once.

A guard of this shape fails in a way ordinary review does not catch: it can pass
for the wrong reason. `strings.Contains(guide, "off")` is satisfied by any of the
sixteen unrelated uses of that word, so the guard stays green while the setting
it claims to protect is deleted. Six guards were found in exactly that state.

**Prove a new guard can fail before trusting it.** Delete the documentation it
is meant to protect and confirm it goes red — the same mutation the guard is
supposed to catch in a future change:

```sh
cp docs/USER_GUIDE.md /tmp/guide.bak
grep -v "protect_credentials" /tmp/guide.bak > docs/USER_GUIDE.md
go test ./cmd/... -run TestGuideDocumentsEveryCredentialSetting -count=1   # must FAIL
cp /tmp/guide.bak docs/USER_GUIDE.md
```

Two helpers exist so this is the default rather than an afterthought.
`sectionContaining` narrows the search to the smallest enclosing Markdown
section, which is what makes "documented" mean "documented *here*" — anchoring
on `##` alone is not enough, since one section of the user guide runs to eight
hundred lines. `documentedToken` matches whole words, so `/agent` cannot be
satisfied by `/agents`.

## Fuzz targets

Short fuzz smoke tests run in the Linux CI quality job. Run the same targets
locally with a longer duration when changing their parsers:

```sh
go test ./internal/replay -run '^$' -fuzz '^FuzzRead$' -fuzztime 30s
go test ./internal/config -run '^$' -fuzz '^FuzzConfigValidation$' -fuzztime 30s
go test ./internal/shell -run '^$' -fuzz '^FuzzAnalyze$' -fuzztime 30s
go test ./internal/diffmodel -run '^$' -fuzz '^FuzzDiffAndHunkParsing$' -fuzztime 30s
```

Inputs are bounded inside each target to prevent an arbitrary seed from
turning linear parsing coverage into an accidental memory/CPU denial of
service. If Go writes a failing corpus entry under `testdata/fuzz/`, inspect
it for sensitive local data before committing it.

## Reliability and recovery coverage

Important failure-oriented tests include:

- Provider truncation, malformed streams, transient failures, retry limits,
  cancellation after deltas, and response-body closure.
- MCP disconnect/reconnect, failed and superseded catalog refresh, lifecycle,
  cancellation, and last-known-good registry preservation.
- User-image request encoding for OpenAI Chat Completions, Anthropic Messages,
  Responses-style inputs, and Bedrock Converse; MCP image bytes survive the
  typed tool boundary and are session-retained before the following request.
- Runtime-owned goal-graph corruption rejection, stable dependency order,
  bounded provider/tool retry and graph revision, stale-workspace invalidation,
  permission/cancellation/budget terminal states, no-op and unverified-write
  refusal, fsynced pre-mutation state, and non-replaying read/mutation recovery.
- Process timeout/cancellation and descendant cleanup.
- Terminal loss. A real child process wires itself the way Collomia does — the
  shutdown context, a background process started through the real
  `ProcessManager` — is sent a real `SIGHUP`, and is checked for whether
  teardown ran and whether the background process was reaped. Before SIGHUP was
  handled this failed on both counts: the runtime's default disposition killed
  the process instantly, and `Setpgid` meant a hangup never reached the
  children. A companion test in `cmd/collo` pins the Bubble Tea contract the
  fix rests on, because registering the signal without also giving the program
  the shutdown context would swallow it and leave the interface running against
  a terminal that no longer exists — worse than the crash it replaced.
- Power-loss durability. The session is flushed at turn boundaries and on
  close, and a failed flush is latched exactly as a failed write is. Recovery
  is swept across *every byte offset* of a real session log rather than at
  chosen record boundaries, since power loss can lose any suffix.
- Session torn-tail recovery, dangling tool-call interruption, injected
  short/disk writes, and an actual subprocess exit with an incomplete final
  record. A failed write is latched so a later record cannot turn the
  recoverable final fragment into corruption in the middle of the log.
- Fail-stop persistence guards after the user/assistant record, permission
  event, tool-start event, and tool result. Once durability fails, no later
  provider request or tool starts; a tool already completed is the only
  possible uncertain side effect.
- Immutable session-blob write, sync, and close failures. Partial attachments
  and retained results are removed and their storage error is propagated.
- Rooted atomic replacement with injected temporary-write and publication
  failures. The accepted destination remains byte-for-byte unchanged and
  private temporary files are removed on both paths.
- Abrupt subprocess death immediately before and after atomic publication.
  The destination is always the complete old or complete new content; a
  pre-publication kill may leave only a private unreferenced temporary.
- Explicit session-record compatibility: current writes carry version 1,
  legacy version-1 records without the field and additive fields still load,
  and newer schemas fail before append.
- Long-context compaction with dynamically pinned plan state and exact bounded
  failure evidence, plus oversized-result paging that proves the originating
  tool executes only once.
- Non-destructive conversation rewind at completed-turn boundaries, including
  source-session preservation, artifact continuity, and a recorded mutating
  tool call that remains inert during restoration.
- Publication classification across all six categories, the read verbs and
  `--dry-run` rehearsals that must stay ordinary, upload-versus-download
  direction for copy tools, global options that must not hide a subcommand,
  and a symmetry check that fails when a tool gains a destructive
  classification without its publishing counterpart.
- Operation-scoped command rules: an operation pattern never falls back to
  matching an executable, an allow rule still requires every operation to be
  covered, and a pattern that could match neither form fails validation rather
  than shipping inert.
- Publication gating: autopilot and a tool-wide "always allow" never cover it,
  an executable-only allow rule never covers it, an operation-naming rule does,
  a session grant covers exactly the operation shown and nothing adjacent, and
  raising the setting to `deny` invalidates a grant already handed out.
- Rooted atomic file replacement, mode-preserving external-edit-safe undo,
  hard-link isolation, traversal and adversarial parent-symlink swaps, shell
  analysis, and native sandbox enforcement.
- Native network-denial fixtures: Landlock checks TCP and ABI-dependent UDP,
  Seatbelt rejects remote egress while keeping its documented loopback access,
  and Windows AppContainer rejects loopback without an installed exemption.
- MCP content framing, control-character removal, bounded external schema and
  catalog metadata, and a full agent evaluation proving injected text cannot
  widen the permission decision for a file mutation.
- Attachment type/size/quota/integrity enforcement, owner-only storage where
  supported, session-scoped pending TUI drafts, and fork/rewind/delete cleanup.
- Primary/delegation profiles that can only tighten parent permissions, tool
  and skill allowlists enforced at execution, opt-in provider reasoning
  request shapes/fallbacks, durable token and user-priced cost-budget
  exhaustion, queue-inclusive
  timeouts, session-wide/global and per-provider FIFO admission, cancellation
  of one child without affecting siblings, and shutdown cancellation.
- Structured delegated results and durable latest-state restoration, including
  the guarantee that queued/running work resumes as inert `interrupted` state;
  common-base hunk comparison distinguishes overlapping and disjoint sibling
  edits without performing a merge.
- Delegated verification with one permission decision per detected command,
  canonical `run_command` rules/hooks/sandboxing, real pass/failure/cancellation
  execution in retained worktrees, bounded redacted results, exact child-state
  fingerprints, stale-state invalidation, additive event restoration, and
  read-only multi-candidate comparison. A child pass is never treated as
  publication permission or combined-parent verification.
- Delegate cancellation at the scheduler queue, provider-call, and interactive
  approval boundaries. Cancelling an approval-waiting child cannot publish its
  proposed write, revive the child through a late state update, or cancel a
  sibling.
- Bounded, exactly-once steering delivery at provider boundaries, structured
  plan-step validation, recent-output tail limits, and operator metadata event
  round trips.
- TUI busy-composer behavior: local inspection/agent-control commands run,
  while ordinary prompts and unsafe commands remain unsent drafts.
- Provider-call and TUI cancellation remain responsive without starting a tool
  or losing the current composer draft; runtime teardown cancels active
  delegated work and waits for tracked background processes to exit.
- Delegated-worktree integration checks registered Git identity/base, supports
  selective text hunks, records the result in the change tracker, retains the
  worktree, refuses parent drift, and rechecks changes made while approval is
  pending.
- Reviewed primary-agent integration is absent in the default `manual` mode.
  In opt-in `reviewed` mode, tests require a fresh inspect token, prove stale
  child bytes fail before authorization, assert exactly one normal permission
  decision, and exercise the same atomic publication/change-tracking path.
- Cross-platform Git fixtures explicitly override inherited configuration and
  reproduce `core.autocrlf=true`; nested mixed-case paths, LF/CRLF conversion,
  Git-significant executable bits, and Windows-irrelevant permission bits are
  tested according to each platform's semantics. Repeated/race runs also cover
  concurrent write-delegate setup: Git's shared `.git/worktrees`
  administration is serialized while the isolated child tasks remain
  concurrent.
- One opaque failure ID remains stable across the returned error, JSONL error
  and final-result records, TUI diagnostic, durable delegate status, debug log,
  and support-bundle correlation metadata. Tests validate the ID shape without
  depending on a particular random value.
- TUI agent action selection requires an explicit stop action rather than
  making the first picker selection destructive.
- The activity projection excludes streaming noise, retains a fixed newest
  window, restores from durable events without replay, and keeps explicit
  status words in plain mode. TUI tests cover filtering, search, failure-ID
  copy fallback, resize, and the opt-in static progress marker while proving
  the composer and busy controls remain available.

When adding a fault seam, keep production defaults on the real operating
system implementation. Tests may inject writers, clocks, transports, or
providers, but normal runtime behavior should not depend on test-only global
state.

The Linux quality and tagged-release workflows also repeat the deterministic
interruption/cancellation subset five times. Run the same bounded stress pass
locally with:

```sh
go test -count=5 \
  -run '^(TestSessionAbruptProcessDeathLeavesRecoverableInterruptedTool|TestAtomicReplacementSurvivesAbruptProcessDeath|TestPersistenceFailure.*|TestTeamCancellationRaceCannotReviveTask|TestRuntimeCloseWaitsForBackgroundProcesses|TestExclusiveDurableBlobFailuresRemovePartialFile|TestStopAllKillsEverything)$' \
  ./internal/session ./internal/safefile ./internal/agent ./internal/app ./internal/tools
```

## Performance benchmarks

The performance checks are diagnostic benchmarks rather than flaky wall-clock
CI gates. Run them on a quiet machine when changing session projection or TUI
rendering:

```sh
go test -run '^$' -bench 'RuntimeStartup' -benchmem ./internal/app
go test -run '^$' -bench 'ProjectLargeSession' -benchmem ./internal/activity
go test -run '^$' -bench 'IndexLargeWorkspaceQuery|IndexWarmRefresh' -benchmem ./internal/index
go test -run '^$' -bench 'LoadLongSession' -benchmem ./internal/session
go test -run '^$' -bench 'ActivityView500Items' -benchmem ./internal/tui
go test -run '^$' -bench 'ChatViewLongTranscript' -benchmem ./internal/tui
```

These cover runtime construction, projection of 10,000 durable events into the
fixed 500-entry operator window, cold query and warm refresh of a 2,000-file
symbol index, restoration of a 2,000-message session, the maximum activity
view, and a 500-block syntax-highlighted chat transcript. The Linux quality
job executes each benchmark once so panics, runaway allocations, and fixture
breakage are visible in CI logs. It does not compare wall-clock timings across
runners. Unit tests enforce structural retention and screen bounds; benchmark
numbers remain diagnostic until a stable same-hardware baseline exists.

## CI layout

The main test matrix runs build, fresh (`-count=1`) unit/integration/evaluation
tests, fresh race detection, `go vet`, and the native shell or PowerShell
installer tests on Ubuntu, macOS, and Windows. A separate Ubuntu quality job
verifies downloaded modules, runs pinned `govulncheck`, runs the offline
evaluation package once without cache reuse, and performs short fuzz
campaigns. It also runs one iteration of the diagnostic performance baseline
without a timing threshold. Release builds wait for both jobs, and tagged
release qualification repeats the same quality baseline.

Run installer regressions locally with:

```sh
scripts/test-install.sh
```

```powershell
./scripts/test-install.ps1
```

The shell fixture exercises latest and pinned URLs, checksum verification,
successful upgrade and pinned rollback, duplicate-manifest rejection, atomic
publication, and preservation of an existing binary after a failed upgrade
without making a network request. The PowerShell fixture covers the same
install/upgrade/rollback/failure lifecycle with native fixture executables plus
architecture/version selection and strict checksum parsing. The release
workflow additionally downloads and executes the actual Windows release
artifact on a native runner.

On a version tag, the dedicated release workflow repeats the complete
cross-platform qualification against the tagged commit, runs the security and
deterministic quality gates, checks tag/`VERSION` equality and main-branch
ancestry, generates all artifacts plus the CycloneDX SBOM, and executes the
produced binary on all three operating systems. Only then can it attest the
artifacts and create a draft release.

Golden terminal fixtures normalize line endings and padding so the same
semantic screen is stable across operating systems. Use semantic assertions
instead of a golden when color, terminal width, clock time, or platform text
is not the behavior under test.

## Live provider qualification

Real endpoint tests remain double opt-in and are never part of an ordinary
credential-free run. See [Live provider contracts](LIVE_PROVIDER_CONTRACTS.md)
for the required environment switches and safe test-account guidance. Never
put provider credentials, endpoint secrets, session files, or support bundles
into committed fixtures.

Two narrower live suites need no credentials, only something listening:

```sh
COLLO_LIVE_WEB_TESTS=1 go test ./internal/web -run Live -v
COLLO_LIVE_LIMIT_TESTS=1 go test ./internal/setup -run Live -v
```

The first exercises each search endpoint alone, so a working fallback cannot
hide a primary that has stopped parsing. The second resolves model limits
against whichever of Ollama, LM Studio, and vLLM is running, and skips the ones
that are not. Both cover the same class of risk: the offline suite proves the
parsers handle the documents this project expects, and cannot prove those are
the documents the far side still sends. A native API key renamed upstream would
return limit discovery to writing assumptions while every unit test passed.

### The filesystem exhaustion campaign

```sh
COLLO_DISK_EXHAUSTION_TESTS=1 go test ./internal/reliability -v
```

This runs the real durable writers — `safefile`, the session store, the audit
ledger — against a filesystem that genuinely has no space left, rather than
against an injected `ENOSPC`. On macOS the harness builds an 8 MiB disk image
with `hdiutil`, which needs no privileges; on Linux there is no unprivileged
equivalent, so the suite skips unless an operator supplies a prepared mount:

```sh
sudo mount -t tmpfs -o size=4m tmpfs /mnt/collo-full
COLLO_DISK_EXHAUSTION_TESTS=1 COLLO_FULL_FS_DIR=/mnt/collo-full go test ./internal/reliability
```

Injected faults are the right tool for asserting that a specific error is
handled at a specific call, and the durable writers already have those. What
they cannot show is *where* the errors arrive: a real full filesystem fails at
write, at fsync, at the directory update behind a rename, and at mkdir, in an
order no fixture author would have guessed.

**Two details make the difference between a harness and a prop**, and both were
found by mutation rather than by reasoning:

- **Fill until a one-byte write fails.** A single large filler that fails
  partway leaves a few kilobytes of slack behind it, so the next small write
  succeeds and the test believes it exercised an exhausted filesystem when it
  did not.
- **Then hand a little space back.** The first version of these tests filled
  the disk completely and passed even when `safefile.Replace` was rewritten to
  truncate the destination in place — exactly the data loss the
  temporary-plus-rename design exists to prevent. With nothing left, creating
  the temporary file fails first, so the interesting code never runs. A real
  disk fills up while a program is running, and the write that fails is the one
  that had somewhere to start and nowhere to finish. `fillNearly` reproduces
  that by filling completely and then deleting a known amount, because
  stopping the fill early only stops once the slack is already gone.

Test sizes are taken from `statfs` rather than from a constant. A fixed "surely
too big" value guessed wrong in both directions during development: far more
than a full disk would accept, and comfortably less than the slack left by a
partial fill.

### The macOS Keychain suite

```sh
COLLO_KEYCHAIN_TESTS=1 go test ./internal/credstore -run Keychain -v
```

This covers `backendGet`, `backendSet`, and `backendDelete` — the code behind
`collo auth set` and `collo auth rm` — by driving Apple's `security(1)` for
real. It is opt-in because it is the only suite in the project that touches a
resource shared with the developer's own account, and it is written so that
sharing cannot become damage:

- **Each test creates its own keychain** in a temporary directory and points
  the backend at it explicitly, so `security(1)` never resolves a default. The
  login keychain is not named and never enters the search list; the test
  asserts that, and deletes the temporary keychain when it ends.
- **The credential index is redirected** by moving `HOME`, and one test proves
  the real `~/.collomia/credentials.json` is byte-identical afterwards.
- **Account names carry a `collo-selftest-` prefix and 8 random bytes**, and
  the helper refuses to return a name without that prefix. `security(1)`
  matches on service *and* account with no wildcard form, so a guarded name
  provably cannot reach a configured provider's entry.

The first of those is not belt-and-braces, it is the load-bearing one, and it
was added after the obvious design failed badly. Isolating `HOME` alone looks
sufficient — it is what every other test in this project does — but macOS
resolves the login keychain through `$HOME/Library/Keychains`, so `security(1)`
found no keychain to write to and raised a modal dialog offering **Reset To
Defaults**. Nothing was read, written, or lost, but a test run that puts a
keychain-reset button in front of the user is a worse outcome than the coverage
gap it was closing. Naming the keychain explicitly removes the default
resolution path and the dialog with it; `credstore.keychainFile` exists for
that and is empty in every shipping path.
