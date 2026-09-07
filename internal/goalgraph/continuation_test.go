package goalgraph

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReadBudgetExtensionAfterTwoAttempts(t *testing.T) {
	g, err := New(Spec{Goal: "finish application", Nodes: []NodeSpec{
		{ID: 1, Title: "backend"},
		{ID: 2, Title: "investigate frontend", Execution: ExecutionReadOnly, DependsOn: []int{1}},
		{ID: 3, Title: "frontend", DependsOn: []int{2}},
	}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, a, err := g.StartNext(t.Context(), "state")
	if err != nil {
		t.Fatal(err)
	}
	if d := successfulRead(t, g, a, "state"); d.Kind != DecisionAccepted {
		t.Fatal(d)
	}
	// First attempt has a recoverable failure; second spends the last read
	// token. This is the shape that made Kanban21's user extension fail.
	for i := 0; i < 2; i++ {
		claims, err := g.StartReadyReads(t.Context(), "state", 1)
		if err != nil || len(claims) != 1 {
			t.Fatalf("claim %d: %v %v", i, claims, err)
		}
		status := "error"
		used := 100
		if i == 1 {
			status = "budget_exhausted"
			used = defaultMaxReadTokens - 100
		}
		if err := g.FinishRead(t.Context(), ReadResult{AttemptID: claims[0].Attempt.ID, Status: status, Error: "read allowance spent", InputTokens: used, Iterations: 1, WorkspaceToken: "state"}); err != nil {
			t.Fatal(err)
		}
	}
	before := g.Snapshot()
	if before.Outcome != OutcomeBudgetExhausted || before.ReadFanout.UsedTokens != defaultMaxReadTokens {
		t.Fatal("fixture did not exhaust read allowance")
	}
	for grant := 1; grant <= 3; grant++ {
		// Exercise the same path after disk restore, not just live memory.
		raw, _ := json.Marshal(g.Snapshot())
		var saved Snapshot
		if err := json.Unmarshal(raw, &saved); err != nil {
			t.Fatal(err)
		}
		g, err = Restore(saved, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err = g.ExtendBudget(t.Context(), "explicit user grant"); err != nil {
			t.Fatal(err)
		}
		after := g.Snapshot()
		if !reflect.DeepEqual(after.Attempts, before.Attempts) || after.ReadFanout.UsedTokens != before.ReadFanout.UsedTokens || after.Nodes[0].State != NodeDone {
			t.Fatal("extension rewrote history or lost accepted node")
		}
		if err = g.Activate(t.Context()); err != nil {
			t.Fatal(err)
		}
		claims, err := g.StartReadyReads(t.Context(), "state", 1)
		if err != nil || len(claims) != 1 || claims[0].TokenBudget <= 0 || claims[0].Attempt.ID == before.Attempts[len(before.Attempts)-1].ID {
			t.Fatalf("not resumable after grant: %v %v", claims, err)
		}
		status := "budget_exhausted"
		summary := ""
		successes := 0
		if grant == 3 {
			status = "done"
			summary = "current grounded research"
			successes = 1
		}
		if err = g.FinishRead(t.Context(), ReadResult{AttemptID: claims[0].Attempt.ID, Status: status, Summary: summary, ToolSuccesses: successes, InputTokens: 10, Iterations: 1, WorkspaceToken: "state"}); err != nil {
			t.Fatal(err)
		}
		before = g.Snapshot()
	}
	n, a, err := g.StartNext(t.Context(), "state")
	if err != nil || n.ID != 3 {
		t.Fatalf("consumer not unlocked: %v %v", n, err)
	}
	if d := successfulRead(t, g, a, "state"); d.Kind != DecisionDone {
		t.Fatal(d)
	}
	if err = ValidateSnapshot(g.Snapshot()); err != nil {
		t.Fatal(err)
	}
}

func TestBudgetGrantRefusalAndPersistenceAreAtomic(t *testing.T) {
	for _, kind := range []string{"ambiguous", "writer", "persistence"} {
		t.Run(kind, func(t *testing.T) {
			g, _ := New(Spec{Goal: "work", Nodes: []NodeSpec{{ID: 1, Title: "work"}}}, 1, Options{})
			_, a, _ := g.StartNext(t.Context(), "state")
			if kind == "ambiguous" {
				if err := g.BeginTool(t.Context(), a.ID, ToolAction{Tool: "run_command", Risk: "execute", NonReplayable: true}, "state"); err != nil {
					t.Fatal(err)
				}
			}
			if err := g.ExhaustBudget(t.Context(), "stop"); err != nil {
				t.Fatal(err)
			}
			if kind == "writer" {
				g.state.Attempts[0].Worktree = "/retained/candidate"
			}
			if kind == "persistence" {
				g.SetPersister(func(context.Context, Snapshot, bool) error { return errors.New("disk full") })
			}
			before := g.Snapshot()
			g.DrainUpdates()
			if err := g.ExtendBudget(t.Context(), "grant"); err == nil {
				t.Fatal("unsafe/undurable extension succeeded")
			}
			if !reflect.DeepEqual(before, g.Snapshot()) || len(g.DrainUpdates()) != 0 {
				t.Fatal("failed grant changed state or emitted success")
			}
		})
	}
}

func TestReadSchedulingWaitsForIndependentPrimaryWork(t *testing.T) {
	g, _ := New(Spec{Goal: "application", Nodes: []NodeSpec{
		{ID: 1, Title: "scaffold"},
		{ID: 2, Title: "backend", DependsOn: []int{1}},
		{ID: 3, Title: "frontend research", Execution: ExecutionReadOnly},
		{ID: 4, Title: "frontend", DependsOn: []int{2, 3}},
	}}, 1, Options{})
	for _, want := range []int{1, 2} {
		claims, err := g.StartReadyReads(t.Context(), "state", 2)
		if err != nil || len(claims) != 0 {
			t.Fatalf("premature read before node %d: %v %v", want, claims, err)
		}
		n, a, err := g.StartNext(t.Context(), "state")
		if err != nil || n.ID != want {
			t.Fatalf("primary %d: %v %v", want, n, err)
		}
		successfulRead(t, g, a, "state")
	}
	claims, err := g.StartReadyReads(t.Context(), "state", 2)
	if err != nil || len(claims) != 1 || claims[0].Node.ID != 3 {
		t.Fatalf("needed research not scheduled: %v %v", claims, err)
	}
}

func TestReadWallExcludesIdleAndCountsConcurrentWaveOnce(t *testing.T) {
	now := time.Now().UTC()
	g, _ := New(Spec{Goal: "research", Nodes: []NodeSpec{
		{ID: 1, Title: "one", Execution: ExecutionReadOnly},
		{ID: 2, Title: "two", Execution: ExecutionReadOnly},
		{ID: 3, Title: "later", Execution: ExecutionReadOnly, DependsOn: []int{1, 2}},
	}}, 1, Options{Now: func() time.Time { return now }, MaxReadWallSeconds: 10})
	claims, err := g.StartReadyReads(t.Context(), "state", 2)
	if err != nil || len(claims) != 2 {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Second)
	for _, c := range claims {
		if err := g.FinishRead(t.Context(), ReadResult{AttemptID: c.Attempt.ID, Status: "done", Summary: "observed", ToolSuccesses: 1, WorkspaceToken: "state"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := g.readActiveWallLocked(now); got != 3*time.Second {
		t.Fatalf("parallel wall double counted: %s", got)
	}
	if err := g.RequestPause(t.Context(), "user pause"); err != nil {
		t.Fatal(err)
	}
	if err := g.ReachPause(t.Context()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if err := g.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	claims, err = g.StartReadyReads(t.Context(), "state", 1)
	if err != nil || len(claims) != 1 || claims[0].TimeoutSeconds != 7 {
		t.Fatalf("idle time charged: %v %v", claims, err)
	}
}

func TestExhaustedStaleNodeIsExtendableInsteadOfStranded(t *testing.T) {
	g, _ := New(Spec{Goal: "research", Nodes: []NodeSpec{{ID: 1, Title: "inspect", Execution: ExecutionReadOnly}}}, 1, Options{MaxAttemptsPerNode: 1})
	claims, _ := g.StartReadyReads(t.Context(), "before", 1)
	if err := g.FinishRead(t.Context(), ReadResult{AttemptID: claims[0].Attempt.ID, Status: "done", Summary: "grounded", ToolSuccesses: 1, WorkspaceToken: "before"}); err != nil {
		t.Fatal(err)
	}
	// An unrelated changed workspace invalidates the accepted observation.
	g.state.Outcome = ""
	g.state.Reason = ""
	_, _ = g.StartReadyReads(t.Context(), "after", 1)
	_, _, err := g.StartNext(t.Context(), "after")
	if !errors.Is(err, ErrGraphTerminal) || g.Snapshot().Outcome != OutcomeBudgetExhausted {
		t.Fatalf("stranded node: %v %v", g.Snapshot().Nodes, err)
	}
	if !strings.Contains(g.Snapshot().Reason, "attempt allowance") {
		t.Fatal(g.Snapshot().Reason)
	}
	if err := g.ExtendBudget(t.Context(), "user grant"); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyAggregateGrantsDoNotWidenWorkerLeasesOnRestore(t *testing.T) {
	g, _ := New(Spec{Goal: "inspect", Nodes: []NodeSpec{{ID: 1, Title: "read", Execution: ExecutionReadOnly}}}, 1, Options{})
	s := g.Snapshot()
	s.AggregateBudget.Extensions = 2
	s.AggregateBudget.MaxIterations *= 3
	s.AggregateBudget.MaxTokens *= 3
	s.AggregateBudget.MaxCostUSD *= 3
	s.AggregateBudget.MaxActiveWallSeconds *= 3
	g, err := Restore(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := g.StartReadyReads(t.Context(), "state", 1)
	if err != nil || len(claims) != 1 || claims[0].MaxIterations != defaultReadTaskIterations || claims[0].TimeoutSeconds != defaultReadTaskWallSeconds {
		t.Fatalf("legacy restore widened worker lease: %v %v", claims, err)
	}
	for _, count := range []int{-1, maxConfigurableGraphIterations + 1} {
		s.AggregateBudget.Extensions = count
		s.AggregateBudget.Grant = AggregateGrant{}
		if _, err := Restore(s, Options{}); err == nil {
			t.Fatalf("accepted corrupt grant count %d", count)
		}
	}
}

func TestInterruptedReadDowntimeDoesNotSpendWallAllowance(t *testing.T) {
	now := time.Now().UTC()
	g, _ := New(Spec{Goal: "inspect", Nodes: []NodeSpec{{ID: 1, Title: "read", Execution: ExecutionReadOnly}}}, 1, Options{Now: func() time.Time { return now }, MaxReadWallSeconds: 10})
	if _, err := g.StartReadyReads(t.Context(), "state", 1); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Second)
	if err := g.Persist(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	s := g.Snapshot()
	now = now.Add(24 * time.Hour)
	g, err := Restore(s, Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Recover(t.Context(), "state"); err != nil {
		t.Fatal(err)
	}
	if got := g.readActiveWallLocked(now); got != 3*time.Second {
		t.Fatalf("restore charged downtime: %s", got)
	}
	claims, err := g.StartReadyReads(t.Context(), "state", 1)
	if err != nil || len(claims) != 1 || claims[0].TimeoutSeconds != 7 {
		t.Fatalf("restored allowance: %v %v", claims, err)
	}
}
