package provider

import (
	"context"
	"encoding/json"
)

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ContentPart is an optional typed part attached to a message. Text-only
// callers continue to use Message.Content. Image bytes are deliberately not
// serialized into durable session JSONL: AttachmentID and the integrity
// metadata are persisted, while the session attachment store resolves Data
// immediately before a provider request.
type ContentPart struct {
	Type         string `json:"type"`
	Text         string `json:"text,omitempty"`
	AttachmentID string `json:"attachment_id,omitempty"`
	Name         string `json:"name,omitempty"`
	MediaType    string `json:"media_type,omitempty"`
	Size         int    `json:"size,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	Data         []byte `json:"-"`
}

const (
	ContentText  = "text"
	ContentImage = "image"
)

type Message struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	Parts      []ContentPart `json:"parts,omitempty"`
	ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

// HasImages reports whether a request carries any typed image content.
func (m Message) HasImages() bool {
	for _, part := range m.Parts {
		if part.Type == ContentImage {
			return true
		}
	}
	return false
}

type Request struct {
	Model       string
	System      string
	Messages    []Message
	Tools       []ToolDefinition
	MaxTokens   int
	Temperature *float64
	// ReasoningEffort is opt-in. An empty value must not alter the provider
	// request, preserving compatibility with models that have no such control.
	ReasoningEffort string
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	// CachedTokens counts prompt tokens served from the provider cache
	// (a subset of InputTokens), when the provider reports it.
	CachedTokens int `json:"cached_tokens,omitempty"`
	// ReasoningTokens counts hidden reasoning output, when reported.
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	// CostUSD is an estimate derived only from user-configured pricing.
	CostUSD float64 `json:"cost_usd,omitempty"`
	// CostAvailable distinguishes a genuine zero-cost estimate from absent
	// pricing or absent usage.
	CostAvailable bool `json:"cost_available,omitempty"`
	// CostEstimated is true for Collomia-calculated costs rather than an
	// authoritative charge returned by a provider.
	CostEstimated bool `json:"cost_estimated,omitempty"`
}

type Response struct {
	Content   string
	ToolCalls []ToolCall
	Usage     Usage
	Stop      string
}

// ToolCallDelta is the provider-neutral streaming representation of a tool
// request. Arguments is an incremental JSON fragment, not necessarily a valid
// document until Done is true. Index preserves provider ordering when several
// tool calls are emitted in parallel.
type ToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	Arguments string
	Done      bool
}

// Delta is one normalized provider stream event. Adapters populate only the
// fields represented by the upstream event. Usage is a complete snapshot, not
// an increment, so consumers can replace a displayed counter without guessing.
type Delta struct {
	Text      string
	Reasoning string
	Warning   string
	ToolCall  *ToolCallDelta
	Usage     *Usage
}

type Client interface {
	Chat(ctx context.Context, req Request, onDelta func(Delta)) (Response, error)
	Name() string
}

// ModelInfo describes one model a provider reports as available.
type ModelInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	// Capabilities describes Collomia's effective adapter support for this
	// model. Catalog APIs that do not publish per-model metadata inherit the
	// adapter declaration and retain unknown model-dependent facts.
	Capabilities Capabilities `json:"capabilities"`
}

// ModelLister is an optional Client capability: providers whose APIs expose
// a model catalog implement it so the UI can offer live discovery.
type ModelLister interface {
	ListModels(ctx context.Context) ([]ModelInfo, error)
}
