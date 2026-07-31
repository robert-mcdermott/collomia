package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/setup"
)

func newTestSetupModel(t *testing.T) setupModel {
	t.Helper()
	theme := defaultTheme()
	return setupModel{
		opts:   SetupOptions{ConfigPath: "/tmp/config.json"},
		theme:  theme,
		styles: newStyles(theme),
		spin:   newSetupSpinner(theme),
		input:  newSetupInput(theme),
		width:  100,
		height: 32,
	}
}

func withProbes(m setupModel, probes []setup.Probe) setupModel {
	m.probes = probes
	m.choices = m.buildChoices()
	m.stage = stageChooseProvider
	return m
}

func readyProbe(name string, models ...string) setup.Probe {
	infos := make([]provider.ModelInfo, 0, len(models))
	for _, id := range models {
		infos = append(infos, provider.ModelInfo{ID: id})
	}
	return setup.Probe{
		Candidate: setup.Candidate{Name: name, Key: strings.ToLower(name), BaseURL: "http://127.0.0.1:11434/v1", Type: "openai-compatible"},
		State:     setup.ProbeReady, Models: infos,
	}
}

func absentProbe(name, start string) setup.Probe {
	return setup.Probe{
		Candidate: setup.Candidate{Name: name, Key: strings.ToLower(name), BaseURL: "http://127.0.0.1:1234/v1", Type: "openai-compatible", Start: start},
		State:     setup.ProbeAbsent,
	}
}

func TestSetupCursorNeverStopsOnARowThatDoesNothing(t *testing.T) {
	// A runtime that is not running is shown rather than hidden, because
	// omitting it looks like Collomia does not support it. But under the plain
	// theme a disabled row has no colour to distinguish it, so a cursor that
	// could land there would leave enter doing nothing with no explanation.
	m := withProbes(newTestSetupModel(t), []setup.Probe{
		readyProbe("Ollama", "a", "b"),
		absentProbe("LM Studio", "start it"),
		absentProbe("vLLM", "vllm serve"),
	})

	var disabled int
	for _, choice := range m.choices {
		if choice.disabled {
			disabled++
		}
	}
	if disabled == 0 {
		t.Fatal("fixture must contain unavailable runtimes for this test to mean anything")
	}

	// Walk the whole list in both directions; the cursor must never rest on a
	// disabled row.
	for i := 0; i < len(m.choices)*2; i++ {
		m.cursor = m.moveCursor(1)
		if m.choices[m.cursor].disabled {
			t.Fatalf("cursor stopped on disabled row %d (%s)", m.cursor, m.choices[m.cursor].label)
		}
	}
	for i := 0; i < len(m.choices)*2; i++ {
		m.cursor = m.moveCursor(-1)
		if m.choices[m.cursor].disabled {
			t.Fatalf("cursor stopped on disabled row %d going up (%s)", m.cursor, m.choices[m.cursor].label)
		}
	}
}

func TestSetupListsRunningRuntimesAboveOnesThatAreNot(t *testing.T) {
	// A list that puts a working local model below three hosted APIs the user
	// has no key for makes the easy path look unavailable.
	m := withProbes(newTestSetupModel(t), []setup.Probe{
		absentProbe("LM Studio", "start it"),
		readyProbe("Ollama", "a"),
	})
	if m.choices[0].label != "Ollama" {
		t.Errorf("a running runtime must come first, got %q", m.choices[0].label)
	}
	last := m.choices[len(m.choices)-1]
	if !last.manual {
		t.Errorf("the manual escape hatch must come last, got %q", last.label)
	}
}

func TestSetupAlwaysOffersAWayForwardWithNothingInstalled(t *testing.T) {
	// The worst case for a first run is a machine with no local runtime and no
	// exported key. It must still present selectable options.
	m := withProbes(newTestSetupModel(t), []setup.Probe{
		absentProbe("Ollama", "ollama serve"),
		absentProbe("LM Studio", "start it"),
	})
	selectable := 0
	for _, choice := range m.choices {
		if !choice.disabled {
			selectable++
		}
	}
	if selectable == 0 {
		t.Fatal("the provider list must never be entirely unselectable")
	}
	if m.choices[m.cursor].disabled {
		t.Fatal("the initial cursor must land on a selectable row")
	}
}

func TestSetupFailureNamesTheModelThatWasActuallyTried(t *testing.T) {
	// The verification is the record of what was attempted. Titling the panel
	// from anything else can name a different model than the diagnosis is
	// about, sending the reader to the wrong fix.
	m := newTestSetupModel(t)
	m.stage = stageFailed
	m.model = "stale-selection"
	m.verification = setup.Verification{
		Model:     "qwen3-coder",
		Diagnosis: setup.Diagnosis{Summary: "not found", Fixes: []string{"This endpoint reports: qwen2.5-coder."}},
	}
	view := m.View()
	if !strings.Contains(view, "qwen3-coder") {
		t.Error("the failure panel must name the model that was verified")
	}
	if strings.Contains(view, "stale-selection") {
		t.Error("the failure panel must not name a model that was not tried")
	}
	if !strings.Contains(view, "qwen2.5-coder") {
		t.Error("the diagnosis fixes must be rendered")
	}
	if !strings.Contains(view, "Nothing has been written") {
		t.Error("a failed verification must say that nothing was written")
	}
}

func TestSetupNeverHidesTheWayOut(t *testing.T) {
	// Every screen states its keys. A wizard that hides how to go back is one
	// people ctrl-c out of, which on the credential screen means abandoning a
	// key they just typed.
	m := newTestSetupModel(t)
	stages := []setupStage{
		stageScanning, stageChooseProvider, stageManualURL, stageChooseModel,
		stageManualModel, stageCredential, stageStorage, stageVerifying,
		stageFailed, stageConfirm, stageDone,
	}
	for _, stage := range stages {
		m.stage = stage
		m.choices = []setupChoice{{label: "x"}}
		m.catalog = []provider.ModelInfo{{ID: "m"}}
		footer := m.footer()
		if strings.TrimSpace(stripANSI(footer)) == strings.TrimSpace(stripANSI(m.rule())) {
			t.Errorf("stage %d renders no control hints", stage)
		}
	}
}

func TestSetupRendersWithoutColorUnderThePlainTheme(t *testing.T) {
	// The plain theme carries no colour at all, so selection has to survive on
	// the marker alone.
	plain, _ := themeByName("plain")
	m := newTestSetupModel(t)
	m.theme, m.styles = plain, newStyles(plain)
	m = withProbes(m, []setup.Probe{readyProbe("Ollama", "a", "b")})
	view := m.View()
	if strings.Contains(view, "\x1b[") {
		t.Error("the plain theme must emit no ANSI colour")
	}
	if !strings.Contains(view, "▸") {
		t.Error("without colour, the selection marker is the only cue and must be present")
	}
}

func TestSetupCredentialInputIsNotEchoed(t *testing.T) {
	// A key typed in clear text on a shared screen is the first thing this
	// wizard could get wrong.
	m := newTestSetupModel(t)
	m = withProbes(m, nil)
	for i, choice := range m.choices {
		if choice.hosted != nil {
			m.cursor = i
			break
		}
	}
	// Ensure the selected family has no key in the environment, so the wizard
	// takes the ask-for-it path rather than the already-exported one.
	hosted := m.choices[m.cursor].hosted
	if _, _, ok := hosted.EnvKey(); ok {
		t.Skip("the environment already exports a key for " + hosted.Name)
	}
	next, _ := m.onSelect()
	updated, ok := next.(setupModel)
	if !ok {
		t.Fatal("unexpected model type")
	}
	if updated.stage != stageCredential {
		t.Fatalf("stage = %d, want the credential prompt", updated.stage)
	}
	if updated.input.EchoMode != 1 { // textinput.EchoPassword
		t.Error("the API key field must not echo what is typed")
	}
	if strings.Contains(updated.View(), "Collomia never writes a key into a configuration file") == false {
		t.Error("the credential screen must state where the key will and will not go")
	}
}

func TestSetupConfirmStatesWhatWillBeWritten(t *testing.T) {
	m := newTestSetupModel(t)
	m.stage = stageConfirm
	m.name = "ollama"
	m.model = "qwen2.5-coder"
	m.provider = appconfig.Provider{Type: "openai-compatible", BaseURL: "http://127.0.0.1:11434/v1"}
	m.verification = setup.Verification{OK: true, Reply: "ok"}
	m.result = setup.Build(m.name, m.provider, m.model, setup.CredentialNone, "", "")
	view := stripANSI(m.View())
	for _, want := range []string{"ollama", "qwen2.5-coder", "127.0.0.1:11434", "/tmp/config.json", "none required"} {
		if !strings.Contains(view, want) {
			t.Errorf("confirmation must show %q", want)
		}
	}
}

func TestSetupWarnsBeforeReplacingAnExistingProvider(t *testing.T) {
	m := newTestSetupModel(t)
	m.opts.Existing = []string{"ollama"}
	m.stage = stageConfirm
	m.name = "ollama"
	m.model = "m"
	m.verification = setup.Verification{OK: true, Reply: "ok"}
	m.result = setup.Build("ollama", appconfig.Provider{Type: "openai-compatible", BaseURL: "http://x/v1"}, "m", setup.CredentialNone, "", "")
	if !strings.Contains(stripANSI(m.View()), "replaces the existing provider") {
		t.Error("overwriting a configured provider must be stated before it happens, not after")
	}
}

func TestSetupCtrlCLeavesNothingWritten(t *testing.T) {
	m := newTestSetupModel(t)
	m.stage = stageConfirm
	next, cmd := m.onKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	updated, ok := next.(setupModel)
	if !ok {
		t.Fatal("unexpected model type")
	}
	if !updated.quitting || cmd == nil {
		t.Fatal("ctrl+c must quit from the confirmation screen")
	}
	if updated.outcome.Wrote {
		t.Fatal("quitting must never report a write")
	}
}

// stripANSI removes escape sequences so assertions read against the text a
// user sees rather than against style codes.
func stripANSI(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		out.WriteByte(s[i])
	}
	return out.String()
}
