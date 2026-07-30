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
	// InputTokens is the whole prompt: Anthropic reports input_tokens net of
	// the cache counters, so 9 + 2 read is what the request actually consumed.
	if reasoning != "checking" || toolDeltas != 3 || usageEvents != 1 || len(response.ToolCalls) != 1 || response.Usage != (Usage{InputTokens: 11, OutputTokens: 3, CachedTokens: 2}) {
		t.Fatalf("response=%+v reasoning=%q toolDeltas=%d usageEvents=%d", response, reasoning, toolDeltas, usageEvents)
	}
}

// cacheBreakpoints reports where a decoded request body carries
// cache_control, as a set of coarse locations, so the assertions below read
// as claims about placement rather than about JSON shape.
func cacheBreakpoints(t *testing.T, body map[string]any) map[string]int {
	t.Helper()
	found := map[string]int{}
	if blocks, ok := body["system"].([]any); ok {
		for _, block := range blocks {
			if entry, ok := block.(map[string]any); ok {
				if _, marked := entry["cache_control"]; marked {
					found["system"]++
				}
			}
		}
	}
	if defs, ok := body["tools"].([]any); ok {
		for _, def := range defs {
			if entry, ok := def.(map[string]any); ok {
				if _, marked := entry["cache_control"]; marked {
					found["tools"]++
				}
			}
		}
	}
	messages, _ := body["messages"].([]any)
	for i, message := range messages {
		entry, ok := message.(map[string]any)
		if !ok {
			continue
		}
		blocks, ok := entry["content"].([]any)
		if !ok {
			continue
		}
		for _, block := range blocks {
			if part, ok := block.(map[string]any); ok {
				if _, marked := part["cache_control"]; marked {
					found["messages"]++
					found["last_marked_index"] = i
				}
			}
		}
	}
	return found
}

func anthropicCacheTestClient(t *testing.T, bodies *[]map[string]any, status func(call int) (int, string)) *AnthropicClient {
	t.Helper()
	return &AnthropicClient{
		Label: "anthropic", BaseURL: "https://example.invalid", APIKey: "secret",
		HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				return nil, err
			}
			*bodies = append(*bodies, body)
			if status != nil {
				if code, payload := status(len(*bodies)); code != 0 {
					return openAIHTTPResponse(req, code, "application/json", payload), nil
				}
			}
			payload := `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
		})},
	}
}

// TestAnthropicCachesTheStablePrefixAndTheConversation pins the two
// breakpoints and, more importantly, that neither lands on the volatile
// trailing message. A breakpoint at or after regenerated content writes a
// prefix nothing ever reads back, and cache writes cost more than ordinary
// input, so misplacing it is worse than not caching at all.
func TestAnthropicCachesTheStablePrefixAndTheConversation(t *testing.T) {
	var bodies []map[string]any
	client := anthropicCacheTestClient(t, &bodies, nil)
	request := Request{
		Model: "model", System: "you are a coding agent", MaxTokens: 10,
		Tools: []ToolDefinition{
			{Name: "read_file", Description: "read", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "write_file", Description: "write", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
		Messages: []Message{
			{Role: "user", Content: "fix the bug"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "t1", Name: "read_file", Arguments: json.RawMessage(`{}`)}}},
			{Role: "tool", ToolCallID: "t1", Content: "file contents"},
			{Role: "user", Content: "current plan", Volatile: true},
		},
	}
	if _, err := client.Chat(t.Context(), request, nil); err != nil {
		t.Fatal(err)
	}
	found := cacheBreakpoints(t, bodies[0])
	if found["system"] != 1 {
		t.Fatalf("expected one breakpoint on the system block: %+v", found)
	}
	if found["tools"] != 0 {
		t.Fatalf("tools are already covered by the system breakpoint ahead of them: %+v", found)
	}
	if found["messages"] != 1 || found["last_marked_index"] != 2 {
		t.Fatalf("conversation breakpoint should sit on the last non-volatile message (index 2): %+v", found)
	}
}

// With no system prompt the stable-prefix breakpoint has to fall back to the
// last tool definition, or the tool schemas — the largest fixed cost in an
// agent request — are never cached at all.
func TestAnthropicCachesToolsWhenThereIsNoSystemPrompt(t *testing.T) {
	var bodies []map[string]any
	client := anthropicCacheTestClient(t, &bodies, nil)
	request := Request{
		Model: "model", MaxTokens: 10,
		Tools:    []ToolDefinition{{Name: "read_file", Description: "read", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Messages: []Message{{Role: "user", Content: "hello"}},
	}
	if _, err := client.Chat(t.Context(), request, nil); err != nil {
		t.Fatal(err)
	}
	if found := cacheBreakpoints(t, bodies[0]); found["tools"] != 1 {
		t.Fatalf("expected the stable-prefix breakpoint to fall back to the last tool: %+v", found)
	}
}

// An endpoint that does not implement caching must not lose the request, and
// must not be asked again on every later call.
func TestAnthropicDropsCachingForTheSessionAfterRejection(t *testing.T) {
	var bodies []map[string]any
	client := anthropicCacheTestClient(t, &bodies, func(call int) (int, string) {
		if call == 1 {
			return http.StatusBadRequest, `{"error":{"message":"unexpected field cache_control"}}`
		}
		return 0, ""
	})
	request := Request{
		Model: "model", System: "prefix", MaxTokens: 10,
		Messages: []Message{{Role: "user", Content: "hello"}},
	}
	var warning string
	response, err := client.Chat(t.Context(), request, func(delta Delta) { warning += delta.Warning })
	if err != nil || response.Content != "ok" || len(bodies) != 2 {
		t.Fatalf("response=%+v bodies=%d err=%v", response, len(bodies), err)
	}
	if found := cacheBreakpoints(t, bodies[1]); len(found) != 0 {
		t.Fatalf("retry still carried breakpoints: %+v", found)
	}
	if system, ok := bodies[1]["system"].(string); !ok || system != "prefix" {
		t.Fatalf("retry did not restore the plain system prompt: %+v", bodies[1]["system"])
	}
	if !strings.Contains(warning, "cache") {
		t.Fatalf("rejection was not reported: %q", warning)
	}
	// The refusal is remembered: a second call must not spend another
	// rejected round trip discovering the same thing.
	if _, err := client.Chat(t.Context(), request, nil); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 3 {
		t.Fatalf("expected one request on the second call, got %d total", len(bodies))
	}
	if found := cacheBreakpoints(t, bodies[2]); len(found) != 0 {
		t.Fatalf("caching was retried after being refused: %+v", found)
	}
}

// InputTokens must mean the whole prompt regardless of how a provider splits
// its counters, because the context gauge and every cost estimate read it.
func TestAnthropicUsageCountsCachedTokensAsInput(t *testing.T) {
	usage := anthropicUsage(120, 40, 8000, 500)
	if usage.InputTokens != 8620 || usage.CachedTokens != 8000 || usage.CacheWriteTokens != 500 || usage.OutputTokens != 40 {
		t.Fatalf("usage=%+v", usage)
	}
}
