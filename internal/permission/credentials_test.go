package permission

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// fakeHome points os.UserHomeDir at a temporary directory so these tests do
// not depend on the developer's own dotfiles.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
	return home
}

// keyReadAction is a command that reaches a private key, shaped the way the
// shell analyzer reports one.
func keyReadAction(home string) tools.Action {
	key := filepath.Join(home, ".ssh", "id_rsa")
	return tools.Action{
		Risk: tools.RiskExecute, Summary: "run: cat " + key,
		Command: "cat " + key, Executables: []string{"cat"},
		CredentialTargets: []string{"SSH private key material: " + key},
	}
}

// decide runs the decision path without an approver, so the outcome reported
// is the policy's own rather than a test double's answer.
func decide(t *testing.T, perms appconfig.Permissions, action tools.Action) (Grant, string) {
	t.Helper()
	m := New(perms, func(context.Context, Request) (Decision, error) {
		t.Fatal("approver should not be consulted by decideBase")
		return Decision{}, nil
	})
	grant, outcome, _ := m.decideBase("run_command", action)
	return grant, outcome
}

func TestReachingACredentialStorePromptsByDefault(t *testing.T) {
	home := fakeHome(t)
	grant, outcome := decide(t, appconfig.Permissions{Mode: "ask"}, keyReadAction(home))
	if outcome != "prompt" {
		t.Fatalf("outcome = %q, want prompt", outcome)
	}
	if grant.Source != "credentials" {
		t.Fatalf("source = %q, want credentials", grant.Source)
	}
}

// The whole point of the control is that a broad approval cannot include a
// private key by accident. Each of these would otherwise reach an "allow".
func TestBroadApprovalsCannotCoverACredentialStore(t *testing.T) {
	home := fakeHome(t)
	cases := map[string]appconfig.Permissions{
		"autopilot mode": {Mode: "autopilot"},
		"tool-wide session grant": {
			Mode: "ask", AllowedTools: []string{"run_command"},
		},
		"blanket allow rule": {
			Mode:  "ask",
			Rules: []appconfig.Rule{{Action: "allow", Tool: "run_command"}},
		},
		"wildcard path allow rule": {
			Mode:  "ask",
			Rules: []appconfig.Rule{{Action: "allow", Path: "**"}},
		},
		"allow outside workspace": {
			Mode: "autopilot", AllowOutsideWorkspace: true,
		},
	}
	for name, perms := range cases {
		grant, outcome := decide(t, perms, keyReadAction(home))
		if outcome != "prompt" {
			t.Errorf("%s: outcome = %q (source %q), want prompt", name, outcome, grant.Source)
		}
	}
}

// An exception a user wrote on purpose is honored, so the control can be
// narrowed without being switched off wholesale.
func TestRuleNamingTheCredentialPathIsHonored(t *testing.T) {
	home := fakeHome(t)
	key := filepath.Join(home, ".ssh", "id_rsa")
	perms := appconfig.Permissions{
		Mode:  "ask",
		Rules: []appconfig.Rule{{Action: "allow", Path: key, Reason: "deploy key needed by this project"}},
	}
	action := keyReadAction(home)
	action.Paths = []string{key}
	_, outcome := decide(t, perms, action)
	if outcome != "allow" {
		t.Fatalf("outcome = %q, want allow for a rule naming the path", outcome)
	}
}

// A deny rule is stricter than the gate, so it must still win.
func TestDenyRuleStillOutranksTheCredentialGate(t *testing.T) {
	home := fakeHome(t)
	perms := appconfig.Permissions{
		Mode:  "ask",
		Rules: []appconfig.Rule{{Action: "deny", Tool: "run_command"}},
	}
	_, outcome := decide(t, perms, keyReadAction(home))
	if outcome != "deny" {
		t.Fatalf("outcome = %q, want deny", outcome)
	}
}

func TestDenySettingRefusesOutright(t *testing.T) {
	home := fakeHome(t)
	perms := appconfig.Permissions{Mode: "ask", ProtectCredentials: appconfig.ProtectCredentialsDeny}
	grant, outcome := decide(t, perms, keyReadAction(home))
	if outcome != "deny" {
		t.Fatalf("outcome = %q, want deny", outcome)
	}
	if grant.Source != "credentials" {
		t.Fatalf("source = %q, want credentials", grant.Source)
	}
}

// "off" restores the pre-wave behavior exactly, which is what makes it a
// usable escape hatch rather than a partial one.
func TestOffSettingRestoresOrdinaryHandling(t *testing.T) {
	home := fakeHome(t)
	perms := appconfig.Permissions{Mode: "autopilot", ProtectCredentials: appconfig.ProtectCredentialsOff}
	_, outcome := decide(t, perms, keyReadAction(home))
	if outcome != "allow" {
		t.Fatalf("outcome = %q, want allow when protection is off", outcome)
	}
}

// A .env sitting in the workspace is an in-workspace read, which would
// otherwise be approved by the implicit-read fast path without anyone seeing
// it. This is the case the gate's placement exists for.
func TestWorkspaceCredentialFileDoesNotTakeTheImplicitReadPath(t *testing.T) {
	fakeHome(t)
	action := tools.Action{
		Risk: tools.RiskRead, Summary: "read .env",
		Paths: []string{filepath.Join("/work", "repo", ".env")},
	}
	grant, outcome := decide(t, appconfig.Permissions{Mode: "ask"}, action)
	if outcome != "prompt" {
		t.Fatalf("outcome = %q (source %q), want prompt", outcome, grant.Source)
	}
}

// Path-declaring tools are covered without setting CredentialTargets
// themselves, so a tool added later inherits the protection.
func TestPathsAloneAreEnoughToClassify(t *testing.T) {
	home := fakeHome(t)
	action := tools.Action{
		Risk: tools.RiskRead, Summary: "read credentials", Outside: true,
		Paths: []string{filepath.Join(home, ".aws", "credentials")},
	}
	_, outcome := decide(t, appconfig.Permissions{Mode: "autopilot"}, action)
	if outcome != "prompt" {
		t.Fatalf("outcome = %q, want prompt", outcome)
	}
}

// Ordinary work must be untouched, or the control will be switched off.
func TestOrdinaryReadsAndCommandsAreUnaffected(t *testing.T) {
	fakeHome(t)
	read := tools.Action{
		Risk: tools.RiskRead, Summary: "read main.go",
		Paths: []string{filepath.Join("/work", "repo", "main.go")},
	}
	if _, outcome := decide(t, appconfig.Permissions{Mode: "ask"}, read); outcome != "allow" {
		t.Errorf("ordinary read outcome = %q, want allow", outcome)
	}
	build := tools.Action{
		Risk: tools.RiskExecute, Summary: "run: go build ./...",
		Command: "go build ./...", Executables: []string{"go"},
	}
	if _, outcome := decide(t, appconfig.Permissions{Mode: "autopilot"}, build); outcome != "allow" {
		t.Errorf("ordinary command outcome = %q, want allow", outcome)
	}
}

// A reusable grant would defeat a one-time confirmation, so no dimension of a
// credential-reaching action is offered as grantable.
func TestCredentialActionsOfferNoReusableGrant(t *testing.T) {
	home := fakeHome(t)
	m := New(appconfig.Permissions{Mode: "ask"}, nil)
	for _, capability := range m.Capabilities(keyReadAction(home)) {
		if capability.Grantable {
			t.Errorf("capability %v was offered as grantable", capability.Kind)
		}
	}
}

func TestNamesSpecificPathDistinguishesDeliberateRules(t *testing.T) {
	specific := []string{"/work/repo/.env", "~/.ssh/id_rsa", "/work/**/.env", "secrets/*"}
	blanket := []string{"", "   ", "*", "**", "*/*", "/", "**/*"}
	for _, pattern := range specific {
		if !namesSpecificPath(pattern) {
			t.Errorf("namesSpecificPath(%q) = false, want true", pattern)
		}
	}
	for _, pattern := range blanket {
		if namesSpecificPath(pattern) {
			t.Errorf("namesSpecificPath(%q) = true, want false", pattern)
		}
	}
}
