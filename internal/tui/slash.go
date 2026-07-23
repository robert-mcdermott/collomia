package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robert-mcdermott/collomia/internal/app"
)

func (m *Model) slash(line string) (bool, tea.Cmd) {
	if m.busy && !busySlashAllowed(line) {
		m.addError(fmt.Errorf("%s is unavailable while the current turn is running", strings.Fields(line)[0]))
		return false, nil
	}
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
		ref, instructions := "", ""
		if len(args) > 0 {
			ref = args[0]
			instructions = strings.Join(args[1:], " ")
		}
		return false, m.startTurn(app.ReviewPrompt(ref, instructions))
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
		m.addPanel("Slash commands", strings.Join(lines, "\n")+"\n\nPress ctrl+t for the Session and Help tabs.")
	case "/status":
		m.addPanel("Status", m.runtime.Summary())
	case "/models":
		m.addPanel("Provider models", renderProviderStatuses(m.runtime.ConfiguredProviders()))
		runtime := m.runtime
		return false, func() tea.Msg {
			return providerStatusMsg{statuses: runtime.InspectProviders(context.Background())}
		}
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
		inspector := fmt.Sprintf("\n\nWhat the model sees each request (≈4 chars/token):\n  base system prompt ~%s tokens\n  project instructions ~%s tokens\n  pinned plan/state  ~%s tokens\n  skills summary     ~%s tokens\n  tool results       ~%s tokens across %d messages",
			formatTokens(breakdown.SystemPromptChars/4), formatTokens(breakdown.InstructionsChars/4), formatTokens(breakdown.PinnedStateChars/4), formatTokens(breakdown.SkillsSummaryChars/4), formatTokens(breakdown.ToolResultChars/4), breakdown.MessagesByRole["tool"])
		inspector += fmt.Sprintf("\n  conversation       %d user / %d assistant messages", breakdown.MessagesByRole["user"], breakdown.MessagesByRole["assistant"])
		if breakdown.Summaries > 0 {
			inspector += fmt.Sprintf("\n  compaction         %d summary block(s) replacing older history", breakdown.Summaries)
		}
		if breakdown.ArtifactCount > 0 {
			inspector += fmt.Sprintf("\n  retained results   %d artifact(s), %s on disk and outside the prompt", breakdown.ArtifactCount, formatByteCount(breakdown.ArtifactBytes))
		}
		if breakdown.ImageCount > 0 {
			inspector += fmt.Sprintf("\n  images             %d typed attachment(s); pre-usage estimate reserves ~1K tokens each", breakdown.ImageCount)
		}
		inspector += "\n\n/compact frees the window; the full transcript always survives in the session log."
		m.addPanel("Context & usage", fmt.Sprintf("Provider usage this session: %d input%s / %d output%s tokens\nEstimated current prompt: ~%d tokens of %s\nMessages: %d%s%s", usage.InputTokens, cached, usage.OutputTokens, reasoning, estimate, windowText, m.runtime.Agent.MessageCount(), sessionID, inspector))
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
		if len(args) > 0 && args[0] == "list" {
			if len(m.runtime.Skills.Skills) == 0 && len(m.runtime.Skills.Disabled) == 0 {
				m.addPanel("Skills", "No skills installed. Create one with `collo skills new <name>` (project) or `collo skills new <name> --global`.")
				break
			}
			var lines []string
			for _, skill := range m.runtime.Skills.Skills {
				line := fmt.Sprintf("- %s (%s", skill.Name, skill.Source)
				if skill.Version != "" {
					line += " v" + skill.Version
				}
				line += "): " + skill.Description
				if n := skill.BundleCount(); n > 0 {
					line += fmt.Sprintf("  [%d bundled files]", n)
				}
				lines = append(lines, line)
			}
			for _, skill := range m.runtime.Skills.Disabled {
				lines = append(lines, fmt.Sprintf("- %s (%s, disabled): %s", skill.Name, skill.Source, skill.Description))
			}
			m.addPanel("Skills", strings.Join(lines, "\n"))
			break
		}
		m.openSkillPicker()
	case "/agents":
		if len(args) > 0 {
			switch args[0] {
			case "stop":
				if len(args) < 2 {
					m.addError(fmt.Errorf("usage: /agents stop <id-or-name>"))
					break
				}
				target := strings.Join(args[1:], " ")
				if err := m.runtime.Team.Stop(target); err != nil {
					m.addError(err)
				} else {
					m.addSystem("Cancellation requested for delegated agent " + target + ".")
				}
			case "steer":
				if len(args) < 3 {
					m.addError(fmt.Errorf("usage: /agents steer <id> <guidance…>"))
					break
				}
				guidance := strings.Join(args[2:], " ")
				if err := m.runtime.Team.Steer(args[1], guidance); err != nil {
					m.addError(err)
				} else {
					m.addSystem("Guidance queued for delegated agent " + args[1] + "; it will be delivered at the next model boundary and grants no permissions.")
				}
			case "apply":
				if len(args) != 2 {
					m.addError(fmt.Errorf("usage: /agents apply <id>"))
					break
				}
				if err := m.openAgentIntegration(args[1]); err != nil {
					m.addError(err)
				}
			default:
				m.addError(fmt.Errorf("usage: /agents [stop <id-or-name>|steer <id> <guidance…>|apply <id>]"))
			}
			break
		}
		m.openAgentPicker()
	case "/prompt":
		path, err := promptPathArgument(line)
		if err != nil {
			m.addError(fmt.Errorf("usage: /prompt [workspace-file]: %w", err))
			break
		}
		if path == "" {
			m.openPromptFilePicker()
			break
		}
		if err := m.loadPromptFile(path); err != nil {
			m.addError(err)
		}
	case "/attach":
		path, err := promptPathArgument(line)
		if err != nil {
			m.addError(fmt.Errorf("usage: /attach [workspace-image]: %w", err))
			break
		}
		if path == "" {
			m.openImagePicker()
			break
		}
		if err := m.addImageAttachment(path); err != nil {
			m.addError(err)
		}
	case "/attachments":
		m.showPendingAttachments()
	case "/detach":
		if err := m.detachImage(args); err != nil {
			m.addError(err)
		}
	case "/mcp":
		if len(args) > 0 {
			m.mcpCommand(args)
			break
		}
		m.openMCPPicker()
	case "/tools":
		m.addPanel("Tools", strings.Join(m.runtime.Registry.Names(), ", "))
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
		m.openDiffView()
	case "/transcript":
		m.openTranscriptView()
	case "/activity":
		m.openActivityView()
	case "/undo":
		snapshot, err := m.runtime.Changes.Undo()
		if err != nil {
			m.addError(err)
			break
		}
		m.addSystem(fmt.Sprintf("Undid %s of %s. Run /undo again to revert earlier changes.", snapshot.Op, snapshot.Path))
	case "/tasks":
		m.addPanel("Task plan", m.runtime.Plan.Current().Render())
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
			m.addPanel("Background processes", "No background processes have been started this session.")
			break
		}
		var lines []string
		for _, p := range procs {
			lines = append(lines, fmt.Sprintf("[%d] %s — %s (started %s ago)", p.ID, p.Command, p.Status, time.Since(p.Started).Round(time.Second)))
		}
		m.addPanel("Background processes", strings.Join(lines, "\n")+"\n\n/ps stop <id> stops one; all are stopped at exit.")
	case "/sessions", "/resume":
		m.openSessionPicker()
	case "/rewind":
		if m.busy {
			m.addError(fmt.Errorf("wait for the current turn to finish first"))
			break
		}
		if len(args) == 0 {
			m.openRewindPicker()
			break
		}
		if len(args) != 1 {
			m.addError(fmt.Errorf("usage: /rewind [completed-turn-number]"))
			break
		}
		turn, err := strconv.Atoi(args[0])
		if err != nil || turn < 0 {
			m.addError(fmt.Errorf("rewind target must be a non-negative completed turn number"))
			break
		}
		m.rewindTo(turn)
	case "/retry":
		if m.busy {
			m.addError(fmt.Errorf("wait for the current turn to finish first"))
			break
		}
		if len(m.promptHistory) == 0 {
			m.addError(fmt.Errorf("there is no previous prompt in this session"))
			break
		}
		m.setComposerValue(m.promptHistory[len(m.promptHistory)-1])
		m.addSystem("Loaded the previous prompt into the composer for review. Nothing has been sent; edit it or press enter when ready.")
	case "/new":
		if m.busy {
			m.addError(fmt.Errorf("wait for the current turn to finish first"))
			break
		}
		m.saveSessionDraft()
		if err := m.runtime.NewSession(); err != nil {
			m.addError(err)
			break
		}
		m.rebuildTranscript()
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
		m.addPanel("Configuration", "Active configuration: "+m.runtime.Config.Source+"\nProject configuration takes precedence over the user configuration. Run `collo init` to create "+m.runtime.Workspace+"/.collomia.json.")
	case "/clear":
		m.runtime.Agent.Clear()
		m.blocks = nil
		m.addSystem("Conversation context cleared.")
	default:
		m.addError(fmt.Errorf("unknown command %s; use /help", command))
	}
	return false, nil
}

// busySlashAllowed defines the deliberately small local-control lane that is
// available while a provider turn is active. These commands inspect local
// state or control a child; they never submit another model prompt, switch the
// session/provider, change autonomy, or integrate workspace bytes.
func busySlashAllowed(line string) bool {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return false
	}
	switch strings.ToLower(fields[0]) {
	case "/help", "/status", "/context", "/tasks", "/tools", "/config", "/attachments", "/transcript", "/activity", "/diff":
		return len(fields) == 1
	case "/ps":
		return len(fields) == 1
	case "/agents":
		if len(fields) == 1 {
			return true
		}
		return fields[1] == "stop" || fields[1] == "steer"
	default:
		return false
	}
}

func (m *Model) addSystem(value string) {
	m.blocks = append(m.blocks, block{role: "system", content: value})
}
func (m *Model) addError(err error) {
	m.blocks = append(m.blocks, block{role: "error", content: err.Error()})
}

func formatByteCount(value int) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	if value < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(value)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(value)/(1024*1024))
}
