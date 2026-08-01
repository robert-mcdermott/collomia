package tui

import (
	"fmt"
	"sort"
	"strings"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

// configPanel renders what the configuration actually resolved to.
//
// The command it replaces printed a path and a sentence about precedence,
// which answers neither question a layered configuration makes hard. Reading
// the file tells you what one layer asked for; it cannot tell you which layer
// won, and it cannot tell you what is in force for the settings no file
// mentions — and the second is most of them.
func configPanel(cfg appconfig.Config, showAll bool) string {
	var out strings.Builder

	out.WriteString("Files, in the order they are applied:\n")
	for _, layer := range cfg.Layers {
		out.WriteString("  " + layerLine(layer) + "\n")
	}

	settings := cfg.SafetySettings()
	heading := "\nEffective safety stance:\n"
	if showAll {
		settings = cfg.EffectiveSettings()
		heading = "\nEvery setting in force:\n"
	}
	out.WriteString(heading)
	out.WriteString(settingsTable(settings))

	if len(cfg.Clamped) > 0 {
		out.WriteString("\nRefused:\n")
		for _, clamped := range cfg.Clamped {
			out.WriteString(fmt.Sprintf("  permissions.%s — the project asked for %q; kept %q\n",
				clamped.Field, clamped.Requested, clamped.Effective))
		}
		out.WriteString("  A project can tighten containment and never loosen it.\n")
	}
	if len(cfg.Quarantined) > 0 {
		quarantined := append([]string(nil), cfg.Quarantined...)
		sort.Strings(quarantined)
		out.WriteString("\nQuarantined until `collo trust`: " + strings.Join(quarantined, ", ") + "\n")
	}

	if !showAll {
		out.WriteString("\n/config all lists every setting, including the ones no file mentions.\n")
	}
	out.WriteString("Edit " + editableTarget(cfg) + ". It is strict JSON and cannot hold comments, so\n")
	out.WriteString("`collo schema config` writes the schema an editor reads for completion and\n")
	out.WriteString("inline validation; `collo config reference` prints the annotated version.")
	return out.String()
}

// layerLine describes one configuration layer, saying why an unapplied one did
// not apply rather than leaving it looking merely absent.
func layerLine(layer appconfig.Layer) string {
	line := fmt.Sprintf("%-9s", layer.Name)
	switch {
	case layer.Path != "":
		line += " " + layer.Path
	case layer.Name == "defaults":
		line += " built in"
	default:
		line += " environment"
	}
	switch {
	case layer.Note != "":
		line += " — " + layer.Note
	case !layer.Applied:
		line += " — not present"
	case len(layer.Keys) > 0:
		line += fmt.Sprintf(" — set %d key(s)", len(layer.Keys))
	}
	return line
}

// settingsTable aligns keys, values, and origins into three columns.
func settingsTable(settings []appconfig.Setting) string {
	if len(settings) == 0 {
		return "  (nothing)\n"
	}
	keyWidth, valueWidth := 0, 0
	for _, setting := range settings {
		keyWidth = max(keyWidth, len(setting.Key))
		valueWidth = max(valueWidth, len(setting.Value))
	}
	// A long value would otherwise push the origin column off a narrow
	// terminal, and the origin is the column this panel exists for.
	valueWidth = min(valueWidth, 28)
	var out strings.Builder
	for _, setting := range settings {
		value := setting.Value
		if len(value) > valueWidth {
			value = value[:valueWidth-1] + "…"
		}
		out.WriteString(fmt.Sprintf("  %-*s  %-*s  %s\n", keyWidth, setting.Key, valueWidth, value, originWords(setting)))
	}
	return out.String()
}

// originWords names where a value came from.
//
// "built-in default" is the answer that carries the most information here: it
// means no file anywhere states this, so someone searching their configuration
// for the setting will not find it — which is exactly the case a merged
// configuration hides and a file cannot answer.
//
// A redacted value with no file behind it is a third case and must not be
// reported as a default: the loader resolves a credential from an environment
// variable or the OS keychain into the merged configuration, so it has a real
// source, just not one in any file.
func originWords(setting appconfig.Setting) string {
	if setting.Redacted && setting.FromDefault() {
		return "resolved at load (environment or credential store)"
	}
	if setting.FromDefault() || setting.Origin == "defaults" {
		return "built-in default"
	}
	return setting.Origin
}

// editableTarget names the file a user should open, preferring the
// highest-precedence one that actually exists.
func editableTarget(cfg appconfig.Config) string {
	if strings.TrimSpace(cfg.Source) != "" {
		return cfg.Source
	}
	return "your configuration file (`collo init` creates one)"
}
