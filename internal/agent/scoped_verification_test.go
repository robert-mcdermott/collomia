package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/taskmode"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

func scopedFixture(t *testing.T, mode taskmode.Mode) (*Agent, *completionController, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX check fixture")
	}
	dir := t.TempDir()
	guard, err := tools.NewPathGuard(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	command, err := tools.NewRunCommandTool(dir, nil, 4096)
	if err != nil {
		t.Fatal(err)
	}
	command.Guard = guard
	a := New(Options{Workspace: dir, Registry: tools.NewRegistry(command, tools.ValidateArtifactTool{Guard: guard}, tools.WriteFileTool{Guard: guard}, tools.EditFileTool{Guard: guard}), Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil), TaskMode: mode})
	c := newCompletionController(plan.NewBoard(), dir, false, mode)
	if err := c.restoreCompletion(&memoryCompletionStore{}); err != nil {
		t.Fatal(err)
	}
	return a, c, dir
}

func runScopedTool(t *testing.T, a *Agent, c *completionController, id, name, raw string) (tools.Result, toolObservation) {
	t.Helper()
	r, o, err := a.executeTool(t.Context(), provider.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(raw)}, false, c, func(event.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	c.observe(o)
	if err := c.finishEffect(o); err != nil {
		t.Fatal(err)
	}
	return r, o
}

func putScopedFile(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

const scopedCheckArgs = `{"command":"sh check.sh","verification":{"paths":["game.html"],"purpose":"Check required game content"}}`

func TestScopedVerificationCompletesWithoutRedundantArtifactCheck(t *testing.T) {
	for _, mode := range []taskmode.Mode{taskmode.Work, taskmode.Developer} {
		t.Run(string(mode), func(t *testing.T) {
			a, c, dir := scopedFixture(t, mode)
			putScopedFile(t, dir, "check.sh", "grep -q ASTEROIDS game.html\n")
			_, o := runScopedTool(t, a, c, "write", "write_file", `{"path":"game.html","content":"<canvas>ASTEROIDS</canvas>"}`)
			if o.Failed {
				t.Fatalf("write: %+v", o)
			}
			c.syncArtifactBrief(&plan.Plan{Artifacts: []plan.Artifact{{Path: "game.html", Role: "deliverable"}}})
			r, o := runScopedTool(t, a, c, "check", "run_command", scopedCheckArgs)
			if o.Failed || len(o.ScopedFiles) != 1 || r.Evidence == nil || r.Evidence.Kind != "scoped_verification" {
				t.Fatalf("check: %+v %+v", r, o)
			}
			if d := c.assess(); !d.done || c.interventions != 0 {
				t.Fatalf("completion: %+v", d)
			}
			// A new controller must obtain new proof, even though the saved
			// obligation came from a successful scoped check.
			restored := newCompletionController(plan.NewBoard(), dir, false, mode)
			if err := restored.restoreCompletion(c.store); err != nil {
				t.Fatal(err)
			}
			if d := restored.assess(); d.done {
				t.Fatal("resume reused a prior receipt")
			}
			putScopedFile(t, dir, "game.html", "CHANGED")
			if d := c.assess(); d.done {
				t.Fatal("external edit kept evidence fresh")
			}
		})
	}
}

func TestScopedVerificationFailureAndFreshRetry(t *testing.T) {
	a, c, dir := scopedFixture(t, taskmode.Work)
	putScopedFile(t, dir, "game.html", "WRONG")
	putScopedFile(t, dir, "check.sh", "grep -q ASTEROIDS game.html\n")
	_, o := runScopedTool(t, a, c, "failed", "run_command", scopedCheckArgs)
	if !o.Failed || c.pending != nil {
		t.Fatalf("expected settled failed check: %+v", o)
	}
	// A different passing check cannot erase this failure.
	_, o = runScopedTool(t, a, c, "unrelated", "run_command", `{"command":"test -s game.html","verification":{"paths":["game.html"],"purpose":"Only nonempty"}}`)
	if o.Failed || len(c.failures) != 1 {
		t.Fatal("weaker check cleared failed requirement")
	}
	runScopedTool(t, a, c, "fix", "write_file", `{"path":"game.html","content":"ASTEROIDS"}`)
	runScopedTool(t, a, c, "retry", "run_command", scopedCheckArgs)
	if d := c.assess(); !d.done {
		t.Fatalf("retry did not finish: %+v", d)
	}
}

func TestScopedCheckAfterInterventionDoesNotAlsoReportUnrecognized(t *testing.T) {
	a, c, dir := scopedFixture(t, taskmode.Work)
	putScopedFile(t, dir, "game.html", "ASTEROIDS")
	putScopedFile(t, dir, "check.sh", "grep -q ASTEROIDS game.html\n")
	c.markDirty([]string{filepath.Join(dir, "game.html")})
	if d := c.assess(); d.notice == "" || strings.Contains(d.notice, "resolved_failures") {
		t.Fatalf("unexpected initial guidance: %+v", d)
	}
	r, o := runScopedTool(t, a, c, "check", "run_command", scopedCheckArgs)
	if o.Failed || strings.Contains(r.Content, "not recorded") || !c.assess().done {
		t.Fatalf("contradictory check result: %+v", r)
	}
}

func TestScopedVerificationRejectsMaskedStatusAndDrift(t *testing.T) {
	for _, command := range []string{"sh check.sh || true", "sh check.sh; true", "sh check.sh | cat", "sh mutate.sh"} {
		t.Run(command, func(t *testing.T) {
			a, c, dir := scopedFixture(t, taskmode.Work)
			putScopedFile(t, dir, "game.html", "ASTEROIDS")
			putScopedFile(t, dir, "check.sh", "exit 1\n")
			putScopedFile(t, dir, "mutate.sh", "printf CHANGED > game.html\n")
			raw, _ := json.Marshal(map[string]any{"command": command, "verification": map[string]any{"paths": []string{"game.html"}, "purpose": "Check"}})
			r, o := runScopedTool(t, a, c, "check", "run_command", string(raw))
			if !o.Failed || len(o.ScopedFiles) != 0 || r.Evidence != nil {
				t.Fatalf("false proof: %+v %+v", r, o)
			}
			if c.assess().done {
				t.Fatal("accepted failed evidence")
			}
		})
	}
}

func TestScopedVerificationDoesNotClearOtherPathsOrFollowSymlink(t *testing.T) {
	a, c, dir := scopedFixture(t, taskmode.Developer)
	putScopedFile(t, dir, "game.html", "ASTEROIDS")
	putScopedFile(t, dir, "other.txt", "OTHER")
	putScopedFile(t, dir, "check.sh", "grep -q ASTEROIDS game.html\n")
	c.markDirty([]string{filepath.Join(dir, "game.html"), filepath.Join(dir, "other.txt")})
	runScopedTool(t, a, c, "check", "run_command", scopedCheckArgs)
	if c.assess().done {
		t.Fatal("scoped check cleared unrelated file")
	}
	if err := os.Remove(filepath.Join(dir, "game.html")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "other.txt"), filepath.Join(dir, "game.html")); err != nil {
		t.Fatal(err)
	}
	if d := c.assess(); d.done || !strings.Contains(d.notice, "receipt") {
		t.Fatalf("retarget accepted: %+v", d)
	}
}

func TestFileRepairRequiresSuccessfulWriteAndFreshEvidence(t *testing.T) {
	a, c, dir := scopedFixture(t, taskmode.Work)
	putScopedFile(t, dir, "game.html", "ASTEROIDS")
	putScopedFile(t, dir, "check.sh", "grep -q ASTEROIDS game.html\n")
	_, o := runScopedTool(t, a, c, "failed-edit", "edit_file", `{"path":"game.html","old_text":"MISSING","new_text":"FIXED"}`)
	if !o.Failed {
		t.Fatal("expected native edit mismatch")
	}
	runScopedTool(t, a, c, "check-old", "run_command", scopedCheckArgs)
	if len(c.failures) != 1 {
		t.Fatal("validation of old bytes erased failed edit")
	}
	runScopedTool(t, a, c, "replace", "write_file", `{"path":"game.html","content":"ASTEROIDS FIXED"}`)
	if len(c.failures) != 1 {
		t.Fatal("unverified replacement erased failure")
	}
	// Repair facts persist, but all validation must run again after resume.
	restored := newCompletionController(plan.NewBoard(), dir, false, taskmode.Work)
	if err := restored.restoreCompletion(c.store); err != nil {
		t.Fatal(err)
	}
	runScopedTool(t, a, restored, "check-new", "run_command", scopedCheckArgs)
	if d := restored.assess(); !d.done || len(restored.failures) != 0 {
		t.Fatalf("verified repair did not recover: %+v", d)
	}
}

func TestScopedAgentRunNeedsNoCompletionInterventions(t *testing.T) {
	for _, mode := range []taskmode.Mode{taskmode.Work, taskmode.Developer} {
		t.Run(string(mode), func(t *testing.T) {
			a, c, dir := scopedFixture(t, mode)
			putScopedFile(t, dir, "check.sh", "grep -q ASTEROIDS game.html\n")
			client := &fakeClient{chat: func(call int, req provider.Request) (provider.Response, error) {
				switch call {
				case 1:
					return graphToolResponse("write", "write_file", `{"path":"game.html","content":"ASTEROIDS"}`), nil
				case 2:
					return graphToolResponse("check", "run_command", scopedCheckArgs), nil
				default:
					return provider.Response{Content: "Created and checked required text; gameplay not tested."}, nil
				}
			}}
			a.client = client
			a.completionPlan = c.board
			a.completionStore = c.store
			notices := 0
			_, err := a.Run(t.Context(), "Create and check the file", func(e event.Event) {
				if event.CompletionNoticeSummary(e.Text) != "" {
					notices++
				}
			})
			if err != nil || client.calls != 3 || notices != 0 {
				t.Fatalf("calls=%d notices=%d err=%v", client.calls, notices, err)
			}
		})
	}
}

func TestCommandRetryTimeoutCompatibility(t *testing.T) {
	old := provider.ToolCall{Name: "run_command", Arguments: json.RawMessage(`{"command":"sh check.sh","timeout_seconds":1}`)}
	retry := provider.ToolCall{Name: "run_command", Arguments: json.RawMessage(`{"command":"sh check.sh","timeout_seconds":120}`)}
	if toolRetryKey(old) != toolRetryKey(retry) {
		t.Fatal("timeout adjustment changed operation")
	}
	failure := unresolvedToolFailure{tool: "run_command", retryKey: canonicalRetryKey(old, false)}
	success := toolObservation{Name: "run_command", RetryKey: toolRetryKey(old), LegacyRetryKey: canonicalRetryKey(old, false)}
	if !matchesRetry(failure, success) {
		t.Fatal("legacy exact operation lost")
	}
	retry.Arguments = json.RawMessage(`{"command":"sh different.sh","timeout_seconds":120}`)
	if toolRetryKey(old) == toolRetryKey(retry) {
		t.Fatal("different command collided")
	}
}

func TestScopedStandardCheckCannotAcceptGraphVerification(t *testing.T) {
	a, _, dir := scopedFixture(t, taskmode.Developer)
	putScopedFile(t, dir, "game.html", "BEFORE")
	putScopedFile(t, dir, "check.sh", "grep -q ASTEROIDS game.html\n")
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "Make game", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "Write and verify game"}}}, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	a.goalGraph = graph
	a.goalStateToken = func(_ context.Context) (string, error) {
		data, err := os.ReadFile(filepath.Join(dir, "game.html"))
		return string(data), err
	}
	a.client = &fakeClient{chat: func(call int, req provider.Request) (provider.Response, error) {
		if call == 1 {
			return graphToolResponse("write", "write_file", `{"path":"game.html","content":"ASTEROIDS"}`), nil
		}
		if call == 2 {
			return graphToolResponse("check", "run_command", scopedCheckArgs), nil
		}
		return provider.Response{Content: "Done"}, nil
	}}
	_, err = a.Run(t.Context(), "Make game", nil)
	if err == nil || graph.Snapshot().Outcome == goalgraph.OutcomeDone {
		t.Fatal("Standard scope bypassed graph verification")
	}
	for _, e := range graph.Snapshot().Evidence {
		if e.Kind == goalgraph.EvidenceVerification && e.Status == "passed" {
			t.Fatal("custom check manufactured graph verification")
		}
	}
}
