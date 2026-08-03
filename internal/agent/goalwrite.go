package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/hooks"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

func (a *Agent) stableGoalWriterBase(ctx context.Context) (goalgraph.WriterBase, error) {
	before, err := a.goalToken(ctx)
	if err != nil {
		return goalgraph.WriterBase{}, err
	}
	status, err := exec.CommandContext(ctx, "git", "-C", a.workspace, "status", "--porcelain=v1", "--untracked-files=normal").Output()
	if err != nil {
		return goalgraph.WriterBase{}, fmt.Errorf("inspect isolated-writer parent workspace: %w", err)
	}
	commit, err := exec.CommandContext(ctx, "git", "-C", a.workspace, "rev-parse", "HEAD").Output()
	if err != nil {
		return goalgraph.WriterBase{}, fmt.Errorf("resolve isolated-writer base commit: %w", err)
	}
	after, err := a.goalToken(ctx)
	if err != nil {
		return goalgraph.WriterBase{}, err
	}
	dirtyPaths, dirtyCount := goalWriterDirtyPaths(string(status))
	return goalgraph.WriterBase{
		WorkspaceToken: after,
		Commit:         strings.TrimSpace(string(commit)),
		Clean:          dirtyCount == 0 && before == after,
		DirtyPaths:     dirtyPaths,
		DirtyCount:     dirtyCount,
	}, nil
}

// GoalWriterBase exposes the same read-only stable-base observation used by
// scheduling so approval can reject an impossible candidate proposal before
// it is converted into durable execution state.
func (a *Agent) GoalWriterBase(ctx context.Context) (goalgraph.WriterBase, error) {
	if a == nil {
		return goalgraph.WriterBase{}, errors.New("agent is unavailable")
	}
	return a.stableGoalWriterBase(ctx)
}

func goalWriterDirtyPaths(status string) ([]string, int) {
	const maxReported = 8
	var paths []string
	count := 0
	for _, line := range strings.Split(status, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		count++
		path := strings.TrimSpace(line)
		if len(line) > 3 {
			path = strings.TrimSpace(line[3:])
		}
		if len(paths) < maxReported {
			paths = append(paths, path)
		}
	}
	return paths, count
}

// runGoalWriteFanout executes one graph-owned candidate wave. Dispatch gets
// the same delegate permission and lifecycle hooks as a model-authored call;
// child verification uses the application's ordinary run_command decisions.
func (a *Agent) runGoalWriteFanout(ctx context.Context, claims []goalgraph.WriterClaim, send Emit) error {
	if len(claims) == 0 {
		return nil
	}
	a.mu.RLock()
	graph := a.goalGraph
	cfg, approver, team, scheduler := a.delegateConfig, a.delegateApprover, a.delegateTeam, a.delegateScheduler
	verify := a.goalWriterVerifier
	a.mu.RUnlock()
	if graph == nil {
		return errors.New("goal graph is unavailable for automatic isolated writers")
	}
	if scheduler == nil || team == nil {
		return errors.New("delegated-agent scheduler is unavailable for automatic isolated writers")
	}

	action, args := writerWaveAction(claims)
	failureKind, authErr := a.authorizeGoalWriterWave(ctx, action, args, send)
	if authErr != nil {
		for _, claim := range claims {
			_ = graph.FinishWriter(context.WithoutCancel(ctx), goalgraph.WriterResult{
				AttemptID: claim.Attempt.ID, Status: "error", FailureKind: failureKind,
				Error: authErr.Error(), WritePaths: claim.WritePaths,
				BaseCommit: claim.Attempt.BaseCommit, ParentWorkspaceToken: claim.Attempt.BaseWorkspaceToken,
			})
		}
		a.emitGoalUpdates(send)
		outcome, reason := graph.Outcome()
		return goalGraphTerminalError(outcome, reason)
	}
	freshBase, baseErr := a.stableGoalWriterBase(ctx)
	if baseErr == nil {
		baseErr = goalgraph.ValidateWriterBase(freshBase)
	}
	if baseErr == nil && (freshBase.WorkspaceToken != claims[0].Attempt.BaseWorkspaceToken || freshBase.Commit != claims[0].Attempt.BaseCommit) {
		baseErr = errors.New("isolated-writer parent workspace changed after the durable claim and before dispatch")
	}
	if baseErr != nil {
		for _, claim := range claims {
			_ = graph.FinishWriter(context.WithoutCancel(ctx), goalgraph.WriterResult{
				AttemptID: claim.Attempt.ID, Status: "error", FailureKind: goalgraph.FailureWorkspaceStale,
				Error: baseErr.Error(), WritePaths: claim.WritePaths,
				BaseCommit: claim.Attempt.BaseCommit, ParentWorkspaceToken: freshBase.WorkspaceToken,
			})
		}
		a.finishGoalWriterWave(ctx, action, baseErr)
		a.emitGoalUpdates(send)
		outcome, reason := graph.Outcome()
		return goalGraphTerminalError(outcome, reason)
	}

	results := make([]DelegateResult, len(claims))
	var wg sync.WaitGroup
	for index, claim := range claims {
		wg.Add(1)
		go func(index int, claim goalgraph.WriterClaim) {
			defer wg.Done()
			task := DelegateTask{
				Name: fmt.Sprintf("goal-write-%d", claim.Node.ID), Task: automaticWritePrompt(claim),
				Write: true, WritePaths: append([]string(nil), claim.WritePaths...), PlanStep: claim.Node.ID,
				RuntimeID: "goal-" + claim.Attempt.ID, GraphNode: true,
				TokenBudgetOverride: claim.TokenBudget, CostBudgetOverride: claim.CostBudgetUSD,
				TimeoutOverride: claim.TimeoutSeconds, MaxIterationsOverride: claim.MaxIterations,
				BaseCommitOverride: claim.Attempt.BaseCommit,
				OnWorktree: func(worktree, branch string) error {
					return graph.RecordWriterWorktree(context.WithoutCancel(ctx), claim.Attempt.ID, worktree, branch)
				},
			}
			results[index] = a.runScheduledDelegate(ctx, index, task, cfg, approver, team, scheduler, nil)
		}(index, claim)
	}
	wg.Wait()

	budgetExhausted := false
	for index, result := range results {
		recordErr := graph.RecordWriterUsage(context.WithoutCancel(ctx), writerResultFromDelegate(claims[index], result, "", DelegateStatus{}))
		if errors.Is(recordErr, goalgraph.ErrAggregateBudget) {
			budgetExhausted = true
			continue
		}
		if recordErr != nil {
			a.retainGoalWriterWorktrees(ctx, graph, claims, results)
			a.finishGoalWriterWave(ctx, action, recordErr)
			a.emitGoalUpdates(send)
			return recordErr
		}
	}
	if budgetExhausted {
		// The wave is over budget, but its worktrees are on disk. Record where
		// each one is before returning the terminal outcome; no further child
		// verification is run, because that would be new work after the ceiling.
		a.retainGoalWriterWorktrees(ctx, graph, claims, results)
		a.finishGoalWriterWave(ctx, action, goalgraph.ErrAggregateBudget)
		a.emitGoalUpdates(send)
		outcome, reason := graph.Outcome()
		return goalGraphTerminalError(outcome, reason)
	}
	if err := ctx.Err(); err != nil {
		// Cancel first so the outcome names the real reason, then record the
		// worktrees the wave already left on disk. A cancelled writer that changed
		// files keeps its worktree, and a graph that cannot name it cannot honour
		// the promise to retain it for inspection.
		_ = graph.Cancel(context.WithoutCancel(ctx), err.Error())
		a.retainGoalWriterWorktrees(ctx, graph, claims, results)
		a.finishGoalWriterWave(ctx, action, err)
		a.emitGoalUpdates(send)
		return err
	}

	verificationErrors := make([]error, len(results))
	for index, result := range results {
		if result.Status != DelegateDone || len(result.ChangedFiles) == 0 || len(result.ScopeViolations) > 0 {
			continue
		}
		if verify == nil {
			verificationErrors[index] = errors.New("automatic isolated-writer verification is unavailable")
			continue
		}
		_, verificationErrors[index] = verify(ctx, result.ID)
	}
	parentToken, _ := a.goalToken(ctx)
	for index, result := range results {
		status, _ := team.Get(result.ID)
		writerResult := writerResultFromDelegate(claims[index], result, parentToken, status)
		if verificationErrors[index] != nil && writerResult.Error == "" {
			writerResult.Error = verificationErrors[index].Error()
		}
		err := graph.FinishWriter(context.WithoutCancel(ctx), writerResult)
		a.emitGoalUpdates(send)
		if err != nil && !errors.Is(err, goalgraph.ErrGraphTerminal) {
			a.finishGoalWriterWave(ctx, action, err)
			return err
		}
	}
	a.finishGoalWriterWave(ctx, action, nil)
	if outcome, reason := graph.Outcome(); outcome != "" {
		return goalGraphTerminalError(outcome, reason)
	}
	return nil
}

// retainGoalWriterWorktrees records where each writer's retained worktree is
// when the wave ends before ordinary child verification could run. It performs
// no verification and unlocks nothing: the graph is already terminal, and this
// only preserves the identity an operator needs to inspect or remove the tree.
func (a *Agent) retainGoalWriterWorktrees(ctx context.Context, graph *goalgraph.Graph, claims []goalgraph.WriterClaim, results []DelegateResult) {
	parentToken, _ := a.goalToken(context.WithoutCancel(ctx))
	for index, result := range results {
		_ = graph.FinishWriter(context.WithoutCancel(ctx), writerResultFromDelegate(claims[index], result, parentToken, DelegateStatus{}))
	}
}

func (a *Agent) finishGoalWriterWave(ctx context.Context, action tools.Action, runErr error) {
	a.permissions.RecordOutcome("delegate", action, runErr)
	payload := hooks.Payload{Event: "tool_end", Workspace: a.workspace, Subject: "delegate", Tool: "delegate", Summary: action.Summary}
	if runErr != nil {
		payload.Error = runErr.Error()
	}
	a.lifecycle.Fire(ctx, payload)
}

func (a *Agent) authorizeGoalWriterWave(ctx context.Context, action tools.Action, args json.RawMessage, send Emit) (goalgraph.FailureKind, error) {
	grant, err := a.permissions.Authorize(ctx, "delegate", action)
	decided := event.New(event.KindPermissionDecision)
	decided.Permission = &event.Permission{Tool: "delegate", Summary: action.Summary, Risk: string(action.Risk), Source: grant.Source, Rule: grant.Rule, Allowed: err == nil}
	send(decided)
	if persistenceErr := a.checkPersistence(); persistenceErr != nil {
		return goalgraph.FailurePersistence, persistenceErr
	}
	allowed := err == nil
	a.lifecycle.Fire(ctx, hooks.Payload{Event: "permission_decision", Workspace: a.workspace, Subject: "delegate", Tool: "delegate", Summary: action.Summary, Allowed: &allowed, Detail: map[string]any{"risk": string(action.Risk), "source": grant.Source, "rule": grant.Rule}})
	if err != nil {
		return goalgraph.FailurePermission, err
	}
	if hookErr := a.lifecycle.Gate(ctx, hooks.Payload{Event: "tool_start", Workspace: a.workspace, Subject: "delegate", Tool: "delegate", Summary: action.Summary, Args: args, Paths: action.Paths}); hookErr != nil {
		return goalgraph.FailureHook, hookErr
	}
	return "", nil
}

func writerWaveAction(claims []goalgraph.WriterClaim) (tools.Action, json.RawMessage) {
	paths := make([]string, 0)
	nodes := make([]map[string]any, 0, len(claims))
	for _, claim := range claims {
		paths = append(paths, claim.WritePaths...)
		nodes = append(nodes, map[string]any{"node_id": claim.Node.ID, "write_paths": claim.WritePaths})
	}
	args, _ := json.Marshal(map[string]any{"runtime_owned": true, "nodes": nodes})
	return tools.Action{Risk: tools.RiskWrite, Summary: fmt.Sprintf("create %d isolated writer candidate(s) for reviewed integration", len(claims)), Paths: paths}, args
}

func writerResultFromDelegate(claim goalgraph.WriterClaim, result DelegateResult, parentToken string, status DelegateStatus) goalgraph.WriterResult {
	verification := make([]goalgraph.CandidateVerification, 0, len(status.VerificationResults))
	for _, observed := range status.VerificationResults {
		verification = append(verification, goalgraph.CandidateVerification{Command: observed.Command, Status: observed.Status, StateToken: observed.StateToken})
	}
	return goalgraph.WriterResult{
		AttemptID: claim.Attempt.ID, WorkerID: result.ID, Status: result.Status,
		Summary: result.Summary, Error: result.Error, Evidence: result.Evidence,
		WritePaths: result.WriteScopes, ChangedFiles: result.ChangedFiles, ScopeViolations: result.ScopeViolations,
		Worktree: result.Worktree, Branch: result.Branch, BaseCommit: result.BaseCommit,
		ParentWorkspaceToken: parentToken, VerificationState: status.VerificationStatus,
		VerificationToken: status.VerificationToken, Verification: verification,
		Iterations: result.Iterations, InputTokens: result.InputTokens, CachedTokens: result.CachedTokens, OutputTokens: result.OutputTokens,
		CostUSD: result.CostUSD, CostAvailable: result.CostAvailable, CostEstimated: result.CostEstimated,
	}
}

func automaticWritePrompt(claim goalgraph.WriterClaim) string {
	criteria := ""
	for _, criterion := range claim.Node.Acceptance {
		criteria += "\n- " + criterion
	}
	return fmt.Sprintf(`Implement one approved Orchestrated Goal node in an isolated Git worktree.

Node %d: %s
Acceptance criteria:%s
Declared write scope:
- %s

Make only the changes needed for this node and stay within the declared write scope. Inspect the repository, implement the work, and return a concise summary grounded in concrete changed files and tool results. Do not delegate, update the shared plan, commit, merge, push, publish, or claim that the whole goal is complete. The runtime will independently inspect and verify the retained candidate before presenting it for review.`, claim.Node.ID, claim.Node.Title, criteria, strings.Join(claim.WritePaths, "\n- "))
}
