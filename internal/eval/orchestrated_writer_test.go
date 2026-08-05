package eval

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/session"
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
	return newGatedWriterEvaluationRuntime(t, workspace, client, steps, nil)
}

// newGatedWriterEvaluationRuntime denies the named tools outright, which is how
// the evaluation reaches a refusal that no autonomy mode can override.
func newGatedWriterEvaluationRuntime(t *testing.T, workspace string, client provider.Client, steps []plan.Step, deniedTools []string) *app.Runtime {
	t.Helper()
	return newRuledWriterEvaluationRuntime(t, workspace, client, steps, deniedTools, nil)
}

// newRuledWriterEvaluationRuntime additionally carries scoped permission rules,
// which is how the evaluation asks whether a rule a user wrote for their own
// workspace governs the graph the same way it governs Standard mode.
func newRuledWriterEvaluationRuntime(t *testing.T, workspace string, client provider.Client, steps []plan.Step, deniedTools []string, rules []appconfig.Rule) *app.Runtime {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configDir := filepath.Join(home, ".collomia")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// One build cache for the whole package rather than a cold one per
	// evaluation. A candidate wave runs the toolchain once per worktree and
	// again over the combined workspace, so starting cold each time was the
	// dominant cost of this package.
	buildCache := sharedBuildCache(t)
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
			// The shared Go build cache lives outside every workspace, so the
			// sandboxed toolchain needs an explicit root to populate it.
			"sandbox_writable_roots": []string{buildCache},
			"denied_tools":           deniedTools,
			"rules":                  rules,
		},
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteEvaluationFile(t, filepath.Join(configDir, "config.json"), string(encoded))

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

// The first time the runtime writes a candidate into the user's own workspace.
// Everything about this path is gated: only a person can reach it, the whole
// candidate goes or none of it does, and the node explicitly does not complete,
// because the child's verification passed against its own isolated tree and
// says nothing about the parent it has just been merged into.
func TestOrchestratedGoalCandidateIntegrationEvaluation(t *testing.T) {
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
	if state := writerFixtureState(t, workspace); state != before {
		t.Fatal("the wave changed the parent workspace before anyone integrated")
	}

	status, err := runtime.IntegrateOrchestratedCandidate(ctx, 1)
	if err != nil {
		t.Fatalf("integrating a verified candidate failed: %v", err)
	}

	// The bytes are really in the parent now.
	published := filepath.Join(workspace, "alpha", "extra.go")
	data, err := os.ReadFile(published)
	if err != nil {
		t.Fatalf("the candidate was not published into the parent: %v", err)
	}
	if !strings.Contains(string(data), "func Extra()") {
		t.Fatalf("published content is not the candidate's: %q", data)
	}
	if state := writerFixtureState(t, workspace); state == before {
		t.Fatal("integration reported success without changing the parent workspace")
	}

	// And the node is explicitly not finished.
	snapshot := runtime.GoalGraph.Snapshot()
	node := snapshot.Nodes[0]
	if node.State != goalgraph.NodeIntegrated {
		t.Fatalf("node state=%q, want integrated", node.State)
	}
	if node.State == goalgraph.NodeDone {
		t.Fatal("a child's verification was accepted as the combined workspace's")
	}
	if !strings.Contains(node.Reason, "combined") || !strings.Contains(node.Reason, "unverified") {
		t.Fatalf("the node does not say the combined result is unverified: %q", node.Reason)
	}
	if snapshot.Outcome != goalgraph.OutcomeAwaitingVerification {
		t.Fatalf("graph outcome=%q, want awaiting_verification", snapshot.Outcome)
	}
	if !strings.Contains(status, string(goalgraph.NodeIntegrated)) {
		t.Fatalf("status does not report the integration:\n%s", status)
	}

	// The publication is undoable: a durable checkpoint recorded what the
	// parent held before it, and the node names it.
	checkpoints := runtime.Session.AllIntegrationCheckpoints()
	if len(checkpoints) != 1 || checkpoints[0].State != session.IntegrationApplied {
		t.Fatalf("integration checkpoints=%+v", checkpoints)
	}
	if checkpoints[0].GraphNode != 1 {
		t.Fatalf("the checkpoint is not attributed to the plan node: %+v", checkpoints[0])
	}
	if !strings.Contains(node.Reason, checkpoints[0].ID) {
		t.Fatalf("the node does not name the checkpoint that can undo it: %q", node.Reason)
	}

	// Integrating twice is refused rather than republished.
	if _, err := runtime.IntegrateOrchestratedCandidate(ctx, 1); err == nil {
		t.Fatal("a node was integrated twice")
	}
}

// A candidate whose parent moved underneath it is refused whole. Publishing
// the part that still applies would produce a combined workspace that is
// neither what the child verified nor what the user had.
func TestOrchestratedGoalIntegrationRefusesAConflictedCandidateEvaluation(t *testing.T) {
	workspace := orchestratedWriterGitFixture(t)
	client := &scopedWriterClient{t: t, written: map[string]bool{}, files: map[string]string{
		"extend alpha": "alpha/extra.go",
	}}
	runtime := newWriterEvaluationRuntime(t, workspace, client, []plan.Step{
		writerStep(1, "extend alpha", "alpha/"),
	})
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	if _, err := runtime.Agent.Run(ctx, "Produce the approved candidate.", func(event.Event) {}); err != nil {
		t.Fatal(err)
	}

	// The user writes their own version of the same file after the wave.
	if err := os.WriteFile(filepath.Join(workspace, "alpha", "extra.go"),
		[]byte("package alpha\n\nfunc Extra() string { return \"mine\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mine := writerFixtureState(t, workspace)

	_, err := runtime.IntegrateOrchestratedCandidate(ctx, 1)
	if err == nil {
		t.Fatal("integration overwrote the user's own conflicting change")
	}
	if state := writerFixtureState(t, workspace); state != mine {
		t.Fatal("a refused integration still changed the parent workspace")
	}
	if node := runtime.GoalGraph.Snapshot().Nodes[0]; node.State != goalgraph.NodeAwaitingReview {
		t.Fatalf("a refused integration moved the node to %q", node.State)
	}
}

// The last gate: an integrated node completes only on evidence about the
// combined workspace it now lives in. This is the whole point of the separate
// `integrated` state — the child's suite passed in an isolated worktree, and
// running the repository's own checks against the merged parent is a different
// question with a different answer.
func TestOrchestratedGoalCombinedVerificationCompletesTheGoalEvaluation(t *testing.T) {
	workspace := orchestratedWriterGitFixture(t)
	client := &scopedWriterClient{t: t, written: map[string]bool{}, files: map[string]string{
		"extend alpha": "alpha/extra.go",
	}}
	runtime := newWriterEvaluationRuntime(t, workspace, client, []plan.Step{
		writerStep(1, "extend alpha", "alpha/"),
	})
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Second)
	defer cancel()
	if _, err := runtime.Agent.Run(ctx, "Produce the approved candidate.", func(event.Event) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.IntegrateOrchestratedCandidate(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if node := runtime.GoalGraph.Snapshot().Nodes[0]; node.State != goalgraph.NodeIntegrated {
		t.Fatalf("node state=%q, want integrated", node.State)
	}

	status, err := runtime.VerifyOrchestratedIntegration(ctx, nil)
	if err != nil {
		t.Fatalf("combined verification of a good merge failed: %v", err)
	}
	snapshot := runtime.GoalGraph.Snapshot()
	if snapshot.Nodes[0].State != goalgraph.NodeDone {
		t.Fatalf("node state=%q after passing combined verification, want done", snapshot.Nodes[0].State)
	}
	if snapshot.Outcome != goalgraph.OutcomeDone {
		t.Fatalf("graph outcome=%q, want done", snapshot.Outcome)
	}
	if !strings.Contains(status, "Combined-workspace verification passed") {
		t.Fatalf("status does not report what completed the goal:\n%s", status)
	}
	// The completing evidence is machine-observed and bound to the workspace
	// the node was accepted in.
	combined := false
	for _, evidence := range snapshot.Evidence {
		if evidence.Tool == "verify_combined_workspace" && evidence.Status == "passed" {
			combined = true
			if evidence.WorkspaceToken != snapshot.WorkspaceToken {
				t.Fatalf("combined evidence is not bound to the accepted workspace: %+v", evidence)
			}
		}
	}
	if !combined {
		t.Fatalf("no combined-workspace verification evidence was recorded: %+v", snapshot.Evidence)
	}
}

// A merge that breaks the repository must not complete the goal, however
// cleanly it applied and however well the child's own tests did.
func TestOrchestratedGoalFailingCombinedVerificationKeepsTheNodeUnfinishedEvaluation(t *testing.T) {
	workspace := orchestratedWriterGitFixture(t)
	client := &scopedWriterClient{t: t, written: map[string]bool{}, files: map[string]string{
		"extend alpha": "alpha/extra.go",
	}}
	runtime := newWriterEvaluationRuntime(t, workspace, client, []plan.Step{
		writerStep(1, "extend alpha", "alpha/"),
	})
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Second)
	defer cancel()
	if _, err := runtime.Agent.Run(ctx, "Produce the approved candidate.", func(event.Event) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.IntegrateOrchestratedCandidate(ctx, 1); err != nil {
		t.Fatal(err)
	}
	// Break a package the candidate never touched, which is exactly the class
	// of failure a child worktree's own suite cannot see.
	if err := os.WriteFile(filepath.Join(workspace, "beta", "beta.go"),
		[]byte("package beta\n\nfunc Name() string { return \"broken\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runtime.VerifyOrchestratedIntegration(ctx, nil); err == nil {
		t.Fatal("a failing combined workspace completed the goal")
	}
	snapshot := runtime.GoalGraph.Snapshot()
	if snapshot.Nodes[0].State == goalgraph.NodeDone {
		t.Fatal("a node completed on a failing combined workspace")
	}
	if snapshot.Outcome == goalgraph.OutcomeDone {
		t.Fatalf("graph outcome=%q on a failing combined workspace", snapshot.Outcome)
	}

	// A waiver is the user's own claim, and it is recorded as such rather than
	// as verification.
	waived, err := runtime.WaiveOrchestratedVerification(ctx, "the beta package is broken by an unrelated local edit, not by this candidate")
	if err != nil {
		t.Fatalf("recording a waiver failed: %v", err)
	}
	if !strings.Contains(waived, "user-authored waiver") || !strings.Contains(waived, "not machine-observed") {
		t.Fatalf("a waiver does not distinguish itself from verification:\n%s", waived)
	}
	final := runtime.GoalGraph.Snapshot()
	if final.Nodes[0].State != goalgraph.NodeDone {
		t.Fatalf("node state=%q after an explicit waiver, want done", final.Nodes[0].State)
	}
	if !strings.Contains(final.Nodes[0].Reason, "user-authored waiver") {
		t.Fatalf("the completed node does not record how it was accepted: %q", final.Nodes[0].Reason)
	}
	// A waiver needs a real reason.
	if _, err := runtime.WaiveOrchestratedVerification(ctx, "ok"); err == nil {
		t.Fatal("an empty-ish waiver reason was accepted")
	}
}

// Publication is the one graph action that changes the user's files, so a
// refusal has to leave everything exactly as it was: no bytes, no checkpoint
// implying a publication happened, and a node still holding its candidate for
// review rather than claiming a state the workspace is not in.
func TestOrchestratedGoalIntegrationDenialLeavesEverythingUntouchedEvaluation(t *testing.T) {
	workspace := orchestratedWriterGitFixture(t)
	client := &scopedWriterClient{t: t, written: map[string]bool{}, files: map[string]string{
		"extend alpha": "alpha/extra.go",
	}}
	runtime := newGatedWriterEvaluationRuntime(t, workspace, client,
		[]plan.Step{writerStep(1, "extend alpha", "alpha/")},
		[]string{"integrate_delegate"})

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Second)
	defer cancel()
	if _, err := runtime.Agent.Run(ctx, "Produce the approved candidate.", func(event.Event) {}); err != nil {
		t.Fatal(err)
	}
	if outcome, _ := runtime.GoalGraph.Outcome(); outcome != goalgraph.OutcomeAwaitingReview {
		t.Fatalf("graph outcome=%q, want awaiting_review", outcome)
	}
	before := writerFixtureState(t, workspace)

	_, err := runtime.IntegrateOrchestratedCandidate(ctx, 1)
	if err == nil {
		t.Fatal("a denied integration published the candidate")
	}
	// Pin the reason, so the test cannot pass because integration failed for
	// some unrelated cause.
	if !strings.Contains(err.Error(), "permission denied") || !strings.Contains(err.Error(), "integrate_delegate") {
		t.Fatalf("integration failed for a reason other than the denial: %v", err)
	}
	if state := writerFixtureState(t, workspace); state != before {
		t.Fatal("a denied integration changed the parent workspace")
	}
	// No checkpoint: the refusal happened before anything was written, so a
	// durable record of a publication would itself be a false claim.
	if checkpoints := runtime.Session.AllIntegrationCheckpoints(); len(checkpoints) != 0 {
		t.Fatalf("a denied integration recorded a checkpoint: %+v", checkpoints)
	}
	snapshot := runtime.GoalGraph.Snapshot()
	if snapshot.Nodes[0].State != goalgraph.NodeAwaitingReview {
		t.Fatalf("node state=%q after denial, want awaiting_review", snapshot.Nodes[0].State)
	}
	if snapshot.Outcome != goalgraph.OutcomeAwaitingReview {
		t.Fatalf("graph outcome=%q after denial", snapshot.Outcome)
	}
	// The candidate is still there to review, which is what makes the denial
	// recoverable rather than merely refused.
	if tree := runtime.GoalGraph.RetainedWorktrees()[0]; tree.Verification != "passed" {
		t.Fatalf("the candidate lost its verification after a denial: %+v", tree)
	}
}

// betaOffLimitsRule is the same user-written rule for both halves of the
// comparison: one scoped deny on a directory of the parent workspace.
//
// It is written in the resolved form, which is what `permissions.rules`
// documents ("resolved path glob") and what the write tools match against. A
// temporary directory on macOS is reached through a symlink, so this is also
// the spelling that catches a mode evaluating the unresolved one.
func betaOffLimitsRule(t *testing.T, workspace string) appconfig.Rule {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	return appconfig.Rule{
		Action: "deny",
		Path:   filepath.ToSlash(filepath.Join(resolved, "beta")) + "/**",
		Reason: "beta is off limits in this repository",
	}
}

// Graduation clause: permission decisions must be equivalent to the same
// actions in Standard mode. This evaluation states what that means precisely,
// because the two modes are not identical everywhere and pretending otherwise
// would be the more dangerous claim.
//
// Equivalence is asserted at the *parent workspace boundary*. A candidate
// worktree is not the user's workspace — it is a quarantined copy at a
// different path whose bytes cannot reach the parent except through
// `integrate_delegate`, and a path rule the user wrote for their own
// repository does not, and should not, follow a scratch directory around the
// filesystem. What must hold is that the rule governs every byte that actually
// lands in the user's workspace, in either mode, and that it is the *same*
// rule that says so.
func TestOrchestratedGoalPermissionDecisionsMatchStandardModeEvaluation(t *testing.T) {
	// Standard mode: the agent writes straight into the user's workspace, so
	// the rule is evaluated against the final path directly.
	standardWorkspace := orchestratedWriterGitFixture(t)
	standardClient := &scriptedProvider{t: t, steps: []scriptedStep{
		{response: toolResponse("allowed", "write_file", `{"path":"alpha/extra.go","content":"package alpha\n\nfunc Extra() string { return \"extra\" }\n"}`)},
		{response: toolResponse("denied", "write_file", `{"path":"beta/extra.go","content":"package beta\n\nfunc Extra() string { return \"extra\" }\n"}`)},
		{check: requireLastToolContains("beta is off limits in this repository"), response: provider.Response{Content: "beta was refused by a permission rule; alpha was written."}},
	}}
	standardAgent, _ := newRuledEvaluationAgent(t, standardWorkspace, standardClient, "workspace", []appconfig.Rule{betaOffLimitsRule(t, standardWorkspace)})
	var standardEvents []event.Event
	if _, err := standardAgent.Run(t.Context(), "Extend alpha and beta.", func(e event.Event) { standardEvents = append(standardEvents, e) }); err != nil {
		t.Fatalf("standard mode run: %v", err)
	}
	// The rule discriminates rather than blanket-refusing: the sibling package
	// outside its scope was written in the same run.
	if _, err := os.Stat(filepath.Join(standardWorkspace, "alpha/extra.go")); err != nil {
		t.Fatalf("standard mode refused a write the rule does not cover: %v", err)
	}
	if _, err := os.Stat(filepath.Join(standardWorkspace, "beta/extra.go")); !os.IsNotExist(err) {
		t.Fatalf("standard mode wrote a denied path: %v", err)
	}
	if deniedDecisions(standardEvents) != 1 {
		t.Fatalf("standard mode denied decisions=%d, want exactly the beta write", deniedDecisions(standardEvents))
	}

	// Orchestrated mode: the same rule, the same target path, a separate
	// repository so neither half can affect the other.
	graphWorkspace := orchestratedWriterGitFixture(t)
	before := writerFixtureState(t, graphWorkspace)
	graphClient := &scopedWriterClient{t: t, written: map[string]bool{}, files: map[string]string{
		"extend beta": "beta/extra.go",
	}}
	runtime := newRuledWriterEvaluationRuntime(t, graphWorkspace, graphClient, []plan.Step{
		writerStep(1, "extend beta", "beta/"),
	}, nil, []appconfig.Rule{betaOffLimitsRule(t, graphWorkspace)})

	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	if _, err := runtime.Agent.Run(ctx, "Produce the approved candidate.", func(event.Event) {}); err != nil {
		t.Fatalf("candidate wave: %v", err)
	}

	// The candidate exists inside its quarantine. This is the honest half of
	// the finding and is asserted rather than glossed: the writer's own path
	// is not the path the rule names, so the rule does not stop it there — and
	// it does not need to, because nothing in that directory is the user's
	// repository yet.
	trees := runtime.GoalGraph.RetainedWorktrees()
	if len(trees) != 1 {
		t.Fatalf("retained worktrees=%+v, want one", trees)
	}
	if _, err := os.Stat(filepath.Join(trees[0].Worktree, "beta/extra.go")); err != nil {
		t.Fatalf("the candidate was not produced in its own tree: %v", err)
	}

	// The boundary that matters. The identical rule refuses the publication,
	// and it is the same rule: the denial carries the reason the user wrote.
	_, err := runtime.IntegrateOrchestratedCandidate(t.Context(), 1)
	if err == nil {
		t.Fatal("a denied path was published into the parent workspace through the graph")
	}
	if !strings.Contains(err.Error(), "beta is off limits in this repository") {
		t.Fatalf("the graph refusal does not name the user's rule: %v", err)
	}
	if !errors.Is(err, permission.ErrDenied) {
		t.Fatalf("the graph refusal is not a permission denial: %v", err)
	}
	if after := writerFixtureState(t, graphWorkspace); after != before {
		t.Fatalf("a refused publication changed the parent workspace:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	// A denial is not a loss. The verified candidate is still there to review,
	// which is what makes the refusal a decision the user can revisit.
	if state := runtime.GoalGraph.Snapshot().Nodes[0].State; state != goalgraph.NodeAwaitingReview {
		t.Fatalf("node state after a refused publication=%q, want awaiting_review", state)
	}
}
