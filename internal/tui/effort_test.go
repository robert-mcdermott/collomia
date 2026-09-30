package tui

import (
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/app"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

func TestEffortPanelSaysWhatDecidedTheEffort(t *testing.T) {
	panel := renderEffortStatus(app.EffortStatus{Provider: "ollama", Model: "glm-5.3-flash:cloud", Effective: "high",
		Source: "set with /effort for this session", Override: true,
		Support: provider.ReasoningSupport{Levels: []string{"low", "high", "max"}, Default: "max", Source: provider.LimitsEndpoint}})
	for _, want := range []string{"ollama/glm-5.3-flash:cloud", "high", "set with /effort", "low, high, max (default max) — reported by the endpoint", "/effort reset", "collo setup --provider ollama"} {
		if !strings.Contains(panel, want) {
			t.Errorf("panel must show %q:\n%s", want, panel)
		}
	}
	if !strings.Contains(renderEffortStatus(app.EffortStatus{}), "none sent — the model decides") {
		t.Error("no effort must read as the model deciding, not as a missing value")
	}
}

func TestEffortChangeMessageNamesWhatItOutranks(t *testing.T) {
	message := effortChangeMessage(app.EffortStatus{Model: "m", Effective: "low", Override: true, Support: provider.ReasoningSupport{Source: provider.LimitsTable, Levels: []string{"low"}}})
	if !strings.Contains(message, "outranks configuration and agent profiles until /effort reset") {
		t.Errorf("message = %q", message)
	}
	if untested := effortChangeMessage(app.EffortStatus{Model: "m", Effective: "low", Override: true}); !strings.Contains(untested, "may ignore or refuse") {
		t.Errorf("an unpublished model must be warned about, got %q", untested)
	}
}
