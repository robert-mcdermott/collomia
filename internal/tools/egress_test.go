package tools

import (
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/egress"
	"github.com/robert-mcdermott/collomia/internal/sandbox"
)

func scopedRunner(t *testing.T, mode sandbox.Mode, hosts ...string) *RunCommandTool {
	t.Helper()
	command, err := NewRunCommandTool(t.TempDir(), nil, 4096)
	if err != nil {
		t.Fatal(err)
	}
	command.SandboxMode = mode
	command.EgressScoped = true
	command.EgressAllowlist = egress.NewAllowlist(hosts)
	return command
}

// Scoped egress is enforcement only alongside a sandbox that denies direct
// remote traffic. These assertions are written against the platform's own
// answer so the same test is meaningful on macOS, Linux, and Windows CI.
func TestPlanEgressFollowsPlatformSupport(t *testing.T) {
	supported, _ := egress.Supported()

	plan := scopedRunner(t, sandbox.ModeAuto, "proxy.golang.org").planEgress()
	if supported {
		if !plan.broker || plan.err != nil || plan.degraded != "" {
			t.Errorf("auto on a supported platform should broker cleanly, got %+v", plan)
		}
	} else {
		if plan.broker {
			t.Error("a platform that cannot enforce scoped egress must not start a broker")
		}
		if plan.degraded == "" {
			t.Error("auto must degrade visibly rather than silently ignoring the setting")
		}
	}

	required := scopedRunner(t, sandbox.ModeRequire, "proxy.golang.org").planEgress()
	if supported {
		if !required.broker || required.err != nil {
			t.Errorf("require on a supported platform should broker, got %+v", required)
		}
	} else if required.err == nil {
		t.Error("require must fail closed where scoped egress cannot be enforced")
	}
}

// With the sandbox off nothing stops a command from ignoring the proxy, so a
// broker would only look like a boundary. It must degrade instead.
func TestPlanEgressRefusesToPretendWithoutASandbox(t *testing.T) {
	plan := scopedRunner(t, sandbox.ModeOff, "proxy.golang.org").planEgress()
	if plan.broker {
		t.Error("scoped egress must not start a broker when the OS sandbox is off")
	}
	if !strings.Contains(plan.degraded, "sandbox is off") {
		t.Errorf("degradation should name the reason, got %q", plan.degraded)
	}
}

func TestPlanEgressWarnsOnEmptyAllowlist(t *testing.T) {
	supported, _ := egress.Supported()
	if !supported {
		t.Skip("the empty-allowlist warning is only reachable where brokering happens")
	}
	plan := scopedRunner(t, sandbox.ModeAuto).planEgress()
	if !plan.broker {
		t.Fatal("an empty allowlist is strict, not a reason to skip brokering")
	}
	if !strings.Contains(plan.degraded, "no allow rule names a host") {
		t.Errorf("an allowlist that permits nothing should say so, got %q", plan.degraded)
	}
}

func TestPlanEgressIsInertWhenNotConfigured(t *testing.T) {
	command, err := NewRunCommandTool(t.TempDir(), nil, 4096)
	if err != nil {
		t.Fatal(err)
	}
	command.SandboxMode = sandbox.ModeRequire
	if plan := command.planEgress(); plan.broker || plan.degraded != "" || plan.err != nil {
		t.Errorf("an unconfigured runner must be unaffected, got %+v", plan)
	}
}

// Brokering denies remote egress at the OS level regardless of
// sandbox_allow_network: that denial is what makes the broker a boundary.
func TestBrokeredPolicyDeniesDirectNetwork(t *testing.T) {
	command := scopedRunner(t, sandbox.ModeAuto, "proxy.golang.org")
	command.AllowNetwork = true
	if command.sandboxPolicy(true).AllowNetwork {
		t.Error("a brokered command must not also be granted direct network access")
	}
	if !command.sandboxPolicy(false).AllowNetwork {
		t.Error("without a broker the configured AllowNetwork must be preserved")
	}
}

func TestCommandEnvRoutesThroughBrokerAndDropsInheritedProxy(t *testing.T) {
	broker, err := egress.Start(egress.NewAllowlist([]string{"proxy.golang.org"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()

	// An inherited NO_PROXY would route a tool around the broker and into a
	// sandbox denial, which surfaces as an unexplained connection failure.
	t.Setenv("NO_PROXY", "*")
	t.Setenv("HTTPS_PROXY", "http://corporate.example.com:8080")

	command := scopedRunner(t, sandbox.ModeAuto, "proxy.golang.org")
	env := command.commandEnv(broker)
	if len(env) == 0 {
		t.Fatal("a brokered command needs an explicit environment")
	}
	var httpsProxy, noProxy string
	for _, entry := range env {
		if name, value, ok := strings.Cut(entry, "="); ok {
			switch strings.ToLower(name) {
			case "https_proxy":
				httpsProxy = value
			case "no_proxy":
				noProxy = value
			}
		}
	}
	if httpsProxy != "http://"+broker.Addr() {
		t.Errorf("HTTPS_PROXY = %q, want the broker; an inherited proxy must not win", httpsProxy)
	}
	if noProxy != "" {
		t.Errorf("NO_PROXY = %q, want it cleared so nothing bypasses the broker", noProxy)
	}
	// Each spelling appears exactly once. Both cases are emitted on purpose
	// because tools disagree about which they read, but a repeated *identical*
	// key would leave the child's routing up to how a library resolves
	// duplicates.
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy"} {
		if count := countKey(env, key); count != 1 {
			t.Errorf("%s appears %d times, want exactly 1", key, count)
		}
	}
}

func TestCommandEnvInheritsWhenNothingNeedsChanging(t *testing.T) {
	command, err := NewRunCommandTool(t.TempDir(), nil, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if env := command.commandEnv(nil); env != nil {
		t.Errorf("an unbrokered full-environment command should inherit, got %d entries", len(env))
	}
}

// Delegated verification and background processes must be brokered on the same
// terms as a foreground command; a runner built by hand is how that stops
// being true.
func TestConfiguredRunCommandToolCarriesEgressSettings(t *testing.T) {
	cfg := appconfig.Defaults()
	cfg.Permissions.SandboxEgress = appconfig.SandboxEgressScoped
	cfg.Permissions.Rules = []appconfig.Rule{
		{Action: "allow", Host: "proxy.golang.org"},
		{Action: "deny", Host: "evil.example.com"},
	}
	command, err := ConfiguredRunCommandTool(t.TempDir(), cfg, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !command.EgressScoped {
		t.Error("the configured posture did not reach the runner")
	}
	if !command.EgressAllowlist.Permits("proxy.golang.org") {
		t.Error("an allow rule's host should be reachable")
	}
	if command.EgressAllowlist.Permits("evil.example.com") {
		t.Error("a deny rule's host must never enter the allowlist")
	}
}

func countKey(env []string, key string) int {
	n := 0
	for _, entry := range env {
		if name, _, ok := strings.Cut(entry, "="); ok && name == key {
			n++
		}
	}
	return n
}
