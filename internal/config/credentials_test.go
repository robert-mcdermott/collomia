package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeProviderConfig writes a global configuration with one provider and
// points the loader at a temporary home so no real configuration is read.
func writeProviderConfig(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".collomia")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return t.TempDir() // workspace with no project configuration
}

// stubStore replaces the credential-store lookup for the duration of a test.
func stubStore(t *testing.T, values map[string]string) {
	t.Helper()
	previous := lookupStoredCredential
	lookupStoredCredential = func(provider string) (string, bool, error) {
		secret, ok := values[provider]
		return secret, ok, nil
	}
	t.Cleanup(func() { lookupStoredCredential = previous })
}

// The resolution order is the promise this feature makes to anyone already
// using environment variables: adding a stored credential must never change
// which key an existing configuration resolves.
func TestCredentialResolutionOrder(t *testing.T) {
	tests := []struct {
		name       string
		provider   string
		env        map[string]string
		stored     map[string]string
		wantKey    string
		wantSource string
	}{
		{
			name:       "explicit api_key wins over everything",
			provider:   `{"type":"openai","base_url":"https://api.example.com/v1","model":"m","api_key":"literal","api_key_env":"TEST_KEY"}`,
			env:        map[string]string{"TEST_KEY": "from-env"},
			stored:     map[string]string{"p": "from-store"},
			wantKey:    "literal",
			wantSource: "api_key",
		},
		{
			name:       "api_key_env wins over the store",
			provider:   `{"type":"openai","base_url":"https://api.example.com/v1","model":"m","api_key_env":"TEST_KEY"}`,
			env:        map[string]string{"TEST_KEY": "from-env"},
			stored:     map[string]string{"p": "from-store"},
			wantKey:    "from-env",
			wantSource: "environment TEST_KEY",
		},
		{
			name:       "the store is used when nothing else resolves",
			provider:   `{"type":"openai","base_url":"https://api.example.com/v1","model":"m","api_key_env":"TEST_KEY"}`,
			stored:     map[string]string{"p": "from-store"},
			wantKey:    "from-store",
			wantSource: "credential store",
		},
		{
			name:     "no store entry leaves the provider unauthenticated",
			provider: `{"type":"openai","base_url":"https://api.example.com/v1","model":"m"}`,
			wantKey:  "",
		},
		{
			name:     "entra takes no stored key",
			provider: `{"type":"azure-openai","auth":"entra","base_url":"https://example.openai.azure.com","deployment":"d"}`,
			stored:   map[string]string{"p": "from-store"},
			wantKey:  "",
		},
		{
			name:     "sigv4 takes no stored key",
			provider: `{"type":"bedrock","auth":"sigv4","region":"us-west-2"}`,
			stored:   map[string]string{"p": "from-store"},
			wantKey:  "",
		},
		{
			name:       "a provider family's own environment variable wins over the store",
			provider:   `{"type":"bedrock","region":"us-west-2"}`,
			env:        map[string]string{"AWS_BEARER_TOKEN_BEDROCK": "from-aws-env"},
			stored:     map[string]string{"p": "from-store"},
			wantKey:    "",
			wantSource: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			if _, ok := test.env["AWS_BEARER_TOKEN_BEDROCK"]; !ok {
				t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
			}
			if _, ok := test.env["TEST_KEY"]; !ok {
				t.Setenv("TEST_KEY", "")
			}
			workspace := writeProviderConfig(t, `{"default_provider":"p","default_model":"m","providers":{"p":`+test.provider+`}}`)
			stubStore(t, test.stored)

			cfg, err := Load(workspace)
			if err != nil {
				t.Fatal(err)
			}
			p := cfg.Providers["p"]
			if p.APIKey != test.wantKey {
				t.Errorf("api key = %q, want %q", p.APIKey, test.wantKey)
			}
			if test.wantSource != "" && p.CredentialSource != test.wantSource {
				t.Errorf("credential source = %q, want %q", p.CredentialSource, test.wantSource)
			}
		})
	}
}

// The Bedrock bearer variable is named in two packages that cannot import each
// other. A rename in one must fail here rather than quietly changing
// precedence.
func TestImplicitCredentialEnvIsDeclaredForBedrock(t *testing.T) {
	if got := ImplicitCredentialEnv("bedrock"); got != "AWS_BEARER_TOKEN_BEDROCK" {
		t.Fatalf("ImplicitCredentialEnv(bedrock) = %q", got)
	}
	if got := ImplicitCredentialEnv("openai"); got != "" {
		t.Fatalf("ImplicitCredentialEnv(openai) = %q, want empty", got)
	}
}

// The credential source describes the environment, so it must never be written
// back into a configuration file.
func TestCredentialSourceIsNotSerialized(t *testing.T) {
	workspace := writeProviderConfig(t, `{"default_provider":"p","providers":{"p":{"type":"openai","base_url":"https://api.example.com/v1","model":"m","api_key":"literal"}}}`)
	stubStore(t, nil)
	cfg, err := Load(workspace)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cfg.Providers["p"])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "credential") || strings.Contains(string(data), "CredentialSource") {
		t.Fatalf("provider serialization leaked the credential source: %s", data)
	}
}
