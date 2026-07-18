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

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type Request struct {
	Model       string
	System      string
	Messages    []Message
	Tools       []ToolDefinition
	MaxTokens   int
	Temperature *float64
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	// CachedTokens counts prompt tokens served from the provider cache
	// (a subset of InputTokens), when the provider reports it.
	CachedTokens int `json:"cached_tokens,omitempty"`
	// ReasoningTokens counts hidden reasoning output, when reported.
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

type Response struct {
	Content   string
	ToolCalls []ToolCall
	Usage     Usage
	Stop      string
}

type Delta struct {
	Text string
}

type Client interface {
	Chat(ctx context.Context, req Request, onDelta func(Delta)) (Response, error)
	Name() string
}
