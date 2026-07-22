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
var zeroContextHunkPattern = regexp.MustCompile(`@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

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

// DelegateHunk identifies a changed range against the common HEAD base used
// by every sibling worktree. Old-side ranges make sibling overlap comparable
// even when their inserted output has different lengths.
type DelegateHunk struct {
	Path     string `json:"path"`
	OldStart int    `json:"old_start"`
	OldLines int    `json:"old_lines"`
	NewStart int    `json:"new_start"`
	NewLines int    `json:"new_lines"`
}

// changedHunks returns zero-context Git hunks for tracked changes and a
// whole-file insertion range for an untracked file. Failure is conservative:
// the caller retains file-level conflict detection when no ranges are known.
func (w *worktree) changedHunks(ctx context.Context, files []string) []DelegateHunk {
	var hunks []DelegateHunk
	for _, path := range files {
		cmd := exec.CommandContext(ctx, "git", "-C", w.path, "diff", "--no-ext-diff", "--no-color", "--unified=0", "HEAD", "--", path)
		out, err := cmd.Output()
		if err == nil {
			for _, match := range zeroContextHunkPattern.FindAllStringSubmatch(string(out), -1) {
				hunks = append(hunks, DelegateHunk{Path: path, OldStart: parseHunkCount(match[1]), OldLines: optionalHunkCount(match[2]), NewStart: parseHunkCount(match[3]), NewLines: optionalHunkCount(match[4])})
			}
		}
		if hasHunkForPath(hunks, path) {
			continue
		}
		// Git diff omits untracked files (and may omit binary ranges). Treat
		// either conservatively as an insertion at the empty base. Do not open
		// the path here: an untracked symlink or enormous file must not turn
		// conflict reporting into an out-of-sandbox read or memory spike.
		hunks = append(hunks, DelegateHunk{Path: path, OldStart: 0, OldLines: 0, NewStart: 1, NewLines: 1})
	}
	return hunks
}

func parseHunkCount(value string) int {
	var parsed int
	_, _ = fmt.Sscanf(value, "%d", &parsed)
	return parsed
}

func optionalHunkCount(value string) int {
	if value == "" {
		return 1
	}
	return parseHunkCount(value)
}

func hasHunkForPath(hunks []DelegateHunk, path string) bool {
	for _, hunk := range hunks {
		if hunk.Path == path {
			return true
		}
	}
	return false
}

// remove tears down the working tree. Callers only invoke this when the
// tree is clean (no changes worth keeping); dirty trees are left in place
// for the user to review or merge by hand.
func (w *worktree) remove(ctx context.Context) {
	_ = exec.CommandContext(ctx, "git", "-C", w.root, "worktree", "remove", "--force", w.path).Run()
	_ = exec.CommandContext(ctx, "git", "-C", w.root, "branch", "-D", w.branch).Run()
}
