package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	// emptyStateCardWidth is the widest the orientation card grows. Past this
	// the label and value columns drift so far apart they stop reading as
	// pairs.
	emptyStateCardWidth = 62
	// emptyStateMinWidth is the narrowest terminal that gets the full card.
	// Below it the banner and a one-line hint are all that fit honestly.
	emptyStateMinWidth = 56
)

// renderEmptyState fills the first screen of a session with the answers to
// "where am I and what will this cost me" — workspace, branch, model,
// autonomy, containment — plus a few openers. The previous first screen was a
// logo and two lines of hint over twenty-five blank rows, which told a new
// user nothing and a returning user less.
func (m *Model) renderEmptyState() string {
	if m.width < emptyStateMinWidth {
		return m.banner()
	}
	width := min(emptyStateCardWidth, max(20, m.bodyWidth()-8))
	var b strings.Builder
	b.WriteString(m.centerBlock(m.bannerArt(), m.bodyWidth()) + "\n\n")
	b.WriteString(m.centerBlock(m.emptyStateCard(width), m.bodyWidth()) + "\n\n")
	b.WriteString(m.centerBlock(m.emptyStateSuggestions(width), m.bodyWidth()))

	// Centre what is left of the viewport too, so the card sits in the middle
	// of the screen rather than pinned under the tab bar with a void below.
	content := b.String()
	if pad := (m.viewport.Height - strings.Count(content, "\n") - 1) / 2; pad > 0 {
		content = strings.Repeat("\n", pad) + content
	}
	return content
}

func (m *Model) emptyStateCard(width int) string {
	providerName, model := m.runtime.Agent.Selection()
	inner := max(10, width-4)

	rows := [][2]string{
		{"workspace", filepath.Base(m.runtime.Workspace)},
		{"model", providerName + "/" + model},
		{"autonomy", m.runtime.Permissions.Mode() + " · " + m.securityStance().Label},
	}
	if status := m.workspaceStatus; status.InRepository && status.Branch != "" {
		dirty := status.Staged + status.Modified + status.Untracked + status.Conflicted
		branch := "⎇ " + status.Branch
		if dirty > 0 {
			branch += fmt.Sprintf(" · %d uncommitted", dirty)
		}
		rows = append(rows[:1], append([][2]string{{"branch", branch}}, rows[1:]...)...)
	}
	if m.projectConfigurationQuarantined() {
		rows = append(rows, [2]string{"trust", "project configuration quarantined"})
	}

	var body strings.Builder
	for i, row := range rows {
		if i > 0 {
			body.WriteByte('\n')
		}
		label := m.styles.muted.Render(padTo(row[0], 11))
		body.WriteString(label + m.styles.accent.Render(ansi.Truncate(row[1], max(1, inner-11), "…")))
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.theme.Border)).
		Padding(0, 1).
		Width(width - 2).
		Render(body.String())
}

func (m *Model) emptyStateSuggestions(width int) string {
	suggestions := [][2]string{}
	if status := m.workspaceStatus; status.Staged+status.Modified+status.Untracked > 0 {
		suggestions = append(suggestions, [2]string{"/review -", "review what is already changed here"})
	}
	suggestions = append(suggestions,
		[2]string{"/verify", "detect and run the build, lint, and tests"},
		[2]string{"/status", "workspace, provider, model, and autonomy"},
	)
	if len(m.promptHistory) > 0 {
		suggestions = append(suggestions, [2]string{"↑", "recall your last prompt"})
	}

	var b strings.Builder
	b.WriteString(m.styles.heading.Render("Try"))
	nameWidth := 0
	for _, s := range suggestions {
		nameWidth = max(nameWidth, ansi.StringWidth(s[0]))
	}
	for _, s := range suggestions {
		desc := ansi.Truncate(s[1], max(1, width-nameWidth-4), "…")
		b.WriteString("\n" + m.styles.muted.Render("▸ ") +
			m.styles.accent.Render(padTo(s[0], nameWidth)) + "  " +
			m.styles.muted.Render(desc))
	}
	b.WriteString("\n\n" + m.styles.system.Render(ansi.Truncate(
		fmt.Sprintf("/ commands · %s tabs · alt+enter newline", m.binding("next_tab")),
		width, "…")))
	return b.String()
}

// centerBlock indents a multi-line block so the whole block is centred as a
// unit. Centring each line on its own would ragged the card's border.
func (m *Model) centerBlock(block string, width int) string {
	lines := strings.Split(block, "\n")
	blockWidth := 0
	for _, line := range lines {
		blockWidth = max(blockWidth, ansi.StringWidth(line))
	}
	pad := max(0, (width-blockWidth)/2)
	if pad == 0 {
		return block
	}
	prefix := strings.Repeat(" ", pad)
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}
