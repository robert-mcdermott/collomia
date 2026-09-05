package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	runtimeevent "github.com/robert-mcdermott/collomia/internal/event"
)

func reasoningEvent(text string) runtimeevent.Event {
	e := runtimeevent.New(runtimeevent.KindReasoningDelta)
	e.Text = text
	return e
}

func TestReasoningLifecycle(t *testing.T) {
	m := newTestModel(t)
	messageCount := m.runtime.Agent.MessageCount()
	m.busy = true
	for _, e := range []runtimeevent.Event{reasoningEvent("Check the "), reasoningEvent("inputs first.")} {
		updated, _ := m.Update(runMsg{event: &e})
		m = updated.(Model)
	}
	if len(m.blocks) != 1 || m.blocks[0].role != "reasoning" || m.blocks[0].content != "Check the inputs first." {
		t.Fatalf("reasoning deltas were lost or split: %+v", m.blocks)
	}
	if got := ansi.Strip(m.chatContent()); !strings.Contains(got, "THINKING SUMMARY") || !strings.Contains(got, "Check the inputs first.") {
		t.Fatalf("live summary not visible: %s", got)
	}
	m.handleEvent(toolStartEvent("read_file", "read inputs.csv"))
	m.handleEvent(toolResultEvent("read_file", "a,b\n1,2"))
	m.handleEvent(reasoningEvent("The totals agree."))
	m.handleEvent(deltaEvent("The result is 3."))
	updated, _ := m.Update(runMsg{done: true, final: "The result is 3."})
	m = updated.(Model)
	var roles []string
	for _, entry := range m.blocks {
		roles = append(roles, entry.role)
	}
	if got := strings.Join(roles, ","); got != "reasoning,tool,tool-result,reasoning,assistant" {
		t.Fatalf("thinking/tool/answer order changed: %s", got)
	}
	if got := ansi.Strip(m.chatContent()); strings.Contains(got, "The totals agree.") || !strings.Contains(got, "The result is 3.") {
		t.Fatalf("finished thinking should collapse separately from the answer: %s", got)
	}
	m = press(t, m, tea.KeyCtrlO)
	if got := m.chatContent(); !strings.Contains(got, "Check the inputs first.") || !strings.Contains(got, "The totals agree.") {
		t.Fatalf("details key did not expand summaries: %s", got)
	}
	m = press(t, m, tea.KeyCtrlO)
	if strings.Contains(m.chatContent(), "The totals agree.") {
		t.Fatal("details key did not collapse summaries")
	}
	if m.runtime.Agent.MessageCount() != messageCount {
		t.Fatal("display events changed the model conversation")
	}
}

func TestReasoningTranscriptSearchAndCopyText(t *testing.T) {
	m := newTestModel(t)
	m.handleEvent(reasoningEvent("A searchable reasoning marker."))
	m.handleEvent(deltaEvent("A separate answer."))
	want := "--- THINKING SUMMARY ---\nA searchable reasoning marker.\n\n--- COLLOMIA ---\nA separate answer."
	if got := m.rawTranscript(); got != want {
		t.Fatalf("copy text mixed reasoning and answer: %q", got)
	}
	m.openTranscriptView()
	m.transcript.query = "reasoning marker"
	m.findTranscriptMatches()
	if len(m.transcript.matches) != 1 || m.transcript.cursor != 0 {
		t.Fatalf("reasoning not searchable: %+v", m.transcript)
	}
}

func TestReasoningDisplayBoundAndUTF8(t *testing.T) {
	m := newTestModel(t)
	m.busy = true
	m.handleEvent(reasoningEvent(strings.Repeat("x", maxReasoningBlockBytes-1)))
	m.handleEvent(reasoningEvent("界"))
	m.handleEvent(reasoningEvent("must not appear"))
	entry := m.blocks[0]
	if len(entry.content) > maxReasoningBlockBytes || !entry.reasoningTruncated || !utf8.ValidString(entry.content) {
		t.Fatalf("invalid bounded summary: bytes=%d truncated=%v UTF8=%v", len(entry.content), entry.reasoningTruncated, utf8.ValidString(entry.content))
	}
	if strings.Contains(entry.content, "must not appear") || !strings.Contains(m.rawTranscript(), reasoningTruncationNotice) {
		t.Fatal("truncation was not sticky and explicit")
	}
	m.handleEvent(deltaEvent("Answer survives."))
	m.handleEvent(reasoningEvent("A later summary survives."))
	if m.blocks[len(m.blocks)-1].content != "A later summary survives." {
		t.Fatal("one truncated summary suppressed a later one")
	}
}

func TestReasoningLivePreviewAndConfiguredKey(t *testing.T) {
	m := newTestModel(t)
	m.runtime.Config.Options.Keybindings = map[string]string{"toggle_tool_output": "alt+o"}
	m.busy = true
	m.handleEvent(reasoningEvent("first-marker\n" + strings.Repeat("middle\n", 10) + "last-marker"))
	got := ansi.Strip(m.renderReasoning(0))
	if strings.Contains(got, "first-marker") || !strings.Contains(got, "last-marker") || !strings.Contains(got, "alt+o to expand") {
		t.Fatalf("expected bounded live tail and effective binding: %s", got)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	m = updated.(Model)
	if !strings.Contains(m.renderReasoning(0), "first-marker") {
		t.Fatal("configured key did not reveal earlier summary text")
	}
}

func TestReasoningAbsentAndStopped(t *testing.T) {
	m := newTestModel(t)
	m.handleEvent(reasoningEvent(""))
	m.handleEvent(deltaEvent("An ordinary answer."))
	if strings.Contains(m.chatContent(), "THINKING SUMMARY") || len(m.blocks) != 1 {
		t.Fatal("invented thinking for a provider without readable reasoning")
	}
	m.busy = true
	m.handleEvent(reasoningEvent("An interrupted thought."))
	updated, _ := m.Update(runMsg{done: true})
	m = updated.(Model)
	if strings.Contains(m.chatContent(), "An interrupted thought.") || !strings.Contains(m.rawTranscript(), "An interrupted thought.") {
		t.Fatal("stopping should collapse but retain the received summary")
	}
}

func TestReasoningNarrowScreens(t *testing.T) {
	for _, width := range []int{20, 40, 80, 150} {
		m := newTestModel(t)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		m = updated.(Model)
		m.busy = true
		m.handleEvent(reasoningEvent(strings.Repeat("思考 very-long-word-", 40)))
		for _, expanded := range []bool{false, true} {
			m.expandTools = expanded
			for _, line := range strings.Split(m.renderReasoning(0), "\n") {
				if got := ansi.StringWidth(line); got > m.transcriptMeasure(0) {
					t.Fatalf("width=%d expanded=%v: line overflows (%d): %q", width, expanded, got, line)
				}
			}
		}
	}
}

func TestReasoningGoldenScreens(t *testing.T) {
	for _, name := range []string{"reasoning_live_80x24.txt", "reasoning_done_80x24.txt"} {
		t.Run(name, func(t *testing.T) {
			m := newTestModel(t)
			plain, _ := themeByName("plain")
			m.applyTheme(plain)
			m.blocks = []block{{role: "user", content: "Check the report totals."}}
			m.busy = true
			m.handleEvent(reasoningEvent("I will check the inputs, reconcile the totals, and inspect the report."))
			if strings.Contains(name, "done") {
				m.handleEvent(deltaEvent("The totals agree with the inputs."))
				m.busy = false
			}
			updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			m = updated.(Model)
			assertGoldenScreen(t, name, m.View())
		})
	}
}
