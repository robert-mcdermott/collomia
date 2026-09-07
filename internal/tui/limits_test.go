package tui

import (
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/agent"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

func TestLimitsCanChangeWhileBusy(t *testing.T) {
	m := newTestModel(t)
	m.busy = true
	m.slash("/limits 500 30")
	noProgress, total := m.runtime.Agent.ExecutionLimits()
	if noProgress != 30 || total != 500 {
		t.Fatalf("limits=%d/%d", noProgress, total)
	}
	m.slash("/limits 0")
	_, total = m.runtime.Agent.ExecutionLimits()
	if total != 500 {
		t.Fatal("invalid edit changed live limit")
	}
}

func TestExecutionLimitIsReportedAsAPause(t *testing.T) {
	for _, err := range []error{agent.ErrIterationBudgetExceeded, agent.ErrAggregateBudgetExceeded} {
		m := newTestModel(t)
		updated, _ := m.Update(runMsg{done: true, err: err})
		m = updated.(Model)
		last := m.blocks[len(m.blocks)-1]
		if last.role != "system" || !strings.Contains(last.content, "Paused at execution limit") || strings.Contains(last.content, "Blocked") {
			t.Fatalf("execution limit presented as failed work: %+v", last)
		}
	}
}

func TestEmptyProviderDoesNotLabelWorkBlocked(t *testing.T) {
	m := newTestModel(t)
	err := (provider.Response{Stop: "stop"}).CompletionError("fixture")
	updated, _ := m.Update(runMsg{done: true, err: err})
	m = updated.(Model)
	last := m.blocks[len(m.blocks)-1]
	if last.role != "system" || !strings.Contains(last.content, "Provider response unavailable") || strings.Contains(last.content, "Blocked") {
		t.Fatalf("misleading status: %+v", last)
	}
}
