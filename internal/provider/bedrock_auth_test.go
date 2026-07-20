package provider

import (
	"io"
	"net/http"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func bedrockTestClient(t *testing.T, inspect func(*http.Request)) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		inspect(req)
		payload := `{"output":{"message":{"content":[{"text":"checked"},{"toolUse":{"toolUseId":"tool_1","name":"read_file","input":{"path":"README.md"}}}]}},"usage":{"inputTokens":8,"outputTokens":2},"stopReason":"tool_use"}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(payload)),
			Request:    req,
		}, nil
	})}
}

func TestBedrockBearerAuthenticationFromConfiguredEnvironment(t *testing.T) {
	t.Setenv("COLLO_BEDROCK_KEY", "short-or-long-term-bedrock-key")
	client := &BedrockClient{
		Label: "bedrock-bearer", Region: "us-west-2", Auth: "bearer", APIKeyEnv: "COLLO_BEDROCK_KEY",
		HTTP: bedrockTestClient(t, func(req *http.Request) {
			if got := req.Header.Get("Authorization"); got != "Bearer short-or-long-term-bedrock-key" {
				t.Errorf("authorization=%q", got)
			}
			if req.Header.Get("X-Amz-Date") != "" || req.Header.Get("X-Amz-Security-Token") != "" {
				t.Errorf("bearer request was also SigV4-signed: %v", req.Header)
			}
			if !strings.Contains(req.URL.Path, "/model/contract-model/converse-stream") {
				t.Errorf("path=%q", req.URL.Path)
			}
			if req.Header.Get("Accept") != "application/vnd.amazon.eventstream" {
				t.Errorf("accept=%q", req.Header.Get("Accept"))
			}
		}),
	}
	assertContractResponse(t, client, "checked", Usage{InputTokens: 8, OutputTokens: 2})
}

func TestBedrockAutoUsesStandardBearerToken(t *testing.T) {
	t.Setenv(BedrockBearerTokenEnv, "standard-bedrock-bearer-token")
	client := &BedrockClient{
		Label: "bedrock-auto", Region: "us-east-1",
		HTTP: bedrockTestClient(t, func(req *http.Request) {
			if got := req.Header.Get("Authorization"); got != "Bearer standard-bedrock-bearer-token" {
				t.Errorf("authorization=%q", got)
			}
		}),
	}
	assertContractResponse(t, client, "checked", Usage{InputTokens: 8, OutputTokens: 2})
}

func TestBedrockSigV4SupportsTemporaryCredentials(t *testing.T) {
	// Explicit sigv4 must win even if a Bedrock bearer token is also present.
	t.Setenv(BedrockBearerTokenEnv, "must-not-be-used")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret-access-key-for-test")
	t.Setenv("AWS_SESSION_TOKEN", "temporary-session-token")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_PROFILE", "")
	client := &BedrockClient{
		Label: "bedrock-sigv4", Region: "us-west-2", Auth: "sigv4",
		HTTP: bedrockTestClient(t, func(req *http.Request) {
			authorization := req.Header.Get("Authorization")
			if !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 ") || !strings.Contains(authorization, "Credential=AKIDEXAMPLE/") {
				t.Errorf("authorization=%q", authorization)
			}
			if got := req.Header.Get("X-Amz-Security-Token"); got != "temporary-session-token" {
				t.Errorf("session token=%q", got)
			}
		}),
	}
	assertContractResponse(t, client, "checked", Usage{InputTokens: 8, OutputTokens: 2})
}

func TestBedrockAutoFallsBackToSigV4WithoutBearerToken(t *testing.T) {
	t.Setenv(BedrockBearerTokenEnv, "")
	client := &BedrockClient{}
	if got := client.authMode(); got != bedrockAuthSigV4 {
		t.Fatalf("auth mode=%q", got)
	}
	client.APIKeyEnv = "MISSING_BEDROCK_TOKEN"
	if got := client.authMode(); got != bedrockAuthBearer {
		t.Fatalf("an explicit key environment should select bearer, got %q", got)
	}
}

func TestFactoryWiresBedrockAuthentication(t *testing.T) {
	client, err := New("bedrock", appconfig.Provider{
		Type: "bedrock", Region: "us-west-2", Profile: "development", Auth: "bearer",
		APIKey: "configured-key", APIKeyEnv: "COLLO_BEDROCK_KEY", Model: "model",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	bedrock, ok := client.(*BedrockClient)
	if !ok {
		t.Fatalf("client type=%T", client)
	}
	if bedrock.Region != "us-west-2" || bedrock.Profile != "development" || bedrock.Auth != "bearer" || bedrock.APIKey != "configured-key" || bedrock.APIKeyEnv != "COLLO_BEDROCK_KEY" {
		t.Fatalf("client=%+v", bedrock)
	}
}

func TestBedrockBearerMissingTokenFailsBeforeRequest(t *testing.T) {
	t.Setenv(BedrockBearerTokenEnv, "")
	called := false
	client := &BedrockClient{
		Label: "bedrock-missing", Auth: "bearer", APIKeyEnv: "MISSING_BEDROCK_TOKEN",
		HTTP: bedrockTestClient(t, func(*http.Request) { called = true }),
	}
	_, err := client.Chat(t.Context(), contractRequest(), nil)
	if err == nil || !strings.Contains(err.Error(), "MISSING_BEDROCK_TOKEN") {
		t.Fatalf("error=%v", err)
	}
	if called {
		t.Fatal("HTTP request should not occur without a bearer token")
	}
}

func TestBedrockBearerRejectsHeaderInjection(t *testing.T) {
	client := &BedrockClient{Auth: "bearer", APIKey: "token\r\nX-Evil: true"}
	_, err := client.Chat(t.Context(), contractRequest(), nil)
	if err == nil || !strings.Contains(err.Error(), "control characters") || strings.Contains(err.Error(), "X-Evil") {
		t.Fatalf("error=%v", err)
	}
}
