# Collomia — High-Level Feature and Security Summary

_Reviewed against Collomia v0.2.1, commit `ecadfac`. Features are implemented unless identified as experimental or unsupported._

- **Deployment and platform support**

  - Distributed as a single Go binary.
  - CI-tested on macOS, Linux, and Windows.
  - Provides an interactive terminal UI, a machine-readable headless mode, and an embedded browser terminal.
  - Designed as a local-first tool: sessions, configuration, trust records, and audit data remain on the user’s machine.
  - Checksum-verifying macOS/Linux and Windows installers validate the downloaded binary's reported version before replacing an existing installation.
  - The Windows installer supports AMD64 and ARM64, requires no elevation, works through the in-memory PowerShell installation form without changing execution policy, and updates the current user's PATH by default with an explicit opt-out.
  - Licensed under Apache 2.0.

- **Interactive user experience**

  - Full-screen terminal interface with 19 themes, syntax highlighting, tabs, status indicators, command palette, responsive layouts, and configurable keybindings.
  - Honors `NO_COLOR`, includes a color-independent `plain` theme, and supports configurable alternate-screen behavior.
  - Integrated approval, question, diff, and hunk-review dialogs.
  - Full-screen change review supports responsive unified and side-by-side diffs, folding, file and hunk navigation, line numbers, and safe external-editor handoff.
  - Activity center, session status dashboard, transcript browser, prompt history, draft preservation, notifications, and OSC52 clipboard support.
  - Workspace-aware `@` file and folder selection, saved prompt insertion, and image attachments.
  - Mouse reporting can be toggled while the application is running, allowing either in-application scrolling and tab selection or ordinary terminal text selection.
  - A running turn can be steered by typing and pressing enter. Guidance arrives at the next iteration boundary, never interrupting an executing tool or a pending approval, and grants no permissions of its own.
  - The status bar keeps the cancel key visible at every terminal width, shortening or dropping lower-priority segments first.
  - Generates completion scripts for Bash, Zsh, Fish, and PowerShell without requiring a shell plugin.
  - Reduced-motion and reduced-dimming accessibility options.

- **Embedded browser terminal**

  - `collo --web` runs the same Bubble Tea interface through an embedded xterm.js client without requiring Node.js or npm.
  - The server binds only to loopback, creates a fresh 256-bit access token for each launch, requires the exact served origin, and permits one controlling connection.
  - Closing the browser connection terminates the PTY and child process group.
  - Browser mode runs on macOS, Linux, and Windows; the Windows backend uses a pseudoconsole and requires Windows 10 1809 or later.
  - It is a local single-user interface, not a remote collaboration service: it has no TLS, remote identity, reconnect, or observer mode and should not be exposed through a proxy or tunnel.

- **Headless operation and automation**

  - `collo run --jsonl` provides a versioned JSONL event stream for automation and integration.
  - `collo schema events` publishes the machine-readable event schema used by automation consumers.
  - Supports durable or ephemeral headless sessions.
  - Returns structured completion, failure, refusal, cancellation, and budget-exhaustion outcomes.
  - Offline trace replay validates and renders recorded execution without loading configuration, providers, MCP servers, or tools.
  - Privacy-conscious support bundles exclude source code, prompts, session contents, audit records, and configuration values by default.

- **Configuration, instructions, and diagnostics**

  - A fresh installation has no invented provider or model. Interactive `collo` startup opens provider setup when none is configured and continues directly into the session only after verification; headless startup fails clearly and points to `collo setup`.
  - `collo setup` is a reusable provider-configuration flow: it lists configured providers for model changes and re-verification, probes local runtimes that are actually listening, reads each endpoint's own model catalog, and offers Azure and Bedrock as forms because neither is discoverable from a name and a key. The choice is proved with two real requests before anything is written — one completion, and one carrying a tool definition, since a model that answers prose but refuses tools cannot run the agent — and a failure names the endpoint, credential, or model at fault rather than reporting a status code. It asks before repointing `default_provider` and never writes an API key into a configuration file.
  - Global and project configuration is schema-versioned, layered, origin-aware, and inspectable through `collo config show`.
  - Starter and commented reference configurations can be generated independently; reference JSONC files are documentation and are never loaded as active configuration.
  - `collo config validate --strict` detects unknown, misspelled, contradictory, and obsolete settings before a session starts.
  - User-wide and trusted project `AGENTS.md` or `COLLOMIA.md` files provide persistent coding conventions and repository-specific guidance without bypassing permissions or containment.
  - `collo doctor` checks the build, platform, merged configuration, repository trust, terminal, Git, provider credentials, MCP definitions, sandbox availability, and diagnostic storage.
  - `collo capabilities --markdown` exposes the generated implementation matrix, while `collo policy check` explains how a command would be classified without executing it.

- **Model and provider support**

  - Supports OpenAI and OpenAI-compatible endpoints such as Ollama, vLLM, and LM Studio.
  - Supports Anthropic-compatible APIs.
  - Supports AWS Bedrock ConverseStream and Bedrock Mantle.
  - Supports Azure OpenAI and Azure AI Foundry, including API-key, bearer-token, and Azure credential-chain authentication.
  - Provides model discovery and a capability registry covering tools, streaming, reasoning, images, structured output, usage reporting, caching, parallel tools, context limits, and endpoint compatibility.
  - Contradictory or unsupported model configurations fail before a provider request is sent.
  - Provider failures are classified, with bounded retries, backoff, jitter, `Retry-After` handling, configurable timeouts, and circuit-health reporting.
  - Streaming output normalizes text, reasoning, tool-call arguments, usage, warnings, and errors across providers.
  - Prompt caching is requested automatically on Anthropic-compatible routes, where the stable prefix of tool schemas and system prompt is otherwise resent in full on every call of a turn. OpenAI-family endpoints cache implicitly; Bedrock is declared without support rather than sending a cache point that varies by model and region.
  - Reported token counts always describe the whole prompt, with cache reads and writes broken out and priced separately.

- **Built-in web access**

  - `web_search` searches DuckDuckGo's no-JavaScript endpoints without an API key, account, or additional configuration.
  - Search failover declares every endpoint it may contact, distinguishes engine/parsing failures from an empty result set, and reports DuckDuckGo throttling as rate limiting.
  - `web_fetch` retrieves one HTTP(S) URL as readable text, markdown with resolved links, or the unchanged textual response body.
  - HTML extraction removes scripts, styles, navigation, headers, footers, asides, and hidden content while preserving useful headings, lists, code blocks, and tables; JSON, plain text, and source files pass through unchanged.
  - Fetches are bounded by a 30-second retrieval timeout, a 5 MiB response limit, and a 1 MiB extracted-text limit; oversized tool output uses the ordinary session artifact mechanism.
  - The tools can reach only the public internet. Connect-time IP checks reject loopback, private, link-local, cloud-metadata, carrier-grade NAT, multicast, documentation, benchmark, and reserved address ranges, including IPv4-mapped and NAT64 forms.
  - The public-address guard cannot be disabled through configuration, inherited proxy settings are ignored, URL credentials are stripped, and no cookie jar is retained.
  - Same-site redirects may be followed, but a redirect to another site is reported and must be fetched as a separate action.
  - Both tools carry external risk, declare their destination hosts for scoped permission rules or session grants, and frame returned material as external data rather than authoritative instructions.
  - Sites that require JavaScript, bot challenges, or browser-specific TLS behavior may still refuse retrieval; the tools do not solve challenges, rotate identities, or bypass site policy.
  - Either tool can be removed from the active tool set through `options.disabled_tools`.

- **File and workspace tools**

  - Workspace-rooted file reading, directory listing, and text search.
  - Atomic file creation and replacement with mode preservation and undo support.
  - Exact-match editing and multi-file patch application.
  - Patch operations are prevalidated, protect against unsafe paths and symlink traversal, and produce machine-readable change sets.
  - Filesystem protections address symlink races, hard-link aliasing, path traversal, and time-of-check/time-of-use risks.
  - Oversized tool results are stored as bounded artifacts that can be read incrementally.
  - Image and artifact quotas prevent unbounded context growth.

- **Command execution**

  - Foreground and background command execution with live output.
  - Timeouts, output limits, cancellation, and process-tree termination.
  - Pseudo-terminal execution on every platform: a Unix PTY, or a Windows pseudoconsole whose child is created suspended and joined to a job object before it runs, so cancellation reaches the whole process tree with no window for a descendant to escape it.
  - Sandboxed commands default to a documented minimal environment that omits common API tokens, cloud credentials, proxy settings, and unrelated toolchain state.
  - Full parent-environment inheritance remains an explicit compatibility option, and individual values can instead be supplied narrowly through the command itself or a wrapper.
  - Background-process listing, output retrieval, and termination through the `/ps` interface.
  - Background processes use the same permission and sandbox rules as foreground commands and are stopped when the session exits.
  - Command analysis recognizes risk based on the command’s likely outcome, rather than relying only on executable names.
  - Outcome classification covers both destruction and publication, so applying infrastructure, pushing an image, opening a pull request, or publishing a package is treated as consequential rather than ordinary.
  - Read verbs and rehearsal switches such as `--dry-run` are excluded from publication classification, and a download-direction copy is distinguished from an upload.

- **Code intelligence and LSP**

  - Incremental, ignore-aware symbol indexing for Go, Python, JavaScript, TypeScript, and Rust.
  - Language Server Protocol integration for diagnostics, definition lookup, reference lookup, and file formatting.
  - Automatic or configured discovery of `gopls`, Pyright, TypeScript Language Server, and `rust-analyzer`.
  - LSP formatting is treated as a tracked file change and participates in undo and review.
  - Definition and reference requests can identify locations by file, line, and symbol without requiring the user to know an exact character column.
  - Read-only Git tools expose status, diff, log, and blame information without providing commit or push authority.
  - `/review` performs read-only review against uncommitted changes, a Git reference, or custom instructions.
  - `/verify` discovers and runs applicable build, lint, and test commands, preserving the resulting evidence.

- **Planning and agent workflow**

  - Structured plans can be created and updated through a dedicated planning tool.
  - Plans are persisted with sessions and can remain pinned in the interface.
  - In primary execution mode, a bounded completion controller refuses an ordinary final response while an active plan is unfinished, a terminal plan step lacks evidence/reason, a tracked write is newer than successful verification, or a tool failure has not been recovered or recorded as blocked.
  - The controller provides at most two deterministic continuation notices. Planning mode remains able to finish with pending implementation steps, and an old terminal plan does not block an unrelated later question.
  - The experimental Orchestrated Goal graph is Collomia's evidence-gated durable execution mode: the model proposes the work, while the runtime owns readiness, attempts, evidence freshness, recovery treatment, terminal state, aggregate accounting, and aggregate bounds. `/orchestrate <goal>` creates a fresh read-only, acceptance-bearing proposal; `/orchestrate approve` activates it once; and status/cooperative-pause/explicit-resume/safe-retry/whole-graph-cancel expose durable node, attempt, failure, evidence, accounting, and budget state. Status separates proposal-plus-primary, automatic-read, and automatic-writer work, including every completed provider-call iteration (also failures and retries), input/output tokens, honestly available estimated cost, elapsed time, and active time. A fixed envelope caps new graphs at 96 provider iterations, 1,000,000 tokens, $5 estimated cost when pricing is complete, and 30 minutes active after approval; project content and configuration cannot widen it, while restored graphs retain their originally stored ceiling. Inside that envelope, `max_iterations` is a consecutive primary no-progress lease: novel durable successful tool evidence or a resolved recoverable failure renews it, equivalent repetition does not. Once the runtime records an exact completion gap, a four-cycle remediation lease renews for a real workspace repair, a novel machine-observed verification failure, or evidence that closes the gate; identical failures, different command wrappers, and unrelated output cannot prolong it. Conservative write-ahead effect history remains separate from observed workspace freshness, so a completed process or smoke check preserves earlier verification when the Git state token is unchanged. Proposal guidance prefers four to six coherent outcome nodes, treats twelve as a maximum, defaults end-to-end changes to primary, and requires the first mutating node in a project without an applicable test surface to create a focused smoke test. Proposal history is compacted once before node execution, and active context is compacted again when it would consume at least one-eighth of the remaining graph allowance; those summary requests remain fully accounted. At most two independently ready approved `read_only` nodes can use governed automatic readers. An explicitly candidate-only graph may run one bounded wave of at most two ready pairwise-disjoint terminal `isolated_write` nodes from a common clean Git commit; approval preflights that base and names dirty paths without consuming the proposal. Candidate graphs cannot contain primary nodes or depend on retained writers. Dispatch and every detected child verification command keep their ordinary permission/hook decisions, candidates stay in retained worktrees, and passing scope/base/freshness evidence stops the graph for review without touching the parent workspace or unlocking dependents. Controlled comparisons retain read fan-out for substantive independent reads while making its extra model work visible; trivial primary work and dependency-serial reads remain serial. After a parent mutation, recognized verification—including an exact redundant workspace `cd ... &&` and final `2>&1` wrapper—must pass against the current Git workspace state and returns a positive state-bound receipt; masking shell composition remains rejected. Interrupted safe reads may be recomputed in a fresh attempt, but ambiguous parent mutations and interrupted isolated writers are blocked rather than replayed; manual retry rejects that same ambiguity. There is no configuration/headless opt-in, automatic candidate selection/integration, exact multi-worker recovery, or optional-branch/node cancellation; Standard mode remains the default.
  - Headless results preserve the schema-v1 `ok`/`error`/`cancelled` process status and add a goal-level `done`/`blocked`/`cancelled`/`budget_exhausted` outcome.
  - The agent can request structured user decisions through a terminal dialog.
  - Tool iterations, tokens, cost, and elapsed time can be bounded.
  - Named profiles can define provider, model, role, reasoning effort, tools, skills, iteration limits, and financial or time budgets.

- **Session and context management**

  - Durable local sessions support resume, fork, rewind, crash recovery, and searchable transcripts.
  - Command-line session administration supports listing, inspection, renaming, archiving, unarchiving, and explicit deletion.
  - Session storage is append-only and fails safely if a required durable write cannot be completed.
  - Rewinding creates a new branch of the conversation; it does not silently replay tool actions.
  - Restoring branches the conversation and reverses the file changes recorded after that turn as one operation, refusing the whole operation and naming every affected file if anything changed outside Collomia. Commands, network calls, and other external effects are never reversed.
  - Automatic and manual context compaction preserve important evidence, including failed verification results.
  - Token and cost accounting is available per turn and per session, with configurable budget limits.
  - Images and other attachments are retained as governed session context.

- **Permission model**

  - Supports `ask`, `workspace`, and `autopilot` autonomy modes.
  - Autonomy modes affect approval behavior but are explicitly not presented as an operating-system security boundary.
  - Ordered `allow`, `prompt`, and `deny` rules can be scoped to tools, paths, commands, hosts, and MCP servers.
  - Per-capability approvals separate file access, command execution, network endpoints, and credential access.
  - A session grant covers only the exact declared capability; all required dimensions must be independently authorized.
  - Blanket tool approval and autopilot do not implicitly authorize protected credential access.
  - Catastrophic command protections include non-overridable denials and one-time confirmation for narrowly legitimate destructive actions.
  - Publishing, deploying, and pushing are governed separately by `publication` (`off`, `prompt` by default, `deny`): package and container registries, source remotes, code-forge writes, infrastructure applies, and commands executed on another host are not covered by autonomy mode, a tool-wide approval, or an allow rule naming only an executable.
  - Rule `command` patterns match either an executable name or, when the pattern contains a space, an operation such as `npm publish` or `gh pr create`, so a policy can permit dependency installation while gating releases.
  - Permission enforcement is shared across foreground commands, background commands, PTYs, delegated verification, hooks, and other execution paths.

- **Monotonic containment policies**

  - Configuration is layered from built-in defaults through user, project, and environment settings.
  - Repository or project configuration may tighten containment but cannot weaken the user’s established security posture.
  - Monotonic enforcement applies to sandbox policy, sandbox network and outside-workspace reads, the command environment, outside-workspace access, command posture, network posture, scoped egress, credential protection, publication posture, and containment presets.
  - Attempts by project configuration to weaken containment are refused and reported.
  - Only the global configuration owner can explicitly disable the operating-system sandbox.
  - Configuration inspection reports both effective values and their origins.
  - Strict schema validation catches unknown, misspelled, contradictory, or obsolete settings.

- **Containment presets**

  - `frictionless`, `standard`, and `hardened` presets expand into ordinary, inspectable configuration fields.
  - Explicit settings override preset-derived values.
  - Presets do not silently change the selected autonomy mode.
  - Hardened mode requires stronger sandbox, read, credential, publication, command, and network postures.
  - The current hardened preset does not itself disable all network access or enable the scoped-egress broker; those remain separate explicit choices.

- **Operating-system sandbox** — _experimental_

  - macOS containment uses Seatbelt through `sandbox-exec`.
  - Linux containment uses Landlock.
  - Windows 11 containment uses AppContainer with Job Objects.
  - Command writes can be confined to the workspace and approved temporary locations.
  - Reads outside the workspace can optionally be prohibited.
  - Remote network access can be denied while retaining controlled loopback communication.
  - Sandbox state is always visible: contained, unsandboxed, or degraded.
  - `sandbox=require` fails closed if the requested backend cannot be established.
  - `sandbox=auto`, currently the default, visibly degrades and continues without OS containment if a backend is unavailable.
  - `sandbox=off` is an explicit global-only opt-out.
  - Process-group or job termination is used to limit orphaned subprocesses.

- **Network policy and scoped egress**

  - Network permissions may be scoped to specific hosts and endpoints.
  - Host policy considers declared HTTP endpoints and recognizable endpoints embedded in command text.
  - Endpoints that cannot be reliably interpreted remain undetermined and are not automatically treated as authorized.
  - Network and command postures participate in monotonic project-policy enforcement.
  - An experimental macOS scoped-egress mode blocks direct remote connections from sandboxed commands.
  - In scoped-egress mode, commands connect through a loopback `CONNECT` broker that may dial only hosts allowed by host-scoped policy.
  - The broker does not intercept or decrypt TLS.
  - Scoped egress fails closed when required and visibly degrades when used in automatic mode.
  - Scoped egress is opt-in and is not currently enabled by a containment preset.

- **Credential and secret protection**

  - `protect_credentials` supports `off`, `prompt`, and `deny`, with `prompt` as the standard posture.
  - Conventional credential stores and files receive special protection.
  - Credential access requires an exact rule or exact session grant; blanket approval is insufficient.
  - Under `deny`, no interactive grant can override the prohibition.
  - Optional provider-secret storage uses macOS Keychain or Windows Credential Manager and is managed through `collo auth set|list|status|import|rm`.
  - Credential values are never returned by the management commands, and interactive entry avoids command-line arguments and shell history.
  - Provider secrets follow defined configuration, environment, provider-family, and secure-store precedence; `collo auth status` reports which source won.
  - Linux has no credential-store backend, and there is intentionally no silent plaintext-file fallback.
  - Sensitive values are redacted from displays, diagnostics, support artifacts, and applicable logs.
  - Sandboxed commands receive a minimized environment to reduce unintended credential exposure.
  - Credential recognition is primarily path- and convention-based rather than general content inspection.

- **Auditability and review**

  - File changes are presented for review before approval where required.
  - Multi-hunk `write_file` changes support individual hunk acceptance; other edit mechanisms support file-level review.
  - A JSONL audit ledger records every permission decision and execution outcome outside the workspace, readable through `collo audit` with filters for session, actor, tool, time window, and refusals, plus JSONL output for external tooling.
  - Every entry names the session and the actor that produced it — `primary`, or `agent:<profile>` with the delegated task id — so one workspace ledger holding concurrent delegated agents can still be separated into what each was permitted to do.
  - A ledger write failure does not stop the agent loop, but it is never silent: failures are counted, reported to the session once, and declared in the file as a gap entry stating how many entries were lost, since when, and why.
  - `collo audit` reports the record's integrity — declared gaps, unreadable lines, a generation discarded at rotation — before any entries, and `collo doctor` reports the same as a warning check, so an incomplete record is never read as a complete one.
  - Ledger growth is bounded by rotation at 64 MiB with one retained previous generation, and a rotation that discarded older history records that fact.
  - There is no configuration setting that makes a failed audit write stop an action; audit is fail-visible, not fail-stop, and is not one of the monotonically clamped containment fields.
  - The session status view exposes effective permissions, grants, sandbox state, network posture, and configuration origins.
  - Containment degradation is shown rather than silently hidden.

- **Repository and configuration trust**

  - Repository-provided `.collomia.json`, project skills, and project instructions are quarantined until the repository content is explicitly trusted.
  - Trust decisions are content-bound so meaningful changes can invalidate prior trust.
  - Project configuration cannot disable the sandbox or weaken global containment.
  - Project/global precedence and shadowing are inspectable.
  - Path, symlink, and workspace-boundary checks are consistently applied to repository-supplied resources.

- **MCP integration**

  - Supports trusted MCP servers over standard input/output and streamable HTTP.
  - Supports MCP tools, resources, prompts, progress notifications, and structured or rich content.
  - MCP-produced content is wrapped with external-data provenance so it is not treated as trusted system instruction.
  - Text, structured data, embedded resources, and images can be passed to capable models; safe textual markers are used for unsupported media.
  - MCP servers can be listed, inspected, added, removed, enabled, disabled, tested, refreshed, reconnected, and health-checked.
  - Runtime status includes protocol version, advertised capabilities, catalog state, errors, and connection health.
  - Failed or disabled servers have their tools withdrawn.
  - Tool-list changes can hot-refresh while preserving the last known good catalog on failure.
  - MCP resources can be browsed and previewed.
  - MCP prompts are expanded into editable user input rather than executed invisibly.
  - Interactive elicitation forms are supported; URL-based elicitation is declined.
  - Headless clients do not advertise elicitation that they cannot safely handle.
  - Server pinning records command, arguments, URL, environment-key names, and remote identity outside the repository; meaningful changes are flagged for review.
  - MCP tasks, resource subscriptions, and OAuth flows are not currently supported.

- **Skills system**

  - Skills use YAML front matter and may declare metadata and allowed tools.
  - Skill instructions, scripts, references, and assets are discovered progressively and loaded on demand.
  - Skills may be installed globally or supplied by a trusted project.
  - Project skills override global skills with visible shadow reporting.
  - Skills can be listed, inspected, created, installed, updated, removed, enabled, and disabled.
  - Skill inspection includes SHA-256 identity information.
  - Symlinked skill content is refused to reduce redirection and substitution attacks.
  - Project skills remain subject to repository trust and cannot bypass ordinary permissions.

- **Hooks and policy extensibility**

  - Hooks are available for session, prompt, permission, tool, file-change, compaction, subagent, and stop events.
  - Prompt and tool-start hooks can block an operation through defined exit status or decision JSON.
  - Hooks can tighten policy or stop actions but cannot grant permission, bypass the sandbox, or weaken containment.
  - Hook execution remains within the same permission and trust framework as the rest of the system.
  - An optional external reviewer command receives structured action metadata before automatic non-read actions and may deny or escalate them, but can never turn a prompt or denial into an approval.

- **Multi-agent and delegated work**

  - Supports named primary and delegated agent profiles.
  - Delegated agents can have narrower model, role, reasoning, tool, skill, iteration, token, cost, time, and write-scope limits.
  - Delegated permissions can only tighten the parent’s authority.
  - A configurable FIFO scheduler manages global and per-provider concurrency.
  - Agents with disjoint repository-relative write scopes may run concurrently; overlapping or workspace-wide scopes are serialized.
  - Queue time participates in cancellation and timeout accounting.
  - Users can inspect, steer, and stop delegated work.
  - Candidate changes can be verified in retained worktrees and compared using read-only evidence.
  - Applying delegated changes requires freshness checks and guarded three-way comparison.
  - Non-overlapping changes may be composed; conflicts are surfaced rather than silently overwritten.
  - Delegated workflows do not automatically commit, merge, push, or delete worktrees.
  - Resumed sessions retain delegation evidence without rerunning prior tool actions.

- **Quality and supply-chain controls**

  - Offline agent evaluations cover search, bug fixes, refactoring, tests, review, refusals, injection resistance, recovery, rewind, compaction, delegation, and integration.
  - Includes provider contract tests, integration fixtures, parser fuzz smoke tests, and performance baselines.
  - Release workflows include cross-platform testing, race detection, vetting, installer tests, vulnerability scanning, evaluation gates, and native smoke tests.
  - Release artifacts include checksums, CycloneDX software bills of materials, and Sigstore/GitHub attestations.
  - Native platform signing and broader package-manager distribution remain incomplete.
  - Security reporting is documented through `SECURITY.md` and GitHub private vulnerability reporting.

- **Important security boundaries and limitations**

  - Collomia remains a technical beta and has not been presented as independently penetration-tested.
  - The operating-system sandbox and scoped-egress broker are marked experimental.
  - Default `sandbox=auto` is fail-visible but fail-open; deployments requiring strict containment should use `sandbox=require`.
  - The standard preset permits command network access and outside-workspace reads unless explicitly tightened.
  - Provider HTTP traffic, remote MCP connections, hooks, and LSP processes run in the Collomia process and are outside the command sandbox and command egress broker.
  - MCP and skills are governed by trust and integrity checks, but installing or enabling them still extends the trusted computing base.
  - The audit ledger records what the permission layer decided and what the resulting execution returned. It is not a system-call audit: a program that was approved and then opened a socket or read a file on its own is outside its view. A ledger write failure is reported and declared rather than silently dropped, but no setting causes it to stop an action.
  - Publication classification is a policy layer, not egress enforcement. It describes what a command's text says it will do, so a program that uploads an artifact without naming the operation on its command line is outside its view, and its catalogue of publishing tools is finite.
  - There is no hosted enterprise identity plane, centralized SSO/RBAC service, or remote policy administration layer.
