package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// ResponsesClient is used by Bedrock Mantle and any other endpoint that
// implements the stateless OpenAI Responses API. The adapter requests SSE but
// still accepts a JSON response from compatible endpoints that complete
// synchronously or ignore the streaming preference.
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
	input, err := responsesInput(in)
	if err != nil {
		return Response{}, err
	}
	body := map[string]any{"model": in.Model, "input": input, "store": false, "stream": true}
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
	resp, err := doWithRetry(client, req, c.Label, "responses")
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp, c.Label, "responses"); err != nil {
		return Response{}, err
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		out, err := parseResponsesStream(resp.Body, c.Label, onDelta)
		return out, protocolError(c.Label, "read responses stream", err)
	}
	out, err := parseResponsesResponse(resp.Body, c.Label, onDelta)
	return out, protocolError(c.Label, "decode responses response", err)
}

func responsesInput(in Request) ([]any, error) {
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
			if len(msg.Parts) == 0 {
				input = append(input, map[string]any{"role": msg.Role, "content": msg.Content})
				continue
			}
			parts, err := messageContentParts(msg)
			if err != nil {
				return nil, err
			}
			content := make([]any, 0, len(parts))
			for _, part := range parts {
				switch part.Type {
				case ContentText:
					content = append(content, map[string]any{"type": "input_text", "text": part.Text})
				case ContentImage:
					content = append(content, map[string]any{"type": "input_image", "image_url": imageDataURL(part)})
				}
			}
			input = append(input, map[string]any{"role": msg.Role, "content": content})
		}
	}
	return input, nil
}

type responsesErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type responsesContent struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

type responsesItem struct {
	Type      string             `json:"type"`
	ID        string             `json:"id"`
	CallID    string             `json:"call_id"`
	Name      string             `json:"name"`
	Arguments json.RawMessage    `json:"arguments"`
	Content   []responsesContent `json:"content"`
}

type responsesPayload struct {
	Status            string                 `json:"status"`
	Error             *responsesErrorPayload `json:"error"`
	Output            []responsesItem        `json:"output"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Usage struct {
		InputTokens        int `json:"input_tokens"`
		OutputTokens       int `json:"output_tokens"`
		InputTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		OutputTokensDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	} `json:"usage"`
}

func parseResponsesResponse(r io.Reader, label string, onDelta func(Delta)) (Response, error) {
	var payload responsesPayload
	if err := json.NewDecoder(r).Decode(&payload); err != nil {
		return Response{}, err
	}
	return responseFromPayload(payload, label, "responses", onDelta)
}

func responseFromPayload(payload responsesPayload, label, operation string, onDelta func(Delta)) (Response, error) {
	if payload.Error != nil {
		return Response{}, responsesStreamError(label, operation, *payload.Error)
	}
	out := Response{Stop: payload.Status, Usage: Usage{
		InputTokens: payload.Usage.InputTokens, OutputTokens: payload.Usage.OutputTokens,
		CachedTokens: payload.Usage.InputTokensDetails.CachedTokens, ReasoningTokens: payload.Usage.OutputTokensDetails.ReasoningTokens,
	}}
	for _, item := range payload.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				out.Content += part.Text + part.Refusal
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
			if !json.Valid([]byte(args)) {
				return Response{}, fmt.Errorf("tool %s returned invalid JSON arguments", item.Name)
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: id, Name: item.Name, Arguments: json.RawMessage(args)})
		}
	}
	if out.Content != "" && onDelta != nil {
		onDelta(Delta{Text: out.Content})
	}
	return out, nil
}

type responsesToolAccumulator struct {
	id, name string
	args     strings.Builder
}

func parseResponsesStream(r io.Reader, label string, onDelta func(Delta)) (Response, error) {
	var out Response
	tools := map[int]*responsesToolAccumulator{}
	itemIndexes := map[string]int{}
	terminal := false
	err := sseLines(r, func(eventName, data string) error {
		if data == "[DONE]" {
			terminal = true
			return nil
		}
		var envelope struct {
			Type        string                 `json:"type"`
			Delta       string                 `json:"delta"`
			ItemID      string                 `json:"item_id"`
			OutputIndex int                    `json:"output_index"`
			Item        *responsesItem         `json:"item"`
			Response    *responsesPayload      `json:"response"`
			Error       *responsesErrorPayload `json:"error"`
			Code        string                 `json:"code"`
			Message     string                 `json:"message"`
		}
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			return fmt.Errorf("decode Responses stream event %s: %w", eventName, err)
		}
		typ := envelope.Type
		if typ == "" {
			typ = eventName
		}
		index := envelope.OutputIndex
		if envelope.ItemID != "" {
			if known, ok := itemIndexes[envelope.ItemID]; ok {
				index = known
			} else {
				itemIndexes[envelope.ItemID] = index
			}
		}
		switch typ {
		case "response.output_text.delta", "response.refusal.delta":
			out.Content += envelope.Delta
			if envelope.Delta != "" && onDelta != nil {
				onDelta(Delta{Text: envelope.Delta})
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if envelope.Delta != "" && onDelta != nil {
				onDelta(Delta{Reasoning: envelope.Delta})
			}
		case "response.output_item.added":
			if envelope.Item != nil && envelope.Item.Type == "function_call" {
				acc := responsesTool(tools, index)
				acc.id, acc.name = responseToolID(*envelope.Item), envelope.Item.Name
				fragment := openAIArgumentFragment(envelope.Item.Arguments)
				acc.args.WriteString(fragment)
				if envelope.Item.ID != "" {
					itemIndexes[envelope.Item.ID] = index
				}
				if onDelta != nil {
					onDelta(Delta{ToolCall: &ToolCallDelta{Index: index, ID: acc.id, Name: acc.name, Arguments: fragment}})
				}
			}
		case "response.function_call_arguments.delta":
			acc := responsesTool(tools, index)
			acc.args.WriteString(envelope.Delta)
			if onDelta != nil {
				onDelta(Delta{ToolCall: &ToolCallDelta{Index: index, ID: acc.id, Name: acc.name, Arguments: envelope.Delta}})
			}
		case "response.output_item.done":
			if envelope.Item != nil && envelope.Item.Type == "function_call" {
				acc := responsesTool(tools, index)
				acc.id, acc.name = responseToolID(*envelope.Item), envelope.Item.Name
				if args := openAIArgumentFragment(envelope.Item.Arguments); args != "" {
					acc.args.Reset()
					acc.args.WriteString(args)
				}
			}
		case "response.completed", "response.incomplete":
			terminal = true
			if envelope.Response == nil {
				return fmt.Errorf("Responses stream reported %s without a response", typ)
			}
			complete, err := responseFromPayload(*envelope.Response, label, "responses stream", nil)
			if err != nil {
				return err
			}
			if err := reconcileStreamedText(&out, complete.Content, onDelta); err != nil {
				return err
			}
			out.Stop, out.Usage = complete.Stop, complete.Usage
			if len(complete.ToolCalls) > 0 {
				out.ToolCalls = complete.ToolCalls
			}
			if typ == "response.incomplete" && onDelta != nil {
				reason := ""
				if envelope.Response.IncompleteDetails != nil {
					reason = strings.TrimSpace(envelope.Response.IncompleteDetails.Reason)
				}
				warning := "provider response was incomplete"
				if reason != "" {
					warning += ": " + reason
				}
				onDelta(Delta{Warning: warning})
			}
		case "response.failed", "error":
			terminal = true
			failure := envelope.Error
			if failure == nil && envelope.Response != nil {
				failure = envelope.Response.Error
			}
			if failure == nil && (envelope.Code != "" || envelope.Message != "") {
				failure = &responsesErrorPayload{Code: envelope.Code, Message: envelope.Message}
			}
			if failure == nil {
				return fmt.Errorf("Responses stream reported %s without error detail", typ)
			}
			return responsesStreamError(label, "responses stream", *failure)
		}
		return nil
	})
	if err != nil {
		return Response{}, err
	}
	if !terminal {
		return Response{}, fmt.Errorf("Responses stream ended without a terminal event")
	}
	if len(out.ToolCalls) == 0 {
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
				return Response{}, fmt.Errorf("tool %s returned invalid JSON arguments", acc.name)
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: acc.id, Name: acc.name, Arguments: json.RawMessage(args)})
		}
	}
	if onDelta != nil {
		if len(tools) == 0 {
			for index, call := range out.ToolCalls {
				onDelta(Delta{ToolCall: &ToolCallDelta{Index: index, ID: call.ID, Name: call.Name, Done: true}})
			}
		} else {
			indexes := make([]int, 0, len(tools))
			for index := range tools {
				indexes = append(indexes, index)
			}
			sort.Ints(indexes)
			for _, index := range indexes {
				acc := tools[index]
				onDelta(Delta{ToolCall: &ToolCallDelta{Index: index, ID: acc.id, Name: acc.name, Done: true}})
			}
		}
	}
	if onDelta != nil && (out.Usage.InputTokens > 0 || out.Usage.OutputTokens > 0) {
		usage := out.Usage
		onDelta(Delta{Usage: &usage})
	}
	return out, nil
}

func responsesTool(tools map[int]*responsesToolAccumulator, index int) *responsesToolAccumulator {
	acc := tools[index]
	if acc == nil {
		acc = &responsesToolAccumulator{}
		tools[index] = acc
	}
	return acc
}

func responseToolID(item responsesItem) string {
	if item.CallID != "" {
		return item.CallID
	}
	return item.ID
}

func reconcileStreamedText(out *Response, complete string, onDelta func(Delta)) error {
	if complete == "" || complete == out.Content {
		return nil
	}
	if strings.HasPrefix(complete, out.Content) {
		suffix := strings.TrimPrefix(complete, out.Content)
		out.Content = complete
		if suffix != "" && onDelta != nil {
			onDelta(Delta{Text: suffix})
		}
		return nil
	}
	return fmt.Errorf("Responses stream final text did not match emitted deltas")
}

func responsesStreamError(label, operation string, failure responsesErrorPayload) error {
	message := failure.Message
	if message == "" {
		message = failure.Code
	}
	return &Error{Provider: label, Operation: operation, Kind: streamErrorKind(failure.Code), Retryable: false, Message: sanitizeProviderText(message, 2048)}
}
