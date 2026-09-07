package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/taskmode"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

func TestResponseLimitOllamaReasoningStream(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			MaxTokens int `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.MaxTokens != 32000 {
			t.Errorf("response limit changed: max_tokens=%d err=%v", req.MaxTokens, err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Planning the next step\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}],\"usage\":{\"prompt_tokens\":9107,\"completion_tokens\":32000}}\n\ndata: [DONE]\n\n")
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"A concise complete answer.\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":9200,\"completion_tokens\":10}}\n\ndata: [DONE]\n\n")
		}
	}))
	defer server.Close()
	cfg := appconfig.Provider{Type: "openai-compatible", BaseURL: server.URL + "/v1", Context: 262000, MaxTokens: 32000}
	client, err := provider.New("ollama", cfg, "deepseek-v4-flash:0731-cloud")
	if err != nil {
		t.Fatal(err)
	}
	a, _, _ := scopedFixture(t, taskmode.Developer)
	a.client, a.providerConfig, a.providerName = client, cfg, "ollama"
	reasoning, warnings := 0, 0
	answer, err := a.Run(t.Context(), "Answer concisely", func(e event.Event) {
		if e.Kind == event.KindReasoningDelta {
			reasoning++
		}
		if e.Kind == event.KindWarning {
			warnings++
		}
		if e.Kind == event.KindError {
			t.Errorf("recovery emitted error: %s", e.Error)
		}
	})
	if err != nil || answer != "A concise complete answer." || requests.Load() != 2 || reasoning != 1 || warnings != 1 || a.Usage().OutputTokens != 32010 {
		t.Fatalf("answer=%q err=%v requests=%d reasoning=%d warnings=%d usage=%+v", answer, err, requests.Load(), reasoning, warnings, a.Usage())
	}
}

func TestResponseLimitContinuesWithoutReplayingTools(t *testing.T) {
	for _, mode := range []string{"developer", "work", "planning", "graph", "graph-worker"} {
		t.Run(mode, func(t *testing.T) {
			executions, warnings := 0, 0
			registry := tools.NewRegistry(tools.Function{Def: provider.ToolDefinition{Name: "read_file"}, Action: tools.Action{Risk: tools.RiskRead}, Run: func(context.Context, json.RawMessage) (string, error) {
				executions++
				return "observed workspace fact", nil
			}})
			client := &fakeClient{chat: func(call int, req provider.Request) (provider.Response, error) {
				for _, m := range req.Messages {
					for _, c := range m.ToolCalls {
						if c.ID == "discarded" {
							t.Fatal("truncated call became pending history")
						}
					}
				}
				if call == 3 {
					found := false
					for _, m := range req.Messages {
						found = found || strings.Contains(m.Content, "Runtime response continuation")
					}
					if !found {
						t.Fatal("continuation did not give smaller-step guidance")
					}
				}
				r := provider.Response{Usage: provider.Usage{InputTokens: 9107, OutputTokens: 20}}
				switch call {
				case 1:
					r.ToolCalls = []provider.ToolCall{{ID: "completed", Name: "read_file", Arguments: json.RawMessage(`{}`)}}
				case 2:
					r.Stop, r.Usage.OutputTokens = "length", 32000
					r.ToolCalls = []provider.ToolCall{{ID: "discarded", Name: "read_file", Arguments: json.RawMessage(`{"path":`)}}
				default:
					r.Content = "The inspected fact answers the question."
				}
				return r, nil
			}}
			opts := Options{Client: client, Workspace: t.TempDir(), Registry: registry, Permissions: permission.New(appconfig.Permissions{Mode: "autopilot"}, nil), TaskMode: taskmode.Developer}
			if mode == "work" {
				opts.TaskMode = taskmode.Work
			}
			if mode == "planning" {
				opts.PlanMode = true
			}
			if mode == "graph-worker" {
				opts.Subagent, opts.GraphWorker, opts.MaxTurnIterations = true, true, 4
			}
			if mode == "graph" {
				g, err := goalgraph.New(goalgraph.Spec{Goal: "inspect", Nodes: []goalgraph.NodeSpec{{ID: 1, Title: "inspect"}}}, 1, goalgraph.Options{})
				if err != nil {
					t.Fatal(err)
				}
				opts.GoalGraph = g
				opts.GoalStateToken = func(context.Context) (string, error) { return "unchanged", nil }
			}
			a := New(opts)
			answer, err := a.Run(t.Context(), "Inspect the workspace fact and answer", func(e event.Event) {
				if e.Kind == event.KindError {
					t.Fatalf("recovered limit emitted a terminal error: %s", e.Error)
				}
				if e.Kind == event.KindWarning && strings.Contains(e.Text, "response limit") {
					warnings++
				}
			})
			if err != nil || answer == "" || client.calls != 3 || executions != 1 || warnings != 1 {
				t.Fatalf("answer=%q err=%v calls=%d executions=%d warnings=%d", answer, err, client.calls, executions, warnings)
			}
			if a.Usage().OutputTokens != 32040 || a.ProviderIterations() != 3 {
				t.Fatalf("lost limit accounting: %+v/%d", a.Usage(), a.ProviderIterations())
			}
			if opts.GoalGraph != nil {
				s := opts.GoalGraph.Snapshot()
				if s.Outcome != goalgraph.OutcomeDone || len(s.Attempts) != 1 || len(s.Attempts[0].Failures) != 0 {
					t.Fatal("recovered limit poisoned graph", s.Outcome)
				}
			}
		})
	}
}

func TestResponseLimitAllowanceDoesNotResetAfterUsableResponses(t *testing.T) {
	a, _, _ := scopedFixture(t, taskmode.Developer)
	a.registry.Add(tools.Function{Def: provider.ToolDefinition{Name: "inspect"}, Action: tools.Action{Risk: tools.RiskRead}, Run: func(context.Context, json.RawMessage) (string, error) { return "observed", nil }})
	client := &fakeClient{chat: func(call int, _ provider.Request) (provider.Response, error) {
		if call%2 == 1 {
			return provider.Response{Stop: "length"}, nil
		}
		return provider.Response{ToolCalls: []provider.ToolCall{{ID: "inspect", Name: "inspect", Arguments: json.RawMessage(`{}`)}}}, nil
	}}
	a.client = client
	_, err := a.Run(t.Context(), "Inspect", nil)
	if !errors.Is(err, provider.ErrResponseTruncated) || GoalOutcomeFor(err) != GoalBudgetExhausted || client.calls != 5 {
		t.Fatalf("unbounded retries: %d %v", client.calls, err)
	}
}

func TestResponseLimitContinuationRespectsBudgetAndCancellation(t *testing.T) {
	for _, bound := range []string{"iterations", "tokens", "cancel"} {
		t.Run(bound, func(t *testing.T) {
			a, _, _ := scopedFixture(t, taskmode.Work)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := ErrIterationBudgetExceeded
			if bound == "iterations" {
				a.maxTurnIterations = 1
			}
			if bound == "tokens" {
				a.tokenBudget, want = 10000, ErrTokenBudgetExceeded
			}
			if bound == "cancel" {
				want = context.Canceled
			}
			client := &fakeClient{chat: func(int, provider.Request) (provider.Response, error) {
				return provider.Response{Stop: "length", Usage: provider.Usage{OutputTokens: 10001}}, nil
			}}
			a.client = client
			_, err := a.Run(ctx, "Answer", func(e event.Event) {
				if bound == "cancel" && e.Kind == event.KindWarning {
					cancel()
				}
			})
			if !errors.Is(err, want) || client.calls != 1 {
				t.Fatalf("continuation escaped %s: calls=%d err=%v", bound, client.calls, err)
			}
		})
	}
}

func TestResponseLimitWorkerResultIsExtendable(t *testing.T) {
	a, _, _ := scopedFixture(t, taskmode.Developer)
	a.client = &fakeClient{chat: func(int, provider.Request) (provider.Response, error) { return provider.Response{Stop: "length"}, nil }}
	cfg := appconfig.Defaults()
	_, _, _, _, _, _, _, iterations, _, _, err := a.runDelegateTask(t.Context(), "limited", DelegateTask{Name: "read", Task: "inspect", GraphNode: true, MaxIterationsOverride: 8}, appconfig.AgentDefinition{}, cfg, nil, nil)
	if !errors.Is(err, provider.ErrResponseTruncated) || iterations != 3 || delegateTerminalStatus(err) != DelegateBudgetExhausted {
		t.Fatalf("worker limit cannot be extended: iterations=%d err=%v status=%s", iterations, err, delegateTerminalStatus(err))
	}
}
