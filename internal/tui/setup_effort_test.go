package tui

import (
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/setup"
)

// effortModel is a model that has verified and accepted its limits, and is now
// on the effort screen with the given support.
func effortModel(t *testing.T, support provider.ReasoningSupport, existing setup.Existing) setupModel {
	t.Helper()
	m := newTestSetupModel(t)
	m.opts.Existing = existing
	m.name, m.model = "local", "glm-5.3-flash:cloud"
	m.provider = appconfig.Provider{Type: "openai-compatible", BaseURL: "http://127.0.0.1:11434/v1"}
	next, _ := m.onVerified(verifiedMsg{verification: setup.Verification{OK: true, ToolsOK: true, Reply: "ok"},
		limits:    provider.Limits{ContextWindow: 131072, MaxOutput: 8192, ContextSource: provider.LimitsEndpoint, OutputSource: provider.LimitsTable},
		reasoning: support})
	return setupPress(t, next.(setupModel), enterKey)
}

var glmSupport = provider.ReasoningSupport{Levels: []string{"low", "high", "max"}, Default: "max", Source: provider.LimitsEndpoint}

func TestSetupOffersTheLevelsTheModelAdvertises(t *testing.T) {
	m := effortModel(t, glmSupport, setup.Existing{})
	if m.stage != stageEffort {
		t.Fatalf("stage = %d, want the effort screen after limits", m.stage)
	}
	view := stripANSI(m.View())
	for _, want := range []string{"Reasoning effort", "Model default", "send no effort; the model uses max", "low", "high", "the model's own default", "levels the endpoint says"} {
		if !strings.Contains(view, want) {
			t.Errorf("effort screen must show %q:\n%s", want, view)
		}
	}
	if m.cursor != 0 {
		t.Error("the default row, which writes nothing, must be preselected")
	}
	m = setupPress(t, m, enterKey)
	if m.stage != stageConfirm || !m.result.EffortChosen || m.result.Effort != "" {
		t.Errorf("choosing the default writes no effort; stage %d result %+v", m.stage, m.result)
	}
	if !strings.Contains(stripANSI(m.View()), "model default — nothing sent") {
		t.Error("the confirmation must state the effort decision")
	}
}

func TestSetupChecksAChosenLevelWithTheEndpoint(t *testing.T) {
	m := effortModel(t, glmSupport, setup.Existing{})
	m = setupPress(t, m, downKey, downKey, enterKey) // high
	if m.stage != stageEffortVerifying || m.effortTrying != "high" {
		t.Fatalf("picking a level must check it first; stage %d trying %q", m.stage, m.effortTrying)
	}

	refused, _ := m.Update(effortVerifiedMsg{level: "high", accepted: false, detail: "provider or model rejected the configured reasoning effort"})
	back := refused.(setupModel)
	if back.stage != stageEffort || !strings.Contains(stripANSI(back.View()), "did not accept high") {
		t.Errorf("a refused level returns to the screen with the reason; stage %d", back.stage)
	}

	accepted, _ := m.Update(effortVerifiedMsg{level: "high", accepted: true})
	done := accepted.(setupModel)
	if done.stage != stageConfirm || done.result.Effort != "high" {
		t.Fatalf("an accepted level goes to the confirmation, got stage %d effort %q", done.stage, done.result.Effort)
	}
	if !strings.Contains(stripANSI(done.View()), "high — accepted by the endpoint") {
		t.Error("the confirmation must say the level was accepted")
	}
	reopened := setupPress(t, done, typed("e"))
	if reopened.stage != stageEffort || reopened.effortChoices[reopened.cursor].Level != "high" {
		t.Errorf("e reopens the effort screen on the chosen level; stage %d", reopened.stage)
	}
	if again := setupPress(t, reopened, escKey); again.stage != stageConfirm {
		t.Error("esc from a reopened effort screen returns to the confirmation")
	}
}

func TestSetupIgnoresAStaleEffortCheck(t *testing.T) {
	m := effortModel(t, glmSupport, setup.Existing{})
	m = setupPress(t, m, downKey, enterKey) // low
	m = setupPress(t, m, escKey)            // cancel the check
	stale, _ := m.Update(effortVerifiedMsg{level: "low", accepted: true})
	if stale.(setupModel).stage != stageEffort {
		t.Error("a cancelled check that answers late must not jump to the confirmation")
	}
}

func TestSetupSkipsEffortForAModelWithoutIt(t *testing.T) {
	m := effortModel(t, provider.ReasoningSupport{Source: provider.LimitsEndpoint}, setup.Existing{})
	if m.stage != stageConfirm {
		t.Fatalf("stage = %d; a model with no effort control gets no effort screen", m.stage)
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "not supported by this model") || strings.Contains(view, "e effort") {
		t.Errorf("the confirmation must say so and not offer e:\n%s", view)
	}
}

func TestSetupEffortOpensOnTheModelsConfiguredLevel(t *testing.T) {
	existing := setup.Existing{Providers: []string{"local"}, Models: map[string]string{"local": "glm-5.3-flash:cloud"},
		Definitions: map[string]appconfig.Provider{"local": {Type: "openai-compatible", Model: "glm-5.3-flash:cloud",
			Reasoning: &appconfig.Reasoning{Effort: "high"},
			Models:    map[string]appconfig.ModelSettings{"glm-5.3-flash:cloud": {Reasoning: &appconfig.Reasoning{Effort: "low"}}}}}}
	m := effortModel(t, glmSupport, existing)
	if m.effortChoices[m.cursor].Level != "low" {
		t.Errorf("cursor on %q, want the model's configured low", m.effortChoices[m.cursor].Level)
	}
	if m.effortChoices[0].Label != "Provider setting" {
		t.Error("with a provider-level effort, the first row inherits it and says so")
	}
}
