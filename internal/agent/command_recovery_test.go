package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/taskmode"
)

// Real loopback HTTP, native shell exit, shell diagnostics, workspace repair,
// deliberate retry and a meaningful check must finish in one agent turn.
// Kanban28 failed before diagnosis because curl's network classification was
// incorrectly treated as proof that the command had an uncertain outcome.
func TestKanban28HTTPFailureDiagnosisRepairAndCompletion(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("native curl fixture")
	}
	for _, mode := range []taskmode.Mode{taskmode.Developer, taskmode.Work} {
		for _, graph := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/graph=%t", mode, graph), func(t *testing.T) {
				a, c, dir := scopedFixture(t, mode)
				a.completionPlan, a.completionStore = c.board, c.store
				var requests atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if _, err := os.Stat(filepath.Join(dir, ".collomia-tmp")); err != nil {
						http.Error(w, "startup failed: database parent directory missing", http.StatusServiceUnavailable)
						return
					}
					fmt.Fprintln(w, "HEALTHY")
				}))
				t.Cleanup(srv.Close)
				bin := filepath.Join(dir, "fixture-bin")
				if err := os.MkdirAll(bin, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(bin, "npm"), []byte("#!/bin/sh\ntest -d .collomia-tmp && grep -qx READY report.txt\n"), 0700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
				if graph {
					g, err := goalgraph.New(goalgraph.Spec{Goal: "repair startup", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "repair and verify", Acceptance: []string{"health and report checks pass"}}}}, 1, goalgraph.Options{})
					if err != nil {
						t.Fatal(err)
					}
					a.goalGraph = g
					a.goalStateToken = func(context.Context) (string, error) {
						data, err := os.ReadFile(filepath.Join(dir, "report.txt"))
						if os.IsNotExist(err) {
							err = nil
						}
						return fmt.Sprintf("%x", sha256.Sum256(data)), err
					}
				}
				probe := json.RawMessage(taskArgs(map[string]any{"command": "curl --fail --silent --show-error " + srv.URL + "/api/health"}))
				calls := []provider.ToolCall{
					{ID: "failed-smoke", Name: "run_command", Arguments: probe},
					{ID: "diagnose", Name: "run_command", Arguments: json.RawMessage(`{"command":"test ! -d .collomia-tmp && printf 'database parent directory missing'"}`)},
					{ID: "repair", Name: "run_command", Arguments: json.RawMessage(`{"command":"mkdir -p .collomia-tmp"}`)},
					{ID: "retry", Name: "run_command", Arguments: probe},
					{ID: "deliver", Name: "write_file", Arguments: json.RawMessage(`{"path":"report.txt","content":"READY"}`)},
					{ID: "verify", Name: "run_command", Arguments: json.RawMessage(`{"command":"npm run build","verification":{"paths":["report.txt"],"purpose":"Verify repaired startup and requested report"}}`)},
				}
				client := &fakeClient{chat: func(n int, req provider.Request) (provider.Response, error) {
					if n == 2 {
						if requests.Load() != 1 {
							t.Fatal("runner automatically retried failed HTTP action")
						}
						var result string
						for _, m := range req.Messages {
							if m.Role == "tool" && m.ToolCallID == "failed-smoke" {
								result = m.Content
							}
						}
						if !strings.Contains(result, "without recovery acknowledgement") || !strings.Contains(result, "exit status 22") {
							t.Fatalf("missing failure/repair feedback: %s", result)
						}
					}
					if n <= len(calls) {
						return provider.Response{ToolCalls: []provider.ToolCall{calls[n-1]}}, nil
					}
					return provider.Response{Content: "Startup repaired; health and report checks passed."}, nil
				}}
				a.client = client
				var terminalErrors, interventions int
				answer, err := a.Run(t.Context(), "Diagnose the failed startup, repair it, and verify the result", func(e event.Event) {
					if e.Kind == event.KindError {
						terminalErrors++
					}
					if event.CompletionNoticeSummary(e.Text) != "" {
						interventions++
					}
				})
				if err != nil || terminalErrors != 0 || interventions != 0 || client.calls != len(calls)+1 || requests.Load() != 2 {
					t.Fatalf("answer=%q error=%v errors=%d interventions=%d calls=%d HTTP=%d", answer, err, terminalErrors, interventions, client.calls, requests.Load())
				}
				if graph && a.goalGraph.Snapshot().Outcome != goalgraph.OutcomeDone {
					t.Fatal("graph failed to complete")
				}
			})
		}
	}
}
