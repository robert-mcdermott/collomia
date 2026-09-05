package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/taskmode"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

type memoryCompletionStore struct {
	raw  json.RawMessage
	fail error
}

func (s *memoryCompletionStore) LoadCompletion() json.RawMessage { return s.raw }
func (s *memoryCompletionStore) SaveCompletion(raw json.RawMessage) error {
	if s.fail != nil {
		return s.fail
	}
	s.raw = append(json.RawMessage(nil), raw...)
	return nil
}
func TestRecoveryDoesNotRestoreReceiptsOrDropDeliverables(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "report.txt")
	if err := os.WriteFile(path, []byte("BEFORE"), 0600); err != nil {
		t.Fatal(err)
	}
	store := &memoryCompletionStore{}
	board := plan.NewBoard()
	c := newCompletionController(board, workspace, false, taskmode.Work)
	if err := c.restoreCompletion(store); err != nil {
		t.Fatal(err)
	}
	c.observe(artifactObservation(t, workspace, path))
	if err := c.saveRecovery(false); err != nil {
		t.Fatal(err)
	} // stop after validation, before done
	if strings.Contains(string(store.raw), "digest") {
		t.Fatal("persisted a validation receipt")
	}
	if err := os.WriteFile(path, []byte("AFTER"), 0600); err != nil {
		t.Fatal(err)
	}
	// A replacement plan cannot erase the prior deliverable obligation.
	next := newCompletionController(plan.NewBoard(), workspace, false, taskmode.Work)
	if err := next.restoreCompletion(store); err != nil {
		t.Fatal(err)
	}
	if decision := next.assess(); decision.done || !strings.Contains(decision.notice, "report.txt") {
		t.Fatalf("lost deliverable: %+v", decision)
	}
	next.observe(artifactObservation(t, workspace, path))
	if decision := next.assess(); !decision.done {
		t.Fatalf("fresh receipt did not close obligation: %+v", decision)
	}
}
func TestRecoveryWriteAheadAndReadInspection(t *testing.T) {
	store := &memoryCompletionStore{}
	c := newCompletionController(plan.NewBoard(), t.TempDir(), false, taskmode.Developer)
	if err := c.restoreCompletion(store); err != nil {
		t.Fatal(err)
	}
	if err := c.beginEffect("run_command", tools.Action{Risk: tools.RiskExecute, Summary: "external operation"}, "key"); err != nil {
		t.Fatal(err)
	}
	next := newCompletionController(plan.NewBoard(), c.workspace, false, taskmode.Developer)
	if err := next.restoreCompletion(store); err != nil {
		t.Fatal(err)
	}
	if err := next.beginEffect("read_file", tools.Action{Risk: tools.RiskRead}, "read"); err != nil {
		t.Fatalf("inspection blocked: %v", err)
	}
	if err := next.beginEffect("run_command", tools.Action{Risk: tools.RiskExecute}, "key"); err == nil {
		t.Fatal("interrupted effect replay allowed")
	}
	if next.assess().done {
		t.Fatal("uncertain effect completed")
	}
	store.fail = errors.New("disk full")
	fresh := newCompletionController(plan.NewBoard(), c.workspace, false, taskmode.Work)
	fresh.store = store
	if err := fresh.beginEffect("write_file", tools.Action{Risk: tools.RiskWrite, Paths: []string{"report.txt"}}, "write"); !errors.Is(err, store.fail) {
		t.Fatal("write-ahead persistence failure hidden")
	}
}
func TestRecoveryFailureIDsCannotOverwriteEarlierTurn(t *testing.T) {
	store := &memoryCompletionStore{}
	c := newCompletionController(plan.NewBoard(), t.TempDir(), false, taskmode.Work)
	c.store = store
	c.observe(toolObservation{CallID: "call", Name: "read_file", Failed: true, RetryKey: "first"})
	if err := c.saveRecovery(false); err != nil {
		t.Fatal(err)
	}
	next := newCompletionController(plan.NewBoard(), c.workspace, false, taskmode.Work)
	if err := next.restoreCompletion(store); err != nil {
		t.Fatal(err)
	}
	next.observe(toolObservation{CallID: "call", Name: "read_file", Failed: true, RetryKey: "second"})
	if len(next.failures) != 2 || next.failures[0].id == next.failures[1].id {
		t.Fatal("new failure erased the old obligation")
	}
	next.observe(toolObservation{CallID: "retry", Name: "read_file", RetryKey: "first"})
	if len(next.failures) != 1 || next.failures[0].retryKey != "second" {
		t.Fatal("retry recovered an unrelated failure")
	}
}

func TestRecoveryModeCannotBypassObligationsAndReadEffectsStayReadable(t *testing.T) {
	store := &memoryCompletionStore{}
	c := newCompletionController(plan.NewBoard(), t.TempDir(), false, taskmode.Work)
	c.store = store
	c.markDirty([]string{"changed.txt"})
	if err := c.saveRecovery(false); err != nil {
		t.Fatal(err)
	}
	other := newCompletionController(plan.NewBoard(), c.workspace, false, taskmode.Developer)
	if err := other.restoreCompletion(store); err == nil {
		t.Fatal("mode switch bypassed unfinished Work")
	}
	c.pending = &pendingEffect{Tool: "run_command"}
	for _, name := range []string{"read_file", "read_session", "git_status", "web_fetch", "load_skill", "ask_user"} {
		if err := c.beginEffect(name, tools.Action{Risk: tools.RiskExternal}, "read"); err != nil {
			t.Fatalf("safe inspection %s blocked: %v", name, err)
		}
	}
	if !executionEffects("opaque_write", tools.Action{Risk: tools.RiskWrite, Paths: []string{"file"}}).Unknown {
		t.Fatal("opaque write claimed a complete effect contract")
	}
}
