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

const maxGoalCompletionRemediationIterations = 4

// refusedVerification is in-memory attempt-scoped diagnostic state. It informs
// a message and never a gate decision, so it is deliberately not persisted:
// an attempt that outlives the process no longer has a blocker to explain.
type refusedVerification struct {
	Count      int
	Suggestion string
}

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
	} else {
		a.goalSteering = nil
	}
	return nil
}

// SetGoalWriterVerifier connects graph-owned retained candidates to the
// application's ordinary detected-command verification path. It adds no new
// command authority: each command is still independently permission-gated.
func (a *Agent) SetGoalWriterVerifier(verify func(context.Context, string) ([]DelegateVerification, error)) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.goalWriterVerifier = verify
	a.mu.Unlock()
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
	err := graph.RecordPrimaryUsage(ctx, goalgraph.WorkUsage{
		Iterations: iterations, InputTokens: usage.InputTokens, CachedTokens: usage.CachedTokens,
		OutputTokens: usage.OutputTokens,
		CostUSD:      usage.CostUSD, CostAvailable: usage.CostAvailable, CostEstimated: usage.CostEstimated,
	})
	if errors.Is(err, goalgraph.ErrAggregateBudget) {
		outcome, reason := graph.Outcome()
		return goalGraphTerminalError(outcome, reason)
	}
	return err
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

// goalAttemptNoProgressBudget reports whether the active primary attempt has
// consumed its ordinary per-agent slice without recording novel durable
// evidence. Useful work renews the lease inside the same immutable attempt;
// the fixed graph aggregate remains the hard outer bound across all work.
func (a *Agent) goalAttemptNoProgressBudget() (goalgraph.Node, goalgraph.Attempt, bool) {
	if a == nil || a.maxIterations <= 0 {
		return goalgraph.Node{}, goalgraph.Attempt{}, false
	}
	a.mu.RLock()
	graph := a.goalGraph
	a.mu.RUnlock()
	if graph == nil {
		return goalgraph.Node{}, goalgraph.Attempt{}, false
	}
	node, attempt, active := graph.Active()
	withoutProgress := attempt.Iterations - attempt.LastProgressIteration
	return node, attempt, active && withoutProgress >= a.maxIterations
}

// goalCompletionGapBudget is narrower than the ordinary progress lease. Once
// the runtime has named an exact acceptance gap, only concrete repair or
// gate-changing evidence renews this bounded remediation window.
func (a *Agent) goalCompletionGapBudget() (goalgraph.Node, goalgraph.Attempt, bool) {
	if a == nil {
		return goalgraph.Node{}, goalgraph.Attempt{}, false
	}
	a.mu.RLock()
	graph := a.goalGraph
	a.mu.RUnlock()
	if graph == nil {
		return goalgraph.Node{}, goalgraph.Attempt{}, false
	}
	node, attempt, active := graph.Active()
	withoutGateProgress := attempt.Iterations - attempt.CompletionGapIteration
	return node, attempt, active && attempt.CompletionGap != "" && withoutGateProgress >= maxGoalCompletionRemediationIterations
}

// recordRefusedVerification remembers that the active attempt ran something
// the runtime read as a check but could not accept. A node that dies for want
// of verification after running verification is the most confusing way this
// mode can fail, so the blocker it produces should say what was refused rather
// than repeating that nothing was recorded.
func (a *Agent) recordRefusedVerification(suggestion string) {
	if a == nil || strings.TrimSpace(suggestion) == "" {
		return
	}
	a.mu.RLock()
	graph := a.goalGraph
	a.mu.RUnlock()
	if graph == nil {
		return
	}
	_, attempt, active := graph.Active()
	if !active {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.refusedVerification == nil {
		a.refusedVerification = make(map[string]refusedVerification)
	}
	record := a.refusedVerification[attempt.ID]
	record.Count++
	record.Suggestion = suggestion
	a.refusedVerification[attempt.ID] = record
}

func (a *Agent) refusedVerificationFor(attemptID string) refusedVerification {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.refusedVerification[attemptID]
}

func (a *Agent) blockStalledGoalCompletion(ctx context.Context, node goalgraph.Node, attempt goalgraph.Attempt, send Emit) error {
	reason := fmt.Sprintf("node %d completion gap made no repair or gate-changing progress for %d provider iterations: %s; repeated tool output, identical verification failures, or equivalent unrecognized verification variants are not progress", node.ID, maxGoalCompletionRemediationIterations, attempt.CompletionGap)
	// Name the refused check. Without this the operator reads that no
	// verification exists directly beneath a passing test suite they watched
	// run, with nothing to act on.
	if refused := a.refusedVerificationFor(attempt.ID); refused.Count > 0 {
		reason += fmt.Sprintf("; %d verification-like command(s) were refused during this attempt — run %q directly, without shell composition", refused.Count, refused.Suggestion)
	}
	if err := a.goalGraph.BlockActive(context.WithoutCancel(ctx), attempt.ID, reason); err != nil {
		return err
	}
	a.emitGoalUpdates(send)
	return fmt.Errorf("%w: %s", ErrGoalBlocked, reason)
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
	if err := a.goalGraph.EnforceAggregateBudget(ctx); err != nil {
		a.emitGoalUpdates(send)
		if errors.Is(err, goalgraph.ErrAggregateBudget) {
			outcome, reason := a.goalGraph.Outcome()
			return goalGraphTerminalError(outcome, reason)
		}
		return err
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
	if a.goalGraph.HasReadyWriters() {
		base, baseErr := a.stableGoalWriterBase(ctx)
		if baseErr != nil {
			base.Problem = baseErr.Error()
		}
		writerClaims, writerErr := a.goalGraph.StartReadyWriters(ctx, base, goalgraph.DefaultLimits().MaxWriterConcurrency)
		a.emitGoalUpdates(send)
		if errors.Is(writerErr, goalgraph.ErrGraphTerminal) {
			outcome, reason := a.goalGraph.Outcome()
			return goalGraphTerminalError(outcome, reason)
		}
		if writerErr != nil {
			return writerErr
		}
		if len(writerClaims) > 0 {
			if err := a.runGoalWriteFanout(ctx, writerClaims, send); err != nil {
				return err
			}
			return a.ensureGoalAttempt(ctx, send)
		}
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
		// Carry the reason. A graph can reach done the moment a revision
		// removes the last unfinished node, in which case this is the only
		// thing the user is told about the turn — and the reason is where the
		// runtime records that the approved plan was reduced to get here.
		if reason = strings.TrimSpace(reason); reason != "" {
			return fmt.Errorf("%w: %s; start /new for unrelated work", ErrGoalGraphComplete, reason)
		}
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
		return fmt.Errorf("%w: %s", ErrAggregateBudgetExceeded, reason)
	case goalgraph.OutcomeAwaitingReview:
		if strings.TrimSpace(reason) == "" {
			reason = "verified candidates are retained for review"
		}
		return fmt.Errorf("%w: %s", ErrGoalAwaitingReview, reason)
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
	verification := observation.Name == "run_command" && observation.Verification
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
	if !a.graphEnabled() || goalgraph.MetaTool(observation.Name) || observation.GraphRecorded || observation.IgnoreGraphFailure {
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

func (a *Agent) aggregateBudgetError(send Emit) error {
	if !a.graphEnabled() {
		return nil
	}
	err := a.goalGraph.EnforceAggregateBudget(context.Background())
	if err == nil {
		return nil
	}
	a.emitGoalUpdates(send)
	outcome, reason := a.goalGraph.Outcome()
	if terminalErr := goalGraphTerminalError(outcome, reason); terminalErr != nil {
		return terminalErr
	}
	return err
}

// goalDoneAnswer appends the account of every node a revision removed before it
// completed.
//
// The closing message on a completed graph is the model's own text, and a model
// that proposed dropping a node it could not finish is the last narrator to
// rely on for mentioning that it did. The runtime owns terminal state, so the
// runtime says what the plan lost — in the answer the user actually reads,
// rather than only in a status command they may never run.
func goalDoneAnswer(content string, graph *goalgraph.Graph) string {
	if graph == nil {
		return content
	}
	retired := graph.RetiredNodes()
	superseded := graph.SupersededVerifications()
	waived := graph.WaivedNodes()
	if len(retired) == 0 && len(superseded) == 0 && len(waived) == 0 {
		return content
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(content, "\n"))
	if len(retired) > 0 {
		fmt.Fprintf(&b, "\n\nThe approved plan was reduced before this finished: %d node(s) were removed by revision without completing, so nothing verified them.\n", len(retired))
		for _, node := range retired {
			fmt.Fprintf(&b, "  - node %d (%s), removed while %s: %s\n", node.ID, node.Title, node.State, node.Reason)
		}
	}
	if len(superseded) > 0 {
		fmt.Fprintf(&b, "\n\n%d earlier check(s) passed against a workspace that later work changed, and were not re-run:\n", len(superseded))
		for _, item := range superseded {
			if item.Command != "" {
				fmt.Fprintf(&b, "  - node %d (%s): %s\n", item.NodeID, item.Title, item.Command)
				continue
			}
			fmt.Fprintf(&b, "  - node %d (%s)\n", item.NodeID, item.Title)
		}
		b.WriteString("\nWhether they still pass is not established either way. Re-run them if it matters.")
	}
	if len(waived) > 0 {
		fmt.Fprintf(&b, "\n\n%d node(s) completed on your written judgement rather than machine-observed verification:\n", len(waived))
		for _, node := range waived {
			fmt.Fprintf(&b, "  - node %d (%s): %s\n", node.NodeID, node.Title, node.Reason)
		}
		b.WriteString("\nA waiver and a passing check are different claims, and this record keeps them apart.")
	}
	b.WriteString("\n\nReview whether that still meets your goal. /orchestrate status shows the same account.")
	return b.String()
}

// goalAwaitingReviewAnswer renders the successful candidate-wave stop as an
// answer rather than an error. The retained worktrees are the deliverable; the
// operator's next action is review, not recovery.
func goalAwaitingReviewAnswer(err error, graph *goalgraph.Graph) string {
	reason := strings.TrimSpace(strings.TrimPrefix(err.Error(), ErrGoalAwaitingReview.Error()+": "))
	var waiting []goalgraph.UnstartedNode
	if graph != nil {
		waiting = graph.UnstartedNodes()
	}
	// "Finished" is only true when the wave took the whole plan. One wave
	// cannot take nodes whose write scopes overlap, so a plan can stop here
	// with approved work that never ran — and the closing line below invites
	// releasing the graph, which would discard it.
	headline := "Orchestrated Goal finished with verified candidates retained for review."
	if len(waiting) > 0 {
		headline = fmt.Sprintf("Orchestrated Goal produced verified candidates for review, but %d approved node(s) have not run yet.", len(waiting))
	}
	answer := headline + " Nothing was integrated into this workspace and no candidate was selected — that decision is yours."
	if reason != "" {
		answer += "\n\n" + reason
	}
	answer += "\n\nInspect them with /agents and /orchestrate status <node-id>, then integrate explicitly."
	if len(waiting) > 0 {
		answer += "\n\nStill waiting to run:"
		for _, node := range waiting {
			answer += fmt.Sprintf("\n  - node %d (%s)", node.NodeID, node.Title)
		}
		// The ordering matters and is not obvious: these nodes are not blocked
		// by a failure, they are blocked on the review above. Integrating and
		// verifying is what lets them start.
		answer += "\n\nThese are not blocked — they are waiting on the review above. Integrating and verifying the candidates lets them run. /orchestrate cancel would release the graph and abandon them."
		return answer
	}
	return answer + " /orchestrate cancel releases the graph when you are done."
}
