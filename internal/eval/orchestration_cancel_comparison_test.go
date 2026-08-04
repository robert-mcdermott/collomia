package eval

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/robert-mcdermott/collomia/internal/agent"
	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// Cancellation is on the comparison list and carries one of the few measures
// the strategy states as an absolute: duplicate or post-cancellation actions
// must remain zero. It is also the case where the two modes differ in a way
// nobody has to interpret — pressing Ctrl-C is not a rare event, and what it
// leaves behind is the whole question.

type cancelComparisonClient struct {
	mode string

	mu           sync.Mutex
	started      int
	afterCancel  int
	cancelled    bool
	bothStarted  chan struct{}
	startedOnce  sync.Once
	wroteInitial bool
	wrote        map[string]bool
}

func (c *cancelComparisonClient) Name() string { return "cancel-comparison" }

// markCancelled is called by the test at the moment it cancels. Every provider
// call entered after this point is counted, and the count must stay at zero.
func (c *cancelComparisonClient) markCancelled() {
	c.mu.Lock()
	c.cancelled = true
	c.mu.Unlock()
}

func (c *cancelComparisonClient) postCancellationCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.afterCancel
}

func (c *cancelComparisonClient) enter() {
	c.mu.Lock()
	if c.cancelled {
		c.afterCancel++
	}
	c.mu.Unlock()
}

func (c *cancelComparisonClient) Chat(ctx context.Context, request provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	c.enter()
	if requestHasText(request, "Implement one approved Orchestrated Goal node") {
		pkg := "alpha"
		if requestHasText(request, "extend beta") {
			pkg = "beta"
		}
		c.mu.Lock()
		if c.wrote == nil {
			c.wrote = map[string]bool{}
		}
		already := c.wrote[pkg]
		c.wrote[pkg] = true
		c.mu.Unlock()
		if !already {
			// The writer must actually produce something before it is
			// cancelled. A worktree holding no changes is removed rather than
			// retained, which is correct — an empty directory is not evidence —
			// so cancelling an idle writer would prove nothing about whether
			// work in progress survives.
			content := "package " + pkg + "\n\nfunc Extra() string { return \"extra\" }\n"
			args, _ := json.Marshal(map[string]string{"path": pkg + "/extra.go", "content": content})
			return provider.Response{
				ToolCalls: []provider.ToolCall{{ID: "wave-" + pkg, Name: "write_file", Arguments: args}},
				Usage:     provider.Usage{InputTokens: 10, OutputTokens: 2},
			}, nil
		}
		c.mu.Lock()
		c.started++
		both := c.started >= 2
		c.mu.Unlock()
		if both {
			c.startedOnce.Do(func() { close(c.bothStarted) })
		}
		// Now hold the writer, with changes already in its worktree, until the
		// test cancels.
		<-ctx.Done()
		return provider.Response{}, ctx.Err()
	}
	c.mu.Lock()
	first := !c.wroteInitial
	c.wroteInitial = true
	c.mu.Unlock()
	if first {
		// Standard mode writes into the user's workspace before anything can
		// be cancelled. That is the point of the comparison, not a trick.
		content := "package alpha\n\nfunc Extra() string { return \"extra\" }\n"
		args, _ := json.Marshal(map[string]string{"path": "alpha/extra.go", "content": content})
		return provider.Response{
			ToolCalls: []provider.ToolCall{{ID: "standard-write", Name: "write_file", Arguments: args}},
			Usage:     provider.Usage{InputTokens: 10, OutputTokens: 2},
		}, nil
	}
	c.startedOnce.Do(func() { close(c.bothStarted) })
	<-ctx.Done()
	return provider.Response{}, ctx.Err()
}

func TestOrchestratedGoalComparativeCancellationEvaluation(t *testing.T) {
	// Standard mode: cancelled after it has already written into the workspace.
	standardWorkspace := orchestratedWriterGitFixture(t)
	standardBefore := writerFixtureState(t, standardWorkspace)
	standardClient := &cancelComparisonClient{mode: "standard", bothStarted: make(chan struct{})}
	standardAgent, _ := newEvaluationAgent(t, standardWorkspace, standardClient, "autopilot")
	standardCtx, cancelStandard := context.WithCancel(t.Context())
	standardResult := make(chan error, 1)
	go func() {
		_, runErr := standardAgent.Run(standardCtx, "Extend alpha and beta.", nil)
		standardResult <- runErr
	}()
	select {
	case <-standardClient.bothStarted:
	case <-time.After(15 * time.Second):
		cancelStandard()
		t.Fatal("standard mode never reached a cancellable point")
	}
	standardClient.markCancelled()
	cancelStandard()
	select {
	case <-standardResult:
	case <-time.After(15 * time.Second):
		t.Fatal("cancelled standard run did not stop")
	}
	standardAfter := writerFixtureState(t, standardWorkspace)
	standardContaminated := standardAfter != standardBefore

	// The wave: cancelled with both writers in flight inside their worktrees.
	waveWorkspace := orchestratedWriterGitFixture(t)
	waveBefore := writerFixtureState(t, waveWorkspace)
	waveClient := &cancelComparisonClient{mode: "wave", bothStarted: make(chan struct{})}
	runtime := newWriterEvaluationRuntime(t, waveWorkspace, waveClient, []plan.Step{
		writerStep(1, "extend alpha", "alpha/"),
		writerStep(2, "extend beta", "beta/"),
	})
	waveCtx, cancelWave := context.WithCancel(t.Context())
	waveResult := make(chan error, 1)
	go func() {
		_, runErr := runtime.Agent.Run(waveCtx, "Produce both approved candidates.", nil)
		waveResult <- runErr
	}()
	select {
	case <-waveClient.bothStarted:
	case <-time.After(30 * time.Second):
		cancelWave()
		t.Fatal("both writers never started")
	}
	waveClient.markCancelled()
	cancelWave()
	select {
	case runErr := <-waveResult:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("cancelled wave error=%v, want context.Canceled", runErr)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("cancelled writer wave did not stop")
	}
	waveAfter := writerFixtureState(t, waveWorkspace)
	waveContaminated := waveAfter != waveBefore
	outcome, _ := runtime.GoalGraph.Outcome()
	trees := runtime.GoalGraph.RetainedWorktrees()

	t.Logf("\ncancellation · Ctrl-C with work in flight\n"+
		"  standard     workspace changed=%v · post-cancellation provider calls=%d\n"+
		"  writer-wave  workspace changed=%v · post-cancellation provider calls=%d · outcome=%q · retained worktrees=%d",
		standardContaminated, standardClient.postCancellationCalls(),
		waveContaminated, waveClient.postCancellationCalls(), outcome, len(trees))

	// The absolute the strategy states: zero post-cancellation actions. It has
	// to hold in both modes, or cancellation means nothing in either.
	if calls := standardClient.postCancellationCalls(); calls != 0 {
		t.Fatalf("standard mode made %d provider call(s) after cancellation", calls)
	}
	if calls := waveClient.postCancellationCalls(); calls != 0 {
		t.Fatalf("the wave made %d provider call(s) after cancellation", calls)
	}

	// What cancelling costs you. Standard mode had already written into the
	// repository, and that half-finished change is still there.
	if !standardContaminated {
		t.Fatal("standard mode left nothing behind; the comparison has no baseline")
	}
	// The wave's writers were mid-flight in their own worktrees, so there is
	// nothing to undo in the user's repository.
	if waveContaminated {
		t.Fatalf("a cancelled wave changed the parent workspace:\nbefore:\n%s\nafter:\n%s", waveBefore, waveAfter)
	}
	if outcome != goalgraph.OutcomeCancelled {
		t.Fatalf("cancelled graph outcome=%q", outcome)
	}

	// A cancelled writer's worktree is still retained and still attributable.
	// Cancellation is not a licence to lose track of a directory that exists.
	if len(trees) == 0 {
		t.Fatal("a cancelled wave retained no attributable worktree")
	}
	for _, tree := range trees {
		if tree.NodeID == 0 || tree.AttemptID == "" || tree.Worktree == "" {
			t.Fatalf("a retained worktree is not attributable after cancellation: %+v", tree)
		}
	}
	for _, status := range runtime.Team.Snapshot() {
		if status.Status != agent.DelegateCancelled {
			t.Fatalf("worker status after cancellation=%+v", status)
		}
	}
	t.Logf("  retained trees remain attributable to their nodes and are listed by /orchestrate reconcile")
	_ = filepath.Join
}
