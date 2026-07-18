// Package diffmodel tracks every file change the agent makes — the before
// and after of each mutation — independent of how it is rendered. It powers
// unified diff output, the approval preview, and checkpoint/undo.
package diffmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
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
	Path   string
	Op     string // write, edit, patch, delete
	Before *string
	After  *string
	Time   time.Time
}

// Tracker records mutations, keeps each file's base (first-seen) content for
// session-level diffs, and supports undoing the most recent mutation.
type Tracker struct {
	mu      sync.Mutex
	base    map[string]*string
	history []Snapshot
}

func NewTracker() *Tracker { return &Tracker{base: map[string]*string{}} }

// Record stores one mutation. Call after the mutation succeeds.
func (t *Tracker) Record(path, op string, before, after *string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, seen := t.base[path]; !seen {
		t.base[path] = before
	}
	t.history = append(t.history, Snapshot{Path: path, Op: op, Before: before, After: after, Time: time.Now().UTC()})
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
	for _, path := range t.Changed() {
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
		out.WriteString(Unified(name, base, current))
	}
	return out.String()
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
	current, readErr := os.ReadFile(last.Path)
	switch {
	case last.After == nil:
		if readErr == nil {
			return Snapshot{}, fmt.Errorf("undo blocked: %s exists but the last operation deleted it", last.Path)
		}
	case readErr != nil:
		return Snapshot{}, fmt.Errorf("undo blocked: cannot read %s: %v", last.Path, readErr)
	case string(current) != *last.After:
		return Snapshot{}, fmt.Errorf("undo blocked: %s changed outside the agent since the last operation", last.Path)
	}
	if last.Before == nil {
		if err := os.Remove(last.Path); err != nil {
			return Snapshot{}, err
		}
	} else {
		if err := os.WriteFile(last.Path, []byte(*last.Before), 0o644); err != nil {
			return Snapshot{}, err
		}
	}
	t.history = t.history[:len(t.history)-1]
	return last, nil
}
