package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/robert-mcdermott/collomia/internal/diffmodel"
)

const (
	approvalModalMaxWidth = 110
	questionModalMaxWidth = 82
	hunkModalMaxWidth     = 110
)

func (m Model) modalActive() bool {
	return m.pending != nil || m.hunkReview != nil || m.question != nil
}

// renderComposer keeps the normal screen layout stable behind a modal. The
// active editor is rendered inside question dialogs; approvals use the same
// quiet placeholder so the transcript does not jump when a dialog opens.
func (m Model) renderComposer() string {
	box := m.styles.inputBox.Width(max(1, m.width-2))
	if !m.modalActive() {
		return box.Render(m.input.View())
	}
	message := "Dialog active"
	if m.question != nil {
		message = "Answer in the dialog"
	} else if m.hunkReview != nil {
		message = "Reviewing selected hunks"
	} else if m.pending != nil {
		message = "Approval required"
	}
	return box.Height(3).Render(m.styles.muted.Render("  " + message + "…"))
}

func (m Model) renderApproval() string {
	req := m.pending.request
	inner := m.modalInnerWidth(approvalModalMaxWidth)
	var body strings.Builder
	body.WriteString(m.modalHeader("⚠", "Permission required", m.theme.Warning, inner))
	body.WriteString("\n\n")
	body.WriteString(m.styles.muted.Render("Tool    ") + m.styles.accent.Render(ansi.Truncate(req.Tool, max(1, inner-8), "…")) + "\n")
	body.WriteString(m.styles.muted.Render("Action  ") + wrapAndLimit(req.Action.Summary, max(1, inner-8), 3))
	if req.Reason != "" {
		body.WriteString("\n" + m.styles.warning.Render(wrapAndLimit(req.Reason, inner, 2)))
	}
	if req.Action.Preview != "" {
		lines := strings.Split(strings.TrimRight(req.Action.Preview, "\n"), "\n")
		maxPreview := min(14, max(2, m.height-17))
		hidden := 0
		if len(lines) > maxPreview {
			hidden = len(lines) - maxPreview
			lines = lines[:maxPreview]
		}
		body.WriteString("\n\n")
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
			body.WriteString(line)
			if i < len(lines)-1 || hidden > 0 {
				body.WriteByte('\n')
			}
		}
		if hidden > 0 {
			body.WriteString(m.styles.muted.Render(fmt.Sprintf("… %d more diff lines", hidden)))
		}
	}
	oneTime := req.Action.Uninspectable || len(req.Action.ConfirmReasons) > 0
	buttons := badge("Y  Approve", m.theme.Success) + "  "
	if !oneTime {
		buttons += badge("A  Always", m.theme.Warning) + "  "
	}
	buttons += badge("N  Deny", m.theme.Error)
	if req.Tool == "write_file" && req.Action.Preview != "" {
		if hunks, err := diffmodel.ParseHunks(req.Action.Preview); err == nil && len(hunks) >= 2 {
			buttons += "  " + badge(fmt.Sprintf("H  Review %d hunks", len(hunks)), m.theme.Accent)
		}
	}
	body.WriteString("\n\n" + ansi.Wordwrap(buttons, inner, ""))
	return m.modalFrame(body.String(), m.theme.Warning, approvalModalMaxWidth)
}

func (m Model) renderQuestion() string {
	q := m.question.question
	inner := m.modalInnerWidth(questionModalMaxWidth)
	var body strings.Builder
	body.WriteString(m.modalHeader("?", "Collomia is asking", m.theme.Primary, inner))
	body.WriteString("\n\n")
	body.WriteString(m.styles.panelBody.Render(wrapAndLimit(q.Text, inner, min(6, max(2, m.height-14)))))

	optionBudget := max(0, m.height-17)
	shownOptions := 0
	for i := 0; i < len(q.Options); i++ {
		option := wrapAndLimit(q.Options[i], max(1, inner-5), 2)
		optionLines := strings.Count(option, "\n") + 1
		if optionLines > optionBudget {
			break
		}
		body.WriteString("\n" + badge(fmt.Sprintf("%d", i+1), m.theme.Border) + " " + option)
		optionBudget -= optionLines
		shownOptions++
	}
	if hidden := len(q.Options) - shownOptions; hidden > 0 {
		body.WriteString("\n" + m.styles.muted.Render(fmt.Sprintf("… %d more options", hidden)))
	}

	in := m.input
	in.Placeholder = "Type an answer or option number…"
	inputBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.theme.Primary)).
		Padding(0, 1)
	if m.theme.Background != "" {
		inputBox = inputBox.Background(lipgloss.Color(m.theme.Background))
	}
	// Lip Gloss Width includes padding but excludes the border. The textarea's
	// SetWidth, by contrast, is its complete rendered width. Reserve each frame
	// exactly once so the nested input cannot exceed the modal content row and
	// be re-wrapped into broken border fragments.
	in.SetWidth(max(1, inner-inputBox.GetHorizontalFrameSize()))
	in.SetHeight(1)
	inputBox = inputBox.Width(max(1, inner-inputBox.GetHorizontalBorderSize()))
	body.WriteString("\n\n" + inputBox.Render(in.View()))
	body.WriteString("\n" + m.styles.muted.Render(wrapAndLimit("Type an answer or option number · enter submit · esc decline", inner, 2)))
	return m.modalFrame(body.String(), m.theme.Primary, questionModalMaxWidth)
}

func (m Model) modalHeader(icon, title, color string, width int) string {
	labelText := ansi.Truncate(icon+" "+title, max(1, width-2), "…")
	label := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color)).Render(labelText)
	fill := max(1, width-lipgloss.Width(label)-1)
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Border)).Render(strings.Repeat("╱", fill))
	return label + " " + rule
}

func (m Model) modalOuterWidth(limit int) int {
	width := min(limit, m.width-4)
	if width < 12 {
		width = max(1, m.width)
	}
	return width
}

func (m Model) modalInnerWidth(limit int) int {
	style := m.modalStyle(m.theme.Border)
	return max(1, m.modalOuterWidth(limit)-style.GetHorizontalFrameSize())
}

func (m Model) modalFrame(body, border string, limit int) string {
	style := m.modalStyle(border)
	// Style.Width is the width before borders and already includes padding.
	// modalInnerWidth is the content width after both have been removed, so
	// subtract only the border here. Subtracting the complete frame made every
	// modal four columns narrower than its body calculations and caused nested
	// question editors to wrap their own border.
	widthBeforeBorder := max(1, m.modalOuterWidth(limit)-style.GetHorizontalBorderSize())
	return style.Width(widthBeforeBorder).Render(body)
}

func (m Model) modalStyle(border string) lipgloss.Style {
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(border)).
		Padding(1, 2)
	if m.theme.Background != "" {
		style = style.Background(lipgloss.Color(m.theme.Background))
	}
	return style
}

func wrapAndLimit(value string, width, maxLines int) string {
	if width < 1 || maxLines < 1 {
		return ""
	}
	wrapped := ansi.Wordwrap(strings.TrimSpace(value), width, "")
	lines := strings.Split(wrapped, "\n")
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = ansi.Truncate(strings.TrimRight(lines[maxLines-1], "…")+"…", width, "…")
	}
	return strings.Join(lines, "\n")
}

// placeOverlay composites a centered ANSI-aware modal over the existing
// screen. It replaces only the modal rectangle, preserving the transcript,
// tabs, composer, and status bar around it.
func placeOverlay(base, overlay string, width, height int) string {
	if width <= 0 || height <= 0 || overlay == "" {
		return base
	}
	baseLines := strings.Split(base, "\n")
	if len(baseLines) > height {
		baseLines = baseLines[:height]
	}
	for len(baseLines) < height {
		baseLines = append(baseLines, "")
	}
	for i := range baseLines {
		baseLines[i] = fitLine(baseLines[i], width)
	}

	overlayLines := strings.Split(overlay, "\n")
	overlayWidth := lipgloss.Width(overlay)
	if overlayWidth > width {
		overlayWidth = width
	}
	if len(overlayLines) > height {
		overlayLines = overlayLines[:height]
	}
	x := max(0, (width-overlayWidth)/2)
	y := max(0, (height-len(overlayLines))/2)
	for i, line := range overlayLines {
		line = fitLine(line, overlayWidth)
		under := baseLines[y+i]
		left := fitLine(ansi.Cut(under, 0, x), x)
		right := fitLine(ansi.Cut(under, x+overlayWidth, width), width-x-overlayWidth)
		baseLines[y+i] = left + line + right
	}
	return strings.Join(baseLines, "\n")
}

func fitLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	line = ansi.Truncate(line, width, "")
	if gap := width - ansi.StringWidth(line); gap > 0 {
		line += strings.Repeat(" ", gap)
	}
	return line
}
