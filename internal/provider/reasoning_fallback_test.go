package provider

import (
	"strings"
	"testing"
)

// collectReasoning runs a synchronous parser and returns the readable thinking
// it reported and the answer, in the order reported.
func collectReasoning(t *testing.T, parse func(func(Delta)) (Response, error)) (reasoning, order string) {
	t.Helper()
	var sequence []string
	_, err := parse(func(delta Delta) {
		if delta.Reasoning != "" {
			reasoning += delta.Reasoning
			sequence = append(sequence, "reasoning")
		}
		if delta.Text != "" {
			sequence = append(sequence, "text")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return reasoning, strings.Join(sequence, ",")
}

func TestSynchronousFallbacksReportReadableThinking(t *testing.T) {
	// A streaming response surfaces thinking as it arrives; the synchronous
	// fallbacks used to drop it, so a turn that fell back showed none and kept
	// none.
	cases := map[string]func(func(Delta)) (Response, error){
		"openai reasoning_content": func(on func(Delta)) (Response, error) {
			return parseOpenAINonStream(strings.NewReader(`{"choices":[{"message":{"content":"answer","reasoning_content":"thought"},"finish_reason":"stop"}]}`), on)
		},
		"openai reasoning": func(on func(Delta)) (Response, error) {
			return parseOpenAINonStream(strings.NewReader(`{"choices":[{"message":{"content":"answer","reasoning":"thought"},"finish_reason":"stop"}]}`), on)
		},
		"anthropic": func(on func(Delta)) (Response, error) {
			return parseAnthropicNonStream(strings.NewReader(`{"content":[{"type":"thinking","thinking":"thought","signature":"sig"},{"type":"text","text":"answer"}],"stop_reason":"end_turn"}`), on)
		},
		"responses summary": func(on func(Delta)) (Response, error) {
			return parseResponsesResponse(strings.NewReader(`{"status":"completed","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"thought"}]},{"type":"message","content":[{"type":"output_text","text":"answer"}]}]}`), "test", on)
		},
		"bedrock": func(on func(Delta)) (Response, error) {
			return parseBedrockResponse(strings.NewReader(`{"output":{"message":{"content":[{"reasoningContent":{"reasoningText":{"text":"thought","signature":"s"}}},{"text":"answer"}]}},"stopReason":"end_turn"}`), on)
		},
	}
	for name, parse := range cases {
		reasoning, order := collectReasoning(t, parse)
		if reasoning != "thought" || order != "reasoning,text" {
			t.Errorf("%s: reasoning %q order %q, want the thinking reported ahead of the answer", name, reasoning, order)
		}
	}
}

func TestCapabilitiesSeparateReasoningConcerns(t *testing.T) {
	openai, _ := CapabilitiesFor("openai", "gpt-5", 0)
	if openai.ReasoningSummaries != CapabilityUnsupported || openai.ReasoningContinuity != CapabilityUnsupported || openai.Reasoning != CapabilityPartial {
		t.Errorf("openai = %+v", openai)
	}
	anthropic, _ := CapabilitiesFor("anthropic", "claude-opus-4-6", 0)
	if anthropic.ReasoningSummaries != CapabilityPartial || anthropic.ReasoningContinuity != CapabilitySupported {
		t.Errorf("anthropic = %+v", anthropic)
	}
	if summary := anthropic.DetailSummary(); !strings.Contains(summary, "reasoning continuity") || !strings.Contains(summary, "reasoning summaries") {
		t.Errorf("/models must describe each reasoning concern separately: %q", summary)
	}
}
