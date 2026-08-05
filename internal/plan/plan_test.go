package plan

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBoardValidation(t *testing.T) {
	board := NewBoard()
	if err := board.Set(Plan{Steps: []Step{{ID: 1, Title: "a", Status: "pending"}}}); err == nil {
		t.Fatal("empty goal must be rejected")
	}
	if err := board.Set(Plan{Goal: "g"}); err == nil {
		t.Fatal("empty steps must be rejected")
	}
	if err := board.Set(Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "a", Status: "banana"}}}); err == nil {
		t.Fatal("invalid status must be rejected")
	}
	if err := board.Set(Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "a", Status: "pending"}, {ID: 1, Title: "b", Status: "pending"}}}); err == nil {
		t.Fatal("duplicate ids must be rejected")
	}
	if err := board.Set(Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "a", Status: "pending", DependsOn: []int{9}}}}); err == nil {
		t.Fatal("unknown dependency must be rejected")
	}
	if err := board.Set(Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "a", Status: "pending", DependsOn: []int{1}}}}); err == nil {
		t.Fatal("self dependency must be rejected")
	}
	if err := board.Set(Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "a", Status: "pending", DependsOn: []int{2}}, {ID: 2, Title: "b", Status: "pending", DependsOn: []int{1}}}}); err == nil {
		t.Fatal("dependency cycle must be rejected")
	}
	if err := board.Set(Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "a", Status: "done"}}}); err == nil {
		t.Fatal("terminal step without evidence must be rejected")
	}
	if err := board.Set(Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "a", Status: "pending"}, {ID: 2, Title: "b", Status: "in_progress", DependsOn: []int{1}}}}); err == nil {
		t.Fatal("in-progress step with unfinished dependency must be rejected")
	}
	if err := board.Set(Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "a", Status: "pending", Acceptance: []string{""}}}}); err == nil {
		t.Fatal("empty acceptance criterion must be rejected")
	}
	if err := board.Set(Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "a", Status: "pending", Execution: "parallel_write"}}}); err == nil {
		t.Fatal("unknown execution intent must be rejected")
	}
	if err := board.Set(Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "a", Status: "pending", Execution: "isolated_write"}}}); err == nil {
		t.Fatal("isolated writer without scope must be rejected")
	}
	if err := board.Set(Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "a", Status: "pending", Execution: "isolated_write", WritePaths: []string{"*"}}}}); err == nil {
		t.Fatal("workspace-wide isolated writer must be rejected")
	}
	if err := board.Set(Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "a", Status: "pending", Execution: "read_only", WritePaths: []string{"docs/"}}}}); err == nil {
		t.Fatal("read-only step with write scope must be rejected")
	}
	if err := board.Set(Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "a", Status: "done", Evidence: "go test passed", Acceptance: []string{"tests pass"}, Execution: "read_only"}, {ID: 2, Title: "b", Status: "in_progress", DependsOn: []int{1}}}}); err != nil {
		t.Fatal(err)
	}
	rendered := board.Current().Render()
	for _, want := range []string{"Goal: g", "[x] 1. a", "go test passed", "acceptance: tests pass", "execution: read_only", "[~] 2. b", "(after 1)"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("render missing %q:\n%s", want, rendered)
		}
	}
}

func TestPlanCompletionAssessment(t *testing.T) {
	tests := []struct {
		name  string
		plan  *Plan
		state CompletionState
	}{
		{name: "none", state: CompletionReady},
		{name: "pending", plan: &Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "work", Status: "pending"}}}, state: CompletionIncomplete},
		{name: "legacy missing evidence", plan: &Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "work", Status: "done"}}}, state: CompletionIncomplete},
		{name: "done", plan: &Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "work", Status: "done", Evidence: "go test passed"}}}, state: CompletionReady},
		{name: "blocked", plan: &Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "work", Status: "blocked", Evidence: "dependency unavailable"}}}, state: CompletionBlocked},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.plan.AssessCompletion()
			if got.State != test.state {
				t.Fatalf("completion=%+v, want %s", got, test.state)
			}
		})
	}
}

func TestBoardSnapshotRevisionAndDeepCopy(t *testing.T) {
	board := NewBoard()
	_, before := board.Snapshot()
	if err := board.Set(Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "a", Status: "pending", DependsOn: []int{2}, Acceptance: []string{"observable"}, Execution: "isolated_write", WritePaths: []string{"docs/"}}, {ID: 2, Title: "b", Status: "done", Evidence: "observed"}}}); err != nil {
		t.Fatal(err)
	}
	current, after := board.Snapshot()
	if after <= before {
		t.Fatalf("revision did not advance: before=%d after=%d", before, after)
	}
	current.Steps[0].DependsOn[0] = 99
	current.Steps[0].Acceptance[0] = "mutated"
	current.Steps[0].WritePaths[0] = "mutated/"
	if got := board.Current().Steps[0].DependsOn[0]; got != 2 {
		t.Fatalf("snapshot aliased board dependency: %d", got)
	}
	if got := board.Current().Steps[0].Acceptance[0]; got != "observable" {
		t.Fatalf("snapshot aliased board acceptance: %q", got)
	}
	if got := board.Current().Steps[0].WritePaths[0]; got != "docs/" {
		t.Fatalf("snapshot aliased board write scope: %q", got)
	}
}

func TestToolAcceptsScopedIsolatedWriterIntent(t *testing.T) {
	board := NewBoard()
	tool := Tool(board)
	out, err := tool.Execute(t.Context(), json.RawMessage(`{"goal":"update docs","steps":[{"id":1,"title":"write guide","status":"pending","execution":"isolated_write","write_paths":["docs/"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	step := board.Current().Steps[0]
	if step.Execution != "isolated_write" || len(step.WritePaths) != 1 || step.WritePaths[0] != "docs/" || !strings.Contains(out, "write paths: docs/") {
		t.Fatalf("step=%+v out=%q", step, out)
	}
}

func TestToolUpdatesBoardAndNotifies(t *testing.T) {
	board := NewBoard()
	var persisted Plan
	board.OnUpdate = func(p Plan) { persisted = p }
	tool := Tool(board)
	out, err := tool.Execute(t.Context(), json.RawMessage(`{"goal":"fix bug","steps":[{"id":1,"title":"reproduce","status":"in_progress","acceptance":["failure is reproduced"],"execution":"read_only"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "reproduce") {
		t.Fatalf("out=%q", out)
	}
	if persisted.Goal != "fix bug" || len(persisted.Steps) != 1 || len(persisted.Steps[0].Acceptance) != 1 || persisted.Steps[0].Execution != "read_only" {
		t.Fatalf("persisted=%+v", persisted)
	}
}
