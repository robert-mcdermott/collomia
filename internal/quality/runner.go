package quality

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/robert-mcdermott/collomia/internal/agent"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/failureid"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/sandbox"
	"github.com/robert-mcdermott/collomia/internal/taskmode"
	"github.com/robert-mcdermott/collomia/internal/tools"
	"github.com/robert-mcdermott/collomia/internal/version"
)

type Options struct {
	Directory, Provider, Model string
	ProviderConfig             appconfig.Provider
	Settings                   Settings
	Tasks                      []Task
	Redact                     func(string) string
	Progress                   func(string)
	// Factory is an offline-test seam; production uses the configured adapter.
	Factory func() (provider.Client, error)
	// Backend is an offline-test seam. Production always uses ForPlatform.
	Backend sandbox.Backend
}

func policyConfig() appconfig.Config {
	var cfg appconfig.Config
	cfg.Options.MaxToolOutputBytes = 16000
	cfg.Permissions.Mode = "autopilot"
	cfg.Permissions.Sandbox = "require"
	cfg.Permissions.CommandEnv = "minimal"
	cfg.Permissions.SandboxAllowNetwork = false
	cfg.Permissions.SandboxAllowReadOutsideWorkspace = false
	cfg.Permissions.SandboxReadableRoots = []string{runtime.GOROOT()}
	return cfg
}

func Preflight(tasks []Task, backend sandbox.Backend) error {
	if backend == nil {
		backend = sandbox.ForPlatform()
	}
	if err := backend.Available(); err != nil {
		return fmt.Errorf("evaluation commands require an available OS sandbox: %w", err)
	}
	if missing := backend.Capabilities().Missing(sandbox.Policy{ConstrainReads: true, AllowNetwork: false}); len(missing) > 0 {
		return fmt.Errorf("evaluation requires containment: %s", strings.Join(missing, ", "))
	}
	return nil
}

func Run(ctx context.Context, opts Options) (Result, error) {
	result := Result{Schema: 1, Suite: SuiteVersion, SuiteDigest: Digest(opts.Tasks), Version: version.Version, Commit: version.Commit, Provider: opts.Provider, ProviderType: opts.ProviderConfig.Type, Model: opts.Model, Platform: runtime.GOOS + "/" + runtime.GOARCH, Settings: opts.Settings, Started: time.Now().UTC(), State: "running"}
	if err := opts.Settings.Validate(); err != nil {
		return result, err
	}
	if len(opts.Tasks) == 0 || len(opts.Tasks) > 32 {
		return result, errors.New("select 1–32 tasks")
	}
	if opts.Settings.CostBudget > 0 && (opts.ProviderConfig.Pricing == nil || opts.ProviderConfig.Pricing.InputPerMillion <= 0 || opts.ProviderConfig.Pricing.OutputPerMillion <= 0) {
		return result, errors.New("a cost budget requires configured positive input and output pricing")
	}
	if err := Preflight(opts.Tasks, opts.Backend); err != nil {
		return result, err
	}
	if opts.Redact == nil {
		opts.Redact = func(s string) string { return s }
	}
	modelSettings, _ := json.Marshal(struct {
		MaxTokens, Context int
		Temperature        *float64
		Reasoning          *appconfig.Reasoning
		Pricing            *appconfig.Pricing
	}{opts.ProviderConfig.MaxTokens, opts.ProviderConfig.Context, opts.ProviderConfig.Temperature, opts.ProviderConfig.Reasoning, opts.ProviderConfig.Pricing})
	digest := sha256.Sum256(modelSettings)
	result.ModelSettingsDigest = hex.EncodeToString(digest[:])
	for _, task := range opts.Tasks {
		result.TaskIDs = append(result.TaskIDs, task.ID)
	}
	if err := os.MkdirAll(filepath.Dir(opts.Directory), 0o700); err != nil {
		return result, err
	}
	if err := os.Mkdir(opts.Directory, 0o700); err != nil {
		return result, fmt.Errorf("output must be a new directory: %w", err)
	}
	if err := Save(opts.Directory, result); err != nil {
		return result, err
	}
	if err := preflightGo(ctx, opts); err != nil {
		result.State = "stopped"
		result.StopReason = opts.Redact(err.Error())
		return result, errors.Join(err, Save(opts.Directory, result))
	}
	totalTokens := 0
	for trial := 1; trial <= opts.Settings.Trials; trial++ {
		for _, task := range opts.Tasks {
			if ctx.Err() != nil {
				result.State = "stopped"
				result.StopReason = "cancelled"
				break
			}
			if opts.Settings.TotalTokens-totalTokens < opts.Settings.TokenBudget {
				result.State = "stopped"
				result.StopReason = "remaining total budget cannot reserve a full task allowance"
				break
			}
			if opts.Progress != nil {
				opts.Progress(fmt.Sprintf("%s trial %d/%d", task.ID, trial, opts.Settings.Trials))
			}
			row, err := runTrial(ctx, opts, task, trial)
			result.Trials = append(result.Trials, row)
			totalTokens += row.Usage.InputTokens + row.Usage.OutputTokens
			if err != nil {
				result.State = "stopped"
				result.StopReason = opts.Redact(err.Error())
			}
			if row.ProviderCalls > 0 && !row.UsageComplete {
				result.State = "stopped"
				result.StopReason = "provider usage incomplete; stopped before another task because the total token budget cannot be accounted reliably"
			}
			if saveErr := Save(opts.Directory, result); saveErr != nil {
				return result, saveErr
			}
			if result.State == "stopped" {
				break
			}
		}
		if result.State == "stopped" {
			break
		}
	}
	if result.State == "running" {
		result.State = "complete"
	}
	return result, Save(opts.Directory, result)
}

func runTrial(ctx context.Context, opts Options, task Task, trial int) (Trial, error) {
	row := Trial{Task: task.ID, Trial: trial, Mode: task.Mode, Category: task.Category, Expected: task.ExpectedOutcome, ReviewPrompt: task.Review, Directory: fmt.Sprintf("%s-%02d", task.ID, trial)}
	started := time.Now()
	directory := filepath.Join(opts.Directory, row.Directory)
	workspace := filepath.Join(directory, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return row, err
	}
	for path, content := range task.Files {
		if err := os.WriteFile(filepath.Join(workspace, path), []byte(content), 0o600); err != nil {
			return row, err
		}
	}
	trace, err := newTrace(directory, opts.Redact)
	if err != nil {
		return row, err
	}
	defer trace.close()
	cfg := policyConfig()
	registry, _, processes, err := tools.Builtins(workspace, cfg)
	if err != nil {
		return row, err
	}
	defer processes.StopAll()
	allowed := map[string]bool{"read_file": true, "list_files": true, "search_files": true, "write_file": true, "edit_file": true, "apply_patch": true, "run_command": true, "detect_verification": true, "validate_artifact": true}
	for _, name := range registry.Names() {
		if !allowed[name] {
			registry.Remove(name)
		}
	}
	command, err := tools.ConfiguredRunCommandTool(workspace, cfg, 16000)
	if err != nil {
		return row, err
	}
	if opts.Backend != nil {
		command.Backend = opts.Backend
	}
	registry.Add(command)
	board := plan.NewBoard()
	registry.Add(plan.Tool(board))
	registry.Add(tools.Function{Def: provider.ToolDefinition{Name: "ask_user", Description: "Ask for missing information. Evaluation tasks have no interactive human; the request is recorded for review.", InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"}},"required":["question"],"additionalProperties":false}`)}, Action: tools.Action{Risk: tools.RiskRead, Summary: "request clarification"}, Run: func(context.Context, json.RawMessage) (string, error) {
		return "No additional information is available in this evaluation. Use the supplied requirements, make reasonable reversible assumptions, or report a required blocker.", nil
	}})
	var client provider.Client
	if opts.Factory != nil {
		client, err = opts.Factory()
	} else {
		client, err = provider.New(opts.Provider, opts.ProviderConfig, opts.Model)
		if err == nil {
			client = provider.WithResilience(client)
		}
	}
	if err != nil {
		return row, err
	}
	meter := &meterClient{Client: client, complete: true}
	mode, _ := taskmode.Parse(task.Mode)
	a := agent.New(agent.Options{Client: meter, ProviderName: opts.Provider, Model: opts.Model, ProviderConfig: opts.ProviderConfig, Workspace: workspace, Registry: registry, Permissions: permission.New(cfg.Permissions, nil), CompletionPlan: board, TaskMode: mode, MaxIterations: opts.Settings.MaxIterations, MaxTurnIterations: opts.Settings.MaxIterations, MaxToolOutput: 16000, TokenBudget: opts.Settings.TokenBudget, CostBudgetUSD: opts.Settings.CostBudget, OnMessage: trace.message, PersistenceError: trace.failure, PinnedContext: func() string {
		if p := board.Current(); p != nil {
			return p.Render()
		}
		return ""
	}, ProjectInstructions: "This is a controlled evaluation workspace with synthetic inputs. Only built-in file, command, plan, and artifact tools are available. Commands require OS containment and have no network. For Go verification use: " + goCheckCommand(workspace) + ". Keep supplied inputs unchanged unless the task requests editing them. Do not modify go.mod or evaluator test files."})
	taskCtx, cancel := context.WithTimeout(ctx, time.Duration(opts.Settings.TimeoutSeconds)*time.Second)
	var runErr error
	for _, prompt := range task.Prompts {
		row.Answer, runErr = a.Run(taskCtx, prompt, trace.event)
		if runErr != nil {
			break
		}
	}
	cancel()
	row.DurationMS = time.Since(started).Milliseconds()
	row.Outcome = string(agent.GoalOutcomeFor(runErr))
	if errors.Is(runErr, context.DeadlineExceeded) {
		row.Outcome = "timeout"
	}
	if runErr != nil {
		row.Error = opts.Redact(clip(runErr.Error(), 2000))
	}
	row.Usage = a.Usage()
	row.ProviderCalls = meter.calls
	row.UsageComplete = meter.complete
	trace.mu.Lock()
	row.ToolCalls = trace.calls
	row.RepeatedToolCalls = trace.repeats
	row.Questions = trace.questions
	row.ControllerInterventions = trace.interventions
	row.PermissionDenials = trace.denials
	receipts := trace.receipts
	trace.mu.Unlock()
	gradeCtx, gradeCancel := context.WithTimeout(ctx, 60*time.Second)
	defer gradeCancel()
	row.Checks = grade(gradeCtx, task, workspace, row.Answer, receipts, command)
	row.Answer = opts.Redact(clip(row.Answer, 16000))
	checksPass := true
	for i := range row.Checks {
		row.Checks[i].Detail = opts.Redact(row.Checks[i].Detail)
		if !row.Checks[i].Passed {
			checksPass = false
		}
	}
	row.MachinePass = checksPass && row.Outcome == task.ExpectedOutcome
	row.FalseDone = row.Outcome == "done" && (!checksPass || task.ExpectedOutcome != "done")
	row.FalseBlockedCandidate = checksPass && task.ExpectedOutcome == "done" && row.Outcome == "blocked"
	end := event.New(event.KindRunResult)
	end.Result = &event.RunResult{Status: "ok", Outcome: string(agent.GoalOutcomeFor(runErr)), Answer: row.Answer, DurationMS: row.DurationMS, Version: version.Version, Commit: version.Commit}
	if runErr != nil {
		runErr = failureid.Ensure(runErr)
		end.Result.Status = "error"
		end.Result.Error = row.Error
		end.Result.Failure = evaluationFailure(runErr)
		end.FailureID = end.Result.Failure.ID
		end.Result.Partial = row.ToolCalls > 0 || row.Answer != ""
		if end.Result.Failure.Kind == event.FailureCancelled {
			end.Result.Status = "cancelled"
		}
	}
	end.Result.Refused = row.PermissionDenials > 0
	end.Result.Mode = task.Mode
	trace.event(end)
	trace.close()
	return row, trace.failure()
}

// Evaluation exits use the same event-v1 classifications as headless runs.
func evaluationFailure(err error) *event.Failure {
	failure := &event.Failure{ID: failureid.ID(err), Kind: event.FailureRuntime}
	if p, ok := provider.AsError(err); ok {
		failure.Kind = event.FailureProvider
		failure.Retryable = p.Retryable
		failure.Provider = &event.ProviderFailure{Name: p.Provider, Operation: p.Operation, Kind: string(p.Kind), StatusCode: p.StatusCode, Retryable: p.Retryable, RetryAfterMS: p.RetryAfter.Milliseconds(), RequestID: p.RequestID}
		if p.Kind == provider.ErrorCancelled {
			failure.Kind = event.FailureCancelled
		} else if p.Kind == provider.ErrorTimeout {
			failure.Kind = event.FailureTimeout
		}
		return failure
	}
	switch {
	case errors.Is(err, context.Canceled):
		failure.Kind = event.FailureCancelled
	case errors.Is(err, context.DeadlineExceeded):
		failure.Kind, failure.Retryable = event.FailureTimeout, true
	case errors.Is(err, permission.ErrDenied):
		failure.Kind = event.FailurePermission
	case errors.Is(err, agent.ErrTokenBudgetExceeded), errors.Is(err, agent.ErrCostBudgetExceeded), errors.Is(err, agent.ErrIterationBudgetExceeded):
		failure.Kind = event.FailureUsage
	}
	return failure
}

type meterClient struct {
	provider.Client
	calls    int
	complete bool
}

func (c *meterClient) Chat(ctx context.Context, req provider.Request, delta func(provider.Delta)) (provider.Response, error) {
	c.calls++
	response, err := c.Client.Chat(ctx, req, delta)
	if err != nil || response.Usage.InputTokens+response.Usage.OutputTokens == 0 {
		c.complete = false
	}
	if err == nil && response.Usage.InputTokens+response.Usage.OutputTokens == 0 {
		err = &provider.Error{Provider: c.Name(), Operation: "evaluation accounting", Kind: provider.ErrorProtocol, Message: "provider omitted token usage; evaluation stopped before tools or another request because its budget cannot be accounted reliably"}
	}
	return response, err
}
func (c *meterClient) Capabilities() provider.Capabilities {
	if reporter, ok := c.Client.(provider.CapabilityReporter); ok {
		return reporter.Capabilities()
	}
	return provider.Capabilities{}
}

type traceLog struct {
	mu                                                sync.Mutex
	files                                             []*os.File
	redact                                            func(string) string
	bytes                                             int
	err                                               error
	calls, repeats, questions, interventions, denials int
	seen                                              map[string]bool
	receipts                                          map[string]string
}

func newTrace(directory string, redact func(string) string) (*traceLog, error) {
	t := &traceLog{redact: redact, seen: map[string]bool{}, receipts: map[string]string{}}
	for _, name := range []string{"events.jsonl", "messages.jsonl"} {
		f, err := os.OpenFile(filepath.Join(directory, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			t.close()
			return nil, err
		}
		t.files = append(t.files, f)
	}
	return t, nil
}
func (t *traceLog) write(index int, value any) {
	if t.err != nil {
		return
	}
	data, err := json.Marshal(value)
	if err == nil {
		var decoded any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		err = decoder.Decode(&decoded)
		if err == nil {
			data, err = json.Marshal(redactValues(decoded, t.redact))
		}
	}
	if err != nil {
		t.err = err
		return
	}
	t.bytes += len(data) + 1
	if t.bytes > 16<<20 {
		t.err = errors.New("evaluation trace exceeded 16 MiB; execution stopped")
		return
	}
	_, t.err = t.files[index].Write(append(data, '\n'))
}
func (t *traceLog) event(e event.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.write(0, e)
	if e.Kind == event.KindWarning && strings.Contains(e.Text, "Collomia completion controller") {
		t.interventions++
	}
	if e.Kind == event.KindPermissionDecision && e.Permission != nil && !e.Permission.Allowed {
		t.denials++
	}
	if e.Kind == event.KindToolResult && e.Tool != nil && !e.Tool.IsError && e.Tool.Evidence != nil && e.Tool.Evidence.Kind == "artifact_validated" {
		t.receipts[e.Tool.Evidence.Subject] = e.Tool.Evidence.Digest
	}
}
func (t *traceLog) message(message provider.Message) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.write(1, message)
	if message.Role != "assistant" {
		return
	}
	for _, call := range message.ToolCalls {
		t.calls++
		if call.Name == "ask_user" {
			t.questions++
		}
		var args any
		decoder := json.NewDecoder(strings.NewReader(string(call.Arguments)))
		decoder.UseNumber()
		_ = decoder.Decode(&args)
		canonical, _ := json.Marshal(args)
		key := call.Name + "\x00" + string(canonical)
		if t.seen[key] {
			t.repeats++
		}
		t.seen[key] = true
	}
}
func (t *traceLog) failure() error { t.mu.Lock(); defer t.mu.Unlock(); return t.err }
func (t *traceLog) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, f := range t.files {
		if err := f.Close(); t.err == nil {
			t.err = err
		}
	}
	t.files = nil
}

func redactValues(value any, redact func(string) string) any {
	switch v := value.(type) {
	case string:
		return redact(v)
	case []any:
		for i := range v {
			v[i] = redactValues(v[i], redact)
		}
		return v
	case map[string]any:
		redacted := make(map[string]any, len(v))
		for key := range v {
			redacted[redact(key)] = redactValues(v[key], redact)
		}
		return redacted
	default:
		return value
	}
}

func preflightGo(ctx context.Context, opts Options) error {
	needed := false
	for _, task := range opts.Tasks {
		for _, check := range task.Checks {
			if check.Kind == "go_test" {
				needed = true
			}
		}
	}
	if !needed {
		return nil
	}
	workspace := filepath.Join(opts.Directory, "preflight")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		return err
	}
	for path, content := range map[string]string{"go.mod": "module preflight\n\ngo 1.26.0\n", "smoke_test.go": "package preflight\nimport \"testing\"\nfunc TestSmoke(t *testing.T) {}\n"} {
		if err := os.WriteFile(filepath.Join(workspace, path), []byte(content), 0o600); err != nil {
			return err
		}
	}
	command, err := tools.ConfiguredRunCommandTool(workspace, policyConfig(), 4000)
	if err != nil {
		return err
	}
	if opts.Backend != nil {
		command.Backend = opts.Backend
	}
	raw, _ := json.Marshal(map[string]any{"command": goCheckCommand(workspace), "timeout_seconds": 60})
	preflightCtx, cancel := context.WithTimeout(ctx, 65*time.Second)
	defer cancel()
	output, err := command.Execute(preflightCtx, raw)
	if err != nil {
		return fmt.Errorf("Go grader preflight failed before model calls (requires local Go 1.26+): %s: %w", clip(output, 2000), err)
	}
	return nil
}
