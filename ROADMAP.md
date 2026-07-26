# Collomia Roadmap

**Status updated:** 2026-07-26

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
  denials, credential-store protection, secret redaction, and an audit ledger;
- durable resumable sessions, crash recovery, compaction, bounded retained
  artifacts, rewind/fork, and fail-stop persistence handling;
- atomic patching, tracked diffs, hunk review, undo, Git inspection, planning,
  verification, LSP diagnostics/definitions/references/formatting, repository
  indexing, PTY commands, and background processes;
- normalized provider capabilities, streaming, retries, health, contracts,
  Azure Entra refresh, Bedrock SigV4/bearer authentication, optional macOS/
  Windows keychain credential storage, opt-in provider-safe reasoning controls,
  user-priced cost estimates, and image input;
- MCP lifecycle/resources/prompts/progress/elicitation, safe live catalogs,
  external-data framing, skills, and hooks;
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
and reliability gates are complete.

No wave is currently active. The completed waves below are the most recent
work; see [Recommended next sequence](#recommended-next-sequence) for what the
dependency order argues for next.

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
- [ ] **P0 — Enforced endpoint-scoped egress:** the policy surface, declared
  endpoints, and scoped grants ship; OS-level enforcement does not. Add a
  Collomia-owned loopback egress broker that allows only policy-matched
  destinations by CONNECT/SNI host without TLS interception, deny direct
  remote egress in the sandbox where the backend supports it (macOS Seatbelt,
  Linux Landlock ABI ≥ 4), degrade visibly on Windows AppContainer given its
  unpackaged-loopback limitation, and keep `require` fail-closed. Preserve
  command networking by default until this can replace the all-or-nothing
  switch without breaking common tooling.
- [ ] **P0 — Independent review:** sustain the adversarial suite and obtain an
  independent security assessment before 1.0.

### Phase 2 — Sessions and context

- [ ] **P1 — Coupled checkpoints:** conversation rewind and file undo exist;
  add an explicit conversation-plus-workspace checkpoint while continuing to
  state that shell and external side effects cannot be reversed automatically.
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
- [ ] **P1 — Windows ConPTY:** add native PTY execution without weakening the
  existing process-tree cancellation contract.

### Phase 4 — Provider platform

- [x] **P1 — Secure credential lifecycle:** `collo auth` keeps provider API
  keys in the macOS Keychain or Windows Credential Manager, with set/list/
  status/rm/import flows, no project-file secrets, and no value ever printed
  back. Environment variables keep precedence and remain fully supported;
  Linux has no backend by design, so headless hosts use `api_key_env`. MCP
  server credentials are covered separately by Phase 5's OAuth item.
- [ ] **P1 — Provider discovery refinements:** Azure deployment/project
  discovery and routing, tested sovereign presets, and clearer resolved AWS
  identity/model-access diagnostics.
- [ ] **P1 — Modern API features:** general OpenAI/Azure Responses routing,
  structured output, provider caching, richer thinking/content blocks, and
  additional media types.
- [ ] **P1 — Explicit routing/fallback:** ordered capability/health/cost/local
  choices that never silently cross privacy or residency boundaries.
- [ ] **P1 — Usage and budgets:** normalized user-priced cost estimates and
  enforceable session/agent monetary budgets ship; an independently
  configurable per-turn dollar cap and richer provider billing caveats remain.
- [ ] **P2 — Setup wizard:** discover local runtimes, validate endpoints and
  credentials, test deployments, and write a minimal user provider profile.

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
- [ ] **P1 — Workspace UI refinements:** the context rail now carries
  workspace, plan, agents, changed files, and background processes beside the
  transcript; automatically surfaced diagnostics and provider price/budget
  visibility remain.
- [ ] **P1 — Accessibility validation:** native screen-reader, colored theme,
  resize, and broader terminal-emulator coverage.
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
  power-loss durability, native terminal loss, remaining diagnostic/audit
  fail-stop policy, and longer cancellation stress.
- [ ] **P1 — Performance budgets:** idle memory, token overhead, compaction
  quality, monorepo fixtures, and same-hardware regression thresholds.
- [ ] **P1 — Optional telemetry decision:** only opt-in, minimal, documented,
  locally inspectable/deletable, and fully disabled by offline mode.
- [ ] **P1 — Native release signing:** Apple signing/notarization, Windows
  Authenticode, and installer-enforced signature verification.
- [ ] **P1 — Package managers:** Homebrew, Scoop/Winget, selected Linux flows,
  and clean-machine install/update/rollback/uninstall testing.

## Recommended next sequence

1. Build the enforced egress broker on top of the shipped policy surface: a
   loopback CONNECT/SNI allowlist, sandbox-level denial of direct remote
   egress where the backend supports it, and honest per-platform degradation.
   This is the last P0 outside Phase 8.
2. Gather real beta feedback on named primary profiles, cost estimates,
   verified delegated results, scoped scheduling, three-way review, and the
   new postures.
3. Add opt-in plan-graph execution using verified results, write scopes,
   dependency readiness, and stale-state invalidation.
4. Add explicit combined-parent verification and conservative result-ranking
   criteria without turning a score into permission.
5. Continue Phase 8 security/reliability campaigns in parallel with every
   feature wave.

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
  by default.
- Persistent semantic memory across unrelated repositories.
- Decorative features that do not improve coding safety, accessibility, or
  throughput.
