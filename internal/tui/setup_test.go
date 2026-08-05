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

	// Ordering contract: everything selectable, then the informational rows for
	// runtimes that are not running. "Something else" is the last selectable
	// entry, because it is the fallback rather than a peer of the named
	// providers.
	lastSelectable := -1
	for i, choice := range m.choices {
		if !choice.disabled {
			lastSelectable = i
		}
	}
	if lastSelectable < 0 {
		t.Fatal("no selectable rows")
	}
	if m.choices[lastSelectable].manual == nil || m.choices[lastSelectable].label != "Something else" {
		t.Errorf("the manual fallback must be the last selectable row, got %q", m.choices[lastSelectable].label)
	}
	for _, choice := range m.choices[lastSelectable+1:] {
		if !choice.disabled {
			t.Error("no selectable row may appear after the informational rows")
		}
	}
}

func TestSetupListsConfiguredProvidersAsReusableActions(t *testing.T) {
	m := newTestSetupModel(t)
	m.opts.Existing = setup.Existing{
		Providers: []string{"bedrock"}, Models: map[string]string{"bedrock": "claude"},
		Definitions: map[string]appconfig.Provider{"bedrock": {Type: "bedrock", Region: "us-west-2", Model: "claude"}},
	}
	m = withProbes(m, []setup.Probe{readyProbe("Ollama", "qwen")})
	if len(m.choices) == 0 || m.choices[0].configured != "bedrock" {
		t.Fatalf("configured provider should be the first reusable action: %+v", m.choices)
	}
	m.cursor = 0
	next, cmd := m.onSelect()
	updated := next.(setupModel)
	if cmd == nil || updated.name != "bedrock" || updated.opts.Reconfigure != "bedrock" || updated.credPlan != setup.CredentialKeep {
		t.Fatalf("configured selection did not enter re-verification: name=%q reconfigure=%q credential=%q", updated.name, updated.opts.Reconfigure, updated.credPlan)
	}
	if updated.provider.Context != 0 || updated.provider.MaxTokens != 0 {
		t.Fatal("reusable setup must re-resolve provider limits")
	}
}

func TestAutomaticSetupCompletionContinuesIntoSession(t *testing.T) {
	m := newTestSetupModel(t)
	m.opts.ContinueToSession = true
	m.stage = stageDone
	m.name, m.model = "local", "model"
	view := stripANSI(m.View())
	if !strings.Contains(view, "continue into your session") || !strings.Contains(view, "enter start session") {
		t.Fatalf("automatic completion does not describe the handoff: %q", view)
	}
	if strings.Contains(view, "first-run") {
		t.Fatalf("reusable setup is still labelled first-run: %q", view)
	}
}

func TestSetupOffersAzureAndBedrockAsForms(t *testing.T) {
	// Neither is configurable from a name and a key: Azure addresses a
	// deployment inside a resource, and Bedrock resolves an identity and grants
	// model access per region. Offering either as a one-line choice produces a
	// selection that dead-ends one screen later.
	m := withProbes(newTestSetupModel(t), nil)
	want := map[string]bool{"azure-openai": false, "azure-foundry": false, "azure-foundry-anthropic": false, "bedrock": false}
	for _, choice := range m.choices {
		if choice.manual != nil {
			if _, ok := want[choice.manual.Key]; ok {
				want[choice.manual.Key] = true
				if len(choice.manual.Fields) == 0 {
					t.Errorf("%s must declare form fields", choice.manual.Key)
				}
			}
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("provider %q is not offered", key)
		}
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
		stageScanning, stageChooseProvider, stageForm, stageChooseModel,
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
	m.result = setup.Build(m.name, m.provider, m.model, setup.CredentialNone, "", "", provider.Limits{})
	view := stripANSI(m.View())
	for _, want := range []string{"ollama", "qwen2.5-coder", "127.0.0.1:11434", "/tmp/config.json", "none required"} {
		if !strings.Contains(view, want) {
			t.Errorf("confirmation must show %q", want)
		}
	}
}

func TestSetupWarnsBeforeReplacingAnExistingProvider(t *testing.T) {
	m := newTestSetupModel(t)
	m.opts.Existing = setup.Existing{Providers: []string{"ollama"}, Models: map[string]string{"ollama": "old-model"}}
	m.stage = stageConfirm
	m.name = "ollama"
	m.model = "m"
	m.verification = setup.Verification{OK: true, Reply: "ok"}
	m.result = setup.Build("ollama", appconfig.Provider{Type: "openai-compatible", BaseURL: "http://x/v1"}, "m", setup.CredentialNone, "", "", provider.Limits{})
	if !strings.Contains(stripANSI(m.View()), "replaces the provider named") {
		t.Error("overwriting a configured provider must be stated before it happens, not after")
	}
}

func TestSetupConfirmReportsBothLimitsAndTheirSource(t *testing.T) {
	// Both numbers used to be decided invisibly — one written from a constant,
	// one never written at all — and the whole value of resolving them is lost
	// if the screen presents an assumption as a measurement.
	m := newTestSetupModel(t)
	m.stage = stageConfirm
	m.name, m.model = "local", "some-unknown-model"
	m.provider = appconfig.Provider{Type: "openai-compatible", BaseURL: "http://127.0.0.1:11434/v1"}
	m.verification = setup.Verification{OK: true, Reply: "ok"}
	m.result = setup.Build(m.name, m.provider, m.model, setup.CredentialNone, "", "", provider.Limits{})
	view := stripANSI(m.View())
	if !strings.Contains(view, "max output") {
		t.Error("the output cap must be shown; it is the field that used to be written by nobody")
	}
	if !strings.Contains(view, "assumed") {
		t.Errorf("a limit nobody established must be labelled: %q", view)
	}

	m.result = setup.Build(m.name, m.provider, m.model, setup.CredentialNone, "", "",
		provider.Limits{ContextWindow: 65536, MaxOutput: 8192, ContextSource: provider.LimitsEndpoint, OutputSource: provider.LimitsEndpoint})
	if got := stripANSI(m.View()); !strings.Contains(got, "reported by the endpoint") {
		t.Errorf("a measured limit must say it was measured: %q", got)
	}
}

func TestSetupReconfigureSkipsTheProviderScan(t *testing.T) {
	// `--provider` names the provider, so scanning for runtimes it never asked
	// about would be a different run from the one requested.
	m := newTestSetupModel(t)
	m.opts.Reconfigure = "bedrock"
	m.opts.Existing = setup.Existing{
		Providers: []string{"bedrock"},
		Models:    map[string]string{"bedrock": "us.anthropic.claude-opus-4-1"},
		Definitions: map[string]appconfig.Provider{
			"bedrock": {Type: "bedrock", Region: "us-west-2", Model: "us.anthropic.claude-opus-4-1", Context: 200000, MaxTokens: 4096},
		},
	}
	target, name, ok := m.reconfigureTarget()
	if !ok || name != "bedrock" {
		t.Fatalf("reconfigure target = %q, ok = %v", name, ok)
	}
	if target.Region != "us-west-2" {
		t.Errorf("the existing definition must be carried in, got %+v", target)
	}
	if target.Context != 0 || target.MaxTokens != 0 {
		t.Error("the old limits must be cleared; re-resolving them is the reason to run this")
	}

	m.name, m.provider = name, target
	if view := stripANSI(m.View()); !strings.Contains(view, "Re-verifying bedrock") {
		t.Errorf("the screen must not claim to be scanning for runtimes: %q", view)
	}
}

func TestSetupReconfigureRejectsAnUnconfiguredName(t *testing.T) {
	m := newTestSetupModel(t)
	m.opts.Reconfigure = "nothing-here"
	if _, _, ok := m.reconfigureTarget(); ok {
		t.Error("a name the file does not contain must not seed the wizard")
	}
}

func TestSetupReconfigureLeavesWithoutAProviderList(t *testing.T) {
	// A run started with --provider never scanned, so its provider list is
	// empty. Going "back" to it would draw a screen with no choices on it and
	// index an empty slice on the next enter.
	m := newTestSetupModel(t)
	m.opts.Reconfigure = "ollama"
	m.name, m.stage = "ollama", stageChooseModel
	m.catalog = []provider.ModelInfo{{ID: "m"}}
	next, cmd := m.onListKey(tea.KeyMsg{Type: tea.KeyEsc})
	updated, ok := next.(setupModel)
	if !ok {
		t.Fatal("unexpected model type")
	}
	if !updated.quitting || cmd == nil {
		t.Error("backing out of the first screen of a --provider run is leaving")
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

func TestSetupDoesNotStealTheDefaultFromAnotherProvider(t *testing.T) {
	// The defect this replaced: MakeDefault was hardcoded true, so adding a
	// second provider silently repointed default_provider at it. Adding
	// Anthropic beside a working Ollama must not change which one runs.
	m := newTestSetupModel(t)
	m.opts.Existing = setup.Existing{
		Providers: []string{"ollama"}, Models: map[string]string{"ollama": "qwen2.5-coder"},
		DefaultProvider: "ollama", DefaultModel: "qwen2.5-coder",
	}
	m.name, m.model = "anthropic", "claude-sonnet-5"
	m.provider = appconfig.Provider{Type: "anthropic", BaseURL: "https://api.anthropic.com"}

	next, _ := m.onVerified(verifiedMsg{verification: setup.Verification{OK: true, ToolsOK: true, Reply: "ok"}})
	updated := next.(setupModel)
	if updated.makeDefault {
		t.Fatal("a new provider must not take the default while another provider holds it")
	}
	if updated.result.MakeDefault {
		t.Fatal("the written result must not repoint default_provider")
	}
	if !strings.Contains(stripANSI(updated.View()), "ollama / qwen2.5-coder stays the default") {
		t.Error("the confirmation must state what stays the default")
	}

	// And it must remain the user's call.
	toggled, _ := updated.onKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if !toggled.(setupModel).makeDefault {
		t.Error("d must toggle the default decision")
	}
}

func TestSetupTakesTheDefaultWhenNothingHoldsIt(t *testing.T) {
	m := newTestSetupModel(t)
	m.name, m.model = "ollama", "qwen2.5-coder"
	m.provider = appconfig.Provider{Type: "openai-compatible", BaseURL: "http://127.0.0.1:11434/v1"}
	next, _ := m.onVerified(verifiedMsg{verification: setup.Verification{OK: true, ToolsOK: true, Reply: "ok"}})
	if !next.(setupModel).makeDefault {
		t.Error("a first provider must become the default")
	}
}

func TestSetupReconfiguringTheDefaultProviderKeepsItDefault(t *testing.T) {
	// Changing the model on the provider that is already default must repoint
	// default_model with it; leaving it aimed at a model this provider may no
	// longer serve would be worse than changing it.
	m := newTestSetupModel(t)
	m.opts.Existing = setup.Existing{
		Providers: []string{"ollama"}, Models: map[string]string{"ollama": "old"},
		DefaultProvider: "ollama", DefaultModel: "old",
	}
	m.name, m.model = "ollama", "new"
	m.provider = appconfig.Provider{Type: "openai-compatible", BaseURL: "http://127.0.0.1:11434/v1"}
	next, _ := m.onVerified(verifiedMsg{verification: setup.Verification{OK: true, ToolsOK: true, Reply: "ok"}})
	if !next.(setupModel).makeDefault {
		t.Error("reconfiguring the current default provider must keep it default")
	}
}

func TestSetupShowsWhatIsAlreadyConfigured(t *testing.T) {
	m := newTestSetupModel(t)
	m.opts.Existing = setup.Existing{
		Providers: []string{"ollama"}, Models: map[string]string{"ollama": "qwen2.5-coder"},
		DefaultProvider: "ollama", DefaultModel: "qwen2.5-coder",
	}
	m = withProbes(m, []setup.Probe{readyProbe("Ollama", "a")})
	view := stripANSI(m.View())
	if !strings.Contains(view, "Currently: ollama / qwen2.5-coder") {
		t.Error("a re-run must show the current selection before asking for a new one")
	}
	if !strings.Contains(view, "configured: qwen2.5-coder") {
		t.Error("a row that would replace an existing provider must say so while choosing")
	}
}

func TestSetupFormCyclesChoicesAndValidatesBeforeLeaving(t *testing.T) {
	var azure setup.Manual
	for _, candidate := range setup.ManualCandidates() {
		if candidate.Key == "azure-openai" {
			azure = candidate
		}
	}
	m := newTestSetupModel(t)
	m.stage = stageForm
	m.form = newSetupForm(azure)
	m.input = m.form.syncInto(m.input)

	// Submitting an incomplete form must refuse with a specific field, not
	// fail later inside an adapter.
	next, _ := m.onFormKey(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(setupModel)
	if updated.stage != stageForm {
		t.Fatal("an incomplete form must not advance")
	}
	if !strings.Contains(updated.form.err, "required") {
		t.Errorf("form error = %q", updated.form.err)
	}

	// The authentication field cycles in place.
	for i, field := range azure.Fields {
		if field.Kind == setup.FieldChoice {
			updated.form.focus = i
		}
	}
	before := updated.form.values["auth"]
	cycled := updated.form.cycle(1)
	if cycled.values["auth"] == before {
		t.Error("a choice field must cycle")
	}
	if !contains(azure.Fields[len(azure.Fields)-1].Options, cycled.values["auth"]) {
		t.Errorf("cycling produced %q, which is not an option", cycled.values["auth"])
	}
}

func TestSetupAzureFormBuildsADeploymentScopedProvider(t *testing.T) {
	var azure setup.Manual
	for _, candidate := range setup.ManualCandidates() {
		if candidate.Key == "azure-openai" {
			azure = candidate
		}
	}
	name, p := azure.Build(map[string]string{
		"base_url": "https://r.openai.azure.com/", "deployment": "my-deploy",
		"api_version": "2024-10-21", "auth": "entra",
	})
	if name != "azure-openai" {
		t.Errorf("name = %q", name)
	}
	if p.Deployment != "my-deploy" || p.APIVersion != "2024-10-21" || p.Auth != "entra" {
		t.Errorf("provider = %+v", p)
	}
	if strings.HasSuffix(p.BaseURL, "/") {
		t.Error("a trailing slash must be trimmed so URL joining stays predictable")
	}
	// entra issues short-lived tokens through DefaultAzureCredential; asking
	// for a key would invite one that is never consulted.
	if azure.NeedsCredential(map[string]string{"auth": "entra"}) {
		t.Error("entra must not ask for an API key")
	}
	if !azure.NeedsCredential(map[string]string{"auth": "api-key"}) {
		t.Error("api-key must ask for a key")
	}
}

func TestSetupBedrockFormRejectsEntraAndSkipsTheKeyForSigV4(t *testing.T) {
	var bedrock setup.Manual
	for _, candidate := range setup.ManualCandidates() {
		if candidate.Key == "bedrock" {
			bedrock = candidate
		}
	}
	if problem := bedrock.Validate(map[string]string{"region": "us-west-2", "auth": "entra"}); problem == "" {
		t.Error("entra is not an AWS authentication mode and must be refused")
	}
	if problem := bedrock.Validate(map[string]string{"auth": "sigv4"}); problem == "" {
		t.Error("a missing region must be refused")
	}
	if bedrock.NeedsCredential(map[string]string{"auth": "sigv4"}) {
		t.Error("the SigV4 chain has nothing to store, so no key should be requested")
	}
	_, p := bedrock.Build(map[string]string{"region": "us-west-2", "auth": "sigv4", "profile": "work"})
	if p.Region != "us-west-2" || p.Profile != "work" || p.Auth != "sigv4" {
		t.Errorf("provider = %+v", p)
	}
}

func TestSetupOmitsImplicitAuthModesFromWhatItWrites(t *testing.T) {
	// A configuration that states a default the adapter would have taken
	// anyway is noise a reader has to check against the reference.
	for _, candidate := range setup.ManualCandidates() {
		switch candidate.Key {
		case "azure-openai":
			if _, p := candidate.Build(map[string]string{"auth": "api-key"}); p.Auth != "" {
				t.Errorf("azure api-key should stay implicit, got %q", p.Auth)
			}
		case "bedrock":
			if _, p := candidate.Build(map[string]string{"auth": "auto"}); p.Auth != "" {
				t.Errorf("bedrock auto should stay implicit, got %q", p.Auth)
			}
		}
	}
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func TestSetupFormDistinguishesSuggestionsFromEnteredValues(t *testing.T) {
	// Colour cannot carry this: the plain theme has none, and a suggested
	// value that reads like an entered one leaves the user unable to tell what
	// they have actually filled in.
	var azure setup.Manual
	for _, candidate := range setup.ManualCandidates() {
		if candidate.Key == "azure-openai" {
			azure = candidate
		}
	}
	plain, _ := themeByName("plain")
	m := newTestSetupModel(t)
	m.theme, m.styles = plain, newStyles(plain)
	m.stage = stageForm
	m.form = newSetupForm(azure)
	m.form.values["base_url"] = "https://real.openai.azure.com"
	m.form.focus = 1
	m.input = m.form.syncInto(m.input)

	view := stripANSI(m.View())
	if !strings.Contains(view, "https://real.openai.azure.com") {
		t.Error("an entered value must be shown")
	}
	if strings.Contains(view, "e.g. https://real.openai.azure.com") {
		t.Error("an entered value must not be labelled as a suggestion")
	}
	// api_version carries a real default, so it is entered, not suggested.
	if strings.Contains(view, "e.g. 2024-10-21") {
		t.Error("a field with a default holds a value, not a suggestion")
	}
}

func TestSetupCredentialFieldHasNoLengthLimit(t *testing.T) {
	// The defect behind a real Bedrock failure. bubbles truncates silently on
	// both SetValue and paste when CharLimit > 0, and a Bedrock short-term API
	// key is base64-encoded session credentials that runs well past any limit
	// worth setting. The truncated key decoded to a structure with fields
	// missing, and AWS answered "Missing required parameters in the API Key" —
	// a correct key, reported as a malformed one.
	in := newSetupInput(defaultTheme())
	if in.CharLimit != 0 {
		t.Fatalf("CharLimit = %d; a credential field must not truncate", in.CharLimit)
	}
	long := strings.Repeat("A", 4096)
	in.SetValue(long)
	if got := in.Value(); len(got) != len(long) {
		t.Errorf("a %d-character credential was truncated to %d", len(long), len(got))
	}
}

func TestSetupCredentialScreenShowsLengthSoTruncationIsVisible(t *testing.T) {
	// A field that does not echo gives no other feedback that a paste arrived
	// short. A length is not a secret.
	m := newTestSetupModel(t)
	m.stage = stageCredential
	m.input.SetValue(strings.Repeat("x", 137))
	view := stripANSI(m.View())
	if !strings.Contains(view, "137 characters") {
		t.Error("the credential screen must show how much was entered")
	}
	if strings.Contains(view, strings.Repeat("x", 20)) {
		t.Error("the key itself must never be rendered")
	}
}

func TestSetupSanitizesAPastedCredential(t *testing.T) {
	m := newTestSetupModel(t)
	m.stage = stageCredential
	m.provider = appconfig.Provider{Type: "bedrock", Region: "us-east-1"}
	m.form = setupForm{spec: setup.Manual{Type: "bedrock"}}
	m.input.SetValue("  \"ABSK\nsecret123\"  ")

	next, _ := m.onSubmit()
	updated := next.(setupModel)
	if updated.secret != "ABSKsecret123" {
		t.Errorf("secret = %q; quotes and embedded whitespace must be removed", updated.secret)
	}
	if updated.provider.APIKey != "ABSKsecret123" {
		t.Errorf("the sanitized value must be what verification uses, got %q", updated.provider.APIKey)
	}
}
