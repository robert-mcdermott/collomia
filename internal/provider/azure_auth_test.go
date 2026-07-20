package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

type azureCredentialFixture struct {
	mu     sync.Mutex
	tokens []azcore.AccessToken
	err    error
	calls  int
	scopes [][]string
}

func (c *azureCredentialFixture) GetToken(_ context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.scopes = append(c.scopes, append([]string(nil), options.Scopes...))
	if c.err != nil {
		return azcore.AccessToken{}, c.err
	}
	if len(c.tokens) == 0 {
		return azcore.AccessToken{}, errors.New("fixture has no token")
	}
	token := c.tokens[0]
	if len(c.tokens) > 1 {
		c.tokens = c.tokens[1:]
	}
	return token, nil
}

type bearerSourceFunc func(context.Context) (string, error)

func (f bearerSourceFunc) Token(ctx context.Context) (string, error) { return f(ctx) }

func TestAzureTokenSourceCachesAndRefreshesBeforeExpiry(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	credential := &azureCredentialFixture{tokens: []azcore.AccessToken{
		{Token: "token-one", ExpiresOn: now.Add(10 * time.Minute), RefreshOn: now.Add(7 * time.Minute)},
		{Token: "token-two", ExpiresOn: now.Add(time.Hour)},
	}}
	source := newCachedAzureTokenSource(credential, []string{AzureOpenAIEntraScope})
	source.now = func() time.Time { return now }

	for range 2 {
		token, err := source.Token(t.Context())
		if err != nil || token != "token-one" {
			t.Fatalf("cached token=%q err=%v", token, err)
		}
	}
	if credential.calls != 1 {
		t.Fatalf("credential calls=%d, want 1 before refresh", credential.calls)
	}

	now = now.Add(7 * time.Minute)
	token, err := source.Token(t.Context())
	if err != nil || token != "token-two" || credential.calls != 2 {
		t.Fatalf("refreshed token=%q calls=%d err=%v", token, credential.calls, err)
	}
	if len(credential.scopes) != 2 || len(credential.scopes[0]) != 1 || credential.scopes[0][0] != AzureOpenAIEntraScope {
		t.Fatalf("token scopes=%v", credential.scopes)
	}
}

func TestAzureTokenSourceSerializesConcurrentRefresh(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	credential := &azureCredentialFixture{tokens: []azcore.AccessToken{{Token: "shared-token", ExpiresOn: now.Add(time.Hour)}}}
	source := newCachedAzureTokenSource(credential, []string{AzureFoundryEntraScope})
	source.now = func() time.Time { return now }

	const workers = 12
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			token, err := source.Token(t.Context())
			if err == nil && token != "shared-token" {
				err = errors.New("unexpected token")
			}
			errs <- err
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if credential.calls != 1 {
		t.Fatalf("concurrent refresh called credential %d times", credential.calls)
	}
}

func TestAzureAdaptersAuthenticateEveryRequestWithCurrentToken(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		client  func(*http.Client, BearerTokenSource) Client
		request Request
	}{
		{
			name: "openai",
			body: "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n",
			client: func(httpClient *http.Client, source BearerTokenSource) Client {
				return &OpenAIClient{Label: "azure-openai/model", ChatURL: "https://example.invalid/chat", Headers: map[string]string{"Authorization": "Bearer stale-header"}, BearerSource: source, HTTP: httpClient}
			},
			request: contractRequest(),
		},
		{
			name: "foundry-anthropic",
			body: "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n",
			client: func(httpClient *http.Client, source BearerTokenSource) Client {
				return &AnthropicClient{Label: "foundry-claude/model", BaseURL: "https://example.invalid/anthropic", Headers: map[string]string{"Authorization": "Bearer stale-header"}, BearerSource: source, HTTP: httpClient}
			},
			request: contractRequest(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			source := bearerSourceFunc(func(context.Context) (string, error) {
				calls++
				return "entra-token", nil
			})
			httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if got := req.Header.Get("Authorization"); got != "Bearer entra-token" {
					t.Errorf("authorization=%q", got)
				}
				if req.Header.Get("api-key") != "" || req.Header.Get("x-api-key") != "" {
					t.Errorf("Entra request also sent an API key: %v", req.Header)
				}
				return &http.Response{
					StatusCode: http.StatusOK, Status: "200 OK",
					Header:  http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:    io.NopCloser(strings.NewReader(test.body)),
					Request: req,
				}, nil
			})}
			response, err := test.client(httpClient, source).Chat(t.Context(), test.request, nil)
			if err != nil || response.Content != "ok" || calls != 1 {
				t.Fatalf("response=%+v token calls=%d err=%v", response, calls, err)
			}
		})
	}
}

func TestAzureAuthenticationFailureStopsBeforeHTTP(t *testing.T) {
	called := false
	client := &OpenAIClient{
		Label: "azure-openai/model", ChatURL: "https://example.invalid/chat",
		BearerSource: bearerSourceFunc(func(context.Context) (string, error) { return "", errors.New("no Azure credential available") }),
		HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			called = true
			return nil, errors.New("must not run")
		})},
	}
	_, err := client.Chat(t.Context(), contractRequest(), nil)
	providerErr, ok := AsError(err)
	if !ok || providerErr.Kind != ErrorAuthentication || providerErr.Retryable || providerErr.Operation != "authenticate" {
		t.Fatalf("authentication error=%+v raw=%v", providerErr, err)
	}
	if called {
		t.Fatal("HTTP request occurred after token acquisition failed")
	}
}

func TestAzureFactorySelectsDocumentedScopes(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "dev")
	tests := []struct {
		providerType string
		baseURL      string
		wantScope    string
	}{
		{"azure-openai", "https://demo.openai.azure.com", AzureOpenAIEntraScope},
		{"azure-foundry", "https://demo.services.ai.azure.com/openai/v1", AzureFoundryEntraScope},
		{"azure-foundry-anthropic", "https://demo.services.ai.azure.com/anthropic", AzureFoundryEntraScope},
	}
	for _, test := range tests {
		t.Run(test.providerType, func(t *testing.T) {
			client, err := New("azure", appconfig.Provider{Type: test.providerType, Auth: "entra", BaseURL: test.baseURL, Model: "deployment"}, "deployment")
			if err != nil {
				t.Fatal(err)
			}
			var source BearerTokenSource
			switch typed := client.(type) {
			case *OpenAIClient:
				source = typed.BearerSource
			case *AnthropicClient:
				source = typed.BearerSource
			default:
				t.Fatalf("client type=%T", client)
			}
			cached, ok := source.(*cachedAzureTokenSource)
			if !ok || len(cached.scopes) != 1 || cached.scopes[0] != test.wantScope {
				t.Fatalf("token source=%T scopes=%v", source, cached.scopes)
			}
		})
	}
}

func TestAzureRBACFailuresIncludeActionableRoleHint(t *testing.T) {
	client := &OpenAIClient{
		Label: "azure-openai/model", ChatURL: "https://example.invalid/chat",
		BearerSource: bearerSourceFunc(func(context.Context) (string, error) { return "entra-token", nil }),
		AuthHint:     azureRBACHint("azure-openai"),
		HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusForbidden, Status: "403 Forbidden", Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{"error":{"message":"access denied"}}`)), Request: req,
			}, nil
		})},
	}
	_, err := client.Chat(t.Context(), contractRequest(), nil)
	providerErr, ok := AsError(err)
	if !ok || providerErr.Kind != ErrorPermission || !strings.Contains(providerErr.Message, "Cognitive Services OpenAI User") || !strings.Contains(providerErr.Message, "several minutes") {
		t.Fatalf("RBAC error=%+v raw=%v", providerErr, err)
	}
}

func TestAzureScopeOverrideAndAuthorityValidation(t *testing.T) {
	if got := AzureEntraScope("azure-foundry", "https://custom.example/.default"); got != "https://custom.example/.default" {
		t.Fatalf("scope override=%q", got)
	}
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "dev")
	if _, err := newAzureTokenSource(appconfig.Provider{
		Type:               "azure-foundry",
		EntraAuthorityHost: "https://login.microsoftonline.us/",
	}); err != nil {
		t.Fatalf("sovereign authority constructor: %v", err)
	}
	for _, invalid := range []string{"http://login.example", "https://user:secret@login.example", "https://login.example/tenant", "https://login.example?x=1"} {
		if err := validateAzureAuthorityHost(invalid); err == nil {
			t.Errorf("authority %q unexpectedly accepted", invalid)
		}
	}
}
