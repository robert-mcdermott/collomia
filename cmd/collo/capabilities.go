package main

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/sandbox"
)

// capabilityRow is one line of the product capability matrix. Status values:
// implemented, experimental, unsupported. This table is the single source of
// truth; `collo capabilities --markdown` regenerates docs/CAPABILITIES.md.
type capabilityRow struct {
	area, capability, status, notes string
}

func capabilityMatrix() []capabilityRow {
	sandboxStatus := "unsupported"
	sandboxNote := "macOS Seatbelt, Linux Landlock, Windows 11 AppContainer + Job Object; no OS backend active on " + runtime.GOOS + "; default auto mode visibly degrades, while require refuses command execution"
	backend := sandbox.ForPlatform()
	if backend.Available() == nil {
		sandboxStatus = "experimental"
		sandboxNote = "built-in backends: macOS Seatbelt, Linux Landlock, Windows 11 AppContainer + Job Object; active platform: " + backend.Name() + "; " + backend.Capabilities().Summary() + "; auto is the compatibility-first default; optional workspace-scoped command user-data reads use sandbox_allow_read_outside_workspace=false; set permissions.sandbox=off only as an explicit compatibility escape hatch, and only in the global configuration"
	}
	return []capabilityRow{
		{"provider", "openai / openai-compatible (Ollama, vLLM, LM Studio)", "implemented", "streaming chat completions + function tools; typed user-image content where the selected model/endpoint supports it"},
		{"provider", "anthropic / anthropic-compatible", "implemented", "streaming messages + tool use; typed user and tool-result image content where the selected model supports it"},
		{"provider", "aws bedrock (ConverseStream)", "implemented", "native event-stream text/tool/reasoning/usage plus typed user/tool-result images; auto/sigv4/bearer auth supports the AWS credential chain and Bedrock API keys"},
		{"provider", "aws bedrock mantle", "implemented", "Responses-style SSE with synchronous JSON fallback and typed user-image input"},
		{"provider", "azure openai / azure ai foundry", "implemented", "API key, static bearer, or DefaultAzureCredential with in-memory caching/proactive refresh; Azure OpenAI/Foundry OpenAI and Claude routes carry typed user images where their selected deployment supports them"},
		{"provider", "model discovery", "implemented", "live OpenAI-compatible and Anthropic catalogs feed /model; /models reports capabilities plus available/unavailable/unverified status"},
		{"provider", "retry/resilience", "implemented", "classified failures; replay-safe 3-attempt backoff+jitter with Retry-After across all HTTP adapters; configurable connect/request/idle timeouts; circuit health in /status"},
		{"provider", "normalized streaming events", "implemented", "text, reasoning, tool-argument fragments, usage, warnings, and classified errors; tool execution waits for complete valid JSON"},
		{"provider", "capability registry + preflight", "implemented", "four-state adapter/model declarations cover tools, streaming, reasoning, images, structured output, usage, caching, parallel tools, discovery, context, and endpoint constraints; known contradictions fail before network I/O"},
		{"provider", "protocol contract suite", "implemented", "credential-free CI fixtures plus a secret-safe, double-opt-in live tool/stream/usage round trip for OpenAI, Anthropic, Responses/Mantle, and Bedrock; cancellation covered across every family"},
		{"tools", "read_file / list_files / search_files", "implemented", "workspace-contained, symlink-aware path guard"},
		{"tools", "write_file / edit_file", "implemented", "race-resistant rooted same-directory atomic replacement; hard-link isolation; mode-preserving /undo where supported; unique-match edits and approval diff"},
		{"tools", "run_command", "implemented", "live streamed output, process-group termination on Unix and Job Object ownership on sandboxed Windows, timeout, output caps, outcome-aware command safety; pty=true on Unix; sandbox degradation is never silent"},
		{"tools", "search_symbols", "implemented", "incremental ignore-aware definition index for Go/Python/JS/TS/Rust; exact/prefix ranked"},
		{"tools", "diagnostics (LSP)", "implemented", "real language-server client (gopls, pyright, typescript-language-server, rust-analyzer auto-detected; lsp config map overrides); per-file severities and lines"},
		{"tools", "background processes", "implemented", "start_process/list_processes/process_output/stop_process with the same safety analysis and sandbox as run_command; /ps in the TUI; all stopped at session exit"},
		{"workflow", "code review (collo review, /review)", "implemented", "read-only review of uncommitted changes or vs a ref; trailing words become custom reviewer instructions"},
		{"workflow", "verification loop (collo verify, /verify)", "implemented", "detect_verification finds real build/lint/test commands from project files; run_command executes them; outcomes tied to update_plan evidence"},
		{"tools", "apply_patch", "implemented", "rooted multi-file change sets with complete pre-validation, atomic per-file publish, safe deletion, rollback, and machine-readable changesets"},
		{"tools", "git_status / git_diff / git_log / git_blame", "implemented", "read-only, bounded, flag-injection safe; never commits or pushes"},
		{"tools", "hunk-level review", "implemented", "pending write_file approvals with 2+ hunks offer 'h' to accept/reject each hunk individually before the write lands; edit_file and apply_patch stay file-level"},
		{"permissions", "autonomy modes ask/workspace/autopilot", "implemented", "policy checks in-process; see docs/SECURITY.md"},
		{"permissions", "scoped allow/prompt/deny rules", "implemented", "ordered rules on tool, path, command, host, server; host matches endpoints a command's text names and HTTP-transport MCP endpoints"},
		{"permissions", "declared network endpoints", "implemented", "policy-layer only, not egress enforcement; unreadable endpoints are reported undetermined and never covered by an allow rule or a session grant"},
		{"permissions", "scoped-network / command-allowlist postures", "implemented", "permissions.network and permissions.commands; default open, prompt-only escalation, monotonic across configuration layers"},
		{"permissions", "containment presets", "implemented", "permissions.preset frictionless/standard/hardened expands to ordinary fields; explicit fields win within a layer, no preset sets mode, origins attribute each expanded value"},
		{"permissions", "containment precedence", "implemented", "a repository can tighten any containment setting but never weaken one, by explicit field or preset alike; refusals are reported by collo config show/validate; the global configuration is unrestricted"},
		{"tui", "always-visible containment mark", "implemented", "the autonomy badge carries ⛨ (contained), ⛉ (no OS sandbox), or ⛨! (applied less than requested); the Session tab lists the full stance and session grants"},
		{"permissions", "per-capability approval grants", "implemented", "the approval dialog shows files/executables/endpoints separately; 'g' grants exactly the reach shown for the session, and every dimension must be covered before a later action is automatic"},
		{"permissions", "catastrophic command protection", "implemented", "non-overridable outcome denials plus mandatory one-time confirmation for destructive but legitimate commands; same checks for foreground, PTY, and background execution"},
		{"permissions", "credential-store protection", "implemented", "permissions.protect_credentials off/prompt/deny, default prompt; recognized by conventional path, not content inspection; a blanket allow rule, tool-wide grant, per-capability grant, and autopilot never cover one, while a rule naming the path does"},
		{"permissions", "audit ledger", "implemented", "JSONL ledger outside the workspace"},
		{"permissions", "OS sandbox", sandboxStatus, sandboxNote},
		{"config", "layering defaults→user→project→env", "implemented", "inspect with `collo config show`"},
		{"config", "schema versioning + validation", "implemented", "`collo config validate [--strict]`; documented config/session/event compatibility and migration policy"},
		{"config", "repository trust", "implemented", "when .collomia.json exists, project config/skills/instructions are quarantined until `collo trust`; trust is content-bound"},
		{"sessions", "persistence / resume / fork / rewind / crash recovery", "implemented", "append-only JSONL store; `collo sessions`, --resume, --continue; rewind creates a non-destructive branch at a completed turn; restoration never replays tools; short/disk writes latch a visible fail-stop guard before later provider/tool actions"},
		{"sessions", "automatic + manual compaction", "implemented", "auto above 80% of the window; /compact [focus]; transcript preserved; recent failure evidence remains exact and bounded"},
		{"context", "referenced oversized tool results", "implemented", "active context gets a bounded preview plus an opaque session artifact id; read_tool_result pages retained output without rerunning the tool; 4 MiB/result and 32 MiB/session quotas"},
		{"context", "session image attachments", "implemented", "reference-only JSONL records resolve owner-only image blobs at provider send time; PNG/JPEG/GIF/WebP, 5 MiB/image, 4 images/turn, 24 MiB/session; fork/rewind/delete aware"},
		{"context", "token and cost accounting", "implemented", "durable provider-reported input/output/cache/reasoning usage; optional estimates use only user-configured pricing; named profiles enforce token and USD budgets without /clear or resume bypass"},
		{"planning", "structured plan artifact", "implemented", "update_plan tool, /tasks view, persisted/restored with the session, and pinned into every model request outside compactable history"},
		{"planning", "user-question primitive", "implemented", "ask_user pauses the run in a transient floating dialog for a typed answer (TUI only)"},
		{"mcp", "tools over stdio / streamable HTTP", "implemented", "trusted servers only; model-visible results/resources/prompts carry explicit external-data provenance frames"},
		{"mcp", "runtime lifecycle management", "implemented", "`/mcp status|ping|refresh|reconnect|enable|disable|add|remove`; health, protocol, negotiated capabilities, catalog state, and initialization errors reported per server; disabled/failed servers withdraw their tools"},
		{"mcp", "persistent lifecycle management", "implemented", "`collo mcp list|show|add|remove|enable|disable|test`; project/global scopes, precedence and quarantine visibility, atomic config edits, secret-safe display, and connection-only testing"},
		{"mcp", "resources", "implemented", "`/mcp resources <server>` browses, `/mcp resource` previews; the agent lists and reads them with `list_mcp_resources`/`read_mcp_resource` (external-risk, server-scoped)"},
		{"mcp", "prompts", "implemented", "`/mcp prompts <server>` lists templates; `/mcp prompt <server> <name> key=value` expands one into the input box for review before sending"},
		{"mcp", "rich tool content", "implemented", "typed content preserved: text, structured output, embedded resources; image bytes pass to capable Anthropic/Bedrock model turns and remain visible type+size markers otherwise; audio remains metadata-only; resource links keep their URI and a read hint"},
		{"mcp", "elicitation", "implemented", "form-mode server questions become typed TUI prompts (esc declines); URL-mode is declined; headless runs never advertise the capability"},
		{"mcp", "progress notifications", "implemented", "server progress streams live into the transcript during tool calls, like command output"},
		{"mcp", "catalog list-change notifications", "implemented", "tool catalogs hot-refresh atomically; resource/prompt changes are marked pending until their next live list; failed refreshes preserve last-known-good tools"},
		{"mcp", "protocol conformance fixtures", "implemented", "official Go SDK negotiation plus capability-specific in-memory fixtures cover tools, resources, prompts, list changes, rich content, progress, elicitation, cancellation, lifecycle, and pinning"},
		{"mcp", "server pinning", "implemented", "definition fingerprints (command/args/url/env keys) and remote identity pinned per workspace outside the repo; changes are flagged at connect"},
		{"mcp", "tasks / resource subscriptions / oauth", "unsupported", "tasks remain experimental; subscriptions and standards-based OAuth/login remain roadmap phase 5"},
		{"skills", "discovery + on-demand load", "implemented", "YAML front matter (folded blocks, metadata, allowed-tools), bundled scripts/references/assets surfaced by load_skill, project-over-global precedence with shadow reporting, validation warnings; a present project config must be trusted"},
		{"skills", "lifecycle management", "implemented", "`collo skills list|show|new|install|update|remove|enable|disable`; sha256 inspection, .disabled marker, symlink-refusing installs"},
		{"hooks", "lifecycle hooks", "implemented", "11 events (session, prompt, permission, tool, file change, compaction, subagent, stop) run configured commands with JSON stdin, matchers, and timeouts; user_prompt/tool_start may block (exit 2 or decision JSON) — hooks only tighten, never bypass permissions or the sandbox"},
		{"subagents", "parallel delegate scheduler", "implemented", "session-wide FIFO queue with configurable global/per-provider concurrency and declared repository-relative write scopes; disjoint writers may run concurrently, overlapping or workspace-wide writers serialize, and queue-inclusive timeouts/cancellation remain enforced"},
		{"agents", "named primary and delegated profiles", "implemented", "availability-gated config sets model/role, opt-in reasoning, tool/skill allowlists, iteration/token/cost/time budgets, and permissions that can only tighten; select primary with default_agent, --agent, or /agent"},
		{"subagents", "operator steering and selective integration", "implemented", "bounded guidance is delivered only at child model boundaries; /agents verify runs repository-detected commands in retained worktrees, /agents compare presents read-only candidate facts, and /agents apply remains the guarded publication path; no commit, merge, push, or worktree deletion"},
		{"subagents", "reviewed verification and integration", "implemented", "opt-in reviewed mode gives only the primary freshness-bound inspect/verify/compare/apply tools; child verification retains canonical run_command policy/hooks/sandboxing, becomes stale on source drift, grants no permission, and does not prove the combined parent workspace"},
		{"subagents", "structured results and conflict reconciliation", "implemented", "bounded durable manifests include declared scopes and violations; integration performs a freshness-bound three-way comparison, exposes overlapping diff3 conflicts, and offers selectable clean non-overlapping composition without auto-ranking or silent overwrite"},
		{"interface", "TUI (themes, palette, tabs, status bar)", "implemented", "19 themes incl. `plain`; NO_COLOR honored; fuzzy pickers stay compact; approvals, hunk review, and questions use centered theme-aware dialogs; Markdown/read_file/diff source is syntax-highlighted"},
		{"interface", "workspace input", "implemented", "@ fuzzy-picks files/folders; /prompt loads bounded UTF-8 text; /attach accepts quoted, escaped, file-URL, and terminal-dropped workspace image paths with picker/list/detach flows and capability checks"},
		{"interface", "delegated-agent control", "implemented", "the busy composer accepts a bounded local-command lane; /agents and the parent/child Session tree show state/recent output plus integration and child-verification disposition, alt+a opens inspect/steer/stop controls, and resume restores evidence but never reruns it"},
		{"interface", "transcript navigation and copy", "implemented", "full-screen raw transcript browser with message navigation, search, live scroll preservation, and bounded OSC 52 copy for one message or the complete transcript"},
		{"interface", "activity center", "implemented", "`/activity` searches and category-filters a bounded event-derived timeline of turns, tools, permissions, changes, plans, agents, context, and failures; resume restores it without replay and failure IDs are copyable"},
		{"interface", "workspace status dashboard", "implemented", "Session tab shows async Git branch/upstream/dirty state, provider/sandbox/MCP/trust health with actionable recovery hints, and bounded recent activity; r refreshes Git state"},
		{"interface", "interactive diff review", "implemented", "full-screen changed-file browser; responsive unified/side-by-side layouts, folding, file/hunk navigation, line numbers, theme-aware changes, and safe external-editor handoff"},
		{"interface", "session continuity and prompt history", "implemented", "full transcript/tool restoration on resume; boundary-aware up/down history; in-process per-session drafts via the alt+s picker; /retry loads but never submits"},
		{"interface", "terminal ergonomics", "implemented", "80x24/narrow responsive layouts, explicit color-independent states, optional `options.reduced_motion` (animations remain the default), alternate-screen overrides, validated keybindings, and generated shell completion"},
		{"interface", "notifications", "implemented", "terminal bell + OSC 9 desktop notification for approvals, questions, and long turns; options.notifications on|bell|off"},
		{"interface", "PTY-backed browser terminal", "implemented", "`collo --web`; loopback-only, token-authenticated, embedded xterm.js; macOS/Linux (Windows ConPTY pending)"},
		{"interface", "headless run + JSONL events", "implemented", "`collo run --jsonl`; embedded/published schema v1; exactly one final `run.result` with stable failure/refusal/partial metadata, opaque cross-surface failure IDs, and exit codes; durable resume or session-free `--ephemeral`"},
		{"interface", "offline trace replay", "implemented", "`collo replay [--check] <trace|->` validates completed schema-v1 JSONL lifecycle/result consistency and renders a control-safe, best-effort-redacted transcript without loading config, providers, sessions, or tools"},
		{"diagnostics", "privacy-conscious support bundle", "implemented", "`collo support bundle`; local-only anonymous config/provider/MCP/sandbox/Git health, recent opaque failure IDs, and capability manifest; no config values, source, prompts, sessions, audits, or log content by default; bounded redacted logs are explicit opt-in"},
		{"quality", "offline agent evaluations", "implemented", "credential-free real agent/permission/tool/session scenarios cover search, bug fix, refactor, test creation, grounded review, refusal/external injection, recovery/rewind, compaction, governed parallel delegation, and selective verified integration"},
		{"quality", "performance baselines", "experimental", "CI smoke-runs diagnostic startup, activity, 2,000-file index, 2,000-message session, and 500-block TUI benchmarks without flaky cross-runner timing thresholds"},
		{"quality", "parser fuzz smoke tests", "implemented", "bounded replay, config validation, shell analysis, and diff/hunk fuzz targets run in the Linux CI quality job"},
		{"quality", "beta release supply chain", "experimental", "tag-matched draft releases require cross-platform test/race/vet, installer tests, vulnerability scan, eval/fuzz gates, and native artifact smoke tests; publish checksums, CycloneDX SBOM, and GitHub/Sigstore attestations; native platform signing and package managers remain"},
		{"platform", "macOS / Linux / Windows builds", "implemented", "CI-tested; browser terminal requires macOS/Linux until ConPTY support is added"},
	}
}

func runCapabilitiesCommand(opts options) error {
	rows := capabilityMatrix()
	if opts.markdown {
		fmt.Print(capabilityMarkdown())
		return nil
	}
	area := ""
	for _, r := range rows {
		if r.area != area {
			area = r.area
			fmt.Printf("\n%s\n", strings.ToUpper(area))
		}
		fmt.Printf("  %-12s %s", r.status, r.capability)
		if r.notes != "" {
			fmt.Printf("  — %s", r.notes)
		}
		fmt.Println()
	}
	return nil
}

func capabilityMarkdown() string {
	var out bytes.Buffer
	fmt.Fprintln(&out, "# Collomia capability matrix")
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "Generated by `collo capabilities --markdown`. Status meanings: **implemented** (works today), **experimental** (usable, incomplete), **unsupported** (not yet built).")
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "| Area | Capability | Status | Notes |")
	fmt.Fprintln(&out, "| --- | --- | --- | --- |")
	for _, row := range capabilityMatrix() {
		fmt.Fprintf(&out, "| %s | %s | %s | %s |\n", row.area, row.capability, row.status, row.notes)
	}
	return out.String()
}
