package activity

import (
	"fmt"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/event"
)

// turnEvent is a minimal projectable event carrying an identifiable turn
// number, so a bounded timeline can be checked for *which* entries survived
// rather than only how many.
func turnEvent(turn int) event.Event {
	return event.Event{Kind: event.KindTurnStart, Turn: turn}
}

func titles(items []Item) string {
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = item.Title
	}
	return strings.Join(parts, ", ")
}

func TestAppendKeepsTheNewestEntriesWhenTheLimitIsReached(t *testing.T) {
	// The bound must drop the oldest entry, not the newest. A timeline that
	// discarded the arriving event would freeze at the moment it filled, which
	// is exactly when an operator starts watching it.
	var items []Item
	for turn := 1; turn <= 5; turn++ {
		items = Append(items, turnEvent(turn), 3)
	}
	if len(items) != 3 {
		t.Fatalf("len = %d, want the limit of 3", len(items))
	}
	if got := titles(items); got != "Turn started · 3, Turn started · 4, Turn started · 5" {
		t.Errorf("kept %q, want the three newest turns in order", got)
	}
}

func TestAppendAndProjectAgreeOnTheSameEvents(t *testing.T) {
	// These are two independent ring implementations over one contract:
	// Project rotates a filled buffer and un-rotates it at the end, Append
	// shifts in place. A divergence would mean the timeline you get by
	// resuming a session differs from the one you get by watching it happen,
	// which is the kind of difference nobody reports as a bug because it is
	// never seen twice.
	for _, limit := range []int{1, 2, 3, 7, 50} {
		for _, count := range []int{0, 1, 2, 5, 9, 51} {
			events := make([]event.Event, 0, count)
			for turn := 1; turn <= count; turn++ {
				events = append(events, turnEvent(turn))
			}
			var incremental []Item
			for _, e := range events {
				incremental = Append(incremental, e, limit)
			}
			projected := Project(events, limit)
			if titles(incremental) != titles(projected) {
				t.Errorf("limit %d, %d events:\n  Append:  %s\n  Project: %s",
					limit, count, titles(incremental), titles(projected))
			}
		}
	}
}

func TestAppendIgnoresAnEventWithNothingToShow(t *testing.T) {
	// FromEvent rejects streaming deltas and payload-less events. Append must
	// return the timeline unchanged rather than growing it with a zero Item,
	// which would consume the bound with blank rows.
	before := []Item{{Title: "existing"}}
	after := Append(before, event.Event{Kind: event.KindTextDelta, Text: "streaming"}, 10)
	if len(after) != 1 || after[0].Title != "existing" {
		t.Errorf("timeline = %q, want it untouched", titles(after))
	}
	// A tool event with no tool payload is the guard-clause case.
	after = Append(before, event.Event{Kind: event.KindToolStart}, 10)
	if len(after) != 1 {
		t.Errorf("a tool event with no payload must be dropped, got %q", titles(after))
	}
}

func TestAppendTreatsANonPositiveLimitAsTheDefault(t *testing.T) {
	for _, limit := range []int{0, -1} {
		var items []Item
		for turn := 1; turn <= DefaultLimit+5; turn++ {
			items = Append(items, turnEvent(turn), limit)
		}
		if len(items) != DefaultLimit {
			t.Errorf("limit %d produced %d items, want the DefaultLimit of %d", limit, len(items), DefaultLimit)
		}
	}
}

func TestAppendShrinksATimelineThatIsAlreadyOverTheLimit(t *testing.T) {
	// A frontend may lower its bound at runtime — a narrower pane, a changed
	// setting. Append is where that takes effect, and the arithmetic that
	// trims has to keep the newest entries rather than reading off the end.
	items := Project(func() []event.Event {
		out := make([]event.Event, 0, 10)
		for turn := 1; turn <= 10; turn++ {
			out = append(out, turnEvent(turn))
		}
		return out
	}(), 10)
	if len(items) != 10 {
		t.Fatalf("setup produced %d items", len(items))
	}
	items = Append(items, turnEvent(11), 4)
	if len(items) != 4 {
		t.Fatalf("len = %d, want 4", len(items))
	}
	if got := titles(items); got != "Turn started · 8, Turn started · 9, Turn started · 10, Turn started · 11" {
		t.Errorf("kept %q, want the four newest", got)
	}
}

func TestTurnTitleOmitsATurnNumberThereIsNoneOf(t *testing.T) {
	// Session-level events carry turn 0. "Turn started · 0" would be a
	// fabricated number in an operator timeline.
	if got := turnTitle("Turn started", 0); got != "Turn started" {
		t.Errorf("turnTitle(…, 0) = %q, want no separator", got)
	}
	if got := turnTitle("Turn started", -3); got != "Turn started" {
		t.Errorf("turnTitle(…, -3) = %q, want no separator", got)
	}
	if got := turnTitle("Turn completed", 12); got != "Turn completed · 12" {
		t.Errorf("turnTitle(…, 12) = %q", got)
	}
}

func TestEveryDelegateStatusMapsToAStatusThatMatchesItsMeaning(t *testing.T) {
	// The mapping decides an item's colour. Getting "cancelled" wrong reads as
	// a failure that did not happen; getting "error" wrong hides one that did.
	for status, want := range map[string]Status{
		"done":             StatusSuccess,
		"error":            StatusError,
		"timed-out":        StatusError,
		"budget-exhausted": StatusError,
		"interrupted":      StatusError,
		"cancelled":        StatusWarning,
		"cancelling":       StatusWarning,
		"waiting-approval": StatusWarning,
		"queued":           StatusActive,
		"running":          StatusActive,
	} {
		if got := delegateStatus(status); got != want {
			t.Errorf("delegateStatus(%q) = %q, want %q", status, got, want)
		}
	}
	// An unrecognized status must be neutral rather than inheriting whichever
	// branch happens to be last.
	if got := delegateStatus("something-new"); got != StatusInfo {
		t.Errorf("an unknown delegate status = %q, want %q", got, StatusInfo)
	}
	if got := delegateStatus(""); got != StatusInfo {
		t.Errorf("an empty delegate status = %q, want %q", got, StatusInfo)
	}
}

func TestFirstNonEmptySkipsWhitespaceOnlyValues(t *testing.T) {
	// A delegate whose CurrentAction is "   " must fall through to its summary
	// rather than showing a blank detail line.
	if got := firstNonEmpty("", "   ", "summary", "error"); got != "summary" {
		t.Errorf("firstNonEmpty = %q, want %q", got, "summary")
	}
	if got := firstNonEmpty("", "  "); got != "" {
		t.Errorf("firstNonEmpty with nothing usable = %q, want empty", got)
	}
	if got := firstNonEmpty(); got != "" {
		t.Errorf("firstNonEmpty() = %q, want empty", got)
	}
}

func TestDelegateItemFallsBackToTheDelegatesOwnFailureID(t *testing.T) {
	// The correlation id is what a support report is searched by. A delegated
	// failure carries its own, and the event-level field is empty for it.
	item, ok := FromEvent(event.Event{
		Kind:     event.KindDelegateUpdate,
		Delegate: &event.DelegateStatus{Name: "reviewer", Status: "error", Error: "boom", FailureID: "err-abc"},
	})
	if !ok {
		t.Fatal("a delegate update must project")
	}
	if item.FailureID != "err-abc" {
		t.Errorf("FailureID = %q, want the delegate's own", item.FailureID)
	}
	if item.Status != StatusError {
		t.Errorf("status = %q, want error", item.Status)
	}
	if item.Detail != "boom" {
		t.Errorf("detail = %q, want the error where there is no action or summary", item.Detail)
	}
}

func TestEveryProjectableEventKindProducesACategoryAndStatus(t *testing.T) {
	// A kind that projected with an empty category would be invisible to every
	// filtering frontend while still consuming a row of the bound.
	for _, e := range []event.Event{
		{Kind: event.KindSessionStart},
		{Kind: event.KindTurnStart},
		{Kind: event.KindTurnEnd},
		{Kind: event.KindToolStart, Tool: &event.Tool{Name: "read_file"}},
		{Kind: event.KindToolResult, Tool: &event.Tool{Name: "read_file"}},
		{Kind: event.KindToolResult, Tool: &event.Tool{Name: "read_file", IsError: true, Output: "no such file"}},
		{Kind: event.KindPermissionDecision, Permission: &event.Permission{Tool: "run_command"}},
		{Kind: event.KindPermissionDecision, Permission: &event.Permission{Tool: "run_command", Allowed: true}},
		{Kind: event.KindFileChange, File: &event.FileChange{Operation: "write", Path: "a.go"}},
		{Kind: event.KindPlanUpdate},
		{Kind: event.KindDelegateUpdate, Delegate: &event.DelegateStatus{Name: "r", Status: "running"}},
		{Kind: event.KindCompaction},
		{Kind: event.KindWarning},
		{Kind: event.KindError},
		{Kind: event.KindRunResult, Result: &event.RunResult{Status: "ok"}},
		{Kind: event.KindRunResult, Result: &event.RunResult{Status: "cancelled"}},
		{Kind: event.KindRunResult, Result: &event.RunResult{Status: "error", Error: "boom"}},
	} {
		item, ok := FromEvent(e)
		if !ok {
			t.Errorf("%s did not project", e.Kind)
			continue
		}
		name := fmt.Sprintf("%s", e.Kind)
		if item.Category == "" {
			t.Errorf("%s produced no category", name)
		}
		if item.Status == "" {
			t.Errorf("%s produced no status", name)
		}
		if item.Title == "" {
			t.Errorf("%s produced no title", name)
		}
	}
}

func TestRunResultStatusesAreDistinguished(t *testing.T) {
	// "cancelled" is a warning and anything else non-ok is an error. Collapsing
	// them would report a deliberate interruption as a failure.
	for status, want := range map[string]Status{
		"ok":        StatusSuccess,
		"cancelled": StatusWarning,
		"error":     StatusError,
	} {
		item, ok := FromEvent(event.Event{Kind: event.KindRunResult, Result: &event.RunResult{Status: status}})
		if !ok {
			t.Fatalf("run result %q did not project", status)
		}
		if item.Status != want {
			t.Errorf("run result %q → %q, want %q", status, item.Status, want)
		}
	}
}
