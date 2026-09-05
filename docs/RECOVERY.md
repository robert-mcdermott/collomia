# Standard recovery and workspace checkpoints

W7b makes unfinished Standard work survive later turns and application restart.
It supports Developer and Work sessions, including ordinary non-Git folders.
The offline checks passed, and the user accepted W7b and full W7 after successful
manual testing on 2026-09-05. Evidence is recorded in
[the improvement plan](IMPROVEMENT_PLAN.md).

## Continuing unfinished work

Use `collo --cwd /path/to/folder --continue`, then send a continuation request.
The runtime retains changed paths needing verification, declared deliverables,
unresolved tool failures, and uncertain in-flight actions. Compaction does not
replace this state. A completed task clears its completion obligations, so later
unrelated questions do not inherit them. `/new` starts a separate task/session;
the previous session retains its unfinished work.

Saved state is **not a validation receipt or permission grant**. New turns require
fresh artifact validation where deliverables remain unfinished, and successful
recovery receipts must come from the current turn. A later plan cannot silently
drop a retained deliverable. Finish in the original task profile; switching modes
cannot bypass unfinished obligations. Ephemeral runs retain their existing
in-process behavior and do not offer durable recovery.

`/recovery` displays runtime obligations and checkpoint availability. `/context task` displays the separate W7a model-authored notes; editing those notes does
not change runtime obligations. A recorded next action never schedules itself.

## Uncertain actions

Before a potentially mutating Standard tool executes, Collo persists and syncs
an in-flight marker. A successful observed result settles it. If the process
stops before settlement, or an opaque command/external tool fails after starting,
the runtime keeps the outcome uncertain. It does not replay that action. Known
read-only tools can inspect the current state, while another possibly mutating
call is refused. The final outcome remains blocked until user reconciliation.

After inspecting the actual result, use:

```text
/recovery acknowledge Inspected the output; one action occurred and I am keeping it
```

This records your reason, keeps current files, discards the old workspace
checkpoint history that cannot promise complete coverage of the uncertain action,
and releases the uncertainty block. Changed paths, failed-tool obligations, and
required validation remain. This is not permission for another external action.
If appropriate, the model can now record a grounded failure disposition or obtain
fresh verification through the usual tool and permission paths.

`/recovery keep REASON` keeps the workspace and discards earlier checkpoint
history without acknowledging an uncertain Standard tool action. Use it after
an interrupted workspace restore once you have inspected the partial result.
Reconciliation commands run only between turns. They do not write workspace files.

## Restoring tracked file changes

`/restore [turn]` branches the conversation at a completed turn and reverses the
retained tracked file changes after that turn. `/undo` reverses the latest retained
file mutation. Both work after restart. `/rewind [turn]` still branches only the
conversation, preserving the current workspace.

Checkpoint records retain exact binary bytes, file existence, and permission
modes. Restoration checks current bytes and modes before changing files, rejects
path escape, and binds the saved checkpoint to the actual workspace directory's
identity. Replacing the workspace directory at the same pathname does not make
old checkpoints valid for it. An ordinary external edit refuses the restore before
the conversation moves and names the changed files.

A restore/undo itself is journaled before the first file changes. A crash or I/O
failure during application can leave a partial result; that is explicitly marked
as interrupted. Subsequent agent work and further restore/undo attempts are blocked
until you inspect it and use `/recovery keep REASON`. The runtime never silently
finishes an interrupted multi-file restore. Existing graph integration recovery
continues to use `/restore integration` and its separate authority contract.

## Bounds and coverage

- The current checkpoint projection retains up to **256 mutations**, **1 MiB per
  file side**, and **8 MiB of prior/result bytes in total**. Larger entries are
  marked unavailable; older dropped entries advance the oldest restorable turn.
  A restore requiring missing coverage is refused. The picker and `/recovery`
  report availability. Binary bytes use base64 in the durable log.
- Checkpoint deltas append changed entries, avoiding repeated copies of the entire
  file history. These limits bound the current recoverable projection, not the
  cumulative append-only session log, which retains historical records.
- Only tracked workspace file edits have reversible bytes. Shell commands,
  external/MCP effects, and authorized writes outside the workspace are not full
  workspace snapshots. An outside-workspace tracked write advances the recovery
  coverage floor. Ordinary shell effects are never undone by `/restore`.
- A crash between a file change and recording its checkpoint can lose that file's
  reversible bytes. The durable in-flight marker preserves the uncertainty rather
  than claiming full recovery. User acknowledgement retains current files and
  explicitly discards the incomplete checkpoint history.
- Sessions created before W7b have no retroactive file checkpoints or runtime
  completion receipts. Their recoverable checkpoint history begins at resume;
  earlier conversation remains available through `/rewind` and history tools.
- Completion state is capped at 128 paths, 64 artifact roles, 64 failed operations,
  and 128 KiB encoded JSON. Overflow remains a gap; it cannot silently establish
  completion. Durable workspace projections are capped at 16 MiB encoded JSON.
- No background continuation, external-effect rollback, automatic approval,
  unattended crash recovery, or cross-project memory is added.

## Manual acceptance checks

Prepared binary: `dist/collo-wave7b`, version `v0.4.2-wave7b`, based on W7a commit
`0f618aa`. If needed, rebuild from the repository root:

```sh
go build -o dist/collo-wave7b -ldflags '-X github.com/robert-mcdermott/collomia/internal/version.Version=v0.4.2-wave7b -X github.com/robert-mcdermott/collomia/internal/version.Commit=0f618aa-wave7b-dirty' ./cmd/collo
```

### 1. Unfinished deliverable across restart

```sh
mkdir -p dist/wave7b-obligations
./dist/collo-wave7b --mode work --cwd dist/wave7b-obligations
```

Send:

> Use update_plan to declare report.txt in artifacts with role deliverable.
> Write report.txt containing DRAFT using write_file. For this recovery test,
> intentionally leave validation unfinished and end the turn. Do not validate,
> remove or demote the deliverable, or add a validation exception.

Expect an unfinished outcome (possibly controller interventions before stopping),
not an accepted done. `/recovery` should retain the deliverable and any dirty path.
Quit, then resume:

```sh
./dist/collo-wave7b --cwd dist/wave7b-obligations --continue
```

Inspect `/recovery` again. Send:

> Finish the previous task. Inspect the current file and validate report.txt
> requiring DRAFT, then complete the plan and report the result.

Expect a fresh `validate_artifact` call and accepted completion. `/recovery`
should have no outstanding completion paths/roles/failures. A subsequent simple
question should work normally. Repeat with a Developer task requiring its normal
verification if you regularly use that profile.

### 2. Checkpoints, external edits, and restart

Start a fresh fixture/session so the two prompts below are turns 1 and 2:

```sh
mkdir -p dist/wave7b-checkpoints
printf 'ORIGINAL\n' > dist/wave7b-checkpoints/checkpoint.txt
./dist/collo-wave7b --mode work --cwd dist/wave7b-checkpoints
```

First prompt:

> Use write_file to replace checkpoint.txt with exactly five bytes FIRST (no
> newline), then validate that file
> requiring FIRST. Finish this task.

Second prompt:

> Use write_file to replace checkpoint.txt with exactly six bytes SECOND (no
> newline), then validate that file
> requiring SECOND. Finish this task.

Quit. Introduce a deliberate outside edit and resume:

```sh
printf 'MANUAL\n' > dist/wave7b-checkpoints/checkpoint.txt
./dist/collo-wave7b --cwd dist/wave7b-checkpoints --continue
```

Run `/restore 1`. Expect refusal naming checkpoint.txt; MANUAL and the current
session must remain unchanged. Quit and put back the exact bytes the second
write_file wrote (normally SECOND with no newline; match its actual output):

```sh
printf 'SECOND' > dist/wave7b-checkpoints/checkpoint.txt
./dist/collo-wave7b --cwd dist/wave7b-checkpoints --continue
```

Run `/restore 1` again. Expect a new conversation branch and file contents FIRST.
Quit/resume the branch and inspect `/recovery`: the reversed second-turn write
must not reappear as a pending change. Run `/new`; prior checkpoint history and
completion obligations must be absent.

### 3. Uncertain command outcome

Use another fresh Work session under a fixture directory:

```sh
mkdir -p dist/wave7b-uncertain
./dist/collo-wave7b --mode work --cwd dist/wave7b-uncertain
```

Send (approve this harmless fixture command if asked):

> Call run_command exactly once with: printf 'ONCE\n' >> uncertain.txt; false
> The nonzero exit is intentional. Do not retry it or append again.

Expect a tool failure and `/recovery` showing an uncertain run_command. Quit and
resume with the same cwd and `--continue`. Send:

> Use read_file to inspect uncertain.txt and explain the retained uncertainty.
> Do not run any command or change any files.

Expect exactly one ONCE line. The state should still be uncertain and blocked;
reading or a confident model answer cannot clear it. After inspecting it, run:

```text
/recovery acknowledge Inspected uncertain.txt; exactly one append occurred; keep it
```

Expect the pending marker to clear, the reason to be recorded, checkpoint history
to be discarded, and remaining completion/failure obligations to stay visible.
No second append should occur. An attempted mutating tool before acknowledgement
must be refused; this is also covered by offline regressions.

### 4. Cancellation

During a separate task, press Esc (or Ctrl+C once) after a write_file result and before validation
finishes. Quit and resume that session. `/recovery` should retain unfinished
validation and the file checkpoint. Ask Collo to finish validation; expect fresh
evidence rather than losing the obligation or repeating the write. Cancellation
inside a running command may instead leave an uncertain action, requiring the
inspection flow above.

These checks passed the user gate on 2026-09-05 and remain a regression guide.
Report the provider/model and failing step if any check fails. W7b/full W7 is
accepted; W6 remains a later, separately authorized wave.
