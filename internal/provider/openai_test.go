package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

func TestOpenAIChatNegotiatesReasoningParametersAndRemembers(t *testing.T) {
	var bodies []map[string]any
	var authorizations []string
	tokenCalls := 0
	client := &OpenAIClient{
		Label: "azure-openai/gpt-5.5", ChatURL: "https://example.invalid/chat",
		BearerSource: bearerSourceFunc(func(context.Context) (string, error) {
			tokenCalls++
			return fmt.Sprintf("entra-token-%d", tokenCalls), nil
		}),
		HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				return nil, err
			}
			bodies = append(bodies, body)
			authorizations = append(authorizations, req.Header.Get("Authorization"))
			switch len(bodies) {
			case 1:
				return openAIHTTPResponse(req, http.StatusBadRequest, "application/json", `{"error":{"message":"Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead.","type":"invalid_request_error","param":"max_tokens","code":"unsupported_parameter"}}`), nil
			case 2:
				return openAIHTTPResponse(req, http.StatusBadRequest, "application/json", `{"error":{"message":"Unsupported value: 'temperature' does not support 0.2 with this model. Only the default (1) value is supported.","type":"invalid_request_error","param":"temperature","code":"unsupported_value"}}`), nil
			default:
				return openAISuccessStream(req, "ok"), nil
			}
		})},
	}
	temperature := 0.2
	request := Request{Model: "gpt-5.5", Messages: []Message{{Role: "user", Content: "hello"}}, MaxTokens: 64, Temperature: &temperature}
	var streamed, warnings strings.Builder
	for range 2 {
		response, err := client.Chat(t.Context(), request, func(delta Delta) {
			streamed.WriteString(delta.Text)
			warnings.WriteString(delta.Warning)
		})
		if err != nil || response.Content != "ok" {
			t.Fatalf("response=%+v err=%v", response, err)
		}
	}
	if len(bodies) != 4 || tokenCalls != 4 {
		t.Fatalf("requests=%d token calls=%d, want 4 adjusted/authenticated attempts", len(bodies), tokenCalls)
	}
	if _, ok := bodies[0]["max_tokens"]; !ok || bodies[0]["temperature"] != 0.2 {
		t.Fatalf("initial body=%+v", bodies[0])
	}
	if _, ok := bodies[1]["max_tokens"]; ok || bodies[1]["max_completion_tokens"] != float64(64) || bodies[1]["temperature"] != 0.2 {
		t.Fatalf("token-adjusted body=%+v", bodies[1])
	}
	for index, body := range bodies[2:] {
		if body["max_completion_tokens"] != float64(64) {
			t.Errorf("learned body %d max tokens=%+v", index+2, body)
		}
		if _, ok := body["max_tokens"]; ok {
			t.Errorf("learned body %d retained max_tokens: %+v", index+2, body)
		}
		if _, ok := body["temperature"]; ok {
			t.Errorf("learned body %d retained temperature: %+v", index+2, body)
		}
	}
	for index, authorization := range authorizations {
		want := fmt.Sprintf("Bearer entra-token-%d", index+1)
		if authorization != want {
			t.Errorf("request %d authorization=%q want=%q", index+1, authorization, want)
		}
	}
	if streamed.String() != "okok" || strings.Count(warnings.String(), "provider rejected") != 1 {
		t.Fatalf("streamed=%q warnings=%q", streamed.String(), warnings.String())
	}
}

func TestOpenAIChatAcceptedParametersRemainUnchanged(t *testing.T) {
	tests := []struct {
		name         string
		client       func(*http.Client) *OpenAIClient
		wantAuthName string
		wantAuth     string
	}{
		{
			name: "older Azure deployment",
			client: func(httpClient *http.Client) *OpenAIClient {
				return &OpenAIClient{Label: "azure-openai/gpt-4", ChatURL: "https://example.invalid/chat", APIKey: "azure-key", APIKeyHeader: "api-key", HTTP: httpClient}
			},
			wantAuthName: "api-key", wantAuth: "azure-key",
		},
		{
			name: "OpenAI-compatible provider",
			client: func(httpClient *http.Client) *OpenAIClient {
				return &OpenAIClient{Label: "compatible/model", ChatURL: "https://example.invalid/chat", APIKey: "compatible-key", HTTP: httpClient}
			},
			wantAuthName: "Authorization", wantAuth: "Bearer compatible-key",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			temperature := 0.4
			httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					return nil, err
				}
				if body["max_tokens"] != float64(32) || body["temperature"] != 0.4 {
					return nil, fmt.Errorf("accepted body changed: %+v", body)
				}
				if _, ok := body["max_completion_tokens"]; ok {
					return nil, fmt.Errorf("accepted body gained max_completion_tokens: %+v", body)
				}
				if got := req.Header.Get(test.wantAuthName); got != test.wantAuth {
					return nil, fmt.Errorf("authentication=%q want=%q", got, test.wantAuth)
				}
				return openAISuccessStream(req, "accepted"), nil
			})}
			response, err := test.client(httpClient).Chat(t.Context(), Request{
				Model: "deployment", Messages: []Message{{Role: "user", Content: "hello"}}, MaxTokens: 32, Temperature: &temperature,
			}, nil)
			if err != nil || response.Content != "accepted" || calls != 1 {
				t.Fatalf("response=%+v calls=%d err=%v", response, calls, err)
			}
		})
	}
}

func TestOpenAIChatDoesNotAdaptUnrelatedBadRequest(t *testing.T) {
	calls := 0
	client := &OpenAIClient{
		Label: "compatible/model", ChatURL: "https://example.invalid/chat",
		HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			return openAIHTTPResponse(req, http.StatusBadRequest, "application/json", `{"error":{"message":"messages are invalid; max_tokens and max_completion_tokens are documented elsewhere","param":"messages","code":"unsupported_parameter"}}`), nil
		})},
	}
	_, err := client.Chat(t.Context(), Request{Model: "model", Messages: []Message{{Role: "user", Content: "hello"}}, MaxTokens: 32}, nil)
	providerErr, ok := AsError(err)
	if !ok || providerErr.Kind != ErrorInvalidRequest || calls != 1 {
		t.Fatalf("error=%+v raw=%v calls=%d", providerErr, err, calls)
	}
}

func TestOpenAIRejectedChatParameterIsStrict(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"max tokens", http.StatusBadRequest, `{"error":{"message":"Unsupported parameter: 'max_tokens'. Use 'max_completion_tokens' instead.","param":"max_tokens","code":"unsupported_parameter"}}`, "max_tokens"},
		{"temperature without param", http.StatusBadRequest, `{"error":{"message":"` + "`temperature`" + ` is deprecated for this model."}}`, "temperature"},
		{"invalid reasoning effort", http.StatusBadRequest, `{"error":{"message":"reasoning_effort must be one of low, medium, or high","param":"reasoning_effort","code":"invalid_value"}}`, "reasoning_effort"},
		{"wrong status", http.StatusInternalServerError, `{"error":{"message":"Unsupported parameter: 'max_tokens'. Use 'max_completion_tokens' instead."}}`, ""},
		{"unrelated parameter", http.StatusBadRequest, `{"error":{"message":"max_tokens and max_completion_tokens appear in docs","param":"messages","code":"unsupported_parameter"}}`, ""},
		{"invalid temperature range", http.StatusBadRequest, `{"error":{"message":"temperature must be between 0 and 2","param":"temperature","code":"unsupported_value"}}`, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := openAIRejectedChatParameter(test.status, []byte(test.body)); got != test.want {
				t.Fatalf("rejected parameter=%q want=%q", got, test.want)
			}
		})
	}
}

func TestOpenAIChatParameterLearningIsConcurrentSafe(t *testing.T) {
	var mu sync.Mutex
	legacyRequests, completionRequests := 0, 0
	client := &OpenAIClient{
		Label: "compatible/reasoning", ChatURL: "https://example.invalid/chat",
		HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				return nil, err
			}
			if _, ok := body["max_tokens"]; ok {
				mu.Lock()
				legacyRequests++
				mu.Unlock()
				return openAIHTTPResponse(req, http.StatusBadRequest, "application/json", `{"error":{"message":"Unsupported parameter: 'max_tokens'. Use 'max_completion_tokens' instead.","param":"max_tokens","code":"unsupported_parameter"}}`), nil
			}
			if body["max_completion_tokens"] != float64(16) {
				return nil, fmt.Errorf("missing negotiated token field: %+v", body)
			}
			mu.Lock()
			completionRequests++
			mu.Unlock()
			return openAISuccessStream(req, "ok"), nil
		})},
	}
	request := Request{Model: "reasoning", Messages: []Message{{Role: "user", Content: "hello"}}, MaxTokens: 16}
	ctx := t.Context()
	const workers = 12
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, err := client.Chat(ctx, request, nil)
			if err == nil && response.Content != "ok" {
				err = fmt.Errorf("content=%q", response.Content)
			}
			errs <- err
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	legacyBeforeFinal, completionBeforeFinal := legacyRequests, completionRequests
	mu.Unlock()
	if legacyBeforeFinal == 0 || completionBeforeFinal != workers {
		t.Fatalf("legacy=%d completion=%d", legacyBeforeFinal, completionBeforeFinal)
	}
	if _, err := client.Chat(ctx, request, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if legacyRequests != legacyBeforeFinal || completionRequests != completionBeforeFinal+1 {
		t.Fatalf("learned profile was not reused: legacy=%d completion=%d", legacyRequests, completionRequests)
	}
}

func openAIHTTPResponse(req *http.Request, status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: req,
	}
}

func openAISuccessStream(req *http.Request, content string) *http.Response {
	body := fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\ndata: [DONE]\n\n", content)
	return openAIHTTPResponse(req, http.StatusOK, "text/event-stream", body)
}

func TestOpenAIReasoningEffortIsOptInAndCanBeLearnedAway(t *testing.T) {
	client := &OpenAIClient{}
	request := Request{Model: "model", Messages: []Message{{Role: "user", Content: "hello"}}}
	body, err := client.chatBody(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := body["reasoning_effort"]; exists {
		t.Fatalf("unset reasoning changed request: %+v", body)
	}
	request.ReasoningEffort = "high"
	body, err = client.chatBody(request)
	if err != nil {
		t.Fatal(err)
	}
	if body["reasoning_effort"] != "high" {
		t.Fatalf("reasoning body=%+v", body)
	}
	if retry, warning := client.parameters.learn("reasoning_effort", body); !retry || !strings.Contains(warning, "reasoning") {
		t.Fatalf("retry=%t warning=%q", retry, warning)
	}
	body, err = client.chatBody(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := body["reasoning_effort"]; exists {
		t.Fatalf("learned profile retained reasoning: %+v", body)
	}
}
