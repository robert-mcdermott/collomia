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
}

func (c *AnthropicClient) Name() string { return c.Label }

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
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("%s request: %w", c.Label, err)
	}
	defer resp.Body.Close()
	if err := checkResponse(resp, c.Label); err != nil {
		return Response{}, err
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		return parseAnthropicNonStream(resp.Body, onDelta)
	}
	return parseAnthropicStream(resp.Body, onDelta)
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
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(r).Decode(&payload); err != nil {
		return Response{}, err
	}
	out := Response{Stop: payload.StopReason, Usage: Usage{InputTokens: payload.Usage.InputTokens, OutputTokens: payload.Usage.OutputTokens}}
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
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
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
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage *struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			return fmt.Errorf("decode Anthropic stream event %s: %w", event, err)
		}
		if envelope.Error != nil {
			return fmt.Errorf("Anthropic stream: %s", envelope.Error.Message)
		}
		if envelope.Message != nil {
			out.Usage.InputTokens = envelope.Message.Usage.InputTokens
			out.Usage.OutputTokens = envelope.Message.Usage.OutputTokens
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
	}
	return out, nil
}
