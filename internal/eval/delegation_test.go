package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robert-mcdermott/collomia/internal/agent"
	"github.com/robert-mcdermott/collomia/internal/app"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/diffmodel"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

type parallelDelegationEvaluationClient struct {
	mu      sync.Mutex
	active  int
	maxSeen int
	ready   chan struct{}
	once    sync.Once
}

func (c *parallelDelegationEvaluationClient) Name() string { return "parallel-delegation-evaluation" }

func (c *parallelDelegationEvaluationClient) Chat(ctx context.Context, request provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	if len(request.Messages) != 1 {
		return provider.Response{Content: "write completed with isolated evidence"}, nil
	}
	c.mu.Lock()
	c.active++
	if c.active > c.maxSeen {
		c.maxSeen = c.active
	}
	if c.active == 2 {
		c.once.Do(func() { close(c.ready) })
	}
	c.mu.Unlock()
	select {
	case <-c.ready:
	case <-ctx.Done():
		c.mu.Lock()
		c.active--
		c.mu.Unlock()
		return provider.Response{}, ctx.Err()
	}
	c.mu.Lock()
	c.active--
	c.mu.Unlock()
	if strings.Contains(request.Messages[0].Content, "write the isolated") {
		return provider.Response{ToolCalls: []provider.ToolCall{{ID: "write-evidence", Name: "write_file", Arguments: json.RawMessage(`{"path":"agent.txt","content":"isolated child result\n"}`)}}}, nil
	}
	return provider.Response{Content: "read-only inspection completed"}, nil
}

// TestParallelGovernedDelegationEvaluation proves the real delegate tool can
// admit a read specialist and an isolated writer concurrently, retain their
// plan association and evidence, and leave the parent workspace untouched.
func TestParallelGovernedDelegationEvaluation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	workspace := t.TempDir()
	runEvalGit(t, workspace, "init", "-b", "main")
	runEvalGit(t, workspace, "config", "core.autocrlf", "false")
	mustWriteEvaluationFile(t, filepath.Join(workspace, "README.md"), "delegation fixture\n")
	runEvalGit(t, workspace, "add", ".")
	runEvalGit(t, workspace, "commit", "-m", "base")

	cfg := appconfig.Defaults()
	cfg.Permissions.Mode = "autopilot"
	cfg.Options.DelegateMaxConcurrency = 2
	registry, _, processes, err := tools.Builtins(workspace, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(processes.StopAll)
	client := &parallelDelegationEvaluationClient{ready: make(chan struct{})}
	root := agent.New(agent.Options{
		Client: client, ProviderName: "fixture", Model: "scripted", ProviderConfig: appconfig.Provider{MaxTokens: 128},
		Workspace: workspace, Registry: registry, Permissions: permission.New(cfg.Permissions, nil), MaxIterations: 4,
	})
	board := plan.NewBoard()
	if err := board.Set(plan.Plan{Goal: "evaluate delegation", Steps: []plan.Step{
		{ID: 1, Title: "prepare", Status: "done", Evidence: "fixture committed"},
		{ID: 2, Title: "parallel investigation", Status: "in_progress", DependsOn: []int{1}},
	}}); err != nil {
		t.Fatal(err)
	}
	team := agent.NewTeam()
	root.AddDelegationTool(cfg, nil, team, board)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result, err := registry.Execute(ctx, "delegate", json.RawMessage(`{"tasks":[{"name":"reader","task":"inspect the committed fixture","plan_step":2},{"name":"writer","task":"write the isolated evidence file","write":true,"plan_step":2}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var inbox struct {
		Tasks []agent.DelegateResult `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(result), &inbox); err != nil {
		t.Fatalf("decode parent inbox: %v\n%s", err, result)
	}
	if len(inbox.Tasks) != 2 {
		t.Fatalf("parent inbox=%+v", inbox)
	}
	client.mu.Lock()
	maxSeen := client.maxSeen
	client.mu.Unlock()
	if maxSeen != 2 {
		t.Fatalf("delegates were not concurrent: max in flight=%d", maxSeen)
	}
	var writer agent.DelegateResult
	for _, task := range inbox.Tasks {
		if task.Status != agent.DelegateDone || task.PlanStep != 2 {
			t.Fatalf("task outcome=%+v", task)
		}
		if task.Name == "writer" {
			writer = task
		}
	}
	if writer.Worktree == "" || len(writer.ChangedFiles) != 1 || writer.ChangedFiles[0] != "agent.txt" {
		t.Fatalf("writer outcome=%+v", writer)
	}
	if _, err := os.Stat(filepath.Join(workspace, "agent.txt")); !os.IsNotExist(err) {
		t.Fatalf("isolated writer changed the parent workspace: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(writer.Worktree, "agent.txt")); err != nil || string(data) != "isolated child result\n" {
		t.Fatalf("isolated artifact=%q err=%v", data, err)
	}
	for _, status := range team.Snapshot() {
		if status.Worktree != "" {
			_ = exec.Command("git", "-C", workspace, "worktree", "remove", "--force", status.Worktree).Run()
			_ = exec.Command("git", "-C", workspace, "branch", "-D", status.Branch).Run()
		}
	}
}

// TestDelegatedSelectiveIntegrationEvaluation exercises the operator-visible
// outcome rather than an isolated parser: a retained child worktree proposes
// two changes, the parent integrates one hunk, and the repository's real test
// command validates the result. The inherited autocrlf setting reproduces the
// Windows Actions environment on every host; repository-local configuration
// must keep the fixture byte-stable.
func TestDelegatedSelectiveIntegrationEvaluation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	globalConfig := filepath.Join(t.TempDir(), "global.gitconfig")
	if err := os.WriteFile(globalConfig, []byte("[core]\n\tautocrlf = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)

	workspace := t.TempDir()
	runEvalGit(t, workspace, "init", "-b", "main")
	runEvalGit(t, workspace, "config", "core.autocrlf", "false")
	mustWriteEvaluationFile(t, filepath.Join(workspace, "go.mod"), "module delegationfixture\n\ngo 1.26.0\n")
	dir := filepath.Join(workspace, "internal", "MixedCase")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const source = `package mixedcase

func Answer() int { return 41 }

// gap 1
// gap 2
// gap 3
// gap 4
// gap 5
// gap 6
// gap 7
// gap 8
// gap 9
// gap 10

func Label() string { return "old" }
`
	const testSource = `package mixedcase

import "testing"

func TestAnswer(t *testing.T) {
	if Answer() != 42 { t.Fatalf("Answer() = %d", Answer()) }
}
`
	parentFile := filepath.Join(dir, "calc.go")
	mustWriteEvaluationFile(t, parentFile, source)
	mustWriteEvaluationFile(t, filepath.Join(dir, "calc_test.go"), testSource)
	runEvalGit(t, workspace, "add", ".")
	runEvalGit(t, workspace, "commit", "-m", "base")
	base := strings.TrimSpace(string(evalGitOutput(t, workspace, "rev-parse", "HEAD")))

	worktree := filepath.Join(t.TempDir(), "DelegatedWorktree")
	branch := "collomia/eval-selective-integration"
	runEvalGit(t, workspace, "worktree", "add", "-b", branch, worktree, "HEAD")
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", workspace, "worktree", "remove", "--force", worktree).Run()
		_ = exec.Command("git", "-C", workspace, "branch", "-D", branch).Run()
	})
	childFile := filepath.Join(worktree, "internal", "MixedCase", "calc.go")
	parentBytes, parentErr := os.ReadFile(parentFile)
	childBytes, childErr := os.ReadFile(childFile)
	if parentErr != nil || childErr != nil || !bytes.Equal(parentBytes, childBytes) {
		t.Fatalf("parent/worktree checkout differs before delegated work: parent=%v child=%v", parentErr, childErr)
	}
	childSource := strings.Replace(source, "return 41", "return 42", 1)
	childSource = strings.Replace(childSource, `return "old"`, `return "new"`, 1)
	mustWriteEvaluationFile(t, childFile, childSource)

	team := agent.NewTeam()
	team.Enqueue(agent.DelegateStart{ID: "evaluation-child", Name: "writer", Task: "fix Answer and modernize Label", Write: true})
	team.FinishDetailed("evaluation-child", "two candidate edits", []string{"child produced two hunks"}, []string{"internal/MixedCase/calc.go"}, worktree, branch, base, provider.Usage{}, nil)
	runtime := &app.Runtime{
		Workspace: workspace, Team: team,
		Permissions: permission.New(appconfig.Permissions{Mode: "workspace"}, nil),
		Changes:     diffmodel.NewTracker(workspace),
	}
	preview, err := runtime.PrepareDelegateIntegration(t.Context(), "evaluation-child")
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Files) != 1 || preview.Files[0].Conflict != "" {
		t.Fatalf("preview=%+v", preview)
	}
	hunks, err := diffmodel.ParseHunks(preview.Files[0].Unified)
	if err != nil {
		t.Fatal(err)
	}
	if len(hunks) != 2 {
		t.Fatalf("expected two selectable hunks, got %d\n%s", len(hunks), preview.Files[0].Unified)
	}
	applied, err := runtime.ApplyDelegateIntegration(t.Context(), "evaluation-child", []app.DelegateIntegrationSelection{{Path: "internal/MixedCase/calc.go", Keep: []bool{true, false}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 || applied[0] != "internal/MixedCase/calc.go" {
		t.Fatalf("applied=%v", applied)
	}
	got, err := os.ReadFile(parentFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("return 42")) || !bytes.Contains(got, []byte(`return "old"`)) || bytes.Contains(got, []byte("\r\n")) {
		t.Fatalf("selective result or line endings are wrong:\n%s", got)
	}
	verify := exec.CommandContext(context.Background(), "go", "test", "./...")
	verify.Dir = workspace
	verify.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if output, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("integrated repository verification failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(childFile); err != nil {
		t.Fatalf("retained child worktree disappeared: %v", err)
	}
	status, ok := team.Get("evaluation-child")
	if !ok || len(status.Integrated) != 1 || status.Integrated[0] != "internal/MixedCase/calc.go" {
		t.Fatalf("durable integration outcome=%+v", status)
	}
}

func mustWriteEvaluationFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runEvalGit(t *testing.T, workspace string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", workspace}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Collomia Evaluation", "GIT_AUTHOR_EMAIL=evaluation@example.invalid", "GIT_COMMITTER_NAME=Collomia Evaluation", "GIT_COMMITTER_EMAIL=evaluation@example.invalid")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func evalGitOutput(t *testing.T, workspace string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", workspace}, args...)...)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return output
}
