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

// openAgentControlPicker remains available while the parent turn is busy. A
// deliberate selection cancels only that child; ctrl+c/esc dismiss the picker
// and do not cancel the parent turn.
func (m *Model) openAgentControlPicker() {
	var items []pickerItem
	for _, status := range m.runtime.Team.Snapshot() {
		if status.Status == agent.DelegateQueued || status.Status == agent.DelegateRunning || status.Status == agent.DelegateWaitingApproval {
			desc := delegateStatusLabel(status.Status)
			if status.CurrentAction != "" {
				desc += " · " + m.runtime.Redactor.Redact(status.CurrentAction)
			}
			items = append(items, pickerItem{id: status.ID, title: m.runtime.Redactor.Redact(status.Name), desc: desc})
		}
	}
	if len(items) == 0 {
		m.addPanel("Delegated agents", "No queued or running delegated agents can be stopped.")
		return
	}
	m.picker = newPicker("Stop delegated agent", items, func(m *Model, item pickerItem) tea.Cmd {
		if err := m.runtime.Team.Stop(item.id); err != nil {
			m.addError(err)
		} else {
			m.addSystem("Cancellation requested for delegated agent " + item.title + " (" + item.id + ").")
		}
		return nil
	})
	m.layout()
	m.refresh()
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
	if status.Status == agent.DelegateQueued || status.Status == agent.DelegateRunning || status.Status == agent.DelegateWaitingApproval || status.Status == agent.DelegateCancelling {
		lines = append(lines, "", "The picker is a snapshot. Reopen /agents to refresh, or use "+m.binding("agent_control")+" to stop an active child.")
	}
	return strings.Join(lines, "\n")
}

func delegateStatusLabel(status string) string {
	return strings.ReplaceAll(status, "_", " ")
}
