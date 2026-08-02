package eval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/agent"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

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
