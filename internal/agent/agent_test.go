package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/failureid"
	"github.com/robert-mcdermott/collomia/internal/hooks"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/session"
	"github.com/robert-mcdermott/collomia/internal/skills"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

type fakeClient struct {
	calls int
	chat  func(int, provider.Request) (provider.Response, error)
}

type capabilityClient struct {
	calls        int
	capabilities provider.Capabilities
}

type streamingClient struct{}

func (streamingClient) Name() string { return "streaming-fixture" }
func (streamingClient) Chat(_ context.Context, _ provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
	usage := provider.Usage{InputTokens: 7, OutputTokens: 3}
	onDelta(provider.Delta{Reasoning: "checking"})
	onDelta(provider.Delta{ToolCall: &provider.ToolCallDelta{Index: 0, ID: "call_1", Name: "read_file"}})
	onDelta(provider.Delta{ToolCall: &provider.ToolCallDelta{Index: 0, Arguments: `{"path":"README.md"}`}})
	onDelta(provider.Delta{ToolCall: &provider.ToolCallDelta{Index: 0, ID: "call_1", Name: "read_file", Done: true}})
	onDelta(provider.Delta{Warning: "fixture warning"})
	onDelta(provider.Delta{Text: "finished"})
	onDelta(provider.Delta{Usage: &usage})
	return provider.Response{Content: "finished", Usage: usage}, nil
}

func (c *capabilityClient) Name() string { return "capability-fixture" }
func (c *capabilityClient) Capabilities() provider.Capabilities {
	return c.capabilities
}
func (c *capabilityClient) Chat(context.Context, provider.Request, func(provider.Delta)) (provider.Response, error) {
	c.calls++
	return provider.Response{Content: "network request should not happen"}, nil
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

func TestAgentRetainsAndResolvesTypedToolImages(t *testing.T) {
	image := append([]byte("\x89PNG\r\n\x1a\n"), []byte("fixture")...)
	registry := tools.NewRegistry(tools.Function{
		Def:    provider.ToolDefinition{Name: "external_chart", InputSchema: json.RawMessage(`{"type":"object"}`)},
		Action: tools.Action{Risk: tools.RiskRead, Summary: "read chart"},
		RunResult: func(context.Context, json.RawMessage, func(string)) (tools.Result, error) {
			return tools.Result{Content: "chart data", Parts: []provider.ContentPart{{Type: provider.ContentImage, Name: "chart.png", MediaType: "image/png", Data: image}}}, nil
		},
	})
	store, err := session.OpenAt(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.New("fixture", "vision")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	attachments := session.NewAttachmentManager()
	attachments.Use(sess)
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		if call == 1 {
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "chart-1", Name: "external_chart", Arguments: json.RawMessage(`{}`)}}}, nil
		}
		last := request.Messages[len(request.Messages)-1]
		if last.Role != "tool" || last.Content != "chart data" || len(last.Parts) != 1 || len(last.Parts[0].Data) == 0 || last.Parts[0].AttachmentID == "" {
			t.Fatalf("resolved tool result=%+v", last)
		}
		return provider.Response{Content: "finished"}, nil
	}}
	a := New(Options{Client: client, ProviderName: "fixture", Model: "vision", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), Attachments: attachments, OnMessage: sess.AppendMessage})
	if _, err := a.Run(t.Context(), "read it", nil); err != nil {
		t.Fatal(err)
	}
	messages := sess.TranscriptMessages()
	tool := messages[len(messages)-2]
	if tool.Role != "tool" || len(tool.Parts) != 1 || tool.Parts[0].AttachmentID == "" || len(tool.Parts[0].Data) != 0 {
		t.Fatalf("durable tool message=%+v", tool)
	}
}

func TestAgentRetainsUserImagesAfterPromptGate(t *testing.T) {
	image := append([]byte("\x89PNG\r\n\x1a\n"), []byte("user fixture")...)
	storeDir := t.TempDir()
	store, err := session.OpenAt(storeDir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.New("fixture", "vision")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	attachments := session.NewAttachmentManager()
	attachments.Use(sess)
	client := &fakeClient{chat: func(_ int, request provider.Request) (provider.Response, error) {
		user := request.Messages[0]
		if len(user.Parts) != 1 || len(user.Parts[0].Data) == 0 || user.Parts[0].AttachmentID == "" {
			t.Fatalf("provider user message=%+v", user)
		}
		return provider.Response{Content: "seen"}, nil
	}}
	a := New(Options{Client: client, ProviderName: "fixture", Model: "vision", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), Attachments: attachments, OnMessage: sess.AppendMessage})
	part := provider.ContentPart{Type: provider.ContentImage, Name: "screen.png", MediaType: "image/png", Size: len(image), Data: image}
	if _, err := a.RunWithParts(t.Context(), "inspect this", []provider.ContentPart{part}, nil); err != nil {
		t.Fatal(err)
	}
	persisted := sess.TranscriptMessages()[0].Parts[0]
	if persisted.AttachmentID == "" || len(persisted.Data) != 0 || persisted.SHA256 == "" {
		t.Fatalf("persisted user image=%+v", persisted)
	}
}

func TestAgentMapsNormalizedProviderDeltasWithoutDuplicatingUsage(t *testing.T) {
	a := New(Options{Client: streamingClient{}, ProviderName: "fixture", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil)})
	var reasoning, warning string
	var toolDeltas, usageEvents int
	result, err := a.Run(t.Context(), "do it", func(e event.Event) {
		switch e.Kind {
		case event.KindReasoningDelta:
			reasoning += e.Text
		case event.KindToolCallDelta:
			if e.ToolCall == nil {
				t.Fatal("tool-call delta missing payload")
			}
			toolDeltas++
		case event.KindWarning:
			warning += e.Text
		case event.KindUsage:
			usageEvents++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "finished" || reasoning != "checking" || warning != "fixture warning" || toolDeltas != 3 || usageEvents != 1 {
		t.Fatalf("result=%q reasoning=%q warning=%q toolDeltas=%d usageEvents=%d", result, reasoning, warning, toolDeltas, usageEvents)
	}
}

func TestAgentPreflightsUnsupportedToolCapability(t *testing.T) {
	registry := tools.NewRegistry(tools.Function{
		Def:    provider.ToolDefinition{Name: "inspect", InputSchema: json.RawMessage(`{"type":"object"}`)},
		Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect"},
		Run:    func(context.Context, json.RawMessage) (string, error) { return "observed", nil },
	})
	client := &capabilityClient{capabilities: provider.Capabilities{
		ProviderType: "fixture", Model: "text-only", Tools: provider.CapabilityUnsupported,
	}}
	a := New(Options{Client: client, ProviderName: "fixture", Model: "text-only", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil)})
	_, err := a.Run(t.Context(), "inspect it", nil)
	if err == nil || !strings.Contains(err.Error(), "capability preflight") {
		t.Fatalf("error=%v", err)
	}
	if client.calls != 0 {
		t.Fatalf("provider was called %d time(s)", client.calls)
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

func TestPinnedContextIsRefreshedForEveryRequest(t *testing.T) {
	pinned := "Active structured plan:\n[ ] 1. inspect"
	registry := tools.NewRegistry(tools.Function{
		Def:    provider.ToolDefinition{Name: "inspect", InputSchema: json.RawMessage(`{"type":"object"}`)},
		Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect"},
		Run: func(context.Context, json.RawMessage) (string, error) {
			pinned = "Active structured plan:\n[x] 1. inspect — verified"
			return "observed", nil
		},
	})
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		switch call {
		case 1:
			if !strings.Contains(request.System, "[ ] 1. inspect") {
				t.Fatalf("initial plan missing from system prompt: %s", request.System)
			}
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "1", Name: "inspect", Arguments: json.RawMessage(`{}`)}}}, nil
		default:
			if !strings.Contains(request.System, "[x] 1. inspect — verified") || strings.Contains(request.System, "[ ] 1. inspect") {
				t.Fatalf("updated plan was not refreshed: %s", request.System)
			}
			return provider.Response{Content: "done"}, nil
		}
	}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "m", ProviderConfig: appconfig.Provider{MaxTokens: 50}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), PinnedContext: func() string { return pinned }})
	if _, err := a.Run(t.Context(), "work the plan", nil); err != nil {
		t.Fatal(err)
	}
	if breakdown := a.ContextBreakdown(); breakdown.PinnedStateChars != len(pinned) {
		t.Fatalf("context breakdown=%+v pinned=%q", breakdown, pinned)
	}
}

func TestCompactionRetainsRecentFailureEvidenceVerbatim(t *testing.T) {
	const exactFailure = "Tool error: compile failed at fixture.go:17: undefined: Widget"
	client := &fakeClient{chat: func(_ int, _ provider.Request) (provider.Response, error) {
		return provider.Response{Content: "summary that accidentally omitted the failure"}, nil
	}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "m", ProviderConfig: appconfig.Provider{MaxTokens: 50}, Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil)})
	messages := []provider.Message{
		{Role: "user", Content: "build it"},
		{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "failed-1", Name: "run_command"}}},
		{Role: "tool", ToolCallID: "failed-1", Content: exactFailure},
		{Role: "assistant", Content: "I will investigate"},
	}
	for i := 0; i < 6; i++ {
		messages = append(messages, provider.Message{Role: "user", Content: fmt.Sprintf("follow-up %d", i)})
	}
	a.SetMessages(messages)
	if _, err := a.Compact(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	a.mu.RLock()
	active := append([]provider.Message(nil), a.messages...)
	a.mu.RUnlock()
	if len(active) == 0 || !strings.Contains(active[0].Content, exactFailure) || !strings.Contains(active[0].Content, "tool_call_id=failed-1") {
		t.Fatalf("failure evidence was not retained exactly: %+v", active)
	}
}

func TestFailureEvidenceMarksItsRetentionLimit(t *testing.T) {
	content := "Tool error: " + strings.Repeat("界", retainedFailureBytes)
	evidence := recentFailureEvidence([]provider.Message{{Role: "tool", ToolCallID: "large-failure", Content: content}})
	if !strings.Contains(evidence, retainedFailureTruncation) {
		t.Fatalf("bounded failure evidence has no truncation marker")
	}
	if len(evidence) > retainedFailureBytes+len("tool_call_id=large-failure\n") {
		t.Fatalf("failure evidence exceeded its bound: %d", len(evidence))
	}
	if !utf8.ValidString(evidence) {
		t.Fatal("failure evidence was clipped inside a UTF-8 sequence")
	}
}

func TestOversizedToolResultUsesSessionArtifactWithoutReplay(t *testing.T) {
	store, err := session.OpenAt(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.New("fixture", "model")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	artifacts := session.NewArtifactManager()
	artifacts.Use(sess)
	executions := 0
	large := strings.Repeat("0123456789", 300)
	registry := tools.NewRegistry(
		tools.Function{Def: provider.ToolDefinition{Name: "large_result", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskRead, Summary: "large result"}, Run: func(context.Context, json.RawMessage) (string, error) {
			executions++
			return large, nil
		}},
		session.ArtifactTool(artifacts),
	)
	artifactID := ""
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		switch call {
		case 1:
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "large", Name: "large_result", Arguments: json.RawMessage(`{}`)}}}, nil
		case 2:
			last := request.Messages[len(request.Messages)-1].Content
			marker := "session artifact "
			start := strings.Index(last, marker)
			if start < 0 {
				t.Fatalf("artifact reference missing from result: %q", last)
			}
			artifactID = strings.Fields(last[start+len(marker):])[0]
			args := fmt.Sprintf(`{"id":%q,"offset":1024,"limit":128}`, artifactID)
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "read", Name: "read_tool_result", Arguments: json.RawMessage(args)}}}, nil
		default:
			last := request.Messages[len(request.Messages)-1].Content
			if !strings.Contains(last, "begin untrusted tool output") || !strings.Contains(last, "next_offset=") {
				t.Fatalf("bounded artifact range missing: %q", last)
			}
			return provider.Response{Content: "done"}, nil
		}
	}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "m", ProviderConfig: appconfig.Provider{MaxTokens: 50}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxToolOutput: 1024, Artifacts: artifacts})
	if _, err := a.Run(t.Context(), "inspect all output", nil); err != nil {
		t.Fatal(err)
	}
	if executions != 1 || artifactID == "" {
		t.Fatalf("originating tool executions=%d artifact=%q", executions, artifactID)
	}
	if stats := artifacts.Stats(); stats.Count != 1 || stats.StoredBytes != len(large) {
		t.Fatalf("artifact stats=%+v", stats)
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

// concurrentClient counts how many Chat calls are in flight at once, so
// tests can assert the delegate scheduler actually parallelizes work.
type concurrentClient struct {
	mu      sync.Mutex
	current int
	maxSeen int
	chat    func(provider.Request) (provider.Response, error)
}

func decodeDelegateResults(t *testing.T, value string) struct {
	Tasks    []DelegateResult `json:"tasks"`
	Warnings []string         `json:"warnings"`
} {
	t.Helper()
	var decoded struct {
		Tasks    []DelegateResult `json:"tasks"`
		Warnings []string         `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		t.Fatalf("decode delegate result: %v\n%s", err, value)
	}
	return decoded
}

func (c *concurrentClient) Name() string { return "fake" }
func (c *concurrentClient) Chat(_ context.Context, request provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
	c.mu.Lock()
	c.current++
	if c.current > c.maxSeen {
		c.maxSeen = c.current
	}
	c.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	response, err := c.chat(request)
	c.mu.Lock()
	c.current--
	c.mu.Unlock()
	if response.Content != "" && onDelta != nil {
		onDelta(provider.Delta{Text: response.Content})
	}
	return response, err
}

func TestDelegateRunsTasksConcurrently(t *testing.T) {
	client := &concurrentClient{chat: func(provider.Request) (provider.Response, error) {
		return provider.Response{Content: "observed"}, nil
	}}
	registry := tools.NewRegistry()
	a := New(Options{Client: client, ProviderName: "fake", Model: "m", ProviderConfig: appconfig.Provider{MaxTokens: 50}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 4})
	team := NewTeam()
	a.AddDelegationTool(appconfig.Config{}, nil, team)
	args := json.RawMessage(`{"tasks":[{"name":"t1","task":"do 1"},{"name":"t2","task":"do 2"},{"name":"t3","task":"do 3"}]}`)
	result, err := registry.Execute(t.Context(), "delegate", args)
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodeDelegateResults(t, result)
	if len(decoded.Tasks) != 3 {
		t.Fatalf("tasks=%+v", decoded.Tasks)
	}
	for i, name := range []string{"t1", "t2", "t3"} {
		if decoded.Tasks[i].Name != name || decoded.Tasks[i].Status != DelegateDone {
			t.Fatalf("task %d=%+v", i, decoded.Tasks[i])
		}
	}
	client.mu.Lock()
	maxSeen := client.maxSeen
	client.mu.Unlock()
	if maxSeen < 2 {
		t.Fatalf("expected concurrent execution, max concurrent calls = %d", maxSeen)
	}
	snapshot := team.Snapshot()
	if len(snapshot) != 3 {
		t.Fatalf("team snapshot=%d", len(snapshot))
	}
	for _, s := range snapshot {
		if s.Status != "done" {
			t.Fatalf("task %s status=%s", s.Name, s.Status)
		}
	}
}

func TestDelegateWriteTaskUsesIsolatedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	workspace := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", workspace}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "initial")

	wrote := false
	client := &concurrentClient{chat: func(provider.Request) (provider.Response, error) {
		if !wrote {
			wrote = true
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "1", Name: "write_file", Arguments: json.RawMessage(`{"path":"new.txt","content":"from worktree\n"}`)}}}, nil
		}
		return provider.Response{Content: "wrote the file"}, nil
	}}
	registry, _, _, err := tools.Builtins(workspace, appconfig.Config{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := appconfig.Config{Permissions: appconfig.Permissions{Mode: "autopilot"}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "m", ProviderConfig: appconfig.Provider{MaxTokens: 50}, Workspace: workspace, Registry: registry, Permissions: permission.New(cfg.Permissions, nil), MaxIterations: 4})
	team := NewTeam()
	a.AddDelegationTool(cfg, nil, team)
	args := json.RawMessage(`{"tasks":[{"name":"writer","task":"add a file","write":true}]}`)
	result, err := registry.Execute(t.Context(), "delegate", args)
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodeDelegateResults(t, result)
	if len(decoded.Tasks) != 1 || len(decoded.Tasks[0].ChangedFiles) != 1 || decoded.Tasks[0].ChangedFiles[0] != "new.txt" {
		t.Fatalf("expected structured changed-file report: %s", result)
	}
	snapshot := team.Snapshot()
	if len(snapshot) != 1 || snapshot[0].Status != "done" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if snapshot[0].Worktree == "" {
		t.Fatal("expected worktree path recorded")
	}
	if _, err := os.Stat(filepath.Join(snapshot[0].Worktree, "new.txt")); err != nil {
		t.Fatalf("expected file in worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "new.txt")); err == nil {
		t.Fatal("write should not have landed in the parent workspace")
	}
	exec.Command("git", "-C", workspace, "worktree", "remove", "--force", snapshot[0].Worktree).Run()
	exec.Command("git", "-C", workspace, "branch", "-D", snapshot[0].Branch).Run()
}

func TestDelegateAgentProfileOverridesModelAndRole(t *testing.T) {
	var sawModel, sawSystem string
	client := &concurrentClient{chat: func(request provider.Request) (provider.Response, error) {
		sawModel = request.Model
		sawSystem = request.System
		return provider.Response{Content: "reviewed"}, nil
	}}
	registry := tools.NewRegistry()
	cfg := appconfig.Config{Agents: map[string]appconfig.AgentDefinition{
		"reviewer": {Model: "model-b", Instructions: "You review code for security issues.", MaxIterations: 3},
	}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "model-a", ProviderConfig: appconfig.Provider{MaxTokens: 50}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 4})
	team := NewTeam()
	a.AddDelegationTool(cfg, nil, team)
	args := json.RawMessage(`{"tasks":[{"name":"r1","task":"review the diff","agent":"reviewer"}]}`)
	result, err := registry.Execute(t.Context(), "delegate", args)
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodeDelegateResults(t, result)
	if len(decoded.Tasks) != 1 || decoded.Tasks[0].Name != "r1" || decoded.Tasks[0].Profile != "reviewer" {
		t.Fatalf("result=%s", result)
	}
	if sawModel != "model-b" {
		t.Fatalf("expected profile model override, got %q", sawModel)
	}
	if !strings.Contains(sawSystem, "You review code for security issues.") {
		t.Fatalf("expected role instructions in system prompt: %s", sawSystem)
	}
}

func TestDelegateUnknownAgentProfileErrors(t *testing.T) {
	client := &concurrentClient{chat: func(provider.Request) (provider.Response, error) {
		t.Fatal("chat should not be called for an unknown profile")
		return provider.Response{}, nil
	}}
	registry := tools.NewRegistry()
	a := New(Options{Client: client, ProviderName: "fake", Model: "m", ProviderConfig: appconfig.Provider{MaxTokens: 50}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 4})
	team := NewTeam()
	a.AddDelegationTool(appconfig.Config{}, nil, team)
	args := json.RawMessage(`{"tasks":[{"name":"x","task":"do it","agent":"missing"}]}`)
	result, err := registry.Execute(t.Context(), "delegate", args)
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodeDelegateResults(t, result)
	if len(decoded.Tasks) != 1 || decoded.Tasks[0].Status != DelegateError || !strings.Contains(decoded.Tasks[0].Error, `unknown agent profile "missing"`) {
		t.Fatalf("result=%s", result)
	}
}

func TestAgentProfilePermissionsOnlyTightenParent(t *testing.T) {
	parent := appconfig.Permissions{
		Mode: "workspace", DeniedTools: []string{"parent-denied"}, DeniedCommands: []string{"parent-command"},
		Rules: []appconfig.Rule{{Action: "allow", Tool: "read_file"}}, Sandbox: "require", SandboxAllowNetwork: true,
	}
	child := appconfig.AgentPermissions{
		Mode: "ask", DeniedTools: []string{"child-denied"}, DeniedCommands: []string{"child-command"},
		Rules: []appconfig.Rule{{Action: "deny", Server: "production-*"}},
	}
	effective := restrictAgentPermissions(parent, child)
	if effective.Mode != "ask" || effective.Sandbox != "require" || !effective.SandboxAllowNetwork {
		t.Fatalf("effective permissions widened or lost parent policy: %+v", effective)
	}
	if !slices.Equal(effective.DeniedTools, []string{"parent-denied", "child-denied"}) || !slices.Equal(effective.DeniedCommands, []string{"parent-command", "child-command"}) {
		t.Fatalf("denials are not additive: %+v", effective)
	}
	if len(effective.Rules) != 1 || effective.Rules[0].Action != "allow" {
		t.Fatalf("parent rule ordering must remain intact for independent child restriction evaluation: %+v", effective.Rules)
	}
	widenAttempt := restrictAgentPermissions(appconfig.Permissions{Mode: "ask"}, appconfig.AgentPermissions{Mode: "autopilot"})
	if widenAttempt.Mode != "ask" {
		t.Fatalf("child widened parent autonomy: %s", widenAttempt.Mode)
	}
}

func TestSubagentSystemPromptMatchesReadOrWriteMode(t *testing.T) {
	a := New(Options{Subagent: true, PlanMode: true, Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil)})
	readPrompt := a.systemPrompt(true)
	if !strings.Contains(readPrompt, "research sub-agent") || !strings.Contains(readPrompt, "do not attempt changes") {
		t.Fatalf("read-only subagent prompt=%q", readPrompt)
	}
	writePrompt := a.systemPrompt(false)
	if !strings.Contains(writePrompt, "implementation sub-agent") || strings.Contains(writePrompt, "do not attempt changes") {
		t.Fatalf("write subagent prompt=%q", writePrompt)
	}
}

func TestReadOnlyDelegateCannotMutateParentPlanArtifact(t *testing.T) {
	registry := tools.NewRegistry(tools.Function{
		Def:    provider.ToolDefinition{Name: "update_plan", InputSchema: json.RawMessage(`{"type":"object"}`)},
		Action: tools.Action{Risk: tools.RiskRead, Summary: "update parent plan"},
		Run: func(context.Context, json.RawMessage) (string, error) {
			t.Fatal("parent plan tool executed in child")
			return "", nil
		},
	})
	client := &fakeClient{chat: func(_ int, request provider.Request) (provider.Response, error) {
		for _, definition := range request.Tools {
			if definition.Name == "update_plan" {
				t.Fatal("parent plan tool was exposed to delegated child")
			}
		}
		return provider.Response{Content: "reported without changing parent plan"}, nil
	}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "m", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 4})
	a.AddDelegationTool(appconfig.Config{}, nil, NewTeam())
	if _, err := registry.Execute(t.Context(), "delegate", json.RawMessage(`{"tasks":[{"task":"review the plan"}]}`)); err != nil {
		t.Fatal(err)
	}
}

func TestReviewedIntegrationToolsArePrimaryOnly(t *testing.T) {
	for _, name := range []string{"delegate", "inspect_delegate_changes", "apply_delegate_changes"} {
		if !parentOnlyTool(name) {
			t.Fatalf("%s should be primary-only", name)
		}
	}
	if parentOnlyTool("read_file") {
		t.Fatal("ordinary workspace tools must remain available to delegated agents")
	}
}

func TestDelegateProfileFiltersSkillsAndEnforcesToolAllowlist(t *testing.T) {
	dangerRan := false
	registry := tools.NewRegistry(tools.Function{
		Def:    provider.ToolDefinition{Name: "danger", InputSchema: json.RawMessage(`{"type":"object"}`)},
		Action: tools.Action{Risk: tools.RiskRead, Summary: "dangerous read"},
		Run:    func(context.Context, json.RawMessage) (string, error) { dangerRan = true; return "ran", nil },
	})
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		if call == 1 {
			if !strings.Contains(request.System, "keep: allowed") || strings.Contains(request.System, "drop: hidden") {
				t.Fatalf("filtered skill catalog not reflected in system prompt:\n%s", request.System)
			}
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "1", Name: "danger", Arguments: json.RawMessage(`{}`)}}}, nil
		}
		last := request.Messages[len(request.Messages)-1]
		if !strings.Contains(last.Content, "not available to this agent") {
			t.Fatalf("hidden tool call was not blocked: %+v", last)
		}
		return provider.Response{Content: "finished safely"}, nil
	}}
	catalog := skills.Catalog{Skills: []skills.Skill{{Name: "drop", Description: "hidden"}, {Name: "keep", Description: "allowed"}}}
	cfg := appconfig.Config{Agents: map[string]appconfig.AgentDefinition{"reviewer": {Tools: []string{"load_skill"}, Skills: []string{"keep"}}}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "m", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), Catalog: catalog, MaxIterations: 4})
	team := NewTeam()
	a.AddDelegationTool(cfg, nil, team)
	result, err := registry.Execute(t.Context(), "delegate", json.RawMessage(`{"tasks":[{"name":"review","task":"review","agent":"reviewer"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodeDelegateResults(t, result)
	if dangerRan || len(decoded.Tasks) != 1 || decoded.Tasks[0].Status != DelegateDone {
		t.Fatalf("tool restriction failed: danger=%t result=%s", dangerRan, result)
	}
}

func TestDelegateTokenBudgetStopsBeforeAnotherProviderCall(t *testing.T) {
	registry := tools.NewRegistry(tools.Function{
		Def:    provider.ToolDefinition{Name: "inspect", InputSchema: json.RawMessage(`{"type":"object"}`)},
		Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect"},
		Run:    func(context.Context, json.RawMessage) (string, error) { return "evidence", nil },
	})
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		if call > 1 {
			t.Fatal("token budget should stop before a second provider request")
		}
		return provider.Response{Usage: provider.Usage{InputTokens: 6000, OutputTokens: 1000}, ToolCalls: []provider.ToolCall{{ID: "1", Name: "inspect", Arguments: json.RawMessage(`{}`)}}}, nil
	}}
	cfg := appconfig.Config{Agents: map[string]appconfig.AgentDefinition{"bounded": {TokenBudget: 10000}}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "m", ProviderConfig: appconfig.Provider{MaxTokens: 1000}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 4})
	team := NewTeam()
	a.AddDelegationTool(cfg, nil, team)
	result, err := registry.Execute(t.Context(), "delegate", json.RawMessage(`{"tasks":[{"name":"bounded","task":"inspect","agent":"bounded"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodeDelegateResults(t, result)
	if len(decoded.Tasks) != 1 || decoded.Tasks[0].Status != DelegateBudgetExhausted || decoded.Tasks[0].InputTokens != 6000 || decoded.Tasks[0].TokenBudget != 10000 {
		t.Fatalf("budget result=%s", result)
	}
}

func TestDelegateInboxCompactionKeepsValidStructuredResults(t *testing.T) {
	results := make([]DelegateResult, 6)
	for i := range results {
		results[i] = DelegateResult{ID: fmt.Sprintf("d%d", i), Name: strings.Repeat("name", 100), Status: DelegateDone, Summary: strings.Repeat("summary", 2000), Evidence: []string{strings.Repeat("evidence", 500)}, TimeoutSeconds: 600}
	}
	encoded, err := encodeDelegateInbox(results, []string{strings.Repeat("warning", 2000)}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 4096 {
		t.Fatalf("compacted inbox=%d bytes", len(encoded))
	}
	var decoded struct {
		Tasks []DelegateResult `json:"tasks"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("compacted inbox is not valid JSON: %v\n%s", err, encoded)
	}
	if len(decoded.Tasks) != 6 {
		t.Fatalf("compacted inbox lost task identities: %+v", decoded.Tasks)
	}
	for _, result := range decoded.Tasks {
		if !result.Truncated || result.ID == "" || result.Status != DelegateDone {
			t.Fatalf("compacted task lost required state: %+v", result)
		}
	}
}

func TestDelegateSchedulerLimitIsSharedAcrossCalls(t *testing.T) {
	client := &concurrentClient{chat: func(provider.Request) (provider.Response, error) { return provider.Response{Content: "done"}, nil }}
	registry := tools.NewRegistry()
	cfg := appconfig.Config{Options: appconfig.Options{DelegateMaxConcurrency: 2}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "m", ProviderConfig: appconfig.Provider{MaxTokens: 50}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 4})
	a.AddDelegationTool(cfg, nil, NewTeam())
	args := json.RawMessage(`{"tasks":[{"task":"one"},{"task":"two"},{"task":"three"}]}`)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := registry.Execute(t.Context(), "delegate", args); err != nil {
				t.Errorf("delegate: %v", err)
			}
		}()
	}
	wg.Wait()
	client.mu.Lock()
	maxSeen := client.maxSeen
	client.mu.Unlock()
	if maxSeen != 2 {
		t.Fatalf("shared scheduler max concurrency=%d, want 2", maxSeen)
	}
}

type cancellableClient struct{ started chan struct{} }

func (c *cancellableClient) Name() string { return "cancellable" }
func (c *cancellableClient) Chat(ctx context.Context, _ provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	select {
	case c.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return provider.Response{}, ctx.Err()
}

func TestDelegateTaskCanBeCancelledIndividually(t *testing.T) {
	client := &cancellableClient{started: make(chan struct{}, 1)}
	registry := tools.NewRegistry()
	a := New(Options{Client: client, ProviderName: "fake", Model: "m", ProviderConfig: appconfig.Provider{MaxTokens: 50}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 4})
	team := NewTeam()
	a.AddDelegationTool(appconfig.Config{}, nil, team)
	done := make(chan string, 1)
	go func() {
		result, _ := registry.Execute(t.Context(), "delegate", json.RawMessage(`{"tasks":[{"name":"cancel-me","task":"wait"}]}`))
		done <- result
	}()
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("delegated provider call did not start")
	}
	status := team.Snapshot()[0]
	if err := team.Stop(status.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		decoded := decodeDelegateResults(t, result)
		if len(decoded.Tasks) != 1 || decoded.Tasks[0].Status != DelegateCancelled {
			t.Fatalf("cancel result=%s", result)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled delegated task did not finish")
	}
}

func TestDelegateCancellationWhileWaitingForApproval(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	workspace := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", workspace}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "initial")

	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		if call == 1 {
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "write", Name: "write_file", Arguments: json.RawMessage(`{"path":"pending.txt","content":"must not land\n"}`)}}}, nil
		}
		return provider.Response{Content: "unexpected continuation"}, nil
	}}
	registry, _, processes, err := tools.Builtins(workspace, appconfig.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(processes.StopAll)
	approvalStarted := make(chan struct{})
	approver := func(ctx context.Context, _ permission.Request) (permission.Decision, error) {
		close(approvalStarted)
		<-ctx.Done()
		return permission.Decision{}, ctx.Err()
	}
	cfg := appconfig.Defaults()
	cfg.Permissions.Mode = "ask"
	a := New(Options{Client: client, ProviderName: "fixture", Model: "m", ProviderConfig: appconfig.Provider{MaxTokens: 50}, Workspace: workspace, Registry: registry, Permissions: permission.New(cfg.Permissions, approver), MaxIterations: 4})
	team := NewTeam()
	a.AddDelegationTool(cfg, approver, team)
	done := make(chan string, 1)
	go func() {
		result, _ := registry.Execute(t.Context(), "delegate", json.RawMessage(`{"tasks":[{"name":"approval-wait","task":"write only after approval","write":true}]}`))
		done <- result
	}()
	select {
	case <-approvalStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("delegated approval did not start")
	}
	status := team.Snapshot()[0]
	if status.Status != DelegateWaitingApproval {
		t.Fatalf("status before cancellation=%+v", status)
	}
	if err := team.Stop(status.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		decoded := decodeDelegateResults(t, result)
		if len(decoded.Tasks) != 1 || decoded.Tasks[0].Status != DelegateCancelled || !failureid.Valid(decoded.Tasks[0].FailureID) {
			t.Fatalf("cancelled approval result=%s", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("approval-waiting delegate did not stop")
	}
	if _, err := os.Stat(filepath.Join(workspace, "pending.txt")); !os.IsNotExist(err) {
		t.Fatalf("cancelled approval mutated the parent workspace: %v", err)
	}
}

func TestDelegateReportsSiblingConflicts(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	workspace := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", workspace}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "initial")

	client := &concurrentClient{chat: func(request provider.Request) (provider.Response, error) {
		if len(request.Messages) == 1 {
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "1", Name: "write_file", Arguments: json.RawMessage(`{"path":"shared.txt","content":"x"}`)}}}, nil
		}
		return provider.Response{Content: "done"}, nil
	}}
	registry, _, _, err := tools.Builtins(workspace, appconfig.Config{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := appconfig.Config{Permissions: appconfig.Permissions{Mode: "autopilot"}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "m", ProviderConfig: appconfig.Provider{MaxTokens: 50}, Workspace: workspace, Registry: registry, Permissions: permission.New(cfg.Permissions, nil), MaxIterations: 4})
	team := NewTeam()
	a.AddDelegationTool(cfg, nil, team)
	args := json.RawMessage(`{"tasks":[{"name":"t1","task":"add shared file","write":true},{"name":"t2","task":"also add shared file","write":true}]}`)
	result, err := registry.Execute(t.Context(), "delegate", args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "conflicting changes") || !strings.Contains(result, "shared.txt") {
		t.Fatalf("expected a conflict warning naming shared.txt: %s", result)
	}
	if !strings.Contains(result, "t1") || !strings.Contains(result, "t2") {
		t.Fatalf("expected both task names in the conflict warning: %s", result)
	}
	for _, s := range team.Snapshot() {
		if s.Worktree != "" {
			exec.Command("git", "-C", workspace, "worktree", "remove", "--force", s.Worktree).Run()
			exec.Command("git", "-C", workspace, "branch", "-D", s.Branch).Run()
		}
	}
}

func TestHunkConflictWarningDistinguishesOverlapAndDisjointChanges(t *testing.T) {
	names := []string{"first", "second"}
	changed := [][]string{{"shared.go"}, {"shared.go"}}
	overlap := hunkConflictWarning(names, changed, [][]DelegateHunk{
		{{Path: "shared.go", OldStart: 10, OldLines: 3}},
		{{Path: "shared.go", OldStart: 12, OldLines: 2}},
	})
	if !strings.Contains(overlap, "overlapping hunks") {
		t.Fatalf("overlap=%q", overlap)
	}
	disjoint := hunkConflictWarning(names, changed, [][]DelegateHunk{
		{{Path: "shared.go", OldStart: 10, OldLines: 2}},
		{{Path: "shared.go", OldStart: 30, OldLines: 2}},
	})
	if !strings.Contains(disjoint, "disjoint hunks") {
		t.Fatalf("disjoint=%q", disjoint)
	}
}

// TestExecuteToolAppliesContentOverride verifies that a hunk-review
// approval (permission.Decision.Content set) replaces write_file's
// proposed content before execution, not just approves the original call.
func TestExecuteToolAppliesContentOverride(t *testing.T) {
	override := "overridden content"
	var received string
	registry := tools.NewRegistry(tools.Function{
		Def:    provider.ToolDefinition{Name: "write_file"},
		Action: tools.Action{Risk: tools.RiskWrite, Summary: "write"},
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var a struct{ Content string }
			if err := json.Unmarshal(raw, &a); err != nil {
				return "", err
			}
			received = a.Content
			return "wrote", nil
		},
	})
	approver := func(context.Context, permission.Request) (permission.Decision, error) {
		return permission.Decision{Allow: true, Content: &override}, nil
	}
	a := New(Options{Client: &fakeClient{}, ProviderName: "fake", Model: "m", Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, approver)})
	call := provider.ToolCall{ID: "1", Name: "write_file", Arguments: json.RawMessage(`{"path":"x.txt","content":"original content"}`)}
	result, err := a.executeTool(t.Context(), call, false, func(event.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if received != override {
		t.Fatalf("tool received content=%q, want override %q (full result: %s)", received, override, result.Content)
	}
}

func TestPersistenceFailureAfterAssistantMessageBlocksToolExecution(t *testing.T) {
	persistenceErr := error(nil)
	executions := 0
	registry := tools.NewRegistry(tools.Function{
		Def:    provider.ToolDefinition{Name: "mutate"},
		Action: tools.Action{Risk: tools.RiskWrite, Summary: "mutate fixture"},
		Run: func(context.Context, json.RawMessage) (string, error) {
			executions++
			return "mutated", nil
		},
	})
	client := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
		return provider.Response{ToolCalls: []provider.ToolCall{{ID: "write-1", Name: "mutate", Arguments: json.RawMessage(`{}`)}}}, nil
	}}
	a := New(Options{
		Client: client, ProviderName: "fixture", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil),
		OnMessage: func(message provider.Message) {
			if message.Role == "assistant" {
				persistenceErr = errors.New("injected session disk failure")
			}
		},
		PersistenceError: func() error { return persistenceErr },
	})
	if _, err := a.Run(t.Context(), "make the change", nil); !errors.Is(err, ErrPersistenceUnavailable) {
		t.Fatalf("run error=%v", err)
	}
	if executions != 0 {
		t.Fatalf("tool executed %d times after persistence failed", executions)
	}
}

func TestPersistenceFailureAtToolStartBlocksExecution(t *testing.T) {
	persistenceErr := error(nil)
	executions := 0
	registry := tools.NewRegistry(tools.Function{
		Def:    provider.ToolDefinition{Name: "inspect"},
		Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect fixture"},
		Run: func(context.Context, json.RawMessage) (string, error) {
			executions++
			return "inspected", nil
		},
	})
	client := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
		return provider.Response{ToolCalls: []provider.ToolCall{{ID: "read-1", Name: "inspect", Arguments: json.RawMessage(`{}`)}}}, nil
	}}
	a := New(Options{
		Client: client, ProviderName: "fixture", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil),
		PersistenceError: func() error { return persistenceErr },
	})
	emit := func(e event.Event) {
		if e.Kind == event.KindToolStart {
			persistenceErr = errors.New("injected tool-start event failure")
		}
	}
	if _, err := a.Run(t.Context(), "inspect", emit); !errors.Is(err, ErrPersistenceUnavailable) {
		t.Fatalf("run error=%v", err)
	}
	if executions != 0 {
		t.Fatalf("tool executed %d times after its start event was not durable", executions)
	}
}

func TestPersistenceFailureAfterToolResultStopsRemainingCalls(t *testing.T) {
	persistenceErr := error(nil)
	executions := 0
	registry := tools.NewRegistry(tools.Function{
		Def:    provider.ToolDefinition{Name: "inspect"},
		Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect fixture"},
		Run: func(context.Context, json.RawMessage) (string, error) {
			executions++
			return "inspected", nil
		},
	})
	client := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
		return provider.Response{ToolCalls: []provider.ToolCall{
			{ID: "read-1", Name: "inspect", Arguments: json.RawMessage(`{}`)},
			{ID: "read-2", Name: "inspect", Arguments: json.RawMessage(`{}`)},
		}}, nil
	}}
	a := New(Options{
		Client: client, ProviderName: "fixture", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil),
		OnMessage: func(message provider.Message) {
			if message.Role == "tool" {
				persistenceErr = errors.New("injected tool-result record failure")
			}
		},
		PersistenceError: func() error { return persistenceErr },
	})
	if _, err := a.Run(t.Context(), "inspect twice", nil); !errors.Is(err, ErrPersistenceUnavailable) {
		t.Fatalf("run error=%v", err)
	}
	if executions != 1 {
		t.Fatalf("executions=%d, want exactly the first already-started tool", executions)
	}
}

func TestWithOverriddenContentRejectsUnsupportedTool(t *testing.T) {
	content := "x"
	if _, err := withOverriddenContent("edit_file", json.RawMessage(`{}`), content); err == nil {
		t.Fatal("expected an error for a tool that doesn't support content override")
	}
}

func TestWithOverriddenContentReplacesField(t *testing.T) {
	content := "new content"
	args, err := withOverriddenContent("write_file", json.RawMessage(`{"path":"a.txt","content":"old"}`), content)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct{ Path, Content string }
	if err := json.Unmarshal(args, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Path != "a.txt" || decoded.Content != content {
		t.Fatalf("decoded=%+v", decoded)
	}
}

func TestToolStartHookBlocksExecution(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("hook test script uses /bin/sh")
	}
	hookScript := filepath.Join(t.TempDir(), "gate.sh")
	if err := os.WriteFile(hookScript, []byte("#!/bin/sh\necho 'no destructive tools during the demo'\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	executed := false
	registry := tools.NewRegistry(tools.Function{Def: provider.ToolDefinition{Name: "danger", Description: "d", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskRead, Summary: "danger"}, Run: func(context.Context, json.RawMessage) (string, error) {
		executed = true
		return "ran", nil
	}})
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		if call == 1 {
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "1", Name: "danger", Arguments: json.RawMessage(`{}`)}}}, nil
		}
		last := request.Messages[len(request.Messages)-1]
		if !strings.Contains(last.Content, "Tool blocked by hook") || !strings.Contains(last.Content, "no destructive tools") {
			t.Fatalf("model should see the block reason, got %q", last.Content)
		}
		return provider.Response{Content: "understood"}, nil
	}}
	lifecycle := hooks.NewRunner(t.TempDir(), map[string][]appconfig.Hook{"tool_start": {{Command: hookScript, Matcher: "^danger$"}}}, nil)
	a := New(Options{Client: client, ProviderName: "fake", Model: "m", Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 4, Hooks: lifecycle})
	if _, err := a.Run(t.Context(), "try it", nil); err != nil {
		t.Fatal(err)
	}
	if executed {
		t.Fatal("blocked tool must not execute")
	}
}

func TestUserPromptHookBlocksTurn(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("hook test script uses /bin/sh")
	}
	hookScript := filepath.Join(t.TempDir(), "gate.sh")
	if err := os.WriteFile(hookScript, []byte("#!/bin/sh\necho '{\"decision\":\"block\",\"reason\":\"prompts are frozen\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
		t.Fatal("provider must not be called for a blocked prompt")
		return provider.Response{}, nil
	}}
	workspace := t.TempDir()
	storeDir := t.TempDir()
	store, err := session.OpenAt(storeDir, workspace)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.New("fake", "m")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	attachments := session.NewAttachmentManager()
	attachments.Use(sess)
	lifecycle := hooks.NewRunner(workspace, map[string][]appconfig.Hook{"user_prompt": {{Command: hookScript}}}, nil)
	a := New(Options{Client: client, ProviderName: "fake", Model: "m", Workspace: workspace, Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), Hooks: lifecycle, Attachments: attachments, OnMessage: sess.AppendMessage})
	image := fixtureImageForAgentTest()
	_, err = a.RunWithParts(t.Context(), "hello", []provider.ContentPart{{Type: provider.ContentImage, Name: "blocked.png", MediaType: "image/png", Size: len(image), Data: image}}, nil)
	if err == nil || !strings.Contains(err.Error(), "prompts are frozen") {
		t.Fatalf("expected prompt block, got %v", err)
	}
	if a.MessageCount() != 0 {
		t.Fatalf("blocked prompt must not enter the conversation, got %d messages", a.MessageCount())
	}
	attachmentDir := filepath.Join(storeDir, sess.Meta.ID+".attachments")
	if _, statErr := os.Stat(attachmentDir); !os.IsNotExist(statErr) {
		t.Fatalf("blocked prompt retained attachment data: %v", statErr)
	}
}

func fixtureImageForAgentTest() []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), []byte("agent fixture")...)
}

func TestProviderErrorEventIncludesMachineReadableClassification(t *testing.T) {
	err := &provider.Error{
		Provider: "openrouter/glm", Operation: "chat", Kind: provider.ErrorRateLimit,
		StatusCode: 429, Retryable: true, RetryAfter: 3 * time.Second, RequestID: "req-123",
		Message: "rate limited",
	}
	tracked := failureid.Ensure(err)
	e := errorEvent(tracked)
	if e.Provider == nil {
		t.Fatal("provider classification missing from error event")
	}
	if e.Provider.Name != "openrouter/glm" || e.Provider.Operation != "chat" || e.Provider.Kind != "rate_limit" || e.Provider.StatusCode != 429 || !e.Provider.Retryable || e.Provider.RetryAfterMS != 3000 || e.Provider.RequestID != "req-123" {
		t.Fatalf("provider failure=%+v", e.Provider)
	}
	if e.FailureID == "" || e.FailureID != failureid.ID(tracked) {
		t.Fatalf("failure correlation missing: event=%q error=%q", e.FailureID, failureid.ID(tracked))
	}
}

func TestRunReturnsSameFailureIDPublishedByErrorEvent(t *testing.T) {
	client := &fakeClient{chat: func(_ int, _ provider.Request) (provider.Response, error) {
		return provider.Response{}, errors.New("fixture provider failed")
	}}
	a := New(Options{
		Client: client, ProviderName: "fixture", Model: "m",
		ProviderConfig: appconfig.Provider{MaxTokens: 32}, Workspace: t.TempDir(),
		Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil),
	})
	var published string
	_, err := a.Run(t.Context(), "hello", func(e event.Event) {
		if e.Kind == event.KindError {
			published = e.FailureID
		}
	})
	if err == nil || !failureid.Valid(published) || failureid.ID(err) != published {
		t.Fatalf("error=%v returned_id=%q published_id=%q", err, failureid.ID(err), published)
	}
}

func TestSteeringBecomesExplicitBoundaryMessageWithoutPermissionGrant(t *testing.T) {
	client := &fakeClient{chat: func(_ int, request provider.Request) (provider.Response, error) {
		if len(request.Messages) != 2 {
			t.Fatalf("messages=%+v", request.Messages)
		}
		last := request.Messages[1]
		if last.Role != "user" || !strings.Contains(last.Content, "Parent steering update") || !strings.Contains(last.Content, "does not grant permissions") || !strings.Contains(last.Content, "focus on tests") {
			t.Fatalf("steering message=%+v", last)
		}
		return provider.Response{Content: "done"}, nil
	}}
	delivered := false
	agent := New(Options{
		Client: client, ProviderName: "fake", Model: "m", Workspace: t.TempDir(),
		Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil),
		TakeSteering: func() []string {
			if delivered {
				return nil
			}
			delivered = true
			return []string{"focus on tests"}
		},
	})
	if _, err := agent.Run(t.Context(), "review", nil); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePlanAssignmentRequiresKnownStepAndCompletedDependencies(t *testing.T) {
	board := plan.NewBoard()
	if err := board.Set(plan.Plan{Goal: "ship", Steps: []plan.Step{{ID: 1, Title: "inspect", Status: "pending"}, {ID: 2, Title: "implement", Status: "pending", DependsOn: []int{1}}}}); err != nil {
		t.Fatal(err)
	}
	if err := validatePlanAssignment(board, 9); err == nil || !strings.Contains(err.Error(), "unknown plan step") {
		t.Fatalf("unknown step error=%v", err)
	}
	if err := validatePlanAssignment(board, 2); err == nil || !strings.Contains(err.Error(), "unfinished step 1") {
		t.Fatalf("dependency error=%v", err)
	}
	if err := board.Set(plan.Plan{Goal: "ship", Steps: []plan.Step{{ID: 1, Title: "inspect", Status: "done"}, {ID: 2, Title: "implement", Status: "pending", DependsOn: []int{1}}}}); err != nil {
		t.Fatal(err)
	}
	if err := validatePlanAssignment(board, 2); err != nil {
		t.Fatal(err)
	}
}
