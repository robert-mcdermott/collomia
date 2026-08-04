package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robert-mcdermott/collomia/internal/app"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

type orchestrationComparisonScenario struct {
	name          string
	goal          string
	firstTitle    string
	secondTitle   string
	firstPath     string
	secondPath    string
	firstContent  string
	secondContent string
	firstSummary  string
	secondSummary string
	answer        string
}

type orchestrationComparisonClient struct {
	mode     string
	scenario orchestrationComparisonScenario
	delay    time.Duration

	mu              sync.Mutex
	delays          []comparisonInterval
	calls           int
	parentCalls     int
	activeReads     int
	maxReads        int
	arrivedReads    int
	readWaveReady   chan struct{}
	readWaveReadyDo sync.Once
}

func (c *orchestrationComparisonClient) Name() string { return "orchestration-comparison" }

func (c *orchestrationComparisonClient) Chat(ctx context.Context, request provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	if requestHasText(request, "Investigate one approved Orchestrated Goal node") {
		return c.automaticRead(ctx, request)
	}
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()
	switch c.mode {
	case "standard":
		return c.standard(ctx, request, call)
	case "primary":
		return c.primary(ctx, request, call)
	case "fanout":
		return c.fanoutPrimary(request)
	default:
		return provider.Response{}, errors.New("unknown comparison mode")
	}
}

func (c *orchestrationComparisonClient) standard(ctx context.Context, request provider.Request, call int) (provider.Response, error) {
	switch call {
	case 1:
		if err := c.wait(ctx, c.delay); err != nil {
			return provider.Response{}, err
		}
		return comparisonToolResponse("standard-first", c.scenario.firstPath), nil
	case 2:
		if !requestHasText(request, c.scenario.firstContent) {
			return provider.Response{}, errors.New("standard mode did not retain the first grounded read")
		}
		if err := c.wait(ctx, c.delay); err != nil {
			return provider.Response{}, err
		}
		return comparisonToolResponse("standard-second", c.scenario.secondPath), nil
	case 3:
		if !requestHasText(request, c.scenario.firstContent) || !requestHasText(request, c.scenario.secondContent) {
			return provider.Response{}, errors.New("standard mode answered without both grounded reads")
		}
		return comparisonTextResponse(c.scenario.answer), nil
	default:
		return provider.Response{}, errors.New("standard mode made an unexpected provider request")
	}
}

func (c *orchestrationComparisonClient) primary(ctx context.Context, request provider.Request, call int) (provider.Response, error) {
	switch call {
	case 1:
		if err := c.wait(ctx, c.delay); err != nil {
			return provider.Response{}, err
		}
		return comparisonToolResponse("primary-first", c.scenario.firstPath), nil
	case 2:
		if !requestHasText(request, c.scenario.firstContent) {
			return provider.Response{}, errors.New("primary graph did not ground its first node")
		}
		return comparisonTextResponse(c.scenario.firstSummary), nil
	case 3:
		if err := c.wait(ctx, c.delay); err != nil {
			return provider.Response{}, err
		}
		return comparisonToolResponse("primary-second", c.scenario.secondPath), nil
	case 4:
		if !requestHasText(request, c.scenario.secondContent) {
			return provider.Response{}, errors.New("primary graph did not ground its second node")
		}
		return comparisonTextResponse(c.scenario.secondSummary), nil
	case 5:
		if !requestHasText(request, c.scenario.firstSummary) || !requestHasText(request, c.scenario.secondSummary) {
			return provider.Response{}, errors.New("primary synthesis did not receive both accepted node summaries")
		}
		return comparisonToolResponse("primary-synthesis", c.scenario.firstPath), nil
	case 6:
		return comparisonTextResponse(c.scenario.answer), nil
	default:
		return provider.Response{}, errors.New("primary graph made an unexpected provider request")
	}
}

func (c *orchestrationComparisonClient) automaticRead(ctx context.Context, request provider.Request) (provider.Response, error) {
	node, path, content, summary := 0, "", "", ""
	switch {
	case requestHasText(request, "Node 1: "+c.scenario.firstTitle):
		node, path, content, summary = 1, c.scenario.firstPath, c.scenario.firstContent, c.scenario.firstSummary
	case requestHasText(request, "Node 2: "+c.scenario.secondTitle):
		node, path, content, summary = 2, c.scenario.secondPath, c.scenario.secondContent, c.scenario.secondSummary
	default:
		return provider.Response{}, errors.New("automatic comparison worker was not tied to an approved node")
	}
	if err := requireAutomaticReadSurface(request); err != nil {
		return provider.Response{}, err
	}
	if requestHasToolResult(request) {
		if !requestHasText(request, content) {
			return provider.Response{}, errors.New("automatic worker summary lacked its grounded file content")
		}
		return comparisonTextResponse(summary), nil
	}
	c.mu.Lock()
	c.activeReads++
	c.arrivedReads++
	if c.activeReads > c.maxReads {
		c.maxReads = c.activeReads
	}
	if c.arrivedReads == 2 {
		c.readWaveReadyDo.Do(func() { close(c.readWaveReady) })
	}
	c.mu.Unlock()
	select {
	case <-c.readWaveReady:
	case <-ctx.Done():
		c.finishComparisonRead()
		return provider.Response{}, ctx.Err()
	}
	if err := c.wait(ctx, c.delay); err != nil {
		c.finishComparisonRead()
		return provider.Response{}, err
	}
	c.finishComparisonRead()
	return comparisonToolResponse("fanout-read-"+string(rune('0'+node)), path), nil
}

func (c *orchestrationComparisonClient) finishComparisonRead() {
	c.mu.Lock()
	c.activeReads--
	c.mu.Unlock()
}

func (c *orchestrationComparisonClient) fanoutPrimary(request provider.Request) (provider.Response, error) {
	c.mu.Lock()
	c.parentCalls++
	call := c.parentCalls
	readsFinished := c.arrivedReads == 2 && c.activeReads == 0
	c.mu.Unlock()
	if !readsFinished {
		return provider.Response{}, errors.New("fan-out primary lane started before both reads finished")
	}
	switch call {
	case 1:
		if !requestHasText(request, c.scenario.firstSummary) || !requestHasText(request, c.scenario.secondSummary) {
			return provider.Response{}, errors.New("fan-out synthesis did not receive both accepted summaries")
		}
		return comparisonToolResponse("fanout-synthesis", c.scenario.firstPath), nil
	case 2:
		return comparisonTextResponse(c.scenario.answer), nil
	default:
		return provider.Response{}, errors.New("fan-out primary lane made an unexpected request")
	}
}

func comparisonToolResponse(id, path string) provider.Response {
	return provider.Response{
		ToolCalls: []provider.ToolCall{{ID: id, Name: "read_file", Arguments: json.RawMessage(`{"path":"` + path + `"}`)}},
		Usage:     provider.Usage{InputTokens: 10, OutputTokens: 2},
	}
}

func comparisonTextResponse(content string) provider.Response {
	return provider.Response{Content: content, Usage: provider.Usage{InputTokens: 12, OutputTokens: 4}}
}

// comparisonInterval is one window of simulated investigation latency. The
// harness compares modes by how much of this work overlapped rather than by
// how long the whole process took, because total elapsed time also contains
// fixture setup, Git, and whatever else the machine was doing — noise that is
// unrelated to the scheduling question and large enough to invert the result
// on a loaded runner.
type comparisonInterval struct{ start, end time.Time }

func (c *orchestrationComparisonClient) wait(ctx context.Context, delay time.Duration) error {
	started := time.Now()
	timer := time.NewTimer(delay)
	defer timer.Stop()
	var err error
	select {
	case <-timer.C:
	case <-ctx.Done():
		err = ctx.Err()
	}
	c.mu.Lock()
	c.delays = append(c.delays, comparisonInterval{start: started, end: time.Now()})
	c.mu.Unlock()
	return err
}

// criticalPathDelay is the wall time in which at least one simulated
// investigation was running: the union of the windows, not their sum. Serial
// modes pay every window; a wave that truly overlaps pays them once.
func (c *orchestrationComparisonClient) criticalPathDelay() time.Duration {
	c.mu.Lock()
	windows := append([]comparisonInterval(nil), c.delays...)
	c.mu.Unlock()
	if len(windows) == 0 {
		return 0
	}
	sort.Slice(windows, func(i, j int) bool { return windows[i].start.Before(windows[j].start) })
	total := time.Duration(0)
	current := windows[0]
	for _, window := range windows[1:] {
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

// simulatedWork is the total investigation latency the mode asked for. The gap
// between this and the critical path is the concurrency actually achieved.
func (c *orchestrationComparisonClient) simulatedWork() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := time.Duration(0)
	for _, window := range c.delays {
		total += window.end.Sub(window.start)
	}
	return total
}

// comparisonMeasurement is one mode's record for a scenario. It is the unit
// the graduation gate's cost/benefit clauses are argued from, so it carries the
// price beside the benefit: a mode that finishes sooner by spending more model
// work has not obviously won, and the record has to make that arguable rather
// than settle it by omission.
type comparisonMeasurement struct {
	mode         string
	answer       string
	criticalPath time.Duration
	simulated    time.Duration
	inputTokens  int
	outputTokens int
	iterations   int
}

func (m comparisonMeasurement) tokens() int { return m.inputTokens + m.outputTokens }

func (m comparisonMeasurement) String() string {
	overlap := m.simulated - m.criticalPath
	return fmt.Sprintf("%-12s critical-path %-7s of %-7s simulated (%-7s overlapped) · %d tokens (%d in, %d out) · %d provider iterations",
		m.mode, m.criticalPath.Round(time.Millisecond), m.simulated.Round(time.Millisecond),
		overlap.Round(time.Millisecond), m.tokens(), m.inputTokens, m.outputTokens, m.iterations)
}

// reportComparison prints the modes as one table. The graduation decision needs
// the numbers, not a pass/fail: a clause asking whether an improvement justifies
// its overhead cannot be answered by an assertion that merely held.
func reportComparison(t *testing.T, scenario string, measurements ...comparisonMeasurement) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "\ncomparison · %s\n", scenario)
	for _, measurement := range measurements {
		fmt.Fprintf(&b, "  %s\n", measurement)
	}
	if len(measurements) > 1 {
		base := measurements[0]
		for _, measurement := range measurements[1:] {
			fmt.Fprintf(&b, "  %s vs %s: critical path %+.0f%%, tokens %+.0f%%\n",
				measurement.mode, base.mode,
				percentDelta(float64(measurement.criticalPath), float64(base.criticalPath)),
				percentDelta(float64(measurement.tokens()), float64(base.tokens())))
		}
	}
	t.Log(b.String())
}

func percentDelta(value, base float64) float64 {
	if base == 0 {
		return 0
	}
	return (value - base) / base * 100
}

// TestOrchestratedGoalComparativeReadFanoutEvaluation keeps the conclusion
// deliberately narrow. Under equal deterministic investigation latency and
// equal grounded answers, two independent read nodes have one expensive
// critical-path wave in fan-out mode instead of two serial waves. It also
// records the extra model work rather than treating lower elapsed time as a
// free improvement.
func TestOrchestratedGoalComparativeReadFanoutEvaluation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configDir := filepath.Join(home, ".collomia")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mustWriteEvaluationFile(t, filepath.Join(configDir, "config.json"), `{"default_provider":"fixture","providers":{"fixture":{"type":"openai-compatible","base_url":"http://127.0.0.1:1/v1","model":"scripted","context_window":16000,"max_tokens":256}}}`)

	scenarios := []orchestrationComparisonScenario{
		{
			name: "decomposable repository facts", goal: "reconcile independent service facts",
			firstTitle: "inspect service port", secondTitle: "inspect retry policy",
			firstPath: "service.yaml", secondPath: "retry.txt",
			firstContent: "port: 8088", secondContent: "retries=3",
			firstSummary: "The service listens on port 8088.", secondSummary: "The retry policy permits three attempts.",
			answer: "The service listens on port 8088 and retries three times.",
		},
		{
			name: "cross-layer source and test", goal: "reconcile implementation and test behavior",
			firstTitle: "inspect API behavior", secondTitle: "inspect test expectation",
			firstPath: "calc.go", secondPath: "calc_test.go",
			firstContent: "return a - b", secondContent: "Add(2, 3) != 5",
			firstSummary: "The implementation currently subtracts the second operand.", secondSummary: "The test requires two plus three to equal five.",
			answer: "The implementation subtracts, while the test requires addition.",
		},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// The comparison targets substantive investigations, not a one-line
			// local read where worker startup dominates. Short/trivial work is
			// covered separately and remains on the serial primary lane.
			const investigationDelay = 1500 * time.Millisecond

			standardWorkspace := comparisonWorkspace(t, scenario)
			standardClient := &orchestrationComparisonClient{mode: "standard", scenario: scenario, delay: investigationDelay}
			standard, _ := newEvaluationAgent(t, standardWorkspace, standardClient, "autopilot")
			standardAnswer, err := standard.Run(t.Context(), scenario.goal, nil)
			if err != nil {
				t.Fatal(err)
			}

			primaryWorkspace := comparisonWorkspace(t, scenario)
			primaryClient := &orchestrationComparisonClient{mode: "primary", scenario: scenario, delay: investigationDelay}
			primary, primaryGraph, _ := newOrchestratedEvaluationAgent(t, primaryWorkspace, primaryClient, "autopilot", comparisonGraphSpec(scenario, goalgraph.ExecutionPrimary))
			primaryAnswer, err := primary.Run(t.Context(), scenario.goal, nil)
			if err != nil {
				t.Fatal(err)
			}

			fanoutWorkspace := comparisonWorkspace(t, scenario)
			t.Setenv("GOCACHE", filepath.Join(fanoutWorkspace, ".collomia-eval-cache"))
			fanoutPlan := comparisonPlan(scenario)
			fanout, err := app.New(t.Context(), app.Options{Workspace: fanoutWorkspace, Autonomy: "autopilot", OrchestratedGoal: fanoutPlan})
			if err != nil {
				t.Fatal(err)
			}
			defer fanout.Close()
			fanoutClient := &orchestrationComparisonClient{mode: "fanout", scenario: scenario, delay: investigationDelay, readWaveReady: make(chan struct{})}
			fanout.Agent.SetProvider("offline-evaluation", "scripted", appconfig.Provider{Type: "fixture", MaxTokens: 256, Context: 16_000}, fanoutClient)
			fanoutAnswer, err := fanout.Agent.Run(t.Context(), scenario.goal, nil)
			if err != nil {
				t.Fatal(err)
			}

			if standardAnswer != scenario.answer || primaryAnswer != scenario.answer || fanoutAnswer != scenario.answer {
				t.Fatalf("answers standard=%q primary=%q fanout=%q", standardAnswer, primaryAnswer, fanoutAnswer)
			}
			if outcome, _ := primaryGraph.Outcome(); outcome != goalgraph.OutcomeDone {
				t.Fatalf("primary graph outcome=%q", outcome)
			}
			if outcome, _ := fanout.GoalGraph.Outcome(); outcome != goalgraph.OutcomeDone {
				t.Fatalf("fan-out graph outcome=%q", outcome)
			}
			fanoutClient.mu.Lock()
			maxReads := fanoutClient.maxReads
			fanoutClient.mu.Unlock()
			if maxReads != 2 {
				t.Fatalf("fan-out max concurrent reads=%d", maxReads)
			}
			standardUsage := standard.Usage()
			primaryUsage := primaryGraph.UsageTotals(time.Time{}).Total
			fanoutUsage := fanout.GoalGraph.UsageTotals(time.Time{}).Total
			standardRecord := comparisonMeasurement{
				mode: "standard", answer: standardAnswer,
				criticalPath: standardClient.criticalPathDelay(), simulated: standardClient.simulatedWork(),
				inputTokens: standardUsage.InputTokens, outputTokens: standardUsage.OutputTokens,
			}
			primaryRecord := comparisonMeasurement{
				mode: "graph-serial", answer: primaryAnswer,
				criticalPath: primaryClient.criticalPathDelay(), simulated: primaryClient.simulatedWork(),
				inputTokens: primaryUsage.InputTokens, outputTokens: primaryUsage.OutputTokens, iterations: primaryUsage.Iterations,
			}
			fanoutRecord := comparisonMeasurement{
				mode: "graph-fanout", answer: fanoutAnswer,
				criticalPath: fanoutClient.criticalPathDelay(), simulated: fanoutClient.simulatedWork(),
				inputTokens: fanoutUsage.InputTokens, outputTokens: fanoutUsage.OutputTokens, iterations: fanoutUsage.Iterations,
			}
			reportComparison(t, scenario.name, standardRecord, primaryRecord, fanoutRecord)

			// The benefit, measured as overlap rather than as total process
			// time. Both serial modes must pay for both investigations in
			// sequence; the wave must pay for them once. The margin is a whole
			// investigation minus generous slack, so a loaded machine slows
			// every mode without inverting the comparison.
			slack := investigationDelay / 2
			for _, serial := range []comparisonMeasurement{standardRecord, primaryRecord} {
				if serial.criticalPath < 2*investigationDelay-slack {
					t.Fatalf("%s did not serialize both investigations: %s", serial.mode, serial)
				}
				if fanoutRecord.criticalPath > serial.criticalPath-investigationDelay+slack {
					t.Fatalf("the wave did not shorten the critical path against %s:\n  %s\n  %s", serial.mode, serial, fanoutRecord)
				}
			}
			// The control that keeps the benefit honest: the wave must have done
			// the same two investigations, not become faster by doing less.
			for _, record := range []comparisonMeasurement{standardRecord, primaryRecord, fanoutRecord} {
				if record.simulated < 2*investigationDelay-slack {
					t.Fatalf("%s skipped an investigation: %s", record.mode, record)
				}
			}
			// The price. A shorter critical path bought with more model work is
			// a trade, not a free improvement, and the record has to show it.
			if fanoutRecord.tokens() <= standardRecord.tokens() || fanoutUsage.Iterations != 6 {
				t.Fatalf("extra model work was not visible:\n  %s\n  %s", standardRecord, fanoutRecord)
			}
		})
	}
}

func comparisonWorkspace(t *testing.T, scenario orchestrationComparisonScenario) string {
	t.Helper()
	workspace := orchestratedGitFixture(t)
	if scenario.firstPath != "calc.go" {
		mustWriteEvaluationFile(t, filepath.Join(workspace, scenario.firstPath), scenario.firstContent+"\n")
	}
	if scenario.secondPath != "calc_test.go" {
		mustWriteEvaluationFile(t, filepath.Join(workspace, scenario.secondPath), scenario.secondContent+"\n")
	}
	if scenario.firstPath != "calc.go" || scenario.secondPath != "calc_test.go" {
		runEvalGit(t, workspace, "add", ".")
		runEvalGit(t, workspace, "commit", "-m", "comparison facts")
	}
	return workspace
}

func comparisonGraphSpec(s orchestrationComparisonScenario, execution goalgraph.Execution) goalgraph.Spec {
	return goalgraph.Spec{Goal: s.goal, Nodes: []goalgraph.NodeSpec{
		{ID: 1, Title: s.firstTitle, Execution: execution, Acceptance: []string{"first fact is grounded"}},
		{ID: 2, Title: s.secondTitle, Execution: execution, Acceptance: []string{"second fact is grounded"}},
		{ID: 3, Title: "synthesize findings", Execution: goalgraph.ExecutionPrimary, DependsOn: []int{1, 2}, Acceptance: []string{"both facts are reconciled"}},
	}}
}

func comparisonPlan(s orchestrationComparisonScenario) *plan.Plan {
	spec := comparisonGraphSpec(s, goalgraph.ExecutionReadOnly)
	result := &plan.Plan{Goal: spec.Goal}
	for _, node := range spec.Nodes {
		result.Steps = append(result.Steps, plan.Step{ID: node.ID, Title: node.Title, Status: "pending", Execution: string(node.Execution), DependsOn: append([]int(nil), node.DependsOn...), Acceptance: append([]string(nil), node.Acceptance...)})
	}
	return result
}
