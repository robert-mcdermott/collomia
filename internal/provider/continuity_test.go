package provider

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The Bedrock shape observed live on 2026-09-30 from us.anthropic.claude-opus-5-5
// with default (omitted) display: reasoning arrives as an empty text delta and
// a signature delta, with no contentBlockStart, then the answer in the next
// block.
func observedBedrockThinkingStream(t *testing.T) []byte {
	return bedrockEventStream(t,
		bedrockEvent{"messageStart", `{"role":"assistant"}`},
		bedrockEvent{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"text":""}}}`},
		bedrockEvent{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"signature":"SIG-A"}}}`},
		bedrockEvent{"contentBlockStop", `{"contentBlockIndex":0}`},
		bedrockEvent{"contentBlockStart", `{"contentBlockIndex":1,"start":{"toolUse":{"toolUseId":"tool_1","name":"read_file"}}}`},
		bedrockEvent{"contentBlockDelta", `{"contentBlockIndex":1,"delta":{"toolUse":{"input":"{\"path\":\"a.txt\"}"}}}`},
		bedrockEvent{"contentBlockStop", `{"contentBlockIndex":1}`},
		bedrockEvent{"messageStop", `{"stopReason":"tool_use"}`},
	)
}

func TestBedrockRecordsSignedReasoningInOrder(t *testing.T) {
	var recorder blockRecorder
	response, err := parseBedrockStreamRecording(bytes.NewReader(observedBedrockThinkingStream(t)), "bedrock", "req", nil, &recorder)
	if err != nil {
		t.Fatal(err)
	}
	state := recorder.state("bedrock us-east-1", "us.anthropic.claude-opus-5-5")
	if state == nil || len(state.Blocks) != 2 {
		t.Fatalf("state = %+v", state)
	}
	if state.Blocks[0] != (ReasoningBlock{Kind: "reasoning", Signature: "SIG-A"}) || state.Blocks[1] != (ReasoningBlock{Kind: "tool_use", ToolUseID: "tool_1"}) {
		t.Errorf("blocks = %+v; an empty reasoning text with a signature is still a complete block", state.Blocks)
	}
	if len(response.ToolCalls) != 1 {
		t.Fatal("recording must not disturb ordinary parsing")
	}
}

func TestBedrockReplaysReasoningVerbatimOnlyToItsRegion(t *testing.T) {
	state := &ReasoningState{Route: "bedrock us-east-1", Model: "m", Blocks: []ReasoningBlock{
		{Kind: "reasoning", Signature: "SIG-A"}, {Kind: "tool_use", ToolUseID: "tool_1"},
	}}
	history := []Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tool_1", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.txt"}`)}}, ReasoningState: state},
		{Role: "tool", ToolCallID: "tool_1", Content: "text"},
	}
	body, err := bedrockRequestFor(Request{Model: "m", Messages: history, MaxTokens: 10}, "bedrock us-east-1", false)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(body["messages"].([]any)[1])
	want := `{"content":[{"reasoningContent":{"reasoningText":{"signature":"SIG-A","text":""}}},{"toolUse":{"input":{"path":"a.txt"},"name":"read_file","toolUseId":"tool_1"}}],"role":"assistant"}`
	if string(encoded) != want {
		t.Errorf("assistant =\n%s\nwant\n%s", encoded, want)
	}
	other, _ := bedrockRequestFor(Request{Model: "m", Messages: history, MaxTokens: 10}, "bedrock us-west-2", false)
	if strings.Contains(mustJSON(t, other), "SIG-A") {
		t.Error("a signature must never be sent to a different region's endpoint")
	}
}

func TestReplayIsSkippedWhenTheToolCallsChanged(t *testing.T) {
	state := &ReasoningState{Route: "r", Blocks: []ReasoningBlock{{Kind: "reasoning", Signature: "S"}, {Kind: "tool_use", ToolUseID: "gone"}}}
	if _, ok := replayBlocks(Message{Role: "assistant", ReasoningState: state}, "r"); ok {
		t.Error("blocks describing a tool call the message no longer carries must not be replayed")
	}
}

func TestAnthropicRecordsAndReplaysThinkingVerbatim(t *testing.T) {
	stream := strings.Join([]string{
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\",\"signature\":\"\"}}\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"considering\"}}\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"SIG\"}}\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"redacted_thinking\",\"data\":\"ENC\"}}\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"text_delta\",\"text\":\"Reading.\"}}\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":3,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"read_file\",\"input\":{}}}\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":3,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\\\"a\\\"}\"}}\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n",
	}, "\n")
	var recorder blockRecorder
	response, err := parseAnthropicStreamRecording(strings.NewReader(stream), nil, &recorder)
	if err != nil {
		t.Fatal(err)
	}
	state := recorder.state("anthropic https://api.test", "claude-opus-5-5")
	if state == nil || len(state.Blocks) != 4 {
		t.Fatalf("state = %+v", state)
	}
	message := Message{Role: "assistant", Content: response.Content, ToolCalls: response.ToolCalls, ReasoningState: state}
	encoded, err := anthropicMessagesFor([]Message{{Role: "user", Content: "go"}, message}, "anthropic https://api.test")
	if err != nil {
		t.Fatal(err)
	}
	got := mustJSON(t, encoded[1])
	want := `{"content":[{"signature":"SIG","thinking":"considering","type":"thinking"},{"data":"ENC","type":"redacted_thinking"},{"text":"Reading.","type":"text"},{"id":"toolu_1","input":{"path":"a"},"name":"read_file","type":"tool_use"}],"role":"assistant"}`
	if got != want {
		t.Errorf("replayed =\n%s\nwant\n%s", got, want)
	}
	plain, _ := anthropicMessagesFor([]Message{message}, "anthropic https://other.test")
	if strings.Contains(mustJSON(t, plain), "SIG") {
		t.Error("a different endpoint must not receive the signature")
	}
}

func TestCacheBreakpointNeverLandsOnAThinkingBlock(t *testing.T) {
	entry := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "thinking", "thinking": "", "signature": "S"}}}
	anthropicMarkCacheBreakpoint(entry)
	if strings.Contains(mustJSON(t, entry), "cache_control") {
		t.Error("thinking blocks cannot carry cache_control")
	}
}

func TestAnthropicAsksDefaultAdaptiveModelsForReadableThinkingOnly(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()
	client := &AnthropicClient{Label: "a", BaseURL: server.URL}
	for _, model := range []string{"claude-opus-5-5", "claude-opus-4-6"} {
		if _, err := client.Chat(t.Context(), Request{Model: model, MaxTokens: 10, Messages: []Message{{Role: "user", Content: "hi"}}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if thinking, _ := bodies[0]["thinking"].(map[string]any); thinking["display"] != "summarized" || thinking["type"] != "adaptive" {
		t.Errorf("a default-adaptive model is asked for summarized display, got %v", bodies[0]["thinking"])
	}
	if _, ok := bodies[1]["thinking"]; ok {
		t.Error("a model that thinks only when asked must not be sent thinking: that would change cost and behavior")
	}
}

func TestAnthropicRefusedReplayFallsBackToTodaysBehavior(t *testing.T) {
	// The safety net: a refusal of replayed thinking costs one retry, never
	// the turn, and replay stops for the rest of this client's life.
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(data))
		if strings.Contains(string(data), "SIG") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"messages.1.content.0: Invalid ` + "`signature`" + ` in ` + "`thinking`" + ` block. The block is bound to a different conversation."}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()
	client := &AnthropicClient{Label: "a", BaseURL: server.URL}
	history := []Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "t1", Name: "read_file", Arguments: json.RawMessage(`{}`)}},
			ReasoningState: &ReasoningState{Route: client.continuityRoute(), Blocks: []ReasoningBlock{{Kind: "reasoning", Signature: "SIG"}, {Kind: "tool_use", ToolUseID: "t1"}}}},
		{Role: "tool", ToolCallID: "t1", Content: "x"},
	}
	var warnings []string
	response, err := client.Chat(t.Context(), Request{Model: "claude-opus-4-6", MaxTokens: 10, Messages: history}, func(d Delta) {
		if d.Warning != "" {
			warnings = append(warnings, d.Warning)
		}
	})
	if err != nil || response.Content != "ok" {
		t.Fatalf("the turn must survive a refused replay: %v", err)
	}
	if len(bodies) != 2 || strings.Contains(bodies[1], "SIG") || len(warnings) != 1 || !strings.Contains(warnings[0], "replayed thinking") {
		t.Fatalf("bodies %d warnings %v", len(bodies), warnings)
	}
	if _, err := client.Chat(t.Context(), Request{Model: "claude-opus-4-6", MaxTokens: 10, Messages: history}, nil); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 3 || strings.Contains(bodies[2], "SIG") {
		t.Error("after a refusal, this client must not replay again")
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
