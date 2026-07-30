package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

func TestSteeringQueueDrainsExactlyOnce(t *testing.T) {
	q := NewSteeringQueue()
	for _, guidance := range []string{"check the parser", "then run the tests"} {
		if err := q.Add(guidance); err != nil {
			t.Fatal(err)
		}
	}
	if q.Pending() != 2 {
		t.Fatalf("pending=%d", q.Pending())
	}
	taken := q.Take()
	if len(taken) != 2 || taken[0] != "check the parser" || taken[1] != "then run the tests" {
		t.Fatalf("taken=%v", taken)
	}
	// A second drain must return nothing. Re-delivering guidance would append
	// it to the conversation on every remaining iteration of the same turn.
	if again := q.Take(); len(again) != 0 || q.Pending() != 0 {
		t.Fatalf("second drain=%v pending=%d", again, q.Pending())
	}
}

func TestSteeringQueueRefusesRatherThanDropping(t *testing.T) {
	q := NewSteeringQueue()
	if err := q.Add("   "); err == nil {
		t.Fatal("empty guidance was accepted")
	}
	if err := q.Add(strings.Repeat("x", maxSteeringTextLength+1)); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("oversize guidance error=%v", err)
	}
	for i := 0; i < maxPendingSteering; i++ {
		if err := q.Add("guidance"); err != nil {
			t.Fatal(err)
		}
	}
	// Guidance the user believes was delivered, and was not, is the failure
	// this refusal exists to prevent.
	if err := q.Add("one too many"); err != ErrSteeringFull {
		t.Fatalf("full queue error=%v", err)
	}
	if q.Pending() != maxPendingSteering {
		t.Fatalf("pending=%d", q.Pending())
	}
}

func TestSteeringQueueClearDiscardsAndReportsDepth(t *testing.T) {
	q := NewSteeringQueue()
	var depths []int
	q.Observe(func(n int) { depths = append(depths, n) })
	if err := q.Add("guidance"); err != nil {
		t.Fatal(err)
	}
	q.Clear()
	if q.Pending() != 0 {
		t.Fatalf("clear left %d pending", q.Pending())
	}
	if len(depths) != 2 || depths[0] != 1 || depths[1] != 0 {
		t.Fatalf("observed depths=%v", depths)
	}
	// Clearing an empty queue is silent; a notice with nothing behind it
	// would be noise at the end of every turn.
	q.Clear()
	if len(depths) != 2 {
		t.Fatalf("empty clear notified: %v", depths)
	}
}

func TestSteeringQueueIsSafeUnderConcurrentAddAndTake(t *testing.T) {
	q := NewSteeringQueue()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = q.Add("guidance") }()
		go func() { defer wg.Done(); q.Take() }()
	}
	wg.Wait()
}

// TestPrimarySteeringLandsBetweenIterationsNotMidTool is the behavior the
// whole feature rests on: guidance typed while a tool is running reaches the
// model at the next iteration boundary, after that tool's result, where the
// conversation is consistent — not underneath the in-flight call.
func TestPrimarySteeringLandsBetweenIterationsNotMidTool(t *testing.T) {
	q := NewSteeringQueue()
	registry := tools.NewRegistry(tools.Function{
		Def:    provider.ToolDefinition{Name: "inspect", InputSchema: json.RawMessage(`{"type":"object"}`)},
		Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect"},
		Run: func(context.Context, json.RawMessage) (string, error) {
			// The user types while this tool is still running.
			if err := q.Add("actually check the parser first"); err != nil {
				t.Error(err)
			}
			return "inspected", nil
		},
	})
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		switch call {
		case 1:
			// Nothing is queued yet, so the first request must be untouched.
			for _, m := range request.Messages {
				if strings.Contains(m.Content, "steering update") {
					t.Fatalf("steering appeared before it was sent: %+v", request.Messages)
				}
			}
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "1", Name: "inspect", Arguments: json.RawMessage(`{}`)}}}, nil
		default:
			last := request.Messages[len(request.Messages)-1]
			if last.Role != "user" || !strings.Contains(last.Content, "User steering update") || !strings.Contains(last.Content, "actually check the parser first") {
				t.Fatalf("steering did not land after the tool result: %+v", request.Messages)
			}
			// It must sit after the tool result, not displace it.
			if prior := request.Messages[len(request.Messages)-2]; prior.Role != "tool" || prior.Content != "inspected" {
				t.Fatalf("tool result was disturbed: %+v", prior)
			}
			return provider.Response{Content: "done"}, nil
		}
	}}
	a := New(Options{
		Client: client, ProviderName: "fake", Model: "m",
		ProviderConfig: appconfig.Provider{MaxTokens: 50}, Workspace: t.TempDir(),
		Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil),
		TakeSteering: q.Take,
	})
	if _, err := a.Run(t.Context(), "review the code", nil); err != nil {
		t.Fatal(err)
	}
	// Delivered guidance must not linger and be re-applied to a later turn.
	if q.Pending() != 0 {
		t.Fatalf("guidance was not drained: pending=%d", q.Pending())
	}
}
