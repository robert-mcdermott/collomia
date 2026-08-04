package eval

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robert-mcdermott/collomia/internal/app"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/diffmodel"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// These are the product evaluations for OG-3's isolated-writer wave. Unlike the
// focused agent tests, they drive a complete runtime: real delegate permission,
// real Git worktrees, and the application's own child verification running the
// repository's actual test command inside each candidate tree.
//
// The property every one of them asserts is the same, because it is OG-3's exit
// gate: whatever the wave does, the parent workspace is exactly as it was.

// orchestratedWriterGitFixture is a repository whose tests pass at the base
// commit and whose packages are genuinely disjoint. Both matter: each writer
// branches from that commit and must be able to verify its own tree, and
// disjoint scopes are what allow two writers to run at once.
func orchestratedWriterGitFixture(t *testing.T) string {
	t.Helper()
	workspace := t.TempDir()
	runEvalGit(t, workspace, "init", "-b", "main")
	runEvalGit(t, workspace, "config", "core.autocrlf", "false")
	mustWriteEvaluationFile(t, filepath.Join(workspace, ".gitignore"), ".collomia-eval-cache/\n")
	mustWriteEvaluationFile(t, filepath.Join(workspace, "go.mod"), "module orchestratedwriterfixture\n\ngo 1.26.0\n")
	for _, pkg := range []string{"alpha", "beta"} {
		if err := os.MkdirAll(filepath.Join(workspace, pkg), 0o755); err != nil {
			t.Fatal(err)
		}
		mustWriteEvaluationFile(t, filepath.Join(workspace, pkg, pkg+".go"),
			"package "+pkg+"\n\nfunc Name() string { return \""+pkg+"\" }\n")
		mustWriteEvaluationFile(t, filepath.Join(workspace, pkg, pkg+"_test.go"),
			"package "+pkg+"\n\nimport \"testing\"\n\nfunc TestName(t *testing.T) { if Name() != \""+pkg+"\" { t.Fatal(\"bad name\") } }\n")
	}
	runEvalGit(t, workspace, "add", ".")
	runEvalGit(t, workspace, "commit", "-m", "base")
	return workspace
}

// newWriterEvaluationRuntime builds the full application runtime a real
// candidate wave runs in, including delegation, worktrees, and the
// application's own child verification.
func newWriterEvaluationRuntime(t *testing.T, workspace string, client provider.Client, steps []plan.Step) *app.Runtime {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configDir := filepath.Join(home, ".collomia")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// The child runs the repository's real test command, so the evaluation
	// needs the same sandbox and environment settings the primary-graph
	// evaluation uses for its verification step.
	config := map[string]any{
		"default_provider": "fixture",
		"providers": map[string]any{
			"fixture": map[string]any{"type": "openai-compatible", "base_url": "http://127.0.0.1:1/v1", "model": "scripted", "context_window": 16000, "max_tokens": 256},
		},
		"permissions": map[string]any{
			"sandbox":                evaluationSandboxMode(),
			"command_env":            "minimal",
			"sandbox_readable_roots": evaluationSandboxReadableRoots(),
		},
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteEvaluationFile(t, filepath.Join(configDir, "config.json"), string(encoded))
	// A shared build cache outside every worktree: each candidate tree is a
	// separate directory, and pointing the cache inside one of them would make
	// the first verification's cost the second's problem.
	t.Setenv("GOCACHE", filepath.Join(home, "go-build-cache"))

	approved := &plan.Plan{Goal: "produce reviewable candidates", Steps: steps}
	runtime, err := app.New(t.Context(), app.Options{Workspace: workspace, Autonomy: "autopilot", OrchestratedGoal: approved})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	runtime.Agent.SetProvider("offline-evaluation", "scripted", appconfig.Provider{Type: "fixture", MaxTokens: 256, Context: 16_000}, client)
	t.Cleanup(func() { removeEvaluationWorktrees(t, runtime, workspace) })
	return runtime
}

// removeEvaluationWorktrees cleans up the directories a wave deliberately
// retains. The runtime never removes them on its own, which is the behaviour
// under test, so the evaluation is responsible for its own leftovers.
func removeEvaluationWorktrees(t *testing.T, runtime *app.Runtime, workspace string) {
	t.Helper()
	if runtime.GoalGraph == nil {
		return
	}
	for _, tree := range runtime.GoalGraph.RetainedWorktrees() {
		_ = exec.Command("git", "-C", workspace, "worktree", "remove", "--force", tree.Worktree).Run()
		_ = exec.Command("git", "-C", workspace, "branch", "-D", tree.Branch).Run()
	}
}

// writerFixtureState is the complete observable state of the parent
// repository: tracked content plus anything untracked. Comparing it before and
// after a wave is how these evaluations prove the parent was not touched,
// rather than checking for the specific files a writer happened to create.
func writerFixtureState(t *testing.T, workspace string) string {
	t.Helper()
	head, err := exec.Command("git", "-C", workspace, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	status, err := exec.Command("git", "-C", workspace, "status", "--porcelain=v1", "--untracked-files=all").Output()
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	err = filepath.WalkDir(workspace, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, relErr := filepath.Rel(workspace, path)
		if relErr != nil {
			return relErr
		}
		if entry.IsDir() {
			if relative == ".git" || relative == ".collomia-eval-cache" {
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, relative)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return strings.TrimSpace(string(head)) + "\n" + string(status) + "\n" + strings.Join(files, "\n")
}

func writerStep(id int, title, scope string) plan.Step {
	return plan.Step{
		ID: id, Title: title, Status: "pending", Execution: "isolated_write",
		WritePaths: []string{scope}, Acceptance: []string{"the package keeps building and passing its tests"},
	}
}

// scopedWriterClient answers each writer child by writing one file inside its
// own declared scope. The parent lane is never expected to run: a candidate-only
// graph is terminal as soon as the wave finishes.
type scopedWriterClient struct {
	mu      sync.Mutex
	t       *testing.T
	files   map[string]string // node title fragment -> path to write
	failFor string            // node title fragment whose child fails outright
	written map[string]bool
}

func (c *scopedWriterClient) Name() string { return "writer-wave-evaluation" }

func (c *scopedWriterClient) Chat(ctx context.Context, request provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	if !requestHasText(request, "Implement one approved Orchestrated Goal node") {
		c.t.Errorf("the primary lane ran during a candidate-only writer wave")
		return provider.Response{Content: "unexpected primary turn"}, nil
	}
	for fragment, path := range c.files {
		if !requestHasText(request, fragment) {
			continue
		}
		if c.failFor != "" && fragment == c.failFor {
			return provider.Response{}, context.DeadlineExceeded
		}
		c.mu.Lock()
		already := c.written[fragment]
		c.written[fragment] = true
		c.mu.Unlock()
		if already {
			return provider.Response{Content: "wrote " + path + " inside the declared scope"}, nil
		}
		content := "package " + filepath.Base(filepath.Dir(path)) + "\n\nfunc Extra() string { return \"extra\" }\n"
		args, _ := json.Marshal(map[string]string{"path": path, "content": content})
		return provider.Response{ToolCalls: []provider.ToolCall{{ID: "write-" + fragment, Name: "write_file", Arguments: args}}}, nil
	}
	c.t.Errorf("writer child prompt matched no known node: %+v", request.Messages)
	return provider.Response{Content: "unmatched"}, nil
}

// The headline case: two disjoint writers both produce verified candidates,
// the graph stops for review rather than reporting success, and the parent
// workspace is byte-for-byte what it was before the wave started.
func TestOrchestratedGoalVerifiedWriterWaveEvaluation(t *testing.T) {
	workspace := orchestratedWriterGitFixture(t)
	before := writerFixtureState(t, workspace)
	client := &scopedWriterClient{t: t, written: map[string]bool{}, files: map[string]string{
		"extend alpha": "alpha/extra.go",
		"extend beta":  "beta/extra.go",
	}}
	runtime := newWriterEvaluationRuntime(t, workspace, client, []plan.Step{
		writerStep(1, "extend alpha", "alpha/"),
		writerStep(2, "extend beta", "beta/"),
	})

	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	var events []event.Event
	answer, err := runtime.Agent.Run(ctx, "Produce both approved candidates.", func(e event.Event) { events = append(events, e) })
	if err != nil {
		t.Fatalf("a verified candidate wave reported an error: %v", err)
	}
	// A verified wave is a finished run naming the review step, not a blocker,
	// and it says plainly that nothing was integrated and nothing was chosen.
	for _, phrase := range []string{"retained for review", "Nothing was integrated", "no candidate was selected"} {
		if !strings.Contains(answer, phrase) {
			t.Fatalf("the answer does not say %q:\n%s", phrase, answer)
		}
	}

	snapshot := runtime.GoalGraph.Snapshot()
	if snapshot.Outcome != goalgraph.OutcomeAwaitingReview {
		t.Fatalf("graph outcome=%q reason=%q", snapshot.Outcome, snapshot.Reason)
	}
	if after := writerFixtureState(t, workspace); after != before {
		t.Fatalf("the parent workspace changed during a candidate wave:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	// Every candidate is attributable to its node and attempt, verified against
	// its own tree, and still on disk for review.
	trees := runtime.GoalGraph.RetainedWorktrees()
	if len(trees) != 2 {
		t.Fatalf("retained worktrees=%+v, want one per node", trees)
	}
	seen := map[int]bool{}
	for _, tree := range trees {
		seen[tree.NodeID] = true
		if tree.Verification != "passed" {
			t.Fatalf("node %d candidate verification=%q, want passed", tree.NodeID, tree.Verification)
		}
		if tree.AttemptID == "" || tree.Branch == "" {
			t.Fatalf("node %d candidate is not attributable: %+v", tree.NodeID, tree)
		}
		expected := "alpha/extra.go"
		if tree.NodeID == 2 {
			expected = "beta/extra.go"
		}
		if _, statErr := os.Stat(filepath.Join(tree.Worktree, expected)); statErr != nil {
			t.Fatalf("node %d retained tree does not hold %s: %v", tree.NodeID, expected, statErr)
		}
	}
	if !seen[1] || !seen[2] {
		t.Fatalf("candidates are not one per node: %+v", trees)
	}
	for _, node := range snapshot.Nodes {
		if node.State != goalgraph.NodeAwaitingReview {
			t.Fatalf("node %d state=%q, want awaiting_review", node.ID, node.State)
		}
	}
	for _, attempt := range snapshot.Attempts {
		if attempt.State != goalgraph.AttemptCandidate {
			t.Fatalf("attempt %s state=%q, want candidate", attempt.ID, attempt.State)
		}
		if attempt.Candidate == nil || attempt.Candidate.VerificationToken == "" || len(attempt.Candidate.Verification) == 0 {
			t.Fatalf("attempt %s carries no machine-observed verification: %+v", attempt.ID, attempt.Candidate)
		}
		// The evidence must be a real detected command run in the candidate
		// tree, not a claim. This is the assertion that makes the evaluation
		// stronger than the focused tests, which stub the verifier out.
		for _, verification := range attempt.Candidate.Verification {
			if !strings.Contains(verification.Command, "go ") || verification.Status != "passed" {
				t.Fatalf("attempt %s verification is not a passing repository command: %+v", attempt.ID, verification)
			}
			if verification.StateToken != attempt.Candidate.VerificationToken {
				t.Fatalf("attempt %s verification is not bound to one child state: %+v", attempt.ID, verification)
			}
		}
	}

	// Two writers ran, both as write delegates, each bound to its own node.
	statuses := runtime.Team.Snapshot()
	if len(statuses) != 2 {
		t.Fatalf("delegate statuses=%+v, want two writers", statuses)
	}
	for _, status := range statuses {
		if !status.Write || status.PlanStep < 1 || status.PlanStep > 2 {
			t.Fatalf("writer status=%+v", status)
		}
	}
	candidateReady := 0
	for _, emitted := range events {
		if emitted.GoalGraph != nil && emitted.GoalGraph.State == "candidate_ready" {
			candidateReady++
		}
	}
	if candidateReady != 2 {
		t.Fatalf("candidate_ready updates=%d, want 2: %+v", candidateReady, events)
	}
}

// The same guarantee has to hold when a writer fails. The parent is still
// untouched, and the wave does not claim a review it never earned.
func TestOrchestratedGoalFailedWriterLeavesParentUntouchedEvaluation(t *testing.T) {
	workspace := orchestratedWriterGitFixture(t)
	before := writerFixtureState(t, workspace)
	client := &scopedWriterClient{t: t, written: map[string]bool{}, failFor: "extend beta", files: map[string]string{
		"extend alpha": "alpha/extra.go",
		"extend beta":  "beta/extra.go",
	}}
	runtime := newWriterEvaluationRuntime(t, workspace, client, []plan.Step{
		writerStep(1, "extend alpha", "alpha/"),
		writerStep(2, "extend beta", "beta/"),
	})

	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	if _, err := runtime.Agent.Run(ctx, "Produce both approved candidates.", func(event.Event) {}); err == nil {
		t.Fatal("a wave with a failed writer reported success")
	}

	snapshot := runtime.GoalGraph.Snapshot()
	if snapshot.Outcome == goalgraph.OutcomeDone {
		t.Fatalf("a wave with a failed writer completed the goal: %q", snapshot.Reason)
	}
	if after := writerFixtureState(t, workspace); after != before {
		t.Fatalf("the parent workspace changed during a failing wave:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	// The sibling's verified work survives its neighbour's failure, and the
	// failed node is blocked rather than quietly dropped.
	var succeeded, failed goalgraph.Node
	for _, node := range snapshot.Nodes {
		switch node.ID {
		case 1:
			succeeded = node
		case 2:
			failed = node
		}
	}
	if succeeded.State != goalgraph.NodeAwaitingReview {
		t.Fatalf("the verified sibling is %q, want awaiting_review", succeeded.State)
	}
	if failed.State != goalgraph.NodeBlocked {
		t.Fatalf("the failed writer is %q, want blocked", failed.State)
	}
}

// The operator's recovery path, end to end in a real runtime: a wave leaves
// directories behind, reconcile says what is actually in them, and discard
// removes one only after that observation and an explicit confirmation.
func TestOrchestratedGoalRetainedWorktreeReconciliationEvaluation(t *testing.T) {
	workspace := orchestratedWriterGitFixture(t)
	before := writerFixtureState(t, workspace)
	client := &scopedWriterClient{t: t, written: map[string]bool{}, files: map[string]string{
		"extend alpha": "alpha/extra.go",
	}}
	runtime := newWriterEvaluationRuntime(t, workspace, client, []plan.Step{
		writerStep(1, "extend alpha", "alpha/"),
	})

	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	if _, err := runtime.Agent.Run(ctx, "Produce the approved candidate.", func(event.Event) {}); err != nil {
		t.Fatalf("a verified candidate wave reported an error: %v", err)
	}
	if outcome, _ := runtime.GoalGraph.Outcome(); outcome != goalgraph.OutcomeAwaitingReview {
		t.Fatalf("graph outcome=%q, want awaiting_review", outcome)
	}

	// Before anyone looks, the runtime says so rather than implying the path is
	// current — and refuses to release the graph that is its only record.
	if pending := runtime.GoalGraph.UnreconciledWorktrees(); len(pending) != 1 {
		t.Fatalf("unreconciled worktrees=%+v, want the retained candidate", pending)
	}
	if _, err := runtime.CancelOrchestratedGoal(ctx); err == nil || !strings.Contains(err.Error(), "/orchestrate reconcile") {
		t.Fatalf("archive before reconciliation error=%v, want a refusal", err)
	}
	if _, err := runtime.DiscardOrchestratedWorktree(ctx, 1, true); err == nil || !strings.Contains(err.Error(), "reconcile") {
		t.Fatalf("discard before reconciliation error=%v, want a refusal", err)
	}

	status, err := runtime.ReconcileOrchestratedWorktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, string(goalgraph.DispositionPresent)) {
		t.Fatalf("reconcile did not report the tree as present:\n%s", status)
	}
	tree := runtime.GoalGraph.RetainedWorktrees()[0]
	if tree.Disposition != goalgraph.DispositionPresent || !strings.Contains(tree.Detail, "changed file") {
		t.Fatalf("observed disposition=%q detail=%q", tree.Disposition, tree.Detail)
	}

	// A tree still holding changes is not discardable by the ordinary command.
	if _, err := runtime.DiscardOrchestratedWorktree(ctx, 1, false); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("unconfirmed discard error=%v, want a demand for confirmation", err)
	}
	if _, statErr := os.Stat(tree.Worktree); statErr != nil {
		t.Fatalf("a refused discard removed the tree: %v", statErr)
	}

	if _, err := runtime.DiscardOrchestratedWorktree(ctx, 1, true); err != nil {
		t.Fatalf("confirmed discard failed: %v", err)
	}
	if _, statErr := os.Stat(tree.Worktree); !os.IsNotExist(statErr) {
		t.Fatalf("discarded worktree is still on disk: %v", statErr)
	}
	// The record of what was removed survives the removal, and the parent was
	// never involved in any of it.
	after := runtime.GoalGraph.RetainedWorktrees()[0]
	if after.Disposition != goalgraph.DispositionDiscarded || after.Worktree != tree.Worktree {
		t.Fatalf("the discarded candidate lost its record: %+v", after)
	}
	if state := writerFixtureState(t, workspace); state != before {
		t.Fatalf("the parent workspace changed across the recovery path:\nbefore:\n%s\nafter:\n%s", before, state)
	}
	// With every tree observed, releasing the graph is allowed.
	if _, err := runtime.CancelOrchestratedGoal(ctx); err != nil {
		t.Fatalf("archive after reconciliation failed: %v", err)
	}
}

// A retained candidate is reviewable through the ordinary delegate surface and
// publishable through none of it. The graph owns the node, attempt, and
// evidence, so a publication it did not perform would leave the node still
// reporting that reviewed integration is required while the parent workspace
// had already changed underneath it — and no combined-workspace verification
// would have run at all. Reviewing stays available because that is what the
// retained worktree is for.
func TestOrchestratedGoalCandidateCannotBePublishedByDelegateIntegrationEvaluation(t *testing.T) {
	workspace := orchestratedWriterGitFixture(t)
	before := writerFixtureState(t, workspace)
	client := &scopedWriterClient{t: t, written: map[string]bool{}, files: map[string]string{
		"extend alpha": "alpha/extra.go",
	}}
	runtime := newWriterEvaluationRuntime(t, workspace, client, []plan.Step{
		writerStep(1, "extend alpha", "alpha/"),
	})

	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	if _, err := runtime.Agent.Run(ctx, "Produce the approved candidate.", func(event.Event) {}); err != nil {
		t.Fatalf("a verified candidate wave reported an error: %v", err)
	}
	if outcome, _ := runtime.GoalGraph.Outcome(); outcome != goalgraph.OutcomeAwaitingReview {
		t.Fatalf("graph outcome=%q, want awaiting_review", outcome)
	}
	candidate := ""
	for _, status := range runtime.Team.Snapshot() {
		if status.Write {
			candidate = status.ID
			if !status.GraphNode {
				t.Fatalf("a graph-owned candidate is not marked as one: %+v", status)
			}
		}
	}
	if candidate == "" {
		t.Fatal("the wave produced no write delegate")
	}

	// Review is allowed and shows the real diff.
	preview, err := runtime.PrepareDelegateIntegration(ctx, candidate)
	if err != nil {
		t.Fatalf("reviewing a graph candidate was refused: %v", err)
	}
	if !preview.GraphOwned || len(preview.Files) == 0 {
		t.Fatalf("preview=%+v, want a graph-owned preview with files", preview)
	}

	selections := make([]app.DelegateIntegrationSelection, 0, len(preview.Files))
	for _, file := range preview.Files {
		hunks, parseErr := diffmodel.ParseHunks(file.Unified)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		keep := make([]bool, len(hunks))
		for i := range keep {
			keep[i] = true
		}
		selections = append(selections, app.DelegateIntegrationSelection{Path: file.Path, Keep: keep})
	}

	// Every publication path refuses, including the primary-agent reviewed one
	// that carries a valid review token.
	applied, err := runtime.ApplyDelegateIntegration(ctx, candidate, selections)
	if err == nil {
		t.Fatalf("delegate integration published a graph candidate: %v", applied)
	}
	if !strings.Contains(err.Error(), "Orchestrated Goal candidate") {
		t.Fatalf("refusal does not explain itself: %v", err)
	}
	if _, err := runtime.ApplyReviewedDelegateIntegration(ctx, candidate, preview.ReviewToken, selections); err == nil {
		t.Fatal("reviewed delegate integration published a graph candidate")
	}
	if _, err := runtime.PrepareReviewedDelegateIntegrationAction(ctx, candidate, preview.ReviewToken, selections); err == nil {
		t.Fatal("a graph candidate produced an authorizable integration action")
	}

	// The parent is untouched and the graph's account of the node still holds.
	if after := writerFixtureState(t, workspace); after != before {
		t.Fatalf("a refused publication changed the parent workspace:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	node := runtime.GoalGraph.Snapshot().Nodes[0]
	if node.State != goalgraph.NodeAwaitingReview {
		t.Fatalf("node state=%q, want awaiting_review", node.State)
	}
	if tree := runtime.GoalGraph.RetainedWorktrees()[0]; tree.Verification != "passed" {
		t.Fatalf("the candidate lost its verification: %+v", tree)
	}
}
