package eval

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// The comparison so far has measured two successes against each other, which
// is the case the isolated-writer wave is least suited to winning: it pays a
// verification multiplier for a review boundary nothing needed. Its value, if
// it has one, is supposed to appear when work goes wrong.
//
// So this measures the same failure in both modes and asks one question the
// speed and token numbers cannot answer: at the moment the change is known to
// be bad, what is in the user's repository?
//
// This is the quality half of the graduation gate's improvement clause, which
// had no evidence at all.

// brokenAlpha does not compile. It fails the detected verification set at its
// first command, which is what makes it a clean test of where bad bytes end up
// rather than of how a suite reports them.
const brokenAlpha = "package alpha\n\nfunc Extra() string { return undefinedSymbol }\n"

type failureComparisonClient struct {
	mu    sync.Mutex
	calls int
	wrote bool
}

func (c *failureComparisonClient) Name() string { return "failure-comparison" }

func (c *failureComparisonClient) Chat(_ context.Context, request provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	if requestHasText(request, "Implement one approved Orchestrated Goal node") {
		return c.writerChild()
	}
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()
	switch call {
	case 1:
		args, _ := json.Marshal(map[string]string{"path": "alpha/extra.go", "content": brokenAlpha})
		return provider.Response{
			ToolCalls: []provider.ToolCall{{ID: "standard-write", Name: "write_file", Arguments: args}},
			Usage:     provider.Usage{InputTokens: 10, OutputTokens: 2},
		}, nil
	case 2:
		return provider.Response{
			ToolCalls: []provider.ToolCall{{ID: "standard-verify", Name: "run_command", Arguments: json.RawMessage(`{"command":"go build ./...","timeout_seconds":300}`)}},
			Usage:     provider.Usage{InputTokens: 10, OutputTokens: 2},
		}, nil
	default:
		// The agent cannot repair it and says so. A session that ends here —
		// because the model gave up, the budget ran out, or the terminal was
		// closed — is the ordinary case, not an exotic one.
		return provider.Response{Content: "I could not get alpha to compile.", Usage: provider.Usage{InputTokens: 12, OutputTokens: 4}}, nil
	}
}

// writerChild produces the identical broken change inside its own worktree. The
// application verifies the candidate tree, so nothing here has to fail on
// purpose: the same bytes that break the parent in Standard mode break only the
// candidate here.
func (c *failureComparisonClient) writerChild() (provider.Response, error) {
	c.mu.Lock()
	already := c.wrote
	c.wrote = true
	c.mu.Unlock()
	if already {
		return provider.Response{Content: "wrote alpha/extra.go inside the declared scope", Usage: provider.Usage{InputTokens: 12, OutputTokens: 4}}, nil
	}
	args, _ := json.Marshal(map[string]string{"path": "alpha/extra.go", "content": brokenAlpha})
	return provider.Response{
		ToolCalls: []provider.ToolCall{{ID: "wave-write", Name: "write_file", Arguments: args}},
		Usage:     provider.Usage{InputTokens: 10, OutputTokens: 2},
	}, nil
}

func workspaceBuilds(t *testing.T, workspace string) bool {
	t.Helper()
	command := exec.Command("go", "build", "./...")
	command.Dir = workspace
	return command.Run() == nil
}

func TestOrchestratedGoalComparativeFailureContainmentEvaluation(t *testing.T) {
	// Standard mode writes into the user's repository, then discovers the
	// change is bad.
	standardWorkspace := orchestratedWriterGitFixture(t)
	standardBefore := writerFixtureState(t, standardWorkspace)
	if !workspaceBuilds(t, standardWorkspace) {
		t.Fatal("the fixture does not build before anything ran")
	}
	standardClient := &failureComparisonClient{}
	standardAgent, _ := newEvaluationAgent(t, standardWorkspace, standardClient, "autopilot")
	standardAnswer, err := standardAgent.Run(t.Context(), "Add Extra to alpha and keep the build green.", nil)
	if err != nil {
		t.Fatalf("standard run returned an error rather than a report: %v", err)
	}
	standardAfter := writerFixtureState(t, standardWorkspace)
	standardBuilds := workspaceBuilds(t, standardWorkspace)
	standardContaminated := standardAfter != standardBefore

	// The wave writes into a worktree and verifies there.
	waveWorkspace := orchestratedWriterGitFixture(t)
	waveBefore := writerFixtureState(t, waveWorkspace)
	waveClient := &failureComparisonClient{}
	runtime := newWriterEvaluationRuntime(t, waveWorkspace, waveClient, []plan.Step{
		writerStep(1, "extend alpha", "alpha/"),
	})
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Second)
	defer cancel()
	waveAnswer, waveErr := runtime.Agent.Run(ctx, "Produce the approved candidate.", nil)
	waveAfter := writerFixtureState(t, waveWorkspace)
	waveBuilds := workspaceBuilds(t, waveWorkspace)
	waveContaminated := waveAfter != waveBefore
	outcome, reason := runtime.GoalGraph.Outcome()

	t.Logf("\nfailure containment · the same non-compiling change in both modes\n"+
		"  standard     workspace changed=%v · still builds=%v · answer=%q\n"+
		"  writer-wave  workspace changed=%v · still builds=%v · outcome=%q\n"+
		"               reason=%q err=%v answer=%q",
		standardContaminated, standardBuilds, standardAnswer,
		waveContaminated, waveBuilds, outcome, reason, waveErr, waveAnswer)

	// Standard mode's behaviour is not a defect — it is what writing directly
	// into a workspace means, and it is the baseline the wave is priced
	// against. Pinning it is what makes the wave's result meaningful rather
	// than a claim about an imaginary alternative.
	if !standardContaminated {
		t.Fatal("standard mode left the workspace unchanged; the comparison has no baseline")
	}
	if standardBuilds {
		t.Fatal("the broken change did not break the standard workspace; the fixture proves nothing")
	}

	// The wave's whole claim. The same bytes, written by the same client,
	// reached a candidate tree and never the user's repository.
	if waveContaminated {
		t.Fatalf("a failing candidate changed the parent workspace:\nbefore:\n%s\nafter:\n%s", waveBefore, waveAfter)
	}
	if !waveBuilds {
		t.Fatal("the parent workspace does not build after a failing candidate wave")
	}
	if outcome != goalgraph.OutcomeBlocked {
		t.Fatalf("a candidate that fails its own verification reached outcome %q reason=%q", outcome, reason)
	}
	// A blocked node is the graph declining to use work, and the reason is the
	// only account of why. "command failed: exit status 1" is an exit code, not
	// an explanation: it says neither that the candidate's own verification
	// rejected it nor which check did. Both are recorded on the candidate, so
	// both must appear.
	for _, phrase := range []string{"failed its own verification", "go build ./..."} {
		if !strings.Contains(reason, phrase) {
			t.Fatalf("the blocking reason does not say %q, so an operator cannot act on it: %q", phrase, reason)
		}
	}

	// The candidate has to be recorded as failed rather than merely absent, or
	// the operator cannot tell a rejected change from one that never ran.
	snapshot := runtime.GoalGraph.Snapshot()
	failed := false
	for _, attempt := range snapshot.Attempts {
		if attempt.Candidate == nil {
			continue
		}
		if attempt.Candidate.VerificationState == "failed" {
			failed = true
			for _, verification := range attempt.Candidate.Verification {
				t.Logf("  candidate verification · %s → %s", verification.Command, verification.Status)
			}
		}
	}
	if !failed && reason == "" {
		t.Fatalf("the wave neither retained a failed candidate nor explained itself: %+v", snapshot.Nodes)
	}
	t.Logf("  candidate recorded as failed=%v", failed)
}
