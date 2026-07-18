package tui

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/robert-mcdermott/collomia/internal/diffmodel"
	"github.com/robert-mcdermott/collomia/internal/permission"
)

// hunkReviewState lets the user accept or reject individual hunks of a
// pending write_file change instead of the whole file at once. It only
// replaces the tool's proposed content (via permission.Decision.Content);
// the write itself still goes through the normal approval/execution path.
type hunkReviewState struct {
	path   string
	before string
	hunks  []diffmodel.Hunk
	keep   []bool
	cursor int
}

// tryEnterHunkReview parses the pending approval's diff preview into hunks
// and switches the overlay into hunk-selection mode. It returns false (and
// leaves the overlay untouched) when the pending action isn't a
// hunk-reviewable write_file change.
func (m Model) tryEnterHunkReview() Model {
	req := m.pending.request
	if req.Tool != "write_file" || len(req.Action.Paths) == 0 || req.Action.Preview == "" {
		return m
	}
	hunks, err := diffmodel.ParseHunks(req.Action.Preview)
	if err != nil || len(hunks) < 2 {
		// A single hunk covers the whole change already; "approve"/"deny"
		// says the same thing hunk review would.
		return m
	}
	before := ""
	if data, readErr := os.ReadFile(req.Action.Paths[0]); readErr == nil {
		before = string(data)
	}
	keep := make([]bool, len(hunks))
	for i := range keep {
		keep[i] = true
	}
	m.hunkReview = &hunkReviewState{path: req.Action.Paths[0], before: before, hunks: hunks, keep: keep}
	return m
}

func (m Model) handleHunkReviewKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	hr := m.hunkReview
	switch key.String() {
	case "up", "k":
		if hr.cursor > 0 {
			hr.cursor--
		}
	case "down", "j":
		if hr.cursor < len(hr.hunks)-1 {
			hr.cursor++
		}
	case " ":
		hr.keep[hr.cursor] = !hr.keep[hr.cursor]
	case "a":
		for i := range hr.keep {
			hr.keep[i] = true
		}
	case "esc":
		m.hunkReview = nil
		m.refresh()
		return m, nil
	case "enter":
		content, err := diffmodel.ApplyHunks(hr.before, hr.hunks, hr.keep)
		decision := permission.Decision{Allow: true, Content: &content}
		if err != nil {
			m.addError(err)
			decision = permission.Decision{}
		}
		m.pending.reply <- decision
		m.pending = nil
		m.hunkReview = nil
		m.input.Focus()
		m.layout()
		m.refresh()
		return m, m.broker.wait()
	}
	return m, nil
}

func (m Model) renderHunkReview() string {
	hr := m.hunkReview
	title := m.styles.warning.Render("⚠ Review hunks — " + displayHunkPath(hr.path))
	var b strings.Builder
	b.WriteString(title + "\n")
	for i, h := range hr.hunks {
		mark := "☐"
		markColor := m.theme.Muted
		if hr.keep[i] {
			mark = "☑"
			markColor = m.theme.Success
		}
		cursor := "  "
		if i == hr.cursor {
			cursor = m.styles.accent.Render("▸ ")
		}
		header := fmt.Sprintf("%s%s hunk %d/%d  @@ -%d,%d +%d,%d @@", cursor, lipgloss.NewStyle().Foreground(lipgloss.Color(markColor)).Render(mark), i+1, len(hr.hunks), h.AStart, h.ACount, h.BStart, h.BCount)
		b.WriteString(header + "\n")
		if i == hr.cursor {
			const maxLines = 10
			lines := h.Lines
			truncated := false
			if len(lines) > maxLines {
				lines = lines[:maxLines]
				truncated = true
			}
			for _, line := range lines {
				switch {
				case strings.HasPrefix(line, "+"):
					b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Success)).Render(line) + "\n")
				case strings.HasPrefix(line, "-"):
					b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Error)).Render(line) + "\n")
				default:
					b.WriteString(m.styles.statusBase.Render(line) + "\n")
				}
			}
			if truncated {
				b.WriteString(m.styles.muted.Render(fmt.Sprintf("… %d more lines in this hunk", len(h.Lines)-maxLines)) + "\n")
			}
		}
	}
	b.WriteString(fmt.Sprintf("\n%s move   %s toggle   %s keep all   %s apply selected   %s cancel",
		badge("↑↓", m.theme.Border), badge("space", m.theme.Warning), badge("a", m.theme.Success), badge("enter", m.theme.Success), badge("esc", m.theme.Error)))
	return m.styles.approvalBox.Width(max(1, m.width-2) - 2).Render(b.String())
}

func displayHunkPath(path string) string {
	if idx := strings.LastIndexByte(path, '/'); idx >= 0 {
		return path[idx+1:]
	}
	return path
}
