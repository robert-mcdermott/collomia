package app

import (
	"context"
	"fmt"
	"strings"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/setup"
)

// EffortStatus describes the reasoning effort the next request will carry and
// where that choice came from.
type EffortStatus struct {
	Provider string
	Model    string
	// Effective is the effort sent, or "" when none is.
	Effective string
	// Source names the setting that decided Effective, in words.
	Source string
	// Override reports that /effort decided it for this session.
	Override bool
	// Support is what the model is known to accept.
	Support provider.ReasoningSupport
}

// withEffortOverride applies /effort's session choice, which outranks every
// configured source including an agent profile: it is the most recent and
// most specific thing the user said.
func (r *Runtime) withEffortOverride(p appconfig.Provider) appconfig.Provider {
	if r.effortOverride == nil {
		return p
	}
	if *r.effortOverride == "" {
		p.Reasoning = nil
		return p
	}
	p.Reasoning = &appconfig.Reasoning{Effort: *r.effortOverride}
	return p
}

// EffortStatus reports the current effort. Support comes from what is already
// known — the listed catalog and the published table — without a request.
func (r *Runtime) EffortStatus() EffortStatus {
	providerName, model := r.Agent.Selection()
	status := EffortStatus{Provider: providerName, Model: model, Support: r.knownReasoning(providerName, model)}
	if settings := r.Agent.ProviderSettings(); settings.Reasoning != nil {
		status.Effective = settings.Reasoning.Effort
	}
	configured := r.Config.Providers[providerName]
	profile, hasProfile := r.Config.Agents[r.ActiveAgent]
	entry, hasEntry := configured.Models[model]
	switch {
	case r.effortOverride != nil:
		status.Override, status.Source = true, "set with /effort for this session"
	case hasProfile && profile.Reasoning != nil:
		status.Source = "agent profile " + r.ActiveAgent
	case hasEntry && entry.Reasoning != nil:
		status.Source = "providers." + providerName + ".models." + model + " in configuration"
	case configured.Reasoning != nil:
		status.Source = "providers." + providerName + " in configuration"
	default:
		status.Source = "nothing configured; the model decides"
	}
	return status
}

// ResolveEffortSupport asks the endpoint what the current model accepts where
// that costs a request (Ollama's model description), and remembers the answer
// for this session. It is for the status view, never for a turn.
func (r *Runtime) ResolveEffortSupport(ctx context.Context) EffortStatus {
	providerName, model := r.Agent.Selection()
	if support := r.knownReasoning(providerName, model); !support.Known() || support.Source != provider.LimitsEndpoint {
		resolved := setup.ModelReasoning(ctx, r.Config.Providers[providerName], model, nil)
		if resolved.Known() {
			r.catalogMu.Lock()
			if r.catalogReasoning == nil {
				r.catalogReasoning = map[string]map[string]provider.ReasoningSupport{}
			}
			if r.catalogReasoning[providerName] == nil {
				r.catalogReasoning[providerName] = map[string]provider.ReasoningSupport{}
			}
			r.catalogReasoning[providerName][model] = resolved
			r.catalogMu.Unlock()
		}
	}
	return r.EffortStatus()
}

func (r *Runtime) knownReasoning(providerName, model string) provider.ReasoningSupport {
	r.catalogMu.Lock()
	support, ok := r.catalogReasoning[providerName][model]
	r.catalogMu.Unlock()
	if ok {
		return support
	}
	if support, ok := provider.KnownReasoning(model); ok {
		return support
	}
	return provider.ReasoningSupport{}
}

// SetEffort changes the reasoning effort for the rest of this session.
//
// "default" sends no effort at all; "reset" returns to what configuration
// says. A level the model is known not to accept is refused with the ones it
// does accept, rather than being sent and silently dropped by the adapter's
// recovery. The change is never written to configuration.
func (r *Runtime) SetEffort(level string) (EffortStatus, error) {
	level = strings.ToLower(strings.TrimSpace(level))
	providerName, model := r.Agent.Selection()
	previous := r.effortOverride
	switch level {
	case "reset":
		r.effortOverride = nil
	case "default":
		none := ""
		r.effortOverride = &none
	default:
		if !validEffort(level) {
			return r.EffortStatus(), fmt.Errorf("unknown effort %q; use one of %s, default, or reset", level, strings.Join(provider.EffortLevels, ", "))
		}
		support := r.knownReasoning(providerName, model)
		if support.Unsupported() {
			return r.EffortStatus(), fmt.Errorf("%s has no reasoning-effort control", model)
		}
		if support.Known() && !support.Supports(level) {
			return r.EffortStatus(), fmt.Errorf("%s accepts %s", model, strings.Join(support.Levels, ", "))
		}
		chosen := level
		r.effortOverride = &chosen
	}
	if err := r.Select(providerName, model); err != nil {
		r.effortOverride = previous
		return r.EffortStatus(), err
	}
	return r.EffortStatus(), nil
}

func validEffort(level string) bool {
	for _, candidate := range provider.EffortLevels {
		if candidate == level {
			return true
		}
	}
	return false
}
