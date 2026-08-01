package permission

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// This is the end-to-end form of the reason git_commit exists as a tool rather
// than as a run_command string.
//
// `git commit -a` carries no path, so shell analysis has no argument to
// classify and a tracked .env is committed with nothing noticing. git_commit
// declares the files entering the commit, and credentialTargets derives the
// protection from Action.Paths — so the gate applies without the Git tool
// knowing what a credential is, which is the property the derivation was
// written for.
func TestCommittingACredentialFileIsGated(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	workspace := gitRepoWithTrackedEnvFile(t)
	guard, err := tools.NewPathGuard(workspace, false)
	if err != nil {
		t.Fatalf("guard: %v", err)
	}
	action, err := tools.GitCommitTool{Guard: guard}.Assess(json.RawMessage(`{"message":"work","paths":[".env"]}`))
	if err != nil {
		t.Fatalf("assess: %v", err)
	}

	// Autopilot is the mode that matters: it is the one that would otherwise
	// approve a commit without anyone seeing it.
	manager := New(appconfig.Permissions{
		Mode:               "autopilot",
		ProtectCredentials: appconfig.ProtectCredentialsPrompt,
	}, nil)
	grant, outcome := manager.Evaluate("git_commit", action)
	if outcome != "prompt" {
		t.Fatalf("committing a .env under autopilot resolved to %q (%s), want a prompt", outcome, grant.Rule)
	}
	if grant.Source != "credentials" {
		t.Fatalf("prompt source = %q, want the credential gate", grant.Source)
	}

	// And "deny" must actually deny rather than merely prompt harder.
	denying := New(appconfig.Permissions{
		Mode:               "autopilot",
		ProtectCredentials: appconfig.ProtectCredentialsDeny,
	}, nil)
	if _, outcome := denying.Evaluate("git_commit", action); outcome != "deny" {
		t.Fatalf("protect_credentials=deny resolved to %q, want deny", outcome)
	}
}

// The control for the test above: an ordinary source file commits without a
// prompt under autopilot, exactly as the equivalent command already did. A gate
// that fires on every commit is one people switch off.
func TestCommittingOrdinaryFilesIsNotGated(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	workspace := gitRepoWithTrackedSourceFile(t)
	guard, err := tools.NewPathGuard(workspace, false)
	if err != nil {
		t.Fatalf("guard: %v", err)
	}
	action, err := tools.GitCommitTool{Guard: guard}.Assess(json.RawMessage(`{"message":"work","paths":["main.go"]}`))
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	manager := New(appconfig.Permissions{
		Mode:               "autopilot",
		ProtectCredentials: appconfig.ProtectCredentialsPrompt,
	}, nil)
	if _, outcome := manager.Evaluate("git_commit", action); outcome != "allow" {
		t.Fatalf("committing a source file under autopilot resolved to %q, want allow", outcome)
	}
}

// A rule naming the operation is the durable exception for a recurring commit,
// which is what keeps the gate above livable.
func TestOperationRuleCoversCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	workspace := gitRepoWithTrackedSourceFile(t)
	guard, err := tools.NewPathGuard(workspace, false)
	if err != nil {
		t.Fatalf("guard: %v", err)
	}
	action, err := tools.GitCommitTool{Guard: guard}.Assess(json.RawMessage(`{"message":"work","paths":["main.go"]}`))
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	manager := New(appconfig.Permissions{
		Mode:  "ask",
		Rules: []appconfig.Rule{{Action: "deny", Command: "git commit"}},
	}, nil)
	grant, outcome := manager.Evaluate("git_commit", action)
	if outcome != "deny" {
		t.Fatalf("a deny rule naming `git commit` resolved to %q (%s)", outcome, grant.Rule)
	}
}

func gitRepoWithTrackedEnvFile(t *testing.T) string {
	t.Helper()
	dir := newGitRepo(t)
	writeAndCommit(t, dir, ".env", "API_KEY=first\n")
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("API_KEY=second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func gitRepoWithTrackedSourceFile(t *testing.T) string {
	t.Helper()
	dir := newGitRepo(t)
	writeAndCommit(t, dir, "main.go", "package main\n")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	runRepoGit(t, dir, "init", "-q", "-b", "main")
	return dir
}

func writeAndCommit(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runRepoGit(t, dir, "add", name)
	runRepoGit(t, dir, "commit", "-q", "-m", "add "+name)
}

func runRepoGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
