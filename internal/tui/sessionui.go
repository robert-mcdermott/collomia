package tui

import (
	"encoding/json"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/activity"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

type savedToolCall struct {
	name    string
	summary string
}

// rebuildTranscript reconstructs the visible chat from the complete durable
// transcript. The agent continues to use Session.Active(), which may contain a
// compacted summary; presentation deliberately uses TranscriptMessages() so
// resuming never makes accepted conversation or tool evidence disappear.
func (m *Model) rebuildTranscript() {
	var messages []provider.Message
	m.activities = nil
	if m.runtime.Session != nil {
		messages = m.runtime.Session.TranscriptMessages()
		m.activities = activity.Project(m.runtime.Session.RecentEvents(), activity.DefaultLimit)
	}
	m.blocks = restoredBlocks(messages)
	m.resetPromptHistory(messages)
	m.restoreSessionDraft()
	m.refresh()
}

// reloadActivities picks up runtime-owned transitions emitted outside an
// agent turn, such as explicit Orchestrated Goal approval, resume, or cancel.
// Turn events continue to append incrementally through handleEvent.
func (m *Model) reloadActivities() {
	if m.runtime.Session == nil {
		return
	}
	m.activities = activity.Project(m.runtime.Session.RecentEvents(), activity.DefaultLimit)
	if m.activityView != nil {
		m.rebuildActivityView()
	}
}

func restoredBlocks(messages []provider.Message) []block {
	blocks := make([]block, 0, len(messages))
	calls := make(map[string]savedToolCall)
	var callOrder []string
	for _, message := range messages {
		switch message.Role {
		case "user":
			if strings.HasPrefix(message.Content, "[Context summary") {
				blocks = append(blocks, block{role: "system", content: "· older model context was compacted; complete conversation restored below ·"})
				continue
			}
			blocks = append(blocks, block{role: "user", content: displayMessageWithAttachments(message.Content, message.Parts)})
		case "assistant":
			if message.Content != "" {
				blocks = append(blocks, block{role: "assistant", content: message.Content})
			}
			for _, call := range message.ToolCalls {
				calls[call.ID] = savedToolCall{name: call.Name, summary: restoredToolSummary(call)}
				callOrder = append(callOrder, call.ID)
			}
		case "tool":
			call, ok := calls[message.ToolCallID]
			if !ok {
				call = savedToolCall{name: "tool", summary: "restored result"}
			}
			blocks = append(blocks,
				block{role: "tool", content: call.name + "\x00" + call.summary},
				block{role: "tool-result", content: message.Content, tool: call.name, summary: call.summary},
			)
			delete(calls, message.ToolCallID)
		case "system":
			if message.Content != "" {
				blocks = append(blocks, block{role: "system", content: message.Content})
			}
		}
	}
	// A normal resumed session synthesizes an interrupted tool result before
	// the TUI is built. Retain a visible marker for malformed/legacy records
	// that still contain a call without a result; never execute it.
	for _, id := range callOrder {
		call, ok := calls[id]
		if !ok {
			continue
		}
		blocks = append(blocks,
			block{role: "tool", content: call.name + "\x00" + call.summary},
			block{role: "system", content: "Saved tool call has no result. It was not replayed."},
		)
	}
	return blocks
}

func restoredToolSummary(call provider.ToolCall) string {
	summary := "restored from saved session"
	var args map[string]any
	if json.Unmarshal(call.Arguments, &args) != nil {
		return summary
	}
	switch call.Name {
	case "read_file":
		if path, ok := args["path"].(string); ok && path != "" {
			return "read " + path
		}
	case "run_command", "start_process":
		if command, ok := args["command"].(string); ok && command != "" {
			return command
		}
	}
	return summary
}

func (m *Model) resetPromptHistory(messages []provider.Message) {
	m.promptHistory = m.promptHistory[:0]
	for _, message := range messages {
		if message.Role == "user" && !strings.HasPrefix(message.Content, "[Context summary") && strings.TrimSpace(message.Content) != "" {
			m.promptHistory = append(m.promptHistory, message.Content)
		}
	}
	m.historyIndex = len(m.promptHistory)
	m.historyDraft = ""
}

func (m *Model) recordPrompt(prompt string) {
	if strings.TrimSpace(prompt) == "" {
		return
	}
	m.promptHistory = append(m.promptHistory, prompt)
	m.historyIndex = len(m.promptHistory)
	m.historyDraft = ""
}

// navigatePromptHistory uses up/down only at the first/last visual line. This
// preserves normal movement inside multiline and soft-wrapped prompts.
func (m *Model) navigatePromptHistory(previous bool) bool {
	if len(m.promptHistory) == 0 {
		return false
	}
	line := m.input.LineInfo()
	if previous {
		if m.input.Line() != 0 || line.RowOffset != 0 {
			return false
		}
		if m.historyIndex == len(m.promptHistory) {
			m.historyDraft = m.input.Value()
		}
		if m.historyIndex == 0 {
			return true
		}
		m.historyIndex--
		m.input.SetValue(m.promptHistory[m.historyIndex])
		m.input.CursorStart()
		return true
	}
	if m.input.Line() != m.input.LineCount()-1 || line.RowOffset+1 < line.Height {
		return false
	}
	if m.historyIndex >= len(m.promptHistory) {
		return false
	}
	m.historyIndex++
	if m.historyIndex == len(m.promptHistory) {
		m.input.SetValue(m.historyDraft)
	} else {
		m.input.SetValue(m.promptHistory[m.historyIndex])
	}
	m.input.CursorEnd()
	return true
}

func (m *Model) leavePromptHistory() {
	if m.historyIndex == len(m.promptHistory) {
		return
	}
	m.historyIndex = len(m.promptHistory)
	m.historyDraft = ""
}

func (m *Model) setComposerValue(value string) {
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.historyIndex = len(m.promptHistory)
	m.historyDraft = ""
	// A loaded prompt, a restored draft, or a recalled history entry can be
	// any number of lines, and the transcript's height is derived from the
	// editor's, so the surrounding layout has to follow the new content.
	if m.syncComposerHeight() {
		m.layout()
	}
}

func (m *Model) sessionDraftKey() string {
	if m.runtime.Session == nil {
		return ""
	}
	return m.runtime.Session.Meta.ID
}

func (m *Model) saveSessionDraft() {
	if key := m.sessionDraftKey(); key != "" {
		m.sessionDrafts[key] = m.input.Value()
		m.sessionAttachments[key] = append([]pendingAttachment(nil), m.pendingAttachments...)
	}
}

func (m *Model) restoreSessionDraft() {
	value := ""
	if key := m.sessionDraftKey(); key != "" {
		value = m.sessionDrafts[key]
		m.pendingAttachments = append([]pendingAttachment(nil), m.sessionAttachments[key]...)
	} else {
		m.pendingAttachments = nil
	}
	m.setComposerValue(value)
}
