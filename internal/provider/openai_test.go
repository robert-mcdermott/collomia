package provider

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

func TestParseOpenAIStreamTextUsageAndToolFragments(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Hi ","reasoning_content":"checking"}}]}`,
		"",
		`data: {"choices":[{"delta":{"content":"there","tool_calls":[{"index":0,"id":"call_1","function":{"name":"read_","arguments":"{\"pa"}}]}}]}`,
		"",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"file","arguments":"th\":\"README.md\"}"}}]},"finish_reason":"tool_calls"}]}`,
		"",
		`data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":4}}`,
		"", `data: [DONE]`, "",
	}, "\n")
	var deltas, reasoning string
	var toolDeltas, usageEvents int
	response, err := parseOpenAIStream(strings.NewReader(stream), func(delta Delta) {
		deltas += delta.Text
		reasoning += delta.Reasoning
		if delta.ToolCall != nil {
			toolDeltas++
		}
		if delta.Usage != nil {
			usageEvents++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "Hi there" || deltas != "Hi there" {
		t.Fatalf("content=%q deltas=%q", response.Content, deltas)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "read_file" || string(response.ToolCalls[0].Arguments) != `{"path":"README.md"}` {
		t.Fatalf("tool calls=%+v", response.ToolCalls)
	}
	if response.Usage.InputTokens != 12 || response.Usage.OutputTokens != 4 {
		t.Fatalf("usage=%+v", response.Usage)
	}
	if reasoning != "checking" || toolDeltas != 3 || usageEvents != 1 {
		t.Fatalf("reasoning=%q toolDeltas=%d usageEvents=%d", reasoning, toolDeltas, usageEvents)
	}
}

func TestAzureOpenAIURL(t *testing.T) {
	p := appconfig.Provider{BaseURL: "https://demo.openai.azure.com", Deployment: "my deployment", APIVersion: "2024-10-21"}
	got, err := azureOpenAIChatURL(p, "ignored")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://demo.openai.azure.com/openai/deployments/my%20deployment/chat/completions?api-version=2024-10-21"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestListModelsParsesCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("auth=%q", got)
		}
		fmt.Fprint(w, `{"data":[{"id":"qwen3-coder"},{"id":"llama3.3"}]}`)
	}))
	defer server.Close()
	client := &OpenAIClient{Label: "test", BaseURL: server.URL, APIKey: "test-key"}
	models, err := client.ListModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "llama3.3" {
		t.Fatalf("models=%v", models)
	}
}

func TestDoWithRetryRecoversFromTransientFailures(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"recovered"}]}`)
	}))
	defer server.Close()
	client := &OpenAIClient{Label: "flaky", BaseURL: server.URL}
	models, err := client.ListModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(models) != 1 || models[0].ID != "recovered" {
		t.Fatalf("calls=%d models=%v", calls, models)
	}
}

func TestDoWithRetryGivesUpOnPersistentFailure(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := &OpenAIClient{Label: "down", BaseURL: server.URL}
	if _, err := client.ListModels(t.Context()); err == nil {
		t.Fatal("expected failure after exhausting retries")
	}
	if calls != 3 {
		t.Fatalf("calls=%d, want 3 attempts", calls)
	}
}

func TestDoWithRetryDoesNotRetryClientErrors(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	client := &OpenAIClient{Label: "auth", BaseURL: server.URL}
	if _, err := client.ListModels(t.Context()); err == nil {
		t.Fatal("expected auth error")
	}
	if calls != 1 {
		t.Fatalf("calls=%d; 401 must not be retried", calls)
	}
}
