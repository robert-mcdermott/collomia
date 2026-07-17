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
