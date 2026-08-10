package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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
	"github.com/robert-mcdermott/collomia/internal/goalgraph"
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

type aliasedTool struct {
	tools.Function
	permissionName string
}

func (t aliasedTool) PermissionToolName() string { return t.permissionName }

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

// The node-boundary handoff replaces the entire active context. Guidance the
// user gave mid-graph — and was told applies to the remaining task — must not
// be what that optimization throws away.
func TestNodeBoundaryHandoffKeepsUserSteering(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "inspect in order", Nodes: []goalgraph.NodeSpec{
		{ID: 1, Title: "inspect source"},
		{ID: 2, Title: "inspect tests", DependsOn: []int{1}},
	}}, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(
		tools.Function{Def: provider.ToolDefinition{Name: "inspect", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect"}, Run: func(context.Context, json.RawMessage) (string, error) { return "observed", nil }},
	)
	steering := NewSteeringQueue()
	if err := steering.Add("prefer the integration suite over unit tests"); err != nil {
		t.Fatal(err)
	}
	sawSteeringAfterBoundary := false
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		switch call {
		case 1:
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "inspect-1", Name: "inspect", Arguments: json.RawMessage(`{}`)}}}, nil
		case 2:
			return provider.Response{Content: "source inspected"}, nil
		case 3:
			if requestContains(request, "[Runtime-owned Orchestrated Goal node handoff]") && requestContains(request, "prefer the integration suite over unit tests") {
				sawSteeringAfterBoundary = true
			}
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "inspect-2", Name: "inspect", Arguments: json.RawMessage(`{}`)}}}, nil
		default:
			return provider.Response{Content: "all inspections complete"}, nil
		}
	}}
	agentRuntime := New(Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil),
		MaxIterations: 8, GoalGraph: graph, TakeSteering: steering.Take,
	})
	if _, err := agentRuntime.Run(t.Context(), "inspect the project", func(event.Event) {}); err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !sawSteeringAfterBoundary {
		t.Fatal("user steering was discarded by the accepted-node handoff")
	}
}

func TestGoalGraphControllerSelectsDependencyReadyNodesOnPrimaryOnly(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "inspect in order", Nodes: []goalgraph.NodeSpec{
		{ID: 1, Title: "inspect source"},
		{ID: 2, Title: "inspect tests", DependsOn: []int{1}},
	}}, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(
		tools.Function{Def: provider.ToolDefinition{Name: "inspect", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect"}, Run: func(context.Context, json.RawMessage) (string, error) { return "observed", nil }},
		tools.Function{Def: provider.ToolDefinition{Name: "update_plan"}, Action: tools.Action{Risk: tools.RiskRead}, Run: func(context.Context, json.RawMessage) (string, error) { return "must not run", nil }},
		tools.Function{Def: provider.ToolDefinition{Name: "delegate"}, Action: tools.Action{Risk: tools.RiskRead}, Run: func(context.Context, json.RawMessage) (string, error) { return "must not run", nil }},
		goalgraph.RevisionTool{Graph: graph}, goalgraph.BlockTool{Graph: graph},
	)
	withUsage := func(response provider.Response) provider.Response {
		response.Usage = provider.Usage{InputTokens: 10, OutputTokens: 5}
		return response
	}
	compacted := 0
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		meta := map[string]bool{}
		for _, definition := range request.Tools {
			if definition.Name == "update_plan" || definition.Name == "delegate" {
				t.Fatalf("graph request exposed %s", definition.Name)
			}
			meta[definition.Name] = true
		}
		if !meta[goalgraph.ReviseToolName] || !meta[goalgraph.BlockToolName] {
			t.Fatal("runtime graph controls were removed by the primary profile allowlist")
		}
		switch call {
		case 1:
			if !requestContains(request, "1. inspect source · running") {
				t.Fatal("first node was not pinned as running")
			}
			return withUsage(graphToolResponse("inspect-1", "inspect", `{}`)), nil
		case 2:
			return withUsage(provider.Response{Content: "source inspected"}), nil
		case 3:
			if !requestContains(request, "2. inspect tests · running") {
				t.Fatal("dependent node was not selected after its dependency")
			}
			if !requestContains(request, "[Runtime-owned Orchestrated Goal node handoff]") || !requestContains(request, "accepted result: source inspected") || requestContains(request, "observed") {
				t.Fatalf("next node inherited prior tool transcript instead of a bounded runtime handoff: %+v", request.Messages)
			}
			return withUsage(graphToolResponse("inspect-2", "inspect", `{}`)), nil
		default:
			return withUsage(provider.Response{Content: "all inspections complete"}), nil
		}
	}}
	agentRuntime := New(Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100, Pricing: &appconfig.Pricing{InputPerMillion: 1, OutputPerMillion: 2}},
		Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil),
		MaxIterations: 8, GoalGraph: graph, OnCompaction: func(summary provider.Message, replaced int) {
			if replaced < 4 || !strings.Contains(summary.Content, "selects the next dependency-ready node") {
				t.Errorf("invalid node handoff: replaced=%d summary=%q", replaced, summary.Content)
			}
			compacted++
		},
	})
	agentRuntime.ApplyProfile(ProfileSettings{Tools: []string{"inspect"}, MaxIterations: 8})
	graphEvents := 0
	result, err := agentRuntime.Run(t.Context(), "inspect the project", func(e event.Event) {
		if e.Kind == event.KindGoalGraphUpdate {
			graphEvents++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "all inspections complete" || client.calls != 4 || graphEvents == 0 || compacted != 1 {
		t.Fatalf("result=%q calls=%d graphEvents=%d compactions=%d", result, client.calls, graphEvents, compacted)
	}
	if outcome, _ := graph.Outcome(); outcome != goalgraph.OutcomeDone {
		t.Fatalf("outcome=%q snapshot=%+v", outcome, graph.Snapshot())
	}
	usage := graph.UsageTotals(time.Time{}).Primary
	if usage.Iterations != 4 || usage.InputTokens != 40 || usage.OutputTokens != 20 || !usage.CostAvailable || !usage.CostEstimated || math.Abs(usage.CostUSD-0.00008) > 1e-12 {
		t.Fatalf("primary graph usage=%+v", usage)
	}
	if agentRuntime.ProviderIterations() != 4 {
		t.Fatalf("provider iterations=%d", agentRuntime.ProviderIterations())
	}
}

func TestGoalGraphExecutionBlocksToolsHiddenByRuntimeOwnership(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "inspect safely", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "inspect"}}}, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	planAssessed := false
	planRan := false
	registry := tools.NewRegistry(
		tools.Function{
			Def: provider.ToolDefinition{Name: "update_plan", InputSchema: json.RawMessage(`{"type":"object"}`)},
			AssessFn: func(json.RawMessage) (tools.Action, error) {
				planAssessed = true
				return tools.Action{}, errors.New("arguments should never be decoded")
			},
			Run: func(context.Context, json.RawMessage) (string, error) {
				planRan = true
				return "must not run", nil
			},
		},
		tools.Function{Def: provider.ToolDefinition{Name: "inspect", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect"}, Run: func(context.Context, json.RawMessage) (string, error) { return "observed", nil }},
	)
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		for _, definition := range request.Tools {
			if definition.Name == "update_plan" {
				t.Fatal("runtime-owned graph exposed update_plan")
			}
		}
		switch call {
		case 1:
			return graphToolResponse("remembered", "update_plan", `{"steps":"malformed"}`), nil
		case 2:
			if !requestContains(request, "not available in the current mode") {
				t.Fatal("hidden tool rejection was not returned to the model")
			}
			return graphToolResponse("inspect", "inspect", `{}`), nil
		default:
			return provider.Response{Content: "inspection complete"}, nil
		}
	}}
	runtime := New(Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil),
		MaxIterations: 4, GoalGraph: graph,
	})
	if result, err := runtime.Run(t.Context(), "inspect", nil); err != nil || result != "inspection complete" {
		t.Fatalf("result=%q error=%v", result, err)
	}
	if planAssessed || planRan {
		t.Fatalf("hidden tool crossed execution boundary: assessed=%v ran=%v", planAssessed, planRan)
	}
	if snapshot := graph.Snapshot(); snapshot.Outcome != goalgraph.OutcomeDone || len(snapshot.Attempts[0].Failures) != 0 {
		t.Fatalf("controller-protocol rejection poisoned active node: %+v", snapshot)
	}
}

func TestGoalGraphPrimaryIterationBudgetRenewsAtAcceptedNodeBoundary(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "inspect in order", Nodes: []goalgraph.NodeSpec{
		{ID: 1, Title: "inspect first"},
		{ID: 2, Title: "inspect second", DependsOn: []int{1}},
	}}, 1, goalgraph.Options{MaxAggregateIterations: 8})
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(tools.Function{Def: provider.ToolDefinition{Name: "inspect", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect"}, Run: func(context.Context, json.RawMessage) (string, error) { return "observed", nil }})
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		if call == 1 || call == 3 {
			return graphToolResponse(fmt.Sprintf("inspect-%d", call), "inspect", `{}`), nil
		}
		return provider.Response{Content: fmt.Sprintf("node %d complete", call/2)}, nil
	}}
	runtime := New(Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil),
		MaxIterations: 2, GoalGraph: graph,
	})
	if result, err := runtime.Run(t.Context(), "inspect both", nil); err != nil || result != "node 2 complete" {
		t.Fatalf("result=%q calls=%d error=%v", result, client.calls, err)
	}
	snapshot := graph.Snapshot()
	if snapshot.Outcome != goalgraph.OutcomeDone || len(snapshot.Attempts) != 2 || snapshot.Attempts[0].Iterations != 2 || snapshot.Attempts[1].Iterations != 2 || snapshot.Accounting.Primary.Iterations != 4 {
		t.Fatalf("iteration slices did not renew at node boundary: %+v", snapshot)
	}
}

func TestGoalGraphPrimaryProgressLeaseRenewsWithinAttempt(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "inspect", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "inspect source"}}}, 1, goalgraph.Options{MaxAggregateIterations: 8})
	if err != nil {
		t.Fatal(err)
	}
	executions := 0
	registry := tools.NewRegistry(tools.Function{
		Def:    provider.ToolDefinition{Name: "inspect", InputSchema: json.RawMessage(`{"type":"object"}`)},
		Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect"},
		Run: func(context.Context, json.RawMessage) (string, error) {
			executions++
			return fmt.Sprintf("novel observation %d", executions), nil
		},
	})
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		if call <= 2 {
			return graphToolResponse(fmt.Sprintf("inspect-%d", call), "inspect", `{}`), nil
		}
		return provider.Response{Content: "inspection complete"}, nil
	}}
	runtime := New(Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil),
		MaxIterations: 2, GoalGraph: graph,
	})
	result, err := runtime.Run(t.Context(), "inspect", nil)
	if err != nil || result != "inspection complete" || client.calls != 3 {
		t.Fatalf("result=%q calls=%d error=%v", result, client.calls, err)
	}
	snapshot := graph.Snapshot()
	if snapshot.Outcome != goalgraph.OutcomeDone || snapshot.Attempts[0].Iterations != 3 || snapshot.Attempts[0].LastProgressIteration != 2 {
		t.Fatalf("productive attempt did not renew its progress lease: %+v", snapshot.Attempts[0])
	}
}

func TestGoalGraphPrimaryProgressLeaseRejectsRepeatedEvidence(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "inspect", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "inspect source"}}}, 1, goalgraph.Options{MaxAggregateIterations: 8})
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(tools.Function{
		Def:    provider.ToolDefinition{Name: "inspect", InputSchema: json.RawMessage(`{"type":"object"}`)},
		Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect"},
		Run:    func(context.Context, json.RawMessage) (string, error) { return "same observation", nil },
	})
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		return graphToolResponse(fmt.Sprintf("inspect-%d", call), "inspect", `{}`), nil
	}}
	runtime := New(Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil),
		MaxIterations: 2, GoalGraph: graph,
	})
	_, err = runtime.Run(t.Context(), "inspect", nil)
	if !errors.Is(err, ErrIterationBudgetExceeded) || client.calls != 3 || !strings.Contains(err.Error(), "no novel durable progress") {
		t.Fatalf("calls=%d error=%v", client.calls, err)
	}
	snapshot := graph.Snapshot()
	if snapshot.Outcome != goalgraph.OutcomeBudgetExhausted || snapshot.Attempts[0].Iterations != 3 || snapshot.Attempts[0].LastProgressIteration != 1 {
		t.Fatalf("repeated evidence incorrectly renewed the progress lease: %+v", snapshot.Attempts[0])
	}
}

func TestGoalGraphCompletionGapIgnoresSuperficiallyNovelEvidence(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "change and verify", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "implement"}}}, 1, goalgraph.Options{MaxAggregateIterations: 20})
	if err != nil {
		t.Fatal(err)
	}
	token := "before"
	reads := 0
	registry := tools.NewRegistry(
		tools.Function{Def: provider.ToolDefinition{Name: "mutate", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskWrite, Summary: "change source"}, Run: func(context.Context, json.RawMessage) (string, error) {
			token = "after"
			return "changed", nil
		}},
		tools.Function{Def: provider.ToolDefinition{Name: "inspect", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect another detail"}, Run: func(context.Context, json.RawMessage) (string, error) {
			reads++
			return fmt.Sprintf("novel but non-verifying observation %d", reads), nil
		}},
	)
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		switch call {
		case 1:
			return graphToolResponse("write", "mutate", `{}`), nil
		case 2:
			return provider.Response{Content: "implemented"}, nil
		default:
			return graphToolResponse(fmt.Sprintf("inspect-%d", call), "inspect", `{}`), nil
		}
	}}
	runtime := New(Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil),
		MaxIterations: 20, GoalGraph: graph, GoalStateToken: func(context.Context) (string, error) { return token, nil },
	})
	_, err = runtime.Run(t.Context(), "change it", nil)
	if !errors.Is(err, ErrGoalBlocked) || !strings.Contains(err.Error(), "no repair or gate-changing progress") || client.calls != 6 {
		t.Fatalf("calls=%d error=%v", client.calls, err)
	}
	snapshot := graph.Snapshot()
	if snapshot.Outcome != goalgraph.OutcomeBlocked || snapshot.Attempts[0].Iterations != 6 || !strings.Contains(snapshot.Attempts[0].CompletionGap, "recognized verification") {
		t.Fatalf("completion gap did not bound remediation: %+v", snapshot)
	}
}

func TestGoalGraphCompletionGapAllowsNovelFailureRepairAndVerification(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "change and verify", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "implement"}}}, 1, goalgraph.Options{MaxAggregateIterations: 20})
	if err != nil {
		t.Fatal(err)
	}
	token := "before"
	writes := 0
	verifications := 0
	registry := tools.NewRegistry(
		tools.Function{Def: provider.ToolDefinition{Name: "mutate", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskWrite, Summary: "change source"}, Run: func(context.Context, json.RawMessage) (string, error) {
			writes++
			token = fmt.Sprintf("after-%d", writes)
			return "changed workspace", nil
		}},
		tools.Function{Def: provider.ToolDefinition{Name: "inspect", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect"}, Run: func(context.Context, json.RawMessage) (string, error) { return "observation", nil }},
		tools.Function{Def: provider.ToolDefinition{Name: "run_command", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskExecute, Summary: "run tests", Command: "go test ./..."}, Run: func(context.Context, json.RawMessage) (string, error) {
			verifications++
			if verifications == 1 {
				return "no tests collected", errors.New("exit status 5")
			}
			return "tests passed", nil
		}},
	)
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		switch call {
		case 1:
			return graphToolResponse("initial-write", "mutate", `{}`), nil
		case 2:
			return provider.Response{Content: "implemented"}, nil
		case 3, 4, 5:
			return graphToolResponse(fmt.Sprintf("inspect-%d", call), "inspect", `{}`), nil
		case 6:
			return graphToolResponse("first-verification", "run_command", `{}`), nil
		case 7:
			return graphToolResponse("repair", "mutate", `{}`), nil
		case 8:
			return graphToolResponse("second-verification", "run_command", `{}`), nil
		default:
			return provider.Response{Content: "implemented and verified"}, nil
		}
	}}
	runtime := New(Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil),
		MaxIterations: 20, GoalGraph: graph, GoalStateToken: func(context.Context) (string, error) { return token, nil },
	})
	result, err := runtime.Run(t.Context(), "change it", nil)
	if err != nil || result != "implemented and verified" || client.calls != 9 {
		t.Fatalf("result=%q calls=%d error=%v", result, client.calls, err)
	}
	snapshot := graph.Snapshot()
	if snapshot.Outcome != goalgraph.OutcomeDone || len(snapshot.Attempts[0].Failures) != 1 || !snapshot.Attempts[0].Failures[0].Resolved {
		t.Fatalf("repair did not resolve the novel verification failure: %+v", snapshot)
	}
	retainedFailureOutput := false
	for _, evidence := range snapshot.Evidence {
		if evidence.Kind == goalgraph.EvidenceVerification && evidence.Status == "failed" && strings.Contains(evidence.Summary, "no tests collected") {
			retainedFailureOutput = true
			break
		}
	}
	if !retainedFailureOutput {
		t.Fatalf("failed verifier output was not retained: %+v", snapshot.Evidence)
	}
}

func TestGoalGraphCompletionGapDoesNotRenewOnEquivalentVerificationFailures(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "change and verify", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "implement"}}}, 1, goalgraph.Options{MaxAggregateIterations: 20})
	if err != nil {
		t.Fatal(err)
	}
	token := "before"
	registry := tools.NewRegistry(
		tools.Function{Def: provider.ToolDefinition{Name: "mutate", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskWrite, Summary: "change source"}, Run: func(context.Context, json.RawMessage) (string, error) {
			token = "after"
			return "changed", nil
		}},
		tools.Function{Def: provider.ToolDefinition{Name: "run_command", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskExecute, Summary: "run tests", Command: "go test ./..."}, Run: func(context.Context, json.RawMessage) (string, error) {
			return "no tests collected", errors.New("exit status 5")
		}},
	)
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		if call == 1 {
			return graphToolResponse("write", "mutate", `{}`), nil
		}
		if call == 2 {
			return provider.Response{Content: "implemented"}, nil
		}
		return graphToolResponse(fmt.Sprintf("verify-%d", call), "run_command", `{}`), nil
	}}
	runtime := New(Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil),
		MaxIterations: 20, GoalGraph: graph, GoalStateToken: func(context.Context) (string, error) { return token, nil },
	})
	_, err = runtime.Run(t.Context(), "change it", nil)
	if !errors.Is(err, ErrGoalBlocked) || client.calls != 7 || !strings.Contains(err.Error(), "identical verification failures") {
		t.Fatalf("calls=%d error=%v", client.calls, err)
	}
	snapshot := graph.Snapshot()
	if snapshot.Outcome != goalgraph.OutcomeBlocked || snapshot.Attempts[0].CompletionGapIteration != 3 {
		t.Fatalf("equivalent failures renewed remediation: %+v", snapshot.Attempts[0])
	}
}

func TestGoalGraphPauseRequestedDuringProviderCallStopsAtNextBoundary(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "inspect", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "inspect"}}}, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		if call != 1 {
			t.Fatalf("provider called after pause request: %d", call)
		}
		if err := graph.RequestPause(context.Background(), "operator requested pause"); err != nil {
			t.Fatal(err)
		}
		return provider.Response{Content: "I will stop at the next boundary."}, nil
	}}
	runtime := New(Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil),
		MaxIterations: 4, GoalGraph: graph,
	})
	var states []string
	result, err := runtime.Run(t.Context(), "inspect", func(e event.Event) {
		if e.GoalGraph != nil {
			states = append(states, e.GoalGraph.State)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 1 || !strings.Contains(result, "paused at a safe scheduling boundary") {
		t.Fatalf("calls=%d result=%q", client.calls, result)
	}
	requested, reached, _ := graph.PauseState()
	if !requested || !reached || !slices.Contains(states, "pause_requested") || !slices.Contains(states, "paused") {
		t.Fatalf("pause requested=%t reached=%t states=%v", requested, reached, states)
	}
	if _, _, active := graph.Active(); !active {
		t.Fatal("cooperative pause discarded the active immutable attempt")
	}
}

func TestCompletedGoalGraphRejectsUnrelatedLaterPromptBeforeProviderCall(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "inspect", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "inspect"}}}, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := graph.StartNext(t.Context(), "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.BeginTool(t.Context(), attempt.ID, goalgraph.ToolAction{Tool: "read_file", Risk: "read"}, "workspace"); err != nil {
		t.Fatal(err)
	}
	if err := graph.FinishTool(t.Context(), attempt.ID, goalgraph.ToolResult{Tool: "read_file", Risk: "read", Summary: "observed", WorkspaceToken: "workspace"}); err != nil {
		t.Fatal(err)
	}
	if decision, err := graph.ProposeCompletion(t.Context(), "done", "workspace"); err != nil || decision.Kind != goalgraph.DecisionDone {
		t.Fatalf("completion decision=%+v err=%v", decision, err)
	}
	client := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
		return provider.Response{Content: "must not run"}, nil
	}}
	runtime := New(Options{Client: client, ProviderName: "fake", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), GoalGraph: graph})
	if _, err := runtime.Run(t.Context(), "now do something unrelated", nil); !errors.Is(err, ErrGoalGraphComplete) {
		t.Fatalf("terminal graph error=%v", err)
	}
	if client.calls != 0 || runtime.MessageCount() != 0 {
		t.Fatalf("terminal graph accepted later work: provider calls=%d messages=%d", client.calls, runtime.MessageCount())
	}
}

func TestGoalGraphControllerRequiresFreshVerificationAfterPrimaryWrite(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "change and verify", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "change source"}}}, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	token := "workspace-before"
	workspace := t.TempDir()
	registry := tools.NewRegistry(
		tools.Function{Def: provider.ToolDefinition{Name: "mutate", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskWrite, Summary: "change source"}, Run: func(context.Context, json.RawMessage) (string, error) {
			token = "workspace-after"
			return "changed", nil
		}},
		tools.Function{Def: provider.ToolDefinition{Name: "run_command", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskExecute, Summary: "run tests", Command: fmt.Sprintf(`cd "%s" && go test ./... 2>&1`, workspace)}, Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }},
	)
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		switch call {
		case 1:
			return graphToolResponse("write", "mutate", `{}`), nil
		case 2:
			return provider.Response{Content: "changed"}, nil
		case 3:
			if !requestContains(request, "cannot be accepted yet") {
				t.Fatal("verification gate notice was not pinned in the retry request")
			}
			return graphToolResponse("verify", "run_command", `{}`), nil
		default:
			if !requestContains(request, "Collomia verification evidence: recorded against the post-command workspace state") || !requestContains(request, "Do not start another node until the runtime selects it") {
				t.Fatal("successful graph verification receipt was not returned to the model")
			}
			return provider.Response{Content: "changed and verified"}, nil
		}
	}}
	agentRuntime := New(Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: workspace, Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil),
		MaxIterations: 8, GoalGraph: graph, GoalStateToken: func(context.Context) (string, error) { return token, nil },
	})
	result, err := agentRuntime.Run(t.Context(), "change it", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != "changed and verified" || client.calls != 4 {
		t.Fatalf("result=%q calls=%d", result, client.calls)
	}
	snapshot := graph.Snapshot()
	if snapshot.Outcome != goalgraph.OutcomeDone {
		t.Fatalf("outcome=%q", snapshot.Outcome)
	}
	found := false
	for _, evidence := range snapshot.Evidence {
		found = found || (evidence.Kind == goalgraph.EvidenceVerification && evidence.Status == "passed" && evidence.WorkspaceToken == "workspace-after")
	}
	if !found {
		t.Fatalf("fresh verification evidence not recorded: %+v", snapshot.Evidence)
	}
}

// The kanban9 session's exact shape: a sandboxed run set its cache directory
// inside the workspace before invoking a suite that passed, and the node died
// four iterations later reporting that no verification existed.
func TestGoalGraphAcceptsVerificationPreparedByAnEnvironmentExport(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "change and verify", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "change source"}}}, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	token := "workspace-before"
	workspace := t.TempDir()
	registry := tools.NewRegistry(
		tools.Function{Def: provider.ToolDefinition{Name: "mutate", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskWrite, Summary: "change source"}, Run: func(context.Context, json.RawMessage) (string, error) {
			token = "workspace-after"
			return "changed", nil
		}},
		tools.Function{Def: provider.ToolDefinition{Name: "run_command", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskExecute, Summary: "run tests", Command: `export UV_CACHE_DIR="$(pwd)/.uv-cache" && uv run pytest -q`}, Run: func(context.Context, json.RawMessage) (string, error) {
			return "11 passed, 1 warning in 1.51s", nil
		}},
	)
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		switch call {
		case 1:
			return graphToolResponse("write", "mutate", `{}`), nil
		case 2:
			return provider.Response{Content: "changed"}, nil
		case 3:
			return graphToolResponse("verify", "run_command", `{}`), nil
		default:
			if requestContains(request, "verification evidence was not recorded") {
				t.Fatal("preparation before the verifier was refused as evidence")
			}
			return provider.Response{Content: "changed and verified"}, nil
		}
	}}
	agentRuntime := New(Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: workspace, Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil),
		MaxIterations: 8, GoalGraph: graph, GoalStateToken: func(context.Context) (string, error) { return token, nil },
	})
	if _, err := agentRuntime.Run(t.Context(), "change it", nil); err != nil {
		t.Fatal(err)
	}
	if outcome := graph.Snapshot().Outcome; outcome != goalgraph.OutcomeDone {
		t.Fatalf("outcome=%q, want done", outcome)
	}
	found := false
	for _, evidence := range graph.Snapshot().Evidence {
		found = found || (evidence.Kind == goalgraph.EvidenceVerification && evidence.Status == "passed" && evidence.WorkspaceToken == "workspace-after")
	}
	if !found {
		t.Fatalf("passing suite was not recorded as verification: %+v", graph.Snapshot().Evidence)
	}
}

// The fredhutch-history session's exact shape: a brand-new directory with no
// manifest of any kind, where the node was asked to create a focused smoke
// test, created one, ran it, watched it pass — and blocked, because the only
// way to run it was an unrecognized command.
func TestGoalGraphAcceptsASmokeTestInAProjectWithNoManifest(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "scaffold and verify", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "scaffold site and smoke test"}}}, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	token := "workspace-before"
	workspace := t.TempDir()
	registry := tools.NewRegistry(
		tools.Function{Def: provider.ToolDefinition{Name: "mutate", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskWrite, Summary: "write index.html and tests/smoke.js"}, Run: func(context.Context, json.RawMessage) (string, error) {
			token = "workspace-after"
			return "wrote 3 files", nil
		}},
		tools.Function{Def: provider.ToolDefinition{Name: "run_command", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskExecute, Summary: "run smoke test", Command: "node tests/smoke.js"}, Run: func(context.Context, json.RawMessage) (string, error) {
			return "SMOKE TEST PASSED: index.html, styles.css, logo asset, and all four hutch CSS vars verified.", nil
		}},
	)
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		switch call {
		case 1:
			return graphToolResponse("scaffold", "mutate", `{}`), nil
		case 2:
			return provider.Response{Content: "scaffolded"}, nil
		case 3:
			return graphToolResponse("verify", "run_command", `{}`), nil
		default:
			if requestContains(request, "verification evidence was not recorded") {
				t.Fatal("the smoke test this mode asks a node to create was refused as evidence")
			}
			return provider.Response{Content: "scaffolded and verified"}, nil
		}
	}}
	agentRuntime := New(Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: workspace, Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil),
		MaxIterations: 8, GoalGraph: graph, GoalStateToken: func(context.Context) (string, error) { return token, nil },
	})
	if _, err := agentRuntime.Run(t.Context(), "build the site", nil); err != nil {
		t.Fatal(err)
	}
	if outcome := graph.Snapshot().Outcome; outcome != goalgraph.OutcomeDone {
		t.Fatalf("outcome=%q, want done", outcome)
	}
	found := false
	for _, evidence := range graph.Snapshot().Evidence {
		found = found || (evidence.Kind == goalgraph.EvidenceVerification && evidence.Status == "passed" && evidence.WorkspaceToken == "workspace-after")
	}
	if !found {
		t.Fatalf("passing smoke test was not recorded as verification: %+v", graph.Snapshot().Evidence)
	}
}

// When a check really is refused, the blocker has to name it. The session that
// motivated this read "no successful recognized verification" directly beneath
// a passing test suite the user had watched run, with nothing to act on.
func TestStalledGoalNodeNamesTheRefusedVerification(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "change and verify", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "change source"}}}, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	token := "workspace-before"
	workspace := t.TempDir()
	registry := tools.NewRegistry(
		tools.Function{Def: provider.ToolDefinition{Name: "mutate", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskWrite, Summary: "change source"}, Run: func(context.Context, json.RawMessage) (string, error) {
			token = "workspace-after"
			return "changed", nil
		}},
		tools.Function{Def: provider.ToolDefinition{Name: "run_command", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskExecute, Summary: "run tests", Command: "uv run pytest -q || true"}, Run: func(context.Context, json.RawMessage) (string, error) {
			return "11 passed", nil
		}},
	)
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		switch call {
		case 1:
			return graphToolResponse("write", "mutate", `{}`), nil
		case 2:
			// The completion proposal is what makes the runtime record an exact
			// gap and start the bounded remediation window.
			return provider.Response{Content: "implemented"}, nil
		default:
			return graphToolResponse(fmt.Sprintf("verify-%d", call), "run_command", `{}`), nil
		}
	}}
	agentRuntime := New(Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: workspace, Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil),
		MaxIterations: 20, GoalGraph: graph, GoalStateToken: func(context.Context) (string, error) { return token, nil },
	})
	_, err = agentRuntime.Run(t.Context(), "change it", nil)
	if err == nil || !errors.Is(err, ErrGoalBlocked) {
		t.Fatalf("stalled node error=%v, want a blocked goal", err)
	}
	if !strings.Contains(err.Error(), "were refused during this attempt") || !strings.Contains(err.Error(), "uv run pytest -q") {
		t.Fatalf("blocker does not name the refused check: %v", err)
	}
}

// An ecosystem the recognizer does not cover must not become silence. The
// model that hit this ran its passing check, was told only that verification
// was missing, and spent its whole remediation lease guessing at the cause —
// so the explanation has to arrive while the lease is still running, and the
// blocker has to name the command if it runs out anyway.
func TestUnrecognizedCheckIsExplainedWhileTheAttemptCanStillAct(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "change and verify", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "change source"}}}, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	token := "workspace-before"
	workspace := t.TempDir()
	registry := tools.NewRegistry(
		tools.Function{Def: provider.ToolDefinition{Name: "mutate", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskWrite, Summary: "change source"}, Run: func(context.Context, json.RawMessage) (string, error) {
			token = "workspace-after"
			return "changed", nil
		}},
		tools.Function{Def: provider.ToolDefinition{Name: "run_command", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskExecute, Summary: "run checks", Command: "./run-my-checks.sh"}, Run: func(context.Context, json.RawMessage) (string, error) {
			return "ALL CHECKS PASSED", nil
		}},
	)
	explained := false
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		switch call {
		case 1:
			return graphToolResponse("write", "mutate", `{}`), nil
		case 2:
			// The completion proposal is what records the exact gap; the
			// explanation is owed on every unrecognized check after it.
			return provider.Response{Content: "implemented"}, nil
		default:
			if requestContains(request, "not a recognized verification command") && requestContains(request, "./run-my-checks.sh") {
				explained = true
			}
			return graphToolResponse(fmt.Sprintf("verify-%d", call), "run_command", `{}`), nil
		}
	}}
	agentRuntime := New(Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: workspace, Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil),
		MaxIterations: 20, GoalGraph: graph, GoalStateToken: func(context.Context) (string, error) { return token, nil },
	})
	_, err = agentRuntime.Run(t.Context(), "change it", nil)
	if err == nil || !errors.Is(err, ErrGoalBlocked) {
		t.Fatalf("stalled node error=%v, want a blocked goal", err)
	}
	if !explained {
		t.Fatal("the model was never told which command was declined, which is the whole failure being fixed")
	}
	if !strings.Contains(err.Error(), "./run-my-checks.sh") || !strings.Contains(err.Error(), "not a recognized verification command") {
		t.Fatalf("blocker does not name the unrecognized check: %v", err)
	}
}

// The explanation is owed only once the runtime has asked for verification.
// Attached to ordinary work it would be noise on every mkdir and every ls.
func TestOrdinaryCommandsAreNotLecturedAboutVerification(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "change and verify", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "change source"}}}, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	registry := tools.NewRegistry(
		tools.Function{Def: provider.ToolDefinition{Name: "run_command", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskRead, Summary: "list the workspace", Command: "ls -la"}, Run: func(context.Context, json.RawMessage) (string, error) {
			return "total 0", nil
		}},
	)
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		if call == 1 {
			return graphToolResponse("inspect", "run_command", `{}`), nil
		}
		if requestContains(request, "not a recognized verification command") {
			t.Fatal("ordinary read work was told it failed to verify")
		}
		return provider.Response{Content: "inspected"}, nil
	}}
	agentRuntime := New(Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: workspace, Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil),
		MaxIterations: 8, GoalGraph: graph, GoalStateToken: func(context.Context) (string, error) { return "workspace", nil },
	})
	if _, err := agentRuntime.Run(t.Context(), "prepare it", nil); err != nil {
		t.Fatal(err)
	}
}

func TestGoalGraphControllerTurnsPermissionDenialIntoBlockedOutcome(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "change", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "change source"}}}, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	executed := false
	registry := tools.NewRegistry(tools.Function{Def: provider.ToolDefinition{Name: "mutate", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskWrite, Summary: "change source"}, Run: func(context.Context, json.RawMessage) (string, error) {
		executed = true
		return "changed", nil
	}})
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		if call == 1 {
			return graphToolResponse("write", "mutate", `{}`), nil
		}
		return provider.Response{Content: "cannot continue"}, nil
	}}
	agentRuntime := New(Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: t.TempDir(), Registry: registry,
		Permissions:   permission.New(appconfig.Permissions{Mode: "autopilot", DeniedTools: []string{"mutate"}}, nil),
		MaxIterations: 4, GoalGraph: graph, GoalStateToken: func(context.Context) (string, error) { return "workspace", nil },
	})
	_, err = agentRuntime.Run(t.Context(), "change it", nil)
	if !errors.Is(err, ErrGoalBlocked) || executed {
		t.Fatalf("error=%v executed=%v", err, executed)
	}
	if outcome, reason := graph.Outcome(); outcome != goalgraph.OutcomeBlocked || !strings.Contains(reason, "permission denied") {
		t.Fatalf("outcome=%q reason=%q", outcome, reason)
	}
}

func TestGoalGraphControllerRetriesNormalizedProviderFailureInFreshAttempt(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "inspect", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "inspect source"}}}, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(tools.Function{Def: provider.ToolDefinition{Name: "inspect", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect"}, Run: func(context.Context, json.RawMessage) (string, error) { return "observed", nil }})
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		switch call {
		case 1:
			return provider.Response{}, &provider.Error{Provider: "fixture", Operation: "request", Kind: provider.ErrorUnavailable, Retryable: true, Message: "temporary outage"}
		case 2:
			if !requestContains(request, "fresh bounded attempt") {
				t.Fatal("provider retry notice was not delivered")
			}
			return graphToolResponse("inspect", "inspect", `{}`), nil
		default:
			return provider.Response{Content: "inspection complete"}, nil
		}
	}}
	agentRuntime := New(Options{Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 6, GoalGraph: graph})
	if result, err := agentRuntime.Run(t.Context(), "inspect", nil); err != nil || result != "inspection complete" || client.calls != 3 {
		t.Fatalf("result=%q calls=%d error=%v", result, client.calls, err)
	}
	snapshot := graph.Snapshot()
	if snapshot.Outcome != goalgraph.OutcomeDone || len(snapshot.Attempts) != 2 || snapshot.Attempts[0].State != goalgraph.AttemptRetryable || snapshot.Attempts[1].State != goalgraph.AttemptAccepted {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if snapshot.Accounting.Primary.Iterations != 3 || snapshot.Attempts[0].Iterations != 1 || snapshot.Attempts[1].Iterations != 2 {
		t.Fatalf("provider-failure accounting=%+v attempts=%+v", snapshot.Accounting.Primary, snapshot.Attempts)
	}
}

func TestGoalGraphControllerPreservesCancellationAndIterationBudget(t *testing.T) {
	t.Run("cancellation", func(t *testing.T) {
		graph, err := goalgraph.New(goalgraph.Spec{Goal: "inspect", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "inspect"}}}, 1, goalgraph.Options{})
		if err != nil {
			t.Fatal(err)
		}
		client := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
			return provider.Response{}, context.Canceled
		}}
		runtime := New(Options{Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), GoalGraph: graph})
		if _, err := runtime.Run(t.Context(), "inspect", nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
		if outcome, _ := graph.Outcome(); outcome != goalgraph.OutcomeCancelled {
			t.Fatalf("outcome=%q", outcome)
		}
	})

	t.Run("iteration budget", func(t *testing.T) {
		graph, err := goalgraph.New(goalgraph.Spec{Goal: "inspect", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "inspect"}}}, 1, goalgraph.Options{})
		if err != nil {
			t.Fatal(err)
		}
		client := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
			return provider.Response{Content: "done without evidence"}, nil
		}}
		runtime := New(Options{Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 1, GoalGraph: graph})
		if _, err := runtime.Run(t.Context(), "inspect", nil); !errors.Is(err, ErrIterationBudgetExceeded) {
			t.Fatalf("error=%v", err)
		}
		if outcome, _ := graph.Outcome(); outcome != goalgraph.OutcomeBudgetExhausted {
			t.Fatalf("outcome=%q", outcome)
		}
	})

	t.Run("whole graph iteration budget", func(t *testing.T) {
		graph, err := goalgraph.New(goalgraph.Spec{Goal: "inspect", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "inspect"}}}, 1, goalgraph.Options{MaxAggregateIterations: 1})
		if err != nil {
			t.Fatal(err)
		}
		client := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
			return provider.Response{Content: "done without evidence"}, nil
		}}
		runtime := New(Options{Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 8, GoalGraph: graph})
		if _, err := runtime.Run(t.Context(), "inspect", nil); !errors.Is(err, ErrAggregateBudgetExceeded) || GoalOutcomeFor(err) != GoalBudgetExhausted {
			t.Fatalf("calls=%d outcome=%s error=%v", client.calls, GoalOutcomeFor(err), err)
		}
		if outcome, reason := graph.Outcome(); outcome != goalgraph.OutcomeBudgetExhausted || !strings.Contains(reason, "1/1") {
			t.Fatalf("outcome=%q reason=%q", outcome, reason)
		}
	})

	t.Run("whole graph active wall interrupts an in-flight provider", func(t *testing.T) {
		graph, err := goalgraph.New(goalgraph.Spec{Goal: "inspect", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "inspect"}}}, 1, goalgraph.Options{MaxActiveWallSeconds: 1})
		if err != nil {
			t.Fatal(err)
		}
		client := &cancellableClient{started: make(chan struct{}, 1)}
		runtime := New(Options{Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 8, GoalGraph: graph})
		if _, err := runtime.Run(t.Context(), "inspect", nil); !errors.Is(err, ErrAggregateBudgetExceeded) || GoalOutcomeFor(err) != GoalBudgetExhausted {
			t.Fatalf("outcome=%s error=%v", GoalOutcomeFor(err), err)
		}
		if outcome, reason := graph.Outcome(); outcome != goalgraph.OutcomeBudgetExhausted || !strings.Contains(reason, "active-wall") {
			t.Fatalf("outcome=%q reason=%q", outcome, reason)
		}
	})
}

func TestGoalGraphControllerFailsBeforeMutationWhenWriteAheadPersistenceFails(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "change", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "change"}}}, 1, goalgraph.Options{Persist: func(_ context.Context, snapshot goalgraph.Snapshot, _ bool) error {
		for _, attempt := range snapshot.Attempts {
			if attempt.PendingAction != nil {
				return errors.New("fixture graph storage failed")
			}
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	executed := false
	registry := tools.NewRegistry(tools.Function{Def: provider.ToolDefinition{Name: "mutate", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskWrite, Summary: "change"}, Run: func(context.Context, json.RawMessage) (string, error) {
		executed = true
		return "changed", nil
	}})
	client := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
		return graphToolResponse("write", "mutate", `{}`), nil
	}}
	runtime := New(Options{Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil), GoalGraph: graph, GoalStateToken: func(context.Context) (string, error) { return "workspace", nil }})
	if _, err := runtime.Run(t.Context(), "change", nil); err == nil || !strings.Contains(err.Error(), "fixture graph storage failed") {
		t.Fatalf("error=%v", err)
	}
	if executed {
		t.Fatal("mutation executed after its write-ahead graph record failed")
	}
}

func requestContains(request provider.Request, value string) bool {
	for _, message := range request.Messages {
		if strings.Contains(message.Content, value) {
			return true
		}
	}
	return false
}

func graphToolResponse(id, name, arguments string) provider.Response {
	return provider.Response{ToolCalls: []provider.ToolCall{{ID: id, Name: name, Arguments: json.RawMessage(arguments)}}}
}

func TestCompletionControllerContinuesAnUnfinishedPlan(t *testing.T) {
	board := plan.NewBoard()
	if err := board.Set(plan.Plan{Goal: "ship", Steps: []plan.Step{{ID: 1, Title: "inspect", Status: "pending"}}}); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(plan.Tool(board))
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		switch call {
		case 1:
			return provider.Response{Content: "Everything is done."}, nil
		case 2:
			found := false
			for _, message := range request.Messages {
				found = found || strings.Contains(message.Content, "completion controller")
			}
			if !found {
				t.Fatal("controller notice was not delivered to the next request")
			}
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "plan", Name: "update_plan", Arguments: json.RawMessage(`{"goal":"ship","steps":[{"id":1,"title":"inspect","status":"done","evidence":"repository inspected"}]}`)}}}, nil
		default:
			return provider.Response{Content: "Inspected and complete."}, nil
		}
	}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 6, CompletionPlan: board})
	warnings := 0
	result, err := a.Run(t.Context(), "finish it", func(e event.Event) {
		if e.Kind == event.KindWarning && strings.Contains(e.Text, "completion controller") {
			warnings++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "Inspected and complete." || client.calls != 3 || warnings != 1 {
		t.Fatalf("result=%q calls=%d warnings=%d", result, client.calls, warnings)
	}
}

func TestCompletionControllerStopsAfterTwoInterventions(t *testing.T) {
	board := plan.NewBoard()
	if err := board.Set(plan.Plan{Goal: "ship", Steps: []plan.Step{{ID: 1, Title: "work", Status: "in_progress"}}}); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
		return provider.Response{Content: "done without updating anything"}, nil
	}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 8, CompletionPlan: board})
	warnings, turnEnds := 0, 0
	result, err := a.Run(t.Context(), "finish it", func(e event.Event) {
		switch e.Kind {
		case event.KindWarning:
			warnings++
		case event.KindTurnEnd:
			turnEnds++
		}
	})
	if !errors.Is(err, ErrGoalBlocked) || GoalOutcomeFor(err) != GoalBlocked {
		t.Fatalf("error=%v outcome=%s", err, GoalOutcomeFor(err))
	}
	if result != "done without updating anything" || client.calls != 3 || warnings != 2 || turnEnds != 1 {
		t.Fatalf("result=%q calls=%d warnings=%d turnEnds=%d", result, client.calls, warnings, turnEnds)
	}
}

func TestCompletionControllerRequiresVerificationAfterWrite(t *testing.T) {
	board := plan.NewBoard()
	if err := board.Set(plan.Plan{Goal: "change", Steps: []plan.Step{{ID: 1, Title: "edit", Status: "pending"}}}); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(
		plan.Tool(board),
		tools.Function{Def: provider.ToolDefinition{Name: "mutate"}, Action: tools.Action{Risk: tools.RiskWrite, Summary: "change file"}, Run: func(context.Context, json.RawMessage) (string, error) { return "changed", nil }},
		tools.Function{Def: provider.ToolDefinition{Name: "run_command"}, Action: tools.Action{Risk: tools.RiskExecute, Summary: "run tests", Command: "go test ./..."}, Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }},
	)
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		switch call {
		case 1:
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "write", Name: "mutate", Arguments: json.RawMessage(`{}`)}}}, nil
		case 2:
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "plan", Name: "update_plan", Arguments: json.RawMessage(`{"goal":"change","steps":[{"id":1,"title":"edit","status":"done","evidence":"file changed"}]}`)}}}, nil
		case 3:
			return provider.Response{Content: "changed"}, nil
		case 4:
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "verify", Name: "run_command", Arguments: json.RawMessage(`{}`)}}}, nil
		default:
			return provider.Response{Content: "changed and verified"}, nil
		}
	}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil), MaxIterations: 8, CompletionPlan: board})
	warnings := 0
	result, err := a.Run(t.Context(), "change it", func(e event.Event) {
		if e.Kind == event.KindWarning {
			warnings++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "changed and verified" || client.calls != 5 || warnings != 1 {
		t.Fatalf("result=%q calls=%d warnings=%d", result, client.calls, warnings)
	}
}

// The controller's advice used to offer one word for an unfinished step, and
// recording it is what makes the whole turn report blocked. A run that built
// and verified its deliverable therefore ended in a red failure because the
// model had abandoned one side attempt — read a reference it turned out not to
// need — and was told the only way to record that was `blocked`.
func TestCompletionNoticeDistinguishesSkippedFromBlocked(t *testing.T) {
	notice := completionNotice([]string{"a failed tool has not been recovered or recorded as blocked: read_file"}, 1)
	for _, want := range []string{
		"`skipped` when the action proved unnecessary or you achieved it another way",
		"`blocked` only when the work genuinely cannot be completed",
		"a blocked step ends this turn as blocked",
	} {
		if !strings.Contains(notice, want) {
			t.Fatalf("completion notice is missing %q:\n%s", want, notice)
		}
	}
	// The distinction has to be real, not just described: a skipped step with
	// a reason completes the turn, and a blocked one does not.
	skipped := plan.Plan{Goal: "build", Steps: []plan.Step{
		{ID: 1, Title: "build it", Status: "done", Evidence: "wrote the files"},
		{ID: 2, Title: "read an optional reference", Status: "skipped", Evidence: "denied outside the workspace, and not needed"},
	}}
	if state := skipped.AssessCompletion().State; state != plan.CompletionReady {
		t.Fatalf("a skipped side attempt left the plan %q, want ready", state)
	}
	blocked := skipped
	blocked.Steps = append([]plan.Step(nil), skipped.Steps...)
	blocked.Steps[1].Status = "blocked"
	if state := blocked.AssessCompletion().State; state != plan.CompletionBlocked {
		t.Fatalf("a blocked step left the plan %q, want blocked", state)
	}
}

func TestCompletionControllerAcceptsExplicitVerificationException(t *testing.T) {
	board := plan.NewBoard()
	if err := board.Set(plan.Plan{Goal: "change docs", Steps: []plan.Step{{ID: 1, Title: "edit prose", Status: "pending"}}}); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(
		plan.Tool(board),
		tools.Function{Def: provider.ToolDefinition{Name: "mutate"}, Action: tools.Action{Risk: tools.RiskWrite, Summary: "change prose"}, Run: func(context.Context, json.RawMessage) (string, error) { return "changed", nil }},
	)
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		switch call {
		case 1:
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "write", Name: "mutate", Arguments: json.RawMessage(`{}`)}}}, nil
		case 2:
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "plan", Name: "update_plan", Arguments: json.RawMessage(`{"goal":"change docs","steps":[{"id":1,"title":"edit prose","status":"done","evidence":"copy reviewed"}],"verification_note":"documentation-only change has no executable check"}`)}}}, nil
		default:
			return provider.Response{Content: "docs updated"}, nil
		}
	}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil), MaxIterations: 5, CompletionPlan: board})
	if result, err := a.Run(t.Context(), "change docs", nil); err != nil || result != "docs updated" || client.calls != 3 {
		t.Fatalf("result=%q calls=%d error=%v", result, client.calls, err)
	}
}

func TestCompletionControllerDoesNotReuseStaleVerificationException(t *testing.T) {
	board := plan.NewBoard()
	const oldNote = "documentation-only change has no executable check"
	if err := board.Set(plan.Plan{Goal: "change docs", VerificationNote: oldNote, Steps: []plan.Step{{ID: 1, Title: "edit prose", Status: "pending"}}}); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(
		plan.Tool(board),
		tools.Function{Def: provider.ToolDefinition{Name: "mutate"}, Action: tools.Action{Risk: tools.RiskWrite, Summary: "change prose"}, Run: func(context.Context, json.RawMessage) (string, error) { return "changed", nil }},
	)
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		switch call {
		case 1:
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "write", Name: "mutate", Arguments: json.RawMessage(`{}`)}}}, nil
		case 2:
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "old-note", Name: "update_plan", Arguments: json.RawMessage(`{"goal":"change docs","steps":[{"id":1,"title":"edit prose","status":"done","evidence":"copy reviewed"}],"verification_note":"documentation-only change has no executable check"}`)}}}, nil
		case 3:
			return provider.Response{Content: "done with stale note"}, nil
		case 4:
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "fresh-note", Name: "update_plan", Arguments: json.RawMessage(`{"goal":"change docs","steps":[{"id":1,"title":"edit prose","status":"done","evidence":"copy reviewed"}],"verification_note":"this turn changed only prose; no generated output or executable behavior exists"}`)}}}, nil
		default:
			return provider.Response{Content: "done with fresh note"}, nil
		}
	}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil), MaxIterations: 6, CompletionPlan: board})
	if result, err := a.Run(t.Context(), "change docs again", nil); err != nil || result != "done with fresh note" || client.calls != 5 {
		t.Fatalf("result=%q calls=%d error=%v", result, client.calls, err)
	}
}

func TestCompletionControllerRequiresRecoveryAfterToolFailure(t *testing.T) {
	board := plan.NewBoard()
	attempts := 0
	registry := tools.NewRegistry(tools.Function{Def: provider.ToolDefinition{Name: "inspect"}, Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect"}, Run: func(context.Context, json.RawMessage) (string, error) {
		attempts++
		if attempts == 1 {
			return "", errors.New("temporary read failure")
		}
		return "observed", nil
	}})
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		switch call {
		case 1, 3:
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: fmt.Sprint(call), Name: "inspect", Arguments: json.RawMessage(`{}`)}}}, nil
		case 2:
			return provider.Response{Content: "giving up"}, nil
		default:
			return provider.Response{Content: "recovered"}, nil
		}
	}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 6, CompletionPlan: board})
	if result, err := a.Run(t.Context(), "inspect", nil); err != nil || result != "recovered" || attempts != 2 || client.calls != 4 {
		t.Fatalf("result=%q attempts=%d calls=%d error=%v", result, attempts, client.calls, err)
	}
}

func TestCompletionControllerDoesNotTreatUnrelatedSuccessAsRecovery(t *testing.T) {
	controller := newCompletionController(plan.NewBoard(), t.TempDir(), false)
	controller.observe(toolObservation{Name: "run_command", Action: tools.Action{Risk: tools.RiskExecute, Summary: "run tests"}, Failed: true})
	controller.observe(toolObservation{Name: "read_file", Action: tools.Action{Risk: tools.RiskRead, Summary: "read a file"}})
	decision := controller.assess()
	if decision.done || decision.blocked || !strings.Contains(decision.notice, "run_command") {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestCompletionControllerRetainsEveryUnresolvedFailure(t *testing.T) {
	controller := newCompletionController(plan.NewBoard(), t.TempDir(), false)
	controller.observe(toolObservation{Name: "run_command", Action: tools.Action{Risk: tools.RiskExecute, Summary: "run tests"}, Failed: true})
	controller.observe(toolObservation{Name: "external_lookup", Action: tools.Action{Risk: tools.RiskExternal, Summary: "look up dependency"}, Failed: true})
	controller.observe(toolObservation{Name: "run_command", Action: tools.Action{Risk: tools.RiskExecute, Summary: "run tests"}})
	decision := controller.assess()
	if decision.done || decision.blocked || !strings.Contains(decision.notice, "external_lookup") || strings.Contains(decision.notice, "run_command (run tests)") {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestCompletionControllerAcceptsCorrectedPlanToolRetry(t *testing.T) {
	controller := newCompletionController(plan.NewBoard(), t.TempDir(), false)
	controller.observe(toolObservation{Name: "update_plan", Action: tools.Action{Risk: tools.RiskRead, Summary: "update the task plan"}, Failed: true})
	controller.observe(toolObservation{Name: "update_plan", Action: tools.Action{Risk: tools.RiskRead, Summary: "update the task plan"}})
	if decision := controller.assess(); !decision.done {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestCompletionControllerTreatsFailedWriteAsPotentialMutation(t *testing.T) {
	controller := newCompletionController(plan.NewBoard(), t.TempDir(), false)
	controller.observe(toolObservation{Name: "edit_file", Action: tools.Action{Risk: tools.RiskWrite, Summary: "edit a file"}, Failed: true})
	decision := controller.assess()
	if decision.done || decision.blocked || !strings.Contains(decision.notice, "files changed") || !strings.Contains(decision.notice, "edit_file") {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestCompletionControllerReturnsExplicitPlanBlock(t *testing.T) {
	board := plan.NewBoard()
	if err := board.Set(plan.Plan{Goal: "ship", Steps: []plan.Step{{ID: 1, Title: "publish", Status: "pending"}}}); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(plan.Tool(board))
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		if call == 1 {
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "plan", Name: "update_plan", Arguments: json.RawMessage(`{"goal":"ship","steps":[{"id":1,"title":"publish","status":"blocked","evidence":"release credential is unavailable"}]}`)}}}, nil
		}
		return provider.Response{Content: "Blocked on the release credential."}, nil
	}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 4, CompletionPlan: board})
	result, err := a.Run(t.Context(), "ship", nil)
	if !errors.Is(err, ErrGoalBlocked) || !strings.Contains(err.Error(), "release credential is unavailable") || result != "Blocked on the release credential." {
		t.Fatalf("result=%q error=%v", result, err)
	}
}

func TestCompletionControllerDoesNotReactivateHistoricalTerminalPlan(t *testing.T) {
	board := plan.NewBoard()
	if err := board.Set(plan.Plan{Goal: "old task", Steps: []plan.Step{{ID: 1, Title: "old work", Status: "blocked", Evidence: "old blocker"}}}); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
		return provider.Response{Content: "Here is the information."}, nil
	}}
	a := New(Options{Client: client, ProviderName: "fake", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), CompletionPlan: board})
	if result, err := a.Run(t.Context(), "unrelated question", nil); err != nil || result != "Here is the information." || client.calls != 1 {
		t.Fatalf("result=%q calls=%d error=%v", result, client.calls, err)
	}
}

func TestCompletionControllerPreservesPlanningAndIterationLimits(t *testing.T) {
	board := plan.NewBoard()
	if err := board.Set(plan.Plan{Goal: "future work", Steps: []plan.Step{{ID: 1, Title: "implement", Status: "pending"}}}); err != nil {
		t.Fatal(err)
	}
	planningClient := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
		return provider.Response{Content: "Plan ready."}, nil
	}}
	planning := New(Options{Client: planningClient, ProviderName: "fake", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), PlanMode: true, CompletionPlan: board})
	if _, err := planning.Run(t.Context(), "plan it", nil); err != nil || planningClient.calls != 1 {
		t.Fatalf("planning calls=%d error=%v", planningClient.calls, err)
	}

	limitedClient := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
		return provider.Response{Content: "premature"}, nil
	}}
	limited := New(Options{Client: limitedClient, ProviderName: "fake", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100}, Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 1, CompletionPlan: board})
	_, err := limited.Run(t.Context(), "execute it", nil)
	if !errors.Is(err, ErrIterationBudgetExceeded) || GoalOutcomeFor(err) != GoalBudgetExhausted || limitedClient.calls != 1 {
		t.Fatalf("calls=%d outcome=%s error=%v", limitedClient.calls, GoalOutcomeFor(err), err)
	}
}

func TestVerificationCommandRecognitionRejectsShellSuccessMasking(t *testing.T) {
	workspace := t.TempDir()
	for _, command := range []string{"go test ./...", "go test ./internal/agent -run TestAgent", "uv run pytest -q", "UV_CACHE_DIR=.uv-cache uv run pytest -v", ".venv/bin/pytest -v", ".venv/bin/python -m pytest -v", "python3 -m mypy app", "uv run python -m pytest -q", "ruff check .", "uv run ruff format --check .", "npm run lint", fmt.Sprintf(`cd "%s" && UV_CACHE_DIR=.uv-cache uv run pytest -v 2>&1`, workspace), "cd . && .venv/bin/pytest -q 2>&1"} {
		if !isVerificationCommand(command, workspace) {
			t.Errorf("verification command was not recognized: %q", command)
		}
	}
	// `git diff --check` is a whitespace linter that passes on nearly any tree.
	// Accepting it would let a mutating node close its verification gate
	// without any check of the change it just made.
	for _, command := range []string{"echo tests passed", "go test ./... || true", "go test ./...; echo ok", "ruff format .", "uv run ruff format .", "cat test.log", "git diff --check", "git status", fmt.Sprintf(`cd "%s" && uv run pytest -q 2>&1 | tail -20`, workspace), "cd /tmp && uv run pytest -q 2>&1"} {
		if isVerificationCommand(command, workspace) {
			t.Errorf("non-verification command was recognized: %q", command)
		}
	}
	assessment := assessVerificationCommand(`UV_CACHE_DIR=.uv-cache uv run pytest -v 2>&1; echo "EXIT_CODE=$?"`, workspace)
	if assessment.Recognized || !assessment.VerificationLike || assessment.Suggestion != "UV_CACHE_DIR=.uv-cache uv run pytest -v" || !strings.Contains(assessment.Reason, "mask") {
		t.Fatalf("masked verification assessment=%+v", assessment)
	}
}

// In `A && B` the shell reports B's status unless A failed, in which case it
// reports a non-zero status. Preparation before a verifier therefore cannot
// turn a failing check into a passing one, and refusing it cost a real session
// its whole node: a sandboxed run set UV_CACHE_DIR inside the workspace, ran a
// passing suite, and was told only that no verification existed.
func TestVerificationRecognitionAcceptsPreparationBeforeTheVerifier(t *testing.T) {
	workspace := t.TempDir()
	for _, command := range []string{
		`export UV_CACHE_DIR="$(pwd)/.uv-cache" && uv run pytest -q`,
		"mkdir -p .cache && go test ./...",
		"source .venv/bin/activate && pytest -q",
		". .venv/bin/activate && python -m pytest",
		"npm ci && npm test",
		fmt.Sprintf(`cd "%s" && export FOO=bar && uv run pytest`, workspace),
	} {
		if !isVerificationCommand(command, workspace) {
			t.Errorf("preparation before a verifier was refused: %q", command)
		}
	}
	// The trailing position is what matters. A verifier that is not last, or a
	// composition that can substitute a success, stays ineligible.
	for _, command := range []string{
		"go test ./... && echo done",
		"pytest -q && rm -rf build",
		"export FOO=bar || pytest -q",
		"export FOO=bar; pytest -q",
		"export FOO=bar && pytest -q | tail -5",
		"export FOO=bar && pytest -q &",
	} {
		if isVerificationCommand(command, workspace) {
			t.Errorf("status-decoupling composition was recognized: %q", command)
		}
	}
	// Relocation is refused for a different reason: the result would not
	// describe the workspace the evidence is bound to.
	elsewhere := assessVerificationCommand("cd /tmp && uv run pytest -q", workspace)
	if elsewhere.Recognized || !strings.Contains(elsewhere.Reason, "changes directory") {
		t.Fatalf("relocated verification assessment=%+v", elsewhere)
	}
	// A refused command names the direct form wherever the verifier sits,
	// because the session that failed was never told which part was the
	// problem.
	trailing := assessVerificationCommand("export FOO=bar; uv run pytest -q", workspace)
	if trailing.Recognized || !trailing.VerificationLike || trailing.Suggestion != "uv run pytest -q" {
		t.Fatalf("trailing verifier assessment=%+v", trailing)
	}
	// A final command assembled by substitution cannot be classified at all,
	// so it is refused rather than guessed at.
	assembled := assessVerificationCommand("export FOO=bar && $(cat runner) -q", workspace)
	if assembled.Recognized {
		t.Fatalf("substituted verifier was recognized: %+v", assembled)
	}
}

// A mutating Orchestrated Goal node cannot complete without recognized
// verification, so an ecosystem missing from the recognizer is an ecosystem in
// which the mode blocks every honest change. This is the list that decides
// whether the feature is usable outside Go, Node, Rust, and bare pytest.
func TestVerificationRecognitionCoversCommonEcosystems(t *testing.T) {
	workspace := t.TempDir()
	recognized := []string{
		"tox", "nox", "python -m tox",
		"poetry run pytest", "pipenv run pytest -q", "pdm run pytest", "hatch test",
		"conda run -n analysis pytest -q", "micromamba run -n bio python -m pytest",
		`Rscript -e "testthat::test_local()"`, `Rscript -e 'devtools::test()'`, "R CMD check .",
		"bundle exec rspec", "rake test", "mix test", "composer test", "vendor/bin/phpunit",
		"swift test", "ctest --test-dir build", "deno test", "bazel test //...",
		"stack test", "cabal build", "just test", "task check", "npx vitest run",
		"./gradlew test", "mvn verify", "dotnet test",
	}
	for _, command := range recognized {
		if !isVerificationCommand(command, workspace) {
			t.Errorf("conventional verifier was not recognized: %q", command)
		}
	}
	// Breadth must not become permissiveness: a wrapper only qualifies when
	// what it wraps is itself a recognized check.
	for _, command := range []string{"conda run -n analysis python app.py", "poetry run python scripts/seed.py", "just deploy", "task release", "bundle exec rails server", "npx serve", "bazel run //app"} {
		if isVerificationCommand(command, workspace) {
			t.Errorf("non-verification wrapper command was recognized: %q", command)
		}
	}
}

// Node was the one ecosystem whose own test entry points were missing, and it
// was the worst one to miss. A directory with no package.json is exactly the
// "no applicable test surface" case where the proposal contract tells the
// first mutating node to create a focused test — so the runtime required a
// test and then refused every way of running it, and the node blocked with a
// passing suite sitting in its own evidence.
func TestNodeVerificationRecognizesItsOwnTestEntryPoints(t *testing.T) {
	workspace := t.TempDir()
	for _, command := range []string{
		"node --test",
		"node --test tests/",
		"node --test --test-reporter=spec",
		"node --check script.js",
		"node -c app.js",
		"node tests/smoke.js",
		"node test/index.js",
		"node ./spec/app.spec.mjs",
		"node smoke.js",
		"node smoke-test.js",
		"node app.test.js",
		"node scripts/db_test.js",
	} {
		if !isVerificationCommand(command, workspace) {
			t.Errorf("node test entry point was not recognized: %q", command)
		}
	}
	// Recognition is by conventional entry point, not by interpreter. `node`
	// runs anything, and an inline expression can be spelled to look like a
	// test path, so neither becomes proof.
	for _, command := range []string{
		"node index.js",
		"node build.js",
		"node scripts/seed.js",
		"node server.js --port 8080",
		`node -e "require('./tests/smoke.js')"`,
		`node --eval "process.exit(0)"`,
		"node -p tests/smoke.js",
	} {
		if isVerificationCommand(command, workspace) {
			t.Errorf("arbitrary node invocation was recognized as verification: %q", command)
		}
	}
}

// The recognizer is a finite table, so some ecosystem is always missing from
// it. What must not be missing is the explanation: a model told only that
// verification is absent, seconds after its own suite passed, has nothing to
// act on and spends its bounded remediation lease diagnosing.
func TestUnrecognizedButPassingVerificationExplainsItself(t *testing.T) {
	workspace := t.TempDir()
	assessment := assessVerificationCommand("./run-my-checks.sh", workspace)
	if assessment.Recognized || assessment.VerificationLike || !assessment.Unrecognized {
		t.Fatalf("unrecognized direct command assessment=%+v", assessment)
	}
	bare := unrecognizedVerificationNotice("./run-my-checks.sh", workspace)
	for _, want := range []string{"./run-my-checks.sh", "not a recognized verification command", "no detected verification commands", "package.json"} {
		if !strings.Contains(bare, want) {
			t.Fatalf("notice for a project with no manifest is missing %q:\n%s", want, bare)
		}
	}
	// Once the project declares an entry point, the notice stops theorizing
	// and names the command that would actually be accepted here.
	if err := os.WriteFile(filepath.Join(workspace, "package.json"), []byte(`{"scripts":{"test":"node tests/smoke.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	detected := unrecognizedVerificationNotice("./run-my-checks.sh", workspace)
	if !strings.Contains(detected, `"npm run test"`) {
		t.Fatalf("notice did not name the project's detected verifier:\n%s", detected)
	}
	if !isVerificationCommand("npm run test", workspace) {
		t.Fatal("the command the notice recommends is not itself recognized")
	}
}

// A Python repository driven by a runner reports the command that actually
// works there. Suggesting bare `pytest` in a Poetry or tox project produces a
// "no module named pytest" failure and sends the model hunting for wrappers.
func TestPythonVerificationFollowsTheProjectRunner(t *testing.T) {
	for _, testCase := range []struct{ marker, command string }{
		{"uv.lock", "uv run pytest"},
		{"poetry.lock", "poetry run pytest"},
		{"Pipfile", "pipenv run pytest"},
		{"tox.ini", "tox"},
		{"noxfile.py", "nox"},
	} {
		workspace := t.TempDir()
		for _, name := range []string{"pyproject.toml", testCase.marker} {
			if err := os.WriteFile(filepath.Join(workspace, name), []byte(""), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if !isVerificationCommand(testCase.command, workspace) {
			t.Errorf("%s project: %q was not recognized", testCase.marker, testCase.command)
		}
		_, detected := tools.DetectVerificationCommands(workspace)
		if len(detected) == 0 || detected[0].Command != testCase.command {
			t.Errorf("%s project: detected %+v, want %q first", testCase.marker, detected, testCase.command)
		}
	}
}

func TestRunCommandExplainsWhyVerificationEvidenceWasRejected(t *testing.T) {
	command := `UV_CACHE_DIR=.uv-cache uv run pytest -v 2>&1; echo "EXIT_CODE=$?"`
	registry := tools.NewRegistry(tools.Function{
		Def:    provider.ToolDefinition{Name: "run_command", InputSchema: json.RawMessage(`{"type":"object"}`)},
		Action: tools.Action{Risk: tools.RiskExecute, Summary: "run tests", Command: command},
		Run:    func(context.Context, json.RawMessage) (string, error) { return "12 passed", nil },
	})
	runtime := New(Options{Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil)})
	result, observation, err := runtime.executeTool(t.Context(), provider.ToolCall{ID: "verify", Name: "run_command", Arguments: json.RawMessage(`{}`)}, false, func(event.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Verification || !strings.Contains(result.Content, "verification evidence was not recorded") || !strings.Contains(result.Content, "UV_CACHE_DIR=.uv-cache uv run pytest -v") {
		t.Fatalf("result=%q observation=%+v", result.Content, observation)
	}
}

func TestGoalOutcomeRecognizesProviderCancellation(t *testing.T) {
	err := &provider.Error{Provider: "fixture", Operation: "chat", Kind: provider.ErrorCancelled}
	if outcome := GoalOutcomeFor(err); outcome != GoalCancelled {
		t.Fatalf("outcome=%s", outcome)
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

func TestOrchestratedGoalCompactsAtApprovalBoundaryAndAccountsTheRequest(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "bounded work", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "inspect"}}}, 1, goalgraph.Options{MaxAggregateTokens: 100_000})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{chat: func(_ int, request provider.Request) (provider.Response, error) {
		if len(request.Tools) != 0 || !strings.Contains(request.Messages[0].Content, "---") {
			t.Fatalf("approval boundary did not use the compaction request: %+v", request)
		}
		return provider.Response{Content: "bounded execution summary", Usage: provider.Usage{InputTokens: 100, OutputTokens: 20}}, nil
	}}
	a := New(Options{Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 1_000, Context: 500_000}, Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), GoalGraph: graph})
	messages := make([]provider.Message, 12)
	for i := range messages {
		messages[i] = provider.Message{Role: "user", Content: fmt.Sprintf("proposal message %d", i)}
	}
	a.SetMessages(messages)
	a.RequestGoalBoundaryCompaction()
	if !a.shouldCompact() {
		t.Fatal("newly approved graph did not request boundary compaction")
	}
	a.clearGoalBoundaryCompaction()
	if replaced, err := a.compact(t.Context(), "", nil); err != nil || replaced != 6 {
		t.Fatalf("boundary compaction replaced=%d error=%v", replaced, err)
	}
	status := graph.BudgetStatus(time.Time{})
	if client.calls != 1 || status.Usage.Iterations != 1 || status.Usage.InputTokens != 100 || status.Usage.OutputTokens != 20 {
		t.Fatalf("boundary compaction calls=%d graph usage=%+v", client.calls, status.Usage)
	}
}

func TestOrchestratedGoalCompactsBeforeCumulativeAllowanceBecomesUnusable(t *testing.T) {
	a := New(Options{ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 1_000, Context: 500_000}, Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil)})
	messages := make([]provider.Message, 10)
	for i := range messages {
		messages[i] = provider.Message{Role: "user", Content: strings.Repeat("context ", 100)}
	}
	a.SetMessages(messages)
	estimated, window := a.ContextEstimate()
	if estimated <= 0 || estimated >= window*80/100 {
		t.Fatalf("fixture accidentally reached context-window compaction: estimate=%d window=%d", estimated, window)
	}
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "bounded work", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "inspect"}}}, 1, goalgraph.Options{MaxAggregateTokens: estimated * 8})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetGoalGraph(graph); err != nil {
		t.Fatal(err)
	}
	if !a.shouldCompact() {
		t.Fatalf("graph pressure did not trigger compaction: estimate=%d remaining=%d", estimated, graph.BudgetStatus(time.Time{}).RemainingTokens)
	}
}

// Under budget pressure the compaction threshold falls as the allowance
// shrinks, so a context already reduced to a summary keeps re-triggering a
// summary that reclaims nothing. A real session (kanban10) spent six
// compactions in its final two minutes doing exactly that.
func TestCompactionDoesNotSpiralOnAnAlreadyCompactedContext(t *testing.T) {
	client := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
		return provider.Response{Content: "summary of earlier work"}, nil
	}}
	a := New(Options{Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 1_000, Context: 500_000}, Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil)})
	messages := make([]provider.Message, 10)
	for i := range messages {
		messages[i] = provider.Message{Role: "user", Content: strings.Repeat("context ", 100)}
	}
	a.SetMessages(messages)
	estimated, _ := a.ContextEstimate()
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "bounded work", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "inspect"}}}, 1, goalgraph.Options{MaxAggregateTokens: estimated * 8})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetGoalGraph(graph); err != nil {
		t.Fatal(err)
	}
	if !a.shouldCompact() {
		t.Fatal("budget pressure did not trigger the first compaction")
	}
	if _, err := a.compact(t.Context(), "", nil); err != nil {
		t.Fatal(err)
	}
	// The allowance is now smaller, so the raw threshold is easier to reach —
	// but the context is already at the floor compaction can achieve.
	if a.shouldCompact() {
		t.Fatal("compaction repeated on a context it had just reduced")
	}
	// Real growth resumes normal behavior.
	a.mu.RLock()
	grown := append([]provider.Message(nil), a.messages...)
	a.mu.RUnlock()
	for i := 0; i < 8; i++ {
		grown = append(grown, provider.Message{Role: "user", Content: strings.Repeat("more context ", 200)})
	}
	a.SetMessages(grown)
	if !a.shouldCompact() {
		t.Fatal("compaction did not resume after the context genuinely grew")
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

func TestPinnedContextTrailsEveryRequestWithoutDisturbingThePrefix(t *testing.T) {
	pinned := "Active structured plan:\n[ ] 1. inspect"
	registry := tools.NewRegistry(tools.Function{
		Def:    provider.ToolDefinition{Name: "inspect", InputSchema: json.RawMessage(`{"type":"object"}`)},
		Action: tools.Action{Risk: tools.RiskRead, Summary: "inspect"},
		Run: func(context.Context, json.RawMessage) (string, error) {
			pinned = "Active structured plan:\n[x] 1. inspect — verified"
			return "observed", nil
		},
	})
	// The plan travels in the trailing message, never in the system prompt,
	// so the request prefix stays byte-identical while the plan changes.
	var firstSystem string
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		if strings.Contains(request.System, "1. inspect") {
			t.Fatalf("plan leaked into the system prompt, which breaks the cached prefix: %s", request.System)
		}
		last := request.Messages[len(request.Messages)-1]
		if last.Role != "user" || !strings.Contains(last.Content, "Pinned session state") {
			t.Fatalf("pinned state is not the trailing message: %+v", last)
		}
		// Marked volatile so a caching adapter keeps its breakpoint behind it.
		if !last.Volatile {
			t.Fatalf("trailing state is not marked volatile: %+v", last)
		}
		switch call {
		case 1:
			firstSystem = request.System
			if !strings.Contains(last.Content, "[ ] 1. inspect") {
				t.Fatalf("initial plan missing from trailing state: %s", last.Content)
			}
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "1", Name: "inspect", Arguments: json.RawMessage(`{}`)}}}, nil
		default:
			if request.System != firstSystem {
				t.Fatalf("system prompt changed between iterations:\n--- first ---\n%s\n--- now ---\n%s", firstSystem, request.System)
			}
			if !strings.Contains(last.Content, "[x] 1. inspect — verified") || strings.Contains(last.Content, "[ ] 1. inspect") {
				t.Fatalf("updated plan was not refreshed: %s", last.Content)
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
	// The trailing state is generated per request, so a stale copy must never
	// land in the durable conversation — two plans in the history is exactly
	// the ambiguity moving it out of the system prompt is meant to avoid.
	for _, m := range a.messages {
		if strings.Contains(m.Content, "Pinned session state") {
			t.Fatalf("trailing state was persisted into the conversation: %+v", m)
		}
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

func TestDelegateRejectsInvalidWriteScopesBeforeAuthorization(t *testing.T) {
	registry := tools.NewRegistry()
	a := New(Options{
		Client: &fakeClient{}, ProviderName: "fake", Model: "m",
		ProviderConfig: appconfig.Provider{MaxTokens: 50}, Workspace: t.TempDir(),
		Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil),
	})
	a.AddDelegationTool(appconfig.Config{}, nil, NewTeam())
	raw := json.RawMessage(`{"tasks":[{"task":"write outside","write":true,"write_paths":["../outside"]}]}`)
	if _, err := registry.Assess("delegate", raw); err == nil || !strings.Contains(err.Error(), "escapes the workspace") {
		t.Fatalf("invalid write scope assessment=%v", err)
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

func TestGoalGraphCreatesVerifiedIsolatedWriterCandidateWithoutTouchingParent(t *testing.T) {
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

	graph, err := goalgraph.New(goalgraph.Spec{Goal: "add a candidate", Nodes: []goalgraph.NodeSpec{{
		ID: 1, Title: "add candidate file", Execution: goalgraph.ExecutionIsolatedWrite,
		WritePaths: []string{"new.txt"}, Acceptance: []string{"candidate passes validation"},
	}}}, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		if call == 1 {
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "write", Name: "write_file", Arguments: json.RawMessage(`{"path":"new.txt","content":"candidate\n"}`)}}}, nil
		}
		return provider.Response{Content: "added the scoped candidate file"}, nil
	}}
	registry, _, processes, err := tools.Builtins(workspace, appconfig.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(processes.StopAll)
	cfg := appconfig.Defaults()
	cfg.Permissions.Mode = "autopilot"
	runtime := New(Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: workspace, Registry: registry, Permissions: permission.New(cfg.Permissions, nil), MaxIterations: 4,
		GoalGraph: graph, GoalStateToken: func(ctx context.Context) (string, error) { return goalgraph.WorkspaceStateToken(ctx, workspace) },
	})
	team := NewTeam()
	runtime.AddDelegationTool(cfg, nil, team)
	runtime.SetGoalWriterVerifier(func(ctx context.Context, id string) ([]DelegateVerification, error) {
		status, ok := team.Get(id)
		if !ok {
			return nil, errors.New("missing retained candidate")
		}
		if out, err := exec.CommandContext(ctx, "git", "-C", status.Worktree, "diff", "--check").CombinedOutput(); err != nil {
			return nil, fmt.Errorf("git diff --check: %w: %s", err, out)
		}
		token, err := goalgraph.WorkspaceStateToken(ctx, status.Worktree)
		if err != nil {
			return nil, err
		}
		result := DelegateVerification{Command: "git diff --check", Status: "passed", StateToken: token}
		team.MarkVerificationResult(id, token, []string{result.Command}, result)
		return []DelegateVerification{result}, nil
	})

	// The wave succeeded: a verified candidate is retained and integration is
	// the user's call. The turn therefore ends with an answer naming the review
	// step, not with a blocker the operator would try to recover from.
	answer, err := runtime.Run(t.Context(), "create the candidate", func(event.Event) {})
	if err != nil {
		t.Fatalf("verified candidate wave reported an error: %v", err)
	}
	if !strings.Contains(answer, "retained for review") || !strings.Contains(answer, "reviewed integration is required") {
		t.Fatalf("run answer=%q", answer)
	}
	if outcome, _ := graph.Outcome(); outcome != goalgraph.OutcomeAwaitingReview {
		t.Fatalf("graph outcome=%q", outcome)
	}
	if _, err := os.Stat(filepath.Join(workspace, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("parent workspace was changed: %v", err)
	}
	snapshot := graph.Snapshot()
	if len(snapshot.Attempts) != 1 || snapshot.Attempts[0].State != goalgraph.AttemptCandidate || snapshot.Attempts[0].Candidate == nil || snapshot.Attempts[0].Candidate.VerificationState != "passed" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	status := team.Snapshot()[0]
	if _, err := os.Stat(filepath.Join(status.Worktree, "new.txt")); err != nil {
		t.Fatalf("retained candidate missing: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", workspace, "worktree", "remove", "--force", status.Worktree).Run()
		_ = exec.Command("git", "-C", workspace, "branch", "-D", status.Branch).Run()
	})
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
	for _, name := range []string{"delegate", "inspect_delegate_changes", "compare_delegate_changes", "verify_delegate_changes", "apply_delegate_changes", goalgraph.ReviseToolName, goalgraph.BlockToolName} {
		if !parentOnlyTool(name) {
			t.Fatalf("%s should be primary-only", name)
		}
	}
	if parentOnlyTool("read_file") {
		t.Fatal("ordinary workspace tools must remain available to delegated agents")
	}
}

func TestCanonicalDisabledToolAlsoHidesAndBlocksWrapper(t *testing.T) {
	ran := false
	registry := tools.NewRegistry(aliasedTool{
		Function: tools.Function{
			Def:    provider.ToolDefinition{Name: "verify_wrapper", InputSchema: json.RawMessage(`{"type":"object"}`)},
			Action: tools.Action{Risk: tools.RiskExecute, Summary: "wrapped command"},
			Run:    func(context.Context, json.RawMessage) (string, error) { ran = true; return "ran", nil },
		},
		permissionName: "run_command",
	})
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		if call == 1 {
			for _, definition := range request.Tools {
				if definition.Name == "verify_wrapper" {
					t.Fatal("wrapper for disabled canonical tool was exposed")
				}
			}
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "1", Name: "verify_wrapper", Arguments: json.RawMessage(`{}`)}}}, nil
		}
		if last := request.Messages[len(request.Messages)-1]; !strings.Contains(last.Content, "not available") {
			t.Fatalf("invented wrapper call was not blocked: %+v", last)
		}
		return provider.Response{Content: "done"}, nil
	}}
	a := New(Options{
		Client: client, ProviderName: "fake", Model: "m", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil),
		DisabledTools: []string{"run_command"}, MaxIterations: 4,
	})
	if _, err := a.Run(t.Context(), "test", nil); err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("wrapper bypassed disabled canonical tool")
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
	result, _, err := a.executeTool(t.Context(), call, false, func(event.Event) {})
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

// Steering must arrive as an ordinary conversational instruction that grants
// nothing, and it must say truthfully who sent it: a delegated child is
// steered by its parent, the primary agent by the person at the keyboard.
// Labelling user guidance as coming from a parent would tell the model an
// instruction originated somewhere it did not.
func TestSteeringBecomesExplicitBoundaryMessageWithoutPermissionGrant(t *testing.T) {
	for _, test := range []struct {
		name     string
		subagent bool
		want     string
	}{
		{name: "primary session is steered by the user", want: "User steering update"},
		{name: "delegated child is steered by its parent", subagent: true, want: "Parent steering update"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeClient{chat: func(_ int, request provider.Request) (provider.Response, error) {
				if len(request.Messages) != 2 {
					t.Fatalf("messages=%+v", request.Messages)
				}
				last := request.Messages[1]
				if last.Role != "user" || !strings.Contains(last.Content, test.want) || !strings.Contains(last.Content, "does not grant permissions") || !strings.Contains(last.Content, "focus on tests") {
					t.Fatalf("steering message=%+v", last)
				}
				return provider.Response{Content: "done"}, nil
			}}
			delivered := false
			agent := New(Options{
				Client: client, ProviderName: "fake", Model: "m", Workspace: t.TempDir(),
				Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil),
				Subagent: test.subagent,
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
		})
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
	if err := board.Set(plan.Plan{Goal: "ship", Steps: []plan.Step{{ID: 1, Title: "inspect", Status: "done", Evidence: "repository inspected"}, {ID: 2, Title: "implement", Status: "pending", DependsOn: []int{1}}}}); err != nil {
		t.Fatal(err)
	}
	if err := validatePlanAssignment(board, 2); err != nil {
		t.Fatal(err)
	}
}

func TestCostBudgetUsesConfiguredPricingAndStopsAfterReportedOvershoot(t *testing.T) {
	calls := 0
	client := &fakeClient{chat: func(_ int, _ provider.Request) (provider.Response, error) {
		calls++
		return provider.Response{Content: "done", Usage: provider.Usage{InputTokens: 1000, OutputTokens: 1000}}, nil
	}}
	a := New(Options{
		Client: client, ProviderName: "fake", Model: "m",
		ProviderConfig: appconfig.Provider{MaxTokens: 100, Pricing: &appconfig.Pricing{InputPerMillion: 1, OutputPerMillion: 2}},
		Workspace:      t.TempDir(), Registry: tools.NewRegistry(),
		Permissions:   permission.New(appconfig.Permissions{Mode: "ask"}, nil),
		MaxIterations: 2, CostBudgetUSD: 0.0025,
	})
	if _, err := a.Run(t.Context(), "test", nil); !errors.Is(err, ErrCostBudgetExceeded) {
		t.Fatalf("cost budget error=%v", err)
	}
	if calls != 1 {
		t.Fatalf("provider calls=%d", calls)
	}
	usage := a.Usage()
	if !usage.CostAvailable || usage.CostUSD != 0.003 {
		t.Fatalf("usage=%+v", usage)
	}
}

// TestCostSeparatesCachedReadsFromCacheWrites pins the three-rate split. A
// cache read is billed far below ordinary input and a cache write above it,
// so folding either into the plain input count misreports the run in opposite
// directions — and the reads are the whole point of asking for caching.
func TestCostSeparatesCachedReadsFromCacheWrites(t *testing.T) {
	cachedRate, writeRate := 0.1, 1.25
	pricing := &appconfig.Pricing{
		InputPerMillion:       1,
		OutputPerMillion:      2,
		CachedInputPerMillion: &cachedRate,
		CacheWritePerMillion:  &writeRate,
	}
	// 10,000 prompt tokens: 8,000 read from cache, 1,000 written to it, and
	// 1,000 ordinary. Plus 500 output tokens.
	usage := estimateCost(provider.Usage{
		InputTokens: 10_000, CachedTokens: 8_000, CacheWriteTokens: 1_000, OutputTokens: 500,
	}, pricing)
	want := (8_000*0.1 + 1_000*1.25 + 1_000*1 + 500*2) / 1_000_000
	if !usage.CostAvailable || math.Abs(usage.CostUSD-want) > 1e-12 {
		t.Fatalf("usage=%+v want cost=%v", usage, want)
	}
	// Without the split every prompt token would be ordinary input, which is
	// the estimate this test exists to stop returning.
	if naive := (10_000*1 + 500*2) / 1_000_000.0; math.Abs(usage.CostUSD-naive) < 1e-9 {
		t.Fatalf("cost was not split by rate: %v", usage.CostUSD)
	}
}

// A provider reporting counters that do not add up must not produce a
// negative ordinary remainder and a nonsense cost.
func TestCostClampsInconsistentCacheCounters(t *testing.T) {
	cachedRate := 0.1
	pricing := &appconfig.Pricing{InputPerMillion: 1, OutputPerMillion: 2, CachedInputPerMillion: &cachedRate}
	usage := estimateCost(provider.Usage{
		InputTokens: 100, CachedTokens: 900, CacheWriteTokens: 900, OutputTokens: 0,
	}, pricing)
	if usage.CostUSD < 0 {
		t.Fatalf("negative cost from inconsistent counters: %+v", usage)
	}
}

// Planning mode exists so a plan can be produced without changing anything.
// The read-only Git tools belong there; the mutating ones do not, and a new
// git_* tool is exactly the kind of addition that gets waved into the list
// beside its siblings because the names look alike.
// An automatic Orchestrated Goal writer works inside its own worktree, but a
// commit there is still an automatic publication step the mode has no
// authority for, and it moves the ref the retained candidate is measured
// against. The registry removal and this check are two layers of the same
// boundary, because a model can always fabricate a call by name.
func TestGraphWorkerCannotReachMutatingGitTools(t *testing.T) {
	workspace := t.TempDir()
	registry, _, procs, err := tools.Builtins(workspace, appconfig.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer procs.StopAll()
	worker := New(Options{Workspace: workspace, Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil), Subagent: true, GraphWorker: true})
	ordinary := New(Options{Workspace: workspace, Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil), Subagent: true})
	for _, name := range []string{"git_commit", "git_branch"} {
		tool, ok := registry.Get(name)
		if !ok {
			t.Fatalf("%s is missing from the builtin registry", name)
		}
		if worker.toolAvailable(tool, false) {
			t.Errorf("%s was available to an automatic graph writer", name)
		}
		if !ordinary.toolAvailable(tool, false) {
			t.Errorf("%s was withheld from an ordinary write delegate", name)
		}
	}
	// The withholding is narrow: a writer still needs to inspect and change
	// files inside its worktree.
	for _, name := range []string{"write_file", "edit_file", "run_command", "git_diff"} {
		tool, ok := registry.Get(name)
		if !ok {
			t.Fatalf("%s is missing from the builtin registry", name)
		}
		if !worker.toolAvailable(tool, false) {
			t.Errorf("%s was withheld from an automatic graph writer", name)
		}
	}
}

func TestPlanModeAdmitsNoMutatingGitTool(t *testing.T) {
	for _, name := range []string{"git_status", "git_diff", "git_log", "git_blame"} {
		if !planTool(name) {
			t.Errorf("%s is read-only and should be available while planning", name)
		}
	}
	for _, name := range []string{"git_commit", "git_branch"} {
		if planTool(name) {
			t.Errorf("%s mutates the repository and must not be available while planning", name)
		}
	}
}
