package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/robert-mcdermott/collomia/internal/agent"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/diffmodel"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/session"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

type integrationFixture struct {
	runtime       *Runtime
	workspace     string
	worktree      string
	branch        string
	base          string
	parentFile    string
	delegatedFile string
}

func newIntegrationFixture(t *testing.T, approver permission.Approver) integrationFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	// Reproduce the Windows Actions default on every platform so the fixture's
	// repository-local setting below is covered rather than merely documented.
	globalGitConfig := filepath.Join(t.TempDir(), "global.gitconfig")
	if err := os.WriteFile(globalGitConfig, []byte("[core]\n\tautocrlf = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", globalGitConfig)
	workspace := t.TempDir()
	runGitTest(t, workspace, "init", "-b", "main")
	// The fixture compares parent and linked-worktree bytes. Do not inherit a
	// developer or CI runner's global autocrlf setting, which can leave the
	// freshly written parent as LF while converting the worktree checkout to
	// CRLF and turn two edits into an unrelated whole-file replacement.
	runGitTest(t, workspace, "config", "core.autocrlf", "false")
	lines := make([]string, 14)
	for i := range lines {
		lines[i] = "line " + string(rune('A'+i))
	}
	content := strings.Join(lines, "\n") + "\n"
	parentFile := filepath.Join(workspace, "sample.txt")
	if err := os.WriteFile(parentFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.com/delegatefixture\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "fixture.go"), []byte("package delegatefixture\n\nfunc Fixture() string { return \"ok\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".gitignore"), []byte(".verify-started\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, workspace, "add", "sample.txt", "go.mod", "fixture.go", ".gitignore")
	runGitTest(t, workspace, "commit", "-m", "base")
	base := strings.TrimSpace(string(runGitOutputTest(t, workspace, "rev-parse", "HEAD")))
	worktree := filepath.Join(t.TempDir(), "child")
	branch := "collomia/integration-test"
	runGitTest(t, workspace, "worktree", "add", "-b", branch, worktree, "HEAD")
	delegatedFile := filepath.Join(worktree, "sample.txt")
	parentBytes, parentErr := os.ReadFile(parentFile)
	delegatedBytes, delegatedErr := os.ReadFile(delegatedFile)
	if parentErr != nil || delegatedErr != nil || !bytes.Equal(parentBytes, delegatedBytes) {
		t.Fatalf("integration fixture parent/worktree bytes differ: parent_err=%v delegated_err=%v", parentErr, delegatedErr)
	}
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", workspace, "worktree", "remove", "--force", worktree).Run()
		_ = exec.Command("git", "-C", workspace, "branch", "-D", branch).Run()
	})

	team := agent.NewTeam()
	team.Enqueue(agent.DelegateStart{ID: "d1", Name: "writer", Task: "update sample", Write: true})
	team.FinishDetailed("d1", "updated sample", []string{"verified"}, []string{"sample.txt"}, worktree, branch, base, provider.Usage{}, nil)
	mode := "workspace"
	if approver != nil {
		mode = "ask"
	}
	runtime := &Runtime{
		Workspace:   workspace,
		Team:        team,
		Permissions: permission.New(appconfig.Permissions{Mode: mode}, approver),
		Changes:     diffmodel.NewTracker(workspace),
	}
	return integrationFixture{runtime: runtime, workspace: workspace, worktree: worktree, branch: branch, base: base, parentFile: parentFile, delegatedFile: delegatedFile}
}

func TestDelegateIntegrationAppliesSelectedHunksAndRetainsWorktree(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)
	data, err := os.ReadFile(fixture.delegatedFile)
	if err != nil {
		t.Fatal(err)
	}
	child := strings.Replace(string(data), "line B", "line B from child", 1)
	child = strings.Replace(child, "line L", "line L from child", 1)
	if err := os.WriteFile(fixture.delegatedFile, []byte(child), 0o644); err != nil {
		t.Fatal(err)
	}
	preview, err := fixture.runtime.PrepareDelegateIntegration(t.Context(), "d1")
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
	applied, err := fixture.runtime.ApplyDelegateIntegration(t.Context(), "d1", []DelegateIntegrationSelection{{Path: "sample.txt", Keep: []bool{true, false}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 || applied[0] != "sample.txt" {
		t.Fatalf("applied=%v", applied)
	}
	parent, err := os.ReadFile(fixture.parentFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(parent), "line B from child") || strings.Contains(string(parent), "line L from child") {
		t.Fatalf("selective integration result:\n%s", parent)
	}
	if _, err := os.Stat(fixture.worktree); err != nil {
		t.Fatalf("worktree was removed: %v", err)
	}
	status, _ := fixture.runtime.Team.Get("d1")
	if len(status.Integrated) != 1 || status.Integrated[0] != "sample.txt" || status.IntegrationStatus != "partial" {
		t.Fatalf("integrated status=%+v", status)
	}
	if changed := fixture.runtime.Changes.Changed(); len(changed) != 1 || changed[0] != fixture.parentFile {
		t.Fatalf("tracker changes=%v", changed)
	}
}

func TestDelegateIntegrationRefusesParentDrift(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)
	if err := os.WriteFile(fixture.delegatedFile, []byte("child replacement\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.parentFile, []byte("user replacement\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	preview, err := fixture.runtime.PrepareDelegateIntegration(t.Context(), "d1")
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Files) != 1 || !strings.Contains(preview.Files[0].Conflict, "overlap") || preview.Files[0].ConflictPreview == "" {
		t.Fatalf("expected stale conflict, got %+v", preview.Files)
	}
}

func TestDelegateIntegrationThreeWayReconcilesDisjointParentAndChildEdits(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)
	parentData, err := os.ReadFile(fixture.parentFile)
	if err != nil {
		t.Fatal(err)
	}
	childData, err := os.ReadFile(fixture.delegatedFile)
	if err != nil {
		t.Fatal(err)
	}
	parent := strings.Replace(string(parentData), "line L", "line L from parent", 1)
	child := strings.Replace(string(childData), "line B", "line B from child", 1)
	if err := os.WriteFile(fixture.parentFile, []byte(parent), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(fixture.parentFile, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(fixture.delegatedFile, []byte(child), 0o644); err != nil {
		t.Fatal(err)
	}
	preview, err := fixture.runtime.PrepareDelegateIntegration(t.Context(), "d1")
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Files) != 1 || preview.Files[0].Conflict != "" || !preview.Files[0].Reconciled || preview.Files[0].ReconciledContent == nil {
		t.Fatalf("three-way preview=%+v", preview.Files)
	}
	if !strings.Contains(*preview.Files[0].ReconciledContent, "line B from child") || !strings.Contains(*preview.Files[0].ReconciledContent, "line L from parent") {
		t.Fatalf("reconciled content:\n%s", *preview.Files[0].ReconciledContent)
	}
	hunks, err := diffmodel.ParseHunks(preview.Files[0].Unified)
	if err != nil {
		t.Fatal(err)
	}
	keep := make([]bool, len(hunks))
	for i := range keep {
		keep[i] = true
	}
	if _, err := fixture.runtime.ApplyDelegateIntegration(t.Context(), "d1", []DelegateIntegrationSelection{{Path: "sample.txt", Keep: keep}}); err != nil {
		t.Fatal(err)
	}
	merged, err := os.ReadFile(fixture.parentFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(merged), "line B from child") || !strings.Contains(string(merged), "line L from parent") {
		t.Fatalf("published reconciliation:\n%s", merged)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(fixture.parentFile)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("three-way integration changed parent permissions to %o", info.Mode().Perm())
		}
	}
	status, _ := fixture.runtime.Team.Get("d1")
	if status.IntegrationStatus != "integrated" {
		t.Fatalf("integration status=%+v", status)
	}
}

func TestDelegateIntegrationBlocksScopeViolations(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)
	fixture.runtime.Team.MarkScopeViolations("d1", []string{"outside.txt"})
	if _, err := fixture.runtime.PrepareDelegateIntegration(t.Context(), "d1"); err == nil || !strings.Contains(err.Error(), "outside its declared") {
		t.Fatalf("scope violation should block automatic integration: %v", err)
	}
}

func TestDelegateIntegrationRechecksAfterApproval(t *testing.T) {
	var parentPath string
	approver := func(_ context.Context, _ permission.Request) (permission.Decision, error) {
		if err := os.WriteFile(parentPath, []byte("user changed during approval\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return permission.Decision{Allow: true}, nil
	}
	fixture := newIntegrationFixture(t, approver)
	parentPath = fixture.parentFile
	if err := os.WriteFile(fixture.delegatedFile, []byte("child replacement\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	preview, err := fixture.runtime.PrepareDelegateIntegration(t.Context(), "d1")
	if err != nil {
		t.Fatal(err)
	}
	hunks, err := diffmodel.ParseHunks(preview.Files[0].Unified)
	if err != nil {
		t.Fatal(err)
	}
	keep := make([]bool, len(hunks))
	for i := range keep {
		keep[i] = true
	}
	_, err = fixture.runtime.ApplyDelegateIntegration(t.Context(), "d1", []DelegateIntegrationSelection{{Path: "sample.txt", Keep: keep}})
	if err == nil || !strings.Contains(err.Error(), "changed while integration approval was pending") && !strings.Contains(err.Error(), "parent workspace changed") {
		t.Fatalf("expected approval-race refusal, got %v", err)
	}
	data, readErr := os.ReadFile(fixture.parentFile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "user changed during approval\n" {
		t.Fatalf("parent edit was overwritten: %q", data)
	}
}

func TestReviewedDelegateIntegrationRequiresInspectAndUsesOnePermissionDecision(t *testing.T) {
	approvals := 0
	approver := func(_ context.Context, request permission.Request) (permission.Decision, error) {
		approvals++
		if request.Tool != "integrate_delegate" {
			t.Fatalf("permission tool=%q", request.Tool)
		}
		if len(request.Action.Paths) != 1 || !strings.Contains(request.Action.Preview, "line B from child") {
			t.Fatalf("permission action=%+v", request.Action)
		}
		return permission.Decision{Allow: true}, nil
	}
	fixture := newIntegrationFixture(t, approver)
	data, err := os.ReadFile(fixture.delegatedFile)
	if err != nil {
		t.Fatal(err)
	}
	child := strings.Replace(string(data), "line B", "line B from child", 1)
	child = strings.Replace(child, "line L", "line L from child", 1)
	if err := os.WriteFile(fixture.delegatedFile, []byte(child), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture.runtime.Config.Options.AgentIntegration = "reviewed"
	fixture.runtime.Registry = tools.NewRegistry()
	fixture.runtime.addReviewedIntegrationTools()

	rawReview, err := fixture.runtime.Registry.Execute(t.Context(), InspectDelegateChangesTool, json.RawMessage(`{"id":"d1"}`))
	if err != nil {
		t.Fatal(err)
	}
	var review delegateReviewDocument
	if err := json.Unmarshal([]byte(rawReview), &review); err != nil {
		t.Fatal(err)
	}
	if review.ReviewToken == "" || len(review.Files) != 1 || len(review.Files[0].Hunks) != 2 {
		t.Fatalf("review=%+v", review)
	}
	status, _ := fixture.runtime.Team.Get("d1")
	if status.IntegrationStatus != "reviewed" {
		t.Fatalf("integration status=%+v", status)
	}

	apply := json.RawMessage(fmt.Sprintf(`{"id":"d1","review_token":%q,"all":true}`, review.ReviewToken))
	action, err := fixture.runtime.Registry.Assess(ApplyDelegateChangesTool, apply)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := fixture.runtime.Registry.Get(ApplyDelegateChangesTool)
	identity, identityOK := item.(tools.PermissionIdentity)
	if !ok || !identityOK || identity.PermissionToolName() != "integrate_delegate" {
		t.Fatal("reviewed apply did not preserve the integrate_delegate permission identity")
	}
	if _, err := fixture.runtime.Permissions.Authorize(t.Context(), identity.PermissionToolName(), action); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.runtime.Registry.Execute(t.Context(), ApplyDelegateChangesTool, apply)
	if err != nil {
		t.Fatal(err)
	}
	if approvals != 1 {
		t.Fatalf("permission decisions=%d want 1", approvals)
	}
	if !strings.Contains(result, `"integration_status": "integrated"`) {
		t.Fatalf("result=%s", result)
	}
	parent, err := os.ReadFile(fixture.parentFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(parent), "line B from child") || !strings.Contains(string(parent), "line L from child") {
		t.Fatalf("reviewed integration result:\n%s", parent)
	}
}

func TestReviewedDelegateIntegrationRejectsStaleReviewToken(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)
	if err := os.WriteFile(fixture.delegatedFile, []byte("first child version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture.runtime.Config.Options.AgentIntegration = "reviewed"
	fixture.runtime.Registry = tools.NewRegistry()
	fixture.runtime.addReviewedIntegrationTools()
	rawReview, err := fixture.runtime.Registry.Execute(t.Context(), InspectDelegateChangesTool, json.RawMessage(`{"id":"d1"}`))
	if err != nil {
		t.Fatal(err)
	}
	var review delegateReviewDocument
	if err := json.Unmarshal([]byte(rawReview), &review); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.delegatedFile, []byte("changed after review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	apply := json.RawMessage(fmt.Sprintf(`{"id":"d1","review_token":%q,"all":true}`, review.ReviewToken))
	if _, err := fixture.runtime.Registry.Assess(ApplyDelegateChangesTool, apply); err == nil || !strings.Contains(err.Error(), "changed after review") {
		t.Fatalf("stale review should fail before authorization: %v", err)
	}
	parent, err := os.ReadFile(fixture.parentFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(parent), "changed after review") {
		t.Fatal("stale child bytes reached parent")
	}
}

func TestDelegateVerificationUsesRunCommandPolicyAndBindsExactChildState(t *testing.T) {
	approvals := 0
	approver := func(_ context.Context, request permission.Request) (permission.Decision, error) {
		approvals++
		if request.Tool != "run_command" || !strings.Contains(request.Action.Summary, "go test ./...") {
			t.Fatalf("verification permission=%+v", request)
		}
		return permission.Decision{Allow: true}, nil
	}
	fixture := newIntegrationFixture(t, approver)
	if err := os.WriteFile(fixture.delegatedFile, []byte("verified child\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture.runtime.Config.Options.AgentIntegration = "reviewed"
	fixture.runtime.Registry = tools.NewRegistry()
	fixture.runtime.addReviewedIntegrationTools()

	rawReview, err := fixture.runtime.Registry.Execute(t.Context(), InspectDelegateChangesTool, json.RawMessage(`{"id":"d1"}`))
	if err != nil {
		t.Fatal(err)
	}
	var review delegateReviewDocument
	if err := json.Unmarshal([]byte(rawReview), &review); err != nil {
		t.Fatal(err)
	}
	if review.VerificationToken == "" || len(review.SuggestedVerification) != 3 {
		t.Fatalf("verification review=%+v", review)
	}
	raw := json.RawMessage(fmt.Sprintf(`{"id":"d1","verification_token":%q,"command":"go test ./..."}`, review.VerificationToken))
	action, err := fixture.runtime.Registry.Assess(VerifyDelegateChangesTool, raw)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := fixture.runtime.Registry.Get(VerifyDelegateChangesTool)
	identity, identityOK := item.(tools.PermissionIdentity)
	if !ok || !identityOK || identity.PermissionToolName() != "run_command" {
		t.Fatal("delegate verification did not preserve run_command permission identity")
	}
	hookIdentity, hookIdentityOK := item.(tools.HookIdentity)
	if !hookIdentityOK || hookIdentity.HookToolName() != "run_command" {
		t.Fatal("delegate verification did not preserve run_command hook identity")
	}
	if _, err := fixture.runtime.Permissions.Authorize(t.Context(), identity.PermissionToolName(), action); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.runtime.Registry.Execute(t.Context(), VerifyDelegateChangesTool, raw)
	if err != nil {
		t.Fatal(err)
	}
	if approvals != 1 || !strings.Contains(result, `"status": "passed"`) {
		t.Fatalf("approvals=%d result=%s", approvals, result)
	}
	status, _ := fixture.runtime.Team.Get("d1")
	if status.VerificationStatus != "partial" || len(status.VerificationResults) != 1 || status.VerificationResults[0].StateToken != review.VerificationToken {
		t.Fatalf("verification status=%+v", status)
	}

	if err := os.WriteFile(fixture.delegatedFile, []byte("changed after verification\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.runtime.PrepareDelegateVerification(t.Context(), "d1"); err != nil {
		t.Fatal(err)
	}
	status, _ = fixture.runtime.Team.Get("d1")
	if status.VerificationStatus != "stale" || !strings.Contains(status.VerificationError, "changed") {
		t.Fatalf("drift did not stale verification: %+v", status)
	}
}

func TestDelegateVerificationRecordsFailedCommandWithoutPublishing(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)
	if err := os.WriteFile(filepath.Join(fixture.worktree, "fixture.go"), []byte("package delegatefixture\n\nthis is invalid Go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := fixture.runtime.PrepareDelegateVerification(t.Context(), "d1")
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.runtime.ExecuteDelegateVerificationCommand(t.Context(), "d1", plan.StateToken, "go test ./...", nil)
	if err == nil || result.Status != "failed" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	status, _ := fixture.runtime.Team.Get("d1")
	if status.VerificationStatus != "failed" || len(status.VerificationResults) != 1 {
		t.Fatalf("verification status=%+v", status)
	}
	parent, readErr := os.ReadFile(filepath.Join(fixture.workspace, "fixture.go"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(parent), "invalid Go") {
		t.Fatal("verification published delegated source into the parent")
	}
}

func TestDelegateVerificationSuiteRequiresOneDecisionPerCommand(t *testing.T) {
	approvals := 0
	approver := func(_ context.Context, request permission.Request) (permission.Decision, error) {
		approvals++
		if request.Tool != "run_command" {
			t.Fatalf("permission tool=%q", request.Tool)
		}
		return permission.Decision{Allow: true}, nil
	}
	fixture := newIntegrationFixture(t, approver)
	if err := os.WriteFile(fixture.delegatedFile, []byte("suite candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	results, err := fixture.runtime.VerifyDelegateSuite(t.Context(), "d1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || approvals != 3 {
		t.Fatalf("results=%+v approvals=%d", results, approvals)
	}
	status, _ := fixture.runtime.Team.Get("d1")
	if status.VerificationStatus != "passed" || len(status.VerificationResults) != 3 {
		t.Fatalf("verification status=%+v", status)
	}
}

func TestDelegateVerificationRecordsOuterPermissionAndHookRefusals(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)
	if err := os.WriteFile(fixture.delegatedFile, []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture.runtime.Config.Options.AgentIntegration = "reviewed"
	fixture.runtime.Registry = tools.NewRegistry()
	fixture.runtime.addReviewedIntegrationTools()
	plan, err := fixture.runtime.PrepareDelegateVerification(t.Context(), "d1")
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(fmt.Sprintf(`{"id":"d1","verification_token":%q,"command":"go test ./..."}`, plan.StateToken))
	item, ok := fixture.runtime.Registry.Get(VerifyDelegateChangesTool)
	if !ok {
		t.Fatal("verification tool is missing")
	}
	authorizationObserver, ok := item.(tools.AuthorizationObserver)
	if !ok {
		t.Fatal("verification tool does not observe permission refusal")
	}
	authorizationObserver.ObserveAuthorization(raw, fmt.Errorf("%w: user declined", permission.ErrDenied))
	status, _ := fixture.runtime.Team.Get("d1")
	if status.VerificationStatus != "rejected" {
		t.Fatalf("permission refusal=%+v", status)
	}
	executionObserver, ok := item.(tools.ExecutionObserver)
	if !ok {
		t.Fatal("verification tool does not observe hook refusal")
	}
	executionObserver.ObserveExecution(raw, errors.New("blocked by verification hook"))
	status, _ = fixture.runtime.Team.Get("d1")
	if status.VerificationStatus != "blocked" || !strings.Contains(status.VerificationError, "hook") {
		t.Fatalf("hook refusal=%+v", status)
	}
}

func TestDelegateVerificationCancellationStopsCommandAndRecordsOutcome(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)
	testSource := `package delegatefixture

import (
	"os"
	"testing"
	"time"
)

func TestWaitForCancellation(t *testing.T) {
	if err := os.WriteFile(".verify-started", []byte("started"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Second)
}
`
	if err := os.WriteFile(filepath.Join(fixture.worktree, "fixture_test.go"), []byte(testSource), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := fixture.runtime.PrepareDelegateVerification(t.Context(), "d1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	type outcome struct {
		result agent.DelegateVerification
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, runErr := fixture.runtime.ExecuteDelegateVerificationCommand(ctx, "d1", plan.StateToken, "go test ./...", nil)
		done <- outcome{result: result, err: runErr}
	}()
	marker := filepath.Join(fixture.worktree, ".verify-started")
	// Windows race builds can spend more than ten seconds compiling the tiny
	// fixture while every package in ./... is instrumented concurrently. This
	// test measures cancellation after the child starts, not compiler latency.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, statErr := os.Stat(marker); statErr == nil {
			break
		}
		select {
		case early := <-done:
			cancel()
			t.Fatalf("verification command exited before starting: result=%+v err=%v", early.result, early.err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			select {
			case late := <-done:
				t.Fatalf("verification command did not start: result=%+v err=%v", late.result, late.err)
			case <-time.After(5 * time.Second):
				t.Fatal("verification command did not start or stop after cancellation")
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	got := <-done
	if got.err == nil || got.result.Status != "cancelled" {
		t.Fatalf("cancelled result=%+v err=%v", got.result, got.err)
	}
	status, _ := fixture.runtime.Team.Get("d1")
	if status.VerificationStatus != "cancelled" {
		t.Fatalf("verification status=%+v", status)
	}
}

func TestDelegateCandidateComparisonIsReadOnlyAndRefreshesVerification(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)
	if err := os.WriteFile(fixture.delegatedFile, []byte("candidate one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := fixture.runtime.PrepareDelegateVerification(t.Context(), "d1")
	if err != nil {
		t.Fatal(err)
	}
	fixture.runtime.Team.MarkVerificationResult("d1", plan.StateToken, []string{"go test ./..."}, agent.DelegateVerification{Purpose: "test", Command: "go test ./...", Status: "passed", StateToken: plan.StateToken})

	secondWorktree := filepath.Join(t.TempDir(), "child-two")
	secondBranch := "collomia/integration-two"
	runGitTest(t, fixture.workspace, "worktree", "add", "-b", secondBranch, secondWorktree, "HEAD")
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", fixture.workspace, "worktree", "remove", "--force", secondWorktree).Run()
		_ = exec.Command("git", "-C", fixture.workspace, "branch", "-D", secondBranch).Run()
	})
	if err := os.WriteFile(filepath.Join(secondWorktree, "sample.txt"), []byte("candidate two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture.runtime.Team.Enqueue(agent.DelegateStart{ID: "d2", Name: "writer-two", Task: "alternative", Write: true})
	fixture.runtime.Team.FinishDetailed("d2", "alternative", nil, []string{"sample.txt"}, secondWorktree, secondBranch, fixture.base, provider.Usage{InputTokens: 3, OutputTokens: 4}, nil)

	candidates, err := fixture.runtime.CompareDelegateCandidates(t.Context(), []string{"d1", "d2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].Readiness != "verified" || candidates[1].Readiness != "review" || candidates[1].InputTokens != 3 {
		t.Fatalf("candidates=%+v", candidates)
	}
	fixture.runtime.Config.Options.AgentIntegration = "reviewed"
	fixture.runtime.Registry = tools.NewRegistry()
	fixture.runtime.addReviewedIntegrationTools()
	rawCompare := json.RawMessage(`{"ids":["d1","d2"]}`)
	action, err := fixture.runtime.Registry.Assess(CompareDelegateChangesTool, rawCompare)
	if err != nil || action.Risk != tools.RiskRead {
		t.Fatalf("compare action=%+v err=%v", action, err)
	}
	compared, err := fixture.runtime.Registry.Execute(t.Context(), CompareDelegateChangesTool, rawCompare)
	if err != nil || !strings.Contains(compared, `"readiness": "verified"`) || !strings.Contains(compared, "grants no permission") {
		t.Fatalf("compare tool=%s err=%v", compared, err)
	}
	parent, err := os.ReadFile(fixture.parentFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(parent), "candidate one") || strings.Contains(string(parent), "candidate two") {
		t.Fatal("candidate comparison modified the parent workspace")
	}
}

func TestManualAgentIntegrationDoesNotExposePrimaryTools(t *testing.T) {
	runtime := &Runtime{Config: appconfig.Defaults(), Registry: tools.NewRegistry()}
	runtime.addReviewedIntegrationTools()
	if _, ok := runtime.Registry.Get(InspectDelegateChangesTool); ok {
		t.Fatal("manual mode exposed inspect_delegate_changes")
	}
	if _, ok := runtime.Registry.Get(ApplyDelegateChangesTool); ok {
		t.Fatal("manual mode exposed apply_delegate_changes")
	}
	if _, ok := runtime.Registry.Get(VerifyDelegateChangesTool); ok {
		t.Fatal("manual mode exposed verify_delegate_changes")
	}
	if _, ok := runtime.Registry.Get(CompareDelegateChangesTool); ok {
		t.Fatal("manual mode exposed compare_delegate_changes")
	}
}

func TestReviewedDelegateIntegrationRecordsOuterPermissionDenial(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)
	fixture.runtime.Config.Options.AgentIntegration = "reviewed"
	fixture.runtime.Registry = tools.NewRegistry()
	fixture.runtime.addReviewedIntegrationTools()
	item, ok := fixture.runtime.Registry.Get(ApplyDelegateChangesTool)
	if !ok {
		t.Fatal("reviewed apply tool is missing")
	}
	observer, ok := item.(tools.AuthorizationObserver)
	if !ok {
		t.Fatal("reviewed apply tool does not observe outer authorization")
	}
	observer.ObserveAuthorization(json.RawMessage(`{"id":"d1","review_token":"review-stale","all":true}`), fmt.Errorf("%w: declined", permission.ErrDenied))
	status, _ := fixture.runtime.Team.Get("d1")
	if status.IntegrationStatus != "rejected" || !strings.Contains(status.IntegrationError, "declined") {
		t.Fatalf("status=%+v", status)
	}
}

func TestDelegateIntegrationGitModeComparisonUsesPortableSemantics(t *testing.T) {
	content := "unchanged\n"
	if !sameGitBaseState(&content, 0o666, &content, 0o644) {
		t.Fatal("non-Git permission differences must not look like parent drift")
	}
	if runtime.GOOS != "windows" && sameGitBaseState(&content, 0o755, &content, 0o644) {
		t.Fatal("Git-significant executable changes must remain visible on Unix")
	}
	changed := "changed\n"
	if sameGitBaseState(&changed, 0o644, &content, 0o644) {
		t.Fatal("content changes must always look like parent drift")
	}
}

func runGitTest(t *testing.T, workspace string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", workspace}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Collomia Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Collomia Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func runGitOutputTest(t *testing.T, workspace string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", workspace}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

// A graph-owned candidate is reviewable and unpublishable through this path.
// The model-facing tool matters as much as the operator one: the primary agent
// holds no authority to publish a node's candidate either, and a refusal that
// only covered the human surface would leave the model's route open.
func TestGraphOwnedCandidateIsReviewableButNotPublishableHere(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)
	fixture.runtime.Team.Enqueue(agent.DelegateStart{ID: "g1", Name: "goal-write-1", Task: "extend alpha", Write: true, GraphNode: true})
	fixture.runtime.Team.FinishDetailed("g1", "extended alpha", []string{"verified"}, []string{"sample.txt"}, fixture.worktree, fixture.branch, fixture.base, provider.Usage{}, nil)
	data, err := os.ReadFile(fixture.delegatedFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.delegatedFile, []byte(strings.Replace(string(data), "line B", "line B from the graph writer", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	parentBefore, err := os.ReadFile(fixture.parentFile)
	if err != nil {
		t.Fatal(err)
	}

	// Reviewing is allowed: the retained worktree exists to be looked at.
	preview, err := fixture.runtime.PrepareDelegateIntegration(t.Context(), "g1")
	if err != nil {
		t.Fatalf("reviewing a graph candidate was refused: %v", err)
	}
	if !preview.GraphOwned || len(preview.Files) != 1 {
		t.Fatalf("preview=%+v, want one graph-owned file", preview)
	}
	selections := []DelegateIntegrationSelection{{Path: "sample.txt", Keep: []bool{true}}}

	for _, publish := range []struct {
		name string
		run  func() error
	}{
		{"operator apply", func() error {
			_, err := fixture.runtime.ApplyDelegateIntegration(t.Context(), "g1", selections)
			return err
		}},
		{"reviewed apply", func() error {
			_, err := fixture.runtime.ApplyReviewedDelegateIntegration(t.Context(), "g1", preview.ReviewToken, selections)
			return err
		}},
		{"reviewed action", func() error {
			_, err := fixture.runtime.PrepareReviewedDelegateIntegrationAction(t.Context(), "g1", preview.ReviewToken, selections)
			return err
		}},
	} {
		err := publish.run()
		if err == nil {
			t.Fatalf("%s published a graph-owned candidate", publish.name)
		}
		if !strings.Contains(err.Error(), "Orchestrated Goal candidate") {
			t.Fatalf("%s refusal does not explain itself: %v", publish.name, err)
		}
	}
	parentAfter, err := os.ReadFile(fixture.parentFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(parentBefore, parentAfter) {
		t.Fatal("a refused publication changed the parent workspace")
	}

	// An ordinary delegate on the same worktree is unaffected, so the refusal
	// is about graph ownership rather than about this repository state.
	if _, err := fixture.runtime.PrepareDelegateIntegration(t.Context(), "d1"); err != nil {
		t.Fatalf("an ordinary delegate was caught by the graph refusal: %v", err)
	}
}

// checkpointFixture gives the integration fixture a durable session, which is
// what makes the pre-integration checkpoint reachable at all.
func checkpointSession(t *testing.T, fixture integrationFixture) *session.Session {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	store, err := session.Open(fixture.workspace)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.New("fixture", "scripted")
	if err != nil {
		t.Fatal(err)
	}
	fixture.runtime.Session = sess
	return sess
}

// The record has to exist before the first byte moves, because the case it is
// for is the process stopping partway. A checkpoint written afterwards would
// describe only integrations that completed — exactly the ones that need no
// recovery.
func TestIntegrationRecordsPriorStateBeforePublishingAndMarksTheOutcome(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)
	sess := checkpointSession(t, fixture)
	data, err := os.ReadFile(fixture.delegatedFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.delegatedFile, []byte(strings.Replace(string(data), "line B", "line B from child", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	parentBefore, err := os.ReadFile(fixture.parentFile)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.runtime.ApplyDelegateIntegration(t.Context(), "d1", []DelegateIntegrationSelection{{Path: "sample.txt", Keep: []bool{true}}}); err != nil {
		t.Fatal(err)
	}
	if pending := sess.PendingIntegrationCheckpoints(); len(pending) != 0 {
		t.Fatalf("a completed integration left a pending checkpoint: %+v", pending)
	}
	recorded := sess.AllIntegrationCheckpoints()
	if len(recorded) != 1 {
		t.Fatalf("recorded checkpoints=%+v, want exactly one", recorded)
	}
	applied := recorded[0]
	if applied.State != session.IntegrationApplied || applied.Delegate != "d1" {
		t.Fatalf("checkpoint=%+v, want an applied record for d1", applied)
	}
	if len(applied.Files) != 1 || applied.Files[0].Path != "sample.txt" || !applied.Files[0].Existed {
		t.Fatalf("checkpoint files=%+v", applied.Files)
	}
	// The retained bytes are the parent's prior content, which is the only
	// thing that can put the workspace back.
	if applied.Files[0].Before == nil || *applied.Files[0].Before != string(parentBefore) {
		t.Fatal("the checkpoint did not retain the exact prior parent content")
	}
	if !applied.Restorable() {
		t.Fatal("a small text file was not recorded as restorable")
	}
}

// An interrupted integration is the one workspace state nothing else can
// explain. The runtime reports it, refuses to guess, and restores only when a
// person asks.
func TestInterruptedIntegrationIsReportedAndRestoredOnlyOnRequest(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)
	sess := checkpointSession(t, fixture)
	parentBefore, err := os.ReadFile(fixture.parentFile)
	if err != nil {
		t.Fatal(err)
	}
	prior := string(parentBefore)

	// Simulate a process that recorded the checkpoint, published the bytes, and
	// stopped before recording the outcome.
	checkpoint := session.NewIntegrationCheckpoint("checkpoint-interrupted", "d1", fixture.workspace, 0,
		[]session.IntegrationFile{{Path: "sample.txt", Existed: true, Before: &prior, Mode: 0o644}}, time.Now())
	if err := sess.AppendIntegrationCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.parentFile, []byte("half published\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pending := fixture.runtime.InterruptedIntegrations()
	if len(pending) != 1 || pending[0].ID != "checkpoint-interrupted" {
		t.Fatalf("interrupted integrations=%+v", pending)
	}
	summary := fixture.runtime.DescribeInterruptedIntegrations()
	if !strings.Contains(summary, "never recorded an outcome") || !strings.Contains(summary, "sample.txt") {
		t.Fatalf("summary does not explain the state:\n%s", summary)
	}
	// Nothing has been restored merely by looking.
	if current, _ := os.ReadFile(fixture.parentFile); string(current) != "half published\n" {
		t.Fatal("reporting an interrupted integration changed the workspace")
	}

	restored, err := fixture.runtime.RestoreIntegrationCheckpoint(t.Context(), "checkpoint-interrupted")
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 1 || restored[0] != "sample.txt" {
		t.Fatalf("restored=%v", restored)
	}
	current, err := os.ReadFile(fixture.parentFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != prior {
		t.Fatalf("restore did not return the exact prior bytes:\n%q", current)
	}
	// A restored checkpoint is resolved and cannot be applied twice.
	if len(fixture.runtime.InterruptedIntegrations()) != 0 {
		t.Fatal("a restored checkpoint is still reported as interrupted")
	}
	if _, err := fixture.runtime.RestoreIntegrationCheckpoint(t.Context(), "checkpoint-interrupted"); err == nil {
		t.Fatal("a resolved checkpoint was restored a second time")
	}
}

// Prior content past the retention bound is dropped, but the path is still
// named. Saying "this file changed and I cannot put it back" is far more
// useful than either refusing the integration or writing an unbounded record.
func TestOversizedPriorContentIsNamedRatherThanRetainedOrRefused(t *testing.T) {
	huge := strings.Repeat("x", (1<<20)+1)
	small := "small\n"
	checkpoint := session.NewIntegrationCheckpoint("checkpoint-big", "d1", "/workspace", 3,
		[]session.IntegrationFile{
			{Path: "big.bin", Existed: true, Before: &huge},
			{Path: "small.txt", Existed: true, Before: &small},
		}, time.Now())
	if checkpoint.Restorable() {
		t.Fatal("a checkpoint that dropped content claims it can restore everything")
	}
	if checkpoint.Files[0].Before != nil || checkpoint.Files[0].Restorable {
		t.Fatalf("oversized file retained content: %+v", checkpoint.Files[0])
	}
	if checkpoint.Files[0].Bytes != len(huge) {
		t.Fatalf("oversized file lost its recorded size: %+v", checkpoint.Files[0])
	}
	if checkpoint.Files[1].Before == nil || !checkpoint.Files[1].Restorable {
		t.Fatalf("a small sibling was dropped too: %+v", checkpoint.Files[1])
	}
	if !strings.Contains(checkpoint.Detail, "big.bin") {
		t.Fatalf("detail does not name what cannot be restored: %q", checkpoint.Detail)
	}
	if checkpoint.GraphNode != 3 {
		t.Fatalf("graph node attribution lost: %+v", checkpoint)
	}
}

// The rollback path must resolve the record too. A pending checkpoint left
// behind by an integration that actually undid itself would send a later
// recovery hunting for a half-published workspace that does not exist.
func TestRolledBackIntegrationResolvesItsCheckpoint(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)
	sess := checkpointSession(t, fixture)
	data, err := os.ReadFile(fixture.delegatedFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.delegatedFile, []byte(strings.Replace(string(data), "line B", "line B from child", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	// Reads must keep working so the freshness re-read inside publication
	// succeeds and the checkpoint is written; only the atomic replacement,
	// which creates a temporary file beside the target, must fail. A
	// read-only workspace directory does exactly that.
	if err := os.Chmod(fixture.workspace, 0o555); err != nil {
		t.Skip("cannot make the workspace read-only on this platform")
	}
	t.Cleanup(func() { _ = os.Chmod(fixture.workspace, 0o755) })
	_, err = fixture.runtime.ApplyDelegateIntegration(t.Context(), "d1", []DelegateIntegrationSelection{{Path: "sample.txt", Keep: []bool{true}}})
	if err == nil {
		t.Skip("the fixture could not make publication fail on this platform")
	}
	// The record must exist at all, which is what pins the write-ahead
	// ordering: a checkpoint written after the mutations would leave a failed
	// integration with no durable trace whatsoever.
	recorded := sess.AllIntegrationCheckpoints()
	if len(recorded) != 1 {
		t.Fatalf("a failed integration recorded %d checkpoints, want exactly one", len(recorded))
	}
	if recorded[0].State != session.IntegrationReverted {
		t.Fatalf("checkpoint state=%q, want reverted", recorded[0].State)
	}
	if !strings.Contains(recorded[0].Detail, "rolled back") {
		t.Fatalf("the reverted checkpoint does not say why: %q", recorded[0].Detail)
	}
}
