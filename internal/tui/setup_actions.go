package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/credstore"
	"github.com/robert-mcdermott/collomia/internal/setup"
)

// providerAction is one row of a configured provider's menu.
type providerAction struct {
	key      string
	label    string
	detail   string
	disabled bool
}

// directEditMsg reports an edit written without verification.
type directEditMsg struct {
	summary     string
	name        string
	model       string
	renamedFrom string
	removed     bool
	err         error
}

// definition is the configured provider as the file records it, unexpanded.
func (m setupModel) definition() appconfig.Provider { return m.opts.Existing.Definitions[m.name] }

// enterActions opens the menu for the configured provider in m.name.
func (m setupModel) enterActions() setupModel {
	m.actions = m.providerActions()
	m.stage, m.cursor, m.intent, m.form.err = stageProviderActions, 0, intentReverify, ""
	return m
}

func (m setupModel) providerActions() []providerAction {
	current := m.definition()
	model := orDash(current.Model)
	isDefault := m.opts.Existing.DefaultProvider == m.name
	settings := "temperature " + temperatureText(current.Temperature)
	if n := len(current.Headers); n > 0 {
		settings += fmt.Sprintf(", %d header(s)", n)
	}
	actions := []providerAction{
		{key: "reverify", label: "Change model or re-verify", detail: "choose a model and prove it with two requests"},
		{key: "default", label: "Make default", detail: "sessions start with " + m.name + "/" + model},
		{key: "switch", label: "Switch model without re-verifying", detail: "pick another model it serves; no requests are sent"},
		{key: "add", label: "Add a model", detail: "verify another model and save its limits and effort; " + model + " stays its model"},
		{key: "connection", label: "Edit connection", detail: "endpoint and authentication, then re-verify; other settings are kept"},
		{key: "settings", label: "Edit temperature and headers", detail: settings},
		{key: "rename", label: "Rename", detail: "a stored key moves with it"},
		{key: "remove", label: "Remove", detail: "delete it from " + m.opts.ConfigPath},
	}
	for i := range actions {
		switch actions[i].key {
		case "default":
			if isDefault && m.opts.Existing.DefaultModel == current.Model {
				actions[i].detail, actions[i].disabled = "already the default", true
			}
		case "remove":
			switch {
			case isDefault:
				actions[i].detail, actions[i].disabled = "it is the default; make another provider the default first", true
			case m.opts.Embedded && m.opts.ActiveProvider == m.name:
				actions[i].detail, actions[i].disabled = "this session is using it; switch with /model first", true
			}
		}
	}
	return actions
}

func temperatureText(value *float64) string {
	if value == nil {
		return "provider default"
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}

// onAction runs the chosen menu row.
func (m setupModel) onAction() (tea.Model, tea.Cmd) {
	action := m.actions[m.cursor]
	if action.disabled {
		return m, nil
	}
	switch action.key {
	case "reverify", "switch", "add":
		m.intent = map[string]setupIntent{"reverify": intentReverify, "switch": intentSwitchModel, "add": intentAddModel}[action.key]
		m.stage = stageScanning
		return m, tea.Batch(m.spin.Tick, m.discoverCmd(m.name, m.provider))
	case "default":
		name, model, path := m.name, m.definition().Model, m.opts.ConfigPath
		m.stage = stageApplying
		return m, func() tea.Msg {
			return directEditMsg{name: name, model: model, err: setup.SetDefault(path, name, model),
				summary: "Made " + name + "/" + model + " the default."}
		}
	case "connection":
		m.form = newConnectionForm(m.definition())
		m.stage = stageConnectionForm
	case "settings":
		m.form = newSettingsForm(m.definition())
		m.stage = stageSettingsForm
	case "rename":
		m.stage = stageRename
		m.input.SetValue("")
		m.input.Placeholder = "new name"
		m.input.EchoMode = textinput.EchoNormal
		m.form.err = ""
		m.input.Focus()
		return m, textinput.Blink
	case "remove":
		m.stage = stageRemoveConfirm
		return m, nil
	}
	m.input = m.form.syncInto(m.input)
	m.input.Focus()
	return m, textinput.Blink
}

// newConnectionForm lays out the identity fields this provider type uses,
// filled with the file's own values, references and all.
func newConnectionForm(p appconfig.Provider) setupForm {
	var fields []setup.Field
	text := func(key, label, value, hint string) setup.Field {
		return setup.Field{Key: key, Label: label, Default: value, Hint: hint, Optional: key != "base_url"}
	}
	choice := func(key, label, value string, options []string) setup.Field {
		if value == "" {
			value = options[0]
		}
		return setup.Field{Key: key, Label: label, Kind: setup.FieldChoice, Options: options, Default: value}
	}
	switch p.Type {
	case "bedrock":
		fields = []setup.Field{
			text("region", "Region", p.Region, "AWS region, such as us-west-2"),
			text("profile", "Profile", p.Profile, "named AWS profile for SigV4; blank uses the default chain"),
			choice("auth", "Auth", p.Auth, []string{"auto", "sigv4", "bearer"}),
		}
	case "azure-openai":
		fields = []setup.Field{
			text("base_url", "Endpoint", p.BaseURL, "the resource endpoint"),
			text("deployment", "Deployment", p.Deployment, "the deployment name requests address"),
			text("api_version", "API version", p.APIVersion, "blank uses the built-in default"),
			choice("auth", "Auth", p.Auth, []string{"api-key", "bearer", "entra"}),
		}
	case "azure-foundry", "azure-foundry-anthropic":
		fields = []setup.Field{
			text("base_url", "Endpoint", p.BaseURL, "the project or resource endpoint"),
			choice("auth", "Auth", p.Auth, []string{"api-key", "bearer", "entra"}),
		}
	default:
		fields = []setup.Field{text("base_url", "Endpoint", p.BaseURL, "the API base URL; ${VAR} references are allowed")}
	}
	return newSetupForm(setup.Manual{Name: "Edit connection", Type: p.Type, Fields: fields,
		Detail: "Change where and how this provider connects. It is verified again before anything is written, and its other settings are kept."})
}

// applyConnection copies the edited fields onto the provider being verified.
func applyConnection(p appconfig.Provider, values map[string]string) appconfig.Provider {
	get := func(key string) string { return strings.TrimSpace(values[key]) }
	if _, ok := values["base_url"]; ok {
		p.BaseURL = strings.TrimRight(appconfig.ExpandEnv(get("base_url")), "/")
	}
	if _, ok := values["region"]; ok {
		p.Region = get("region")
	}
	if _, ok := values["profile"]; ok {
		p.Profile = get("profile")
	}
	if _, ok := values["deployment"]; ok {
		p.Deployment = get("deployment")
	}
	if _, ok := values["api_version"]; ok {
		p.APIVersion = get("api_version")
	}
	if auth, ok := values["auth"]; ok {
		p.Auth = strings.TrimSpace(auth)
		if p.Auth == "api-key" || p.Auth == "auto" {
			p.Auth = ""
		}
	}
	return p
}

// newSettingsForm lays out temperature, each existing header, and one new
// header. A header whose name suggests a credential is never echoed.
func newSettingsForm(p appconfig.Provider) setupForm {
	fields := []setup.Field{{Key: "temperature", Label: "Temperature", Default: temperatureValue(p.Temperature), Optional: true,
		Placeholder: "blank uses the provider default", Hint: "0 to 2. Blank removes it, so the model's own default applies."}}
	for _, name := range setup.SortedHeaderNames(p.Headers) {
		kind := setup.FieldText
		if setup.HeaderLooksSecret(name) {
			kind = setup.FieldSecret
		}
		fields = append(fields, setup.Field{Key: "header:" + name, Label: name, Kind: kind, Default: p.Headers[name], Optional: true,
			Hint: "Blank removes this header. ${VAR} references are expanded when requests are sent."})
	}
	fields = append(fields,
		setup.Field{Key: "new_header_name", Label: "New header", Optional: true, Placeholder: "X-Header-Name"},
		setup.Field{Key: "new_header_value", Label: "New value", Kind: setup.FieldSecret, Optional: true, Placeholder: "value or ${VAR}"},
	)
	return newSetupForm(setup.Manual{Name: "Temperature and headers", Fields: fields,
		Detail: "Saved without a request. Headers are sent with every request to this provider's endpoint."})
}

func temperatureValue(value *float64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}

// parseSettings turns the settings form into an edit, or a problem to show.
func parseSettings(values map[string]string) (setup.SettingsEdit, string) {
	edit := setup.SettingsEdit{Headers: map[string]string{}}
	if text := strings.TrimSpace(values["temperature"]); text != "" {
		value, err := strconv.ParseFloat(text, 64)
		if err != nil || value < 0 || value > 2 {
			return setup.SettingsEdit{}, "Temperature must be a number from 0 to 2, or blank"
		}
		edit.Temperature = &value
	}
	for key, value := range values {
		if name, ok := strings.CutPrefix(key, "header:"); ok && strings.TrimSpace(value) != "" {
			edit.Headers[name] = value
		}
	}
	name, value := strings.TrimSpace(values["new_header_name"]), values["new_header_value"]
	switch {
	case name == "" && strings.TrimSpace(value) != "":
		return setup.SettingsEdit{}, "The new header needs a name"
	case name != "" && strings.TrimSpace(value) == "":
		return setup.SettingsEdit{}, "The new header " + name + " needs a value"
	case name != "" && strings.ContainsAny(name, " :\t"):
		return setup.SettingsEdit{}, "A header name cannot contain spaces or a colon"
	case name != "":
		edit.Headers[name] = value
	}
	return edit, ""
}

// onEditFormKey drives the connection and settings forms.
func (m setupModel) onEditFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.input.Blur()
		return m.enterActions(), nil
	case "up", "shift+tab":
		m.form = m.form.capture(m.input).move(-1)
		m.input = m.form.syncInto(m.input)
		return m, nil
	case "down", "tab":
		m.form = m.form.capture(m.input).move(1)
		m.input = m.form.syncInto(m.input)
		return m, nil
	case "left", "right":
		if m.form.current().Kind == setup.FieldChoice {
			step := 1
			if msg.String() == "left" {
				step = -1
			}
			m.form = m.form.cycle(step)
			return m, nil
		}
	case "enter":
		m.form = m.form.capture(m.input)
		if m.stage == stageConnectionForm {
			if problem := m.form.spec.Validate(m.form.values); problem != "" {
				m.form.err = problem
				return m, nil
			}
			m.input.Blur()
			m.provider = applyConnection(m.provider, m.form.values)
			m.intent, m.catalog, m.stage = intentConnection, nil, stageScanning
			return m, tea.Batch(m.spin.Tick, m.discoverCmd(m.name, m.provider))
		}
		edit, problem := parseSettings(m.form.values)
		if problem != "" {
			m.form.err = problem
			return m, nil
		}
		m.input.Blur()
		name, model, path := m.name, m.definition().Model, m.opts.ConfigPath
		m.stage = stageApplying
		return m, func() tea.Msg {
			return directEditMsg{name: name, model: model, err: setup.EditSettings(path, name, edit),
				summary: "Updated " + name + "'s temperature and headers."}
		}
	}
	if m.form.current().Kind == setup.FieldChoice {
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.form = m.form.capture(m.input)
	return m, cmd
}

func (m setupModel) onRenameKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.input.Blur()
		return m.enterActions(), nil
	case "enter":
		to := strings.TrimSpace(m.input.Value())
		if problem := setup.ValidProviderName(to, m.opts.Existing); problem != "" {
			m.form.err = problem
			return m, nil
		}
		m.input.Blur()
		from, model, path := m.name, m.definition().Model, m.opts.ConfigPath
		m.stage = stageApplying
		return m, func() tea.Msg {
			moved, err := setup.RenameProvider(path, from, to)
			summary := "Renamed " + from + " to " + to + "."
			if moved {
				summary += " Its stored key moved with it."
			}
			return directEditMsg{name: to, model: model, renamedFrom: from, err: err, summary: summary}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m setupModel) onRemoveKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		name, model, path := m.name, m.definition().Model, m.opts.ConfigPath
		m.stage = stageApplying
		return m, func() tea.Msg {
			summary := "Removed " + name + "."
			if credstore.Available() {
				summary += " A key stored for it in " + credstore.Backend() + " stays there; `collo auth rm " + name + "` removes it."
			}
			return directEditMsg{name: name, model: model, removed: true, err: setup.RemoveProvider(path, name), summary: summary}
		}
	case "n", "esc", "b":
		return m.enterActions(), nil
	}
	return m, nil
}

// onDirectEdit finishes an edit written without verification.
func (m setupModel) onDirectEdit(msg directEditMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m = m.enterActions()
		m.form.err = msg.err.Error()
		return m, nil
	}
	m.name, m.model = msg.name, msg.model
	m.outcome = SetupOutcome{Wrote: true, ConfigPath: m.opts.ConfigPath, Summary: msg.summary,
		RenamedFrom: msg.renamedFrom, Removed: msg.removed, Result: setup.Result{Name: msg.name, Model: msg.model}}
	m.stage = stageDone
	return m, nil
}

// actionsView is a configured provider's menu.
func (m setupModel) actionsView() string {
	current := m.definition()
	lines := []string{
		m.title(m.name + " · " + orDash(current.Model)),
		m.hint(orDash(current.BaseURL) + " (" + current.Type + ")"),
		"",
	}
	for i, action := range m.actions {
		lines = append(lines, m.choiceLine(i, action.label, action.detail, action.disabled))
	}
	if m.form.err != "" {
		lines = append(lines, "", m.styles.errText.Render(wrapText(m.form.err, m.contentWidth())))
	}
	return strings.Join(lines, "\n")
}

func (m setupModel) renameView() string {
	lines := []string{
		m.title("Rename " + m.name),
		m.hint("The default selection follows the new name, and a key in the credential store moves with it."),
		"", "  " + m.input.View(),
	}
	if m.form.err != "" {
		lines = append(lines, "", m.styles.errText.Render(m.form.err))
	}
	return strings.Join(lines, "\n")
}

func (m setupModel) removeView() string {
	body := "Remove " + m.name + " (" + orDash(m.definition().Model) + ") from " + m.opts.ConfigPath + "?"
	if credstore.Available() {
		body += " A key stored for it in " + credstore.Backend() + " is kept; `collo auth rm " + m.name + "` removes it."
	}
	return strings.Join([]string{
		m.title("Remove " + m.name),
		"",
		m.box("remove", m.styles.panelBody.Render(wrapText(body, m.contentWidth()-4)), m.theme.Error),
	}, "\n")
}
