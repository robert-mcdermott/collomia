// Package taskmode defines the user-selected task profile independently of
// execution strategy, planning state, and permission autonomy.
package taskmode

import (
	"fmt"
	"strings"
)

type Mode string

const (
	Developer Mode = "developer"
	Work      Mode = "work"
)

// Parse accepts an explicit mode. Empty input retains the backward-compatible
// Developer default for callers and legacy sessions that predate task_mode.
func Parse(value string) (Mode, error) {
	switch normalized := Mode(strings.ToLower(strings.TrimSpace(value))); normalized {
	case "", Developer:
		return Developer, nil
	case Work:
		return Work, nil
	default:
		return "", fmt.Errorf("unknown task mode %q (expected developer or work)", value)
	}
}

func (m Mode) String() string {
	if m == "" {
		return string(Developer)
	}
	return string(m)
}
