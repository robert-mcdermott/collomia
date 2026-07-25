package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// toolStatus is the outcome of one tool call as the transcript knows it.
type toolStatus int

const (
	// toolUnknown is the zero value, used for tool calls replayed from a
	// saved session where no outcome or timing was recorded.
	toolUnknown toolStatus = iota
	toolRunning
	toolSucceeded
	toolFailed
)

// toolDurationFloor hides timings too short to be meaningful. A read that
// finished in four milliseconds says nothing worth a column of its own, and
// stamping every line with one buries the durations that do matter.
const toolDurationFloor = 100 * time.Millisecond

func (m Model) toolStatusStyle(status toolStatus) (string, lipgloss.Style) {
	switch status {
	case toolRunning:
		return "●", m.styles.warning
	case toolSucceeded:
		return "✓", m.styles.success
	case toolFailed:
		return "✗", m.styles.errText
	}
	return "⚙", m.styles.tool
}

// renderToolHeader draws the one-line record of a tool call: outcome glyph,
// name, summary, and the elapsed time pushed to the right margin so the
// durations form a scannable column instead of hiding at the end of summaries
// of wildly different lengths.
func (m Model) renderToolHeader(b block) string {
	name, summary, _ := strings.Cut(b.content, "\x00")
	glyph, glyphStyle := m.toolStatusStyle(b.status)
	width := max(20, m.bodyWidth())

	right := ""
	if b.elapsed >= toolDurationFloor {
		right = formatToolDuration(b.elapsed)
	}
	rightWidth := 0
	if right != "" {
		rightWidth = ansi.StringWidth(right) + 1 // one column of breathing room
	}

	// The glyph is one cell plus its trailing space; the name and summary
	// share whatever the duration column does not claim.
	budget := max(1, width-2-rightWidth)
	name = ansi.Truncate(name, budget, "…")
	line := glyphStyle.Render(glyph) + " " + m.styles.toolName.Render(name)
	if rest := budget - ansi.StringWidth(name); summary != "" && rest > 2 {
		line += m.styles.tool.Render(ansi.Truncate("  "+summary, rest, "…"))
	}
	if right == "" {
		return line
	}
	gap := width - ansi.StringWidth(line) - ansi.StringWidth(right)
	if gap < 1 {
		gap = 1
	}
	return line + strings.Repeat(" ", gap) + m.styles.tool.Render(right)
}

// formatToolDuration keeps every timing to at most five columns so the right
// margin stays straight.
func formatToolDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	default:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}

// completeToolBlock stamps the outcome onto the most recent still-running
// tool header. Results arrive after any streamed output has already appended
// its own blocks, so the header is not necessarily the last entry.
func (m *Model) completeToolBlock(name string, failed bool) {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].role != "tool" || m.blocks[i].status != toolRunning {
			continue
		}
		if blockToolName(m.blocks[i]) != name {
			continue
		}
		m.blocks[i].status = toolSucceeded
		if failed {
			m.blocks[i].status = toolFailed
		}
		if !m.blocks[i].started.IsZero() {
			m.blocks[i].elapsed = time.Since(m.blocks[i].started)
		}
		return
	}
}

// settleRunningTools clears the running marker when a turn ends without a
// result for every call, which is what a cancelled or failed turn looks like.
// Leaving the glyph spinning would claim work is still in flight.
func (m *Model) settleRunningTools() {
	for i := range m.blocks {
		if m.blocks[i].role == "tool" && m.blocks[i].status == toolRunning {
			m.blocks[i].status = toolUnknown
		}
	}
}

func blockToolName(b block) string {
	name, _, _ := strings.Cut(b.content, "\x00")
	return name
}
