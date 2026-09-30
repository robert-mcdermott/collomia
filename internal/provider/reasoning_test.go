package provider

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestKnownReasoningFollowsPublishedSupport(t *testing.T) {
	cases := []struct {
		model  string
		levels []string
		known  bool
	}{
		{"claude-opus-4-6", []string{"low", "medium", "high", "max"}, true},
		{"us.anthropic.claude-opus-4-7-v1", []string{"low", "medium", "high", "xhigh", "max"}, true},
		{"claude-opus-4-5-20251101", []string{"low", "medium", "high"}, true},
		{"claude-sonnet-4-5-20250929", nil, true},
		{"claude-3-5-sonnet", nil, true},
		{"gpt-5", []string{"minimal", "low", "medium", "high"}, true},
		{"gpt-5-mini", []string{"minimal", "low", "medium", "high"}, true},
		{"gpt-5.1", []string{"none", "low", "medium", "high"}, true},
		{"gpt-5.2-codex", []string{"none", "low", "medium", "high", "xhigh"}, true},
		{"gpt-4.1", nil, true},
		{"gpt-oss:20b", []string{"low", "medium", "high"}, true},
		// Released after this build: unknown rather than guessed.
		{"gpt-5.3", nil, false},
		{"claude-opus-9", nil, false},
		{"glm-5.3-flash:cloud", nil, false},
	}
	for _, c := range cases {
		support, _ := KnownReasoning(c.model)
		if support.Known() != c.known || !reflect.DeepEqual(nilIfEmpty(support.Levels), c.levels) {
			t.Errorf("%s: %+v, want known=%v levels=%v", c.model, support, c.known, c.levels)
		}
	}
	if support, _ := KnownReasoning("claude-opus-5-5"); support.Default != "medium" {
		t.Errorf("Opus 5.5 defaults to medium, got %q", support.Default)
	}
}

func nilIfEmpty(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return values
}

func TestAnthropicEffortSupportReadsTheModelsAPI(t *testing.T) {
	var effort map[string]json.RawMessage
	if err := json.Unmarshal([]byte(`{"supported":true,"low":{"supported":true},"medium":{"supported":true},
		"high":{"supported":true},"max":{"supported":true},"xhigh":null}`), &effort); err != nil {
		t.Fatal(err)
	}
	support := anthropicEffortSupport(effort)
	if !reflect.DeepEqual(support.Levels, []string{"low", "medium", "high", "max"}) || support.Source != LimitsEndpoint {
		t.Errorf("support = %+v; a null xhigh is not supported", support)
	}
	if err := json.Unmarshal([]byte(`{"supported":false,"low":{"supported":false}}`), &effort); err != nil {
		t.Fatal(err)
	}
	if support := anthropicEffortSupport(effort); !support.Unsupported() {
		t.Errorf("a model reporting no effort support must be known-unsupported, got %+v", support)
	}
}

func TestOrderedEffortsDropsNamesOutsideTheVocabulary(t *testing.T) {
	got := OrderedEfforts([]string{"max", "LOW", "ultra", "high", "low"})
	if !reflect.DeepEqual(got, []string{"low", "high", "max"}) {
		t.Errorf("got %v", got)
	}
}
