package main

import (
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
	sandboxNote := "no OS backend on " + runtime.GOOS + "; approval checks only"
	backend := sandbox.ForPlatform()
	if backend.Available() == nil {
		sandboxStatus = "experimental"
		sandboxNote = backend.Name() + "; enable with permissions.sandbox=auto|require"
	}
	return []capabilityRow{
		{"provider", "openai / openai-compatible (Ollama, vLLM, LM Studio)", "implemented", "streaming chat completions + function tools"},
		{"provider", "anthropic / anthropic-compatible", "implemented", "streaming messages + tool use"},
		{"provider", "aws bedrock (Converse)", "implemented", "non-streaming; SigV4 via AWS credential chain"},
		{"provider", "aws bedrock mantle", "implemented", "responses-style; non-streaming"},
		{"provider", "azure openai / azure ai foundry", "implemented", "API key or caller-supplied bearer token; no Entra refresh"},
		{"provider", "model discovery", "implemented", "live catalogs from OpenAI-compatible and Anthropic APIs feed the /model picker"},
		{"provider", "retry/resilience", "experimental", "3 attempts with backoff+jitter and Retry-After on 408/429/5xx; circuit breaking remains"},
		{"provider", "capability negotiation", "unsupported", "context windows and features come from configuration"},
		{"tools", "read_file / list_files / search_files", "implemented", "workspace-contained, symlink-aware path guard"},
		{"tools", "write_file / edit_file", "implemented", "unique-match edits; diff preview shown at approval; /diff and /undo"},
		{"tools", "run_command", "implemented", "live streamed output, process-group kill, timeout, output caps, conservative command analysis; pty=true on Unix for terminal-dependent programs"},
		{"tools", "search_symbols", "implemented", "incremental ignore-aware definition index for Go/Python/JS/TS/Rust; exact/prefix ranked"},
		{"tools", "diagnostics (LSP)", "implemented", "real language-server client (gopls, pyright, typescript-language-server, rust-analyzer auto-detected; lsp config map overrides); per-file severities and lines"},
		{"tools", "background processes", "implemented", "start_process/list_processes/process_output/stop_process with the same safety analysis and sandbox as run_command; /ps in the TUI; all stopped at session exit"},
		{"workflow", "code review (collo review, /review)", "implemented", "read-only review of uncommitted changes or vs a ref; trailing words become custom reviewer instructions"},
		{"workflow", "verification loop (collo verify, /verify)", "implemented", "detect_verification finds real build/lint/test commands from project files; run_command executes them; outcomes tied to update_plan evidence"},
		{"tools", "apply_patch", "implemented", "atomic multi-file change sets with validation, rollback, and machine-readable changesets"},
		{"tools", "git_status / git_diff / git_log / git_blame", "implemented", "read-only, bounded, flag-injection safe; never commits or pushes"},
		{"tools", "hunk-level review", "implemented", "pending write_file approvals with 2+ hunks offer 'h' to accept/reject each hunk individually before the write lands; edit_file and apply_patch stay file-level"},
		{"permissions", "autonomy modes ask/workspace/autopilot", "implemented", "policy checks in-process; see docs/SECURITY.md"},
		{"permissions", "scoped allow/prompt/deny rules", "implemented", "ordered rules on tool, path, command, host, server"},
		{"permissions", "audit ledger", "implemented", "JSONL ledger outside the workspace"},
		{"permissions", "OS sandbox", sandboxStatus, sandboxNote},
		{"config", "layering defaults→user→project→env", "implemented", "inspect with `collo config show`"},
		{"config", "schema versioning + validation", "implemented", "`collo config validate [--strict]`"},
		{"config", "repository trust", "implemented", "project config/skills/instructions quarantined until `collo trust`"},
		{"sessions", "persistence / resume / fork / crash recovery", "implemented", "append-only JSONL store; `collo sessions`, --resume, --continue"},
		{"sessions", "automatic + manual compaction", "implemented", "auto above 80% of the window; /compact [focus]; transcript preserved"},
		{"context", "token accounting", "implemented", "usage-anchored estimates; cached/reasoning tokens where reported; cost pending pricing data"},
		{"planning", "structured plan artifact", "implemented", "update_plan tool, /tasks view, persisted and restored with the session"},
		{"planning", "user-question primitive", "implemented", "ask_user pauses the run for a typed answer (TUI only)"},
		{"mcp", "tools over stdio / streamable HTTP", "implemented", "trusted servers only"},
		{"mcp", "resources / prompts / elicitation / oauth", "unsupported", "roadmap phase 5"},
		{"skills", "discovery + on-demand load", "implemented", "simple front matter; project skills require trust"},
		{"subagents", "parallel delegate scheduler", "implemented", "up to 4 concurrent tasks; write tasks isolated in their own git worktree; no recursion, no auto-merge"},
		{"subagents", "named agent profiles", "implemented", "config `agents.<name>` sets model/role instructions/tool allowlist/iteration budget; selected per task via delegate's `agent` field"},
		{"subagents", "sibling conflict detection", "implemented", "a delegate batch flags files touched by more than one sub-agent's worktree; nothing is auto-reconciled"},
		{"interface", "TUI (themes, palette, tabs, status bar)", "implemented", "11 themes incl. `plain`; NO_COLOR honored; fuzzy pickers for models, themes, sessions, files, skills, and MCP servers"},
		{"interface", "notifications", "implemented", "terminal bell + OSC 9 desktop notification for approvals, questions, and long turns; options.notifications on|bell|off"},
		{"interface", "PTY-backed browser terminal", "implemented", "`collo --web`; loopback-only, token-authenticated, embedded xterm.js; macOS/Linux (Windows ConPTY pending)"},
		{"interface", "headless run + JSONL events", "implemented", "`collo run --jsonl`; schema v1; final `run.result` line carries status (ok/error/cancelled), answer, changed files, and usage"},
		{"platform", "macOS / Linux / Windows builds", "implemented", "CI-tested; browser terminal requires macOS/Linux until ConPTY support is added"},
	}
}

func runCapabilitiesCommand(opts options) error {
	rows := capabilityMatrix()
	if opts.markdown {
		fmt.Println("# Collomia capability matrix")
		fmt.Println()
		fmt.Println("Generated by `collo capabilities --markdown`. Status meanings: **implemented** (works today), **experimental** (usable, incomplete), **unsupported** (not yet built).")
		fmt.Println()
		fmt.Println("| Area | Capability | Status | Notes |")
		fmt.Println("| --- | --- | --- | --- |")
		for _, r := range rows {
			fmt.Printf("| %s | %s | %s | %s |\n", r.area, r.capability, r.status, r.notes)
		}
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
