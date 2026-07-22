package app

import (
	"context"
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
	workspace := t.TempDir()
	runGitTest(t, workspace, "init", "-b", "main")
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
	return integrationFixture{runtime: runtime, workspace: workspace, worktree: worktree, branch: branch, base: base, parentFile: parentFile, delegatedFile: filepath.Join(worktree, "sample.txt")}
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
	if len(status.Integrated) != 1 || status.Integrated[0] != "sample.txt" {
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
