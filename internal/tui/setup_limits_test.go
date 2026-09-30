package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/setup"
)

// verifiedModel is a model that has just passed verification for m with the
// given discovered limits.
func verifiedModel(t *testing.T, model string, limits provider.Limits) setupModel {
	t.Helper()
	m := newTestSetupModel(t)
	m.name, m.model = "local", model
	m.provider = appconfig.Provider{Type: "openai-compatible", BaseURL: "http://127.0.0.1:11434/v1"}
	// Effort is marked unsupported so these tests stay about limits; the
	// effort screen has its own tests.
	next, _ := m.onVerified(verifiedMsg{verification: setup.Verification{OK: true, ToolsOK: true, Reply: "ok"}, limits: limits,
		reasoning: provider.ReasoningSupport{Source: provider.LimitsTable}})
	return next.(setupModel)
}

func setupPress(t *testing.T, m setupModel, keys ...tea.KeyMsg) setupModel {
	t.Helper()
	for _, key := range keys {
		next, _ := m.onKey(key)
		m = next.(setupModel)
	}
	return m
}

func typed(text string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)} }

var (
	enterKey = tea.KeyMsg{Type: tea.KeyEnter}
	clearKey = tea.KeyMsg{Type: tea.KeyCtrlU}
	downKey  = tea.KeyMsg{Type: tea.KeyDown}
	escKey   = tea.KeyMsg{Type: tea.KeyEsc}
)

func TestSetupAsksForLimitsItCouldNotDetermine(t *testing.T) {
	// The reported problem: an unrecognized model was written with 32768 and
	// 8192 and the only sign was a line on the confirmation. Now the screen
	// asks, and a blank answer is an explicit acceptance of a stated guess.
	m := verifiedModel(t, "some-unknown-model", provider.Limits{})
	if m.stage != stageLimits {
		t.Fatalf("stage = %d, want the limits screen", m.stage)
	}
	if m.limitsForm.values["context_window"] != "" || m.limitsForm.values["max_tokens"] != "" {
		t.Errorf("an assumed limit must open empty so the screen asks rather than states, got %v", m.limitsForm.values)
	}
	view := stripANSI(m.View())
	for _, want := range []string{"Token limits", "couldn't be determined", "leave it blank to assume 32768"} {
		if !strings.Contains(view, want) {
			t.Errorf("limits screen must say %q:\n%s", want, view)
		}
	}

	confirmed := setupPress(t, m, enterKey)
	if confirmed.stage != stageConfirm {
		t.Fatalf("a blank answer must accept the assumption, problem %q", confirmed.limitsForm.err)
	}
	if confirmed.result.Limits.ContextSource != provider.LimitsAssumed || confirmed.result.Provider.Context != 32768 {
		t.Errorf("result = %+v", confirmed.result.Limits)
	}
	if !strings.Contains(stripANSI(confirmed.View()), "assumed") {
		t.Error("the confirmation must still say the value was assumed")
	}
}

func TestSetupLetsTheUserReplaceADetectedLimit(t *testing.T) {
	m := verifiedModel(t, "qwen3-coder", provider.Limits{ContextWindow: 262144, ContextSource: provider.LimitsEndpoint, MaxOutput: 16384, OutputSource: provider.LimitsTable})
	if m.limitsForm.focus != 0 {
		t.Fatalf("with nothing assumed the cursor starts on the context window, got field %d", m.limitsForm.focus)
	}
	if m.limitsForm.values["context_window"] != "262144" {
		t.Fatalf("a detected window must be filled in, got %q", m.limitsForm.values["context_window"])
	}
	m = setupPress(t, m, clearKey, typed("65,536"), enterKey)
	if m.stage != stageConfirm {
		t.Fatalf("problem: %q", m.limitsForm.err)
	}
	if m.result.Provider.Context != 65536 || m.result.Limits.ContextSource != provider.LimitsConfigured {
		t.Errorf("result = %d (%s), want the typed value labelled configured", m.result.Provider.Context, m.result.Limits.ContextSource)
	}
	if !strings.Contains(stripANSI(m.View()), "context 65536 — as configured") {
		t.Errorf("confirmation:\n%s", stripANSI(m.View()))
	}
}

func TestSetupStartsOnTheLimitItNeedsAnswered(t *testing.T) {
	// A runtime that states its window but no output cap is the commonest
	// case; the cursor goes to the question, not to the answered field.
	m := verifiedModel(t, "some-unknown-model", provider.Limits{ContextWindow: 32768, ContextSource: provider.LimitsEndpoint})
	if m.limitsForm.focus != 1 {
		t.Errorf("focus = %d, want the assumed max output", m.limitsForm.focus)
	}
}

func TestSetupRefusesALimitPairNoRequestCouldSatisfy(t *testing.T) {
	m := verifiedModel(t, "qwen3-coder", provider.Limits{ContextWindow: 32768, ContextSource: provider.LimitsEndpoint, MaxOutput: 16384, OutputSource: provider.LimitsTable})
	m = setupPress(t, m, clearKey, typed("4096"), downKey, clearKey, typed("8192"), enterKey)
	if m.stage != stageLimits {
		t.Fatal("an output cap at or above the window must be refused before anything is written")
	}
	if !strings.Contains(m.limitsForm.err, "at or above context_window") {
		t.Errorf("the refusal must read like the loader's, got %q", m.limitsForm.err)
	}
	if !strings.Contains(stripANSI(m.View()), "at or above context_window") {
		t.Error("the refusal must be on screen")
	}
}

func TestSetupExplainsAWindowTheRuntimeServesSmall(t *testing.T) {
	m := verifiedModel(t, "qwen3.5:9b", provider.Limits{ContextWindow: 4096, ContextSource: provider.LimitsEndpoint, ModelMaximum: 262144})
	view := stripANSI(m.View())
	if !strings.Contains(view, "supports up to 262144") || !strings.Contains(view, "OLLAMA_CONTEXT_LENGTH") {
		t.Errorf("a runtime serving far less than the model supports must say so and say how to fix it:\n%s", view)
	}
}

func TestSetupReconfigureOpensOnTheConfiguredLimit(t *testing.T) {
	m := newTestSetupModel(t)
	m.opts.Existing = setup.Existing{
		Providers:   []string{"local"},
		Models:      map[string]string{"local": "qwen3-coder"},
		Definitions: map[string]appconfig.Provider{"local": {Type: "openai-compatible", Model: "qwen3-coder", Context: 16384, MaxTokens: 4096}},
	}
	m.name, m.model = "local", "qwen3-coder"
	m.provider = appconfig.Provider{Type: "openai-compatible", BaseURL: "http://127.0.0.1:11434/v1"}
	next, _ := m.onVerified(verifiedMsg{verification: setup.Verification{OK: true, Reply: "ok"},
		limits: provider.Limits{ContextWindow: 262144, ContextSource: provider.LimitsEndpoint}})
	m = next.(setupModel)
	if m.limitsForm.values["context_window"] != "16384" {
		t.Errorf("a limit configured for this model must be kept by default, got %q", m.limitsForm.values["context_window"])
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "currently configured") || !strings.Contains(view, "detected 262144") {
		t.Errorf("both the configured and the detected value must be visible:\n%s", view)
	}
}

func TestSetupConfirmationReopensTheLimits(t *testing.T) {
	m := acceptLimits(t, verifiedModel(t, "qwen3-coder", provider.Limits{ContextWindow: 32768, ContextSource: provider.LimitsEndpoint, MaxOutput: 16384, OutputSource: provider.LimitsTable}))
	m = setupPress(t, m, typed("l"))
	if m.stage != stageLimits || m.limitsForm.values["context_window"] != "32768" {
		t.Fatalf("l must reopen the limits on the values about to be written, got stage %d values %v", m.stage, m.limitsForm.values)
	}
	m = setupPress(t, m, clearKey, typed("9999"), escKey)
	if m.stage != stageConfirm || m.result.Provider.Context != 32768 {
		t.Errorf("esc from a reopened screen returns to the confirmation unchanged, got stage %d context %d", m.stage, m.result.Provider.Context)
	}
	if !strings.Contains(stripANSI(m.View()), "l limits") {
		t.Error("the confirmation footer must offer the limits key")
	}
}

func TestSetupConfirmationNamesTheSettingsAnUpdateKeeps(t *testing.T) {
	m := newTestSetupModel(t)
	raw := json.RawMessage(`{"type":"openai-compatible","base_url":"http://127.0.0.1:11434/v1","model":"old","headers":{"X-Team":"a"},"temperature":0.3}`)
	m.opts.Existing = setup.Existing{
		Providers: []string{"local"}, Models: map[string]string{"local": "old"},
		Raw: map[string]json.RawMessage{"local": raw},
	}
	m.stage, m.name, m.model = stageConfirm, "local", "new"
	m.provider = appconfig.Provider{Type: "openai-compatible", BaseURL: "http://127.0.0.1:11434/v1"}
	m.verification = setup.Verification{OK: true, Reply: "ok"}
	m.result = setup.Build(m.name, m.provider, m.model, setup.CredentialNone, "", "", provider.Limits{})
	view := stripANSI(m.View())
	if strings.Contains(view, "replaces the provider") {
		t.Error("the same endpoint is an update, not a replacement")
	}
	for _, want := range []string{"updates the provider named local", "headers, temperature"} {
		if !strings.Contains(strings.Join(strings.Fields(view), " "), want) {
			t.Errorf("confirmation must say %q:\n%s", want, view)
		}
	}
}

func TestSetupReopeningKeepsAnAcceptedAssumptionLabelled(t *testing.T) {
	// A blank assumed output cap follows a typed window (4096 → 2048). Showing
	// 2048 on reopen would turn a value nobody typed into a "configured" one
	// the moment enter was pressed.
	m := verifiedModel(t, "some-unknown-model", provider.Limits{})
	m = setupPress(t, m, typed("4096"), enterKey)
	if m.stage != stageConfirm || m.result.Provider.MaxTokens != 2048 {
		t.Fatalf("stage %d, max output %d; problem %q", m.stage, m.result.Provider.MaxTokens, m.limitsForm.err)
	}
	m = setupPress(t, m, typed("l"), enterKey)
	if m.result.Limits.OutputSource != provider.LimitsAssumed {
		t.Errorf("output source = %q after reopening; an accepted assumption must stay labelled", m.result.Limits.OutputSource)
	}
	if m.result.Limits.ContextSource != provider.LimitsConfigured {
		t.Errorf("the typed window must stay configured, got %q", m.result.Limits.ContextSource)
	}
}

func TestSetupConfirmationSaysTheOldModelKeepsItsLimits(t *testing.T) {
	m := newTestSetupModel(t)
	raw := json.RawMessage(`{"type":"openai-compatible","base_url":"http://127.0.0.1:11434/v1","model":"old","context_window":16384,"max_tokens":2048}`)
	m.opts.Existing = setup.Existing{Providers: []string{"local"}, Models: map[string]string{"local": "old"}, Raw: map[string]json.RawMessage{"local": raw}}
	m.stage, m.name, m.model = stageConfirm, "local", "new"
	m.provider = appconfig.Provider{Type: "openai-compatible", BaseURL: "http://127.0.0.1:11434/v1"}
	m.verification = setup.Verification{OK: true, Reply: "ok"}
	m.result = setup.Build(m.name, m.provider, m.model, setup.CredentialNone, "", "", provider.Limits{})
	view := strings.Join(strings.Fields(stripANSI(m.View())), " ")
	if !strings.Contains(view, "recorded for old move to its own models entry") {
		t.Errorf("confirmation must say where the old model's limits go:\n%s", view)
	}
}
