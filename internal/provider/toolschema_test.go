package provider

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

// parameterlessTool is the shape that broke LM Studio: a tool taking no
// arguments, declared the way JSON Schema permits and one gateway does not.
func parameterlessTool() []ToolDefinition {
	return []ToolDefinition{{
		Name: "git_status", Description: "show repository status",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
	}}
}

func TestToolParameterSchemaNamesPropertiesOnAParameterlessTool(t *testing.T) {
	// LM Studio rejects the whole request, not the one tool, with
	// `invalid_type` at [n, "function", "parameters", "properties"] — so a
	// single parameterless tool fails every request in the session, and the
	// only clue to which tool is a numeric index into the sent array.
	decoded, err := toolParameterSchema("git_status", json.RawMessage(`{"type":"object","additionalProperties":false}`))
	if err != nil {
		t.Fatal(err)
	}
	object := decoded.(map[string]any)
	properties, present := object["properties"]
	if !present {
		t.Fatalf("schema still omits properties: %+v", object)
	}
	if len(properties.(map[string]any)) != 0 {
		t.Errorf("a tool with no arguments must declare no properties, got %+v", properties)
	}
	// The rest of the schema has to survive intact: dropping
	// additionalProperties would silently start accepting arguments the tool
	// does not read.
	if object["additionalProperties"] != false || object["type"] != "object" {
		t.Errorf("normalization altered the rest of the schema: %+v", object)
	}
}

func TestToolParameterSchemaLeavesDeclaredSchemasExactlyAsWritten(t *testing.T) {
	// The guard against this change reaching any tool that did not need it.
	// Every provider receives whatever the tool actually declared, byte for
	// byte, so a working Anthropic or Bedrock session cannot be altered by a
	// fix aimed at one OpenAI-compatible server.
	raw := `{"type":"object","properties":{"path":{"type":"string","description":"file"},"limit":{"type":"integer","minimum":1}},"required":["path"],"additionalProperties":false}`
	decoded, err := toolParameterSchema("read_file", json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	var want any
	if err := json.Unmarshal([]byte(raw), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, want) {
		t.Errorf("a declared schema must pass through unchanged:\n got %+v\nwant %+v", decoded, want)
	}
}

func TestToolParameterSchemaLeavesNonObjectSchemasAlone(t *testing.T) {
	// Adding `properties` to a schema that is not an object would be
	// meaningless at best and a lie about the tool's interface at worst.
	decoded, err := toolParameterSchema("odd", json.RawMessage(`{"type":"string"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, present := decoded.(map[string]any)["properties"]; present {
		t.Errorf("non-object schema was given properties: %+v", decoded)
	}
}

func TestToolParameterSchemaStillRejectsMalformedSchemas(t *testing.T) {
	// Normalization must not turn a broken tool definition into a silently
	// empty one; a schema that does not parse is a bug worth surfacing.
	if _, err := toolParameterSchema("broken", json.RawMessage(`{"type":`)); err == nil {
		t.Fatal("malformed schema must still be an error")
	}
}

func TestOpenAISendsPropertiesForAParameterlessTool(t *testing.T) {
	client := &OpenAIClient{Label: "lmstudio", BaseURL: "http://localhost:1234/v1", APIKey: "x"}
	body, err := client.chatBody(Request{
		Model: "m", Messages: []Message{{Role: "user", Content: "hi"}},
		Tools: parameterlessTool(), MaxTokens: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	sent := body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	if _, present := sent["parameters"].(map[string]any)["properties"]; !present {
		t.Errorf("parameters omit properties: %+v", sent["parameters"])
	}
}

func TestBedrockSendsPropertiesForAParameterlessTool(t *testing.T) {
	body, err := bedrockRequest(Request{
		Model: "m", Messages: []Message{{Role: "user", Content: "hi"}},
		Tools: parameterlessTool(), MaxTokens: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := body["toolConfig"].(map[string]any)["tools"].([]any)[0].(map[string]any)["toolSpec"].(map[string]any)
	schema := spec["inputSchema"].(map[string]any)["json"].(map[string]any)
	if _, present := schema["properties"]; !present {
		t.Errorf("inputSchema omits properties: %+v", schema)
	}
}

// captureToolSchema runs one request through a client and returns the tool
// entry it put on the wire, for the two adapters that build their body inside
// Chat rather than in a separately callable function.
func captureToolSchema(t *testing.T, build func(*http.Client) Client, reply string) map[string]any {
	t.Helper()
	var captured map[string]any
	http := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&captured); err != nil {
			return nil, err
		}
		return openAIHTTPResponse(req, 200, "application/json", reply), nil
	})}
	if _, err := build(http).Chat(t.Context(), Request{
		Model: "m", Messages: []Message{{Role: "user", Content: "hi"}},
		Tools: parameterlessTool(), MaxTokens: 16,
	}, func(Delta) {}); err != nil {
		t.Fatal(err)
	}
	return captured
}

func TestAnthropicSendsPropertiesForAParameterlessTool(t *testing.T) {
	body := captureToolSchema(t, func(c *http.Client) Client {
		return &AnthropicClient{Label: "anthropic", BaseURL: "https://example.invalid", APIKey: "secret", HTTP: c}
	}, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	schema := body["tools"].([]any)[0].(map[string]any)["input_schema"].(map[string]any)
	if _, present := schema["properties"]; !present {
		t.Errorf("input_schema omits properties: %+v", schema)
	}
}

func TestResponsesSendsPropertiesForAParameterlessTool(t *testing.T) {
	body := captureToolSchema(t, func(c *http.Client) Client {
		return &ResponsesClient{Label: "responses", BaseURL: "https://example.invalid", APIKey: "secret", HTTP: c}
	}, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	schema := body["tools"].([]any)[0].(map[string]any)["parameters"].(map[string]any)
	if _, present := schema["properties"]; !present {
		t.Errorf("parameters omit properties: %+v", schema)
	}
}

// TestNormalizationDoesNotChangeAnOrdinaryToolOnTheWire is the cross-adapter
// no-harm check: the schema a normal tool declares must reach every provider
// exactly as written, so this fix cannot regress a working configuration.
func TestNormalizationDoesNotChangeAnOrdinaryToolOnTheWire(t *testing.T) {
	raw := `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`
	var want any
	if err := json.Unmarshal([]byte(raw), &want); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}, MaxTokens: 16,
		Tools: []ToolDefinition{{Name: "read_file", InputSchema: json.RawMessage(raw)}},
	}

	openAIBody, err := (&OpenAIClient{Label: "o", BaseURL: "https://example.invalid", APIKey: "x"}).chatBody(request)
	if err != nil {
		t.Fatal(err)
	}
	got := openAIBody["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)["parameters"]
	if !reflect.DeepEqual(got, want) {
		t.Errorf("OpenAI parameters changed:\n got %+v\nwant %+v", got, want)
	}

	bedrockBody, err := bedrockRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	spec := bedrockBody["toolConfig"].(map[string]any)["tools"].([]any)[0].(map[string]any)["toolSpec"].(map[string]any)
	if got := spec["inputSchema"].(map[string]any)["json"]; !reflect.DeepEqual(got, want) {
		t.Errorf("Bedrock inputSchema changed:\n got %+v\nwant %+v", got, want)
	}
}
