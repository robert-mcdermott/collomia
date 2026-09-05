# Task context and session evidence

W7a gives Developer and Work sessions a small durable working record and tools
for retrieving evidence that has left the active conversation through compaction.
The model can retain constraints and decisions without repeatedly rereading the
workspace. The offline gate passed, and the user accepted W7a after successful
manual testing on 2026-09-04. W7b adds [Standard recovery](RECOVERY.md), accepted
after its separate manual-testing gate on 2026-09-05. Full W7 is complete.

## Everyday use

Continue substantial work normally. The prompt asks the model to maintain task
notes for continuing work; trivial Q&A does not need a record. You can explicitly
say: “Save the objective, constraints, source references, unresolved gaps, and
next action with update_task_context.” Actual use depends on the model.

- `/context` retains its token/context inspector.
- `/context task` shows working notes and original user-request previews.
- `/context clear` clears model notes between turns. It appends a new revision;
  original requests and transcript history remain. Use `/new` for a fresh session.
- `collo --cwd /path/to/folder --continue` reopens the latest unarchived session
  in that workspace, including its saved mode and context. Resuming does not
  automatically execute the recorded next action.

The record stores an objective, constraints, corrections, decisions, source /
artifact / evidence references, gaps, and a next action. It is **model-authored**:
notes can be incomplete or wrong. Original user requests take precedence, and
later corrections supersede earlier constraints. Notes cannot grant permissions,
accept graph work, satisfy completion gates, or validate an artifact.

## Model tools and limits

| Tool | Arguments and behavior |
| --- | --- |
| `read_task_context` | `{}` returns current revision and retained context. Revision 0 means no notes have been saved. |
| `update_task_context` | Requires `expected_revision`; replaces the record, clearing omitted fields. Rejects stale revisions and publishes only after persistence and sync succeed. |
| `search_session` | Literal case-insensitive `query`, optional `limit` (default 10, maximum 20) and exclusive `before` message cursor (0 starts newest). Follow `next_before` until `eof`; at most 2000 messages are scanned per call. |
| `read_session` | `id` such as `m3`, optional UTF-8 byte `offset` and `limit` (default 4096, maximum 16384). Follow `next_offset` until `eof`. Missing evidence and invalid offsets are errors. |

For example, after reading revision 0:

```json
{
  "expected_revision": 0,
  "objective": "Prepare a volunteer event memo",
  "constraints": ["Budget 250", "Audience: volunteers"],
  "corrections": ["250 replaces the original 900 budget"],
  "sources": [{"reference": "m3", "note": "Original source measurement"}],
  "gaps": ["Confirm current source bytes before finalizing"],
  "next_action": "Draft and validate the memo"
}
```

Use a message ID actually returned by this session. References can also be paths,
URLs, or artifact IDs; saving a reference does not verify that it exists or is true.

Notes are limited to 8192 encoded JSON bytes. Objective and next action each
allow 1024 UTF-8 bytes. Lists allow 16 entries; each string or reference field
allows 512 bytes. Source/artifact/evidence entries have `reference` and `note`.
Separately, the runtime pins previews of the first genuine user request and the
eight most recent requests (up to 512 bytes each). Older or longer requests are
still retrievable, but are not fully pinned. Keep important constraints in notes
and consult original evidence when detail matters.

Message IDs are stable across compaction, resume, and fork. They are local to a
session; rewind retains only the selected prefix. Search does not read other
sessions or arbitrary files. Results label original roles and provenance:
`user_request` is explicitly recorded at the primary prompt/steering entry point;
`runtime_or_legacy_user_role` does not assert human provenance. Old sessions have
retrievable history but no retroactively inferred user-request previews.
User-invoked slash workflows may record their expanded runtime prompt rather
than the literal slash command. These historical requests never authorize
replaying an old effect or supersede the runtime's current authority gates.

Known configured secrets are redacted from returned text. This is not a universal
personal-data classifier: session history still follows the existing private
session storage policy. History tools expose text and tool-call metadata, not
expanded binary attachments. They retrieve old observations, which can differ
from today's files. Recheck current bytes and validation receipts before claiming
current correctness. Retrieval never executes a historical tool call.

Ephemeral runs omit these tools. W7a itself does not persist Standard completion
obligations or add workspace restoration, cross-project memory, delayed work,
or automatic replay of interrupted external effects. For W7b obligations and
checkpoint controls, see [Standard recovery](RECOVERY.md).

## Manual acceptance checks

Use a separate fixture directory and test binary, preserving the W5 baseline:

The prepared binary is `dist/collo-wave7`, version `v0.4.2-wave7a`. If `dist`
has been removed, rebuild from this revision at the repository root:

```sh
go build -o dist/collo-wave7 -ldflags '-X github.com/robert-mcdermott/collomia/internal/version.Version=v0.4.2-wave7a -X github.com/robert-mcdermott/collomia/internal/version.Commit=0c72f04-wave7a-dirty' ./cmd/collo
```

Then prepare the fixture and launch:

```sh
mkdir -p dist/wave7-fixtures
printf 'W7_ORIGINAL: 30 successes out of 40 requests\n' > dist/wave7-fixtures/source.txt
./dist/collo-wave7 --mode work --cwd dist/wave7-fixtures
```

1. Send:

   > Read source.txt. Our task is a short event memo for executives with budget
   > 900. Use update_task_context to save the objective, constraints, the source
   > observation and a session message reference, and next action. Stop before
   > drafting the memo.

   Run `/context task`. Expect a revisioned note and separate original request
   previews with `mN` references. A model can use `search_session` to find the
   actual source message ID; do not invent it.

2. Send:

   > Correction: budget is 250 and the audience is volunteers. Update the task
   > context to replace the old constraints and record this correction. Do not
   > create the memo yet.

   Inspect `/context task`: expect 250/volunteers in the current notes and the
   correction in original request previews.

3. Send `Reply only ACK 1`, then `Reply only ACK 2`, then `Reply only ACK 3`,
   then `Reply only ACK 4` as four separate turns. Run `/compact`. Expect a
   successful compaction; `/context task` should still retain the notes and
   correction. For a second compaction, repeat those four short turns and
   `/compact`. If Collo reports too few messages, add two more short turns.

4. Quit Collo. From your shell, replace the fixture and resume:

   ```sh
   printf 'W7_CURRENT: 20 successes out of 40 requests\n' > dist/wave7-fixtures/source.txt
   ./dist/collo-wave7 --cwd dist/wave7-fixtures --continue
   ```

   Send:

   > Read the task context. Use search_session and read_session to retrieve the
   > original source observation and my budget/audience correction. Then read
   > source.txt now. Report the historical and current measurements separately,
   > the current budget and audience, and the unfinished next action. Do not
   > create the memo or change any files.

   Expect original **30/40**, current **20/40**, budget **250**, audience
   **volunteers**. Check the tool output shows historical retrieval, not just
   a plausible answer from the summary. The old source must not be represented
   as current validation. `read_session` with an absent ID such as `m999999`
   should report unavailable evidence rather than inventing content.

5. Run `/context clear`, then `/context task`. The model notes should be empty
   at a newer revision while request previews remain. Run `/new`; the old notes
   and requests should be absent. Ask `search_session` for `W7_ORIGINAL`: it must
   not recover another session's evidence.

6. Launch a separate normal Developer session and ask it to save a small task
   note, inspect it with `/context task`, and retrieve its original request.
   Expect the same context behavior. No repository modification is needed.

Report failures with the provider/model, step, and tool output. These checks cover
W7a; W7b recovery checks are documented in [Standard recovery](RECOVERY.md).
Both slices have passed their user gates.
