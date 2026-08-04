package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/diffmodel"
	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/permission"
)

// IntegrateOrchestratedCandidate publishes one node's whole verified candidate
// into the parent workspace and records what that means for the plan.
//
// Three things about it are deliberate. It is reachable only by a person: the
// model cannot call it, and no autonomy mode reaches it, because this is the
// point where unreviewed work becomes the user's own files. It publishes the
// candidate whole rather than by selected hunks, because the child's
// verification passed against its entire tree and a subset would put bytes in
// the parent that no verification ever covered. And it does not mark the node
// done — the child's pass says nothing about the parent it has just been
// merged into, so the node moves to `integrated` and the graph reports the
// combined result as unverified.
func (r *Runtime) IntegrateOrchestratedCandidate(ctx context.Context, nodeID int) (string, error) {
	if r == nil || r.Agent == nil {
		return "", errors.New("runtime is unavailable")
	}
	if nodeID <= 0 {
		return "", errors.New("orchestrated goal node id must be a positive integer")
	}
	r.orchestrationMu.Lock()
	defer r.orchestrationMu.Unlock()
	graph, err := r.reconcilableGoalGraphLocked(ctx)
	if err != nil {
		return "", err
	}
	candidate, attemptID, err := graph.PrepareCandidateIntegration(nodeID)
	if err != nil {
		return "", err
	}
	if r.Team == nil {
		return "", errors.New("delegated-agent state is unavailable")
	}
	if _, ok := r.Team.Get(candidate.WorkerID); !ok {
		return "", fmt.Errorf("node %d's candidate %s is not available in this session; its retained worktree can still be inspected at %s", nodeID, candidate.WorkerID, candidate.Worktree)
	}

	preview, err := r.PrepareDelegateIntegration(ctx, candidate.WorkerID)
	if err != nil {
		return "", fmt.Errorf("prepare node %d's candidate: %w", nodeID, err)
	}
	selections, identical, err := wholeCandidateSelections(preview)
	if err != nil {
		return "", err
	}
	if len(selections) == 0 && len(identical) == len(preview.Files) {
		return "", fmt.Errorf("node %d's candidate is already present in the parent workspace; nothing would change", nodeID)
	}

	_, mutations, action, err := r.prepareIntegrationMutations(ctx, candidate.WorkerID, selections, true)
	if err != nil {
		return "", err
	}
	action.Summary = fmt.Sprintf("integrate Orchestrated Goal node %d's verified candidate (%d file(s)) into the workspace", nodeID, len(mutations))
	if _, err := r.Permissions.Authorize(ctx, "integrate_delegate", action); err != nil {
		status := "blocked"
		if errors.Is(err, permission.ErrDenied) {
			status = "rejected"
		}
		r.markDelegateIntegration(candidate.WorkerID, status, err)
		return "", err
	}

	published, checkpoint, err := r.publishDelegateIntegrationCheckpointed(ctx, candidate.WorkerID, mutations, true)
	if err != nil {
		// The publication either fully rolled back or left a durable
		// checkpoint. Either way the graph is untouched, so the node stays
		// awaiting review rather than claiming a state the workspace is not in.
		return "", err
	}
	token, tokenErr := r.goalStateTokenValue(ctx)
	if tokenErr != nil {
		return "", fmt.Errorf("observe the combined workspace after integrating node %d: %w (the bytes are published; checkpoint %s can restore the state from before)", nodeID, tokenErr, checkpoint)
	}
	if err := graph.IntegrateCandidate(ctx, nodeID, goalgraph.CandidateIntegration{
		AttemptID: attemptID, Files: published, AlreadyIdentical: identical,
		ParentWorkspaceToken: token, CheckpointID: checkpoint,
	}); err != nil {
		return "", fmt.Errorf("record node %d's integration: %w (the bytes are published; checkpoint %s can restore the state from before)", nodeID, err, checkpoint)
	}
	r.logGoalGraphUpdates()
	return graph.Inspect(nodeID)
}

// wholeCandidateSelections keeps every hunk of every file. It refuses rather
// than publishing part of a candidate: a conflict means the parent moved under
// the candidate, and applying the rest would produce a combined workspace that
// is neither what the child verified nor what the user had.
func wholeCandidateSelections(preview *DelegateIntegration) ([]DelegateIntegrationSelection, []string, error) {
	var selections []DelegateIntegrationSelection
	var identical []string
	for _, file := range preview.Files {
		if file.Conflict != "" {
			return nil, nil, fmt.Errorf("cannot integrate %s: %s; the parent changed under this candidate, so publishing part of it would produce a workspace neither verified nor intended", file.Path, file.Conflict)
		}
		if file.AlreadyApplied || file.Unified == "" {
			identical = append(identical, file.Path)
			continue
		}
		hunks, err := diffmodel.ParseHunks(file.Unified)
		if err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", file.Path, err)
		}
		keep := make([]bool, len(hunks))
		for i := range keep {
			keep[i] = true
		}
		selections = append(selections, DelegateIntegrationSelection{Path: file.Path, Keep: keep})
	}
	sort.Strings(identical)
	return selections, identical, nil
}

func (r *Runtime) goalStateTokenValue(ctx context.Context) (string, error) {
	if r.goalStateToken == nil {
		return "", errors.New("workspace state observation is unavailable")
	}
	token, err := r.goalStateToken(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(token) == "" {
		return "", errors.New("workspace state could not be observed")
	}
	return token, nil
}
