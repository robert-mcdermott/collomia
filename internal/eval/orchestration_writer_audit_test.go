package eval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// The graduation review made the isolated-writer wave trail read fan-out on one
// stated ground: nearly every defect found across the milestone's audits was in
// the writer or publication path. The release condition that follows from that
// reasoning is its negation — an audit of that path that stops finding things
// which change what a user gets. These are that pass.

type scopeBreakingClient struct {
	mu    sync.Mutex
	calls int
}

func (c *scopeBreakingClient) Name() string { return "scope-breaking" }

func (c *scopeBreakingClient) Chat(_ context.Context, request provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	if !requestHasText(request, "Implement one approved Orchestrated Goal node") {
		return provider.Response{Content: "no primary work", Usage: provider.Usage{InputTokens: 4, OutputTokens: 1}}, nil
	}
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()
	switch call {
	case 1:
		args, _ := json.Marshal(map[string]string{"path": "alpha/extra.go", "content": "package alpha\n\nfunc Extra() string { return \"extra\" }\n"})
		return provider.Response{ToolCalls: []provider.ToolCall{{ID: "in-scope", Name: "write_file", Arguments: args}}, Usage: provider.Usage{InputTokens: 10, OutputTokens: 2}}, nil
	case 2:
		args, _ := json.Marshal(map[string]string{"path": "beta/sneaky.go", "content": "package beta\n\nfunc Sneaky() string { return \"sneaky\" }\n"})
		return provider.Response{ToolCalls: []provider.ToolCall{{ID: "out-of-scope", Name: "write_file", Arguments: args}}, Usage: provider.Usage{InputTokens: 10, OutputTokens: 2}}, nil
	default:
		return provider.Response{Content: "wrote both files", Usage: provider.Usage{InputTokens: 12, OutputTokens: 4}}, nil
	}
}

// Declared write scopes are what make two concurrent writers safe: the wave
// only runs candidates whose scopes are pairwise disjoint, so if scope is not
// actually enforced the disjointness argument is decoration. Three layers of
// unit tests covered the scope helper, but nothing drove a real child writing
// outside its scope in a real worktree.
func TestOrchestratedGoalWriterOutsideItsScopeIsRefusedEvaluation(t *testing.T) {
	workspace := orchestratedWriterGitFixture(t)
	before := writerFixtureState(t, workspace)
	runtime := newWriterEvaluationRuntime(t, workspace, &scopeBreakingClient{}, []plan.Step{
		writerStep(1, "extend alpha", "alpha/"),
	})
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Second)
	defer cancel()
	_, err := runtime.Agent.Run(ctx, "Produce the approved candidate.", nil)
	if err == nil {
		t.Fatal("a candidate that wrote outside its declared scope was accepted")
	}
	outcome, reason := runtime.GoalGraph.Outcome()
	if outcome != goalgraph.OutcomeBlocked {
		t.Fatalf("outcome=%q reason=%q", outcome, reason)
	}
	// The exact path has to be named. "Wrote outside its scope" without saying
	// where leaves the reader to diff two trees by hand.
	if !strings.Contains(reason, "beta/sneaky.go") {
		t.Fatalf("the reason does not name the offending path: %q", reason)
	}

	// The violation is caught before the candidate is ever verified, and the
	// stray file stays quarantined in the worktree rather than reaching the
	// repository — which is the whole reason writers work in one.
	snapshot := runtime.GoalGraph.Snapshot()
	for _, attempt := range snapshot.Attempts {
		if attempt.Candidate == nil {
			continue
		}
		if !containsEvalString(attempt.Candidate.ScopeViolations, "beta/sneaky.go") {
			t.Fatalf("the violation was not recorded on the candidate: %+v", attempt.Candidate.ScopeViolations)
		}
	}
	if after := writerFixtureState(t, workspace); after != before {
		t.Fatalf("a scope-violating candidate changed the parent workspace:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	for _, tree := range runtime.GoalGraph.RetainedWorktrees() {
		if _, statErr := os.Stat(filepath.Join(tree.Worktree, "beta/sneaky.go")); statErr != nil {
			t.Fatalf("the out-of-scope write did not stay quarantined in the worktree: %v", statErr)
		}
	}
}

// Retained worktrees live under the OS temp directory, so a reboot or a cleaner
// removing one between the wave and the integration is ordinary rather than
// exotic. This audit found the refusal correct and its wording wrong: one
// message covered both a tree Git no longer registers and a tree that is simply
// gone, which are opposite problems.
func TestOrchestratedGoalIntegratingAVanishedWorktreeSaysSoEvaluation(t *testing.T) {
	workspace := orchestratedWriterGitFixture(t)
	before := writerFixtureState(t, workspace)
	client := &scopedWriterClient{t: t, written: map[string]bool{}, files: map[string]string{
		"extend alpha": "alpha/extra.go",
	}}
	runtime := newWriterEvaluationRuntime(t, workspace, client, []plan.Step{
		writerStep(1, "extend alpha", "alpha/"),
	})
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Second)
	defer cancel()
	if _, err := runtime.Agent.Run(ctx, "Produce the approved candidate.", nil); err != nil {
		t.Fatalf("wave failed: %v", err)
	}
	trees := runtime.GoalGraph.RetainedWorktrees()
	if len(trees) != 1 {
		t.Fatalf("retained trees=%d", len(trees))
	}
	if err := os.RemoveAll(trees[0].Worktree); err != nil {
		t.Fatal(err)
	}

	_, err := runtime.IntegrateOrchestratedCandidate(ctx, 1)
	if err == nil {
		t.Fatal("integration succeeded against a worktree that no longer exists")
	}
	// A mismatch is worth investigating; an absent directory is a swept temp
	// path. Sending someone to look for a repository problem when the answer is
	// that the work is gone changes what they do next.
	for _, phrase := range []string{"no longer exists", "swept by the operating system", "/orchestrate reconcile"} {
		if !strings.Contains(err.Error(), phrase) {
			t.Fatalf("the refusal does not say %q, so a swept worktree reads as a repository fault: %v", phrase, err)
		}
	}
	if strings.Contains(err.Error(), "is not the recorded Git worktree") {
		t.Fatalf("an absent worktree still reports the mismatch wording: %v", err)
	}

	// The graph is left coherent: nothing was consumed, so the node still holds
	// its candidate and the workspace is untouched.
	if outcome, _ := runtime.GoalGraph.Outcome(); outcome != goalgraph.OutcomeAwaitingReview {
		t.Fatalf("a refused integration changed the outcome to %q", outcome)
	}
	if after := writerFixtureState(t, workspace); after != before {
		t.Fatal("a refused integration changed the parent workspace")
	}
	// And reconciliation can still account for it rather than leaving a
	// dangling pointer nobody can resolve.
	if _, reconcileErr := runtime.ReconcileOrchestratedWorktrees(ctx); reconcileErr != nil {
		t.Fatalf("reconcile after a vanished worktree: %v", reconcileErr)
	}
	for _, tree := range runtime.GoalGraph.RetainedWorktrees() {
		if tree.Disposition != goalgraph.DispositionMissing {
			t.Fatalf("a vanished tree reconciled to %q, want missing", tree.Disposition)
		}
	}
}

func containsEvalString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
