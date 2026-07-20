package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

const (
	// AzureOpenAIEntraScope is the documented audience for traditional Azure
	// OpenAI deployment-scoped data-plane endpoints.
	AzureOpenAIEntraScope = "https://cognitiveservices.azure.com/.default"
	// AzureFoundryEntraScope is the documented audience for current Microsoft
	// Foundry OpenAI/v1, Claude, and project endpoints.
	AzureFoundryEntraScope = "https://ai.azure.com/.default"

	azureTokenRefreshSkew = 2 * time.Minute
)

// BearerTokenSource supplies a current token for one request. Implementations
// may cache and refresh tokens; callers must never persist or log the value.
type BearerTokenSource interface {
	Token(context.Context) (string, error)
}

type cachedAzureTokenSource struct {
	credential    azcore.TokenCredential
	scopes        []string
	now           func() time.Time
	refreshBefore time.Duration

	mu    sync.Mutex
	token azcore.AccessToken
}

func newCachedAzureTokenSource(credential azcore.TokenCredential, scopes []string) *cachedAzureTokenSource {
	return &cachedAzureTokenSource{
		credential: credential, scopes: append([]string(nil), scopes...),
		now: time.Now, refreshBefore: azureTokenRefreshSkew,
	}
}

func (s *cachedAzureTokenSource) Token(ctx context.Context) (string, error) {
	if s == nil || s.credential == nil {
		return "", errors.New("Microsoft Entra credential is not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token.Token != "" && tokenFresh(s.token, s.now(), s.refreshBefore) {
		return s.token.Token, nil
	}
	token, err := s.credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: append([]string(nil), s.scopes...)})
	if err != nil {
		return "", fmt.Errorf("DefaultAzureCredential could not acquire a token for %s: %w", strings.Join(s.scopes, ", "), err)
	}
	if strings.TrimSpace(token.Token) == "" {
		return "", errors.New("DefaultAzureCredential returned an empty token")
	}
	if strings.ContainsAny(token.Token, "\r\n") {
		return "", errors.New("DefaultAzureCredential returned a token containing invalid control characters")
	}
	s.token = token
	return token.Token, nil
}

func tokenFresh(token azcore.AccessToken, now time.Time, refreshBefore time.Duration) bool {
	if token.Token == "" || token.ExpiresOn.IsZero() {
		return false
	}
	refreshAt := token.ExpiresOn.Add(-refreshBefore)
	if !token.RefreshOn.IsZero() && token.RefreshOn.Before(refreshAt) {
		refreshAt = token.RefreshOn
	}
	return now.Before(refreshAt)
}

func newAzureTokenSource(providerConfig appconfig.Provider) (BearerTokenSource, error) {
	scope := AzureEntraScope(providerConfig.Type, providerConfig.EntraScope)
	options := &azidentity.DefaultAzureCredentialOptions{TenantID: strings.TrimSpace(providerConfig.EntraTenantID)}
	if authority := strings.TrimSpace(providerConfig.EntraAuthorityHost); authority != "" {
		if err := validateAzureAuthorityHost(authority); err != nil {
			return nil, err
		}
		options.ClientOptions.Cloud = cloud.Configuration{
			ActiveDirectoryAuthorityHost: authority,
			Services:                     map[cloud.ServiceName]cloud.ServiceConfiguration{},
		}
	}
	credential, err := azidentity.NewDefaultAzureCredential(options)
	if err != nil {
		return nil, fmt.Errorf("create DefaultAzureCredential: %w", err)
	}
	return newCachedAzureTokenSource(credential, []string{scope}), nil
}

// AzureEntraScope returns the explicit scope or the provider-family default.
func AzureEntraScope(providerType, configured string) string {
	if scope := strings.TrimSpace(configured); scope != "" {
		return scope
	}
	if providerType == "azure-foundry" || providerType == "azure-foundry-anthropic" {
		return AzureFoundryEntraScope
	}
	return AzureOpenAIEntraScope
}

func validateAzureAuthorityHost(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("entra_authority_host must be an absolute HTTPS origin without credentials, path, query, or fragment")
	}
	return nil
}

func authorizeWithBearerSource(ctx context.Context, reqHeader mapHeader, source BearerTokenSource, label string) error {
	if source == nil {
		return nil
	}
	token, err := source.Token(ctx)
	if err != nil {
		return &Error{
			Provider: label, Operation: "authenticate", Kind: ErrorAuthentication,
			Retryable: false, Message: sanitizeProviderText(err.Error(), 2048), Err: err,
		}
	}
	reqHeader.Set("Authorization", "Bearer "+token)
	return nil
}

// mapHeader is the small part of http.Header needed by the token source. The
// interface keeps token tests independent of an HTTP transport.
type mapHeader interface {
	Set(string, string)
}

func withAzureRBACHint(err error, hint string) error {
	if err == nil || hint == "" {
		return err
	}
	providerErr, ok := AsError(err)
	if !ok || (providerErr.Kind != ErrorAuthentication && providerErr.Kind != ErrorPermission) {
		return err
	}
	copy := *providerErr
	message := strings.TrimSpace(copy.Message)
	if message != "" {
		message += "; "
	}
	copy.Message = sanitizeProviderText(message+hint, 2048)
	return &copy
}

func azureRBACHint(providerType string) string {
	if providerType == "azure-openai" {
		return "verify the Entra scope and assign Cognitive Services OpenAI User (or Contributor) on the Azure OpenAI resource; role assignments can take several minutes to propagate"
	}
	return "verify the Entra scope and assign Cognitive Services User on the Microsoft Foundry resource; role assignments can take several minutes to propagate"
}
