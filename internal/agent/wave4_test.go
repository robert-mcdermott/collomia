package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/taskmode"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

func artifactObservation(t *testing.T, workspace, path string) toolObservation {
	t.Helper()
	guard, err := tools.NewPathGuard(workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	tool := tools.ValidateArtifactTool{Guard: guard}
	raw, _ := json.Marshal(map[string]string{"path": path})
	action, err := tool.Assess(raw)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.ExecuteResultStream(t.Context(), raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	return toolObservation{Name: "validate_artifact", Action: action, ArtifactPath: path, ArtifactValidation: true, ArtifactEvidence: result.Evidence}
}

func TestArtifactFinalBytesOverrideNotesAndCommands(t *testing.T) {
	for _, mutation := range []string{"rewrite", "delete", "directory", "failed-command", "external-edit"} {
		t.Run(mutation, func(t *testing.T) {
			workspace := t.TempDir()
			path := filepath.Join(workspace, "report.txt")
			if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
				t.Fatal(err)
			}
			board := plan.NewBoard()
			c := newCompletionController(board, workspace, false, taskmode.Work)
			c.observe(artifactObservation(t, workspace, path))
			switch mutation {
			case "delete", "directory":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if mutation == "directory" {
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			default:
				if err := os.WriteFile(path, []byte("after!"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if mutation != "external-edit" {
				c.observe(toolObservation{Name: "run_command", Action: tools.Action{Risk: tools.RiskExecute}, Effects: toolEffects{Unknown: true}, Failed: mutation == "failed-command"})
			}
			if err := board.Set(plan.Plan{Goal: "report", Steps: []plan.Step{{ID: 1, Title: "write", Status: "done", Evidence: "claimed checked"}}, ValidationNote: "I manually checked everything"}); err != nil {
				t.Fatal(err)
			}
			c.observe(toolObservation{Name: "update_plan"})
			c.observe(toolObservation{Name: "run_command", Action: tools.Action{Risk: tools.RiskExecute}, Verification: true})
			decision := c.assess()
			if decision.done || !strings.Contains(decision.notice, "report.txt") || !strings.Contains(decision.notice, "no current receipt") {
				t.Fatalf("stale artifact accepted: %+v", decision)
			}
		})
	}
}

func TestArtifactRevalidationAndUnchangedEffects(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "report.txt")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := newCompletionController(plan.NewBoard(), workspace, false, taskmode.Work)
	c.observe(artifactObservation(t, workspace, path))
	for _, observation := range []toolObservation{
		{Name: "read_file", Action: tools.Action{Risk: tools.RiskRead, Paths: []string{path}}},
		{Name: "run_command", Action: tools.Action{Risk: tools.RiskExecute}, Effects: toolEffects{Unknown: true}},
		{Name: "write_file", Action: tools.Action{Risk: tools.RiskWrite, Paths: []string{path}}},
	} {
		c.observe(observation)
		if decision := c.assess(); !decision.done {
			t.Fatalf("unchanged bytes lost acceptance: %+v", decision)
		}
	}
	if err := os.WriteFile(path, []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c.assess().done {
		t.Fatal("unobserved mutation accepted")
	}
	c.observe(artifactObservation(t, workspace, path))
	if decision := c.assess(); !decision.done {
		t.Fatalf("fresh receipt rejected: %+v", decision)
	}
}

func TestArtifactBriefDistinguishesScratchAndKeepsObligations(t *testing.T) {
	workspace := t.TempDir()
	board := plan.NewBoard()
	c := newCompletionController(board, workspace, false, taskmode.Work)
	p := plan.Plan{Goal: "report", Steps: []plan.Step{{ID: 1, Title: "report", Status: "done", Evidence: "authored"}}, Artifacts: []plan.Artifact{{Path: "report.txt", Role: "deliverable"}, {Path: "helper.sh", Role: "scratch"}}}
	if err := board.Set(p); err != nil {
		t.Fatal(err)
	}
	c.observe(toolObservation{Name: "update_plan"})
	c.observe(toolObservation{Name: "write_file", Action: tools.Action{Risk: tools.RiskWrite, Paths: []string{filepath.Join(workspace, "helper.sh")}}})
	decision := c.assess()
	if decision.done || !strings.Contains(decision.notice, "report.txt") || strings.Contains(decision.notice, `"helper.sh"`) {
		t.Fatalf("bad brief gate: %+v", decision)
	}
	p.Artifacts = []plan.Artifact{{Path: "report.txt", Role: "scratch"}}
	p.ValidationNote = "no machine validation applies"
	if err := board.Set(p); err != nil {
		t.Fatal(err)
	}
	c.observe(toolObservation{Name: "update_plan"})
	if c.assess().done {
		t.Fatal("demotion waived declared output")
	}
	path := filepath.Join(workspace, "report.txt")
	if err := os.WriteFile(path, []byte("final"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.observe(artifactObservation(t, workspace, path))
	if decision = c.assess(); !decision.done {
		t.Fatalf("valid output and scratch rejected: %+v", decision)
	}
}

func TestArtifactReceiptCannotBeInventedOrWaived(t *testing.T) {
	c := newCompletionController(plan.NewBoard(), t.TempDir(), false, taskmode.Work)
	path := filepath.Join(c.workspace, "report.txt")
	c.observe(toolObservation{Name: "write_file", Action: tools.Action{Risk: tools.RiskWrite, Paths: []string{path}}})
	c.observe(toolObservation{Name: "validate_artifact", Action: tools.Action{Paths: []string{path}}, ArtifactValidation: true})
	if c.assess().done {
		t.Fatal("boolean without typed receipt accepted")
	}
}

func TestEffectScopeIndependentOfRiskAndExecutionGates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.txt")
	for _, risk := range []tools.Risk{tools.RiskRead, tools.RiskExecute, tools.RiskExternal} {
		c := newCompletionController(plan.NewBoard(), filepath.Dir(path), false, taskmode.Work)
		action := tools.Action{Risk: risk, Paths: []string{path}}
		c.observe(toolObservation{Name: "write_file", Action: action, Effects: executionEffects("write_file", action), Failed: true})
		if !c.dirty {
			t.Fatalf("possible partial write ignored for risk %s", risk)
		}
	}
	c := newCompletionController(plan.NewBoard(), filepath.Dir(path), false, taskmode.Work)
	c.observe(toolObservation{Name: "write_file", Action: tools.Action{Risk: tools.RiskWrite, Paths: []string{path}}, Failed: true, ExecutionPrevented: true})
	if c.dirty {
		t.Fatal("denied execution counted as a file mutation")
	}
	if !executionEffects("run_command", tools.Action{Risk: tools.RiskRead}).Unknown {
		t.Fatal("command falsely declared effect-free")
	}
	if !executionEffects("mcp_example", tools.Action{Risk: tools.RiskRead}).Unknown {
		t.Fatal("remote read annotation trusted as effect proof")
	}
	if executionEffects("web_fetch", tools.Action{Risk: tools.RiskExternal}).Unknown {
		t.Fatal("built-in read-only fetch treated as an uncertain mutation")
	}
}

func TestWorkRealShellMutationRequiresFreshReceipt(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "report.txt")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	guard, err := tools.NewPathGuard(workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	command, err := tools.NewRunCommandTool(workspace, nil, 4096)
	if err != nil {
		t.Fatal(err)
	}
	board := plan.NewBoard()
	client := &fakeClient{chat: func(call int, req provider.Request) (provider.Response, error) {
		switch call {
		case 1:
			return graphToolResponse("v1", "validate_artifact", `{"path":"report.txt"}`), nil
		case 2:
			return graphToolResponse("shell", "run_command", `{"command":"printf after > report.txt"}`), nil
		case 3:
			return provider.Response{Content: "Done."}, nil
		case 4:
			if !requestContains(req, "contents changed since validation") {
				t.Fatal("missing stale-digest intervention")
			}
			return graphToolResponse("v2", "validate_artifact", `{"path":"report.txt","required_text":["after"]}`), nil
		default:
			return provider.Response{Content: "Final file checked."}, nil
		}
	}}
	a := New(Options{Client: client, Workspace: workspace, Registry: tools.NewRegistry(command, tools.ValidateArtifactTool{Guard: guard}), Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil), TaskMode: taskmode.Work, CompletionPlan: board, MaxIterations: 8})
	answer, err := a.Run(t.Context(), "Update the report", nil)
	if err != nil || answer != "Final file checked." || client.calls != 5 {
		t.Fatalf("answer=%q calls=%d err=%v", answer, client.calls, err)
	}
}

func TestOpaqueFailedExecutionReportsUncertainty(t *testing.T) {
	workspace := t.TempDir()
	tool := tools.Function{Def: provider.ToolDefinition{Name: "external_action"}, Action: tools.Action{Risk: tools.RiskExternal}, Run: func(context.Context, json.RawMessage) (string, error) { return "", errors.New("response lost") }}
	a := New(Options{Workspace: workspace, Registry: tools.NewRegistry(tool), Permissions: permission.New(appconfig.Permissions{Mode: "autopilot", Rules: []appconfig.Rule{{Action: "allow", Tool: "external_action"}}}, nil)})
	result, observation, err := a.executeTool(t.Context(), provider.ToolCall{ID: "action", Name: "external_action", Arguments: json.RawMessage(`{}`)}, false, nil, func(_ event.Event) {})
	if err != nil || !observation.Failed || !observation.Effects.Unknown || !strings.Contains(result.Content, "do not blindly replay") {
		t.Fatalf("result=%+v observation=%+v err=%v", result, observation, err)
	}
}

func TestArtifactBriefScratchReceiptAndCompletedHistory(t *testing.T) {
	workspace := t.TempDir()
	board := plan.NewBoard()
	c := newCompletionController(board, workspace, false, taskmode.Work)
	p := plan.Plan{Goal: "scratch only", Steps: []plan.Step{{ID: 1, Title: "calculate", Status: "done", Evidence: "calculated"}}, Artifacts: []plan.Artifact{{Path: "helper.txt", Role: "scratch"}}}
	if err := board.Set(p); err != nil {
		t.Fatal(err)
	}
	c.observe(toolObservation{Name: "update_plan"})
	path := filepath.Join(workspace, "helper.txt")
	if err := os.WriteFile(path, []byte("scratch"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.observe(artifactObservation(t, workspace, path))
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if decision := c.assess(); !decision.done {
		t.Fatalf("scratch validation became a deliverable: %+v", decision)
	}
	p.Artifacts = []plan.Artifact{{Path: "old-report.txt", Role: "deliverable"}}
	if err := board.Set(p); err != nil {
		t.Fatal(err)
	}
	fresh := newCompletionController(board, workspace, false, taskmode.Work)
	if decision := fresh.assess(); !decision.done {
		t.Fatalf("completed historical plan blocked unrelated turn: %+v", decision)
	}
}

func TestArtifactMissingOutputEndsNeedsVerification(t *testing.T) {
	board := plan.NewBoard()
	c := newCompletionController(board, t.TempDir(), false, taskmode.Work)
	if err := board.Set(plan.Plan{Goal: "report", Steps: []plan.Step{{ID: 1, Title: "write", Status: "done", Evidence: "claimed done"}}, Artifacts: []plan.Artifact{{Path: "missing.txt", Role: "deliverable"}}, ValidationNote: "manual review"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if decision := c.assess(); decision.done || decision.notice == "" {
			t.Fatalf("unexpected intervention: %+v", decision)
		}
	}
	if decision := c.assess(); !decision.needsVerification || decision.done {
		t.Fatalf("missing output completed: %+v", decision)
	}
}
