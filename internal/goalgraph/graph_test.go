package goalgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type graphFixture struct {
	now       time.Time
	ids       int
	snapshots []Snapshot
	durable   []bool
}

func (f *graphFixture) options() Options {
	return Options{
		Now: func() time.Time {
			f.now = f.now.Add(time.Second)
			return f.now
		},
		NewID: func(prefix string) string {
			f.ids++
			return fmt.Sprintf("%s-%d", prefix, f.ids)
		},
		Persist: func(_ context.Context, snapshot Snapshot, durable bool) error {
			f.snapshots = append(f.snapshots, snapshot)
			f.durable = append(f.durable, durable)
			return nil
		},
	}
}

func testSpec() Spec {
	return Spec{Goal: "ship", Nodes: []NodeSpec{
		{ID: 1, Title: "inspect"},
		{ID: 2, Title: "implement", DependsOn: []int{1}},
		{ID: 3, Title: "document", DependsOn: []int{1}},
		{ID: 4, Title: "finish", DependsOn: []int{2, 3}},
	}}
}

func successfulRead(t *testing.T, graph *Graph, attempt Attempt, token string) Decision {
	t.Helper()
	if err := graph.BeginTool(t.Context(), attempt.ID, ToolAction{Tool: "read_file", Risk: "read", Summary: "read fixture"}, token); err != nil {
		t.Fatal(err)
	}
	if err := graph.FinishTool(t.Context(), attempt.ID, ToolResult{Tool: "read_file", Risk: "read", Summary: "fixture inspected", WorkspaceToken: token}); err != nil {
		t.Fatal(err)
	}
	decision, err := graph.ProposeCompletion(t.Context(), "inspection complete", token)
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

func TestGraphSelectsDependencyReadyNodesInStableOrder(t *testing.T) {
	fixture := &graphFixture{now: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}
	graph, err := New(testSpec(), 7, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	if got := graph.Snapshot().Nodes[0].State; got != NodeReady {
		t.Fatalf("first node state=%s", got)
	}
	node, attempt, err := graph.StartNext(t.Context(), "state-1")
	if err != nil || node.ID != 1 || attempt.Number != 1 {
		t.Fatalf("first selection node=%+v attempt=%+v err=%v", node, attempt, err)
	}
	if decision := successfulRead(t, graph, attempt, "state-1"); decision.Kind != DecisionAccepted {
		t.Fatalf("first decision=%+v", decision)
	}
	node, attempt, err = graph.StartNext(t.Context(), "state-1")
	if err != nil || node.ID != 2 {
		t.Fatalf("stable ready order selected node=%+v err=%v", node, err)
	}
	if got := graph.Snapshot().Nodes[2].State; got != NodeReady {
		t.Fatalf("parallel logical sibling state=%s", got)
	}
	if len(fixture.snapshots) == 0 || !slices.Contains(fixture.durable, true) {
		t.Fatal("graph transitions were not persisted durably")
	}
}

func TestGraphClaimsTwoStableReadWorkersBeforePrimaryLane(t *testing.T) {
	fixture := &graphFixture{}
	graph, err := New(Spec{Goal: "diagnose then repair", Nodes: []NodeSpec{
		{ID: 1, Title: "inspect API", Execution: ExecutionReadOnly, Acceptance: []string{"API evidence is grounded"}},
		{ID: 2, Title: "inspect tests", Execution: ExecutionReadOnly, Acceptance: []string{"test evidence is grounded"}},
		{ID: 3, Title: "repair", DependsOn: []int{1, 2}, Execution: ExecutionPrimary},
	}}, 1, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	claims, err := graph.StartReadyReads(t.Context(), "state", 2)
	if err != nil || len(claims) != 2 || claims[0].Node.ID != 1 || claims[1].Node.ID != 2 {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	if err := ValidateSnapshot(graph.Snapshot()); err != nil {
		t.Fatalf("two running read attempts are not structurally valid: %v", err)
	}
	for index, claim := range claims {
		err := graph.FinishRead(t.Context(), ReadResult{
			AttemptID: claim.Attempt.ID, WorkerID: fmt.Sprintf("worker-%d", index+1), Status: "done",
			Summary: fmt.Sprintf("grounded result %d", index+1), Evidence: []string{"read_file: completed — repository evidence"},
			ToolSuccesses: 1, WorkspaceToken: "state", InputTokens: 100, OutputTokens: 20,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	node, _, err := graph.StartNext(t.Context(), "state")
	if err != nil || node.ID != 3 || node.Execution != ExecutionPrimary {
		t.Fatalf("dependent primary node=%+v err=%v", node, err)
	}
	snapshot := graph.Snapshot()
	if snapshot.ReadFanout.Starts != 2 || snapshot.ReadFanout.UsedTokens != 240 {
		t.Fatalf("fan-out usage=%+v", snapshot.ReadFanout)
	}
	status, err := graph.Inspect(1)
	if err != nil || !strings.Contains(status, "worker worker-1") || !strings.Contains(status, "delegate_read") {
		t.Fatalf("read inspection err=%v\n%s", err, status)
	}
}

func TestGraphClaimsOneVerifiedDisjointWriterWaveAndStopsForReview(t *testing.T) {
	graph, err := New(Spec{Goal: "implement independent changes", Nodes: []NodeSpec{
		{ID: 1, Title: "change API", Execution: ExecutionIsolatedWrite, WritePaths: []string{"internal/api/"}, Acceptance: []string{"API tests pass"}},
		{ID: 2, Title: "change docs", Execution: ExecutionIsolatedWrite, WritePaths: []string{"docs/"}, Acceptance: []string{"docs checks pass"}},
		{ID: 3, Title: "integrate", Execution: ExecutionPrimary, DependsOn: []int{1, 2}},
	}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	base := WriterBase{WorkspaceToken: "parent-state", Commit: "abcdef", Clean: true}
	claims, err := graph.StartReadyWriters(t.Context(), base, 2)
	if err != nil || len(claims) != 2 || claims[0].Node.ID != 1 || claims[1].Node.ID != 2 {
		t.Fatalf("writer claims=%+v err=%v", claims, err)
	}
	if claims[0].Attempt.BaseCommit != base.Commit || claims[1].Attempt.BaseWorkspaceToken != base.WorkspaceToken {
		t.Fatalf("claims do not share immutable base: %+v", claims)
	}
	for index, claim := range claims {
		path := "internal/api/api.go"
		if index == 1 {
			path = "docs/guide.md"
		}
		token := fmt.Sprintf("child-state-%d", index+1)
		err := graph.FinishWriter(t.Context(), WriterResult{
			AttemptID: claim.Attempt.ID, WorkerID: fmt.Sprintf("writer-%d", index+1), Status: "done",
			Summary: "implemented and checked candidate", Evidence: []string{"edit_file: completed — candidate changed"},
			WritePaths: claim.WritePaths, ChangedFiles: []string{path}, Worktree: "/tmp/candidate", Branch: fmt.Sprintf("collomia/writer-%d", index+1), BaseCommit: base.Commit,
			ParentWorkspaceToken: base.WorkspaceToken, VerificationState: "passed", VerificationToken: token,
			Verification: []CandidateVerification{{Command: "go test ./...", Status: "passed", StateToken: token}},
			Iterations:   2, InputTokens: 100, OutputTokens: 20,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot := graph.Snapshot()
	if outcome, reason := graph.Outcome(); outcome != OutcomeBlocked || !strings.Contains(reason, "reviewed integration is required") {
		t.Fatalf("outcome=%q reason=%q", outcome, reason)
	}
	if snapshot.Nodes[0].State != NodeBlocked || snapshot.Nodes[1].State != NodeBlocked || snapshot.Nodes[2].State == NodeDone {
		t.Fatalf("candidate wave advanced logical completion: %+v", snapshot.Nodes)
	}
	if snapshot.Attempts[0].State != AttemptCandidate || snapshot.Attempts[0].Candidate == nil || snapshot.Attempts[1].Candidate == nil {
		t.Fatalf("retained candidates=%+v", snapshot.Attempts)
	}
	if snapshot.Accounting.AutomaticWriters.Iterations != 4 || snapshot.Accounting.AutomaticWriters.InputTokens != 200 {
		t.Fatalf("writer accounting=%+v", snapshot.Accounting.AutomaticWriters)
	}
	if err := ValidateSnapshot(snapshot); err != nil {
		t.Fatalf("candidate snapshot is invalid: %v", err)
	}
	corrupted := graph.Snapshot()
	corrupted.Attempts[0].Candidate.Verification[0].StateToken = "different-child-state"
	if err := ValidateSnapshot(corrupted); err == nil || !strings.Contains(err.Error(), "failed or stale verification") {
		t.Fatalf("corrupted candidate verification was accepted: %v", err)
	}
}

func TestExecutableSpecRejectsUnschedulableCandidateTopology(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec Spec
		want string
	}{
		{
			name: "mixed primary and candidate lanes",
			spec: Spec{Goal: "build app", Nodes: []NodeSpec{
				{ID: 1, Title: "scaffold", Execution: ExecutionPrimary},
				{ID: 2, Title: "backend candidate", Execution: ExecutionIsolatedWrite, WritePaths: []string{"app/"}},
			}},
			want: "cannot be mixed with primary",
		},
		{
			name: "candidate used as dependency",
			spec: Spec{Goal: "candidate preview", Nodes: []NodeSpec{
				{ID: 1, Title: "backend candidate", Execution: ExecutionIsolatedWrite, WritePaths: []string{"app/"}},
				{ID: 2, Title: "inspect candidate", Execution: ExecutionReadOnly, DependsOn: []int{1}},
			}},
			want: "must be a terminal leaf",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateExecutableSpec(tc.spec); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	valid := Spec{Goal: "candidate preview", Nodes: []NodeSpec{
		{ID: 1, Title: "inspect", Execution: ExecutionReadOnly},
		{ID: 2, Title: "docs candidate", Execution: ExecutionIsolatedWrite, WritePaths: []string{"docs/"}, DependsOn: []int{1}},
	}}
	if err := ValidateExecutableSpec(valid); err != nil {
		t.Fatalf("valid candidate-only topology rejected: %v", err)
	}
}

func TestGraphWriterClaimsRejectDirtyBaseAndSerializeOverlappingScopes(t *testing.T) {
	spec := Spec{Goal: "bounded writers", Nodes: []NodeSpec{
		{ID: 1, Title: "broad", Execution: ExecutionIsolatedWrite, WritePaths: []string{"internal/"}},
		{ID: 2, Title: "nested", Execution: ExecutionIsolatedWrite, WritePaths: []string{"internal/api/"}},
		{ID: 3, Title: "independent", Execution: ExecutionIsolatedWrite, WritePaths: []string{"docs/"}},
	}}
	graph, err := New(spec, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := graph.StartReadyWriters(t.Context(), WriterBase{WorkspaceToken: "state", Commit: "abc", Clean: true}, 2)
	if err != nil || len(claims) != 2 || claims[0].Node.ID != 1 || claims[1].Node.ID != 3 {
		t.Fatalf("overlapping scopes were co-scheduled: claims=%+v err=%v", claims, err)
	}

	dirty, err := New(spec, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dirty.StartReadyWriters(t.Context(), WriterBase{WorkspaceToken: "state", Commit: "abc", Clean: false, DirtyPaths: []string{"pyproject.toml", "app/"}, DirtyCount: 2}, 2); !errors.Is(err, ErrGraphTerminal) {
		t.Fatalf("dirty base error=%v", err)
	}
	if outcome, reason := dirty.Outcome(); outcome != OutcomeBlocked || !strings.Contains(reason, "dirty (2 paths: pyproject.toml, app/)") || !strings.Contains(reason, "commit or reconcile") {
		t.Fatalf("dirty outcome=%q reason=%q", outcome, reason)
	}
}

func TestGraphWriterCandidateRejectsStaleParentAndScopeViolations(t *testing.T) {
	for _, tc := range []struct {
		name       string
		parent     string
		changed    []string
		violations []string
		kind       FailureKind
	}{
		{name: "stale parent", parent: "new-parent", changed: []string{"internal/api.go"}, kind: FailureWorkspaceStale},
		{name: "scope violation", parent: "parent", changed: []string{"README.md"}, violations: []string{"README.md"}, kind: FailureTool},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graph, err := New(Spec{Goal: "write", Nodes: []NodeSpec{{ID: 1, Title: "change API", Execution: ExecutionIsolatedWrite, WritePaths: []string{"internal/"}}}}, 1, Options{})
			if err != nil {
				t.Fatal(err)
			}
			claims, err := graph.StartReadyWriters(t.Context(), WriterBase{WorkspaceToken: "parent", Commit: "abc", Clean: true}, 1)
			if err != nil {
				t.Fatal(err)
			}
			err = graph.FinishWriter(t.Context(), WriterResult{
				AttemptID: claims[0].Attempt.ID, WorkerID: "writer", Status: "done", Summary: "candidate",
				WritePaths: claims[0].WritePaths, ChangedFiles: tc.changed, ScopeViolations: tc.violations,
				Worktree: "/tmp/candidate", Branch: "collomia/writer", BaseCommit: "abc", ParentWorkspaceToken: tc.parent,
				VerificationState: "passed", VerificationToken: "child", Verification: []CandidateVerification{{Command: "go test ./...", Status: "passed", StateToken: "child"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			attempt := graph.Snapshot().Attempts[0]
			if attempt.State == AttemptCandidate || len(attempt.Failures) != 1 || attempt.Failures[0].Kind != tc.kind {
				t.Fatalf("attempt=%+v", attempt)
			}
		})
	}
}

func TestGraphRecoveryNeverReplaysInterruptedIsolatedWriter(t *testing.T) {
	graph, err := New(Spec{Goal: "write", Nodes: []NodeSpec{{ID: 1, Title: "write docs", Execution: ExecutionIsolatedWrite, WritePaths: []string{"docs/"}}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if claims, err := graph.StartReadyWriters(t.Context(), WriterBase{WorkspaceToken: "parent", Commit: "abc", Clean: true}, 1); err != nil || len(claims) != 1 {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	restored, err := Restore(graph.Snapshot(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Recover(t.Context(), "parent"); err != nil {
		t.Fatal(err)
	}
	snapshot := restored.Snapshot()
	if outcome, reason := restored.Outcome(); outcome != OutcomeBlocked || !strings.Contains(reason, "may have changed its retained worktree") {
		t.Fatalf("outcome=%q reason=%q", outcome, reason)
	}
	if snapshot.Nodes[0].State != NodeBlocked || snapshot.Attempts[0].State != AttemptInterrupted || len(snapshot.Attempts[0].Failures) != 1 || snapshot.Attempts[0].Failures[0].Kind != FailureInterruptedAction {
		t.Fatalf("recovered snapshot=%+v", snapshot)
	}
	if err := restored.RetryNode(t.Context(), 1, "try again"); !errors.Is(err, ErrUnsafeNodeRetry) {
		t.Fatalf("interrupted writer retry error=%v", err)
	}
}

func TestGraphIsolatedWriterRequiresExplicitNarrowScope(t *testing.T) {
	for _, scopes := range [][]string{nil, {"*"}} {
		_, err := New(Spec{Goal: "write", Nodes: []NodeSpec{{ID: 1, Title: "write", Execution: ExecutionIsolatedWrite, WritePaths: scopes}}}, 1, Options{})
		if err == nil {
			t.Fatalf("invalid scope %v was accepted", scopes)
		}
	}
	if _, err := New(Spec{Goal: "read", Nodes: []NodeSpec{{ID: 1, Title: "read", Execution: ExecutionReadOnly, WritePaths: []string{"docs/"}}}}, 1, Options{}); err == nil {
		t.Fatal("read_only node accepted a write scope")
	}
}

func TestGraphAggregateAccountingSeparatesPrimaryAndAutomaticReads(t *testing.T) {
	started := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	fixture := &graphFixture{now: started}
	opts := fixture.options()
	opts.AccountingStarted = started
	opts.InitialPrimary = WorkUsage{Iterations: 1, InputTokens: 100, OutputTokens: 20, CostUSD: 0.001, CostAvailable: true, CostEstimated: true}
	graph, err := New(Spec{Goal: "inspect then repair", Nodes: []NodeSpec{
		{ID: 1, Title: "inspect", Execution: ExecutionReadOnly},
		{ID: 2, Title: "repair", DependsOn: []int{1}},
	}}, 1, opts)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := graph.StartReadyReads(t.Context(), "state", 2)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	if err := graph.FinishRead(t.Context(), ReadResult{
		AttemptID: claims[0].Attempt.ID, WorkerID: "reader", Status: "done", Summary: "grounded",
		Evidence: []string{"read_file: completed — evidence"}, ToolSuccesses: 1, WorkspaceToken: "state",
		Iterations: 2, InputTokens: 200, OutputTokens: 40, CostUSD: 0.002,
	}); err != nil {
		t.Fatal(err)
	}
	_, primaryAttempt, err := graph.StartNext(t.Context(), "state")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.RecordPrimaryUsage(t.Context(), WorkUsage{Iterations: 1, InputTokens: 50, OutputTokens: 10, CostUSD: 0.0005, CostAvailable: true, CostEstimated: true}); err != nil {
		t.Fatal(err)
	}

	usage := graph.UsageTotals(started.Add(30 * time.Second))
	if usage.Primary.Iterations != 2 || usage.Primary.InputTokens != 150 || usage.Primary.OutputTokens != 30 || math.Abs(usage.Primary.CostUSD-0.0015) > 1e-12 || !usage.Primary.CostAvailable {
		t.Fatalf("primary usage=%+v", usage.Primary)
	}
	if usage.AutomaticReads.Iterations != 2 || usage.AutomaticReads.InputTokens != 200 || usage.AutomaticReads.OutputTokens != 40 || usage.AutomaticReads.CostAvailable {
		t.Fatalf("automatic-read usage=%+v", usage.AutomaticReads)
	}
	if usage.Total.Iterations != 4 || usage.Total.InputTokens != 350 || usage.Total.OutputTokens != 70 || usage.Total.CostAvailable || usage.Elapsed != 30*time.Second {
		t.Fatalf("total usage=%+v elapsed=%s", usage.Total, usage.Elapsed)
	}
	snapshot := graph.Snapshot()
	primary := snapshot.Attempts[len(snapshot.Attempts)-1]
	if primary.ID != primaryAttempt.ID || primary.Iterations != 1 || primary.InputTokens != 50 || primary.OutputTokens != 10 {
		t.Fatalf("primary attempt accounting=%+v", primary)
	}
	status, err := graph.Inspect(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Aggregate model work:", "Primary (proposal + serial lane):", "Automatic reads:", "4 provider iterations", "cost unavailable"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status missing %q:\n%s", want, status)
		}
	}

	legacy := graph.Snapshot()
	legacy.Accounting = Accounting{}
	restored, err := Restore(legacy, Options{})
	if err != nil {
		t.Fatal(err)
	}
	restoredUsage := restored.UsageTotals(started.Add(30 * time.Second))
	if restoredUsage.Primary.InputTokens != 50 || restoredUsage.AutomaticReads.InputTokens != 200 || restored.Snapshot().Accounting.Started.IsZero() {
		t.Fatalf("legacy reconstructed usage=%+v snapshot=%+v", restoredUsage, restored.Snapshot().Accounting)
	}
	corrupt := graph.Snapshot()
	corrupt.Accounting.Primary.Iterations = -1
	if _, err := Restore(corrupt, Options{}); err == nil || !strings.Contains(err.Error(), "aggregate accounting") {
		t.Fatalf("invalid accounting restore error=%v", err)
	}
	corruptProgress := graph.Snapshot()
	corruptProgress.Attempts[len(corruptProgress.Attempts)-1].LastProgressIteration = corruptProgress.Attempts[len(corruptProgress.Attempts)-1].Iterations + 1
	if _, err := Restore(corruptProgress, Options{}); err == nil || !strings.Contains(err.Error(), "invalid attempt") {
		t.Fatalf("invalid progress restore error=%v", err)
	}
}

func TestGraphAggregateEnvelopeEnforcesEveryMachineObservableBound(t *testing.T) {
	start := time.Date(2026, 8, 2, 15, 0, 0, 0, time.UTC)
	newGraph := func(t *testing.T, opts Options) (*Graph, *time.Time) {
		t.Helper()
		now := start
		opts.Now = func() time.Time { return now }
		graph, err := New(Spec{Goal: "bounded", Nodes: []NodeSpec{{ID: 1, Title: "work"}}}, 1, opts)
		if err != nil {
			t.Fatal(err)
		}
		return graph, &now
	}

	t.Run("iterations stop admission at the exact boundary", func(t *testing.T) {
		graph, _ := newGraph(t, Options{MaxAggregateIterations: 2, InitialPrimary: WorkUsage{Iterations: 1}})
		if _, _, err := graph.StartNext(t.Context(), "state"); err != nil {
			t.Fatal(err)
		}
		if err := graph.RecordPrimaryUsage(t.Context(), WorkUsage{Iterations: 1}); err != nil {
			t.Fatal(err)
		}
		if err := graph.EnforceAggregateBudget(t.Context()); !errors.Is(err, ErrAggregateBudget) {
			t.Fatalf("iteration admission error=%v", err)
		}
		if outcome, reason := graph.Outcome(); outcome != OutcomeBudgetExhausted || !strings.Contains(reason, "2/2") {
			t.Fatalf("iteration outcome=%q reason=%q", outcome, reason)
		}
	})

	t.Run("tokens and priced cost stop immediately after an overage", func(t *testing.T) {
		for name, tc := range map[string]struct {
			opts   Options
			usage  WorkUsage
			reason string
		}{
			"tokens": {opts: Options{MaxAggregateTokens: 10}, usage: WorkUsage{Iterations: 1, InputTokens: 8, OutputTokens: 3}, reason: "token"},
			"cost":   {opts: Options{MaxAggregateCostUSD: 0.001}, usage: WorkUsage{Iterations: 1, InputTokens: 1, CostUSD: 0.002, CostAvailable: true, CostEstimated: true}, reason: "cost"},
		} {
			t.Run(name, func(t *testing.T) {
				graph, _ := newGraph(t, tc.opts)
				if _, _, err := graph.StartNext(t.Context(), "state"); err != nil {
					t.Fatal(err)
				}
				if err := graph.RecordPrimaryUsage(t.Context(), tc.usage); !errors.Is(err, ErrAggregateBudget) {
					t.Fatalf("overage error=%v", err)
				}
				if outcome, gotReason := graph.Outcome(); outcome != OutcomeBudgetExhausted || !strings.Contains(gotReason, tc.reason) {
					t.Fatalf("outcome=%q reason=%q", outcome, gotReason)
				}
			})
		}
	})

	t.Run("a terminal parallel wave retains every completed worker usage", func(t *testing.T) {
		graph, err := New(Spec{Goal: "bounded read wave", Nodes: []NodeSpec{
			{ID: 1, Title: "one", Execution: ExecutionReadOnly},
			{ID: 2, Title: "two", Execution: ExecutionReadOnly},
		}}, 1, Options{MaxAggregateTokens: 10})
		if err != nil {
			t.Fatal(err)
		}
		claims, err := graph.StartReadyReads(t.Context(), "state", 2)
		if err != nil || len(claims) != 2 {
			t.Fatalf("claims=%+v error=%v", claims, err)
		}
		first := ReadResult{AttemptID: claims[0].Attempt.ID, WorkerID: "one", Iterations: 1, InputTokens: 11}
		if err := graph.RecordReadUsage(t.Context(), first); !errors.Is(err, ErrAggregateBudget) {
			t.Fatalf("first overage error=%v", err)
		}
		second := ReadResult{AttemptID: claims[1].Attempt.ID, WorkerID: "two", Iterations: 1, InputTokens: 7}
		if err := graph.RecordReadUsage(t.Context(), second); !errors.Is(err, ErrAggregateBudget) {
			t.Fatalf("late sibling accounting error=%v", err)
		}
		snapshot := graph.Snapshot()
		if snapshot.Accounting.AutomaticReads.Iterations != 2 || snapshot.Accounting.AutomaticReads.InputTokens != 18 || !snapshot.Attempts[1].UsageRecorded {
			t.Fatalf("parallel terminal accounting=%+v attempts=%+v", snapshot.Accounting.AutomaticReads, snapshot.Attempts)
		}
	})

	t.Run("unpriced work remains bounded by tokens and iterations", func(t *testing.T) {
		graph, _ := newGraph(t, Options{MaxAggregateCostUSD: 0.001})
		if _, _, err := graph.StartNext(t.Context(), "state"); err != nil {
			t.Fatal(err)
		}
		if err := graph.RecordPrimaryUsage(t.Context(), WorkUsage{Iterations: 1, InputTokens: 100, CostUSD: 10}); err != nil {
			t.Fatalf("unpriced cost was treated as enforceable: %v", err)
		}
		status := graph.BudgetStatus(time.Time{})
		if status.CostEnforceable || status.RemainingTokens != defaultMaxGraphTokens-100 {
			t.Fatalf("unpriced budget status=%+v", status)
		}
	})

	t.Run("active wall excludes reached pause time", func(t *testing.T) {
		graph, now := newGraph(t, Options{MaxActiveWallSeconds: 10})
		*now = now.Add(5 * time.Second)
		if err := graph.RequestPause(t.Context(), "inspect"); err != nil {
			t.Fatal(err)
		}
		*now = now.Add(time.Hour)
		if err := graph.Resume(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := graph.BudgetStatus(*now).ActiveElapsed; got != 5*time.Second {
			t.Fatalf("paused time consumed active wall: %s", got)
		}
		*now = now.Add(5 * time.Second)
		if err := graph.EnforceAggregateBudget(t.Context()); !errors.Is(err, ErrAggregateBudget) {
			t.Fatalf("active-wall error=%v", err)
		}
	})

	t.Run("restore freezes active time until explicit activation", func(t *testing.T) {
		graph, now := newGraph(t, Options{MaxActiveWallSeconds: 10})
		*now = now.Add(4 * time.Second)
		if err := graph.Persist(t.Context(), true); err != nil {
			t.Fatal(err)
		}
		snapshot := graph.Snapshot()
		restoredNow := now.Add(time.Hour)
		restored, err := Restore(snapshot, Options{Now: func() time.Time { return restoredNow }})
		if err != nil {
			t.Fatal(err)
		}
		if got := restored.BudgetStatus(restoredNow).ActiveElapsed; got != 4*time.Second {
			t.Fatalf("restore counted downtime: %s", got)
		}
		if err := restored.Activate(t.Context()); err != nil {
			t.Fatal(err)
		}
		restoredNow = restoredNow.Add(6 * time.Second)
		if err := restored.EnforceAggregateBudget(t.Context()); !errors.Is(err, ErrAggregateBudget) {
			t.Fatalf("reactivated active-wall error=%v", err)
		}
	})
}

func TestGraphAggregateAllowanceNarrowsAutomaticReadClaims(t *testing.T) {
	now := time.Date(2026, 8, 2, 16, 0, 0, 0, time.UTC)
	graph, err := New(Spec{Goal: "bounded fanout", Nodes: []NodeSpec{
		{ID: 1, Title: "one", Execution: ExecutionReadOnly},
		{ID: 2, Title: "two", Execution: ExecutionReadOnly},
	}}, 1, Options{
		Now: func() time.Time { return now }, MaxAggregateIterations: 5, MaxAggregateTokens: 100,
		MaxAggregateCostUSD: 2, MaxActiveWallSeconds: 20,
		InitialPrimary: WorkUsage{Iterations: 1, InputTokens: 50, OutputTokens: 10, CostUSD: 1, CostAvailable: true, CostEstimated: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := graph.StartReadyReads(t.Context(), "state", 2)
	if err != nil || len(claims) != 2 {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	for _, claim := range claims {
		if claim.TokenBudget != 20 || claim.MaxIterations != 2 || math.Abs(claim.CostBudgetUSD-0.5) > 1e-12 || claim.TimeoutSeconds != 20 {
			t.Fatalf("claim did not inherit remaining aggregate allowance: %+v", claim)
		}
		if claim.Attempt.TokenBudget != claim.TokenBudget || claim.Attempt.IterationBudget != claim.MaxIterations || claim.Attempt.CostBudgetUSD != claim.CostBudgetUSD {
			t.Fatalf("attempt did not durably retain claim bounds: %+v", claim.Attempt)
		}
	}

	widened := graph.Snapshot()
	widened.AggregateBudget.MaxTokens = defaultMaxGraphTokens + 1
	if _, err := Restore(widened, Options{}); err == nil || !strings.Contains(err.Error(), "aggregate budget") {
		t.Fatalf("widened stored envelope was accepted: %v", err)
	}

	legacy := graph.Snapshot()
	legacy.AggregateBudget.MaxTokens = 192_000
	restoredLegacy, err := Restore(legacy, Options{})
	if err != nil {
		t.Fatalf("previous fixed envelope no longer restores: %v", err)
	}
	if got := restoredLegacy.BudgetStatus(time.Time{}).Limits.MaxTokens; got != 192_000 {
		t.Fatalf("restore widened previous graph budget to %d", got)
	}

	oneToken, err := New(Spec{Goal: "one token remains", Nodes: []NodeSpec{
		{ID: 1, Title: "one", Execution: ExecutionReadOnly},
		{ID: 2, Title: "two", Execution: ExecutionReadOnly},
	}}, 1, Options{MaxAggregateTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	claims, err = oneToken.StartReadyReads(t.Context(), "state", 2)
	if err != nil || len(claims) != 1 || claims[0].TokenBudget != 1 {
		t.Fatalf("one remaining token admitted more than one worker: claims=%+v err=%v", claims, err)
	}
}

func TestGraphLeavesPrimaryOnlyAndDependencySerialWorkSerial(t *testing.T) {
	primary, err := New(Spec{Goal: "small change", Nodes: []NodeSpec{{ID: 1, Title: "implement"}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := primary.StartReadyReads(t.Context(), "state", 2)
	if err != nil || len(claims) != 0 {
		t.Fatalf("primary-only graph claimed reads: claims=%+v err=%v", claims, err)
	}
	if node, _, err := primary.StartNext(t.Context(), "state"); err != nil || node.ID != 1 {
		t.Fatalf("primary selection node=%+v err=%v", node, err)
	}

	serial, err := New(Spec{Goal: "serial reads", Nodes: []NodeSpec{
		{ID: 1, Title: "first", Execution: ExecutionReadOnly},
		{ID: 2, Title: "second", Execution: ExecutionReadOnly, DependsOn: []int{1}},
	}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	claims, err = serial.StartReadyReads(t.Context(), "state", 2)
	if err != nil || len(claims) != 1 || claims[0].Node.ID != 1 {
		t.Fatalf("serial first wave=%+v err=%v", claims, err)
	}
}

func TestGraphReadFanoutCancellationAndAggregateStartBound(t *testing.T) {
	spec := Spec{Goal: "inspect", Nodes: []NodeSpec{
		{ID: 1, Title: "one", Execution: ExecutionReadOnly},
		{ID: 2, Title: "two", Execution: ExecutionReadOnly},
	}}
	cancelled, err := New(spec, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if claims, err := cancelled.StartReadyReads(t.Context(), "state", 2); err != nil || len(claims) != 2 {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	if err := cancelled.Cancel(t.Context(), "user cancelled"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSnapshot(cancelled.Snapshot()); err != nil {
		t.Fatalf("cancelled fan-out snapshot invalid: %v", err)
	}

	bounded, err := New(spec, 1, Options{MaxReadStarts: 1})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := bounded.StartReadyReads(t.Context(), "state", 2)
	if err != nil || len(claims) != 1 {
		t.Fatalf("bounded first wave=%+v err=%v", claims, err)
	}
	if err := bounded.FinishRead(t.Context(), ReadResult{AttemptID: claims[0].Attempt.ID, WorkerID: "worker", Status: "done", Summary: "grounded", Evidence: []string{"read_file passed"}, ToolSuccesses: 1, WorkspaceToken: "state"}); err != nil {
		t.Fatal(err)
	}
	if _, err := bounded.StartReadyReads(t.Context(), "state", 2); !errors.Is(err, ErrGraphTerminal) {
		t.Fatalf("aggregate start bound error=%v", err)
	}
	if outcome, _ := bounded.Outcome(); outcome != OutcomeBudgetExhausted {
		t.Fatalf("outcome=%q", outcome)
	}
}

func TestGraphRetriesReadWhoseWorkspaceFreshnessChanged(t *testing.T) {
	graph, err := New(Spec{Goal: "inspect", Nodes: []NodeSpec{{ID: 1, Title: "inspect", Execution: ExecutionReadOnly}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := graph.StartReadyReads(t.Context(), "before", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.FinishRead(t.Context(), ReadResult{AttemptID: claims[0].Attempt.ID, WorkerID: "worker", Status: "done", Summary: "stale facts", Evidence: []string{"read_file passed"}, ToolSuccesses: 1, WorkspaceToken: "after"}); err != nil {
		t.Fatal(err)
	}
	snapshot := graph.Snapshot()
	if snapshot.Nodes[0].State != NodeReady || snapshot.Attempts[0].State != AttemptRetryable {
		t.Fatalf("freshness mismatch was not retried: node=%+v attempt=%+v", snapshot.Nodes[0], snapshot.Attempts[0])
	}
}

func TestGraphRejectsFailedOnlyDelegateEvidence(t *testing.T) {
	graph, err := New(Spec{Goal: "inspect", Nodes: []NodeSpec{{ID: 1, Title: "inspect", Execution: ExecutionReadOnly}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := graph.StartReadyReads(t.Context(), "state", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.FinishRead(t.Context(), ReadResult{
		AttemptID: claims[0].Attempt.ID, WorkerID: "worker", Status: "done",
		Summary:  "the missing file probably contains the answer",
		Evidence: []string{"read_file: failed — file does not exist"}, WorkspaceToken: "state",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := graph.Snapshot()
	if snapshot.Nodes[0].State != NodeReady || snapshot.Attempts[0].State != AttemptRetryable || snapshot.Attempts[0].ToolSuccesses != 0 {
		t.Fatalf("failed-only evidence was accepted: node=%+v attempt=%+v", snapshot.Nodes[0], snapshot.Attempts[0])
	}
	if len(snapshot.Attempts[0].Failures) != 1 || snapshot.Attempts[0].Failures[0].Kind != FailureTool {
		t.Fatalf("failed-only evidence classification=%+v", snapshot.Attempts[0].Failures)
	}
}

func TestGraphInspectShowsBoundedOperatorEvidence(t *testing.T) {
	fixture := &graphFixture{}
	graph, err := New(Spec{Goal: "inspect", Nodes: []NodeSpec{{ID: 1, Title: "read state", Acceptance: []string{"facts are grounded"}}}}, 1, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := graph.StartNext(t.Context(), "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.FinishTool(t.Context(), attempt.ID, ToolResult{Tool: "read_file", Risk: "read", Summary: "read config.go", WorkspaceToken: "workspace"}); err != nil {
		t.Fatal(err)
	}
	status, err := graph.Inspect(1)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Experimental Orchestrated Goal", "one serial primary lane", "acceptance: facts are grounded", attempt.ID, "evidence: tool_result", "read config.go"} {
		if !strings.Contains(status, want) {
			t.Fatalf("inspection missing %q:\n%s", want, status)
		}
	}
	if _, err := graph.Inspect(99); err == nil {
		t.Fatal("unknown node inspection succeeded")
	}
}

func TestPrimaryMutationRequiresFreshCombinedVerification(t *testing.T) {
	fixture := &graphFixture{}
	graph, err := New(Spec{Goal: "change", Nodes: []NodeSpec{{ID: 1, Title: "implement"}}}, 1, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := graph.StartNext(t.Context(), "before")
	if err != nil {
		t.Fatal(err)
	}
	write := ToolAction{Tool: "write_file", Risk: "write", Summary: "write main.go", PotentialMutation: true, NonReplayable: true}
	if err := graph.BeginTool(t.Context(), attempt.ID, write, "before"); err != nil {
		t.Fatal(err)
	}
	if !fixture.durable[len(fixture.durable)-1] || fixture.snapshots[len(fixture.snapshots)-1].Attempts[0].PendingAction == nil {
		t.Fatal("potential mutation was not write-ahead persisted")
	}
	if err := graph.FinishTool(t.Context(), attempt.ID, ToolResult{Tool: "write_file", Risk: "write", Summary: "wrote main.go", WorkspaceToken: "after"}); err != nil {
		t.Fatal(err)
	}
	decision, err := graph.ProposeCompletion(t.Context(), "implemented", "after")
	if err != nil || decision.Kind != DecisionContinue {
		t.Fatalf("unverified mutation decision=%+v err=%v", decision, err)
	}
	verify := ToolAction{Tool: "run_command", Risk: "execute", Summary: "go test ./...", Command: "go test ./...", PotentialMutation: true, NonReplayable: true}
	if err := graph.BeginTool(t.Context(), attempt.ID, verify, "after"); err != nil {
		t.Fatal(err)
	}
	if err := graph.FinishTool(t.Context(), attempt.ID, ToolResult{Tool: "run_command", Risk: "execute", Summary: "go test ./... passed", Command: "go test ./...", Verification: true, WorkspaceToken: "verified"}); err != nil {
		t.Fatal(err)
	}
	decision, err = graph.ProposeCompletion(t.Context(), "implemented and tested", "verified")
	if err != nil || decision.Kind != DecisionDone || decision.Outcome != OutcomeDone {
		t.Fatalf("verified mutation decision=%+v err=%v", decision, err)
	}
	if snapshot := graph.Snapshot(); snapshot.Outcome != OutcomeDone || snapshot.Nodes[0].State != NodeDone || len(snapshot.Evidence) < 3 {
		t.Fatalf("terminal snapshot=%+v", snapshot)
	}
}

func TestCompletionGapSnapshotRoundTripsAndLegacyOmissionIsSafe(t *testing.T) {
	graph, err := New(Spec{Goal: "change", Nodes: []NodeSpec{{ID: 1, Title: "implement"}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := graph.StartNext(t.Context(), "before")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.RecordPrimaryUsage(t.Context(), WorkUsage{Iterations: 1}); err != nil {
		t.Fatal(err)
	}
	if err := graph.BeginTool(t.Context(), attempt.ID, ToolAction{Tool: "write_file", Risk: "write", PotentialMutation: true, NonReplayable: true}, "before"); err != nil {
		t.Fatal(err)
	}
	if err := graph.FinishTool(t.Context(), attempt.ID, ToolResult{Tool: "write_file", Risk: "write", Summary: "changed", WorkspaceToken: "after"}); err != nil {
		t.Fatal(err)
	}
	if err := graph.RecordPrimaryUsage(t.Context(), WorkUsage{Iterations: 1}); err != nil {
		t.Fatal(err)
	}
	if decision, err := graph.ProposeCompletion(t.Context(), "done", "after"); err != nil || decision.Kind != DecisionContinue {
		t.Fatalf("decision=%+v error=%v", decision, err)
	}
	snapshot := graph.Snapshot()
	if snapshot.Attempts[0].CompletionGapIteration != 2 || !strings.Contains(snapshot.Attempts[0].CompletionGap, "recognized verification") {
		t.Fatalf("completion gap=%+v", snapshot.Attempts[0])
	}
	if _, err := Restore(snapshot, Options{}); err != nil {
		t.Fatalf("restore completion gap: %v", err)
	}
	legacy := snapshot
	legacy.Attempts[0].CompletionGap = ""
	legacy.Attempts[0].CompletionGapIteration = 0
	if _, err := Restore(legacy, Options{}); err != nil {
		t.Fatalf("restore omitted legacy gap: %v", err)
	}
}

func TestUnchangedPotentialEffectsDoNotStaleWorkspaceVerification(t *testing.T) {
	fixture := &graphFixture{}
	graph, err := New(Spec{Goal: "change", Nodes: []NodeSpec{{ID: 1, Title: "implement and verify"}}}, 1, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := graph.StartNext(t.Context(), "before")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.BeginTool(t.Context(), attempt.ID, ToolAction{Tool: "write_file", Risk: "write", PotentialMutation: true, NonReplayable: true}, "before"); err != nil {
		t.Fatal(err)
	}
	if err := graph.FinishTool(t.Context(), attempt.ID, ToolResult{Tool: "write_file", Risk: "write", Summary: "wrote app", WorkspaceToken: "after"}); err != nil {
		t.Fatal(err)
	}
	if err := graph.BeginTool(t.Context(), attempt.ID, ToolAction{Tool: "run_command", Risk: "execute", Command: "go test ./...", PotentialMutation: true, NonReplayable: true}, "after"); err != nil {
		t.Fatal(err)
	}
	if err := graph.FinishTool(t.Context(), attempt.ID, ToolResult{Tool: "run_command", Risk: "execute", Command: "go test ./...", Summary: "tests passed", Verification: true, WorkspaceToken: "after"}); err != nil {
		t.Fatal(err)
	}
	if err := graph.BeginTool(t.Context(), attempt.ID, ToolAction{Tool: "start_process", Risk: "execute", PotentialMutation: true, NonReplayable: true}, "after"); err != nil {
		t.Fatal(err)
	}
	if err := graph.FinishTool(t.Context(), attempt.ID, ToolResult{Tool: "start_process", Risk: "execute", Summary: "server started", WorkspaceToken: "after"}); err != nil {
		t.Fatal(err)
	}
	decision, err := graph.ProposeCompletion(t.Context(), "implemented, tested, and smoke checked", "after")
	if err != nil || decision.Kind != DecisionDone {
		t.Fatalf("unchanged external effect made verification stale: decision=%+v error=%v", decision, err)
	}
	snapshot := graph.Snapshot()
	if snapshot.MutationGeneration != 3 || snapshot.Attempts[0].MutationGeneration != 1 {
		t.Fatalf("write-ahead and observed generations were conflated: graph=%d attempt=%d", snapshot.MutationGeneration, snapshot.Attempts[0].MutationGeneration)
	}
	for _, evidence := range snapshot.Evidence {
		if evidence.Kind == EvidenceVerification && evidence.MutationGeneration != 1 {
			t.Fatalf("verification was bound to potential rather than observed mutation: %+v", evidence)
		}
	}
}

func TestLegacyActiveAttemptWithEvidenceReceivesFreshProgressLease(t *testing.T) {
	graph, err := New(Spec{Goal: "inspect", Nodes: []NodeSpec{{ID: 1, Title: "inspect"}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := graph.StartNext(t.Context(), "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.RecordPrimaryUsage(t.Context(), WorkUsage{Iterations: 5}); err != nil {
		t.Fatal(err)
	}
	if err := graph.FinishTool(t.Context(), attempt.ID, ToolResult{Tool: "read_file", Risk: "read", Summary: "observed", WorkspaceToken: "workspace"}); err != nil {
		t.Fatal(err)
	}
	legacy := graph.Snapshot()
	legacy.Attempts[0].LastProgressIteration = 0
	restored, err := Restore(legacy, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.RecordPrimaryUsage(t.Context(), WorkUsage{Iterations: 1}); err != nil {
		t.Fatal(err)
	}
	got := restored.Snapshot().Attempts[0]
	if got.Iterations != 6 || got.LastProgressIteration != 5 {
		t.Fatalf("legacy progress lease was not initialized from durable evidence: %+v", got)
	}
}

func TestChangedWorkspaceStillStalesEarlierVerification(t *testing.T) {
	graph, err := New(Spec{Goal: "change", Nodes: []NodeSpec{{ID: 1, Title: "implement"}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := graph.StartNext(t.Context(), "before")
	if err != nil {
		t.Fatal(err)
	}
	base := "before"
	for _, action := range []struct {
		tool, token  string
		verification bool
	}{
		{tool: "write_file", token: "after-write"},
		{tool: "run_command", token: "after-write", verification: true},
		{tool: "run_command", token: "after-later-change"},
	} {
		if err := graph.BeginTool(t.Context(), attempt.ID, ToolAction{Tool: action.tool, Risk: "execute", PotentialMutation: true, NonReplayable: true}, base); err != nil {
			t.Fatal(err)
		}
		if err := graph.FinishTool(t.Context(), attempt.ID, ToolResult{Tool: action.tool, Risk: "execute", Summary: "completed", Verification: action.verification, WorkspaceToken: action.token}); err != nil {
			t.Fatal(err)
		}
		base = action.token
	}
	decision, err := graph.ProposeCompletion(t.Context(), "done", "after-later-change")
	if err != nil || decision.Kind != DecisionContinue || !strings.Contains(decision.Notice, "no successful recognized verification") {
		t.Fatalf("later workspace change retained stale verification: decision=%+v error=%v", decision, err)
	}
}

func TestSuccessfulWriteWithoutCombinedWorkspaceChangeCannotComplete(t *testing.T) {
	graph, err := New(Spec{Goal: "change", Nodes: []NodeSpec{{ID: 1, Title: "implement"}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := graph.StartNext(t.Context(), "unchanged")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.BeginTool(t.Context(), attempt.ID, ToolAction{Tool: "write_file", Risk: "write", Summary: "write", PotentialMutation: true, NonReplayable: true}, "unchanged"); err != nil {
		t.Fatal(err)
	}
	if err := graph.FinishTool(t.Context(), attempt.ID, ToolResult{Tool: "write_file", Risk: "write", Summary: "no-op write", WorkspaceToken: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	if err := graph.BeginTool(t.Context(), attempt.ID, ToolAction{Tool: "run_command", Risk: "execute", Summary: "test", PotentialMutation: true, NonReplayable: true}, "unchanged"); err != nil {
		t.Fatal(err)
	}
	if err := graph.FinishTool(t.Context(), attempt.ID, ToolResult{Tool: "run_command", Risk: "execute", Summary: "passed", Verification: true, WorkspaceToken: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	decision, err := graph.ProposeCompletion(t.Context(), "done", "unchanged")
	if err != nil || decision.Kind != DecisionContinue || !strings.Contains(decision.Notice, "no combined-workspace change") {
		t.Fatalf("decision=%+v error=%v", decision, err)
	}
}

func TestMutationIsBlockedWithoutGitBackedState(t *testing.T) {
	graph, err := New(Spec{Goal: "change", Nodes: []NodeSpec{{ID: 1, Title: "implement"}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := graph.StartNext(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	err = graph.BeginTool(t.Context(), attempt.ID, ToolAction{Tool: "write_file", Risk: "write", PotentialMutation: true, NonReplayable: true}, "")
	if !errors.Is(err, ErrWorkspaceState) {
		t.Fatalf("missing state error=%v", err)
	}
	decision, err := graph.ProposeCompletion(t.Context(), "done", "")
	if err != nil || decision.Kind != DecisionBlocked || decision.Outcome != OutcomeBlocked {
		t.Fatalf("missing-state decision=%+v err=%v", decision, err)
	}
}

func TestRecoverNeverReplaysAmbiguousMutation(t *testing.T) {
	fixture := &graphFixture{}
	graph, err := New(Spec{Goal: "change", Nodes: []NodeSpec{{ID: 1, Title: "implement"}}}, 1, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := graph.StartNext(t.Context(), "before")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.BeginTool(t.Context(), attempt.ID, ToolAction{Tool: "run_command", Risk: "execute", Summary: "generate", PotentialMutation: true, NonReplayable: true}, "before"); err != nil {
		t.Fatal(err)
	}
	stored := fixture.snapshots[len(fixture.snapshots)-1]
	restored, err := Restore(stored, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Recover(t.Context(), "before"); err != nil {
		t.Fatal(err)
	}
	snapshot := restored.Snapshot()
	if snapshot.Nodes[0].State != NodeBlocked || snapshot.Outcome != OutcomeBlocked || snapshot.Attempts[0].State != AttemptInterrupted {
		t.Fatalf("ambiguous mutation recovery=%+v", snapshot)
	}
	if _, _, err := restored.StartNext(t.Context(), "before"); !errors.Is(err, ErrGraphTerminal) {
		t.Fatalf("ambiguous mutation became executable: %v", err)
	}
}

func TestRecoverReadOnlyAttemptCreatesANewBoundedAttempt(t *testing.T) {
	fixture := &graphFixture{}
	graph, err := New(Spec{Goal: "inspect", Nodes: []NodeSpec{{ID: 1, Title: "read"}}}, 1, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := graph.StartNext(t.Context(), "state")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.BeginTool(t.Context(), attempt.ID, ToolAction{Tool: "read_file", Risk: "read", Summary: "read"}, "state"); err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(graph.Snapshot(), fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Recover(t.Context(), "state"); err != nil {
		t.Fatal(err)
	}
	_, next, err := restored.StartNext(t.Context(), "state")
	if err != nil || next.Number != 2 || next.ID == attempt.ID {
		t.Fatalf("read recovery attempt=%+v err=%v", next, err)
	}
}

func TestRecoverableFailureUsesOneFreshAttemptThenBlocks(t *testing.T) {
	fixture := &graphFixture{}
	graph, err := New(Spec{Goal: "verify", Nodes: []NodeSpec{{ID: 1, Title: "test"}}}, 1, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	_, first, _ := graph.StartNext(t.Context(), "state")
	if err := graph.BeginTool(t.Context(), first.ID, ToolAction{Tool: "run_command", Risk: "execute", Summary: "go test", PotentialMutation: true, NonReplayable: true}, "state"); err != nil {
		t.Fatal(err)
	}
	if err := graph.FinishTool(t.Context(), first.ID, ToolResult{Tool: "run_command", Risk: "execute", Summary: "tests failed", FailureDetail: "tests failed", Failed: true, Retryable: true, Verification: true, WorkspaceToken: "state"}); err != nil {
		t.Fatal(err)
	}
	decision, err := graph.ProposeCompletion(t.Context(), "could not finish", "state")
	if err != nil || decision.Kind != DecisionRetry {
		t.Fatalf("first failure decision=%+v err=%v", decision, err)
	}
	_, second, err := graph.StartNext(t.Context(), "state")
	if err != nil || second.Number != 2 {
		t.Fatalf("second attempt=%+v err=%v", second, err)
	}
	if err := graph.RecordFailure(t.Context(), second.ID, Failure{Kind: FailureTool, Tool: "run_command", Risk: "execute", Detail: "tests still fail", Retryable: true}); err != nil {
		t.Fatal(err)
	}
	decision, err = graph.ProposeCompletion(t.Context(), "still failing", "state")
	if err != nil || decision.Kind != DecisionBlocked {
		t.Fatalf("exhausted failure decision=%+v err=%v", decision, err)
	}
}

func TestPermissionDenialBlocksInsteadOfBeingRoutedAround(t *testing.T) {
	graph, err := New(Spec{Goal: "publish", Nodes: []NodeSpec{{ID: 1, Title: "write"}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, _ := graph.StartNext(t.Context(), "state")
	if err := graph.RecordFailure(t.Context(), attempt.ID, Failure{Kind: FailurePermission, Tool: "write_file", Risk: "write", Detail: "user denied write"}); err != nil {
		t.Fatal(err)
	}
	decision, err := graph.ProposeCompletion(t.Context(), "done", "state")
	if err != nil || decision.Kind != DecisionBlocked || decision.Reason != "user denied write" {
		t.Fatalf("permission decision=%+v err=%v", decision, err)
	}
}

func TestGraphPauseStopsSchedulingAtDurableBoundaryAndResumesSameAttempt(t *testing.T) {
	fixture := &graphFixture{}
	graph, err := New(Spec{Goal: "inspect", Nodes: []NodeSpec{{ID: 1, Title: "inspect"}}}, 1, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := graph.StartNext(t.Context(), "state")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.RequestPause(t.Context(), "operator requested a checkpoint"); err != nil {
		t.Fatal(err)
	}
	requested, reached, reason := graph.PauseState()
	if !requested || reached || !strings.Contains(reason, "checkpoint") {
		t.Fatalf("pause state requested=%t reached=%t reason=%q", requested, reached, reason)
	}
	if _, _, err := graph.StartNext(t.Context(), "state"); !errors.Is(err, ErrGraphPaused) {
		t.Fatalf("paused start error=%v", err)
	}
	if err := graph.ReachPause(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, reached, _ := graph.PauseState(); !reached {
		t.Fatal("safe pause boundary was not retained")
	}
	if err := ValidateSnapshot(graph.Snapshot()); err != nil {
		t.Fatalf("paused snapshot is invalid: %v", err)
	}
	if err := graph.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	if requested, reached, reason := graph.PauseState(); requested || reached || reason != "" {
		t.Fatalf("resumed pause state requested=%t reached=%t reason=%q", requested, reached, reason)
	}
	node, resumed, active := graph.Active()
	if !active || node.ID != 1 || resumed.ID != attempt.ID {
		t.Fatalf("resume replaced the active attempt: node=%+v attempt=%+v active=%t", node, resumed, active)
	}
	if len(fixture.snapshots) < 4 || !fixture.durable[len(fixture.durable)-1] {
		t.Fatal("pause/resume transitions were not persisted durably")
	}
}

func TestGraphPauseBeforeWorkStartsIsImmediatelyReached(t *testing.T) {
	graph, err := New(Spec{Goal: "inspect", Nodes: []NodeSpec{{ID: 1, Title: "inspect", Execution: ExecutionReadOnly}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.RequestPause(t.Context(), "pause before scheduling"); err != nil {
		t.Fatal(err)
	}
	if requested, reached, _ := graph.PauseState(); !requested || !reached {
		t.Fatalf("idle pause requested=%t reached=%t", requested, reached)
	}
	if _, err := graph.StartReadyReads(t.Context(), "state", 2); !errors.Is(err, ErrGraphPaused) {
		t.Fatalf("paused read scheduling error=%v", err)
	}
}

func TestGraphPauseBoundaryRejectsPendingActionAndMalformedSnapshot(t *testing.T) {
	graph, err := New(Spec{Goal: "inspect", Nodes: []NodeSpec{{ID: 1, Title: "inspect"}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := graph.StartNext(t.Context(), "state")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.BeginTool(t.Context(), attempt.ID, ToolAction{Tool: "read_file", Risk: "read", Summary: "inspect"}, "state"); err != nil {
		t.Fatal(err)
	}
	if err := graph.RequestPause(t.Context(), "pause after the read"); err != nil {
		t.Fatal(err)
	}
	if err := graph.ReachPause(t.Context()); err == nil || !strings.Contains(err.Error(), "action is pending") {
		t.Fatalf("pending-action pause error=%v", err)
	}

	snapshot := graph.Snapshot()
	snapshot.PauseRequested = false
	snapshot.PauseReached = false
	if err := ValidateSnapshot(snapshot); err == nil || !strings.Contains(err.Error(), "pause reason") {
		t.Fatalf("orphaned pause-reason validation error=%v", err)
	}
	snapshot.PauseRequested = true
	snapshot.PauseReason = ""
	if err := ValidateSnapshot(snapshot); err == nil || !strings.Contains(err.Error(), "without a reason") {
		t.Fatalf("missing pause-reason validation error=%v", err)
	}
}

func TestRetryNodeReopensSafeBlockerAndPreservesAttempt(t *testing.T) {
	graph, err := New(Spec{Goal: "write", Nodes: []NodeSpec{{ID: 1, Title: "write"}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, first, err := graph.StartNext(t.Context(), "state")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.RecordFailure(t.Context(), first.ID, Failure{Kind: FailurePermission, Tool: "write_file", Risk: "write", Detail: "permission denied"}); err != nil {
		t.Fatal(err)
	}
	decision, err := graph.ProposeCompletion(t.Context(), "blocked", "state")
	if err != nil || decision.Kind != DecisionBlocked {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	if err := graph.RetryNode(t.Context(), 1, "permission policy changed"); err != nil {
		t.Fatal(err)
	}
	if outcome, reason := graph.Outcome(); outcome != "" || reason != "" {
		t.Fatalf("retry retained terminal outcome=%q reason=%q", outcome, reason)
	}
	_, second, err := graph.StartNext(t.Context(), "state")
	if err != nil || second.Number != 2 || second.ID == first.ID {
		t.Fatalf("fresh retry attempt=%+v err=%v", second, err)
	}
	snapshot := graph.Snapshot()
	if snapshot.Attempts[0].State != AttemptBlocked || snapshot.Attempts[0].Failures[0].Detail != "permission denied" {
		t.Fatalf("blocked attempt was rewritten: %+v", snapshot.Attempts[0])
	}
}

func TestRetryNodeRefusesAmbiguousMutationAndExhaustedBound(t *testing.T) {
	graph, err := New(Spec{Goal: "publish", Nodes: []NodeSpec{{ID: 1, Title: "publish"}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := graph.StartNext(t.Context(), "before")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.BeginTool(t.Context(), attempt.ID, ToolAction{Tool: "run_command", Risk: "execute", Summary: "publish", PotentialMutation: true, NonReplayable: true}, "before"); err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(graph.Snapshot(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Recover(t.Context(), "before"); err != nil {
		t.Fatal(err)
	}
	if err := restored.RetryNode(t.Context(), 1, "try again"); !errors.Is(err, ErrUnsafeNodeRetry) {
		t.Fatalf("ambiguous retry error=%v", err)
	}

	exhausted, err := New(Spec{Goal: "inspect", Nodes: []NodeSpec{{ID: 1, Title: "inspect"}}}, 1, Options{MaxAttemptsPerNode: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, only, _ := exhausted.StartNext(t.Context(), "state")
	if err := exhausted.RecordFailure(t.Context(), only.ID, Failure{Kind: FailurePermission, Detail: "denied"}); err != nil {
		t.Fatal(err)
	}
	if _, err := exhausted.ProposeCompletion(t.Context(), "blocked", "state"); err != nil {
		t.Fatal(err)
	}
	if err := exhausted.RetryNode(t.Context(), 1, "try again"); err == nil || !strings.Contains(err.Error(), "attempt bound") {
		t.Fatalf("exhausted retry error=%v", err)
	}
}

func TestExternalWorkspaceDriftStalesRecordedDoneNodes(t *testing.T) {
	fixture := &graphFixture{}
	graph, err := New(Spec{Goal: "inspect", Nodes: []NodeSpec{{ID: 1, Title: "one"}, {ID: 2, Title: "two"}}}, 1, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	_, first, _ := graph.StartNext(t.Context(), "state-one")
	if decision := successfulRead(t, graph, first, "state-one"); decision.Kind != DecisionAccepted {
		t.Fatalf("decision=%+v", decision)
	}
	node, second, err := graph.StartNext(t.Context(), "state-two")
	if err != nil || node.ID != 1 || second.Number != 2 {
		t.Fatalf("drift did not rerun stale first node: node=%+v attempt=%+v err=%v", node, second, err)
	}
}

func TestRevisionUsesOptimisticConcurrencyAndInvalidatesDependents(t *testing.T) {
	fixture := &graphFixture{}
	graph, err := New(Spec{Goal: "ship", Nodes: []NodeSpec{{ID: 1, Title: "inspect"}, {ID: 2, Title: "implement", DependsOn: []int{1}}, {ID: 3, Title: "later"}}}, 1, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, _ := graph.StartNext(t.Context(), "state")
	if decision := successfulRead(t, graph, attempt, "state"); decision.Kind != DecisionAccepted {
		t.Fatalf("decision=%+v", decision)
	}
	_, dependentAttempt, err := graph.StartNext(t.Context(), "state")
	if err != nil {
		t.Fatal(err)
	}
	if decision := successfulRead(t, graph, dependentAttempt, "state"); decision.Kind != DecisionAccepted {
		t.Fatalf("dependent decision=%+v", decision)
	}
	revised := Spec{Goal: "ship", Nodes: []NodeSpec{{ID: 1, Title: "inspect more"}, {ID: 2, Title: "implement", DependsOn: []int{1}}, {ID: 3, Title: "later"}}}
	if err := graph.Revise(t.Context(), 99, "new evidence", revised); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale revision error=%v", err)
	}
	if err := graph.Revise(t.Context(), 1, "new evidence", revised); err != nil {
		t.Fatal(err)
	}
	snapshot := graph.Snapshot()
	if snapshot.Generation != 2 || snapshot.Nodes[0].State != NodeReady || snapshot.Nodes[1].State != NodeStale || snapshot.Nodes[0].AcceptedAttemptID != "" || snapshot.Nodes[1].AcceptedAttemptID != "" {
		t.Fatalf("revised graph=%+v", snapshot)
	}
}

func TestGraphControlToolsApplyValidatedRuntimeTransitions(t *testing.T) {
	graph, err := New(Spec{Goal: "inspect", Nodes: []NodeSpec{{ID: 1, Title: "inspect"}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	revision := RevisionTool{Graph: graph}
	if _, err := revision.Assess(json.RawMessage(`{"base_generation":1,"reason":"new evidence","goal":"inspect safely","nodes":[{"id":2,"title":"inspect safely"}]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := revision.Execute(t.Context(), json.RawMessage(`{"base_generation":1,"reason":"new evidence","goal":"inspect safely","nodes":[{"id":2,"title":"inspect safely"}]}`)); err != nil {
		t.Fatal(err)
	}
	if graph.Generation() != 2 || !MetaTool(ReviseToolName) || !MetaTool(BlockToolName) {
		t.Fatalf("generation=%d", graph.Generation())
	}
	_, attempt, err := graph.StartNext(t.Context(), "state")
	if err != nil {
		t.Fatal(err)
	}
	block := BlockTool{Graph: graph}
	input := json.RawMessage(fmt.Sprintf(`{"attempt_id":%q,"reason":"material user input is required"}`, attempt.ID))
	if _, err := block.Execute(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if outcome, reason := graph.Outcome(); outcome != OutcomeBlocked || !strings.Contains(reason, "user input") {
		t.Fatalf("outcome=%q reason=%q", outcome, reason)
	}
}

func TestRestoreRejectsStructurallyFalseDoneSnapshot(t *testing.T) {
	graph, err := New(Spec{Goal: "inspect", Nodes: []NodeSpec{{ID: 1, Title: "inspect"}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := graph.Snapshot()
	snapshot.Outcome = OutcomeDone
	snapshot.Reason = "tampered terminal state"
	if _, err := Restore(snapshot, Options{}); err == nil || !strings.Contains(err.Error(), "retains node") {
		t.Fatalf("restore error=%v", err)
	}
}

func TestRestoreDefaultsPreFanoutSnapshotsToPrimaryExecution(t *testing.T) {
	graph, err := New(Spec{Goal: "inspect", Nodes: []NodeSpec{{ID: 1, Title: "inspect"}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	legacy := graph.Snapshot()
	legacy.Nodes[0].Execution = ""
	legacy.Revisions[0].Spec.Nodes[0].Execution = ""
	legacy.ReadFanout = ReadFanout{}
	restored, err := Restore(legacy, Options{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := restored.Snapshot()
	if snapshot.Nodes[0].Execution != ExecutionPrimary || snapshot.ReadFanout.MaxConcurrent != defaultMaxReadConcurrency {
		t.Fatalf("legacy defaults=%+v read=%+v", snapshot.Nodes[0], snapshot.ReadFanout)
	}
	claims, err := restored.StartReadyReads(t.Context(), "state", 2)
	if err != nil || len(claims) != 0 {
		t.Fatalf("legacy primary node was delegated: claims=%+v err=%v", claims, err)
	}
}

func TestWorkspaceStateTokenTracksWorkingAndUntrackedBytes(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	git("init", "-q")
	git("config", "user.email", "fixture@example.com")
	git("config", "user.name", "Fixture")
	path := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "tracked.txt")
	git("commit", "-qm", "fixture")
	first, err := WorkspaceStateToken(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := WorkspaceStateToken(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("tracked working-tree change did not change state token")
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := WorkspaceStateToken(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if third == second {
		t.Fatal("untracked content did not change state token")
	}
}

func TestWorkspaceStateTokenCoversStagedAndUnstagedBytesBeforeFirstCommit(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	git("init", "-q")
	path := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(path, []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "new.txt")
	staged, err := WorkspaceStateToken(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("unstaged replacement\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mixed, err := WorkspaceStateToken(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if staged == mixed {
		t.Fatal("unborn repository token missed unstaged bytes layered over the index")
	}
}
