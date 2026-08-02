package goalgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
