// Package diffmodel tracks every file change the agent makes — the before
// and after of each mutation — independent of how it is rendered. It powers
// unified diff output, the approval preview, and checkpoint/undo.
package diffmodel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robert-mcdermott/collomia/internal/safefile"
)

// Unified renders a unified diff between two contents. It uses a simple
// longest-common-subsequence walk with a size guard; very large inputs fall
// back to a whole-file replacement rendering.
func Unified(name string, before, after string) string {
	if before == after {
		return ""
	}
	a := splitLines(before)
	b := splitLines(after)
	var body strings.Builder
	fmt.Fprintf(&body, "--- a/%s\n+++ b/%s\n", name, name)
	const sizeGuard = 4000
	if len(a)*len(b) > sizeGuard*sizeGuard {
		fmt.Fprintf(&body, "@@ -1,%d +1,%d @@\n", len(a), len(b))
		for _, line := range a {
			body.WriteString("-" + line + "\n")
		}
		for _, line := range b {
			body.WriteString("+" + line + "\n")
		}
		return body.String()
	}
	ops := lcsOps(a, b)
	// Group ops into hunks with up to 3 context lines.
	const context = 3
	i := 0
	for i < len(ops) {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := i
		for start > 0 && i-start < context && ops[start-1].kind == ' ' {
			start--
		}
		end := i
		gap := 0
		for end < len(ops) {
			if ops[end].kind == ' ' {
				gap++
				if gap > context*2 {
					end -= gap - context
					break
				}
			} else {
				gap = 0
			}
			end++
		}
		if end > len(ops) {
			end = len(ops)
		}
		aStart, bStart := ops[start].aLine, ops[start].bLine
		var aCount, bCount int
		var hunk strings.Builder
		for _, op := range ops[start:end] {
			switch op.kind {
			case ' ':
				aCount++
				bCount++
			case '-':
				aCount++
			case '+':
				bCount++
			}
			hunk.WriteString(string(op.kind) + op.text + "\n")
		}
		fmt.Fprintf(&body, "@@ -%d,%d +%d,%d @@\n%s", aStart+1, aCount, bStart+1, bCount, hunk.String())
		i = end
	}
	return body.String()
}

type diffOp struct {
	kind         byte // ' ', '-', '+'
	text         string
	aLine, bLine int
}

func lcsOps(a, b []string) []diffOp {
	// Classic DP table; guarded by the caller for size.
	n, m := len(a), len(b)
	table := make([][]int32, n+1)
	for i := range table {
		table[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else {
				table[i][j] = max32(table[i+1][j], table[i][j+1])
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i], i, j})
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			ops = append(ops, diffOp{'-', a[i], i, j})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j], i, j})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i], i, j})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j], i, j})
	}
	return ops
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// Snapshot is one recorded mutation: the state of a file before and after.
// Before/After of nil mean the file did not exist on that side.
type Snapshot struct {
	Path       string
	Op         string // write, edit, patch, delete
	Before     *string
	After      *string
	BeforeMode os.FileMode
	AfterMode  os.FileMode
	Time       time.Time
	// Turn is the 1-based conversational turn the mutation happened during,
	// so a checkpoint restore can select exactly the mutations that followed a
	// completed turn. Zero means the tracker was never told about turns, which
	// is the case for standalone diff callers.
	Turn int
}

// FileDiff is one session-touched file rendered against its first-seen base.
// Before and After support alternate review layouts; Unified remains the
// canonical representation used by approvals and headless output.
type FileDiff struct {
	Path, Name    string
	Before, After string
	Unified       string
}

// AlignedLine is one row in a side-by-side comparison. A zero line number
// means that side has no corresponding line. Kind is ' ', '-', or '+'.
type AlignedLine struct {
	LeftNumber, RightNumber int
	Left, Right             string
	Kind                    byte
}

// Tracker records mutations, keeps each file's base (first-seen) content for
// session-level diffs, and supports undoing the most recent mutation.
type Tracker struct {
	mu      sync.Mutex
	root    string
	rootID  safefile.RootIdentity
	rootErr error
	base    map[string]*string
	history []Snapshot
	// completedTurns is how many conversational turns have finished. A
	// mutation recorded now belongs to the turn after them.
	completedTurns int
}

// NewTracker optionally anchors undo beneath a workspace root. Built-in tools
// always provide it; the variadic form keeps standalone diff tests and callers
// source-compatible.
func NewTracker(root ...string) *Tracker {
	tracker := &Tracker{base: map[string]*string{}}
	if len(root) > 0 {
		tracker.root = root[0]
		tracker.rootID, tracker.rootErr = safefile.CaptureRootIdentity(root[0])
	}
	return tracker
}

// Record stores one mutation. Call after the mutation succeeds.
func (t *Tracker) Record(path, op string, before, after *string) {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	t.RecordWithMode(path, op, before, after, mode, mode)
}

// RecordWithMode stores the content and permission mode on both sides of a
// mutation so undo can restore the prior file without silently changing its
// executable or privacy bits.
func (t *Tracker) RecordWithMode(path, op string, before, after *string, beforeMode, afterMode os.FileMode) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, seen := t.base[path]; !seen {
		t.base[path] = before
	}
	t.history = append(t.history, Snapshot{Path: path, Op: op, Before: before, After: after, BeforeMode: beforeMode.Perm(), AfterMode: afterMode.Perm(), Time: time.Now().UTC(), Turn: t.completedTurns + 1})
}

// CompleteTurn records that a conversational turn finished, so later mutations
// belong to the next one. The caller is whichever surface observes the durable
// turn boundary; the tracker never infers one from elapsed time.
func (t *Tracker) CompleteTurn() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.completedTurns++
}

// SetCompletedTurns aligns the tracker's turn numbering with a session that
// already has completed turns — a resume, a rewind, or a switch to another
// session. Mutations recorded before the call keep the turn they were made in,
// which is what keeps a restore honest: only this process's own writes are
// reversible, and a restore to a turn from an earlier process finds nothing to
// reverse rather than claiming to have undone it.
func (t *Tracker) SetCompletedTurns(turns int) {
	if turns < 0 {
		turns = 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.completedTurns = turns
}

// CompletedTurns reports the tracker's current turn accounting.
func (t *Tracker) CompletedTurns() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.completedTurns
}

// Changed lists the files touched this session, in first-touch order.
func (t *Tracker) Changed() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	seen := map[string]bool{}
	var paths []string
	for _, snapshot := range t.history {
		if !seen[snapshot.Path] {
			seen[snapshot.Path] = true
			paths = append(paths, snapshot.Path)
		}
	}
	return paths
}

// Diff renders the session diff: each touched file's base content against
// what is on disk now.
func (t *Tracker) Diff(workspace string) string {
	var out strings.Builder
	for _, file := range t.FileDiffs(workspace) {
		out.WriteString(file.Unified)
	}
	return out.String()
}

// FileDiffs returns structured per-file session diffs in first-touch order.
// Reading current content is deliberately best-effort, matching Diff: a
// deleted or unreadable current path compares as empty content.
func (t *Tracker) FileDiffs(workspace string) []FileDiff {
	paths := t.Changed()
	files := make([]FileDiff, 0, len(paths))
	for _, path := range paths {
		t.mu.Lock()
		basePtr := t.base[path]
		t.mu.Unlock()
		base := ""
		if basePtr != nil {
			base = *basePtr
		}
		current := ""
		if data, err := os.ReadFile(path); err == nil {
			current = string(data)
		}
		name := path
		if rel, err := filepath.Rel(workspace, path); err == nil && !strings.HasPrefix(rel, "..") {
			name = filepath.ToSlash(rel)
		}
		unified := Unified(name, base, current)
		if unified != "" {
			files = append(files, FileDiff{Path: path, Name: name, Before: base, After: current, Unified: unified})
		}
	}
	return files
}

// Align returns stable rows for a side-by-side viewer. It uses the same LCS
// walk as Unified and falls back to a bounded whole-file replacement for very
// large inputs rather than allocating an unbounded dynamic-programming table.
func Align(before, after string) []AlignedLine {
	a := splitLines(before)
	b := splitLines(after)
	const sizeGuard = 4000
	var ops []diffOp
	if len(a)*len(b) > sizeGuard*sizeGuard {
		for i, line := range a {
			ops = append(ops, diffOp{kind: '-', text: line, aLine: i})
		}
		for i, line := range b {
			ops = append(ops, diffOp{kind: '+', text: line, aLine: len(a), bLine: i})
		}
	} else {
		ops = lcsOps(a, b)
	}
	rows := make([]AlignedLine, 0, len(ops))
	for _, op := range ops {
		row := AlignedLine{Kind: op.kind}
		switch op.kind {
		case ' ':
			row.LeftNumber, row.RightNumber = op.aLine+1, op.bLine+1
			row.Left, row.Right = op.text, op.text
		case '-':
			row.LeftNumber, row.Left = op.aLine+1, op.text
		case '+':
			row.RightNumber, row.Right = op.bLine+1, op.text
		}
		rows = append(rows, row)
	}
	return rows
}

// Undo reverts the most recent mutation by restoring the file's prior state,
// and removes it from history. It refuses if the file changed since the
// mutation (an external edit would be clobbered).
func (t *Tracker) Undo() (Snapshot, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.history) == 0 {
		return Snapshot{}, fmt.Errorf("nothing to undo")
	}
	last := t.history[len(t.history)-1]
	target, err := t.mutationTarget(last.Path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("undo blocked: secure target: %w", err)
	}
	defer target.Close()
	current, readErr := target.ReadFile()
	switch {
	case last.After == nil:
		if readErr == nil {
			return Snapshot{}, fmt.Errorf("undo blocked: %s exists but the last operation deleted it", last.Path)
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			return Snapshot{}, fmt.Errorf("undo blocked: cannot inspect %s: %v", last.Path, readErr)
		}
	case readErr != nil:
		return Snapshot{}, fmt.Errorf("undo blocked: cannot read %s: %v", last.Path, readErr)
	case string(current) != *last.After:
		return Snapshot{}, fmt.Errorf("undo blocked: %s changed outside the agent since the last operation", last.Path)
	}
	if last.Before == nil {
		if err := target.Remove(); err != nil {
			return Snapshot{}, err
		}
	} else {
		mode := last.BeforeMode
		if mode == 0 {
			mode = 0o644
		}
		if err := target.Replace([]byte(*last.Before), mode); err != nil {
			return Snapshot{}, err
		}
	}
	t.history = t.history[:len(t.history)-1]
	return last, nil
}

// Restore reports what a checkpoint restore reversed. Files are listed in the
// order they were first touched after the checkpoint.
type Restore struct {
	Turn      int
	Files     []string
	Mutations int
}

// DriftError refuses a restore because the workspace moved underneath it. It
// names every file rather than the first one found: a user deciding what to do
// next needs the whole list, and discovering the second file only after acting
// on the first is the same trap as a partial restore.
type DriftError struct {
	Turn  int
	Files []string
}

func (e *DriftError) Error() string {
	return fmt.Sprintf("restore blocked: %s changed outside the agent since the checkpoint, so restoring to turn %d would discard those changes", strings.Join(e.Files, ", "), e.Turn)
}

// reversal is the collapsed plan for one file: what the last recorded mutation
// left on disk, and what the first one found there. Collapsing per file rather
// than replaying each mutation backwards means one write per file instead of
// one per mutation, so a file touched twenty times cannot be left halfway.
type reversal struct {
	path       string
	expected   *string
	target     *string
	targetMode os.FileMode
	mutations  int
}

// PendingSince reports how many files and mutations a restore to the given
// completed turn would reverse, without touching the workspace. It is what
// lets a picker say what a choice costs before the choice is made.
func (t *Tracker) PendingSince(turn int) (files, mutations int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	plan := t.planReversals(turn)
	for _, r := range plan {
		mutations += r.mutations
	}
	return len(plan), mutations
}

// VerifyRestore reports whether a restore to the given completed turn would
// succeed, without touching the workspace. A coupled conversation-plus-
// workspace restore checks this before it branches the conversation, so the
// failure this control exists for — a file changed outside the agent — leaves
// nothing at all changed rather than a conversation that moved alone.
func (t *Tracker) VerifyRestore(turn int) error {
	if turn < 0 {
		return errors.New("restore turn must not be negative")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	targets, err := t.verifyLocked(t.planReversals(turn), turn)
	for _, target := range targets {
		if target != nil {
			_ = target.Close()
		}
	}
	return err
}

// verifyLocked proves every file in the plan still holds exactly what the agent
// last wrote there, and returns the opened targets so the caller can write
// through the same verified handles. Callers hold the lock.
func (t *Tracker) verifyLocked(plan []reversal, turn int) ([]*safefile.Target, error) {
	targets := make([]*safefile.Target, len(plan))
	var drifted []string
	for i, r := range plan {
		target, err := t.mutationTarget(r.path)
		if err != nil {
			return targets, fmt.Errorf("restore blocked: secure target for %s: %w", r.path, err)
		}
		targets[i] = target
		current, readErr := target.ReadFile()
		switch {
		case r.expected == nil:
			// The last recorded mutation deleted the file. Anything present
			// now was put there after the agent stopped touching it.
			if readErr == nil {
				drifted = append(drifted, r.path)
				continue
			}
			if !errors.Is(readErr, os.ErrNotExist) {
				return targets, fmt.Errorf("restore blocked: cannot inspect %s: %v", r.path, readErr)
			}
		case readErr != nil:
			if errors.Is(readErr, os.ErrNotExist) {
				drifted = append(drifted, r.path)
				continue
			}
			return targets, fmt.Errorf("restore blocked: cannot read %s: %v", r.path, readErr)
		case string(current) != *r.expected:
			drifted = append(drifted, r.path)
		}
	}
	if len(drifted) > 0 {
		sort.Strings(drifted)
		return targets, &DriftError{Turn: turn, Files: drifted}
	}
	return targets, nil
}

// RestoreTo reverses every tracked mutation made after the given completed
// turn, returning the workspace to its state at that checkpoint.
//
// It verifies the whole plan before writing anything and refuses outright if
// any file changed outside the agent, because a restore that half-applied and
// then stopped leaves a workspace in a state neither the user nor the
// conversation describes. Only mutations this process recorded are reversible:
// the tracker is in-memory, so a restore to a turn from an earlier process
// finds nothing to reverse rather than pretending otherwise, and shell,
// network, and other external side effects are never reversed at all.
func (t *Tracker) RestoreTo(turn int) (Restore, error) {
	if turn < 0 {
		return Restore{}, errors.New("restore turn must not be negative")
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	plan := t.planReversals(turn)
	if len(plan) == 0 {
		t.completedTurns = turn
		return Restore{Turn: turn}, nil
	}

	targets, err := t.verifyLocked(plan, turn)
	defer func() {
		for _, target := range targets {
			if target != nil {
				_ = target.Close()
			}
		}
	}()
	if err != nil {
		return Restore{}, err
	}

	// Apply. Verification has already ruled out the failure this operation
	// exists to avoid, so what remains is ordinary I/O.
	result := Restore{Turn: turn}
	for i, r := range plan {
		var err error
		if r.target == nil {
			err = targets[i].Remove()
		} else {
			mode := r.targetMode
			if mode == 0 {
				mode = 0o644
			}
			err = targets[i].Replace([]byte(*r.target), mode)
		}
		if err != nil {
			return result, fmt.Errorf("restore of %s failed after %d of %d files were restored: %w", r.path, len(result.Files), len(plan), err)
		}
		result.Files = append(result.Files, r.path)
		result.Mutations += r.mutations
	}

	kept := make([]Snapshot, 0, len(t.history))
	for _, snapshot := range t.history {
		if snapshot.Turn <= turn {
			kept = append(kept, snapshot)
		}
	}
	t.history = kept
	t.completedTurns = turn
	return result, nil
}

// planReversals collapses the mutations recorded after a completed turn into
// one reversal per file, in first-touch order. Callers hold the lock.
func (t *Tracker) planReversals(turn int) []reversal {
	byPath := map[string]*reversal{}
	var order []*reversal
	for _, snapshot := range t.history {
		if snapshot.Turn <= turn {
			continue
		}
		r, seen := byPath[snapshot.Path]
		if !seen {
			r = &reversal{path: snapshot.Path, target: snapshot.Before, targetMode: snapshot.BeforeMode}
			byPath[snapshot.Path] = r
			order = append(order, r)
		}
		r.expected = snapshot.After
		r.mutations++
	}
	plan := make([]reversal, 0, len(order))
	for _, r := range order {
		plan = append(plan, *r)
	}
	return plan
}

func (t *Tracker) mutationTarget(path string) (*safefile.Target, error) {
	if t.root != "" {
		rootAbs, rootErr := filepath.Abs(t.root)
		pathAbs, pathErr := filepath.Abs(path)
		if rootErr == nil && pathErr == nil {
			rel, relErr := filepath.Rel(rootAbs, pathAbs)
			inside := relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
			if inside {
				if t.rootErr != nil {
					return nil, fmt.Errorf("workspace mutation root identity unavailable: %w", t.rootErr)
				}
				target, err := safefile.Open(rootAbs, pathAbs)
				if err != nil {
					return nil, err
				}
				if t.rootID.Valid() {
					opened, identityErr := target.RootIdentity()
					if identityErr != nil || !t.rootID.Same(opened) {
						_ = target.Close()
						if identityErr != nil {
							return nil, identityErr
						}
						return nil, fmt.Errorf("workspace mutation root changed since startup")
					}
				}
				return target, nil
			}
		}
	}
	return safefile.OpenParent(path)
}
