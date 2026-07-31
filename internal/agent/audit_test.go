package agent

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/audit"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// isolateUserRoot points ~/.collomia at a temporary directory so a test can
// exercise the real ledger location without touching the developer's own.
func isolateUserRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	}
	return dir
}

// TestAuditLedgerHasOneAttachmentSite fails when a second caller opens a
// ledger and wires it to a permission manager by hand.
//
// This is not style enforcement. Both delegated-agent construction paths used
// to do it themselves, and both dropped audit.Open's error — so a child whose
// ledger could not be opened ran completely unaudited while the primary
// session's identical failure was reported as a startup warning. Every field
// that has to be remembered (redaction, the failure report, and the session,
// actor, and task identity) is a field the next copy will forget. A new
// caller belongs in Agent.attachLedger.
func TestAuditLedgerHasOneAttachmentSite(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		// The single attachment site, and the primary session's construction
		// of the ledger it hands to the permission manager.
		filepath.Join("internal", "agent", "agent.go"): true,
		filepath.Join("internal", "app", "app.go"):     true,
	}
	var offenders []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "dist" || name == "collo" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !strings.Contains(string(body), "audit.Open(") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if !allowed[rel] {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("audit.Open is called outside Agent.attachLedger in %v; route it there so identity and failure reporting cannot drift", offenders)
	}
}

func TestDelegatedEntriesNameTheAgentAndTaskThatWroteThem(t *testing.T) {
	isolateUserRoot(t)
	workspace := t.TempDir()
	parent := New(Options{
		Workspace: workspace, SessionID: "sess-42",
		AuditRedact: func(text string) string { return text },
	})
	manager := permission.New(appconfig.Permissions{Mode: "autopilot"}, nil)
	parent.attachLedger(manager, workspace, audit.AgentActor("reviewer"), "task-3")

	action := tools.Action{Risk: tools.RiskRead, Summary: "read: main.go", Paths: []string{"main.go"}}
	if _, err := manager.Authorize(t.Context(), "read_file", action); err != nil {
		t.Fatal(err)
	}
	manager.RecordOutcome("read_file", action, nil)

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
		if entry.Actor != "agent:reviewer" {
			t.Errorf("entry is not attributable to the delegated agent: %+v", entry)
		}
		if entry.Task != "task-3" {
			t.Errorf("entry does not name the task: %+v", entry)
		}
		if entry.Session != "sess-42" {
			t.Errorf("entry does not name the session: %+v", entry)
		}
	}
}

// A child that cannot open a ledger is an unaudited actor, and used to be a
// completely silent one.
func TestAnUnopenableChildLedgerIsReportedRatherThanDropped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a read-only home directory is the Unix way to force the failure")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	home := isolateUserRoot(t)
	// A file where ~/.collomia belongs makes MkdirAll fail.
	if err := os.WriteFile(filepath.Join(home, ".collomia"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	var reported []error
	parent := New(Options{
		Workspace: home, SessionID: "sess-1",
		AuditFailure: func(err error) { reported = append(reported, err) },
	})
	manager := permission.New(appconfig.Permissions{Mode: "autopilot"}, nil)
	parent.attachLedger(manager, home, audit.AgentActor("reviewer"), "task-1")
	if len(reported) != 1 {
		t.Fatalf("want one report of the unavailable ledger, got %v", reported)
	}
	if !strings.Contains(reported[0].Error(), "agent:reviewer") {
		t.Errorf("the report must name the actor that will go unaudited: %v", reported[0])
	}
}
