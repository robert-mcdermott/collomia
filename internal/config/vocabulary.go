package config

import (
	"fmt"
	"slices"
	"strings"
)

// This file holds the enumerated vocabularies — one definition per field.
//
// They were previously inline `switch` literals inside ValidateFields, and
// `permissions.mode` already had two copies: one for the top-level setting and
// one for a delegated agent's. That is the shape this repository keeps finding
// (the inert `host` matcher, the hand-built action in `collo policy check`, the
// `command` rule pattern that matched nothing), and a generated JSON Schema
// would have made it worse by adding a third copy that no compiler relates to
// the first two. The schema reads these lists, so a value the validator accepts
// and the schema rejects — or the reverse, which is worse, since an editor
// would offer a value that fails on load — cannot be written.

// AutonomyModes are the permission stances the primary agent can run under.
func AutonomyModes() []string { return slices.Clone(autonomyModes) }

// AgentAutonomyModes are the stances a delegated profile may request. It is
// the same vocabulary; the intersection with the parent's effective mode is
// applied at runtime rather than here.
func AgentAutonomyModes() []string { return slices.Clone(autonomyModes) }

// SandboxModes are the OS sandbox enforcement settings for commands.
func SandboxModes() []string { return slices.Clone(sandboxModes) }

// NetworkPostures are the postures for actions that reach the network.
func NetworkPostures() []string { return slices.Clone(networkPostures) }

// CommandPostures are the postures for command execution.
func CommandPostures() []string { return slices.Clone(commandPostures) }

// SandboxEgressModes are the sandboxed-egress settings.
func SandboxEgressModes() []string { return slices.Clone(sandboxEgressModes) }

// CommandEnvModes are the environment settings for agent commands.
func CommandEnvModes() []string { return slices.Clone(commandEnvModes) }

// RuleActions are the decisions a permission rule may state.
func RuleActions() []string { return slices.Clone(ruleActions) }

// AgentRuleActions are the decisions a delegated profile's rule may state.
// It is deliberately narrower than RuleActions: a child may tighten the
// parent's policy and never loosen it, so "allow" is absent by design.
func AgentRuleActions() []string { return slices.Clone(agentRuleActions) }

// ProviderTypes are the supported provider adapters.
func ProviderTypes() []string { return slices.Clone(providerTypes) }

// BedrockAuthModes are the authentication modes for a Bedrock provider.
func BedrockAuthModes() []string { return slices.Clone(bedrockAuthModes) }

// AzureAuthModes are the authentication modes for the Azure provider family.
func AzureAuthModes() []string { return slices.Clone(azureAuthModes) }

// AgentAvailabilities are the roles a named agent profile can be selected for.
func AgentAvailabilities() []string { return slices.Clone(agentAvailabilities) }

// ReasoningEfforts are the provider-neutral reasoning levels.
func ReasoningEfforts() []string { return slices.Clone(reasoningEfforts) }

// AgentIntegrationModes decide who may publish retained delegated-worktree
// changes into the parent workspace.
func AgentIntegrationModes() []string { return slices.Clone(agentIntegrationModes) }

// NotificationModes are the TUI attention settings.
func NotificationModes() []string { return slices.Clone(notificationModes) }

// MCPTransports are the supported MCP server transports.
func MCPTransports() []string { return slices.Clone(mcpTransports) }

var (
	autonomyModes         = []string{"ask", "workspace", "autopilot"}
	sandboxModes          = []string{"off", "auto", "require"}
	networkPostures       = []string{"open", "scoped"}
	commandPostures       = []string{"open", "allowlist"}
	sandboxEgressModes    = []string{SandboxEgressOff, SandboxEgressScoped}
	commandEnvModes       = []string{"full", "minimal"}
	ruleActions           = []string{"allow", "prompt", "deny"}
	agentRuleActions      = []string{"prompt", "deny"}
	agentAvailabilities   = []string{"delegate", "primary", "both"}
	reasoningEfforts      = []string{"low", "medium", "high", "xhigh", "max"}
	agentIntegrationModes = []string{"manual", "reviewed"}
	notificationModes     = []string{"on", "bell", "off"}
	mcpTransports         = []string{"stdio", "http", "streamable-http"}

	providerTypes = []string{
		"openai", "openai-compatible",
		"anthropic", "anthropic-compatible",
		"bedrock", "bedrock-mantle",
		"azure-openai", "azure-foundry", "azure-foundry-anthropic",
	}
	bedrockAuthModes = []string{"auto", "sigv4", "bearer"}
	azureAuthModes   = []string{"api_key", "bearer", "entra"}
)

// enumField is one enumerated value and the vocabulary that decides it.
//
// The normalization flags are not stylistic. Fields differ in whether they
// accept an absent value (the ones normalization fills later do) and in
// whether they fold case, and both properties have to reach the schema:
// omitting `optional` would make an editor flag a field the loader is happy to
// supply, and omitting `fold` would make it flag a spelling the loader accepts.
type enumField struct {
	field    string
	value    string
	allowed  []string
	optional bool
	fold     bool
	// suffix qualifies the generated message where one vocabulary belongs to a
	// particular provider family ("… for Bedrock").
	suffix string
}

// check returns the field error this value produces, if any.
func (e enumField) check() (FieldError, bool) {
	value := e.value
	if e.fold {
		value = strings.ToLower(strings.TrimSpace(value))
	}
	if value == "" && e.optional {
		return FieldError{}, false
	}
	if slices.Contains(e.allowed, value) {
		return FieldError{}, false
	}
	return FieldError{e.field, fmt.Sprintf("must be %s%s (got %q)", englishList(e.allowed), e.suffix, e.value)}, true
}

// appendEnumErrors checks several enumerated fields in declaration order.
func appendEnumErrors(errs []FieldError, fields ...enumField) []FieldError {
	for _, field := range fields {
		if err, bad := field.check(); bad {
			errs = append(errs, err)
		}
	}
	return errs
}

// englishList renders a vocabulary the way the existing validation messages
// do, so the wording people have already read does not change underneath them.
func englishList(values []string) string {
	switch len(values) {
	case 0:
		return ""
	case 1:
		return values[0]
	case 2:
		return values[0] + " or " + values[1]
	default:
		return strings.Join(values[:len(values)-1], ", ") + ", or " + values[len(values)-1]
	}
}

// ValidEnumValue reports whether a value is in a vocabulary, applying the same
// folding the validator does. It exists for callers outside this package that
// need to check a value without assembling a Config.
func ValidEnumValue(value string, vocabulary []string) bool {
	return slices.Contains(vocabulary, strings.ToLower(strings.TrimSpace(value)))
}
