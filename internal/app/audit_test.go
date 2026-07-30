package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/audit"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// The primary session's ledger is wired here rather than in the agent, so its
// identity and its failure route need their own coverage: the delegated path's
// guard test cannot see this one.

func TestPrimaryDecisionsAreAttributedToTheSession(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	runtime, err := New(context.Background(), Options{Workspace: workspace, Autonomy: "autopilot"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	action := tools.Action{Risk: tools.RiskRead, Summary: "read: main.go", Paths: []string{"main.go"}}
	if _, err := runtime.Permissions.Authorize(t.Context(), "read_file", action); err != nil {
		t.Fatal(err)
	}
	runtime.Permissions.RecordOutcome("read_file", action, nil)

	dir, err := audit.Dir()
	if err != nil {
		t.Fatal(err)
	}
	report, err := audit.Read(filepath.Join(dir, audit.FileName(workspace)), audit.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 2 {
		t.Fatalf("want a decision and an outcome, got %d entries", len(report.Entries))
	}
	for _, entry := range report.Entries {
		if entry.Actor != audit.ActorPrimary {
			t.Errorf("primary entry is not attributed: %+v", entry)
		}
		if entry.Session != runtime.Session.Meta.ID {
			t.Errorf("primary entry does not name its session: %+v", entry)
		}
	}
	if !report.Complete() {
		t.Error("a freshly written ledger must read as complete")
	}
	if failures, _, _ := runtime.AuditHealth(); failures != 0 {
		t.Errorf("a healthy session reports %d audit failures", failures)
	}
}

// A ledger that fails mid-session must make the session say so, because the
// startup warning has already been shown and dismissed by then.
func TestALedgerFailureLatchesOnTheRuntime(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	runtime, err := New(context.Background(), Options{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if runtime.Audit == nil {
		t.Fatal("the primary session did not get a ledger")
	}
	if failures, _, _ := runtime.AuditHealth(); failures != 0 {
		t.Fatalf("a fresh session starts with %d failures", failures)
	}
	// Report through the ledger's own hook, which is the route a real write
	// failure takes.
	runtime.Audit.OnFailure(errors.New("no space left on device"))
	failures, first, at := runtime.AuditHealth()
	if failures != 1 || first == nil || at.IsZero() {
		t.Fatalf("the failure did not latch: %d %v %v", failures, first, at)
	}
	// Recovery does not erase the latch: entries lost earlier stay lost, and
	// a session that reported a complete record after losing entries would be
	// worse than one that never claimed anything.
	runtime.Audit.OnFailure(nil)
	if failures, _, _ := runtime.AuditHealth(); failures != 1 {
		t.Errorf("recovery cleared the record of the loss, leaving %d", failures)
	}
}
