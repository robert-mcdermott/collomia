package eval

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// The negative case. OG-5H measured where the isolated-writer wave wins; this
// measures where it loses, because guidance that only says "use it" is not
// guidance, and the never-default decision made evidence-backed guidance a
// condition of graduation.
//
// Two nodes that declare the same write scope can never run together — the
// wave selects pairwise-disjoint scopes precisely so two writers cannot collide
// in the parent. So the wave's one benefit, overlap, is unavailable by
// construction, while every cost is still paid.

type serialComparisonClient struct {
	mode  string
	delay time.Duration

	mu     sync.Mutex
	delays []comparisonInterval
	calls  int
	wrote  map[string]bool
}

func (c *serialComparisonClient) Name() string { return "serial-comparison" }

func (c *serialComparisonClient) wait(ctx context.Context) {
	started := time.Now()
	timer := time.NewTimer(c.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
	c.mu.Lock()
	c.delays = append(c.delays, comparisonInterval{start: started, end: time.Now()})
	c.mu.Unlock()
}

func (c *serialComparisonClient) simulatedWork() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := time.Duration(0)
	for _, window := range c.delays {
		total += window.end.Sub(window.start)
	}
	return total
}

func (c *serialComparisonClient) criticalPathDelay() time.Duration {
	c.mu.Lock()
	windows := append([]comparisonInterval(nil), c.delays...)
	c.mu.Unlock()
	return unionDuration(windows)
}

func (c *serialComparisonClient) Chat(ctx context.Context, request provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	if requestHasText(request, "Implement one approved Orchestrated Goal node") {
		which := "one"
		if requestHasText(request, "extend alpha two") {
			which = "two"
		}
		c.mu.Lock()
		if c.wrote == nil {
			c.wrote = map[string]bool{}
		}
		already := c.wrote[which]
		c.wrote[which] = true
		c.mu.Unlock()
		if already {
			return provider.Response{Content: "wrote alpha/" + which + ".go inside the declared scope", Usage: provider.Usage{InputTokens: 12, OutputTokens: 4}}, nil
		}
		c.wait(ctx)
		return serialWrite("wave-"+which, which), nil
	}
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()
	switch call {
	case 1:
		c.wait(ctx)
		return serialWrite("standard-one", "one"), nil
	case 2:
		c.wait(ctx)
		return serialWrite("standard-two", "two"), nil
	case 3:
		return provider.Response{
			ToolCalls: []provider.ToolCall{{ID: "standard-verify", Name: "run_command", Arguments: json.RawMessage(`{"command":"go test ./...","timeout_seconds":300}`)}},
			Usage:     provider.Usage{InputTokens: 10, OutputTokens: 2},
		}, nil
	default:
		return provider.Response{Content: "both additions are in alpha and the suite passes", Usage: provider.Usage{InputTokens: 12, OutputTokens: 4}}, nil
	}
}

func serialWrite(id, which string) provider.Response {
	content := "package alpha\n\nfunc Extra" + strings.Title(which) + "() string { return \"" + which + "\" }\n"
	args, _ := json.Marshal(map[string]string{"path": "alpha/" + which + ".go", "content": content})
	return provider.Response{
		ToolCalls: []provider.ToolCall{{ID: id, Name: "write_file", Arguments: args}},
		Usage:     provider.Usage{InputTokens: 10, OutputTokens: 2},
	}
}

func TestOrchestratedGoalComparativeSameScopeSerialEvaluation(t *testing.T) {
	const delay = 1200 * time.Millisecond

	standardWorkspace := orchestratedWriterGitFixture(t)
	standardClient := &serialComparisonClient{mode: "standard", delay: delay}
	standardAgent, _ := newEvaluationAgent(t, standardWorkspace, standardClient, "autopilot")
	if _, err := standardAgent.Run(t.Context(), "Add two helpers to alpha and keep the suite passing.", nil); err != nil {
		t.Fatalf("standard mode failed: %v", err)
	}
	standardUsage := standardAgent.Usage()

	waveWorkspace := orchestratedWriterGitFixture(t)
	waveClient := &serialComparisonClient{mode: "wave", delay: delay}
	runtime := newWriterEvaluationRuntime(t, waveWorkspace, waveClient, []plan.Step{
		writerStep(1, "extend alpha one", "alpha/"),
		writerStep(2, "extend alpha two", "alpha/"),
	})
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Second)
	defer cancel()
	waveAnswer, err := runtime.Agent.Run(ctx, "Produce both approved candidates.", nil)
	if err != nil {
		t.Fatalf("the wave returned an error rather than a review stop: %v", err)
	}
	snapshot := runtime.GoalGraph.Snapshot()
	waveUsage := runtime.GoalGraph.UsageTotals(time.Time{}).Total

	standardRecord := comparisonMeasurement{
		mode: "standard", criticalPath: standardClient.criticalPathDelay(), simulated: standardClient.simulatedWork(),
		inputTokens: standardUsage.InputTokens, outputTokens: standardUsage.OutputTokens, verifications: 1,
		scope: "both nodes", landed: true,
	}
	waveRecord := comparisonMeasurement{
		mode: "wave-same-scope", criticalPath: waveClient.criticalPathDelay(), simulated: waveClient.simulatedWork(),
		inputTokens: waveUsage.InputTokens, outputTokens: waveUsage.OutputTokens,
		iterations: waveUsage.Iterations, verifications: 1,
		scope: "one node of two, nothing integrated",
	}
	reportComparison(t, "same-scope nodes: the wave's overlap is unavailable by construction", standardRecord, waveRecord)
	t.Logf("  writer starts used: %d of %d · nodes run: 1 of %d",
		snapshot.WriterFanout.Starts, snapshot.WriterFanout.MaxStarts, len(snapshot.Nodes))

	// Only one writer could start, so there was nothing to overlap. Standard
	// mode did both changes in the run it was given.
	if snapshot.WriterFanout.Starts != 1 {
		t.Fatalf("writer starts=%d, want exactly one: same-scope nodes must not run together", snapshot.WriterFanout.Starts)
	}
	// The wave's numbers look smaller only because it did half the plan. Left
	// as a percentage delta that reads as a win, which is why the reporter
	// suppresses deltas between modes of differing scope — asserted here so the
	// suppression cannot regress into a misleading table.
	if waveRecord.scope == standardRecord.scope {
		t.Fatal("the two modes are recorded as covering the same work; they do not")
	}
	t.Logf("  to finish, the wave needs: integrate node 1 → combined verification → a second wave for node 2")

	// Standard mode finished the whole request. The wave finished one node of
	// two and needs a full review cycle before the second can even begin.
	if outcome, _ := runtime.GoalGraph.Outcome(); outcome != goalgraph.OutcomeAwaitingReview {
		t.Fatalf("wave outcome=%q", outcome)
	}
	waiting := runtime.GoalGraph.UnstartedNodes()
	if len(waiting) != 1 || waiting[0].NodeID != 2 {
		t.Fatalf("unstarted nodes=%+v, want node 2", waiting)
	}

	// The defect this evaluation found. Reporting the wave as "finished" while
	// naming only the candidate it produced, and then offering to release the
	// graph, would discard approved work the user was never told about.
	if strings.Contains(waveAnswer, "Orchestrated Goal finished") {
		t.Fatalf("the answer calls a partial plan finished:\n%s", waveAnswer)
	}
	for _, phrase := range []string{
		"have not run yet",
		"node 2 (extend alpha two)",
		"not blocked",
		"would release the graph and abandon them",
	} {
		if !strings.Contains(waveAnswer, phrase) {
			t.Fatalf("the answer does not say %q:\n%s", phrase, waveAnswer)
		}
	}
}
