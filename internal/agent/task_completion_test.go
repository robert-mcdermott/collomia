package agent

import (
	"context"
	"encoding/json"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/taskmode"
)

func taskArgs(v any) string { b, _ := json.Marshal(v); return string(b) }

// Regression for worktest2: helpers, final artifact, corrected verification,
// and one final answer. No model is needed to repair completion bookkeeping.
func TestTaskCompletionDashboard(t *testing.T) {
	for _, mode := range []taskmode.Mode{taskmode.Developer, taskmode.Work} {
		for _, artifactOnly := range []bool{false, true} {
			name := string(mode)
			if artifactOnly {
				name += "/artifact"
			} else {
				name += "/command"
			}
			t.Run(name, func(t *testing.T) {
				a, c, _ := scopedFixture(t, mode)
				calls := []provider.ToolCall{
					{ID: "analysis", Name: "write_file", Arguments: json.RawMessage(`{"path":".collomia-tmp/analyze.sh","content":"printf 'analysis'"}`)},
					{ID: "inject", Name: "write_file", Arguments: json.RawMessage(`{"path":".collomia-tmp/inject.sh","content":"printf 'inject'"}`)},
					{ID: "dashboard", Name: "write_file", Arguments: json.RawMessage(`{"path":"dashboard.html","content":"<html>sunspots 269.3</html>"}`)},
				}
				if artifactOnly {
					calls = append(calls, provider.ToolCall{ID: "validate", Name: "validate_artifact", Arguments: json.RawMessage(`{"path":"dashboard.html","format":"text","required_text":["sunspots","269.3"]}`)})
				} else {
					scope := map[string]any{"paths": []string{"dashboard.html"}, "purpose": "Check dashboard data"}
					calls = append(calls,
						provider.ToolCall{ID: "rejected", Name: "run_command", Arguments: json.RawMessage(taskArgs(map[string]any{"command": "false || true", "verification": scope}))},
						provider.ToolCall{ID: "helper", Name: "write_file", Arguments: json.RawMessage(`{"path":".collomia-tmp/check.sh","content":"grep -q 'sunspots 269.3' dashboard.html\n"}`)},
						provider.ToolCall{ID: "passed", Name: "run_command", Arguments: json.RawMessage(taskArgs(map[string]any{"command": "sh .collomia-tmp/check.sh", "verification": scope}))})
				}
				client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
					if call <= len(calls) {
						return provider.Response{ToolCalls: []provider.ToolCall{calls[call-1]}}, nil
					}
					return provider.Response{Content: "Created dashboard; content checks passed."}, nil
				}}
				a.client = client
				a.completionPlan = c.board
				a.completionStore = c.store
				notices := 0
				finals := 0
				result, err := a.Run(t.Context(), "Analyze data and create dashboard", func(e event.Event) {
					if event.CompletionNoticeSummary(e.Text) != "" {
						notices++
					}
					if e.Kind == event.KindTextDelta && strings.Contains(e.Text, "Created dashboard") {
						finals++
					}
				})
				if err != nil || notices != 0 || finals != 1 || client.calls != len(calls)+1 {
					t.Fatalf("result=%q err=%v notices=%d finals=%d calls=%d", result, err, notices, finals, client.calls)
				}
			})
		}
	}
}

func TestTaskCompletionScratchBoundaries(t *testing.T) {
	for _, mode := range []taskmode.Mode{taskmode.Work, taskmode.Developer} {
		t.Run(string(mode), func(t *testing.T) {
			a, c, dir := scopedFixture(t, mode)
			runScopedTool(t, a, c, "scratch", "write_file", `{"path":"helper.sh","content":"exit 0"}`)
			c.syncArtifactBrief(&plan.Plan{Artifacts: []plan.Artifact{{Path: "helper.sh", Role: "scratch"}}})
			if !c.assess().done {
				t.Fatal("declared helper needs independent validation")
			}
			runScopedTool(t, a, c, "output", "write_file", `{"path":".collomia-tmp/requested.txt","content":"requested"}`)
			c.syncArtifactBrief(&plan.Plan{Artifacts: []plan.Artifact{{Path: ".collomia-tmp/requested.txt", Role: "deliverable"}}})
			if c.assess().done {
				t.Fatal("scratch location hid explicit deliverable")
			}
			runScopedTool(t, a, c, "checked", "validate_artifact", `{"path":".collomia-tmp/requested.txt","required_text":["requested"]}`)
			if !c.assess().done {
				t.Fatal("declared deliverable check failed")
			}
			c.syncArtifactBrief(&plan.Plan{Artifacts: []plan.Artifact{{Path: ".collomia-tmp/requested.txt", Role: "scratch"}}})
			putScopedFile(t, dir, ".collomia-tmp/requested.txt", "changed")
			if c.assess().done {
				t.Fatal("demotion hid stale deliverable")
			}
		})
	}
}

func TestTaskCompletionScratchDoesNotHideSourceOrFailures(t *testing.T) {
	a, c, dir := scopedFixture(t, taskmode.Developer)
	runScopedTool(t, a, c, "source", "write_file", `{"path":"app.js","content":"broken"}`)
	runScopedTool(t, a, c, "output", "write_file", `{"path":"report.txt","content":"ready"}`)
	runScopedTool(t, a, c, "checked", "validate_artifact", `{"path":"report.txt","required_text":["ready"]}`)
	d := c.assess()
	if d.done || !strings.Contains(d.notice, "app.js") || strings.Contains(d.notice, "files changed after") {
		t.Fatalf("misleading or missing scope: %+v", d)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".collomia-tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "app.js"), filepath.Join(dir, ".collomia-tmp", "alias.js")); err != nil {
		t.Fatal(err)
	}
	if c.scratchPath(filepath.Join(dir, ".collomia-tmp", "alias.js")) {
		t.Fatal("scratch symlink hid source")
	}
	a, c, _ = scopedFixture(t, taskmode.Work)
	runScopedTool(t, a, c, "helper", "write_file", `{"path":".collomia-tmp/check.sh","content":"exit 1\n"}`)
	runScopedTool(t, a, c, "failed", "run_command", `{"command":"sh .collomia-tmp/check.sh"}`)
	if c.assess().done || len(c.failures) != 1 {
		t.Fatal("scratch directory hid failed check")
	}
}

func TestTaskCompletionRejectedCheckRecoveryAfterResume(t *testing.T) {
	a, c, dir := scopedFixture(t, taskmode.Developer)
	putScopedFile(t, dir, "game.html", "ASTEROIDS")
	putScopedFile(t, dir, "check.sh", "grep -q ASTEROIDS game.html\n")
	runScopedTool(t, a, c, "rejected", "run_command", `{"command":"sh check.sh || true","verification":{"paths":["game.html"],"purpose":"Check required game content"}}`)
	restored := newCompletionController(plan.NewBoard(), dir, false, taskmode.Developer)
	if err := restored.restoreCompletion(c.store); err != nil {
		t.Fatal(err)
	}
	runScopedTool(t, a, restored, "weaker", "run_command", `{"command":"test -s game.html","verification":{"paths":["game.html"],"purpose":"Only nonempty"}}`)
	if len(restored.failures) != 1 {
		t.Fatal("different intent cleared rejection")
	}
	runScopedTool(t, a, restored, "replacement", "run_command", scopedCheckArgs)
	if !restored.assess().done {
		t.Fatal("corrected preflight did not recover across resume")
	}
}

func TestTaskCompletionQuotedChecks(t *testing.T) {
	for _, command := range []string{
		"sh -c 'test -s game.html\nprintf \"literal ; | && > <\"'",
		"sh -c \"test -s game.html\nprintf 'literal ; | && > <'\"",
	} {
		a, c, dir := scopedFixture(t, taskmode.Work)
		putScopedFile(t, dir, "game.html", "ASTEROIDS")
		a.permissions = permission.New(appconfig.Permissions{Mode: "ask"}, func(context.Context, permission.Request) (permission.Decision, error) {
			return permission.Decision{Allow: true}, nil
		})
		raw := taskArgs(map[string]any{"command": command, "verification": map[string]any{"paths": []string{"game.html"}, "purpose": "Check file"}})
		r, o := runScopedTool(t, a, c, "quoted", "run_command", raw)
		if o.Failed || !c.assess().done {
			t.Fatalf("%q: %+v", command, r)
		}
	}
	for _, command := range []string{"false\ntrue", "false; true", "false || true", "false | cat", "false > out", "! false", "echo $(false)", "echo \"`false`\"", "echo \"$(false)\"", "if false; then true; fi", "echo 'unterminated"} {
		if _, reason := scopedVerificationChain(command, t.TempDir()); reason == "" {
			t.Errorf("accepted %q", command)
		}
	}
}

func TestTaskCompletionHoldsUnacceptedFinal(t *testing.T) {
	a, c, _ := scopedFixture(t, taskmode.Developer)
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		switch call {
		case 1:
			return graphToolResponse("write", "write_file", `{"path":"output.txt","content":"RESULT"}`), nil
		case 2:
			return provider.Response{Content: "PREMATURE everything is tested"}, nil
		case 3:
			return graphToolResponse("check", "validate_artifact", `{"path":"output.txt","required_text":["RESULT"]}`), nil
		default:
			return provider.Response{Content: "FINAL content check passed"}, nil
		}
	}}
	a.client = client
	a.completionPlan = c.board
	a.completionStore = c.store
	var displayed strings.Builder
	_, err := a.Run(t.Context(), "Make output", func(e event.Event) {
		if e.Kind == event.KindTextDelta {
			displayed.WriteString(e.Text)
		}
	})
	if err != nil || strings.Contains(displayed.String(), "PREMATURE") || strings.Count(displayed.String(), "FINAL") != 1 {
		t.Fatalf("err=%v displayed=%q", err, displayed.String())
	}
}

// Existing sessions can classify helpers in place; moving or rebuilding their
// outputs is unnecessary. Exercise the real update_plan tool in Developer mode.
func TestTaskCompletionExistingHelpersCanBeClassified(t *testing.T) {
	a, c, _ := scopedFixture(t, taskmode.Developer)
	a.registry.Add(plan.Tool(c.board))
	for _, path := range []string{"analyze.sh", "inject.sh", "verify.sh"} {
		runScopedTool(t, a, c, path, "write_file", taskArgs(map[string]any{"path": path, "content": "exit 0"}))
	}
	runScopedTool(t, a, c, "output", "write_file", `{"path":"dashboard.html","content":"sunspots"}`)
	runScopedTool(t, a, c, "checked", "validate_artifact", `{"path":"dashboard.html","required_text":["sunspots"]}`)
	if c.assess().done {
		t.Fatal("unclassified project files were silently discarded")
	}
	runScopedTool(t, a, c, "classify", "update_plan", `{"goal":"Dashboard","steps":[{"id":1,"title":"Produce and check dashboard","status":"done","evidence":"Native artifact check passed"}],"artifacts":[{"path":"dashboard.html","role":"deliverable"},{"path":"analyze.sh","role":"scratch"},{"path":"inject.sh","role":"scratch"},{"path":"verify.sh","role":"scratch"}]}`)
	if d := c.assess(); !d.done {
		t.Fatalf("helpers could not be classified: %+v", d)
	}
}

func TestTaskCompletionCheckingScratchDoesNotPromoteIt(t *testing.T) {
	for _, mode := range []taskmode.Mode{taskmode.Work, taskmode.Developer} {
		t.Run(string(mode), func(t *testing.T) {
			a, c, _ := scopedFixture(t, mode)
			runScopedTool(t, a, c, "helper", "write_file", `{"path":".collomia-tmp/notes.txt","content":"first"}`)
			runScopedTool(t, a, c, "inspect", "validate_artifact", `{"path":".collomia-tmp/notes.txt","required_text":["first"]}`)
			runScopedTool(t, a, c, "revise", "write_file", `{"path":".collomia-tmp/notes.txt","content":"second"}`)
			if d := c.assess(); !d.done {
				t.Fatalf("scratch inspection created a stale deliverable: %+v", d)
			}
		})
	}
}
