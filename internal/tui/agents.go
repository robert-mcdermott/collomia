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
		desc := fmt.Sprintf("%s · %s · %s", status.Status, kind, agentDuration(status, now))
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

func (m *Model) renderAgentDetails(status agent.DelegateStatus) string {
	kind := "read-only investigation in the shared workspace"
	if status.Write {
		kind = "write task in an isolated Git worktree"
	}
	lines := []string{
		"Status:   " + status.Status,
		"Mode:     " + kind,
		"Duration: " + agentDuration(status, time.Now()).String(),
		"Task:     " + m.runtime.Redactor.Redact(status.Task),
	}
	if status.Summary != "" {
		lines = append(lines, "", "Outcome:", m.runtime.Redactor.Redact(status.Summary))
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
	if status.Status == "running" {
		lines = append(lines, "", "The picker is a snapshot. Reopen /agents to refresh live status.")
	}
	return strings.Join(lines, "\n")
}
