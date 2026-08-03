package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/agent"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/session"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// scriptedClient replays a fixed provider conversation so a full runtime can
// be exercised end to end without a network.
type scriptedClient struct {
	calls    int
	steps    []provider.Response
	requests []provider.Request
}

func TestOrchestratedProposalPromptPrefersCoherentGraphNodes(t *testing.T) {
	prompt := OrchestratedProposalPrompt("build a small application")
	for _, want := range []string{"Prefer 4–6 substantive outcome nodes", "12 steps is a hard maximum, not a target", "Coalesce serial changes that touch the same scope", "Default to primary for end-to-end build and change goals", "every isolated_write node must be a terminal leaf", "first mutating node must create a focused smoke test"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("proposal prompt missing %q:\n%s", want, prompt)
		}
	}
}

// isolateGlobalFiles points per-user state at a scratch home so tests never
// read or write the real ~/.collomia. It returns that home.
func isolateGlobalFiles(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeGlobalConfig(t, home, `{"default_provider":"ollama","default_model":"qwen3-coder","providers":{"ollama":{"type":"openai-compatible","base_url":"http://127.0.0.1:11434/v1","model":"qwen3-coder","context_window":32768,"max_tokens":8192}}}`)
	return home
}

// writeGlobalConfig installs a user-layer configuration in the isolated home.
func writeGlobalConfig(t *testing.T, home, configJSON string) {
	t.Helper()
	dir := filepath.Join(home, ".collomia")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEphemeralRuntimeSkipsDurableSessionButKeepsAuditInfrastructure(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	runtime, err := New(context.Background(), Options{Workspace: workspace, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if runtime.Sessions != nil || runtime.Session != nil {
		t.Fatalf("ephemeral runtime opened durable session state: store=%v session=%v", runtime.Sessions, runtime.Session)
	}
	if _, ok := runtime.Registry.Get("read_tool_result"); ok {
		t.Fatal("ephemeral runtime exposed durable artifact tool")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".collomia", "sessions")); !os.IsNotExist(err) {
		t.Fatalf("ephemeral run created sessions directory: %v", err)
	}
	if info, err := os.Stat(filepath.Join(home, ".collomia", "audit")); err != nil || !info.IsDir() {
		t.Fatalf("audit infrastructure should remain available: info=%v err=%v", info, err)
	}
}

func TestOrchestratedGoalRejectsEphemeralExecution(t *testing.T) {
	isolateGlobalFiles(t)
	approved := &plan.Plan{Goal: "change safely", Steps: []plan.Step{{ID: 1, Title: "change source"}}}
	if _, err := New(t.Context(), Options{Workspace: t.TempDir(), Ephemeral: true, OrchestratedGoal: approved}); err == nil || !strings.Contains(err.Error(), "durable session") {
		t.Fatalf("error=%v", err)
	}
}

func TestExplicitOrchestratedGoalPreviewRequiresFreshApprovedProposal(t *testing.T) {
	isolateGlobalFiles(t)
	runtime, err := New(t.Context(), Options{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	stale := plan.Plan{Goal: "old task", Steps: []plan.Step{{ID: 1, Title: "old step", Status: "pending", Acceptance: []string{"old result exists"}}}}
	if err := runtime.Plan.Set(stale); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.ApproveOrchestratedGoal(t.Context()); err == nil || !strings.Contains(err.Error(), "no new") {
		t.Fatalf("restored/ordinary plan was accepted without opt-in: %v", err)
	}

	prompt, err := runtime.BeginOrchestratedProposal("repair the service and verify it")
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.Agent.Plan() || !strings.Contains(prompt, "read-only planning mode") {
		t.Fatalf("proposal did not enter an explicit read-only design phase: plan=%t prompt=%q", runtime.Agent.Plan(), prompt)
	}
	if _, _, err := runtime.RewindSession(0); err == nil || !strings.Contains(err.Error(), "proposal") {
		t.Fatalf("proposal consent marker was allowed to cross a rewind boundary: %v", err)
	}
	if _, _, err := runtime.ApproveOrchestratedGoal(t.Context()); err == nil || !strings.Contains(err.Error(), "did not create a new") {
		t.Fatalf("stale pre-proposal plan was accepted: %v", err)
	}

	missingAcceptance := plan.Plan{Goal: "repair the service", Steps: []plan.Step{{ID: 1, Title: "repair", Status: "pending"}}}
	if err := runtime.Plan.Set(missingAcceptance); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.ApproveOrchestratedGoal(t.Context()); err == nil || !strings.Contains(err.Error(), "acceptance criterion") {
		t.Fatalf("proposal without acceptance criteria was accepted: %v", err)
	}

	approved := plan.Plan{Goal: "repair the service", Steps: []plan.Step{
		{ID: 1, Title: "inspect failure", Status: "pending", Acceptance: []string{"the failure cause is grounded in repository evidence"}},
		{ID: 2, Title: "repair and verify", Status: "pending", DependsOn: []int{1}, Acceptance: []string{"the repository test suite passes after the repair"}},
	}}
	if err := runtime.Plan.Set(approved); err != nil {
		t.Fatal(err)
	}
	proposalStatus, err := runtime.OrchestratedGoalStatus(0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(proposalStatus, "Proposal work to seed at approval:") || !strings.Contains(proposalStatus, "0/1000000 tokens") {
		t.Fatalf("proposal status did not distinguish cumulative budget:\n%s", proposalStatus)
	}
	if phase, used, limit, ok := runtime.OrchestratedGoalTokenBudget(); !ok || phase != "proposal" || used != 0 || limit != 1_000_000 {
		t.Fatalf("proposal token budget phase=%q used=%d limit=%d ok=%t", phase, used, limit, ok)
	}
	status, executionPrompt, err := runtime.ApproveOrchestratedGoal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GoalGraph == nil || runtime.Agent.Plan() {
		t.Fatalf("approval did not attach execution graph and leave planning mode: graph=%v plan=%t", runtime.GoalGraph, runtime.Agent.Plan())
	}
	for _, want := range []string{"Experimental Orchestrated Goal", "one serial primary lane", "Aggregate envelope:", "0/96 provider iterations", "0/1000000 tokens", "acceptance: the repository test suite passes"} {
		if !strings.Contains(status, want) {
			t.Fatalf("approved status missing %q:\n%s", want, status)
		}
	}
	if phase, used, limit, ok := runtime.OrchestratedGoalTokenBudget(); !ok || phase != "active" || used != 0 || limit != 1_000_000 {
		t.Fatalf("active token budget phase=%q used=%d limit=%d ok=%t", phase, used, limit, ok)
	}
	if !strings.Contains(executionPrompt, "explicitly approved") {
		t.Fatalf("execution prompt=%q", executionPrompt)
	}
	if _, ok := runtime.Registry.Get(goalgraph.ReviseToolName); !ok {
		t.Fatal("approved preview did not expose bounded graph revision control")
	}
	if err := runtime.NewSession(); err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("active graph allowed a session reset: %v", err)
	}
}

func TestOrchestratedSpecPreservesScopedIsolatedWriterIntent(t *testing.T) {
	proposal := &plan.Plan{Goal: "update independent docs", Steps: []plan.Step{
		{ID: 1, Title: "write user guide", Status: "pending", Execution: "isolated_write", WritePaths: []string{"docs/USER_GUIDE.md"}, Acceptance: []string{"guide check passes"}},
		{ID: 2, Title: "write security guide", Status: "pending", Execution: "isolated_write", WritePaths: []string{"docs/SECURITY.md"}, Acceptance: []string{"security check passes"}},
	}}
	spec, err := orchestratedSpec(proposal)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Nodes[0].Execution != goalgraph.ExecutionIsolatedWrite || len(spec.Nodes[0].WritePaths) != 1 || spec.Nodes[0].WritePaths[0] != "docs/USER_GUIDE.md" {
		t.Fatalf("spec=%+v", spec)
	}
	proposal.Steps[0].WritePaths[0] = "mutated"
	if spec.Nodes[0].WritePaths[0] != "docs/USER_GUIDE.md" {
		t.Fatal("orchestrated spec aliased the mutable plan write scope")
	}
}

func TestOrchestratedSpecRejectsImpossibleEndToEndWriterGraph(t *testing.T) {
	proposal := &plan.Plan{Goal: "build a Kanban application", Steps: []plan.Step{
		{ID: 1, Title: "scaffold", Status: "pending", Execution: "primary", Acceptance: []string{"dependencies import"}},
		{ID: 2, Title: "implement backend", Status: "pending", Execution: "isolated_write", WritePaths: []string{"app/"}, DependsOn: []int{1}, Acceptance: []string{"API tests pass"}},
		{ID: 3, Title: "integrate", Status: "pending", Execution: "primary", DependsOn: []int{2}, Acceptance: []string{"end-to-end test passes"}},
	}}
	if _, err := orchestratedSpec(proposal); err == nil || !strings.Contains(err.Error(), "use primary nodes for an end-to-end goal") {
		t.Fatalf("impossible proposal error=%v", err)
	}
}

func TestOrchestratedApprovalPreflightsDirtyCandidateBase(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	runGitTest(t, workspace, "init")
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, workspace, "add", "README.md")
	runGitTest(t, workspace, "commit", "-m", "base")
	runtime, err := New(t.Context(), Options{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err := runtime.BeginOrchestratedProposal("produce a docs candidate for review"); err != nil {
		t.Fatal(err)
	}
	proposal := plan.Plan{Goal: "produce a docs candidate", Steps: []plan.Step{{ID: 1, Title: "write docs candidate", Status: "pending", Execution: "isolated_write", WritePaths: []string{"docs/"}, Acceptance: []string{"docs check passes"}}}}
	if err := runtime.Plan.Set(proposal); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "dirty.txt"), []byte("not committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.ApproveOrchestratedGoal(t.Context()); err == nil || !strings.Contains(err.Error(), "dirty (1 paths: dirty.txt)") {
		t.Fatalf("dirty candidate approval error=%v", err)
	}
	if runtime.GoalGraph != nil || runtime.OrchestratedGoalPhase() != "proposal" {
		t.Fatalf("failed preflight consumed proposal: graph=%v phase=%q", runtime.GoalGraph, runtime.OrchestratedGoalPhase())
	}
	if err := os.Remove(filepath.Join(workspace, "dirty.txt")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.ApproveOrchestratedGoal(t.Context()); err != nil {
		t.Fatalf("clean candidate approval failed after correction: %v", err)
	}
}

func TestCancelOrchestratedProposalRestoresPreviousPlanMode(t *testing.T) {
	isolateGlobalFiles(t)
	runtime, err := New(t.Context(), Options{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	for _, previous := range []bool{false, true} {
		runtime.Agent.SetPlan(previous)
		if _, err := runtime.BeginOrchestratedProposal("inspect without executing"); err != nil {
			t.Fatal(err)
		}
		if !runtime.Agent.Plan() {
			t.Fatal("proposal did not force read-only planning mode")
		}
		status, err := runtime.CancelOrchestratedGoal(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if runtime.Agent.Plan() != previous || runtime.OrchestratedGoalPhase() != "" {
			t.Fatalf("cancel did not restore plan mode %t: plan=%t phase=%q", previous, runtime.Agent.Plan(), runtime.OrchestratedGoalPhase())
		}
		if !strings.Contains(status, "cannot be approved") {
			t.Fatalf("cancel status=%q", status)
		}
	}
}

func TestSavedOrchestratedGoalRemainsInertUntilExplicitResume(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	first, err := New(t.Context(), Options{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.BeginOrchestratedProposal("inspect the repository"); err != nil {
		t.Fatal(err)
	}
	approved := plan.Plan{Goal: "inspect the repository", Steps: []plan.Step{{ID: 1, Title: "inspect", Status: "pending", Acceptance: []string{"repository facts are reported with file evidence"}}}}
	if err := first.Plan.Set(approved); err != nil {
		t.Fatal(err)
	}
	if _, _, err := first.ApproveOrchestratedGoal(t.Context()); err != nil {
		t.Fatal(err)
	}
	id := first.Session.Meta.ID
	first.Close()

	resumed, err := New(t.Context(), Options{Workspace: workspace, Resume: id})
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if resumed.GoalGraph != nil {
		t.Fatal("saved graph activated without an explicit resume action")
	}
	status, err := resumed.OrchestratedGoalStatus(0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "Saved graph is inert") {
		t.Fatalf("saved status=%q", status)
	}
	status, prompt, runnable, err := resumed.ResumeOrchestratedGoal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if resumed.GoalGraph == nil || !runnable || prompt == "" || !strings.Contains(status, "inspect the repository") {
		t.Fatalf("explicit resume did not reattach runnable graph: graph=%v runnable=%t prompt=%q status=%q", resumed.GoalGraph, runnable, prompt, status)
	}
}

func TestSavedBlockedGoalCanBeExplicitlyRetriedWithoutSeparateResume(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	approved := &plan.Plan{Goal: "repair safely", Steps: []plan.Step{{ID: 1, Title: "repair", Acceptance: []string{"repository evidence is recorded"}}}}
	first, err := New(t.Context(), Options{Workspace: workspace, OrchestratedGoal: approved})
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := first.GoalGraph.StartNext(t.Context(), "state")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.GoalGraph.BlockActive(t.Context(), attempt.ID, "bounded remediation stopped"); err != nil {
		t.Fatal(err)
	}
	id := first.Session.Meta.ID
	first.Close()

	restored, err := New(t.Context(), Options{Workspace: workspace, Resume: id})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.GoalGraph != nil {
		t.Fatal("saved blocked graph activated without an explicit user action")
	}
	status, prompt, runnable, err := restored.RetryOrchestratedNode(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if restored.GoalGraph == nil || !runnable || prompt == "" || !strings.Contains(status, "repair · ready") {
		t.Fatalf("direct saved retry graph=%v runnable=%t prompt=%q status=%q", restored.GoalGraph, runnable, prompt, status)
	}
	snapshot := restored.GoalGraph.Snapshot()
	if snapshot.Outcome != "" || len(snapshot.Attempts) != 1 || snapshot.Attempts[0].State != goalgraph.AttemptBlocked || snapshot.Nodes[0].State != goalgraph.NodeReady {
		t.Fatalf("direct saved retry did not preserve the blocker and reopen the node: %+v", snapshot)
	}
}

func TestOrchestratedGoalPauseResumeAndSafeRetryControls(t *testing.T) {
	isolateGlobalFiles(t)
	approved := &plan.Plan{Goal: "inspect safely", Steps: []plan.Step{{ID: 1, Title: "inspect", Acceptance: []string{"repository evidence is recorded"}}}}
	runtime, err := New(t.Context(), Options{Workspace: t.TempDir(), OrchestratedGoal: approved})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	status, err := runtime.PauseOrchestratedGoal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.OrchestratedGoalPhase() != "paused" || !strings.Contains(status, "Scheduling: paused") {
		t.Fatalf("phase=%q status=%q", runtime.OrchestratedGoalPhase(), status)
	}
	status, prompt, runnable, err := runtime.ResumeOrchestratedGoal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !runnable || runtime.OrchestratedGoalPhase() != "running" || !strings.Contains(prompt, "explicitly resumed") || !strings.Contains(status, "Scheduling: active") {
		t.Fatalf("runnable=%t phase=%q prompt=%q status=%q", runnable, runtime.OrchestratedGoalPhase(), prompt, status)
	}

	_, attempt, err := runtime.GoalGraph.StartNext(t.Context(), "state")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.GoalGraph.RecordFailure(t.Context(), attempt.ID, goalgraph.Failure{Kind: goalgraph.FailurePermission, Tool: "read_file", Risk: "read", Detail: "permission denied"}); err != nil {
		t.Fatal(err)
	}
	decision, err := runtime.GoalGraph.ProposeCompletion(t.Context(), "blocked", "state")
	if err != nil || decision.Kind != goalgraph.DecisionBlocked {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	status, prompt, runnable, err = runtime.RetryOrchestratedNode(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if !runnable || runtime.OrchestratedGoalPhase() != "running" || !strings.Contains(prompt, "safe bounded retry") || !strings.Contains(status, "attempt 1 · blocked") {
		t.Fatalf("runnable=%t phase=%q prompt=%q status=%q", runnable, runtime.OrchestratedGoalPhase(), prompt, status)
	}
	_, second, err := runtime.GoalGraph.StartNext(t.Context(), "state")
	if err != nil || second.Number != 2 {
		t.Fatalf("second attempt=%+v err=%v", second, err)
	}
}

func TestSavedPausedGoalRemainsInertAndExplicitResumeClearsPause(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	approved := &plan.Plan{Goal: "inspect safely", Steps: []plan.Step{{ID: 1, Title: "inspect", Acceptance: []string{"repository evidence is recorded"}}}}
	first, err := New(t.Context(), Options{Workspace: workspace, OrchestratedGoal: approved})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.PauseOrchestratedGoal(t.Context()); err != nil {
		t.Fatal(err)
	}
	id := first.Session.Meta.ID
	first.Close()

	restored, err := New(t.Context(), Options{Workspace: workspace, Resume: id})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.GoalGraph != nil {
		t.Fatal("paused saved graph activated without explicit resume")
	}
	status, err := restored.OrchestratedGoalStatus(0)
	if err != nil || !strings.Contains(status, "Scheduling: paused") || !strings.Contains(status, "Saved graph is inert") {
		t.Fatalf("saved paused status=%q err=%v", status, err)
	}
	status, _, runnable, err := restored.ResumeOrchestratedGoal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !runnable || restored.OrchestratedGoalPhase() != "running" || !strings.Contains(status, "Scheduling: active") {
		t.Fatalf("runnable=%t phase=%q status=%q", runnable, restored.OrchestratedGoalPhase(), status)
	}
}

func TestRuntimeAcceptsVerifiedTransientCredentialWithoutPersistingIt(t *testing.T) {
	home := isolateGlobalFiles(t)
	writeGlobalConfig(t, home, `{"default_provider":"hosted","providers":{"hosted":{"type":"openai","base_url":"https://api.example.invalid/v1","api_key_env":"HOSTED_API_KEY","model":"m"}}}`)
	t.Setenv("HOSTED_API_KEY", "")
	runtime, err := New(context.Background(), Options{Workspace: t.TempDir(), Ephemeral: true, ProviderCredential: "verified-on-setup"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if got := runtime.Config.Providers["hosted"].APIKey; got != "verified-on-setup" {
		t.Fatalf("runtime credential=%q", got)
	}
	data, err := os.ReadFile(filepath.Join(home, ".collomia", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "verified-on-setup") {
		t.Fatal("transient setup credential was persisted")
	}
}

func TestRuntimeCloseCancelsActiveDelegates(t *testing.T) {
	isolateGlobalFiles(t)
	runtime, err := New(context.Background(), Options{Workspace: t.TempDir(), Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime.Team.Enqueue(agent.DelegateStart{ID: "active", Name: "worker", Task: "wait", Cancel: cancel})
	runtime.Close()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("runtime close did not cancel active delegated work")
	}
}

func TestRuntimeCloseWaitsForBackgroundProcesses(t *testing.T) {
	home := isolateGlobalFiles(t)
	// The subject here is Close's process bookkeeping, not OS containment: a
	// sandboxed background process would pull the platform backend (AppContainer
	// profiles and temp-directory ACL grants on Windows) into a lifecycle test.
	// Containment and its teardown are covered by internal/sandbox.
	writeGlobalConfig(t, home, `{"default_provider":"ollama","default_model":"qwen3-coder","providers":{"ollama":{"type":"openai-compatible","base_url":"http://127.0.0.1:11434/v1","model":"qwen3-coder","context_window":32768,"max_tokens":8192}},"permissions":{"sandbox":"off"}}`)
	runtime, err := New(context.Background(), Options{Workspace: t.TempDir(), Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Config.Permissions.Sandbox != "off" {
		runtime.Close()
		t.Fatalf("sandbox=%q; the test's own configuration was not applied", runtime.Config.Permissions.Sandbox)
	}
	command := "sleep 60"
	if goruntime.GOOS == "windows" {
		command = "ping -n 60 127.0.0.1"
	}
	args, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Registry.Execute(t.Context(), "start_process", args); err != nil {
		runtime.Close()
		t.Fatal(err)
	}
	if runtime.Processes.Running() != 1 {
		runtime.Close()
		t.Fatalf("running=%d", runtime.Processes.Running())
	}
	runtime.Close()
	if running := runtime.Processes.Running(); running != 0 {
		t.Fatalf("runtime close returned with %d background processes still running", running)
	}
}

func BenchmarkRuntimeStartup(b *testing.B) {
	home := b.TempDir()
	b.Setenv("HOME", home)
	b.Setenv("USERPROFILE", home)
	workspace := b.TempDir()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runtime, err := New(context.Background(), Options{Workspace: workspace, Ephemeral: true})
		if err != nil {
			b.Fatal(err)
		}
		runtime.Close()
	}
}

func TestDurableRuntimeEnablesBoundedResultArtifacts(t *testing.T) {
	isolateGlobalFiles(t)
	runtime, err := New(context.Background(), Options{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, ok := runtime.Registry.Get("read_tool_result"); !ok {
		t.Fatal("durable runtime did not expose artifact reader")
	}
	item, ok := runtime.Registry.Get("run_command")
	if !ok {
		t.Fatal("run_command missing")
	}
	command, ok := item.(*tools.RunCommandTool)
	if !ok {
		t.Fatalf("run_command type=%T", item)
	}
	if command.StreamOutputBytes != runtime.Config.Options.MaxToolOutputBytes || command.MaxOutputBytes != session.ArtifactResultLimit+1 {
		t.Fatalf("command capture=%d stream=%d config=%d", command.MaxOutputBytes, command.StreamOutputBytes, runtime.Config.Options.MaxToolOutputBytes)
	}
}

func TestSelectAgentTightensPermissionsAndPreservesUsage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configDir := filepath.Join(home, ".collomia")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configJSON := `{
	  "schema_version": 1,
	  "default_provider": "ollama",
	  "default_model": "base-model",
	  "providers": {
	    "ollama": {
	      "type": "openai-compatible",
	      "base_url": "http://127.0.0.1:11434/v1",
	      "model": "base-model",
	      "max_tokens": 1024,
	      "pricing": {
	        "input_per_million": 1,
	        "output_per_million": 2
	      }
	    }
	  },
	  "permissions": {
	    "mode": "autopilot"
	  },
	  "agents": {
	    "builder": {
	      "availability": "primary",
	      "model": "builder-model",
	      "reasoning": {"effort": "high"},
	      "tools": ["read_file"],
	      "max_iterations": 4,
	      "token_budget": 2000,
	      "cost_budget_usd": 0.25,
	      "permissions": {
	        "mode": "ask",
	        "denied_tools": ["run_command"]
	      }
	    }
	  }
	}`
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	runtime, err := New(context.Background(), Options{Workspace: t.TempDir(), Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.Agent.SetUsage(provider.Usage{InputTokens: 30, OutputTokens: 10, CostUSD: 0.00005, CostAvailable: true, CostEstimated: true})

	if err := runtime.SelectAgent("builder"); err != nil {
		t.Fatal(err)
	}
	if runtime.ActiveAgent != "builder" {
		t.Fatalf("active agent=%q", runtime.ActiveAgent)
	}
	if providerName, model := runtime.Agent.Selection(); providerName != "ollama" || model != "builder-model" {
		t.Fatalf("selection=%s/%s", providerName, model)
	}
	if profile, reasoning, tokens, cost := runtime.Agent.Profile(); profile != "builder" || reasoning != "high" || tokens != 2000 || cost != 0.25 {
		t.Fatalf("profile=%q reasoning=%q tokens=%d cost=%v", profile, reasoning, tokens, cost)
	}
	if runtime.Permissions.Mode() != "ask" {
		t.Fatalf("profile widened or ignored permission mode: %s", runtime.Permissions.Mode())
	}
	if grant, decision := runtime.Permissions.Evaluate("run_command", tools.Action{Risk: tools.RiskRead}); decision != "deny" || grant.Source != "denied-tool" {
		t.Fatalf("profile denied tool decision=%q grant=%+v", decision, grant)
	}
	if usage := runtime.Agent.Usage(); usage.InputTokens != 30 || usage.OutputTokens != 10 || !usage.CostAvailable {
		t.Fatalf("profile switch reset usage: %+v", usage)
	}

	if err := runtime.SelectAgent("default"); err != nil {
		t.Fatal(err)
	}
	if runtime.ActiveAgent != "" || runtime.Permissions.Mode() != "autopilot" {
		t.Fatalf("default profile was not restored: active=%q mode=%s", runtime.ActiveAgent, runtime.Permissions.Mode())
	}
	if _, model := runtime.Agent.Selection(); model != "base-model" {
		t.Fatalf("default model=%q", model)
	}
	if usage := runtime.Agent.Usage(); usage.InputTokens != 30 || usage.OutputTokens != 10 || !usage.CostAvailable {
		t.Fatalf("restoring default reset usage: %+v", usage)
	}
}

func TestResumeRestoresDelegatedOutcomesAndMarksActiveWorkInterrupted(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	runtime, err := New(context.Background(), Options{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	id := runtime.Session.Meta.ID
	runtime.Team.Start("done", "review", "review code", false)
	runtime.Team.Finish("done", "all clear", []string{"main.go"}, "", "", nil)
	runtime.Team.Start("active", "tests", "run tests", false)
	runtime.Close()

	resumed, err := New(context.Background(), Options{Workspace: workspace, Resume: id})
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	statuses := resumed.Team.Snapshot()
	if len(statuses) != 2 || statuses[0].Status != "done" || statuses[0].Summary != "all clear" || statuses[1].Status != "interrupted" {
		t.Fatalf("restored delegated agents=%+v", statuses)
	}
	if resumed.Team.Active() != 0 {
		t.Fatal("resume must not restart recorded delegated work")
	}
}

func (s *scriptedClient) Name() string { return "scripted" }
func (s *scriptedClient) Chat(_ context.Context, request provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
	s.requests = append(s.requests, request)
	step := s.steps[min(s.calls, len(s.steps)-1)]
	s.calls++
	if step.Content != "" && onDelta != nil {
		onDelta(provider.Delta{Text: step.Content})
	}
	return step, nil
}

// TestEndToEndRunIsFullyRepresentedByEventSchema drives a real Runtime —
// registry, permission pipeline, audit, agent loop — through a tool-using
// turn and verifies the emitted JSONL event stream carries the whole run in
// schema v1.
func TestEndToEndRunIsFullyRepresentedByEventSchema(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "hello.txt"), []byte("hello fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime, err := New(context.Background(), Options{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	client := &scriptedClient{steps: []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "1", Name: "read_file", Arguments: json.RawMessage(`{"path":"hello.txt"}`)}}},
		{Content: "The file says hello.", Usage: provider.Usage{InputTokens: 10, OutputTokens: 5}},
	}}
	runtime.Agent.SetProvider("scripted", "fixture-model", appconfig.Provider{MaxTokens: 100}, client)

	var out strings.Builder
	writer := event.NewJSONLWriter(&out)
	final, err := runtime.Agent.Run(context.Background(), "what does hello.txt say?", writer.Handle)
	if err != nil {
		t.Fatal(err)
	}
	if final != "The file says hello." {
		t.Fatalf("final=%q", final)
	}

	var kinds []event.Kind
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var e event.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("event line is not valid JSON: %q: %v", line, err)
		}
		if e.Schema != event.SchemaVersion {
			t.Fatalf("event missing schema version: %q", line)
		}
		if e.Time.IsZero() {
			t.Fatalf("event missing timestamp: %q", line)
		}
		kinds = append(kinds, e.Kind)
	}
	wantOrder := []event.Kind{event.KindTurnStart, event.KindPermissionDecision, event.KindToolStart, event.KindToolResult, event.KindTextDelta, event.KindUsage, event.KindTurnEnd}
	pos := 0
	for _, kind := range kinds {
		if pos < len(wantOrder) && kind == wantOrder[pos] {
			pos++
		}
	}
	if pos != len(wantOrder) {
		t.Fatalf("event stream missing %v (in order); got %v", wantOrder[pos], kinds)
	}
}

// TestSessionResumeAcrossRestart kills a runtime after a completed turn and
// verifies a new runtime resumes the same conversation from disk.
func TestSessionResumeAcrossRestart(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	first, err := New(context.Background(), Options{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	client := &scriptedClient{steps: []provider.Response{{Content: "the answer is 42"}}}
	first.Agent.SetProvider("scripted", "fixture", appconfig.Provider{MaxTokens: 100}, client)
	if _, err := first.Agent.Run(context.Background(), "what is the answer?", nil); err != nil {
		t.Fatal(err)
	}
	id := first.Session.Meta.ID
	first.Close()

	resumed, err := New(context.Background(), Options{Workspace: workspace, Resume: id})
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if resumed.Agent.MessageCount() != 2 {
		t.Fatalf("resumed messages=%d", resumed.Agent.MessageCount())
	}
	// The resumed conversation is live: a follow-up turn sees the history.
	follow := &scriptedClient{steps: []provider.Response{{Content: "still 42"}}}
	resumed.Agent.SetProvider("scripted", "fixture", appconfig.Provider{MaxTokens: 100}, follow)
	if _, err := resumed.Agent.Run(context.Background(), "and again?", nil); err != nil {
		t.Fatal(err)
	}
	if resumed.Agent.MessageCount() != 4 {
		t.Fatalf("post-follow-up messages=%d", resumed.Agent.MessageCount())
	}

	// --continue picks the same session.
	resumed.Close()
	continued, err := New(context.Background(), Options{Workspace: workspace, Continue: true})
	if err != nil {
		t.Fatal(err)
	}
	defer continued.Close()
	if continued.Session.Meta.ID != id {
		t.Fatalf("continue resumed %s, want %s", continued.Session.Meta.ID, id)
	}
}

func TestOrchestratedGoalRestoresAmbiguousMutationAsBlocker(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	approved := &plan.Plan{Goal: "change safely", Steps: []plan.Step{{ID: 1, Title: "change source"}}}
	first, err := New(t.Context(), Options{Workspace: workspace, OrchestratedGoal: approved})
	if err != nil {
		t.Fatal(err)
	}
	if first.GoalGraph == nil || first.Session == nil {
		t.Fatal("internal orchestrated runtime did not create a durable graph")
	}
	_, attempt, err := first.GoalGraph.StartNext(t.Context(), "workspace-before")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.GoalGraph.BeginTool(t.Context(), attempt.ID, goalgraph.ToolAction{
		Tool: "write_file", Risk: string(tools.RiskWrite), Summary: "change source",
		PotentialMutation: true, NonReplayable: true,
	}, "workspace-before"); err != nil {
		t.Fatal(err)
	}
	id := first.Session.Meta.ID
	first.Close()
	standard, err := New(t.Context(), Options{Workspace: workspace, Resume: id})
	if err != nil {
		t.Fatal(err)
	}
	if standard.GoalGraph != nil {
		t.Fatal("persisted graph data activated orchestration without the programmatic option")
	}
	if _, exists := standard.Registry.Get(goalgraph.ReviseToolName); exists {
		t.Fatal("standard resume exposed internal graph-control tools")
	}
	standard.Close()

	resumed, err := New(t.Context(), Options{Workspace: workspace, Resume: id, OrchestratedGoal: approved})
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if outcome, reason := resumed.GoalGraph.Outcome(); outcome != goalgraph.OutcomeBlocked || !strings.Contains(reason, "may have taken effect") {
		t.Fatalf("outcome=%q reason=%q", outcome, reason)
	}
	restored := resumed.GoalGraph.Snapshot()
	if len(restored.Attempts) != 1 || restored.Attempts[0].State != goalgraph.AttemptInterrupted {
		t.Fatalf("restored attempts=%+v", restored.Attempts)
	}
	if len(resumed.Session.GoalGraphRaw) == 0 {
		t.Fatal("recovered graph was not persisted back to the session")
	}
	if err := resumed.NewSession(); err != nil {
		t.Fatalf("terminal graph should allow an explicit fresh session: %v", err)
	}
	if resumed.GoalGraph != nil {
		t.Fatal("new session retained the prior terminal graph")
	}
}

// TestAskUserToolPausesForAnswer verifies the user-question primitive: the
// run pauses for a typed answer and continues without corrupting the turn.
func TestAskUserToolPausesForAnswer(t *testing.T) {
	isolateGlobalFiles(t)
	asked := ""
	runtime, err := New(context.Background(), Options{Workspace: t.TempDir(), Asker: func(_ context.Context, question string, options []string) (string, error) {
		asked = question
		return "use PostgreSQL", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	client := &scriptedClient{steps: []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "q1", Name: "ask_user", Arguments: json.RawMessage(`{"question":"Which database?","options":["PostgreSQL","SQLite"]}`)}}},
		{Content: "Using PostgreSQL."},
	}}
	runtime.Agent.SetProvider("scripted", "fixture", appconfig.Provider{MaxTokens: 100}, client)
	final, err := runtime.Agent.Run(context.Background(), "set up the db layer", nil)
	if err != nil {
		t.Fatal(err)
	}
	if asked != "Which database?" || final != "Using PostgreSQL." {
		t.Fatalf("asked=%q final=%q", asked, final)
	}
}

// TestPlanPersistsAcrossResume verifies the structured plan artifact
// survives a restart with the session.
func TestPlanPersistsAcrossResume(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	first, err := New(context.Background(), Options{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	client := &scriptedClient{steps: []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "p1", Name: "update_plan", Arguments: json.RawMessage(`{"goal":"ship it","steps":[{"id":1,"title":"build","status":"done","evidence":"go build"}]}`)}}},
		{Content: "planned"},
	}}
	first.Agent.SetProvider("scripted", "fixture", appconfig.Provider{MaxTokens: 100}, client)
	if _, err := first.Agent.Run(context.Background(), "plan the work", nil); err != nil {
		t.Fatal(err)
	}
	id := first.Session.Meta.ID
	first.Close()

	resumed, err := New(context.Background(), Options{Workspace: workspace, Resume: id})
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	current := resumed.Plan.Current()
	if current == nil || current.Goal != "ship it" || len(current.Steps) != 1 {
		t.Fatalf("plan not restored: %+v", current)
	}
	follow := &scriptedClient{steps: []provider.Response{{Content: "continued"}}}
	resumed.Agent.SetProvider("scripted", "fixture", appconfig.Provider{MaxTokens: 100}, follow)
	if _, err := resumed.Agent.Run(context.Background(), "continue the plan", nil); err != nil {
		t.Fatal(err)
	}
	// The restored plan rides the trailing message, not the system prompt,
	// so that the cached request prefix survives every update_plan call.
	if len(follow.requests) != 1 {
		t.Fatalf("expected one follow-up request, got %d", len(follow.requests))
	}
	messages := follow.requests[0].Messages
	trailing := messages[len(messages)-1].Content
	if !strings.Contains(trailing, "Active structured plan") || !strings.Contains(trailing, "ship it") || !strings.Contains(trailing, "go build") {
		t.Fatalf("restored plan was not pinned in the next request: %q", trailing)
	}
	if strings.Contains(follow.requests[0].System, "Active structured plan") {
		t.Fatalf("plan leaked into the system prompt: %s", follow.requests[0].System)
	}
}

// TestRuntimeQuarantinesUntrustedProject verifies the wiring end to end:
// an untrusted workspace's project config must not reach the runtime.
func TestRuntimeQuarantinesUntrustedProject(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	project := `{"permissions":{"mode":"autopilot"}}`
	if err := os.WriteFile(filepath.Join(workspace, appconfig.ProjectFile), []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := New(context.Background(), Options{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if runtime.Permissions.Mode() == "autopilot" {
		t.Fatal("untrusted project config must not set the autonomy mode")
	}
	warned := false
	for _, w := range runtime.Warnings {
		if strings.Contains(w.Error(), "not trusted") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("quarantine warning missing: %v", runtime.Warnings)
	}
}

func TestProviderInspectionCombinesCapabilitiesAndAvailability(t *testing.T) {
	isolateGlobalFiles(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path=%q", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"live-model"}]}`)
	}))
	defer server.Close()

	runtime, err := New(context.Background(), Options{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.Config.Providers = map[string]appconfig.Provider{
		"live": {Type: "openai-compatible", BaseURL: server.URL, Model: "live-model", Context: 64_000},
		"aws":  {Type: "bedrock", Model: "bedrock-model", Region: "us-east-1", Context: 128_000},
	}
	runtime.Config.DefaultProvider = "live"

	statuses := runtime.InspectProviders(t.Context())
	if len(statuses) != 2 || statuses[0].Name != "aws" || statuses[1].Name != "live" {
		t.Fatalf("statuses=%+v", statuses)
	}
	if statuses[0].Availability != ProviderUnverified || statuses[0].Capabilities.Streaming != provider.CapabilitySupported {
		t.Fatalf("aws=%+v", statuses[0])
	}
	if statuses[1].Availability != ProviderAvailable || len(statuses[1].Models) != 1 || statuses[1].Models[0].Capabilities.ContextWindow != 64_000 {
		t.Fatalf("live=%+v", statuses[1])
	}
}

func TestNewRedactorIncludesStandardBedrockBearerToken(t *testing.T) {
	t.Setenv(provider.BedrockBearerTokenEnv, "bedrock-bearer-token-secret")
	cfg := appconfig.Defaults()
	cfg.Providers["bedrock"] = appconfig.Provider{Type: "bedrock", Model: "model"}
	redactor := NewRedactor(cfg)
	if got := redactor.Redact("Authorization: Bearer bedrock-bearer-token-secret"); strings.Contains(got, "bedrock-bearer-token-secret") {
		t.Fatalf("token was not redacted: %q", got)
	}
}

func TestNewRedactorIncludesAzureEnvironmentCredentials(t *testing.T) {
	t.Setenv("AZURE_CLIENT_SECRET", "azure-client-secret-value")
	t.Setenv("AZURE_CLIENT_CERTIFICATE_PASSWORD", "azure-certificate-password")
	cfg := appconfig.Defaults()
	cfg.Providers["azure"] = appconfig.Provider{Type: "azure-foundry", Auth: "entra", BaseURL: "https://example.services.ai.azure.com", Model: "model"}
	redactor := NewRedactor(cfg)
	got := redactor.Redact("client=azure-client-secret-value certificate=azure-certificate-password")
	if strings.Contains(got, "azure-client-secret-value") || strings.Contains(got, "azure-certificate-password") {
		t.Fatalf("Azure environment credential was not redacted: %q", got)
	}
}
