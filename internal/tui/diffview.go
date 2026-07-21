package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/robert-mcdermott/collomia/internal/diffmodel"
)

const sideBySideMinWidth = 108

type diffViewState struct {
	files       []diffmodel.FileDiff
	file        int
	mode        string
	folded      bool
	viewport    viewport.Model
	hunkOffsets []int
	hunk        int
	notice      string
	stats       []diffStats
}

type diffStats struct{ added, deleted int }

type reviewRow struct {
	line   *diffmodel.AlignedLine
	hidden int
}

func (m *Model) openDiffView() {
	files := m.runtime.Changes.FileDiffs(m.runtime.Workspace)
	if len(files) == 0 {
		m.addSystem("No agent file changes this session.")
		m.refresh()
		return
	}
	mode := "unified"
	if m.width >= sideBySideMinWidth {
		mode = "side-by-side"
	}
	stats := make([]diffStats, len(files))
	for i, file := range files {
		stats[i].added, stats[i].deleted = diffCounts(file)
	}
	m.diffView = &diffViewState{
		files:    files,
		stats:    stats,
		mode:     mode,
		folded:   true,
		viewport: viewport.New(max(1, m.width), max(1, m.height-2)),
	}
	m.input.Blur()
	m.rebuildDiffView()
}

func (m *Model) rebuildDiffView() {
	state := m.diffView
	if state == nil || len(state.files) == 0 {
		return
	}
	if state.mode == "side-by-side" && m.width < sideBySideMinWidth {
		state.mode = "unified"
		state.notice = fmt.Sprintf("side-by-side needs at least %d columns", sideBySideMinWidth)
	}
	offset := state.viewport.YOffset
	state.viewport.Width = max(1, m.width)
	state.viewport.Height = max(1, m.height-2)
	state.hunkOffsets = state.hunkOffsets[:0]
	file := state.files[state.file]
	var content string
	if state.mode == "side-by-side" {
		content = m.renderSideBySide(file, state)
	} else {
		content = m.renderUnifiedReview(file, state)
	}
	state.viewport.SetContent(content)
	state.viewport.SetYOffset(offset)
	if len(state.hunkOffsets) == 0 {
		state.hunk = 0
	} else if state.hunk >= len(state.hunkOffsets) {
		state.hunk = len(state.hunkOffsets) - 1
	}
}

func (m Model) renderUnifiedReview(file diffmodel.FileDiff, state *diffViewState) string {
	if state.folded {
		var b strings.Builder
		for lineOffset, line := range strings.Split(strings.TrimRight(file.Unified, "\n"), "\n") {
			if strings.HasPrefix(line, "@@") {
				state.hunkOffsets = append(state.hunkOffsets, lineOffset)
			}
			b.WriteString(m.styleDiffLine(ansi.Truncate(line, max(1, m.width), "…")))
			b.WriteByte('\n')
		}
		return strings.TrimRight(b.String(), "\n")
	}

	rows := visibleReviewRows(diffmodel.Align(file.Before, file.After), false)
	return m.renderUnifiedRows(rows, state)
}

func (m Model) renderUnifiedRows(rows []reviewRow, state *diffViewState) string {
	var b strings.Builder
	previousChanged := false
	lineOffset := 0
	for _, item := range rows {
		if item.hidden > 0 {
			fmt.Fprintf(&b, "%s\n", m.styles.muted.Render(fmt.Sprintf("… %d unchanged lines …", item.hidden)))
			previousChanged = false
			lineOffset++
			continue
		}
		row := *item.line
		changed := row.Kind != ' '
		if changed && !previousChanged {
			state.hunkOffsets = append(state.hunkOffsets, lineOffset)
		}
		previousChanged = changed
		left, right := lineNumber(row.LeftNumber), lineNumber(row.RightNumber)
		text := row.Left
		if row.Kind == '+' {
			text = row.Right
		}
		line := fmt.Sprintf("%5s %5s %c %s", left, right, row.Kind, text)
		line = ansi.Truncate(line, max(1, m.width), "…")
		switch row.Kind {
		case '+':
			line = m.styles.success.Render(line)
		case '-':
			line = m.styles.errText.Render(line)
		default:
			line = m.styles.panelBody.Render(line)
		}
		b.WriteString(line)
		b.WriteByte('\n')
		lineOffset++
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) renderSideBySide(file diffmodel.FileDiff, state *diffViewState) string {
	rows := visibleReviewRows(diffmodel.Align(file.Before, file.After), state.folded)
	divider := " │ "
	column := max(10, (m.width-len(divider))/2)
	var b strings.Builder
	previousChanged := false
	lineOffset := 0
	for _, item := range rows {
		if item.hidden > 0 {
			label := fmt.Sprintf("… %d unchanged lines …", item.hidden)
			b.WriteString(m.styles.muted.Render(centerText(label, m.width)))
			b.WriteByte('\n')
			previousChanged = false
			lineOffset++
			continue
		}
		row := *item.line
		changed := row.Kind != ' '
		if changed && !previousChanged {
			state.hunkOffsets = append(state.hunkOffsets, lineOffset)
		}
		previousChanged = changed
		leftPrefix := fmt.Sprintf("%5s %c ", lineNumber(row.LeftNumber), sideMarker(row.Kind, true))
		rightPrefix := fmt.Sprintf("%5s %c ", lineNumber(row.RightNumber), sideMarker(row.Kind, false))
		left := fitLine(leftPrefix+row.Left, column)
		right := fitLine(rightPrefix+row.Right, column)
		if row.Kind == '-' {
			left = m.styles.errText.Render(left)
			right = m.styles.muted.Render(right)
		} else if row.Kind == '+' {
			left = m.styles.muted.Render(left)
			right = m.styles.success.Render(right)
		} else {
			left, right = m.styles.panelBody.Render(left), m.styles.panelBody.Render(right)
		}
		b.WriteString(left + m.styles.muted.Render(divider) + right + "\n")
		lineOffset++
	}
	return strings.TrimRight(b.String(), "\n")
}

func visibleReviewRows(rows []diffmodel.AlignedLine, folded bool) []reviewRow {
	if !folded {
		out := make([]reviewRow, len(rows))
		for i := range rows {
			out[i].line = &rows[i]
		}
		return out
	}
	visible := make([]bool, len(rows))
	for i, row := range rows {
		if row.Kind == ' ' {
			continue
		}
		start, end := max(0, i-3), min(len(rows), i+4)
		for j := start; j < end; j++ {
			visible[j] = true
		}
	}
	var out []reviewRow
	for i := 0; i < len(rows); {
		if visible[i] {
			out = append(out, reviewRow{line: &rows[i]})
			i++
			continue
		}
		start := i
		for i < len(rows) && !visible[i] {
			i++
		}
		out = append(out, reviewRow{hidden: i - start})
	}
	return out
}

func (m Model) styleDiffLine(line string) string {
	switch {
	case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
		return m.styles.success.Render(line)
	case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
		return m.styles.errText.Render(line)
	case strings.HasPrefix(line, "@@"):
		return m.styles.accent.Render(line)
	case strings.HasPrefix(line, "---"), strings.HasPrefix(line, "+++"):
		return m.styles.heading.Render(line)
	default:
		return m.styles.panelBody.Render(line)
	}
}

func (m Model) handleDiffViewKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := m.diffView
	keyName := key.String()
	switch keyName {
	case "esc", "q":
		m.diffView = nil
		m.input.Focus()
		m.refresh()
		return m, nil
	case "[", "left":
		m.moveDiffFile(-1)
		return m, nil
	case "]", "right":
		m.moveDiffFile(1)
		return m, nil
	case "up", "k":
		state.viewport.LineUp(1)
	case "down", "j":
		state.viewport.LineDown(1)
	case "n":
		m.moveDiffHunk(1)
		return m, nil
	case "N":
		m.moveDiffHunk(-1)
		return m, nil
	case "f":
		state.folded = !state.folded
		state.notice = map[bool]string{true: "unchanged regions folded", false: "showing complete file"}[state.folded]
		state.viewport.GotoTop()
		m.rebuildDiffView()
		return m, nil
	case "u":
		state.mode = "unified"
		state.notice = "unified view"
		state.viewport.GotoTop()
		m.rebuildDiffView()
		return m, nil
	case "s":
		if m.width < sideBySideMinWidth {
			state.notice = fmt.Sprintf("side-by-side needs at least %d columns", sideBySideMinWidth)
			return m, nil
		}
		state.mode = "side-by-side"
		state.notice = "side-by-side view"
		state.viewport.GotoTop()
		m.rebuildDiffView()
		return m, nil
	default:
		switch {
		case m.keyIs("page_up", keyName):
			state.viewport.PageUp()
		case m.keyIs("page_down", keyName):
			state.viewport.PageDown()
		case m.keyIs("scroll_top", keyName):
			state.viewport.GotoTop()
		case m.keyIs("scroll_bottom", keyName):
			state.viewport.GotoBottom()
		}
	}
	return m, nil
}

func (m *Model) moveDiffFile(delta int) {
	state := m.diffView
	state.file = (state.file + delta + len(state.files)) % len(state.files)
	state.hunk = 0
	state.viewport.GotoTop()
	state.notice = ""
	m.rebuildDiffView()
}

func (m *Model) moveDiffHunk(delta int) {
	state := m.diffView
	if len(state.hunkOffsets) == 0 {
		state.notice = "no change hunks"
		return
	}
	state.hunk = (state.hunk + delta + len(state.hunkOffsets)) % len(state.hunkOffsets)
	state.viewport.SetYOffset(state.hunkOffsets[state.hunk])
	state.notice = fmt.Sprintf("hunk %d/%d", state.hunk+1, len(state.hunkOffsets))
}

func (m Model) renderDiffView() string {
	state := m.diffView
	file := state.files[state.file]
	stats := state.stats[state.file]
	header := m.styles.brand.Render(" Diff review ") + m.styles.accent.Render(file.Name) +
		m.styles.muted.Render(fmt.Sprintf("  file %d/%d · +%d -%d · %s", state.file+1, len(state.files), stats.added, stats.deleted, state.mode))
	footer := "[ ] file · n/N hunk · ↑↓/page scroll · u unified · s side-by-side · f fold · esc close"
	if state.notice != "" {
		footer = state.notice + "  ·  " + footer
	}
	return fitLine(header, max(1, m.width)) + "\n" + state.viewport.View() + "\n" + fitLine(m.styles.muted.Render(footer), max(1, m.width))
}

func diffCounts(file diffmodel.FileDiff) (added, deleted int) {
	for _, row := range diffmodel.Align(file.Before, file.After) {
		switch row.Kind {
		case '+':
			added++
		case '-':
			deleted++
		}
	}
	return added, deleted
}

func sideMarker(kind byte, left bool) byte {
	if (left && kind == '-') || (!left && kind == '+') {
		return kind
	}
	return ' '
}

func lineNumber(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}

func centerText(value string, width int) string {
	valueWidth := ansi.StringWidth(value)
	if width <= valueWidth {
		return ansi.Truncate(value, max(1, width), "…")
	}
	return strings.Repeat(" ", (width-valueWidth)/2) + value
}
