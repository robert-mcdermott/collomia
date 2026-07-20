package permission

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/audit"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	return string(data), err
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

func TestPermissionModes(t *testing.T) {
	requests := 0
	m := New(appconfig.Permissions{Mode: "ask"}, func(_ context.Context, _ Request) (Decision, error) { requests++; return Decision{Allow: true}, nil })
	if _, err := m.Authorize(t.Context(), "read_file", tools.Action{Risk: tools.RiskRead}); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatal("workspace read should not prompt")
	}
	if _, err := m.Authorize(t.Context(), "write_file", tools.Action{Risk: tools.RiskWrite}); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests=%d", requests)
	}
}

func TestAutopilotOutsideRequiresExplicitGrant(t *testing.T) {
	action := tools.Action{Risk: tools.RiskWrite, Outside: true}
	without := New(appconfig.Permissions{Mode: "autopilot"}, nil)
	if _, err := without.Authorize(t.Context(), "write_file", action); !errors.Is(err, ErrDenied) {
		t.Fatalf("expected denial, got %v", err)
	}
	with := New(appconfig.Permissions{Mode: "autopilot", AllowOutsideWorkspace: true}, nil)
	if _, err := with.Authorize(t.Context(), "write_file", action); err != nil {
		t.Fatal(err)
	}
}

func TestDenyRuleBeatsAutopilot(t *testing.T) {
	m := New(appconfig.Permissions{Mode: "autopilot", Rules: []appconfig.Rule{
		{Action: "deny", Tool: "run_command", Command: "curl", Reason: "no downloads"},
	}}, nil)
	action := tools.Action{Risk: tools.RiskExecute, Summary: "run: curl evil.example", Executables: []string{"curl"}}
	grant, err := m.Authorize(t.Context(), "run_command", action)
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected denial, got %v", err)
	}
	if grant.Source != "rule" {
		t.Fatalf("source=%q", grant.Source)
	}
}

func TestAllowRuleSkipsPromptInAskMode(t *testing.T) {
	m := New(appconfig.Permissions{Mode: "ask", Rules: []appconfig.Rule{
		{Action: "allow", Tool: "run_command", Command: "go"},
	}}, nil)
	action := tools.Action{Risk: tools.RiskExecute, Summary: "run: go test", Executables: []string{"go"}}
	grant, err := m.Authorize(t.Context(), "run_command", action)
	if err != nil {
		t.Fatal(err)
	}
	if grant.Source != "rule" {
		t.Fatalf("source=%q", grant.Source)
	}
}

func TestAllowRuleDoesNotCoverUninspectableCommand(t *testing.T) {
	m := New(appconfig.Permissions{Mode: "ask", Rules: []appconfig.Rule{
		{Action: "allow", Tool: "run_command", Command: "*"},
	}}, nil)
	action := tools.Action{Risk: tools.RiskExecute, Summary: "run: go $(cat cmd)", Executables: []string{"go"}, Uninspectable: true, AnalysisReasons: []string{"command substitution"}}
	if _, err := m.Authorize(t.Context(), "run_command", action); !errors.Is(err, ErrDenied) {
		t.Fatalf("uninspectable command must not be auto-allowed, got %v", err)
	}
}

func TestUninspectableCommandPromptsEvenInAutopilot(t *testing.T) {
	prompted := false
	m := New(appconfig.Permissions{Mode: "autopilot"}, func(_ context.Context, r Request) (Decision, error) {
		prompted = true
		if r.Reason == "" {
			t.Error("prompt should carry the analysis reason")
		}
		return Decision{Allow: true}, nil
	})
	action := tools.Action{Risk: tools.RiskExecute, Summary: "run: eval $X", Uninspectable: true, AnalysisReasons: []string{"eval defeats static analysis"}}
	if _, err := m.Authorize(t.Context(), "run_command", action); err != nil {
		t.Fatal(err)
	}
	if !prompted {
		t.Fatal("autopilot must still prompt for uninspectable commands")
	}
}

func TestAlwaysDoesNotStickForUninspectableCommands(t *testing.T) {
	prompts := 0
	m := New(appconfig.Permissions{Mode: "ask"}, func(context.Context, Request) (Decision, error) {
		prompts++
		return Decision{Allow: true, Always: true}, nil
	})
	action := tools.Action{Risk: tools.RiskExecute, Uninspectable: true}
	for range 2 {
		if _, err := m.Authorize(t.Context(), "run_command", action); err != nil {
			t.Fatal(err)
		}
	}
	if prompts != 2 {
		t.Fatalf("prompts=%d; 'always' must not persist for uninspectable commands", prompts)
	}
}

func TestCatastrophicCommandCannotBeApproved(t *testing.T) {
	prompts := 0
	m := New(appconfig.Permissions{Mode: "autopilot", Rules: []appconfig.Rule{
		{Action: "allow", Tool: "run_command", Command: "*"},
	}}, func(context.Context, Request) (Decision, error) {
		prompts++
		return Decision{Allow: true}, nil
	})
	action := tools.Action{
		Risk: tools.RiskExecute, Summary: "run: rm -rf /",
		Executables: []string{"rm"}, HardDenyReasons: []string{"recursive rm targets protected root /"},
	}
	grant, err := m.Authorize(t.Context(), "run_command", action)
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("catastrophic command must be denied, got %v", err)
	}
	if grant.Source != "safety" {
		t.Fatalf("source=%q", grant.Source)
	}
	if prompts != 0 {
		t.Fatalf("catastrophic denial must not be presented as approvable; prompts=%d", prompts)
	}
}

func TestMandatoryConfirmationOverridesAutopilotAndAllowRule(t *testing.T) {
	prompts := 0
	m := New(appconfig.Permissions{Mode: "autopilot", Rules: []appconfig.Rule{
		{Action: "allow", Tool: "run_command", Command: "git"},
	}}, func(_ context.Context, request Request) (Decision, error) {
		prompts++
		if !strings.Contains(request.Reason, "one-time confirmation") {
			t.Errorf("reason=%q", request.Reason)
		}
		return Decision{Allow: true, Always: true}, nil
	})
	action := tools.Action{
		Risk: tools.RiskExecute, Summary: "run: git reset --hard",
		Executables: []string{"git"}, ConfirmReasons: []string{"git reset --hard can discard uncommitted work"},
	}
	for range 2 {
		if _, err := m.Authorize(t.Context(), "run_command", action); err != nil {
			t.Fatal(err)
		}
	}
	if prompts != 2 {
		t.Fatalf("one-time confirmations must never persist; prompts=%d", prompts)
	}
}

func TestPathScopedDenyRule(t *testing.T) {
	m := New(appconfig.Permissions{Mode: "autopilot", Rules: []appconfig.Rule{
		{Action: "deny", Path: "/workspace/secrets/**"},
	}}, nil)
	blocked := tools.Action{Risk: tools.RiskRead, Paths: []string{filepath.FromSlash("/workspace/secrets/key.pem")}}
	if _, err := m.Authorize(t.Context(), "read_file", blocked); !errors.Is(err, ErrDenied) {
		t.Fatalf("expected denial, got %v", err)
	}
	allowed := tools.Action{Risk: tools.RiskRead, Paths: []string{filepath.FromSlash("/workspace/main.go")}}
	if _, err := m.Authorize(t.Context(), "read_file", allowed); err != nil {
		t.Fatal(err)
	}
}

func TestReviewerVetoEscalatesToPrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh reviewer stubs")
	}
	prompted := false
	cfg := appconfig.Permissions{Mode: "autopilot", ReviewerCommand: `echo '{"decision":"deny","reason":"risky"}'`}
	m := New(cfg, func(_ context.Context, r Request) (Decision, error) {
		prompted = true
		if !strings.Contains(r.Reason, "risky") {
			t.Errorf("prompt should carry reviewer reason, got %q", r.Reason)
		}
		return Decision{Allow: true}, nil
	})
	action := tools.Action{Risk: tools.RiskExecute, Summary: "run: go generate", Executables: []string{"go"}}
	grant, err := m.Authorize(t.Context(), "run_command", action)
	if err != nil {
		t.Fatal(err)
	}
	if !prompted {
		t.Fatal("reviewer veto must escalate to the human")
	}
	if grant.Source != "interactive" {
		t.Fatalf("source=%q", grant.Source)
	}

	// A veto with no approver (headless) denies.
	headless := New(cfg, nil)
	if _, err := headless.Authorize(t.Context(), "run_command", action); !errors.Is(err, ErrDenied) {
		t.Fatalf("expected denial, got %v", err)
	}
}

func TestReviewerAllowKeepsAutoApproval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh reviewer stubs")
	}
	m := New(appconfig.Permissions{Mode: "autopilot", ReviewerCommand: `echo '{"decision":"allow"}'`}, nil)
	action := tools.Action{Risk: tools.RiskExecute, Summary: "run: go test", Executables: []string{"go"}}
	if _, err := m.Authorize(t.Context(), "run_command", action); err != nil {
		t.Fatal(err)
	}
}

func TestBrokenReviewerFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh reviewer stubs")
	}
	m := New(appconfig.Permissions{Mode: "autopilot", ReviewerCommand: "exit 3"}, nil)
	action := tools.Action{Risk: tools.RiskWrite, Summary: "write x"}
	if _, err := m.Authorize(t.Context(), "write_file", action); !errors.Is(err, ErrDenied) {
		t.Fatalf("a broken reviewer must not widen access, got %v", err)
	}
}

func TestDecisionsAreAudited(t *testing.T) {
	dir := t.TempDir()
	ledger := audit.OpenAt(filepath.Join(dir, "audit.jsonl"), "/workspace")
	m := New(appconfig.Permissions{Mode: "autopilot"}, nil)
	m.SetLedger(ledger)
	action := tools.Action{Risk: tools.RiskExecute, Summary: "run: go test", Executables: []string{"go"}}
	if _, err := m.Authorize(t.Context(), "run_command", action); err != nil {
		t.Fatal(err)
	}
	m.RecordOutcome("run_command", action, nil)
	data, err := readFile(ledger.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"kind":"decision"`, `"kind":"outcome"`, `"source":"mode"`, `"exec:go"`, `"outcome":"ok"`} {
		if !contains(data, want) {
			t.Errorf("ledger missing %s:\n%s", want, data)
		}
	}
}
