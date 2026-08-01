package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/shell"
)

// Git write tools.
//
// These add structure over a capability the agent already had rather than a
// capability it lacked: `run_command` has always been able to run `git commit`,
// and on a stock configuration under autopilot it was approved silently,
// because internal/shell/safety.go classifies destruction and a commit destroys
// nothing. So the question this file answers is not whether the agent may
// commit — it could — but whether committing is something the permission layer
// can see, describe, and gate.
//
// Two properties follow from being a tool rather than a command string:
//
//   - Classification is shared, not restated. Every argv is analyzed by
//     shell.AnalyzeArgv, so `git push` typed as a command and `git push` built
//     here reach the publication tier through the same code. A structured Git
//     tool that classified itself would be a documented way around the controls
//     that govern the same command as text.
//
//   - The paths are declared. This is the part `run_command` structurally
//     cannot do: `git commit -a` names no file, so shell analysis sees no
//     argument to classify and a .env in the working tree is committed without
//     anything noticing. A declared path list runs through the permission
//     layer's own derivation from Action.Paths, so committing a credential file
//     prompts under protect_credentials without this file knowing what a
//     credential is.
//
// Deliberately absent: push. The publication tier already governs it through
// run_command, and ROADMAP.md's deferred list has said from the start that
// pushes are not something the agent does on its own initiative. Adding a
// dedicated tool for it would be adding the outward-facing capability rather
// than governing the one that was already there.

// gitPreviewCap bounds the diff carried into an approval prompt. The prompt has
// to stay readable; the full diff is a git_diff call away.
const gitPreviewCap = 32 * 1024

// gitWriteAction builds the permission-facing description of a Git mutation
// from the argv that will actually run.
//
// It routes through the same two functions run_command uses — shell.AnalyzeArgv
// and ActionFromAnalysis — so a field added to the analysis reaches both without
// anyone remembering this file exists.
func gitWriteAction(summary string, workspace string, argv []string) Action {
	analysis := shell.AnalyzeArgv(argv, workspace)
	return ActionFromAnalysis(summary, analysis.Raw, analysis)
}

// gitRepoRelative converts a guarded absolute path to the form git wants,
// always with forward slashes so a Windows path is a valid pathspec.
func gitRepoRelative(workspace, absolute string) (string, error) {
	relative, err := filepath.Rel(workspace, absolute)
	if err != nil {
		return "", err
	}
	relative = filepath.ToSlash(relative)
	if relative == ".." || strings.HasPrefix(relative, "../") {
		return "", fmt.Errorf("%s is outside the repository", absolute)
	}
	return relative, nil
}

type GitCommitTool struct{ Guard *PathGuard }

func (t GitCommitTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{
		Name: "git_commit",
		Description: "Commit specific files in the workspace repository. " +
			"List every file the commit should contain in paths, including files that are new and not yet tracked; " +
			"the commit contains exactly those files and nothing else. " +
			"Other changes in the working tree — the user's own edits, build output, scratch files — are left alone, " +
			"as is anything the user has staged. Use git_status first if you are not sure what you changed. " +
			"This never pushes: use run_command if a push is actually wanted, which is governed separately.",
		InputSchema: schema(`{"type":"object","properties":{"message":{"type":"string","description":"Commit message. The first line should be a concise summary."},"paths":{"type":"array","items":{"type":"string"},"minItems":1,"description":"Workspace-relative files to commit. Every file the commit should contain, including new ones."}},"required":["message","paths"],"additionalProperties":false}`),
	}
}

type gitCommitArgs struct {
	Message string   `json:"message"`
	Paths   []string `json:"paths"`
}

func (a gitCommitArgs) message() (string, error) {
	message := strings.TrimSpace(a.Message)
	if message == "" {
		return "", errors.New("message is required")
	}
	// A message beginning with '-' would be read as an option by git rather
	// than as text, exactly as safeGitArg guards elsewhere in this package.
	// The message always travels after a literal -m, but the argv is also the
	// text the permission prompt renders, and a leading dash there reads as a
	// flag to a person too.
	if strings.HasPrefix(message, "-") {
		return "", errors.New("message must not start with '-'")
	}
	return message, nil
}

// gitCommitPlan is the exact file set the commit will contain, resolved once so
// Assess and Execute cannot disagree about what was approved.
type gitCommitPlan struct {
	// staged are repo-relative pathspecs, the form git wants.
	staged []string
	// absolute are the same files as absolute paths, which is the form the
	// permission layer classifies (secrets.Classify resolves against $HOME).
	absolute []string
}

// planCommit resolves the named files and nothing else.
//
// `paths` is required, and the commit is restricted to it, because the
// alternative was worse in a way that only showed up when someone asked the
// right question. An earlier version let `paths` be omitted and then committed
// every changed tracked file — which is `git commit -a`, and which decides the
// contents of a commit from whatever happens to be in the working tree. In a
// repository where the user has their own edit in progress, an agent committing
// "its" change would have taken that edit with it, silently, under autopilot,
// with no prompt because nothing about it reaches a credential store.
//
// A tool whose stated purpose is to say what is in a commit cannot have a mode
// where the answer is "whatever was lying around". The agent knows which files
// it changed; git_status is there for when it does not.
func (t GitCommitTool) planCommit(_ context.Context, args gitCommitArgs) (gitCommitPlan, error) {
	workspace := t.Guard.Workspace
	set := map[string]bool{}
	for _, requested := range args.Paths {
		if strings.TrimSpace(requested) == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(requested), "-") {
			return gitCommitPlan{}, fmt.Errorf("path must not start with '-': %s", requested)
		}
		resolved, outside, err := t.Guard.Resolve(requested)
		if err != nil {
			return gitCommitPlan{}, err
		}
		if outside {
			return gitCommitPlan{}, fmt.Errorf("%s is outside the workspace; a commit only covers this repository", requested)
		}
		relative, err := gitRepoRelative(workspace, resolved)
		if err != nil {
			return gitCommitPlan{}, err
		}
		set[relative] = true
	}
	if len(set) == 0 {
		return gitCommitPlan{}, errors.New("paths is required and must name at least one file; use git_status to see what changed")
	}
	plan := gitCommitPlan{}
	for name := range set {
		plan.staged = append(plan.staged, name)
	}
	sort.Strings(plan.staged)
	for _, name := range plan.staged {
		plan.absolute = append(plan.absolute, filepath.Join(workspace, filepath.FromSlash(name)))
	}
	return plan, nil
}

func (t GitCommitTool) Assess(raw json.RawMessage) (Action, error) {
	var a gitCommitArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return Action{}, err
	}
	message, err := a.message()
	if err != nil {
		return Action{}, err
	}
	ctx := context.Background()
	if err := requireGitRepository(ctx, t.Guard.Workspace); err != nil {
		return Action{}, err
	}
	plan, err := t.planCommit(ctx, a)
	if err != nil {
		return Action{}, err
	}
	action := gitWriteAction("git commit: "+firstLine(message), t.Guard.Workspace, commitArgv(message, plan))
	action.Paths = plan.absolute
	action.Preview = t.previewCommit(ctx, plan)
	return action, nil
}

// previewCommit renders what the commit will contain. It is display only: a
// failure here must not block a commit the user is entitled to make, so an
// unreadable diff degrades to naming the files.
func (t GitCommitTool) previewCommit(ctx context.Context, plan gitCommitPlan) string {
	args := append([]string{"diff", "HEAD", "--"}, plan.staged...)
	diff, err := runGitRaw(ctx, t.Guard.Workspace, args...)
	if err != nil || strings.TrimSpace(diff) == "" {
		// No HEAD yet (the first commit in a repository) or an unreadable
		// diff. The file list is still the useful part.
		return "files in this commit:\n  " + strings.Join(plan.staged, "\n  ")
	}
	if len(diff) > gitPreviewCap {
		diff = diff[:gitPreviewCap] + "\n… diff truncated; use git_diff for the rest\n"
	}
	return diff
}

func (t GitCommitTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var a gitCommitArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	message, err := a.message()
	if err != nil {
		return "", err
	}
	plan, err := t.planCommit(ctx, a)
	if err != nil {
		return "", err
	}
	if err := requireGitRepository(ctx, t.Guard.Workspace); err != nil {
		return "", err
	}
	if err := requireCommitIdentity(ctx, t.Guard.Workspace); err != nil {
		return "", err
	}
	// Staging is needed for a file git does not track yet — `git commit -- new`
	// fails, because an untracked path matches nothing git knows about.
	stage := append([]string{"add", "--"}, plan.staged...)
	if _, err := runGitRaw(ctx, t.Guard.Workspace, stage...); err != nil {
		return "", fmt.Errorf("staging failed: %w", err)
	}
	if _, err := runGitRaw(ctx, t.Guard.Workspace, commitArgv(message, plan)[1:]...); err != nil {
		return "", err
	}
	summary, err := runGitRaw(ctx, t.Guard.Workspace, "log", "-1", "--pretty=format:%h %s", "--stat")
	if err != nil {
		return "committed", nil
	}
	return strings.TrimSpace(summary), nil
}

// commitArgv is the argv the permission layer is shown and the one that runs,
// derived once so they cannot drift.
//
// The trailing pathspec is what makes the tool's promise exact rather than
// approximate. `git commit -- <paths>` commits those paths and only those
// paths: anything else the user has staged stays staged, and any other change
// in the working tree stays uncommitted. Without it a commit takes the whole
// index, so a file the user staged by hand would be swept into a commit the
// agent proposed and described as something else.
func commitArgv(message string, plan gitCommitPlan) []string {
	argv := []string{"git", "commit", "-m", message, "--"}
	return append(argv, plan.staged...)
}

type GitBranchTool struct{ Guard *PathGuard }

func (t GitBranchTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{
		Name: "git_branch",
		Description: "Create a new branch at the current commit and switch to it. " +
			"The working tree is untouched: any uncommitted work carries over to the new branch. " +
			"Switching to a branch that already exists is refused — use run_command for that.",
		InputSchema: schema(`{"type":"object","properties":{"name":{"type":"string","description":"New branch name, e.g. fix/parser-panic"}},"required":["name"],"additionalProperties":false}`),
	}
}

type gitBranchArgs struct {
	Name string `json:"name"`
}

func (t GitBranchTool) branchName(raw json.RawMessage) (string, error) {
	var a gitBranchArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	name, err := safeGitArg(a.Name, "name")
	if err != nil {
		return "", err
	}
	if name == "" {
		return "", errors.New("name is required")
	}
	return name, nil
}

func (t GitBranchTool) Assess(raw json.RawMessage) (Action, error) {
	name, err := t.branchName(raw)
	if err != nil {
		return Action{}, err
	}
	return gitWriteAction("git branch: "+name, t.Guard.Workspace, branchArgv(name)), nil
}

func (t GitBranchTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	name, err := t.branchName(raw)
	if err != nil {
		return "", err
	}
	workspace := t.Guard.Workspace
	if err := requireGitRepository(ctx, workspace); err != nil {
		return "", err
	}
	// git's own rules rather than a hand-written character list. A second
	// vocabulary here would be one more list nobody updates, and it would
	// disagree with the git that actually runs.
	if _, err := runGitRaw(ctx, workspace, "check-ref-format", "--branch", name); err != nil {
		return "", fmt.Errorf("%q is not a valid branch name", name)
	}
	// Creating a branch at HEAD leaves every file on disk exactly as it was,
	// which is the reason this tool only creates. Checking out an existing
	// branch rewrites the working tree from outside Collomia's change
	// tracking, and /restore verifies the workspace before it will reverse
	// anything — so a switch would silently disarm the recovery path for every
	// turn that came before it.
	if _, err := runGitRaw(ctx, workspace, "rev-parse", "--verify", "--quiet", "refs/heads/"+name); err == nil {
		return "", fmt.Errorf("branch %q already exists; switching to an existing branch changes files outside Collomia's tracking, which would prevent /restore from reversing earlier turns", name)
	}
	if _, err := runGitRaw(ctx, workspace, branchArgv(name)[1:]...); err != nil {
		return "", err
	}
	return "created and switched to branch " + name, nil
}

func branchArgv(name string) []string {
	return []string{"git", "checkout", "-b", name}
}

// requireGitRepository turns git's own message into one that names the cause.
func requireGitRepository(ctx context.Context, workspace string) error {
	if _, err := runGitRaw(ctx, workspace, "rev-parse", "--git-dir"); err != nil {
		return errors.New("this workspace is not a Git repository")
	}
	return nil
}

// requireCommitIdentity fails before staging rather than after.
//
// git refuses to commit without an author identity and says so in four lines of
// advice aimed at a person at a terminal. Reaching that after the index has
// already been modified leaves the workspace changed by a tool call that
// reported failure, which is the state hardest to reason about.
//
// `git var` is asked rather than `git config --get user.email`, because config
// is only one of the places the identity comes from: GIT_AUTHOR_NAME and
// GIT_COMMITTER_EMAIL are how CI and this package's own tests supply it, and a
// config-only check would have refused commits that git performs perfectly
// well. Asking git to resolve it is the same choice as asking
// check-ref-format about a branch name — one vocabulary, and it is git's.
func requireCommitIdentity(ctx context.Context, workspace string) error {
	for _, ident := range []string{"GIT_AUTHOR_IDENT", "GIT_COMMITTER_IDENT"} {
		if _, err := runGitRaw(ctx, workspace, "var", ident); err != nil {
			return errors.New("git cannot determine who is committing; set it with `git config --global user.email \"…\"` and `git config --global user.name \"…\"`")
		}
	}
	return nil
}

func firstLine(value string) string {
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return strings.TrimSpace(value[:index])
	}
	return value
}
