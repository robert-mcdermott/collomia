package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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

// concurrentClient counts how many Chat calls are in flight at once, so
// tests can assert the delegate scheduler actually parallelizes work.
type concurrentClient struct {
	mu      sync.Mutex
	current int
	maxSeen int
	chat    func(provider.Request) (provider.Response, error)
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
	for _, name := range []string{"[t1]", "[t2]", "[t3]"} {
		if !strings.Contains(result, name) {
			t.Fatalf("missing %s in result: %s", name, result)
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
	registry, _, err := tools.Builtins(workspace, appconfig.Config{})
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
	if !strings.Contains(result, "changed 1 file(s)") {
		t.Fatalf("expected changed-file report: %s", result)
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
	if !strings.Contains(result, "[r1]") {
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
	if !strings.Contains(result, `unknown agent profile "missing"`) {
		t.Fatalf("result=%s", result)
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
	registry, _, err := tools.Builtins(workspace, appconfig.Config{})
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
