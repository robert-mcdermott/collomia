package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

type AnthropicClient struct {
	Label      string
	BaseURL    string
	APIKey     string
	Headers    map[string]string
	BearerAuth bool
	HTTP       *http.Client
	Declared   Capabilities
}

func (c *AnthropicClient) Name() string { return c.Label }

func (c *AnthropicClient) Capabilities() Capabilities {
	if c.Declared.ProviderType != "" {
		return c.Declared
	}
	capabilities, _ := CapabilitiesFor("anthropic-compatible", "", 0)
	return capabilities
}

// ListModels queries GET /v1/models on the Anthropic API.
func (c *AnthropicClient) ListModels(ctx context.Context) ([]ModelInfo, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models?limit=100", nil)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" && c.BearerAuth {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	} else if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Accept", "application/json")
	applyHeaders(req, c.Headers)
	client := c.HTTP
	if client == nil {
		client = httpClient()
	}
	resp, err := doWithRetry(client, req, c.Label, "list models")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp, c.Label, "list models"); err != nil {
		return nil, err
	}
	var payload struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, protocolError(c.Label, "list models", err)
	}
	models := make([]ModelInfo, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID != "" {
			models = append(models, ModelInfo{ID: m.ID, DisplayName: m.DisplayName})
		}
	}
	return models, nil
}

func (c *AnthropicClient) Chat(ctx context.Context, in Request, onDelta func(Delta)) (Response, error) {
	body := map[string]any{
		"model": in.Model, "messages": anthropicMessages(in.Messages),
		"max_tokens": in.MaxTokens, "stream": true,
	}
	if in.System != "" {
		body["system"] = in.System
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
			defs = append(defs, map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": schema})
		}
		body["tools"] = defs
	}
	base := strings.TrimRight(c.BaseURL, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	req, err := newJSONRequest(ctx, http.MethodPost, base+"/messages", body)
	if err != nil {
		return Response{}, err
	}
	if c.APIKey != "" && c.BearerAuth {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	} else if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	applyHeaders(req, c.Headers)
	client := c.HTTP
	if client == nil {
		client = httpClient()
	}
	resp, err := doWithRetry(client, req, c.Label, "chat")
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp, c.Label, "chat"); err != nil {
		return Response{}, err
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		response, err := parseAnthropicNonStream(resp.Body, onDelta)
		return response, protocolError(c.Label, "decode chat response", err)
	}
	response, err := parseAnthropicStream(resp.Body, onDelta)
	return response, protocolError(c.Label, "read chat stream", err)
}

func parseAnthropicNonStream(r interface{ Read([]byte) (int, error) }, onDelta func(Delta)) (Response, error) {
	var payload struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens          int `json:"input_tokens"`
			OutputTokens         int `json:"output_tokens"`
			CacheReadInputTokens int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(r).Decode(&payload); err != nil {
		return Response{}, err
	}
	out := Response{Stop: payload.StopReason, Usage: Usage{InputTokens: payload.Usage.InputTokens, OutputTokens: payload.Usage.OutputTokens, CachedTokens: payload.Usage.CacheReadInputTokens}}
	for _, part := range payload.Content {
		switch part.Type {
		case "text":
			out.Content += part.Text
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: part.ID, Name: part.Name, Arguments: rawObject(part.Input)})
		}
	}
	if out.Content != "" && onDelta != nil {
		onDelta(Delta{Text: out.Content})
	}
	return out, nil
}

func anthropicMessages(messages []Message) []any {
	out := make([]any, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == "tool" {
			out = append(out, map[string]any{"role": "user", "content": []any{map[string]any{
				"type": "tool_result", "tool_use_id": msg.ToolCallID, "content": msg.Content,
			}}})
			continue
		}
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			content := []any{}
			if msg.Content != "" {
				content = append(content, map[string]any{"type": "text", "text": msg.Content})
			}
			for _, call := range msg.ToolCalls {
				var input any
				_ = json.Unmarshal(rawObject(call.Arguments), &input)
				content = append(content, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": input})
			}
			out = append(out, map[string]any{"role": "assistant", "content": content})
			continue
		}
		out = append(out, map[string]any{"role": msg.Role, "content": msg.Content})
	}
	return out
}

func parseAnthropicStream(r interface{ Read([]byte) (int, error) }, onDelta func(Delta)) (Response, error) {
	var out Response
	tools := map[int]*toolAccumulator{}
	err := sseLines(r, func(event, data string) error {
		var envelope struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message *struct {
				Usage struct {
					InputTokens          int `json:"input_tokens"`
					OutputTokens         int `json:"output_tokens"`
					CacheReadInputTokens int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			ContentBlock *struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
				Text  string          `json:"text"`
			} `json:"content_block"`
			Delta *struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage *struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			return fmt.Errorf("decode Anthropic stream event %s: %w", event, err)
		}
		if envelope.Error != nil {
			return &Error{Kind: streamErrorKind(envelope.Error.Type), Retryable: false, Message: sanitizeProviderText(envelope.Error.Message, 2048)}
		}
		if envelope.Message != nil {
			out.Usage.InputTokens = envelope.Message.Usage.InputTokens
			out.Usage.OutputTokens = envelope.Message.Usage.OutputTokens
			out.Usage.CachedTokens = envelope.Message.Usage.CacheReadInputTokens
		}
		if envelope.Usage != nil {
			out.Usage.OutputTokens = envelope.Usage.OutputTokens
		}
		if envelope.ContentBlock != nil {
			switch envelope.ContentBlock.Type {
			case "text":
				if envelope.ContentBlock.Text != "" {
					out.Content += envelope.ContentBlock.Text
					if onDelta != nil {
						onDelta(Delta{Text: envelope.ContentBlock.Text})
					}
				}
			case "tool_use":
				acc := &toolAccumulator{id: envelope.ContentBlock.ID, name: envelope.ContentBlock.Name}
				if len(envelope.ContentBlock.Input) > 0 && string(envelope.ContentBlock.Input) != "{}" {
					acc.args.Write(envelope.ContentBlock.Input)
				}
				tools[envelope.Index] = acc
				if onDelta != nil {
					onDelta(Delta{ToolCall: &ToolCallDelta{Index: envelope.Index, ID: acc.id, Name: acc.name}})
				}
			}
		}
		if envelope.Delta != nil {
			if envelope.Delta.Text != "" {
				out.Content += envelope.Delta.Text
				if onDelta != nil {
					onDelta(Delta{Text: envelope.Delta.Text})
				}
			}
			if envelope.Delta.PartialJSON != "" {
				if tools[envelope.Index] == nil {
					tools[envelope.Index] = &toolAccumulator{}
				}
				tools[envelope.Index].args.WriteString(envelope.Delta.PartialJSON)
				if onDelta != nil {
					onDelta(Delta{ToolCall: &ToolCallDelta{Index: envelope.Index, Arguments: envelope.Delta.PartialJSON}})
				}
			}
			if envelope.Delta.Thinking != "" && onDelta != nil {
				onDelta(Delta{Reasoning: envelope.Delta.Thinking})
			}
			if envelope.Delta.StopReason != "" {
				out.Stop = envelope.Delta.StopReason
			}
		}
		return nil
	})
	if err != nil {
		return Response{}, err
	}
	indexes := make([]int, 0, len(tools))
	for i := range tools {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	for _, i := range indexes {
		acc := tools[i]
		args := acc.args.String()
		if args == "" {
			args = "{}"
		}
		if !json.Valid([]byte(args)) {
			return Response{}, fmt.Errorf("tool %s returned invalid JSON arguments", acc.name)
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: acc.id, Name: acc.name, Arguments: json.RawMessage(args)})
		if onDelta != nil {
			onDelta(Delta{ToolCall: &ToolCallDelta{Index: i, ID: acc.id, Name: acc.name, Done: true}})
		}
	}
	if onDelta != nil && (out.Usage.InputTokens > 0 || out.Usage.OutputTokens > 0) {
		usage := out.Usage
		onDelta(Delta{Usage: &usage})
	}
	return out, nil
}
