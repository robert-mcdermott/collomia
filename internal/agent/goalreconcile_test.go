package agent

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// retainedCandidate runs one successful writer wave and returns the tree it
// left behind. Every reconciliation question is about a directory that really
// exists, so these tests start from one the runtime actually created.
func retainedCandidate(t *testing.T) (*writeWave, goalgraph.RetainedWorktree) {
	t.Helper()
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		if call == 1 {
			args, _ := json.Marshal(map[string]string{"path": "new.txt", "content": "candidate\n"})
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "w", Name: "write_file", Arguments: args}}}, nil
		}
		return provider.Response{Content: "wrote the scoped file"}, nil
	}}
	wave := newWriteWave(t, client, nil, writerNode(1, "add candidate file", "new.txt"))
	if _, err := wave.runtime.Run(t.Context(), "create the candidate", func(event.Event) {}); err != nil {
		t.Fatalf("verified candidate wave reported an error: %v", err)
	}
	trees := wave.graph.RetainedWorktrees()
	if len(trees) != 1 {
		t.Fatalf("retained worktrees=%+v, want exactly one", trees)
	}
	if trees[0].Disposition != "" {
		t.Fatalf("a tree nobody has observed already claims disposition %q", trees[0].Disposition)
	}
	return wave, trees[0]
}

func (w *writeWave) observe(t *testing.T, tree goalgraph.RetainedWorktree) goalgraph.WorktreeObservation {
	t.Helper()
	observations := w.runtime.ObserveRetainedWorktrees(t.Context(), []goalgraph.RetainedWorktree{tree})
	if len(observations) != 1 {
		t.Fatalf("observations=%+v, want exactly one", observations)
	}
	return observations[0]
}

// reconciled records an observation the way the command surface does, and
// returns the tree as the graph now describes it.
func (w *writeWave) reconciled(t *testing.T, tree goalgraph.RetainedWorktree) goalgraph.RetainedWorktree {
	t.Helper()
	if err := w.graph.RecordWorktreeDispositions(t.Context(), []goalgraph.WorktreeObservation{w.observe(t, tree)}); err != nil {
		t.Fatal(err)
	}
	for _, updated := range w.graph.RetainedWorktrees() {
		if updated.AttemptID == tree.AttemptID {
			return updated
		}
	}
	t.Fatalf("attempt %s lost its retained worktree while being reconciled", tree.AttemptID)
	return goalgraph.RetainedWorktree{}
}

// A retained tree that is still there, still registered, and still holds the
// child's work is the case the whole review flow depends on. The count matters
// as much as the verdict: it is what a person decides against.
func TestReconcileReportsWhatIsStillInARetainedWorktree(t *testing.T) {
	wave, tree := retainedCandidate(t)

	observation := wave.observe(t, tree)
	if observation.Disposition != goalgraph.DispositionPresent {
		t.Fatalf("disposition=%q detail=%q, want present", observation.Disposition, observation.Detail)
	}
	if !strings.Contains(observation.Detail, "1 changed file") {
		t.Fatalf("detail does not say what is in the tree: %q", observation.Detail)
	}
	if err := wave.graph.RecordWorktreeDispositions(t.Context(), []goalgraph.WorktreeObservation{observation}); err != nil {
		t.Fatal(err)
	}
	status, err := wave.graph.Inspect(0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "present") || strings.Contains(status, "unreconciled — run") {
		t.Fatalf("status does not report the observed disposition:\n%s", status)
	}
	if pending := wave.graph.UnreconciledWorktrees(); len(pending) != 0 {
		t.Fatalf("an observed tree is still counted unreconciled: %+v", pending)
	}
}

// Worktrees live under the system temp directory, so the ordinary fate of one
// left by an interrupted session is deletion by something that is not
// Collomia. Reporting the remembered path as though it were still there is the
// specific false claim this replaces.
func TestReconcileReportsAWorktreeTheSystemSweptAway(t *testing.T) {
	wave, tree := retainedCandidate(t)
	if err := os.RemoveAll(tree.Worktree); err != nil {
		t.Fatal(err)
	}

	observation := wave.observe(t, tree)
	if observation.Disposition != goalgraph.DispositionMissing {
		t.Fatalf("disposition=%q detail=%q, want missing", observation.Disposition, observation.Detail)
	}
	// The identity survives the directory. What was there, and which node
	// caused it, is the audit record and must not be erased by the answer.
	updated := wave.reconciled(t, tree)
	if updated.Worktree != tree.Worktree || updated.NodeID != tree.NodeID {
		t.Fatalf("reconciling a missing tree erased its identity: %+v", updated)
	}
	if updated.Disposition.OnDisk() {
		t.Fatal("a missing tree still reports as being on disk")
	}
}

// A directory Git does not recognize cannot be removed through Git, and
// removing it any other way means recursively deleting a path read out of a
// durable record. The runtime declines to do that on its own.
func TestReconcileReportsADirectoryGitNoLongerRegisters(t *testing.T) {
	wave, tree := retainedCandidate(t)
	if out, err := exec.Command("git", "-C", wave.workspace, "worktree", "remove", "--force", tree.Worktree).CombinedOutput(); err != nil {
		t.Fatalf("git worktree remove: %v: %s", err, out)
	}
	if err := os.MkdirAll(tree.Worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree.Worktree, "new.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	observation := wave.observe(t, tree)
	if observation.Disposition != goalgraph.DispositionOrphaned {
		t.Fatalf("disposition=%q detail=%q, want orphaned", observation.Disposition, observation.Detail)
	}
	orphan := wave.reconciled(t, tree)
	err := wave.runtime.DiscardRetainedWorktree(t.Context(), orphan)
	if err == nil || !strings.Contains(err.Error(), "remove it by hand") {
		t.Fatalf("discard of an unregistered directory error=%v, want a refusal", err)
	}
	if _, statErr := os.Stat(orphan.Worktree); statErr != nil {
		t.Fatalf("a refused discard removed the directory anyway: %v", statErr)
	}
}

// scratchWorktree registers a real worktree directly, for the states a
// successful wave cannot produce: the runtime removes a tree its child left
// unchanged, so an empty retained tree only ever arrives from outside.
func scratchWorktree(t *testing.T, workspace, name string) (path, branch string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), name)
	branch = "collomia/" + name
	if out, err := exec.Command("git", "-C", workspace, "worktree", "add", "-b", branch, path).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", workspace, "worktree", "remove", "--force", path).Run()
		_ = exec.Command("git", "-C", workspace, "branch", "-D", branch).Run()
	})
	return path, branch
}

func TestReconcileReportsAnIntactButEmptyTreeAsSafeToDiscard(t *testing.T) {
	wave, _ := retainedCandidate(t)
	path, branch := scratchWorktree(t, wave.workspace, "empty-candidate")

	observation := wave.observe(t, goalgraph.RetainedWorktree{AttemptID: "scratch", NodeID: 9, Worktree: path, Branch: branch})
	if observation.Disposition != goalgraph.DispositionEmpty {
		t.Fatalf("disposition=%q detail=%q, want empty", observation.Disposition, observation.Detail)
	}
}

// A candidate is only meaningful as a difference from the commit its claim
// recorded. When that commit is gone the tree is intact but the comparison is
// not available, and saying "present" would overstate what can be reviewed.
func TestReconcileReportsACandidateWhoseBaseCommitIsGone(t *testing.T) {
	wave, _ := retainedCandidate(t)
	path, branch := scratchWorktree(t, wave.workspace, "unmoored-candidate")
	if err := os.WriteFile(filepath.Join(path, "new.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	observation := wave.observe(t, goalgraph.RetainedWorktree{
		AttemptID: "scratch", NodeID: 9, Worktree: path, Branch: branch,
		BaseCommit: "0000000000000000000000000000000000000000",
	})
	if observation.Disposition != goalgraph.DispositionBaseUnreachable {
		t.Fatalf("disposition=%q detail=%q, want base_unreachable", observation.Disposition, observation.Detail)
	}
	if !strings.Contains(observation.Detail, "1 changed file") {
		t.Fatalf("detail hides what is in the tree: %q", observation.Detail)
	}
}

// Discarding is the one irreversible thing this surface can do, so it refuses
// to act on a tree whose contents nobody has been shown.
func TestDiscardRefusesAWorktreeNobodyHasLookedAt(t *testing.T) {
	wave, tree := retainedCandidate(t)

	err := wave.runtime.DiscardRetainedWorktree(t.Context(), tree)
	if err == nil || !strings.Contains(err.Error(), "reconcile") {
		t.Fatalf("discard of an unobserved tree error=%v, want a refusal naming reconcile", err)
	}
	if _, statErr := os.Stat(tree.Worktree); statErr != nil {
		t.Fatalf("a refused discard removed the tree anyway: %v", statErr)
	}
}

func TestDiscardRemovesAReconciledWorktreeAndItsBranch(t *testing.T) {
	wave, tree := retainedCandidate(t)
	reconciled := wave.reconciled(t, tree)

	if err := wave.runtime.DiscardRetainedWorktree(t.Context(), reconciled); err != nil {
		t.Fatalf("discard of a reconciled tree failed: %v", err)
	}
	if _, err := os.Stat(reconciled.Worktree); !os.IsNotExist(err) {
		t.Fatalf("discarded worktree is still on disk: %v", err)
	}
	if err := exec.Command("git", "-C", wave.workspace, "rev-parse", "--verify", "--quiet", reconciled.Branch).Run(); err == nil {
		t.Fatalf("discarded candidate left branch %s behind", reconciled.Branch)
	}
	if err := wave.graph.RecordWorktreeDispositions(t.Context(), []goalgraph.WorktreeObservation{{
		AttemptID: reconciled.AttemptID, Disposition: goalgraph.DispositionDiscarded, Detail: "removed on explicit user request",
	}}); err != nil {
		t.Fatal(err)
	}
	// The node and attempt keep the record of what was removed. Losing that
	// would make a discarded candidate indistinguishable from one that never
	// existed, which is the accountability OG-3B1 established.
	status, err := wave.graph.Inspect(0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "discarded") || !strings.Contains(status, reconciled.Worktree) {
		t.Fatalf("status forgot the discarded candidate:\n%s", status)
	}
}
