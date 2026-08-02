package goalgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

const (
	ReviseToolName = "propose_goal_graph_revision"
	BlockToolName  = "block_goal_node"
)

// MetaTool reports graph-control tools that should not be counted as task
// evidence. They change scheduling state but perform no repository or external
// action and grant no permission.
func MetaTool(name string) bool {
	return name == ReviseToolName || name == BlockToolName
}

// RevisionTool lets the model propose a bounded logical replan using
// optimistic concurrency. The runtime validates and applies it; the tool does
// not rewrite attempts or evidence and is present only in graph-controlled
// execution.
type RevisionTool struct{ Graph *Graph }

func (t RevisionTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{
		Name:        ReviseToolName,
		Description: "Propose a bounded revision to the runtime-owned logical goal graph after recorded evidence changes the approach. base_generation must equal the generation shown in pinned graph state. Send the complete revised logical graph, preserving or explicitly changing each node's primary/read_only execution class. The runtime preserves immutable attempts/evidence, rejects stale/cyclic revisions, and invalidates affected downstream nodes. This changes scheduling only and grants no permission or scope.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"base_generation":{"type":"integer","minimum":1},"reason":{"type":"string","minLength":1,"maxLength":4096},"goal":{"type":"string","minLength":1,"maxLength":2048},"nodes":{"type":"array","minItems":1,"maxItems":12,"items":{"type":"object","properties":{"id":{"type":"integer"},"title":{"type":"string","minLength":1,"maxLength":512},"depends_on":{"type":"array","maxItems":12,"items":{"type":"integer"}},"acceptance":{"type":"array","maxItems":8,"items":{"type":"string","minLength":1,"maxLength":512}},"execution":{"type":"string","enum":["primary","read_only"]}},"required":["id","title"],"additionalProperties":false}}},"required":["base_generation","reason","goal","nodes"],"additionalProperties":false}`),
	}
}

func (t RevisionTool) Assess(json.RawMessage) (tools.Action, error) {
	if t.Graph == nil {
		return tools.Action{}, errors.New("goal graph is unavailable")
	}
	return tools.Action{Risk: tools.RiskRead, Summary: "propose a bounded goal graph revision"}, nil
}

func (t RevisionTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	if t.Graph == nil {
		return "", errors.New("goal graph is unavailable")
	}
	var input struct {
		BaseGeneration uint64     `json:"base_generation"`
		Reason         string     `json:"reason"`
		Goal           string     `json:"goal"`
		Nodes          []NodeSpec `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", err
	}
	if err := t.Graph.Revise(ctx, input.BaseGeneration, input.Reason, Spec{Goal: input.Goal, Nodes: input.Nodes}); err != nil {
		return "", err
	}
	return fmt.Sprintf("Goal graph revision accepted at generation %d. The runtime will select the next dependency-ready node.\n%s", t.Graph.Generation(), t.Graph.Render()), nil
}

// BlockTool gives the model a typed way to report a real blocker. The runtime
// owns the transition and retains the exact reason; a final-sounding prose
// response cannot silently turn an open node into blocked.
type BlockTool struct{ Graph *Graph }

func (t BlockTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{
		Name:        BlockToolName,
		Description: "Mark the currently running goal-graph node blocked with the exact material reason after safe alternatives are exhausted or user authority/input is required. attempt_id must equal the active attempt shown in pinned graph state. This ends the graph as blocked when no other ready work can resolve it; it grants no permission.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"attempt_id":{"type":"string","minLength":1},"reason":{"type":"string","minLength":1,"maxLength":4096}},"required":["attempt_id","reason"],"additionalProperties":false}`),
	}
}

func (t BlockTool) Assess(raw json.RawMessage) (tools.Action, error) {
	if t.Graph == nil {
		return tools.Action{}, errors.New("goal graph is unavailable")
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tools.Action{}, err
	}
	if strings.TrimSpace(input.Reason) == "" {
		return tools.Action{}, errors.New("blocked node reason must not be empty")
	}
	return tools.Action{Risk: tools.RiskRead, Summary: "block the current goal graph node: " + strings.TrimSpace(input.Reason)}, nil
}

func (t BlockTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	if t.Graph == nil {
		return "", errors.New("goal graph is unavailable")
	}
	var input struct {
		AttemptID string `json:"attempt_id"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", err
	}
	if err := t.Graph.BlockActive(ctx, input.AttemptID, input.Reason); err != nil {
		return "", err
	}
	return "Goal graph node recorded as blocked: " + strings.TrimSpace(input.Reason), nil
}
