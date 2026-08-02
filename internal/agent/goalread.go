package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/robert-mcdermott/collomia/internal/goalgraph"
)

// runGoalReadFanout executes graph-owned read claims through the same bounded
// delegated-agent path used by the manual delegate tool. Claims are already
// durable and dependency-ready; this function adds no scheduling authority.
func (a *Agent) runGoalReadFanout(ctx context.Context, claims []goalgraph.ReadClaim, send Emit) error {
	if len(claims) == 0 {
		return nil
	}
	a.mu.RLock()
	graph := a.goalGraph
	cfg, approver, team, scheduler := a.delegateConfig, a.delegateApprover, a.delegateTeam, a.delegateScheduler
	a.mu.RUnlock()
	if graph == nil {
		return errors.New("goal graph is unavailable for automatic read fan-out")
	}
	if scheduler == nil || team == nil {
		return errors.New("delegated-agent scheduler is unavailable for automatic read fan-out")
	}

	results := make([]DelegateResult, len(claims))
	var wg sync.WaitGroup
	for index, claim := range claims {
		wg.Add(1)
		go func(index int, claim goalgraph.ReadClaim) {
			defer wg.Done()
			task := DelegateTask{
				Name:                  fmt.Sprintf("goal-read-%d", claim.Node.ID),
				Task:                  automaticReadPrompt(claim),
				PlanStep:              claim.Node.ID,
				RuntimeID:             "goal-" + claim.Attempt.ID,
				GraphNode:             true,
				TokenBudgetOverride:   claim.TokenBudget,
				CostBudgetOverride:    claim.CostBudgetUSD,
				TimeoutOverride:       claim.TimeoutSeconds,
				MaxIterationsOverride: claim.MaxIterations,
			}
			results[index] = a.runScheduledDelegate(ctx, index, task, cfg, approver, team, scheduler, nil)
		}(index, claim)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		// A turn cancellation is a graph-level terminal decision, not a set of
		// unrelated child failures. Retain completed provider accounting first,
		// then persist the terminal decision so cancellation cannot be misreported
		// as blocked or erase work that the runtime actually observed.
		budgetExhausted := false
		for index, result := range results {
			recordErr := graph.RecordReadUsage(context.WithoutCancel(ctx), readResultFromDelegate(claims[index].Attempt.ID, result, ""))
			budgetExhausted = budgetExhausted || errors.Is(recordErr, goalgraph.ErrAggregateBudget)
		}
		if budgetExhausted {
			a.emitGoalUpdates(send)
			outcome, reason := graph.Outcome()
			return goalGraphTerminalError(outcome, reason)
		}
		_ = graph.Cancel(context.WithoutCancel(ctx), err.Error())
		a.emitGoalUpdates(send)
		return err
	}

	// Account the entire completed wave before interpreting any one result.
	// Otherwise the first over-budget result would make the graph terminal and
	// hide work already spent by its concurrently finishing siblings.
	budgetExhausted := false
	for index, result := range results {
		recordErr := graph.RecordReadUsage(context.WithoutCancel(ctx), readResultFromDelegate(claims[index].Attempt.ID, result, ""))
		if errors.Is(recordErr, goalgraph.ErrAggregateBudget) {
			budgetExhausted = true
			continue
		}
		if recordErr != nil {
			return recordErr
		}
	}
	if budgetExhausted {
		a.emitGoalUpdates(send)
		outcome, reason := graph.Outcome()
		return goalGraphTerminalError(outcome, reason)
	}

	for index, result := range results {
		token, _ := a.goalToken(ctx)
		err := graph.FinishRead(context.WithoutCancel(ctx), readResultFromDelegate(claims[index].Attempt.ID, result, token))
		a.emitGoalUpdates(send)
		if errors.Is(err, goalgraph.ErrAggregateBudget) {
			outcome, reason := graph.Outcome()
			return goalGraphTerminalError(outcome, reason)
		}
		if err != nil && !errors.Is(err, goalgraph.ErrGraphTerminal) {
			return err
		}
	}
	if outcome, reason := graph.Outcome(); outcome != "" {
		return goalGraphTerminalError(outcome, reason)
	}
	return nil
}

func readResultFromDelegate(attemptID string, result DelegateResult, workspaceToken string) goalgraph.ReadResult {
	return goalgraph.ReadResult{
		AttemptID: attemptID, WorkerID: result.ID, Status: result.Status,
		Summary: result.Summary, Error: result.Error, Evidence: result.Evidence,
		ToolSuccesses: completedDelegateToolCount(result.Evidence), WorkspaceToken: workspaceToken,
		Iterations: result.Iterations, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens,
		CostUSD: result.CostUSD, CostAvailable: result.CostAvailable, CostEstimated: result.CostEstimated,
	}
}

func completedDelegateToolCount(evidence []string) int {
	count := 0
	for _, line := range evidence {
		if strings.Contains(line, ": completed —") {
			count++
		}
	}
	return count
}

func automaticReadPrompt(claim goalgraph.ReadClaim) string {
	criteria := ""
	for _, criterion := range claim.Node.Acceptance {
		criteria += "\n- " + criterion
	}
	return fmt.Sprintf(`Investigate one approved Orchestrated Goal node as a read-only specialist.

Node %d: %s
Acceptance criteria:%s

Use available read-only repository tools to ground the answer. Return a concise result with concrete file, symbol, or observed-output references that the primary agent can consume. Do not edit files, run commands, update the shared plan, delegate, or claim that the whole goal is complete.`, claim.Node.ID, claim.Node.Title, criteria)
}
