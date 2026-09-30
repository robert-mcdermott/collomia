package agent

import (
	"context"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// ceilingClient answers once, reporting that the provider stated a smaller
// output ceiling than the request asked for.
type ceilingClient struct{}

func (ceilingClient) Name() string { return "ceiling-fixture" }
func (ceilingClient) Chat(_ context.Context, _ provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
	onDelta(provider.Delta{Warning: "provider rejected max_tokens=100 for model; retrying at its stated ceiling of 64", OutputCeiling: 64})
	onDelta(provider.Delta{Text: "done"})
	return provider.Response{Content: "done"}, nil
}

func TestAgentRemembersAStatedOutputCeiling(t *testing.T) {
	a := New(Options{Client: ceilingClient{}, ProviderName: "fake", Model: "model", ProviderConfig: appconfig.Provider{MaxTokens: 100},
		Workspace: t.TempDir(), Registry: tools.NewRegistry(), Permissions: permission.New(appconfig.Permissions{Mode: "ask"}, nil), MaxIterations: 2})
	if _, _, ceiling := a.LearnedOutputCeiling(); ceiling != 0 {
		t.Fatal("nothing is learned before a provider says anything")
	}
	if _, err := a.Run(t.Context(), "hello", func(event.Event) {}); err != nil {
		t.Fatal(err)
	}
	providerName, model, ceiling := a.LearnedOutputCeiling()
	if providerName != "fake" || model != "model" || ceiling != 64 {
		t.Errorf("learned = %s/%s/%d", providerName, model, ceiling)
	}
}
