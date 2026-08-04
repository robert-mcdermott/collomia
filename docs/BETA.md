# Collomia beta status and known limitations

Collomia is suitable for a public **technical beta** aimed at developers who
want an interactive, inspectable terminal coding agent. Beta means the core
permission, session, provider, editing, MCP, and multi-agent paths are usable
and heavily tested; it does not mean unattended execution is risk-free or that
every roadmap feature is complete.

## Appropriate beta use

- Interactive repository work with `permissions.mode: "ask"`.
- Reviewable workspace edits backed by Git and Collomia's diff/undo tools.
- Provider, MCP, skills, hooks, LSP, web lookup, and headless evaluation in
  non-production environments.
- Default `auto` sandboxing after reviewing the platform-specific behavior and
  granting only the dependency/cache roots a toolchain actually needs.

Keep valuable repositories backed up, review permission dialogs and diffs, and
use ordinary Git branches for recoverability. Start with non-sensitive projects
when evaluating new providers, MCP servers, hooks, skills, or agent profiles.

## Important limitations

- Sandboxing defaults to capability-aware `auto`, while command network access
  and broad command reads remain available for compatibility. External caches
  may need narrow writable grants, sandboxed commands receive the minimal
  environment by default, and Windows AppContainer always confines user-data
  reads and blocks ordinary unpackaged localhost services. `auto` warns and
  continues when a backend is unavailable; use `require` for fail-closed
  operation.
- Endpoint-scoped policy for commands — host rules, the `network: "scoped"`
  posture, and per-capability session grants — describes only the endpoints a
  command's own text names. A program that opens a socket without naming it on
  a command line is invisible to that layer. OS-enforced per-host egress does
  exist as opt-in `sandbox_egress: "scoped"`, but it is experimental and macOS
  only: Landlock filters TCP by port rather than address, and Windows
  AppContainer cannot reach a loopback broker at all, so neither platform gets
  an enforcement claim it could not keep. The built-in `web_search` and
  `web_fetch` tools are the exception to all of this — Collomia opens those
  connections itself and enforces a public-internet-only address boundary that
  no configuration can disable.
- Credential files are protected by conventional location, not by detecting
  secret material. Reaching one prompts by default and is not covered by a
  broad approval, but a key stored somewhere unconventional is not recognized,
  and the check describes what a command's text names rather than what a
  running process opens. Redaction does not sit between a tool result and the
  provider, so a secret an agent legitimately reads still reaches the model.
- `autopilot` is not a promise that arbitrary commands are safe. Built-in
  catastrophic denials, policy, and OS sandboxing reduce risk but do not replace
  review, backups, source control, or host isolation. It also does not approve
  everything: actions classified as external risk, which includes MCP tool
  calls and both web tools, still require a rule, a session grant, or a person —
  and neither does it approve publishing.
- Publishing, deploying, and pushing require their own decision by default
  (`permissions.publication`), covering package and container registries,
  source remotes, code-forge writes, infrastructure applies, and commands run
  on another host. This is a policy layer, not enforcement: it reads what a
  command's text says it will do, so a build script that uploads an artifact
  without naming the operation is outside its view, its catalogue of publishing
  tools is finite, and it cannot distinguish `kubectl apply` against a local
  cluster from the same command against production. Set it to `off` to restore
  the behavior of earlier releases exactly.
- macOS and Windows release binaries are not yet platform code-signed or Apple
  notarized. Release provenance is signed through GitHub/Sigstore instead.
- `run_command` with `pty: true` and `collo --web` now work on every supported
  platform. Windows uses a pseudoconsole, which requires Windows 10 1809 or
  later; on anything older the console cannot be created and the command
  reports that rather than running without terminal semantics. Windows has no
  signal equivalent to SIGTERM, so cancelling a pseudoconsole session closes
  the child's console input and terminates the job after a short grace period
  rather than asking politely first.
- MCP OAuth, experimental tasks, resource subscriptions, audio passthrough,
  annotations, and argument-level permission matching remain incomplete.
- LSP code actions are not implemented. Diagnostics, `find_definition`,
  `find_references`, `format_file`, and the lexical symbol index are available
  now, and each language server must be installed for its language.
- The built-in web tools read the public web and nothing else. They cannot
  reach a local development server, an intranet host, or a cloud metadata
  endpoint, and that boundary has no configuration escape — use `run_command`
  with `curl` for anything inside your own network. They do not run
  JavaScript, so a page that renders its content client-side comes back as a
  near-empty shell with a note saying so rather than as the article. Search
  parses DuckDuckGo's HTML endpoints, which can change without notice; a
  layout change is reported as an engine failure rather than as "no results",
  and bursts of searches are rate limited per address. Page content is
  untrusted external data: it arrives inside a provenance frame, but framing
  guides a model rather than constraining it, and the controls that actually
  hold are the permission pipeline and the address boundary.
- Multi-agent work is isolated and selectively integrated, but Collomia does
  not automatically reconcile conflicts or resume pending child work. The
  runtime-owned goal graph can now be tried as a TUI-only experimental
  **evidence-gated durable execution** mode: `/orchestrate <goal>` creates a
  read-only proposal and only the separate `/orchestrate approve` action
  executes it. The model cannot mark its own claim done; the runtime owns
  readiness, attempts, evidence freshness, recovery treatment, and terminal
  state. Status, node evidence, cancellation, and explicit saved-graph resume
  are inspectable. At most two
  independently ready approved `read_only` nodes can run through automatic
  governed workers before execution returns to the one serial primary lane.
  Workers cannot write, run commands, recurse, update the plan, or control the
  graph; their prose is accepted only with successful tool evidence and a
  fresh workspace token. A mutation requires recognized verification against
  the current Git state, and an ambiguous interrupted mutation is blocked
  instead of replayed. Recognized verification covers the conventional
  build/lint/test entry points of the ecosystems Collomia meets — including
  `uv`, `poetry`, `pipenv`, `conda`, `tox`/`nox`, R, Ruby, Elixir, PHP, Swift,
  CMake, Deno, Haskell, Bazel, and task runners — because a mutating node
  blocks honestly when it cannot verify, so a missing ecosystem would mean the
  mode could not finish work there. A wrapper counts only when what it wraps is
  itself a recognized check, and a command that passes on almost any tree, such
  as `git diff --check`, is not verification of a change. Cooperative pause/resume is available at the next safe
  provider/scheduler boundary, and a blocked node can be retried only when its
  attempt budget and non-replay checks allow it; whole-graph cancel remains
  immediate. The whole-graph envelope charges new work rather than prompt tokens
  the provider served from cache, so a long node's re-sent context does not
  exhaust it, and compaction will not repeat on a context it has already
  reduced. The envelope's four bounds default to the values above and are set
  in `options.orchestration_*`. Each is a speed bump rather than a wall: if one
  is reached, the accepted nodes and retained candidates stand and
  `/orchestrate extend` grants another envelope of the same size, as often as
  the user decides to; unfinished nodes resume in new attempts, so nothing
  interrupted is replayed. Repository text, hooks, skills, and the model cannot
  widen it, and permission, verification, write-scope, and publication remain
  safety gates that no envelope affects. A terminal graph yields to a fresh `/orchestrate <goal>` in the
  same session through an append-only tombstone, and cancel on an already-
  terminal graph performs the same archive action without deleting evidence.
  Proposal-time `done` or `in_progress` annotations are never imported as
  runtime state: approval starts every node pending with no model-authored
  evidence. `/plan off` safely cancels an unapproved proposal and restores
  execution mode; it does not approve or execute the saved plan.
  An explicit retry can reattach its saved blocked graph directly
  after the exact conversation is reopened; restored bytes remain inert until
  that user action. Status durably separates proposal-plus-primary and automatic-read
  iterations, tokens, honest price availability, estimated cost, elapsed
  time, and active time. A whole-graph envelope limits the preview to 96
  provider iterations, 1,000,000 tokens, $5 estimated cost when pricing is
  complete, and 30 minutes of active post-approval execution; each is settable
  through `options.orchestration_*`, and only implausible values are refused.
  Older saved graphs retain their stored ceiling. The ordinary `max_iterations` value is a
  provider-response-cycle limit, not a tool-call count. In this mode it bounds
  consecutive cycles without novel durable successful tool evidence inside a
  primary attempt; productive evidence renews that lease, equivalent repeated
  evidence does not, and 96 remains the whole-graph outer limit. Once an exact
  completion gap is open, a separate four-cycle lease renews for an actual
  workspace repair, a novel machine-observed verification failure, or evidence
  that closes the gate. Identical failures, command variation, and unrelated
  output do not prolong it. Proposal guidance prefers one to three nodes for a
  scoped change and four to six only for broad work, and requires the first mutating node
  in a project without an applicable test surface to create a focused smoke
  test. Passing verification is acknowledged as recorded against the current workspace, and
  later process/network actions do not stale it unless the observed repository
  token changes. The receipt also tells the model to stop at the current node;
  after runtime acceptance, a zero-provider handoff replaces that node's active
  tool-loop context while the complete transcript remains durable. Approval
  compacts proposal history once,
  and later cumulative-budget pressure can compact again; these requests are
  counted rather than treated as free work. Controlled
  comparisons retain two-worker fan-out for substantive independent reads
  while showing its extra model work; trivial and dependency-serial work stays
  serial. End-to-end change graphs use the primary lane. An explicitly
  candidate-only graph can instead declare narrowly scoped terminal
  `isolated_write` nodes; it cannot mix those retained candidates with primary
  work or make later nodes depend on them. Approval first checks the clean Git
  base and names dirty paths without consuming the proposal. One bounded wave starts at most two ready,
  pairwise-disjoint writers from the same clean commit, keeps them in isolated
  worktrees, and requires ordinary dispatch permission plus fresh detected
  child verification. Automatic writers cannot commit or create branches even
  inside their own worktree. Passing candidates stop the graph in a distinct
  `awaiting_review` state — reported as a finished run naming the review step,
  not as a blocker — and Collomia does not select or integrate them; the parent
  workspace remains unchanged. Every worktree the runtime creates stays
  attributable to its node and attempt however the wave ends: identity is
  recorded durably the moment Git creates the tree, and a wave that is
  cancelled or exhausts the aggregate budget still records where each one is.
  `/orchestrate status` lists them for the whole graph, live or saved, and says
  when the runtime never examined one. An interrupted writer is blocked rather
  than replayed, and recovery names the exact orphaned worktree and branch.
  `/orchestrate reconcile` then answers what is actually left in each one —
  present, empty, missing, no longer registered with Git, or unmoored from the
  base commit its claim recorded — and stores that answer durably, so a
  remembered path is never reported as though someone had just checked it.
  `/orchestrate discard <node-id>` removes a tree you no longer want: it
  refuses one nobody has reconciled, asks you to repeat the command with
  `confirm` when the tree still holds changes, and declines outright to delete
  a directory Git no longer registers, since that would be a recursive delete
  of a path rather than a Git operation. Archiving a terminal graph waits until
  its worktrees have been observed, because the graph is the only thing that
  knows they exist. Nothing reuses a candidate: that is still review work you
  do yourself, and `/agents apply` refuses a graph candidate rather than
  publishing it behind the graph's back — you can review it there, but not
  apply it. There is
  deterministic feedback when a verification-like shell command cannot count
  as evidence, naming the direct command to run wherever the check sits in
  what was refused, and a node that stalls after refused checks repeats it in
  the blocker. A composed command qualifies when the shell provably reports the
  verifier's own status — an `&&` chain ending in the check, so preparation
  such as a sandbox-required cache redirect or a virtualenv activation is fine
  — while `||`, `;`, pipelines, backgrounding, redirection, a check that is not
  last, and a leading segment that would run it outside the workspace do not. Graph-hidden plan/delegation tools are blocked again before
  their arguments are decoded. There is
  no optional-branch/node cancellation, automatic candidate integration, or
  exact multi-worker recovery. Configuration, a repository, a saved graph,
  and a headless flag still cannot enable the preview. Standard execution
  remains the default.
- Prompt caching is requested on the Anthropic Messages routes only, with the
  provider's default five-minute lifetime, so a session resumed after a longer
  pause pays a full uncached prompt again. OpenAI-family endpoints cache
  implicitly and need nothing from Collomia. Bedrock is declared without cache
  support on purpose: its cache points vary by model and region and fail the
  whole request rather than being ignored, so support waits until it can be
  qualified against a real deployment. An endpoint that rejects a cache
  breakpoint disables caching for the life of the process after one wasted
  request, which is correct but costs that request.
- `collo setup` proves a provider configuration with two real requests before
  writing it, but that is a reachability and tool-acceptance check rather than a
  judgement about the model: one that answers a trivial prompt and accepts a
  tool definition can still be too weak to drive an agent usefully. Azure
  OpenAI and Bedrock are configured by naming their fields rather than by
  enumeration — deployment listing needs the ARM management plane and Bedrock's
  `ListFoundationModels` needs a dependency Collomia does not carry — so a
  mistyped deployment or a region without model access is caught by the
  verification step rather than prevented by a list, and sovereign-cloud
  endpoints are untested. Setup writes the user-level configuration only, never
  a repository's `.collomia.json`. It can store a key in the OS credential
  manager on macOS and Windows; other platforms, including Linux, have no
  supported backend, so a key there is referenced by environment-variable name
  and must be exported by the user.
- Provider behavior still depends on the selected model, account, deployment,
  regional availability, and upstream API changes. Use the capability display,
  `collo doctor`, and live provider qualification before relying on a hosted
  endpoint.
- The project has extensive automated security tests but has not completed the
  independent security review required for 1.0.
- Configuration, event, and session formats have explicit version checks and a
  documented [compatibility policy](COMPATIBILITY.md), but downgrading a global
  state directory after a newer release has written to it is not guaranteed.
- Durable-session write failures fail visibly and block later provider/tool
  boundaries. An action already executing at the instant of a disk failure or
  process death may still have taken effect; resume marks it interrupted and
  requires inspection instead of replaying it.
- The audit ledger records what Collomia's permission layer decided and what
  the resulting execution returned; it is not a system-call audit, so a program
  that was approved and then opened a socket or read a file on its own is
  outside its view. A ledger write failure does not stop the agent loop, but it
  is reported and declared in the file as a gap, so an incomplete record never
  reads as a complete one — check with `collo audit` or `collo doctor`.
  Redaction is best-effort pattern matching, so review a ledger before sharing
  it.

Do not advertise the beta as safe for unattended production changes,
deployments, credential-bearing automation, or security-critical environments.

## Reporting problems

Run `collo doctor` first. For reproducible runtime failures, note the opaque
`err-…` identifier and create a privacy-conscious bundle with:

```sh
collo support bundle
```

The default bundle excludes configuration values, prompts, source files,
session transcripts, audit content, and logs. Review any bundle before sharing
it. Use the repository's [security policy](../SECURITY.md) for vulnerabilities;
use an ordinary issue for non-sensitive defects and usability feedback.
