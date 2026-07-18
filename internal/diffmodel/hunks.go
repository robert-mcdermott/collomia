package diffmodel

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Hunk is one contiguous change region from a unified diff produced by
// Unified, in the exact hunk-header/line format that function writes.
// AStart/ACount describe the 1-based line range in the "before" content the
// hunk covers; Lines carries each hunk line with its ' '/'+'/'-' prefix.
type Hunk struct {
	AStart, ACount, BStart, BCount int
	Lines                          []string
}

var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+),(\d+) \+(\d+),(\d+) @@$`)

// ParseHunks splits a unified diff (as produced by Unified) into its
// individual hunks, so a caller can selectively keep or discard each one.
func ParseHunks(diff string) ([]Hunk, error) {
	var hunks []Hunk
	var current *Hunk
	for _, line := range strings.Split(diff, "\n") {
		if m := hunkHeaderRe.FindStringSubmatch(line); m != nil {
			if current != nil {
				hunks = append(hunks, *current)
			}
			aStart, _ := strconv.Atoi(m[1])
			aCount, _ := strconv.Atoi(m[2])
			bStart, _ := strconv.Atoi(m[3])
			bCount, _ := strconv.Atoi(m[4])
			current = &Hunk{AStart: aStart, ACount: aCount, BStart: bStart, BCount: bCount}
			continue
		}
		if current == nil {
			continue // file header lines ("--- a/x", "+++ b/x") before the first hunk
		}
		current.Lines = append(current.Lines, line)
	}
	if current != nil {
		hunks = append(hunks, *current)
	}
	// strings.Split leaves a trailing "" for the diff's final newline; drop
	// it from whichever hunk absorbed it.
	for i := range hunks {
		for len(hunks[i].Lines) > 0 && hunks[i].Lines[len(hunks[i].Lines)-1] == "" {
			hunks[i].Lines = hunks[i].Lines[:len(hunks[i].Lines)-1]
		}
	}
	if len(hunks) == 0 {
		return nil, errors.New("no hunks found in diff")
	}
	return hunks, nil
}

// ApplyHunks reconstructs file content from the original ("before") text,
// applying only the hunks marked true in keep and leaving the rest as they
// were originally — the same selection semantics as `git add -p`. Keeping
// every hunk reproduces the diff's "after" content exactly.
func ApplyHunks(before string, hunks []Hunk, keep []bool) (string, error) {
	if len(keep) != len(hunks) {
		return "", fmt.Errorf("keep length %d does not match hunk count %d", len(keep), len(hunks))
	}
	orig := splitLines(before)
	var out []string
	cursor := 0 // 0-based index into orig of the next line not yet copied
	for i, h := range hunks {
		end := h.AStart - 1
		if end < cursor {
			end = cursor
		}
		if end > len(orig) {
			end = len(orig)
		}
		out = append(out, orig[cursor:end]...)
		keepPrefixes := " -"
		if keep[i] {
			keepPrefixes = " +"
		}
		for _, line := range h.Lines {
			if line == "" || !strings.ContainsRune(keepPrefixes, rune(line[0])) {
				continue
			}
			out = append(out, line[1:])
		}
		cursor = h.AStart - 1 + h.ACount
		if cursor < 0 {
			cursor = 0
		}
	}
	if cursor < len(orig) {
		out = append(out, orig[cursor:]...)
	}
	if len(out) == 0 {
		return "", nil
	}
	return strings.Join(out, "\n") + "\n", nil
}
