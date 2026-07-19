package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ResponsesClient is used by Bedrock Mantle and any other endpoint that
// implements the stateless OpenAI Responses API. Non-streaming is intentional:
// Mantle may run long-lived asynchronous inference while the agent loop still
// receives the exact same tool-call representation as streaming adapters.
type ResponsesClient struct {
	Label    string
	BaseURL  string
	APIKey   string
	Headers  map[string]string
	HTTP     *http.Client
	Declared Capabilities
}

func (c *ResponsesClient) Name() string { return c.Label }

func (c *ResponsesClient) Capabilities() Capabilities {
	if c.Declared.ProviderType != "" {
		return c.Declared
	}
	capabilities, _ := CapabilitiesFor("bedrock-mantle", "", 0)
	return capabilities
}

func (c *ResponsesClient) Chat(ctx context.Context, in Request, onDelta func(Delta)) (Response, error) {
	input := make([]any, 0, len(in.Messages)+1)
	if in.System != "" {
		input = append(input, map[string]any{"role": "system", "content": in.System})
	}
	for _, msg := range in.Messages {
		switch {
		case msg.Role == "tool":
			input = append(input, map[string]any{"type": "function_call_output", "call_id": msg.ToolCallID, "output": msg.Content})
		case len(msg.ToolCalls) > 0:
			if msg.Content != "" {
				input = append(input, map[string]any{"role": "assistant", "content": msg.Content})
			}
			for _, call := range msg.ToolCalls {
				input = append(input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": string(rawObject(call.Arguments))})
			}
		default:
			input = append(input, map[string]any{"role": msg.Role, "content": msg.Content})
		}
	}
	body := map[string]any{"model": in.Model, "input": input, "store": false}
	if in.MaxTokens > 0 {
		body["max_output_tokens"] = in.MaxTokens
	}
	if in.Temperature != nil {
		body["temperature"] = *in.Temperature
	}
	if len(in.Tools) > 0 {
		defs := make([]any, 0, len(in.Tools))
		for _, tool := range in.Tools {
			var schema any
			if err := json.Unmarshal(rawObject(tool.InputSchema), &schema); err != nil {
				return Response{}, err
			}
			defs = append(defs, map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": schema})
		}
		body["tools"] = defs
	}
	req, err := newJSONRequest(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/responses", body)
	if err != nil {
		return Response{}, err
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	applyHeaders(req, c.Headers)
	client := c.HTTP
	if client == nil {
		client = httpClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("%s request: %w", c.Label, err)
	}
	defer resp.Body.Close()
	if err := checkResponse(resp, c.Label); err != nil {
		return Response{}, err
	}
	var payload struct {
		Status string `json:"status"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
		Output []struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			CallID    string          `json:"call_id"`
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Content   []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Response{}, err
	}
	if payload.Error != nil {
		return Response{}, fmt.Errorf("%s: %s", c.Label, payload.Error.Message)
	}
	out := Response{Stop: payload.Status, Usage: Usage{InputTokens: payload.Usage.InputTokens, OutputTokens: payload.Usage.OutputTokens}}
	for _, item := range payload.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Text != "" {
					out.Content += part.Text
				}
				if part.Refusal != "" {
					out.Content += part.Refusal
				}
			}
		case "function_call":
			id := item.CallID
			if id == "" {
				id = item.ID
			}
			args := openAIArgumentFragment(item.Arguments)
			if args == "" {
				args = "{}"
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: id, Name: item.Name, Arguments: json.RawMessage(args)})
		}
	}
	if out.Content != "" && onDelta != nil {
		onDelta(Delta{Text: out.Content})
	}
	return out, nil
}
