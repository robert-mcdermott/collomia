package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/agent"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

func useRecoveryClient(r *Runtime, steps ...provider.Response) *scriptedClient {
	c := &scriptedClient{steps: steps}
	r.Agent.SetProvider("fixture", "model", appconfig.Provider{MaxTokens: 100}, c)
	return c
}
func recoveryCall(id, name, args string) provider.Response {
	return provider.Response{ToolCalls: []provider.ToolCall{{ID: id, Name: name, Arguments: json.RawMessage(args)}}}
}
func TestRecoveryObligationsSurviveTurnsAndRestart(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	r, err := New(t.Context(), Options{Autonomy: "autopilot", Workspace: workspace, TaskMode: "work"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	useRecoveryClient(r, recoveryCall("write", "write_file", `{"path":"report.txt","content":"BEFORE"}`), provider.Response{Content: "Done"}, provider.Response{Content: "Done"}, provider.Response{Content: "Done"})
	_, err = r.Agent.Run(t.Context(), "Write report.txt", r.LogEvent)
	if !errors.Is(err, agent.ErrGoalNeedsVerification) {
		t.Fatalf("unvalidated turn: %v", err)
	}
	id := r.Session.Meta.ID
	r.Close()
	next, err := New(t.Context(), Options{Autonomy: "autopilot", Workspace: workspace, Resume: id})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	useRecoveryClient(next, provider.Response{Content: "Done"}, provider.Response{Content: "Done"}, provider.Response{Content: "Done"})
	if _, err := next.Agent.Run(t.Context(), "Continue", next.LogEvent); !errors.Is(err, agent.ErrGoalNeedsVerification) {
		t.Fatalf("restart forgot dirty artifact: %v", err)
	}
	useRecoveryClient(next, recoveryCall("validate", "validate_artifact", `{"path":"report.txt","required_text":["BEFORE"]}`), provider.Response{Content: "Validated"})
	if _, err := next.Agent.Run(t.Context(), "Validate final bytes", next.LogEvent); err != nil {
		t.Fatal(err)
	}
	if state := string(next.Session.LoadCompletion()); strings.Contains(state, "report.txt") {
		t.Fatalf("done left obligations: %s", state)
	}
	useRecoveryClient(next, provider.Response{Content: "Hello"})
	if _, err := next.Agent.Run(t.Context(), "Hello", next.LogEvent); err != nil {
		t.Fatalf("unrelated Q&A regressed: %v", err)
	}
}
func TestRecoveryCheckpointRestoresAcrossRestartAndRejectsDrift(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	path := filepath.Join(workspace, "report.txt")
	if err := os.WriteFile(path, []byte("ORIGINAL"), 0600); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(t.Context(), Options{Autonomy: "autopilot", Workspace: workspace, TaskMode: "work"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	write, _ := r.Registry.Get("write_file")
	if _, err := write.Execute(t.Context(), json.RawMessage(`{"path":"report.txt","content":"FIRST"}`)); err != nil {
		t.Fatal(err)
	}
	r.LogEvent(event.New(event.KindTurnEnd))
	if _, err := write.Execute(t.Context(), json.RawMessage(`{"path":"report.txt","content":"SECOND"}`)); err != nil {
		t.Fatal(err)
	}
	r.LogEvent(event.New(event.KindTurnEnd))
	id := r.Session.Meta.ID
	r.Close()
	resumed, err := New(t.Context(), Options{Autonomy: "autopilot", Workspace: workspace, Resume: id})
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if err := os.WriteFile(path, []byte("OUTSIDE"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := resumed.RestoreCheckpoint(1); err == nil {
		t.Fatal("restore overwrote an external edit")
	}
	if resumed.Session.Meta.ID != id {
		t.Fatal("refusal changed session")
	}
	if err := os.WriteFile(path, []byte("SECOND"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := resumed.RestoreCheckpoint(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("no durable changes restored: %+v", result)
	}
	data, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(data) != "FIRST" || info.Mode().Perm() != originalInfo.Mode().Perm() {
		t.Fatal("restore lost bytes or mode")
	}
	branch := resumed.Session.Meta.ID
	resumed.Close()
	final, err := New(t.Context(), Options{Autonomy: "autopilot", Workspace: workspace, Resume: branch})
	if err != nil {
		t.Fatal(err)
	}
	defer final.Close()
	if files, _ := final.Changes.PendingSince(1); files != 0 {
		t.Fatal("restored writes reappeared after restart")
	}
	if err := final.NewSession(); err != nil {
		t.Fatal(err)
	}
	if len(final.Changes.Changed()) != 0 {
		t.Fatal("new session leaked old history")
	}
}
func TestRecoveryUnknownFailurePreventsReplayUntilUserInspection(t *testing.T) {
	isolateGlobalFiles(t)
	r, err := New(t.Context(), Options{Workspace: t.TempDir(), Autonomy: "autopilot"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	executions := 0
	r.Registry.Add(tools.Function{Def: provider.ToolDefinition{Name: "fixture_effect", InputSchema: json.RawMessage(`{"type":"object"}`)}, Action: tools.Action{Risk: tools.RiskExecute, Summary: "ambiguous effect"}, Run: func(_ context.Context, _ json.RawMessage) (string, error) {
		executions++
		return "", errors.New("result lost")
	}})
	useRecoveryClient(r, recoveryCall("effect", "fixture_effect", `{}`), provider.Response{Content: "Done"})
	if _, err := r.Agent.Run(t.Context(), "Run fixture", r.LogEvent); err == nil {
		t.Fatal("unknown effect accepted")
	}
	useRecoveryClient(r, recoveryCall("retry", "fixture_effect", `{}`))
	if _, err := r.Agent.Run(t.Context(), "Continue", r.LogEvent); err == nil {
		t.Fatal("uncertain replay accepted")
	}
	if executions != 1 {
		t.Fatalf("effect replayed %d times", executions)
	}
	if err := r.ReconcileRecovery(true, "Inspected fixture: keep its current state"); err != nil {
		t.Fatal(err)
	}
	uncertain, err := agent.CompletionUncertain(r.Session)
	if err != nil || uncertain {
		t.Fatalf("user reconciliation failed: %v", err)
	}
	if !strings.Contains(string(r.Session.LoadCompletion()), "dirty") {
		t.Fatal("acknowledgement falsely validated work")
	}
}

func TestRecoveryCancelledWriteRetainsObligationAndCheckpoint(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	r, err := New(t.Context(), Options{Workspace: workspace, TaskMode: "work", Autonomy: "autopilot"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	useRecoveryClient(r, recoveryCall("write", "write_file", `{"path":"cancelled.txt","content":"SAVED"}`), provider.Response{Content: "Done"})
	_, err = r.Agent.Run(ctx, "Write the file", func(e event.Event) {
		r.LogEvent(e)
		if e.Kind == event.KindToolResult && e.Tool != nil && e.Tool.Name == "write_file" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel not observed: %v", err)
	}
	id := r.Session.Meta.ID
	r.Close()
	next, err := New(t.Context(), Options{Workspace: workspace, Resume: id, Autonomy: "autopilot"})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if !strings.Contains(string(next.Session.LoadCompletion()), "cancelled.txt") {
		t.Fatal("cancel erased completion state")
	}
	if len(next.Changes.Changed()) != 1 {
		t.Fatal("cancel erased checkpoint")
	}
	useRecoveryClient(next, recoveryCall("validate", "validate_artifact", `{"path":"cancelled.txt"}`), provider.Response{Content: "Validated"})
	if _, err := next.Agent.Run(t.Context(), "Finish validating", next.LogEvent); err != nil {
		t.Fatal(err)
	}
}
