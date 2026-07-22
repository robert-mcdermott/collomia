package tui

import (
	"strings"
)

type commandInfo struct {
	name, args, desc string
	// complete marks palette entries whose name is a full command line
	// (argument completion) rather than a command awaiting arguments.
	complete bool
}

var slashCommands = []commandInfo{
	{name: "/help", args: "", desc: "show slash commands and keybindings"},
	{name: "/status", args: "", desc: "workspace, provider, model, and autonomy"},
	{name: "/model", args: "[provider[/model]]", desc: "show or switch the active provider/model"},
	{name: "/models", args: "", desc: "list configured providers and default models"},
	{name: "/context", args: "", desc: "token usage and estimated context size"},
	{name: "/plan", args: "[on|off]", desc: "toggle read-only planning mode"},
	{name: "/autonomy", args: "[mode]", desc: "set ask, workspace, or autopilot"},
	{name: "/theme", args: "[name]", desc: "list or switch color themes"},
	{name: "/skills", args: "[list]", desc: "pick a skill to use (list prints them instead)"},
	{name: "/agents", args: "[stop|steer|apply …]", desc: "inspect, guide, stop, or integrate delegated agents"},
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
	{name: "/undo", args: "", desc: "revert the agent's most recent file change"},
	{name: "/tasks", args: "", desc: "show the structured task plan"},
	{name: "/ps", args: "", desc: "list background processes (stop with /ps stop <id>)"},
	{name: "/sessions", args: "", desc: "pick a saved session to resume"},
	{name: "/rewind", args: "[turn]", desc: "branch from an earlier completed turn without undoing files"},
	{name: "/retry", args: "", desc: "load the previous prompt for review without running it"},
	{name: "/new", args: "", desc: "start a fresh session (current one stays saved)"},
	{name: "/compact", args: "[focus]", desc: "summarize older context to free the window"},
	{name: "/config", args: "", desc: "show the active configuration path"},
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
		candidates = []candidate{{"on", "read-only planning mode"}, {"off", "execution mode"}}
	case "/model":
		for _, name := range m.runtime.Config.ProviderNames() {
			p := m.runtime.Config.Providers[name]
			desc := p.Type
			if p.Model != "" {
				desc += " · " + p.Model
			}
			candidates = append(candidates, candidate{name, desc})
		}
	default:
		return nil
	}
	var out []commandInfo
	for _, c := range candidates {
		if _, ok := fuzzyScore(partial, c.value); ok {
			out = append(out, commandInfo{name: command + " " + c.value, desc: c.desc, complete: true})
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

func (m *Model) renderPalette() string {
	rows := m.paletteRows()
	if rows == 0 {
		return ""
	}
	start := 0
	if m.paletteSel >= rows {
		start = m.paletteSel - rows + 1
	}
	nameWidth := 0
	for _, cmd := range m.palette {
		if w := len(cmd.name + " " + cmd.args); w > nameWidth {
			nameWidth = w
		}
	}
	var lines []string
	for i := start; i < start+rows && i < len(m.palette); i++ {
		cmd := m.palette[i]
		label := cmd.name
		if cmd.args != "" {
			label += " " + cmd.args
		}
		label += strings.Repeat(" ", max(0, nameWidth-len(label)))
		if i == m.paletteSel {
			lines = append(lines, m.styles.paletteSel.Render("▸ "+label)+"  "+m.styles.paletteDesc.Render(cmd.desc))
		} else {
			lines = append(lines, "  "+m.styles.paletteCmd.Render(label)+"  "+m.styles.paletteDesc.Render(cmd.desc))
		}
	}
	hint := m.styles.paletteDesc.Render("↑↓ select · tab complete · enter run · esc dismiss")
	body := strings.Join(lines, "\n") + "\n" + hint
	return m.styles.paletteBox.Width(max(1, m.width-2) - 4).Render(body)
}

// paletteHeight is the number of terminal rows the palette currently occupies.
func (m *Model) paletteHeight() int {
	if !m.paletteOn {
		return 0
	}
	return m.paletteRows() + 3 // rows + hint line + top/bottom border
}
