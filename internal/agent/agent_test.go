package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

type fakeClient struct {
	calls int
	chat  func(int, provider.Request) (provider.Response, error)
}

func (f *fakeClient) Name() string { return "fake" }
func (f *fakeClient) Chat(_ context.Context, request provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
	f.calls++
	response, err := f.chat(f.calls, request)
	if response.Content != "" && onDelta != nil {
		onDelta(provider.Delta{Text: response.Content})
	}
	return response, err
}

func TestAgentRunsToolLoop(t *testing.T) {
	registry := tools.NewRegistry(tools.Function{Def: provider.ToolDefinition{Name: "inspect", Description: "inspect", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect"}, Run: func(context.Context, json.RawMessage) (string, error) { return "observed", nil }})
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		if call == 1 {
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "1", Name: "inspect", Arguments: json.RawMessage(`{}`)}}}, nil
		}
		last := request.Messages[len(request.Messages)-1]
		if last.Role != "tool" || last.Content != "observed" {
			t.Fatalf("unexpected tool result: %+v", last)
		}
		return provider.Response{Content: "finished"}, nil
	}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 4})
	var delta string
	result, err := a.Run(t.Context(), "do it", func(e event.Event) {
		if e.Kind == event.KindTextDelta {
			delta += e.Text
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "finished" || delta != "finished" || client.calls != 2 {
		t.Fatalf("result=%q delta=%q calls=%d", result, delta, client.calls)
	}
}

// TestAutomaticCompactionCompletesLongTask forces a tiny context window and
// verifies the loop compacts (summarizing older messages) instead of
// overflowing, while the task still completes.
func TestAutomaticCompactionCompletesLongTask(t *testing.T) {
	long := strings.Repeat("tool output line\n", 40)
	registry := tools.NewRegistry(tools.Function{Def: provider.ToolDefinition{Name: "inspect", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect"}, Run: func(context.Context, json.RawMessage) (string, error) { return long, nil }})
	summarized := 0
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		if len(request.Tools) == 0 {
			// The compaction summarization request carries no tools.
			summarized++
			return provider.Response{Content: "summary of earlier work"}, nil
		}
		if call < 8 {
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: fmt.Sprintf("c%d", call), Name: "inspect", Arguments: json.RawMessage(`{}`)}}}, nil
		}
		return provider.Response{Content: "done"}, nil
	}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "m", ProviderConfig: appconfig.Provider{MaxTokens: 50, Context: 600}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 20, OnCompaction: func(summary provider.Message, replaced int) {
		if replaced <= 0 || !strings.Contains(summary.Content, "summary") {
			t.Errorf("bad compaction notification: replaced=%d summary=%q", replaced, summary.Content)
		}
	}})
	var kinds []event.Kind
	result, err := a.Run(t.Context(), "inspect everything", func(e event.Event) { kinds = append(kinds, e.Kind) })
	if err != nil {
		t.Fatal(err)
	}
	if result != "done" {
		t.Fatalf("result=%q", result)
	}
	if summarized == 0 {
		t.Fatal("expected at least one automatic compaction")
	}
	if !slices.Contains(kinds, event.KindCompaction) {
		t.Fatalf("compaction event missing from %v", kinds)
	}
}

func TestManualCompactUsesFocus(t *testing.T) {
	client := &fakeClient{chat: func(_ int, request provider.Request) (provider.Response, error) {
		if !strings.Contains(request.Messages[0].Content, "the database schema") {
			t.Errorf("focus missing from summarization prompt")
		}
		return provider.Response{Content: "focused summary"}, nil
	}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "m", ProviderConfig: appconfig.Provider{MaxTokens: 50}, Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil)})
	var seed []provider.Message
	for i := 0; i < 10; i++ {
		seed = append(seed, provider.Message{Role: "user", Content: fmt.Sprintf("message %d", i)})
	}
	a.SetMessages(seed)
	replaced, err := a.Compact(t.Context(), "the database schema")
	if err != nil {
		t.Fatal(err)
	}
	if replaced != 4 {
		t.Fatalf("replaced=%d", replaced)
	}
	if a.MessageCount() != 7 {
		t.Fatalf("messages=%d", a.MessageCount())
	}
}

func TestPlanModeExposesOnlyReadTools(t *testing.T) {
	registry := tools.NewRegistry(
		tools.Function{Def: provider.ToolDefinition{Name: "read_file"}, Action: tools.Action{Risk: tools.RiskRead}, Run: func(context.Context, json.RawMessage) (string, error) { return "", nil }},
		tools.Function{Def: provider.ToolDefinition{Name: "write_file"}, Action: tools.Action{Risk: tools.RiskWrite}, Run: func(context.Context, json.RawMessage) (string, error) { return "", nil }},
	)
	client := &fakeClient{chat: func(_ int, request provider.Request) (provider.Response, error) {
		var names []string
		for _, def := range request.Tools {
			names = append(names, def.Name)
		}
		if !slices.Contains(names, "read_file") || slices.Contains(names, "write_file") {
			t.Fatalf("plan tools=%v", names)
		}
		return provider.Response{Content: "plan"}, nil
	}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), PlanMode: true})
	if _, err := a.Run(t.Context(), "plan", nil); err != nil {
		t.Fatal(err)
	}
}
