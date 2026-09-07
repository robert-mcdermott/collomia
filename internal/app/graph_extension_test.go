package app

import (
	"testing"

	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/plan"
)

func TestGraphExtensionThroughSavedSession(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	r, err := New(t.Context(), Options{Workspace: workspace, OrchestratedGoal: &plan.Plan{Goal: "research", Steps: []plan.Step{{ID: 1, Title: "inspect", Execution: "read_only", Acceptance: []string{"grounded research"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for i := 0; i < 2; i++ {
		claims, err := r.GoalGraph.StartReadyReads(t.Context(), "", 1)
		if err != nil || len(claims) != 1 {
			t.Fatalf("claim: %v %v", claims, err)
		}
		status := "error"
		if i == 1 {
			status = "budget_exhausted"
		}
		if err := r.GoalGraph.FinishRead(t.Context(), goalgraph.ReadResult{AttemptID: claims[0].Attempt.ID, Status: status, Error: "worker allowance exhausted", InputTokens: 32000, Iterations: 1}); err != nil {
			t.Fatal(err)
		}
	}
	id := r.Session.Meta.ID
	r.Close()
	resumed, err := New(t.Context(), Options{Workspace: workspace, Resume: id})
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if resumed.GoalGraph != nil {
		t.Fatal("saved graph activated without user consent")
	}
	_, prompt, runnable, err := resumed.ExtendOrchestratedGoal(t.Context())
	if err != nil || !runnable || prompt == "" {
		t.Fatalf("extension: runnable=%v err=%v", runnable, err)
	}
	s := resumed.GoalGraph.Snapshot()
	if s.Nodes[0].State != goalgraph.NodeReady || s.ReadFanout.UsedTokens != 64000 || s.ReadFanout.MaxTokens != 128000 || s.MaxAttemptsPerNode != 4 {
		t.Fatalf("resume lost grant or usage: %+v", s)
	}
	claims, err := resumed.GoalGraph.StartReadyReads(t.Context(), "", 1)
	if err != nil || len(claims) != 1 || claims[0].Attempt.Number != 3 {
		t.Fatalf("new attempt: %v %v", claims, err)
	}
	if err := resumed.GoalGraph.FinishRead(t.Context(), goalgraph.ReadResult{AttemptID: claims[0].Attempt.ID, Status: "done", Summary: "fresh research", ToolSuccesses: 1, InputTokens: 100, Iterations: 1}); err != nil {
		t.Fatal(err)
	}
	if outcome, _ := resumed.GoalGraph.Outcome(); outcome != goalgraph.OutcomeDone {
		t.Fatal(outcome)
	}
	resumed.Close()
	finished, err := New(t.Context(), Options{Workspace: workspace, Resume: id})
	if err != nil {
		t.Fatal(err)
	}
	defer finished.Close()
	if _, err := finished.OrchestratedGoalStatus(0); err != nil {
		t.Fatal(err)
	}
}
