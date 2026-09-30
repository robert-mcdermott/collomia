package setup

import (
	"strconv"
	"strings"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// LimitProposal is what the limits screen opens with for one of the two token
// limits, and what established it.
type LimitProposal struct {
	// Value is the proposed number and Source says what established it.
	Value  int
	Source provider.LimitSource
	// Detected is what this run's discovery established, recorded only when
	// the proposal is a configured value that differs from it, so the screen
	// can show both rather than silently preferring one.
	Detected       int
	DetectedSource provider.LimitSource
}

// Assumed reports whether nothing established the proposal. That is the case
// the screen asks about instead of stating.
func (p LimitProposal) Assumed() bool { return p.Source == provider.LimitsAssumed }

// maxPlausibleLimit rejects a number no model has, which in practice is a typo
// with an extra digit or two. It is deliberately far above any published
// window so it can never refuse a real one.
const maxPlausibleLimit = 100_000_000

// ProposeLimits decides what the limits screen opens with.
//
// A new provider opens on what this run resolved from the endpoint, the
// published-limits table, or an assumption. Re-verifying the model a file
// already configures opens on the file's own numbers instead, labelled as
// configured, with the freshly detected ones beside them. A limit someone set
// deliberately — lowered to save memory, say — must not be replaced because
// setup ran again. A different model gets its own detected limits, because a
// window written for one model says nothing about another.
//
// "Configured for this model" means the model's own `models` entry, or the
// provider-level limits when this is the provider's own model. A
// provider-level limit written for a different model is not a configuration
// of this one.
func ProposeLimits(result Result, previous appconfig.Provider) (contextWindow, maxOutput LimitProposal) {
	contextWindow = LimitProposal{Value: result.Provider.Context, Source: result.Limits.ContextSource}
	maxOutput = LimitProposal{Value: result.Provider.MaxTokens, Source: result.Limits.OutputSource}
	if strings.TrimSpace(previous.Model) == "" {
		return contextWindow, maxOutput
	}
	configured := previous.ForModel(result.Model)
	window, output := configured.Context, configured.MaxTokens
	if configured.ContextInherited {
		window = 0
	}
	if configured.MaxTokensInherited {
		output = 0
	}
	return keepConfigured(contextWindow, window), keepConfigured(maxOutput, output)
}

func keepConfigured(detected LimitProposal, configured int) LimitProposal {
	if configured <= 0 || configured == detected.Value {
		return detected
	}
	return LimitProposal{
		Value: configured, Source: provider.LimitsConfigured,
		Detected: detected.Value, DetectedSource: detected.Source,
	}
}

// ParseLimits turns what was typed on the limits screen into the limits to
// write, or a problem to show beside the form.
//
// A blank field accepts its proposal. That is how an assumption is accepted:
// deliberately, on a screen that says it is one, rather than written without
// anyone seeing it. A number equal to the proposal keeps the proposal's
// source, so accepting a reported window does not relabel it as a guess the
// user made; any other number is the user's and is labelled configured.
func ParseLimits(contextText, outputText string, contextWindow, maxOutput LimitProposal) (provider.Limits, string) {
	window, windowSource, problem := parseLimit("Context window", contextText, contextWindow)
	if problem != "" {
		return provider.Limits{}, problem
	}
	output, outputSource, problem := parseLimit("Max output", outputText, maxOutput)
	if problem != "" {
		return provider.Limits{}, problem
	}
	// A blank assumed output cap follows the window it is paired with, exactly
	// as Build clamps one. The assumption was never about this model, so it must
	// not be the reason a window the user just typed is refused.
	if strings.TrimSpace(outputText) == "" && maxOutput.Assumed() && output >= window {
		output = window / 2
	}
	for _, issue := range appconfig.TokenLimitErrors("", window, output) {
		label := "Context window"
		if issue.Field == "max_tokens" {
			label = "Max output"
		}
		return provider.Limits{}, label + " " + issue.Message
	}
	return provider.Limits{
		ContextWindow: window, ContextSource: windowSource,
		MaxOutput: output, OutputSource: outputSource,
	}, ""
}

func parseLimit(label, text string, proposal LimitProposal) (int, provider.LimitSource, string) {
	text = strings.TrimSpace(text)
	if text == "" {
		if proposal.Value <= 0 {
			return 0, "", label + " is required"
		}
		return proposal.Value, proposal.Source, ""
	}
	// Thousands separators are how people copy these numbers out of vendor
	// documentation, so they are accepted rather than refused.
	cleaned := strings.NewReplacer(",", "", "_", "", " ", "").Replace(text)
	value, err := strconv.Atoi(cleaned)
	if err != nil || value <= 0 {
		return 0, "", label + " must be a whole number of tokens, such as 131072"
	}
	if value > maxPlausibleLimit {
		return 0, "", label + " " + cleaned + " is larger than any model's window; check for an extra digit"
	}
	if value == proposal.Value {
		return value, proposal.Source, ""
	}
	return value, provider.LimitsConfigured, ""
}
