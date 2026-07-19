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
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

type BedrockClient struct {
	Label    string
	Region   string
	Profile  string
	HTTP     *http.Client
	Declared Capabilities
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
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if c.Profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(c.Profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return Response{}, fmt.Errorf("load AWS configuration: %w", err)
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
	credentials, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return Response{}, fmt.Errorf("retrieve AWS credentials: %w", err)
	}
	digest := sha256.Sum256(data)
	if err := v4.NewSigner().SignHTTP(ctx, credentials, req, hex.EncodeToString(digest[:]), "bedrock", region, time.Now()); err != nil {
		return Response{}, fmt.Errorf("sign Bedrock request: %w", err)
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

func bedrockRequest(in Request) (map[string]any, error) {
	messages := make([]any, 0, len(in.Messages))
	for _, msg := range in.Messages {
		role := msg.Role
		content := []any{}
		switch {
		case msg.Role == "tool":
			role = "user"
			content = append(content, map[string]any{"toolResult": map[string]any{"toolUseId": msg.ToolCallID, "content": []any{map[string]any{"text": msg.Content}}, "status": "success"}})
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
