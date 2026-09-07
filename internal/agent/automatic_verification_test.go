package agent

import (
	"encoding/json"
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
)

func automaticBuildFixture(t *testing.T, mode taskmode.Mode) (*Agent, *completionController, string) {
	t.Helper()
	a, c, dir := scopedFixture(t, mode)
	for _, path := range []string{"frontend/src", "fixture-bin"} {
		if err := os.MkdirAll(filepath.Join(dir, path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	putScopedFile(t, dir, "frontend/package.json", `{"scripts":{"build":"fixture"}}`)
	putScopedFile(t, dir, "frontend/package-lock.json", "LOCK")
	putScopedFile(t, dir, "frontend/src/main.js", "READY")
	// Real native process and meaningful assertions, no downloaded dependencies.
	checker := "#!/bin/sh\nset -e\ntest \"$*\" = 'run build'\ngrep -q READY src/main.js\ngrep -q build package.json\ngrep -qx LOCK package-lock.json\nmkdir -p dist\nprintf generated > dist/app.js\n"
	if err := os.WriteFile(filepath.Join(dir, "fixture-bin/npm"), []byte(checker), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(dir, "fixture-bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	return a, c, dir
}

func TestKanban29ProjectBuildFinishesAfterDocumentationAndNarrowerCheck(t *testing.T) {
	for _, mode := range []taskmode.Mode{taskmode.Developer, taskmode.Work} {
		t.Run(string(mode), func(t *testing.T) {
			a, c, _ := automaticBuildFixture(t, mode)
			a.registry.Add(plan.Tool(c.board))
			a.completionPlan, a.completionStore = c.board, c.store
			calls := []provider.ToolCall{
				{ID: "bad-scope", Name: "run_command", Arguments: json.RawMessage(`{"command":"cd frontend && npm run build","verification":{"paths":[],"purpose":"Build"}}`)},
				{ID: "build", Name: "run_command", Arguments: json.RawMessage(`{"command":"cd frontend && npm run build"}`)},
				{ID: "docs", Name: "write_file", Arguments: json.RawMessage(`{"path":"README.md","content":"Run npm run build inside frontend."}`)},
				{ID: "rejected", Name: "run_command", Arguments: json.RawMessage(`{"command":"cat README.md | head -c 10","verification":{"paths":["README.md"],"purpose":"Check docs"}}`)},
				{ID: "docs-check", Name: "validate_artifact", Arguments: json.RawMessage(`{"path":"README.md","format":"markdown","required_text":["npm run build"]}`)},
				{ID: "narrower", Name: "run_command", Arguments: json.RawMessage(`{"command":"cd frontend && npm run build","verification":{"paths":["frontend/src"],"purpose":"Check source"}}`)},
				{ID: "plan", Name: "update_plan", Arguments: json.RawMessage(`{"goal":"Build and document app","steps":[{"id":1,"title":"Build and check","status":"done","evidence":"Native project build and docs check passed"}],"artifacts":[{"path":"frontend","role":"deliverable"},{"path":"README.md","role":"deliverable"}]}`)},
			}
			client := &fakeClient{chat: func(n int, req provider.Request) (provider.Response, error) {
				if n <= len(calls) {
					return provider.Response{ToolCalls: []provider.ToolCall{calls[n-1]}}, nil
				}
				return provider.Response{Content: "Built and checked; documentation ready."}, nil
			}}
			a.client = client
			notices := 0
			_, err := a.Run(t.Context(), "Build and document the frontend", func(e event.Event) {
				if event.CompletionNoticeSummary(e.Text) != "" {
					notices++
				}
			})
			if err != nil || notices != 0 || client.calls != len(calls)+1 {
				t.Fatalf("err=%v notices=%d calls=%d", err, notices, client.calls)
			}
			state, err := decodeCompletion(c.store.LoadCompletion())
			if err != nil || state.Dirty || len(state.Failures)+len(state.Roles) != 0 {
				t.Fatalf("unfinished state: %+v %v", state, err)
			}
		})
	}
}

func TestAutomaticProjectBuildRetainsFailureFreshnessAndPermissions(t *testing.T) {
	for _, change := range []string{"failed-build", "source-drift", "lock-drift", "during-build", "new-input", "denied-input", "explicit-narrow", "masked-status"} {
		t.Run(change, func(t *testing.T) {
			a, c, dir := automaticBuildFixture(t, taskmode.Developer)
			c.syncArtifactBrief(&plan.Plan{Artifacts: []plan.Artifact{{Path: "frontend", Role: "deliverable"}}})
			raw := `{"command":"cd frontend && npm run build"}`
			switch change {
			case "failed-build":
				putScopedFile(t, dir, "frontend/src/main.js", "BROKEN")
			case "during-build":
				if err := os.WriteFile(filepath.Join(dir, "fixture-bin/npm"), []byte("#!/bin/sh\nprintf CHANGED > package-lock.json\n"), 0700); err != nil {
					t.Fatal(err)
				}
			case "denied-input":
				a.permissions = permission.New(appconfig.Permissions{Mode: "autopilot", Rules: []appconfig.Rule{{Action: "deny", Path: completionPath(filepath.Join(dir, "frontend/package-lock.json"))}}}, nil)
			case "explicit-narrow":
				raw = `{"command":"cd frontend && npm run build","verification":{"paths":["frontend/src"],"purpose":"Source only"}}`
			case "masked-status":
				raw = `{"command":"cd frontend && npm run build | cat"}`
			}
			_, o := runScopedTool(t, a, c, "build", "run_command", raw)
			if change == "denied-input" && (!o.Failed || !o.ExecutionPrevented || o.InputCorrection) {
				t.Fatalf("permission bypass: %+v", o)
			}
			if change == "failed-build" && (!o.Failed || len(o.ScopedFiles) > 0 || len(c.failures) != 1) {
				t.Fatalf("failed build accepted: %+v", o)
			}
			if change == "during-build" && (!o.Failed || o.InputCorrection || len(o.ScopedFiles) > 0) {
				t.Fatalf("changed inputs accepted: %+v", o)
			}
			switch change {
			case "source-drift":
				putScopedFile(t, dir, "frontend/src/main.js", "CHANGED")
			case "lock-drift":
				putScopedFile(t, dir, "frontend/package-lock.json", "CHANGED")
			case "new-input":
				putScopedFile(t, dir, "frontend/new.js", "NEW")
			}
			issues := c.checkArtifacts(nil, false)
			if len(issues) == 0 {
				t.Fatal("unverified project accepted")
			}
			if change == "explicit-narrow" && !strings.Contains(strings.Join(issues, " "), "current narrower scopes: \"frontend/src\"") {
				t.Fatalf("imprecise gap: %v", issues)
			}
		})
	}
}

func TestAutomaticProjectScopeInferenceIsConservative(t *testing.T) {
	_, _, dir := automaticBuildFixture(t, taskmode.Work)
	for _, command := range []string{"npm run build", "cd frontend && npm run build -- --mode test", "cd frontend && npm run build; true", "cd frontend && npm run build | cat", "touch changed && cd frontend && npm run build", "cd $PROJECT && npm run build", "cd frontend && echo 'x && npm run build'", "npm --prefix frontend run build"} {
		raw := json.RawMessage(taskArgs(map[string]string{"command": command}))
		if string(automaticProjectVerification(raw, dir)) != string(raw) {
			t.Errorf("inferred ambiguous command: %s", command)
		}
	}
}
