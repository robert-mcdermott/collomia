package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	mcpclient "github.com/robert-mcdermott/collomia/internal/mcp"
	workspacestate "github.com/robert-mcdermott/collomia/internal/workspace"
)

type workspaceStatusMsg struct {
	generation int
	status     workspacestate.GitStatus
}

func inspectWorkspaceCmd(root string, generation int) tea.Cmd {
	return func() tea.Msg {
		return workspaceStatusMsg{generation: generation, status: workspacestate.InspectGit(context.Background(), root)}
	}
}

func (m *Model) refreshWorkspaceStatus() tea.Cmd {
	m.workspaceGeneration++
	m.workspaceLoading = true
	return inspectWorkspaceCmd(m.runtime.Workspace, m.workspaceGeneration)
}

func (m Model) gitStatusText() string {
	if m.workspaceLoading && m.workspaceRefreshed.IsZero() {
		return "checking…"
	}
	status := m.workspaceStatus
	if status.Error != "" {
		return "unavailable · " + status.Error
	}
	if !status.InRepository {
		return "not a Git repository"
	}
	identity := status.Branch
	if status.Upstream != "" {
		identity += " → " + status.Upstream
	}
	if status.Ahead > 0 || status.Behind > 0 {
		identity += fmt.Sprintf(" · ahead %d / behind %d", status.Ahead, status.Behind)
	}
	dirty := status.Staged + status.Modified + status.Untracked + status.Conflicted
	if dirty == 0 {
		return identity + " · clean"
	}
	return fmt.Sprintf("%s · staged %d / modified %d / untracked %d / conflicts %d", identity, status.Staged, status.Modified, status.Untracked, status.Conflicted)
}

func (m Model) projectTrustText() string {
	for _, layer := range m.runtime.Config.Layers {
		if layer.Name != "project" {
			continue
		}
		if layer.Applied {
			return "trusted project configuration"
		}
		return "project configuration quarantined"
	}
	return "no project configuration"
}

func (m Model) projectConfigurationQuarantined() bool {
	for _, layer := range m.runtime.Config.Layers {
		if layer.Name == "project" {
			return !layer.Applied
		}
	}
	return false
}

func (m Model) mcpHealthText() string {
	statuses := m.runtime.MCP.Statuses()
	if len(statuses) == 0 {
		return "none configured"
	}
	counts := map[string]int{}
	for _, status := range statuses {
		counts[status.Status]++
	}
	var parts []string
	for _, status := range []string{mcpclient.StatusConnected, mcpclient.StatusError, mcpclient.StatusDisabled, mcpclient.StatusUntrusted} {
		if counts[status] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[status], status))
		}
	}
	if len(parts) == 0 {
		return "status unknown"
	}
	return strings.Join(parts, " / ")
}

func (m Model) mcpNeedsAttention() bool {
	for _, status := range m.runtime.MCP.Statuses() {
		if status.Status == mcpclient.StatusError || status.Status == mcpclient.StatusUntrusted {
			return true
		}
	}
	return false
}
