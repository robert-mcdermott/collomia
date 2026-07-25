package permission

import (
	"context"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

func networkAction() tools.Action {
	return tools.Action{
		Risk: tools.RiskExecute, Summary: "run: curl https://api.example.com",
		Command: "curl https://api.example.com", Executables: []string{"curl"},
		Hosts: []string{"api.example.com"}, Network: true,
	}
}

func undeterminedAction() tools.Action {
	return tools.Action{
		Risk: tools.RiskExecute, Summary: "run: git push origin main",
		Command: "git push origin main", Executables: []string{"git"},
		Network: true, HostsUndetermined: true,
		HostReasons: []string{"git push uses a remote configured in the repository"},
	}
}

// The open posture is what every earlier release did: autopilot approves a
// network-bearing command without asking.
func TestOpenPostureKeepsAutopilotAutomatic(t *testing.T) {
	prompts := 0
	m := New(appconfig.Permissions{Mode: "autopilot"}, func(context.Context, Request) (Decision, error) {
		prompts++
		return Decision{Allow: true}, nil
	})
	if _, err := m.Authorize(t.Context(), "run_command", networkAction()); err != nil {
		t.Fatal(err)
	}
	if prompts != 0 {
		t.Fatalf("prompts=%d, want 0", prompts)
	}
}

func TestScopedNetworkPostureEscalatesToPrompt(t *testing.T) {
	var seen Request
	m := New(appconfig.Permissions{Mode: "autopilot", Network: "scoped"}, func(_ context.Context, r Request) (Decision, error) {
		seen = r
		return Decision{Allow: true}, nil
	})
	if _, err := m.Authorize(t.Context(), "run_command", networkAction()); err != nil {
		t.Fatal(err)
	}
	if !seen.PostureGated {
		t.Fatal("prompt should be marked posture-gated")
	}
	if !contains(seen.Reason, "api.example.com") {
		t.Fatalf("reason = %q", seen.Reason)
	}
}

// A tool-wide session grant must not hand out the access the posture exists
// to withhold, or "always allow run_command" would silently defeat it.
func TestToolWideSessionGrantDoesNotSatisfyNetworkPosture(t *testing.T) {
	prompts := 0
	m := New(appconfig.Permissions{Mode: "autopilot", Network: "scoped"}, func(context.Context, Request) (Decision, error) {
		prompts++
		return Decision{Allow: true, Always: true}, nil
	})
	for range 2 {
		if _, err := m.Authorize(t.Context(), "run_command", networkAction()); err != nil {
			t.Fatal(err)
		}
	}
	if prompts != 2 {
		t.Fatalf("prompts=%d, want 2", prompts)
	}
}

func TestNetworkGrantCoversLaterActionsToTheSameHost(t *testing.T) {
	prompts := 0
	m := New(appconfig.Permissions{Mode: "autopilot", Network: "scoped"}, func(_ context.Context, r Request) (Decision, error) {
		prompts++
		return Decision{Allow: true, Grants: GrantableKinds(r.Capabilities)}, nil
	})
	if _, err := m.Authorize(t.Context(), "run_command", networkAction()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Authorize(t.Context(), "run_command", networkAction()); err != nil {
		t.Fatal(err)
	}
	if prompts != 1 {
		t.Fatalf("prompts=%d, want 1", prompts)
	}
	// A different endpoint is a different grant.
	other := networkAction()
	other.Hosts = []string{"other.example.com"}
	if _, err := m.Authorize(t.Context(), "run_command", other); err != nil {
		t.Fatal(err)
	}
	if prompts != 2 {
		t.Fatalf("prompts=%d, want 2", prompts)
	}
}

// An endpoint nobody could read must never be grantable: the user would be
// approving traffic they were not shown.
func TestUndeterminedEndpointsAreNotGrantable(t *testing.T) {
	prompts := 0
	m := New(appconfig.Permissions{Mode: "autopilot", Network: "scoped"}, func(_ context.Context, r Request) (Decision, error) {
		prompts++
		for _, capability := range r.Capabilities {
			if capability.Kind == CapabilityNetwork && capability.Grantable {
				t.Fatal("undetermined endpoints must not be grantable")
			}
		}
		return Decision{Allow: true, Grants: []string{CapabilityNetwork}}, nil
	})
	for range 2 {
		if _, err := m.Authorize(t.Context(), "run_command", undeterminedAction()); err != nil {
			t.Fatal(err)
		}
	}
	if prompts != 2 {
		t.Fatalf("prompts=%d, want 2", prompts)
	}
}

func TestCommandAllowlistPostureRequiresAGrant(t *testing.T) {
	prompts := 0
	m := New(appconfig.Permissions{Mode: "autopilot", Commands: "allowlist"}, func(_ context.Context, r Request) (Decision, error) {
		prompts++
		if !r.PostureGated {
			t.Fatal("prompt should be marked posture-gated")
		}
		return Decision{Allow: true, Grants: GrantableKinds(r.Capabilities)}, nil
	})
	build := tools.Action{Risk: tools.RiskExecute, Summary: "run: go build ./...", Command: "go build ./...", Executables: []string{"go"}}
	for range 2 {
		if _, err := m.Authorize(t.Context(), "run_command", build); err != nil {
			t.Fatal(err)
		}
	}
	if prompts != 1 {
		t.Fatalf("prompts=%d, want 1", prompts)
	}
	other := tools.Action{Risk: tools.RiskExecute, Summary: "run: make", Command: "make", Executables: []string{"make"}}
	if _, err := m.Authorize(t.Context(), "run_command", other); err != nil {
		t.Fatal(err)
	}
	if prompts != 2 {
		t.Fatalf("prompts=%d, want 2", prompts)
	}
}

// An allow rule remains the configured way to cover a command; the posture
// only removes the automatic approval that had no rule behind it.
func TestAllowRuleSatisfiesBothPostures(t *testing.T) {
	prompts := 0
	m := New(appconfig.Permissions{
		Mode: "autopilot", Network: "scoped", Commands: "allowlist",
		Rules: []appconfig.Rule{{Action: "allow", Tool: "run_command", Host: "api.example.com"}},
	}, func(context.Context, Request) (Decision, error) {
		prompts++
		return Decision{Allow: true}, nil
	})
	if _, err := m.Authorize(t.Context(), "run_command", networkAction()); err != nil {
		t.Fatal(err)
	}
	if prompts != 0 {
		t.Fatalf("prompts=%d, want 0", prompts)
	}
}

// A grant is per-capability: approving the executable does not approve the
// endpoint it contacts.
func TestGrantingOneCapabilityDoesNotCoverAnother(t *testing.T) {
	prompts := 0
	m := New(appconfig.Permissions{Mode: "autopilot", Network: "scoped", Commands: "allowlist"}, func(context.Context, Request) (Decision, error) {
		prompts++
		return Decision{Allow: true, Grants: []string{CapabilityExecutable}}, nil
	})
	for range 2 {
		if _, err := m.Authorize(t.Context(), "run_command", networkAction()); err != nil {
			t.Fatal(err)
		}
	}
	if prompts != 2 {
		t.Fatalf("prompts=%d, want 2", prompts)
	}
}

func TestCapabilitiesDescribeReach(t *testing.T) {
	m := New(appconfig.Permissions{Mode: "ask"}, nil)
	capabilities := m.Capabilities(networkAction())
	kinds := map[string]Capability{}
	for _, capability := range capabilities {
		kinds[capability.Kind] = capability
	}
	exec, ok := kinds[CapabilityExecutable]
	if !ok || !exec.Grantable || exec.Granted {
		t.Fatalf("executable capability = %+v", exec)
	}
	network, ok := kinds[CapabilityNetwork]
	if !ok || !network.Grantable || network.Unknown {
		t.Fatalf("network capability = %+v", network)
	}
}

// An uninspectable command still needs approval every time, so nothing about
// it may be remembered for the session.
func TestUninspectableActionsGrantNothing(t *testing.T) {
	prompts := 0
	m := New(appconfig.Permissions{Mode: "autopilot"}, func(context.Context, Request) (Decision, error) {
		prompts++
		return Decision{Allow: true, Always: true, Grants: []string{CapabilityExecutable, CapabilityNetwork}}, nil
	})
	opaque := networkAction()
	opaque.Uninspectable = true
	opaque.AnalysisReasons = []string{"command substitution"}
	for range 2 {
		if _, err := m.Authorize(t.Context(), "run_command", opaque); err != nil {
			t.Fatal(err)
		}
	}
	if prompts != 2 {
		t.Fatalf("prompts=%d, want 2", prompts)
	}
}

// The documented limits of host rules. These lock the examples in
// docs/USER_GUIDE.md ("Allowing and blocking specific endpoints") so the
// guidance cannot drift from the engine.
func TestDenyRuleOnlyBlocksNamedEndpoints(t *testing.T) {
	rules := []appconfig.Rule{
		{Action: "deny", Host: "evil.com"},
		{Action: "deny", Host: "*.evil.com"},
	}
	m := New(appconfig.Permissions{Mode: "autopilot", Rules: rules}, nil)

	named := networkAction()
	named.Hosts = []string{"drop.evil.com"}
	if _, outcome := m.Evaluate("run_command", named); outcome != "deny" {
		t.Fatalf("subdomain outcome=%s, want deny", outcome)
	}
	apex := networkAction()
	apex.Hosts = []string{"evil.com"}
	if _, outcome := m.Evaluate("run_command", apex); outcome != "deny" {
		t.Fatalf("apex outcome=%s, want deny (a `*.` pattern alone would miss it)", outcome)
	}
	// The documented limit: an endpoint the command never names cannot be
	// matched, so autopilot still approves it under the open posture.
	if _, outcome := m.Evaluate("run_command", undeterminedAction()); outcome != "allow" {
		t.Fatalf("undetermined outcome=%s, want allow under the open posture", outcome)
	}
}

// ...and the documented remedy: the scoped posture turns an unnamed endpoint
// into a prompt rather than an approval.
func TestScopedPostureCatchesWhatDenyRulesCannot(t *testing.T) {
	m := New(appconfig.Permissions{
		Mode: "autopilot", Network: "scoped",
		Rules: []appconfig.Rule{{Action: "deny", Host: "*.evil.com"}},
	}, nil)
	if _, outcome := m.Evaluate("run_command", undeterminedAction()); outcome != "prompt" {
		t.Fatalf("undetermined outcome=%s, want prompt under the scoped posture", outcome)
	}
}

func TestHostGlobsMatchIPLiteralsButNotCIDR(t *testing.T) {
	lab := New(appconfig.Permissions{
		Mode: "autopilot", Network: "scoped",
		Rules: []appconfig.Rule{{Action: "allow", Host: "10.0.*"}},
	}, nil)
	inside := networkAction()
	inside.Hosts = []string{"10.0.0.5"}
	if _, outcome := lab.Evaluate("run_command", inside); outcome != "allow" {
		t.Fatalf("10.0.0.5 outcome=%s, want allow", outcome)
	}
	outside := networkAction()
	outside.Hosts = []string{"10.1.0.5"}
	if _, outcome := lab.Evaluate("run_command", outside); outcome != "prompt" {
		t.Fatalf("10.1.0.5 outcome=%s, want prompt", outcome)
	}
	// A CIDR string is a literal, not a netmask, and matches nothing.
	cidr := New(appconfig.Permissions{
		Mode: "autopilot", Network: "scoped",
		Rules: []appconfig.Rule{{Action: "allow", Host: "10.0.0.0/24"}},
	}, nil)
	if _, outcome := cidr.Evaluate("run_command", inside); outcome != "prompt" {
		t.Fatalf("CIDR outcome=%s: a netmask must not appear to work", outcome)
	}
}

// A host-only allow rule does not restrict which program connects, and it
// satisfies the command allowlist as well, because a matching allow rule is
// evaluated before either posture.
func TestHostOnlyAllowRuleDoesNotRestrictTheExecutable(t *testing.T) {
	m := New(appconfig.Permissions{
		Mode: "autopilot", Network: "scoped", Commands: "allowlist",
		Rules: []appconfig.Rule{{Action: "allow", Host: "api.example.com"}},
	}, nil)
	for _, executable := range []string{"curl", "wget"} {
		action := networkAction()
		action.Executables = []string{executable}
		if _, outcome := m.Evaluate("run_command", action); outcome != "allow" {
			t.Fatalf("%s outcome=%s, want allow", executable, outcome)
		}
	}
}
