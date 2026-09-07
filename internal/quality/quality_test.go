package quality

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/robert-mcdermott/collomia/internal/agent"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/redact"
	"github.com/robert-mcdermott/collomia/internal/replay"
	"github.com/robert-mcdermott/collomia/internal/sandbox"
)

// Only deterministic in-process fixtures use this backend. Live runs always
// require the platform sandbox before constructing a provider client.
type fixtureBackend struct{ unavailable bool }

func (f fixtureBackend) Name() string { return "fixture" }
func (f fixtureBackend) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{WriteIsolation: true, ReadIsolation: true, NetworkIsolation: sandbox.NetworkFull, ProcessIsolation: true}
}
func (f fixtureBackend) Available() error {
	if f.unavailable {
		return errors.New("fixture unavailable")
	}
	return nil
}
func (f fixtureBackend) Wrap(argv []string, _ sandbox.Policy) ([]string, error) { return argv, nil }

type fixtureClient struct {
	err      error
	usage    bool
	calls    int
	response func(int) provider.Response
}

func (f *fixtureClient) Name() string { return "fixture/model" }
func (f *fixtureClient) Chat(_ context.Context, _ provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	f.calls++
	r := provider.Response{Content: "42"}
	if f.response != nil {
		r = f.response(f.calls)
	}
	if f.usage {
		r.Usage = provider.Usage{InputTokens: 20, OutputTokens: 10}
	}
	return r, f.err
}

func fixtureOptions(t *testing.T) Options {
	t.Helper()
	return Options{Directory: filepath.Join(t.TempDir(), "run"), Provider: "fixture", Model: "model", ProviderConfig: appconfig.Provider{Type: "openai", MaxTokens: 4096}, Settings: Settings{Trials: 1, TokenBudget: 20000, TotalTokens: 100000, TimeoutSeconds: 10, MaxIterations: 5}, Tasks: []Task{{ID: "answer", Mode: "work", Category: "fixture", Prompts: []string{"What is six times seven?"}, Files: map[string]string{}, ExpectedOutcome: "done", Checks: []Check{{Kind: "answer_contains", Want: "42"}}, Review: "Review correctness."}}, Factory: func() (provider.Client, error) { return &fixtureClient{usage: true}, nil }, Backend: fixtureBackend{}}
}

func TestSuiteBalancedStableAndSelectable(t *testing.T) {
	tasks := Suite()
	counts := map[string]int{}
	seen := map[string]bool{}
	for _, task := range tasks {
		if seen[task.ID] {
			t.Fatal("duplicate task")
		}
		seen[task.ID] = true
		counts[task.Mode]++
		if len(task.Checks) == 0 || task.Review == "" {
			t.Fatal("missing checks or rubric")
		}
	}
	if len(tasks) != 12 || counts["work"] != 6 || counts["developer"] != 6 {
		t.Fatalf("balance: %v", counts)
	}
	if Digest(tasks) != Digest(Suite()) {
		t.Fatal("unstable suite digest")
	}
	tasks[0].Prompts[0] += " changed"
	if Digest(tasks) == Digest(Suite()) {
		t.Fatal("prompt change retained digest")
	}
	if _, err := Select("missing"); err == nil {
		t.Fatal("unknown task accepted")
	}
	if _, err := Select("code_review,code_review"); err == nil {
		t.Fatal("duplicate selection accepted")
	}
}

func TestRunPersistsScorecardAndComparison(t *testing.T) {
	opts := fixtureOptions(t)
	opts.Settings.Trials = 2
	result, err := Run(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "complete" || len(result.Trials) != 2 || !result.Trials[0].MachinePass || result.Trials[0].Review != nil {
		t.Fatalf("result=%+v", result)
	}
	loaded, err := Load(opts.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compare(result, loaded); err != nil {
		t.Fatal(err)
	}
	loaded.Model = "different"
	if _, err := Compare(result, loaded); err == nil {
		t.Fatal("different models compared silently")
	}
	report := Report(result)
	if !strings.Contains(report, "2 pending") || !strings.Contains(report, "unavailable or incomplete") {
		t.Fatalf("report misstates acceptance or cost: %s", report)
	}
	for _, name := range []string{"events.jsonl", "messages.jsonl"} {
		data, err := os.ReadFile(filepath.Join(opts.Directory, "answer-01", name))
		if err != nil || len(data) == 0 {
			t.Fatalf("trace %s: %v", name, err)
		}
		if name == "events.jsonl" {
			if _, err := replay.Read(strings.NewReader(string(data))); err != nil {
				t.Fatalf("evaluation trace is not replay-compatible: %v", err)
			}
		}
	}
	if _, err := Run(t.Context(), opts); err == nil {
		t.Fatal("existing output overwritten")
	}
}

func TestRunBudgetAndUsageStops(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "budget", true: "usage"}[missing], func(t *testing.T) {
			opts := fixtureOptions(t)
			opts.Settings.Trials = 2
			opts.Settings.TotalTokens = opts.Settings.TokenBudget
			opts.Factory = func() (provider.Client, error) { return &fixtureClient{usage: !missing}, nil }
			result, err := Run(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if result.State != "stopped" || len(result.Trials) != 1 {
				t.Fatalf("limits ignored: %+v", result)
			}
			if missing && !strings.Contains(result.StopReason, "usage incomplete") {
				t.Fatal(result.StopReason)
			}
		})
	}
}

func TestRunPreflightMakesNoProviderCalls(t *testing.T) {
	opts := fixtureOptions(t)
	opts.Backend = fixtureBackend{unavailable: true}
	called := false
	opts.Factory = func() (provider.Client, error) { called = true; return &fixtureClient{}, nil }
	if _, err := Run(t.Context(), opts); err == nil || called {
		t.Fatal("provider ran before sandbox preflight")
	}
	if _, err := os.Stat(opts.Directory); !os.IsNotExist(err) {
		t.Fatal("preflight created run output")
	}
}

func TestTraceCountsAttemptsAndRedactsSecrets(t *testing.T) {
	r := redact.New()
	r.AddSecret("configured-secret")
	trace, err := newTrace(t.TempDir(), r.Redact)
	if err != nil {
		t.Fatal(err)
	}
	defer trace.close()
	for _, args := range []string{`{"path":"x","limit":1}`, `{ "limit": 1, "path": "x" }`} {
		trace.message(provider.Message{Role: "assistant", Content: "configured-secret", ToolCalls: []provider.ToolCall{{Name: "read_file", Arguments: json.RawMessage(args)}}})
	}
	if trace.calls != 2 || trace.repeats != 1 {
		t.Fatalf("calls=%d repeats=%d", trace.calls, trace.repeats)
	}
	trace.message(provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{Name: "read_file", Arguments: json.RawMessage(`{"offset":9007199254740993,"configured-secret":"value"}`)}}})
	data, err := os.ReadFile(trace.files[1].Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "configured-secret") {
		t.Fatal("secret in trace")
	}
	if !strings.Contains(string(data), "9007199254740993") {
		t.Fatal("trace lost exact integer tool arguments")
	}
	trace.bytes = 16 << 20
	trace.message(provider.Message{Role: "assistant", Content: "limit"})
	if trace.failure() == nil {
		t.Fatal("trace bound not enforced")
	}
}

func TestGraderChecksInputsAndConfinesReads(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "input.txt"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	checks := grade(t.Context(), Task{Files: map[string]string{"input.txt": "original"}, Checks: []Check{{Kind: "json_equal", Path: "missing.json", Want: `{}`}}}, workspace, "", nil, nil)
	if checks[0].Passed || checks[1].Passed {
		t.Fatal("changed or missing inputs accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	checks = grade(ctx, Task{Checks: []Check{{Kind: "no_changes"}}}, t.TempDir(), "", nil, nil)
	if checks[0].Passed {
		t.Fatal("cancelled workspace inspection accepted")
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "link")); err != nil {
		t.Skip(err)
	}
	if _, err := readOutput(workspace, "link"); err == nil {
		t.Fatal("grader read escaped workspace")
	}
}

func TestFalseDoneDoesNotBecomeHumanAcceptance(t *testing.T) {
	opts := fixtureOptions(t)
	opts.Tasks[0].Checks = []Check{{Kind: "answer_contains", Want: "different"}}
	result, err := Run(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	row := result.Trials[0]
	if row.MachinePass || !row.FalseDone || row.Review != nil {
		t.Fatalf("false done lost: %+v", row)
	}
}

func TestMissingUsageStopsBeforeToolEffects(t *testing.T) {
	opts := fixtureOptions(t)
	client := &fixtureClient{response: func(int) provider.Response {
		return provider.Response{ToolCalls: []provider.ToolCall{{ID: "write", Name: "write_file", Arguments: json.RawMessage(`{"path":"should-not-exist.txt","content":"no"}`)}}}
	}}
	opts.Factory = func() (provider.Client, error) { return client, nil }
	result, err := Run(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "stopped" || client.calls != 1 {
		t.Fatalf("result=%+v calls=%d", result, client.calls)
	}
	if _, err := os.Stat(filepath.Join(opts.Directory, "answer-01", "workspace", "should-not-exist.txt")); !os.IsNotExist(err) {
		t.Fatal("unaccounted response executed a tool")
	}
}

func TestFailedEvaluationTracesReplay(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		kind event.FailureKind
	}{
		{"budget", agent.ErrTokenBudgetExceeded, event.FailureUsage},
		{"blocked", agent.ErrGoalBlocked, event.FailureRuntime},
		{"cancelled", context.Canceled, event.FailureCancelled},
		{"timeout", context.DeadlineExceeded, event.FailureTimeout},
		{"provider", &provider.Error{Provider: "fixture", Kind: provider.ErrorProtocol, Message: "bad response"}, event.FailureProvider},
		{"provider_cancelled", &provider.Error{Provider: "fixture", Kind: provider.ErrorCancelled, Message: "cancelled"}, event.FailureCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := fixtureOptions(t)
			opts.Factory = func() (provider.Client, error) { return &fixtureClient{usage: true, err: tc.err}, nil }
			result, err := Run(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(opts.Directory, result.Trials[0].Directory, "events.jsonl")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := replay.Read(strings.NewReader(string(data))); err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			var final event.Event
			if err := json.Unmarshal([]byte(lines[len(lines)-1]), &final); err != nil {
				t.Fatal(err)
			}
			if final.Result.Failure == nil || final.Result.Failure.Kind != tc.kind || final.FailureID == "" || final.FailureID != final.Result.Failure.ID {
				t.Fatalf("missing classification/correlation: %+v", final.Result)
			}
		})
	}
}

func TestGoAcceptanceUsesIndependentTests(t *testing.T) {
	opts := fixtureOptions(t)
	tasks, err := Select("code_boundary")
	if err != nil {
		t.Fatal(err)
	}
	opts.Tasks = tasks
	opts.Factory = func() (provider.Client, error) {
		return &fixtureClient{usage: true, response: func(int) provider.Response { return provider.Response{Content: "Done."} }}, nil
	}
	result, err := Run(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Trials[0].FalseDone || result.Trials[0].MachinePass {
		t.Fatal("unfixed boundary passed independent Go tests")
	}
}

func TestPlatformGoPreflight(t *testing.T) {
	tasks, err := Select("code_boundary")
	if err != nil {
		t.Fatal(err)
	}
	if err := Preflight(tasks, nil); err != nil {
		t.Skipf("required platform containment unavailable: %v", err)
	}
	opts := Options{Directory: t.TempDir(), Tasks: tasks}
	if err := preflightGo(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
}

func TestWorkDeliverableReceiptsReachGrader(t *testing.T) {
	opts := fixtureOptions(t)
	opts.Tasks, _ = Select("work_totals")
	opts.Factory = func() (provider.Client, error) {
		return &fixtureClient{usage: true, response: func(call int) provider.Response {
			switch call {
			case 1:
				return provider.Response{ToolCalls: []provider.ToolCall{{ID: "write", Name: "write_file", Arguments: json.RawMessage(`{"path":"totals.json","content":"{\"net_revenue\":39,\"rows\":3}"}`)}}}
			case 2:
				return provider.Response{ToolCalls: []provider.ToolCall{{ID: "validate", Name: "validate_artifact", Arguments: json.RawMessage(`{"path":"totals.json","format":"json"}`)}}}
			default:
				return provider.Response{Content: "The total is 39 across 3 rows; totals.json is validated."}
			}
		}}, nil
	}
	result, err := Run(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Trials[0].MachinePass {
		t.Fatalf("valid deliverable failed: %+v", result.Trials[0])
	}
}

func TestScopedWorkReceiptsReachGraderWithoutInterventions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX assertion fixture")
	}
	opts := fixtureOptions(t)
	opts.Tasks, _ = Select("work_totals")
	opts.Factory = func() (provider.Client, error) {
		return &fixtureClient{usage: true, response: func(call int) provider.Response {
			switch call {
			case 1:
				return provider.Response{ToolCalls: []provider.ToolCall{{ID: "write", Name: "write_file", Arguments: json.RawMessage(`{"path":"totals.json","content":"{\"net_revenue\":39,\"rows\":3}"}`)}}}
			case 2:
				return provider.Response{ToolCalls: []provider.ToolCall{{ID: "check", Name: "run_command", Arguments: json.RawMessage(`{"command":"grep -q 39 totals.json","verification":{"paths":["totals.json"],"purpose":"Check fixture total text"}}`)}}}
			default:
				return provider.Response{Content: "Created totals.json; checked total text. Independent grading checks the numbers."}
			}
		}}, nil
	}
	result, err := Run(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Trials[0].MachinePass || result.Trials[0].ControllerInterventions != 0 {
		t.Fatalf("scoped receipt rejected: %+v", result.Trials[0])
	}
}

func TestGeneratedResultsValidateAgainstPublishedSchema(t *testing.T) {
	opts := fixtureOptions(t)
	result, err := Run(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(JSONSchema(), &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(document); err != nil {
		t.Fatal(err)
	}
}
