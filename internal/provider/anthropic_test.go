package provider

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestParseAnthropicNonStream(t *testing.T) {
	payload := `{"content":[{"type":"text","text":"checking"},{"type":"tool_use","id":"tool_1","name":"read_file","input":{"path":"README.md"}}],"stop_reason":"tool_use","usage":{"input_tokens":9,"output_tokens":3}}`
	var delta string
	response, err := parseAnthropicNonStream(strings.NewReader(payload), func(part Delta) { delta += part.Text })
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "checking" || delta != "checking" {
		t.Fatalf("content=%q delta=%q", response.Content, delta)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "read_file" {
		t.Fatalf("tool calls=%+v", response.ToolCalls)
	}
	if response.Usage.InputTokens != 9 || response.Usage.OutputTokens != 3 {
		t.Fatalf("usage=%+v", response.Usage)
	}
}

func TestAnthropicReasoningFallsBackOnlyAfterExplicitRejection(t *testing.T) {
	var bodies []map[string]any
	client := &AnthropicClient{
		Label: "anthropic", BaseURL: "https://example.invalid", APIKey: "secret",
		HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				return nil, err
			}
			bodies = append(bodies, body)
			if len(bodies) == 1 {
				return openAIHTTPResponse(req, http.StatusBadRequest, "application/json", `{"error":{"message":"output_config.effort must be one of low, medium, or high"}}`), nil
			}
			payload := `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
		})},
	}
	var warning string
	response, err := client.Chat(t.Context(), Request{Model: "model", Messages: []Message{{Role: "user", Content: "hello"}}, MaxTokens: 10, ReasoningEffort: "high"}, func(delta Delta) {
		warning += delta.Warning
	})
	if err != nil || response.Content != "ok" || len(bodies) != 2 {
		t.Fatalf("response=%+v bodies=%+v err=%v", response, bodies, err)
	}
	if _, ok := bodies[0]["output_config"]; !ok {
		t.Fatalf("first request omitted reasoning: %+v", bodies[0])
	}
	if _, ok := bodies[1]["output_config"]; ok || !strings.Contains(warning, "reasoning") {
		t.Fatalf("fallback body=%+v warning=%q", bodies[1], warning)
	}
}

func TestParseAnthropicStreamNormalizesReasoningToolAndUsage(t *testing.T) {
	stream := strings.Join([]string{
		`event: message_start`, `data: {"message":{"usage":{"input_tokens":9,"output_tokens":0,"cache_read_input_tokens":2}}}`, "",
		`event: content_block_delta`, `data: {"index":0,"delta":{"type":"thinking_delta","thinking":"checking"}}`, "",
		`event: content_block_start`, `data: {"index":1,"content_block":{"type":"tool_use","id":"tool_1","name":"read_file","input":{}}}`, "",
		`event: content_block_delta`, `data: {"index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"README.md\"}"}}`, "",
		`event: message_delta`, `data: {"delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3}}`, "",
	}, "\n")
	var reasoning string
	var toolDeltas, usageEvents int
	response, err := parseAnthropicStream(strings.NewReader(stream), func(delta Delta) {
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
	if reasoning != "checking" || toolDeltas != 3 || usageEvents != 1 || len(response.ToolCalls) != 1 || response.Usage != (Usage{InputTokens: 9, OutputTokens: 3, CachedTokens: 2}) {
		t.Fatalf("response=%+v reasoning=%q toolDeltas=%d usageEvents=%d", response, reasoning, toolDeltas, usageEvents)
	}
}
