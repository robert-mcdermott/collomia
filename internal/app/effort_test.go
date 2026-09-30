package app

import (
	"strings"
	"testing"
)

func TestEffortCommandChangesTheSessionOnly(t *testing.T) {
	home := isolateGlobalFiles(t)
	writeGlobalConfig(t, home, `{"default_provider":"ollama","default_model":"gpt-oss:20b","providers":{"ollama":{
		"type":"openai-compatible","base_url":"http://127.0.0.1:11434/v1","model":"gpt-oss:20b",
		"context_window":131072,"max_tokens":16384,"reasoning":{"effort":"medium"}}},
		"agents":{"thinker":{"description":"test","availability":"primary","reasoning":{"effort":"high"}}}}`)
	runtime, err := New(t.Context(), Options{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	if status := runtime.EffortStatus(); status.Effective != "medium" || !strings.Contains(status.Source, "providers.ollama") {
		t.Fatalf("initial = %+v", status)
	}
	status, err := runtime.SetEffort("low")
	if err != nil {
		t.Fatal(err)
	}
	if status.Effective != "low" || !status.Override || runtime.Agent.ProviderSettings().Reasoning.Effort != "low" {
		t.Errorf("after /effort low = %+v", status)
	}
	if _, err := runtime.SetEffort("max"); err == nil || !strings.Contains(err.Error(), "low, medium, high") {
		t.Errorf("a level gpt-oss does not accept must be refused with the ones it does, got %v", err)
	}
	if runtime.Agent.ProviderSettings().Reasoning.Effort != "low" {
		t.Error("a refused change must leave the effort as it was")
	}

	// /effort outranks an agent profile selected afterwards, and survives it.
	if err := runtime.SelectAgent("thinker"); err != nil {
		t.Fatal(err)
	}
	if got := runtime.Agent.ProviderSettings().Reasoning.Effort; got != "low" {
		t.Errorf("after selecting a profile, effort = %q; /effort holds until reset", got)
	}

	status, err = runtime.SetEffort("default")
	if err != nil {
		t.Fatal(err)
	}
	if status.Effective != "" || runtime.Agent.ProviderSettings().Reasoning != nil {
		t.Errorf("default must send no effort, got %+v", status)
	}
	status, err = runtime.SetEffort("reset")
	if err != nil {
		t.Fatal(err)
	}
	if status.Override || status.Effective != "high" || !strings.Contains(status.Source, "agent profile thinker") {
		t.Errorf("reset returns to configuration, where the active profile decides: %+v", status)
	}
	if _, err := runtime.SetEffort("ludicrous"); err == nil {
		t.Error("an unknown level must be refused")
	}
}
