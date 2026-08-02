package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

func (a *Agent) graphEnabled() bool {
	if a == nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.goalGraph != nil
}

// SetGoalGraph attaches an explicitly approved runtime graph between turns.
// It changes scheduling only: the registry, permissions, hooks, sandbox, and
// budgets remain the same. Callers must not replace a live graph.
func (a *Agent) SetGoalGraph(graph *goalgraph.Graph) error {
	if a == nil {
		return errors.New("agent is unavailable")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.goalGraph != nil && graph != nil && a.goalGraph != graph {
		return errors.New("agent already has a different goal graph")
	}
	if graph != nil && a.subagent {
		return errors.New("delegated agents cannot own an orchestrated goal graph")
	}
	a.goalGraph = graph
	if graph != nil {
		a.planMode = false
	}
	return nil
}

func (a *Agent) goalToken(ctx context.Context) (string, error) {
	if a == nil || a.goalStateToken == nil {
		return "", errors.New("combined-workspace state token provider is unavailable")
	}
	return a.goalStateToken(ctx)
}

func (a *Agent) recordGoalProviderUsage(ctx context.Context, usage provider.Usage, iterations int) error {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	graph := a.goalGraph
	a.mu.RUnlock()
	if graph == nil {
		return nil
	}
	return graph.RecordPrimaryUsage(ctx, goalgraph.WorkUsage{
		Iterations: iterations, InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
		CostUSD: usage.CostUSD, CostAvailable: usage.CostAvailable, CostEstimated: usage.CostEstimated,
	})
}

func (a *Agent) emitGoalUpdates(send Emit) {
	if !a.graphEnabled() {
		return
	}
	for _, update := range a.goalGraph.DrainUpdates() {
		e := event.New(event.KindGoalGraphUpdate)
		e.Time = update.Time
		e.GoalGraph = &event.GoalGraphStatus{
			ID: update.GraphID, Generation: update.Generation, NodeID: update.NodeID,
			AttemptID: update.AttemptID, State: update.State, Reason: update.Reason,
			Ready: append([]int(nil), update.Ready...), Outcome: string(update.Outcome),
		}
		send(e)
	}
}

// ensureGoalAttempt makes exactly one deterministic node active. It is called
// only at provider boundaries, never while a tool or approval is in flight.
func (a *Agent) ensureGoalAttempt(ctx context.Context, send Emit) error {
	if !a.graphEnabled() {
		return nil
	}
	if outcome, reason := a.goalGraph.Outcome(); outcome != "" {
		return goalGraphTerminalError(outcome, reason)
	}
	if requested, reached, _ := a.goalGraph.PauseState(); requested {
		if !reached {
			if err := a.goalGraph.ReachPause(ctx); err != nil {
				return err
			}
			a.emitGoalUpdates(send)
		}
		return goalgraph.ErrGraphPaused
	}
	if _, _, active := a.goalGraph.Active(); active {
		return nil
	}
	token, _ := a.goalToken(ctx) // read-only graphs may run without Git state.
	claims, readErr := a.goalGraph.StartReadyReads(ctx, token, goalgraph.DefaultLimits().MaxReadConcurrency)
	a.emitGoalUpdates(send)
	if errors.Is(readErr, goalgraph.ErrGraphTerminal) {
		outcome, reason := a.goalGraph.Outcome()
		return goalGraphTerminalError(outcome, reason)
	}
	if readErr != nil {
		return readErr
	}
	if len(claims) > 0 {
		if err := a.runGoalReadFanout(ctx, claims, send); err != nil {
			return err
		}
		// A completed read wave may unlock another read wave. Re-enter the
		// deterministic selector before allowing the serial primary lane to run.
		return a.ensureGoalAttempt(ctx, send)
	}
	_, _, err := a.goalGraph.StartNext(ctx, token)
	a.emitGoalUpdates(send)
	if errors.Is(err, goalgraph.ErrGraphTerminal) {
		outcome, reason := a.goalGraph.Outcome()
		return goalGraphTerminalError(outcome, reason)
	}
	return err
}

func goalGraphTerminalError(outcome goalgraph.Outcome, reason string) error {
	switch outcome {
	case goalgraph.OutcomeDone:
		return fmt.Errorf("%w; start /new for unrelated work", ErrGoalGraphComplete)
	case goalgraph.OutcomeCancelled:
		if strings.TrimSpace(reason) == "" {
			reason = "goal graph cancelled"
		}
		return fmt.Errorf("%s: %w", reason, context.Canceled)
	case goalgraph.OutcomeBudgetExhausted:
		if strings.TrimSpace(reason) == "" {
			reason = "goal graph budget exhausted"
		}
		return fmt.Errorf("%w: %s", ErrIterationBudgetExceeded, reason)
	case goalgraph.OutcomeBlocked:
		if strings.TrimSpace(reason) == "" {
			reason = "goal graph blocked"
		}
		return fmt.Errorf("%w: %s", ErrGoalBlocked, reason)
	default:
		return nil
	}
}

func (a *Agent) beginGoalTool(ctx context.Context, callName string, action tools.Action, send Emit) (bool, error) {
	if !a.graphEnabled() || goalgraph.MetaTool(callName) {
		return false, nil
	}
	_, attempt, active := a.goalGraph.Active()
	if !active {
		return false, errors.New("goal graph has no active attempt for tool execution")
	}
	potentialMutation := action.Risk == tools.RiskWrite || action.Risk == tools.RiskExecute
	nonReplayable := action.Risk != tools.RiskRead
	token := ""
	if potentialMutation {
		var err error
		token, err = a.goalToken(ctx)
		if err != nil {
			// BeginTool records the typed failure and persists it before returning
			// ErrWorkspaceState; pass empty rather than bypassing the gate.
			token = ""
		}
	}
	err := a.goalGraph.BeginTool(ctx, attempt.ID, goalgraph.ToolAction{
		Tool: callName, Risk: string(action.Risk), Summary: action.Summary, Command: action.Command,
		PotentialMutation: potentialMutation, NonReplayable: nonReplayable,
	}, token)
	a.emitGoalUpdates(send)
	return true, err
}

func (a *Agent) finishGoalTool(ctx context.Context, observation toolObservation, send Emit) error {
	if !a.graphEnabled() || goalgraph.MetaTool(observation.Name) {
		a.emitGoalUpdates(send)
		return nil
	}
	_, attempt, active := a.goalGraph.Active()
	if !active {
		return errors.New("goal graph lost its active attempt during tool execution")
	}
	verification := observation.Name == "run_command" && isVerificationCommand(observation.Action.Command, a.workspace)
	token := ""
	if observation.Action.Risk == tools.RiskWrite || observation.Action.Risk == tools.RiskExecute || verification {
		// A cancelled command may still have changed bytes. Token inspection must
		// therefore outlive the cancelled action context for a short bounded read.
		stateCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		token, _ = a.goalToken(stateCtx)
		cancel()
	}
	err := a.goalGraph.FinishTool(ctx, attempt.ID, goalgraph.ToolResult{
		Tool: observation.Name, Risk: string(observation.Action.Risk), Summary: firstNonemptyGraphText(observation.ResultSummary, observation.Action.Summary),
		Command: observation.Action.Command, Failed: observation.Failed, FailureKind: observation.FailureKind,
		FailureDetail: observation.FailureDetail, Retryable: observation.Retryable, Verification: verification,
		WorkspaceToken: token, Started: observation.Started, Finished: observation.Finished,
	})
	a.emitGoalUpdates(send)
	return err
}

func firstNonemptyGraphText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (a *Agent) recordGoalFailure(ctx context.Context, observation toolObservation, send Emit) error {
	if !a.graphEnabled() || goalgraph.MetaTool(observation.Name) || observation.GraphRecorded {
		a.emitGoalUpdates(send)
		return nil
	}
	_, attempt, active := a.goalGraph.Active()
	if !active {
		return errors.New("goal graph has no active attempt for a tool failure")
	}
	kind := observation.FailureKind
	if kind == "" {
		kind = goalgraph.FailureTool
	}
	err := a.goalGraph.RecordFailure(ctx, attempt.ID, goalgraph.Failure{
		Kind: kind, Tool: observation.Name, Risk: string(observation.Action.Risk),
		Detail: observation.FailureDetail, Retryable: observation.Retryable,
	})
	a.emitGoalUpdates(send)
	return err
}

// recordProviderFailure accounts for the request boundary separately from
// tool failures. Provider resilience has already spent its transport retry
// policy before this runs; a still-retryable normalized failure may consume
// one fresh node attempt, while an unclassified or deterministic failure
// blocks instead of spinning or pretending the node completed.
func (a *Agent) recordProviderFailure(ctx context.Context, providerFailure error, send Emit) (goalgraph.Decision, error) {
	if !a.graphEnabled() {
		return goalgraph.Decision{}, errors.New("goal graph is unavailable")
	}
	_, attempt, active := a.goalGraph.Active()
	if !active {
		return goalgraph.Decision{}, errors.New("goal graph has no active attempt for a provider failure")
	}
	retryable := false
	if normalized, ok := provider.AsError(providerFailure); ok {
		retryable = normalized.Retryable
	}
	if err := a.goalGraph.RecordFailure(ctx, attempt.ID, goalgraph.Failure{
		Kind: goalgraph.FailureProvider, Tool: a.providerName,
		Detail: providerFailure.Error(), Retryable: retryable,
	}); err != nil {
		return goalgraph.Decision{}, err
	}
	a.emitGoalUpdates(send)
	return a.assessGoalCompletion(ctx, "provider request failed", send)
}

func (a *Agent) assessGoalCompletion(ctx context.Context, summary string, send Emit) (goalgraph.Decision, error) {
	if !a.graphEnabled() {
		return goalgraph.Decision{}, errors.New("goal graph is unavailable")
	}
	token, _ := a.goalToken(ctx)
	decision, err := a.goalGraph.ProposeCompletion(ctx, summary, token)
	a.emitGoalUpdates(send)
	return decision, err
}

func (a *Agent) cancelGoalGraph(reason string, send Emit) {
	if !a.graphEnabled() {
		return
	}
	_ = a.goalGraph.Cancel(context.Background(), reason)
	a.emitGoalUpdates(send)
}

func (a *Agent) exhaustGoalGraph(reason string, send Emit) {
	if !a.graphEnabled() {
		return
	}
	_ = a.goalGraph.ExhaustBudget(context.Background(), reason)
	a.emitGoalUpdates(send)
}
