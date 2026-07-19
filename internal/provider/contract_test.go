package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func contractRequest() Request {
	return Request{
		Model: "contract-model", System: "Be concise.",
		Messages:  []Message{{Role: "user", Content: "Inspect README.md"}},
		Tools:     []ToolDefinition{{Name: "read_file", Description: "read a file", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}},
		MaxTokens: 256,
	}
}

func assertContractResponse(t *testing.T, client Client, wantContent string, wantUsage Usage) {
	t.Helper()
	var streamed strings.Builder
	response, err := client.Chat(t.Context(), contractRequest(), func(delta Delta) { streamed.WriteString(delta.Text) })
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != wantContent || streamed.String() != wantContent {
		t.Fatalf("content=%q streamed=%q", response.Content, streamed.String())
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "read_file" || string(response.ToolCalls[0].Arguments) != `{"path":"README.md"}` {
		t.Fatalf("tool calls=%+v", response.ToolCalls)
	}
	if response.Usage != wantUsage {
		t.Fatalf("usage=%+v want=%+v", response.Usage, wantUsage)
	}
}

func TestOpenAIChatCompletionsContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("request path=%q auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["stream"] != true || len(body["tools"].([]any)) != 1 {
			t.Errorf("body=%+v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"checked\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"README.md\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":4,\"prompt_tokens_details\":{\"cached_tokens\":3},\"completion_tokens_details\":{\"reasoning_tokens\":2}}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	assertContractResponse(t, &OpenAIClient{Label: "openai-contract", BaseURL: server.URL, APIKey: "secret"}, "checked", Usage{InputTokens: 12, OutputTokens: 4, CachedTokens: 3, ReasoningTokens: 2})
}

func TestAnthropicMessagesContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "secret" || r.Header.Get("anthropic-version") == "" {
			t.Errorf("request path=%q headers=%v", r.URL.Path, r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["stream"] != true || len(body["tools"].([]any)) != 1 {
			t.Errorf("body=%+v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"message\":{\"usage\":{\"input_tokens\":9,\"output_tokens\":0,\"cache_read_input_tokens\":2}}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"checked\"}}\n\n")
		fmt.Fprint(w, "event: content_block_start\ndata: {\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool_1\",\"name\":\"read_file\",\"input\":{}}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\\\"README.md\\\"}\"}}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":3}}\n\n")
	}))
	defer server.Close()
	assertContractResponse(t, &AnthropicClient{Label: "anthropic-contract", BaseURL: server.URL, APIKey: "secret"}, "checked", Usage{InputTokens: 9, OutputTokens: 3, CachedTokens: 2})
}

func TestResponsesContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("request path=%q auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["store"] != false || len(body["tools"].([]any)) != 1 {
			t.Errorf("body=%+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"checked"}]},{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"README.md\"}"}],"usage":{"input_tokens":11,"output_tokens":5}}`)
	}))
	defer server.Close()
	assertContractResponse(t, &ResponsesClient{Label: "responses-contract", BaseURL: server.URL, APIKey: "secret"}, "checked", Usage{InputTokens: 11, OutputTokens: 5})
}

func TestBedrockConverseContract(t *testing.T) {
	body, err := bedrockRequest(contractRequest())
	if err != nil {
		t.Fatal(err)
	}
	toolConfig, ok := body["toolConfig"].(map[string]any)
	if !ok || len(toolConfig["tools"].([]any)) != 1 {
		t.Fatalf("request body=%+v", body)
	}
	payload := `{"output":{"message":{"content":[{"text":"checked"},{"toolUse":{"toolUseId":"tool_1","name":"read_file","input":{"path":"README.md"}}}]}},"usage":{"inputTokens":8,"outputTokens":2},"stopReason":"tool_use"}`
	var streamed strings.Builder
	response, err := parseBedrockResponse(strings.NewReader(payload), func(delta Delta) { streamed.WriteString(delta.Text) })
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "checked" || streamed.String() != "checked" || len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "read_file" || string(response.ToolCalls[0].Arguments) != `{"path":"README.md"}` || response.Usage != (Usage{InputTokens: 8, OutputTokens: 2}) {
		t.Fatalf("response=%+v streamed=%q", response, streamed.String())
	}
}
