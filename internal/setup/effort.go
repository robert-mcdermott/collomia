package setup

import (
	"context"
	"encoding/json"
	"strings"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// EffortChoice is one row on setup's reasoning-effort screen.
type EffortChoice struct {
	// Level is the effort to write, or "" for the row that writes none.
	Level  string
	Label  string
	Detail string
}

// EffortChoices lays out the effort screen for one model.
//
// The first row always writes no effort for this model, which is how
// reasoning stays opt-in: selecting nothing changes nothing. When the provider
// already carries a provider-level effort, that row says so, because "send
// nothing for this model" would then mean "use the provider's". A model whose
// support is unknown is offered every level, marked untested; a model known to
// have no effort control gets no screen at all (see Offered).
func EffortChoices(support provider.ReasoningSupport, providerLevel string) []EffortChoice {
	first := EffortChoice{Label: "Model default", Detail: "send no effort; the model decides"}
	if support.Default != "" {
		first.Detail = "send no effort; the model uses " + support.Default
	}
	if providerLevel != "" {
		first = EffortChoice{Label: "Provider setting", Detail: "use this provider's reasoning effort, " + providerLevel}
	}
	choices := []EffortChoice{first}
	levels, detail := support.Levels, ""
	if !support.Known() {
		levels, detail = provider.EffortLevels, "untested for this model"
	}
	for _, level := range levels {
		row := EffortChoice{Level: level, Label: level, Detail: detail}
		switch {
		case level == support.Default:
			row.Detail = "the model's own default"
		case level == "none" && detail != "":
			row.Detail = "no reasoning; " + detail
		case level == "none":
			row.Detail = "no reasoning"
		}
		choices = append(choices, row)
	}
	return choices
}

// Offered reports whether setup should ask about effort for this model at all.
func Offered(support provider.ReasoningSupport) bool { return !support.Unsupported() }

// ConfiguredEffort is the effort a model's own `models` entry already sets,
// which the effort screen opens on when re-verifying that model.
func ConfiguredEffort(previous appconfig.Provider, model string) string {
	if entry, ok := previous.Models[model]; ok && entry.Reasoning != nil {
		return entry.Reasoning.Effort
	}
	return ""
}

// WithEffort records the effort chosen on the effort screen. An empty level
// removes this model's own effort, so the model default — or a provider-level
// effort — applies. Setup owns only the chosen model's `reasoning`; a
// provider-level `reasoning` is the user's and is never touched.
func (r Result) WithEffort(level string) Result {
	r.Effort, r.EffortChosen = level, true
	return r
}

// VerifyEffort sends one short request with the chosen effort and reports
// whether the endpoint accepted it.
//
// Adapters recover from a rejected effort by retrying without it and emitting
// a warning, which keeps a session working but would let setup write a level
// that is silently discarded on every request. The warning is therefore the
// signal here. An endpoint that ignores an unknown field without complaint
// cannot be told apart from one that honours it, which is why the screen only
// claims "accepted".
func VerifyEffort(ctx context.Context, name string, p appconfig.Provider, model, level string) (accepted bool, detail string) {
	client, err := provider.New(name, p, model)
	if err != nil {
		return false, err.Error()
	}
	callCtx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	var warning string
	_, err = client.Chat(callCtx, provider.Request{
		Model:           model,
		Messages:        []provider.Message{{Role: "user", Content: verifyPrompt}},
		MaxTokens:       verifyMaxTokens,
		ReasoningEffort: level,
	}, func(delta provider.Delta) {
		if strings.Contains(delta.Warning, "reasoning effort") && warning == "" {
			warning = delta.Warning
		}
	})
	switch {
	case warning != "":
		return false, warning
	case err != nil:
		return false, err.Error()
	}
	return true, ""
}

// applyEffort writes or removes the chosen model's own `reasoning` inside a
// provider block, leaving every other model entry and the provider-level
// `reasoning` alone.
func applyEffort(block map[string]json.RawMessage, result Result) error {
	if !result.EffortChosen {
		return nil
	}
	models := map[string]map[string]json.RawMessage{}
	if raw, ok := block["models"]; ok {
		if err := json.Unmarshal(raw, &models); err != nil {
			return nil
		}
	}
	entry := models[result.Model]
	if entry == nil {
		entry = map[string]json.RawMessage{}
	}
	if result.Effort == "" {
		delete(entry, "reasoning")
	} else {
		encoded, err := json.Marshal(appconfig.Reasoning{Effort: result.Effort})
		if err != nil {
			return err
		}
		entry["reasoning"] = encoded
	}
	if len(entry) == 0 {
		delete(models, result.Model)
	} else {
		models[result.Model] = entry
	}
	if len(models) == 0 {
		delete(block, "models")
		return nil
	}
	encoded, err := json.Marshal(models)
	if err != nil {
		return err
	}
	block["models"] = encoded
	return nil
}
