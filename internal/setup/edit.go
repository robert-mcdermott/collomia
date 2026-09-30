package setup

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/credstore"
)

// These edits change a configured provider without verifying anything,
// because none of them changes what the endpoint is asked. Each goes through
// editConfigFile, so every other setting in the file survives.

// The credential store is reached through these so tests never touch the
// user's own keychain.
var (
	storeAvailable = credstore.Available
	storeGet       = credstore.Get
	storeSet       = credstore.Set
	storeDelete    = credstore.Delete
)

// providerNamePattern keeps provider names to what reads cleanly in a
// command (`/model name/model`) and a credential-store entry.
var providerNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidProviderName reports why a new provider name is unusable, or "".
func ValidProviderName(name string, existing Existing) string {
	switch {
	case strings.TrimSpace(name) == "":
		return "a name is required"
	case !providerNamePattern.MatchString(name):
		return "use letters, digits, dot, hyphen, or underscore, starting with a letter or digit"
	case existing.Has(name):
		return name + " is already configured"
	}
	return ""
}

// SetDefault makes a configured provider and model the default selection.
func SetDefault(path, name, model string) error {
	return editConfigFile(path, func(document, providers map[string]json.RawMessage) error {
		if _, ok := providers[name]; !ok {
			return fmt.Errorf("%s is not configured in %s", name, path)
		}
		var err error
		if document["default_provider"], err = json.Marshal(name); err != nil {
			return err
		}
		document["default_model"], err = json.Marshal(model)
		return err
	})
}

// SettingsEdit is the set of provider fields edited directly: sampling
// temperature and extra request headers. A nil Temperature removes the field,
// so the provider's own default applies; an empty Headers removes them all.
type SettingsEdit struct {
	Temperature *float64
	Headers     map[string]string
}

// EditSettings replaces a provider's temperature and headers.
func EditSettings(path, name string, edit SettingsEdit) error {
	return editProvider(path, name, func(block map[string]json.RawMessage) error {
		delete(block, "temperature")
		delete(block, "headers")
		if edit.Temperature != nil {
			encoded, err := json.Marshal(*edit.Temperature)
			if err != nil {
				return err
			}
			block["temperature"] = encoded
		}
		if len(edit.Headers) > 0 {
			encoded, err := json.Marshal(edit.Headers)
			if err != nil {
				return err
			}
			block["headers"] = encoded
		}
		return nil
	})
}

// RenameProvider renames a provider, repointing default_provider and moving a
// key held in the OS credential store, which is filed under the provider's
// name and would otherwise be stranded under the old one.
//
// The key is copied before the file changes and the old entry removed only
// after it has, so a failure at either step leaves a provider that still
// authenticates.
func RenameProvider(path, from, to string) (movedCredential bool, err error) {
	secret, stored := "", false
	if storeAvailable() {
		if value, ok, getErr := storeGet(from); getErr == nil && ok {
			secret, stored = value, true
			if err := storeSet(to, secret); err != nil {
				return false, fmt.Errorf("copy the stored key to %s: %w", to, err)
			}
		}
	}
	err = editConfigFile(path, func(document, providers map[string]json.RawMessage) error {
		block, ok := providers[from]
		if !ok {
			return fmt.Errorf("%s is not configured in %s", from, path)
		}
		if _, taken := providers[to]; taken {
			return fmt.Errorf("%s is already configured", to)
		}
		providers[to] = block
		delete(providers, from)
		var current string
		_ = json.Unmarshal(document["default_provider"], &current)
		if current == from {
			encoded, err := json.Marshal(to)
			if err != nil {
				return err
			}
			document["default_provider"] = encoded
		}
		return nil
	})
	if err != nil {
		if stored {
			_, _ = storeDelete(to)
		}
		return false, err
	}
	if stored {
		_, _ = storeDelete(from)
	}
	return stored, nil
}

// RemoveProvider deletes a provider from the file. The default provider is
// refused: removing it would leave default_provider naming nothing, and the
// next session would fail to start. A stored key is left in the credential
// store, where `collo auth rm` removes it deliberately.
func RemoveProvider(path, name string) error {
	return editConfigFile(path, func(document, providers map[string]json.RawMessage) error {
		if _, ok := providers[name]; !ok {
			return fmt.Errorf("%s is not configured in %s", name, path)
		}
		var current string
		_ = json.Unmarshal(document["default_provider"], &current)
		if current == name {
			return fmt.Errorf("%s is the default provider; make another provider the default first", name)
		}
		delete(providers, name)
		return nil
	})
}

// SaveMaxTokens records a model's output ceiling, typically the one a provider
// stated when it rejected a larger request. It lands where that model's limit
// is read from: the model's own entry when it has one or is not the
// provider's own model, the provider level otherwise.
func SaveMaxTokens(path, name, model string, maxTokens int) error {
	return editProvider(path, name, func(block map[string]json.RawMessage) error {
		var p appconfig.Provider
		raw, err := json.Marshal(block)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		effective := p.ForModel(model)
		if issues := appconfig.TokenLimitErrors("", effective.Context, maxTokens); len(issues) > 0 && !effective.ContextInherited {
			return fmt.Errorf("max_tokens %s", issues[0].Message)
		}
		encoded, err := json.Marshal(maxTokens)
		if err != nil {
			return err
		}
		entry, hasEntry := p.Models[model]
		if p.Model == model && !(hasEntry && entry.MaxTokens > 0) {
			block["max_tokens"] = encoded
			return nil
		}
		models := map[string]map[string]json.RawMessage{}
		if existing, ok := block["models"]; ok {
			if err := json.Unmarshal(existing, &models); err != nil {
				return fmt.Errorf("models is not an object of objects: %w", err)
			}
		}
		if models[model] == nil {
			models[model] = map[string]json.RawMessage{}
		}
		models[model]["max_tokens"] = encoded
		block["models"], err = json.Marshal(models)
		return err
	})
}

// editProvider edits one provider block as a document.
func editProvider(path, name string, edit func(block map[string]json.RawMessage) error) error {
	return editConfigFile(path, func(_, providers map[string]json.RawMessage) error {
		raw, ok := providers[name]
		if !ok {
			return fmt.Errorf("%s is not configured in %s", name, path)
		}
		block := map[string]json.RawMessage{}
		if err := json.Unmarshal(raw, &block); err != nil {
			return fmt.Errorf("providers.%s is not an object: %w", name, err)
		}
		if err := edit(block); err != nil {
			return err
		}
		encoded, err := json.Marshal(block)
		if err != nil {
			return err
		}
		providers[name] = encoded
		return nil
	})
}

// HeaderLooksSecret reports whether a header's name suggests its value is a
// credential, so an editor can avoid echoing it.
func HeaderLooksSecret(name string) bool {
	lower := strings.ToLower(name)
	for _, marker := range []string{"authorization", "key", "token", "secret", "password", "cookie"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// SortedHeaderNames lists header names in a stable order for an editor.
func SortedHeaderNames(headers map[string]string) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
