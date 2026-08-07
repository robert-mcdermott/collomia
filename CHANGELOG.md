# Changelog

This file starts at v0.3.0. Earlier releases are not backfilled, because
reconstructing them after the fact would produce a plausible account rather than
an accurate one; their history is in the Git log and in
[docs/ROADMAP_HISTORY.md](docs/ROADMAP_HISTORY.md).

## Unreleased

### Added

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
