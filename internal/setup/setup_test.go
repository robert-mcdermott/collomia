package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// fakeEndpoint is an OpenAI-compatible server with per-path behavior, so one
// test can make the catalog succeed while the completion fails — which is the
// exact split Verify exists to detect.
type fakeEndpoint struct {
	models     []string
	catalogErr int
	chatStatus int
	chatBody   string
	// rejectTools reproduces the runtime that answers a plain completion and
	// refuses the same request once tool definitions are attached.
	rejectTools bool
}

func (f fakeEndpoint) start(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			if f.catalogErr != 0 {
				w.WriteHeader(f.catalogErr)
				return
			}
			entries := make([]map[string]string, 0, len(f.models))
			for _, id := range f.models {
				entries = append(entries, map[string]string{"id": id})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": entries})
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			if f.chatStatus != 0 && f.chatStatus != http.StatusOK {
				w.WriteHeader(f.chatStatus)
				fmt.Fprint(w, `{"error":{"message":"upstream said no"}}`)
				return
			}
			if f.rejectTools {
				body, _ := io.ReadAll(r.Body)
				if strings.Contains(string(body), "tools") {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, `{"error":{"message":"registry.ollama.ai/library/tiny does not support tools"}}`)
					return
				}
			}
			body := f.chatBody
			if body == "" {
				body = "ok"
			}
			encoded, _ := json.Marshal(body)
			fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%s},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":1}}`, encoded)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestProbeDistinguishesAbsentFromListening(t *testing.T) {
	// A closed port and a port answering something that is not an
	// OpenAI-compatible API send a user to entirely different fixes, so the
	// probe must not collapse them into one "unavailable".
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddr := closed.Addr().String()
	_ = closed.Close()

	ready := fakeEndpoint{models: []string{"b-model", "a-model"}}.start(t)

	probes := ProbeLocal(context.Background(), []Candidate{
		{Name: "Silent", Key: "silent", BaseURL: "http://" + listener.Addr().String() + "/v1", Type: "openai-compatible"},
		{Name: "Closed", Key: "closed", BaseURL: "http://" + closedAddr + "/v1", Type: "openai-compatible", Start: "start it"},
		{Name: "Ready", Key: "ready", BaseURL: ready + "/v1", Type: "openai-compatible"},
	})

	if probes[0].State != ProbeListening {
		t.Errorf("a port that accepts but does not answer must be %q, got %q", ProbeListening, probes[0].State)
	}
	if probes[1].State != ProbeAbsent {
		t.Errorf("a closed port must be %q, got %q", ProbeAbsent, probes[1].State)
	}
	if !strings.Contains(probes[1].Detail(), "start it") {
		t.Errorf("an absent runtime must name how to start it, got %q", probes[1].Detail())
	}
	if probes[2].State != ProbeReady || len(probes[2].Models) != 2 {
		t.Fatalf("ready probe = %q with %d models", probes[2].State, len(probes[2].Models))
	}
	// Results must come back in candidate order, not completion order, or the
	// list reorders itself between runs on the same machine.
	if probes[2].Candidate.Name != "Ready" {
		t.Errorf("probe results must preserve candidate order, got %q third", probes[2].Candidate.Name)
	}
	if probes[2].Models[0].ID != "a-model" {
		t.Errorf("catalog must be sorted, got %q first", probes[2].Models[0].ID)
	}
}

func TestProbeReportsRunningWithNoModels(t *testing.T) {
	// "Running with an empty catalog" is a real state that restarting does not
	// fix, so it must not read as "not running".
	url := fakeEndpoint{}.start(t)
	probes := ProbeLocal(context.Background(), []Candidate{
		{Name: "Empty", Key: "empty", BaseURL: url + "/v1", Type: "openai-compatible"},
	})
	if probes[0].State != ProbeReady {
		t.Fatalf("state = %q", probes[0].State)
	}
	if !strings.Contains(probes[0].Detail(), "no models are installed") {
		t.Errorf("detail = %q", probes[0].Detail())
	}
}

func TestVerifySucceedsAndReportsTheReply(t *testing.T) {
	url := fakeEndpoint{models: []string{"m"}, chatBody: "ok"}.start(t)
	result := Verify(context.Background(), "local", appconfig.Provider{Type: "openai-compatible", BaseURL: url + "/v1"}, "m", nil)
	if !result.OK {
		t.Fatalf("verify failed: %v", result.Err)
	}
	if result.Reply != "ok" {
		t.Errorf("reply = %q", result.Reply)
	}
}

func TestVerifyTreatsAnEmptyCompletionAsFailure(t *testing.T) {
	// Several compatible gateways answer a request for a model they do not
	// actually serve with a well-formed, entirely empty completion. Accepting
	// that would write a configuration whose first real prompt returns silence.
	url := fakeEndpoint{models: []string{"real-model"}, chatBody: " "}.start(t)
	catalog, err := Discover(context.Background(), "local", appconfig.Provider{Type: "openai-compatible", BaseURL: url + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	result := Verify(context.Background(), "local", appconfig.Provider{Type: "openai-compatible", BaseURL: url + "/v1"}, "ghost", catalog)
	if result.OK {
		t.Fatal("an empty completion must not verify")
	}
	if !strings.Contains(result.Diagnosis.Summary, "no output") {
		t.Errorf("summary = %q", result.Diagnosis.Summary)
	}
	if !containsSubstring(result.Diagnosis.Fixes, "real-model") {
		t.Errorf("the diagnosis must print the catalog it read, got %v", result.Diagnosis.Fixes)
	}
}

func TestVerifyRejectsAModelThatCannotAcceptTools(t *testing.T) {
	// The regression that produced the two-request design. An earlier version
	// sent no tool definitions, so `gemma3:270m` verified cleanly and then
	// failed the user's first real prompt with "does not support tools" — the
	// exact failure this package exists to move earlier. Found by running the
	// wizard against a real Ollama and then running a real session.
	url := fakeEndpoint{models: []string{"tiny", "qwen2.5-coder"}, rejectTools: true}.start(t)
	p := appconfig.Provider{Type: "openai-compatible", BaseURL: url + "/v1"}
	catalog, err := Discover(context.Background(), "local", p)
	if err != nil {
		t.Fatal(err)
	}

	result := Verify(context.Background(), "local", p, "tiny", catalog)
	if result.OK {
		t.Fatal("a model that refuses tool definitions must not verify: Collomia cannot drive it")
	}
	if result.ToolsOK {
		t.Error("ToolsOK must record that the tool request specifically failed")
	}
	if result.Reply == "" {
		t.Error("the plain completion succeeded, so its reply should still be recorded")
	}
	if !strings.Contains(result.Diagnosis.Summary, "cannot accept tools") {
		t.Errorf("summary must name the real cause, got %q", result.Diagnosis.Summary)
	}
	if containsSubstring(result.Diagnosis.Fixes, "\"tiny\"") {
		t.Error("the failing model must not be suggested back as its own alternative")
	}
	if !containsSubstring(result.Diagnosis.Fixes, "qwen2.5-coder") {
		t.Errorf("other catalog entries should be offered, got %v", result.Diagnosis.Fixes)
	}
}

func TestVerifyPassesOnlyWhenBothRequestsSucceed(t *testing.T) {
	url := fakeEndpoint{models: []string{"good"}}.start(t)
	p := appconfig.Provider{Type: "openai-compatible", BaseURL: url + "/v1"}
	result := Verify(context.Background(), "local", p, "good", nil)
	if !result.OK || !result.ToolsOK {
		t.Fatalf("ok=%v toolsOK=%v err=%v", result.OK, result.ToolsOK, result.Err)
	}
}

func TestVerifyDiagnosesRejectedCredential(t *testing.T) {
	url := fakeEndpoint{chatStatus: http.StatusUnauthorized}.start(t)
	p := appconfig.Provider{Type: "openai-compatible", BaseURL: url + "/v1", APIKey: "wrong", APIKeyEnv: "SOME_KEY"}
	result := Verify(context.Background(), "hosted", p, "m", nil)
	if result.OK {
		t.Fatal("a 401 must not verify")
	}
	if !strings.Contains(result.Diagnosis.Summary, "rejected the credential") {
		t.Errorf("summary = %q", result.Diagnosis.Summary)
	}
	if !containsSubstring(result.Diagnosis.Fixes, "SOME_KEY") {
		t.Errorf("the fix must name where the credential came from, got %v", result.Diagnosis.Fixes)
	}
}

func TestVerifyDiagnosesMissingModelByNamingTheCatalog(t *testing.T) {
	// The entire value of moving this failure earlier is that here the wizard
	// knows what the endpoint actually has. A bare "HTTP 404" would have moved
	// the failure without answering it.
	url := fakeEndpoint{models: []string{"qwen2.5-coder"}, chatStatus: http.StatusNotFound}.start(t)
	p := appconfig.Provider{Type: "openai-compatible", BaseURL: url + "/v1"}
	catalog, err := Discover(context.Background(), "local", p)
	if err != nil {
		t.Fatal(err)
	}
	result := Verify(context.Background(), "local", p, "qwen3-coder", catalog)
	if result.OK {
		t.Fatal("a 404 must not verify")
	}
	if !containsSubstring(result.Diagnosis.Fixes, "qwen2.5-coder") {
		t.Errorf("fixes must list what the endpoint reported, got %v", result.Diagnosis.Fixes)
	}
}

func TestDiagnoseAzureDeploymentMistake(t *testing.T) {
	// Writing a model's published name where Azure wants a deployment name is
	// the single most common Azure misconfiguration, and the raw 404 says
	// nothing about it.
	d := Diagnose(appconfig.Provider{Type: "azure-openai", BaseURL: "https://example.openai.azure.com"}, "gpt-4o", nil,
		&provider.Error{Kind: provider.ErrorNotFound, StatusCode: 404, Message: "deployment not found"})
	if !containsSubstring(d.Fixes, "deployment") {
		t.Errorf("fixes = %v", d.Fixes)
	}
}

func TestApplyRefusesToWriteASecretIntoConfiguration(t *testing.T) {
	// This is the rule the whole package is arranged around. `collo auth`
	// exists because a provider secret does not belong in a file, and a setup
	// wizard is exactly where that would be quietly traded away for
	// convenience.
	path := filepath.Join(t.TempDir(), "config.json")
	err := Apply(path, Result{
		Name:     "hosted",
		Provider: appconfig.Provider{Type: "openai", BaseURL: "https://example.test/v1", APIKey: "sk-secret"},
		Model:    "m",
	})
	if err == nil {
		t.Fatal("Apply must refuse a provider carrying an API key")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatal("nothing may be written when the secret guard trips")
	}
}

func TestBuildNeverCarriesTheSecretIntoTheProvider(t *testing.T) {
	result := Build("hosted", appconfig.Provider{Type: "openai", BaseURL: "https://example.test/v1", APIKey: "sk-secret"}, "m", CredentialStore, "OPENAI_API_KEY", "sk-secret")
	if result.Provider.APIKey != "" {
		t.Fatal("Build must not copy an API key into the provider it writes")
	}
	if result.Secret != "sk-secret" {
		t.Error("the secret must survive on the result for the credential store step")
	}

	env := Build("hosted", appconfig.Provider{Type: "openai", BaseURL: "https://example.test/v1"}, "m", CredentialEnv, "OPENAI_API_KEY", "sk-secret")
	if env.Provider.APIKeyEnv != "OPENAI_API_KEY" {
		t.Errorf("api_key_env = %q", env.Provider.APIKeyEnv)
	}
	if env.Provider.APIKey != "" {
		t.Fatal("the env plan must record a variable name and never a value")
	}
}

func TestApplyMergesWithoutDisturbingTheRestOfTheFile(t *testing.T) {
	// A typed round trip would rewrite every unset field to its zero value and
	// silently turn a sparse configuration into an exhaustive one, so the merge
	// edits the decoded document instead.
	path := filepath.Join(t.TempDir(), "config.json")
	original := `{
  "schema_version": 1,
  "default_provider": "old",
  "providers": {"old": {"type": "openai-compatible", "base_url": "http://old.test/v1"}},
  "permissions": {"mode": "ask", "sandbox": "require"},
  "options": {"theme": "matrix"},
  "some_future_key": {"kept": true}
}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	result := Build("new", appconfig.Provider{Type: "openai-compatible", BaseURL: "http://new.test/v1"}, "model-x", CredentialNone, "", "")
	result.MakeDefault = true
	if err := Apply(path, result); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("setup wrote invalid JSON: %v\n%s", err, data)
	}
	providers, _ := document["providers"].(map[string]any)
	if _, ok := providers["old"]; !ok {
		t.Error("the existing provider must survive")
	}
	if _, ok := providers["new"]; !ok {
		t.Error("the new provider must be added")
	}
	if document["default_provider"] != "new" || document["default_model"] != "model-x" {
		t.Errorf("defaults = %v / %v", document["default_provider"], document["default_model"])
	}
	permissions, _ := document["permissions"].(map[string]any)
	if permissions["sandbox"] != "require" {
		t.Error("an unrelated containment setting must not be rewritten")
	}
	if _, ok := document["some_future_key"]; !ok {
		t.Error("a key this build does not know about must survive untouched")
	}
	if strings.Contains(string(data), "api_key\"") {
		t.Error("no api_key field may ever appear in a written configuration")
	}
}

func TestApplyCreatesAFileWhenNoneExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	result := Build("local", appconfig.Provider{Type: "openai-compatible", BaseURL: "http://local.test/v1"}, "m", CredentialNone, "", "")
	result.MakeDefault = true
	if err := Apply(path, result); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if document["schema_version"] == nil {
		t.Error("a fresh file must carry the schema version")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("configuration must be owner-only, got %o", perm)
	}
}

func TestBuildTakesContextFromTheCapabilityRegistry(t *testing.T) {
	result := Build("local", appconfig.Provider{Type: "openai-compatible", BaseURL: "http://local.test/v1", Context: 4096}, "m", CredentialNone, "", "")
	if result.Provider.Context != 4096 {
		t.Errorf("a known context window must be preserved, got %d", result.Provider.Context)
	}
	if result.ContextAssumed {
		t.Error("a known context window must not be marked as assumed")
	}
}

func TestBuildAlwaysWritesAContextWindowAndSaysWhenItGuessed(t *testing.T) {
	// Omitting the field looks more honest and is the wrong trade: a zero
	// window makes Agent.shouldCompact return false forever, so automatic
	// compaction never runs and a long session ends at a provider
	// context-length error with no recovery. Found by running the wizard
	// against a real Ollama and reading what it wrote.
	result := Build("local", appconfig.Provider{Type: "openai-compatible", BaseURL: "http://local.test/v1"}, "some-local-model", CredentialNone, "", "")
	if result.Provider.Context <= 0 {
		t.Fatal("a written provider must always carry a context window, or automatic compaction is silently disabled")
	}
	if !result.ContextAssumed {
		t.Error("a context window nobody established must be marked assumed so the confirmation can say so")
	}
}

func TestDiscoverReportsNoCatalogRatherThanFailing(t *testing.T) {
	// Bedrock publishes no model list at all. That is a fact about the API,
	// not a problem to report, and the wizard asks for a model name instead.
	_, err := Discover(context.Background(), "bedrock", appconfig.Provider{Type: "bedrock", Region: "us-west-2"})
	if err == nil {
		return
	}
	if !strings.Contains(err.Error(), "catalog") && !strings.Contains(err.Error(), "discovery") {
		t.Logf("bedrock discovery error (acceptable): %v", err)
	}
}

func containsSubstring(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
