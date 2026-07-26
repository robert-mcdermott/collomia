package config

import (
	"os"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/credstore"
)

// lookupStoredCredential is a variable so configuration tests can exercise
// resolution order without touching a real keychain.
var lookupStoredCredential = credstore.Get

// implicitCredentialEnv names the environment variable a provider family reads
// on its own, without any Collomia configuration. The stored credential is
// consulted only after these, so exporting the variable a provider already
// documents keeps overriding a stored value — the same precedence
// `api_key_env` has.
//
// The Bedrock entry must stay equal to provider.BedrockBearerTokenEnv; a test
// in the provider package fails if the two drift, because this package cannot
// import provider without a cycle.
var implicitCredentialEnv = map[string]string{"bedrock": "AWS_BEARER_TOKEN_BEDROCK"}

// ImplicitCredentialEnv returns the environment variable a provider family
// resolves on its own, or "" when it has none.
func ImplicitCredentialEnv(providerType string) string {
	return implicitCredentialEnv[strings.ToLower(strings.TrimSpace(providerType))]
}

// usesStoredCredential reports whether a stored credential could apply to this
// provider at all. Delegated authentication modes are excluded because there
// is nothing static to store: Microsoft Entra issues short-lived tokens
// through DefaultAzureCredential, and SigV4 draws on the AWS credential chain,
// which already owns profiles, SSO, roles, and instance identity.
func usesStoredCredential(p Provider) bool {
	switch p.Auth {
	case "entra", "sigv4":
		return false
	}
	if env := ImplicitCredentialEnv(p.Type); env != "" && strings.TrimSpace(os.Getenv(env)) != "" {
		return false
	}
	return true
}

// StoredCredentialApplies reports whether a stored credential would be
// consulted for this provider, so diagnostics can explain a provider the
// credential store cannot help — rather than leaving a user to wonder why
// `collo auth set` changed nothing.
func StoredCredentialApplies(p Provider) bool { return usesStoredCredential(p) }
