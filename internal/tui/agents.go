package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robert-mcdermott/collomia/internal/agent"
	"github.com/robert-mcdermott/collomia/internal/app"
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
		if current, ok := m.runtime.Team.Get(item.id); ok && current.Write {
			_, _ = m.runtime.PrepareDelegateVerification(context.Background(), item.id)
		}
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
	if len(status.WriteScopes) > 0 {
		lines = append(lines, "Write scope: "+strings.Join(status.WriteScopes, ", "))
	}
	if len(status.ScopeViolations) > 0 {
		lines = append(lines, "", "Write-scope violations:")
		for _, path := range status.ScopeViolations {
			lines = append(lines, "- "+path)
		}
		lines = append(lines, "Guarded integration is blocked; inspect the retained worktree manually.")
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
	if status.Usage.CostAvailable {
		if status.CostBudgetUSD > 0 {
			lines = append(lines, fmt.Sprintf("Cost:     $%.6f / $%.6f estimated", status.Usage.CostUSD, status.CostBudgetUSD))
		} else {
			lines = append(lines, fmt.Sprintf("Cost:     $%.6f estimated", status.Usage.CostUSD))
		}
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
	if status.IntegrationStatus != "" {
		lines = append(lines, "", "Integration: "+delegateStatusLabel(status.IntegrationStatus))
		if status.IntegrationError != "" {
			lines = append(lines, "Reason: "+m.runtime.Redactor.Redact(status.IntegrationError))
		}
	}
	if status.VerificationStatus != "" {
		lines = append(lines, "", "Child verification: "+delegateStatusLabel(status.VerificationStatus))
		if status.VerificationError != "" {
			lines = append(lines, "Verification note: "+m.runtime.Redactor.Redact(status.VerificationError))
		}
		for _, result := range status.VerificationResults {
			line := "- " + result.Command + ": " + delegateStatusLabel(result.Status)
			if result.Purpose != "" {
				line += " (" + result.Purpose + ")"
			}
			lines = append(lines, line)
			if result.Error != "" {
				lines = append(lines, "  "+m.runtime.Redactor.Redact(result.Error))
			}
		}
		lines = append(lines, "Scope: retained child worktree only; verify the combined parent workspace after integration.")
	}
	if status.Status == agent.DelegateQueued || status.Status == agent.DelegateRunning || status.Status == agent.DelegateWaitingApproval || status.Status == agent.DelegateCancelling {
		lines = append(lines, "", "Control:", "/agents steer "+status.ID+" <guidance…>", "/agents stop "+status.ID)
	} else if status.Write && len(status.Changed) > 0 && status.Worktree != "" && len(status.ScopeViolations) == 0 {
		lines = append(lines, "", "Review, verify, and selectively integrate:", "/agents verify "+status.ID, "/agents apply "+status.ID)
		if m.runtime.Config.Options.AgentIntegration == "reviewed" {
			lines = append(lines, "Primary-agent reviewed integration is enabled.")
		}
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderDelegateComparison(candidates []app.DelegateCandidateSummary) string {
	var lines []string
	for _, candidate := range candidates {
		line := fmt.Sprintf("%s (%s) · %s · %d file(s), %d hunk(s), %d conflict(s)",
			m.runtime.Redactor.Redact(candidate.Name), candidate.ID, candidate.Readiness,
			candidate.SelectableFiles, candidate.SelectableHunks, candidate.Conflicts)
		if candidate.VerificationStatus != "" {
			line += " · verification " + delegateStatusLabel(candidate.VerificationStatus)
		} else {
			line += " · unverified"
		}
		if candidate.InputTokens+candidate.OutputTokens > 0 {
			line += fmt.Sprintf(" · %d tokens", candidate.InputTokens+candidate.OutputTokens)
		}
		lines = append(lines, line)
		if candidate.Summary != "" {
			lines = append(lines, "  "+truncateRunes(strings.Join(strings.Fields(m.runtime.Redactor.Redact(candidate.Summary)), " "), 160))
		}
		if candidate.VerificationError != "" {
			lines = append(lines, "  verification: "+m.runtime.Redactor.Redact(candidate.VerificationError))
		}
	}
	lines = append(lines, "", "Comparison is read-only. Inspect the exact hunks before applying. A passing child-worktree suite grants no permission and does not prove the combined parent workspace.")
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
