package provider

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

func New(name string, p appconfig.Provider, model string) (Client, error) {
	label := name + "/" + model
	capabilities, err := CapabilitiesFor(p.Type, model, p.Context)
	if err != nil {
		return nil, err
	}
	switch p.Type {
	case "openai", "openai-compatible":
		return &OpenAIClient{Label: label, BaseURL: p.BaseURL, APIKey: p.APIKey, Headers: p.Headers, Declared: capabilities}, nil
	case "anthropic", "anthropic-compatible":
		return &AnthropicClient{Label: label, BaseURL: p.BaseURL, APIKey: p.APIKey, Headers: p.Headers, BearerAuth: p.Auth == "bearer", Declared: capabilities}, nil
	case "bedrock-mantle":
		return &ResponsesClient{Label: label, BaseURL: p.BaseURL, APIKey: p.APIKey, Headers: p.Headers, Declared: capabilities}, nil
	case "bedrock":
		return &BedrockClient{Label: label, Region: p.Region, Profile: p.Profile, Auth: p.Auth, APIKey: p.APIKey, APIKeyEnv: p.APIKeyEnv, Declared: capabilities}, nil
	case "azure-openai":
		endpoint, err := azureOpenAIChatURL(p, model)
		if err != nil {
			return nil, err
		}
		return &OpenAIClient{Label: label, APIKey: p.APIKey, Headers: p.Headers, ChatURL: endpoint, APIKeyHeader: azureKeyHeader(p), Declared: capabilities}, nil
	case "azure-foundry":
		base := strings.TrimRight(p.BaseURL, "/")
		if !strings.Contains(base, "/openai/v1") {
			base += "/openai/v1"
		}
		keyHeader := azureKeyHeader(p)
		return &OpenAIClient{Label: label, BaseURL: base, APIKey: p.APIKey, Headers: p.Headers, APIKeyHeader: keyHeader, Declared: capabilities}, nil
	case "azure-foundry-anthropic":
		base := strings.TrimRight(p.BaseURL, "/")
		if !strings.HasSuffix(base, "/anthropic") {
			base += "/anthropic"
		}
		return &AnthropicClient{Label: label, BaseURL: base, APIKey: p.APIKey, Headers: p.Headers, BearerAuth: p.Auth == "bearer", Declared: capabilities}, nil
	default:
		return nil, fmt.Errorf("unsupported provider type %q", p.Type)
	}
}

func azureKeyHeader(p appconfig.Provider) string {
	if p.Auth == "bearer" {
		return ""
	}
	return "api-key"
}

func azureOpenAIChatURL(p appconfig.Provider, model string) (string, error) {
	base := strings.TrimRight(p.BaseURL, "/")
	if strings.Contains(base, "/openai/v1") {
		return base + "/chat/completions", nil
	}
	deployment := p.Deployment
	if deployment == "" {
		deployment = model
	}
	version := p.APIVersion
	if version == "" {
		version = "2024-10-21"
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = path.Join(u.Path, "openai", "deployments", deployment, "chat", "completions")
	query := u.Query()
	query.Set("api-version", version)
	u.RawQuery = query.Encode()
	return u.String(), nil
}
