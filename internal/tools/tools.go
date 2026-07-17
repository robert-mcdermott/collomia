package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/robert-mcdermott/collomia/internal/provider"
)

type Risk string

const (
	RiskRead     Risk = "read"
	RiskWrite    Risk = "write"
	RiskExecute  Risk = "execute"
	RiskExternal Risk = "external"
)

type Action struct {
	Risk    Risk
	Summary string
	Outside bool
}

type Tool interface {
	Definition() provider.ToolDefinition
	Assess(args json.RawMessage) (Action, error)
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry(items ...Tool) *Registry {
	r := &Registry{tools: map[string]Tool{}}
	for _, item := range items {
		r.Add(item)
	}
	return r
}

func (r *Registry) Add(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Definition().Name] = tool
}

func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *Registry) Definitions(allow func(Tool) bool) []provider.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]provider.ToolDefinition, 0, len(r.tools))
	for _, tool := range r.tools {
		if allow == nil || allow(tool) {
			defs = append(defs, tool.Definition())
		}
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}

func (r *Registry) Names() []string {
	defs := r.Definitions(nil)
	names := make([]string, len(defs))
	for i, def := range defs {
		names[i] = def.Name
	}
	return names
}

func (r *Registry) Assess(name string, args json.RawMessage) (Action, error) {
	tool, ok := r.Get(name)
	if !ok {
		return Action{}, fmt.Errorf("unknown tool %q", name)
	}
	return tool.Assess(args)
}

func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) (string, error) {
	tool, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("unknown tool %q", name)
	}
	return tool.Execute(ctx, args)
}

type Function struct {
	Def      provider.ToolDefinition
	Action   Action
	AssessFn func(json.RawMessage) (Action, error)
	Run      func(context.Context, json.RawMessage) (string, error)
}

func (f Function) Definition() provider.ToolDefinition { return f.Def }
func (f Function) Assess(args json.RawMessage) (Action, error) {
	if f.AssessFn != nil {
		return f.AssessFn(args)
	}
	return f.Action, nil
}
func (f Function) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return f.Run(ctx, args)
}

func schema(value string) json.RawMessage { return json.RawMessage(value) }
