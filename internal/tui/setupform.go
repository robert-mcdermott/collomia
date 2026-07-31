package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/robert-mcdermott/collomia/internal/setup"
)

// setupForm is the multi-field screen for providers that cannot be configured
// from a name and a key.
//
// It keeps one shared textinput rather than one per field, syncing on every
// focus change. A dozen live textinputs would each hold their own cursor state
// and blink independently, which is a lot of moving parts for a form whose
// fields are a URL and a deployment name.
type setupForm struct {
	spec   setup.Manual
	values map[string]string
	focus  int
	err    string
}

func newSetupForm(spec setup.Manual) setupForm {
	values := map[string]string{}
	for _, field := range spec.Fields {
		values[field.Key] = field.Default
		if field.Kind == setup.FieldChoice && field.Default == "" && len(field.Options) > 0 {
			values[field.Key] = field.Options[0]
		}
	}
	return setupForm{spec: spec, values: values}
}

func (f setupForm) current() setup.Field { return f.spec.Fields[f.focus] }

// syncInto loads the focused field's value into the shared input, configuring
// echo and placeholder for that field.
func (f setupForm) syncInto(in textinput.Model) textinput.Model {
	field := f.current()
	in.SetValue(f.values[field.Key])
	in.Placeholder = field.Placeholder
	in.EchoMode = textinput.EchoNormal
	if field.Kind == setup.FieldSecret {
		in.EchoMode = textinput.EchoPassword
	}
	in.CursorEnd()
	return in
}

// capture stores what is currently typed back onto the focused field.
func (f setupForm) capture(in textinput.Model) setupForm {
	if f.current().Kind != setup.FieldChoice {
		f.values[f.current().Key] = in.Value()
	}
	return f
}

func (f setupForm) move(step int) setupForm {
	count := len(f.spec.Fields)
	if count == 0 {
		return f
	}
	f.focus = (f.focus + step + count) % count
	return f
}

// cycle advances a choice field. Choice fields are cycled in place rather than
// opening a sub-list, so the whole provider stays visible on one screen while
// the authentication mode is chosen.
func (f setupForm) cycle(step int) setupForm {
	field := f.current()
	if field.Kind != setup.FieldChoice || len(field.Options) == 0 {
		return f
	}
	index := 0
	for i, option := range field.Options {
		if option == f.values[field.Key] {
			index = i
		}
	}
	index = (index + step + len(field.Options)) % len(field.Options)
	f.values[field.Key] = field.Options[index]
	return f
}

// onFormKey handles the form screen.
func (m setupModel) onFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.input.Blur()
		m.stage, m.cursor = stageChooseProvider, 0
		return m, nil
	case "up", "shift+tab":
		m.form = m.form.capture(m.input).move(-1)
		m.input = m.form.syncInto(m.input)
		return m, nil
	case "down", "tab":
		m.form = m.form.capture(m.input).move(1)
		m.input = m.form.syncInto(m.input)
		return m, nil
	case "left":
		if m.form.current().Kind == setup.FieldChoice {
			m.form = m.form.cycle(-1)
			return m, nil
		}
	case "right":
		if m.form.current().Kind == setup.FieldChoice {
			m.form = m.form.cycle(1)
			return m, nil
		}
	case "enter":
		m.form = m.form.capture(m.input)
		if problem := m.form.spec.Validate(m.form.values); problem != "" {
			m.form.err = problem
			return m, nil
		}
		m.form.err = ""
		return m.onFormComplete()
	}
	if m.form.current().Kind == setup.FieldChoice {
		// Typing into a choice field would silently do nothing; ignoring the
		// key keeps the shared input from drifting out of step with the value
		// actually stored for this field.
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.form = m.form.capture(m.input)
	return m, cmd
}

// onFormComplete turns a validated form into a provider and routes to whatever
// the provider still needs: a credential, a catalog, or a typed model.
func (m setupModel) onFormComplete() (tea.Model, tea.Cmd) {
	name, p := m.form.spec.Build(m.form.values)
	m.name, m.provider = name, p
	m.input.Blur()

	if !m.form.spec.NeedsCredential(m.form.values) {
		m.credPlan, m.envVar, m.secret = setup.CredentialNone, "", ""
		return m.afterCredential()
	}
	// An already-exported variable is still preferred over asking, even here.
	if variable, value, ok := manualEnvCredential(m.form.spec); ok {
		cleaned := setup.SanitizeSecret(value)
		m.secret, m.envVar, m.credPlan = cleaned, variable, setup.CredentialEnv
		m.provider.APIKey = cleaned
		return m.afterCredential()
	}
	m.envVar = manualEnvVar(m.form.spec)
	m.stage = stageCredential
	m.input.SetValue("")
	m.input.Placeholder = "API key"
	m.input.EchoMode = textinput.EchoPassword
	m.input.Focus()
	return m, textinput.Blink
}

// afterCredential runs the step that follows having (or not needing) a key:
// discovery where the provider publishes a catalog, a typed model where it does
// not, and the AWS identity report alongside either for Bedrock.
func (m setupModel) afterCredential() (tea.Model, tea.Cmd) {
	cmds := []tea.Cmd{m.spin.Tick}
	if m.provider.Type == "bedrock" {
		cmds = append(cmds, m.awsIdentityCmd())
	}
	if m.form.spec.Discovers {
		m.stage = stageScanning
		cmds = append(cmds, m.discoverCmd(m.name, m.provider))
		return m, tea.Batch(cmds...)
	}
	m.stage = stageManualModel
	m.input.SetValue("")
	m.input.Placeholder = manualModelPlaceholder(m.provider.Type)
	m.input.EchoMode = textinput.EchoNormal
	m.input.Focus()
	cmds = append(cmds, textinput.Blink)
	return m, tea.Batch(cmds...)
}

func manualModelPlaceholder(providerType string) string {
	switch providerType {
	case "bedrock":
		return "anthropic.claude-sonnet-4-20250514-v1:0"
	case "azure-openai":
		return "the deployment's underlying model, or the deployment name"
	}
	return "model identifier"
}

// manualEnvVar delegates to the one definition, which the documentation guard
// also reads. A second copy here is how the wizard would start honoring a
// variable the guide does not mention.
func manualEnvVar(spec setup.Manual) string { return setup.ManualEnvVar(spec) }

func manualEnvCredential(spec setup.Manual) (variable, value string, ok bool) {
	name := manualEnvVar(spec)
	if name == "" {
		return "", "", false
	}
	if v := strings.TrimSpace(osGetenv(name)); v != "" {
		return name, v, true
	}
	return "", "", false
}
