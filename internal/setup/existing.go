package setup

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
)

// Existing describes what the file setup is about to write already contains.
//
// It reads that one file rather than the merged configuration, and the
// distinction is load-bearing. `appconfig.Load` returns defaults, user, and a
// trusted project layer composed together, so a provider defined in a trusted
// repository's `.collomia.json` appears in the result — and setup writes the
// global file. Warning "this replaces ollama" about an entry in a project file
// names something setup will not touch, and worse, the project layer overrides
// the user layer, so the write would be shadowed and the wizard would look like
// it had done nothing.
type Existing struct {
	// Path is the file these facts came from.
	Path string
	// Providers are the names configured in that file, sorted.
	Providers []string
	// Models maps a provider name to the model recorded for it, so a
	// replacement can show what it is replacing rather than only that it is.
	Models map[string]string
	// DefaultProvider and DefaultModel are the file's current selection.
	DefaultProvider string
	DefaultModel    string
}

// Has reports whether a provider name is already configured in this file.
func (e Existing) Has(name string) bool {
	for _, existing := range e.Providers {
		if existing == name {
			return true
		}
	}
	return false
}

// Describes returns a short description of what is currently recorded for a
// provider, for the "you are replacing this" line.
func (e Existing) Describes(name string) string {
	if model := strings.TrimSpace(e.Models[name]); model != "" {
		return model
	}
	return "no model recorded"
}

// HasDefault reports whether the file already selects a default provider.
func (e Existing) HasDefault() bool { return strings.TrimSpace(e.DefaultProvider) != "" }

// ReadExisting parses the configuration file setup writes, tolerating absence.
//
// A file that cannot be parsed is reported as an error rather than as an empty
// configuration: setup is a reasonable thing to reach for when a file is
// broken, but silently treating a broken file as empty would let a merge
// destroy settings the user still has.
func ReadExisting(path string) (Existing, error) {
	result := Existing{Path: path, Models: map[string]string{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return result, nil
	}

	var document struct {
		DefaultProvider string `json:"default_provider"`
		DefaultModel    string `json:"default_model"`
		Providers       map[string]struct {
			Model string `json:"model"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return result, err
	}
	result.DefaultProvider, result.DefaultModel = document.DefaultProvider, document.DefaultModel
	for name, entry := range document.Providers {
		result.Providers = append(result.Providers, name)
		result.Models[name] = entry.Model
	}
	sort.Strings(result.Providers)
	return result, nil
}
