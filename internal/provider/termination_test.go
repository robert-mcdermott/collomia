package provider

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestResponseTermination(t *testing.T) {
	for _, tc := range []struct {
		stop string
		want Termination
	}{
		{"", TerminationCompleted}, {"stop", TerminationCompleted}, {"end_turn", TerminationCompleted},
		{"completed", TerminationCompleted}, {"stop_sequence", TerminationCompleted},
		{"length", TerminationTruncated}, {"max_tokens", TerminationTruncated},
		{"max_output_tokens", TerminationTruncated}, {"model_context_window_exceeded", TerminationTruncated},
		{"refusal", TerminationRefused}, {"content_filter", TerminationRefused}, {"guardrail_intervened", TerminationRefused},
		{"failed", TerminationFailed}, {"incomplete", TerminationFailed}, {"pause_turn", TerminationFailed},
		{"cancelled", TerminationFailed}, {"future_unknown", TerminationFailed}, {"in_progress", TerminationFailed},
		{"tool_calls", TerminationFailed}, {"tool_use", TerminationFailed},
	} {
		t.Run(tc.stop, func(t *testing.T) {
			r := Response{Stop: tc.stop, Content: "provider output"}
			if got := r.Termination(); got != tc.want {
				t.Fatalf("termination=%s want=%s", got, tc.want)
			}
			err := r.CompletionError("fixture")
			if tc.want == TerminationCompleted {
				if err != nil {
					t.Fatal(err)
				}
			} else if classified, ok := AsError(err); !ok || classified.Retryable {
				t.Fatalf("expected a non-retryable provider failure: %v", err)
			}
		})
	}
	for _, stop := range []string{"", "tool_calls", "tool_use", "function_call", "completed"} {
		r := Response{Stop: stop, ToolCalls: []ToolCall{{ID: "one", Name: "inspect"}}}
		if r.Termination() != TerminationTools || r.CompletionError("fixture") != nil {
			t.Fatalf("valid tool response rejected: %+v", r)
		}
	}
	if (Response{}).Termination() != TerminationFailed {
		t.Fatal("empty response accepted")
	}
	if !errors.Is((Response{Stop: "incomplete", StopDetail: "max_output_tokens"}).CompletionError("fixture"), ErrResponseTruncated) {
		t.Fatal("lost incomplete reason")
	}
}

func TestRefusalParsing(t *testing.T) {
	for name, parse := range map[string]func() (Response, error){
		"openai sync": func() (Response, error) {
			return parseOpenAINonStream(strings.NewReader(`{"choices":[{"message":{"refusal":"Cannot comply."},"finish_reason":"stop"}]}`), nil)
		},
		"openai stream": func() (Response, error) {
			return parseOpenAIStream(strings.NewReader("data: {\"choices\":[{\"delta\":{\"refusal\":\"Cannot comply.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"), nil)
		},
		"responses sync": func() (Response, error) {
			return parseResponsesResponse(strings.NewReader(`{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"Cannot comply."}]}]}`), "fixture", nil)
		},
		"responses stream": func() (Response, error) {
			return parseResponsesStream(strings.NewReader("data: {\"type\":\"response.refusal.delta\",\"delta\":\"Cannot comply.\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"), "fixture", nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, err := parse()
			if err != nil || r.Content != "Cannot comply." || !errors.Is(r.CompletionError("fixture"), ErrResponseRefused) {
				t.Fatalf("response=%+v err=%v", r, err)
			}
		})
	}
}

func TestStreamsWithoutTerminalStatusRemainIncomplete(t *testing.T) {
	openai, err := parseOpenAIStream(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"Partial\"}}]}\n\n"), nil)
	if err != nil || openai.Content != "Partial" || openai.CompletionError("fixture") == nil {
		t.Fatalf("response=%+v err=%v", openai, err)
	}
	anthropic, err := parseAnthropicStream(strings.NewReader("event: content_block_delta\ndata: {\"delta\":{\"type\":\"text_delta\",\"text\":\"Partial\"}}\n\n"), nil)
	if err != nil || anthropic.Content != "Partial" || anthropic.CompletionError("fixture") == nil {
		t.Fatalf("response=%+v err=%v", anthropic, err)
	}
}

func TestResponsesIncompleteReasonPreserved(t *testing.T) {
	payload := `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","content":[{"type":"output_text","text":"Partial"}]}],"usage":{"output_tokens":10}}`
	for _, streaming := range []bool{false, true} {
		var r Response
		var err error
		if streaming {
			r, err = parseResponsesStream(strings.NewReader("data: {\"type\":\"response.incomplete\",\"response\":"+payload+"}\n\n"), "fixture", nil)
		} else {
			r, err = parseResponsesResponse(strings.NewReader(payload), "fixture", nil)
		}
		if err != nil || r.Content != "Partial" || r.Usage.OutputTokens != 10 || r.Termination() != TerminationTruncated {
			t.Fatalf("stream=%v response=%+v err=%v", streaming, r, err)
		}
	}
}

func TestTruncatedToolArgumentsPreservePartialResponse(t *testing.T) {
	for name, parse := range map[string]func() (Response, error){
		"bedrock": func() (Response, error) {
			payload := bedrockEventStream(t,
				bedrockEvent{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"Partial"}}`},
				bedrockEvent{"contentBlockStart", `{"contentBlockIndex":1,"start":{"toolUse":{"toolUseId":"call","name":"write_file"}}}`},
				bedrockEvent{"contentBlockDelta", `{"contentBlockIndex":1,"delta":{"toolUse":{"input":"{"}}}`},
				bedrockEvent{"messageStop", `{"stopReason":"max_tokens"}`},
				bedrockEvent{"metadata", `{"usage":{"outputTokens":10}}`},
			)
			return parseBedrockStream(bytes.NewReader(payload), "fixture", "", nil)
		},
		"openai": func() (Response, error) {
			return parseOpenAIStream(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"Partial\",\"tool_calls\":[{\"index\":0,\"id\":\"call\",\"function\":{\"name\":\"write_file\",\"arguments\":\"{\"}}]},\"finish_reason\":\"length\"}],\"usage\":{\"completion_tokens\":10}}\n\ndata: [DONE]\n\n"), nil)
		},
		"anthropic": func() (Response, error) {
			return parseAnthropicStream(strings.NewReader("event: content_block_delta\ndata: {\"delta\":{\"text\":\"Partial\"}}\n\nevent: content_block_start\ndata: {\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call\",\"name\":\"write_file\"}}\n\nevent: content_block_delta\ndata: {\"index\":1,\"delta\":{\"partial_json\":\"{\"}}\n\nevent: message_delta\ndata: {\"delta\":{\"stop_reason\":\"max_tokens\"},\"usage\":{\"output_tokens\":10}}\n\n"), nil)
		},
		"responses": func() (Response, error) {
			return parseResponsesResponse(strings.NewReader(`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"function_call","call_id":"call","name":"write_file","arguments":"{"},{"type":"message","content":[{"type":"output_text","text":"Partial"}]}],"usage":{"output_tokens":10}}`), "fixture", nil)
		},
		"responses stream": func() (Response, error) {
			return parseResponsesStream(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"Partial\"}\n\ndata: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"id\":\"call\",\"name\":\"write_file\",\"arguments\":\"{\"}}\n\ndata: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"usage\":{\"output_tokens\":10}}}\n\n"), "fixture", nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, err := parse()
			if err != nil || r.Content != "Partial" || r.Usage.OutputTokens != 10 || len(r.ToolCalls) != 0 || r.Termination() != TerminationTruncated {
				t.Fatalf("partial response lost to tool JSON error: response=%+v err=%v", r, err)
			}
		})
	}
}
