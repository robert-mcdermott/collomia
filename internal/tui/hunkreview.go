package tui

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	inner := m.modalInnerWidth(hunkModalMaxWidth)
	var b strings.Builder
	b.WriteString(m.modalHeader("✎", "Review hunks", m.theme.Warning, inner))
	b.WriteString("\n\n" + m.styles.muted.Render(ansi.Truncate(displayHunkPath(hr.path), inner, "…")))
	kept := 0
	for _, keep := range hr.keep {
		if keep {
			kept++
		}
	}
	h := hr.hunks[hr.cursor]
	mark := "☐ excluded"
	markColor := m.theme.Muted
	if hr.keep[hr.cursor] {
		mark = "☑ included"
		markColor = m.theme.Success
	}
	status := fmt.Sprintf("%s  hunk %d/%d · %d selected", mark, hr.cursor+1, len(hr.hunks), kept)
	b.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color(markColor)).Render(ansi.Truncate(status, inner, "…")))
	b.WriteString("\n" + m.styles.muted.Render(ansi.Truncate(fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.AStart, h.ACount, h.BStart, h.BCount), inner, "…")))

	maxLines := min(14, max(2, m.height-15))
	lines := h.Lines
	hidden := 0
	if len(lines) > maxLines {
		hidden = len(lines) - maxLines
		lines = lines[:maxLines]
	}
	b.WriteString("\n\n")
	for i, line := range lines {
		line = ansi.Truncate(line, inner, "…")
		switch {
		case strings.HasPrefix(line, "+"):
			line = m.styles.success.Render(line)
		case strings.HasPrefix(line, "-"):
			line = m.styles.errText.Render(line)
		default:
			line = m.styles.muted.Render(line)
		}
		b.WriteString(line)
		if i < len(lines)-1 || hidden > 0 {
			b.WriteByte('\n')
		}
	}
	if hidden > 0 {
		b.WriteString(m.styles.muted.Render(fmt.Sprintf("… %d more lines in this hunk", hidden)))
	}
	b.WriteString("\n\n" + ansi.Wordwrap(
		badge("↑↓  Move", m.theme.Border)+"  "+badge("Space  Toggle", m.theme.Warning)+"  "+
			badge("A  Keep all", m.theme.Success)+"  "+badge("Enter  Apply", m.theme.Success)+"  "+badge("Esc  Back", m.theme.Error),
		inner, ""))
	return m.modalFrame(b.String(), m.theme.Warning, hunkModalMaxWidth)
}

func displayHunkPath(path string) string {
	if idx := strings.LastIndexByte(path, '/'); idx >= 0 {
		return path[idx+1:]
	}
	return path
}
