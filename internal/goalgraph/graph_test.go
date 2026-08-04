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
	// A verified candidate wave is the feature working. It stops the graph
	// because integration is user authority, not because anything failed, so it
	// must be distinguishable from a blocker in both state and outcome.
	if outcome, reason := graph.Outcome(); outcome != OutcomeAwaitingReview || !strings.Contains(reason, "reviewed integration is required") {
		t.Fatalf("outcome=%q reason=%q", outcome, reason)
	}
	if snapshot.Nodes[0].State != NodeAwaitingReview || snapshot.Nodes[1].State != NodeAwaitingReview || snapshot.Nodes[2].State == NodeDone {
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

// A wave that crosses the aggregate budget on its final result still left real
// worktrees on disk. If the graph drops the candidate record at that moment,
// the operator has orphaned worktrees and nothing pointing at them.
func TestBudgetExhaustionRetainsTheCandidateItAlreadyProduced(t *testing.T) {
	graph, err := New(Spec{Goal: "produce a candidate", Nodes: []NodeSpec{
		{ID: 1, Title: "change docs", Execution: ExecutionIsolatedWrite, WritePaths: []string{"docs/"}, Acceptance: []string{"docs checks pass"}},
	}}, 1, Options{MaxAggregateTokens: 1000})
	if err != nil {
		t.Fatal(err)
	}
	base := WriterBase{WorkspaceToken: "parent-state", Commit: "abcdef", Clean: true}
	claims, err := graph.StartReadyWriters(t.Context(), base, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("writer claims=%+v err=%v", claims, err)
	}
	err = graph.FinishWriter(t.Context(), WriterResult{
		AttemptID: claims[0].Attempt.ID, WorkerID: "writer-1", Status: "done",
		Summary: "implemented and checked candidate", WritePaths: claims[0].WritePaths,
		ChangedFiles: []string{"docs/guide.md"}, Worktree: "/tmp/candidate", Branch: "collomia/writer-1",
		BaseCommit: base.Commit, ParentWorkspaceToken: base.WorkspaceToken,
		VerificationState: "passed", VerificationToken: "child-state",
		Verification: []CandidateVerification{{Command: "go test ./...", Status: "passed", StateToken: "child-state"}},
		Iterations:   2, InputTokens: 900, OutputTokens: 400,
	})
	if !errors.Is(err, ErrAggregateBudget) {
		t.Fatalf("over-budget writer result error=%v", err)
	}
	snapshot := graph.Snapshot()
	if outcome, _ := graph.Outcome(); outcome != OutcomeBudgetExhausted {
		t.Fatalf("outcome=%q", outcome)
	}
	candidate := snapshot.Attempts[0].Candidate
	if candidate == nil || candidate.Worktree != "/tmp/candidate" || candidate.Branch != "collomia/writer-1" {
		t.Fatalf("retained candidate was discarded when the budget ran out: %+v", snapshot.Attempts[0])
	}
	if err := ValidateSnapshot(snapshot); err != nil {
		t.Fatalf("snapshot invalid: %v", err)
	}
}

// An agentic node resends its whole prompt every iteration. Charging cache
// reads at full weight made the ceiling a function of context length times
// iteration count, and a real session (kanban10) exhausted 1,000,000 tokens
// on 937,617 input tokens of which 681,717 were cache reads — while cost sat
// at $1.50 of $5 and iterations at 49 of 96.
func TestAggregateTokenCeilingChargesNewWorkNotCacheReads(t *testing.T) {
	graph, err := New(Spec{Goal: "long node", Nodes: []NodeSpec{{ID: 1, Title: "implement"}}}, 1, Options{MaxAggregateTokens: 1000})
	if err != nil {
		t.Fatal(err)
	}
	// Ten iterations that each re-read a 900-token cached prompt and add 90
	// new tokens: 9,900 raw input, but only 900 of it new.
	for i := 0; i < 10; i++ {
		if err := graph.RecordPrimaryUsage(t.Context(), WorkUsage{
			Iterations: 1, InputTokens: 990, CachedTokens: 900, OutputTokens: 10,
		}); err != nil {
			t.Fatalf("iteration %d was refused: %v", i, err)
		}
	}
	status := graph.BudgetStatus(time.Now())
	if status.Usage.InputTokens != 9900 {
		t.Fatalf("raw input accounting changed: %+v", status.Usage)
	}
	// 10 × (990 − 900 + 10) = 1000 charged, against a 1000 ceiling.
	if billable := status.Usage.BillableTokens(); billable != 1000 {
		t.Fatalf("billable tokens=%d, want 1000", billable)
	}
	if outcome, _ := graph.Outcome(); outcome != "" {
		t.Fatalf("graph terminated on cache reads: %q", outcome)
	}
	// The ceiling still binds on new work.
	err = graph.RecordPrimaryUsage(t.Context(), WorkUsage{Iterations: 1, InputTokens: 200, OutputTokens: 10})
	if !errors.Is(err, ErrAggregateBudget) {
		t.Fatalf("new work past the ceiling was accepted: %v", err)
	}
	if reason := graph.Snapshot().Reason; !strings.Contains(reason, "served from the provider cache") {
		t.Fatalf("exhaustion reason hides the cache accounting: %q", reason)
	}
}

// Exhaustion used to cost the whole graph: the only way on was to start over,
// discarding every accepted node. A person can now grant another envelope.
func TestUserGrantedBudgetExtensionResumesWithoutReplayingWork(t *testing.T) {
	graph, err := New(Spec{Goal: "two nodes", Nodes: []NodeSpec{
		{ID: 1, Title: "first"},
		{ID: 2, Title: "second", DependsOn: []int{1}},
	}}, 1, Options{MaxAggregateTokens: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.ExhaustBudget(t.Context(), "aggregate token budget exhausted"); err != nil {
		t.Fatal(err)
	}
	if outcome, reason := graph.Outcome(); outcome != OutcomeBudgetExhausted || !strings.Contains(reason, "/orchestrate extend") {
		t.Fatalf("exhaustion does not name the way forward: %q %q", outcome, reason)
	}
	before := graph.Snapshot().AggregateBudget
	if err := graph.ExtendBudget(t.Context(), "user granted more allowance"); err != nil {
		t.Fatal(err)
	}
	snapshot := graph.Snapshot()
	if snapshot.Outcome != "" {
		t.Fatalf("extended graph is still terminal: %q", snapshot.Outcome)
	}
	if snapshot.AggregateBudget.Extensions != 1 || snapshot.AggregateBudget.MaxTokens != before.MaxTokens+before.Grant.Tokens {
		t.Fatalf("envelope was not granted: %+v", snapshot.AggregateBudget)
	}
	if snapshot.Nodes[0].State != NodeReady {
		t.Fatalf("first node did not become schedulable: %+v", snapshot.Nodes[0])
	}
	if err := ValidateSnapshot(snapshot); err != nil {
		t.Fatalf("extended snapshot invalid: %v", err)
	}
	// The wider envelope survives a restore, and its bound is validated
	// against the grants actually recorded.
	if _, err := Restore(snapshot, Options{}); err != nil {
		t.Fatalf("restore extended graph: %v", err)
	}
	// Each grant adds one envelope of the size this graph was configured with,
	// not a build constant, and a person may keep deciding to continue.
	for i := 2; i <= 4; i++ {
		if err := graph.ExhaustBudget(t.Context(), "exhausted again"); err != nil {
			t.Fatal(err)
		}
		if err := graph.ExtendBudget(t.Context(), "another grant"); err != nil {
			t.Fatalf("grant %d was refused: %v", i, err)
		}
		budget := graph.Snapshot().AggregateBudget
		if budget.Extensions != i || budget.MaxTokens != budget.Grant.Tokens*(i+1) {
			t.Fatalf("grant %d did not add one configured envelope: %+v", i, budget)
		}
	}
	if err := ValidateSnapshot(graph.Snapshot()); err != nil {
		t.Fatalf("repeatedly extended snapshot invalid: %v", err)
	}
	// A snapshot whose stored envelope exceeds what its recorded grants could
	// produce is still refused.
	forged := graph.Snapshot()
	forged.AggregateBudget.MaxTokens = maxConfigurableGraphTokens * (forged.AggregateBudget.Extensions + 2)
	if err := ValidateSnapshot(forged); err == nil {
		t.Fatal("a snapshot claiming an implausible envelope was accepted")
	}
}

// The envelope is configuration, not a build constant: a session set up for
// more work gets it, and each user grant adds that same larger envelope.
func TestConfiguredEnvelopeIsHonouredAndSizesEachGrant(t *testing.T) {
	graph, err := New(Spec{Goal: "long job", Nodes: []NodeSpec{{ID: 1, Title: "implement"}}}, 1, Options{
		MaxAggregateIterations: defaultMaxGraphIterations * 4,
		MaxAggregateTokens:     defaultMaxGraphTokens * 6,
		MaxAggregateCostUSD:    defaultMaxGraphCostUSD * 3,
		MaxActiveWallSeconds:   defaultMaxActiveWallSeconds * 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	budget := graph.Snapshot().AggregateBudget
	if budget.MaxTokens != defaultMaxGraphTokens*6 || budget.MaxIterations != defaultMaxGraphIterations*4 {
		t.Fatalf("configured envelope was clamped to the default: %+v", budget)
	}
	if budget.Grant.Tokens != defaultMaxGraphTokens*6 {
		t.Fatalf("recorded grant does not match the configured envelope: %+v", budget.Grant)
	}
	if err := graph.ExhaustBudget(t.Context(), "exhausted"); err != nil {
		t.Fatal(err)
	}
	if err := graph.ExtendBudget(t.Context(), "user grant"); err != nil {
		t.Fatal(err)
	}
	if extended := graph.Snapshot().AggregateBudget; extended.MaxTokens != defaultMaxGraphTokens*12 {
		t.Fatalf("grant added a build constant rather than the configured envelope: %+v", extended)
	}
	// An implausible value is still refused, and zero still means the default.
	huge, err := New(Spec{Goal: "absurd", Nodes: []NodeSpec{{ID: 1, Title: "implement"}}}, 1, Options{MaxAggregateTokens: maxConfigurableGraphTokens * 100})
	if err != nil {
		t.Fatal(err)
	}
	if tokens := huge.Snapshot().AggregateBudget.MaxTokens; tokens != maxConfigurableGraphTokens {
		t.Fatalf("implausible envelope was honoured: %d", tokens)
	}
	plain, err := New(Spec{Goal: "default", Nodes: []NodeSpec{{ID: 1, Title: "implement"}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if tokens := plain.Snapshot().AggregateBudget.MaxTokens; tokens != defaultMaxGraphTokens {
		t.Fatalf("omitted configuration did not use the default: %d", tokens)
	}
}

// Cancellation is the other way a wave ends while its worktrees exist. The
// cancelled outcome stands, but the identity of what is on disk still has to
// be recorded, and it must not be mistaken for a verified candidate.
func TestCancellationRetainsWorktreeIdentityWithoutClaimingVerification(t *testing.T) {
	graph, err := New(Spec{Goal: "produce a candidate", Nodes: []NodeSpec{
		{ID: 1, Title: "change docs", Execution: ExecutionIsolatedWrite, WritePaths: []string{"docs/"}, Acceptance: []string{"docs checks pass"}},
	}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	base := WriterBase{WorkspaceToken: "parent-state", Commit: "abcdef", Clean: true}
	claims, err := graph.StartReadyWriters(t.Context(), base, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("writer claims=%+v err=%v", claims, err)
	}
	if err := graph.Cancel(t.Context(), "user interrupted the turn"); err != nil {
		t.Fatal(err)
	}
	err = graph.FinishWriter(t.Context(), WriterResult{
		AttemptID: claims[0].Attempt.ID, WorkerID: "writer-1", Status: "cancelled",
		WritePaths: claims[0].WritePaths, ChangedFiles: []string{"docs/guide.md"},
		Worktree: "/tmp/cancelled-candidate", Branch: "collomia/writer-1",
		BaseCommit: base.Commit, ParentWorkspaceToken: base.WorkspaceToken,
		Iterations: 1, InputTokens: 40, OutputTokens: 10,
	})
	if !errors.Is(err, ErrGraphTerminal) {
		t.Fatalf("cancelled writer result error=%v, want ErrGraphTerminal", err)
	}
	snapshot := graph.Snapshot()
	if snapshot.Outcome != OutcomeCancelled {
		t.Fatalf("outcome=%q, want cancelled", snapshot.Outcome)
	}
	attempt := snapshot.Attempts[0]
	if attempt.State != AttemptCancelled {
		t.Fatalf("attempt state=%q, want cancelled", attempt.State)
	}
	if attempt.Candidate == nil || attempt.Candidate.Worktree != "/tmp/cancelled-candidate" {
		t.Fatalf("cancelled wave discarded its retained worktree: %+v", attempt)
	}
	if attempt.Candidate.VerificationState != "" {
		t.Fatalf("cancelled candidate claims verification %q", attempt.Candidate.VerificationState)
	}
	if snapshot.Nodes[0].State != NodeCancelled {
		t.Fatalf("node state=%q, want cancelled", snapshot.Nodes[0].State)
	}
	// The accounting the child already spent is still the graph's to report.
	if snapshot.Accounting.AutomaticWriters.InputTokens != 40 {
		t.Fatalf("cancelled writer usage was dropped: %+v", snapshot.Accounting.AutomaticWriters)
	}
	if err := ValidateSnapshot(snapshot); err != nil {
		t.Fatalf("snapshot invalid: %v", err)
	}
	// Inspection is the whole point of retention: a restored graph must still
	// be able to tell the operator where the directory is.
	restored, err := Restore(snapshot, Options{})
	if err != nil {
		t.Fatalf("restore cancelled candidate snapshot: %v", err)
	}
	status, err := restored.Inspect(0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "Retained candidates") || !strings.Contains(status, "/tmp/cancelled-candidate") {
		t.Fatalf("restored graph overview omits the retained worktree:\n%s", status)
	}
	if !strings.Contains(status, "unverified") {
		t.Fatalf("restored overview must not imply verification:\n%s", status)
	}
}

// A disposition is a claim about a real directory, so a snapshot cannot carry
// one that describes nothing, one nobody recorded a time for, or one from
// outside the vocabulary. Older snapshots have no disposition at all, and that
// must keep restoring cleanly: never observed is the honest starting state.
func TestSnapshotRejectsARetainedWorktreeDispositionThatDescribesNothing(t *testing.T) {
	graph, err := New(Spec{Goal: "produce a candidate", Nodes: []NodeSpec{
		{ID: 1, Title: "change docs", Execution: ExecutionIsolatedWrite, WritePaths: []string{"docs/"}, Acceptance: []string{"docs checks pass"}},
	}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	base := WriterBase{WorkspaceToken: "parent-state", Commit: "abcdef", Clean: true}
	claims, err := graph.StartReadyWriters(t.Context(), base, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("writer claims=%+v err=%v", claims, err)
	}
	if err := graph.Cancel(t.Context(), "user interrupted the turn"); err != nil {
		t.Fatal(err)
	}
	_ = graph.FinishWriter(t.Context(), WriterResult{
		AttemptID: claims[0].Attempt.ID, WorkerID: "writer-1", Status: "cancelled",
		WritePaths: claims[0].WritePaths, ChangedFiles: []string{"docs/guide.md"},
		Worktree: "/tmp/cancelled-candidate", Branch: "collomia/writer-1",
		BaseCommit: base.Commit, ParentWorkspaceToken: base.WorkspaceToken,
	})
	// A graph that has never reconciled is valid; that is every graph written
	// before this field existed.
	if err := ValidateSnapshot(graph.Snapshot()); err != nil {
		t.Fatalf("an unreconciled snapshot was rejected: %v", err)
	}
	if pending := graph.UnreconciledWorktrees(); len(pending) != 1 || pending[0].Worktree != "/tmp/cancelled-candidate" {
		t.Fatalf("unreconciled worktrees=%+v, want the retained candidate", pending)
	}

	if err := graph.RecordWorktreeDispositions(t.Context(), []WorktreeObservation{{AttemptID: claims[0].Attempt.ID, Disposition: "vanished"}}); err == nil {
		t.Fatal("the graph accepted a disposition outside its vocabulary")
	}
	if err := graph.RecordWorktreeDispositions(t.Context(), []WorktreeObservation{{
		AttemptID: claims[0].Attempt.ID, Disposition: DispositionPresent, Detail: "1 changed file(s) still in the tree",
	}}); err != nil {
		t.Fatal(err)
	}
	reconciled := graph.Snapshot()
	if err := ValidateSnapshot(reconciled); err != nil {
		t.Fatalf("a reconciled snapshot was rejected: %v", err)
	}
	if reconciled.Attempts[0].Reconciled.IsZero() {
		t.Fatal("a recorded disposition carries no time of observation")
	}

	forged := graph.Snapshot()
	forged.Attempts[0].Worktree, forged.Attempts[0].Candidate = "", nil
	if err := ValidateSnapshot(forged); err == nil {
		t.Fatal("a disposition describing no worktree was accepted")
	}
	undated := graph.Snapshot()
	undated.Attempts[0].Reconciled = time.Time{}
	if err := ValidateSnapshot(undated); err == nil {
		t.Fatal("a disposition with no time of observation was accepted")
	}
	unclaimed := graph.Snapshot()
	unclaimed.Attempts[0].Disposition = ""
	if err := ValidateSnapshot(unclaimed); err == nil {
		t.Fatal("a reconciliation time without a disposition was accepted")
	}
}

// A retained candidate is a fact the graph promised to keep. Older snapshots
// recorded it as a blocked node, and restoring must not lose the distinction.
func TestRestoreUpgradesLegacyBlockedCandidateToAwaitingReview(t *testing.T) {
	graph, err := New(Spec{Goal: "produce a candidate", Nodes: []NodeSpec{
		{ID: 1, Title: "change docs", Execution: ExecutionIsolatedWrite, WritePaths: []string{"docs/"}, Acceptance: []string{"docs checks pass"}},
	}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	base := WriterBase{WorkspaceToken: "parent-state", Commit: "abcdef", Clean: true}
	claims, err := graph.StartReadyWriters(t.Context(), base, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("writer claims=%+v err=%v", claims, err)
	}
	if err := graph.FinishWriter(t.Context(), WriterResult{
		AttemptID: claims[0].Attempt.ID, WorkerID: "writer-1", Status: "done",
		Summary: "implemented and checked candidate", WritePaths: claims[0].WritePaths,
		ChangedFiles: []string{"docs/guide.md"}, Worktree: "/tmp/candidate", Branch: "collomia/writer-1",
		BaseCommit: base.Commit, ParentWorkspaceToken: base.WorkspaceToken,
		VerificationState: "passed", VerificationToken: "child-state",
		Verification: []CandidateVerification{{Command: "go test ./...", Status: "passed", StateToken: "child-state"}},
	}); err != nil {
		t.Fatal(err)
	}
	legacy := graph.Snapshot()
	legacy.Nodes[0].State = NodeBlocked
	legacy.Outcome, legacy.Reason = OutcomeBlocked, "review required"
	restored, err := Restore(legacy, Options{})
	if err != nil {
		t.Fatalf("restore legacy candidate snapshot: %v", err)
	}
	if state := restored.Snapshot().Nodes[0].State; state != NodeAwaitingReview {
		t.Fatalf("legacy candidate node restored as %q", state)
	}
	// Retry is refused with a reason that names the candidate rather than
	// telling the operator the node has no blocked attempt to retry.
	err = restored.RetryNode(t.Context(), 1, "")
	if err == nil || !strings.Contains(err.Error(), "awaiting review") {
		t.Fatalf("retry of an awaiting-review graph: %v", err)
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

// Two scopes differing only in case may address the same directory on a
// case-insensitive filesystem, so the wave must serialize them. Overlap folds
// case for exactly this reason: over-detecting a collision costs parallelism,
// while missing one lets two writers claim the same path from one commit.
func TestGraphWriterClaimsSerializeScopesDifferingOnlyByCase(t *testing.T) {
	graph, err := New(Spec{Goal: "bounded writers", Nodes: []NodeSpec{
		{ID: 1, Title: "lower", Execution: ExecutionIsolatedWrite, WritePaths: []string{"src/"}},
		{ID: 2, Title: "upper", Execution: ExecutionIsolatedWrite, WritePaths: []string{"SRC/"}},
	}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := graph.StartReadyWriters(t.Context(), WriterBase{WorkspaceToken: "state", Commit: "abc", Clean: true}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].Node.ID != 1 {
		t.Fatalf("case-folded sibling scopes were co-scheduled: claims=%+v", claims)
	}
}

// The wave bound is two starts for the whole graph, so a third writer is
// reachable in a valid graph and must never silently become finished work. It
// stays unattempted while the graph is terminal, and once an explicit retry
// reopens the graph the spent starts budget blocks with a stated reason rather
// than leaving the graph running with nothing it can schedule.
func TestGraphWriterBeyondTheWaveBoundIsNeverSilentlyFinished(t *testing.T) {
	graph, err := New(Spec{Goal: "bounded writers", Nodes: []NodeSpec{
		{ID: 1, Title: "alpha", Execution: ExecutionIsolatedWrite, WritePaths: []string{"alpha/"}},
		{ID: 2, Title: "beta", Execution: ExecutionIsolatedWrite, WritePaths: []string{"beta/"}},
		{ID: 3, Title: "gamma", Execution: ExecutionIsolatedWrite, WritePaths: []string{"gamma/"}},
	}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	base := WriterBase{WorkspaceToken: "state", Commit: "abc", Clean: true}
	claims, err := graph.StartReadyWriters(t.Context(), base, 2)
	if err != nil || len(claims) != 2 {
		t.Fatalf("first wave claims=%+v err=%v", claims, err)
	}
	for _, claim := range claims {
		if err := graph.FinishWriter(t.Context(), WriterResult{
			AttemptID: claim.Attempt.ID, WorkerID: "writer-" + claim.Attempt.ID, Status: "error",
			Error: "provider is unavailable", WritePaths: claim.WritePaths,
			BaseCommit: base.Commit, ParentWorkspaceToken: base.WorkspaceToken,
		}); err != nil && !errors.Is(err, ErrGraphTerminal) {
			t.Fatal(err)
		}
	}
	if outcome, _ := graph.Outcome(); outcome != OutcomeBlocked {
		t.Fatalf("outcome=%q, want blocked", outcome)
	}
	// The unattempted node is reported as never having run, not as done and not
	// as a candidate. A graph that quietly finished it would be claiming work.
	gamma := nodeByID(t, graph, 3)
	if gamma.State == NodeDone || gamma.State == NodeAwaitingReview {
		t.Fatalf("a writer that never ran is %q", gamma.State)
	}
	if len(gamma.AttemptIDs) != 0 {
		t.Fatalf("a writer that never ran has attempts: %+v", gamma.AttemptIDs)
	}

	// Reopening the graph must not leave it running with nothing schedulable:
	// the starts budget is spent, so the next wave attempt blocks and says so.
	for _, id := range []int{1, 2} {
		if err := graph.RetryNode(t.Context(), id, "retry requested explicitly by the user"); err != nil {
			t.Fatalf("retry node %d: %v", id, err)
		}
	}
	if _, err := graph.StartReadyWriters(t.Context(), base, 2); !errors.Is(err, ErrGraphTerminal) {
		t.Fatalf("wave beyond the starts bound error=%v, want ErrGraphTerminal", err)
	}
	// The writer starts bound is a resource bound, so it stops the graph as
	// budget exhaustion rather than as a failure — the same treatment every
	// other bound gets, which is what makes it extendable rather than fatal.
	outcome, reason := graph.Outcome()
	if outcome != OutcomeBudgetExhausted {
		t.Fatalf("exhausted writer starts outcome=%q, want budget_exhausted", outcome)
	}
	if !strings.Contains(reason, "starts 2/2") {
		t.Fatalf("the outcome does not say the writer starts budget is spent: %q", reason)
	}
}

func nodeByID(t *testing.T, graph *Graph, id int) Node {
	t.Helper()
	for _, node := range graph.Snapshot().Nodes {
		if node.ID == id {
			return node
		}
	}
	t.Fatalf("graph has no node %d", id)
	return Node{}
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

// The write-ahead worktree record is what turns "something may be on disk"
// into an exact path an operator can act on after a process boundary.
func TestGraphRecoveryNamesTheOrphanedWorktreeOfAnInterruptedWriter(t *testing.T) {
	graph, err := New(Spec{Goal: "write", Nodes: []NodeSpec{{ID: 1, Title: "write docs", Execution: ExecutionIsolatedWrite, WritePaths: []string{"docs/"}}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := graph.StartReadyWriters(t.Context(), WriterBase{WorkspaceToken: "parent", Commit: "abc", Clean: true}, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	if err := graph.RecordWriterWorktree(t.Context(), claims[0].Attempt.ID, "/tmp/collomia-worktrees/goal-write-1-7", "collomia/goal-write-1-7"); err != nil {
		t.Fatal(err)
	}
	// The record must survive the process boundary it exists for.
	restored, err := Restore(graph.Snapshot(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Recover(t.Context(), "parent"); err != nil {
		t.Fatal(err)
	}
	_, reason := restored.Outcome()
	if !strings.Contains(reason, "/tmp/collomia-worktrees/goal-write-1-7") || !strings.Contains(reason, "collomia/goal-write-1-7") {
		t.Fatalf("recovery reason does not name the orphaned worktree: %q", reason)
	}
	status, err := restored.Inspect(0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "/tmp/collomia-worktrees/goal-write-1-7") || !strings.Contains(status, "unreconciled") {
		t.Fatalf("overview does not list the orphaned worktree as unreconciled:\n%s", status)
	}
	if err := ValidateSnapshot(restored.Snapshot()); err != nil {
		t.Fatalf("snapshot invalid: %v", err)
	}
}

// A writer that changed nothing has its worktree removed by the delegate path,
// so the graph must stop pointing at a directory that no longer exists.
func TestFinishWriterClearsTheRecordWhenNothingChanged(t *testing.T) {
	graph, err := New(Spec{Goal: "write", Nodes: []NodeSpec{{ID: 1, Title: "write docs", Execution: ExecutionIsolatedWrite, WritePaths: []string{"docs/"}}}}, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	base := WriterBase{WorkspaceToken: "parent", Commit: "abc", Clean: true}
	claims, err := graph.StartReadyWriters(t.Context(), base, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	if err := graph.RecordWriterWorktree(t.Context(), claims[0].Attempt.ID, "/tmp/empty-candidate", "collomia/empty"); err != nil {
		t.Fatal(err)
	}
	if err := graph.FinishWriter(t.Context(), WriterResult{
		AttemptID: claims[0].Attempt.ID, WorkerID: "writer-1", Status: "done",
		Summary: "found nothing to change", WritePaths: claims[0].WritePaths,
		BaseCommit: base.Commit, ParentWorkspaceToken: base.WorkspaceToken,
	}); err != nil {
		t.Fatal(err)
	}
	attempt := graph.Snapshot().Attempts[0]
	if attempt.Worktree != "" || attempt.Branch != "" {
		t.Fatalf("graph still points at a removed worktree: %+v", attempt)
	}
	status, err := graph.Inspect(0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(status, "Retained candidates") {
		t.Fatalf("overview lists a candidate that does not exist:\n%s", status)
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

	// A larger stored envelope is a configuration decision and restores as
	// written; only an implausible one is refused.
	configured := graph.Snapshot()
	configured.AggregateBudget.MaxTokens = defaultMaxGraphTokens * 4
	configured.AggregateBudget.Grant.Tokens = defaultMaxGraphTokens * 4
	if _, err := Restore(configured, Options{}); err != nil {
		t.Fatalf("a configured larger envelope was refused: %v", err)
	}
	absurd := graph.Snapshot()
	absurd.AggregateBudget.MaxTokens = maxConfigurableGraphTokens * 4
	if _, err := Restore(absurd, Options{}); err == nil || !strings.Contains(err.Error(), "aggregate budget") {
		t.Fatalf("implausible stored envelope was accepted: %v", err)
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
	if kinds := snapshot.Attempts[0].CompletionGapKinds; len(kinds) != 1 || kinds[0] != GapNoFreshVerification {
		t.Fatalf("completion gap kinds=%v", snapshot.Attempts[0].CompletionGapKinds)
	}
	omitted := graph.Snapshot()
	omitted.Attempts[0].CompletionGap = ""
	omitted.Attempts[0].CompletionGapKinds = nil
	omitted.Attempts[0].CompletionGapIteration = 0
	if _, err := Restore(omitted, Options{}); err != nil {
		t.Fatalf("restore omitted gap: %v", err)
	}
	// A snapshot written before the gap was typed carries only the sentence.
	// Restore recovers the kind so the running controller never parses prose.
	untyped := graph.Snapshot()
	untyped.Attempts[0].CompletionGapKinds = nil
	restored, err := Restore(untyped, Options{})
	if err != nil {
		t.Fatalf("restore untyped legacy gap: %v", err)
	}
	if kinds := restored.Snapshot().Attempts[0].CompletionGapKinds; len(kinds) != 1 || kinds[0] != GapNoFreshVerification {
		t.Fatalf("legacy gap kinds=%v", kinds)
	}
	// An unrecognizable legacy sentence cannot bound remediation, so it is
	// cleared rather than restored as an unenforceable gap.
	unknown := graph.Snapshot()
	unknown.Attempts[0].CompletionGapKinds = nil
	unknown.Attempts[0].CompletionGap = "something a previous build worded differently"
	cleared, err := Restore(unknown, Options{})
	if err != nil {
		t.Fatalf("restore unknown legacy gap: %v", err)
	}
	if attempt := cleared.Snapshot().Attempts[0]; attempt.CompletionGap != "" || len(attempt.CompletionGapKinds) != 0 || attempt.CompletionGapIteration != 0 {
		t.Fatalf("unrecognized legacy gap survived: %+v", attempt)
	}
}

// Every transition rewrites the whole snapshot into the durable session, so an
// attempt that keeps every tool result makes persistence cost grow with the
// square of the node's tool calls. Long nodes are exactly the ones that reach
// that point.
func TestLongAttemptBoundsRetainedToolEvidence(t *testing.T) {
	fixture := &graphFixture{}
	graph, err := New(Spec{Goal: "long node", Nodes: []NodeSpec{{ID: 1, Title: "iterate"}}}, 1, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := graph.StartNext(t.Context(), "token")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.FinishTool(t.Context(), attempt.ID, ToolResult{Tool: "run_command", Command: "go test ./...", Summary: "ok", Verification: true, WorkspaceToken: "token"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxAttemptToolEvidence*3; i++ {
		if err := graph.FinishTool(t.Context(), attempt.ID, ToolResult{Tool: "read_file", Summary: fmt.Sprintf("read %d", i), WorkspaceToken: "token"}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := graph.Snapshot()
	if len(snapshot.Evidence) > maxAttemptToolEvidence+1 {
		t.Fatalf("retained %d evidence records, want at most %d", len(snapshot.Evidence), maxAttemptToolEvidence+1)
	}
	if snapshot.Attempts[0].EvidencePruned == 0 {
		t.Fatal("pruning was not recorded on the attempt")
	}
	// Verification evidence is what acceptance depends on, so it is never the
	// thing pruning drops, and the newest results survive.
	var verifications int
	for _, item := range snapshot.Evidence {
		if item.Kind == EvidenceVerification {
			verifications++
		}
	}
	if verifications != 1 {
		t.Fatalf("verification evidence count=%d", verifications)
	}
	newest := snapshot.Evidence[len(snapshot.Evidence)-1]
	if newest.Summary != fmt.Sprintf("read %d", maxAttemptToolEvidence*3-1) {
		t.Fatalf("newest evidence was pruned: %+v", newest)
	}
	if err := ValidateSnapshot(snapshot); err != nil {
		t.Fatalf("pruned snapshot invalid: %v", err)
	}
	// The completion gate still sees the verification it needs.
	if decision, err := graph.ProposeCompletion(t.Context(), "done", "token"); err != nil || decision.Kind != DecisionAccepted && decision.Kind != DecisionDone {
		t.Fatalf("decision=%+v error=%v", decision, err)
	}
}

// The remediation lease is what stops a model from burning the graph budget
// re-running an equivalent command, so it must key off the typed gate rather
// than the sentence the runtime happened to render for it.
func TestCompletionGapRenewalUsesTypedKindsNotProse(t *testing.T) {
	verificationGap := &Attempt{CompletionGapKinds: []GapKind{GapNoFreshVerification}, BaseWorkspaceToken: "before"}
	if completionGapAdvanced(verificationGap, nil, ToolResult{Tool: "read_file", WorkspaceToken: "before"}) {
		t.Error("an unrelated read renewed a verification gap")
	}
	if !completionGapAdvanced(verificationGap, nil, ToolResult{Tool: "run_command", Verification: true, WorkspaceToken: "before"}) {
		t.Error("recognized verification did not renew a verification gap")
	}
	writeGap := &Attempt{CompletionGapKinds: []GapKind{GapNoOpWrite}, BaseWorkspaceToken: "before"}
	if completionGapAdvanced(writeGap, nil, ToolResult{Tool: "write_file", WorkspaceToken: "before"}) {
		t.Error("an unchanged workspace renewed a no-op-write gap")
	}
	if !completionGapAdvanced(writeGap, nil, ToolResult{Tool: "write_file", WorkspaceToken: "after"}) {
		t.Error("a real workspace change did not renew a no-op-write gap")
	}
	// Every rendered sentence is derived from a kind, so presentation cannot
	// drift away from the state the controller enforces.
	for _, kind := range []GapKind{GapNoToolEvidence, GapNoStateToken, GapNoOpWrite, GapNoFreshVerification} {
		if description := gapDescription(kind); description == "" || description == string(kind) {
			t.Errorf("gap kind %q has no operator-facing description", kind)
		}
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

// OG-5's first claim is that a restart reproduces a multi-worker schedule
// rather than approximating one. Three things have to survive: which nodes the
// scheduler picks and in what order, that no running attempt is ever resumed
// in place, and that the aggregate envelope is not quietly refilled. The last
// is the one worth stating plainly — a restart is *charged* for the work it
// re-does. Starts that reset on restore would make an unstable session an
// unbounded one, which is precisely the budget a person agreed to when they
// approved the graph.
func TestRestartReproducesTheMultiWorkerScheduleAndItsSpentBudget(t *testing.T) {
	fixture := &graphFixture{}
	spec := Spec{Goal: "diagnose then repair", Nodes: []NodeSpec{
		{ID: 1, Title: "inspect API", Execution: ExecutionReadOnly, Acceptance: []string{"API evidence is grounded"}},
		{ID: 2, Title: "inspect tests", Execution: ExecutionReadOnly, Acceptance: []string{"test evidence is grounded"}},
		{ID: 3, Title: "repair", DependsOn: []int{1, 2}, Execution: ExecutionPrimary},
	}}
	graph, err := New(spec, 1, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	claims, err := graph.StartReadyReads(t.Context(), "state", 2)
	if err != nil || len(claims) != 2 || claims[0].Node.ID != 1 || claims[1].Node.ID != 2 {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	before := graph.Snapshot()
	if before.ReadFanout.Starts != 2 {
		t.Fatalf("fan-out before the restart=%+v", before.ReadFanout)
	}

	// Round-trip through JSON rather than handing the in-memory snapshot
	// across: the durable record is bytes, and a field that only survives
	// because it shared a pointer would not survive an actual restart.
	raw, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	var durable Snapshot
	if err := json.Unmarshal(raw, &durable); err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(durable, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Recover(t.Context(), "state"); err != nil {
		t.Fatal(err)
	}

	recovered := restored.Snapshot()
	// Neither worker is resumed in place. Both attempts are closed as
	// interrupted and preserved, and both nodes are schedulable again.
	for i, attempt := range recovered.Attempts {
		if attempt.State != AttemptInterrupted {
			t.Fatalf("attempt %d state=%s, want every interrupted worker closed", i, attempt.State)
		}
	}
	if recovered.Nodes[0].State != NodeReady || recovered.Nodes[1].State != NodeReady || recovered.Nodes[2].State != NodeProposed {
		t.Fatalf("recovered node states=%+v", recovered.Nodes)
	}
	if recovered.Outcome != "" {
		t.Fatalf("recovery made an interrupted read wave terminal: %q %q", recovered.Outcome, recovered.Reason)
	}
	// The spent envelope carries across the restart untouched, including the
	// wall clock the wave started against.
	if recovered.ReadFanout != before.ReadFanout {
		t.Fatalf("fan-out after recovery=%+v, want %+v", recovered.ReadFanout, before.ReadFanout)
	}

	next, err := restored.StartReadyReads(t.Context(), "state", 2)
	if err != nil || len(next) != 2 {
		t.Fatalf("reissued claims=%+v err=%v", next, err)
	}
	if next[0].Node.ID != claims[0].Node.ID || next[1].Node.ID != claims[1].Node.ID {
		t.Fatalf("restart reordered the schedule: %d,%d then %d,%d",
			claims[0].Node.ID, claims[1].Node.ID, next[0].Node.ID, next[1].Node.ID)
	}
	// Fresh attempts, not the interrupted ones handed back.
	if next[0].Attempt.ID == claims[0].Attempt.ID || next[1].Attempt.ID == claims[1].Attempt.ID {
		t.Fatalf("restart reused an interrupted attempt: %s %s", next[0].Attempt.ID, next[1].Attempt.ID)
	}
	if spent := restored.Snapshot().ReadFanout.Starts; spent != 4 {
		t.Fatalf("starts after re-claiming=%d, want the restart charged for both (4)", spent)
	}
}

// The graduation gate forbids reporting done with an open required node.
// Deleting the node is the way around that rule, and it is a move the model
// can make: revision is its tool, and a graph it cannot finish becomes one it
// can by proposing a smaller one. Retiring work is legitimate — requirements
// genuinely turn out unnecessary — so the answer is not to forbid it but to
// stop the terminal state from claiming the removed node passed anything.
func TestDoneNeverClaimsARetiredNodePassedItsGates(t *testing.T) {
	fixture := &graphFixture{}
	graph, err := New(Spec{Goal: "implement and document", Nodes: []NodeSpec{
		{ID: 1, Title: "implement", Acceptance: []string{"tests pass"}},
		{ID: 2, Title: "document", Acceptance: []string{"docs updated"}},
	}}, 1, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := graph.StartNext(t.Context(), "state")
	if err != nil {
		t.Fatal(err)
	}
	if decision := successfulRead(t, graph, attempt, "state"); decision.Kind != DecisionAccepted {
		t.Fatalf("first node decision=%+v", decision)
	}

	// The model proposes a graph that simply does not contain node 2.
	if err := graph.Revise(t.Context(), graph.Snapshot().Generation, "node 2 turned out to be unnecessary", Spec{
		Goal:  "implement and document",
		Nodes: []NodeSpec{{ID: 1, Title: "implement", Acceptance: []string{"tests pass"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := graph.StartNext(t.Context(), "state"); !errors.Is(err, ErrGraphTerminal) {
		t.Fatalf("graph did not settle after the revision: %v", err)
	}

	snapshot := graph.Snapshot()
	if snapshot.Outcome != OutcomeDone {
		t.Fatalf("outcome=%q, want done: legitimate replanning must still be able to finish", snapshot.Outcome)
	}
	// The reason is the whole point. "All required nodes passed" is false about
	// a node that was deleted, and a reader deciding whether the goal was met
	// has to be told the plan shrank and what left with it.
	if strings.HasPrefix(snapshot.Reason, "all required nodes passed") {
		t.Fatalf("done claims every required node passed after one was removed: %q", snapshot.Reason)
	}
	for _, phrase := range []string{"the approved plan was reduced", "node 2 (document)", "node 2 turned out to be unnecessary"} {
		if !strings.Contains(snapshot.Reason, phrase) {
			t.Fatalf("the done reason does not say %q:\n%s", phrase, snapshot.Reason)
		}
	}

	retired := graph.RetiredNodes()
	if len(retired) != 1 || retired[0].ID != 2 || retired[0].State != NodeReady {
		t.Fatalf("retired record=%+v, want node 2 as it stood when it was removed", retired)
	}
	// An operator sees it in status, where the node list cannot show it: the
	// graph no longer contains that node at all.
	status, err := graph.Inspect(0)
	if err != nil || !strings.Contains(status, "Retired by revision") || !strings.Contains(status, "node 2 (document)") {
		t.Fatalf("status does not account for the retired node err=%v\n%s", err, status)
	}
	// The account is durable. A restart that lost it would restore a graph
	// whose done reason no longer matched its own record.
	restored, err := Restore(snapshot, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.RetiredNodes(); len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("restored retirements=%+v", got)
	}
}

// Removing work that already finished is not a retirement. Its evidence
// stands, and recording it as abandoned would overstate the loss in exactly
// the direction the record exists to prevent overstating.
func TestRemovingACompletedNodeIsNotRecordedAsRetired(t *testing.T) {
	fixture := &graphFixture{}
	graph, err := New(Spec{Goal: "implement then verify", Nodes: []NodeSpec{
		{ID: 1, Title: "implement", Acceptance: []string{"tests pass"}},
		{ID: 2, Title: "verify", Acceptance: []string{"suite is green"}},
	}}, 1, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := graph.StartNext(t.Context(), "state")
	if err != nil {
		t.Fatal(err)
	}
	if decision := successfulRead(t, graph, attempt, "state"); decision.Kind != DecisionAccepted {
		t.Fatalf("first node decision=%+v", decision)
	}
	if err := graph.Revise(t.Context(), graph.Snapshot().Generation, "the implemented node is no longer part of the plan", Spec{
		Goal:  "implement then verify",
		Nodes: []NodeSpec{{ID: 2, Title: "verify", Acceptance: []string{"suite is green"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if retired := graph.RetiredNodes(); len(retired) != 0 {
		t.Fatalf("a completed node was recorded as retired: %+v", retired)
	}
}

// A snapshot may not claim a retirement that did not happen. The record is
// what keeps a terminal state from overstating what passed, so a forged or
// incoherent one has to be rejected before it can be scheduled against.
func TestSnapshotRejectsAnIncoherentRetirementRecord(t *testing.T) {
	fixture := &graphFixture{}
	graph, err := New(Spec{Goal: "implement", Nodes: []NodeSpec{
		{ID: 1, Title: "implement", Acceptance: []string{"tests pass"}},
	}}, 1, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	base := graph.Snapshot()
	valid := RetiredNode{ID: 2, Title: "document", State: NodeReady, Reason: "dropped", Generation: 1, Time: base.Created}

	for name, mutate := range map[string]func(RetiredNode) RetiredNode{
		"a node the graph still contains": func(r RetiredNode) RetiredNode { r.ID = 1; return r },
		"completed work":                  func(r RetiredNode) RetiredNode { r.State = NodeDone; return r },
		"no reason":                       func(r RetiredNode) RetiredNode { r.Reason = ""; return r },
		"no identity":                     func(r RetiredNode) RetiredNode { r.Title = ""; return r },
		"no time":                         func(r RetiredNode) RetiredNode { r.Time = time.Time{}; return r },
	} {
		snapshot := cloneSnapshot(base)
		snapshot.RetiredNodes = []RetiredNode{mutate(valid)}
		if err := ValidateSnapshot(snapshot); err == nil {
			t.Fatalf("a retirement record claiming %s was accepted", name)
		}
	}

	// The coherent record is accepted, so the cases above fail for their own
	// reason rather than because retirements are rejected wholesale.
	snapshot := cloneSnapshot(base)
	snapshot.RetiredNodes = []RetiredNode{valid}
	if err := ValidateSnapshot(snapshot); err != nil {
		t.Fatalf("a coherent retirement record was rejected: %v", err)
	}
}

// verifiedMutatingNode drives one node through a mutation and a passing check,
// which is the shape every gate in a mutating plan has.
func verifiedMutatingNode(t *testing.T, graph *Graph, startToken, endToken, command string) Decision {
	t.Helper()
	_, attempt, err := graph.StartNext(t.Context(), startToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.BeginTool(t.Context(), attempt.ID, ToolAction{Tool: "edit_file", Risk: "write", Summary: "change", PotentialMutation: true, NonReplayable: true}, startToken); err != nil {
		t.Fatal(err)
	}
	if err := graph.FinishTool(t.Context(), attempt.ID, ToolResult{Tool: "edit_file", Risk: "write", Summary: "changed", WorkspaceToken: endToken}); err != nil {
		t.Fatal(err)
	}
	if err := graph.BeginTool(t.Context(), attempt.ID, ToolAction{Tool: "run_command", Risk: "execute", Summary: command, PotentialMutation: true, NonReplayable: true}, endToken); err != nil {
		t.Fatal(err)
	}
	if err := graph.FinishTool(t.Context(), attempt.ID, ToolResult{Tool: "run_command", Command: command, Risk: "execute", Summary: "ok", Verification: true, WorkspaceToken: endToken}); err != nil {
		t.Fatal(err)
	}
	decision, err := graph.ProposeCompletion(t.Context(), "node complete and checked", endToken)
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

// A node's gate is evaluated against the state it completed in, and nothing
// re-runs it afterwards — nor should it, since staling every finished node on
// each mutation would stop a multi-node plan converging. But that means a later
// node with a narrower check can break an earlier node's work and still let the
// graph finish: feature B's suite passing says nothing about feature A.
//
// The runtime cannot tell a repository-wide check from a narrow one without
// interpreting commands, so it claims neither that the earlier work still holds
// nor that it is broken. It reports which checks ran against a state that is no
// longer current and leaves the conclusion to the reader.
func TestDoneNamesChecksALaterMutationLeftBehind(t *testing.T) {
	fixture := &graphFixture{}
	graph, err := New(Spec{Goal: "two features", Nodes: []NodeSpec{
		{ID: 1, Title: "feature A with tests", Acceptance: []string{"A's tests pass"}},
		{ID: 2, Title: "feature B with tests", DependsOn: []int{1}, Acceptance: []string{"B's tests pass"}},
	}}, 1, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	if decision := verifiedMutatingNode(t, graph, "state-0", "state-A", "go test ./featureA"); decision.Kind != DecisionAccepted {
		t.Fatalf("node 1 decision=%+v", decision)
	}
	if decision := verifiedMutatingNode(t, graph, "state-A", "state-B", "go test ./featureB"); decision.Kind != DecisionDone {
		t.Fatalf("node 2 decision=%+v", decision)
	}

	snapshot := graph.Snapshot()
	if snapshot.Outcome != OutcomeDone {
		t.Fatalf("outcome=%q, want done: a later mutation must not block a finished plan", snapshot.Outcome)
	}
	superseded := graph.SupersededVerifications()
	// Node 2's check ran against the final state, so it is accounted for. Only
	// node 1's is behind — which is also what proves this discriminates rather
	// than flagging every node in a mutating plan.
	if len(superseded) != 1 || superseded[0].NodeID != 1 || superseded[0].Command != "go test ./featureA" || superseded[0].Token != "state-A" {
		t.Fatalf("superseded=%+v, want only node 1's check", superseded)
	}
	for _, phrase := range []string{"have not been re-run against the final workspace", "node 1 (feature A with tests)", "go test ./featureA"} {
		if !strings.Contains(snapshot.Reason, phrase) {
			t.Fatalf("the done reason does not say %q:\n%s", phrase, snapshot.Reason)
		}
	}
	status, err := graph.Inspect(0)
	if err != nil || !strings.Contains(status, "Verified against an earlier workspace state") || !strings.Contains(status, "go test ./featureA") {
		t.Fatalf("status does not surface the superseded check err=%v\n%s", err, status)
	}
}

// The negative control. A plan whose last check describes the state it finished
// in has nothing to disclose, and saying otherwise would make the warning above
// noise that gets ignored.
func TestDoneSaysNothingExtraWhenEveryCheckDescribesTheFinalWorkspace(t *testing.T) {
	fixture := &graphFixture{}
	graph, err := New(Spec{Goal: "one feature", Nodes: []NodeSpec{
		{ID: 1, Title: "feature A with tests", Acceptance: []string{"A's tests pass"}},
	}}, 1, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	if decision := verifiedMutatingNode(t, graph, "state-0", "state-A", "go test ./..."); decision.Kind != DecisionDone {
		t.Fatalf("node 1 decision=%+v", decision)
	}
	if superseded := graph.SupersededVerifications(); len(superseded) != 0 {
		t.Fatalf("a check bound to the final workspace was reported as superseded: %+v", superseded)
	}
	snapshot := graph.Snapshot()
	if snapshot.Reason != "all required nodes passed runtime acceptance gates" {
		t.Fatalf("an unqualified completion gained a qualification: %q", snapshot.Reason)
	}
	status, err := graph.Inspect(0)
	if err != nil || strings.Contains(status, "Verified against an earlier workspace state") {
		t.Fatalf("status warns about nothing err=%v\n%s", err, status)
	}
}

// A candidate can fail verification in four distinguishable ways, and an
// operator needs to know which. Collapsing them into one message — or letting
// the child's raw exit code stand alone — turns a blocked node into a mystery.
func TestCandidateVerificationFailureNamesWhatWentWrong(t *testing.T) {
	cases := []struct {
		name      string
		candidate *WriterCandidate
		raw       string
		want      []string
		absent    string
	}{
		{
			name: "a failed check is named with its command",
			candidate: &WriterCandidate{VerificationState: "failed", Verification: []CandidateVerification{
				{Command: "go build ./...", Status: "failed"},
				{Command: "go vet ./...", Status: "passed"},
			}},
			raw:    "command failed: exit status 1",
			want:   []string{"failed its own verification", "go build ./...", "command failed: exit status 1"},
			absent: "go vet ./...",
		},
		{
			name:      "no verification at all is a different problem from a failing one",
			candidate: &WriterCandidate{VerificationState: "failed"},
			want:      []string{"no machine-observed verification", "nothing established that its changes work"},
		},
		{
			name: "checks that all passed but are not bound to one state",
			candidate: &WriterCandidate{VerificationState: "passed", Verification: []CandidateVerification{
				{Command: "go test ./...", Status: "passed", StateToken: "a"},
			}},
			want: []string{"not bound to a single settled state"},
		},
		{
			name: "no candidate at all",
			raw:  "worktree vanished",
			want: []string{"no candidate to verify", "worktree vanished"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			detail := candidateVerificationFailureDetail(testCase.candidate, testCase.raw)
			for _, phrase := range testCase.want {
				if !strings.Contains(detail, phrase) {
					t.Fatalf("detail does not say %q: %q", phrase, detail)
				}
			}
			// Naming a check that passed would send the operator to the wrong
			// command, which is worse than saying less.
			if testCase.absent != "" && strings.Contains(detail, testCase.absent) {
				t.Fatalf("detail names a check that passed (%q): %q", testCase.absent, detail)
			}
		})
	}
}

// A node that has never run and a node that ran and failed recoverably are
// different situations for an operator. The first is waiting on the review
// above it; the second already has its own reason explaining what went wrong.
// Reporting them together would attach a "not started" label to work that did.
func TestUnstartedNodesExcludeWorkThatAlreadyRan(t *testing.T) {
	fixture := &graphFixture{}
	graph, err := New(Spec{Goal: "implement independent changes", Nodes: []NodeSpec{
		{ID: 1, Title: "change alpha", Execution: ExecutionIsolatedWrite, WritePaths: []string{"alpha/"}, Acceptance: []string{"alpha checks pass"}},
		{ID: 2, Title: "change alpha again", Execution: ExecutionIsolatedWrite, WritePaths: []string{"alpha/"}, Acceptance: []string{"alpha checks still pass"}},
	}}, 1, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	base := WriterBase{WorkspaceToken: "parent-state", Commit: "abcdef", Clean: true}
	claims, err := graph.StartReadyWriters(t.Context(), base, 2)
	// Overlapping scopes are exactly why one node is left behind: two writers
	// with the same scope could collide, so the wave takes one.
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims=%d err=%v, want exactly one for same-scope nodes", len(claims), err)
	}
	if err := graph.FinishWriter(t.Context(), WriterResult{
		AttemptID: claims[0].Attempt.ID, WorkerID: "writer-1", Status: "done",
		Summary: "implemented and checked candidate", Evidence: []string{"edit_file: completed — candidate changed"},
		WritePaths: claims[0].WritePaths, ChangedFiles: []string{"alpha/one.go"},
		Worktree: "/tmp/candidate", Branch: "collomia/writer-1", BaseCommit: base.Commit,
		ParentWorkspaceToken: base.WorkspaceToken, VerificationState: "passed", VerificationToken: "child-state",
		Verification: []CandidateVerification{{Command: "go test ./...", Status: "passed", StateToken: "child-state"}},
	}); err != nil {
		t.Fatal(err)
	}

	waiting := graph.UnstartedNodes()
	if len(waiting) != 1 || waiting[0].NodeID != 2 {
		t.Fatalf("unstarted=%+v, want only node 2", waiting)
	}
	// The terminal state has to carry it. A person reading only that a
	// candidate is retained could release the graph and lose node 2.
	outcome, reason := graph.Outcome()
	if outcome != OutcomeAwaitingReview {
		t.Fatalf("outcome=%q", outcome)
	}
	for _, phrase := range []string{"not started yet", "node 2 (change alpha again)"} {
		if !strings.Contains(reason, phrase) {
			t.Fatalf("the awaiting_review reason does not say %q: %q", phrase, reason)
		}
	}
	// The node that produced the candidate is not itself "unstarted".
	for _, node := range waiting {
		if node.NodeID == 1 {
			t.Fatalf("a node that ran was reported as never started: %+v", waiting)
		}
	}
}
