package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
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
		m.addSystem(fmt.Sprintf("Switched to %s/%s. Use /model %s/<id> for a different model on this provider.", providerName, model, providerName))
		return nil
	})
	m.layout()
	m.refresh()
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
