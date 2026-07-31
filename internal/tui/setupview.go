package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/robert-mcdermott/collomia/internal/credstore"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/setup"
	"github.com/robert-mcdermott/collomia/internal/version"
)

// setupMaxWidth keeps the wizard readable on a wide terminal for the same
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
// wizard is visibly the same program rather than an installer that happens to
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
	subtitle := m.styles.muted.Render("first-run setup · " + version.Short())
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
		m.title("Which provider should Collomia use?"),
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
func capabilityNote(model provider.ModelInfo) string {
	notes := make([]string, 0, 3)
	if model.Capabilities.ContextWindow > 0 {
		notes = append(notes, fmt.Sprintf("context %d", model.Capabilities.ContextWindow))
	}
	if model.Capabilities.Tools == provider.CapabilitySupported {
		notes = append(notes, "tools")
	}
	if model.Capabilities.Images == provider.CapabilitySupported || model.Capabilities.Images == provider.CapabilityPartial {
		notes = append(notes, "images")
	}
	return strings.Join(notes, " · ")
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
		label := m.styles.muted.Render(pad(field.Label, labelWidth))
		if focused {
			label = m.styles.accent.Render(pad(field.Label, labelWidth))
		}

		var value string
		switch {
		case field.Kind == setup.FieldChoice:
			value = m.choiceValue(field, m.form.values[field.Key], focused)
		case focused:
			value = m.input.View()
		default:
			// An unfilled field shows its placeholder prefixed with "e.g.".
			// Colour alone cannot carry this: the plain theme has none, and
			// even in a colour theme a suggested value that looks like an
			// entered one leaves the user unable to tell what they have
			// actually filled in.
			shown := m.form.values[field.Key]
			if strings.TrimSpace(shown) == "" {
				placeholder := "required"
				if field.Optional {
					placeholder = "optional"
				}
				if field.Placeholder != "" {
					placeholder = "e.g. " + field.Placeholder
				}
				shown = m.styles.muted.Render(placeholder)
			} else {
				shown = m.styles.panelBody.Render(shown)
			}
			value = "  " + shown
		}

		marker := "  "
		if focused {
			marker = m.styles.accent.Render("▸ ")
		}
		lines = append(lines, marker+label+" "+value)
		if focused && field.Hint != "" {
			lines = append(lines, m.styles.muted.Render(indentLines(wrapText(field.Hint, m.contentWidth()-labelWidth-6), labelWidth+4)))
		}
	}
	if m.form.err != "" {
		lines = append(lines, "", m.styles.errText.Render(m.form.err))
	}
	return strings.Join(lines, "\n")
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
		context := fmt.Sprintf("%d", m.result.Provider.Context)
		if m.result.ContextAssumed {
			// Neither the endpoint nor the registry establishes this, and
			// automatic compaction depends on it, so the guess is labelled
			// rather than presented as something that was measured.
			context += " — assumed; set context_window if your model differs"
		}
		rows = append(rows, [2]string{"context", context})
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
	if m.overwrites() {
		body = append(body, "", m.styles.warning.Render(wrapText(
			"This replaces the provider named "+m.name+" in this file, currently "+
				m.opts.Existing.Describes(m.name)+".", m.contentWidth()-4)))
	}
	return strings.Join([]string{
		m.title("Ready to write"),
		m.hint(m.opts.ConfigPath),
		"",
		m.box("✓ verified", strings.Join(body, "\n"), m.theme.Success),
	}, "\n")
}

func (m setupModel) doneView() string {
	return strings.Join([]string{
		m.title("Done"),
		"",
		m.box("✓ "+m.name+" / "+m.model, strings.Join([]string{
			m.styles.panelBody.Render("Written to " + m.opts.ConfigPath),
			"",
			m.styles.muted.Render("Start a session with ") + m.styles.accent.Render("collo"),
			m.styles.muted.Render("Inspect the whole configuration with ") + m.styles.accent.Render("collo doctor"),
			m.styles.muted.Render("Add another provider by running ") + m.styles.accent.Render("collo setup") + m.styles.muted.Render(" again"),
		}, "\n"), m.theme.Success),
	}, "\n")
}

// overwrites reports whether confirming replaces a provider that already
// exists *in the file being written*, so the confirmation can say so rather
// than the user finding out by losing a configuration.
func (m setupModel) overwrites() bool { return m.opts.Existing.Has(m.name) }

// defaultRow states what will happen to default_provider, including what it is
// being changed from. Adding a provider and silently repointing the default at
// it is the behavior this row exists to make impossible.
func (m setupModel) defaultRow() string {
	current := m.opts.Existing.DefaultProvider
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
	case stageScanning, stageVerifying:
		keys = [][2]string{{"esc", "cancel"}}
	case stageChooseProvider:
		keys = [][2]string{{"↑↓", "move"}, {"enter", "select"}, {"esc", "quit"}}
	case stageChooseModel, stageStorage:
		keys = [][2]string{{"↑↓", "move"}, {"enter", "select"}, {"esc", "back"}}
	case stageManualModel, stageCredential:
		keys = [][2]string{{"enter", "continue"}, {"esc", "back"}}
	case stageForm:
		keys = [][2]string{{"↑↓", "field"}, {"←→", "option"}, {"enter", "continue"}, {"esc", "back"}}
	case stageFailed:
		keys = [][2]string{{"r", "retry"}, {"b", "back"}, {"q", "quit"}}
	case stageConfirm:
		keys = [][2]string{{"enter", "write"}, {"d", "default"}, {"b", "back"}, {"q", "quit"}}
	case stageDone:
		keys = [][2]string{{"enter", "close"}}
	}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, m.styles.statusKey.Render(key[0])+" "+m.styles.muted.Render(key[1]))
	}
	return m.rule() + "\n" + strings.Join(parts, m.styles.muted.Render("  ·  "))
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
