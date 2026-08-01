package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type OpenAIClient struct {
	Label        string
	BaseURL      string
	APIKey       string
	Headers      map[string]string
	ChatURL      string
	APIKeyHeader string
	BearerSource BearerTokenSource
	AuthHint     string
	HTTP         *http.Client
	Declared     Capabilities
	parameters   openAIChatParameterProfile
}

type openAIChatParameterProfile struct {
	mu                     sync.RWMutex
	useMaxCompletionTokens bool
	omitTemperature        bool
	omitReasoningEffort    bool
	// outputCeiling is the largest completion this model accepts, learned from
	// the provider's own rejection of a larger one. Zero means nothing has
	// been learned and the configured value is sent unchanged.
	outputCeiling int
}

const maxOpenAIParameterAdjustments = 3

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
	// The extra fields are what OpenRouter and several gateways publish beside
	// the id, and what this adapter used to discard. A catalog that states a
	// model's own context window is the only source that can be trusted over
	// the configured value, so throwing it away meant every limit downstream
	// was a guess. Endpoints that publish nothing are unaffected: the fields
	// are absent, decode to zero, and ModelInfo.Limits stays empty rather than
	// carrying a number nobody stated.
	var payload struct {
		Data []struct {
			ID            string `json:"id"`
			ContextLength int    `json:"context_length"`
			// LM Studio and a few local gateways use their own spelling on the
			// OpenAI-compatible route.
			MaxContextLength    int `json:"max_context_length"`
			LoadedContextLength int `json:"loaded_context_length"`
			TopProvider         *struct {
				ContextLength       int `json:"context_length"`
				MaxCompletionTokens int `json:"max_completion_tokens"`
			} `json:"top_provider"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, protocolError(c.Label, "list models", err)
	}
	models := make([]ModelInfo, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID == "" {
			continue
		}
		info := ModelInfo{ID: m.ID}
		// A runtime that has loaded a model with a smaller window than the
		// weights allow is serving the smaller one, and that is the number a
		// session actually has to live inside.
		switch {
		case m.LoadedContextLength > 0:
			info.Limits.ContextWindow = m.LoadedContextLength
		case m.ContextLength > 0:
			info.Limits.ContextWindow = m.ContextLength
		case m.MaxContextLength > 0:
			info.Limits.ContextWindow = m.MaxContextLength
		}
		if m.TopProvider != nil {
			if info.Limits.ContextWindow <= 0 {
				info.Limits.ContextWindow = m.TopProvider.ContextLength
			}
			info.Limits.MaxOutput = m.TopProvider.MaxCompletionTokens
		}
		if info.Limits.ContextWindow > 0 {
			info.Limits.ContextSource = LimitsEndpoint
		}
		if info.Limits.MaxOutput > 0 {
			info.Limits.OutputSource = LimitsEndpoint
		}
		models = append(models, info)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

func (c *OpenAIClient) Chat(ctx context.Context, in Request, onDelta func(Delta)) (Response, error) {
	url := c.ChatURL
	if url == "" {
		url = strings.TrimRight(c.BaseURL, "/") + "/chat/completions"
	}
	for adjustments := 0; ; {
		body, err := c.chatBody(in)
		if err != nil {
			return Response{}, err
		}
		resp, err := c.sendChatRequest(ctx, url, body)
		if err != nil {
			return Response{}, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			errorBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
			resp.Body.Close()
			if readErr != nil {
				return Response{}, protocolError(c.Label, "read chat error response", readErr)
			}
			if adjustments < maxOpenAIParameterAdjustments {
				rejected := openAIRejectedChatParameter(resp.StatusCode, errorBody)
				if retry, warning := c.parameters.learn(rejected, body); retry {
					adjustments++
					if warning != "" && onDelta != nil {
						onDelta(Delta{Warning: warning})
					}
					continue
				}
				// Checked after the parameter negotiation, because a provider
				// that rejects the max_tokens *field* must switch spelling
				// before anything can be concluded about its value.
				ceiling := rejectedOutputCeiling(resp.StatusCode, errorBody)
				if retry, warning := c.parameters.learnOutputCeiling(ceiling, body); retry {
					adjustments++
					if warning != "" && onDelta != nil {
						onDelta(Delta{Warning: warning})
					}
					continue
				}
			}
			return Response{}, withAzureRBACHint(responseError(resp, c.Label, "chat", errorBody), c.AuthHint)
		}
		defer resp.Body.Close()
		if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
			response, err := parseOpenAINonStream(resp.Body, onDelta)
			return response, protocolError(c.Label, "decode chat response", err)
		}
		response, err := parseOpenAIStream(resp.Body, onDelta)
		return response, protocolError(c.Label, "read chat stream", err)
	}
}

func (c *OpenAIClient) chatBody(in Request) (map[string]any, error) {
	useMaxCompletionTokens, omitTemperature, omitReasoningEffort, outputCeiling := c.parameters.snapshot()
	messages, err := openAIMessages(in)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"model": in.Model, "messages": messages, "stream": true,
		"stream_options": map[string]bool{"include_usage": true},
	}
	if in.MaxTokens > 0 {
		parameter := "max_tokens"
		if useMaxCompletionTokens {
			parameter = "max_completion_tokens"
		}
		requested := in.MaxTokens
		if outputCeiling > 0 && requested > outputCeiling {
			requested = outputCeiling
		}
		body[parameter] = requested
	}
	if in.Temperature != nil && !omitTemperature {
		body["temperature"] = *in.Temperature
	}
	if in.ReasoningEffort != "" && !omitReasoningEffort {
		body["reasoning_effort"] = in.ReasoningEffort
	}
	if len(in.Tools) > 0 {
		tools := make([]any, 0, len(in.Tools))
		for _, tool := range in.Tools {
			schema, err := toolParameterSchema(tool.Name, tool.InputSchema)
			if err != nil {
				return nil, err
			}
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
				"name": tool.Name, "description": tool.Description, "parameters": schema,
			}})
		}
		body["tools"] = tools
		body["tool_choice"] = "auto"
	}
	return body, nil
}

func (c *OpenAIClient) sendChatRequest(ctx context.Context, url string, body map[string]any) (*http.Response, error) {
	req, err := newJSONRequest(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" && c.APIKeyHeader != "" {
		req.Header.Set(c.APIKeyHeader, c.APIKey)
	} else if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	applyHeaders(req, c.Headers)
	if err := authorizeWithBearerSource(ctx, req.Header, c.BearerSource, c.Label); err != nil {
		return nil, err
	}
	client := c.HTTP
	if client == nil {
		client = httpClient()
	}
	return doWithRetry(client, req, c.Label, "chat")
}

func (p *openAIChatParameterProfile) snapshot() (useMaxCompletionTokens, omitTemperature, omitReasoningEffort bool, outputCeiling int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.useMaxCompletionTokens, p.omitTemperature, p.omitReasoningEffort, p.outputCeiling
}

// learnOutputCeiling records a ceiling the provider named and reports whether
// the request should be retried under it.
//
// This is what makes a configured max_tokens that is too large a one-time
// correction rather than a failed turn. Both halves of this project's limits
// story depend on it: the published-limits table understates on purpose but
// cannot be verified from inside a build, and a hand-written configuration is
// a number somebody remembered. The provider is the only authority on its own
// ceiling, and it states one in the rejection.
func (p *openAIChatParameterProfile) learnOutputCeiling(ceiling int, sent map[string]any) (retry bool, warning string) {
	if ceiling <= 0 {
		return false, ""
	}
	requested := 0
	for _, key := range []string{"max_tokens", "max_completion_tokens"} {
		if value, ok := sent[key].(int); ok {
			requested = value
		}
	}
	if requested <= ceiling {
		// The rejection named a ceiling this request was already under, so
		// retrying would send the same thing and fail the same way.
		return false, ""
	}
	p.mu.Lock()
	if p.outputCeiling == 0 || ceiling < p.outputCeiling {
		p.outputCeiling = ceiling
	}
	p.mu.Unlock()
	return true, fmt.Sprintf("provider rejected max_tokens=%d for this model; retrying at its stated ceiling of %d and remembering that for the active model — set max_tokens in your provider configuration to make it permanent", requested, ceiling)
}

func (p *openAIChatParameterProfile) learn(rejected string, sent map[string]any) (retry bool, warning string) {
	switch rejected {
	case "max_tokens":
		if _, ok := sent["max_tokens"]; !ok {
			return false, ""
		}
		p.mu.Lock()
		p.useMaxCompletionTokens = true
		p.mu.Unlock()
		return true, ""
	case "temperature":
		if _, ok := sent["temperature"]; !ok {
			return false, ""
		}
		p.mu.Lock()
		p.omitTemperature = true
		p.mu.Unlock()
		return true, "provider rejected the configured temperature; retrying with its default and remembering that choice for this active model"
	case "reasoning_effort":
		if _, ok := sent["reasoning_effort"]; !ok {
			return false, ""
		}
		p.mu.Lock()
		p.omitReasoningEffort = true
		p.mu.Unlock()
		return true, "provider or model rejected the configured reasoning effort; retrying with its default and remembering that choice for this active model"
	default:
		return false, ""
	}
}

// openAIRejectedChatParameter recognizes only an explicit HTTP 400 rejection
// of parameters Collomia owns. This keeps successful and unrelated invalid
// requests byte-for-byte unchanged for OpenAI-compatible providers.
func openAIRejectedChatParameter(status int, body []byte) string {
	if status != http.StatusBadRequest {
		return ""
	}
	var envelope struct {
		Message string `json:"message"`
		Error   *struct {
			Message string `json:"message"`
			Param   any    `json:"param"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &envelope)
	message := envelope.Message
	parameter, code := "", ""
	if envelope.Error != nil {
		if envelope.Error.Message != "" {
			message = envelope.Error.Message
		}
		parameter, _ = envelope.Error.Param.(string)
		code, _ = envelope.Error.Code.(string)
	}
	message = strings.ToLower(message)
	parameter = strings.ToLower(strings.TrimSpace(parameter))
	code = strings.ToLower(strings.TrimSpace(code))
	if message == "" {
		message = strings.ToLower(strings.TrimSpace(string(body)))
	}
	unsupportedMessage := strings.Contains(message, "unsupported") || strings.Contains(message, "not supported") ||
		strings.Contains(message, "deprecated") || strings.Contains(message, "only the default")
	switch parameter {
	case "max_tokens":
		if strings.Contains(message, "max_completion_tokens") && (code == "unsupported_parameter" || unsupportedMessage) {
			return "max_tokens"
		}
		return ""
	case "temperature":
		if code == "unsupported_parameter" || unsupportedMessage {
			return "temperature"
		}
		return ""
	case "reasoning_effort":
		invalidValue := strings.Contains(message, "invalid") ||
			strings.Contains(message, "must be") ||
			strings.Contains(message, "expected") ||
			strings.Contains(message, "allowed")
		if code == "unsupported_parameter" || unsupportedMessage || invalidValue {
			return "reasoning_effort"
		}
		return ""
	case "":
		// A few compatible services omit error.param. Their message must still
		// name both the rejected field and its replacement (or explicitly name
		// temperature) before Collomia changes the authored request.
		if unsupportedMessage && strings.Contains(message, "max_tokens") && strings.Contains(message, "max_completion_tokens") {
			return "max_tokens"
		}
		if unsupportedMessage && mentionsQuotedParameter(message, "temperature") {
			return "temperature"
		}
		invalidValue := strings.Contains(message, "invalid") ||
			strings.Contains(message, "must be") ||
			strings.Contains(message, "expected") ||
			strings.Contains(message, "allowed")
		if (unsupportedMessage || invalidValue) && mentionsQuotedParameter(message, "reasoning_effort") {
			return "reasoning_effort"
		}
	}
	return ""
}

// rejectedOutputCeiling extracts the largest completion a provider says it
// will accept, from its own rejection of a larger one.
//
// The message is the only place the number exists — no catalog publishes it —
// and the wording differs per vendor, so this recognizes the shape rather than
// a sentence: a 400 that names an output-token field and carries two numbers,
// of which the smaller is the ceiling. Anthropic writes "max_tokens: 128000 >
// 64000, which is the maximum allowed number of output tokens for <model>";
// OpenAI writes "max_tokens is too large: 128000. This model supports at most
// 16384 completion tokens". Returning zero means nothing was recognized, and
// the caller then fails the turn with the provider's message intact — which is
// the right outcome, because guessing a ceiling would be inventing the fact
// this function exists to read.
func rejectedOutputCeiling(status int, body []byte) int {
	if status != http.StatusBadRequest {
		return 0
	}
	message := strings.ToLower(errorMessageText(body))
	if message == "" {
		return 0
	}
	if !strings.Contains(message, "max_tokens") && !strings.Contains(message, "max_completion_tokens") &&
		!strings.Contains(message, "maxtokens") && !strings.Contains(message, "output tokens") {
		return 0
	}
	// A rejection of the *parameter* is a different negotiation, already
	// handled above. Only a rejection of the value is a ceiling.
	if strings.Contains(message, "unsupported") || strings.Contains(message, "not supported") {
		return 0
	}
	for _, pattern := range outputCeilingPatterns {
		match := pattern.FindStringSubmatch(message)
		if match == nil {
			continue
		}
		if value, err := strconv.Atoi(match[len(match)-1]); err == nil && value > 0 {
			return value
		}
	}
	return 0
}

// outputCeilingPatterns are anchored on the phrasing that carries the number,
// never on "the smallest number in the message".
//
// That distinction is the whole correctness of this: a rejection routinely
// names the model, and `claude-sonnet-4-5-20250929` contributes 4, 5, and a
// date to any scan of the digits in the text. A ceiling of 4 output tokens
// would be learned silently and remembered for the session, which is a far
// worse failure than not recognizing the message at all.
var outputCeilingPatterns = []*regexp.Regexp{
	// Anthropic: "max_tokens: 128000 > 64000, which is the maximum allowed…"
	regexp.MustCompile(`max_?tokens:\s*\d+\s*>\s*(\d+)`),
	// OpenAI: "…This model supports at most 16384 completion tokens"
	regexp.MustCompile(`at most\s+(\d+)`),
	// Assorted gateways: "maximum is 8192", "maximum value of 8192".
	regexp.MustCompile(`maximum(?:\s+\w+)?\s+(?:is|of)\s+(\d+)`),
}

// errorMessageText pulls the human-readable message out of either error
// envelope shape, falling back to the raw body.
func errorMessageText(body []byte) string {
	var envelope struct {
		Message string `json:"message"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &envelope)
	if envelope.Error != nil && envelope.Error.Message != "" {
		return envelope.Error.Message
	}
	if envelope.Message != "" {
		return envelope.Message
	}
	return string(body)
}

func mentionsQuotedParameter(message, parameter string) bool {
	return strings.Contains(message, "'"+parameter+"'") ||
		strings.Contains(message, "`"+parameter+"`") ||
		strings.Contains(message, `"`+parameter+`"`)
}

func openAIMessages(in Request) ([]any, error) {
	messages := make([]any, 0, len(in.Messages)+1)
	if in.System != "" {
		messages = append(messages, map[string]any{"role": "system", "content": in.System})
	}
	for _, msg := range in.Messages {
		var content any = msg.Content
		// Chat Completions supports image_url parts on user input. Its tool
		// message content contract is not consistently multimodal across OpenAI-
		// compatible endpoints, so retain the visible marker text for tool images
		// instead of emitting a request shape many gateways reject.
		if len(msg.Parts) > 0 && msg.Role != "tool" {
			parts, err := messageContentParts(msg)
			if err != nil {
				return nil, err
			}
			encoded := make([]any, 0, len(parts))
			for _, part := range parts {
				switch part.Type {
				case ContentText:
					encoded = append(encoded, map[string]any{"type": "text", "text": part.Text})
				case ContentImage:
					encoded = append(encoded, map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageDataURL(part), "detail": "auto"}})
				}
			}
			content = encoded
		}
		out := map[string]any{"role": msg.Role, "content": content}
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
	return messages, nil
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
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
			Choices []struct {
				Delta struct {
					Content          string            `json:"content"`
					Reasoning        string            `json:"reasoning"`
					ReasoningContent string            `json:"reasoning_content"`
					ToolCalls        []openAIToolDelta `json:"tool_calls"`
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
			return &Error{Kind: streamErrorKind(chunk.Error.Type + " " + chunk.Error.Code), Retryable: false, Message: sanitizeProviderText(chunk.Error.Message, 2048)}
		}
		if chunk.Usage != nil {
			out.Usage = Usage{InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens, CachedTokens: chunk.Usage.PromptDetails.CachedTokens, ReasoningTokens: chunk.Usage.CompletionDetails.ReasoningTokens}
			if onDelta != nil {
				usage := out.Usage
				onDelta(Delta{Usage: &usage})
			}
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				out.Content += choice.Delta.Content
				if onDelta != nil {
					onDelta(Delta{Text: choice.Delta.Content})
				}
			}
			reasoning := choice.Delta.ReasoningContent
			if reasoning == "" {
				reasoning = choice.Delta.Reasoning
			}
			if reasoning != "" && onDelta != nil {
				onDelta(Delta{Reasoning: reasoning})
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
				fragment := openAIArgumentFragment(td.Function.Arguments)
				acc.args.WriteString(fragment)
				if onDelta != nil {
					onDelta(Delta{ToolCall: &ToolCallDelta{Index: td.Index, ID: acc.id, Name: acc.name, Arguments: fragment}})
				}
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
		if onDelta != nil {
			onDelta(Delta{ToolCall: &ToolCallDelta{Index: index, ID: id, Name: acc.name, Done: true}})
		}
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
