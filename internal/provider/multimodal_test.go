package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func imageFixturePart() ContentPart {
	return ContentPart{Type: ContentImage, Name: "screen.png", MediaType: "image/png", Size: 12, Data: []byte("\x89PNG\r\n\x1a\nimg")}
}

func multimodalRequest() Request {
	return Request{Model: "vision", Messages: []Message{{Role: "user", Content: "Explain this screenshot.", Parts: []ContentPart{imageFixturePart()}}}}
}

func TestOpenAIMultimodalEncodingIsAdditive(t *testing.T) {
	messages, err := openAIMessages(multimodalRequest())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(messages)
	text := string(data)
	for _, want := range []string{`"type":"text"`, `"type":"image_url"`, `data:image/png;base64,`} {
		if !strings.Contains(text, want) {
			t.Fatalf("OpenAI messages missing %q: %s", want, text)
		}
	}
	plain, err := openAIMessages(Request{Messages: []Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(plain)
	if strings.Contains(string(data), "image_url") || !strings.Contains(string(data), `"content":"hello"`) {
		t.Fatalf("text-only request shape changed: %s", data)
	}
}

func TestAnthropicMultimodalEncoding(t *testing.T) {
	messages, err := anthropicMessages(multimodalRequest().Messages)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(messages)
	text := string(data)
	for _, want := range []string{`"type":"image"`, `"type":"base64"`, `"media_type":"image/png"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("Anthropic messages missing %q: %s", want, text)
		}
	}
}

func TestBedrockMultimodalEncoding(t *testing.T) {
	body, err := bedrockRequest(multimodalRequest())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(body)
	text := string(data)
	for _, want := range []string{`"image"`, `"format":"png"`, `"bytes"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("Bedrock request missing %q: %s", want, text)
		}
	}
}

func TestBedrockEmptyToolResultKeepsRequiredContentBlock(t *testing.T) {
	result, err := bedrockToolResult(Message{Role: "tool", ToolCallID: "call-1"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	if !strings.Contains(string(data), `"content":[{"text":""}]`) {
		t.Fatalf("empty tool result lost required content block: %s", data)
	}
}

func TestResponsesMultimodalEncoding(t *testing.T) {
	input, err := responsesInput(multimodalRequest())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(input)
	text := string(data)
	for _, want := range []string{`"type":"input_text"`, `"type":"input_image"`, `data:image/png;base64,`} {
		if !strings.Contains(text, want) {
			t.Fatalf("Responses input missing %q: %s", want, text)
		}
	}
}

func TestImageCapabilityPreflightRejectsKnownTextOnlyProvider(t *testing.T) {
	req := multimodalRequest()
	err := ValidateRequest(Capabilities{ProviderType: "fixture", Model: "text", Images: CapabilityUnsupported}, req)
	if err == nil || !strings.Contains(err.Error(), "does not support image input") {
		t.Fatalf("preflight error=%v", err)
	}
}
