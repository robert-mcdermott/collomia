package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// KeybindingActions is the stable set of global TUI actions users may remap.
// Approval and question-dialog decisions are intentionally excluded: their
// visible y/a/n/enter/escape controls remain fixed and unambiguous.
var KeybindingActions = []string{
	"agent_control",
	"compose_editor",
	"context_rail",
	"diff_view",
	"next_tab",
	"page_down",
	"page_up",
	"scroll_bottom",
	"scroll_top",
	"session_picker",
	"toggle_mouse",
	"toggle_tool_output",
	"transcript_view",
}

// DefaultKeybindings returns a fresh copy of Collomia's default global keys.
func DefaultKeybindings() map[string]string {
	// alt+e and alt+r are deliberate: every ctrl+letter that reads as a
	// mnemonic here (ctrl+e line end, ctrl+b character backward) is already an
	// emacs motion the composer's textarea binds, and a global handler would
	// shadow it before the editor ever saw the key.
	return map[string]string{
		"agent_control":      "alt+a",
		"compose_editor":     "alt+e",
		"context_rail":       "alt+r",
		"diff_view":          "ctrl+d",
		"next_tab":           "ctrl+t",
		"page_down":          "pgdown",
		"page_up":            "pgup",
		"scroll_bottom":      "end",
		"scroll_top":         "home",
		"session_picker":     "alt+s",
		"toggle_mouse":       "alt+m",
		"toggle_tool_output": "ctrl+o",
		"transcript_view":    "ctrl+y",
	}
}

var configurableKeyRE = regexp.MustCompile(`^(?:(?:ctrl|alt)\+[a-z0-9]|f(?:[1-9]|1[0-2])|pgup|pgdown|home|end)$`)

// ValidateKeybindings rejects misspelled actions, keys Bubble Tea cannot
// identify consistently, and global collisions. Keys used only inside a
// modal may intentionally overlap these global keys because modal routing
// takes precedence.
func ValidateKeybindings(bindings map[string]string) []FieldError {
	known := make(map[string]struct{}, len(KeybindingActions))
	for _, action := range KeybindingActions {
		known[action] = struct{}{}
	}
	keys := make([]string, 0, len(bindings))
	for action := range bindings {
		keys = append(keys, action)
	}
	sort.Strings(keys)
	seen := map[string]string{}
	var errs []FieldError
	for _, action := range keys {
		field := "options.keybindings." + action
		key := strings.ToLower(strings.TrimSpace(bindings[action]))
		if _, ok := known[action]; !ok {
			errs = append(errs, FieldError{field, fmt.Sprintf("unknown action (known: %s)", strings.Join(KeybindingActions, ", "))})
			continue
		}
		if !configurableKeyRE.MatchString(key) {
			errs = append(errs, FieldError{field, fmt.Sprintf("unsupported key %q (use ctrl+letter, alt+letter, f1-f12, pgup, pgdown, home, or end)", bindings[action])})
			continue
		}
		if prior, exists := seen[key]; exists {
			errs = append(errs, FieldError{field, fmt.Sprintf("key %q is already assigned to %s", key, prior)})
			continue
		}
		seen[key] = action
	}
	for _, action := range KeybindingActions {
		if strings.TrimSpace(bindings[action]) == "" {
			errs = append(errs, FieldError{"options.keybindings." + action, "a key is required"})
		}
	}
	return errs
}
