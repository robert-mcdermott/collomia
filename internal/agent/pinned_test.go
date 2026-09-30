package agent

import (
	"context"
	"encoding/json"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// bindingClient calls one tool and then answers, reporting the continuity
// capability it is constructed with, and keeps every request it received.
type bindingClient struct {
	continuity provider.CapabilityState
	requests   [][]provider.Message
}

func (c *bindingClient) Name() string { return "binding-fixture" }
func (c *bindingClient) Capabilities() provider.Capabilities {
	return provider.Capabilities{ReasoningContinuity: c.continuity}
}
func (c *bindingClient) Chat(_ context.Context, in provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	c.requests = append(c.requests, append([]provider.Message(nil), in.Messages...))
	if len(c.requests) == 1 {
		return provider.Response{ToolCalls: []provider.ToolCall{{ID: "t1", Name: "inspect", Arguments: json.RawMessage(`{}`)}}, Stop: "tool_use"}, nil
	}
	return provider.Response{Content: "done"}, nil
}

func runWithPlan(t *testing.T, continuity provider.CapabilityState, plans ...string) (*bindingClient, []provider.Message) {
	t.Helper()
	client := &bindingClient{continuity: continuity}
	registry := tools.NewRegistry(tools.Function{Def: provider.ToolDefinition{Name: "inspect", Description: "inspect", InputSchema: json.RawMessage(`{"type":"object"}`)},
		Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect"}, Run: func(context.Context, json.RawMessage) (string, error) { return "observed", nil }})
	var appended []provider.Message
	a := New(Options{Client: client, ProviderName: "fake", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 4,
		PinnedContext: func() string { return plans[min(len(client.requests), len(plans)-1)] },
		OnMessage:     func(m provider.Message) { appended = append(appended, m) }})
	if _, err := a.Run(t.Context(), "go", func(event.Event) {}); err != nil {
		t.Fatal(err)
	}
	return client, appended
}

func pinnedCount(messages []provider.Message) int {
	n := 0
	for _, m := range messages {
		if m.Pinned {
			n++
		}
	}
	return n
}

func TestBindingProvidersRecordPlanStateOncePerChange(t *testing.T) {
	// For a provider that binds signed reasoning to the conversation before
	// it, every request must repeat exactly what came before. The plan state
	// is therefore recorded into the conversation, once per change.
	client, appended := runWithPlan(t, provider.CapabilitySupported, "plan v1")
	if got := pinnedCount(appended); got != 1 {
		t.Fatalf("an unchanged plan is recorded once, got %d", got)
	}
	first, second := client.requests[0], client.requests[1]
	for i := range first {
		if first[i].Content != second[i].Content || first[i].Role != second[i].Role {
			t.Fatalf("request 2 must begin with request 1 exactly; differs at %d", i)
		}
	}
	for _, m := range second {
		if m.Volatile {
			t.Error("no per-request copy may be sent to a binding provider")
		}
	}

	_, changed := runWithPlan(t, provider.CapabilitySupported, "plan v1", "plan v2")
	if got := pinnedCount(changed); got != 2 {
		t.Errorf("a changed plan is recorded again, got %d", got)
	}
}

func TestOtherProvidersKeepThePerRequestPlanState(t *testing.T) {
	client, appended := runWithPlan(t, provider.CapabilityUnsupported, "plan v1")
	if pinnedCount(appended) != 0 {
		t.Error("nothing is recorded for providers that do not bind reasoning")
	}
	for _, request := range client.requests {
		last := request[len(request)-1]
		if !last.Volatile || last.Content == "" {
			t.Errorf("the plan state stays the last, per-request message: %+v", last)
		}
	}
}

func TestRecordedPlanStateIsLeftOutForOtherProviders(t *testing.T) {
	a := New(Options{Client: &bindingClient{continuity: provider.CapabilityUnsupported}, ProviderName: "fake", Model: "model",
		Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil)})
	messages := a.withPinnedState([]provider.Message{{Role: "user", Content: "go"}, {Role: "user", Content: "old plan", Pinned: true}})
	if pinnedCount(messages) != 0 {
		t.Error("copies recorded under a binding provider must not reach one that does not bind")
	}
}
