package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/agent"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/session"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// scriptedClient replays a fixed provider conversation so a full runtime can
// be exercised end to end without a network.
type scriptedClient struct {
	calls    int
	steps    []provider.Response
	requests []provider.Request
}

func isolateGlobalFiles(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func TestEphemeralRuntimeSkipsDurableSessionButKeepsAuditInfrastructure(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	runtime, err := New(context.Background(), Options{Workspace: workspace, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if runtime.Sessions != nil || runtime.Session != nil {
		t.Fatalf("ephemeral runtime opened durable session state: store=%v session=%v", runtime.Sessions, runtime.Session)
	}
	if _, ok := runtime.Registry.Get("read_tool_result"); ok {
		t.Fatal("ephemeral runtime exposed durable artifact tool")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".collomia", "sessions")); !os.IsNotExist(err) {
		t.Fatalf("ephemeral run created sessions directory: %v", err)
	}
	if info, err := os.Stat(filepath.Join(home, ".collomia", "audit")); err != nil || !info.IsDir() {
		t.Fatalf("audit infrastructure should remain available: info=%v err=%v", info, err)
	}
}

func TestRuntimeCloseCancelsActiveDelegates(t *testing.T) {
	isolateGlobalFiles(t)
	runtime, err := New(context.Background(), Options{Workspace: t.TempDir(), Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime.Team.Enqueue(agent.DelegateStart{ID: "active", Name: "worker", Task: "wait", Cancel: cancel})
	runtime.Close()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("runtime close did not cancel active delegated work")
	}
}

func TestRuntimeCloseWaitsForBackgroundProcesses(t *testing.T) {
	isolateGlobalFiles(t)
	runtime, err := New(context.Background(), Options{Workspace: t.TempDir(), Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	command := "sleep 60"
	if goruntime.GOOS == "windows" {
		command = "ping -n 60 127.0.0.1"
	}
	args, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Registry.Execute(t.Context(), "start_process", args); err != nil {
		runtime.Close()
		t.Fatal(err)
	}
	if runtime.Processes.Running() != 1 {
		runtime.Close()
		t.Fatalf("running=%d", runtime.Processes.Running())
	}
	runtime.Close()
	if running := runtime.Processes.Running(); running != 0 {
		t.Fatalf("runtime close returned with %d background processes still running", running)
	}
}

func BenchmarkRuntimeStartup(b *testing.B) {
	home := b.TempDir()
	b.Setenv("HOME", home)
	b.Setenv("USERPROFILE", home)
	workspace := b.TempDir()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runtime, err := New(context.Background(), Options{Workspace: workspace, Ephemeral: true})
		if err != nil {
			b.Fatal(err)
		}
		runtime.Close()
	}
}

func TestDurableRuntimeEnablesBoundedResultArtifacts(t *testing.T) {
	isolateGlobalFiles(t)
	runtime, err := New(context.Background(), Options{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, ok := runtime.Registry.Get("read_tool_result"); !ok {
		t.Fatal("durable runtime did not expose artifact reader")
	}
	item, ok := runtime.Registry.Get("run_command")
	if !ok {
		t.Fatal("run_command missing")
	}
	command, ok := item.(*tools.RunCommandTool)
	if !ok {
		t.Fatalf("run_command type=%T", item)
	}
	if command.StreamOutputBytes != runtime.Config.Options.MaxToolOutputBytes || command.MaxOutputBytes != session.ArtifactResultLimit+1 {
		t.Fatalf("command capture=%d stream=%d config=%d", command.MaxOutputBytes, command.StreamOutputBytes, runtime.Config.Options.MaxToolOutputBytes)
	}
}

func TestSelectAgentTightensPermissionsAndPreservesUsage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configDir := filepath.Join(home, ".collomia")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configJSON := `{
	  "schema_version": 1,
	  "default_provider": "ollama",
	  "default_model": "base-model",
	  "providers": {
	    "ollama": {
	      "type": "openai-compatible",
	      "base_url": "http://127.0.0.1:11434/v1",
	      "model": "base-model",
	      "max_tokens": 1024,
	      "pricing": {
	        "input_per_million": 1,
	        "output_per_million": 2
	      }
	    }
	  },
	  "permissions": {
	    "mode": "autopilot"
	  },
	  "agents": {
	    "builder": {
	      "availability": "primary",
	      "model": "builder-model",
	      "reasoning": {"effort": "high"},
	      "tools": ["read_file"],
	      "max_iterations": 4,
	      "token_budget": 2000,
	      "cost_budget_usd": 0.25,
	      "permissions": {
	        "mode": "ask",
	        "denied_tools": ["run_command"]
	      }
	    }
	  }
	}`
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	runtime, err := New(context.Background(), Options{Workspace: t.TempDir(), Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.Agent.SetUsage(provider.Usage{InputTokens: 30, OutputTokens: 10, CostUSD: 0.00005, CostAvailable: true, CostEstimated: true})

	if err := runtime.SelectAgent("builder"); err != nil {
		t.Fatal(err)
	}
	if runtime.ActiveAgent != "builder" {
		t.Fatalf("active agent=%q", runtime.ActiveAgent)
	}
	if providerName, model := runtime.Agent.Selection(); providerName != "ollama" || model != "builder-model" {
		t.Fatalf("selection=%s/%s", providerName, model)
	}
	if profile, reasoning, tokens, cost := runtime.Agent.Profile(); profile != "builder" || reasoning != "high" || tokens != 2000 || cost != 0.25 {
		t.Fatalf("profile=%q reasoning=%q tokens=%d cost=%v", profile, reasoning, tokens, cost)
	}
	if runtime.Permissions.Mode() != "ask" {
		t.Fatalf("profile widened or ignored permission mode: %s", runtime.Permissions.Mode())
	}
	if grant, decision := runtime.Permissions.Evaluate("run_command", tools.Action{Risk: tools.RiskRead}); decision != "deny" || grant.Source != "denied-tool" {
		t.Fatalf("profile denied tool decision=%q grant=%+v", decision, grant)
	}
	if usage := runtime.Agent.Usage(); usage.InputTokens != 30 || usage.OutputTokens != 10 || !usage.CostAvailable {
		t.Fatalf("profile switch reset usage: %+v", usage)
	}

	if err := runtime.SelectAgent("default"); err != nil {
		t.Fatal(err)
	}
	if runtime.ActiveAgent != "" || runtime.Permissions.Mode() != "autopilot" {
		t.Fatalf("default profile was not restored: active=%q mode=%s", runtime.ActiveAgent, runtime.Permissions.Mode())
	}
	if _, model := runtime.Agent.Selection(); model != "base-model" {
		t.Fatalf("default model=%q", model)
	}
	if usage := runtime.Agent.Usage(); usage.InputTokens != 30 || usage.OutputTokens != 10 || !usage.CostAvailable {
		t.Fatalf("restoring default reset usage: %+v", usage)
	}
}

func TestResumeRestoresDelegatedOutcomesAndMarksActiveWorkInterrupted(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	runtime, err := New(context.Background(), Options{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	id := runtime.Session.Meta.ID
	runtime.Team.Start("done", "review", "review code", false)
	runtime.Team.Finish("done", "all clear", []string{"main.go"}, "", "", nil)
	runtime.Team.Start("active", "tests", "run tests", false)
	runtime.Close()

	resumed, err := New(context.Background(), Options{Workspace: workspace, Resume: id})
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	statuses := resumed.Team.Snapshot()
	if len(statuses) != 2 || statuses[0].Status != "done" || statuses[0].Summary != "all clear" || statuses[1].Status != "interrupted" {
		t.Fatalf("restored delegated agents=%+v", statuses)
	}
	if resumed.Team.Active() != 0 {
		t.Fatal("resume must not restart recorded delegated work")
	}
}

func (s *scriptedClient) Name() string { return "scripted" }
func (s *scriptedClient) Chat(_ context.Context, request provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
	s.requests = append(s.requests, request)
	step := s.steps[min(s.calls, len(s.steps)-1)]
	s.calls++
	if step.Content != "" && onDelta != nil {
		onDelta(provider.Delta{Text: step.Content})
	}
	return step, nil
}

// TestEndToEndRunIsFullyRepresentedByEventSchema drives a real Runtime —
// registry, permission pipeline, audit, agent loop — through a tool-using
// turn and verifies the emitted JSONL event stream carries the whole run in
// schema v1.
func TestEndToEndRunIsFullyRepresentedByEventSchema(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "hello.txt"), []byte("hello fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime, err := New(context.Background(), Options{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	client := &scriptedClient{steps: []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "1", Name: "read_file", Arguments: json.RawMessage(`{"path":"hello.txt"}`)}}},
		{Content: "The file says hello.", Usage: provider.Usage{InputTokens: 10, OutputTokens: 5}},
	}}
	runtime.Agent.SetProvider("scripted", "fixture-model", appconfig.Provider{MaxTokens: 100}, client)

	var out strings.Builder
	writer := event.NewJSONLWriter(&out)
	final, err := runtime.Agent.Run(context.Background(), "what does hello.txt say?", writer.Handle)
	if err != nil {
		t.Fatal(err)
	}
	if final != "The file says hello." {
		t.Fatalf("final=%q", final)
	}

	var kinds []event.Kind
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var e event.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("event line is not valid JSON: %q: %v", line, err)
		}
		if e.Schema != event.SchemaVersion {
			t.Fatalf("event missing schema version: %q", line)
		}
		if e.Time.IsZero() {
			t.Fatalf("event missing timestamp: %q", line)
		}
		kinds = append(kinds, e.Kind)
	}
	wantOrder := []event.Kind{event.KindTurnStart, event.KindPermissionDecision, event.KindToolStart, event.KindToolResult, event.KindTextDelta, event.KindUsage, event.KindTurnEnd}
	pos := 0
	for _, kind := range kinds {
		if pos < len(wantOrder) && kind == wantOrder[pos] {
			pos++
		}
	}
	if pos != len(wantOrder) {
		t.Fatalf("event stream missing %v (in order); got %v", wantOrder[pos], kinds)
	}
}

// TestSessionResumeAcrossRestart kills a runtime after a completed turn and
// verifies a new runtime resumes the same conversation from disk.
func TestSessionResumeAcrossRestart(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	first, err := New(context.Background(), Options{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	client := &scriptedClient{steps: []provider.Response{{Content: "the answer is 42"}}}
	first.Agent.SetProvider("scripted", "fixture", appconfig.Provider{MaxTokens: 100}, client)
	if _, err := first.Agent.Run(context.Background(), "what is the answer?", nil); err != nil {
		t.Fatal(err)
	}
	id := first.Session.Meta.ID
	first.Close()

	resumed, err := New(context.Background(), Options{Workspace: workspace, Resume: id})
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if resumed.Agent.MessageCount() != 2 {
		t.Fatalf("resumed messages=%d", resumed.Agent.MessageCount())
	}
	// The resumed conversation is live: a follow-up turn sees the history.
	follow := &scriptedClient{steps: []provider.Response{{Content: "still 42"}}}
	resumed.Agent.SetProvider("scripted", "fixture", appconfig.Provider{MaxTokens: 100}, follow)
	if _, err := resumed.Agent.Run(context.Background(), "and again?", nil); err != nil {
		t.Fatal(err)
	}
	if resumed.Agent.MessageCount() != 4 {
		t.Fatalf("post-follow-up messages=%d", resumed.Agent.MessageCount())
	}

	// --continue picks the same session.
	resumed.Close()
	continued, err := New(context.Background(), Options{Workspace: workspace, Continue: true})
	if err != nil {
		t.Fatal(err)
	}
	defer continued.Close()
	if continued.Session.Meta.ID != id {
		t.Fatalf("continue resumed %s, want %s", continued.Session.Meta.ID, id)
	}
}

// TestAskUserToolPausesForAnswer verifies the user-question primitive: the
// run pauses for a typed answer and continues without corrupting the turn.
func TestAskUserToolPausesForAnswer(t *testing.T) {
	isolateGlobalFiles(t)
	asked := ""
	runtime, err := New(context.Background(), Options{Workspace: t.TempDir(), Asker: func(_ context.Context, question string, options []string) (string, error) {
		asked = question
		return "use PostgreSQL", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	client := &scriptedClient{steps: []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "q1", Name: "ask_user", Arguments: json.RawMessage(`{"question":"Which database?","options":["PostgreSQL","SQLite"]}`)}}},
		{Content: "Using PostgreSQL."},
	}}
	runtime.Agent.SetProvider("scripted", "fixture", appconfig.Provider{MaxTokens: 100}, client)
	final, err := runtime.Agent.Run(context.Background(), "set up the db layer", nil)
	if err != nil {
		t.Fatal(err)
	}
	if asked != "Which database?" || final != "Using PostgreSQL." {
		t.Fatalf("asked=%q final=%q", asked, final)
	}
}

// TestPlanPersistsAcrossResume verifies the structured plan artifact
// survives a restart with the session.
func TestPlanPersistsAcrossResume(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	first, err := New(context.Background(), Options{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	client := &scriptedClient{steps: []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "p1", Name: "update_plan", Arguments: json.RawMessage(`{"goal":"ship it","steps":[{"id":1,"title":"build","status":"done","evidence":"go build"}]}`)}}},
		{Content: "planned"},
	}}
	first.Agent.SetProvider("scripted", "fixture", appconfig.Provider{MaxTokens: 100}, client)
	if _, err := first.Agent.Run(context.Background(), "plan the work", nil); err != nil {
		t.Fatal(err)
	}
	id := first.Session.Meta.ID
	first.Close()

	resumed, err := New(context.Background(), Options{Workspace: workspace, Resume: id})
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	current := resumed.Plan.Current()
	if current == nil || current.Goal != "ship it" || len(current.Steps) != 1 {
		t.Fatalf("plan not restored: %+v", current)
	}
	follow := &scriptedClient{steps: []provider.Response{{Content: "continued"}}}
	resumed.Agent.SetProvider("scripted", "fixture", appconfig.Provider{MaxTokens: 100}, follow)
	if _, err := resumed.Agent.Run(context.Background(), "continue the plan", nil); err != nil {
		t.Fatal(err)
	}
	if len(follow.requests) != 1 || !strings.Contains(follow.requests[0].System, "Active structured plan") || !strings.Contains(follow.requests[0].System, "ship it") || !strings.Contains(follow.requests[0].System, "go build") {
		t.Fatalf("restored plan was not pinned in the next request: %+v", follow.requests)
	}
}

// TestRuntimeQuarantinesUntrustedProject verifies the wiring end to end:
// an untrusted workspace's project config must not reach the runtime.
func TestRuntimeQuarantinesUntrustedProject(t *testing.T) {
	isolateGlobalFiles(t)
	workspace := t.TempDir()
	project := `{"permissions":{"mode":"autopilot"}}`
	if err := os.WriteFile(filepath.Join(workspace, appconfig.ProjectFile), []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := New(context.Background(), Options{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if runtime.Permissions.Mode() == "autopilot" {
		t.Fatal("untrusted project config must not set the autonomy mode")
	}
	warned := false
	for _, w := range runtime.Warnings {
		if strings.Contains(w.Error(), "not trusted") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("quarantine warning missing: %v", runtime.Warnings)
	}
}

func TestProviderInspectionCombinesCapabilitiesAndAvailability(t *testing.T) {
	isolateGlobalFiles(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path=%q", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"live-model"}]}`)
	}))
	defer server.Close()

	runtime, err := New(context.Background(), Options{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.Config.Providers = map[string]appconfig.Provider{
		"live": {Type: "openai-compatible", BaseURL: server.URL, Model: "live-model", Context: 64_000},
		"aws":  {Type: "bedrock", Model: "bedrock-model", Region: "us-east-1", Context: 128_000},
	}
	runtime.Config.DefaultProvider = "live"

	statuses := runtime.InspectProviders(t.Context())
	if len(statuses) != 2 || statuses[0].Name != "aws" || statuses[1].Name != "live" {
		t.Fatalf("statuses=%+v", statuses)
	}
	if statuses[0].Availability != ProviderUnverified || statuses[0].Capabilities.Streaming != provider.CapabilitySupported {
		t.Fatalf("aws=%+v", statuses[0])
	}
	if statuses[1].Availability != ProviderAvailable || len(statuses[1].Models) != 1 || statuses[1].Models[0].Capabilities.ContextWindow != 64_000 {
		t.Fatalf("live=%+v", statuses[1])
	}
}

func TestNewRedactorIncludesStandardBedrockBearerToken(t *testing.T) {
	t.Setenv(provider.BedrockBearerTokenEnv, "bedrock-bearer-token-secret")
	cfg := appconfig.Defaults()
	cfg.Providers["bedrock"] = appconfig.Provider{Type: "bedrock", Model: "model"}
	redactor := NewRedactor(cfg)
	if got := redactor.Redact("Authorization: Bearer bedrock-bearer-token-secret"); strings.Contains(got, "bedrock-bearer-token-secret") {
		t.Fatalf("token was not redacted: %q", got)
	}
}

func TestNewRedactorIncludesAzureEnvironmentCredentials(t *testing.T) {
	t.Setenv("AZURE_CLIENT_SECRET", "azure-client-secret-value")
	t.Setenv("AZURE_CLIENT_CERTIFICATE_PASSWORD", "azure-certificate-password")
	cfg := appconfig.Defaults()
	cfg.Providers["azure"] = appconfig.Provider{Type: "azure-foundry", Auth: "entra", BaseURL: "https://example.services.ai.azure.com", Model: "model"}
	redactor := NewRedactor(cfg)
	got := redactor.Redact("client=azure-client-secret-value certificate=azure-certificate-password")
	if strings.Contains(got, "azure-client-secret-value") || strings.Contains(got, "azure-certificate-password") {
		t.Fatalf("Azure environment credential was not redacted: %q", got)
	}
}
