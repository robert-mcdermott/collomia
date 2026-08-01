package tools

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/shell"
)

// gitWriteRepo builds a repository with one committed file and returns the
// workspace and a guard over it.
func gitWriteRepo(t *testing.T) (string, *PathGuard) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	// EvalSymlinks so the guard's canonical workspace and the paths compared
	// against it agree on macOS, where TempDir sits under /var -> /private/var.
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	gitTest(t, dir, "init", "-q", "-b", "main")
	writeRepoFile(t, dir, "tracked.txt", "one\n")
	gitTest(t, dir, "add", "tracked.txt")
	gitTest(t, dir, "commit", "-q", "-m", "first")
	guard, err := NewPathGuard(dir, false)
	if err != nil {
		t.Fatalf("guard: %v", err)
	}
	return dir, guard
}

func gitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeRepoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// gitLines runs a git query and returns its non-empty output lines, sorted, so
// a test can assert on a file set without depending on git's ordering.
func gitLines(t *testing.T, dir string, args ...string) ([]string, error) {
	t.Helper()
	out, err := runGitRaw(t.Context(), dir, args...)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	sort.Strings(lines)
	return lines, nil
}

func assessCommit(t *testing.T, tool GitCommitTool, args string) Action {
	t.Helper()
	action, err := tool.Assess(json.RawMessage(args))
	if err != nil {
		t.Fatalf("assess %s: %v", args, err)
	}
	return action
}

// The property that makes this a tool rather than a run_command string: the
// files entering the commit are declared, so the permission layer can classify
// them. `git commit -a` names no file and can carry anything.
func TestCommitDeclaresEveryFileItWillContain(t *testing.T) {
	dir, guard := gitWriteRepo(t)
	tool := GitCommitTool{Guard: guard}
	writeRepoFile(t, dir, "tracked.txt", "one\ntwo\n")
	writeRepoFile(t, dir, "added.txt", "new\n")

	action := assessCommit(t, tool, `{"message":"work","paths":["added.txt"]}`)
	if want := filepath.Join(dir, "added.txt"); !containsPath(action.Paths, want) {
		t.Errorf("declared paths %v missing the requested file %s", action.Paths, want)
	}
	if unwanted := filepath.Join(dir, "tracked.txt"); containsPath(action.Paths, unwanted) {
		t.Errorf("declared paths %v include a file that was not requested", action.Paths)
	}
}

// The guarantee, stated as one test: a commit contains the files it named and
// nothing else.
//
// Everything else in the repository is somebody else's — the user's edit in
// progress, the user's own staged work, an untracked scratch file, a .env. An
// agent committing "its" change must not carry any of it along, and must not
// disturb a hand-built index while it works.
func TestCommitContainsExactlyTheNamedFilesAndNothingElse(t *testing.T) {
	dir, guard := gitWriteRepo(t)
	writeRepoFile(t, dir, "unrelated.txt", "committed state\n")
	gitTest(t, dir, "add", "unrelated.txt")
	gitTest(t, dir, "commit", "-q", "-m", "second")

	// The agent's change, plus three things that are not its business.
	writeRepoFile(t, dir, "tracked.txt", "one\nagent's change\n")
	writeRepoFile(t, dir, "unrelated.txt", "the user's work in progress\n")
	writeRepoFile(t, dir, "staged-by-user.txt", "the user staged this\n")
	gitTest(t, dir, "add", "staged-by-user.txt")
	writeRepoFile(t, dir, ".env", "API_KEY=secret\n")

	tool := GitCommitTool{Guard: guard}
	action := assessCommit(t, tool, `{"message":"work","paths":["tracked.txt"]}`)
	if want := []string{filepath.Join(dir, "tracked.txt")}; !equalStrings(action.Paths, want) {
		t.Fatalf("declared paths = %v, want exactly %v", action.Paths, want)
	}
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"message":"work","paths":["tracked.txt"]}`)); err != nil {
		t.Fatalf("execute: %v", err)
	}

	committed, err := gitLines(t, dir, "show", "--name-only", "--format=", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tracked.txt"}; !equalStrings(committed, want) {
		t.Fatalf("the commit contains %v, want exactly %v", committed, want)
	}
	// The user's staged work is still staged: the tool committed around it
	// rather than through it.
	staged, err := gitLines(t, dir, "diff", "--cached", "--name-only")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"staged-by-user.txt"}; !equalStrings(staged, want) {
		t.Fatalf("index after the commit = %v, want the user's staged file untouched (%v)", staged, want)
	}
	// The user's uncommitted edit is still uncommitted.
	unstaged, err := gitLines(t, dir, "diff", "--name-only")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"unrelated.txt"}; !equalStrings(unstaged, want) {
		t.Fatalf("working tree after the commit = %v, want the user's edit untouched (%v)", unstaged, want)
	}
	// And nothing made the untracked file tracked.
	if out, err := runGitRaw(t.Context(), dir, "ls-files", ".env"); err != nil || strings.TrimSpace(out) != "" {
		t.Fatalf(".env became tracked: out=%q err=%v", out, err)
	}
}

// `paths` is required. An earlier version let it be omitted and committed every
// changed tracked file, which is `git commit -a` — it decides a commit's
// contents from whatever is in the working tree, so an agent committing its own
// change would have taken the user's in-progress edit with it, under autopilot,
// with no prompt.
func TestCommitRequiresPaths(t *testing.T) {
	dir, guard := gitWriteRepo(t)
	tool := GitCommitTool{Guard: guard}
	writeRepoFile(t, dir, "tracked.txt", "one\ntwo\n")
	for _, args := range []string{
		`{"message":"work"}`,
		`{"message":"work","paths":[]}`,
		`{"message":"work","paths":["  "]}`,
	} {
		if _, err := tool.Assess(json.RawMessage(args)); err == nil {
			t.Errorf("Assess(%s) was accepted; paths must be required", args)
		}
	}
}

// A new file is committed when it is named. Requiring the name is the whole
// cost of the guarantee above, so it has to actually work.
func TestCommitAddsUntrackedFileWhenNamed(t *testing.T) {
	dir, guard := gitWriteRepo(t)
	tool := GitCommitTool{Guard: guard}
	writeRepoFile(t, dir, "added.txt", "new\n")

	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"message":"add a file","paths":["added.txt"]}`)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out, err := runGitRaw(t.Context(), dir, "ls-files", "added.txt")
	if err != nil || strings.TrimSpace(out) != "added.txt" {
		t.Fatalf("named untracked file was not committed: out=%q err=%v", out, err)
	}
}

func TestCommitPreviewShowsTheChange(t *testing.T) {
	dir, guard := gitWriteRepo(t)
	tool := GitCommitTool{Guard: guard}
	writeRepoFile(t, dir, "tracked.txt", "one\ntwo\n")

	action := assessCommit(t, tool, `{"message":"work","paths":["tracked.txt"]}`)
	if !strings.Contains(action.Preview, "+two") {
		t.Fatalf("preview does not show the change being committed:\n%s", action.Preview)
	}
}

func TestCommitRefusesFlagShapedArguments(t *testing.T) {
	_, guard := gitWriteRepo(t)
	tool := GitCommitTool{Guard: guard}
	for _, args := range []string{
		`{"message":"--amend"}`,
		`{"message":"work","paths":["--all"]}`,
		`{"message":""}`,
		`{"message":"   "}`,
	} {
		if _, err := tool.Assess(json.RawMessage(args)); err == nil {
			t.Errorf("Assess(%s) was accepted", args)
		}
	}
}

func TestCommitRefusesPathsOutsideTheWorkspace(t *testing.T) {
	_, guard := gitWriteRepo(t)
	tool := GitCommitTool{Guard: guard}
	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	if err := os.WriteFile(outside, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]any{"message": "work", "paths": []string{outside}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Assess(args); err == nil {
		t.Fatal("a path outside the workspace was accepted")
	}
}

// A named file with no change is git's own refusal, and it should reach the
// model as git wrote it rather than as a guess made before running anything.
func TestCommitRefusesWhenTheNamedFileHasNotChanged(t *testing.T) {
	_, guard := gitWriteRepo(t)
	tool := GitCommitTool{Guard: guard}
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"message":"work","paths":["tracked.txt"]}`)); err == nil {
		t.Fatal("a commit with no change was accepted")
	}
}

// Creating a branch at HEAD leaves the working tree alone. Checking out an
// existing branch does not, and /restore verifies the workspace before
// reversing anything — so allowing the switch would silently disarm recovery
// for every turn before it.
func TestBranchCreatesWithoutTouchingTheWorkingTree(t *testing.T) {
	dir, guard := gitWriteRepo(t)
	tool := GitBranchTool{Guard: guard}
	writeRepoFile(t, dir, "tracked.txt", "one\nuncommitted\n")

	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"name":"feature/x"}`)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	branch, err := runGitRaw(t.Context(), dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || strings.TrimSpace(branch) != "feature/x" {
		t.Fatalf("branch=%q err=%v", branch, err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "tracked.txt"))
	if err != nil || string(content) != "one\nuncommitted\n" {
		t.Fatalf("uncommitted work did not survive the branch: %q %v", content, err)
	}
}

func TestBranchRefusesAnExistingBranch(t *testing.T) {
	_, guard := gitWriteRepo(t)
	tool := GitBranchTool{Guard: guard}
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"name":"main"}`)); err == nil {
		t.Fatal("switching to an existing branch was allowed")
	}
}

func TestBranchRefusesInvalidNames(t *testing.T) {
	_, guard := gitWriteRepo(t)
	tool := GitBranchTool{Guard: guard}
	for _, name := range []string{"--force", "has space", "bad..name", "-x", ""} {
		args, err := json.Marshal(map[string]string{"name": name})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tool.Execute(t.Context(), args); err == nil {
			t.Errorf("branch name %q was accepted", name)
		}
	}
}

// The guard against this file becoming a second classification site. Whatever
// the write tools propose must be classified exactly as the same command typed
// into run_command — otherwise a structured tool is a way around the
// confirmations and the publication tier that govern the text form.
func TestGitWriteToolsClassifyAsTheEquivalentCommand(t *testing.T) {
	dir, guard := gitWriteRepo(t)
	writeRepoFile(t, dir, "tracked.txt", "one\ntwo\n")

	commit := assessCommit(t, GitCommitTool{Guard: guard}, `{"message":"work","paths":["tracked.txt"]}`)
	branch, err := GitBranchTool{Guard: guard}.Assess(json.RawMessage(`{"name":"feature/x"}`))
	if err != nil {
		t.Fatalf("assess branch: %v", err)
	}
	for _, tc := range []struct {
		name   string
		action Action
		argv   []string
	}{
		{"commit", commit, []string{"git", "commit", "-m", "work", "--", "tracked.txt"}},
		{"branch", branch, []string{"git", "checkout", "-b", "feature/x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := ActionFromAnalysis(tc.action.Summary, shell.QuoteArgv(tc.argv), shell.AnalyzeInWorkspace(shell.QuoteArgv(tc.argv), dir))
			if got := tc.action.Risk; got != want.Risk {
				t.Errorf("risk = %v, run_command = %v", got, want.Risk)
			}
			if !equalStrings(tc.action.Executables, want.Executables) {
				t.Errorf("executables = %v, run_command = %v", tc.action.Executables, want.Executables)
			}
			if !equalStrings(tc.action.Operations, want.Operations) {
				t.Errorf("operations = %v, run_command = %v", tc.action.Operations, want.Operations)
			}
			if !equalStrings(tc.action.PublicationTargets, want.PublicationTargets) {
				t.Errorf("publication = %v, run_command = %v", tc.action.PublicationTargets, want.PublicationTargets)
			}
			if !equalStrings(tc.action.ConfirmReasons, want.ConfirmReasons) {
				t.Errorf("confirm reasons = %v, run_command = %v", tc.action.ConfirmReasons, want.ConfirmReasons)
			}
			if tc.action.Uninspectable != want.Uninspectable {
				t.Errorf("uninspectable = %v, run_command = %v", tc.action.Uninspectable, want.Uninspectable)
			}
		})
	}
}

// A rule naming the operation is the expressible exception, so the operation
// string has to be the one a person would write down.
func TestGitWriteToolsNameTheirOperation(t *testing.T) {
	dir, guard := gitWriteRepo(t)
	writeRepoFile(t, dir, "tracked.txt", "one\ntwo\n")
	commit := assessCommit(t, GitCommitTool{Guard: guard}, `{"message":"work","paths":["tracked.txt"]}`)
	if !containsPath(commit.Operations, "git commit") {
		t.Errorf("commit operations = %v, want to include %q", commit.Operations, "git commit")
	}
	branch, err := GitBranchTool{Guard: guard}.Assess(json.RawMessage(`{"name":"feature/x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !containsPath(branch.Operations, "git checkout") {
		t.Errorf("branch operations = %v, want to include %q", branch.Operations, "git checkout")
	}
}

// Neither tool may publish. If a future edit routes a push through here it
// should fail loudly rather than inherit the approval path of a commit.
func TestGitWriteToolsNeverPublish(t *testing.T) {
	dir, guard := gitWriteRepo(t)
	writeRepoFile(t, dir, "tracked.txt", "one\ntwo\n")
	commit := assessCommit(t, GitCommitTool{Guard: guard}, `{"message":"work","paths":["tracked.txt"]}`)
	branch, err := GitBranchTool{Guard: guard}.Assess(json.RawMessage(`{"name":"feature/x"}`))
	if err != nil {
		t.Fatal(err)
	}
	for name, action := range map[string]Action{"git_commit": commit, "git_branch": branch} {
		if len(action.PublicationTargets) > 0 {
			t.Errorf("%s reported a publication target: %v", name, action.PublicationTargets)
		}
	}
}

func containsPath(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
