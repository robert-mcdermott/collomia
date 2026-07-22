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
cross-compilation in CI still type-checks platform-specific code.

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
| Permission refusal | Agent loop, command analysis, permission manager | A headless `ask` run records denial, never starts the command, and continues with an honest answer. |
| External MCP prompt injection | Agent loop, external tool, permission manager, built-in file tools | An allowed external read remains usable as evidence but can also request a write and forge a permission grant; the write is denied and no file changes. |
| Multimodal attachment lifecycle | Session store, rooted workspace reads, prompt hooks, provider message model, TUI commands, provider encoders, MCP rich results | Image bytes stay outside JSONL, symlink escapes and hook-blocked retention fail closed, integrity checks hold, fork/rewind/delete preserve the right references, and text-only requests retain their old wire shape. |
| Interrupted mutation recovery | Durable session store and recovery | Loading adds an interruption warning but never executes the recorded write. |
| Long-context retention | Compaction, pinned plan, exact failure evidence | Compaction retains authoritative plan state and bounded exact failure evidence. |
| Conversation rewind | Durable branch creation and restoration | Rewind preserves the source, never replays recorded tools, and leaves workspace state unchanged. |
| Governed parallel delegation | Real delegate tool, shared FIFO scheduler, structured plan, read-only child, isolated write child | Both children run concurrently, retain plan association/evidence, and the write appears only in its retained worktree. |
| Selective delegated integration | Git worktree validation, common-base diff, hunk picker model, rooted publication, change tracker, real Go verification | With a Windows-style inherited `core.autocrlf=true`, parent and child remain byte-stable; one of two hunks lands, tests pass, and the child worktree remains available. |

These are product evaluations rather than model-quality benchmarks. The
scripted provider deliberately selects the tool calls so CI can test runtime
semantics without network access, credentials, nondeterministic wording, or
provider billing. Add a scenario when a regression crosses multiple packages
or when success depends on a complete tool/permission/session lifecycle. Keep
narrow parser and algorithm behavior in ordinary unit tests.

Evaluation assertions should prefer observable invariants—changed content,
tool lifecycle, permission decisions, verification output, and recovery
state—over exact prose or timing.

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
- Process timeout/cancellation and descendant cleanup.
- Session torn-tail recovery, dangling tool-call interruption, and injected
  short writes. A short write is latched so a later record cannot turn the
  recoverable final fragment into corruption in the middle of the log.
- Long-context compaction with dynamically pinned plan state and exact bounded
  failure evidence, plus oversized-result paging that proves the originating
  tool executes only once.
- Non-destructive conversation rewind at completed-turn boundaries, including
  source-session preservation, artifact continuity, and a recorded mutating
  tool call that remains inert during restoration.
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
- Delegation profiles that can only tighten parent permissions, tool and skill
  allowlists enforced at execution, token-budget exhaustion, queue-inclusive
  timeouts, session-wide/global and per-provider FIFO admission, cancellation
  of one child without affecting siblings, and shutdown cancellation.
- Structured delegated results and durable latest-state restoration, including
  the guarantee that queued/running work resumes as inert `interrupted` state;
  common-base hunk comparison distinguishes overlapping and disjoint sibling
  edits without performing a merge.
- Delegate cancellation at the scheduler queue, provider-call, and interactive
  approval boundaries. Cancelling an approval-waiting child cannot publish its
  proposed write, revive the child through a late state update, or cancel a
  sibling.
- Bounded, exactly-once steering delivery at provider boundaries, structured
  plan-step validation, recent-output tail limits, and operator metadata event
  round trips.
- TUI busy-composer behavior: local inspection/agent-control commands run,
  while ordinary prompts and unsafe commands remain unsent drafts.
- Delegated-worktree integration checks registered Git identity/base, supports
  selective text hunks, records the result in the change tracker, retains the
  worktree, refuses parent drift, and rechecks changes made while approval is
  pending.
- Cross-platform Git fixtures explicitly override inherited configuration and
  reproduce `core.autocrlf=true`; nested mixed-case paths, LF/CRLF conversion,
  Git-significant executable bits, and Windows-irrelevant permission bits are
  tested according to each platform's semantics.
- One opaque failure ID remains stable across the returned error, JSONL error
  and final-result records, TUI diagnostic, durable delegate status, debug log,
  and support-bundle correlation metadata. Tests validate the ID shape without
  depending on a particular random value.
- TUI agent action selection requires an explicit stop action rather than
  making the first picker selection destructive.

When adding a fault seam, keep production defaults on the real operating
system implementation. Tests may inject writers, clocks, transports, or
providers, but normal runtime behavior should not depend on test-only global
state.

## CI layout

The main test matrix runs build, fresh (`-count=1`) unit/integration/evaluation
tests, fresh race detection, and `go vet` on Ubuntu, macOS, and Windows. A
separate Ubuntu quality job runs the offline evaluation package once without
cache reuse and performs short fuzz campaigns. Release builds wait for both
jobs.

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
