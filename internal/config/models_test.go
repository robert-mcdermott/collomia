package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestForModelAppliesTheModelsOwnEntry(t *testing.T) {
	p := Provider{
		Type: "openai-compatible", Model: "big", Context: 131072, MaxTokens: 16384,
		Reasoning: &Reasoning{Effort: "high"},
		Models: map[string]ModelSettings{
			"small": {Context: 8192, MaxTokens: 2048, Reasoning: &Reasoning{Effort: "low"}},
			"big":   {Reasoning: &Reasoning{Effort: "max"}},
		},
	}
	small := p.ForModel("small")
	if small.Context != 8192 || small.MaxTokens != 2048 || small.Reasoning.Effort != "low" {
		t.Errorf("small = %d/%d/%s, want its own entry", small.Context, small.MaxTokens, small.Reasoning.Effort)
	}
	if small.ContextInherited || small.MaxTokensInherited {
		t.Error("limits from the model's own entry are not inherited")
	}
	big := p.ForModel("big")
	if big.Context != 131072 || big.Reasoning.Effort != "max" || big.ContextInherited {
		t.Errorf("big = %+v; the provider's own model keeps provider-level limits and takes its entry's reasoning", big)
	}
	if p.Reasoning.Effort != "high" {
		t.Error("ForModel must not mutate the provider it was called on")
	}
}

func TestForModelMarksLimitsWrittenForAnotherModel(t *testing.T) {
	// A window setup wrote for the provider's own model says nothing about a
	// different one, so the runtime is told the value is only inherited.
	p := Provider{Type: "openai-compatible", Model: "big", Context: 131072, MaxTokens: 16384, Reasoning: &Reasoning{Effort: "high"}}
	other := p.ForModel("other")
	if !other.ContextInherited || !other.MaxTokensInherited {
		t.Errorf("other = %+v, want both limits marked inherited", other)
	}
	if other.Reasoning == nil || other.Reasoning.Effort != "high" {
		t.Error("provider-level reasoning keeps its provider-wide meaning")
	}

	// A provider with no model of its own keeps the older meaning, where its
	// limits apply to every model.
	legacy := Provider{Type: "openai-compatible", Context: 32768, MaxTokens: 4096}.ForModel("anything")
	if legacy.ContextInherited || legacy.MaxTokensInherited {
		t.Error("limits on a provider with no model of its own apply to every model")
	}
}

func TestModelEntriesAreValidated(t *testing.T) {
	cfg := Config{Providers: map[string]Provider{"p": {
		Type: "openai-compatible", BaseURL: "http://x.test/v1", Model: "m", Context: 8192, MaxTokens: 1024,
		Models: map[string]ModelSettings{
			"bad-pair":  {Context: 4096, MaxTokens: 4096},
			"bad-level": {Reasoning: &Reasoning{Effort: "extreme"}},
			"bad-price": {Pricing: &Pricing{InputPerMillion: 0, OutputPerMillion: 1}},
			// Only max_tokens for the provider's own model meets the
			// provider-level window, and that combined pair is what runs.
			"m": {MaxTokens: 9000},
		},
	}}}
	var fields []string
	for _, issue := range cfg.ValidateFields() {
		fields = append(fields, issue.Field)
	}
	joined := strings.Join(fields, " ")
	for _, want := range []string{
		"providers.p.models.bad-pair.max_tokens",
		"providers.p.models.bad-level.reasoning.effort",
		"providers.p.models.bad-price.pricing.input_per_million",
		"providers.p.models.m.max_tokens",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("validation must report %s; got %v", want, fields)
		}
	}
}

func TestModelEntriesSurviveLoadingAndNormalizeTheirEffort(t *testing.T) {
	var p Provider
	if err := json.Unmarshal([]byte(`{"type":"openai","model":"a","models":{"b":{"context_window":65536,"reasoning":{"effort":" HIGH "}}}}`), &p); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Providers: map[string]Provider{"p": p}}
	cfg.normalize()
	entry := cfg.Providers["p"].Models["b"]
	if entry.Context != 65536 || entry.Reasoning == nil || entry.Reasoning.Effort != "high" {
		t.Errorf("entry = %+v", entry)
	}
}

func TestEffectiveSettingsListModelEntries(t *testing.T) {
	cfg := Config{Providers: map[string]Provider{"p": {Type: "openai", Model: "a", Models: map[string]ModelSettings{"b": {Context: 65536}}}}}
	found := false
	for _, setting := range cfg.EffectiveSettings() {
		if strings.Contains(setting.Key, "models") && strings.Contains(setting.Key, "context_window") {
			found = true
		}
	}
	if !found {
		t.Error("/config all must show per-model settings")
	}
}
