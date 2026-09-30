package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/robert-mcdermott/collomia/internal/credstore"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/setup"
	"github.com/robert-mcdermott/collomia/internal/version"
)

// setupMaxWidth keeps provider setup readable on a wide terminal for the same
// reason panels are capped: a two-column form stretched across 300 columns is
// harder to read, not easier.
const setupMaxWidth = 84

func (m setupModel) contentWidth() int {
	width := m.width - 4
	if width > setupMaxWidth {
		width = setupMaxWidth
	}
	if width < 32 {
		width = 32
	}
	return width
}

func (m setupModel) View() string {
	if m.quitting && m.stage != stageDone {
		return ""
	}
	sections := []string{m.header()}
	switch m.stage {
	case stageScanning:
		sections = append(sections, m.scanView())
	case stageChooseProvider:
		sections = append(sections, m.providerView())
	case stageForm:
		sections = append(sections, m.formView())
	case stageChooseModel:
		sections = append(sections, m.modelView())
	case stageManualModel:
		sections = append(sections, m.promptView("Model", "This endpoint publishes no catalog, so the model is named rather than chosen."))
	case stageCredential:
		sections = append(sections, m.credentialView())
	case stageStorage:
		sections = append(sections, m.storageView())
	case stageVerifying:
		sections = append(sections, m.verifyingView())
	case stageFailed:
		sections = append(sections, m.failedView())
	case stageLimits:
		sections = append(sections, m.limitsView())
	case stageEffort:
		sections = append(sections, m.effortView())
	case stageProviderActions:
		sections = append(sections, m.actionsView())
	case stageConnectionForm, stageSettingsForm:
		sections = append(sections, m.formView())
	case stageRename:
		sections = append(sections, m.renameView())
	case stageRemoveConfirm:
		sections = append(sections, m.removeView())
	case stageApplying:
		sections = append(sections, m.title("Saving")+"\n\n  "+m.spin.View()+" writing "+m.opts.ConfigPath)
	case stageEffortVerifying:
		sections = append(sections, strings.Join([]string{
			m.title("Checking reasoning effort"),
			m.hint("One short request with effort " + m.effortTrying + ", so a level the endpoint refuses is caught here rather than discarded on every request later."),
			"",
			"  " + m.spin.View() + " " + m.styles.accent.Render(m.model) + m.styles.muted.Render(" at effort "+m.effortTrying),
		}, "\n"))
	case stageConfirm:
		sections = append(sections, m.confirmView())
	case stageDone:
		sections = append(sections, m.doneView())
	}
	sections = append(sections, "", m.footer())
	body := strings.Join(sections, "\n")
	return lipgloss.NewStyle().Padding(1, 2).Render(body)
}

// header is the wordmark, drawn exactly as the session splash draws it, so the
// setup is visibly the same program rather than an installer that happens to
// ship alongside it.
func (m setupModel) header() string {
	art := wordmarkArt
	if m.contentWidth() >= splashLogoWidth {
		art = joinBlocks(splashLogoGap, blossomArt, wordmarkArt)
	} else if m.contentWidth() < blockWidth(wordmarkArt) {
		art = compactLogoArt
	}
	logo := art
	if !m.theme.plain() {
		logo = gradient(art, m.theme.Primary, m.theme.Secondary)
	}
	subtitle := m.styles.muted.Render("provider setup · " + version.Short())
	return logo + "\n\n" + subtitle + "\n" + m.rule() + "\n"
}

func (m setupModel) rule() string {
	return m.styles.rule.Render(strings.Repeat("─", m.contentWidth()))
}

func (m setupModel) title(text string) string {
	return m.styles.heading.Render(text)
}

// hint is the explanatory line under a title. Every screen has one, because a
// wizard that only labels its fields assumes the user already knows what the
// field is for — which is the assumption that made setup hard enough to need
// a wizard.
func (m setupModel) hint(text string) string {
	return m.styles.muted.Render(wrapText(text, m.contentWidth()))
}

func (m setupModel) scanView() string {
	if m.opts.Reconfigure != "" && m.name != "" {
		// Nothing is being looked for: the provider was named on the command
		// line, and the screen must not claim to be scanning for runtimes it
		// never asked about.
		return strings.Join([]string{
			m.title("Re-verifying " + m.name),
			m.hint(orDash(m.provider.BaseURL) + " — its credential and settings are read from " + m.opts.ConfigPath),
			"",
			"  " + m.spin.View() + " asking the endpoint what it has…",
		}, "\n")
	}
	lines := []string{m.title("Looking for local model runtimes"), ""}
	if len(m.probes) == 0 {
		for _, candidate := range setup.LocalCandidates() {
			lines = append(lines, "  "+m.spin.View()+" "+candidate.Name)
		}
	} else {
		lines = append(lines, "  "+m.spin.View()+" asking the endpoint what it has…")
	}
	return strings.Join(lines, "\n")
}

func (m setupModel) providerView() string {
	// A re-run must show what is already configured. Choosing without seeing
	// the current selection is how someone replaces a working provider by
	// accident.
	subtitle := "Nothing is written until a real request to the endpoint succeeds."
	if m.opts.Existing.HasDefault() {
		subtitle = "Currently: " + m.opts.Existing.DefaultProvider + " / " + m.opts.Existing.DefaultModel +
			". Nothing is written until a real request to the endpoint succeeds."
	}
	lines := []string{
		m.title("Add or change a provider"),
		m.hint(subtitle),
		"",
	}
	for i, choice := range m.choices {
		lines = append(lines, m.choiceLine(i, choice.label, choice.detail, choice.disabled))
	}
	return strings.Join(lines, "\n")
}

func (m setupModel) modelView() string {
	lines := []string{
		m.title("Which model?"),
		m.hint(fmt.Sprintf("%d reported by %s.", len(m.catalog), orDash(m.provider.BaseURL))),
		"",
	}
	window, first := m.visibleWindow(len(m.catalog))
	for i := first; i < first+window && i < len(m.catalog); i++ {
		lines = append(lines, m.choiceLine(i, m.catalog[i].ID, capabilityNote(m.catalog[i]), false))
	}
	if first+window < len(m.catalog) {
		lines = append(lines, m.styles.muted.Render(fmt.Sprintf("  … %d more", len(m.catalog)-first-window)))
	}
	return strings.Join(lines, "\n")
}

// capabilityNote reports what the registry declares, and is careful not to
// read as a measurement: the verification request deliberately carries no
// tools, so nothing here has been observed on this endpoint.
//
// The context figure is the exception, and it is shown only when this model's
// own limits were established — by the catalog, or by the runtime's native
// description endpoint. It used to show the window being assembled for the
// provider, repeated identically beside every entry in the list.
func capabilityNote(model provider.ModelInfo) string {
	notes := make([]string, 0, 3)
	if model.Limits.ContextWindow > 0 {
		notes = append(notes, "context "+compactTokens(model.Limits.ContextWindow))
	}
	if model.Capabilities.Tools == provider.CapabilitySupported {
		notes = append(notes, "tools")
	}
	if model.Capabilities.Images == provider.CapabilitySupported || model.Capabilities.Images == provider.CapabilityPartial {
		notes = append(notes, "images")
	}
	return strings.Join(notes, " · ")
}

// compactTokens renders a token count short enough to sit beside a model name
// in a list. Rounding down is deliberate: this is a label, and 131K reads as a
// size where 131072 reads as a measurement.
func compactTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dK", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// limitSourceNote says where a written limit came from, in the words a reader
// needs to decide whether to change it.
func limitSourceNote(source provider.LimitSource) string {
	switch source {
	case provider.LimitsEndpoint:
		return "reported by the endpoint"
	case provider.LimitsTable:
		return "published limits for this model"
	case provider.LimitsConfigured:
		return "as configured"
	default:
		return "assumed — nothing established it (l to change)"
	}
}

// limitProposalNote explains one field on the limits screen: where its value
// came from, what else was detected, and — for a limit nothing established —
// what the user is being asked to do.
func limitProposalNote(proposal setup.LimitProposal, modelMaximum int) string {
	var note string
	switch proposal.Source {
	case provider.LimitsEndpoint:
		note = "reported by the endpoint"
	case provider.LimitsTable:
		note = "published limits for this model, not measured; change it if your deployment differs"
	case provider.LimitsConfigured:
		note = "currently configured"
		if proposal.Detected > 0 {
			note += "; this run detected " + strconv.Itoa(proposal.Detected) + " (" + detectedWords(proposal.DetectedSource) + ")"
		}
	default:
		note = "couldn't be determined for this model. Enter it from the model's documentation, or leave it blank to assume " + strconv.Itoa(proposal.Value)
	}
	if modelMaximum > proposal.Value && proposal.Source == provider.LimitsEndpoint {
		// The local-runtime case: the weights support far more than the
		// runtime was started with, and the fix is the runtime's setting, not
		// this number.
		note += ". The model supports up to " + strconv.Itoa(modelMaximum) +
			"; the runtime is serving less (for Ollama, raise OLLAMA_CONTEXT_LENGTH and run setup again)"
	}
	return note
}

func detectedWords(source provider.LimitSource) string {
	switch source {
	case provider.LimitsEndpoint:
		return "reported by the endpoint"
	case provider.LimitsTable:
		return "published limits"
	default:
		return "assumed"
	}
}

// effortView is the reasoning-effort screen that follows the token limits.
func (m setupModel) effortView() string {
	var source string
	switch m.effortSupport.Source {
	case provider.LimitsEndpoint:
		source = "These are the levels the endpoint says " + m.model + " accepts."
	case provider.LimitsTable:
		source = "These are the levels published for " + m.model + "."
	default:
		source = "Nothing established which levels " + m.model + " accepts, so every level is offered untested; the next step checks the one you pick."
	}
	lines := []string{
		m.title("Reasoning effort"),
		m.hint("How much " + m.model + " reasons before answering. Higher levels are slower and use more tokens. " + source),
		"",
	}
	for i, choice := range m.effortChoices {
		lines = append(lines, m.choiceLine(i, choice.Label, choice.Detail, false))
	}
	if m.effortProblem != "" {
		lines = append(lines, "", m.styles.errText.Render(wrapText(m.effortProblem, m.contentWidth())))
	}
	lines = append(lines, "", m.styles.muted.Render(wrapText("Saved for this model only. /effort changes it for one session.", m.contentWidth())))
	return strings.Join(lines, "\n")
}

// effortRow states the reasoning effort the confirmation will write.
func (m setupModel) effortRow() (string, bool) {
	switch {
	case m.effortSupport.Unsupported():
		return "not supported by this model", true
	case !m.result.EffortChosen:
		return "", false
	case m.result.Effort != "":
		return m.result.Effort + " — accepted by the endpoint   (e to change)", true
	case m.providerEffort() != "":
		return "provider setting, " + m.providerEffort() + "   (e to change)", true
	default:
		return "model default — nothing sent   (e to change)", true
	}
}

// limitsView is the screen between verification and the confirmation where the
// two token limits are seen, changed, or supplied.
func (m setupModel) limitsView() string {
	lines := []string{
		m.title("Token limits"),
		m.hint("How much " + m.model + " can hold, and how much it may write in one answer. " +
			"Collomia compacts the conversation before the context window fills, and no answer can run past max output."),
		"",
	}
	labelWidth := 0
	for _, field := range m.limitsForm.spec.Fields {
		labelWidth = max(labelWidth, len(field.Label))
	}
	for i, field := range m.limitsForm.spec.Fields {
		focused := i == m.limitsForm.focus
		empty := ""
		if m.limitProposals[i].Assumed() {
			empty = "blank — assumes " + strconv.Itoa(m.limitProposals[i].Value)
		}
		lines = append(lines, m.fieldLine(field, m.limitsForm.values[field.Key], focused, labelWidth, empty))
		modelMaximum := 0
		if field.Key == "context_window" {
			modelMaximum = m.result.Limits.ModelMaximum
		}
		note := limitProposalNote(m.limitProposals[i], modelMaximum)
		style := m.styles.muted
		if m.limitProposals[i].Assumed() {
			style = m.styles.warning
		}
		lines = append(lines, style.Render(indentLines(wrapText(note, m.contentWidth()-labelWidth-6), labelWidth+4)), "")
	}
	if m.limitsForm.err != "" {
		lines = append(lines, m.styles.errText.Render(wrapText(m.limitsForm.err, m.contentWidth())))
	}
	return strings.Join(lines, "\n")
}

// formView renders the multi-field screen for a provider that has to be
// described rather than discovered.
func (m setupModel) formView() string {
	lines := []string{m.title(m.form.spec.Name), m.hint(m.form.spec.Detail), ""}
	labelWidth := 0
	for _, field := range m.form.spec.Fields {
		labelWidth = max(labelWidth, len(field.Label))
	}
	for i, field := range m.form.spec.Fields {
		focused := i == m.form.focus
		lines = append(lines, m.fieldLine(field, m.form.values[field.Key], focused, labelWidth, ""))
		if focused && field.Hint != "" {
			lines = append(lines, m.styles.muted.Render(indentLines(wrapText(field.Hint, m.contentWidth()-labelWidth-6), labelWidth+4)))
		}
	}
	if m.form.err != "" {
		lines = append(lines, "", m.styles.errText.Render(m.form.err))
	}
	return strings.Join(lines, "\n")
}

// fieldLine renders one form field: marker, label, and either the live input,
// the stored value, or a placeholder that cannot be mistaken for a value.
// emptyText, when set, replaces the "e.g." placeholder for an unfilled field
// whose blank has a meaning of its own rather than wanting an example.
func (m setupModel) fieldLine(field setup.Field, stored string, focused bool, labelWidth int, emptyText string) string {
	label := m.styles.muted.Render(pad(field.Label, labelWidth))
	if focused {
		label = m.styles.accent.Render(pad(field.Label, labelWidth))
	}

	var value string
	switch {
	case field.Kind == setup.FieldChoice:
		value = m.choiceValue(field, stored, focused)
	case focused:
		value = m.input.View()
	default:
		// An unfilled field shows its placeholder prefixed with "e.g.".
		// Colour alone cannot carry this: the plain theme has none, and
		// even in a colour theme a suggested value that looks like an
		// entered one leaves the user unable to tell what they have
		// actually filled in.
		shown := stored
		if strings.TrimSpace(shown) == "" {
			placeholder := "required"
			if field.Optional {
				placeholder = "optional"
			}
			if field.Placeholder != "" {
				placeholder = "e.g. " + field.Placeholder
			}
			if emptyText != "" {
				placeholder = emptyText
			}
			shown = m.styles.muted.Render(placeholder)
		} else if field.Kind == setup.FieldSecret {
			// Masked when the cursor is elsewhere too, not only while being
			// typed: a form lists every field at once, and a credential on a
			// shared screen is exposed whichever field has focus.
			shown = m.styles.muted.Render(fmt.Sprintf("hidden · %d characters", len([]rune(shown))))
		} else {
			shown = m.styles.panelBody.Render(shown)
		}
		value = "  " + shown
	}

	marker := "  "
	if focused {
		marker = m.styles.accent.Render("▸ ")
	}
	return marker + label + " " + value
}

// choiceValue renders a cycling option field, showing every option so the
// alternatives are visible without pressing anything.
func (m setupModel) choiceValue(field setup.Field, selected string, focused bool) string {
	parts := make([]string, 0, len(field.Options))
	for _, option := range field.Options {
		switch {
		case option == selected && focused:
			parts = append(parts, m.styles.paletteSel.Render(" "+option+" "))
		case option == selected:
			parts = append(parts, m.styles.paletteCmd.Render(" "+option+" "))
		default:
			parts = append(parts, m.styles.muted.Render(" "+option+" "))
		}
	}
	return "  " + strings.Join(parts, m.styles.muted.Render("·"))
}

func pad(value string, width int) string {
	if len(value) >= width {
		return value
	}
	return value + strings.Repeat(" ", width-len(value))
}

func indentLines(text string, by int) string {
	prefix := strings.Repeat(" ", by)
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func (m setupModel) promptView(title, hint string) string {
	return strings.Join([]string{
		m.title(title), m.hint(hint), "", "  " + m.input.View(),
	}, "\n")
}

func (m setupModel) credentialView() string {
	// Echo is forced here rather than trusted from whichever transition led in.
	// Every path currently sets it, but the cost of one that forgets is a
	// provider key rendered in clear text on a screen someone may be sharing,
	// so the guarantee belongs at the point of rendering. The receiver is a
	// value, so this cannot leak back into the model.
	m.input.EchoMode = textinput.EchoPassword

	// The character count is the only feedback a field that does not echo can
	// give, and it is the difference between noticing a key that arrived
	// truncated and discovering it from an endpoint's error several screens
	// later. It reveals nothing: a length is not a secret.
	counter := ""
	if n := len([]rune(m.input.Value())); n > 0 {
		counter = m.styles.muted.Render(fmt.Sprintf("   %d characters", n))
	}
	return strings.Join([]string{
		m.title("API key"),
		m.hint("Typed without echo. Collomia never writes a key into a configuration file — the next screen chooses where it goes."),
		"", "  " + m.input.View() + counter,
		"", m.styles.muted.Render(wrapText("Paste the whole value. Surrounding quotes and any whitespace are removed for you, including newlines from a wrapped copy.", m.contentWidth())),
	}, "\n")
}

func (m setupModel) storageView() string {
	lines := []string{
		m.title("Where should the key live?"),
		m.hint("Configuration files record where a credential is found, never the credential."),
		"",
	}
	if credstore.Available() {
		lines = append(lines,
			m.choiceLine(0, credstore.Backend(), "stored by the operating system; Collomia reads it on demand", false),
			m.choiceLine(1, "An environment variable", "Collomia records the name $"+m.envVar+"; you export the value", false),
		)
		return strings.Join(lines, "\n")
	}
	// Linux has no credential-store backend and deliberately no encrypted-file
	// fallback, so there is one real option. Saying why beats presenting a
	// single choice as though it were a decision.
	lines = append(lines,
		m.choiceLine(0, "An environment variable", "Collomia records the name $"+m.envVar+"; you export the value", false),
		"",
		m.styles.muted.Render(wrapText("There is no OS credential store on this platform, and Collomia does not fall back to an encrypted file — a passphrase-protected file would only move the problem.", m.contentWidth())),
	)
	return strings.Join(lines, "\n")
}

func (m setupModel) verifyingView() string {
	return strings.Join([]string{
		m.title("Verifying"),
		m.hint("One short request through the same adapter a session uses. A catalog listing proves the host answers; only this proves the model will."),
		"",
		"  " + m.spin.View() + " " + m.styles.accent.Render(m.model) + m.styles.muted.Render(" at "+orDash(m.provider.BaseURL)),
	}, "\n")
}

func (m setupModel) failedView() string {
	d := m.verification.Diagnosis
	body := []string{m.styles.errText.Render(orDash(d.Summary))}
	if d.Detail != "" {
		body = append(body, "", m.styles.muted.Render(wrapText(d.Detail, m.contentWidth()-4)))
	}
	// On a Bedrock failure the resolved identity is evidence, not decoration:
	// it separates "the chain produced nothing" from "it produced the wrong
	// account", which are different fixes, and it is already known by now.
	if m.awsIdentity != nil {
		body = append(body, "", m.styles.muted.Render(wrapText("AWS identity: "+m.awsIdentity.Describe(), m.contentWidth()-4)))
	}
	if len(d.Fixes) > 0 {
		body = append(body, "")
		for _, fix := range d.Fixes {
			body = append(body, m.styles.panelBody.Render(wrapBullet("• ", fix, m.contentWidth()-6)))
		}
	}
	// Titled from the verification rather than from m.model: the verification
	// is the record of what was actually attempted, and a panel that names a
	// different model than the one the diagnosis is about would send the
	// reader to the wrong fix.
	return strings.Join([]string{
		m.title("Not verified"),
		m.hint("Nothing has been written."),
		"",
		m.box("✗ "+orDash(m.verification.Model), strings.Join(body, "\n"), m.theme.Error),
	}, "\n")
}

func (m setupModel) confirmView() string {
	rows := [][2]string{
		{"provider", m.name + " (" + m.provider.Type + ")"},
		{"endpoint", orDash(m.provider.BaseURL)},
		{"model", m.model},
		{"credential", m.result.CredentialSummary()},
	}
	if m.result.Provider.Context > 0 {
		// Both numbers are reported together with what established them.
		// Automatic compaction depends on the window and every long answer
		// depends on the cap, and an assumption presented as a measurement is
		// how a wrong number survives to the first failed session.
		rows = append(rows, [2]string{"limits", fmt.Sprintf("context %d — %s",
			m.result.Provider.Context, limitSourceNote(m.result.Limits.ContextSource))})
		rows = append(rows, [2]string{"", fmt.Sprintf("max output %d — %s",
			m.result.Provider.MaxTokens, limitSourceNote(m.result.Limits.OutputSource))})
	}
	if effort, ok := m.effortRow(); ok {
		rows = append(rows, [2]string{"reasoning", effort})
	}
	if m.awsIdentity != nil {
		// The commonest Bedrock confusion is not a missing credential but not
		// knowing which of several sources won.
		rows = append(rows, [2]string{"aws identity", m.awsIdentity.Describe()})
	}
	rows = append(rows,
		[2]string{"verified", m.verification.Describe()},
		[2]string{"default", m.defaultRow()},
	)

	body := make([]string, 0, len(rows)+3)
	for _, row := range rows {
		body = append(body, m.styles.muted.Render(fmt.Sprintf("%-13s", row[0]))+" "+m.styles.panelBody.Render(row[1]))
	}
	switch update := m.opts.Existing.Update(m.result); {
	case update.Replaced:
		text := "This replaces the provider named " + m.name + " in this file, currently " +
			m.opts.Existing.Describes(m.name) + "."
		if len(update.Dropped) > 0 {
			text += " It points somewhere else now, so its other settings are not carried over: " +
				strings.Join(update.Dropped, ", ") + "."
		}
		body = append(body, "", m.styles.warning.Render(wrapText(text, m.contentWidth()-4)))
	case update.Exists && m.result.EntryOnly:
		text := "This adds " + m.model + " to " + m.name + "'s models with the limits and effort above. " +
			m.opts.Existing.Describes(m.name) + " stays its own model, and nothing else changes. /model " + m.name + "/" + m.model + " uses it."
		body = append(body, "", m.styles.muted.Render(wrapText(text, m.contentWidth()-4)))
	case update.Exists:
		text := "This updates the provider named " + m.name + " in this file, currently " +
			m.opts.Existing.Describes(m.name) + "."
		if len(update.Kept) > 0 && update.EndpointChanged {
			text += " Its other settings are kept and will now be sent to the new endpoint: " + strings.Join(update.Kept, ", ") + "."
		} else if len(update.Kept) > 0 {
			text += " Settings setup does not ask about are kept: " + strings.Join(update.Kept, ", ") + "."
		}
		if update.MovedLimitsFor != "" {
			text += " The limits recorded for " + update.MovedLimitsFor +
				" move to its own models entry, so /model can switch back to it with them."
		}
		body = append(body, "", m.styles.muted.Render(wrapText(text, m.contentWidth()-4)))
	}
	return strings.Join([]string{
		m.title("Ready to write"),
		m.hint(m.opts.ConfigPath),
		"",
		m.box("✓ verified", strings.Join(body, "\n"), m.theme.Success),
	}, "\n")
}

func (m setupModel) doneView() string {
	next := []string{
		m.styles.muted.Render("Start a session with ") + m.styles.accent.Render("collo"),
		m.styles.muted.Render("Inspect the whole configuration with ") + m.styles.accent.Render("collo doctor"),
		m.styles.muted.Render("Add or change a provider by running ") + m.styles.accent.Render("collo setup") + m.styles.muted.Render(" again"),
	}
	if m.opts.Embedded {
		next = []string{
			m.styles.panelBody.Render("Press enter to return to your session, where this change is applied."),
			m.styles.muted.Render("Run ") + m.styles.accent.Render("/providers") + m.styles.muted.Render(" again to change another provider"),
		}
	} else if m.opts.ContinueToSession {
		next = []string{
			m.styles.panelBody.Render("Provider verified. Press enter to continue into your session."),
			m.styles.muted.Render("You can add or change providers later with ") + m.styles.accent.Render("collo setup"),
			m.styles.muted.Render("Inspect the whole configuration with ") + m.styles.accent.Render("collo doctor"),
		}
	}
	title, written := "✓ "+m.name+" / "+m.model, "Written to "+m.opts.ConfigPath
	if m.outcome.Summary != "" {
		title, written = "✓ "+m.name, m.outcome.Summary+" Written to "+m.opts.ConfigPath+"."
	}
	return strings.Join([]string{
		m.title("Done"),
		"",
		m.box(title, strings.Join([]string{
			m.styles.panelBody.Render(wrapText(written, m.contentWidth()-4)),
			"",
			strings.Join(next, "\n"),
		}, "\n"), m.theme.Success),
	}, "\n")
}

// defaultRow states what will happen to default_provider, including what it is
// being changed from. Adding a provider and silently repointing the default at
// it is the behavior this row exists to make impossible.
func (m setupModel) defaultRow() string {
	current := m.opts.Existing.DefaultProvider
	if m.result.EntryOnly {
		return "unchanged — adding a model does not change the default"
	}
	switch {
	case m.makeDefault && current != "" && current != m.name:
		return "yes — changed from " + current + " / " + m.opts.Existing.DefaultModel + "   (d to keep " + current + ")"
	case m.makeDefault:
		return "yes — sessions will use this unless told otherwise   (d to change)"
	case current != "":
		return "no — " + current + " / " + m.opts.Existing.DefaultModel + " stays the default   (d to change)"
	default:
		return "no — nothing else is configured, so no default will be set   (d to change)"
	}
}

// choiceLine renders one selectable row in the same idiom as the command
// palette: a marker, a bold label, and a muted detail.
func (m setupModel) choiceLine(index int, label, detail string, disabled bool) string {
	selected := index == m.cursor
	marker := "  "
	name := label
	switch {
	case disabled:
		name = m.styles.muted.Render(label)
	case selected:
		marker = m.styles.accent.Render("▸ ")
		name = m.styles.paletteSel.Render(label)
	default:
		name = m.styles.paletteCmd.Render(label)
	}
	line := marker + name
	if detail != "" {
		line += m.styles.paletteDesc.Render("  " + detail)
	}
	if width := m.contentWidth(); ansi.StringWidth(line) > width {
		line = ansi.Truncate(line, width, "…")
	}
	return line
}

// visibleWindow keeps the selected row on screen for a catalog longer than the
// terminal, scrolling the window rather than the cursor.
func (m setupModel) visibleWindow(total int) (window, first int) {
	window = m.height - 18
	if window < 3 {
		window = 3
	}
	if window > total {
		window = total
	}
	first = 0
	if m.cursor >= window {
		first = m.cursor - window + 1
	}
	if first+window > total {
		first = total - window
	}
	if first < 0 {
		first = 0
	}
	return window, first
}

// box is the wizard's panel: the same rounded frame with the title spliced
// into the top border that the session uses for /status and /context, drawn
// here with a per-state accent so success and failure are distinguishable
// without reading the text.
func (m setupModel) box(title, content, accent string) string {
	width := m.contentWidth()
	inner := width - 2
	border := m.theme.Border
	if !m.theme.plain() && accent != "" {
		border = accent
	}
	body := m.styles.panelBody.
		Border(lipgloss.RoundedBorder(), false, true, true, true).
		BorderForeground(lipgloss.Color(border)).
		Padding(0, 1).
		Width(inner).
		Render(strings.TrimRight(content, "\n"))
	label := " " + title + " "
	fill := inner - lipgloss.Width(label) - 2
	if fill < 0 {
		fill = 0
	}
	edge := lipgloss.NewStyle().Foreground(lipgloss.Color(border))
	if m.theme.plain() {
		edge = lipgloss.NewStyle()
	}
	top := edge.Render("╭──") + m.styles.panelTitle.Render(label) + edge.Render(strings.Repeat("─", fill)+"╮")
	return top + "\n" + body
}

// footer states the keys for the current screen. The controls are never
// omitted: a wizard that hides how to go back is a wizard people ctrl-c out of.
func (m setupModel) footer() string {
	var keys [][2]string
	switch m.stage {
	case stageScanning, stageVerifying, stageEffortVerifying:
		keys = [][2]string{{"esc", "cancel"}}
	case stageEffort:
		keys = [][2]string{{"↑↓", "move"}, {"enter", "select"}, {"esc", "back"}}
	case stageProviderActions:
		back := "back"
		if len(m.choices) == 0 {
			back = m.leaveWord()
		}
		keys = [][2]string{{"↑↓", "move"}, {"enter", "select"}, {"esc", back}}
	case stageConnectionForm:
		keys = [][2]string{{"↑↓", "field"}, {"←→", "option"}, {"enter", "verify"}, {"esc", "back"}}
	case stageSettingsForm:
		keys = [][2]string{{"↑↓", "field"}, {"enter", "save"}, {"esc", "back"}}
	case stageRename:
		keys = [][2]string{{"enter", "rename"}, {"esc", "back"}}
	case stageRemoveConfirm:
		keys = [][2]string{{"y", "remove"}, {"n", "keep it"}}
	case stageApplying:
		keys = nil
	case stageChooseProvider:
		keys = [][2]string{{"↑↓", "move"}, {"enter", "select"}, {"esc", m.leaveWord()}}
	case stageChooseModel, stageStorage:
		keys = [][2]string{{"↑↓", "move"}, {"enter", "select"}, {"esc", "back"}}
	case stageManualModel, stageCredential:
		keys = [][2]string{{"enter", "continue"}, {"esc", "back"}}
	case stageForm:
		keys = [][2]string{{"↑↓", "field"}, {"←→", "option"}, {"enter", "continue"}, {"esc", "back"}}
	case stageFailed:
		keys = [][2]string{{"r", "retry"}, {"b", "back"}, {"q", m.leaveWord()}}
	case stageLimits:
		keys = [][2]string{{"↑↓", "field"}, {"enter", "continue"}, {"esc", "back"}}
	case stageConfirm:
		keys = [][2]string{{"enter", "write"}, {"l", "limits"}}
		if setup.Offered(m.effortSupport) {
			keys = append(keys, [2]string{"e", "effort"})
		}
		keys = append(keys, [2]string{"d", "default"}, [2]string{"b", "back"}, [2]string{"q", m.leaveWord()})
	case stageDone:
		action := "close"
		if m.opts.ContinueToSession {
			action = "start session"
		}
		if m.opts.Embedded {
			action = "return to session"
		}
		keys = [][2]string{{"enter", action}}
	}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, m.styles.statusKey.Render(key[0])+" "+m.styles.muted.Render(key[1]))
	}
	return m.rule() + "\n" + strings.Join(parts, m.styles.muted.Render("  ·  "))
}

// leaveWord names what leaving does: ending the program, or closing the flow
// back to the session that hosts it.
func (m setupModel) leaveWord() string {
	if m.opts.Embedded {
		return "close"
	}
	return "quit"
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// wrapText word-wraps to a width, so an endpoint's own error message does not
// run off the panel it is shown in.
func wrapText(text string, width int) string {
	if width < 8 {
		width = 8
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	lines := []string{words[0]}
	for _, word := range words[1:] {
		last := len(lines) - 1
		if ansi.StringWidth(lines[last])+1+ansi.StringWidth(word) <= width {
			lines[last] += " " + word
			continue
		}
		lines = append(lines, word)
	}
	return strings.Join(lines, "\n")
}

// wrapBullet wraps a bullet item so continuation lines align under the text
// rather than under the marker.
func wrapBullet(marker, text string, width int) string {
	wrapped := wrapText(text, width-len(marker))
	lines := strings.Split(wrapped, "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = marker + lines[i]
			continue
		}
		lines[i] = strings.Repeat(" ", len(marker)) + lines[i]
	}
	return strings.Join(lines, "\n")
}
