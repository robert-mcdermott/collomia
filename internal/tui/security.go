package tui

import (
	"fmt"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/app"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/egress"
)

// stance is the compact, always-visible summary of how contained this session
// is. It exists so the answer to "what is protecting me right now?" is on
// screen rather than several commands away.
type stance struct {
	// Label is the short badge text: the configured preset when there is one,
	// otherwise a name derived from the effective settings.
	Label string
	// Degraded marks a sandbox that asked for enforcement and did not get all
	// of it. A badge that claimed protection the OS did not apply would be
	// worse than no badge.
	Degraded bool
	// Color is the theme color the badge is rendered in.
	Color string
}

func (m Model) securityStance() stance {
	permissions := m.runtime.Config.Permissions
	summary := m.runtime.SandboxSummary()
	degraded := strings.Contains(summary, "degraded") || strings.Contains(summary, "unavailable")

	label := strings.ToLower(strings.TrimSpace(permissions.Preset))
	if label == "" {
		label = derivedStanceLabel(permissions)
	}
	color := m.theme.Secondary
	switch {
	case degraded:
		color = m.theme.Warning
	case permissions.Sandbox == "off":
		color = m.theme.Warning
	case label == appconfig.PresetHardened || permissions.Sandbox == "require":
		color = m.theme.Success
	}
	return stance{Label: label, Degraded: degraded, Color: color}
}

// derivedStanceLabel names an unnamed configuration by what it actually does,
// so a hand-composed policy still reads as something rather than as blank.
func derivedStanceLabel(permissions appconfig.Permissions) string {
	switch {
	case permissions.Sandbox == "off":
		return "unsandboxed"
	case permissions.Network == "scoped" || permissions.Commands == "allowlist":
		return "scoped"
	case permissions.Sandbox == "require":
		return "enforced"
	default:
		return appconfig.PresetStandard
	}
}

// stanceGlyph is the always-present containment mark carried by the mode
// badge. A filled shield means OS containment is in force; a struck shield
// means it is not, and an exclamation means less of it was applied than was
// asked for. Two columns, never dropped.
func (m Model) stanceGlyph() string {
	s := m.securityStance()
	glyph := "⛨"
	if s.Label == appconfig.PresetFrictionless || s.Label == "unsandboxed" {
		glyph = "⛉"
	}
	if s.Degraded {
		glyph += "!"
	}
	return glyph
}

// stanceNameBadge spells the stance out when it is not the ordinary one, so
// an unusual or degraded posture is readable at a glance rather than encoded
// in a glyph. It returns "" when there is nothing worth the columns; the
// Session tab always carries the full detail.
func (m Model) stanceNameBadge() string {
	s := m.securityStance()
	if s.Label == appconfig.PresetStandard && !s.Degraded {
		return ""
	}
	text := s.Label
	if s.Degraded {
		text += " degraded"
	}
	return badge(text, s.Color)
}

// securitySection renders the complete containment picture in one place:
// every switch that decides what an approved action can reach, plus the
// session grants that have accumulated since startup.
func (m Model) securityContent(width int) string {
	h := m.styles.heading.Render
	kv := func(key, value string) string {
		return fitLine(m.styles.accent.Render(fmt.Sprintf("  %-16s", key))+value, max(1, width))
	}
	// The block grew past a dozen rows, at which point an unbroken list stops
	// being readable. Three groups match the three questions a reader actually
	// arrives with: what did I choose, what is the OS enforcing, and what has
	// this session handed out.
	group := func(name string) string {
		return m.styles.muted.Render("  "+name) + "\n"
	}
	permissions := m.runtime.Config.Permissions
	var b strings.Builder
	b.WriteString(h("Security") + "\n")

	posture := strings.ToLower(strings.TrimSpace(permissions.Preset))
	if posture == "" {
		posture = derivedStanceLabel(permissions) + " (no preset set)"
	} else if summary := appconfig.PresetSummary(posture); summary != "" {
		posture += " — " + summary
	}
	b.WriteString(group("Policy"))
	b.WriteString(kv("stance", posture) + "\n")
	b.WriteString(kv("autonomy", m.runtime.Permissions.Mode()) + "\n")
	b.WriteString(kv("network policy", orDefault(permissions.Network, "open")+" — declared endpoints only, not egress enforcement") + "\n")
	b.WriteString(kv("command policy", orDefault(permissions.Commands, "open")) + "\n")
	b.WriteString(kv("credentials", credentialSummary(permissions.ProtectCredentials)) + "\n")
	b.WriteString(kv("publication", publicationSummary(permissions.Publication)) + "\n")
	b.WriteString(kv("rules", fmt.Sprintf("%d scoped rule(s), %d regex denial(s)", len(permissions.Rules), len(permissions.DeniedCommands))) + "\n")
	// A setting a repository asked for and did not get looks like a bug until
	// it is named, so it is reported here rather than only by config show.
	for _, note := range m.runtime.Config.Clamped {
		b.WriteString(m.styles.warning.Render(fitLine("  ⚠ refused project "+note.Field+"="+note.Requested+"; kept "+note.Effective, max(1, width))) + "\n")
	}

	b.WriteString(group("Enforcement"))
	sandboxSummary := m.runtime.SandboxSummary()
	b.WriteString(kv("sandbox", sandboxSummary) + "\n")
	if strings.Contains(sandboxSummary, "unavailable") || strings.Contains(sandboxSummary, "degraded") {
		b.WriteString(m.styles.warning.Render(fitLine("  ⚠ commands are running with less containment than requested; run collo doctor", max(1, width))) + "\n")
	}
	b.WriteString(kv("egress", egressSummary(permissions)) + "\n")
	b.WriteString(kv("command env", orDefault(permissions.CommandEnv, "minimal when sandboxed")) + "\n")
	b.WriteString(kv("outside reads", fmt.Sprintf("%t (built-in file tools)", permissions.AllowOutsideWorkspace)) + "\n")

	// The record of what was decided belongs beside the settings that decided
	// it. A degraded ledger is reported as a warning row rather than an
	// ordinary one, for the same reason degraded sandboxing is: a control that
	// has quietly stopped working must not read like a control that is on.
	b.WriteString(kv("audit record", auditSummary(m.runtime)) + "\n")
	if failures, first, _ := m.runtime.AuditHealth(); failures > 0 {
		detail := "unknown error"
		if first != nil {
			detail = first.Error()
		}
		b.WriteString(m.styles.warning.Render(fitLine("  ⚠ this session's permission record is incomplete: "+detail, max(1, width))) + "\n")
	}

	b.WriteString(group("This session"))
	commands, hosts, credentials, publications := m.runtime.Permissions.SessionGrants()
	b.WriteString(kv("grants", describeGrants(commands, hosts, credentials, publications)) + "\n")
	if len(commands) > 0 || len(hosts) > 0 || len(credentials) > 0 || len(publications) > 0 {
		b.WriteString(m.styles.muted.Render("  grants last until this process exits; persistent policy belongs in configuration") + "\n")
	}
	b.WriteString(m.styles.muted.Render("  full reference: collo config reference · docs/SECURITY.md") + "\n\n")
	return b.String()
}

// auditSummary states where the permission record for this workspace lives
// and whether it is currently complete, so the answer to "can I reconstruct
// what happened" is visible before it is needed rather than after.
func auditSummary(runtime *app.Runtime) string {
	failures, _, _ := runtime.AuditHealth()
	if failures > 0 {
		return fmt.Sprintf("INCOMPLETE — %d write failure(s) this session; read with collo audit", failures)
	}
	if runtime.Audit == nil {
		return "unavailable — no ledger was opened, so this session is unrecorded"
	}
	return "recording — read with collo audit"
}

// egressSummary reports what a sandboxed command can actually reach, which is
// not always what the setting asks for: scoped egress is enforceable only
// where the backend can deny remote traffic while keeping loopback reachable.
// A platform that cannot do that says so here rather than showing "scoped" and
// quietly behaving like the coarse switch.
func egressSummary(permissions appconfig.Permissions) string {
	if !strings.EqualFold(strings.TrimSpace(permissions.SandboxEgress), appconfig.SandboxEgressScoped) {
		if permissions.SandboxAllowNetwork {
			return "all-or-nothing — command networking allowed"
		}
		return "all-or-nothing — command networking denied"
	}
	allowlist := egress.FromRules(permissions.Rules)
	if supported, why := egress.Supported(); !supported {
		return "scoped requested but unavailable — " + why
	}
	if allowlist.Empty() {
		return "scoped — no host rule set, so every outbound connection is refused"
	}
	return fmt.Sprintf("scoped — brokered to %d allowed host pattern(s)", len(allowlist.Patterns()))
}

// credentialSummary states what reaching a key or token store actually does,
// rather than echoing the setting name back at the reader.
func credentialSummary(setting string) string {
	switch strings.ToLower(strings.TrimSpace(setting)) {
	case appconfig.ProtectCredentialsOff:
		return "off — key and token files are treated as ordinary"
	case appconfig.ProtectCredentialsDeny:
		return "deny — reaching a key or token store is refused"
	default:
		return "prompt — reaching a key or token store always asks"
	}
}

// publicationSummary states what happens when the agent tries to put
// something outside this machine. It sits next to the credential row because
// the two settings answer the same kind of question: what a broad approval is
// not allowed to sweep in as a side effect.
func publicationSummary(setting string) string {
	switch strings.ToLower(strings.TrimSpace(setting)) {
	case appconfig.PublicationOff:
		return "off — publishing and deploying are ordinary commands"
	case appconfig.PublicationDeny:
		return "deny — publishing, pushing, and deploying are refused"
	default:
		return "prompt — publishing, pushing, or deploying always asks"
	}
}

func describeGrants(commands, hosts, credentials, publications []string) string {
	var parts []string
	if len(commands) > 0 {
		parts = append(parts, "commands "+strings.Join(commands, ", "))
	}
	if len(hosts) > 0 {
		parts = append(parts, "endpoints "+strings.Join(hosts, ", "))
	}
	if len(credentials) > 0 {
		parts = append(parts, fmt.Sprintf("credentials %d file(s)", len(credentials)))
	}
	if len(publications) > 0 {
		parts = append(parts, "publication "+strings.Join(publications, ", "))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " · ")
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
