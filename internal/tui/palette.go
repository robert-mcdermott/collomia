package tui

import (
	"strings"
)

type commandInfo struct{ name, args, desc string }

var slashCommands = []commandInfo{
	{"/help", "", "show slash commands and keybindings"},
	{"/status", "", "workspace, provider, model, and autonomy"},
	{"/model", "[provider[/model]]", "show or switch the active provider/model"},
	{"/models", "", "list configured providers and default models"},
	{"/context", "", "token usage and estimated context size"},
	{"/plan", "[on|off]", "toggle read-only planning mode"},
	{"/autonomy", "[mode]", "set ask, workspace, or autopilot"},
	{"/theme", "[name]", "list or switch color themes"},
	{"/skills", "", "list discovered SKILL.md / skills.md instructions"},
	{"/mcp", "", "list connected MCP servers"},
	{"/tools", "", "list tools visible to the agent"},
	{"/diff", "", "show all agent file changes this session"},
	{"/undo", "", "revert the agent's most recent file change"},
	{"/tasks", "", "show the structured task plan"},
	{"/sessions", "", "list saved sessions for this workspace"},
	{"/compact", "[focus]", "summarize older context to free the window"},
	{"/config", "", "show the active configuration path"},
	{"/clear", "", "clear conversation context"},
	{"/quit", "", "exit Collomia"},
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
	if !m.busy && m.pending == nil && !m.paletteDismissed &&
		strings.HasPrefix(value, "/") && !strings.Contains(value, "\n") {
		fields := strings.Fields(value)
		token := "/"
		if len(fields) > 0 {
			token = fields[0]
		}
		// Once the user is typing arguments, stop filtering on them.
		if len(fields) <= 1 && !strings.HasSuffix(value, " ") {
			matches = matchCommands(token)
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
