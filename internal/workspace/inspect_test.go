package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ParseGitStatus has its own tests because it must behave identically on every
// platform without a Git executable. These cover the half that was untested:
// actually running Git and turning what it says into a GitStatus. The two are
// not interchangeable — a parser that is perfect against a transcript proves
// nothing about the flags the command was invoked with, and `--porcelain=v2`
// output is what those flags produce.

// gitCommand builds an invocation isolated from the developer's own Git
// configuration and carrying an identity, so these tests do not depend on the
// machine running them and cannot be broken by a global hook, template, or
// default branch name.
func gitCommand(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	return cmd
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitCommand(dir, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// gitExpectingFailure runs a command whose non-zero exit is the point, and
// returns its output so a test can prove the failure was the intended one
// rather than a missing identity or a refusing hook.
func gitExpectingFailure(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitCommand(dir, args...).CombinedOutput()
	if err == nil {
		t.Fatalf("git %s unexpectedly succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

// newRepo returns an initialized repository with one commit, which is the
// state every interesting case builds on: several porcelain fields do not
// appear at all until HEAD exists.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	git(t, dir, "init", "--initial-branch=main", dir)
	write(t, dir, "committed.txt", "one\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "first")
	return dir
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInspectGitReportsACleanRepository(t *testing.T) {
	status := InspectGit(t.Context(), newRepo(t))
	if status.Error != "" {
		t.Fatalf("unexpected error: %s", status.Error)
	}
	if !status.InRepository {
		t.Error("a repository must be recognized as one")
	}
	if status.Branch != "main" {
		t.Errorf("branch = %q, want main", status.Branch)
	}
	if status.Staged+status.Modified+status.Untracked+status.Conflicted != 0 {
		t.Errorf("a clean repository must report no changes, got %+v", status)
	}
}

func TestInspectGitCountsEachKindOfChangeSeparately(t *testing.T) {
	// Staged and modified are separate counters over the same two-character
	// field, and a file that is both must be counted in both — which is the
	// part a transcript test is least likely to get right by accident.
	dir := newRepo(t)
	write(t, dir, "staged.txt", "new\n")
	git(t, dir, "add", "staged.txt")
	write(t, dir, "committed.txt", "changed\n")
	write(t, dir, "untracked.txt", "loose\n")
	write(t, dir, "both.txt", "staged\n")
	git(t, dir, "add", "both.txt")
	write(t, dir, "both.txt", "and then modified\n")

	status := InspectGit(t.Context(), dir)
	if status.Error != "" {
		t.Fatalf("unexpected error: %s", status.Error)
	}
	if status.Staged != 2 {
		t.Errorf("staged = %d, want 2 (staged.txt and both.txt)", status.Staged)
	}
	if status.Modified != 2 {
		t.Errorf("modified = %d, want 2 (committed.txt and both.txt)", status.Modified)
	}
	if status.Untracked != 1 {
		t.Errorf("untracked = %d, want 1", status.Untracked)
	}
}

func TestInspectGitReportsAheadAndBehindAgainstARealUpstream(t *testing.T) {
	origin := newRepo(t)
	clone := t.TempDir()
	git(t, clone, "clone", origin, clone)

	// One commit only the clone has, one only the origin has.
	write(t, clone, "local.txt", "ours\n")
	git(t, clone, "add", ".")
	git(t, clone, "commit", "-m", "ours")
	write(t, origin, "remote.txt", "theirs\n")
	git(t, origin, "add", ".")
	git(t, origin, "commit", "-m", "theirs")
	git(t, clone, "fetch", "origin")

	status := InspectGit(t.Context(), clone)
	if status.Error != "" {
		t.Fatalf("unexpected error: %s", status.Error)
	}
	if status.Upstream == "" {
		t.Error("a cloned branch must report its upstream")
	}
	if status.Ahead != 1 || status.Behind != 1 {
		t.Errorf("ahead/behind = %d/%d, want 1/1 — behind is reported by Git as a negative and must be normalized", status.Ahead, status.Behind)
	}
}

func TestInspectGitReportsAConflictAsItsOwnCount(t *testing.T) {
	// A conflicted file appears on a `u ` line, not a `1 `/`2 ` line, so it
	// would be silently invisible if only the ordinary change lines were read.
	dir := newRepo(t)
	git(t, dir, "checkout", "-b", "other")
	write(t, dir, "committed.txt", "other side\n")
	git(t, dir, "commit", "-am", "other")
	git(t, dir, "checkout", "main")
	write(t, dir, "committed.txt", "main side\n")
	git(t, dir, "commit", "-am", "main")

	// The merge must fail *because of the conflict*. An earlier version of this
	// test ran the merge without an identity, so Git refused to commit at all,
	// the tree stayed clean, and the assertion below failed for a reason that
	// had nothing to do with conflict counting.
	if out := gitExpectingFailure(t, dir, "merge", "other"); !strings.Contains(strings.ToLower(out), "conflict") {
		t.Fatalf("merge failed for some reason other than a conflict:\n%s", out)
	}

	status := InspectGit(t.Context(), dir)
	if status.Conflicted != 1 {
		t.Errorf("conflicted = %d, want 1 (status: %+v)", status.Conflicted, status)
	}
}

func TestInspectGitTreatsADetachedHeadAsAReadableBranchName(t *testing.T) {
	dir := newRepo(t)
	head := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
	git(t, dir, "checkout", "--detach", head)

	status := InspectGit(t.Context(), dir)
	if status.Branch != "detached HEAD" {
		t.Errorf("branch = %q, want %q rather than Git's own %q", status.Branch, "detached HEAD", "(detached)")
	}
}

func TestInspectGitOutsideARepositoryIsNotAnError(t *testing.T) {
	// This is the documented contract and the one most likely to regress into
	// an error string, because Git exits non-zero for it. A status bar that
	// showed "fatal: not a git repository" in every non-repository directory
	// would be reporting normality as a fault.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	status := InspectGit(t.Context(), t.TempDir())
	if status.InRepository {
		t.Error("a plain directory is not a repository")
	}
	if status.Error != "" {
		t.Errorf("being outside a repository is a normal state, not an error: %q", status.Error)
	}
}

func TestInspectGitReportsACancelledContextAsATimeout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	status := InspectGit(ctx, newRepo(t))
	if status.Error == "" {
		t.Error("a cancelled inspection must say so rather than reporting a clean repository")
	}
}

func TestLimitedBufferStopsAtItsLimitAndSaysSo(t *testing.T) {
	// The buffer must keep reporting the full write length to its caller: an
	// io.Writer that returns a short count without an error makes exec treat
	// the pipe as broken, which would turn a large status into a command
	// failure rather than the truncation notice InspectGit reports.
	buffer := &limitedBuffer{limit: 10}
	n, err := buffer.Write([]byte("0123456789abcdef"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 16 {
		t.Errorf("Write returned %d, want the full 16 it was given", n)
	}
	if !buffer.truncated {
		t.Error("exceeding the limit must be recorded")
	}
	if got := buffer.String(); got != "0123456789" {
		t.Errorf("buffered %q, want the first 10 bytes only", got)
	}

	// A second write past a full buffer must not panic on a negative remainder.
	if n, err = buffer.Write([]byte("more")); err != nil || n != 4 {
		t.Errorf("second write returned (%d, %v), want (4, nil)", n, err)
	}
	if got := buffer.String(); got != "0123456789" {
		t.Errorf("a full buffer must accept nothing further, got %q", got)
	}
}

func TestCompactErrorCollapsesAndBounds(t *testing.T) {
	// Git errors are multi-line and go into a single status row.
	if got := compactError("fatal: something\n  went   wrong\n"); got != "fatal: something went wrong" {
		t.Errorf("compactError = %q", got)
	}
	long := compactError(strings.Repeat("x", 400))
	if len([]rune(long)) != 241 || !strings.HasSuffix(long, "…") {
		t.Errorf("a long error must be bounded and marked, got %d runes ending %q", len([]rune(long)), long[len(long)-3:])
	}
	if got := compactError(""); got != "" {
		t.Errorf("compactError(\"\") = %q, want empty", got)
	}
}
