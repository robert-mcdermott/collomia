package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/session"
	"github.com/robert-mcdermott/collomia/internal/taskmode"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

func bookkeepingFixture(t *testing.T, mode taskmode.Mode) (*Agent, *completionController, string) {
	t.Helper()
	a, c, dir := scopedFixture(t, mode)
	store, err := session.OpenAt(t.TempDir(), dir)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.New("fixture", "model")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	m := session.NewContextManager(nil)
	m.Use(sess)
	for _, tool := range session.ContextTools(m) {
		a.registry.Add(tool)
	}
	a.registry.Add(plan.Tool(c.board))
	a.completionPlan, a.completionStore = c.board, c.store
	return a, c, dir
}

func TestSessionContextToolsStayOutsideCompletionEvidence(t *testing.T) {
	for _, tool := range session.ContextTools(session.NewContextManager(nil)) {
		name := tool.Definition().Name
		if !completionMetaTool(name) || parentOnlyTool(name) {
			t.Fatalf("session housekeeping tool %s must be available to workers without becoming task evidence", name)
		}
	}
}

// Exercise the actual runner, native notes, real file writes/checks and terminal
// decision, not just a handcrafted successful plan. Kanban27's note errors must
// not start an ID-reconciliation loop, even when the last note stays unsaved.
func TestKanban27VerifiedWorkFinishesDespiteBookkeepingErrors(t *testing.T) {
	for _, mode := range []taskmode.Mode{taskmode.Developer, taskmode.Work} {
		for _, graph := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/graph=%t", mode, graph), func(t *testing.T) {
				a, _, dir := bookkeepingFixture(t, mode)
				bin := filepath.Join(dir, ".collomia-tmp", "bin")
				if err := os.MkdirAll(bin, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(bin, "npm"), []byte("#!/bin/sh\ngrep -qx READY report.txt\n"), 0700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
				if graph {
					g, err := goalgraph.New(goalgraph.Spec{Goal: "prepare report", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "write and verify report", Acceptance: []string{"report check passes"}}}}, 1, goalgraph.Options{})
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
				check := json.RawMessage(`{"command":"npm run build","verification":{"paths":["report.txt"],"purpose":"Check requested report content"}}`)
				calls := []provider.ToolCall{
					{ID: "bad-role", Name: "update_task_context", Arguments: json.RawMessage(`{"expected_revision":0,"artifacts":[{"reference":"report.txt","note":"requested report","role":"deliverable"}]}`)},
					{ID: "corrected-notes", Name: "update_task_context", Arguments: json.RawMessage(`{"expected_revision":0,"objective":"Prepare report"}`)},
					{ID: "write", Name: "write_file", Arguments: json.RawMessage(`{"path":"report.txt","content":"BROKEN"}`)},
					{ID: "failed-check", Name: "run_command", Arguments: check},
					{ID: "repair", Name: "write_file", Arguments: json.RawMessage(`{"path":"report.txt","content":"READY"}`)},
					{ID: "passed-check", Name: "run_command", Arguments: check},
					{ID: "bad-field", Name: "update_task_context", Arguments: json.RawMessage(`{"expected_revision":1,"":"malformed notes"}`)},
					{ID: "corrected-final-notes", Name: "update_task_context", Arguments: json.RawMessage(`{"expected_revision":1,"objective":"Report ready"}`)},
					{ID: "stale-notes", Name: "update_task_context", Arguments: json.RawMessage(`{"expected_revision":1,"objective":"Report ready"}`)},
				}
				client := &fakeClient{chat: func(n int, request provider.Request) (provider.Response, error) {
					if n <= len(calls) {
						return provider.Response{ToolCalls: []provider.ToolCall{calls[n-1]}}, nil
					}
					return provider.Response{Content: "Report ready. Its content check passed."}, nil
				}}
				a.client = client
				var terminalErrors, interventions int
				answer, err := a.Run(t.Context(), "Write and check the report", func(e event.Event) {
					if e.Kind == event.KindError {
						terminalErrors++
					}
					if event.CompletionNoticeSummary(e.Text) != "" {
						interventions++
					}
				})
				if err != nil || terminalErrors != 0 || interventions != 0 || client.calls != len(calls)+1 {
					t.Fatalf("answer=%q error=%v errors=%d interventions=%d requests=%d", answer, err, terminalErrors, interventions, client.calls)
				}
				if graph && a.goalGraph.Snapshot().Outcome != goalgraph.OutcomeDone {
					t.Fatal("graph did not complete")
				}
			})
		}
	}
}

func TestBookkeepingCannotProveWorkEraseFailuresOrRenewProgress(t *testing.T) {
	for _, name := range []string{"update_plan", "detect_verification", "update_task_context", "read_task_context", "read_session", "search_session"} {
		t.Run(name, func(t *testing.T) {
			c := newCompletionController(plan.NewBoard(), t.TempDir(), false, taskmode.Developer)
			c.observe(toolObservation{CallID: "real-failure", Name: "run_command", Failed: true, Action: tools.Action{Risk: tools.RiskExecute}})
			progress := c.progressVersion
			for n := 0; n < 5; n++ {
				c.observe(toolObservation{CallID: fmt.Sprint("notes", n), Name: name, Failed: n%2 == 0, ResultSummary: fmt.Sprintf("revision %d", n), Action: tools.Action{Risk: tools.RiskRead}})
			}
			if len(c.failures) != 1 || c.failures[0].id != "real-failure" || c.progressVersion != progress {
				t.Fatalf("bookkeeping altered task failures or renewed progress: %+v", c)
			}
			p := &plan.Plan{Goal: "task", Steps: []plan.Step{{ID: 1, Title: "work", Status: "done", Evidence: "claims success"}}}
			for _, disposition := range []string{"recovered_by_retry", "recovered_by_alternative"} {
				if issue := c.validateFailureResolution(p, c.failures[0], plan.FailureResolution{FailureID: "real-failure", StepID: 1, Disposition: disposition, RecoveryToolCallID: "notes3", Evidence: "notes say complete"}); !strings.Contains(issue, "metadata") {
					t.Fatalf("notes accepted as proof: %q", issue)
				}
			}
		})
	}
}

func TestLegacyKanban27BookkeepingObligationsMigrateWithoutAcknowledgement(t *testing.T) {
	state := completionState{Schema: 1, Mode: taskmode.Developer, Sequence: 72, Failures: []recoveryFailure{
		{Sequence: 9, ID: "call_19trlhmb", Tool: "update_task_context", Risk: tools.RiskRead},
		{Sequence: 37, ID: "call_2an204wz", Tool: "update_task_context", Risk: tools.RiskRead},
	}}
	raw, _ := json.Marshal(state)
	store := &memoryCompletionStore{raw: raw}
	status, err := CompletionRecoveryStatus(store)
	if err != nil || strings.Contains(status, "call_19trlhmb") {
		t.Fatalf("stale status: %s %v", status, err)
	}
	c := newCompletionController(plan.NewBoard(), t.TempDir(), false, taskmode.Work)
	if err := c.restoreCompletion(store); err != nil {
		t.Fatal("obsolete notes pinned the old mode:", err)
	}
	if !c.assess().done || c.pending != nil {
		t.Fatal("legacy bookkeeping still blocks")
	}
	if string(store.LoadCompletion()) != string(raw) {
		t.Fatal("reading migration rewrote historical state")
	}
	// Filtering notes must not drop a real failed command, pending action or
	// file obligation. A resumed run still needs fresh checks of deliverables.
	state.Pending = &pendingEffect{Tool: "run_command", Unknown: true}
	state.Roles = map[string]string{"report.txt": "deliverable"}
	state.Failures = append(state.Failures, recoveryFailure{ID: "real-failure", Tool: "run_command", Risk: tools.RiskExecute})
	raw, _ = json.Marshal(state)
	decoded, err := decodeCompletion(raw)
	if err != nil || len(decoded.Failures) != 1 || decoded.Failures[0].ID != "real-failure" || decoded.Pending == nil || decoded.Roles["report.txt"] != "deliverable" {
		t.Fatalf("migration lost real obligations: %+v %v", decoded, err)
	}
}

func TestBookkeepingDoesNotWaiveTaskReadinessOrPersistence(t *testing.T) {
	a, c, dir := bookkeepingFixture(t, taskmode.Work)
	runScopedTool(t, a, c, "write", "write_file", `{"path":"report.txt","content":"READY"}`)
	runScopedTool(t, a, c, "validate", "validate_artifact", `{"path":"report.txt","required_text":["READY"]}`)
	if !c.assess().done {
		t.Fatal("fixture not verified")
	}
	if err := c.board.Set(plan.Plan{Goal: "task", Steps: []plan.Step{{ID: 1, Title: "Remaining requested work", Status: "pending"}}}); err != nil {
		t.Fatal(err)
	}
	runScopedTool(t, a, c, "bad-notes", "update_task_context", `{"expected_revision":0,"invalid":true}`)
	if c.assess().done {
		t.Fatal("notes error waived unfinished work")
	}
	if err := c.board.Set(plan.Plan{Goal: "task", Steps: []plan.Step{{ID: 1, Title: "Work", Status: "done", Evidence: "Checked report"}}}); err != nil {
		t.Fatal(err)
	}
	putScopedFile(t, dir, "report.txt", "CHANGED")
	runScopedTool(t, a, c, "notes", "update_task_context", `{"expected_revision":0,"objective":"Claims work complete"}`)
	if c.assess().done {
		t.Fatal("notes renewed stale evidence")
	}
	c.store.(*memoryCompletionStore).fail = errors.New("completion storage unavailable")
	if err := c.saveRecovery(true); err == nil {
		t.Fatal("housekeeping policy suppressed durable completion storage failure")
	}
}
