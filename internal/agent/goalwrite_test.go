package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// writeWave is the application-level harness for one graph-owned isolated
// writer wave. These tests drive the real controller, delegate permission,
// worktree, and verification paths rather than the graph state machine alone,
// because that is where a failing wave decides what happens to real
// directories on disk.
type writeWave struct {
	workspace string
	graph     *goalgraph.Graph
	runtime   *Agent
	team      *Team
}

func newWriteWave(t *testing.T, client provider.Client, approver permission.Approver, nodes ...goalgraph.NodeSpec) *writeWave {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	workspace := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", workspace}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "initial")

	graph, err := goalgraph.New(goalgraph.Spec{Goal: "produce reviewable candidates", Nodes: nodes}, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	registry, _, processes, err := tools.Builtins(workspace, appconfig.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(processes.StopAll)
	cfg := appconfig.Defaults()
	cfg.Permissions.Mode = "autopilot"
	if approver != nil {
		cfg.Permissions.Mode = "ask"
	}
	runtime := New(Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: workspace, Registry: registry, Permissions: permission.New(cfg.Permissions, approver), MaxIterations: 4,
		GoalGraph: graph, GoalStateToken: func(ctx context.Context) (string, error) { return goalgraph.WorkspaceStateToken(ctx, workspace) },
	})
	team := NewTeam()
	runtime.AddDelegationTool(cfg, approver, team)
	wave := &writeWave{workspace: workspace, graph: graph, runtime: runtime, team: team}
	wave.verifyWith(func(ctx context.Context, id string) ([]DelegateVerification, error) {
		return wave.passingVerification(ctx, id)
	})
	t.Cleanup(wave.removeRetainedWorktrees)
	return wave
}

// verifyWith replaces the application's child-verification step. The real one
// redetects repository-standard commands inside the retained worktree; the
// tests need only its contract, which is that a candidate is eligible when
// every command passes against one unchanged child state token.
func (w *writeWave) verifyWith(verify func(context.Context, string) ([]DelegateVerification, error)) {
	w.runtime.SetGoalWriterVerifier(verify)
}

func (w *writeWave) passingVerification(ctx context.Context, id string) ([]DelegateVerification, error) {
	status, ok := w.team.Get(id)
	if !ok {
		return nil, errors.New("missing retained candidate")
	}
	token, err := goalgraph.WorkspaceStateToken(ctx, status.Worktree)
	if err != nil {
		return nil, err
	}
	result := DelegateVerification{Command: "go test ./...", Status: "passed", StateToken: token}
	w.team.MarkVerificationResult(id, token, []string{result.Command}, result)
	return []DelegateVerification{result}, nil
}

func (w *writeWave) removeRetainedWorktrees() {
	for _, status := range w.team.Snapshot() {
		if status.Worktree != "" {
			_ = exec.Command("git", "-C", w.workspace, "worktree", "remove", "--force", status.Worktree).Run()
		}
		if status.Branch != "" {
			_ = exec.Command("git", "-C", w.workspace, "branch", "-D", status.Branch).Run()
		}
	}
}

func (w *writeWave) node(t *testing.T, id int) goalgraph.Node {
	t.Helper()
	for _, node := range w.graph.Snapshot().Nodes {
		if node.ID == id {
			return node
		}
	}
	t.Fatalf("graph has no node %d", id)
	return goalgraph.Node{}
}

func (w *writeWave) attemptFor(t *testing.T, nodeID int) goalgraph.Attempt {
	t.Helper()
	for _, attempt := range w.graph.Snapshot().Attempts {
		if attempt.NodeID == nodeID {
			return attempt
		}
	}
	t.Fatalf("graph has no attempt for node %d", nodeID)
	return goalgraph.Attempt{}
}

func writerNode(id int, title, scope string) goalgraph.NodeSpec {
	return goalgraph.NodeSpec{
		ID: id, Title: title, Execution: goalgraph.ExecutionIsolatedWrite,
		WritePaths: []string{scope}, Acceptance: []string{"candidate passes runtime validation"},
	}
}

func writeThenSummarize(path, content, summary string) func(int, provider.Request) (provider.Response, error) {
	return func(call int, _ provider.Request) (provider.Response, error) {
		if call == 1 {
			args, _ := json.Marshal(map[string]string{"path": path, "content": content})
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "w", Name: "write_file", Arguments: args}}}, nil
		}
		return provider.Response{Content: summary}, nil
	}
}

func requestMentions(request provider.Request, needle string) bool {
	for _, message := range request.Messages {
		if strings.Contains(message.Content, needle) {
			return true
		}
	}
	return false
}

// A cancelled wave still leaves real worktrees on disk. The graph must be able
// to name them afterwards, or the promise to retain candidates for inspection
// is one the runtime cannot keep.
func TestGoalWriteWaveCancellationRecordsRetainedWorktrees(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		if call == 1 {
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "w", Name: "write_file", Arguments: json.RawMessage(`{"path":"new.txt","content":"candidate\n"}`)}}}, nil
		}
		// The user interrupts after the child has already changed files.
		cancel()
		return provider.Response{Content: "wrote the scoped file"}, nil
	}}
	wave := newWriteWave(t, client, nil, writerNode(1, "add candidate file", "new.txt"))

	if _, err := wave.runtime.Run(ctx, "create the candidate", func(event.Event) {}); err == nil {
		t.Fatal("cancelled wave reported success")
	}
	if outcome, _ := wave.graph.Outcome(); outcome != goalgraph.OutcomeCancelled {
		t.Fatalf("graph outcome=%q, want cancelled", outcome)
	}
	attempt := wave.attemptFor(t, 1)
	if attempt.Candidate == nil {
		t.Fatal("cancelled wave discarded the retained worktree it left on disk")
	}
	if attempt.State != goalgraph.AttemptCancelled {
		t.Fatalf("attempt state=%q, want cancelled", attempt.State)
	}
	if _, err := os.Stat(filepath.Join(attempt.Candidate.Worktree, "new.txt")); err != nil {
		t.Fatalf("recorded worktree does not hold the change: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wave.workspace, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("parent workspace was changed: %v", err)
	}
	// An operator who cancelled needs to find the directory without guessing
	// node identifiers, so the overview names it too.
	status, err := wave.graph.Inspect(0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "Retained candidates") || !strings.Contains(status, attempt.Candidate.Worktree) {
		t.Fatalf("graph overview omits the retained worktree:\n%s", status)
	}
	if !strings.Contains(status, "unverified") {
		t.Fatalf("a cancelled candidate must not read as verified:\n%s", status)
	}
}

// The record has to be durable before the child can change anything, because
// the case it exists for is the session ending without any further write.
func TestGoalWriteWaveRecordsTheWorktreeBeforeTheChildRuns(t *testing.T) {
	var wave *writeWave
	var recorded goalgraph.Attempt
	// Observe the durable state at the child's first provider call: by then the
	// worktree exists, and nothing in it has been written yet.
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		if call == 1 {
			recorded = wave.attemptFor(t, 1)
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "w", Name: "write_file", Arguments: json.RawMessage(`{"path":"new.txt","content":"candidate\n"}`)}}}, nil
		}
		return provider.Response{Content: "wrote the scoped file"}, nil
	}}
	wave = newWriteWave(t, client, nil, writerNode(1, "add candidate file", "new.txt"))

	if _, err := wave.runtime.Run(t.Context(), "create the candidate", func(event.Event) {}); err != nil {
		t.Fatalf("verified candidate wave reported an error: %v", err)
	}
	if recorded.Worktree == "" || recorded.Branch == "" {
		t.Fatalf("worktree identity was not durable before the child ran: %+v", recorded)
	}
	if recorded.Candidate != nil {
		t.Fatal("an in-flight attempt already carried an examined candidate")
	}
	final := wave.attemptFor(t, 1)
	if final.Candidate == nil || final.Candidate.Worktree != recorded.Worktree {
		t.Fatalf("the finished candidate does not match the recorded worktree: %+v", final)
	}
}

// Dispatch uses the ordinary delegate write permission. A refusal has to stop
// the wave before any worktree exists, not after.
func TestGoalWriteWaveDelegateDenialBlocksBeforeAnyWorktree(t *testing.T) {
	client := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
		t.Error("denied writer wave still reached the provider")
		return provider.Response{Content: "unreachable"}, nil
	}}
	denied := func(context.Context, permission.Request) (permission.Decision, error) {
		return permission.Decision{Allow: false}, nil
	}
	wave := newWriteWave(t, client, denied, writerNode(1, "add candidate file", "new.txt"))

	if _, err := wave.runtime.Run(t.Context(), "create the candidate", func(event.Event) {}); err == nil {
		t.Fatal("denied wave reported success")
	}
	node := wave.node(t, 1)
	if node.State != goalgraph.NodeBlocked {
		t.Fatalf("node state=%q, want blocked", node.State)
	}
	attempt := wave.attemptFor(t, 1)
	if len(attempt.Failures) == 0 || attempt.Failures[0].Kind != goalgraph.FailurePermission {
		t.Fatalf("attempt failures=%+v, want a permission failure", attempt.Failures)
	}
	if attempt.Candidate != nil {
		t.Fatalf("denied wave recorded a candidate: %+v", attempt.Candidate)
	}
	if len(wave.team.Snapshot()) != 0 {
		t.Fatalf("denied wave dispatched a child: %+v", wave.team.Snapshot())
	}
}

// Verification is the gate, so a child whose verification fails must not reach
// awaiting_review — while its worktree stays recorded, because a failed
// candidate is exactly what an operator wants to look at.
func TestGoalWriteWaveVerificationFailureBlocksButKeepsTheWorktree(t *testing.T) {
	client := &fakeClient{chat: writeThenSummarize("new.txt", "candidate\n", "wrote the scoped file")}
	wave := newWriteWave(t, client, nil, writerNode(1, "add candidate file", "new.txt"))
	wave.verifyWith(func(context.Context, string) ([]DelegateVerification, error) {
		return nil, errors.New("go test ./...: FAIL example/pkg")
	})

	if _, err := wave.runtime.Run(t.Context(), "create the candidate", func(event.Event) {}); err == nil {
		t.Fatal("unverified candidate wave reported success")
	}
	node := wave.node(t, 1)
	if node.State != goalgraph.NodeBlocked {
		t.Fatalf("node state=%q, want blocked", node.State)
	}
	attempt := wave.attemptFor(t, 1)
	if attempt.State == goalgraph.AttemptCandidate {
		t.Fatal("unverified attempt reached candidate state")
	}
	if len(attempt.Failures) == 0 || attempt.Failures[0].Kind != goalgraph.FailureVerification {
		t.Fatalf("attempt failures=%+v, want a verification failure", attempt.Failures)
	}
	if attempt.Candidate == nil || attempt.Candidate.VerificationState == "passed" {
		t.Fatalf("candidate=%+v, want a retained but unverified worktree", attempt.Candidate)
	}
}

// The declared scope is validated against what the child actually changed, so
// an out-of-scope write blocks the node and is named in the retained facts.
func TestGoalWriteWaveScopeViolationBlocksAndNamesTheStrayPath(t *testing.T) {
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		switch call {
		case 1:
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "w1", Name: "write_file", Arguments: json.RawMessage(`{"path":"new.txt","content":"candidate\n"}`)}}}, nil
		case 2:
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "w2", Name: "write_file", Arguments: json.RawMessage(`{"path":"stray.txt","content":"outside\n"}`)}}}, nil
		}
		return provider.Response{Content: "wrote both files"}, nil
	}}
	wave := newWriteWave(t, client, nil, writerNode(1, "add candidate file", "new.txt"))

	if _, err := wave.runtime.Run(t.Context(), "create the candidate", func(event.Event) {}); err == nil {
		t.Fatal("out-of-scope wave reported success")
	}
	node := wave.node(t, 1)
	if node.State != goalgraph.NodeBlocked {
		t.Fatalf("node state=%q, want blocked", node.State)
	}
	attempt := wave.attemptFor(t, 1)
	if attempt.State == goalgraph.AttemptCandidate {
		t.Fatal("out-of-scope attempt reached candidate state")
	}
	if attempt.Candidate == nil || !slicesContain(attempt.Candidate.ScopeViolations, "stray.txt") {
		t.Fatalf("candidate=%+v, want the stray path recorded", attempt.Candidate)
	}
	if _, err := os.Stat(filepath.Join(wave.workspace, "stray.txt")); !os.IsNotExist(err) {
		t.Fatalf("out-of-scope write reached the parent workspace: %v", err)
	}
}

// One writer failing must not discard a sibling's verified candidate. The
// graph is still blocked overall — every node is required — but the retained
// work survives for review.
func TestGoalWriteWaveKeepsAVerifiedSiblingWhenOneWriterFails(t *testing.T) {
	client := &concurrentClient{chat: func(request provider.Request) (provider.Response, error) {
		if requestMentions(request, "beta node") {
			return provider.Response{}, errors.New("provider is unavailable")
		}
		if len(request.Messages) == 1 {
			return provider.Response{ToolCalls: []provider.ToolCall{{ID: "w", Name: "write_file", Arguments: json.RawMessage(`{"path":"alpha.txt","content":"candidate\n"}`)}}}, nil
		}
		return provider.Response{Content: "wrote the scoped file"}, nil
	}}
	wave := newWriteWave(t, client, nil,
		writerNode(1, "alpha node", "alpha.txt"),
		writerNode(2, "beta node", "beta.txt"),
	)

	if _, err := wave.runtime.Run(t.Context(), "create the candidates", func(event.Event) {}); err == nil {
		t.Fatal("partially failed wave reported success")
	}
	if state := wave.node(t, 1).State; state != goalgraph.NodeAwaitingReview {
		t.Fatalf("verified node state=%q, want awaiting_review", state)
	}
	if state := wave.node(t, 2).State; state != goalgraph.NodeBlocked {
		t.Fatalf("failed node state=%q, want blocked", state)
	}
	alpha := wave.attemptFor(t, 1)
	if alpha.State != goalgraph.AttemptCandidate || alpha.Candidate == nil || alpha.Candidate.VerificationState != "passed" {
		t.Fatalf("verified attempt=%+v", alpha)
	}
	if outcome, _ := wave.graph.Outcome(); outcome != goalgraph.OutcomeBlocked {
		t.Fatalf("graph outcome=%q, want blocked", outcome)
	}
	status, err := wave.graph.Inspect(0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, alpha.Candidate.Worktree) {
		t.Fatalf("blocked graph hides the surviving candidate:\n%s", status)
	}
}

func slicesContain(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
