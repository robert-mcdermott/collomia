package app

import (
	"fmt"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// ProviderReload describes what applying a changed configuration file did to
// the running session.
type ProviderReload struct {
	// Active is the provider/model the session runs with afterwards.
	ActiveProvider, ActiveModel string
	// Reselected reports that the active provider's settings changed on disk
	// and were applied, so new limits and effort take effect next turn.
	Reselected bool
	// Shadowed names a provider whose written change is overridden by a
	// project configuration layer, so it does not apply in this workspace.
	Shadowed string
}

// ReloadProviders applies provider changes written to the user configuration
// by `/providers`, without restarting the session.
//
// Only providers and the default selection are taken from the reloaded file.
// Permissions, options, hooks, and MCP servers stay exactly as the session
// started: `/providers` edits provider blocks, and reloading safety posture
// mid-session as a side effect of changing a model would be a change nobody
// asked for.
func (r *Runtime) ReloadProviders(changed, writtenModel string) (ProviderReload, error) {
	loaded, err := appconfig.Load(r.Workspace)
	if err != nil {
		return ProviderReload{}, err
	}
	for name, fresh := range loaded.Providers {
		// A credential supplied to this process at first run lives only in
		// memory; a reload from disk would drop it and leave the session's
		// own provider unable to authenticate.
		if old, ok := r.Config.Providers[name]; ok && fresh.APIKey == "" && old.APIKey != "" && old.APIKeyEnv == fresh.APIKeyEnv {
			fresh.APIKey = old.APIKey
			loaded.Providers[name] = fresh
		}
	}
	r.Config.Providers = loaded.Providers
	r.Config.DefaultProvider, r.Config.DefaultModel = loaded.DefaultProvider, loaded.DefaultModel
	r.catalogMu.Lock()
	r.catalogLimits = map[string]map[string]provider.Limits{}
	r.catalogReasoning = map[string]map[string]provider.ReasoningSupport{}
	r.catalogMu.Unlock()

	result := ProviderReload{}
	if fresh, ok := loaded.Providers[changed]; ok && writtenModel != "" && fresh.Model != writtenModel {
		result.Shadowed = changed
	}
	active, model := r.Agent.Selection()
	if _, ok := r.Config.Providers[active]; !ok {
		return result, fmt.Errorf("the active provider %q is no longer configured; choose another with /model", active)
	}
	if active == changed {
		if err := r.Select(active, model); err != nil {
			return result, err
		}
		result.Reselected = true
	}
	result.ActiveProvider, result.ActiveModel = r.Agent.Selection()
	return result, nil
}
