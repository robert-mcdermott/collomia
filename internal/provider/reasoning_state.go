package provider

import (
	"sort"
	"strings"
	"sync"
)

// ReasoningState is the provider-bound reasoning of one assistant message:
// every content block of the response in the order the provider produced it,
// with the reasoning blocks carrying the signature or opaque payload that lets
// the provider continue from them.
//
// Anthropic (directly, through Azure Foundry, or through Bedrock) requires the
// thinking blocks of an assistant turn to come back unmodified, in place, when
// the conversation continues after a tool call; a model that does not get them
// back re-derives its reasoning from scratch. Order matters because thinking
// can sit between tool calls, which is why text and tool-use positions are
// recorded too rather than only the reasoning.
//
// It is never rendered: readable thinking lives in Message.Reasoning. It is
// replayed only to the Route that issued it, because a signature means
// nothing to any other endpoint.
type ReasoningState struct {
	// Route identifies the endpoint that signed the blocks, such as
	// "anthropic https://api.anthropic.com" or "bedrock us-east-1".
	Route  string           `json:"route"`
	Model  string           `json:"model"`
	Blocks []ReasoningBlock `json:"blocks"`
}

// ReasoningBlock is one content block of an assistant response.
type ReasoningBlock struct {
	// Kind is "reasoning", "text", or "tool_use".
	Kind string `json:"kind"`
	// Text is readable reasoning (possibly empty when the provider omits it)
	// or answer text.
	Text string `json:"text,omitempty"`
	// Signature authenticates a reasoning block.
	Signature string `json:"signature,omitempty"`
	// Redacted is an encrypted reasoning payload the provider returned in
	// place of readable text.
	Redacted string `json:"redacted,omitempty"`
	// ToolUseID ties a tool_use block to the message's ToolCall of that ID.
	ToolUseID string `json:"tool_use_id,omitempty"`
}

// replayBlocks returns a message's blocks when they can be replayed to route:
// issued there, and still describing exactly the tool calls the message
// carries. A message whose calls were changed after the fact — dropped by a
// termination error, say — is sent without its reasoning rather than with a
// block sequence that no longer matches it.
func replayBlocks(message Message, route string) ([]ReasoningBlock, bool) {
	state := message.ReasoningState
	if state == nil || route == "" || state.Route != route {
		return nil, false
	}
	calls := make(map[string]bool, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		calls[call.ID] = true
	}
	uses := 0
	for _, block := range state.Blocks {
		if block.Kind == "tool_use" {
			if !calls[block.ToolUseID] {
				return nil, false
			}
			uses++
		}
	}
	if uses != len(message.ToolCalls) {
		return nil, false
	}
	return state.Blocks, true
}

// blockRecorder assembles a response's content blocks by index as a stream
// delivers them, for adapters that report reasoning with signatures.
type blockRecorder struct {
	blocks map[int]*ReasoningBlock
}

func (r *blockRecorder) at(index int, kind string) *ReasoningBlock {
	if r.blocks == nil {
		r.blocks = map[int]*ReasoningBlock{}
	}
	block := r.blocks[index]
	if block == nil {
		block = &ReasoningBlock{Kind: kind}
		r.blocks[index] = block
	}
	if block.Kind == "" {
		block.Kind = kind
	}
	return block
}

func (r *blockRecorder) text(index int, text string)  { r.at(index, "text").Text += text }
func (r *blockRecorder) toolUse(index int, id string) { r.at(index, "tool_use").ToolUseID = id }
func (r *blockRecorder) reasoning(index int, text string) {
	r.at(index, "reasoning").Text += text
}
func (r *blockRecorder) signature(index int, signature string) {
	r.at(index, "reasoning").Signature += signature
}
func (r *blockRecorder) redacted(index int, data string) {
	r.at(index, "reasoning").Redacted += data
}

// state returns the recorded blocks as a ReasoningState, or nil when the
// response carried no reasoning block with something to replay. Readable text
// alone is not state: without a signature or payload there is nothing the
// provider could verify.
func (r *blockRecorder) state(route, model string) *ReasoningState {
	indexes := make([]int, 0, len(r.blocks))
	replayable := false
	for index, block := range r.blocks {
		indexes = append(indexes, index)
		if block.Kind == "reasoning" && (block.Signature != "" || block.Redacted != "") {
			replayable = true
		}
	}
	if !replayable {
		return nil
	}
	sort.Ints(indexes)
	state := &ReasoningState{Route: route, Model: model}
	for _, index := range indexes {
		block := *r.blocks[index]
		if block.Kind == "text" && block.Text == "" {
			continue
		}
		state.Blocks = append(state.Blocks, block)
	}
	return state
}

// continuityControl remembers, per client, a provider's refusal of replayed
// reasoning or of the requested thinking display, so the refused request is
// retried once without it and not attempted again for this client.
type continuityControl struct {
	mu             sync.Mutex
	replayRefused  bool
	displayRefused bool
	replayWarned   bool
	displayWarned  bool
}

func (c *continuityControl) replay() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.replayRefused
}

func (c *continuityControl) display() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.displayRefused
}

func (c *continuityControl) refuseReplay() {
	c.mu.Lock()
	c.replayRefused = true
	c.mu.Unlock()
}

func (c *continuityControl) refuseDisplay() {
	c.mu.Lock()
	c.displayRefused = true
	c.mu.Unlock()
}

// rejectedThinking reports a request refused over thinking content or
// configuration: a modified or foreign signature, a block bound to a different
// conversation, or a thinking field the model does not accept.
func rejectedThinking(status int, body []byte) bool {
	if status != 400 {
		return false
	}
	message := strings.ToLower(string(body))
	return strings.Contains(message, "thinking") || strings.Contains(message, "signature") ||
		strings.Contains(message, "reasoningcontent") || strings.Contains(message, "reasoning_content")
}

// claudeAdaptiveByDefault names the Claude models that think adaptively when
// no thinking field is sent and omit readable thinking unless asked. For these
// only, requesting display "summarized" changes what is shown and nothing
// about whether or how much the model thinks. Earlier models think only when
// asked, so sending thinking to them would change cost and behavior.
func claudeAdaptiveByDefault(model string) bool {
	normalized := normalizeModelID(model)
	for _, prefix := range []string{"claude-opus-5", "claude-sonnet-5", "claude-fable-5", "claude-mythos-5", "claude-mythos-preview"} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}
