package tui

import (
	"fmt"
	"strings"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
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
func (m Model) securityContent() string {
	h := m.styles.heading.Render
	kv := func(key, value string) string {
		return fitLine(m.styles.accent.Render(fmt.Sprintf("  %-16s", key))+value, max(1, m.width))
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
	b.WriteString(kv("stance", posture) + "\n")
	b.WriteString(kv("autonomy", m.runtime.Permissions.Mode()) + "\n")

	sandboxSummary := m.runtime.SandboxSummary()
	b.WriteString(kv("sandbox", sandboxSummary) + "\n")
	if strings.Contains(sandboxSummary, "unavailable") || strings.Contains(sandboxSummary, "degraded") {
		b.WriteString(m.styles.warning.Render("  commands are running with less containment than requested; run collo doctor") + "\n")
	}
	b.WriteString(kv("command env", orDefault(permissions.CommandEnv, "minimal when sandboxed")) + "\n")
	b.WriteString(kv("outside reads", fmt.Sprintf("%t (built-in file tools)", permissions.AllowOutsideWorkspace)) + "\n")

	b.WriteString(kv("network policy", orDefault(permissions.Network, "open")+" — declared endpoints only, not egress enforcement") + "\n")
	b.WriteString(kv("command policy", orDefault(permissions.Commands, "open")) + "\n")
	b.WriteString(kv("rules", fmt.Sprintf("%d scoped rule(s), %d regex denial(s)", len(permissions.Rules), len(permissions.DeniedCommands))) + "\n")

	commands, hosts := m.runtime.Permissions.SessionGrants()
	b.WriteString(kv("session grants", describeGrants(commands, hosts)) + "\n")
	if len(commands) > 0 || len(hosts) > 0 {
		b.WriteString(m.styles.muted.Render("  grants last until this process exits; persistent policy belongs in configuration") + "\n")
	}
	b.WriteString(m.styles.muted.Render("  full reference: collo config reference · docs/SECURITY.md") + "\n\n")
	return b.String()
}

func describeGrants(commands, hosts []string) string {
	var parts []string
	if len(commands) > 0 {
		parts = append(parts, "commands "+strings.Join(commands, ", "))
	}
	if len(hosts) > 0 {
		parts = append(parts, "endpoints "+strings.Join(hosts, ", "))
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
