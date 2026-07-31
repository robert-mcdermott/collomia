package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

const (
	// BedrockBearerTokenEnv is the standard environment variable recognized by
	// Amazon Bedrock for short- and long-term Bedrock API keys.
	BedrockBearerTokenEnv = "AWS_BEARER_TOKEN_BEDROCK"
	bedrockAuthAuto       = "auto"
	bedrockAuthSigV4      = "sigv4"
	bedrockAuthBearer     = "bearer"
	// Bedrock events are normally tiny token/tool fragments. Bound a single
	// frame so a corrupt or hostile endpoint cannot force an unbounded decoder
	// allocation while still allowing unusually large tool-argument chunks.
	maxBedrockEventMessage = 4 * 1024 * 1024
)

type BedrockClient struct {
	Label     string
	Region    string
	Profile   string
	Auth      string
	APIKey    string
	APIKeyEnv string
	HTTP      *http.Client
	Declared  Capabilities
}

func (c *BedrockClient) Name() string { return c.Label }

func (c *BedrockClient) Capabilities() Capabilities {
	if c.Declared.ProviderType != "" {
		return c.Declared
	}
	capabilities, _ := CapabilitiesFor("bedrock", "", 0)
	return capabilities
}

func (c *BedrockClient) Chat(ctx context.Context, in Request, onDelta func(Delta)) (Response, error) {
	if in.ReasoningEffort != "" && !bedrockClaudeModel(in.Model) {
		if onDelta != nil {
			onDelta(Delta{Warning: fmt.Sprintf("reasoning effort is not mapped for Bedrock model %q; using the model's default", in.Model)})
		}
		in.ReasoningEffort = ""
	}
	region := c.Region
	if region == "" {
		region = "us-east-1"
	}
	body, err := bedrockRequest(in)
	if err != nil {
		return Response{}, err
	}
	endpoint := fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/converse-stream", region, url.PathEscape(in.Model))
	for adjustment := 0; ; adjustment++ {
		data, err := json.Marshal(body)
		if err != nil {
			return Response{}, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
		if err != nil {
			return Response{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/vnd.amazon.eventstream")
		if err := c.authenticate(ctx, req, data, region); err != nil {
			return Response{}, &Error{Provider: c.Label, Operation: "authenticate", Kind: ErrorAuthentication, Message: sanitizeProviderText(err.Error(), 2048), Err: err}
		}
		client := c.HTTP
		if client == nil {
			client = httpClient()
		}
		resp, err := doWithRetry(client, req, c.Label, "converse-stream")
		if err != nil {
			return Response{}, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			errorBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
			resp.Body.Close()
			if readErr != nil {
				return Response{}, protocolError(c.Label, "read converse-stream error response", readErr)
			}
			if adjustment == 0 && in.ReasoningEffort != "" && bedrockRejectedReasoning(resp.StatusCode, errorBody) {
				delete(body, "additionalModelRequestFields")
				if onDelta != nil {
					onDelta(Delta{Warning: "Bedrock model rejected the configured reasoning effort; retrying with its default for this request"})
				}
				continue
			}
			return Response{}, responseError(resp, c.Label, "converse-stream", errorBody)
		}
		defer resp.Body.Close()
		if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "application/vnd.amazon.eventstream") {
			response, err := parseBedrockStream(resp.Body, c.Label, requestID(resp.Header), onDelta)
			return response, protocolError(c.Label, "read converse stream", err)
		}
		// Some compatible test/proxy endpoints return the ordinary Converse JSON
		// envelope even on the streaming route. Accept it without weakening the
		// advertised AWS path, which always uses event-stream framing.
		response, err := parseBedrockResponse(resp.Body, onDelta)
		return response, protocolError(c.Label, "decode converse response", err)
	}
}

func bedrockRejectedReasoning(status int, body []byte) bool {
	if status != http.StatusBadRequest {
		return false
	}
	message := strings.ToLower(string(body))
	rejected := strings.Contains(message, "unsupported") || strings.Contains(message, "not supported") ||
		strings.Contains(message, "invalid") || strings.Contains(message, "extraneous")
	return rejected && (strings.Contains(message, "output_config") || strings.Contains(message, "effort") || strings.Contains(message, "additionalmodelrequestfields"))
}

func (c *BedrockClient) authenticate(ctx context.Context, req *http.Request, body []byte, region string) error {
	switch c.authMode() {
	case bedrockAuthBearer:
		token, source, err := c.bearerToken()
		if err != nil {
			return err
		}
		if strings.ContainsAny(token, "\r\n") {
			return fmt.Errorf("Bedrock bearer token from %s contains invalid control characters", source)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	case bedrockAuthSigV4:
		opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
		if c.Profile != "" {
			opts = append(opts, awsconfig.WithSharedConfigProfile(c.Profile))
		}
		cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
		if err != nil {
			return fmt.Errorf("load AWS configuration: %w", err)
		}
		credentials, err := cfg.Credentials.Retrieve(ctx)
		if err != nil {
			return fmt.Errorf("retrieve AWS credentials: %w", err)
		}
		digest := sha256.Sum256(body)
		if err := v4.NewSigner().SignHTTP(ctx, credentials, req, hex.EncodeToString(digest[:]), "bedrock", region, time.Now()); err != nil {
			return fmt.Errorf("sign Bedrock request: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported Bedrock auth mode %q", c.Auth)
	}
}

// authMode resolves auto without reading or exposing the token value. An
// explicitly named api_key_env selects bearer even when it is currently
// unset, producing a useful missing-variable error instead of silently
// falling back to unrelated AWS credentials.
func (c *BedrockClient) authMode() string {
	mode := strings.ToLower(strings.TrimSpace(c.Auth))
	if mode == "" {
		mode = bedrockAuthAuto
	}
	if mode != bedrockAuthAuto {
		return mode
	}
	if strings.TrimSpace(c.APIKey) != "" || strings.TrimSpace(c.APIKeyEnv) != "" || strings.TrimSpace(os.Getenv(BedrockBearerTokenEnv)) != "" {
		return bedrockAuthBearer
	}
	return bedrockAuthSigV4
}

func (c *BedrockClient) bearerToken() (token, source string, err error) {
	if token = strings.TrimSpace(c.APIKey); token != "" {
		return token, "configured api_key", nil
	}
	if envName := strings.TrimSpace(c.APIKeyEnv); envName != "" {
		if token = strings.TrimSpace(os.Getenv(envName)); token == "" {
			return "", envName, fmt.Errorf("Bedrock bearer auth requires environment variable %s to be set", envName)
		}
		return token, envName, nil
	}
	if token = strings.TrimSpace(os.Getenv(BedrockBearerTokenEnv)); token == "" {
		return "", BedrockBearerTokenEnv, fmt.Errorf("Bedrock bearer auth requires api_key, api_key_env, or environment variable %s", BedrockBearerTokenEnv)
	}
	return token, BedrockBearerTokenEnv, nil
}

func bedrockRequest(in Request) (map[string]any, error) {
	messages := make([]any, 0, len(in.Messages))
	for i := 0; i < len(in.Messages); i++ {
		msg := in.Messages[i]
		role := msg.Role
		content := []any{}
		switch {
		case msg.Role == "tool":
			role = "user"
			// Converse requires the results for every toolUse block from one
			// assistant response in the content array of the immediately
			// following user message. Collomia stores each result as a separate
			// normalized message, so coalesce the consecutive result batch here.
			for {
				result, err := bedrockToolResult(msg)
				if err != nil {
					return nil, err
				}
				content = append(content, result)
				if i+1 >= len(in.Messages) || in.Messages[i+1].Role != "tool" {
					break
				}
				i++
				msg = in.Messages[i]
			}
		default:
			parts, err := bedrockContentParts(msg)
			if err != nil {
				return nil, err
			}
			content = append(content, parts...)
			for _, call := range msg.ToolCalls {
				var input any
				if err := json.Unmarshal(rawObject(call.Arguments), &input); err != nil {
					return nil, err
				}
				content = append(content, map[string]any{"toolUse": map[string]any{"toolUseId": call.ID, "name": call.Name, "input": input}})
			}
		}
		messages = append(messages, map[string]any{"role": role, "content": content})
	}
	body := map[string]any{"messages": messages, "inferenceConfig": map[string]any{"maxTokens": in.MaxTokens}}
	if in.ReasoningEffort != "" && bedrockClaudeModel(in.Model) {
		body["additionalModelRequestFields"] = map[string]any{
			"output_config": map[string]any{"effort": in.ReasoningEffort},
		}
	}
	if in.System != "" {
		body["system"] = []any{map[string]any{"text": in.System}}
	}
	if in.Temperature != nil {
		body["inferenceConfig"].(map[string]any)["temperature"] = *in.Temperature
	}
	if len(in.Tools) > 0 {
		tools := make([]any, 0, len(in.Tools))
		for _, tool := range in.Tools {
			schema, err := toolParameterSchema(tool.Name, tool.InputSchema)
			if err != nil {
				return nil, err
			}
			tools = append(tools, map[string]any{"toolSpec": map[string]any{"name": tool.Name, "description": tool.Description, "inputSchema": map[string]any{"json": schema}}})
		}
		body["toolConfig"] = map[string]any{"tools": tools}
	}
	return body, nil
}

func bedrockClaudeModel(model string) bool {
	model = strings.ToLower(model)
	return strings.Contains(model, "anthropic") || strings.Contains(model, "claude")
}

func bedrockToolResult(msg Message) (map[string]any, error) {
	content, err := bedrockContentParts(msg)
	if err != nil {
		return nil, err
	}
	// Preserve the pre-multimodal shape for an empty string result. Bedrock
	// requires at least one content block for every toolResult.
	if len(content) == 0 {
		content = []any{map[string]any{"text": msg.Content}}
	}
	return map[string]any{"toolResult": map[string]any{
		"toolUseId": msg.ToolCallID,
		"content":   content,
		"status":    "success",
	}}, nil
}

func bedrockContentParts(message Message) ([]any, error) {
	parts, err := messageContentParts(message)
	if err != nil {
		return nil, err
	}
	encoded := make([]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case ContentText:
			encoded = append(encoded, map[string]any{"text": part.Text})
		case ContentImage:
			format, err := bedrockImageFormat(part.MediaType)
			if err != nil {
				return nil, err
			}
			encoded = append(encoded, map[string]any{"image": map[string]any{"format": format, "source": map[string]any{"bytes": imageBase64(part)}}})
		}
	}
	return encoded, nil
}

func parseBedrockResponse(r io.Reader, onDelta func(Delta)) (Response, error) {
	var payload struct {
		Output struct {
			Message struct {
				Content []struct {
					Text    string `json:"text"`
					ToolUse *struct {
						ToolUseID string          `json:"toolUseId"`
						Name      string          `json:"name"`
						Input     json.RawMessage `json:"input"`
					} `json:"toolUse"`
				} `json:"content"`
			} `json:"message"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"inputTokens"`
			OutputTokens int `json:"outputTokens"`
		} `json:"usage"`
		StopReason string `json:"stopReason"`
	}
	if err := json.NewDecoder(r).Decode(&payload); err != nil {
		return Response{}, err
	}
	out := Response{Stop: payload.StopReason, Usage: Usage{InputTokens: payload.Usage.InputTokens, OutputTokens: payload.Usage.OutputTokens}}
	for _, part := range payload.Output.Message.Content {
		if part.Text != "" {
			out.Content += part.Text
			if onDelta != nil {
				onDelta(Delta{Text: part.Text})
			}
		}
		if part.ToolUse != nil {
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: part.ToolUse.ToolUseID, Name: part.ToolUse.Name, Arguments: rawObject(part.ToolUse.Input)})
		}
	}
	return out, nil
}

func parseBedrockStream(r io.Reader, label, requestID string, onDelta func(Delta)) (Response, error) {
	decoder := eventstream.NewDecoder()
	tools := map[int]*toolAccumulator{}
	var out Response
	terminal := false
	for {
		limited := &io.LimitedReader{R: r, N: maxBedrockEventMessage}
		message, err := decoder.Decode(limited, nil)
		if err == io.EOF {
			if limited.N == 0 {
				return Response{}, fmt.Errorf("Bedrock stream event exceeds %d-byte limit", maxBedrockEventMessage)
			}
			break
		}
		if err != nil {
			return Response{}, err
		}
		messageType := bedrockHeader(message.Headers, ":message-type")
		switch messageType {
		case "exception", "error":
			code := bedrockHeader(message.Headers, ":exception-type")
			if code == "" {
				code = bedrockHeader(message.Headers, ":error-code")
			}
			return Response{}, bedrockStreamError(label, requestID, code, message.Payload)
		case "event":
		default:
			return Response{}, fmt.Errorf("Bedrock stream message has unsupported type %q", messageType)
		}

		eventType := bedrockHeader(message.Headers, ":event-type")
		switch eventType {
		case "messageStart":
			// Role is always assistant for a ConverseStream response.
		case "contentBlockStart":
			var payload struct {
				ContentBlockIndex int `json:"contentBlockIndex"`
				Start             struct {
					ToolUse *struct {
						ToolUseID string `json:"toolUseId"`
						Name      string `json:"name"`
					} `json:"toolUse"`
				} `json:"start"`
			}
			if err := json.Unmarshal(message.Payload, &payload); err != nil {
				return Response{}, fmt.Errorf("decode Bedrock contentBlockStart: %w", err)
			}
			if payload.Start.ToolUse != nil {
				acc := &toolAccumulator{id: payload.Start.ToolUse.ToolUseID, name: payload.Start.ToolUse.Name}
				tools[payload.ContentBlockIndex] = acc
				if onDelta != nil {
					onDelta(Delta{ToolCall: &ToolCallDelta{Index: payload.ContentBlockIndex, ID: acc.id, Name: acc.name}})
				}
			}
		case "contentBlockDelta":
			var payload struct {
				ContentBlockIndex int `json:"contentBlockIndex"`
				Delta             struct {
					Text    string `json:"text"`
					ToolUse *struct {
						Input string `json:"input"`
					} `json:"toolUse"`
					ReasoningContent *struct {
						Text string `json:"text"`
					} `json:"reasoningContent"`
				} `json:"delta"`
			}
			if err := json.Unmarshal(message.Payload, &payload); err != nil {
				return Response{}, fmt.Errorf("decode Bedrock contentBlockDelta: %w", err)
			}
			if payload.Delta.Text != "" {
				out.Content += payload.Delta.Text
				if onDelta != nil {
					onDelta(Delta{Text: payload.Delta.Text})
				}
			}
			if payload.Delta.ReasoningContent != nil && payload.Delta.ReasoningContent.Text != "" && onDelta != nil {
				onDelta(Delta{Reasoning: payload.Delta.ReasoningContent.Text})
			}
			if payload.Delta.ToolUse != nil {
				acc := tools[payload.ContentBlockIndex]
				if acc == nil {
					acc = &toolAccumulator{}
					tools[payload.ContentBlockIndex] = acc
				}
				acc.args.WriteString(payload.Delta.ToolUse.Input)
				if onDelta != nil {
					onDelta(Delta{ToolCall: &ToolCallDelta{Index: payload.ContentBlockIndex, ID: acc.id, Name: acc.name, Arguments: payload.Delta.ToolUse.Input}})
				}
			}
		case "contentBlockStop":
			var payload struct {
				ContentBlockIndex int `json:"contentBlockIndex"`
			}
			if err := json.Unmarshal(message.Payload, &payload); err != nil {
				return Response{}, fmt.Errorf("decode Bedrock contentBlockStop: %w", err)
			}
			if acc := tools[payload.ContentBlockIndex]; acc != nil && onDelta != nil {
				onDelta(Delta{ToolCall: &ToolCallDelta{Index: payload.ContentBlockIndex, ID: acc.id, Name: acc.name, Done: true}})
			}
		case "messageStop":
			var payload struct {
				StopReason string `json:"stopReason"`
			}
			if err := json.Unmarshal(message.Payload, &payload); err != nil {
				return Response{}, fmt.Errorf("decode Bedrock messageStop: %w", err)
			}
			out.Stop = payload.StopReason
			terminal = true
		case "metadata":
			var payload struct {
				Usage struct {
					InputTokens          int `json:"inputTokens"`
					OutputTokens         int `json:"outputTokens"`
					CacheReadInputTokens int `json:"cacheReadInputTokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(message.Payload, &payload); err != nil {
				return Response{}, fmt.Errorf("decode Bedrock metadata: %w", err)
			}
			out.Usage = Usage{InputTokens: payload.Usage.InputTokens, OutputTokens: payload.Usage.OutputTokens, CachedTokens: payload.Usage.CacheReadInputTokens}
			if onDelta != nil {
				usage := out.Usage
				onDelta(Delta{Usage: &usage})
			}
		default:
			if onDelta != nil {
				onDelta(Delta{Warning: fmt.Sprintf("Bedrock stream ignored unknown event %q", eventType)})
			}
		}
	}
	if !terminal {
		return Response{}, fmt.Errorf("Bedrock stream ended without messageStop")
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
			return Response{}, fmt.Errorf("tool %s returned invalid JSON arguments", acc.name)
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: acc.id, Name: acc.name, Arguments: json.RawMessage(args)})
	}
	return out, nil
}

func bedrockHeader(headers eventstream.Headers, name string) string {
	value := headers.Get(name)
	if value == nil {
		return ""
	}
	return value.String()
}

func bedrockStreamError(label, requestID, code string, payload []byte) error {
	var detail struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(payload, &detail)
	if detail.Message == "" {
		detail.Message = code
	}
	return &Error{Provider: label, Operation: "converse stream", Kind: streamErrorKind(code), Retryable: false, RequestID: requestID, Message: sanitizeProviderText(detail.Message, 2048)}
}
