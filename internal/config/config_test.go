package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProjectConfigAndExpandEnvironment(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TEST_COLLO_KEY", "secret-value")
	data := `{"default_provider":"custom","default_model":"fallback","providers":{"custom":{"type":"openai-compatible","base_url":"http://localhost:1234/v1/","api_key":"${TEST_COLLO_KEY}","model":"preferred"}},"permissions":{"mode":"workspace"}}`
	if err := os.WriteFile(filepath.Join(dir, ProjectFile), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source != filepath.Join(dir, ProjectFile) {
		t.Fatalf("source=%q", cfg.Source)
	}
	p := cfg.Providers["custom"]
	if p.APIKey != "secret-value" {
		t.Fatalf("key not expanded: %q", p.APIKey)
	}
	if p.BaseURL != "http://localhost:1234/v1" {
		t.Fatalf("base URL=%q", p.BaseURL)
	}
	name, _, model, err := cfg.Selected("custom", "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "custom" || model != "preferred" {
		t.Fatalf("selected %s/%s", name, model)
	}
}

func TestValidateAzureProviderTypes(t *testing.T) {
	cfg := Defaults()
	cfg.Providers["azure"] = Provider{Type: "azure-openai", BaseURL: "https://example.openai.azure.com", Deployment: "code"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Providers["azure"] = Provider{Type: "made-up", BaseURL: "https://example.com"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unsupported provider error")
	}
}

func TestDefaultsValidate(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatal(err)
	}
}
