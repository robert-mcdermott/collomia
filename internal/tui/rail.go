package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/robert-mcdermott/collomia/internal/agent"
)

const (
	// railColumns includes the rail's own left border and padding.
	railColumns = 34
	// railMinTotalWidth is the narrowest terminal that can spare the rail
	// without squeezing the transcript below a readable measure.
	railMinTotalWidth = 116
	// railAutoWidth is where the rail appears on its own. Below it the rail
	// is available but opt-in, so a default-size terminal is never surprised.
	railAutoWidth = 146
)

// railVisible reports whether the context rail should be drawn. A terminal
// that cannot afford the columns never shows it, whatever the preference.
func (m Model) railVisible() bool {
	if m.width < railMinTotalWidth || m.tab != tabChat {
		return false
	}
	if m.railManual {
		return m.railOn
	}
	return m.width >= railAutoWidth
}

func (m Model) railWidth() int {
	if !m.railVisible() {
		return 0
	}
	return railColumns
}

// bodyWidth is the width available to the transcript once the rail has taken
// its columns.
func (m Model) bodyWidth() int {
	return max(20, m.width-m.railWidth())
}

// toggleRail flips the rail and remembers that the choice was deliberate, so
// resizing the terminal no longer overrides it.
func (m *Model) toggleRail() {
	m.railOn = !m.railVisible()
	m.railManual = true
}

// renderRail draws the persistent context panel: what the workspace looks
// like, what the plan is, and what is still outstanding. Everything in it is
// already tracked by the runtime; the rail exists so the user does not have
// to leave the transcript to see it.
func (m Model) renderRail(height int) string {
	inner := railColumns - 3 // left border plus one column of padding each side
	var sections []string

	if branch := m.railWorkspace(inner); branch != "" {
		sections = append(sections, branch)
	}
	if plan := m.railPlan(inner); plan != "" {
		sections = append(sections, plan)
	}
	if agents := m.railAgents(inner); agents != "" {
		sections = append(sections, agents)
	}
	if changes := m.railChanges(inner); changes != "" {
		sections = append(sections, changes)
	}
	if procs := m.railProcesses(inner); procs != "" {
		sections = append(sections, procs)
	}
	// The workspace block is always present, so an otherwise idle rail is a
	// name and a branch over twenty blank rows, which reads as a panel that
	// failed to load rather than one with nothing to report yet.
	if len(sections) <= 1 {
		sections = append(sections, m.styles.muted.Render(wrapAndLimit(
			"Plan steps, delegated agents, changed files, and background processes appear here as the session progresses.", inner, 5)))
	}

	body := strings.Join(sections, "\n\n")
	lines := strings.Split(body, "\n")
	// Trim rather than let the panel push the composer off screen; the
	// Session tab remains the complete record.
	if len(lines) > height {
		lines = lines[:max(0, height-1)]
		lines = append(lines, m.styles.muted.Render("… Session tab for the rest"))
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = " " + fitLine(lines[i], inner+1)
	}

	border := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Border))
	edge := border.Render("│")
	for i := range lines {
		lines[i] = edge + lines[i]
	}
	return strings.Join(lines, "\n")
}

func (m Model) railHeading(text string) string {
	return m.styles.heading.Render(text)
}

func (m Model) railWorkspace(inner int) string {
	var b strings.Builder
	b.WriteString(m.railHeading("Workspace"))
	name := filepath.Base(m.runtime.Workspace)
	b.WriteString("\n" + m.styles.accent.Render(ansi.Truncate(name, inner, "…")))
	status := m.workspaceStatus
	if status.Branch != "" {
		b.WriteString("\n" + m.styles.muted.Render(ansi.Truncate("⎇ "+status.Branch, inner, "…")))
	}
	counts := []string{}
	if status.Staged > 0 {
		counts = append(counts, m.styles.success.Render(fmt.Sprintf("%d staged", status.Staged)))
	}
	if status.Modified > 0 {
		counts = append(counts, m.styles.warning.Render(fmt.Sprintf("%d modified", status.Modified)))
	}
	if status.Untracked > 0 {
		counts = append(counts, m.styles.muted.Render(fmt.Sprintf("%d untracked", status.Untracked)))
	}
	if status.Conflicted > 0 {
		counts = append(counts, m.styles.errText.Render(fmt.Sprintf("%d conflicts", status.Conflicted)))
	}
	if len(counts) > 0 {
		b.WriteString("\n" + strings.Join(counts, m.styles.muted.Render(" · ")))
	}
	return b.String()
}

func (m Model) railPlan(inner int) string {
	if graph := m.runtime.GoalGraph; graph != nil {
		snapshot := graph.Snapshot()
		done := 0
		for _, node := range snapshot.Nodes {
			if node.State == "done" {
				done++
			}
		}
		var b strings.Builder
		b.WriteString(m.railHeading("Goal · EXP") + m.styles.muted.Render(fmt.Sprintf("  %d/%d", done, len(snapshot.Nodes))))
		for _, node := range snapshot.Nodes {
			glyph, style := m.graphNodeStyle(string(node.State))
			b.WriteString("\n" + style.Render(glyph) + " " + m.styles.panelBody.Render(ansi.Truncate(node.Title, max(1, inner-2), "…")))
		}
		return b.String()
	}
	current := m.runtime.Plan.Current()
	if current == nil || len(current.Steps) == 0 {
		return ""
	}
	done := 0
	for _, step := range current.Steps {
		if step.Status == "done" || step.Status == "skipped" {
			done++
		}
	}
	var b strings.Builder
	heading := "Plan"
	if m.runtime.OrchestratedGoalPhase() == "proposal" {
		heading = "Goal proposal · EXP"
	}
	b.WriteString(m.railHeading(heading) + m.styles.muted.Render(fmt.Sprintf("  %d/%d", done, len(current.Steps))))
	for _, step := range current.Steps {
		glyph, style := m.planStepStyle(step.Status)
		b.WriteString("\n" + style.Render(glyph) + " " + m.styles.panelBody.Render(ansi.Truncate(step.Title, max(1, inner-2), "…")))
	}
	return b.String()
}

func (m Model) graphNodeStyle(status string) (string, lipgloss.Style) {
	switch status {
	case "done":
		return "●", m.styles.success
	case "running", "ready", "retryable":
		return "◐", m.styles.warning
	case "blocked", "cancelled", "budget_exhausted":
		return "✗", m.styles.errText
	case "awaiting_review":
		// A retained verified candidate succeeded; it waits on the user rather
		// than on the runtime, so it must not read as a failure.
		return "◆", m.styles.accent
	case "stale":
		return "!", m.styles.warning
	}
	return "○", m.styles.muted
}

func (m Model) planStepStyle(status string) (string, lipgloss.Style) {
	switch status {
	case "done":
		return "●", m.styles.success
	case "in_progress":
		return "◐", m.styles.warning
	case "blocked":
		return "✗", m.styles.errText
	case "skipped":
		return "−", m.styles.muted
	}
	return "○", m.styles.muted
}

func (m Model) railAgents(inner int) string {
	agents := m.runtime.Team.Snapshot()
	if len(agents) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.railHeading("Agents") + m.styles.muted.Render(fmt.Sprintf("  %d", len(agents))))
	for _, a := range agents {
		glyph, style := m.delegateGlyph(a.Status)
		name := m.runtime.Redactor.Redact(a.Name)
		b.WriteString("\n" + style.Render(glyph) + " " + m.styles.panelBody.Render(ansi.Truncate(name, max(1, inner-2), "…")))
	}
	return b.String()
}

func (m Model) delegateGlyph(status string) (string, lipgloss.Style) {
	switch status {
	case agent.DelegateDone:
		return "●", m.styles.success
	case agent.DelegateRunning, agent.DelegateWaitingApproval, agent.DelegateCancelling:
		return "◐", m.styles.warning
	case agent.DelegateError, agent.DelegateTimedOut, agent.DelegateBudgetExhausted, agent.DelegateInterrupted:
		return "✗", m.styles.errText
	}
	return "○", m.styles.muted
}

func (m Model) railChanges(inner int) string {
	changed := m.runtime.Changes.Changed()
	if len(changed) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.railHeading("Changed") + m.styles.muted.Render(fmt.Sprintf("  %d", len(changed))))
	shown := changed
	hidden := 0
	if len(shown) > 6 {
		hidden = len(shown) - 6
		shown = shown[:6]
	}
	for _, path := range shown {
		display := path
		if rel, err := filepath.Rel(m.runtime.Workspace, path); err == nil && !strings.HasPrefix(rel, "..") {
			display = rel
		}
		b.WriteString("\n" + m.styles.accent.Render(truncateLeft(display, inner)))
	}
	if hidden > 0 {
		b.WriteString("\n" + m.styles.muted.Render(fmt.Sprintf("+%d more · %s diff", hidden, m.binding("diff_view"))))
	}
	return b.String()
}

func (m Model) railProcesses(inner int) string {
	procs := m.runtime.Processes.Snapshot()
	if len(procs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.railHeading("Processes"))
	for _, p := range procs {
		glyph, style := "○", m.styles.muted
		if p.Running {
			glyph, style = "●", m.styles.success
		}
		b.WriteString("\n" + style.Render(glyph) + " " + m.styles.panelBody.Render(ansi.Truncate(p.Command, max(1, inner-2), "…")))
	}
	return b.String()
}

// truncateLeft keeps the tail of a path, which is the part that identifies
// the file. Cutting the other end leaves a column of identical directories.
func truncateLeft(value string, width int) string {
	if width < 2 || ansi.StringWidth(value) <= width {
		return value
	}
	runes := []rune(value)
	for len(runes) > 0 && ansi.StringWidth("…"+string(runes)) > width {
		runes = runes[1:]
	}
	return "…" + string(runes)
}
