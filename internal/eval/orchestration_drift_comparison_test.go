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

// Parent and child drift is the last entry on the comparison list, and the one
// where the modes differ structurally rather than by degree. Standard mode has
// one workspace, so there is nothing to drift against and no concept of
// staleness to detect: whatever the user edits mid-run is simply the state the
// agent is working in, and it will be silently overwritten or silently built
// upon. The wave pins a candidate to the base it started from, so an edit the
// user makes while a writer runs is detectable — and the question this measures
// is what the runtime does when it detects one.

type driftComparisonClient struct {
	t         *testing.T
	workspace string
	mode      string

	mu      sync.Mutex
	calls   int
	wrote   bool
	edited  bool
	edition string
}

func (c *driftComparisonClient) Name() string { return "drift-comparison" }

// editParent stands in for the user changing their own repository while work is
// in flight. It touches a file outside every declared write scope, so nothing
// here is a scope violation — only a moved base.
func (c *driftComparisonClient) editParent() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.edited {
		return
	}
	c.edited = true
	c.edition = "package beta\n\nfunc Name() string { return \"edited by the user\" }\n"
	mustWriteEvaluationFile(c.t, filepath.Join(c.workspace, "beta", "beta.go"), c.edition)
}

func (c *driftComparisonClient) Chat(_ context.Context, request provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	if requestHasText(request, "Implement one approved Orchestrated Goal node") {
		c.mu.Lock()
		already := c.wrote
		c.wrote = true
		c.mu.Unlock()
		if already {
			c.editParent()
			return provider.Response{Content: "wrote alpha/extra.go inside the declared scope", Usage: provider.Usage{InputTokens: 12, OutputTokens: 4}}, nil
		}
		return driftWrite("wave-alpha")
	}
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()
	switch call {
	case 1:
		return driftWrite("standard-alpha")
	case 2:
		c.editParent()
		return provider.Response{
			ToolCalls: []provider.ToolCall{{ID: "standard-verify", Name: "run_command", Arguments: json.RawMessage(`{"command":"go build ./...","timeout_seconds":300}`)}},
			Usage:     provider.Usage{InputTokens: 10, OutputTokens: 2},
		}, nil
	default:
		return provider.Response{Content: "alpha extended and the build is green", Usage: provider.Usage{InputTokens: 12, OutputTokens: 4}}, nil
	}
}

func driftWrite(id string) (provider.Response, error) {
	content := "package alpha\n\nfunc Extra() string { return \"extra\" }\n"
	args, _ := json.Marshal(map[string]string{"path": "alpha/extra.go", "content": content})
	return provider.Response{
		ToolCalls: []provider.ToolCall{{ID: id, Name: "write_file", Arguments: args}},
		Usage:     provider.Usage{InputTokens: 10, OutputTokens: 2},
	}, nil
}

func TestOrchestratedGoalComparativeParentDriftEvaluation(t *testing.T) {
	// Standard mode: the user's edit lands in the same workspace the agent is
	// working in, and nothing notices.
	standardWorkspace := orchestratedWriterGitFixture(t)
	standardClient := &driftComparisonClient{t: t, workspace: standardWorkspace, mode: "standard"}
	standardAgent, _ := newEvaluationAgent(t, standardWorkspace, standardClient, "autopilot")
	if _, err := standardAgent.Run(t.Context(), "Extend alpha.", nil); err != nil {
		t.Fatalf("standard mode failed: %v", err)
	}

	// The wave: the same edit, made while the writer is inside its worktree.
	waveWorkspace := orchestratedWriterGitFixture(t)
	waveClient := &driftComparisonClient{t: t, workspace: waveWorkspace, mode: "wave"}
	runtime := newWriterEvaluationRuntime(t, waveWorkspace, waveClient, []plan.Step{
		writerStep(1, "extend alpha", "alpha/"),
	})
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Second)
	defer cancel()
	_, waveErr := runtime.Agent.Run(ctx, "Produce the approved candidate.", nil)
	outcome, reason := runtime.GoalGraph.Outcome()
	trees := runtime.GoalGraph.RetainedWorktrees()

	t.Logf("\nparent drift · the user edits their repository while work is in flight\n"+
		"  standard     detected=false — one workspace, so there is no base to drift from\n"+
		"  writer-wave  detected=true · outcome=%q · retained candidates=%d\n"+
		"               %s", outcome, len(trees), reason)

	// Standard mode's edit survives because nothing competed with it here, but
	// nothing checked either: the agent's own verification ran against a
	// workspace that had changed under it, and no record says so.
	current, err := os.ReadFile(filepath.Join(standardWorkspace, "beta", "beta.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(current), "edited by the user") {
		t.Fatal("the standard-mode fixture did not apply the user's edit; the comparison has no baseline")
	}

	// The wave detects it and refuses to treat the candidate as integrable.
	if outcome != goalgraph.OutcomeBlocked {
		t.Fatalf("a wave whose parent moved reached outcome %q reason=%q err=%v", outcome, reason, waveErr)
	}
	// Which thing moved is knowable, so saying "the parent workspace or the Git
	// base" would offer a choice of two explanations the runtime could have
	// distinguished.
	if !strings.Contains(reason, "the parent workspace changed") {
		t.Fatalf("the reason does not name what moved: %q", reason)
	}
	if strings.Contains(reason, "workspace or candidate Git base") {
		t.Fatalf("the reason still offers an undistinguished either/or: %q", reason)
	}
	// This is the one rejection where the work is finished, passed its own
	// checks, and is still on disk. A reason that omits that reads as loss.
	for _, phrase := range []string{"passed its own checks", "retained at", "nothing is lost"} {
		if !strings.Contains(reason, phrase) {
			t.Fatalf("the reason does not say %q, so a verified candidate reads as lost: %q", phrase, reason)
		}
	}
	if len(trees) != 1 {
		t.Fatalf("retained candidates=%d, want the drifted candidate kept for inspection", len(trees))
	}
	if _, statErr := os.Stat(filepath.Join(trees[0].Worktree, "alpha/extra.go")); statErr != nil {
		t.Fatalf("the drifted candidate's work is not on disk: %v", statErr)
	}
}
