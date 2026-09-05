package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

func TestRejectedProviderResponseNeverFinishesOrExecutesTools(t *testing.T) {
	for _, mode := range []taskmode.Mode{taskmode.Developer, taskmode.Work} {
		for _, stop := range []string{"length", "max_tokens", "incomplete", "content_filter", "failed", "pause_turn", "unknown_status"} {
			for _, withTool := range []bool{false, true} {
				t.Run(string(mode)+"/"+stop+"/"+map[bool]string{false: "answer", true: "tools"}[withTool], func(t *testing.T) {
					executed := false
					registry := tools.NewRegistry(tools.Function{Def: provider.ToolDefinition{Name: "inspect"}, Action: tools.Action{Risk: tools.RiskRead}, Run: func(context.Context, json.RawMessage) (string, error) { executed = true; return "observed", nil }})
					client := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
						r := provider.Response{Content: "Partial answer", Stop: stop, Usage: provider.Usage{InputTokens: 20, OutputTokens: 10}}
						if withTool {
							r.ToolCalls = []provider.ToolCall{{ID: "unsafe", Name: "inspect", Arguments: json.RawMessage(`{}`)}}
						}
						return r, nil
					}}
					a := New(Options{Client: client, ProviderName: "fixture", Model: "model", Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), TaskMode: mode, CompletionPlan: plan.NewBoard(), MaxIterations: 4})
					var events []event.Event
					answer, err := a.Run(t.Context(), "inspect", func(e event.Event) { events = append(events, e) })
					if err == nil || GoalOutcomeFor(err) != GoalBlocked || answer != "Partial answer" || executed || client.calls != 1 {
						t.Fatalf("answer=%q err=%v executed=%v calls=%d", answer, err, executed, client.calls)
					}
					if a.Usage().InputTokens != 20 || a.Usage().OutputTokens != 10 {
						t.Fatalf("usage lost: %+v", a.Usage())
					}
					for _, m := range a.messages {
						if len(m.ToolCalls) > 0 {
							t.Fatal("unaccepted calls persisted")
						}
					}
					if !strings.Contains(a.messages[len(a.messages)-1].Content, "[Runtime response status]") {
						t.Fatal("resume lost partial status")
					}
					if events[len(events)-1].Kind != event.KindTurnEnd {
						t.Fatal("missing terminal event")
					}
				})
			}
		}
	}
}

func TestTruncationWithoutCompletionControllerAndBudgetAccounting(t *testing.T) {
	client := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
		return provider.Response{Content: "partial", Stop: "length", Usage: provider.Usage{OutputTokens: 20000}}, nil
	}}
	a := New(Options{Client: client, ProviderName: "fixture", Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{}, nil)})
	if _, err := a.Run(t.Context(), "answer", nil); !errors.Is(err, provider.ErrResponseTruncated) {
		t.Fatalf("without controller: %v", err)
	}
	a.tokenBudget = 10000
	if _, err := a.Run(t.Context(), "continue", nil); !errors.Is(err, ErrTokenBudgetExceeded) || client.calls != 1 {
		t.Fatalf("budget reset or extra request: err=%v calls=%d", err, client.calls)
	}
}

func TestRejectedEmptyResponseDoesNotPoisonNextTurn(t *testing.T) {
	client := &fakeClient{chat: func(call int, request provider.Request) (provider.Response, error) {
		if call == 1 {
			return provider.Response{Stop: "content_filter"}, nil
		}
		for _, message := range request.Messages {
			if message.Role == "assistant" && strings.TrimSpace(message.Content) == "" && len(message.ToolCalls) == 0 {
				t.Fatal("empty rejected assistant message was replayed to the provider")
			}
		}
		return provider.Response{Content: "A new answer.", Stop: "stop"}, nil
	}}
	a := New(Options{Client: client, Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{}, nil)})
	if _, err := a.Run(t.Context(), "first", nil); !errors.Is(err, provider.ErrResponseRefused) {
		t.Fatal(err)
	}
	if answer, err := a.Run(t.Context(), "a different request", nil); err != nil || answer != "A new answer." {
		t.Fatalf("next turn failed: answer=%q err=%v", answer, err)
	}
}

func TestRejectedCompactionRetainsOriginalContext(t *testing.T) {
	client := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
		return provider.Response{Content: "partial summary", Stop: "max_tokens", Usage: provider.Usage{OutputTokens: 10}}, nil
	}}
	compacted := false
	a := New(Options{Client: client, Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{}, nil), OnCompaction: func(provider.Message, int) { compacted = true }})
	var messages []provider.Message
	for i := 0; i < 14; i++ {
		messages = append(messages, provider.Message{Role: "user", Content: "Keep the original constraint and evidence."})
	}
	a.SetMessages(messages)
	if _, err := a.Compact(t.Context(), ""); !errors.Is(err, provider.ErrResponseTruncated) {
		t.Fatalf("compaction err=%v", err)
	}
	if compacted || !reflect.DeepEqual(a.messages, messages) || a.Usage().OutputTokens != 10 {
		t.Fatal("incomplete summary replaced context or lost usage")
	}
}

func TestGoalGraphRejectsTruncatedCompletion(t *testing.T) {
	graph, err := goalgraph.New(goalgraph.Spec{Goal: "inspect", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "inspect"}}}, 1, goalgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
		return provider.Response{Content: "partial", Stop: "length"}, nil
	}}
	a := New(Options{Client: client, ProviderName: "fixture", Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{}, nil), GoalGraph: graph, GoalStateToken: func(context.Context) (string, error) { return "workspace", nil }})
	if _, err := a.Run(t.Context(), "inspect", nil); !errors.Is(err, provider.ErrResponseTruncated) {
		t.Fatalf("err=%v", err)
	}
	if outcome, _ := graph.Outcome(); outcome != goalgraph.OutcomeBlocked || client.calls != 1 {
		t.Fatalf("outcome=%s calls=%d", outcome, client.calls)
	}
}

func TestRetryIdentityKeepsDifferentOperationsUnresolved(t *testing.T) {
	for _, tc := range []struct{ name, first, second string }{
		{"read_file", `{"path":"required.csv"}`, `{"path":"README.md"}`},
		{"read_file", `{"path":"required.csv","offset":1}`, `{"path":"required.csv","offset":2}`},
		{"run_command", `{"command":"go test ./..."}`, `{"command":"pwd"}`},
		{"run_command", `{"command":"go test","cwd":"a"}`, `{"command":"go test","cwd":"b"}`},
		{"mcp_service", `{"id":9007199254740992}`, `{"id":9007199254740993}`},
	} {
		c := newCompletionController(plan.NewBoard(), t.TempDir(), false, taskmode.Work)
		first := toolRetryKey(provider.ToolCall{Name: tc.name, Arguments: json.RawMessage(tc.first)})
		second := toolRetryKey(provider.ToolCall{Name: tc.name, Arguments: json.RawMessage(tc.second)})
		c.observe(toolObservation{CallID: "failed", Name: tc.name, RetryKey: first, Failed: true})
		c.observe(toolObservation{CallID: "different", Name: tc.name, RetryKey: second})
		if len(c.failures) != 1 {
			t.Fatalf("%s erased unrelated failure", tc.name)
		}
		c.observe(toolObservation{CallID: "retry", Name: tc.name, RetryKey: first})
		if !c.assess().done {
			t.Fatalf("%s exact successful retry requires ceremony", tc.name)
		}
	}
	if toolRetryKey(provider.ToolCall{Name: "read_file", Arguments: json.RawMessage(`{"path":"a","limit":1}`)}) != toolRetryKey(provider.ToolCall{Name: "read_file", Arguments: json.RawMessage(`{ "limit": 1, "path": "a" }`)}) {
		t.Fatal("JSON key order changed retry identity")
	}
	for _, raw := range []string{`{`, `{} {}`} {
		if toolRetryKey(provider.ToolCall{Name: "read_file", Arguments: json.RawMessage(raw)}) != "" {
			t.Fatal("invalid JSON has retry identity")
		}
	}
}

func TestActualReadRetryRecoversWithoutPlanMetadata(t *testing.T) {
	for _, mode := range []taskmode.Mode{taskmode.Developer, taskmode.Work} {
		t.Run(string(mode), func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("readme"), 0o600); err != nil {
				t.Fatal(err)
			}
			guard, err := tools.NewPathGuard(root, false)
			if err != nil {
				t.Fatal(err)
			}
			client := &fakeClient{chat: func(call int, req provider.Request) (provider.Response, error) {
				switch call {
				case 1:
					return graphToolResponse("failed-read", "read_file", `{"path":"required.csv","limit":1}`), nil
				case 2:
					if err := os.WriteFile(filepath.Join(root, "required.csv"), []byte("value\n1"), 0o600); err != nil {
						t.Fatal(err)
					}
					return graphToolResponse("unrelated-read", "read_file", `{"path":"README.md"}`), nil
				case 3:
					return provider.Response{Content: "Done."}, nil
				case 4:
					if !requestContains(req, "unresolved tool failure failed-read") {
						t.Fatal("unrelated read cleared the failed input")
					}
					return graphToolResponse("exact-retry", "read_file", `{"limit":1,"path":"required.csv"}`), nil
				case 5:
					return provider.Response{Content: "Required input read."}, nil
				default:
					t.Fatalf("unexpected recovery loop: call=%d", call)
					return provider.Response{}, nil
				}
			}}
			a := New(Options{Client: client, Workspace: root, Registry: tools.NewRegistry(tools.ReadFileTool{Guard: guard}), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), TaskMode: mode, CompletionPlan: plan.NewBoard(), MaxIterations: 8})
			answer, err := a.Run(t.Context(), "Read required.csv", nil)
			if err != nil || answer != "Required input read." || client.calls != 5 {
				t.Fatalf("answer=%q err=%v calls=%d", answer, err, client.calls)
			}
		})
	}
}

func TestDifferentSameToolReceiptNeedsExplicitAlternative(t *testing.T) {
	board := plan.NewBoard()
	c := newCompletionController(board, t.TempDir(), false, taskmode.Work)
	c.observe(toolObservation{CallID: "failed", Name: "read_file", RetryKey: "required-input", Failed: true})
	c.observe(toolObservation{CallID: "alternate", Name: "read_file", RetryKey: "alternate-input"})
	p := &plan.Plan{Goal: "inspect input", Steps: []plan.Step{{ID: 1, Title: "inspect input", Status: "done", Evidence: "an alternative input supplies the needed facts"}}}
	r := plan.FailureResolution{FailureID: "failed", Disposition: "recovered_by_retry", StepID: 1, RecoveryToolCallID: "alternate", Evidence: "alternative input"}
	if issue := c.validateFailureResolution(p, c.failures[0], r); !strings.Contains(issue, "not the same operation") {
		t.Fatalf("an unrelated same-tool receipt masqueraded as a retry: %q", issue)
	}
	r.Disposition = "recovered_by_alternative"
	if issue := c.validateFailureResolution(p, c.failures[0], r); issue != "" {
		t.Fatalf("explicit alternative rejected: %s", issue)
	}
	r.Disposition, p.Steps[0].Status = "skipped_unnecessary", "skipped"
	r.RecoveryToolCallID = ""
	if issue := c.validateFailureResolution(p, c.failures[0], r); issue != "" {
		t.Fatalf("unnecessary exploratory miss forced a blocker: %s", issue)
	}
}
