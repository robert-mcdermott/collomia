// Package eval contains credential-free product evaluations. Unlike narrow
// unit tests, these scenarios drive the real agent, permission manager, and
// built-in tool registry through representative repository tasks. Scripted
// providers make the expected decisions deterministic and keep CI offline.
package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/agent"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/session"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

type scriptedStep struct {
	check    func(provider.Request) error
	response provider.Response
}

type scriptedProvider struct {
	t     *testing.T
	steps []scriptedStep
	next  int
}

func (p *scriptedProvider) Name() string { return "offline-evaluation" }

func (p *scriptedProvider) Chat(_ context.Context, request provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
	if p.next >= len(p.steps) {
		return provider.Response{}, fmt.Errorf("evaluation provider received unexpected request %d", p.next+1)
	}
	step := p.steps[p.next]
	p.next++
	if step.check != nil {
		if err := step.check(request); err != nil {
			p.t.Errorf("provider request %d: %v", p.next, err)
			return provider.Response{}, err
		}
	}
	if step.response.Content != "" && onDelta != nil {
		onDelta(provider.Delta{Text: step.response.Content})
	}
	return step.response, nil
}

func TestRepositoryInspectionEvaluation(t *testing.T) {
	workspace := t.TempDir()
	mustWrite(t, filepath.Join(workspace, "main.go"), "package main\n\nconst Answer = 42\n")
	client := &scriptedProvider{t: t, steps: []scriptedStep{
		{response: toolResponse("search", "search_files", `{"pattern":"Answer","path":".","file_glob":"*.go"}`)},
		{check: requireLastToolContains("main.go:3", "Answer = 42"), response: toolResponse("read", "read_file", `{"path":"main.go"}`)},
		{check: requireLastToolContains("const Answer = 42"), response: provider.Response{Content: "Answer is defined in main.go at line 3."}},
	}}
	agentRuntime, tracker := newEvaluationAgent(t, workspace, client, "ask")
	var events []event.Event
	answer, err := agentRuntime.Run(t.Context(), "Find where Answer is defined and report its value.", func(e event.Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if answer != "Answer is defined in main.go at line 3." || client.next != len(client.steps) {
		t.Fatalf("answer=%q provider steps=%d/%d", answer, client.next, len(client.steps))
	}
	if len(tracker.Changed()) != 0 {
		t.Fatalf("read-only evaluation changed files: %v", tracker.Changed())
	}
	if countKind(events, event.KindToolStart) != 2 || deniedDecisions(events) != 0 {
		t.Fatalf("unexpected event lifecycle: starts=%d denied=%d", countKind(events, event.KindToolStart), deniedDecisions(events))
	}
}

func TestBugFixAndVerificationEvaluation(t *testing.T) {
	workspace := t.TempDir()
	mustWrite(t, filepath.Join(workspace, "go.mod"), "module fixture\n\ngo 1.26.0\n")
	mustWrite(t, filepath.Join(workspace, "calc.go"), "package fixture\n\nfunc Add(a, b int) int { return a - b }\n")
	mustWrite(t, filepath.Join(workspace, "calc_test.go"), "package fixture\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(2, 3) != 5 { t.Fatal(\"bad sum\") } }\n")
	client := &scriptedProvider{t: t, steps: []scriptedStep{
		{response: toolResponse("edit", "edit_file", `{"path":"calc.go","old_text":"return a - b","new_text":"return a + b"}`)},
		{check: requireLastToolContains("edited"), response: toolResponse("detect", "detect_verification", `{}`)},
		{check: requireLastToolContains("go test ./..."), response: toolResponse("test", "run_command", `{"command":"go test ./...","timeout_seconds":60}`)},
		{check: requireLastToolContains("ok"), response: provider.Response{Content: "Fixed Add and verified the package tests pass."}},
	}}
	agentRuntime, tracker := newEvaluationAgent(t, workspace, client, "autopilot")
	var events []event.Event
	answer, err := agentRuntime.Run(t.Context(), "Fix Add and verify the repository.", func(e event.Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if answer != "Fixed Add and verified the package tests pass." {
		t.Fatalf("answer=%q", answer)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "calc.go"))
	if err != nil || !strings.Contains(string(data), "return a + b") {
		t.Fatalf("fix missing: data=%q err=%v", data, err)
	}
	if len(tracker.Changed()) != 1 || countKind(events, event.KindToolStart) != 3 || deniedDecisions(events) != 0 {
		t.Fatalf("changed=%v starts=%d denied=%d", tracker.Changed(), countKind(events, event.KindToolStart), deniedDecisions(events))
	}
}

func TestPermissionRefusalEvaluation(t *testing.T) {
	workspace := t.TempDir()
	client := &scriptedProvider{t: t, steps: []scriptedStep{
		{response: toolResponse("command", "run_command", `{"command":"go version"}`)},
		{check: requireLastToolContains("Tool denied", "requires interactive approval"), response: provider.Response{Content: "The command was not run because approval was unavailable."}},
	}}
	agentRuntime, tracker := newEvaluationAgent(t, workspace, client, "ask")
	var events []event.Event
	answer, err := agentRuntime.Run(t.Context(), "Run go version.", func(e event.Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(answer, "not run") || len(tracker.Changed()) != 0 {
		t.Fatalf("answer=%q changed=%v", answer, tracker.Changed())
	}
	if deniedDecisions(events) != 1 || countKind(events, event.KindToolStart) != 0 {
		t.Fatalf("denied=%d starts=%d", deniedDecisions(events), countKind(events, event.KindToolStart))
	}
}

func TestInterruptedMutationRecoveryEvaluation(t *testing.T) {
	workspace := t.TempDir()
	store, err := session.OpenAt(t.TempDir(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.New("fixture", "model")
	if err != nil {
		t.Fatal(err)
	}
	first.AppendMessage(provider.Message{Role: "user", Content: "change config.go"})
	first.AppendMessage(provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "write-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"config.go","content":"changed"}`)}}})
	id := first.Meta.ID
	first.Close()

	recovered, err := store.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	messages := recovered.Active()
	if len(messages) != 3 || messages[2].Role != "tool" || messages[2].ToolCallID != "write-1" || !strings.Contains(messages[2].Content, "may or may not have taken effect") {
		t.Fatalf("recovered messages=%+v", messages)
	}
	if _, err := os.Stat(filepath.Join(workspace, "config.go")); err == nil {
		t.Fatal("loading an interrupted call unexpectedly executed the mutation")
	}
}

func newEvaluationAgent(t *testing.T, workspace string, client provider.Client, mode string) (*agent.Agent, interface{ Changed() []string }) {
	t.Helper()
	cfg := appconfig.Defaults()
	cfg.Permissions.Mode = mode
	registry, tracker, processes, err := tools.Builtins(workspace, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(processes.StopAll)
	runtime := agent.New(agent.Options{
		Client: client, ProviderName: "offline-evaluation", Model: "scripted",
		ProviderConfig: appconfig.Provider{Type: "fixture", MaxTokens: 256, Context: 16_000},
		Workspace:      workspace, Registry: registry, Permissions: permission.New(cfg.Permissions, nil),
		MaxIterations: 8, MaxToolOutput: cfg.Options.MaxToolOutputBytes,
	})
	return runtime, tracker
}

func toolResponse(id, name, arguments string) provider.Response {
	return provider.Response{ToolCalls: []provider.ToolCall{{ID: id, Name: name, Arguments: json.RawMessage(arguments)}}}
}

func requireLastToolContains(values ...string) func(provider.Request) error {
	return func(request provider.Request) error {
		if len(request.Messages) == 0 {
			return fmt.Errorf("request has no messages")
		}
		last := request.Messages[len(request.Messages)-1]
		if last.Role != "tool" {
			return fmt.Errorf("last message role=%q, want tool", last.Role)
		}
		for _, value := range values {
			if !strings.Contains(last.Content, value) {
				return fmt.Errorf("tool result missing %q: %q", value, last.Content)
			}
		}
		return nil
	}
}

func countKind(events []event.Event, kind event.Kind) int {
	count := 0
	for _, item := range events {
		if item.Kind == kind {
			count++
		}
	}
	return count
}

func deniedDecisions(events []event.Event) int {
	count := 0
	for _, item := range events {
		if item.Kind == event.KindPermissionDecision && item.Permission != nil && !item.Permission.Allowed {
			count++
		}
	}
	return count
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
