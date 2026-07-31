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
	// reasoning makes the endpoint answer the way a reasoning model does, with
	// hidden reasoning tokens and a visible answer only if chatBody is set.
	// Streamed, because that is the shape a real runtime sends and the shape in
	// which reasoning is surfaced at all.
	reasoning string
	// finishReason overrides "stop". "length" is truncation at the ceiling.
	finishReason string
	// maxTokens records the ceiling the last chat request actually carried.
	maxTokens *int
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
			raw, _ := io.ReadAll(r.Body)
			if f.maxTokens != nil {
				var sent struct {
					MaxTokens int `json:"max_tokens"`
				}
				_ = json.Unmarshal(raw, &sent)
				*f.maxTokens = sent.MaxTokens
			}
			if f.rejectTools && strings.Contains(string(raw), "tools") {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":{"message":"registry.ollama.ai/library/tiny does not support tools"}}`)
				return
			}
			finish := f.finishReason
			if finish == "" {
				finish = "stop"
			}
			if f.reasoning != "" {
				w.Header().Set("Content-Type", "text/event-stream")
				reasoning, _ := json.Marshal(f.reasoning)
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":%s}}]}\n\n", reasoning)
				if f.chatBody != "" {
					content, _ := json.Marshal(f.chatBody)
					fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%s}}]}\n\n", content)
				}
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":%q}]}\n\n", finish)
				fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":170,\"completion_tokens_details\":{\"reasoning_tokens\":170}}}\n\n")
				fmt.Fprint(w, "data: [DONE]\n\n")
				return
			}
			body := f.chatBody
			if body == "" && finish == "stop" {
				body = "ok"
			}
			encoded, _ := json.Marshal(body)
			fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%s},"finish_reason":%q}],"usage":{"prompt_tokens":9,"completion_tokens":1}}`, encoded, finish)
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

func TestVerifyAcceptsAReasoningModelThatSpendsTheBudgetThinking(t *testing.T) {
	// The LM Studio regression, reported against a real qwen/qwen3.5-9b. The
	// budget was 32 tokens; the model spent about 170 reasoning before its first
	// visible word, so the visible answer came back empty and the wizard told the
	// user a model LM Studio was actively serving was "not actually served
	// behind" the gateway — a confident diagnosis of entirely the wrong thing.
	//
	// Tokens came back, so the route, the model, and the credential are all
	// proven. The absence of a visible answer within a deliberately small
	// verification budget says nothing about a real session, which sets a
	// working limit.
	url := fakeEndpoint{models: []string{"qwen3.5"}, reasoning: "Thinking Process: the user wants one word.", finishReason: "length"}.start(t)
	p := appconfig.Provider{Type: "openai-compatible", BaseURL: url + "/v1"}
	result := Verify(context.Background(), "local", p, "qwen3.5", nil)
	if !result.OK {
		t.Fatalf("a reasoning model that reasons must verify: %v — %q", result.Err, result.Diagnosis.Summary)
	}
	if !result.Reasoned {
		t.Error("Reasoned must record why the visible reply is empty")
	}
	if strings.Contains(result.Describe(), `""`) {
		t.Errorf("the confirmation must not report an empty string as the model's reply: %q", result.Describe())
	}
}

func TestVerifyDoesNotBlameTheModelForHittingTheTokenCeiling(t *testing.T) {
	// Truncation with nothing visible and no reasoning reported. The endpoint
	// answered and was cut off, which is not evidence that the model is missing,
	// so it must not be offered the catalog as though it had chosen a bad name.
	url := fakeEndpoint{models: []string{"real-model"}, chatBody: "", finishReason: "length"}.start(t)
	p := appconfig.Provider{Type: "openai-compatible", BaseURL: url + "/v1"}
	catalog, err := Discover(context.Background(), "local", p)
	if err != nil {
		t.Fatal(err)
	}
	result := Verify(context.Background(), "local", p, "real-model", catalog)
	if result.OK {
		t.Fatal("no visible output must not verify")
	}
	if !strings.Contains(result.Diagnosis.Summary, "token limit") {
		t.Errorf("summary must name the ceiling, got %q", result.Diagnosis.Summary)
	}
	if strings.Contains(result.Diagnosis.Detail, "not actually served") {
		t.Errorf("truncation is not evidence the model is missing: %q", result.Diagnosis.Detail)
	}
	if !containsSubstring(result.Diagnosis.Fixes, "nothing is wrong with either") {
		t.Errorf("the diagnosis must say the endpoint and credential are proven, got %v", result.Diagnosis.Fixes)
	}
}

func TestVerificationBudgetLeavesRoomForReasoning(t *testing.T) {
	// A guard on the constant itself, because the failure it prevents is
	// invisible from the wizard's own tests: shrinking it back toward a
	// "trivial prompt needs a trivial budget" value breaks only against real
	// reasoning models. 170 tokens was one small model on the simplest possible
	// prompt, so the floor here is well above it.
	var sent int
	url := fakeEndpoint{models: []string{"m"}, maxTokens: &sent}.start(t)
	Verify(context.Background(), "local", appconfig.Provider{Type: "openai-compatible", BaseURL: url + "/v1"}, "m", nil)
	if sent < 512 {
		t.Errorf("verification asked for %d tokens; a reasoning model exhausts that before answering", sent)
	}
}

func TestDiagnoseReadsARefusedRequestBeforeAnsweringIt(t *testing.T) {
	// LM Studio's real message when a model in its catalog cannot serve chat.
	// This used to produce Azure deployment-name and API-version advice for a
	// local runtime, while withholding the one thing that helps: the list of
	// models the wizard had just read off that same endpoint.
	detail := `Invalid model identifier "text-embedding-nomic-embed-text-v1.5". Please specify a valid downloaded model (e.g., qwen/qwen3.5-9b).`
	err := &provider.Error{Kind: provider.ErrorInvalidRequest, StatusCode: 400, Message: detail}
	p := appconfig.Provider{Type: "openai-compatible", BaseURL: "http://localhost:1234/v1"}
	catalog := []provider.ModelInfo{{ID: "qwen/qwen3.5-9b"}, {ID: "text-embedding-nomic-embed-text-v1.5"}}

	d := Diagnose(p, "text-embedding-nomic-embed-text-v1.5", catalog, err)
	if !strings.Contains(d.Summary, "will not serve that model") {
		t.Errorf("summary = %q", d.Summary)
	}
	if !containsSubstring(d.Fixes, "qwen/qwen3.5-9b") {
		t.Errorf("must print the catalog it read, got %v", d.Fixes)
	}
	if !containsSubstring(d.Fixes, "embedding model") {
		t.Errorf("an embedding model chosen for chat should be named as such, got %v", d.Fixes)
	}
	if containsSubstring(d.Fixes, "Azure") || containsSubstring(d.Fixes, "deployment") {
		t.Errorf("a local endpoint must not be given Azure advice, got %v", d.Fixes)
	}
}

func TestDiagnoseWithholdsAzureAdviceFromEndpointsThatAreNotAzure(t *testing.T) {
	// The other half of the refused-request branch: a rejection that says
	// nothing about the model still must not send a local-runtime user looking
	// for a deployment name they will never find.
	err := &provider.Error{Kind: provider.ErrorInvalidRequest, StatusCode: 400, Message: "unsupported parameter: top_k"}
	d := Diagnose(appconfig.Provider{Type: "openai-compatible", BaseURL: "http://localhost:1234/v1"}, "qwen3.5", nil, err)
	if containsSubstring(d.Fixes, "deployment") {
		t.Errorf("a non-Azure endpoint must not be given deployment advice, got %v", d.Fixes)
	}
	if len(d.Fixes) == 0 {
		t.Error("dropping the Azure line must not leave the diagnosis with no advice at all")
	}
}

func TestDiagnoseStillGivesAzureDeploymentAdviceOnAzure(t *testing.T) {
	// The Azure hint was worth keeping, just not worth giving to everyone: the
	// deployment-versus-model mistake is Azure's commonest, and this failure
	// does not quote the model back.
	err := &provider.Error{Kind: provider.ErrorInvalidRequest, StatusCode: 400, Message: "unsupported parameter for this API version"}
	d := Diagnose(appconfig.Provider{Type: "azure-openai", BaseURL: "https://r.openai.azure.com"}, "my-deployment", nil, err)
	if !containsSubstring(d.Fixes, "deployment name") {
		t.Errorf("fixes = %v", d.Fixes)
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

func TestReadExistingReadsOnlyTheTargetFile(t *testing.T) {
	// The defect this replaced: existing provider names came from
	// appconfig.Load, which composes defaults, user, and a trusted project
	// layer. Setup writes the global file, so a provider defined in a
	// repository's .collomia.json produced a warning about something setup
	// would not touch — and the project layer would shadow the write besides,
	// making the wizard look like it had done nothing.
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{
  "schema_version": 1,
  "default_provider": "ollama",
  "default_model": "qwen2.5-coder",
  "providers": {
    "ollama": {"type": "openai-compatible", "model": "qwen2.5-coder"},
    "work": {"type": "anthropic", "model": "claude-sonnet-5"}
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}

	existing, err := ReadExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	if existing.DefaultProvider != "ollama" || existing.DefaultModel != "qwen2.5-coder" {
		t.Errorf("default = %s / %s", existing.DefaultProvider, existing.DefaultModel)
	}
	if !existing.Has("ollama") || !existing.Has("work") {
		t.Errorf("providers = %v", existing.Providers)
	}
	if existing.Has("bedrock") {
		t.Error("a provider absent from this file must not be reported as present")
	}
	if existing.Describes("ollama") != "qwen2.5-coder" {
		t.Errorf("describes = %q", existing.Describes("ollama"))
	}
	if !existing.HasDefault() {
		t.Error("HasDefault must be true when a default is recorded")
	}
}

func TestReadExistingTreatsAMissingFileAsEmptyAndABrokenFileAsAnError(t *testing.T) {
	// Absent is the ordinary first-run case. Unparsable is not: silently
	// treating a broken file as empty would let the merge destroy settings the
	// user still has.
	absent, err := ReadExisting(filepath.Join(t.TempDir(), "nothing.json"))
	if err != nil {
		t.Fatalf("a missing file must not be an error: %v", err)
	}
	if absent.HasDefault() || len(absent.Providers) != 0 {
		t.Error("a missing file must read as empty")
	}

	broken := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadExisting(broken); err == nil {
		t.Fatal("an unparsable configuration must be reported, not silently treated as empty")
	}
}

func TestApplyLeavesTheDefaultAloneWhenNotAsked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{
  "schema_version": 1,
  "default_provider": "ollama",
  "default_model": "qwen2.5-coder",
  "providers": {"ollama": {"type": "openai-compatible", "model": "qwen2.5-coder"}}
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result := Build("anthropic", appconfig.Provider{Type: "anthropic", BaseURL: "https://api.anthropic.com"}, "claude-sonnet-5", CredentialEnv, "ANTHROPIC_API_KEY", "")
	result.MakeDefault = false
	if err := Apply(path, result); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document["default_provider"] != "ollama" || document["default_model"] != "qwen2.5-coder" {
		t.Errorf("adding a provider must not repoint the default: %v / %v", document["default_provider"], document["default_model"])
	}
	providers := document["providers"].(map[string]any)
	if _, ok := providers["anthropic"]; !ok {
		t.Error("the new provider must still be added")
	}
}

func TestSanitizeSecretHandlesRealPasteDamage(t *testing.T) {
	// A field that does not echo cannot show the user that their key arrived
	// wrapped, quoted, or with a trailing newline.
	cases := map[string]string{
		"  ABSKkey123  ":     "ABSKkey123",
		"\"ABSKkey123\"":     "ABSKkey123",
		"'ABSKkey123'":       "ABSKkey123",
		"ABSK\nkey\r\n123":   "ABSKkey123",
		"ABSK key 123":       "ABSKkey123",
		"\"ABSK\nkey123\"\n": "ABSKkey123",
		"ABSKkey123":         "ABSKkey123",
	}
	for input, want := range cases {
		if got := SanitizeSecret(input); got != want {
			t.Errorf("SanitizeSecret(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDiagnoseDoesNotClaimTheCredentialIsValidWhenTheEndpointSaysOtherwise(t *testing.T) {
	// Reported from a real Bedrock run: the panel asserted "The credential is
	// valid, but not allowed to use this model" directly above AWS's own
	// "Missing required parameters in the API Key". The summary contradicted
	// the evidence printed beneath it and sent the reader to the model-access
	// console instead of to the key.
	d := Diagnose(
		appconfig.Provider{Type: "bedrock", Region: "us-east-1"},
		"us.anthropic.claude-opus-5", nil,
		&provider.Error{Kind: provider.ErrorPermission, StatusCode: 403, Message: "Missing required parameters in the API Key"},
	)
	if strings.Contains(d.Summary, "credential is valid") {
		t.Errorf("summary must not assert a valid credential against a message about the key: %q", d.Summary)
	}
	if !strings.Contains(d.Summary, "rejected the credential") {
		t.Errorf("summary = %q", d.Summary)
	}
	if !containsSubstring(d.Fixes, "incomplete") {
		t.Errorf("truncation must lead the fixes, since it is invisible in a field that does not echo: %v", d.Fixes)
	}
	if !containsSubstring(d.Fixes, "AWS_BEARER_TOKEN_BEDROCK") {
		t.Errorf("the env-var route avoids the text field entirely and should be offered: %v", d.Fixes)
	}
	if !containsSubstring(d.Fixes, "per region") {
		t.Errorf("region-scoped Bedrock keys should be named: %v", d.Fixes)
	}
}

func TestDiagnoseStillReportsRealModelAccessDenials(t *testing.T) {
	// The credential-shape branch must not swallow the genuine entitlement
	// case, which is what a Bedrock 403 usually is.
	d := Diagnose(
		appconfig.Provider{Type: "bedrock", Region: "us-east-1"},
		"us.anthropic.claude-opus-5", nil,
		&provider.Error{Kind: provider.ErrorPermission, StatusCode: 403, Message: "You don't have access to the model with the specified model ID."},
	)
	if !strings.Contains(d.Summary, "not allowed to use this model") {
		t.Errorf("summary = %q", d.Summary)
	}
	if !containsSubstring(d.Fixes, "request model access") {
		t.Errorf("fixes = %v", d.Fixes)
	}
}

func TestDiagnoseSigV4FailureNamesTheAWSChainNotAnAPIKey(t *testing.T) {
	// SigV4 signs with ordinary IAM credentials that Collomia never holds, so
	// advice about a revoked or mistyped API key is useless — the real cause is
	// an unset AWS_ACCESS_KEY_ID, a missing profile, or an expired SSO session.
	d := Diagnose(
		appconfig.Provider{Type: "bedrock", Region: "us-east-1", Auth: "sigv4"},
		"anthropic.claude-sonnet-4-20250514-v1:0", nil,
		&provider.Error{Kind: provider.ErrorAuthentication, Message: "retrieve AWS credentials: no EC2 IMDS role found"},
	)
	if !strings.Contains(d.Summary, "No AWS credentials could be resolved") {
		t.Errorf("summary = %q", d.Summary)
	}
	if !containsSubstring(d.Fixes, "AWS_ACCESS_KEY_ID") {
		t.Errorf("fixes must name the IAM variables: %v", d.Fixes)
	}
	if !containsSubstring(d.Fixes, "aws sso login") {
		t.Errorf("an expired Identity Center session is the usual cause and should be named: %v", d.Fixes)
	}
	if containsSubstring(d.Fixes, "revoked") {
		t.Error("a chain failure must not offer API-key advice; there is no key to check")
	}
}

func TestDiagnoseTreatsBedrockAutoWithNoTokenAsTheAWSChain(t *testing.T) {
	// `auto` with no token resolves to SigV4, so the user never chose the word
	// "sigv4" and would not connect the failure to it on their own.
	d := Diagnose(
		appconfig.Provider{Type: "bedrock", Region: "us-east-1"},
		"model", nil,
		&provider.Error{Kind: provider.ErrorAuthentication, Message: "retrieve AWS credentials"},
	)
	if !containsSubstring(d.Fixes, "AWS_ACCESS_KEY_ID") {
		t.Errorf("auto-without-token is the AWS chain and must be diagnosed as such: %v", d.Fixes)
	}

	// But an explicit bearer configuration is a key problem, not a chain one.
	bearer := Diagnose(
		appconfig.Provider{Type: "bedrock", Region: "us-east-1", Auth: "bearer", APIKeyEnv: "AWS_BEARER_TOKEN_BEDROCK"},
		"model", nil,
		&provider.Error{Kind: provider.ErrorAuthentication, Message: "invalid token"},
	)
	if containsSubstring(bearer.Fixes, "AWS_ACCESS_KEY_ID") {
		t.Errorf("a bearer failure must not send the user to IAM variables: %v", bearer.Fixes)
	}
}

func TestBedrockAuthHintExplainsSigV4(t *testing.T) {
	// "SigV4" is the one term on these screens that assumes AWS knowledge.
	var bedrock Manual
	for _, candidate := range ManualCandidates() {
		if candidate.Key == "bedrock" {
			bedrock = candidate
		}
	}
	var hint string
	for _, field := range bedrock.Fields {
		if field.Key == "auth" {
			hint = field.Hint
		}
	}
	for _, want := range []string{"AWS_ACCESS_KEY_ID", "AWS_SESSION_TOKEN", "profile", "No key is entered here"} {
		if !strings.Contains(hint, want) {
			t.Errorf("the authentication hint should mention %q; got %q", want, hint)
		}
	}
}
