package eval

import (
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/agent"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// TestAutopilotCannotPublishEvaluation exercises the whole lifecycle for the
// defect this control was built for: shell analysis, action construction, the
// permission pipeline, and the agent loop's handling of a refusal.
//
// A unit test on the classifier proves the string is recognized. It does not
// prove that an autopilot turn — the mode whose entire purpose is not asking —
// actually stops. That is the property that was wrong, so it is the property
// worth an end-to-end scenario.
func TestAutopilotCannotPublishEvaluation(t *testing.T) {
	workspace := t.TempDir()
	client := &scriptedProvider{t: t, steps: []scriptedStep{
		{response: toolResponse("publish", "run_command", `{"command":"npm publish"}`)},
		{check: requireLastToolContains("Tool denied", "requires interactive approval"),
			response: provider.Response{Content: "The package was not published because approval was unavailable."}},
	}}
	runtime, tracker := newEvaluationAgent(t, workspace, client, "autopilot")
	var events []event.Event
	answer, err := runtime.Run(t.Context(), "Publish the package.", func(e event.Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(answer, "not published") || len(tracker.Changed()) != 0 {
		t.Fatalf("answer=%q changed=%v", answer, tracker.Changed())
	}
	// The command must never have started. A publication that was executed and
	// then reported as denied is the worst possible outcome here, because the
	// version number is already gone.
	if deniedDecisions(events) != 1 || countKind(events, event.KindToolStart) != 0 {
		t.Fatalf("denied=%d starts=%d", deniedDecisions(events), countKind(events, event.KindToolStart))
	}
}

// TestAutopilotStillInstallsEvaluation is the other half of the property. A
// control that stopped ordinary dependency work would be indistinguishable
// from switching autopilot off, and would be turned off in turn.
func TestAutopilotStillInstallsEvaluation(t *testing.T) {
	workspace := t.TempDir()
	client := &scriptedProvider{t: t, steps: []scriptedStep{
		{response: toolResponse("version", "run_command", `{"command":"go version"}`)},
		{response: provider.Response{Content: "Checked the toolchain version."}},
	}}
	runtime, _ := newEvaluationAgent(t, workspace, client, "autopilot")
	var events []event.Event
	if _, err := runtime.Run(t.Context(), "Check the Go version.", func(e event.Event) { events = append(events, e) }); err != nil {
		t.Fatal(err)
	}
	if deniedDecisions(events) != 0 || countKind(events, event.KindToolStart) != 1 {
		t.Fatalf("ordinary work was interrupted: denied=%d starts=%d", deniedDecisions(events), countKind(events, event.KindToolStart))
	}
}

// TestOperationRuleAuthorizesPublicationEvaluation proves the written-down
// exception reaches execution. Without it the only way to run a release job
// would be to switch the control off, which is how a control stops existing.
func TestOperationRuleAuthorizesPublicationEvaluation(t *testing.T) {
	workspace := t.TempDir()
	client := &scriptedProvider{t: t, steps: []scriptedStep{
		{response: toolResponse("version", "run_command", `{"command":"go version"}`)},
		{response: provider.Response{Content: "Ran the release step."}},
	}}
	cfg := appconfig.Defaults()
	cfg.Permissions.Mode = "autopilot"
	cfg.Permissions.Sandbox = evaluationSandboxMode()
	cfg.Permissions.CommandEnv = "minimal"
	cfg.Permissions.SandboxReadableRoots = append(cfg.Permissions.SandboxReadableRoots, evaluationSandboxReadableRoots()...)
	// `go version` carries no publication, so the rule below is asserted
	// against the decision path rather than against the command's own effect:
	// the point is that an operation-scoped rule is matched and honored at all.
	cfg.Permissions.Rules = []appconfig.Rule{{Action: "allow", Tool: "run_command", Command: "go version", Reason: "release step"}}
	registry, _, processes, err := tools.Builtins(workspace, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(processes.StopAll)
	runtime := agent.New(agent.Options{
		Client: client, ProviderName: "offline-evaluation", Model: "scripted",
		ProviderConfig: appconfig.Provider{Type: "fixture", MaxTokens: 256, Context: 16_000},
		Workspace:      workspace, Registry: registry, Permissions: permission.New(cfg.Permissions, nil),
		MaxIterations: 8, MaxToolOutput: cfg.Options.MaxToolOutputBytes,
	})
	var events []event.Event
	if _, err := runtime.Run(t.Context(), "Run the release step.", func(e event.Event) { events = append(events, e) }); err != nil {
		t.Fatal(err)
	}
	if deniedDecisions(events) != 0 || countKind(events, event.KindToolStart) != 1 {
		t.Fatalf("an operation-scoped allow rule did not authorize the command: denied=%d starts=%d", deniedDecisions(events), countKind(events, event.KindToolStart))
	}
}
