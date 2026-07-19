package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robert-mcdermott/collomia/internal/app"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// openModelPicker lists every configured provider (and its default model)
// for fuzzy selection.
func (m *Model) openModelPicker() {
	currentProvider, currentModel := m.runtime.Agent.Selection()
	var items []pickerItem
	for _, name := range m.runtime.Config.ProviderNames() {
		p := m.runtime.Config.Providers[name]
		desc := p.Type
		if p.Model != "" {
			desc += " · " + p.Model
		}
		if capabilities, err := provider.CapabilitiesFor(p.Type, p.Model, p.Context); err == nil {
			desc += " · " + capabilities.CompactSummary()
		}
		if name == currentProvider {
			desc += "  (current: " + currentModel + ")"
		}
		items = append(items, pickerItem{id: name, title: name, desc: desc})
	}
	m.picker = newPicker("Switch provider", items, func(m *Model, item pickerItem) tea.Cmd {
		if err := m.runtime.Select(item.id, ""); err != nil {
			m.addError(err)
			return nil
		}
		providerName, model := m.runtime.Agent.Selection()
		m.addSystem(fmt.Sprintf("Switched to %s/%s. Checking which models %s offers…", providerName, model, providerName))
		runtime := m.runtime
		return func() tea.Msg {
			models, err := runtime.ListModels(context.Background(), item.id)
			return modelListMsg{provider: item.id, models: models, err: err}
		}
	})
	m.layout()
	m.refresh()
}

// modelListMsg carries a provider's discovered model catalog.
type modelListMsg struct {
	provider string
	models   []provider.ModelInfo
	err      error
}

// openDiscoveredModels shows the live model catalog for a provider.
func (m *Model) openDiscoveredModels(msg modelListMsg) {
	currentProvider, currentModel := m.runtime.Agent.Selection()
	if msg.err != nil || len(msg.models) == 0 || msg.provider != currentProvider {
		// Discovery is best-effort; the provider default is already active.
		return
	}
	var items []pickerItem
	for _, info := range msg.models {
		desc := info.DisplayName
		if summary := info.Capabilities.CompactSummary(); summary != "capabilities unknown" {
			if desc != "" {
				desc += " · "
			}
			desc += summary
		}
		if info.ID == currentModel {
			if desc != "" {
				desc += " · "
			}
			desc += "current"
		}
		items = append(items, pickerItem{id: info.ID, title: info.ID, desc: desc})
	}
	m.picker = newPicker("Pick a model on "+msg.provider, items, func(m *Model, item pickerItem) tea.Cmd {
		if err := m.runtime.Select(msg.provider, item.id); err != nil {
			m.addError(err)
			return nil
		}
		providerName, model := m.runtime.Agent.Selection()
		m.addSystem(fmt.Sprintf("Switched to %s/%s.", providerName, model))
		return nil
	})
	m.layout()
	m.refresh()
}

// providerStatusMsg carries the asynchronous live-catalog checks behind
// /models. The static capability declaration is rendered immediately.
type providerStatusMsg struct {
	statuses []app.ProviderStatus
}

func renderProviderStatuses(statuses []app.ProviderStatus) string {
	if len(statuses) == 0 {
		return "No providers are configured."
	}
	var lines []string
	for _, status := range statuses {
		model := status.DefaultModel
		if model == "" {
			model = "(no default model)"
		}
		lines = append(lines, fmt.Sprintf("- %s [%s] %s", status.Name, status.Type, model))
		availability := "checking live catalog…"
		switch status.Availability {
		case app.ProviderAvailable:
			availability = fmt.Sprintf("available · %d model(s) in live catalog", len(status.Models))
		case app.ProviderUnavailable:
			availability = "unavailable"
			if status.Error != "" {
				availability += " · " + status.Error
			}
		case app.ProviderUnverified:
			availability = "availability unverified · this adapter has no model catalog"
		}
		lines = append(lines, "    "+availability)
		lines = append(lines, "    "+status.Capabilities.DetailSummary())
		if len(status.Capabilities.Constraints) > 0 {
			lines = append(lines, "    note: "+strings.Join(status.Capabilities.Constraints, "; "))
		}
	}
	return strings.Join(lines, "\n")
}

func (m *Model) replaceProviderStatusPanel(statuses []app.ProviderStatus) {
	content := renderProviderStatuses(statuses)
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].role == "panel" && m.blocks[i].title == "Provider models" {
			m.blocks[i].content = content
			m.layout()
			m.refresh()
			return
		}
	}
	m.addPanel("Provider models", content)
}

// openThemePicker lists themes; choosing one applies it immediately.
func (m *Model) openThemePicker() {
	var items []pickerItem
	for _, t := range themes {
		desc := ""
		if t.Name == m.theme.Name {
			desc = "current"
		}
		items = append(items, pickerItem{id: t.Name, title: t.Name, desc: desc})
	}
	m.picker = newPicker("Switch theme", items, func(m *Model, item pickerItem) tea.Cmd {
		if t, ok := themeByName(item.id); ok {
			m.applyTheme(t)
			m.addSystem("Theme switched to " + t.Name + ". Set options.theme in the configuration to persist it.")
		}
		return nil
	})
	m.layout()
	m.refresh()
}

// openSessionPicker lists saved sessions; choosing one switches the live
// conversation to it without restarting the program.
func (m *Model) openSessionPicker() {
	if m.runtime.Sessions == nil {
		m.addSystem("Session persistence is unavailable.")
		return
	}
	if m.busy {
		m.addError(fmt.Errorf("wait for the current turn to finish before switching sessions"))
		return
	}
	metas, err := m.runtime.Sessions.List()
	if err != nil {
		m.addError(err)
		return
	}
	current := ""
	if m.runtime.Session != nil {
		current = m.runtime.Session.Meta.ID
	}
	var items []pickerItem
	for _, meta := range metas {
		if meta.Archived {
			continue
		}
		title := meta.Title
		if title == "" {
			title = meta.ID
		}
		desc := fmt.Sprintf("%d turns · %s", meta.Turns, meta.UpdatedAt.Local().Format("Jan 2 15:04"))
		if meta.ID == current {
			desc += " · current"
		}
		items = append(items, pickerItem{id: meta.ID, title: title, desc: desc})
	}
	if len(items) == 0 {
		m.addSystem("No saved sessions yet. Every conversation is saved automatically; /new starts a fresh one.")
		return
	}
	m.picker = newPicker("Resume session", items, func(m *Model, item pickerItem) tea.Cmd {
		if m.runtime.Session != nil && item.id == m.runtime.Session.Meta.ID {
			m.addSystem("Already on session " + item.id + ".")
			return nil
		}
		if err := m.runtime.SwitchSession(item.id); err != nil {
			m.addError(err)
			return nil
		}
		m.rebuildTranscript()
		m.addSystem(fmt.Sprintf("Resumed session %s (%d turns). The conversation continues from where it left off.", item.id, m.runtime.Session.Meta.Turns))
		return nil
	})
	m.layout()
	m.refresh()
}

// openSkillPicker lists discovered skills; choosing one pre-fills the input
// with a prompt that applies the skill, so the user only adds the task.
func (m *Model) openSkillPicker() {
	if len(m.runtime.Skills.Skills) == 0 {
		m.addPanel("Skills", "No skills installed. Scaffold one with `collo skills new <name>` (project scope; requires `collo trust`) or `collo skills new <name> --global` for every workspace.")
		return
	}
	var items []pickerItem
	for _, skill := range m.runtime.Skills.Skills {
		desc := skill.Description + "  · " + skill.Source
		items = append(items, pickerItem{id: skill.Name, title: skill.Name, desc: desc})
	}
	m.picker = newPicker("Use a skill", items, func(m *Model, item pickerItem) tea.Cmd {
		m.input.SetValue(`Use the "` + item.id + `" skill: `)
		m.input.CursorEnd()
		m.input.Focus()
		return nil
	})
	m.layout()
	m.refresh()
}

// openMCPPicker lists connected MCP servers; choosing one prints that
// server's tools with descriptions.
func (m *Model) openMCPPicker() {
	servers := m.runtime.MCP.Servers()
	if len(servers) == 0 {
		m.addPanel("MCP servers", "No MCP servers connected. Configure mcp.<name> in the configuration file (project servers require `collo trust`), connect one now with /mcp add <name> <command…>, or check /mcp status for connection errors.")
		return
	}
	sort.Strings(servers)
	var items []pickerItem
	for _, server := range servers {
		count := len(m.serverTools(server))
		items = append(items, pickerItem{id: server, title: server, desc: fmt.Sprintf("%d tools", count)})
	}
	m.picker = newPicker("MCP servers", items, func(m *Model, item pickerItem) tea.Cmd {
		defs := m.serverTools(item.id)
		if len(defs) == 0 {
			m.addPanel("MCP · "+item.id, "This server exposes no tools.")
			return nil
		}
		prefix := "MCP server " + item.id + " tool "
		var lines []string
		for _, def := range defs {
			lines = append(lines, "- "+def.Name+" — "+strings.TrimPrefix(def.Description, prefix))
		}
		for _, status := range m.runtime.MCP.Statuses() {
			if status.Name != item.id {
				continue
			}
			var extras []string
			for _, capability := range status.Capabilities {
				switch capability {
				case "prompts":
					extras = append(extras, "/mcp prompts "+item.id)
				case "resources":
					extras = append(extras, "/mcp resources "+item.id)
				}
			}
			if len(extras) > 0 {
				lines = append(lines, "\nThis server also offers: "+strings.Join(extras, " · "))
			}
		}
		m.addPanel("MCP · "+item.id, strings.Join(lines, "\n"))
		return nil
	})
	m.layout()
	m.refresh()
}

// serverTools returns the registered tool definitions contributed by one MCP
// server, identified by the description prefix stamped at registration.
func (m *Model) serverTools(server string) []provider.ToolDefinition {
	prefix := "MCP server " + server + " tool "
	var out []provider.ToolDefinition
	for _, def := range m.runtime.Registry.Definitions(func(tools.Tool) bool { return true }) {
		if strings.HasPrefix(def.Description, prefix) {
			out = append(out, def)
		}
	}
	return out
}

const filePickerCap = 4000

// workspaceFiles lists workspace-relative file paths for the @ mention
// picker, skipping VCS and dependency directories.
func (m *Model) workspaceFiles() []pickerItem {
	var items []pickerItem
	root := m.runtime.Workspace
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == "node_modules" || name == "vendor" || name == ".cache" || name == "__pycache__" || name == ".venv" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		items = append(items, pickerItem{id: rel, title: rel})
		if len(items) >= filePickerCap {
			return fmt.Errorf("capped")
		}
		return nil
	})
	return items
}

// openFilePicker powers @ mentions: the chosen path replaces the trailing
// "@token" in the prompt. Cancelling leaves the typed text untouched.
func (m *Model) openFilePicker() {
	items := m.workspaceFiles()
	if len(items) == 0 {
		return
	}
	m.picker = newPicker("Mention file", items, func(m *Model, item pickerItem) tea.Cmd {
		value := m.input.Value()
		if at := strings.LastIndex(value, "@"); at >= 0 {
			value = value[:at]
		}
		m.input.SetValue(value + item.id + " ")
		m.input.CursorEnd()
		m.input.Focus()
		return nil
	})
	m.layout()
	m.refresh()
}

// rebuildTranscript replaces the chat view with the switched-to session's
// conversation so the screen matches the model's context.
func (m *Model) rebuildTranscript() {
	m.blocks = nil
	for _, message := range m.runtime.Session.Active() {
		switch message.Role {
		case "user":
			if strings.HasPrefix(message.Content, "[Context summary") {
				m.blocks = append(m.blocks, block{role: "system", content: "· older context compacted into a summary ·"})
				continue
			}
			m.blocks = append(m.blocks, block{role: "user", content: message.Content})
		case "assistant":
			if message.Content != "" {
				m.blocks = append(m.blocks, block{role: "assistant", content: message.Content})
			}
			for _, call := range message.ToolCalls {
				m.blocks = append(m.blocks, block{role: "tool", content: call.Name + "\x00(from saved session)"})
			}
		}
	}
	m.refresh()
}
