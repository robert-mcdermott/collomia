package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

func TestCompletionNoticeIsCollapsibleAndRestored(t *testing.T) {
	detail := "Collomia completion controller (intervention 1 of 2): this response cannot finish the turn yet.\nRecorded gaps:\n- PRIVATE_DIAGNOSTIC"
	m := newTestModel(t)
	m.handleEvent(event.Event{Kind: event.KindWarning, Text: detail})
	if got := m.chatContent(); !strings.Contains(got, "Checking remaining work") || strings.Contains(got, "PRIVATE_DIAGNOSTIC") {
		t.Fatalf("not collapsed: %s", got)
	}
	m.expandTools = true
	if got := m.chatContent(); !strings.Contains(got, "PRIVATE_DIAGNOSTIC") {
		t.Fatal("details missing on expansion")
	}
	entry := m.blocks[len(m.blocks)-1]
	if rawBlockText(entry) != detail {
		t.Fatal("diagnostics lost from searchable/copyable transcript")
	}
	restored := restoredBlocks([]provider.Message{{Role: "user", Content: detail}})
	if len(restored) != 1 || restored[0].role != "status-detail" || restored[0].content != detail {
		t.Fatalf("resume: %+v", restored)
	}
	m.handleEvent(event.Event{Kind: event.KindWarning, Text: "Real warning needs attention"})
	if m.blocks[len(m.blocks)-1].role != "system" {
		t.Fatal("ordinary warning collapsed")
	}
	m.addError(errors.New("Actual blocked outcome"))
	if m.blocks[len(m.blocks)-1].role != "error" {
		t.Fatal("block hidden")
	}
}
