package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/provider"
)

func registryFixture(name string) Function {
	return Function{
		Def: provider.ToolDefinition{Name: name, InputSchema: json.RawMessage(`{"type":"object"}`)},
		Run: func(context.Context, json.RawMessage) (string, error) {
			return name, nil
		},
	}
}

func TestRegistryReplaceWithdrawsAndInstallsAsOneUpdate(t *testing.T) {
	registry := NewRegistry(registryFixture("old"), registryFixture("untouched"))
	registry.Replace([]string{"old"}, registryFixture("new"))

	if _, ok := registry.Get("old"); ok {
		t.Fatal("old tool remained registered")
	}
	if _, ok := registry.Get("new"); !ok {
		t.Fatal("replacement tool was not registered")
	}
	if _, ok := registry.Get("untouched"); !ok {
		t.Fatal("unrelated tool was removed")
	}
}
