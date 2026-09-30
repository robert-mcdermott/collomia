package setup

import (
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

func TestProposeLimitsOpensOnWhatThisRunEstablished(t *testing.T) {
	result := Build("local", appconfig.Provider{Type: "openai-compatible"}, "m", CredentialNone, "", "",
		provider.Limits{ContextWindow: 32768, MaxOutput: 4096, ContextSource: provider.LimitsEndpoint, OutputSource: provider.LimitsTable})
	window, output := ProposeLimits(result, appconfig.Provider{})
	if window.Value != 32768 || window.Source != provider.LimitsEndpoint || output.Value != 4096 || output.Source != provider.LimitsTable {
		t.Errorf("proposals = %+v / %+v", window, output)
	}
}

func TestProposeLimitsKeepsAConfiguredLimitForTheSameModel(t *testing.T) {
	// Someone who lowered a window to save memory must not have it raised
	// behind their back because setup ran again. Both numbers are shown.
	result := Build("local", appconfig.Provider{Type: "openai-compatible"}, "m", CredentialNone, "", "",
		provider.Limits{ContextWindow: 131072, MaxOutput: 8192, ContextSource: provider.LimitsEndpoint, OutputSource: provider.LimitsTable})
	window, output := ProposeLimits(result, appconfig.Provider{Model: "m", Context: 16384, MaxTokens: 8192})
	if window.Value != 16384 || window.Source != provider.LimitsConfigured {
		t.Errorf("window = %+v, want the configured value", window)
	}
	if window.Detected != 131072 || window.DetectedSource != provider.LimitsEndpoint {
		t.Errorf("the detected value must be carried for display, got %+v", window)
	}
	if output.Source != provider.LimitsTable {
		t.Errorf("a configured value equal to the detected one keeps the more informative source, got %+v", output)
	}

	// A different model gets its own limits: a window written for one model
	// says nothing about another.
	window, _ = ProposeLimits(result, appconfig.Provider{Model: "other", Context: 16384})
	if window.Value != 131072 {
		t.Errorf("a different model must not inherit the old one's window, got %+v", window)
	}
}

func TestParseLimitsAcceptsBlanksAsTheProposal(t *testing.T) {
	window := LimitProposal{Value: 32768, Source: provider.LimitsAssumed}
	output := LimitProposal{Value: 8192, Source: provider.LimitsAssumed}
	limits, problem := ParseLimits("", "", window, output)
	if problem != "" {
		t.Fatal(problem)
	}
	if limits.ContextWindow != 32768 || limits.ContextSource != provider.LimitsAssumed || limits.OutputSource != provider.LimitsAssumed {
		t.Errorf("accepting an assumption must keep it labelled as one, got %+v", limits)
	}
}

func TestParseLimitsLabelsTypedValuesAsConfigured(t *testing.T) {
	window := LimitProposal{Value: 32768, Source: provider.LimitsEndpoint}
	output := LimitProposal{Value: 8192, Source: provider.LimitsTable}
	limits, problem := ParseLimits("131,072", "8192", window, output)
	if problem != "" {
		t.Fatal(problem)
	}
	if limits.ContextWindow != 131072 || limits.ContextSource != provider.LimitsConfigured {
		t.Errorf("a changed value is the user's own, got %+v", limits)
	}
	if limits.OutputSource != provider.LimitsTable {
		t.Errorf("an unchanged value keeps its source, got %+v", limits)
	}
}

func TestParseLimitsClampsABlankAssumedOutputToATypedWindow(t *testing.T) {
	// The assumed 8192 was never about this model; typing a 4096 window must
	// not be refused because of it.
	limits, problem := ParseLimits("4096", "", LimitProposal{Value: 32768, Source: provider.LimitsAssumed}, LimitProposal{Value: 8192, Source: provider.LimitsAssumed})
	if problem != "" {
		t.Fatal(problem)
	}
	if limits.MaxOutput != 2048 {
		t.Errorf("max output = %d, want half the window", limits.MaxOutput)
	}
}

func TestParseLimitsRefusesWhatTheLoaderWouldRefuse(t *testing.T) {
	window := LimitProposal{Value: 32768, Source: provider.LimitsEndpoint}
	output := LimitProposal{Value: 8192, Source: provider.LimitsTable}
	for _, c := range []struct{ window, output, want string }{
		{"4096", "8192", "at or above context_window"},
		{"lots", "", "whole number of tokens"},
		{"0", "", "whole number of tokens"},
		{"-5", "", "whole number of tokens"},
		{"1310720000", "", "extra digit"},
		{"", "1e3", "whole number of tokens"},
	} {
		_, problem := ParseLimits(c.window, c.output, window, output)
		if !strings.Contains(problem, c.want) {
			t.Errorf("ParseLimits(%q, %q) problem = %q, want it to mention %q", c.window, c.output, problem, c.want)
		}
	}
}

func TestWithLimitsReplacesWhatIsWritten(t *testing.T) {
	result := Build("local", appconfig.Provider{Type: "openai-compatible"}, "m", CredentialNone, "", "", provider.Limits{ContextWindow: 32768, ContextSource: provider.LimitsEndpoint, ModelMaximum: 262144})
	updated := result.WithLimits(provider.Limits{ContextWindow: 65536, MaxOutput: 4096, ContextSource: provider.LimitsConfigured, OutputSource: provider.LimitsConfigured})
	if updated.Provider.Context != 65536 || updated.Provider.MaxTokens != 4096 || updated.ContextAssumed {
		t.Errorf("result = %+v", updated)
	}
	if updated.Limits.ModelMaximum != 262144 {
		t.Error("the model's maximum is a fact about the endpoint and must survive an edit")
	}
}

func TestProposeLimitsReadsTheChosenModelsOwnEntry(t *testing.T) {
	// Re-running setup for a model the provider already has an entry for is
	// a reconfiguration of that model, even though it is not the provider's
	// own model.
	result := Build("local", appconfig.Provider{Type: "openai-compatible"}, "b", CredentialNone, "", "",
		provider.Limits{ContextWindow: 131072, MaxOutput: 8192, ContextSource: provider.LimitsEndpoint, OutputSource: provider.LimitsTable})
	previous := appconfig.Provider{Model: "a", Context: 32768, MaxTokens: 4096,
		Models: map[string]appconfig.ModelSettings{"b": {Context: 65536}}}
	window, output := ProposeLimits(result, previous)
	if window.Value != 65536 || window.Source != provider.LimitsConfigured {
		t.Errorf("window = %+v, want b's own entry", window)
	}
	if output.Value != 8192 || output.Source != provider.LimitsTable {
		t.Errorf("output = %+v; a's provider-level cap is not a configuration of b", output)
	}
}
