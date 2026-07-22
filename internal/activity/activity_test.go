package activity

import (
	"fmt"
	"testing"
	"time"

	"github.com/robert-mcdermott/collomia/internal/event"
)

func TestProjectClassifiesAndBoundsEvents(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	var events []event.Event
	for i := 0; i < 8; i++ {
		e := event.Event{Schema: 1, Time: base.Add(time.Duration(i) * time.Second), Kind: event.KindToolResult}
		e.Tool = &event.Tool{Name: fmt.Sprintf("tool-%d", i), Summary: "completed"}
		events = append(events, e)
	}
	items := Project(events, 3)
	if len(items) != 3 || items[0].Title != "tool-5 completed" || items[2].Title != "tool-7 completed" {
		t.Fatalf("bounded projection = %+v", items)
	}
	if items[0].Category != CategoryTool || items[0].Status != StatusSuccess {
		t.Fatalf("classification = %+v", items[0])
	}
}

func TestFromEventPreservesFailureCorrelation(t *testing.T) {
	t.Parallel()
	e := event.New(event.KindError)
	e.Error = "provider connection failed"
	e.FailureID = "err-0123456789abcdef"
	item, ok := FromEvent(e)
	if !ok || item.Category != CategoryFailure || item.Status != StatusError {
		t.Fatalf("failure item = %+v ok=%t", item, ok)
	}
	if item.FailureID != e.FailureID || item.Detail != e.Error {
		t.Fatalf("failure correlation lost: %+v", item)
	}
}

func TestFromEventExcludesStreamingNoise(t *testing.T) {
	t.Parallel()
	for _, kind := range []event.Kind{event.KindTextDelta, event.KindToolOutput, event.KindToolCallDelta, event.KindUsage, event.KindPermissionRequest} {
		if item, ok := FromEvent(event.New(kind)); ok {
			t.Fatalf("%s unexpectedly projected as %+v", kind, item)
		}
	}
}

func TestFromEventRemovesTerminalControls(t *testing.T) {
	t.Parallel()
	e := event.New(event.KindWarning)
	e.Text = "before\x1b[2Jafter\x07"
	item, ok := FromEvent(e)
	if !ok || item.Detail != "before[2Jafter" {
		t.Fatalf("control-safe detail=%q ok=%t", item.Detail, ok)
	}
}

func BenchmarkProjectLargeSession(b *testing.B) {
	events := make([]event.Event, 10_000)
	for i := range events {
		events[i] = event.New(event.KindToolResult)
		events[i].Tool = &event.Tool{Name: "read_file", Summary: "read internal/example.go"}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Project(events, DefaultLimit)
	}
}
