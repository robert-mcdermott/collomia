package eval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
		if err := waitComparisonDelay(ctx, c.delay); err != nil {
			return provider.Response{}, err
		}
		return comparisonToolResponse("standard-first", c.scenario.firstPath), nil
	case 2:
		if !requestHasText(request, c.scenario.firstContent) {
			return provider.Response{}, errors.New("standard mode did not retain the first grounded read")
		}
		if err := waitComparisonDelay(ctx, c.delay); err != nil {
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
		if err := waitComparisonDelay(ctx, c.delay); err != nil {
			return provider.Response{}, err
		}
		return comparisonToolResponse("primary-first", c.scenario.firstPath), nil
	case 2:
		if !requestHasText(request, c.scenario.firstContent) {
			return provider.Response{}, errors.New("primary graph did not ground its first node")
		}
		return comparisonTextResponse(c.scenario.firstSummary), nil
	case 3:
		if err := waitComparisonDelay(ctx, c.delay); err != nil {
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
	if err := waitComparisonDelay(ctx, c.delay); err != nil {
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

func waitComparisonDelay(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
			started := time.Now()
			standardAnswer, err := standard.Run(t.Context(), scenario.goal, nil)
			standardElapsed := time.Since(started)
			if err != nil {
				t.Fatal(err)
			}

			primaryWorkspace := comparisonWorkspace(t, scenario)
			primaryClient := &orchestrationComparisonClient{mode: "primary", scenario: scenario, delay: investigationDelay}
			primary, primaryGraph, _ := newOrchestratedEvaluationAgent(t, primaryWorkspace, primaryClient, "autopilot", comparisonGraphSpec(scenario, goalgraph.ExecutionPrimary))
			started = time.Now()
			primaryAnswer, err := primary.Run(t.Context(), scenario.goal, nil)
			primaryElapsed := time.Since(started)
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
			started = time.Now()
			fanoutAnswer, err := fanout.Agent.Run(t.Context(), scenario.goal, nil)
			fanoutElapsed := time.Since(started)
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
			if fanoutElapsed >= standardElapsed || fanoutElapsed >= primaryElapsed {
				t.Fatalf("controlled elapsed standard=%s primary=%s fanout=%s", standardElapsed, primaryElapsed, fanoutElapsed)
			}
			t.Logf("controlled elapsed standard=%s primary-only=%s fan-out=%s", standardElapsed, primaryElapsed, fanoutElapsed)
			standardUsage := standard.Usage()
			fanoutUsage := fanout.GoalGraph.UsageTotals(time.Time{}).Total
			if standardUsage.InputTokens+standardUsage.OutputTokens >= fanoutUsage.InputTokens+fanoutUsage.OutputTokens || fanoutUsage.Iterations != 6 {
				t.Fatalf("extra work was not visible: standard=%+v fanout=%+v", standardUsage, fanoutUsage)
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
