package setup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readDocument(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := jsonUnmarshal(data, &document); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	return document
}

// fakeStore replaces the OS credential store for one test.
func fakeStore(t *testing.T, contents map[string]string, failSet bool) {
	t.Helper()
	saved := [4]any{storeAvailable, storeGet, storeSet, storeDelete}
	storeAvailable = func() bool { return true }
	storeGet = func(name string) (string, bool, error) { value, ok := contents[name]; return value, ok, nil }
	storeSet = func(name, secret string) error {
		if failSet {
			return errors.New("store refused")
		}
		contents[name] = secret
		return nil
	}
	storeDelete = func(name string) (bool, error) { _, ok := contents[name]; delete(contents, name); return ok, nil }
	t.Cleanup(func() {
		storeAvailable = saved[0].(func() bool)
		storeGet = saved[1].(func(string) (string, bool, error))
		storeSet = saved[2].(func(string, string) error)
		storeDelete = saved[3].(func(string) (bool, error))
	})
}

const twoProviders = `{"default_provider": "a", "default_model": "m1", "providers": {
  "a": {"type": "openai-compatible", "base_url": "http://a.test/v1", "model": "m1", "context_window": 32768, "max_tokens": 4096,
        "headers": {"X-Team": "one"}, "temperature": 0.5},
  "b": {"type": "openai-compatible", "base_url": "http://b.test/v1", "model": "m2"}},
  "permissions": {"mode": "ask"}}`

func TestSetDefaultChangesOnlyTheSelection(t *testing.T) {
	path := writeConfig(t, twoProviders)
	if err := SetDefault(path, "b", "m2"); err != nil {
		t.Fatal(err)
	}
	document := readDocument(t, path)
	if document["default_provider"] != "b" || document["default_model"] != "m2" {
		t.Errorf("defaults = %v / %v", document["default_provider"], document["default_model"])
	}
	if document["permissions"] == nil {
		t.Error("an unrelated setting must survive")
	}
	if err := SetDefault(path, "missing", "x"); err == nil {
		t.Error("a provider the file does not have cannot become the default")
	}
}

func TestEditSettingsReplacesTemperatureAndHeaders(t *testing.T) {
	path := writeConfig(t, twoProviders)
	low := 0.1
	if err := EditSettings(path, "a", SettingsEdit{Temperature: &low, Headers: map[string]string{"Authorization": "Bearer ${TOKEN}"}}); err != nil {
		t.Fatal(err)
	}
	block := readDocument(t, path)["providers"].(map[string]any)["a"].(map[string]any)
	if block["temperature"] != 0.1 || !reflect.DeepEqual(block["headers"], map[string]any{"Authorization": "Bearer ${TOKEN}"}) {
		t.Errorf("block = %v", block)
	}
	if block["context_window"] != float64(32768) {
		t.Error("every other field of the provider must survive")
	}
	if err := EditSettings(path, "a", SettingsEdit{}); err != nil {
		t.Fatal(err)
	}
	block = readDocument(t, path)["providers"].(map[string]any)["a"].(map[string]any)
	if _, ok := block["temperature"]; ok {
		t.Error("an empty edit removes the temperature so the model default applies")
	}
	if _, ok := block["headers"]; ok {
		t.Error("an empty edit removes the headers")
	}
}

func TestRenameMovesTheDefaultAndTheStoredKey(t *testing.T) {
	store := map[string]string{"a": "sk-a"}
	fakeStore(t, store, false)
	path := writeConfig(t, twoProviders)
	moved, err := RenameProvider(path, "a", "primary")
	if err != nil || !moved {
		t.Fatalf("moved=%v err=%v", moved, err)
	}
	document := readDocument(t, path)
	providers := document["providers"].(map[string]any)
	if _, ok := providers["a"]; ok || providers["primary"] == nil || document["default_provider"] != "primary" {
		t.Errorf("document = %v", document)
	}
	if store["primary"] != "sk-a" || store["a"] != "" {
		t.Errorf("store = %v; the key follows the name", store)
	}
	if _, err := RenameProvider(path, "primary", "b"); err == nil {
		t.Error("renaming onto an existing provider must be refused")
	}
}

func TestRenameLeavesEverythingWhenTheStoreRefuses(t *testing.T) {
	fakeStore(t, map[string]string{"a": "sk-a"}, true)
	path := writeConfig(t, twoProviders)
	if _, err := RenameProvider(path, "a", "primary"); err == nil {
		t.Fatal("a failed key copy must stop the rename")
	}
	if readDocument(t, path)["providers"].(map[string]any)["a"] == nil {
		t.Error("the file must be unchanged, so the provider still authenticates")
	}
}

func TestRemoveRefusesTheDefault(t *testing.T) {
	path := writeConfig(t, twoProviders)
	if err := RemoveProvider(path, "a"); err == nil || !strings.Contains(err.Error(), "default") {
		t.Errorf("removing the default must be refused, got %v", err)
	}
	if err := RemoveProvider(path, "b"); err != nil {
		t.Fatal(err)
	}
	if _, ok := readDocument(t, path)["providers"].(map[string]any)["b"]; ok {
		t.Error("b must be gone")
	}
}

func TestSaveMaxTokensLandsWhereTheModelsLimitIsRead(t *testing.T) {
	path := writeConfig(t, twoProviders)
	if err := SaveMaxTokens(path, "a", "m1", 2048); err != nil {
		t.Fatal(err)
	}
	if got := readDocument(t, path)["providers"].(map[string]any)["a"].(map[string]any)["max_tokens"]; got != float64(2048) {
		t.Errorf("the provider's own model reads the provider level, got %v", got)
	}
	if err := SaveMaxTokens(path, "a", "other", 1024); err != nil {
		t.Fatal(err)
	}
	models := readDocument(t, path)["providers"].(map[string]any)["a"].(map[string]any)["models"].(map[string]any)
	if models["other"].(map[string]any)["max_tokens"] != float64(1024) {
		t.Errorf("another model gets its own entry, got %v", models)
	}
	if err := SaveMaxTokens(path, "a", "m1", 40000); err == nil {
		t.Error("a cap at or above the model's window must be refused")
	}
}

func TestValidProviderName(t *testing.T) {
	existing := Existing{Providers: []string{"a"}}
	for name, ok := range map[string]bool{"work-gw": true, "a": false, "": false, "has space": false, "-x": false} {
		if got := ValidProviderName(name, existing) == ""; got != ok {
			t.Errorf("%q valid=%v, want %v", name, got, ok)
		}
	}
}

func TestAddingAModelWritesOnlyItsEntry(t *testing.T) {
	path := writeConfig(t, twoProviders)
	result := Build("a", appconfig.Provider{Type: "openai-compatible", BaseURL: "http://a.test/v1"}, "m3", CredentialKeep, "", "",
		provider.Limits{ContextWindow: 65536, MaxOutput: 8192, ContextSource: provider.LimitsEndpoint, OutputSource: provider.LimitsTable})
	result.EntryOnly = true
	if err := Apply(path, result.WithEffort("low")); err != nil {
		t.Fatal(err)
	}
	block := readDocument(t, path)["providers"].(map[string]any)["a"].(map[string]any)
	if block["model"] != "m1" || block["context_window"] != float64(32768) || block["temperature"] != 0.5 {
		t.Errorf("the provider's own fields must be untouched, got %v", block)
	}
	want := map[string]any{"context_window": float64(65536), "max_tokens": float64(8192), "reasoning": map[string]any{"effort": "low"}}
	if got := block["models"].(map[string]any)["m3"]; !reflect.DeepEqual(got, want) {
		t.Errorf("m3 entry = %v", got)
	}
}

func TestEditingAConnectionKeepsSettingsAtTheNewEndpoint(t *testing.T) {
	path := writeConfig(t, twoProviders)
	existing, err := ReadExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	result := Build("a", appconfig.Provider{Type: "openai-compatible", BaseURL: "http://moved.test/v1"}, "m1", CredentialKeep, "", "", provider.Limits{})
	result.KeepSettings = true
	if update := existing.Update(result); update.Replaced || !update.EndpointChanged || !contains(update.Kept, "headers") {
		t.Errorf("update = %+v; an edited connection keeps settings and says they now go to the new endpoint", update)
	}
	if err := Apply(path, result); err != nil {
		t.Fatal(err)
	}
	block := readDocument(t, path)["providers"].(map[string]any)["a"].(map[string]any)
	if block["base_url"] != "http://moved.test/v1" || block["headers"] == nil {
		t.Errorf("block = %v", block)
	}
}

func TestSkippedVerificationSaysSo(t *testing.T) {
	if got := Unverified("m").Describe(); !strings.Contains(got, "not re-verified") {
		t.Errorf("describe = %q", got)
	}
}

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
