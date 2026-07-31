package tui

import (
	"context"

	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/credstore"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/setup"
)

// The setup wizard is a separate Bubble Tea program from the session model,
// not a mode inside it. The session model assumes a runtime, a provider, and a
// session store — the three things the wizard exists because the user does not
// have yet. It shares this package for the theme, the wordmark, and the panel
// vocabulary, so it looks like Collomia rather than like a generic form.

// SetupOptions configures one wizard run.
type SetupOptions struct {
	// ConfigPath is the file that will be written.
	ConfigPath string
	// ThemeName selects the palette; empty uses the default, and NO_COLOR
	// still forces the plain theme through the same path the session uses.
	ThemeName string
	// Existing is the currently configured provider names, used to warn before
	// overwriting one rather than after.
	Existing []string
}

// SetupOutcome reports what a completed wizard did, so the caller can print a
// closing line outside the alternate screen.
type SetupOutcome struct {
	Wrote      bool
	Result     setup.Result
	ConfigPath string
}

type setupStage int

const (
	stageScanning setupStage = iota
	stageChooseProvider
	stageManualURL
	stageChooseModel
	stageManualModel
	stageCredential
	stageStorage
	stageVerifying
	stageFailed
	stageConfirm
	stageDone
)

// setupChoice is one selectable line on the provider screen.
type setupChoice struct {
	label    string
	detail   string
	disabled bool
	local    *setup.Probe
	hosted   *setup.Hosted
	manual   bool
}

type setupModel struct {
	opts   SetupOptions
	theme  Theme
	styles styles
	width  int
	height int

	stage   setupStage
	spin    spinner.Model
	input   textinput.Model
	choices []setupChoice
	cursor  int

	probes  []setup.Probe
	catalog []provider.ModelInfo

	name     string
	provider appconfig.Provider
	model    string
	secret   string
	envVar   string
	credPlan setup.CredentialPlan

	verification setup.Verification
	result       setup.Result
	outcome      SetupOutcome
	err          error
	quitting     bool
}

type probesDoneMsg struct{ probes []setup.Probe }
type catalogMsg struct {
	models []provider.ModelInfo
	err    error
}
type verifiedMsg struct{ verification setup.Verification }
type wroteMsg struct{ err error }

// RunSetup runs the interactive first-run wizard and reports what it wrote.
func RunSetup(ctx context.Context, opts SetupOptions) (SetupOutcome, error) {
	theme := resolveTheme(opts.ThemeName)
	m := setupModel{
		opts:   opts,
		theme:  theme,
		styles: newStyles(theme),
		stage:  stageScanning,
		spin:   newSetupSpinner(theme),
		input:  newSetupInput(theme),
		width:  80,
		height: 24,
	}
	program := tea.NewProgram(m, tea.WithContext(ctx), tea.WithAltScreen())
	final, err := program.Run()
	if err != nil {
		return SetupOutcome{}, err
	}
	done, ok := final.(setupModel)
	if !ok {
		return SetupOutcome{}, nil
	}
	return done.outcome, done.err
}

func newSetupSpinner(t Theme) spinner.Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	if !t.plain() {
		s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent))
	}
	return s
}

func newSetupInput(t Theme) textinput.Model {
	in := textinput.New()
	in.Prompt = "› "
	in.CharLimit = 512
	if !t.plain() {
		in.PromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Primary))
		in.Cursor.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent))
	}
	return in
}

func (m setupModel) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, scanCmd())
}

func scanCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return probesDoneMsg{probes: setup.ProbeLocal(ctx, setup.LocalCandidates())}
	}
}

func (m setupModel) discoverCmd(name string, p appconfig.Provider) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		models, err := setup.Discover(ctx, name, p)
		return catalogMsg{models: models, err: err}
	}
}

func (m setupModel) verifyCmd() tea.Cmd {
	name, p, model, catalog := m.name, m.provider, m.model, m.catalog
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return verifiedMsg{verification: setup.Verify(ctx, name, p, model, catalog)}
	}
}

func (m setupModel) writeCmd() tea.Cmd {
	path, result := m.opts.ConfigPath, m.result
	return func() tea.Msg { return wroteMsg{err: setup.Apply(path, result)} }
}

func (m setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = min(m.contentWidth()-4, 72)
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case probesDoneMsg:
		m.probes = msg.probes
		m.choices = m.buildChoices()
		m.stage = stageChooseProvider
		return m, nil
	case catalogMsg:
		return m.onCatalog(msg)
	case verifiedMsg:
		return m.onVerified(msg)
	case wroteMsg:
		if msg.err != nil {
			m.err = msg.err
			m.quitting = true
			return m, tea.Quit
		}
		m.outcome = SetupOutcome{Wrote: true, Result: m.result, ConfigPath: m.opts.ConfigPath}
		m.stage = stageDone
		return m, nil
	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m setupModel) onCatalog(msg catalogMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		// No catalog is not a failure. Bedrock publishes none at all, and a
		// gateway may withhold one; the model is typed instead of chosen, and
		// the verification step still has to pass either way.
		m.stage = stageManualModel
		m.input.SetValue("")
		m.input.Placeholder = "model identifier"
		m.input.EchoMode = textinput.EchoNormal
		m.input.Focus()
		return m, textinput.Blink
	}
	m.catalog = msg.models
	if len(m.catalog) == 0 {
		m.stage = stageManualModel
		m.input.SetValue("")
		m.input.Placeholder = "model identifier"
		m.input.EchoMode = textinput.EchoNormal
		m.input.Focus()
		return m, textinput.Blink
	}
	m.stage, m.cursor = stageChooseModel, 0
	return m, nil
}

func (m setupModel) onVerified(msg verifiedMsg) (tea.Model, tea.Cmd) {
	m.verification = msg.verification
	if !msg.verification.OK {
		m.stage = stageFailed
		return m, nil
	}
	m.result = setup.Build(m.name, m.provider, m.model, m.credPlan, m.envVar, m.secret)
	m.result.MakeDefault = true
	m.stage = stageConfirm
	return m, nil
}

func (m setupModel) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	}

	switch m.stage {
	case stageChooseProvider, stageChooseModel, stageStorage:
		return m.onListKey(msg)
	case stageManualURL, stageManualModel, stageCredential:
		return m.onInputKey(msg)
	case stageFailed:
		switch msg.String() {
		case "r":
			m.stage = stageVerifying
			return m, tea.Batch(m.spin.Tick, m.verifyCmd())
		case "b", "esc":
			m.stage, m.cursor = stageChooseProvider, 0
			return m, nil
		case "q":
			m.quitting = true
			return m, tea.Quit
		}
	case stageConfirm:
		switch msg.String() {
		case "enter", "y":
			return m, m.writeCmd()
		case "esc", "b":
			m.stage, m.cursor = stageChooseProvider, 0
			return m, nil
		case "q":
			m.quitting = true
			return m, tea.Quit
		}
	case stageDone:
		return m, tea.Quit
	case stageScanning, stageVerifying:
		if msg.String() == "esc" {
			m.quitting = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m setupModel) onListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.cursor = m.moveCursor(-1)
		return m, nil
	case "down", "j":
		m.cursor = m.moveCursor(1)
		return m, nil
	case "esc":
		if m.stage == stageChooseProvider {
			m.quitting = true
			return m, tea.Quit
		}
		m.stage, m.cursor = stageChooseProvider, 0
		return m, nil
	case "enter":
		return m.onSelect()
	}
	return m, nil
}

func (m setupModel) onInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.input.Blur()
		m.stage, m.cursor = stageChooseProvider, 0
		return m, nil
	case "enter":
		return m.onSubmit()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m setupModel) onSelect() (tea.Model, tea.Cmd) {
	switch m.stage {
	case stageChooseProvider:
		choice := m.choices[m.cursor]
		if choice.disabled {
			return m, nil
		}
		switch {
		case choice.local != nil:
			m.name = choice.local.Candidate.Key
			m.provider = appconfig.Provider{Type: choice.local.Candidate.Type, BaseURL: choice.local.Candidate.BaseURL}
			m.credPlan, m.envVar, m.secret = setup.CredentialNone, "", ""
			m.catalog = choice.local.Models
			if len(m.catalog) == 0 {
				m.stage = stageManualModel
				m.input.SetValue("")
				m.input.Placeholder = "model identifier"
				m.input.EchoMode = textinput.EchoNormal
				m.input.Focus()
				return m, textinput.Blink
			}
			m.stage, m.cursor = stageChooseModel, 0
			return m, nil
		case choice.hosted != nil:
			hosted := *choice.hosted
			m.name = hosted.Key
			m.provider = appconfig.Provider{Type: hosted.Type, BaseURL: hosted.BaseURL}
			if key, variable, ok := hosted.EnvKey(); ok {
				// The environment already has it. This is the path where the
				// wizard never touches a secret at all: it records the
				// variable name and moves on.
				m.secret, m.envVar, m.credPlan = key, variable, setup.CredentialEnv
				m.provider.APIKey = key
				m.stage = stageScanning
				return m, tea.Batch(m.spin.Tick, m.discoverCmd(m.name, m.provider))
			}
			m.envVar = hosted.EnvVar
			m.stage = stageCredential
			m.input.SetValue("")
			m.input.Placeholder = hosted.KeyHint
			m.input.EchoMode = textinput.EchoPassword
			m.input.Focus()
			return m, textinput.Blink
		case choice.manual:
			m.name = "custom"
			m.provider = appconfig.Provider{Type: "openai-compatible"}
			m.stage = stageManualURL
			m.input.SetValue("http://")
			m.input.Placeholder = "http://host:port/v1"
			m.input.EchoMode = textinput.EchoNormal
			m.input.Focus()
			return m, textinput.Blink
		}
	case stageChooseModel:
		m.model = m.catalog[m.cursor].ID
		m.stage = stageVerifying
		return m, tea.Batch(m.spin.Tick, m.verifyCmd())
	case stageStorage:
		if m.cursor == 0 && credstore.Available() {
			m.credPlan = setup.CredentialStore
		} else {
			m.credPlan = setup.CredentialManual
		}
		m.stage = stageScanning
		return m, tea.Batch(m.spin.Tick, m.discoverCmd(m.name, m.provider))
	}
	return m, nil
}

func (m setupModel) onSubmit() (tea.Model, tea.Cmd) {
	value := strings.TrimSpace(m.input.Value())
	if value == "" {
		return m, nil
	}
	switch m.stage {
	case stageManualURL:
		m.provider.BaseURL = value
		m.input.Blur()
		m.stage = stageScanning
		return m, tea.Batch(m.spin.Tick, m.discoverCmd(m.name, m.provider))
	case stageManualModel:
		m.model = value
		m.input.Blur()
		m.stage = stageVerifying
		return m, tea.Batch(m.spin.Tick, m.verifyCmd())
	case stageCredential:
		m.secret = value
		m.provider.APIKey = value
		m.input.Blur()
		m.stage, m.cursor = stageStorage, 0
		return m, nil
	}
	return m, nil
}

func (m setupModel) listLength() int {
	switch m.stage {
	case stageChooseProvider:
		return len(m.choices)
	case stageChooseModel:
		return len(m.catalog)
	case stageStorage:
		// One option where there is no credential store, so the cursor cannot
		// sit on a choice that does not exist.
		if credstore.Available() {
			return 2
		}
		return 1
	}
	return 0
}

// moveCursor steps by one, skipping rows that cannot be chosen and wrapping at
// both ends.
//
// A runtime that is not running is shown rather than hidden — "LM Studio: not
// running" is information, and a list that silently omits it looks like
// Collomia does not support it. But a row the cursor can land on where enter
// does nothing is worse than either, and under the plain theme a disabled row
// has no colour to distinguish it, so skipping is what makes showing it safe.
func (m setupModel) moveCursor(step int) int {
	length := m.listLength()
	if length == 0 {
		return 0
	}
	index := m.cursor
	for i := 0; i < length; i++ {
		index = (index + step + length) % length
		if !m.disabledAt(index) {
			return index
		}
	}
	return m.cursor
}

// disabledAt reports whether a row is present for information only. Only the
// provider list has such rows; the model and storage lists are all selectable.
func (m setupModel) disabledAt(index int) bool {
	if m.stage != stageChooseProvider || index < 0 || index >= len(m.choices) {
		return false
	}
	return m.choices[index].disabled
}

// buildChoices orders the provider list by what the machine actually answered:
// runtimes that are running come first, then hosted families whose credential
// is already present, then the rest. A list that puts a working local model
// below a hosted API the user has no key for is a list that makes the easy
// path look unavailable.
func (m setupModel) buildChoices() []setupChoice {
	choices := make([]setupChoice, 0, len(m.probes)+len(setup.HostedCandidates())+1)
	for i := range m.probes {
		probe := m.probes[i]
		if probe.State == setup.ProbeReady && len(probe.Models) > 0 {
			choices = append(choices, setupChoice{
				label: probe.Candidate.Name, detail: probe.Detail(), local: &m.probes[i],
			})
		}
	}
	for _, hosted := range setup.HostedCandidates() {
		entry := hosted
		detail := "needs an API key"
		if _, variable, ok := hosted.EnvKey(); ok {
			detail = "$" + variable + " is already set"
		}
		choices = append(choices, setupChoice{label: hosted.Name, detail: detail, hosted: &entry})
	}
	for i := range m.probes {
		probe := m.probes[i]
		if probe.State != setup.ProbeReady || len(probe.Models) == 0 {
			choices = append(choices, setupChoice{
				label: probe.Candidate.Name, detail: probe.Detail(), disabled: true,
			})
		}
	}
	choices = append(choices, setupChoice{
		label:  "Something else",
		detail: "any OpenAI-compatible endpoint — you supply the URL",
		manual: true,
	})
	return choices
}
