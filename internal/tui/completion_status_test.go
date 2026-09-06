package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/agent"
)

func TestVerificationIncompleteIsNotBlocked(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(runMsg{done: true, err: fmt.Errorf("%w: check output.txt", agent.ErrGoalNeedsVerification)})
	m = updated.(Model)
	last := m.blocks[len(m.blocks)-1]
	if last.role != "system" || !strings.Contains(last.content, "Verification incomplete") || strings.Contains(last.content, "Blocked") {
		t.Fatalf("misleading status: %+v", last)
	}
}
