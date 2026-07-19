package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// panelMaxWidth caps informational panels so they stay readable on very wide
// terminals instead of stretching a few words across 300 columns.
const panelMaxWidth = 100

// addPanel appends a titled card to the transcript. Use it for informational
// slash-command output (/status, /context, /ps, …); quick acknowledgements
// ("Theme switched…") stay as plain system lines via addSystem.
func (m *Model) addPanel(title, content string) {
	m.blocks = append(m.blocks, block{role: "panel", title: title, content: content})
}

// renderPanel draws a rounded, theme-colored box with the title spliced into
// the top border and the content word-wrapped inside:
//
//	╭─ Background processes ──────────╮
//	│ [1] npm run dev — running        │
//	╰──────────────────────────────────╯
func (m *Model) renderPanel(title, content string) string {
	width := m.width - 4
	if width > panelMaxWidth {
		width = panelMaxWidth
	}
	if width < 20 {
		width = 20
	}
	// inner is the content width between the side borders (includes padding).
	inner := width - 2
	body := m.styles.panelBody.
		Border(lipgloss.RoundedBorder(), false, true, true, true).
		BorderForeground(lipgloss.Color(m.theme.Border)).
		Padding(0, 1).
		Width(inner).
		Render(strings.TrimRight(content, "\n"))
	label := " " + title + " "
	if w := lipgloss.Width(label); w > inner-4 {
		runes := []rune(title)
		keep := inner - 6
		if keep < 1 {
			keep = 1
		}
		if keep < len(runes) {
			label = " " + string(runes[:keep]) + "… "
		}
	}
	fill := inner - lipgloss.Width(label) - 2
	if fill < 0 {
		fill = 0
	}
	rule := m.styles.rule.Render
	top := rule("╭──") + m.styles.panelTitle.Render(label) + rule(strings.Repeat("─", fill)+"╮")
	return top + "\n" + body
}
