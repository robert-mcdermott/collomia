package eval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robert-mcdermott/collomia/internal/agent"
	"github.com/robert-mcdermott/collomia/internal/app"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

type automaticReadGraphEvaluationClient struct {
	mu           sync.Mutex
	ready        chan struct{}
	readyOnce    sync.Once
	activeReads  int
	maxReads     int
	arrivedReads int
	primaryCalls int
}

type cancellingReadGraphEvaluationClient struct {
	mu         sync.Mutex
	started    int
	allStarted chan struct{}
	once       sync.Once
}

func (c *cancellingReadGraphEvaluationClient) Name() string {
	return "cancelling-read-graph-evaluation"
}

func (c *cancellingReadGraphEvaluationClient) Chat(ctx context.Context, request provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	if !requestHasText(request, "Investigate one approved Orchestrated Goal node") {
		return provider.Response{}, errors.New("primary lane started during cancellation evaluation")
	}
	if err := requireAutomaticReadSurface(request); err != nil {
		return provider.Response{}, err
	}
	c.mu.Lock()
	c.started++
	if c.started == 2 {
		c.once.Do(func() { close(c.allStarted) })
	}
	c.mu.Unlock()
	<-ctx.Done()
	return provider.Response{}, ctx.Err()
}

func (c *automaticReadGraphEvaluationClient) Name() string { return "automatic-read-graph-evaluation" }

func (c *automaticReadGraphEvaluationClient) Chat(ctx context.Context, request provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	nodeID := 0
	if requestHasText(request, "Investigate one approved Orchestrated Goal node") {
		switch {
		case requestHasText(request, "Node 1: inspect API behavior"):
			nodeID = 1
		case requestHasText(request, "Node 2: inspect test expectation"):
			nodeID = 2
		default:
			return provider.Response{}, errors.New("automatic read prompt did not identify an approved node")
		}
	}
	if nodeID != 0 {
		if err := requireAutomaticReadSurface(request); err != nil {
			return provider.Response{}, err
		}
		if !requestHasToolResult(request) {
			c.mu.Lock()
			c.activeReads++
			c.arrivedReads++
			if c.activeReads > c.maxReads {
				c.maxReads = c.activeReads
			}
			if c.arrivedReads == 2 {
				c.readyOnce.Do(func() { close(c.ready) })
			}
			c.mu.Unlock()
			select {
			case <-c.ready:
			case <-ctx.Done():
				c.mu.Lock()
				c.activeReads--
				c.mu.Unlock()
				return provider.Response{}, ctx.Err()
			}
			c.mu.Lock()
			c.activeReads--
			c.mu.Unlock()
			path := "calc.go"
			if nodeID == 2 {
				path = "calc_test.go"
			}
			return provider.Response{
				ToolCalls: []provider.ToolCall{{ID: "read-evidence", Name: "read_file", Arguments: json.RawMessage(`{"path":"` + path + `"}`)}},
				Usage:     provider.Usage{InputTokens: 11, OutputTokens: 2},
			}, nil
		}
		if nodeID == 1 {
			return provider.Response{Content: "calc.go shows Add currently subtracts b from a.", Usage: provider.Usage{InputTokens: 13, OutputTokens: 4}}, nil
		}
		return provider.Response{Content: "calc_test.go requires Add(2, 3) to equal 5.", Usage: provider.Usage{InputTokens: 13, OutputTokens: 4}}, nil
	}

	c.mu.Lock()
	c.primaryCalls++
	primaryCall := c.primaryCalls
	readsFinished := c.arrivedReads == 2 && c.activeReads == 0
	c.mu.Unlock()
	if !readsFinished {
		return provider.Response{}, errors.New("primary lane started before the automatic read wave finished")
	}
	if primaryCall == 1 {
		if !requestHasText(request, "calc.go shows Add currently subtracts") || !requestHasText(request, "calc_test.go requires Add(2, 3) to equal 5") {
			return provider.Response{}, errors.New("primary lane did not receive both bounded read summaries")
		}
		return provider.Response{
			ToolCalls: []provider.ToolCall{{ID: "primary-read", Name: "read_file", Arguments: json.RawMessage(`{"path":"calc.go"}`)}},
			Usage:     provider.Usage{InputTokens: 17, OutputTokens: 2},
		}, nil
	}
	if primaryCall == 2 && requestHasToolResult(request) {
		return provider.Response{Content: "Both investigations are grounded and the primary synthesis is complete.", Usage: provider.Usage{InputTokens: 19, OutputTokens: 6}}, nil
	}
	return provider.Response{}, errors.New("unexpected primary request sequence")
}

func requireAutomaticReadSurface(request provider.Request) error {
	names := map[string]bool{}
	for _, definition := range request.Tools {
		names[definition.Name] = true
	}
	if !names["read_file"] {
		return errors.New("automatic read worker has no repository read tool")
	}
	for _, forbidden := range []string{"write_file", "edit_file", "run_command", "delegate", "update_plan", "revise_goal_graph", "block_goal_node"} {
		if names[forbidden] {
			return errors.New("automatic read worker exposed forbidden tool " + forbidden)
		}
	}
	return nil
}

func requestHasToolResult(request provider.Request) bool {
	for _, message := range request.Messages {
		if message.Role == "tool" {
			return true
		}
	}
	return false
}

// TestOrchestratedGoalAutomaticReadFanoutEvaluation proves the OG-2B1
// product boundary: the runtime, not the model, selects two dependency-ready
// approved read nodes, runs them concurrently through the existing governed
// delegate boundary, ingests grounded fresh results, and only then advances
// the one serial primary lane.
func TestOrchestratedGoalAutomaticReadFanoutEvaluation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configDir := filepath.Join(home, ".collomia")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mustWriteEvaluationFile(t, filepath.Join(configDir, "config.json"), `{"default_provider":"fixture","providers":{"fixture":{"type":"openai-compatible","base_url":"http://127.0.0.1:1/v1","model":"scripted","context_window":16000,"max_tokens":256}}}`)
	workspace := orchestratedGitFixture(t)
	t.Setenv("GOCACHE", filepath.Join(workspace, ".collomia-eval-cache"))
	approved := &plan.Plan{Goal: "synthesize two repository facts", Steps: []plan.Step{
		{ID: 1, Title: "inspect API behavior", Status: "pending", Execution: "read_only", Acceptance: []string{"implementation evidence is grounded"}},
		{ID: 2, Title: "inspect test expectation", Status: "pending", Execution: "read_only", Acceptance: []string{"test evidence is grounded"}},
		{ID: 3, Title: "synthesize findings", Status: "pending", Execution: "primary", DependsOn: []int{1, 2}, Acceptance: []string{"both findings are reconciled"}},
	}}
	runtime, err := app.New(t.Context(), app.Options{Workspace: workspace, Autonomy: "autopilot", OrchestratedGoal: approved})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	client := &automaticReadGraphEvaluationClient{ready: make(chan struct{})}
	runtime.Agent.SetProvider("offline-evaluation", "scripted", appconfig.Provider{Type: "fixture", MaxTokens: 256, Context: 16_000}, client)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var events []event.Event
	answer, err := runtime.Agent.Run(ctx, "Synthesize the approved findings.", func(e event.Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if answer != "Both investigations are grounded and the primary synthesis is complete." {
		t.Fatalf("answer=%q", answer)
	}
	client.mu.Lock()
	maxReads, primaryCalls := client.maxReads, client.primaryCalls
	client.mu.Unlock()
	if maxReads != 2 || primaryCalls != 2 {
		t.Fatalf("max concurrent reads=%d primary calls=%d", maxReads, primaryCalls)
	}
	snapshot := runtime.GoalGraph.Snapshot()
	if snapshot.Outcome != goalgraph.OutcomeDone || snapshot.ReadFanout.Starts != 2 || snapshot.ReadFanout.UsedTokens != 60 {
		t.Fatalf("graph outcome/read envelope=%q %+v", snapshot.Outcome, snapshot.ReadFanout)
	}
	delegatedEvidence := 0
	for _, evidence := range snapshot.Evidence {
		if evidence.Kind == goalgraph.EvidenceDelegateRead && evidence.Status == "accepted" && evidence.WorkspaceToken != "" {
			delegatedEvidence++
		}
	}
	if delegatedEvidence != 2 {
		t.Fatalf("delegated read evidence=%+v", snapshot.Evidence)
	}
	statuses := runtime.Team.Snapshot()
	if len(statuses) != 2 {
		t.Fatalf("automatic worker statuses=%+v", statuses)
	}
	for _, status := range statuses {
		if status.Status != agent.DelegateDone || status.Write || status.PlanStep < 1 || status.PlanStep > 2 || status.Usage.InputTokens == 0 || len(status.Evidence) == 0 {
			t.Fatalf("automatic worker status=%+v", status)
		}
	}
	foundReason := false
	for _, emitted := range events {
		foundReason = foundReason || emitted.GoalGraph != nil && emitted.GoalGraph.State == "delegated_read" && strings.Contains(emitted.GoalGraph.Reason, "dependency-ready")
	}
	if !foundReason {
		t.Fatalf("scheduler reason was not observable: %+v", events)
	}
}

func TestOrchestratedGoalAutomaticReadCancellationEvaluation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configDir := filepath.Join(home, ".collomia")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mustWriteEvaluationFile(t, filepath.Join(configDir, "config.json"), `{"default_provider":"fixture","providers":{"fixture":{"type":"openai-compatible","base_url":"http://127.0.0.1:1/v1","model":"scripted","context_window":16000,"max_tokens":256}}}`)
	workspace := orchestratedGitFixture(t)
	approved := &plan.Plan{Goal: "cancel a read wave", Steps: []plan.Step{
		{ID: 1, Title: "inspect API behavior", Status: "pending", Execution: "read_only", Acceptance: []string{"implementation evidence is grounded"}},
		{ID: 2, Title: "inspect test expectation", Status: "pending", Execution: "read_only", Acceptance: []string{"test evidence is grounded"}},
		{ID: 3, Title: "synthesize findings", Status: "pending", DependsOn: []int{1, 2}, Acceptance: []string{"both findings are reconciled"}},
	}}
	runtime, err := app.New(t.Context(), app.Options{Workspace: workspace, Autonomy: "autopilot", OrchestratedGoal: approved})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	client := &cancellingReadGraphEvaluationClient{allStarted: make(chan struct{})}
	runtime.Agent.SetProvider("offline-evaluation", "scripted", appconfig.Provider{Type: "fixture", MaxTokens: 256, Context: 16_000}, client)

	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, runErr := runtime.Agent.Run(ctx, "Start the approved work.", nil)
		result <- runErr
	}()
	select {
	case <-client.allStarted:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("automatic read workers did not both start")
	}
	cancel()
	select {
	case runErr := <-result:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("run error=%v", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled automatic read wave did not stop")
	}
	if outcome, _ := runtime.GoalGraph.Outcome(); outcome != goalgraph.OutcomeCancelled {
		t.Fatalf("cancelled graph outcome=%q snapshot=%+v", outcome, runtime.GoalGraph.Snapshot())
	}
	statuses := runtime.Team.Snapshot()
	if len(statuses) != 2 {
		t.Fatalf("cancelled worker statuses=%+v", statuses)
	}
	for _, status := range statuses {
		if status.Status != agent.DelegateCancelled {
			t.Fatalf("cancelled worker status=%+v", status)
		}
	}
}

// TestOrchestratedGoalExplicitPreviewEvaluation exercises the OG-2A product
// boundary: a real application runtime stays Standard until a user starts a
// read-only proposal and explicitly approves the fresh plan. Only then does
// the same primary agent receive runtime-owned graph scheduling.
func TestOrchestratedGoalExplicitPreviewEvaluation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configDir := filepath.Join(home, ".collomia")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mustWriteEvaluationFile(t, filepath.Join(configDir, "config.json"), `{"default_provider":"fixture","providers":{"fixture":{"type":"openai-compatible","base_url":"http://127.0.0.1:1/v1","model":"scripted","context_window":16000,"max_tokens":256}}}`)
	workspace := orchestratedGitFixture(t)
	client := &scriptedProvider{t: t, steps: []scriptedStep{
		{check: func(request provider.Request) error {
			if !strings.Contains(request.System, "planning mode") {
				return errors.New("proposal did not use the read-only planning system prompt")
			}
			names := map[string]bool{}
			for _, definition := range request.Tools {
				names[definition.Name] = true
			}
			if !names["update_plan"] || names["write_file"] {
				return errors.New("proposal tool surface was not read-only")
			}
			return nil
		}, response: toolResponse("plan", "update_plan", `{"goal":"inspect the implementation","steps":[{"id":1,"title":"inspect current behavior","status":"pending","acceptance":["the implementation is reported from repository evidence"]}]}`)},
		{check: requireGraphToolContains("acceptance: the implementation is reported"), response: provider.Response{Content: "The proposal is ready for explicit review."}},
		{check: requireGraphNode(1, "inspect current behavior"), response: toolResponse("read", "read_file", `{"path":"calc.go"}`)},
		{check: requireGraphToolContains("func Add"), response: provider.Response{Content: "The implementation is grounded in calc.go."}},
	}}
	runtime, err := app.New(t.Context(), app.Options{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.Agent.SetProvider("offline-evaluation", "scripted", appconfig.Provider{Type: "fixture", MaxTokens: 256, Context: 16_000}, client)

	proposalPrompt, err := runtime.BeginOrchestratedProposal("inspect the implementation")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GoalGraph != nil {
		t.Fatal("proposal action activated a graph before approval")
	}
	if _, err := runtime.Agent.Run(t.Context(), proposalPrompt, runtime.LogEvent); err != nil {
		t.Fatal(err)
	}
	status, executionPrompt, err := runtime.ApproveOrchestratedGoal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GoalGraph == nil || !strings.Contains(status, "one serial primary lane") {
		t.Fatalf("approval did not attach the visible primary graph: %s", status)
	}
	answer, err := runtime.Agent.Run(t.Context(), executionPrompt, runtime.LogEvent)
	if err != nil {
		t.Fatal(err)
	}
	if answer != "The implementation is grounded in calc.go." || client.next != len(client.steps) {
		t.Fatalf("answer=%q provider steps=%d/%d", answer, client.next, len(client.steps))
	}
	if outcome, _ := runtime.GoalGraph.Outcome(); outcome != goalgraph.OutcomeDone {
		t.Fatalf("explicit preview outcome=%q", outcome)
	}
}

// TestOrchestratedGoalPrimaryGraphEvaluation exercises OG-1 as a product
// slice: one primary agent follows dependency readiness, changes the real
// combined workspace through the ordinary permission/tool path, and cannot
// complete until a fresh real verification is bound to that workspace.
func TestOrchestratedGoalPrimaryGraphEvaluation(t *testing.T) {
	workspace := orchestratedGitFixture(t)
	client := &scriptedProvider{t: t, steps: []scriptedStep{
		{check: requireGraphNode(1, "inspect current behavior"), response: toolResponse("read", "read_file", `{"path":"calc.go"}`)},
		{check: requireGraphToolContains("return a - b"), response: provider.Response{Content: "inspection complete"}},
		{check: requireGraphNode(2, "repair and verify"), response: toolResponse("edit", "edit_file", `{"path":"calc.go","old_text":"return a - b","new_text":"return a + b"}`)},
		{check: requireGraphToolContains("edited"), response: toolResponse("test", "run_command", `{"command":"go test ./...","timeout_seconds":300}`)},
		{check: requireGraphToolContains("ok"), response: provider.Response{Content: "repair verified"}},
	}}
	runtime, graph, tracker := newOrchestratedEvaluationAgent(t, workspace, client, "autopilot", goalgraph.Spec{
		Goal: "repair Add",
		Nodes: []goalgraph.NodeSpec{
			{ID: 1, Title: "inspect current behavior"},
			{ID: 2, Title: "repair and verify", DependsOn: []int{1}},
		},
	})
	var events []event.Event
	answer, err := runtime.Run(t.Context(), "Repair Add and prove it works.", func(e event.Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if answer != "repair verified" || client.next != len(client.steps) {
		t.Fatalf("answer=%q provider steps=%d/%d", answer, client.next, len(client.steps))
	}
	data, err := os.ReadFile(filepath.Join(workspace, "calc.go"))
	if err != nil || !strings.Contains(string(data), "return a + b") {
		t.Fatalf("repair missing: data=%q err=%v", data, err)
	}
	if outcome, _ := graph.Outcome(); outcome != goalgraph.OutcomeDone {
		t.Fatalf("graph outcome=%q snapshot=%+v", outcome, graph.Snapshot())
	}
	if len(tracker.Changed()) != 1 || countKind(events, event.KindGoalGraphUpdate) == 0 || countKind(events, event.KindDelegateUpdate) != 0 {
		t.Fatalf("changed=%v graph_updates=%d delegate_updates=%d", tracker.Changed(), countKind(events, event.KindGoalGraphUpdate), countKind(events, event.KindDelegateUpdate))
	}
	verification := false
	for _, evidence := range graph.Snapshot().Evidence {
		verification = verification || evidence.Kind == goalgraph.EvidenceVerification && evidence.Status == "passed" && evidence.WorkspaceToken != ""
	}
	if !verification {
		t.Fatalf("combined-workspace verification missing: %+v", graph.Snapshot().Evidence)
	}
}

// A failed tool cannot be washed away by final prose. The controller records
// the failure, spends one fresh attempt, and accepts only the successful path.
func TestOrchestratedGoalRecoverableFailureEvaluation(t *testing.T) {
	workspace := orchestratedGitFixture(t)
	client := &scriptedProvider{t: t, steps: []scriptedStep{
		{response: toolResponse("missing", "read_file", `{"path":"missing.go"}`)},
		{check: requireGraphToolContains("Tool error"), response: provider.Response{Content: "the first read failed"}},
		{check: func(request provider.Request) error {
			if !requestHasText(request, "fresh bounded attempt") {
				return errors.New("fresh-attempt controller notice is absent")
			}
			return nil
		}, response: toolResponse("read", "read_file", `{"path":"calc.go"}`)},
		{check: requireGraphToolContains("func Add"), response: provider.Response{Content: "recovered with grounded evidence"}},
	}}
	runtime, graph, _ := newOrchestratedEvaluationAgent(t, workspace, client, "ask", goalgraph.Spec{Goal: "inspect source", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "inspect source"}}})
	answer, err := runtime.Run(t.Context(), "Inspect the implementation.", nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := graph.Snapshot()
	if answer != "recovered with grounded evidence" || snapshot.Outcome != goalgraph.OutcomeDone || len(snapshot.Attempts) != 2 {
		t.Fatalf("answer=%q snapshot=%+v", answer, snapshot)
	}
	if snapshot.Attempts[0].State != goalgraph.AttemptRetryable || snapshot.Attempts[1].State != goalgraph.AttemptAccepted {
		t.Fatalf("attempts=%+v", snapshot.Attempts)
	}
}

func TestOrchestratedGoalPermissionDenialEvaluation(t *testing.T) {
	workspace := orchestratedGitFixture(t)
	client := &scriptedProvider{t: t, steps: []scriptedStep{
		{response: toolResponse("write", "write_file", `{"path":"denied.txt","content":"must not exist"}`)},
		{check: requireGraphToolContains("Tool denied"), response: provider.Response{Content: "permission prevented the change"}},
	}}
	runtime, graph, tracker := newOrchestratedEvaluationAgent(t, workspace, client, "ask", goalgraph.Spec{Goal: "write file", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "write denied file"}}})
	_, err := runtime.Run(t.Context(), "Create denied.txt.", nil)
	if !errors.Is(err, agent.ErrGoalBlocked) {
		t.Fatalf("error=%v", err)
	}
	if outcome, reason := graph.Outcome(); outcome != goalgraph.OutcomeBlocked || !strings.Contains(reason, "permission denied") {
		t.Fatalf("outcome=%q reason=%q", outcome, reason)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, "denied.txt")); !os.IsNotExist(statErr) || len(tracker.Changed()) != 0 {
		t.Fatalf("denied mutation reached workspace: stat=%v changed=%v", statErr, tracker.Changed())
	}
}

func orchestratedGitFixture(t *testing.T) string {
	t.Helper()
	workspace := t.TempDir()
	runEvalGit(t, workspace, "init", "-b", "main")
	runEvalGit(t, workspace, "config", "core.autocrlf", "false")
	mustWriteEvaluationFile(t, filepath.Join(workspace, ".gitignore"), ".collomia-eval-cache/\n")
	mustWriteEvaluationFile(t, filepath.Join(workspace, "go.mod"), "module orchestratedfixture\n\ngo 1.26.0\n")
	mustWriteEvaluationFile(t, filepath.Join(workspace, "calc.go"), "package orchestratedfixture\n\nfunc Add(a, b int) int { return a - b }\n")
	mustWriteEvaluationFile(t, filepath.Join(workspace, "calc_test.go"), "package orchestratedfixture\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(2, 3) != 5 { t.Fatal(\"bad sum\") } }\n")
	runEvalGit(t, workspace, "add", ".")
	runEvalGit(t, workspace, "commit", "-m", "base")
	return workspace
}

func newOrchestratedEvaluationAgent(t *testing.T, workspace string, client provider.Client, mode string, spec goalgraph.Spec) (*agent.Agent, *goalgraph.Graph, interface{ Changed() []string }) {
	t.Helper()
	t.Setenv("GOCACHE", filepath.Join(workspace, ".collomia-eval-cache"))
	cfg := appconfig.Defaults()
	cfg.Permissions.Mode = mode
	cfg.Permissions.Sandbox = evaluationSandboxMode()
	cfg.Permissions.CommandEnv = "minimal"
	cfg.Permissions.SandboxReadableRoots = append(cfg.Permissions.SandboxReadableRoots, evaluationSandboxReadableRoots()...)
	registry, tracker, processes, err := tools.Builtins(workspace, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(processes.StopAll)
	graph, err := goalgraph.New(spec, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	registry.Add(goalgraph.RevisionTool{Graph: graph})
	registry.Add(goalgraph.BlockTool{Graph: graph})
	runtime := agent.New(agent.Options{
		Client: client, ProviderName: "offline-evaluation", Model: "scripted",
		ProviderConfig: appconfig.Provider{Type: "fixture", MaxTokens: 256, Context: 16_000},
		Workspace:      workspace, Registry: registry, Permissions: permission.New(cfg.Permissions, nil),
		MaxIterations: 10, MaxToolOutput: cfg.Options.MaxToolOutputBytes,
		GoalGraph: graph, GoalStateToken: func(ctx context.Context) (string, error) { return goalgraph.WorkspaceStateToken(ctx, workspace) },
	})
	return runtime, graph, tracker
}

func requireGraphNode(id int, title string) func(provider.Request) error {
	return func(request provider.Request) error {
		needle := "[~] " + string(rune('0'+id)) + ". " + title + " · running"
		if !requestHasText(request, needle) {
			return errors.New("active graph node is absent: " + needle)
		}
		for _, definition := range request.Tools {
			if definition.Name == "delegate" || definition.Name == "update_plan" {
				return errors.New("primary-only graph exposed " + definition.Name)
			}
		}
		return nil
	}
}

func requestHasText(request provider.Request, needle string) bool {
	for _, message := range request.Messages {
		if strings.Contains(message.Content, needle) {
			return true
		}
	}
	return false
}

func requireGraphToolContains(values ...string) func(provider.Request) error {
	return func(request provider.Request) error {
		for i := len(request.Messages) - 1; i >= 0; i-- {
			message := request.Messages[i]
			if message.Role != "tool" {
				continue
			}
			for _, value := range values {
				if !strings.Contains(message.Content, value) {
					return errors.New("tool result missing " + value)
				}
			}
			return nil
		}
		return errors.New("request has no tool result")
	}
}
