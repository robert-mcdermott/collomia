package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/robert-mcdermott/collomia/internal/diffmodel"
	"github.com/robert-mcdermott/collomia/internal/hooks"
	"github.com/robert-mcdermott/collomia/internal/safefile"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

const (
	delegateIntegrationFileLimit  = 1 << 20
	delegateIntegrationTotalLimit = 4 << 20
	delegateIntegrationMaxFiles   = 256
)

// DelegateIntegration is a point-in-time, review-only comparison between an
// isolated child worktree and the parent workspace. Apply re-prepares it after
// permission approval so this snapshot can never authorize stale bytes.
type DelegateIntegration struct {
	ID, Name, Worktree, Branch, BaseCommit string
	Files                                  []DelegateIntegrationFile
}

type DelegateIntegrationFile struct {
	Path           string
	Before, After  *string
	BeforeMode     os.FileMode
	AfterMode      os.FileMode
	Unified        string
	Conflict       string
	AlreadyApplied bool
}

type DelegateIntegrationSelection struct {
	Path string
	Keep []bool
}

// PrepareDelegateIntegration validates a retained delegated worktree and
// creates text diffs without changing the repository.
func (r *Runtime) PrepareDelegateIntegration(ctx context.Context, id string) (*DelegateIntegration, error) {
	if r.Team == nil {
		return nil, errors.New("delegated-agent state is unavailable")
	}
	status, ok := r.Team.Get(id)
	if !ok {
		return nil, fmt.Errorf("unknown delegated agent %q", id)
	}
	if !status.Write || status.Worktree == "" || status.Branch == "" || status.BaseCommit == "" {
		return nil, fmt.Errorf("delegated agent %q has no retained write worktree", id)
	}
	if !strings.HasPrefix(status.Branch, "collomia/") {
		return nil, fmt.Errorf("refusing unexpected delegated branch %q", status.Branch)
	}
	if err := validateDelegateWorktree(ctx, r.Workspace, status.Worktree, status.Branch, status.BaseCommit); err != nil {
		return nil, err
	}
	changed, err := delegateChangedPaths(ctx, status.Worktree)
	if err != nil {
		return nil, err
	}
	if len(changed) == 0 {
		return nil, errors.New("delegated worktree has no changes to integrate")
	}
	if len(changed) > delegateIntegrationMaxFiles {
		return nil, fmt.Errorf("delegated worktree changes %d files; maximum reviewable integration is %d", len(changed), delegateIntegrationMaxFiles)
	}
	preview := &DelegateIntegration{ID: id, Name: status.Name, Worktree: status.Worktree, Branch: status.Branch, BaseCommit: status.BaseCommit}
	total := 0
	for _, path := range changed {
		base, baseMode, err := readGitBaseText(ctx, r.Workspace, status.BaseCommit, path)
		if err != nil {
			return nil, fmt.Errorf("read base %s: %w", path, err)
		}
		parent, parentMode, err := readRootedText(r.Workspace, path)
		if err != nil {
			return nil, fmt.Errorf("read parent %s: %w", path, err)
		}
		child, childMode, err := readRootedText(status.Worktree, path)
		if err != nil {
			return nil, fmt.Errorf("read delegated %s: %w", path, err)
		}
		total += contentBytes(base) + contentBytes(parent) + contentBytes(child)
		if total > delegateIntegrationTotalLimit {
			return nil, fmt.Errorf("delegated integration exceeds the %d MiB review limit", delegateIntegrationTotalLimit>>20)
		}
		file := DelegateIntegrationFile{Path: path, Before: parent, After: child, BeforeMode: parentMode, AfterMode: childMode}
		switch {
		case sameOSFileState(parent, parentMode, child, childMode):
			file.AlreadyApplied = true
		case !sameGitBaseState(parent, parentMode, base, baseMode):
			file.Conflict = "parent workspace changed from the delegated base"
		default:
			file.Unified = diffmodel.Unified(path, contentString(parent), contentString(child))
			if file.Unified == "" && parentMode != childMode {
				file.Conflict = "mode-only changes are not integrated automatically"
			}
		}
		preview.Files = append(preview.Files, file)
	}
	return preview, nil
}

// ApplyDelegateIntegration authorizes and atomically publishes the selected
// text hunks. It never commits, merges, deletes the worktree, or resolves a
// stale parent file. Multi-file publication rolls back earlier entries if a
// later rooted mutation fails.
func (r *Runtime) ApplyDelegateIntegration(ctx context.Context, id string, selections []DelegateIntegrationSelection) ([]string, error) {
	preview, err := r.PrepareDelegateIntegration(ctx, id)
	if err != nil {
		return nil, err
	}
	type mutation struct {
		path                             string
		before, after, expectedChild     *string
		beforeMode, afterMode, childMode os.FileMode
		preview                          string
	}
	files := make(map[string]DelegateIntegrationFile, len(preview.Files))
	for _, file := range preview.Files {
		files[file.Path] = file
	}
	mutations := make([]mutation, 0, len(selections))
	for _, selection := range selections {
		file, ok := files[selection.Path]
		if !ok {
			return nil, fmt.Errorf("delegated file %q is no longer present", selection.Path)
		}
		if file.Conflict != "" {
			return nil, fmt.Errorf("cannot integrate %s: %s", file.Path, file.Conflict)
		}
		if file.AlreadyApplied || file.Unified == "" {
			continue
		}
		hunks, err := diffmodel.ParseHunks(file.Unified)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", file.Path, err)
		}
		if len(selection.Keep) != len(hunks) {
			return nil, fmt.Errorf("selection for %s has %d flags for %d hunks", file.Path, len(selection.Keep), len(hunks))
		}
		selected := false
		all := true
		for _, keep := range selection.Keep {
			selected = selected || keep
			all = all && keep
		}
		if !selected {
			continue
		}
		var after *string
		afterMode := file.BeforeMode
		if all && file.After == nil {
			after = nil
		} else {
			value, applyErr := diffmodel.ApplyHunks(contentString(file.Before), hunks, selection.Keep)
			if applyErr != nil {
				return nil, fmt.Errorf("apply selected hunks for %s: %w", file.Path, applyErr)
			}
			after = &value
			if all {
				afterMode = file.AfterMode
			}
		}
		mutations = append(mutations, mutation{path: file.Path, before: cloneText(file.Before), after: after, expectedChild: cloneText(file.After), beforeMode: file.BeforeMode, afterMode: afterMode, childMode: file.AfterMode, preview: diffmodel.Unified(file.Path, contentString(file.Before), contentString(after))})
	}
	if len(mutations) == 0 {
		return nil, errors.New("no delegated hunks were selected")
	}
	paths := make([]string, len(mutations))
	var combined strings.Builder
	for i, mutation := range mutations {
		paths[i] = filepath.Join(r.Workspace, filepath.FromSlash(mutation.path))
		combined.WriteString(mutation.preview)
	}
	action := tools.Action{Risk: tools.RiskWrite, Summary: fmt.Sprintf("integrate %d file(s) from delegated agent %s (%s)", len(mutations), preview.Name, id), Paths: paths, Preview: combined.String()}
	if _, err := r.Permissions.Authorize(ctx, "integrate_delegate", action); err != nil {
		return nil, err
	}

	// Approval may have taken arbitrarily long. Re-read every source and target
	// and refuse if either side changed while the dialog was open.
	fresh, err := r.PrepareDelegateIntegration(ctx, id)
	if err != nil {
		return nil, err
	}
	freshFiles := make(map[string]DelegateIntegrationFile, len(fresh.Files))
	for _, file := range fresh.Files {
		freshFiles[file.Path] = file
	}
	for _, mutation := range mutations {
		file, ok := freshFiles[mutation.path]
		if !ok || file.Conflict != "" || file.AlreadyApplied ||
			!sameOSFileState(file.Before, file.BeforeMode, mutation.before, mutation.beforeMode) ||
			!sameOSFileState(file.After, file.AfterMode, mutation.expectedChild, mutation.childMode) {
			return nil, fmt.Errorf("%s changed while integration approval was pending; review again", mutation.path)
		}
	}

	applied := make([]mutation, 0, len(mutations))
	rollback := func() {
		for i := len(applied) - 1; i >= 0; i-- {
			_ = replaceRooted(r.Workspace, applied[i].path, applied[i].before, applied[i].beforeMode)
		}
	}
	for _, mutation := range mutations {
		if err := replaceRooted(r.Workspace, mutation.path, mutation.after, mutation.afterMode); err != nil {
			rollback()
			return nil, fmt.Errorf("integrate %s: %w", mutation.path, err)
		}
		applied = append(applied, mutation)
	}
	for _, mutation := range mutations {
		absolute := filepath.Join(r.Workspace, filepath.FromSlash(mutation.path))
		if r.Changes != nil {
			r.Changes.RecordWithMode(absolute, "delegate integration", mutation.before, mutation.after, mutation.beforeMode, mutation.afterMode)
		}
		if r.Hooks != nil {
			r.Hooks.Fire(ctx, hooks.Payload{Event: "file_change", Workspace: r.Workspace, Subject: "integrate_delegate", Tool: "integrate_delegate", Paths: []string{absolute}})
		}
	}
	integrated := make([]string, len(mutations))
	for i, mutation := range mutations {
		integrated[i] = mutation.path
	}
	r.Team.MarkIntegrated(id, integrated)
	return integrated, nil
}

func validateDelegateWorktree(ctx context.Context, workspace, worktree, branch, base string) error {
	timeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(timeout, "git", "-C", workspace, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return fmt.Errorf("list Git worktrees: %w", err)
	}
	wantBranch := "refs/heads/" + branch
	registered := false
	var currentPath, currentBranch string
	flush := func() {
		if sameDirectory(currentPath, worktree) && currentBranch == wantBranch {
			registered = true
		}
		currentPath, currentBranch = "", ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "worktree ") {
			currentPath = strings.TrimPrefix(line, "worktree ")
		} else if strings.HasPrefix(line, "branch ") {
			currentBranch = strings.TrimPrefix(line, "branch ")
		}
	}
	flush()
	if !registered {
		return errors.New("retained delegated path is not the recorded Git worktree for this repository")
	}
	resolved, err := exec.CommandContext(timeout, "git", "-C", worktree, "rev-parse", "HEAD").Output()
	if err != nil {
		return fmt.Errorf("inspect delegated branch: %w", err)
	}
	if strings.TrimSpace(string(resolved)) != base {
		return errors.New("delegated branch moved from its recorded base; review it manually")
	}
	return nil
}

func sameDirectory(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

func delegateChangedPaths(ctx context.Context, worktree string) ([]string, error) {
	timeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tracked, err := exec.CommandContext(timeout, "git", "-C", worktree, "diff", "--name-status", "--no-renames", "-z", "HEAD", "--").Output()
	if err != nil {
		return nil, fmt.Errorf("inspect delegated changes: %w", err)
	}
	seen := map[string]bool{}
	tokens := bytes.Split(tracked, []byte{0})
	for i := 0; i+1 < len(tokens); i += 2 {
		status, path := string(tokens[i]), string(tokens[i+1])
		if status == "" || path == "" {
			continue
		}
		if status[0] != 'M' && status[0] != 'A' && status[0] != 'D' && status[0] != 'T' {
			return nil, fmt.Errorf("unsupported delegated Git status %q for %q", status, path)
		}
		if err := validateDelegatePath(path); err != nil {
			return nil, err
		}
		seen[filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))] = true
	}
	untracked, err := exec.CommandContext(timeout, "git", "-C", worktree, "ls-files", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("inspect delegated untracked files: %w", err)
	}
	for _, raw := range bytes.Split(untracked, []byte{0}) {
		path := string(raw)
		if path == "" {
			continue
		}
		if err := validateDelegatePath(path); err != nil {
			return nil, err
		}
		seen[filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))] = true
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func validateDelegatePath(path string) error {
	if path == "" || strings.ContainsAny(path, "\x00\r\n:") {
		return fmt.Errorf("delegated path %q is not safely reviewable", path)
	}
	native := filepath.FromSlash(path)
	clean := filepath.Clean(native)
	if filepath.IsAbs(native) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("delegated path %q escapes the worktree", path)
	}
	return nil
}

func readRootedText(root, path string) (*string, os.FileMode, error) {
	if err := validateDelegatePath(path); err != nil {
		return nil, 0, err
	}
	target, err := safefile.Open(root, filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return nil, 0, err
	}
	defer target.Close()
	info, err := target.Lstat()
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("refusing non-regular file")
	}
	if info.Size() > delegateIntegrationFileLimit {
		return nil, 0, fmt.Errorf("file is larger than the %d MiB review limit", delegateIntegrationFileLimit>>20)
	}
	data, err := target.ReadFile()
	if err != nil {
		return nil, 0, err
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, 0, errors.New("binary or non-UTF-8 content requires manual integration")
	}
	value := string(data)
	return &value, info.Mode().Perm(), nil
}

func readGitBaseText(ctx context.Context, workspace, base, path string) (*string, os.FileMode, error) {
	if err := validateDelegatePath(path); err != nil {
		return nil, 0, err
	}
	timeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tree, err := exec.CommandContext(timeout, "git", "-C", workspace, "ls-tree", "-z", base, "--", path).Output()
	if err != nil {
		return nil, 0, err
	}
	if len(tree) == 0 {
		return nil, 0, nil
	}
	header, _, ok := bytes.Cut(tree, []byte{'\t'})
	if !ok {
		return nil, 0, errors.New("unexpected Git tree entry")
	}
	fields := strings.Fields(string(header))
	if len(fields) < 3 || fields[1] != "blob" {
		return nil, 0, errors.New("base entry is not a regular file")
	}
	modeValue, err := strconv.ParseUint(fields[0], 8, 32)
	if err != nil {
		return nil, 0, err
	}
	data, err := exec.CommandContext(timeout, "git", "-C", workspace, "show", base+":"+path).Output()
	if err != nil {
		return nil, 0, err
	}
	if len(data) > delegateIntegrationFileLimit {
		return nil, 0, fmt.Errorf("base file is larger than the %d MiB review limit", delegateIntegrationFileLimit>>20)
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, 0, errors.New("binary or non-UTF-8 base content requires manual integration")
	}
	value := string(data)
	return &value, os.FileMode(modeValue).Perm(), nil
}

func replaceRooted(root, path string, content *string, mode os.FileMode) error {
	target, err := safefile.Open(root, filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return err
	}
	defer target.Close()
	if content == nil {
		if err := target.Remove(); errors.Is(err, os.ErrNotExist) {
			return nil
		} else {
			return err
		}
	}
	return target.Replace([]byte(*content), mode)
}

func sameFileContent(left, right *string) bool {
	if (left == nil) != (right == nil) {
		return false
	}
	if left == nil {
		return true
	}
	return *left == *right
}

// sameOSFileState compares two live filesystem snapshots. Windows does not
// expose Unix permission bits through a checked-out Git worktree, so content
// is the complete portable state there. Unix retains the stricter permission
// comparison used for post-approval drift detection.
func sameOSFileState(left *string, leftMode os.FileMode, right *string, rightMode os.FileMode) bool {
	if !sameFileContent(left, right) {
		return false
	}
	return runtime.GOOS == "windows" || leftMode.Perm() == rightMode.Perm()
}

// sameGitBaseState compares a live parent file with a Git tree entry. Git
// stores content plus only the executable distinction, not group/other write
// bits. On Windows even that distinction is not represented reliably in
// os.FileMode, so clean checked-out content is authoritative.
func sameGitBaseState(parent *string, parentMode os.FileMode, base *string, baseMode os.FileMode) bool {
	if !sameFileContent(parent, base) {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	parentExecutable := parentMode.Perm()&0o111 != 0
	baseExecutable := baseMode.Perm()&0o111 != 0
	return parentExecutable == baseExecutable
}

func contentString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func contentBytes(value *string) int {
	if value == nil {
		return 0
	}
	return len(*value)
}

func cloneText(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
