package agent

import (
	"context"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

type thinkingClient struct{}

func (thinkingClient) Name() string { return "thinking-fixture" }
func (thinkingClient) Chat(_ context.Context, _ provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
	onDelta(provider.Delta{Reasoning: "first, "})
	onDelta(provider.Delta{Reasoning: "then the answer"})
	onDelta(provider.Delta{Text: "done"})
	return provider.Response{Content: "done"}, nil
}

func TestAssistantMessagesKeepTheirReadableThinking(t *testing.T) {
	var appended []provider.Message
	a := New(Options{Client: thinkingClient{}, ProviderName: "fake", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 2,
		OnMessage: func(m provider.Message) { appended = append(appended, m) }})
	if _, err := a.Run(t.Context(), "hello", func(event.Event) {}); err != nil {
		t.Fatal(err)
	}
	var assistant *provider.Message
	for i := range appended {
		if appended[i].Role == "assistant" {
			assistant = &appended[i]
		}
	}
	if assistant == nil || assistant.Reasoning != "first, then the answer" || assistant.Content != "done" {
		t.Fatalf("assistant = %+v", assistant)
	}
}

func TestBoundedTextStopsAtTheCapWithoutSplittingARune(t *testing.T) {
	var b boundedText
	b.add(strings.Repeat("a", maxRetainedReasoning-1))
	b.add("é more")
	got := b.String()
	if !strings.HasSuffix(got, "[Thinking summary truncated at 64 KiB.]") {
		t.Error("truncation must be stated")
	}
	if strings.ContainsRune(got, '�') || strings.Contains(got, "é") {
		t.Error("a multi-byte rune straddling the cap must be dropped whole")
	}
}
