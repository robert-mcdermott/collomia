package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

const (
	liveProviderTestsEnv  = "COLLO_LIVE_PROVIDER_TESTS"
	liveProviderConfigEnv = "COLLO_LIVE_PROVIDER_CONFIG"
)

type liveContractManifest struct {
	// RequiredFamilies makes a qualification run fail before making requests
	// when one of the four protocol families is absent. Smaller development
	// manifests may omit it and exercise only their configured endpoints.
	RequiredFamilies []string                      `json:"required_families,omitempty"`
	TimeoutSeconds   int                           `json:"timeout_seconds,omitempty"`
	Providers        map[string]appconfig.Provider `json:"providers"`
}

type liveStreamSignals struct {
	textDeltas int
	toolDeltas int
	usage      bool
}

type preparedLiveProvider struct {
	name    string
	model   string
	client  Client
	secrets []string
}

// TestLiveProviderContracts is deliberately double opt-in: the enable flag
// and a manifest path are both required. The manifest contains environment
// variable names, never credential values. See docs/LIVE_PROVIDER_CONTRACTS.md.
func TestLiveProviderContracts(t *testing.T) {
	if os.Getenv(liveProviderTestsEnv) != "1" {
		t.Skip("set COLLO_LIVE_PROVIDER_TESTS=1 and COLLO_LIVE_PROVIDER_CONFIG to run live provider contracts")
	}
	manifestPath := strings.TrimSpace(os.Getenv(liveProviderConfigEnv))
	if manifestPath == "" {
		t.Fatal("COLLO_LIVE_PROVIDER_CONFIG must name a live-contract manifest")
	}
	manifest, err := loadLiveContractManifest(manifestPath)
	if err != nil {
		t.Fatalf("load live-contract manifest: %v", err)
	}
	if err := validateLiveFamilyCoverage(manifest); err != nil {
		t.Fatal(err)
	}

	timeout := time.Duration(manifest.TimeoutSeconds) * time.Second
	if manifest.TimeoutSeconds < 0 {
		t.Fatal("timeout_seconds must not be negative")
	}
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	names := make([]string, 0, len(manifest.Providers))
	for name := range manifest.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	prepared := make([]preparedLiveProvider, 0, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			t.Fatal("provider names must not be empty")
		}
		providerConfig, secrets, err := resolveLiveProviderConfig(manifest.Providers[name])
		if err != nil {
			t.Fatalf("provider %q: %v", name, redactLiveContractError(err, secrets))
		}
		client, err := New(name, providerConfig, providerConfig.Model)
		if err != nil {
			t.Fatalf("provider %q: %v", name, redactLiveContractError(err, secrets))
		}
		prepared = append(prepared, preparedLiveProvider{name: name, model: providerConfig.Model, client: client, secrets: secrets})
	}
	for _, provider := range prepared {
		t.Run(provider.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			if err := runLiveProviderContract(ctx, provider.client, provider.model); err != nil {
				t.Fatal(redactLiveContractError(err, provider.secrets))
			}
		})
	}
}

func loadLiveContractManifest(path string) (liveContractManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return liveContractManifest{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var manifest liveContractManifest
	if err := decoder.Decode(&manifest); err != nil {
		return liveContractManifest{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return liveContractManifest{}, errors.New("manifest contains more than one JSON value")
		}
		return liveContractManifest{}, err
	}
	if len(manifest.Providers) == 0 {
		return liveContractManifest{}, errors.New("manifest providers must not be empty")
	}
	return manifest, nil
}

func validateLiveFamilyCoverage(manifest liveContractManifest) error {
	configured := map[string]bool{}
	for name, providerConfig := range manifest.Providers {
		family, err := liveProviderFamily(strings.ToLower(strings.TrimSpace(providerConfig.Type)))
		if err != nil {
			return fmt.Errorf("provider %q: %w", name, err)
		}
		configured[family] = true
	}
	seen := map[string]bool{}
	for _, family := range manifest.RequiredFamilies {
		family = strings.ToLower(strings.TrimSpace(family))
		if seen[family] {
			continue
		}
		seen[family] = true
		switch family {
		case "openai", "anthropic", "responses", "bedrock":
		default:
			return fmt.Errorf("unknown required provider family %q", family)
		}
		if !configured[family] {
			return fmt.Errorf("required provider family %q has no configured endpoint", family)
		}
	}
	return nil
}

func liveProviderFamily(providerType string) (string, error) {
	switch providerType {
	case "openai", "openai-compatible", "azure-openai", "azure-foundry":
		return "openai", nil
	case "anthropic", "anthropic-compatible", "azure-foundry-anthropic":
		return "anthropic", nil
	case "bedrock-mantle":
		return "responses", nil
	case "bedrock":
		return "bedrock", nil
	default:
		return "", fmt.Errorf("unsupported provider type %q", providerType)
	}
}

func resolveLiveProviderConfig(providerConfig appconfig.Provider) (appconfig.Provider, []string, error) {
	providerConfig.Type = strings.ToLower(strings.TrimSpace(providerConfig.Type))
	providerConfig.Auth = strings.ToLower(strings.TrimSpace(providerConfig.Auth))
	providerConfig.Model = strings.TrimSpace(providerConfig.Model)
	if providerConfig.Type == "" {
		return appconfig.Provider{}, nil, errors.New("type is required")
	}
	if providerConfig.Model == "" {
		return appconfig.Provider{}, nil, errors.New("model is required")
	}
	if _, err := liveProviderFamily(providerConfig.Type); err != nil {
		return appconfig.Provider{}, nil, err
	}
	if strings.TrimSpace(providerConfig.APIKey) != "" {
		return appconfig.Provider{}, nil, errors.New("api_key is forbidden in a live-contract manifest; use api_key_env")
	}
	if providerConfig.Type == "bedrock" {
		switch providerConfig.Auth {
		case "", "auto", "sigv4", "bearer":
		default:
			return appconfig.Provider{}, nil, fmt.Errorf("auth must be auto, sigv4, or bearer (got %q)", providerConfig.Auth)
		}
		if providerConfig.Auth == "sigv4" && providerConfig.APIKeyEnv != "" {
			return appconfig.Provider{}, nil, errors.New("api_key_env is not used with auth=sigv4")
		}
		if providerConfig.Auth == "bearer" && providerConfig.Profile != "" {
			return appconfig.Provider{}, nil, errors.New("profile is not used with auth=bearer")
		}
		if providerConfig.Auth == "bearer" && providerConfig.APIKeyEnv == "" && strings.TrimSpace(os.Getenv(BedrockBearerTokenEnv)) == "" {
			return appconfig.Provider{}, nil, fmt.Errorf("auth=bearer requires api_key_env or %s", BedrockBearerTokenEnv)
		}
	}

	var secrets []string
	// SigV4 credentials are consumed by the AWS SDK rather than this provider
	// config, but include standard environment sources in the final safeguard
	// against an unexpectedly echoing endpoint or SDK error.
	for _, name := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", BedrockBearerTokenEnv} {
		if value := os.Getenv(name); value != "" {
			secrets = append(secrets, value)
		}
	}
	fail := func(err error) (appconfig.Provider, []string, error) {
		return appconfig.Provider{}, compactSecrets(secrets), err
	}
	if envName := strings.TrimSpace(providerConfig.APIKeyEnv); envName != "" {
		secret, ok := os.LookupEnv(envName)
		if !ok || strings.TrimSpace(secret) == "" {
			return fail(fmt.Errorf("credential environment variable %s is not set", envName))
		}
		providerConfig.APIKey = secret
		secrets = append(secrets, secret)
	}

	baseURL, expandedSecrets, err := expandLiveContractEnv(providerConfig.BaseURL)
	secrets = append(secrets, expandedSecrets...)
	if err != nil {
		return fail(fmt.Errorf("base_url: %w", err))
	}
	providerConfig.BaseURL = strings.TrimRight(baseURL, "/")
	if providerConfig.BaseURL != "" {
		parsed, err := url.Parse(providerConfig.BaseURL)
		if err != nil {
			return fail(fmt.Errorf("base_url: %w", err))
		}
		if parsed.User != nil {
			return fail(errors.New("base_url must not contain embedded credentials"))
		}
		if !parsed.IsAbs() || parsed.Host == "" {
			return fail(errors.New("base_url must be an absolute HTTP(S) URL"))
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return fail(errors.New("base_url must use HTTP or HTTPS"))
		}
	}
	if providerConfig.Type != "bedrock" && providerConfig.BaseURL == "" {
		return fail(errors.New("base_url is required"))
	}

	resolvedHeaders := make(map[string]string, len(providerConfig.Headers))
	for key, value := range providerConfig.Headers {
		if isCredentialHeader(key) && value != "" && !strings.Contains(value, "$") {
			return fail(fmt.Errorf("header %s must reference an environment variable, not contain a literal credential", key))
		}
		expanded, headerSecrets, err := expandLiveContractEnv(value)
		secrets = append(secrets, headerSecrets...)
		if err != nil {
			return fail(fmt.Errorf("header %s: %w", key, err))
		}
		resolvedHeaders[key] = expanded
	}
	providerConfig.Headers = resolvedHeaders
	for _, timeout := range []struct {
		key   string
		value int
	}{
		{"connect_timeout_seconds", providerConfig.ConnectTimeoutSeconds},
		{"request_timeout_seconds", providerConfig.RequestTimeoutSeconds},
		{"stream_idle_timeout_seconds", providerConfig.StreamIdleTimeoutSeconds},
	} {
		if timeout.value < 0 {
			return fail(fmt.Errorf("%s must not be negative", timeout.key))
		}
	}
	if providerConfig.ConnectTimeoutSeconds == 0 {
		providerConfig.ConnectTimeoutSeconds = 10
	}
	if providerConfig.RequestTimeoutSeconds == 0 {
		providerConfig.RequestTimeoutSeconds = 120
	}
	if providerConfig.StreamIdleTimeoutSeconds == 0 {
		providerConfig.StreamIdleTimeoutSeconds = 60
	}
	return providerConfig, compactSecrets(secrets), nil
}

func expandLiveContractEnv(value string) (string, []string, error) {
	var secrets []string
	var missing []string
	expanded := os.Expand(value, func(name string) string {
		value, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
			return ""
		}
		if value != "" {
			secrets = append(secrets, value)
		}
		return value
	})
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", secrets, fmt.Errorf("environment variable %s is not set", strings.Join(missing, ", "))
	}
	return expanded, secrets, nil
}

func isCredentialHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "api-key", "x-api-key", "x-goog-api-key":
		return true
	default:
		return false
	}
}

func compactSecrets(values []string) []string {
	unique := map[string]bool{}
	var out []string
	for _, value := range values {
		if value == "" || unique[value] {
			continue
		}
		unique[value] = true
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func redactLiveContractError(err error, secrets []string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, secret := range compactSecrets(secrets) {
		message = strings.ReplaceAll(message, secret, "[REDACTED]")
	}
	return errors.New(message)
}

func runLiveProviderContract(ctx context.Context, client Client, model string) error {
	request := Request{
		Model:  model,
		System: "This is an automated provider protocol contract. Follow the tool and response instructions exactly.",
		Messages: []Message{{
			Role:    "user",
			Content: `Call echo_contract exactly once with {"value":"ok"}. Do not answer with text before calling the tool.`,
		}},
		Tools: []ToolDefinition{{
			Name:        "echo_contract",
			Description: "Return a value unchanged for an automated protocol contract.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
		}},
		MaxTokens: 256,
	}

	var firstSignals liveStreamSignals
	first, err := client.Chat(ctx, request, func(delta Delta) { recordLiveStreamSignal(&firstSignals, delta) })
	if err != nil {
		return fmt.Errorf("tool-call turn: %w", err)
	}
	if len(first.ToolCalls) != 1 {
		return fmt.Errorf("tool-call turn returned %d tool calls, want 1", len(first.ToolCalls))
	}
	call := first.ToolCalls[0]
	if call.Name != "echo_contract" {
		return fmt.Errorf("tool-call turn requested %q, want echo_contract", call.Name)
	}
	var arguments struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
		return fmt.Errorf("tool-call arguments are not valid JSON: %w", err)
	}
	if arguments.Value != "ok" {
		return fmt.Errorf("tool-call value is %q, want ok", arguments.Value)
	}
	if firstSignals.toolDeltas == 0 {
		return errors.New("tool-call turn did not stream a normalized tool-call delta")
	}
	if !firstSignals.usage {
		return errors.New("tool-call turn did not stream a normalized usage event")
	}
	if first.Usage.InputTokens+first.Usage.OutputTokens == 0 {
		return errors.New("tool-call turn did not report token usage")
	}

	followup := request
	followup.System += " After the tool result, do not call another tool; answer with exactly CONTRACT_OK."
	followup.Messages = append(followup.Messages,
		Message{Role: "assistant", Content: first.Content, ToolCalls: first.ToolCalls},
		Message{Role: "tool", ToolCallID: call.ID, Content: `{"value":"ok"}`},
	)
	var followupSignals liveStreamSignals
	final, err := client.Chat(ctx, followup, func(delta Delta) { recordLiveStreamSignal(&followupSignals, delta) })
	if err != nil {
		return fmt.Errorf("tool-result turn: %w", err)
	}
	if len(final.ToolCalls) != 0 {
		return fmt.Errorf("tool-result turn unexpectedly returned %d more tool calls", len(final.ToolCalls))
	}
	if !strings.Contains(strings.ToUpper(final.Content), "CONTRACT_OK") {
		return fmt.Errorf("tool-result turn did not acknowledge the contract (content length %d)", len(final.Content))
	}
	if followupSignals.textDeltas == 0 {
		return errors.New("tool-result turn did not stream a normalized text delta")
	}
	if !followupSignals.usage {
		return errors.New("tool-result turn did not stream a normalized usage event")
	}
	if final.Usage.InputTokens+final.Usage.OutputTokens == 0 {
		return errors.New("tool-result turn did not report token usage")
	}
	return nil
}

func recordLiveStreamSignal(signals *liveStreamSignals, delta Delta) {
	if delta.Text != "" {
		signals.textDeltas++
	}
	if delta.ToolCall != nil {
		signals.toolDeltas++
	}
	if delta.Usage != nil {
		signals.usage = true
	}
}

func TestLiveProviderFamilyCoversBuiltInTypes(t *testing.T) {
	wants := map[string]string{
		"openai": "openai", "openai-compatible": "openai", "azure-openai": "openai", "azure-foundry": "openai",
		"anthropic": "anthropic", "anthropic-compatible": "anthropic", "azure-foundry-anthropic": "anthropic",
		"bedrock-mantle": "responses", "bedrock": "bedrock",
	}
	for providerType, want := range wants {
		got, err := liveProviderFamily(providerType)
		if err != nil || got != want {
			t.Errorf("type=%s family=%s want=%s err=%v", providerType, got, want, err)
		}
	}
}

func TestResolveLiveProviderConfigKeepsCredentialsOutOfManifest(t *testing.T) {
	_, _, err := resolveLiveProviderConfig(appconfig.Provider{Type: "openai", BaseURL: "https://example.invalid", APIKey: "literal-secret", Model: "model"})
	if err == nil || !strings.Contains(err.Error(), "api_key is forbidden") {
		t.Fatalf("literal credential error=%v", err)
	}

	t.Setenv("COLLO_LIVE_TEST_KEY", "environment-secret")
	resolved, secrets, err := resolveLiveProviderConfig(appconfig.Provider{Type: "openai", BaseURL: "https://example.invalid", APIKeyEnv: "COLLO_LIVE_TEST_KEY", Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, secret := range secrets {
		found = found || secret == "environment-secret"
	}
	if resolved.APIKey != "environment-secret" || !found {
		t.Fatalf("resolved key length=%d secrets=%d", len(resolved.APIKey), len(secrets))
	}
	redacted := redactLiveContractError(errors.New("failed environment-secret"), secrets)
	if strings.Contains(redacted.Error(), "environment-secret") || !strings.Contains(redacted.Error(), "[REDACTED]") {
		t.Fatalf("redacted error=%q", redacted)
	}
}

func TestValidateLiveFamilyCoverage(t *testing.T) {
	manifest := liveContractManifest{
		RequiredFamilies: []string{"openai", "bedrock"},
		Providers: map[string]appconfig.Provider{
			"gateway": {Type: "openai-compatible"},
		},
	}
	err := validateLiveFamilyCoverage(manifest)
	if err == nil || !strings.Contains(err.Error(), strconv.Quote("bedrock")) {
		t.Fatalf("coverage error=%v", err)
	}
}

type liveContractFixtureClient struct {
	t       *testing.T
	calls   int
	results []Message
}

func (c *liveContractFixtureClient) Name() string { return "live-fixture/model" }

func (c *liveContractFixtureClient) Chat(_ context.Context, request Request, onDelta func(Delta)) (Response, error) {
	c.t.Helper()
	c.calls++
	usage := Usage{InputTokens: 10 + c.calls, OutputTokens: 2}
	switch c.calls {
	case 1:
		if len(request.Tools) != 1 || request.Tools[0].Name != "echo_contract" || len(request.Messages) != 1 {
			c.t.Fatalf("initial live request=%+v", request)
		}
		onDelta(Delta{ToolCall: &ToolCallDelta{Index: 0, ID: "call-1", Name: "echo_contract", Arguments: `{"value":"ok"}`, Done: true}})
		onDelta(Delta{Usage: &usage})
		return Response{
			ToolCalls: []ToolCall{{ID: "call-1", Name: "echo_contract", Arguments: json.RawMessage(`{"value":"ok"}`)}},
			Usage:     usage,
		}, nil
	case 2:
		if len(request.Messages) != 3 || len(request.Messages[1].ToolCalls) != 1 || request.Messages[2].Role != "tool" || request.Messages[2].ToolCallID != "call-1" {
			c.t.Fatalf("follow-up live messages=%+v", request.Messages)
		}
		c.results = append(c.results, request.Messages[2])
		onDelta(Delta{Text: "CONTRACT_OK"})
		onDelta(Delta{Usage: &usage})
		return Response{Content: "CONTRACT_OK", Usage: usage}, nil
	default:
		c.t.Fatalf("unexpected live contract request %d", c.calls)
		return Response{}, nil
	}
}

func TestRunLiveProviderContractRoundTripsSyntheticTool(t *testing.T) {
	client := &liveContractFixtureClient{t: t}
	if err := runLiveProviderContract(t.Context(), client, "contract-model"); err != nil {
		t.Fatal(err)
	}
	if client.calls != 2 || len(client.results) != 1 || client.results[0].Content != `{"value":"ok"}` {
		t.Fatalf("calls=%d tool results=%+v", client.calls, client.results)
	}
}
