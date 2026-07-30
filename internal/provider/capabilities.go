package provider

import (
	"fmt"
	"strconv"
	"strings"
)

// CapabilityState distinguishes a feature Collomia implements from one it
// cannot use, only partially supports, or cannot determine for a particular
// endpoint/model. Unknown is intentionally different from unsupported: many
// OpenAI-compatible catalogs return model names without feature metadata.
type CapabilityState string

const (
	CapabilityUnknown     CapabilityState = "unknown"
	CapabilitySupported   CapabilityState = "supported"
	CapabilityPartial     CapabilityState = "partial"
	CapabilityUnsupported CapabilityState = "unsupported"
)

// Capabilities is the effective feature declaration for one configured
// provider/model selection. It describes what Collomia's adapter can actually
// send and consume, not every feature the upstream vendor may offer.
type Capabilities struct {
	ProviderType      string          `json:"provider_type"`
	Model             string          `json:"model"`
	Tools             CapabilityState `json:"tools"`
	Streaming         CapabilityState `json:"streaming"`
	Reasoning         CapabilityState `json:"reasoning"`
	Images            CapabilityState `json:"images"`
	StructuredOutput  CapabilityState `json:"structured_output"`
	TokenCounting     CapabilityState `json:"token_counting"`
	PromptCaching     CapabilityState `json:"prompt_caching"`
	ParallelToolCalls CapabilityState `json:"parallel_tool_calls"`
	ModelDiscovery    CapabilityState `json:"model_discovery"`
	ContextWindow     int             `json:"context_window,omitempty"`
	Constraints       []string        `json:"constraints,omitempty"`
}

// CapabilityReporter is implemented by built-in clients. Keeping it optional
// preserves the small Client interface for test doubles and custom clients.
type CapabilityReporter interface {
	Capabilities() Capabilities
}

// CapabilitiesFor returns the declaration for a built-in provider type. Model
// identity is retained even where the upstream catalog exposes no per-model
// feature metadata, so callers can report unknown facts without guessing.
func CapabilitiesFor(providerType, model string, contextWindow int) (Capabilities, error) {
	c := Capabilities{
		ProviderType:      providerType,
		Model:             model,
		Tools:             CapabilitySupported,
		Streaming:         CapabilitySupported,
		Reasoning:         CapabilityUnsupported,
		Images:            CapabilityUnsupported,
		StructuredOutput:  CapabilityUnsupported,
		TokenCounting:     CapabilitySupported,
		PromptCaching:     CapabilityPartial,
		ParallelToolCalls: CapabilitySupported,
		ModelDiscovery:    CapabilitySupported,
		ContextWindow:     contextWindow,
	}

	switch providerType {
	case "openai":
		c.Reasoning = CapabilityPartial
		c.Images = CapabilityPartial
		c.Constraints = []string{"Chat Completions adapter; model-specific features may be unknown; explicit max_tokens/temperature rejections are negotiated and remembered for the active model"}
	case "openai-compatible":
		c.Reasoning = CapabilityPartial
		c.Images = CapabilityPartial
		c.Constraints = []string{"compatible endpoints may implement a smaller model-specific subset; accepted request parameters remain unchanged, while explicit max_tokens/temperature rejections are negotiated for the active model"}
	case "anthropic", "anthropic-compatible":
		c.Reasoning = CapabilityPartial
		c.Images = CapabilityPartial
		c.PromptCaching = CapabilitySupported
		c.Constraints = []string{"Messages adapter; provider reasoning deltas are surfaced, but signed thinking blocks are not yet round-tripped; prompt cache breakpoints are sent and dropped for the session if the endpoint rejects them"}
	case "azure-openai":
		c.Reasoning = CapabilityPartial
		c.Images = CapabilityPartial
		c.ModelDiscovery = CapabilityUnsupported
		c.Constraints = []string{"deployment-scoped Chat Completions route; API key, caller-supplied bearer token, or refreshable DefaultAzureCredential authentication; reasoning-model max_completion_tokens/default-temperature requirements are negotiated from explicit provider rejections"}
	case "azure-foundry":
		c.Reasoning = CapabilityPartial
		c.Images = CapabilityPartial
		c.Constraints = []string{"OpenAI v1 Chat Completions route; API key, caller-supplied bearer token, or refreshable DefaultAzureCredential authentication; reasoning-model max_completion_tokens/default-temperature requirements are negotiated from explicit provider rejections"}
	case "azure-foundry-anthropic":
		c.Reasoning = CapabilityPartial
		c.Images = CapabilityPartial
		c.PromptCaching = CapabilitySupported
		c.Constraints = []string{"Anthropic Messages route; provider reasoning deltas are surfaced; prompt cache breakpoints are sent and dropped for the session if the deployment rejects them; API key, caller-supplied bearer token, or refreshable DefaultAzureCredential authentication"}
	case "bedrock":
		c.Reasoning = CapabilityPartial
		c.Images = CapabilityPartial
		c.PromptCaching = CapabilityUnsupported
		c.ModelDiscovery = CapabilityUnsupported
		c.Constraints = []string{"ConverseStream route; SigV4 AWS credentials or Bedrock bearer API key; model access and streaming support are governed by the AWS account, model, and region"}
	case "bedrock-mantle":
		c.Reasoning = CapabilityPartial
		c.Images = CapabilityPartial
		c.PromptCaching = CapabilityUnsupported
		c.ModelDiscovery = CapabilityUnsupported
		c.Constraints = []string{"Responses-style route; requests SSE and accepts synchronous JSON fallback"}
	default:
		return Capabilities{}, fmt.Errorf("unsupported provider type %q", providerType)
	}
	return c, nil
}

// ValidateRequest rejects requests that contradict a known declaration before
// an adapter performs network I/O. Unknown or partial capabilities are allowed:
// they are reported to the user, but are not guessed into hard failures.
func ValidateRequest(c Capabilities, req Request) error {
	if len(req.Tools) > 0 && c.Tools == CapabilityUnsupported {
		return fmt.Errorf("provider %s model %s does not support tool calling", c.ProviderType, c.Model)
	}
	for _, message := range req.Messages {
		if message.HasImages() && c.Images == CapabilityUnsupported {
			return fmt.Errorf("provider %s model %s does not support image input", c.ProviderType, c.Model)
		}
	}
	if c.ContextWindow > 0 && req.MaxTokens > c.ContextWindow {
		return fmt.Errorf("provider %s model %s requests %d output tokens, exceeding its configured %d-token context window", c.ProviderType, c.Model, req.MaxTokens, c.ContextWindow)
	}
	return nil
}

// CompactSummary is short enough for pickers and status displays.
func (c Capabilities) CompactSummary() string {
	var features []string
	for _, item := range []struct {
		label string
		state CapabilityState
	}{
		{"tools", c.Tools},
		{"stream", c.Streaming},
		{"usage", c.TokenCounting},
		{"parallel-tools", c.ParallelToolCalls},
	} {
		if item.state == CapabilitySupported {
			features = append(features, item.label)
		}
	}
	if c.ContextWindow > 0 {
		features = append(features, "context "+compactNumber(c.ContextWindow))
	}
	if len(features) == 0 {
		return "capabilities unknown"
	}
	return strings.Join(features, ", ")
}

// DetailSummary preserves uncertainty and partial support for /models.
func (c Capabilities) DetailSummary() string {
	groups := []struct {
		state  CapabilityState
		prefix string
	}{
		{CapabilitySupported, "supported"},
		{CapabilityPartial, "partial"},
		{CapabilityUnsupported, "unavailable"},
		{CapabilityUnknown, "unknown"},
	}
	features := []struct {
		label string
		state CapabilityState
	}{
		{"tools", c.Tools}, {"streaming", c.Streaming}, {"reasoning", c.Reasoning},
		{"images", c.Images}, {"structured output", c.StructuredOutput},
		{"token usage", c.TokenCounting}, {"prompt caching", c.PromptCaching},
		{"parallel tools", c.ParallelToolCalls}, {"model discovery", c.ModelDiscovery},
	}
	var parts []string
	for _, group := range groups {
		var labels []string
		for _, feature := range features {
			state := feature.state
			if state == "" {
				state = CapabilityUnknown
			}
			if state == group.state {
				labels = append(labels, feature.label)
			}
		}
		if len(labels) > 0 {
			parts = append(parts, group.prefix+": "+strings.Join(labels, ", "))
		}
	}
	if c.ContextWindow > 0 {
		parts = append(parts, "configured context: "+strconv.Itoa(c.ContextWindow))
	}
	return strings.Join(parts, "; ")
}

func compactNumber(value int) string {
	if value >= 1_000_000 && value%1_000_000 == 0 {
		return strconv.Itoa(value/1_000_000) + "M"
	}
	if value >= 1_000 && value%1_000 == 0 {
		return strconv.Itoa(value/1_000) + "K"
	}
	return strconv.Itoa(value)
}
