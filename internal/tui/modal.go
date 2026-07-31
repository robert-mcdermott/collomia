package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/robert-mcdermott/collomia/internal/diffmodel"
	"github.com/robert-mcdermott/collomia/internal/permission"
)

const (
	approvalModalMaxWidth = 110
	questionModalMaxWidth = 82
	hunkModalMaxWidth     = 110
)

func (m Model) modalActive() bool {
	return m.pending != nil || m.hunkReview != nil || m.question != nil || m.agentIntegration != nil
}

// renderComposer keeps the normal screen layout stable behind a modal. The
// active editor is rendered inside question dialogs; approvals use the same
// quiet placeholder so the transcript does not jump when a dialog opens.
func (m Model) renderComposer() string {
	box := m.styles.inputBox.Width(max(1, m.width-2))
	if !m.modalActive() {
		rendered := box.Render(m.input.View())
		if hint := m.continuationHint(); hint != "" {
			rendered += "\n" + fitLine(" "+m.styles.muted.Render(ansi.Truncate(hint, max(1, m.width-2), "…")), max(1, m.width))
		}
		return rendered
	}
	message := "Dialog active"
	if m.question != nil {
		message = "Answer in the dialog"
	} else if m.hunkReview != nil {
		message = "Reviewing selected hunks"
	} else if m.pending != nil {
		message = "Approval required"
	} else if m.agentIntegration != nil {
		message = "Reviewing delegated changes"
	}
	// Match the editor's live height so opening a dialog does not make the
	// transcript jump by however many rows the draft happened to occupy.
	return box.Height(m.input.Height()).Render(m.styles.muted.Render("  " + message + "…"))
}

func (m Model) renderApproval() string {
	req := m.pending.request
	inner := m.modalInnerWidth(approvalModalMaxWidth)
	var body strings.Builder
	// A credential approval is a different kind of decision from "may this
	// command run", so it does not wear the same chrome. Reading a key is not
	// reversible by declining the next prompt.
	header, accent := "Permission required", m.theme.Warning
	// Publishing gets its own header for the same reason a credential does:
	// "this leaves the machine" is a different question from "may I run a
	// command", and a reader who sees the ordinary chrome answers the ordinary
	// question. Credential access keeps precedence when an action is both.
	if publicationCapability(req.Capabilities) != nil {
		header, accent = "Publishing outside this machine", m.theme.Error
	}
	if credentialCapability(req.Capabilities) != nil {
		header, accent = "Credential access", m.theme.Error
	}
	body.WriteString(m.modalHeader("⚠", header, accent, inner))
	body.WriteString("\n\n")
	body.WriteString(m.styles.muted.Render("Tool    ") + m.styles.accent.Render(ansi.Truncate(req.Tool, max(1, inner-8), "…")) + "\n")
	body.WriteString(m.styles.muted.Render("Action  ") + wrapAndLimit(req.Action.Summary, max(1, inner-8), 3))
	if req.Reason != "" {
		body.WriteString("\n" + m.styles.warning.Render(wrapAndLimit(req.Reason, inner, 2)))
	}
	if req.Action.Preview != "" {
		lines := strings.Split(strings.TrimRight(req.Action.Preview, "\n"), "\n")
		maxPreview := min(14, max(2, m.height-17))
		hidden := 0
		if len(lines) > maxPreview {
			hidden = len(lines) - maxPreview
			lines = lines[:maxPreview]
		}
		body.WriteString("\n\n")
		if looksLikeDiff(lines) {
			lines = m.renderDiffPreview(lines, approvalPreviewPath(req), inner)
		} else {
			for i, line := range lines {
				lines[i] = m.styles.muted.Render(ansi.Truncate(expandTabs(line), inner, "…"))
			}
		}
		for i, line := range lines {
			body.WriteString(line)
			if i < len(lines)-1 || hidden > 0 {
				body.WriteByte('\n')
			}
		}
		if hidden > 0 {
			body.WriteString(m.styles.muted.Render(fmt.Sprintf("… %d more diff lines", hidden)))
		}
	}
	body.WriteString(m.renderCapabilities(req.Capabilities, inner))
	buttons := badge("Y  Approve", m.theme.Success) + "  "
	// Whether a tool-wide "always" is available is the permission layer's
	// answer, not a rule restated here. Restating it is how this dialog came
	// to offer "Always" for a private key that the permission layer then
	// declined to remember.
	if req.AllowsAlways {
		buttons += badge("A  Always", m.theme.Warning) + "  "
	}
	if grantable := grantableCapabilities(req.Capabilities); len(grantable) > 0 {
		buttons += badge("G  Allow "+describeGrant(grantable)+" this session", m.theme.Accent) + "  "
	}
	buttons += badge("N  Deny", m.theme.Error)
	if req.Tool == "write_file" && req.Action.Preview != "" {
		if hunks, err := diffmodel.ParseHunks(req.Action.Preview); err == nil && len(hunks) >= 2 {
			buttons += "  " + badge(fmt.Sprintf("H  Review %d hunks", len(hunks)), m.theme.Accent)
		}
	}
	body.WriteString("\n\n" + ansi.Wordwrap(buttons, inner, ""))
	body.WriteString(m.renderDurableHint(req, inner))
	return m.modalFrame(body.String(), accent, approvalModalMaxWidth)
}

// approvalPreviewPath is the file the preview describes, used only to pick a
// lexer. An action touching several paths gets the first: the preview is one
// file's diff, and the resource list is ordered by the tool call.
func approvalPreviewPath(req permission.Request) string {
	if len(req.Action.Paths) == 0 {
		return ""
	}
	return req.Action.Paths[0]
}

// renderDurableHint shows the configuration that ends a recurring prompt.
//
// A prompt with no durable answer is how approval fatigue starts: the same
// question every time teaches people to answer without reading it. This is
// deliberately absent for an uninspectable command, where no rule would help
// and suggesting one would be a lie.
func (m Model) renderDurableHint(req permission.Request, inner int) string {
	rule := ""
	switch {
	case publicationCapability(req.Capabilities) != nil && len(req.Action.Operations) > 0:
		// The operation, never the executable: suggesting {"command": "npm"}
		// here would hand out publishing rights in exchange for wanting to
		// stop being asked about one release.
		rule = fmt.Sprintf(`{ "action": "allow", "command": %q }`, publicationOperation(req))
	case credentialCapability(req.Capabilities) != nil:
		capability := credentialCapability(req.Capabilities)
		if _, path := splitCredentialTarget(capability.Values[0]); path != "" {
			rule = fmt.Sprintf(`{ "action": "allow", "path": %q }`, path)
		}
	case req.PostureGated && len(req.Action.Hosts) > 0 && !req.Action.HostsUndetermined:
		rule = fmt.Sprintf(`{ "action": "allow", "host": %q }`, req.Action.Hosts[0])
	case req.PostureGated && len(req.Action.Executables) > 0 && !req.Action.Uninspectable:
		rule = fmt.Sprintf(`{ "action": "allow", "command": %q }`, req.Action.Executables[0])
	}
	if rule == "" {
		return ""
	}
	return "\n\n" + m.styles.muted.Render(wrapAndLimit("To stop being asked, add to permissions.rules:  "+rule, inner, 3))
}

func credentialCapability(capabilities []permission.Capability) *permission.Capability {
	return capabilityOfKind(capabilities, permission.CapabilityCredential)
}

func publicationCapability(capabilities []permission.Capability) *permission.Capability {
	return capabilityOfKind(capabilities, permission.CapabilityPublication)
}

func capabilityOfKind(capabilities []permission.Capability, kind string) *permission.Capability {
	for i, capability := range capabilities {
		if capability.Kind == kind && len(capability.Values) > 0 {
			return &capabilities[i]
		}
	}
	return nil
}

// splitCredentialTarget separates a "label: path" target for display. The
// permission layer keeps the two joined because that exact string is the grant
// key; presentation is this package's problem.
func splitCredentialTarget(target string) (label, path string) {
	if index := strings.Index(target, ": "); index > 0 {
		return target[:index], target[index+2:]
	}
	return "", target
}

// renderCapabilities shows what the action reaches, one dimension at a time,
// so approving is a decision about access rather than about a tool name. A
// dimension the analyzer could not fully read says so instead of appearing
// empty.
func (m Model) renderCapabilities(capabilities []permission.Capability, inner int) string {
	if len(capabilities) == 0 {
		return ""
	}
	labelWidth := 7
	var out strings.Builder
	out.WriteString("\n\n" + m.styles.muted.Render("Reach"))
	for _, capability := range capabilities {
		label := capabilityLabel(capability.Kind)
		label += strings.Repeat(" ", max(1, labelWidth-len(label)))
		value := summarizeValues(capability.Values)
		style := m.styles.panelBody
		if capability.Kind == permission.CapabilityPublication {
			// Lead with the operation and keep the category as a suffix: the
			// command is what the reader is deciding about.
			named := make([]string, 0, len(capability.Values))
			for _, target := range capability.Values {
				if kind, operation := splitCredentialTarget(target); kind != "" {
					named = append(named, operation+" ("+kind+")")
				} else {
					named = append(named, operation)
				}
			}
			value = summarizeValues(named)
			style = m.styles.errText
		}
		if capability.Kind == permission.CapabilityCredential {
			// Lead with the path and keep the kind of secret as a suffix: the
			// file is what the reader is deciding about.
			named := make([]string, 0, len(capability.Values))
			for _, target := range capability.Values {
				if kind, path := splitCredentialTarget(target); kind != "" {
					named = append(named, path+" ("+kind+")")
				} else {
					named = append(named, path)
				}
			}
			value = summarizeValues(named)
			style = m.styles.errText
		}
		switch {
		case capability.Unknown && value == "":
			value = "could not be determined"
			style = m.styles.warning
		case capability.Unknown:
			value += "  + endpoints that could not be determined"
			style = m.styles.warning
		case capability.Granted:
			value += "  (granted this session)"
			style = m.styles.success
		}
		out.WriteString("\n" + m.styles.muted.Render("  "+label) + style.Render(wrapAndLimit(value, max(1, inner-labelWidth-2), 2)))
	}
	return out.String()
}

func capabilityLabel(kind string) string {
	switch kind {
	case permission.CapabilityFilesystem:
		return "files"
	case permission.CapabilityExecutable:
		return "exec"
	case permission.CapabilityNetwork:
		return "net"
	case permission.CapabilityServer:
		return "server"
	case permission.CapabilityCredential:
		return "secret"
	case permission.CapabilityPublication:
		return "publish"
	}
	return kind
}

// publicationOperation names the operation a durable rule should cover. The
// analysis carries the same operation inside each "label: operation" target,
// and the rule needs the bare operation.
func publicationOperation(req permission.Request) string {
	if capability := publicationCapability(req.Capabilities); capability != nil {
		if _, operation := splitCredentialTarget(capability.Values[0]); operation != "" {
			return operation
		}
	}
	return req.Action.Operations[0]
}

// describeGrant names exactly what a session grant would cover, so the button
// is a statement of the access being handed over rather than a yes/no.
func describeGrant(capabilities []permission.Capability) string {
	parts := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		// A credential target carries its label and full path, which is right
		// for the Reach block above and far too long for a button — it wrapped
		// the row and split "Deny" onto its own line. The file names suffice
		// here because the full paths are listed directly above.
		if capability.Kind == permission.CapabilityCredential {
			parts = append(parts, credentialGrantLabel(capability.Values))
			continue
		}
		// The operation alone, for the same reason: the category prefix is
		// already in the Reach block above and only lengthens the button.
		if capability.Kind == permission.CapabilityPublication {
			parts = append(parts, publicationGrantLabel(capability.Values))
			continue
		}
		parts = append(parts, capabilityLabel(capability.Kind)+" "+summarizeValues(capability.Values))
	}
	return strings.Join(parts, " + ")
}

// credentialGrantLabel names what a credential grant covers in as few
// characters as stay unambiguous.
func credentialGrantLabel(values []string) string {
	if len(values) == 1 {
		_, path := splitCredentialTarget(values[0])
		if base := path[strings.LastIndexAny(path, `/\`)+1:]; base != "" {
			return base
		}
		return path
	}
	return fmt.Sprintf("these %d files", len(values))
}

// publicationGrantLabel names what a publication grant covers.
func publicationGrantLabel(values []string) string {
	if len(values) == 1 {
		if _, operation := splitCredentialTarget(values[0]); operation != "" {
			return operation
		}
		return values[0]
	}
	return fmt.Sprintf("these %d operations", len(values))
}

func grantableCapabilities(capabilities []permission.Capability) []permission.Capability {
	var out []permission.Capability
	for _, capability := range capabilities {
		if capability.Grantable && !capability.Granted {
			out = append(out, capability)
		}
	}
	return out
}

func summarizeValues(values []string) string {
	if len(values) == 0 {
		return ""
	}
	if len(values) <= 3 {
		return strings.Join(values, ", ")
	}
	return fmt.Sprintf("%s + %d more", strings.Join(values[:3], ", "), len(values)-3)
}

func (m Model) renderQuestion() string {
	q := m.question.question
	inner := m.modalInnerWidth(questionModalMaxWidth)
	var body strings.Builder
	body.WriteString(m.modalHeader("?", "Collomia is asking", m.theme.Primary, inner))
	body.WriteString("\n\n")
	body.WriteString(m.styles.panelBody.Render(wrapAndLimit(q.Text, inner, min(6, max(2, m.height-14)))))

	optionBudget := max(0, m.height-17)
	shownOptions := 0
	for i := 0; i < len(q.Options); i++ {
		option := wrapAndLimit(q.Options[i], max(1, inner-5), 2)
		optionLines := strings.Count(option, "\n") + 1
		if optionLines > optionBudget {
			break
		}
		body.WriteString("\n" + badge(fmt.Sprintf("%d", i+1), m.theme.Border) + " " + option)
		optionBudget -= optionLines
		shownOptions++
	}
	if hidden := len(q.Options) - shownOptions; hidden > 0 {
		body.WriteString("\n" + m.styles.muted.Render(fmt.Sprintf("… %d more options", hidden)))
	}

	in := m.input
	in.Placeholder = "Type an answer or option number…"
	inputBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.theme.Primary)).
		Padding(0, 1)
	if m.theme.Background != "" {
		inputBox = inputBox.Background(lipgloss.Color(m.theme.Background))
	}
	// Lip Gloss Width includes padding but excludes the border. The textarea's
	// SetWidth, by contrast, is its complete rendered width. Reserve each frame
	// exactly once so the nested input cannot exceed the modal content row and
	// be re-wrapped into broken border fragments.
	in.SetWidth(max(1, inner-inputBox.GetHorizontalFrameSize()))
	in.SetHeight(1)
	inputBox = inputBox.Width(max(1, inner-inputBox.GetHorizontalBorderSize()))
	body.WriteString("\n\n" + inputBox.Render(in.View()))
	body.WriteString("\n" + m.styles.muted.Render(wrapAndLimit("Type an answer or option number · enter submit · esc decline", inner, 2)))
	return m.modalFrame(body.String(), m.theme.Primary, questionModalMaxWidth)
}

func (m Model) modalHeader(icon, title, color string, width int) string {
	labelText := ansi.Truncate(icon+" "+title, max(1, width-2), "…")
	label := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color)).Render(labelText)
	fill := max(1, width-lipgloss.Width(label)-1)
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Border)).Render(strings.Repeat("╱", fill))
	return label + " " + rule
}

func (m Model) modalOuterWidth(limit int) int {
	width := min(limit, m.width-4)
	if width < 12 {
		width = max(1, m.width)
	}
	return width
}

func (m Model) modalInnerWidth(limit int) int {
	style := m.modalStyle(m.theme.Border)
	return max(1, m.modalOuterWidth(limit)-style.GetHorizontalFrameSize())
}

func (m Model) modalFrame(body, border string, limit int) string {
	style := m.modalStyle(border)
	// Style.Width is the width before borders and already includes padding.
	// modalInnerWidth is the content width after both have been removed, so
	// subtract only the border here. Subtracting the complete frame made every
	// modal four columns narrower than its body calculations and caused nested
	// question editors to wrap their own border.
	widthBeforeBorder := max(1, m.modalOuterWidth(limit)-style.GetHorizontalBorderSize())
	return style.Width(widthBeforeBorder).Render(body)
}

func (m Model) modalStyle(border string) lipgloss.Style {
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(border)).
		Padding(1, 2)
	if m.theme.Background != "" {
		style = style.Background(lipgloss.Color(m.theme.Background))
	}
	return style
}

func wrapAndLimit(value string, width, maxLines int) string {
	if width < 1 || maxLines < 1 {
		return ""
	}
	wrapped := ansi.Wordwrap(strings.TrimSpace(value), width, "")
	lines := strings.Split(wrapped, "\n")
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = ansi.Truncate(strings.TrimRight(lines[maxLines-1], "…")+"…", width, "…")
	}
	return strings.Join(lines, "\n")
}

// overlayGutter is the ring of blanked cells kept between a modal's border
// and whatever it covers.
const overlayGutter = 1

// overlayMinMargin is the narrowest strip of base layer worth keeping beside
// a modal. A wide dialog on an 80-column terminal leaves one or two columns
// on each side, and a single orphaned character per row looks like damage
// rather than context, so anything under this is cleared as well.
const overlayMinMargin = 6

// scrim dims one line of the base layer so the modal reads as the focused
// element. Colour is dropped entirely rather than blended: the transcript
// carries syntax highlighting, diff greens and reds, and status accents, and
// leaving any of them at full saturation next to the dialog keeps drawing the
// eye back to content that is not currently actionable.
func scrim(line string) string {
	stripped := ansi.Strip(line)
	if strings.TrimSpace(stripped) == "" {
		return stripped
	}
	return scrimStyle.Render(stripped)
}

var scrimStyle = lipgloss.NewStyle().Faint(true)

// placeOverlay composites a centered modal over the existing screen. A gutter
// is cleared around the dialog so the columns framing it are blank instead of
// mid-word transcript fragments that read as a corrupted redraw. The base
// layer is dimmed as well unless dim is false — see options.dim_background.
func placeOverlay(base, overlay string, width, height int, dim bool) string {
	if width <= 0 || height <= 0 || overlay == "" {
		return base
	}
	baseLines := strings.Split(base, "\n")
	if len(baseLines) > height {
		baseLines = baseLines[:height]
	}
	for len(baseLines) < height {
		baseLines = append(baseLines, "")
	}
	for i := range baseLines {
		line := baseLines[i]
		if dim {
			line = scrim(line)
		}
		baseLines[i] = fitLine(line, width)
	}

	overlayLines := strings.Split(overlay, "\n")
	overlayWidth := lipgloss.Width(overlay)
	if overlayWidth > width {
		overlayWidth = width
	}
	if len(overlayLines) > height {
		overlayLines = overlayLines[:height]
	}
	x := max(0, (width-overlayWidth)/2)
	y := max(0, (height-len(overlayLines))/2)

	gutterTop := max(0, y-overlayGutter)
	gutterBottom := min(height, y+len(overlayLines)+overlayGutter)
	gutterLeft := max(0, x-overlayGutter)
	gutterRight := min(width, x+overlayWidth+overlayGutter)
	if gutterLeft < overlayMinMargin {
		gutterLeft = 0
	}
	if width-gutterRight < overlayMinMargin {
		gutterRight = width
	}
	for i := gutterTop; i < gutterBottom; i++ {
		baseLines[i] = fitLine(ansi.Cut(baseLines[i], 0, gutterLeft), gutterLeft) +
			strings.Repeat(" ", gutterRight-gutterLeft) +
			fitLine(ansi.Cut(baseLines[i], gutterRight, width), width-gutterRight)
	}

	for i, line := range overlayLines {
		line = fitLine(line, overlayWidth)
		under := baseLines[y+i]
		left := fitLine(ansi.Cut(under, 0, x), x)
		right := fitLine(ansi.Cut(under, x+overlayWidth, width), width-x-overlayWidth)
		baseLines[y+i] = left + line + right
	}
	return strings.Join(baseLines, "\n")
}

func fitLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	line = ansi.Truncate(line, width, "")
	if gap := width - ansi.StringWidth(line); gap > 0 {
		line += strings.Repeat(" ", gap)
	}
	return line
}
