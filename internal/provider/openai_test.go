package provider

import (
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

func TestParseOpenAIStreamTextUsageAndToolFragments(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Hi "}}]}`,
		"",
		`data: {"choices":[{"delta":{"content":"there","tool_calls":[{"index":0,"id":"call_1","function":{"name":"read_","arguments":"{\"pa"}}]}}]}`,
		"",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"file","arguments":"th\":\"README.md\"}"}}]},"finish_reason":"tool_calls"}]}`,
		"",
		`data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":4}}`,
		"", `data: [DONE]`, "",
	}, "\n")
	var deltas string
	response, err := parseOpenAIStream(strings.NewReader(stream), func(delta Delta) { deltas += delta.Text })
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "Hi there" || deltas != "Hi there" {
		t.Fatalf("content=%q deltas=%q", response.Content, deltas)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "read_file" || string(response.ToolCalls[0].Arguments) != `{"path":"README.md"}` {
		t.Fatalf("tool calls=%+v", response.ToolCalls)
	}
	if response.Usage.InputTokens != 12 || response.Usage.OutputTokens != 4 {
		t.Fatalf("usage=%+v", response.Usage)
	}
}

func TestAzureOpenAIURL(t *testing.T) {
	p := appconfig.Provider{BaseURL: "https://demo.openai.azure.com", Deployment: "my deployment", APIVersion: "2024-10-21"}
	got, err := azureOpenAIChatURL(p, "ignored")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://demo.openai.azure.com/openai/deployments/my%20deployment/chat/completions?api-version=2024-10-21"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
