package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var slugPattern = regexp.MustCompile(`[^a-z0-9]+`)

// slugify turns an arbitrary task name into a short, filesystem- and
// branch-name-safe token.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugPattern.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "task"
	}
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// worktree is an isolated git working tree checked out for one
// write-capable delegated agent, so parallel agents never race on the same
// files. It is never merged, committed to, or pushed automatically.
type worktree struct {
	path   string
	branch string
	root   string // the origin repository workspace
}

func isGitRepo(ctx context.Context, workspace string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", workspace, "rev-parse", "--is-inside-work-tree")
	return cmd.Run() == nil
}

// newWorktree creates a new branch and working tree off HEAD, isolated
// under the system temp directory.
func newWorktree(ctx context.Context, workspace, name string) (*worktree, error) {
	base := filepath.Join(os.TempDir(), "collomia-worktrees")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, err
	}
	id := fmt.Sprintf("%s-%d", slugify(name), time.Now().UnixNano())
	path := filepath.Join(base, id)
	branch := "collomia/" + id
	cmd := exec.CommandContext(ctx, "git", "-C", workspace, "worktree", "add", "-b", branch, path, "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git worktree add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return &worktree{path: path, branch: branch, root: workspace}, nil
}

// changedFiles lists paths touched relative to the branch point.
func (w *worktree) changedFiles(ctx context.Context) []string {
	cmd := exec.CommandContext(ctx, "git", "-C", w.path, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if len(line) > 3 {
			files = append(files, strings.TrimSpace(line[3:]))
		}
	}
	return files
}

// remove tears down the working tree. Callers only invoke this when the
// tree is clean (no changes worth keeping); dirty trees are left in place
// for the user to review or merge by hand.
func (w *worktree) remove(ctx context.Context) {
	_ = exec.CommandContext(ctx, "git", "-C", w.root, "worktree", "remove", "--force", w.path).Run()
	_ = exec.CommandContext(ctx, "git", "-C", w.root, "branch", "-D", w.branch).Run()
}
