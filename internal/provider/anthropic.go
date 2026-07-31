package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
)

type AnthropicClient struct {
	Label        string
	BaseURL      string
	APIKey       string
	Headers      map[string]string
	BearerAuth   bool
	BearerSource BearerTokenSource
	AuthHint     string
	HTTP         *http.Client
	Declared     Capabilities
	caching      anthropicCacheProfile
}

// anthropicCacheProfile remembers an endpoint's refusal of cache_control for
// the life of the client. Renegotiating per request would spend a rejected
// round trip on every call to a compatible endpoint that does not implement
// caching, which is the opposite of what the feature is for.
type anthropicCacheProfile struct {
	mu      sync.RWMutex
	refused bool
}

func (p *anthropicCacheProfile) enabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return !p.refused
}

func (p *anthropicCacheProfile) refuse() {
	p.mu.Lock()
	p.refused = true
	p.mu.Unlock()
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
	if err := authorizeWithBearerSource(ctx, req.Header, c.BearerSource, c.Label); err != nil {
		return nil, err
	}
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
		return nil, withAzureRBACHint(err, c.AuthHint)
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
	messages, err := anthropicMessages(in.Messages)
	if err != nil {
		return Response{}, err
	}
	body := map[string]any{
		"model": in.Model, "messages": messages,
		"max_tokens": in.MaxTokens, "stream": true,
	}
	if in.System != "" {
		body["system"] = in.System
	}
	if in.Temperature != nil {
		body["temperature"] = *in.Temperature
	}
	if in.ReasoningEffort != "" {
		body["output_config"] = map[string]any{"effort": in.ReasoningEffort}
	}
	if len(in.Tools) > 0 {
		defs := make([]any, 0, len(in.Tools))
		for _, tool := range in.Tools {
			schema, err := toolParameterSchema(tool.Name, tool.InputSchema)
			if err != nil {
				return Response{}, err
			}
			defs = append(defs, map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": schema})
		}
		body["tools"] = defs
	}
	caching := c.caching.enabled()
	if caching {
		anthropicApplyCaching(body, in.Messages, messages)
	}
	base := strings.TrimRight(c.BaseURL, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	reasoningRetried, cachingRetried := false, false
	for {
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
		if err := authorizeWithBearerSource(ctx, req.Header, c.BearerSource, c.Label); err != nil {
			return Response{}, err
		}
		client := c.HTTP
		if client == nil {
			client = httpClient()
		}
		resp, err := doWithRetry(client, req, c.Label, "chat")
		if err != nil {
			return Response{}, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			errorBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
			resp.Body.Close()
			if readErr != nil {
				return Response{}, protocolError(c.Label, "read chat error response", readErr)
			}
			if !reasoningRetried && in.ReasoningEffort != "" && anthropicRejectedReasoning(resp.StatusCode, errorBody) {
				reasoningRetried = true
				delete(body, "output_config")
				if onDelta != nil {
					onDelta(Delta{Warning: "provider or model rejected the configured reasoning effort; retrying with its default for this request"})
				}
				continue
			}
			// A compatible endpoint that does not implement prompt caching
			// must not lose the request over it. The refusal is remembered
			// on the client so the wasted round trip happens once rather
			// than on every later call.
			if !cachingRetried && caching && anthropicRejectedCaching(resp.StatusCode, errorBody) {
				cachingRetried = true
				caching = false
				c.caching.refuse()
				plain, plainErr := anthropicMessages(in.Messages)
				if plainErr != nil {
					return Response{}, plainErr
				}
				body["messages"] = plain
				if in.System != "" {
					body["system"] = in.System
				}
				if defs, ok := body["tools"].([]any); ok && len(defs) > 0 {
					if last, ok := defs[len(defs)-1].(map[string]any); ok {
						delete(last, "cache_control")
					}
				}
				if onDelta != nil {
					onDelta(Delta{Warning: "endpoint rejected prompt cache breakpoints; retrying without caching and not attempting it again for this provider"})
				}
				continue
			}
			return Response{}, withAzureRBACHint(responseError(resp, c.Label, "chat", errorBody), c.AuthHint)
		}
		defer resp.Body.Close()
		if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
			response, err := parseAnthropicNonStream(resp.Body, onDelta)
			return response, protocolError(c.Label, "decode chat response", err)
		}
		response, err := parseAnthropicStream(resp.Body, onDelta)
		return response, protocolError(c.Label, "read chat stream", err)
	}
}

// anthropicUsage normalizes Anthropic's split token counters to the Usage
// contract, where InputTokens is the whole prompt.
//
// Anthropic reports input_tokens net of both cache counters: a warm request
// can bill twenty thousand prompt tokens and report input_tokens in the
// hundreds. Passing that through would price the cached remainder at nothing
// and make the context gauge read near-empty exactly when the context is
// fullest.
func anthropicUsage(input, output, cacheRead, cacheWrite int) Usage {
	return Usage{
		InputTokens:      input + cacheRead + cacheWrite,
		OutputTokens:     output,
		CachedTokens:     cacheRead,
		CacheWriteTokens: cacheWrite,
	}
}

// anthropicEphemeral is the cache breakpoint marker.
//
// The default five-minute lifetime is used deliberately rather than the
// one-hour extension: the longer TTL is requested through a beta header, and
// sending an unrecognized beta header to a compatible endpoint is a
// compatibility risk taken on behalf of a saving that has not been measured
// here. A tool call refreshes the entry, so an active loop keeps the prefix
// warm; a session resumed after a long pause pays one full-price request.
func anthropicEphemeral() map[string]any { return map[string]any{"type": "ephemeral"} }

// anthropicApplyCaching places the request's cache breakpoints.
//
// Two, out of the four Anthropic permits, because they are the two that pay
// for themselves in an agent loop:
//
//   - One at the end of the stable prefix. The request is ordered tools,
//     system, messages, so a breakpoint on the system block also covers every
//     tool definition ahead of it; with no system prompt it goes on the last
//     tool instead. This region does not change for the life of a session, so
//     it is written once and read by every later request.
//   - One rolling breakpoint at the end of the conversation, so the next
//     request in the loop reads this one's history instead of paying for it
//     again. A turn with ten tool calls is eleven requests over the same
//     growing prefix, which is where nearly all of the cost is.
//
// The rolling breakpoint must sit before any volatile trailing message.
// Writing a prefix that includes content regenerated on every request would
// produce an entry that is never read back — cache writes cost more than
// ordinary input, so that is strictly worse than not caching at all.
func anthropicApplyCaching(body map[string]any, source []Message, encoded []any) {
	if system, ok := body["system"].(string); ok && system != "" {
		body["system"] = []any{map[string]any{"type": "text", "text": system, "cache_control": anthropicEphemeral()}}
	} else if defs, ok := body["tools"].([]any); ok && len(defs) > 0 {
		if last, ok := defs[len(defs)-1].(map[string]any); ok {
			last["cache_control"] = anthropicEphemeral()
		}
	}
	// anthropicMessages emits exactly one entry per input message, which is
	// what lets a breakpoint be chosen by index. If that ever stops being
	// true, skip the conversation breakpoint rather than mark the wrong
	// message: a misplaced breakpoint is a silent cost, not a visible error.
	if len(encoded) != len(source) {
		return
	}
	for i := len(source) - 1; i >= 0; i-- {
		if source[i].Volatile {
			continue
		}
		anthropicMarkCacheBreakpoint(encoded[i])
		return
	}
}

// anthropicMarkCacheBreakpoint attaches cache_control to a message's last
// content block, promoting plain string content to a block first. An empty
// message is left alone: an empty text block is rejected by the API, and
// losing the request to a caching optimization is not a trade worth making.
func anthropicMarkCacheBreakpoint(entry any) {
	msg, ok := entry.(map[string]any)
	if !ok {
		return
	}
	switch content := msg["content"].(type) {
	case string:
		if content == "" {
			return
		}
		msg["content"] = []any{map[string]any{"type": "text", "text": content, "cache_control": anthropicEphemeral()}}
	case []any:
		if len(content) == 0 {
			return
		}
		if last, ok := content[len(content)-1].(map[string]any); ok {
			last["cache_control"] = anthropicEphemeral()
		}
	}
}

// anthropicRejectedCaching keys on the field name alone. Compatible endpoints
// word their rejections differently, and any 400 naming cache_control is
// about this request's breakpoints whatever the surrounding prose says.
func anthropicRejectedCaching(status int, body []byte) bool {
	if status != http.StatusBadRequest {
		return false
	}
	message := strings.ToLower(string(body))
	return strings.Contains(message, "cache_control") || strings.Contains(message, "cache control") ||
		strings.Contains(message, "cachecontrol") || strings.Contains(message, "prompt caching")
}

func anthropicRejectedReasoning(status int, body []byte) bool {
	if status != http.StatusBadRequest {
		return false
	}
	message := strings.ToLower(string(body))
	rejected := strings.Contains(message, "unsupported") || strings.Contains(message, "not supported") ||
		strings.Contains(message, "unrecognized") || strings.Contains(message, "extra inputs") ||
		strings.Contains(message, "invalid") || strings.Contains(message, "must be") ||
		strings.Contains(message, "expected")
	return rejected && (strings.Contains(message, "output_config") || strings.Contains(message, "effort"))
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
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(r).Decode(&payload); err != nil {
		return Response{}, err
	}
	out := Response{Stop: payload.StopReason, Usage: anthropicUsage(payload.Usage.InputTokens, payload.Usage.OutputTokens, payload.Usage.CacheReadInputTokens, payload.Usage.CacheCreationInputTokens)}
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

func anthropicMessages(messages []Message) ([]any, error) {
	out := make([]any, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == "tool" {
			resultContent := any(msg.Content)
			if len(msg.Parts) > 0 {
				parts, err := anthropicContentParts(msg)
				if err != nil {
					return nil, err
				}
				resultContent = parts
			}
			out = append(out, map[string]any{"role": "user", "content": []any{map[string]any{
				"type": "tool_result", "tool_use_id": msg.ToolCallID, "content": resultContent,
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
		if len(msg.Parts) > 0 {
			parts, err := anthropicContentParts(msg)
			if err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"role": msg.Role, "content": parts})
		} else {
			out = append(out, map[string]any{"role": msg.Role, "content": msg.Content})
		}
	}
	return out, nil
}

func anthropicContentParts(message Message) ([]any, error) {
	parts, err := messageContentParts(message)
	if err != nil {
		return nil, err
	}
	encoded := make([]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case ContentText:
			encoded = append(encoded, map[string]any{"type": "text", "text": part.Text})
		case ContentImage:
			encoded = append(encoded, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": part.MediaType, "data": imageBase64(part)}})
		}
	}
	return encoded, nil
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
					InputTokens              int `json:"input_tokens"`
					OutputTokens             int `json:"output_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
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
			usage := anthropicUsage(envelope.Message.Usage.InputTokens, envelope.Message.Usage.OutputTokens,
				envelope.Message.Usage.CacheReadInputTokens, envelope.Message.Usage.CacheCreationInputTokens)
			out.Usage.InputTokens = usage.InputTokens
			out.Usage.OutputTokens = usage.OutputTokens
			out.Usage.CachedTokens = usage.CachedTokens
			out.Usage.CacheWriteTokens = usage.CacheWriteTokens
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
