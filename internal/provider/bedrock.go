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
	"strings"
	"time"

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
	region := c.Region
	if region == "" {
		region = "us-east-1"
	}
	body, err := bedrockRequest(in)
	if err != nil {
		return Response{}, err
	}
	data, err := json.Marshal(body)
	if err != nil {
		return Response{}, err
	}
	endpoint := fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/converse", region, url.PathEscape(in.Model))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := c.authenticate(ctx, req, data, region); err != nil {
		return Response{}, err
	}
	client := c.HTTP
	if client == nil {
		client = httpClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("Bedrock request: %w", err)
	}
	defer resp.Body.Close()
	if err := checkResponse(resp, "Bedrock"); err != nil {
		return Response{}, err
	}
	return parseBedrockResponse(resp.Body, onDelta)
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
				content = append(content, bedrockToolResult(msg))
				if i+1 >= len(in.Messages) || in.Messages[i+1].Role != "tool" {
					break
				}
				i++
				msg = in.Messages[i]
			}
		default:
			if msg.Content != "" {
				content = append(content, map[string]any{"text": msg.Content})
			}
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
	if in.System != "" {
		body["system"] = []any{map[string]any{"text": in.System}}
	}
	if in.Temperature != nil {
		body["inferenceConfig"].(map[string]any)["temperature"] = *in.Temperature
	}
	if len(in.Tools) > 0 {
		tools := make([]any, 0, len(in.Tools))
		for _, tool := range in.Tools {
			var schema any
			if err := json.Unmarshal(rawObject(tool.InputSchema), &schema); err != nil {
				return nil, err
			}
			tools = append(tools, map[string]any{"toolSpec": map[string]any{"name": tool.Name, "description": tool.Description, "inputSchema": map[string]any{"json": schema}}})
		}
		body["toolConfig"] = map[string]any{"tools": tools}
	}
	return body, nil
}

func bedrockToolResult(msg Message) map[string]any {
	return map[string]any{"toolResult": map[string]any{
		"toolUseId": msg.ToolCallID,
		"content":   []any{map[string]any{"text": msg.Content}},
		"status":    "success",
	}}
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
