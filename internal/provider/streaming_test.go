package provider

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestResponsesStreamRejectsTruncatedResponse(t *testing.T) {
	stream := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"
	_, err := parseResponsesStream(strings.NewReader(stream), "responses-test", nil)
	if err == nil || !strings.Contains(err.Error(), "without a terminal event") {
		t.Fatalf("error=%v", err)
	}
}

func TestOpenAIStreamClassifiesInBandFailure(t *testing.T) {
	stream := "data: {\"error\":{\"type\":\"rate_limit_error\",\"message\":\"slow down\"}}\n\n"
	_, err := parseOpenAIStream(strings.NewReader(stream), nil)
	var providerErr *Error
	if !errors.As(err, &providerErr) || providerErr.Kind != ErrorRateLimit || providerErr.Retryable {
		t.Fatalf("error=%#v", err)
	}
}

func TestAnthropicStreamClassifiesInBandFailure(t *testing.T) {
	stream := "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"busy\"}}\n\n"
	_, err := parseAnthropicStream(strings.NewReader(stream), nil)
	var providerErr *Error
	if !errors.As(err, &providerErr) || providerErr.Kind != ErrorUnavailable || providerErr.Retryable {
		t.Fatalf("error=%#v", err)
	}
}

func TestResponsesStreamClassifiesInBandFailure(t *testing.T) {
	stream := "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"rate_limit_exceeded\",\"message\":\"slow down\"}}}\n\n"
	_, err := parseResponsesStream(strings.NewReader(stream), "responses-test", nil)
	var providerErr *Error
	if !errors.As(err, &providerErr) || providerErr.Kind != ErrorRateLimit || providerErr.Retryable || providerErr.Message != "slow down" {
		t.Fatalf("error=%#v", err)
	}
}

func TestResponsesStreamEmitsReasoningWarningToolAndUsage(t *testing.T) {
	stream := strings.Join([]string{
		`event: response.reasoning_summary_text.delta`, `data: {"type":"response.reasoning_summary_text.delta","delta":"checking"}`, "",
		`event: response.output_item.added`, `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_1","name":"read_file","arguments":""}}`, "",
		`event: response.function_call_arguments.delta`, `data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"path\":\"README.md\"}"}`, "",
		`event: response.incomplete`, `data: {"type":"response.incomplete","response":{"status":"incomplete","output":[{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"README.md\"}"}],"incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":4,"output_tokens":2}}}`, "",
	}, "\n")
	var reasoning, warning string
	var toolEvents, usageEvents int
	response, err := parseResponsesStream(strings.NewReader(stream), "responses-test", func(delta Delta) {
		reasoning += delta.Reasoning
		warning += delta.Warning
		if delta.ToolCall != nil {
			toolEvents++
		}
		if delta.Usage != nil {
			usageEvents++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if reasoning != "checking" || !strings.Contains(warning, "max_output_tokens") || toolEvents < 2 || usageEvents != 1 || len(response.ToolCalls) != 1 || response.Usage != (Usage{InputTokens: 4, OutputTokens: 2}) {
		t.Fatalf("response=%+v reasoning=%q warning=%q toolEvents=%d usageEvents=%d", response, reasoning, warning, toolEvents, usageEvents)
	}
}

func TestBedrockStreamRejectsMissingMessageStop(t *testing.T) {
	stream := bedrockEventStream(t, bedrockEvent{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"partial"}}`})
	_, err := parseBedrockStream(bytes.NewReader(stream), "bedrock-test", "req-1", nil)
	if err == nil || !strings.Contains(err.Error(), "without messageStop") {
		t.Fatalf("error=%v", err)
	}
}

func TestBedrockStreamRejectsOversizedEventAfterMessageStop(t *testing.T) {
	stream := bedrockEventStream(t,
		bedrockEvent{"messageStop", `{"stopReason":"end_turn"}`},
		bedrockEvent{"metadata", strings.Repeat("x", maxBedrockEventMessage)},
	)
	if _, err := parseBedrockStream(bytes.NewReader(stream), "bedrock-test", "req-1", nil); err == nil {
		t.Fatal("oversized event was accepted")
	}
}

func TestBedrockStreamClassifiesInBandFailure(t *testing.T) {
	stream := bedrockExceptionStream(t, "throttlingException", `{"message":"slow down"}`)
	_, err := parseBedrockStream(bytes.NewReader(stream), "bedrock-test", "req-123", nil)
	var providerErr *Error
	if !errors.As(err, &providerErr) || providerErr.Kind != ErrorRateLimit || providerErr.Retryable || providerErr.RequestID != "req-123" || providerErr.Message != "slow down" {
		t.Fatalf("error=%#v", err)
	}
}

func TestBedrockStreamEmitsReasoningToolAndUsage(t *testing.T) {
	stream := bedrockEventStream(t,
		bedrockEvent{"messageStart", `{"role":"assistant"}`},
		bedrockEvent{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"text":"checking"}}}`},
		bedrockEvent{"contentBlockStart", `{"contentBlockIndex":1,"start":{"toolUse":{"toolUseId":"tool_1","name":"read_file"}}}`},
		bedrockEvent{"contentBlockDelta", `{"contentBlockIndex":1,"delta":{"toolUse":{"input":"{\"path\":\"README.md\"}"}}}`},
		bedrockEvent{"contentBlockStop", `{"contentBlockIndex":1}`},
		bedrockEvent{"messageStop", `{"stopReason":"tool_use"}`},
		bedrockEvent{"metadata", `{"usage":{"inputTokens":8,"outputTokens":2,"cacheReadInputTokens":3}}`},
	)
	var reasoning string
	var toolEvents, usageEvents int
	response, err := parseBedrockStream(bytes.NewReader(stream), "bedrock-test", "req-1", func(delta Delta) {
		reasoning += delta.Reasoning
		if delta.ToolCall != nil {
			toolEvents++
		}
		if delta.Usage != nil {
			usageEvents++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if reasoning != "checking" || toolEvents != 3 || usageEvents != 1 || len(response.ToolCalls) != 1 || response.Usage != (Usage{InputTokens: 8, OutputTokens: 2, CachedTokens: 3}) {
		t.Fatalf("response=%+v reasoning=%q toolEvents=%d usageEvents=%d", response, reasoning, toolEvents, usageEvents)
	}
}
