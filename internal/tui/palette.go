package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type commandInfo struct {
	name, args, desc string
	// complete marks palette entries whose name is a full command line
	// (argument completion) rather than a command awaiting arguments.
	complete bool
	// needsArg keeps a subcommand suggestion in the composer instead of
	// executing it, so a required second argument can be entered.
	needsArg bool
}

var slashCommands = []commandInfo{
	{name: "/help", args: "", desc: "show slash commands and keybindings"},
	{name: "/status", args: "", desc: "workspace, provider, model, and autonomy"},
	{name: "/model", args: "[provider[/model]]", desc: "show or switch the active provider/model"},
	{name: "/agent", args: "[name]", desc: "show or switch the named primary agent profile"},
	{name: "/models", args: "", desc: "list configured providers and default models"},
	{name: "/context", args: "", desc: "token usage and estimated context size"},
	{name: "/plan", args: "[on|off]", desc: "toggle read-only planning mode"},
	{name: "/orchestrate", args: "[goal|approve|status [node]|pause|resume|retry node|extend|integrate node|reconcile|discard node [confirm]|cancel]", desc: "explicit experimental goal proposal and execution"},
	{name: "/autonomy", args: "[mode]", desc: "set ask, workspace, or autopilot"},
	{name: "/theme", args: "[name]", desc: "list or switch color themes"},
	{name: "/skills", args: "[list]", desc: "pick a skill to use (list prints them instead)"},
	{name: "/agents", args: "[stop|steer|verify|compare|apply …]", desc: "inspect, guide, verify, compare, stop, or integrate delegated agents"},
	{name: "/prompt", args: "[workspace-file]", desc: "load a UTF-8 text file into the composer"},
	{name: "/attach", args: "[workspace-image]", desc: "attach a PNG, JPEG, GIF, or WebP image"},
	{name: "/attachments", args: "", desc: "list images attached to the pending prompt"},
	{name: "/detach", args: "[number|all]", desc: "remove a pending image attachment"},
	{name: "/mcp", args: "[list|prompts|resources|ping|refresh|reconnect|enable|disable|add|remove]", desc: "browse and manage MCP servers, prompts, and resources"},
	{name: "/tools", args: "", desc: "list tools visible to the agent"},
	{name: "/review", args: "[ref] [instructions…]", desc: "review changes; '-' = uncommitted; extra words focus the review"},
	{name: "/verify", args: "[focus]", desc: "detect and run build/lint/test commands"},
	{name: "/diff", args: "", desc: "show all agent file changes this session"},
	{name: "/transcript", args: "", desc: "search and copy the complete session transcript"},
	{name: "/activity", args: "", desc: "search and filter tools, decisions, changes, agents, and failures"},
	{name: "/undo", args: "", desc: "revert the agent's most recent file change"},
	{name: "/tasks", args: "", desc: "show the structured task plan"},
	{name: "/ps", args: "", desc: "list background processes (stop with /ps stop <id>)"},
	{name: "/sessions", args: "", desc: "pick a saved session to resume"},
	{name: "/rewind", args: "[turn]", desc: "branch from an earlier completed turn without undoing files"},
	{name: "/restore", args: "[turn|integration [id]]", desc: "return to a completed turn, or inspect and undo an interrupted integration"},
	{name: "/retry", args: "", desc: "load the previous prompt for review without running it"},
	{name: "/new", args: "", desc: "start a fresh session (current one stays saved)"},
	{name: "/compact", args: "[focus]", desc: "summarize older context to free the window"},
	{name: "/config", args: "[all]", desc: "show what the configuration resolved to and which layer set it"},
	{name: "/clear", args: "", desc: "clear conversation context"},
	{name: "/quit", args: "", desc: "exit Collomia"},
}

// matchCommands returns slash commands matching the typed token, prefix
// matches first, then substring matches, preserving declaration order within
// each group.
func matchCommands(token string) []commandInfo {
	query := strings.ToLower(strings.TrimPrefix(token, "/"))
	var prefix, substr []commandInfo
	for _, cmd := range slashCommands {
		name := strings.TrimPrefix(cmd.name, "/")
		switch {
		case query == "":
			prefix = append(prefix, cmd)
		case strings.HasPrefix(name, query):
			prefix = append(prefix, cmd)
		case strings.Contains(name, query):
			substr = append(substr, cmd)
		}
	}
	return append(prefix, substr...)
}

// argumentMatches suggests completion values for a slash command's first
// argument, filtered by the fuzzy matcher.
func (m *Model) argumentMatches(command, partial string) []commandInfo {
	type candidate struct{ value, desc string }
	var candidates []candidate
	switch command {
	case "/theme":
		for _, t := range themes {
			desc := "color theme"
			if t.Name == m.theme.Name {
				desc = "current theme"
			}
			candidates = append(candidates, candidate{t.Name, desc})
		}
	case "/autonomy":
		candidates = []candidate{
			{"ask", "confirm every write, command, and MCP call"},
			{"workspace", "auto-approve workspace writes; commands still ask"},
			{"autopilot", "auto-approve workspace actions (hard denials remain)"},
		}
	case "/plan":
		off := "execution mode"
		if m.runtime.OrchestratedGoalPhase() == "proposal" {
			off = "cancel goal proposal and restore execution mode"
		}
		candidates = []candidate{{"on", "read-only planning mode"}, {"off", off}}
	case "/orchestrate":
		candidates = []candidate{
			{"approve", "approve the fresh visible proposal and execute once"},
			{"status", "inspect proposal or runtime graph state"},
			{"pause", "pause at the next safe scheduling boundary"},
			{"resume", "resume a paused graph or reattach a saved graph"},
			{"retry", "safely retry an eligible blocked node"},
			{"extend", "grant an exhausted graph another bounded envelope"},
			{"integrate", "publish a verified candidate into your workspace (unverified combined)"},
			{"reconcile", "observe what is actually left in each retained worktree"},
			{"discard", "remove a reconciled retained worktree you no longer want"},
			{"cancel", "cancel the proposal or active graph"},
		}
	case "/model":
		for _, name := range m.runtime.Config.ProviderNames() {
			p := m.runtime.Config.Providers[name]
			desc := p.Type
			if p.Model != "" {
				desc += " · " + p.Model
			}
			candidates = append(candidates, candidate{name, desc})
		}
	case "/agent":
		for _, name := range m.runtime.PrimaryAgentNames() {
			desc := "ordinary primary agent"
			if name != "default" {
				profile := m.runtime.Config.Agents[name]
				desc = profile.Availability
				if profile.Model != "" {
					desc += " · " + profile.Model
				}
			}
			if name == m.runtime.ActiveAgent || (name == "default" && m.runtime.ActiveAgent == "") {
				desc += " · current"
			}
			candidates = append(candidates, candidate{name, desc})
		}
	default:
		return nil
	}
	var out []commandInfo
	for _, c := range candidates {
		if _, ok := fuzzyScore(partial, c.value); ok {
			needsArg := command == "/orchestrate" && (c.value == "retry" || c.value == "discard" || c.value == "integrate")
			out = append(out, commandInfo{name: command + " " + c.value, desc: c.desc, complete: !needsArg, needsArg: needsArg})
		}
	}
	return out
}

const paletteMaxRows = 7

// updatePalette recomputes the command palette from the current input value.
// It returns true when the palette open/closed state or row count changed so
// the caller can re-run layout.
func (m *Model) updatePalette() bool {
	value := m.input.Value()
	if value != m.lastInput {
		m.paletteDismissed = false
		m.lastInput = value
	}
	open := false
	var matches []commandInfo
	if m.pending == nil && !m.paletteDismissed &&
		strings.HasPrefix(value, "/") && !strings.Contains(value, "\n") {
		fields := strings.Fields(value)
		token := "/"
		if len(fields) > 0 {
			token = fields[0]
		}
		switch {
		case len(fields) <= 1 && !strings.HasSuffix(value, " "):
			matches = matchCommands(token)
			open = len(matches) > 0
		case len(fields) >= 1:
			// Argument completion: suggest known values for the command.
			partial := ""
			if len(fields) > 1 {
				partial = fields[1]
			}
			if len(fields) <= 2 && !strings.HasSuffix(value, " ") || (len(fields) == 1 && strings.HasSuffix(value, " ")) {
				matches = m.argumentMatches(fields[0], partial)
				open = len(matches) > 0
			}
		}
		if m.busy {
			filtered := matches[:0]
			for _, match := range matches {
				if busySlashAllowed(match.name) {
					filtered = append(filtered, match)
				}
			}
			matches = filtered
			open = len(matches) > 0
		}
	}
	prevOpen, prevRows := m.paletteOn, len(m.palette)
	m.paletteOn = open
	m.palette = matches
	if m.paletteSel >= len(matches) {
		m.paletteSel = 0
	}
	return prevOpen != open || prevRows != len(matches)
}

func (m *Model) paletteRows() int {
	if !m.paletteOn {
		return 0
	}
	if len(m.palette) > paletteMaxRows {
		return paletteMaxRows
	}
	return len(m.palette)
}

const (
	// paletteMarkerWidth is the "▸ " selection gutter, which is present on
	// every row so the name column does not shift as the selection moves.
	paletteMarkerWidth = 2
	// paletteGapWidth separates the name column from the description.
	paletteGapWidth = 2
	// paletteMinDescWidth is the narrowest description worth keeping. Below
	// it the column is dropped entirely rather than shown as an ellipsis.
	paletteMinDescWidth = 16
	// paletteNameMaxWidth fits every command and argument list but /mcp's,
	// which is longer than most terminals are wide. Sizing the column to that
	// one entry would leave a hand's width of empty space on every other row.
	paletteNameMaxWidth = 30
)

func (m *Model) renderPalette() string {
	rows := m.paletteRows()
	if rows == 0 {
		return ""
	}
	start := 0
	if m.paletteSel >= rows {
		start = m.paletteSel - rows + 1
	}

	// Match the composer: same outer width, so the two boxes stack as one
	// control instead of two differently sized panels.
	outer := max(1, m.width-2)
	inner := max(1, outer-2) // the box pads one column on each side

	// Padding every name to the widest entry is what pushed descriptions off
	// the edge and made lipgloss soft-wrap each row into the next one. Cap
	// the column and truncate into it.
	nameCap := max(8, min(paletteNameMaxWidth, (inner-paletteMarkerWidth-paletteGapWidth)/2))
	nameWidth := 0
	for _, cmd := range m.palette {
		if w := ansi.StringWidth(paletteLabel(cmd)); w > nameWidth {
			nameWidth = w
		}
	}
	nameWidth = min(nameWidth, nameCap)

	descWidth := inner - paletteMarkerWidth - nameWidth - paletteGapWidth
	if descWidth < paletteMinDescWidth {
		descWidth = 0
		nameWidth = inner - paletteMarkerWidth
	}

	var lines []string
	for i := start; i < start+rows && i < len(m.palette); i++ {
		cmd := m.palette[i]
		label := padTo(ansi.Truncate(paletteLabel(cmd), nameWidth, "…"), nameWidth)
		desc := ""
		if descWidth > 0 {
			desc = ansi.Truncate(cmd.desc, descWidth, "…")
		}
		if i == m.paletteSel {
			// Highlight the whole row rather than just the name. Styling the
			// padded label alone drew a bar whose length tracked the longest
			// command in the list, which read as a rendering fault.
			row := padTo("▸ "+label+strings.Repeat(" ", paletteGapWidth)+desc, inner)
			lines = append(lines, m.styles.paletteSel.Render(row))
			continue
		}
		row := strings.Repeat(" ", paletteMarkerWidth) + m.styles.paletteCmd.Render(label)
		if desc != "" {
			row += strings.Repeat(" ", paletteGapWidth) + m.styles.paletteDesc.Render(desc)
		}
		lines = append(lines, row)
	}
	hint := m.styles.paletteDesc.Render(ansi.Truncate("↑↓ select · tab complete · enter run · esc dismiss", inner, "…"))
	body := strings.Join(lines, "\n") + "\n" + hint
	return m.styles.paletteBox.Width(outer).Render(body)
}

func paletteLabel(cmd commandInfo) string {
	if cmd.args == "" {
		return cmd.name
	}
	return cmd.name + " " + cmd.args
}

// padTo right-pads to an exact display width, measuring cells rather than
// bytes so multi-byte names stay aligned.
func padTo(value string, width int) string {
	if gap := width - ansi.StringWidth(value); gap > 0 {
		return value + strings.Repeat(" ", gap)
	}
	return value
}

// paletteHeight is the number of terminal rows the palette currently occupies.
func (m *Model) paletteHeight() int {
	if !m.paletteOn {
		return 0
	}
	return m.paletteRows() + 3 // rows + hint line + top/bottom border
}
