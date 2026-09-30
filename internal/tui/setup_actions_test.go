package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robert-mcdermott/collomia/internal/app"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/setup"
)

const actionsConfig = `{"default_provider": "a", "default_model": "claude-opus-4-6", "providers": {
  "a": {"type": "anthropic", "model": "claude-opus-4-6", "temperature": 0.4, "headers": {"Authorization": "Bearer ${T}", "X-Team": "one"}},
  "b": {"type": "anthropic", "model": "claude-sonnet-4-6"}}}`

// actionsModel opens the menu for one provider of a real temporary file.
func actionsModel(t *testing.T, name, active string) setupModel {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(actionsConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	existing, err := setup.ReadExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	m := newSetupModel(SetupOptions{ConfigPath: path, Existing: existing, Embedded: true, ActiveProvider: active, Reconfigure: name})
	m.width, m.height = 100, 40
	if m.stage != stageProviderActions {
		t.Fatalf("stage = %d, want the provider menu", m.stage)
	}
	return m
}

func actionIndex(t *testing.T, m setupModel, key string) int {
	t.Helper()
	for i, action := range m.actions {
		if action.key == key {
			return i
		}
	}
	t.Fatalf("no %s action", key)
	return -1
}

func TestProviderMenuGuardsTheDefaultAndTheActiveProvider(t *testing.T) {
	a := actionsModel(t, "a", "b")
	if !a.actions[actionIndex(t, a, "default")].disabled {
		t.Error("the current default cannot be made the default again")
	}
	if remove := a.actions[actionIndex(t, a, "remove")]; !remove.disabled || !strings.Contains(remove.detail, "default") {
		t.Errorf("the default provider cannot be removed: %+v", remove)
	}
	b := actionsModel(t, "b", "b")
	if remove := b.actions[actionIndex(t, b, "remove")]; !remove.disabled || !strings.Contains(remove.detail, "/model") {
		t.Errorf("the provider this session uses cannot be removed from under it: %+v", remove)
	}
	if b.actions[actionIndex(t, b, "default")].disabled {
		t.Error("another provider can be made the default")
	}
	view := stripANSI(a.View())
	for _, want := range []string{"Change model or re-verify", "Switch model without re-verifying", "Add a model", "temperature 0.4, 2 header(s)"} {
		if !strings.Contains(view, want) {
			t.Errorf("menu must show %q:\n%s", want, view)
		}
	}
}

func choose(t *testing.T, m setupModel, key string) (setupModel, tea.Cmd) {
	t.Helper()
	m.cursor = actionIndex(t, m, key)
	next, cmd := m.onSelect()
	return next.(setupModel), cmd
}

func TestMakeDefaultWritesWithoutARequest(t *testing.T) {
	m, cmd := choose(t, actionsModel(t, "b", "a"), "default")
	if m.stage != stageApplying || cmd == nil {
		t.Fatalf("stage = %d", m.stage)
	}
	next, _ := m.Update(cmd())
	m = next.(setupModel)
	if m.stage != stageDone || !strings.Contains(m.outcome.Summary, "Made b/claude-sonnet-4-6 the default") {
		t.Fatalf("stage %d outcome %+v", m.stage, m.outcome)
	}
	existing, _ := setup.ReadExisting(m.opts.ConfigPath)
	if existing.DefaultProvider != "b" || existing.DefaultModel != "claude-sonnet-4-6" {
		t.Errorf("file defaults = %s/%s", existing.DefaultProvider, existing.DefaultModel)
	}
}

func TestSwitchingModelSendsNothingAndSaysSo(t *testing.T) {
	m, _ := choose(t, actionsModel(t, "a", "b"), "switch")
	if m.stage != stageScanning || m.intent != intentSwitchModel {
		t.Fatalf("stage %d intent %d", m.stage, m.intent)
	}
	next, _ := m.Update(catalogMsg{models: []provider.ModelInfo{{ID: "claude-opus-4-6"}, {ID: "claude-opus-4-7"}}})
	m = next.(setupModel)
	m.cursor = 1
	next, _ = m.onSelect()
	m = next.(setupModel)
	msg := m.verifyCmd()().(verifiedMsg)
	if !msg.verification.Skipped || msg.limits.ContextWindow == 0 {
		t.Fatalf("switching must skip requests but still resolve limits, got %+v", msg)
	}
	next, _ = m.Update(msg)
	m = acceptLimits(t, next.(setupModel))
	view := stripANSI(m.View())
	if !strings.Contains(view, "not re-verified") || !strings.Contains(view, "claude-opus-4-7") {
		t.Errorf("confirmation:\n%s", view)
	}
	if !m.result.MakeDefault {
		t.Error("switching the default provider's model keeps default_model pointing at what it runs")
	}
}

func TestAddingAModelLeavesTheDefaultAlone(t *testing.T) {
	m, _ := choose(t, actionsModel(t, "a", "b"), "add")
	next, _ := m.Update(catalogMsg{models: []provider.ModelInfo{{ID: "claude-opus-4-6"}, {ID: "claude-opus-4-7"}}})
	m = next.(setupModel)
	m.cursor = 1
	next, _ = m.onSelect()
	next, _ = next.(setupModel).Update(verifiedMsg{verification: setup.Verification{OK: true, ToolsOK: true, Reply: "ok"},
		limits: provider.Limits{ContextWindow: 200000, MaxOutput: 32000, ContextSource: provider.LimitsTable, OutputSource: provider.LimitsTable}})
	m = acceptLimits(t, next.(setupModel))
	if !m.result.EntryOnly || m.result.MakeDefault {
		t.Fatalf("result = %+v", m.result)
	}
	view := strings.Join(strings.Fields(stripANSI(m.View())), " ")
	if !strings.Contains(view, "unchanged — adding a model") || !strings.Contains(view, "stays its own model") {
		t.Errorf("confirmation:\n%s", view)
	}
	if toggled := setupPress(t, m, typed("d")); toggled.makeDefault {
		t.Error("d cannot make an added model the default")
	}
}

func TestSettingsFormMasksCredentialHeaders(t *testing.T) {
	m, _ := choose(t, actionsModel(t, "a", "b"), "settings")
	if m.stage != stageSettingsForm {
		t.Fatalf("stage = %d", m.stage)
	}
	var authKind setup.FieldKind = -1
	for _, field := range m.form.spec.Fields {
		if field.Key == "header:Authorization" {
			authKind = field.Kind
		}
	}
	if authKind != setup.FieldSecret {
		t.Error("an Authorization header must not be echoed")
	}
	if strings.Contains(stripANSI(m.View()), "Bearer") {
		t.Error("the credential value must not appear on screen")
	}
}

func TestParseSettings(t *testing.T) {
	edit, problem := parseSettings(map[string]string{"temperature": "0.7", "header:X-Team": "", "header:Keep": "v", "new_header_name": "X-New", "new_header_value": "n"})
	if problem != "" || *edit.Temperature != 0.7 || edit.Headers["Keep"] != "v" || edit.Headers["X-New"] != "n" {
		t.Errorf("edit = %+v problem %q", edit, problem)
	}
	if _, ok := edit.Headers["X-Team"]; ok {
		t.Error("a blanked header is removed")
	}
	for _, bad := range []map[string]string{{"temperature": "3"}, {"temperature": "warm"}, {"new_header_name": "X"}, {"new_header_value": "v"}, {"new_header_name": "Bad Name", "new_header_value": "v"}} {
		if _, problem := parseSettings(bad); problem == "" {
			t.Errorf("%v must be refused", bad)
		}
	}
}

func TestRemovingAProviderAsksFirst(t *testing.T) {
	m, _ := choose(t, actionsModel(t, "b", "a"), "remove")
	if m.stage != stageRemoveConfirm || !strings.Contains(stripANSI(m.View()), "Remove b") {
		t.Fatalf("stage = %d", m.stage)
	}
	if kept := setupPress(t, m, typed("n")); kept.stage != stageProviderActions {
		t.Error("n keeps the provider and returns to its menu")
	}
	next, cmd := m.onKey(typed("y"))
	next, _ = next.(setupModel).Update(cmd())
	m = next.(setupModel)
	if m.stage != stageDone || !m.outcome.Removed {
		t.Fatalf("stage %d outcome %+v", m.stage, m.outcome)
	}
	if existing, _ := setup.ReadExisting(m.opts.ConfigPath); existing.Has("b") {
		t.Error("b must be removed from the file")
	}
}

func TestRenameRefusesAnUnusableName(t *testing.T) {
	m, _ := choose(t, actionsModel(t, "b", "a"), "rename")
	m = setupPress(t, m, typed("a"), enterKey)
	if m.stage != stageRename || !strings.Contains(m.form.err, "already configured") {
		t.Errorf("stage %d err %q", m.stage, m.form.err)
	}
}

func TestDirectEditMessageSaysWhatTheSessionRuns(t *testing.T) {
	_ = appconfig.Provider{}
	message := directEditMessage(SetupOutcome{Summary: "Updated a's temperature and headers."}, appReload("a", "m", true))
	if !strings.Contains(message, "Applied to this session (a/m)") {
		t.Errorf("message = %q", message)
	}
}

func appReload(name, model string, reselected bool) app.ProviderReload {
	return app.ProviderReload{ActiveProvider: name, ActiveModel: model, Reselected: reselected}
}
