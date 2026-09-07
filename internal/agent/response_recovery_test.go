package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/taskmode"
)

func TestEmptyResponsesBoundedAndAccounted(t *testing.T) {
	for _, budget := range []int{2, 10} {
		t.Run(string(rune('0'+budget)), func(t *testing.T) {
			a, c, _ := scopedFixture(t, taskmode.Developer)
			a.completionPlan, a.completionStore = c.board, c.store
			a.maxTurnIterations = budget
			client := &fakeClient{chat: func(_ int, request provider.Request) (provider.Response, error) {
				for _, message := range request.Messages {
					if strings.Contains(message.Content, "[Runtime response status]") {
						t.Fatal("retry polluted prompt with a failed turn")
					}
				}
				return provider.Response{Stop: "stop", Usage: provider.Usage{InputTokens: 25, OutputTokens: 1}}, nil
			}}
			a.client = client
			_, err := a.Run(t.Context(), "Answer the question", nil)
			if budget == 2 && !errors.Is(err, ErrIterationBudgetExceeded) {
				t.Fatalf("retry escaped iteration budget: %v", err)
			}
			if budget == 10 && (!errors.Is(err, provider.ErrResponseEmpty) || !strings.Contains(err.Error(), "after 3 empty responses")) {
				t.Fatalf("missing provider diagnosis: %v", err)
			}
			if client.calls != min(budget, 3) || a.Usage().InputTokens != 25*client.calls || a.Usage().OutputTokens != client.calls {
				t.Fatalf("accounting: calls=%d usage=%+v", client.calls, a.Usage())
			}
		})
	}
}

type incompleteToolStream struct{ calls int }

func (c *incompleteToolStream) Name() string { return "incomplete-stream" }
func (c *incompleteToolStream) Chat(_ context.Context, _ provider.Request, emit func(provider.Delta)) (provider.Response, error) {
	c.calls++
	emit(provider.Delta{ToolCall: &provider.ToolCallDelta{Index: 0, Name: "write_file", Arguments: "{"}})
	return provider.Response{Stop: "stop"}, nil
}
func TestEmptyResponseWithToolFragmentsIsNotRetried(t *testing.T) {
	a, c, _ := scopedFixture(t, taskmode.Developer)
	a.completionPlan, a.completionStore = c.board, c.store
	client := &incompleteToolStream{}
	a.client = client
	_, err := a.Run(t.Context(), "Write a file", nil)
	if err == nil || client.calls != 1 {
		t.Fatalf("partial tool payload was retried: calls=%d err=%v", client.calls, err)
	}
}

func TestLiveExecutionLimitChange(t *testing.T) {
	a, c, _ := scopedFixture(t, taskmode.Developer)
	a.completionPlan, a.completionStore = c.board, c.store
	if err := a.SetExecutionLimits(24, 1); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		if call == 1 {
			return provider.Response{Stop: "stop"}, nil
		}
		return provider.Response{Content: "Answer"}, nil
	}}
	a.client = client
	answer, err := a.Run(t.Context(), "Answer", func(e event.Event) {
		if e.Kind == event.KindWarning {
			if err := a.SetExecutionLimits(24, 4); err != nil {
				t.Fatal(err)
			}
		}
	})
	if err != nil || answer != "Answer" || client.calls != 2 {
		t.Fatalf("live limit was not observed: answer=%q err=%v calls=%d", answer, err, client.calls)
	}
}
