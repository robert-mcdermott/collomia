package plan

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func TestPartialPlanUpdatePreservesFutureWorkAndCriteria(t *testing.T) {
	b := NewBoard()
	p := Plan{Goal: "build application"}
	for i := 1; i <= 7; i++ {
		p.Steps = append(p.Steps, Step{ID: i, Title: fmt.Sprintf("task %d", i), Status: "pending", Acceptance: []string{"observable result"}})
	}
	if err := b.Set(p); err != nil {
		t.Fatal(err)
	}
	tool := Tool(b)
	for _, raw := range []string{
		`{"steps":[{"id":1,"status":"done","evidence":"checked dependencies"},{"id":2,"status":"in_progress"}]}`,
		`{"goal":"build application","steps":[{"id":1,"status":"done"},{"id":2,"status":"done","evidence":"checked backend"},{"id":3,"status":"done","evidence":"tests pass"},{"id":4,"status":"in_progress"}]}`,
	} {
		if _, err := tool.Execute(t.Context(), json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
		got := b.Current()
		if len(got.Steps) != 7 || got.Steps[6].Title != "task 7" || len(got.Steps[1].Acceptance) != 1 || got.Steps[0].Evidence == "" {
			t.Fatalf("plan forgot work: %+v", got)
		}
	}
}

func TestPlanReplacementIsExplicitAndInvalidUpdatesAreAtomic(t *testing.T) {
	b := NewBoard()
	if err := b.Set(Plan{Goal: "first", Steps: []Step{{ID: 1, Title: "one", Status: "pending"}, {ID: 2, Title: "two", Status: "pending"}}}); err != nil {
		t.Fatal(err)
	}
	before, revision := b.Snapshot()
	for _, raw := range []string{`{"steps":[{"id":1,"status":"done"}]}`, `{"steps":[{"id":3,"status":"pending"}]}`, `{"steps":[{"id":1},{"id":1}]}`, `{"steps":[{"id":1,"depends_on":[99]}]}`} {
		if err := b.Update(json.RawMessage(raw)); err == nil {
			t.Fatal("invalid update accepted", raw)
		}
		after, rev := b.Snapshot()
		if rev != revision || !reflect.DeepEqual(before, after) {
			t.Fatal("failed update mutated plan")
		}
	}
	if err := b.Update(json.RawMessage(`{"replace":true,"goal":"next task","steps":[{"id":3,"title":"new scope","status":"pending"}]}`)); err != nil {
		t.Fatal(err)
	}
	if got := b.Current(); got.Goal != "next task" || len(got.Steps) != 1 || got.Steps[0].ID != 3 {
		t.Fatal(got)
	}
}
