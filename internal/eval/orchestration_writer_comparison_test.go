package eval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// The isolated-writer wave is where the Orchestrated Goal's cost actually
// sits, and it was the largest hole in the comparison matrix. The read fan-out
// measured in OG-5F overlaps two provider calls; a writer wave creates a Git
// worktree per node, runs the repository's real test suite inside each one, and
// then runs it again over the combined workspace after integration.
//
// Simulated provider latency cannot show that. The suite runs are real, and
// counting them is the only honest way to state what the wave costs.
//
// This comparison also refuses to pretend the two modes finish in the same
// place. Standard mode ends with the work in the user's repository. The wave
// ends at awaiting_review with the parent untouched, and reaching the same end
// state takes two explicit integrations and a combined verification the user
// has to ask for. Comparing cost without saying that would be the most
// misleading number in the file, so the end state is measured too.

const writerComparisonDelay = 1200 * time.Millisecond

type writerComparisonClient struct {
	mode  string
	delay time.Duration

	mu            sync.Mutex
	delays        []comparisonInterval
	suiteCommands int
	standardCalls int
	written       map[string]bool
}

func (c *writerComparisonClient) Name() string { return "writer-wave-comparison" }

func (c *writerComparisonClient) wait(ctx context.Context) {
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

func (c *writerComparisonClient) criticalPathDelay() time.Duration {
	c.mu.Lock()
	windows := append([]comparisonInterval(nil), c.delays...)
	c.mu.Unlock()
	return unionDuration(windows)
}

func (c *writerComparisonClient) simulatedWork() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := time.Duration(0)
	for _, window := range c.delays {
		total += window.end.Sub(window.start)
	}
	return total
}

func (c *writerComparisonClient) Chat(ctx context.Context, request provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	if requestHasText(request, "Implement one approved Orchestrated Goal node") {
		return c.writerChild(ctx, request)
	}
	return c.standard(ctx, request)
}

// standard implements both changes serially in the user's own workspace and
// verifies once, which is what the wave is being compared against.
func (c *writerComparisonClient) standard(ctx context.Context, request provider.Request) (provider.Response, error) {
	c.mu.Lock()
	c.standardCalls++
	call := c.standardCalls
	c.mu.Unlock()
	switch call {
	case 1:
		c.wait(ctx)
		return writerComparisonWrite("standard-alpha", "alpha/extra.go", "alpha"), nil
	case 2:
		c.wait(ctx)
		return writerComparisonWrite("standard-beta", "beta/extra.go", "beta"), nil
	case 3:
		c.mu.Lock()
		c.suiteCommands++
		c.mu.Unlock()
		return provider.Response{
			ToolCalls: []provider.ToolCall{{ID: "standard-verify", Name: "run_command", Arguments: json.RawMessage(`{"command":"go test ./...","timeout_seconds":300}`)}},
			Usage:     provider.Usage{InputTokens: 10, OutputTokens: 2},
		}, nil
	default:
		if !requestHasText(request, "ok") && !requestHasText(request, "PASS") {
			return provider.Response{Content: "both packages extended and the suite passes", Usage: provider.Usage{InputTokens: 12, OutputTokens: 4}}, nil
		}
		return provider.Response{Content: "both packages extended and the suite passes", Usage: provider.Usage{InputTokens: 12, OutputTokens: 4}}, nil
	}
}

// writerChild answers one isolated writer inside its own worktree. It never
// runs the suite itself: the application verifies each candidate tree, which is
// exactly the cost being counted.
func (c *writerComparisonClient) writerChild(ctx context.Context, request provider.Request) (provider.Response, error) {
	pkg := ""
	switch {
	case requestHasText(request, "extend alpha"):
		pkg = "alpha"
	case requestHasText(request, "extend beta"):
		pkg = "beta"
	default:
		return provider.Response{Content: "unexpected writer node", Usage: provider.Usage{InputTokens: 4, OutputTokens: 1}}, nil
	}
	c.mu.Lock()
	already := c.written[pkg]
	if c.written == nil {
		c.written = map[string]bool{}
	}
	c.written[pkg] = true
	c.mu.Unlock()
	if already {
		return provider.Response{Content: "wrote " + pkg + "/extra.go inside the declared scope", Usage: provider.Usage{InputTokens: 12, OutputTokens: 4}}, nil
	}
	c.wait(ctx)
	return writerComparisonWrite("wave-"+pkg, pkg+"/extra.go", pkg), nil
}

func writerComparisonWrite(id, path, pkg string) provider.Response {
	content := "package " + pkg + "\n\nfunc Extra() string { return \"extra\" }\n"
	args, _ := json.Marshal(map[string]string{"path": path, "content": content})
	return provider.Response{
		ToolCalls: []provider.ToolCall{{ID: id, Name: "write_file", Arguments: args}},
		Usage:     provider.Usage{InputTokens: 10, OutputTokens: 2},
	}
}

func unionDuration(windows []comparisonInterval) time.Duration {
	if len(windows) == 0 {
		return 0
	}
	sorted := append([]comparisonInterval(nil), windows...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].start.Before(sorted[j-1].start); j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	total := time.Duration(0)
	current := sorted[0]
	for _, window := range sorted[1:] {
		if window.start.After(current.end) {
			total += current.end.Sub(current.start)
			current = window
			continue
		}
		if window.end.After(current.end) {
			current.end = window.end
		}
	}
	return total + current.end.Sub(current.start)
}

func TestOrchestratedGoalComparativeWriterWaveEvaluation(t *testing.T) {
	// Standard mode: one agent, both packages, one suite run, work landed.
	standardWorkspace := orchestratedWriterGitFixture(t)
	standardClient := &writerComparisonClient{mode: "standard", delay: writerComparisonDelay}
	standardAgent, _ := newEvaluationAgent(t, standardWorkspace, standardClient, "autopilot")
	standardStarted := time.Now()
	if _, err := standardAgent.Run(t.Context(), "Extend both packages and keep the suite passing.", nil); err != nil {
		t.Fatalf("standard mode failed: %v", err)
	}
	standardWall := time.Since(standardStarted)
	standardUsage := standardAgent.Usage()
	standardClient.mu.Lock()
	standardSuites := standardClient.suiteCommands
	standardClient.mu.Unlock()
	standardLanded := fileExists(filepath.Join(standardWorkspace, "alpha/extra.go")) &&
		fileExists(filepath.Join(standardWorkspace, "beta/extra.go"))

	// The wave: two worktrees, two candidate verifications, parent untouched.
	waveWorkspace := orchestratedWriterGitFixture(t)
	waveClient := &writerComparisonClient{mode: "wave", delay: writerComparisonDelay, written: map[string]bool{}}
	runtime := newWriterEvaluationRuntime(t, waveWorkspace, waveClient, []plan.Step{
		writerStep(1, "extend alpha", "alpha/"),
		writerStep(2, "extend beta", "beta/"),
	})
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Second)
	defer cancel()
	waveStarted := time.Now()
	if _, err := runtime.Agent.Run(ctx, "Produce both approved candidates.", nil); err != nil {
		t.Fatalf("the candidate wave failed: %v", err)
	}
	if outcome, _ := runtime.GoalGraph.Outcome(); outcome != goalgraph.OutcomeAwaitingReview {
		t.Fatalf("wave outcome=%q, want awaiting_review", outcome)
	}
	// This is the honest part of the comparison: at the point the wave
	// "finished", the user's repository has nothing in it.
	waveLandedAtReview := fileExists(filepath.Join(waveWorkspace, "alpha/extra.go")) ||
		fileExists(filepath.Join(waveWorkspace, "beta/extra.go"))
	if waveLandedAtReview {
		t.Fatal("a candidate wave published into the parent workspace without review")
	}

	// Reaching Standard mode's end state takes two explicit integrations and a
	// combined verification nobody ran automatically.
	for _, node := range []int{1, 2} {
		if _, err := runtime.IntegrateOrchestratedCandidate(ctx, node); err != nil {
			t.Fatalf("integrate node %d: %v", node, err)
		}
	}
	if _, err := runtime.VerifyOrchestratedIntegration(ctx, nil); err != nil {
		t.Fatalf("combined verification: %v", err)
	}
	waveWall := time.Since(waveStarted)
	if outcome, _ := runtime.GoalGraph.Outcome(); outcome != goalgraph.OutcomeDone {
		t.Fatalf("outcome after integration and verification=%q, want done", outcome)
	}
	waveLanded := fileExists(filepath.Join(waveWorkspace, "alpha/extra.go")) &&
		fileExists(filepath.Join(waveWorkspace, "beta/extra.go"))

	// Count what the wave actually paid for, in rounds over a whole tree rather
	// than in individual commands. A round is one pass of the repository's
	// detected verification set: the wave runs one per candidate worktree and
	// one more over the combined workspace, so three where standard runs one.
	//
	// Rounds are the comparable unit and commands are not. Each of the wave's
	// rounds executes the full detected set — here go build, go vet, and go
	// test — because the runtime detected it, while standard mode's round
	// contains whatever the model decided to run. Comparing 9 against 1 would
	// be measuring the model's taste; comparing 3 against 1 measures the
	// structure, which is the part that is true of every repository.
	snapshot := runtime.GoalGraph.Snapshot()
	waveRounds, waveCommands := 0, 0
	for _, attempt := range snapshot.Attempts {
		if attempt.Candidate != nil && len(attempt.Candidate.Verification) > 0 {
			waveRounds++
			waveCommands += len(attempt.Candidate.Verification)
			for _, verification := range attempt.Candidate.Verification {
				t.Logf("  candidate round · attempt %s · %s → %s", attempt.ID, verification.Command, verification.Status)
			}
		}
	}
	// The graph keeps only a summary of the combined round, so the command set
	// comes from the same detector the runtime ran it from.
	_, combinedCommands := tools.DetectVerificationCommands(waveWorkspace)
	waveRounds++
	waveCommands += len(combinedCommands)
	for _, command := range combinedCommands {
		t.Logf("  combined round · %s", command.Command)
	}
	waveUsage := runtime.GoalGraph.UsageTotals(time.Time{}).Total

	standardRecord := comparisonMeasurement{
		mode: "standard", criticalPath: standardClient.criticalPathDelay(), simulated: standardClient.simulatedWork(),
		inputTokens: standardUsage.InputTokens, outputTokens: standardUsage.OutputTokens,
		verifications: standardSuites, verificationCommands: standardSuites, wall: standardWall, landed: standardLanded,
	}
	waveRecord := comparisonMeasurement{
		mode: "writer-wave", criticalPath: waveClient.criticalPathDelay(), simulated: waveClient.simulatedWork(),
		inputTokens: waveUsage.InputTokens, outputTokens: waveUsage.OutputTokens, iterations: waveUsage.Iterations,
		verifications: waveRounds, verificationCommands: waveCommands, wall: waveWall, landed: waveLanded,
	}
	reportComparison(t, "isolated-writer wave vs standard", standardRecord, waveRecord)
	t.Logf("  end state: standard landed=%v · wave landed=%v (after 2 explicit integrations and 1 combined verification)",
		standardRecord.landed, waveRecord.landed)

	// Both modes must actually have done the job, or the cost comparison is
	// between two different pieces of work.
	if !standardRecord.landed || !waveRecord.landed {
		t.Fatalf("the modes did not reach the same end state: %+v %+v", standardRecord, waveRecord)
	}
	// The wave overlaps the two implementations, which is its benefit.
	slack := writerComparisonDelay / 2
	if standardRecord.criticalPath < 2*writerComparisonDelay-slack {
		t.Fatalf("standard mode did not serialize both implementations: %s", standardRecord)
	}
	if waveRecord.criticalPath > standardRecord.criticalPath-writerComparisonDelay+slack {
		t.Fatalf("the wave did not shorten the implementation critical path:\n  %s\n  %s", standardRecord, waveRecord)
	}
	// And its cost. The suite count is the number simulated latency hides, and
	// it is the one that scales with a real repository's test time.
	if waveRecord.verifications != 3 || standardRecord.verifications != 1 {
		t.Fatalf("verification rounds are not one per candidate plus one combined:\n  %s\n  %s", standardRecord, waveRecord)
	}
	if waveRecord.tokens() <= standardRecord.tokens() {
		t.Fatalf("the wave's extra model work was not visible:\n  %s\n  %s", standardRecord, waveRecord)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
