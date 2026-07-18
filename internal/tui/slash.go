package tui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robert-mcdermott/collomia/internal/app"
)

func (m *Model) slash(line string) (bool, tea.Cmd) {
	parts := strings.Fields(line)
	command := strings.ToLower(parts[0])
	args := parts[1:]
	switch command {
	case "/quit", "/exit":
		return true, nil
	case "/review":
		if m.busy {
			m.addError(fmt.Errorf("wait for the current turn to finish first"))
			break
		}
		ref := ""
		if len(args) > 0 {
			ref = args[0]
		}
		return false, m.startTurn(app.ReviewPrompt(ref))
	case "/verify":
		if m.busy {
			m.addError(fmt.Errorf("wait for the current turn to finish first"))
			break
		}
		return false, m.startTurn(app.VerifyPrompt(strings.Join(args, " ")))
	case "/help":
		var lines []string
		for _, cmd := range slashCommands {
			label := cmd.name
			if cmd.args != "" {
				label += " " + cmd.args
			}
			lines = append(lines, fmt.Sprintf("%-26s %s", label, cmd.desc))
		}
		m.addSystem("Slash commands:\n" + strings.Join(lines, "\n") + "\n\nPress ctrl+t for the Session and Help tabs.")
	case "/status":
		m.addSystem(m.runtime.Summary())
	case "/models":
		var lines []string
		for _, name := range m.runtime.Config.ProviderNames() {
			p := m.runtime.Config.Providers[name]
			lines = append(lines, fmt.Sprintf("- %s  [%s]  %s", name, p.Type, p.Model))
		}
		m.addSystem("Configured providers:\n" + strings.Join(lines, "\n"))
	case "/model":
		if len(args) == 0 {
			m.openModelPicker()
			break
		}
		selection := strings.Join(args, " ")
		providerName, currentModel := m.runtime.Agent.Selection()
		model := selection
		if candidate, rest, ok := strings.Cut(selection, "/"); ok {
			if _, exists := m.runtime.Config.Providers[candidate]; exists {
				providerName = candidate
				model = rest
			}
		}
		if _, exists := m.runtime.Config.Providers[selection]; exists {
			providerName = selection
			model = ""
		}
		if err := m.runtime.Select(providerName, model); err != nil {
			m.addError(err)
			break
		}
		providerName, currentModel = m.runtime.Agent.Selection()
		m.addSystem(fmt.Sprintf("Switched to %s/%s", providerName, currentModel))
	case "/context":
		usage := m.runtime.Agent.Usage()
		estimate, window := m.runtime.Agent.ContextEstimate()
		windowText := "unknown"
		if window > 0 {
			windowText = fmt.Sprintf("%d", window)
		}
		cached := ""
		if usage.CachedTokens > 0 {
			cached = fmt.Sprintf(" (%d cached)", usage.CachedTokens)
		}
		reasoning := ""
		if usage.ReasoningTokens > 0 {
			reasoning = fmt.Sprintf(" (%d reasoning)", usage.ReasoningTokens)
		}
		sessionID := ""
		if m.runtime.Session != nil {
			sessionID = "\nSession: " + m.runtime.Session.Meta.ID
		}
		breakdown := m.runtime.Agent.ContextBreakdown()
		inspector := fmt.Sprintf("\n\nWhat the model sees each request (≈4 chars/token):\n  system prompt      ~%s tokens\n  project instructions ~%s tokens\n  skills summary     ~%s tokens\n  tool results       ~%s tokens across %d messages",
			formatTokens(breakdown.SystemPromptChars/4), formatTokens(breakdown.InstructionsChars/4), formatTokens(breakdown.SkillsSummaryChars/4), formatTokens(breakdown.ToolResultChars/4), breakdown.MessagesByRole["tool"])
		inspector += fmt.Sprintf("\n  conversation       %d user / %d assistant messages", breakdown.MessagesByRole["user"], breakdown.MessagesByRole["assistant"])
		if breakdown.Summaries > 0 {
			inspector += fmt.Sprintf("\n  compaction         %d summary block(s) replacing older history", breakdown.Summaries)
		}
		inspector += "\n\n/compact frees the window; the full transcript always survives in the session log."
		m.addSystem(fmt.Sprintf("Provider usage this session: %d input%s / %d output%s tokens\nEstimated current prompt: ~%d tokens of %s\nMessages: %d%s%s", usage.InputTokens, cached, usage.OutputTokens, reasoning, estimate, windowText, m.runtime.Agent.MessageCount(), sessionID, inspector))
	case "/plan":
		enabled := !m.runtime.Agent.Plan()
		if len(args) > 0 {
			switch strings.ToLower(args[0]) {
			case "on", "true":
				enabled = true
			case "off", "false":
				enabled = false
			}
		}
		m.runtime.Agent.SetPlan(enabled)
		if enabled {
			m.addSystem("Planning mode enabled. Only read-only discovery tools are exposed.")
		} else {
			m.addSystem("Planning mode disabled. Execution tools are available subject to permissions.")
		}
	case "/autonomy":
		if len(args) == 0 {
			m.addSystem("Autonomy mode: " + m.runtime.Permissions.Mode() + " (ask, workspace, autopilot)")
			break
		}
		if err := m.runtime.Permissions.SetMode(strings.ToLower(args[0])); err != nil {
			m.addError(err)
			break
		}
		note := "Autonomy set to " + m.runtime.Permissions.Mode()
		if m.runtime.Permissions.Mode() == "autopilot" {
			note += ". Workspace tools can now run without prompts; hard safety denials still apply."
		}
		m.addSystem(note)
	case "/skills":
		if len(m.runtime.Skills.Skills) == 0 {
			m.addSystem("No skills discovered.")
			break
		}
		var lines []string
		for _, skill := range m.runtime.Skills.Skills {
			lines = append(lines, fmt.Sprintf("- %s: %s", skill.Name, skill.Description))
		}
		m.addSystem("Discovered skills:\n" + strings.Join(lines, "\n"))
	case "/mcp":
		servers := m.runtime.MCP.Servers()
		if len(servers) == 0 {
			m.addSystem("No MCP servers connected.")
			break
		}
		sort.Strings(servers)
		m.addSystem("Connected MCP servers: " + strings.Join(servers, ", "))
	case "/tools":
		m.addSystem("Available tools: " + strings.Join(m.runtime.Registry.Names(), ", "))
	case "/theme":
		if len(args) == 0 {
			m.openThemePicker()
			break
		}
		t, ok := themeByName(args[0])
		if !ok {
			m.addError(fmt.Errorf("unknown theme %q; use /theme to list themes", args[0]))
			break
		}
		m.applyTheme(t)
		m.addSystem("Theme switched to " + t.Name + ".")
	case "/diff":
		diff := m.runtime.Changes.Diff(m.runtime.Workspace)
		if strings.TrimSpace(diff) == "" {
			m.addSystem("No agent file changes this session.")
			break
		}
		m.blocks = append(m.blocks, block{role: "tool-result", content: "```diff\n" + diff + "```"})
	case "/undo":
		snapshot, err := m.runtime.Changes.Undo()
		if err != nil {
			m.addError(err)
			break
		}
		m.addSystem(fmt.Sprintf("Undid %s of %s. Run /undo again to revert earlier changes.", snapshot.Op, snapshot.Path))
	case "/tasks":
		m.addSystem(m.runtime.Plan.Current().Render())
	case "/ps":
		if len(args) == 2 && args[0] == "stop" {
			id, convErr := strconv.Atoi(args[1])
			if convErr != nil {
				m.addError(fmt.Errorf("usage: /ps stop <id>"))
				break
			}
			out, err := m.runtime.Registry.Execute(context.Background(), "stop_process", []byte(fmt.Sprintf(`{"id":%d}`, id)))
			if err != nil {
				m.addError(err)
				break
			}
			m.addSystem(out)
			break
		}
		procs := m.runtime.Processes.Snapshot()
		if len(procs) == 0 {
			m.addSystem("No background processes have been started this session.")
			break
		}
		var lines []string
		for _, p := range procs {
			lines = append(lines, fmt.Sprintf("[%d] %s — %s (started %s ago)", p.ID, p.Command, p.Status, time.Since(p.Started).Round(time.Second)))
		}
		m.addSystem("Background processes:\n" + strings.Join(lines, "\n") + "\n\n/ps stop <id> stops one; all are stopped at exit.")
	case "/sessions", "/resume":
		m.openSessionPicker()
	case "/new":
		if m.busy {
			m.addError(fmt.Errorf("wait for the current turn to finish first"))
			break
		}
		if err := m.runtime.NewSession(); err != nil {
			m.addError(err)
			break
		}
		m.blocks = nil
		m.addSystem("Started a fresh session (" + m.runtime.Session.Meta.ID + "). The previous conversation is saved — /sessions to return to it.")
	case "/compact":
		count, err := m.runtime.Agent.Compact(context.Background(), strings.Join(args, " "))
		if err != nil {
			m.addError(err)
			break
		}
		estimate, window := m.runtime.Agent.ContextEstimate()
		m.addSystem(fmt.Sprintf("Compacted %d messages into a summary. Estimated context is now ~%d tokens (window %d). The full transcript remains in the session log.", count, estimate, window))
	case "/config":
		m.addSystem("Active configuration: " + m.runtime.Config.Source + "\nProject configuration takes precedence over the user configuration. Run `collo init` to create " + m.runtime.Workspace + "/.collomia.json.")
	case "/clear":
		m.runtime.Agent.Clear()
		m.blocks = nil
		m.addSystem("Conversation context cleared.")
	default:
		m.addError(fmt.Errorf("unknown command %s; use /help", command))
	}
	return false, nil
}

func (m *Model) addSystem(value string) {
	m.blocks = append(m.blocks, block{role: "system", content: value})
}
func (m *Model) addError(err error) {
	m.blocks = append(m.blocks, block{role: "error", content: err.Error()})
}
