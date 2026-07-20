package provider

import (
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
