package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/taskmode"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// Reproduce Kanban25 with native file/command tools and a local uv fixture,
// without downloading packages or contacting a model. An invalid patch, a
// completed install failure, inspection, repair and retry must finish in one
// turn. Neither the rejected arguments nor networking may create a false latch.
func TestKanban25CorrectionCompletesAcrossModes(t *testing.T) {
	for _, mode := range []taskmode.Mode{taskmode.Developer, taskmode.Work} {
		for _, graph := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/graph=%t", mode, graph), func(t *testing.T) {
				a, c, dir := dependencyFixture(t, mode)
				a.completionPlan, a.completionStore = c.board, c.store
				if graph {
					g, err := goalgraph.New(goalgraph.Spec{Goal: "repair manifest", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "repair and verify manifest", Acceptance: []string{"manifest check passes"}}}}, 1, goalgraph.Options{})
					if err != nil {
						t.Fatal(err)
					}
					a.goalGraph = g
					a.goalStateToken = func(context.Context) (string, error) {
						data, err := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
						return fmt.Sprintf("%x", sha256.Sum256(data)), err
					}
				}
				calls := []provider.ToolCall{
					{ID: "malformed", Name: "apply_patch", Arguments: json.RawMessage(`{"operations":[{"op":"update","path":"pyproject.toml","old_text":"BEFORE","new_text":"","content":"AFTER"}]}`)},
					{ID: "install-failed", Name: "run_command", Arguments: json.RawMessage(dependencyCommand)},
					{ID: "inspect", Name: "read_file", Arguments: json.RawMessage(`{"path":"pyproject.toml"}`)},
					{ID: "repair", Name: "write_file", Arguments: json.RawMessage(`{"path":"pyproject.toml","content":"AFTER"}`)},
					{ID: "install-retry", Name: "run_command", Arguments: json.RawMessage(dependencyCommand)},
					{ID: "verification", Name: "run_command", Arguments: json.RawMessage(`{"command":"uv run pytest","verification":{"paths":["pyproject.toml"],"purpose":"Verify manifest repair"}}`)},
				}
				client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
					if call == 2 {
						data, err := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
						if err != nil || string(data) != "BEFORE" {
							t.Fatalf("malformed patch changed manifest: %q %v", data, err)
						}
					}
					if call == 3 {
						last := ""
						for _, message := range request.Messages {
							if message.Role == "tool" && message.ToolCallID == "install-failed" {
								last = message.Content
							}
						}
						if !strings.Contains(last, "exit status 2") || !strings.Contains(last, "without recovery acknowledgement") {
							t.Fatalf("missing observed failure/correction guidance: %s", last)
						}
					}
					if call <= len(calls) {
						return provider.Response{ToolCalls: []provider.ToolCall{calls[call-1]}}, nil
					}
					return provider.Response{Content: "Manifest repaired; installation and checks passed."}, nil
				}}
				a.client = client
				var terminalErrors, interventions int
				answer, err := a.Run(t.Context(), "Repair the manifest and check it", func(e event.Event) {
					if e.Kind == event.KindError {
						terminalErrors++
					}
					if event.CompletionNoticeSummary(e.Text) != "" {
						interventions++
					}
				})
				if err != nil || terminalErrors != 0 || interventions != 0 || client.calls != len(calls)+1 {
					t.Fatalf("answer=%q err=%v errors=%d interventions=%d requests=%d", answer, err, terminalErrors, interventions, client.calls)
				}
				if graph && a.goalGraph.Snapshot().Outcome != goalgraph.OutcomeDone {
					t.Fatal("graph did not complete")
				}
			})
		}
	}
}

const dependencyCommand = `{"command":"UV_CACHE_DIR=\"$PWD/.uv-cache\" UV_PYTHON_INSTALL_DIR=\"$PWD/.uv-python\" uv add fastapi \"uvicorn[standard]\" sqlalchemy"}`

func dependencyFixture(t *testing.T, mode taskmode.Mode) (*Agent, *completionController, string) {
	t.Helper()
	a, c, dir := scopedFixture(t, mode)
	guard, err := tools.NewPathGuard(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	a.registry.Add(tools.ApplyPatchTool{Guard: guard})
	a.registry.Add(tools.ReadFileTool{Guard: guard})
	bin := filepath.Join(dir, ".collomia-tmp", "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	fixture := "#!/bin/sh\nif grep -q AFTER pyproject.toml; then exit 0; fi\necho 'error: Project is missing a [project] table' >&2\nexit 2\n"
	if err := os.WriteFile(filepath.Join(bin, "uv"), []byte(fixture), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	putScopedFile(t, dir, "pyproject.toml", "BEFORE")
	return a, c, dir
}

func TestDependencyFailureAllowsRepairAfterResume(t *testing.T) {
	for _, mode := range []taskmode.Mode{taskmode.Developer, taskmode.Work} {
		t.Run(string(mode), func(t *testing.T) {
			a, c, dir := dependencyFixture(t, mode)
			_, o := runScopedTool(t, a, c, "failed", "run_command", dependencyCommand)
			if !o.CommandExited || !o.Action.Network || !o.Failed || c.pending != nil || len(c.failures) != 1 {
				t.Fatalf("install failure misclassified: %+v %+v", o, c.recoveryState())
			}
			next := newCompletionController(c.board, dir, false, mode)
			if err := next.restoreCompletion(c.store); err != nil {
				t.Fatal(err)
			}
			runScopedTool(t, a, next, "inspect", "read_file", `{"path":"pyproject.toml"}`)
			runScopedTool(t, a, next, "repair", "write_file", `{"path":"pyproject.toml","content":"AFTER"}`)
			if len(next.failures) != 1 || !next.dirty {
				t.Fatal("repair erased failed installation or verification obligations")
			}
			_, o = runScopedTool(t, a, next, "retry", "run_command", dependencyCommand)
			if o.Failed || len(next.failures) != 0 || next.pending != nil || !next.dirty {
				t.Fatalf("deliberate retry did not settle the failure: %+v", next.recoveryState())
			}
		})
	}
}

func TestFileInputFeedbackKeepsPriorFailuresAndValidation(t *testing.T) {
	a, c, _ := dependencyFixture(t, taskmode.Developer)
	runScopedTool(t, a, c, "failed-install", "run_command", dependencyCommand)
	for _, tc := range []struct{ name, raw string }{
		{"write_file", `{"path":"pyproject.toml"}`},
		{"edit_file", `{"path":"pyproject.toml","old_text":"BEFORE","content":"AFTER"}`},
		{"apply_patch", `{"operations":[{"op":"update","path":"pyproject.toml","old_text":"missing","new_text":"AFTER"}]}`},
	} {
		_, o := runScopedTool(t, a, c, tc.name, tc.name, tc.raw)
		if !o.Failed || !o.InputCorrection || !o.ExecutionPrevented || !o.IgnoreGraphFailure || c.pending != nil || len(c.failures) != 1 {
			t.Fatalf("input correction hid an executed failure or added a false fence: %+v %+v", o, c.recoveryState())
		}
	}
	runScopedTool(t, a, c, "repair", "write_file", `{"path":"pyproject.toml","content":"AFTER"}`)
	if !c.dirty || len(c.failures) != 1 || c.assess().done {
		t.Fatal("corrected file inputs waived failed install or current validation")
	}
}
