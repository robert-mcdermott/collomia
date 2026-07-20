package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
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
		if body["store"] != false || body["stream"] != true || len(body["tools"].([]any)) != 1 {
			t.Errorf("body=%+v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"id\":\"item_1\",\"call_id\":\"call_1\",\"name\":\"read_file\",\"arguments\":\"\"}}\n\n")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"checked\"}\n\n")
		fmt.Fprint(w, "event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"item_1\",\"output_index\":1,\"delta\":\"{\\\"path\\\":\\\"README.md\\\"}\"}\n\n")
		fmt.Fprint(w, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"id\":\"item_1\",\"call_id\":\"call_1\",\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"README.md\\\"}\"}}\n\n")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"checked\"}]},{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"README.md\\\"}\"}],\"usage\":{\"input_tokens\":11,\"output_tokens\":5}}}\n\n")
	}))
	defer server.Close()
	assertContractResponse(t, &ResponsesClient{Label: "responses-contract", BaseURL: server.URL, APIKey: "secret"}, "checked", Usage{InputTokens: 11, OutputTokens: 5})
}

func TestResponsesSynchronousJSONFallback(t *testing.T) {
	payload := `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"checked"}]},{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"README.md\"}"}],"usage":{"input_tokens":11,"output_tokens":5}}`
	response, err := parseResponsesResponse(strings.NewReader(payload), "responses-contract", nil)
	if err != nil || response.Content != "checked" || len(response.ToolCalls) != 1 || response.Usage != (Usage{InputTokens: 11, OutputTokens: 5}) {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

func TestResponsesRetriesTransientFailure(t *testing.T) {
	calls := 0
	client := &ResponsesClient{
		Label: "responses-retry", BaseURL: "https://example.invalid", APIKey: "secret",
		HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			body := `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"recovered"}]}]}`
			status := http.StatusOK
			if calls == 1 {
				status = http.StatusServiceUnavailable
				body = `{"error":{"message":"temporarily unavailable"}}`
			}
			return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: http.Header{"Retry-After": []string{"0"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
		})},
	}
	response, err := client.Chat(t.Context(), contractRequest(), nil)
	if err != nil || response.Content != "recovered" || calls != 2 {
		t.Fatalf("response=%+v calls=%d err=%v", response, calls, err)
	}
}

func TestBedrockConverseStreamContract(t *testing.T) {
	body, err := bedrockRequest(contractRequest())
	if err != nil {
		t.Fatal(err)
	}
	toolConfig, ok := body["toolConfig"].(map[string]any)
	if !ok || len(toolConfig["tools"].([]any)) != 1 {
		t.Fatalf("request body=%+v", body)
	}
	payload := bedrockEventStream(t,
		bedrockEvent{"messageStart", `{"role":"assistant"}`},
		bedrockEvent{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"checked"}}`},
		bedrockEvent{"contentBlockStart", `{"contentBlockIndex":1,"start":{"toolUse":{"toolUseId":"tool_1","name":"read_file"}}}`},
		bedrockEvent{"contentBlockDelta", `{"contentBlockIndex":1,"delta":{"toolUse":{"input":"{\"path\":\"README.md\"}"}}}`},
		bedrockEvent{"contentBlockStop", `{"contentBlockIndex":1}`},
		bedrockEvent{"messageStop", `{"stopReason":"tool_use"}`},
		bedrockEvent{"metadata", `{"usage":{"inputTokens":8,"outputTokens":2}}`},
	)
	var streamed strings.Builder
	response, err := parseBedrockStream(bytes.NewReader(payload), "bedrock-contract", "req-1", func(delta Delta) { streamed.WriteString(delta.Text) })
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "checked" || streamed.String() != "checked" || len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "read_file" || string(response.ToolCalls[0].Arguments) != `{"path":"README.md"}` || response.Usage != (Usage{InputTokens: 8, OutputTokens: 2}) {
		t.Fatalf("response=%+v streamed=%q", response, streamed.String())
	}
	client := &BedrockClient{
		Label: "bedrock-contract", Region: "us-east-1", Auth: "bearer", APIKey: "secret",
		HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/model/contract-model/converse-stream" || req.Header.Get("Accept") != "application/vnd.amazon.eventstream" || req.Header.Get("Authorization") != "Bearer secret" {
				t.Errorf("path=%q headers=%v", req.URL.Path, req.Header)
			}
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}, "x-amzn-requestid": []string{"req-1"}}, Body: io.NopCloser(bytes.NewReader(payload)), Request: req}, nil
		})},
	}
	assertContractResponse(t, client, "checked", Usage{InputTokens: 8, OutputTokens: 2})
}

type bedrockEvent struct {
	typ     string
	payload string
}

func bedrockEventStream(t *testing.T, events ...bedrockEvent) []byte {
	t.Helper()
	var out bytes.Buffer
	encoder := eventstream.NewEncoder()
	for _, item := range events {
		headers := eventstream.Headers{}
		headers.Set(":message-type", eventstream.StringValue("event"))
		headers.Set(":event-type", eventstream.StringValue(item.typ))
		headers.Set(":content-type", eventstream.StringValue("application/json"))
		if err := encoder.Encode(&out, eventstream.Message{Headers: headers, Payload: []byte(item.payload)}); err != nil {
			t.Fatal(err)
		}
	}
	return out.Bytes()
}

func bedrockExceptionStream(t *testing.T, code, payload string) []byte {
	t.Helper()
	var out bytes.Buffer
	headers := eventstream.Headers{}
	headers.Set(":message-type", eventstream.StringValue("exception"))
	headers.Set(":exception-type", eventstream.StringValue(code))
	if err := eventstream.NewEncoder().Encode(&out, eventstream.Message{Headers: headers, Payload: []byte(payload)}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestBedrockConverseRetriesTransientFailure(t *testing.T) {
	calls := 0
	client := &BedrockClient{
		Label: "bedrock-retry", Region: "us-east-1", Auth: "bearer", APIKey: "secret",
		HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			body := `{"output":{"message":{"content":[{"text":"recovered"}]}},"usage":{"inputTokens":1,"outputTokens":1},"stopReason":"end_turn"}`
			status := http.StatusOK
			if calls == 1 {
				status = http.StatusTooManyRequests
				body = `{"message":"slow down"}`
			}
			return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: http.Header{"Retry-After": []string{"0"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
		})},
	}
	response, err := client.Chat(t.Context(), contractRequest(), nil)
	if err != nil || response.Content != "recovered" || calls != 2 {
		t.Fatalf("response=%+v calls=%d err=%v", response, calls, err)
	}
}

func TestBedrockConverseGroupsParallelToolResults(t *testing.T) {
	request := contractRequest()
	request.Messages = []Message{
		{Role: "user", Content: "Inspect two files"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "tool_1", Name: "read_file", Arguments: json.RawMessage(`{"path":"README.md"}`)},
			{ID: "tool_2", Name: "read_file", Arguments: json.RawMessage(`{"path":"go.mod"}`)},
		}},
		{Role: "tool", ToolCallID: "tool_1", Content: "README contents"},
		{Role: "tool", ToolCallID: "tool_2", Content: "module contents"},
	}

	body, err := bedrockRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	messages := body["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("messages=%#v", messages)
	}
	resultMessage := messages[2].(map[string]any)
	if resultMessage["role"] != "user" {
		t.Fatalf("tool-result role=%#v", resultMessage["role"])
	}
	content := resultMessage["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("tool-result content=%#v", content)
	}
	for i, wantID := range []string{"tool_1", "tool_2"} {
		block := content[i].(map[string]any)["toolResult"].(map[string]any)
		if block["toolUseId"] != wantID {
			t.Fatalf("tool result %d=%#v", i, block)
		}
	}
}
