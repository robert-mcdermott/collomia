// Package diffmodel tracks every file change the agent makes — the before
// and after of each mutation — independent of how it is rendered. It powers
// unified diff output, the approval preview, and checkpoint/undo.
package diffmodel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	t.history = append(t.history, Snapshot{Path: path, Op: op, Before: before, After: after, BeforeMode: beforeMode.Perm(), AfterMode: afterMode.Perm(), Time: time.Now().UTC()})
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
