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

// The credential dimension is grantable so a project that legitimately reads
// its own .env has a durable answer; every other dimension of the same action
// is not, and the tool-wide "always" stays unavailable. Granting one file must
// not be a back door to granting the tool.
func TestOnlyTheCredentialDimensionIsGrantable(t *testing.T) {
	home := fakeHome(t)
	action := keyReadAction(home)
	action.Network, action.Hosts = true, []string{"example.com"}
	m := New(appconfig.Permissions{Mode: "ask"}, nil)
	var sawCredential bool
	for _, capability := range m.Capabilities(action) {
		if capability.Kind == CapabilityCredential {
			sawCredential = true
			if !capability.Grantable {
				t.Error("credential dimension was not grantable under the prompt setting")
			}
			continue
		}
		if capability.Grantable {
			t.Errorf("dimension %q was offered as grantable alongside a credential", capability.Kind)
		}
	}
	if !sawCredential {
		t.Fatal("no credential capability was reported")
	}
}

// "deny" makes the answer unavailable rather than merely inconvenient.
func TestDenySettingOffersNoCredentialGrant(t *testing.T) {
	home := fakeHome(t)
	m := New(appconfig.Permissions{Mode: "ask", ProtectCredentials: appconfig.ProtectCredentialsDeny}, nil)
	for _, capability := range m.Capabilities(keyReadAction(home)) {
		if capability.Kind == CapabilityCredential && capability.Grantable {
			t.Error("deny offered a credential grant")
		}
	}
}

// An uninspectable command or a mandatory confirmation is one-time in the
// strong sense: not even the narrow credential grant is offered.
func TestOneTimeActionsOfferNoCredentialGrantEither(t *testing.T) {
	home := fakeHome(t)
	m := New(appconfig.Permissions{Mode: "ask"}, nil)
	for name, mutate := range map[string]func(*tools.Action){
		"uninspectable": func(a *tools.Action) { a.Uninspectable = true },
		"must confirm":  func(a *tools.Action) { a.ConfirmReasons = []string{"destructive"} },
	} {
		action := keyReadAction(home)
		mutate(&action)
		for _, capability := range m.Capabilities(action) {
			if capability.Kind == CapabilityCredential && capability.Grantable {
				t.Errorf("%s action offered a credential grant", name)
			}
		}
	}
}

// The grant covers the exact target and nothing else: a second credential in
// the same action still prompts, and so does a different file.
func TestCredentialGrantCoversOnlyTheTargetShown(t *testing.T) {
	home := fakeHome(t)
	m := New(appconfig.Permissions{Mode: "ask"}, nil)
	key := keyReadAction(home)
	m.applyGrants(key, []string{CapabilityCredential})

	if _, outcome, _ := m.decideBase("run_command", key); outcome != "allow" {
		t.Fatalf("granted target still %q", outcome)
	}
	envAction := tools.Action{
		Risk: tools.RiskRead, Summary: "read .env",
		Paths: []string{filepath.Join("/work", "repo", ".env")},
	}
	if _, outcome, _ := m.decideBase("read_file", envAction); outcome != "prompt" {
		t.Fatalf("a different credential file was covered by the grant: %q", outcome)
	}
	both := key
	both.CredentialTargets = append(append([]string(nil), key.CredentialTargets...), "environment file: /work/repo/.env")
	if _, outcome, _ := m.decideBase("run_command", both); outcome != "prompt" {
		t.Fatalf("an action reaching one granted and one ungranted store was allowed: %q", outcome)
	}
}

// Raising the setting to deny must not be satisfiable by a grant handed out
// while it was still prompt.
func TestRaisingToDenyOverridesAnEarlierGrant(t *testing.T) {
	home := fakeHome(t)
	m := New(appconfig.Permissions{Mode: "ask"}, nil)
	action := keyReadAction(home)
	m.applyGrants(action, []string{CapabilityCredential})
	m.mu.Lock()
	m.protectCredentials = appconfig.ProtectCredentialsDeny
	m.mu.Unlock()
	if _, outcome, _ := m.decideBase("run_command", action); outcome != "deny" {
		t.Fatalf("outcome = %q, want deny", outcome)
	}
}

// A credential grant is never a tool-wide always, whichever way it was
// obtained.
func TestCredentialRequestNeverAllowsAlways(t *testing.T) {
	home := fakeHome(t)
	var seen Request
	m := New(appconfig.Permissions{Mode: "ask"}, func(_ context.Context, r Request) (Decision, error) {
		seen = r
		return Decision{Allow: true, Always: true, Grants: []string{CapabilityCredential}}, nil
	})
	if _, err := m.Authorize(t.Context(), "run_command", keyReadAction(home)); err != nil {
		t.Fatal(err)
	}
	if seen.AllowsAlways {
		t.Error("credential request advertised a tool-wide always")
	}
	m.mu.RLock()
	toolWide := m.allowed["run_command"]
	m.mu.RUnlock()
	if toolWide {
		t.Error("a tool-wide always was recorded for a credential action")
	}
	// The narrow grant it did ask for must have stuck.
	if _, _, credentials := m.SessionGrants(); len(credentials) != 1 {
		t.Fatalf("session credential grants = %v, want exactly one", credentials)
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
