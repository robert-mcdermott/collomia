package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/taskmode"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// Kanban regression: nested source trees beyond the old 16-file scope limit,
// corrected arguments, failed checks, a passing retry with added scope, a budget
// pause, lost provider responses, and historical alternative recovery. Only
// native tools establish evidence; no fixture injects passing observations.
func TestProjectCompletionAcrossBudgetPause(t *testing.T) {
	for _, mode := range []taskmode.Mode{taskmode.Developer, taskmode.Work} {
		t.Run(string(mode), func(t *testing.T) {
			a, c, dir := scopedFixture(t, mode)
			guard, _ := tools.NewPathGuard(dir, false)
			a.registry.Add(tools.ApplyPatchTool{Guard: guard})
			a.registry.Add(plan.Tool(c.board))
			if err := c.board.Set(plan.Plan{Goal: "Build application", Steps: []plan.Step{{ID: 1, Title: "Implement and test", Status: "in_progress"}}, Artifacts: []plan.Artifact{{Path: "app", Role: "deliverable"}, {Path: "frontend", Role: "deliverable"}, {Path: "frontend/src", Role: "deliverable"}, {Path: "tests", Role: "deliverable"}, {Path: "README.md", Role: "deliverable"}}}); err != nil {
				t.Fatal(err)
			}
			tool := func(id, name, args string) provider.ToolCall {
				return provider.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(args)}
			}
			var writes []provider.ToolCall
			for i := 0; i < 40; i++ {
				path := fmt.Sprintf("frontend/src/module%d.js", i)
				if i < 10 {
					path = fmt.Sprintf("app/routes/module%d.py", i)
				}
				if i >= 35 {
					path = fmt.Sprintf("tests/test%d.txt", i)
				}
				writes = append(writes, tool(fmt.Sprintf("file-%d", i), "write_file", taskArgs(map[string]string{"path": path, "content": "READY"})))
			}
			writes = append(writes, tool("readme", "write_file", `{"path":"README.md","content":"BEFORE"}`), tool("checker", "write_file", taskArgs(map[string]string{"path": ".collomia-tmp/check.sh", "content": "set -e\nfor f in app/routes/* frontend/src/* tests/* README.md; do grep -q READY \"$f\"; done\nmkdir -p frontend/dist .pytest_cache\nprintf generated > frontend/dist/bundle.js\nprintf cache > .pytest_cache/state\n"})))
			check := `{"command":"sh .collomia-tmp/check.sh","verification":{"paths":["app","frontend","tests","README.md"],"purpose":"Check all application inputs"}}`
			responses := [][]provider.ToolCall{
				writes,
				{tool("bad-patch", "apply_patch", `{"operations":"not an array"}`)},
				{tool("patch", "apply_patch", `{"operations":[{"op":"update","path":"README.md","old_text":"BEFORE","new_text":"NOT-YET"}]}`)},
				{tool("old-check", "run_command", `{"command":"false"}`)},
				{tool("failed-check", "run_command", `{"command":"sh .collomia-tmp/check.sh","timeout_seconds":1}`)},
				{tool("repair", "write_file", `{"path":"README.md","content":"READY"}`)},
				{tool("previous-pass", "run_command", check)},
			}
			a.client = &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
				if call > len(responses) {
					t.Fatal("budget failed to stop")
				}
				return provider.Response{ToolCalls: responses[call-1]}, nil
			}}
			a.completionPlan, a.completionStore = c.board, c.store
			if err := a.SetExecutionLimits(24, len(responses)); err != nil {
				t.Fatal(err)
			}
			_, err := a.Run(t.Context(), "Build and test the app", nil)
			if !errors.Is(err, ErrIterationBudgetExceeded) {
				t.Fatalf("expected budget pause: %v", err)
			}
			state, err := decodeCompletion(c.store.LoadCompletion())
			if err != nil || len(state.Failures) != 1 || state.Failures[0].ID != "old-check" || state.Pending != nil {
				t.Fatalf("pause state: %+v, %v", state, err)
			}
			client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
				switch call {
				case 1, 2:
					return provider.Response{Stop: "stop", Usage: provider.Usage{InputTokens: 100}}, nil
				case 3:
					return provider.Response{ToolCalls: []provider.ToolCall{tool("fresh-pass", "run_command", check)}}, nil
				case 4:
					return provider.Response{ToolCalls: []provider.ToolCall{tool("done-plan", "update_plan", `{"goal":"Build application","steps":[{"id":1,"title":"Implement and test","status":"done","evidence":"All source assertions passed; replaced obsolete check"}],"resolved_failures":[{"failure_id":"old-check","step_id":1,"disposition":"recovered_by_alternative","recovery_tool_call_id":"previous-pass","evidence":"Replaced the obsolete failing check with assertions across all application inputs"}]}`)}}, nil
				default:
					return provider.Response{Content: "Application complete; checks passed."}, nil
				}
			}}
			resumed := New(Options{Client: client, Workspace: dir, Registry: a.registry, Permissions: a.permissions, TaskMode: mode, CompletionPlan: c.board, CompletionStore: c.store, MaxTurnIterations: 20})
			resumed.SetMessages(a.messages)
			notices, retries := 0, 0
			answer, err := resumed.Run(t.Context(), "Continue", func(e event.Event) {
				if event.CompletionNoticeSummary(e.Text) != "" {
					notices++
				}
				if e.Kind == event.KindWarning && strings.Contains(e.Text, "empty completed") {
					retries++
				}
			})
			if err != nil || answer != "Application complete; checks passed." || notices != 0 || retries != 2 || client.calls != 5 {
				t.Fatalf("answer=%q err=%v notices=%d retries=%d calls=%d", answer, err, notices, retries, client.calls)
			}
			state, err = decodeCompletion(c.store.LoadCompletion())
			if err != nil || state.Dirty || len(state.Roles)+len(state.Failures)+len(state.Successes) != 0 {
				t.Fatalf("completed state not cleared: %+v, %v", state, err)
			}
		})
	}
}

func TestProjectScopeHonorsChildReadDenial(t *testing.T) {
	a, c, dir := scopedFixture(t, taskmode.Developer)
	if err := os.Mkdir(filepath.Join(dir, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	putScopedFile(t, dir, "src/private.txt", "do not read")
	a.permissions = permission.New(appconfig.Permissions{Mode: "autopilot", Rules: []appconfig.Rule{{Action: "deny", Path: completionPath(filepath.Join(dir, "src", "private.txt"))}}}, nil)
	_, o := runScopedTool(t, a, c, "denied", "run_command", `{"command":"touch executed","verification":{"paths":["src"],"purpose":"Check source"}}`)
	if !o.Failed || !o.ExecutionPrevented || o.RejectedVerification != nil || len(c.artifacts.receipts) != 0 {
		t.Fatalf("child policy bypassed: %+v", o)
	}
	if _, err := os.Stat(filepath.Join(dir, "executed")); !os.IsNotExist(err) {
		t.Fatal("denied check executed")
	}
}

func TestProjectScopeDoesNotFollowNestedSymlinks(t *testing.T) {
	a, c, dir := scopedFixture(t, taskmode.Developer)
	runScopedTool(t, a, c, "source", "write_file", `{"path":"src/main.js","content":"READY"}`)
	outside := t.TempDir()
	putScopedFile(t, outside, "secret.txt", "OUTSIDE")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "src/link")); err != nil {
		t.Fatal(err)
	}
	_, o := runScopedTool(t, a, c, "check", "run_command", `{"command":"grep -q READY src/main.js","verification":{"paths":["src"],"purpose":"Check source"}}`)
	if o.Failed {
		t.Fatalf("check: %+v", o)
	}
	for _, receipt := range o.ScopedFiles {
		if receipt.covers(filepath.Join(outside, "secret.txt")) || receipt.covers(filepath.Join(dir, "src/link")) {
			t.Fatal("symlink target counted as covered")
		}
	}
	putScopedFile(t, outside, "secret.txt", "CHANGED OUTSIDE")
	if issues := c.checkArtifacts(nil, false); len(issues) != 0 {
		t.Fatalf("unfollowed outside target affected receipt: %v", issues)
	}
	if err := os.Remove(filepath.Join(dir, "src/link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("main.js", filepath.Join(dir, "src/link")); err != nil {
		t.Fatal(err)
	}
	if issues := c.checkArtifacts(nil, false); len(issues) == 0 {
		t.Fatal("retargeted project link accepted")
	}
}

func TestHistoricalSuccessCannotRecoverLaterFailure(t *testing.T) {
	a, c, dir := scopedFixture(t, taskmode.Developer)
	putScopedFile(t, dir, "check.sh", "exit 0\n")
	runScopedTool(t, a, c, "old-pass", "run_command", `{"command":"sh check.sh"}`)
	putScopedFile(t, dir, "check.sh", "exit 1\n")
	runScopedTool(t, a, c, "new-failure", "run_command", `{"command":"sh check.sh"}`)
	restored := newCompletionController(plan.NewBoard(), dir, false, taskmode.Developer)
	if err := restored.restoreCompletion(c.store); err != nil {
		t.Fatal(err)
	}
	p := &plan.Plan{Goal: "Check", Steps: []plan.Step{{ID: 1, Title: "Check", Status: "done", Evidence: "Check passed"}}}
	resolution := plan.FailureResolution{FailureID: "new-failure", StepID: 1, Disposition: "recovered_by_retry", RecoveryToolCallID: "old-pass", Evidence: "Old check passed"}
	if len(restored.failures) != 1 {
		t.Fatalf("failures=%+v", restored.failures)
	}
	if issue := restored.validateFailureResolution(p, restored.failures[0], resolution); !strings.Contains(issue, "predates the failure") {
		t.Fatalf("older success accepted: %s", issue)
	}
	// Reused provider IDs cannot resurrect that historical success either.
	runScopedTool(t, a, restored, "old-pass", "run_command", `{"command":"sh check.sh"}`)
	if _, ok := restored.successes["old-pass"]; ok {
		t.Fatal("reused failed call ID retained its old success")
	}
}

func TestProjectReceiptFreshnessAndScope(t *testing.T) {
	for _, change := range []string{"edit", "add", "delete", "replace-root", "outside", "excluded-output", "build-output"} {
		t.Run(change, func(t *testing.T) {
			a, c, dir := scopedFixture(t, taskmode.Developer)
			runScopedTool(t, a, c, "source", "write_file", `{"path":"src/main.js","content":"READY"}`)
			_, o := runScopedTool(t, a, c, "check", "run_command", `{"command":"grep -q READY src/main.js","verification":{"paths":["src"],"purpose":"Check source"}}`)
			if o.Failed {
				t.Fatalf("check: %+v", o)
			}
			switch change {
			case "edit":
				putScopedFile(t, dir, "src/main.js", "BROKEN")
			case "add":
				putScopedFile(t, dir, "src/new.js", "NEW")
			case "delete":
				if err := os.Remove(filepath.Join(dir, "src/main.js")); err != nil {
					t.Fatal(err)
				}
			case "replace-root":
				if err := os.Rename(filepath.Join(dir, "src"), filepath.Join(dir, "old-src")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(dir, "src"), 0700); err != nil {
					t.Fatal(err)
				}
				putScopedFile(t, dir, "src/main.js", "READY")
			case "outside":
				runScopedTool(t, a, c, "other", "write_file", `{"path":"other.txt","content":"unverified"}`)
			case "excluded-output":
				runScopedTool(t, a, c, "output", "write_file", `{"path":"src/dist/output.txt","content":"unverified"}`)
			case "build-output":
				if err := os.MkdirAll(filepath.Join(dir, "src/dist"), 0700); err != nil {
					t.Fatal(err)
				}
				putScopedFile(t, dir, "src/dist/output.js", "generated")
			}
			issues := c.checkArtifacts(nil, false)
			if change == "build-output" {
				if len(issues) != 0 || c.dirty {
					t.Fatalf("build output invalidated inputs: %v", issues)
				}
			} else if len(issues) == 0 && !c.dirty {
				t.Fatal("unverified change accepted")
			}
		})
	}
}
