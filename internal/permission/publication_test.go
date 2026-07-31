package permission

import (
	"context"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// publishAction is a command that publishes, shaped the way the shell analyzer
// reports one.
func publishAction() tools.Action {
	return tools.Action{
		Risk: tools.RiskExecute, Summary: "run: npm publish",
		Command: "npm publish", Executables: []string{"npm"},
		Operations:         []string{"npm publish"},
		Network:            true,
		HostsUndetermined:  true,
		HostReasons:        []string{"npm publish contacts endpoints chosen by configuration"},
		PublicationTargets: []string{"package registry: npm publish"},
	}
}

func installAction() tools.Action {
	return tools.Action{
		Risk: tools.RiskExecute, Summary: "run: npm install",
		Command: "npm install", Executables: []string{"npm"},
		Operations: []string{"npm install"},
		Network:    true, HostsUndetermined: true,
	}
}

// TestAutopilotDoesNotCoverPublication is the defect this control was built
// for. On a stock configuration `npm publish`, `git push`, `gh pr create`,
// `kubectl apply`, and `terraform apply` were all allowed with no prompt,
// while every deletion counterpart of the same tools required one.
func TestAutopilotDoesNotCoverPublication(t *testing.T) {
	grant, outcome := decide(t, appconfig.Permissions{Mode: "autopilot"}, publishAction())
	if outcome != "prompt" {
		t.Fatalf("autopilot outcome = %q, want prompt", outcome)
	}
	if grant.Source != "publication" {
		t.Fatalf("grant source = %q, want publication", grant.Source)
	}
	// The same mode must still approve ordinary work, or the control has
	// simply turned autopilot off.
	if _, outcome := decide(t, appconfig.Permissions{Mode: "autopilot"}, installAction()); outcome != "allow" {
		t.Fatalf("npm install under autopilot = %q, want allow", outcome)
	}
}

// A rule naming the executable allowed a package manager. That is not a
// decision to publish with it, so it must not be the thing that authorizes a
// release — exactly as a blanket path rule does not cover a private key.
func TestExecutableRuleDoesNotCoverPublication(t *testing.T) {
	perms := appconfig.Permissions{
		Mode:  "autopilot",
		Rules: []appconfig.Rule{{Action: "allow", Tool: "run_command", Command: "npm"}},
	}
	if _, outcome := decide(t, perms, publishAction()); outcome != "prompt" {
		t.Fatalf("executable allow rule covered a publication: outcome = %q", outcome)
	}
	if _, outcome := decide(t, perms, installAction()); outcome != "allow" {
		t.Fatalf("executable allow rule stopped covering ordinary work: outcome = %q", outcome)
	}
}

// The written-down exception has to work, or the only way to run a release job
// is to switch the control off entirely.
func TestOperationRuleIsADeliberatePublicationException(t *testing.T) {
	perms := appconfig.Permissions{
		Mode:  "autopilot",
		Rules: []appconfig.Rule{{Action: "allow", Tool: "run_command", Command: "npm publish", Reason: "release job"}},
	}
	if _, outcome := decide(t, perms, publishAction()); outcome != "allow" {
		t.Fatalf("operation allow rule did not cover its own publication: outcome = %q", outcome)
	}
}

func TestPublicationDenyRefusesOutright(t *testing.T) {
	perms := appconfig.Permissions{Mode: "ask", Publication: appconfig.PublicationDeny}
	grant, outcome := decide(t, perms, publishAction())
	if outcome != "deny" {
		t.Fatalf("outcome = %q, want deny", outcome)
	}
	if grant.Source != "publication" {
		t.Fatalf("grant source = %q, want publication", grant.Source)
	}
	// An ordered rule naming the operation still outranks the setting, exactly
	// as a rule naming a path outranks protect_credentials=deny. The two
	// exceptions are deliberately not equivalent: a rule is written down,
	// inspectable, and survives review, while an interactive grant is a
	// decision made under time pressure with the work half-done. Only the
	// first outranks deny; the grant test below pins the second.
	perms.Rules = []appconfig.Rule{{Action: "allow", Tool: "run_command", Command: "npm publish"}}
	if _, outcome := decide(t, perms, publishAction()); outcome != "allow" {
		t.Fatalf("a written rule did not outrank publication=deny: outcome = %q", outcome)
	}
	// An executable-only rule still does not, so deny is not reachable by a
	// broad allow that happened to cover the tool.
	perms.Rules = []appconfig.Rule{{Action: "allow", Tool: "run_command", Command: "npm"}}
	if _, outcome := decide(t, perms, publishAction()); outcome != "deny" {
		t.Fatalf("a blanket executable rule reached past publication=deny: outcome = %q", outcome)
	}
}

func TestPublicationOffRestoresEarlierBehavior(t *testing.T) {
	perms := appconfig.Permissions{Mode: "autopilot", Publication: appconfig.PublicationOff}
	if _, outcome := decide(t, perms, publishAction()); outcome != "allow" {
		t.Fatalf("publication=off outcome = %q, want allow", outcome)
	}
}

// A tool-wide "always allow run_command" must not become the thing that
// authorized a release, and the dialog must not offer one either.
func TestToolWideAlwaysIsNotOfferedForPublication(t *testing.T) {
	action := publishAction()
	if !oneTimeOnly(action, nil, action.PublicationTargets) {
		t.Fatal("a publishing action was eligible for a tool-wide always")
	}
	m := New(appconfig.Permissions{Mode: "ask"}, nil)
	m.mu.Lock()
	m.allowed["run_command"] = true
	m.mu.Unlock()
	if _, outcome, _ := m.decideBase("run_command", action); outcome != "prompt" {
		t.Fatalf("an existing tool-wide grant covered a publication: outcome = %q", outcome)
	}
}

// The narrow grant is what keeps the control livable: publishing twenty
// packages from one release should not be twenty identical prompts.
func TestPublicationSessionGrantCoversExactlyTheOperationShown(t *testing.T) {
	action := publishAction()
	m := New(appconfig.Permissions{Mode: "ask"}, func(context.Context, Request) (Decision, error) {
		return Decision{Allow: true, Grants: []string{CapabilityPublication}}, nil
	})
	if _, err := m.Authorize(context.Background(), "run_command", action); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if _, outcome, _ := m.decideBase("run_command", action); outcome != "allow" {
		t.Fatalf("the granted operation still prompted: outcome = %q", outcome)
	}
	// A different operation was never shown, so it was never granted.
	other := publishAction()
	other.Operations = []string{"cargo publish"}
	other.PublicationTargets = []string{"package registry: cargo publish"}
	if _, outcome, _ := m.decideBase("run_command", other); outcome != "prompt" {
		t.Fatalf("a grant for npm publish covered cargo publish: outcome = %q", outcome)
	}
	if _, _, _, publications := m.SessionGrants(); len(publications) != 1 {
		t.Fatalf("session publication grants = %v, want exactly one", publications)
	}
}

// Raising the setting to deny mid-session must invalidate a grant handed out
// while it was still prompt, the same way a credential grant is invalidated.
func TestRaisingPublicationToDenyInvalidatesAGrant(t *testing.T) {
	action := publishAction()
	m := New(appconfig.Permissions{Mode: "ask"}, func(context.Context, Request) (Decision, error) {
		return Decision{Allow: true, Grants: []string{CapabilityPublication}}, nil
	})
	if _, err := m.Authorize(context.Background(), "run_command", action); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	m.mu.Lock()
	m.publication = appconfig.PublicationDeny
	m.mu.Unlock()
	if _, outcome, _ := m.decideBase("run_command", action); outcome != "deny" {
		t.Fatalf("a grant survived the setting being raised to deny: outcome = %q", outcome)
	}
}

// Under deny nothing is grantable, so an approver cannot be handed a choice
// the setting exists to remove.
func TestPublicationCapabilityIsNotGrantableUnderDeny(t *testing.T) {
	m := New(appconfig.Permissions{Mode: "ask", Publication: appconfig.PublicationDeny}, nil)
	for _, capability := range m.Capabilities(publishAction()) {
		if capability.Kind == CapabilityPublication && capability.Grantable {
			t.Fatal("publication was grantable under deny")
		}
	}
}

// The capability has to reach the dialog at all, or the prompt cannot say what
// it is asking about.
func TestPublicationAppearsAsItsOwnCapability(t *testing.T) {
	m := New(appconfig.Permissions{Mode: "ask"}, nil)
	var found *Capability
	for i, capability := range m.Capabilities(publishAction()) {
		if capability.Kind == CapabilityPublication {
			found = &m.Capabilities(publishAction())[i]
		}
	}
	if found == nil {
		t.Fatal("no publication capability was reported")
	}
	if !found.Grantable {
		t.Fatal("publication was not grantable under the default prompt setting")
	}
	if len(found.Values) != 1 || found.Values[0] != "package registry: npm publish" {
		t.Fatalf("capability values = %v", found.Values)
	}
}

// A publication that is also a mandatory confirmation stays a confirmation:
// the stricter gate wins and neither is grantable.
func TestForcedPushRemainsAOneTimeConfirmation(t *testing.T) {
	action := tools.Action{
		Risk: tools.RiskExecute, Summary: "run: git push --force origin main",
		Command: "git push --force origin main", Executables: []string{"git"},
		Operations:         []string{"git push"},
		ConfirmReasons:     []string{"forced Git push rewrites remote history"},
		PublicationTargets: []string{"source remote: git push"},
	}
	grant, outcome := decide(t, appconfig.Permissions{Mode: "autopilot"}, action)
	if outcome != "prompt" {
		t.Fatalf("outcome = %q, want prompt", outcome)
	}
	if grant.Source != "publication" && grant.Source != "safety" {
		t.Fatalf("grant source = %q, want publication or safety", grant.Source)
	}
	m := New(appconfig.Permissions{Mode: "ask"}, nil)
	for _, capability := range m.Capabilities(action) {
		if capability.Kind == CapabilityPublication && capability.Grantable {
			t.Fatal("a mandatory confirmation was made grantable through publication")
		}
	}
}
