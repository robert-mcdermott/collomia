package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

type OpenAIClient struct {
	Label        string
	BaseURL      string
	APIKey       string
	Headers      map[string]string
	ChatURL      string
	APIKeyHeader string
	HTTP         *http.Client
	Declared     Capabilities
}

func (c *OpenAIClient) Name() string { return c.Label }

func (c *OpenAIClient) Capabilities() Capabilities {
	if c.Declared.ProviderType != "" {
		return c.Declared
	}
	capabilities, _ := CapabilitiesFor("openai-compatible", "", 0)
	return capabilities
}

// ListModels queries GET /models, which OpenAI, Ollama, vLLM, LM Studio,
// and most compatible gateways implement.
func (c *OpenAIClient) ListModels(ctx context.Context) ([]ModelInfo, error) {
	url := strings.TrimRight(c.BaseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.APIKey != "" && c.APIKeyHeader != "" {
		req.Header.Set(c.APIKeyHeader, c.APIKey)
	} else if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	applyHeaders(req, c.Headers)
	client := c.HTTP
	if client == nil {
		client = httpClient()
	}
	resp, err := doWithRetry(client, req, c.Label)
	if err != nil {
		return nil, fmt.Errorf("%s models: %w", c.Label, err)
	}
	defer resp.Body.Close()
	if err := checkResponse(resp, c.Label); err != nil {
		return nil, err
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	models := make([]ModelInfo, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID != "" {
			models = append(models, ModelInfo{ID: m.ID})
		}
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

func (c *OpenAIClient) Chat(ctx context.Context, in Request, onDelta func(Delta)) (Response, error) {
	body := map[string]any{
		"model": in.Model, "messages": openAIMessages(in), "stream": true,
		"stream_options": map[string]bool{"include_usage": true},
	}
	if in.MaxTokens > 0 {
		body["max_tokens"] = in.MaxTokens
	}
	if in.Temperature != nil {
		body["temperature"] = *in.Temperature
	}
	if len(in.Tools) > 0 {
		tools := make([]any, 0, len(in.Tools))
		for _, tool := range in.Tools {
			var schema any
			if err := json.Unmarshal(rawObject(tool.InputSchema), &schema); err != nil {
				return Response{}, fmt.Errorf("tool %s schema: %w", tool.Name, err)
			}
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
				"name": tool.Name, "description": tool.Description, "parameters": schema,
			}})
		}
		body["tools"] = tools
		body["tool_choice"] = "auto"
	}

	url := c.ChatURL
	if url == "" {
		url = strings.TrimRight(c.BaseURL, "/") + "/chat/completions"
	}
	req, err := newJSONRequest(ctx, http.MethodPost, url, body)
	if err != nil {
		return Response{}, err
	}
	if c.APIKey != "" && c.APIKeyHeader != "" {
		req.Header.Set(c.APIKeyHeader, c.APIKey)
	} else if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	applyHeaders(req, c.Headers)
	client := c.HTTP
	if client == nil {
		client = httpClient()
	}
	resp, err := doWithRetry(client, req, c.Label)
	if err != nil {
		return Response{}, fmt.Errorf("%s request: %w", c.Label, err)
	}
	defer resp.Body.Close()
	if err := checkResponse(resp, c.Label); err != nil {
		return Response{}, err
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		return parseOpenAINonStream(resp.Body, onDelta)
	}
	return parseOpenAIStream(resp.Body, onDelta)
}

func openAIMessages(in Request) []any {
	messages := make([]any, 0, len(in.Messages)+1)
	if in.System != "" {
		messages = append(messages, map[string]any{"role": "system", "content": in.System})
	}
	for _, msg := range in.Messages {
		out := map[string]any{"role": msg.Role, "content": msg.Content}
		if msg.ToolCallID != "" {
			out["tool_call_id"] = msg.ToolCallID
		}
		if len(msg.ToolCalls) > 0 {
			calls := make([]any, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				calls = append(calls, map[string]any{
					"id": call.ID, "type": "function",
					"function": map[string]any{"name": call.Name, "arguments": string(rawObject(call.Arguments))},
				})
			}
			out["tool_calls"] = calls
		}
		messages = append(messages, out)
	}
	return messages
}

type openAIToolDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type toolAccumulator struct {
	id, name string
	args     strings.Builder
}

func parseOpenAIStream(r io.Reader, onDelta func(Delta)) (Response, error) {
	var out Response
	tools := map[int]*toolAccumulator{}
	err := sseLines(r, func(_ string, data string) error {
		if data == "[DONE]" {
			return nil
		}
		var chunk struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			Choices []struct {
				Delta struct {
					Content   string            `json:"content"`
					ToolCalls []openAIToolDelta `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				PromptDetails    struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
				CompletionDetails struct {
					ReasoningTokens int `json:"reasoning_tokens"`
				} `json:"completion_tokens_details"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("decode OpenAI stream event: %w", err)
		}
		if chunk.Error != nil {
			return fmt.Errorf("OpenAI stream: %s", chunk.Error.Message)
		}
		if chunk.Usage != nil {
			out.Usage = Usage{InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens, CachedTokens: chunk.Usage.PromptDetails.CachedTokens, ReasoningTokens: chunk.Usage.CompletionDetails.ReasoningTokens}
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				out.Content += choice.Delta.Content
				if onDelta != nil {
					onDelta(Delta{Text: choice.Delta.Content})
				}
			}
			if choice.FinishReason != "" {
				out.Stop = choice.FinishReason
			}
			for _, td := range choice.Delta.ToolCalls {
				acc := tools[td.Index]
				if acc == nil {
					acc = &toolAccumulator{}
					tools[td.Index] = acc
				}
				if td.ID != "" {
					acc.id = td.ID
				}
				if td.Function.Name != "" {
					acc.name += td.Function.Name
				}
				acc.args.WriteString(openAIArgumentFragment(td.Function.Arguments))
			}
		}
		return nil
	})
	if err != nil {
		return Response{}, err
	}
	indexes := make([]int, 0, len(tools))
	for index := range tools {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		acc := tools[index]
		args := strings.TrimSpace(acc.args.String())
		if args == "" {
			args = "{}"
		}
		if !json.Valid([]byte(args)) {
			return Response{}, fmt.Errorf("tool %s returned invalid JSON arguments: %s", acc.name, args)
		}
		id := acc.id
		if id == "" {
			id = "call_" + strconv.Itoa(index)
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: id, Name: acc.name, Arguments: json.RawMessage(args)})
	}
	return out, nil
}

func openAIArgumentFragment(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return string(raw)
}

func parseOpenAINonStream(r io.Reader, onDelta func(Delta)) (Response, error) {
	var payload struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(r).Decode(&payload); err != nil {
		return Response{}, err
	}
	out := Response{Usage: Usage{InputTokens: payload.Usage.PromptTokens, OutputTokens: payload.Usage.CompletionTokens}}
	if len(payload.Choices) == 0 {
		return out, nil
	}
	choice := payload.Choices[0]
	out.Content, out.Stop = choice.Message.Content, choice.FinishReason
	if out.Content != "" && onDelta != nil {
		onDelta(Delta{Text: out.Content})
	}
	for i, tc := range choice.Message.ToolCalls {
		id := tc.ID
		if id == "" {
			id = "call_" + strconv.Itoa(i)
		}
		args := openAIArgumentFragment(tc.Function.Arguments)
		if args == "" {
			args = "{}"
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: id, Name: tc.Function.Name, Arguments: json.RawMessage(args)})
	}
	return out, nil
}
