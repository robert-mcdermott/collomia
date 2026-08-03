package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// Reconciliation answers one question the graph cannot answer about itself:
// are the directories it still points at actually there, and do they still
// hold work? Isolated writer worktrees live under the system temp directory,
// so between one session and the next the operating system may have swept
// them, a person may have deleted one by hand, or they may be sitting there
// full of unreviewed changes. Naming a path is not the same as knowing which
// of those is true, and every later decision — discard it, inspect it, keep
// it for integration — depends on knowing.
//
// Observation is read-only and belongs here rather than in the graph: the
// graph owns the durable record of the answer, the application owns the
// filesystem and Git access that produces it.

// ObserveRetainedWorktrees inspects each retained tree and reports what it
// found. It never writes to a tree, never reuses one, and never removes one.
func (a *Agent) ObserveRetainedWorktrees(ctx context.Context, trees []goalgraph.RetainedWorktree) []goalgraph.WorktreeObservation {
	if a == nil || len(trees) == 0 {
		return nil
	}
	registered := registeredWorktrees(ctx, a.workspace)
	observations := make([]goalgraph.WorktreeObservation, 0, len(trees))
	for _, tree := range trees {
		disposition, detail := observeWorktree(ctx, a.workspace, tree, registered)
		observations = append(observations, goalgraph.WorktreeObservation{
			AttemptID: tree.AttemptID, Disposition: disposition, Detail: detail,
		})
	}
	return observations
}

func observeWorktree(ctx context.Context, workspace string, tree goalgraph.RetainedWorktree, registered map[string]bool) (goalgraph.WorktreeDisposition, string) {
	info, err := os.Stat(tree.Worktree)
	if err != nil || !info.IsDir() {
		return goalgraph.DispositionMissing, "the directory is gone; temporary directories are swept by the operating system, so nothing remains to reconcile"
	}
	if !registered[canonicalWorktreePath(tree.Worktree)] {
		return goalgraph.DispositionOrphaned, "the directory exists but Git no longer registers it as a worktree of this repository; inspect and remove it by hand"
	}
	status, statusErr := exec.CommandContext(ctx, "git", "-C", tree.Worktree, "status", "--porcelain=v1", "--untracked-files=normal").Output()
	if statusErr != nil {
		return goalgraph.DispositionOrphaned, "the directory exists but Git cannot read it as a working tree; inspect and remove it by hand"
	}
	changed := 0
	for _, line := range strings.Split(string(status), "\n") {
		if strings.TrimSpace(line) != "" {
			changed++
		}
	}
	if tree.BaseCommit != "" {
		if err := exec.CommandContext(ctx, "git", "-C", workspace, "rev-parse", "--verify", "--quiet", tree.BaseCommit+"^{commit}").Run(); err != nil {
			// The tree is intact, but the commit its diff is defined against is
			// not in the parent any more. Say so instead of reporting a changed
			// file count whose baseline no longer exists.
			return goalgraph.DispositionBaseUnreachable, fmt.Sprintf("%d changed file(s), but base commit %s is no longer in the parent repository, so the candidate cannot be diffed against its claim", changed, shortCommit(tree.BaseCommit))
		}
	}
	if changed == 0 {
		return goalgraph.DispositionEmpty, "the tree is registered and intact but holds no changes; discarding it loses nothing"
	}
	return goalgraph.DispositionPresent, fmt.Sprintf("%d changed file(s) still in the tree, on branch %s", changed, tree.Branch)
}

// registeredWorktrees reports the working trees Git itself associates with
// this repository. A directory that exists without being in this set is not a
// worktree any more, whatever it looks like, and must not be removed through
// Git as though it were.
func registeredWorktrees(ctx context.Context, workspace string) map[string]bool {
	registered := make(map[string]bool)
	out, err := exec.CommandContext(ctx, "git", "-C", workspace, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return registered
	}
	for _, line := range strings.Split(string(out), "\n") {
		path, found := strings.CutPrefix(strings.TrimSpace(line), "worktree ")
		if !found {
			continue
		}
		registered[canonicalWorktreePath(path)] = true
	}
	return registered
}

// canonicalWorktreePath resolves the symlinks that make two spellings of the
// same directory compare unequal. The system temp directory is itself a
// symlink on macOS, so a raw string comparison between what Git recorded and
// what the graph recorded would report every tree orphaned.
func canonicalWorktreePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// DiscardRetainedWorktree removes one retained tree because a person asked
// for it by name. It is deliberately not reachable by the model, by a hook,
// or by an autonomy mode: this deletes work that was never reviewed, and the
// only authority that can decide it is worthless is the user's.
//
// The caller must have reconciled the tree first, so the decision is made
// against what is actually in it rather than against a path.
func (a *Agent) DiscardRetainedWorktree(ctx context.Context, tree goalgraph.RetainedWorktree) error {
	if a == nil {
		return errors.New("agent is unavailable")
	}
	if tree.Worktree == "" {
		return errors.New("this attempt has no retained worktree")
	}
	action := tools.Action{
		Risk:    tools.RiskWrite,
		Summary: fmt.Sprintf("discard retained isolated-writer worktree for node %d", tree.NodeID),
		Paths:   []string{tree.Worktree},
	}
	err := a.discardWorktree(ctx, tree)
	// The removal is recorded in the audit ledger either way. A candidate that
	// existed and no longer does is exactly the kind of event `collo audit`
	// has to be able to account for.
	a.permissions.RecordOutcome("orchestrate_discard", action, err)
	return err
}

func (a *Agent) discardWorktree(ctx context.Context, tree goalgraph.RetainedWorktree) error {
	switch tree.Disposition {
	case "":
		return errors.New("this worktree has not been reconciled; run /orchestrate reconcile first so the decision is made against its contents")
	case goalgraph.DispositionDiscarded:
		return errors.New("this worktree was already discarded")
	case goalgraph.DispositionOrphaned:
		// Git does not own this directory any more, so removing it would be a
		// plain recursive delete of a path read out of a durable record. That
		// is the one case a person should carry out themselves.
		return fmt.Errorf("Git no longer registers %s as a worktree of this repository; inspect and remove it by hand", tree.Worktree)
	case goalgraph.DispositionMissing:
		// Nothing to remove, but the administrative record and the branch ref
		// in the parent repository outlive the directory. Clearing them is the
		// whole of the cleanup that is left.
		_ = exec.CommandContext(ctx, "git", "-C", a.workspace, "worktree", "prune").Run()
		_ = exec.CommandContext(ctx, "git", "-C", a.workspace, "branch", "-D", tree.Branch).Run()
		return nil
	}
	if out, err := exec.CommandContext(ctx, "git", "-C", a.workspace, "worktree", "remove", "--force", tree.Worktree).CombinedOutput(); err != nil {
		return fmt.Errorf("remove retained worktree %s: %w: %s", tree.Worktree, err, strings.TrimSpace(string(out)))
	}
	// The branch is the last reference to the candidate. Its removal failing is
	// not worth failing the discard over: the tree, which is what the user asked
	// to be rid of, is already gone.
	_ = exec.CommandContext(ctx, "git", "-C", a.workspace, "branch", "-D", tree.Branch).Run()
	return nil
}
