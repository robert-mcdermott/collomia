package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/robert-mcdermott/collomia/internal/app"
	"github.com/robert-mcdermott/collomia/internal/setup"
)

// openProviderSetup starts the setup flow inside the session, optionally
// pointed at one configured provider.
//
// It is refused while a turn is running. Applying a change re-selects the
// active provider, and swapping the client and its limits under a turn that is
// mid-request is not a change anyone could reason about.
func (m *Model) openProviderSetup(args []string) tea.Cmd {
	if m.busy {
		m.addError(fmt.Errorf("finish or cancel the running turn before changing providers"))
		return nil
	}
	if len(args) > 1 {
		m.addError(fmt.Errorf("usage: /providers [name|save-ceiling]"))
		return nil
	}
	if len(args) == 1 && args[0] == "save-ceiling" {
		m.saveLearnedCeiling()
		return nil
	}
	path, err := setup.GlobalPath()
	if err != nil {
		m.addError(err)
		return nil
	}
	existing, err := setup.ReadExisting(path)
	if err != nil {
		m.addError(fmt.Errorf("read %s: %w", path, err))
		return nil
	}
	active, _ := m.runtime.Agent.Selection()
	opts := SetupOptions{ConfigPath: path, ThemeName: m.theme.Name, Existing: existing, Embedded: true, ActiveProvider: active}
	if len(args) == 1 {
		if !existing.Has(args[0]) {
			m.addError(fmt.Errorf("%s does not configure %q; it has %s", path, args[0], orDash(strings.Join(existing.Providers, ", "))))
			return nil
		}
		opts.Reconfigure = args[0]
	}
	hosted := newSetupModel(opts)
	hosted.width, hosted.height = m.width, m.height
	hosted.input.Width = min(hosted.contentWidth()-4, 72)
	m.providerSetup = &hosted
	m.paletteOn = false
	m.input.Blur()
	return hosted.Init()
}

// routeToProviderSetup forwards the messages the hosted flow is waiting on.
// Keys are routed separately, after approvals and questions from a running
// turn, which keep priority over everything.
func (m Model) routeToProviderSetup(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg.(type) {
	case probesDoneMsg, catalogMsg, verifiedMsg, effortVerifiedMsg, wroteMsg, awsIdentityMsg, directEditMsg:
		next, cmd := m.providerSetup.Update(msg)
		updated := next.(setupModel)
		m.providerSetup = &updated
		return m, cmd, true
	case spinner.TickMsg, tea.WindowSizeMsg:
		// Both the session and the flow have spinners and layouts; each
		// ignores a tick that is not its own.
		next, setupCmd := m.providerSetup.Update(msg)
		updated := next.(setupModel)
		m.providerSetup = &updated
		model, cmd := m.updateWithoutSetup(msg)
		return model, tea.Batch(setupCmd, cmd), true
	case tea.MouseMsg:
		return m, nil, true
	}
	return m, nil, false
}

// updateWithoutSetup runs the session's own handling for a message the flow
// has already seen.
func (m Model) updateWithoutSetup(msg tea.Msg) (tea.Model, tea.Cmd) {
	hosted := m.providerSetup
	m.providerSetup = nil
	model, cmd := m.Update(msg)
	updated := model.(Model)
	updated.providerSetup = hosted
	return updated, cmd
}

// closeProviderSetup returns to the session and, when the flow wrote a
// provider, applies it to the running session.
func (m Model) closeProviderSetup() (tea.Model, tea.Cmd) {
	hosted := m.providerSetup
	m.providerSetup = nil
	m.input.Focus()
	if hosted == nil {
		return m, nil
	}
	if hosted.err != nil {
		m.addError(hosted.err)
		m.refresh()
		return m, nil
	}
	if !hosted.outcome.Wrote {
		m.addSystem("Provider setup closed; nothing was written.")
		m.refresh()
		return m, nil
	}
	result := hosted.outcome.Result
	reload, err := m.runtime.ReloadProviders(app.ProviderChange{Name: result.Name, Model: result.Model,
		RenamedFrom: hosted.outcome.RenamedFrom, Removed: hosted.outcome.Removed})
	if err != nil {
		m.addError(fmt.Errorf("saved %s/%s to %s, but applying it to this session failed: %w", result.Name, result.Model, hosted.outcome.ConfigPath, err))
		m.refresh()
		return m, nil
	}
	if hosted.outcome.Summary != "" {
		m.addSystem(directEditMessage(hosted.outcome, reload))
		m.refresh()
		return m, nil
	}
	m.addSystem(providerAppliedMessage(result, reload, hosted.outcome.ConfigPath))
	m.refresh()
	return m, nil
}

// providerAppliedMessage says what was saved and what it changed in this
// session, which are not always the same thing.
func providerAppliedMessage(result setup.Result, reload app.ProviderReload, path string) string {
	parts := []string{fmt.Sprintf("Saved %s/%s to %s.", result.Name, result.Model, path)}
	switch {
	case reload.Shadowed != "":
		parts = append(parts, "A project configuration also defines "+reload.Shadowed+" and wins in this workspace, so the change applies only elsewhere.")
	case reload.Reselected && reload.ActiveModel == result.Model:
		effort := "no effort sent"
		if result.Effort != "" {
			effort = "effort " + result.Effort
		}
		parts = append(parts, fmt.Sprintf("Applied from the next turn: context %d, max output %d, %s.", result.Provider.Context, result.Provider.MaxTokens, effort))
	case reload.Reselected:
		parts = append(parts, fmt.Sprintf("This session keeps %s/%s with its own saved settings; /model %s/%s switches.",
			reload.ActiveProvider, reload.ActiveModel, result.Name, result.Model))
	default:
		parts = append(parts, fmt.Sprintf("This session still runs %s/%s; /model %s switches.", reload.ActiveProvider, reload.ActiveModel, result.Name))
	}
	return strings.Join(parts, " ")
}

// directEditMessage reports an edit made without verification and what it
// means for this session.
func directEditMessage(outcome SetupOutcome, reload app.ProviderReload) string {
	message := outcome.Summary
	switch {
	case outcome.Removed:
	case reload.Reselected:
		message += fmt.Sprintf(" Applied to this session (%s/%s) from the next turn.", reload.ActiveProvider, reload.ActiveModel)
	case outcome.RenamedFrom == "":
		message += fmt.Sprintf(" This session runs %s/%s.", reload.ActiveProvider, reload.ActiveModel)
	}
	return message
}

// offerLearnedCeiling tells the user, once, that a provider stated a smaller
// output ceiling than configured, and how to save it. The adapter already
// retries under the ceiling for the rest of the session; only saving it
// makes that permanent.
func (m *Model) offerLearnedCeiling() {
	providerName, model, ceiling := m.runtime.Agent.LearnedOutputCeiling()
	if ceiling <= 0 {
		return
	}
	key := fmt.Sprintf("%s/%s/%d", providerName, model, ceiling)
	if key == m.offeredCeiling {
		return
	}
	m.offeredCeiling = key
	m.addSystem(fmt.Sprintf("%s accepts at most %d output tokens for %s, and this session now uses that. /providers save-ceiling saves it as the model's max_tokens.", providerName, ceiling, model))
}

// saveLearnedCeiling writes the learned ceiling to the user configuration and
// applies it to the session.
func (m *Model) saveLearnedCeiling() {
	providerName, model, ceiling := m.runtime.Agent.LearnedOutputCeiling()
	if ceiling <= 0 {
		m.addError(fmt.Errorf("no provider has stated an output ceiling in this session"))
		return
	}
	path, err := setup.GlobalPath()
	if err != nil {
		m.addError(err)
		return
	}
	if err := setup.SaveMaxTokens(path, providerName, model, ceiling); err != nil {
		m.addError(fmt.Errorf("save max_tokens for %s/%s: %w", providerName, model, err))
		return
	}
	if _, err := m.runtime.ReloadProviders(app.ProviderChange{Name: providerName}); err != nil {
		m.addError(fmt.Errorf("saved max_tokens %d for %s/%s to %s, but applying it failed: %w", ceiling, providerName, model, path, err))
		return
	}
	m.addSystem(fmt.Sprintf("Saved max_tokens %d for %s/%s to %s.", ceiling, providerName, model, path))
}
