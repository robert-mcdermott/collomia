package app

import (
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

func TestProviderForModelPrefersWhatIsKnownAboutTheSelectedModel(t *testing.T) {
	// The defect this replaced: /model switched within a provider kept the
	// window written for the provider's own model, so a 1M-token model ran
	// with a 32K window, or a 32K model with a 1M one and never compacted.
	p := appconfig.Provider{Type: "openai-compatible", Model: "qwen3-coder", Context: 262144, MaxTokens: 16384}

	fromCatalog := ProviderForModel(p, "tiny", provider.Limits{ContextWindow: 8192, ContextSource: provider.LimitsEndpoint})
	if fromCatalog.Context != 8192 {
		t.Errorf("context = %d, want what the catalog reported for this model", fromCatalog.Context)
	}
	if fromCatalog.MaxTokens >= fromCatalog.Context {
		t.Errorf("an inherited cap of %d cannot run inside an 8192 window", fromCatalog.MaxTokens)
	}

	fromTable := ProviderForModel(p, "gpt-oss:20b", provider.Limits{})
	if fromTable.Context != 131072 || fromTable.MaxTokens != 16384 || fromTable.ContextInherited {
		t.Errorf("gpt-oss = %d/%d, want its published limits", fromTable.Context, fromTable.MaxTokens)
	}

	unknown := ProviderForModel(p, "never-heard-of-it", provider.Limits{})
	if unknown.Context != 262144 || !unknown.ContextInherited {
		t.Errorf("with nothing known the inherited window is kept as a last resort, got %+v", unknown)
	}

	own := ProviderForModel(p, "qwen3-coder", provider.Limits{ContextWindow: 4096, ContextSource: provider.LimitsEndpoint})
	if own.Context != 262144 {
		t.Error("the provider's own model keeps its configured limits; a report never overrides configuration")
	}
}

func TestProviderForModelUsesAModelsOwnEntryOverEverything(t *testing.T) {
	p := appconfig.Provider{
		Type: "openai-compatible", Model: "a", Context: 32768, MaxTokens: 4096,
		Models: map[string]appconfig.ModelSettings{"b": {Context: 65536, MaxTokens: 8192, Reasoning: &appconfig.Reasoning{Effort: "low"}}},
	}
	b := ProviderForModel(p, "b", provider.Limits{ContextWindow: 1000000, ContextSource: provider.LimitsEndpoint})
	if b.Context != 65536 || b.MaxTokens != 8192 || b.Reasoning == nil || b.Reasoning.Effort != "low" {
		t.Errorf("b = %+v, want its own entry", b)
	}
}

func TestSwitchingModelsAppliesEachModelsSettings(t *testing.T) {
	home := isolateGlobalFiles(t)
	writeGlobalConfig(t, home, `{"default_provider":"ollama","default_model":"qwen3-coder","providers":{"ollama":{
		"type":"openai-compatible","base_url":"http://127.0.0.1:11434/v1","model":"qwen3-coder",
		"context_window":32768,"max_tokens":8192,
		"models":{"gpt-oss:20b":{"context_window":65536,"max_tokens":4096,"reasoning":{"effort":"low"}}}}}}`)
	runtime, err := New(t.Context(), Options{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	if got := runtime.Agent.ProviderSettings(); got.Context != 32768 || got.Reasoning != nil {
		t.Fatalf("startup = %d / %+v", got.Context, got.Reasoning)
	}
	if err := runtime.Select("ollama", "gpt-oss:20b"); err != nil {
		t.Fatal(err)
	}
	got := runtime.Agent.ProviderSettings()
	if got.Context != 65536 || got.MaxTokens != 4096 || got.Reasoning == nil || got.Reasoning.Effort != "low" {
		t.Errorf("after /model = %d/%d/%+v, want the model's own entry", got.Context, got.MaxTokens, got.Reasoning)
	}
	if err := runtime.Select("ollama", "qwen3-coder"); err != nil {
		t.Fatal(err)
	}
	if got := runtime.Agent.ProviderSettings(); got.Context != 32768 || got.Reasoning != nil {
		t.Errorf("switching back = %d / %+v, want the provider's own model's settings", got.Context, got.Reasoning)
	}
}

func TestSwitchingToAListedModelUsesTheCatalogsReport(t *testing.T) {
	isolateGlobalFiles(t)
	runtime, err := New(t.Context(), Options{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.catalogLimits = map[string]map[string]provider.Limits{"ollama": {
		"listed-model": {ContextWindow: 16384, ContextSource: provider.LimitsEndpoint},
	}}
	if err := runtime.Select("ollama", "listed-model"); err != nil {
		t.Fatal(err)
	}
	if got := runtime.Agent.ProviderSettings().Context; got != 16384 {
		t.Errorf("context = %d, want the catalog's report for the model switched to", got)
	}
}
