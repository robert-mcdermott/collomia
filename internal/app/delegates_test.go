package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/agent"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/diffmodel"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/provider"
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
	runGitTest(t, workspace, "add", "sample.txt")
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
	if len(preview.Files) != 1 || !strings.Contains(preview.Files[0].Conflict, "parent workspace changed") {
		t.Fatalf("expected stale conflict, got %+v", preview.Files)
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

func TestManualAgentIntegrationDoesNotExposePrimaryTools(t *testing.T) {
	runtime := &Runtime{Config: appconfig.Defaults(), Registry: tools.NewRegistry()}
	runtime.addReviewedIntegrationTools()
	if _, ok := runtime.Registry.Get(InspectDelegateChangesTool); ok {
		t.Fatal("manual mode exposed inspect_delegate_changes")
	}
	if _, ok := runtime.Registry.Get(ApplyDelegateChangesTool); ok {
		t.Fatal("manual mode exposed apply_delegate_changes")
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
