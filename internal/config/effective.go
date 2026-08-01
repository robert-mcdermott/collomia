package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Setting is one configuration key as it actually took effect, with the layer
// that decided it.
type Setting struct {
	// Key is the dotted path, matching the keys used in Config.Origins.
	Key string
	// Value is the effective value, rendered for display and redacted where
	// the key is one that can hold a credential.
	Value string
	// Origin names the layer that last set the key, or "" when nothing in any
	// file did and a built-in default is in force.
	Origin string
	// Redacted records that Value is a placeholder rather than the real one.
	Redacted bool
}

// FromDefault reports whether no configuration file set this key.
func (s Setting) FromDefault() bool { return s.Origin == "" }

// EffectiveSettings returns every configuration key in force, in sorted order,
// each attributed to the layer that set it.
//
// This answers a question the layered loader could not previously be asked
// from inside a session: not "what is in my file" — which people can read for
// themselves — but "which of my four files won, and what is in force where
// none of them said anything". Those are the two cases a merged configuration
// makes genuinely hard to reason about, and the second is invisible in every
// file on disk.
func (c Config) EffectiveSettings() []Setting {
	encoded, err := json.Marshal(c)
	if err != nil {
		return nil
	}
	var root map[string]any
	if err := json.Unmarshal(encoded, &root); err != nil {
		return nil
	}
	var settings []Setting
	var walk func(prefix string, value any)
	walk = func(prefix string, value any) {
		object, ok := value.(map[string]any)
		if !ok || len(object) == 0 {
			// Arrays and empty objects are leaves, matching flattenJSON so the
			// paths here and the paths in Origins are the same paths.
			rendered, redacted := renderSetting(prefix, value)
			settings = append(settings, Setting{Key: prefix, Value: rendered, Origin: c.Origins[prefix], Redacted: redacted})
			return
		}
		for key, child := range object {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			walk(path, child)
		}
	}
	walk("", root)
	sort.Slice(settings, func(i, j int) bool { return settings[i].Key < settings[j].Key })
	return settings
}

// renderSetting formats one value for display, redacting the keys that can
// carry a credential.
//
// Redaction is decided by *where the value sits*, never by what it looks like.
// A pattern matcher has to recognize a secret to protect it, and the secret it
// fails to recognize is printed in full; a structural rule protects a
// credential from a provider nobody has heard of yet. This matters more here
// than it would in a log, because the merged configuration is not the file:
// `resolveProviderEnvironment` copies a resolved credential — from the
// environment, or from the OS keychain — into Provider.APIKey, so a panel that
// printed the effective configuration verbatim would display a secret that
// appears in no file the user could think to check.
func renderSetting(path string, value any) (string, bool) {
	if isCredentialPath(path) {
		if value == nil || value == "" {
			return "(unset)", false
		}
		return "(redacted)", true
	}
	switch typed := value.(type) {
	case nil:
		return "(unset)", false
	case string:
		if typed == "" {
			return `""`, false
		}
		return typed, false
	case bool:
		return fmt.Sprint(typed), false
	case float64:
		if typed == float64(int64(typed)) {
			return fmt.Sprint(int64(typed)), false
		}
		return fmt.Sprint(typed), false
	case []any:
		if len(typed) == 0 {
			return "(none)", false
		}
		// A short list of plain values reads better than a count; anything
		// longer, or structured, is summarized so one rule set cannot push the
		// rest of the panel off the screen.
		var parts []string
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return fmt.Sprintf("%d entr%s", len(typed), plural(len(typed))), false
			}
			parts = append(parts, text)
		}
		joined := strings.Join(parts, ", ")
		if len(joined) > 60 {
			return fmt.Sprintf("%d entr%s", len(typed), plural(len(typed))), false
		}
		return joined, false
	case map[string]any:
		return "(empty)", false
	default:
		return fmt.Sprint(typed), false
	}
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// isCredentialPath reports whether a configuration path can hold a secret.
//
// Three shapes, all structural: the provider API key itself; anything under a
// `headers` map, since that is where a bearer token or an api-key header goes;
// and anything under an MCP server's `env`, which is how a server is handed
// its token.
func isCredentialPath(path string) bool {
	segments := strings.Split(path, ".")
	last := segments[len(segments)-1]
	if last == "api_key" {
		return true
	}
	for i, segment := range segments {
		if (segment == "headers" || segment == "env") && i < len(segments)-1 {
			return true
		}
	}
	return false
}

// SafetySettings returns the effective containment stance: the settings a
// project layer may tighten and may never loosen.
//
// It reads ContainmentFields rather than naming the settings again, so a new
// clamped field appears here without anyone remembering to add it — the
// alternative being a stance display that is silently one field out of date,
// which is the failure mode the whole clamp exists to prevent.
func (c Config) SafetySettings() []Setting {
	// Reflected from the struct rather than filtered out of EffectiveSettings,
	// and that is not a shortcut avoided but a defect fixed. Those settings are
	// read from the marshalled configuration, where `omitempty` removes any
	// field sitting at its zero value — so `sandbox_egress` and `command_env`
	// disappeared whenever they were unset, and `sandbox_allow_network` and
	// `sandbox_allow_read_outside_workspace` disappeared precisely when they
	// were turned **off**. A containment display that silently omits a
	// tightened boundary is worse than no display: it reads as though the
	// setting is not there to be had.
	wanted := make(map[string]bool, len(ContainmentFields())+1)
	for _, field := range ContainmentFields() {
		wanted[field] = true
	}
	// The autonomy mode is not clamped — a project may set it freely — but it
	// is the first thing anyone reading a stance wants to know, and omitting it
	// would make the panel answer a narrower question than it appears to.
	wanted["mode"] = true

	permissions := reflect.ValueOf(c.Permissions)
	permissionsType := permissions.Type()
	var stance []Setting
	for i := 0; i < permissionsType.NumField(); i++ {
		name, ok := jsonFieldName(permissionsType.Field(i))
		if !ok || !wanted[name] {
			continue
		}
		key := "permissions." + name
		stance = append(stance, Setting{
			Key:    key,
			Value:  containmentValue(name, permissions.Field(i)),
			Origin: c.Origins[key],
		})
	}
	sort.Slice(stance, func(i, j int) bool { return stance[i].Key < stance[j].Key })
	return stance
}

// containmentValue renders one containment setting, naming the value in force
// when the field is empty.
//
// An empty string is not a value a user can act on: `permissions.network: ""`
// behaves as `open`, and reporting the blank would make the stance display
// answer "nothing is set" to a question that was "what is in force".
func containmentValue(field string, value reflect.Value) string {
	switch value.Kind() {
	case reflect.Bool:
		return fmt.Sprint(value.Bool())
	case reflect.String:
		if text := strings.TrimSpace(value.String()); text != "" {
			return text
		}
		if fallback, ok := containmentWhenUnset[field]; ok {
			return fallback + " (unset)"
		}
		return "(unset)"
	default:
		return fmt.Sprint(value.Interface())
	}
}

// containmentWhenUnset names the behavior of each containment setting that is
// left out of the file entirely.
//
// TestEveryContainmentSettingNamesItsUnsetBehavior fails when a clamped string
// setting has no entry, so a new posture cannot ship with a stance display that
// reports it as blank.
var containmentWhenUnset = map[string]string{
	"mode":                "ask",
	"network":             "open",
	"commands":            "open",
	"sandbox":             "auto",
	"sandbox_egress":      SandboxEgressOff,
	"command_env":         "full",
	"protect_credentials": ProtectCredentialsPrompt,
	"publication":         PublicationPrompt,
}
