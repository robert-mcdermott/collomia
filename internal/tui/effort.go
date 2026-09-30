package tui

import (
	"fmt"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/app"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// effortStatusMsg carries /effort's status once the model's accepted levels
// have been looked up, which for a local runtime costs a request.
type effortStatusMsg struct{ status app.EffortStatus }

// renderEffortStatus is the /effort panel: what the next request carries, the
// setting that decided it, and what the model accepts.
func renderEffortStatus(status app.EffortStatus) string {
	effective := status.Effective
	if effective == "" {
		effective = "none sent — the model decides"
	}
	lines := []string{
		fmt.Sprintf("%-12s %s/%s", "Model", status.Provider, status.Model),
		fmt.Sprintf("%-12s %s", "Effort", effective),
		fmt.Sprintf("%-12s %s", "Decided by", status.Source),
		fmt.Sprintf("%-12s %s", "Accepts", acceptedLevels(status.Support)),
	}
	lines = append(lines, "",
		"/effort <level>   use a level for the rest of this session",
		"/effort default   send no effort",
		"/effort reset     go back to what configuration says",
		"",
		"Session only. Run `collo setup --provider "+status.Provider+"` to save a model's effort.")
	return strings.Join(lines, "\n")
}

func acceptedLevels(support provider.ReasoningSupport) string {
	switch {
	case support.Unsupported():
		return "no effort control for this model"
	case !support.Known():
		return "not published for this model; any level can be tried: " + strings.Join(provider.EffortLevels, ", ")
	}
	text := strings.Join(support.Levels, ", ")
	if support.Default != "" {
		text += " (default " + support.Default + ")"
	}
	if support.Source == provider.LimitsEndpoint {
		return text + " — reported by the endpoint"
	}
	return text + " — published limits"
}

// effortChangeMessage confirms an /effort change in one line, naming what it
// now outranks so an agent profile's setting is never silently replaced.
func effortChangeMessage(status app.EffortStatus) string {
	if !status.Override {
		return fmt.Sprintf("Reasoning effort follows configuration again: %s (%s).", orNone(status.Effective), status.Source)
	}
	message := fmt.Sprintf("Reasoning effort for %s is %s for the rest of this session, from the next turn.", status.Model, orNone(status.Effective))
	if !status.Support.Known() && status.Effective != "" {
		message += " This model's levels are not published, so the endpoint may ignore or refuse it."
	}
	return message + " It outranks configuration and agent profiles until /effort reset."
}

func orNone(effort string) string {
	if effort == "" {
		return "none sent"
	}
	return effort
}
