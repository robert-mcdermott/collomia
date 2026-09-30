package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// readProvider returns one provider block from a written file, decoded
// generically so keys this build does not know are visible to the test.
func readProvider(t *testing.T, path, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Providers map[string]map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("invalid JSON written: %v\n%s", err, data)
	}
	return document.Providers[name]
}

func TestReconfigureKeepsEverySettingSetupDoesNotOwn(t *testing.T) {
	// The defect this replaced: re-verifying a provider to change its model
	// rewrote the whole entry from the fields setup verifies, so headers,
	// temperature, reasoning, pricing, timeouts, and anything newer than the
	// build were silently deleted.
	t.Setenv("GW_ROOT", "http://gateway.test")
	path := filepath.Join(t.TempDir(), "config.json")
	original := `{
  "providers": {
    "gw": {
      "type": "openai-compatible",
      "base_url": "${GW_ROOT}/v1",
      "model": "old-model",
      "api_key_env": "GW_KEY",
      "headers": {"X-Team": "research"},
      "temperature": 0.2,
      "reasoning": {"effort": "high"},
      "pricing": {"input_per_million": 1.5, "output_per_million": 6},
      "request_timeout_seconds": 300,
      "a_future_setting": {"kept": true},
      "context_window": 9000,
      "max_tokens": 1000
    }
  }
}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	existing, err := ReadExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	resolved := appconfig.ResolveProvider("gw", existing.Definitions["gw"])
	result := Build("gw", resolved, "new-model", CredentialKeep, "GW_KEY", "",
		provider.Limits{ContextWindow: 65536, MaxOutput: 8192, ContextSource: provider.LimitsEndpoint, OutputSource: provider.LimitsTable})

	update := existing.Update(result)
	if !update.Exists || update.Replaced {
		t.Fatalf("the same endpoint must be an update, got %+v", update)
	}
	for _, key := range []string{"a_future_setting", "headers", "pricing", "reasoning", "request_timeout_seconds", "temperature"} {
		if !contains(update.Kept, key) {
			t.Errorf("the confirmation must name %q as kept, got %v", key, update.Kept)
		}
	}

	if err := Apply(path, result); err != nil {
		t.Fatal(err)
	}
	written := readProvider(t, path, "gw")
	if written["model"] != "new-model" {
		t.Errorf("model = %v", written["model"])
	}
	if written["context_window"] != float64(65536) || written["max_tokens"] != float64(8192) {
		t.Errorf("limits = %v / %v", written["context_window"], written["max_tokens"])
	}
	if written["base_url"] != "${GW_ROOT}/v1" {
		t.Errorf("base_url = %v; a reference the user maintains must not become a literal", written["base_url"])
	}
	if written["api_key_env"] != "GW_KEY" {
		t.Errorf("api_key_env = %v", written["api_key_env"])
	}
	if !reflect.DeepEqual(written["headers"], map[string]any{"X-Team": "research"}) {
		t.Errorf("headers = %v", written["headers"])
	}
	if written["temperature"] != 0.2 || written["request_timeout_seconds"] != float64(300) {
		t.Errorf("temperature/timeout = %v / %v", written["temperature"], written["request_timeout_seconds"])
	}
	if !reflect.DeepEqual(written["reasoning"], map[string]any{"effort": "high"}) {
		t.Errorf("reasoning = %v", written["reasoning"])
	}
	if written["pricing"] == nil || written["a_future_setting"] == nil {
		t.Error("pricing and unknown keys must survive")
	}
}

func TestReplacingWithADifferentEndpointCarriesNothingOver(t *testing.T) {
	// Headers commonly hold a gateway's credentials. Carrying them to a
	// different host under a reused name would send them somewhere the user
	// never pointed them, so a changed endpoint replaces the entry whole — and
	// says what that drops.
	path := filepath.Join(t.TempDir(), "config.json")
	original := `{"providers": {"work": {"type": "openai-compatible", "base_url": "http://old-gateway.test/v1",
	  "model": "m", "headers": {"Authorization": "Bearer gateway-token"}, "temperature": 0.1}}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	existing, err := ReadExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	result := Build("work", appconfig.Provider{Type: "openai-compatible", BaseURL: "http://new-host.test/v1"}, "m", CredentialNone, "", "", provider.Limits{})
	update := existing.Update(result)
	if !update.Replaced || !contains(update.Dropped, "headers") || !contains(update.Dropped, "temperature") {
		t.Fatalf("a different endpoint must be a replacement naming what it drops, got %+v", update)
	}
	if err := Apply(path, result); err != nil {
		t.Fatal(err)
	}
	written := readProvider(t, path, "work")
	if _, ok := written["headers"]; ok {
		t.Error("headers must not follow a provider name to a different host")
	}
	if written["base_url"] != "http://new-host.test/v1" {
		t.Errorf("base_url = %v", written["base_url"])
	}
}

func TestALiteralKeySurvivesOnlyWhenTheCredentialIsKept(t *testing.T) {
	// Setup never writes a key, but a file may already hold one. Keeping the
	// arrangement keeps it; choosing a new credential must remove it, or the
	// old literal would go on winning over the key just stored or exported.
	original := `{"providers": {"p": {"type": "openai", "base_url": "https://api.test/v1", "model": "m", "api_key": "sk-in-file"}}}`
	for _, c := range []struct {
		plan CredentialPlan
		keep bool
	}{{CredentialKeep, true}, {CredentialEnv, false}} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
			t.Fatal(err)
		}
		result := Build("p", appconfig.Provider{Type: "openai", BaseURL: "https://api.test/v1"}, "m", c.plan, "OPENAI_API_KEY", "", provider.Limits{})
		if err := Apply(path, result); err != nil {
			t.Fatal(err)
		}
		_, kept := readProvider(t, path, "p")["api_key"]
		if kept != c.keep {
			t.Errorf("plan %s: api_key kept = %v, want %v", c.plan, kept, c.keep)
		}
	}
}

func TestOwnedKeysCoverEverythingBuildWrites(t *testing.T) {
	// A field Build learns to write but ownedKeys does not list would be
	// treated as the user's own on the next update and kept stale forever.
	full := appconfig.Provider{
		Type: "azure-openai", BaseURL: "https://r.test", Region: "r", Profile: "p", Deployment: "d",
		APIVersion: "v", Auth: "entra", EntraScope: "https://s/.default", EntraTenantID: "t", EntraAuthorityHost: "https://a",
		APIKeyEnv: "K",
	}
	result := Build("x", full, "m", CredentialKeep, "K", "", provider.Limits{ContextWindow: 100, MaxOutput: 10, ContextSource: provider.LimitsEndpoint, OutputSource: provider.LimitsEndpoint})
	encoded, err := json.Marshal(result.Provider)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &keys); err != nil {
		t.Fatal(err)
	}
	for key := range keys {
		if !ownedKeys[key] {
			t.Errorf("Build writes %q but ownedKeys does not list it", key)
		}
	}
}

func TestReadExistingKeepsEachProviderBlockVerbatim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"providers": {"p": {"type": "openai", "a_future_setting": 1}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	existing, err := ReadExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(existing.Raw["p"]), "a_future_setting") {
		t.Errorf("raw block = %s; the confirmation cannot name a kept setting it never saw", existing.Raw["p"])
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestChangingAProvidersModelKeepsTheOldModelsLimits(t *testing.T) {
	// Before per-model settings, choosing a new model overwrote the limits the
	// old one ran with, so /model back to it ran on the new model's numbers.
	path := filepath.Join(t.TempDir(), "config.json")
	original := `{"providers": {"local": {"type": "openai-compatible", "base_url": "http://local.test/v1",
	  "model": "old", "context_window": 16384, "max_tokens": 2048,
	  "models": {"new": {"context_window": 4096, "reasoning": {"effort": "low"}}}}}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	existing, err := ReadExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	result := Build("local", appconfig.Provider{Type: "openai-compatible", BaseURL: "http://local.test/v1"}, "new", CredentialNone, "", "",
		provider.Limits{ContextWindow: 131072, MaxOutput: 8192, ContextSource: provider.LimitsEndpoint, OutputSource: provider.LimitsTable})
	if update := existing.Update(result); update.MovedLimitsFor != "old" {
		t.Errorf("the confirmation must say the old model's limits move, got %+v", update)
	}
	if err := Apply(path, result); err != nil {
		t.Fatal(err)
	}
	written := readProvider(t, path, "local")
	models, _ := written["models"].(map[string]any)
	old, _ := models["old"].(map[string]any)
	if old["context_window"] != float64(16384) || old["max_tokens"] != float64(2048) {
		t.Errorf("old model entry = %v, want its previous limits", old)
	}
	fresh, _ := models["new"].(map[string]any)
	if _, shadowed := fresh["context_window"]; shadowed {
		t.Error("the chosen model's entry must not shadow the limits just verified for it")
	}
	if !reflect.DeepEqual(fresh["reasoning"], map[string]any{"effort": "low"}) {
		t.Errorf("the chosen model's other settings must stay, got %v", fresh)
	}
	if written["model"] != "new" || written["context_window"] != float64(131072) {
		t.Errorf("provider-level = %v / %v", written["model"], written["context_window"])
	}

	// And the file must load to the same answer the runtime would give.
	loaded, err := ReadExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Definitions["local"].ForModel("old"); got.Context != 16384 || got.ContextInherited {
		t.Errorf("old model now resolves to %+v", got)
	}
}

func TestKeepingTheSameModelMovesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"providers": {"local": {"type": "openai-compatible", "base_url": "http://local.test/v1", "model": "m", "context_window": 16384}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result := Build("local", appconfig.Provider{Type: "openai-compatible", BaseURL: "http://local.test/v1"}, "m", CredentialNone, "", "", provider.Limits{})
	if err := Apply(path, result); err != nil {
		t.Fatal(err)
	}
	if _, ok := readProvider(t, path, "local")["models"]; ok {
		t.Error("re-verifying the same model must not create a models entry")
	}
}
