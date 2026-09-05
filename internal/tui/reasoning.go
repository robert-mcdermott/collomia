package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxReasoningBlockBytes    = 64 * 1024
	reasoningPreviewLines     = 6
	reasoningTruncationNotice = "[Thinking summary truncated in this view at 64 KiB.]"
)

// appendReasoning consumes only readable text the provider actually emitted.
// It never mixes that text into the answer or model-visible conversation.
func (m *Model) appendReasoning(text string) {
	if text == "" {
		return
	}
	if len(m.blocks) == 0 || m.blocks[len(m.blocks)-1].role != "reasoning" {
		m.blocks = append(m.blocks, block{role: "reasoning"})
	}
	entry := &m.blocks[len(m.blocks)-1]
	if entry.reasoningTruncated {
		return
	}
	remaining := maxReasoningBlockBytes - len(entry.content)
	if len(text) > remaining {
		// Provider JSON strings are UTF-8; don't split a rune at the cap.
		end := remaining
		for end > 0 && !utf8.RuneStart(text[end]) {
			end--
		}
		text = text[:end]
		entry.reasoningTruncated = true
	}
	entry.content += text
}

// renderReasoning keeps the live tail readable without letting a long summary
// displace the entire chat. Finished summaries collapse; the existing details
// key and transcript search/copy view reveal the retained text.
func (m *Model) renderReasoning(i int) string {
	entry := m.blocks[i]
	header := m.styles.muted.Render(m.wrapProse("THINKING SUMMARY", 0))
	current := m.busy && i == len(m.blocks)-1
	if !m.expandTools && !current {
		note := fmt.Sprintf("▸ %s to expand", m.binding("toggle_tool_output"))
		if entry.reasoningTruncated {
			note += " · truncated at 64 KiB"
		}
		return header + "\n" + m.styles.muted.Render(m.wrapProse(note, 0))
	}
	lines := strings.Split(m.wrapOutput(rawBlockText(entry), 0), "\n")
	if !m.expandTools && len(lines) > reasoningPreviewLines {
		header += "\n" + m.styles.muted.Render(m.wrapProse(
			fmt.Sprintf("… earlier lines hidden · %s to expand", m.binding("toggle_tool_output")), 0))
		lines = lines[len(lines)-reasoningPreviewLines:]
	}
	return header + "\n" + m.styles.muted.Render(strings.Join(lines, "\n"))
}
