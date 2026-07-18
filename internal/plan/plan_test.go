package plan

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBoardValidation(t *testing.T) {
	board := NewBoard()
	if err := board.Set(Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "a", Status: "banana"}}}); err == nil {
		t.Fatal("invalid status must be rejected")
	}
	if err := board.Set(Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "a", Status: "pending"}, {ID: 1, Title: "b", Status: "pending"}}}); err == nil {
		t.Fatal("duplicate ids must be rejected")
	}
	if err := board.Set(Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "a", Status: "pending", DependsOn: []int{9}}}}); err == nil {
		t.Fatal("unknown dependency must be rejected")
	}
	if err := board.Set(Plan{Goal: "g", Steps: []Step{{ID: 1, Title: "a", Status: "done", Evidence: "go test passed"}, {ID: 2, Title: "b", Status: "in_progress", DependsOn: []int{1}}}}); err != nil {
		t.Fatal(err)
	}
	rendered := board.Current().Render()
	for _, want := range []string{"Goal: g", "[x] 1. a", "go test passed", "[~] 2. b", "(after 1)"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("render missing %q:\n%s", want, rendered)
		}
	}
}

func TestToolUpdatesBoardAndNotifies(t *testing.T) {
	board := NewBoard()
	var persisted Plan
	board.OnUpdate = func(p Plan) { persisted = p }
	tool := Tool(board)
	out, err := tool.Execute(t.Context(), json.RawMessage(`{"goal":"fix bug","steps":[{"id":1,"title":"reproduce","status":"in_progress"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "reproduce") {
		t.Fatalf("out=%q", out)
	}
	if persisted.Goal != "fix bug" || len(persisted.Steps) != 1 {
		t.Fatalf("persisted=%+v", persisted)
	}
}
