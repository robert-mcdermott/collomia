package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robert-mcdermott/collomia/internal/agent"
)

func agentDuration(status agent.DelegateStatus, now time.Time) time.Duration {
	end := status.Finished
	if end.IsZero() {
		end = now
	}
	if status.Started.IsZero() || end.Before(status.Started) {
		return 0
	}
	return end.Sub(status.Started).Round(time.Second)
}

func (m *Model) openAgentPicker() {
	agents := m.runtime.Team.Snapshot()
	if len(agents) == 0 {
		m.addPanel("Delegated agents", "No delegated agents have run in this session. Collomia creates them when the model uses the delegate tool for independent investigations or isolated write tasks.")
		return
	}
	now := time.Now()
	items := make([]pickerItem, 0, len(agents))
	for _, status := range agents {
		kind := "read-only"
		if status.Write {
			kind = "isolated write"
		}
		desc := fmt.Sprintf("%s · %s · %s", delegateStatusLabel(status.Status), kind, agentDuration(status, now))
		if status.Summary != "" {
			summary := strings.Join(strings.Fields(m.runtime.Redactor.Redact(status.Summary)), " ")
			if len([]rune(summary)) > 80 {
				runes := []rune(summary)
				summary = string(runes[:79]) + "…"
			}
			desc += " · " + summary
		}
		items = append(items, pickerItem{id: status.ID, title: status.Name, desc: desc})
	}
	m.picker = newPicker("Delegated agents", items, func(m *Model, item pickerItem) tea.Cmd {
		for _, status := range m.runtime.Team.Snapshot() {
			if status.ID == item.id {
				m.addPanel("Agent · "+status.Name, m.renderAgentDetails(status))
				break
			}
		}
		return nil
	})
	m.layout()
	m.refresh()
}

// openAgentControlPicker remains available while the parent turn is busy. It
// opens inspection rather than making Enter destructive; stop and steering
// remain explicit slash commands whose IDs are shown in the details panel.
func (m *Model) openAgentControlPicker() {
	var items []pickerItem
	for _, status := range m.runtime.Team.Snapshot() {
		if status.Status == agent.DelegateQueued || status.Status == agent.DelegateRunning || status.Status == agent.DelegateWaitingApproval {
			desc := delegateStatusLabel(status.Status) + " · enter to inspect"
			if status.CurrentAction != "" {
				desc += " · " + m.runtime.Redactor.Redact(status.CurrentAction)
			}
			items = append(items, pickerItem{id: status.ID, title: m.runtime.Redactor.Redact(status.Name), desc: desc})
		}
	}
	if len(items) == 0 {
		m.addPanel("Delegated agents", "No queued or running delegated agents can be controlled.")
		return
	}
	m.picker = newPicker("Active delegated agents", items, func(m *Model, item pickerItem) tea.Cmd {
		if status, ok := m.runtime.Team.Get(item.id); ok {
			m.openAgentActionPicker(status)
		}
		return nil
	})
	m.layout()
	m.refresh()
}

func (m *Model) openAgentActionPicker(status agent.DelegateStatus) {
	items := []pickerItem{
		{id: "inspect", title: "Inspect", desc: "show task, output, evidence, budget, and controls"},
		{id: "steer", title: "Steer", desc: "prepare bounded guidance for the next model boundary"},
		{id: "stop", title: "Stop", desc: "cancel only this delegated agent"},
	}
	m.picker = newPicker("Agent · "+status.Name, items, func(m *Model, item pickerItem) tea.Cmd {
		switch item.id {
		case "inspect":
			if current, ok := m.runtime.Team.Get(status.ID); ok {
				m.addPanel("Agent · "+current.Name, m.renderAgentDetails(current))
			}
		case "steer":
			m.setComposerValue("/agents steer " + status.ID + " ")
			m.input.Focus()
			m.addSystem("Type guidance after the agent ID and press enter. It will be delivered only at the next model boundary and grants no permissions.")
		case "stop":
			if err := m.runtime.Team.Stop(status.ID); err != nil {
				m.addError(err)
			} else {
				m.addSystem("Cancellation requested for delegated agent " + status.Name + " (" + status.ID + ").")
			}
		}
		return nil
	})
}

func (m *Model) renderAgentDetails(status agent.DelegateStatus) string {
	kind := "read-only investigation in the shared workspace"
	if status.Write {
		kind = "write task in an isolated Git worktree"
	}
	lines := []string{
		"ID:       " + status.ID,
		"Status:   " + delegateStatusLabel(status.Status),
		"Mode:     " + kind,
		"Duration: " + agentDuration(status, time.Now()).String(),
		"Task:     " + m.runtime.Redactor.Redact(status.Task),
	}
	if status.Profile != "" {
		lines = append(lines, "Profile:  "+m.runtime.Redactor.Redact(status.Profile))
	}
	if status.PlanStep > 0 {
		lines = append(lines, fmt.Sprintf("Plan step: %d", status.PlanStep))
	}
	if status.Provider != "" || status.Model != "" {
		lines = append(lines, "Provider: "+m.runtime.Redactor.Redact(status.Provider+"/"+status.Model))
	}
	if status.CurrentAction != "" {
		lines = append(lines, "Action:   "+m.runtime.Redactor.Redact(status.CurrentAction))
	}
	usage := status.Usage.InputTokens + status.Usage.OutputTokens
	if status.TokenBudget > 0 {
		lines = append(lines, fmt.Sprintf("Tokens:   %d / %d", usage, status.TokenBudget))
	} else if usage > 0 {
		lines = append(lines, fmt.Sprintf("Tokens:   %d (%d input / %d output)", usage, status.Usage.InputTokens, status.Usage.OutputTokens))
	}
	if status.TimeoutSeconds > 0 {
		lines = append(lines, fmt.Sprintf("Timeout:  %ds (includes queue time)", status.TimeoutSeconds))
	}
	if status.Summary != "" {
		lines = append(lines, "", "Outcome:", m.runtime.Redactor.Redact(status.Summary))
	}
	if status.Error != "" {
		lines = append(lines, "", "Error:", m.runtime.Redactor.Redact(status.Error))
		if status.FailureID != "" {
			lines = append(lines, "Failure ID: "+status.FailureID)
		}
	}
	if len(status.Guidance) > 0 {
		lines = append(lines, "", fmt.Sprintf("Steering (%d, %d pending):", len(status.Guidance), status.PendingGuidance))
		for _, guidance := range status.Guidance {
			lines = append(lines, "- "+m.runtime.Redactor.Redact(guidance))
		}
	}
	if strings.TrimSpace(status.RecentOutput) != "" {
		lines = append(lines, "", "Recent output:", m.runtime.Redactor.Redact(status.RecentOutput))
	}
	if len(status.Evidence) > 0 {
		lines = append(lines, "", fmt.Sprintf("Evidence (%d):", len(status.Evidence)))
		for _, item := range status.Evidence {
			lines = append(lines, "- "+m.runtime.Redactor.Redact(item))
		}
	}
	if len(status.Changed) > 0 {
		lines = append(lines, "", fmt.Sprintf("Changed files (%d):", len(status.Changed)))
		for _, path := range status.Changed {
			display := path
			if rel, err := filepath.Rel(m.runtime.Workspace, path); err == nil && !strings.HasPrefix(rel, "..") {
				display = filepath.ToSlash(rel)
			}
			lines = append(lines, "- "+display)
		}
	}
	if status.Worktree != "" {
		lines = append(lines, "", "Worktree: "+status.Worktree)
	}
	if status.Branch != "" {
		lines = append(lines, "Branch:   "+status.Branch)
	}
	if status.BaseCommit != "" {
		lines = append(lines, "Base:     "+status.BaseCommit)
	}
	if len(status.Integrated) > 0 {
		lines = append(lines, "", fmt.Sprintf("Integrated files (%d):", len(status.Integrated)))
		for _, path := range status.Integrated {
			lines = append(lines, "- "+path)
		}
	}
	if status.Status == agent.DelegateQueued || status.Status == agent.DelegateRunning || status.Status == agent.DelegateWaitingApproval || status.Status == agent.DelegateCancelling {
		lines = append(lines, "", "Control:", "/agents steer "+status.ID+" <guidance…>", "/agents stop "+status.ID)
	} else if status.Write && len(status.Changed) > 0 && status.Worktree != "" {
		lines = append(lines, "", "Review and selectively integrate:", "/agents apply "+status.ID)
	}
	return strings.Join(lines, "\n")
}

func delegateStatusLabel(status string) string {
	return strings.ReplaceAll(status, "_", " ")
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:max(0, limit-1)]) + "…"
}
