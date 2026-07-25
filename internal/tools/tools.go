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
	// Normalized resources for scoped policy rules and the audit ledger.
	Paths       []string
	Executables []string
	// Command is the original immutable command text for additive agent-profile
	// denial regexes. It is populated only by command-bearing built-ins.
	Command string
	// Hosts are the normalized network endpoints the action declares.
	Hosts []string
	// Network marks an action that reaches the network at all, so a scoped
	// network posture can require an explicit grant for it.
	Network bool
	// HostsUndetermined is true when the action reaches an endpoint it could
	// not name (a configured registry, a named Git remote). A host-scoped
	// allow rule must never cover such an action.
	HostsUndetermined bool
	// HostReasons explains every undetermined endpoint.
	HostReasons []string
	Server      string
	// Uninspectable marks actions (typically shell commands) whose full
	// effect could not be statically determined; they always require
	// interactive approval.
	Uninspectable   bool
	AnalysisReasons []string
	// HardDenyReasons are catastrophic outcomes that cannot be approved or
	// overridden by autonomy mode, rules, or session grants.
	HardDenyReasons []string
	// ConfirmReasons are destructive but potentially legitimate operations
	// that require a fresh interactive decision for each invocation.
	ConfirmReasons []string
	// CredentialTargets names the well-known credential stores this action
	// reaches, each as "label: path". Tools that expose Paths do not need to
	// set this; the permission layer derives those itself so a tool added
	// later is covered without having to remember. Command-shaped tools set it
	// from their shell analysis, which sees targets no path field carries.
	CredentialTargets []string
	// Preview carries a human-reviewable rendering of the proposed change
	// (typically a unified diff) for approval prompts.
	Preview string
}

type Tool interface {
	Definition() provider.ToolDefinition
	Assess(args json.RawMessage) (Action, error)
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// AuthorizationObserver is an optional tool hook for durable tool-specific
// state that must reflect a denied outer agent permission. It cannot change
// the decision and runs after Authorize has completed.
type AuthorizationObserver interface {
	ObserveAuthorization(args json.RawMessage, err error)
}

// ExecutionObserver is an optional tool hook for durable state when a
// post-authorization lifecycle gate blocks execution. It observes but cannot
// change the outcome.
type ExecutionObserver interface {
	ObserveExecution(args json.RawMessage, err error)
}

// PermissionIdentity lets a model-facing wrapper preserve an established
// permission-policy name. It does not alter tool lookup or execution.
type PermissionIdentity interface {
	PermissionToolName() string
}

// HookIdentity lets a model-facing wrapper preserve the lifecycle-hook name
// of the underlying action. It does not alter transcript tool names.
type HookIdentity interface {
	HookToolName() string
}

// Result preserves optional typed content returned by a tool. Ordinary tools
// return only Content; MCP tools can additionally return bounded image parts.
type Result struct {
	Content string
	Parts   []provider.ContentPart
}

// Streamer is an optional Tool capability: tools that produce output
// incrementally (long commands) implement it so the UI can show progress
// live. The returned string is still the complete, bounded result.
type Streamer interface {
	ExecuteStream(ctx context.Context, args json.RawMessage, onOutput func(string)) (string, error)
}

// ResultStreamer is the typed counterpart to Streamer.
type ResultStreamer interface {
	ExecuteResultStream(ctx context.Context, args json.RawMessage, onOutput func(string)) (Result, error)
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

// Remove deletes a tool by name. It exists for MCP lifecycle management:
// when a server is disabled, removed, or reconnected, its stale tool entries
// must leave the registry so the model cannot call dead sessions.
func (r *Registry) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}

// Replace atomically withdraws a set of tool names and installs their
// replacements. MCP catalog refreshes use this so the model can observe the
// old catalog or the new catalog, but never a partially refreshed mixture.
func (r *Registry) Replace(remove []string, replacements ...Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range remove {
		delete(r.tools, name)
	}
	for _, tool := range replacements {
		r.tools[tool.Definition().Name] = tool
	}
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

// Clone returns a point-in-time registry containing the same tool instances.
// It is used for delegated agents so profile-specific visibility can be
// applied without mutating the parent's live registry. Stateful tools remain
// shared intentionally; the child's permission manager and plan mode still
// govern every execution.
func (r *Registry) Clone() *Registry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	clone := NewRegistry()
	for _, tool := range r.tools {
		clone.tools[tool.Definition().Name] = tool
	}
	return clone
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

// ExecuteStream runs a tool, streaming incremental output through onOutput
// when the tool supports it; otherwise it behaves exactly like Execute.
func (r *Registry) ExecuteStream(ctx context.Context, name string, args json.RawMessage, onOutput func(string)) (string, error) {
	tool, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("unknown tool %q", name)
	}
	if streamer, can := tool.(Streamer); can && onOutput != nil {
		return streamer.ExecuteStream(ctx, args, onOutput)
	}
	return tool.Execute(ctx, args)
}

// ExecuteResultStream preserves typed content for tools that provide it and
// adapts every existing string-only tool without changing its behavior.
func (r *Registry) ExecuteResultStream(ctx context.Context, name string, args json.RawMessage, onOutput func(string)) (Result, error) {
	tool, ok := r.Get(name)
	if !ok {
		return Result{}, fmt.Errorf("unknown tool %q", name)
	}
	if rich, can := tool.(ResultStreamer); can {
		return rich.ExecuteResultStream(ctx, args, onOutput)
	}
	content, err := r.ExecuteStream(ctx, name, args, onOutput)
	return Result{Content: content}, err
}

type Function struct {
	Def      provider.ToolDefinition
	Action   Action
	AssessFn func(json.RawMessage) (Action, error)
	Run      func(context.Context, json.RawMessage) (string, error)
	// RunResult preserves typed content. When present it is used by both the
	// typed registry path and the legacy string-only Execute method.
	RunResult func(context.Context, json.RawMessage, func(string)) (Result, error)
	// RunStream, when set, is preferred by ExecuteStream so the tool can
	// surface incremental progress (MCP progress notifications, long
	// commands) while still returning the complete result.
	RunStream func(context.Context, json.RawMessage, func(string)) (string, error)
}

func (f Function) Definition() provider.ToolDefinition { return f.Def }
func (f Function) Assess(args json.RawMessage) (Action, error) {
	if f.AssessFn != nil {
		return f.AssessFn(args)
	}
	return f.Action, nil
}
func (f Function) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if f.RunResult != nil {
		result, err := f.RunResult(ctx, args, nil)
		return result.Content, err
	}
	return f.Run(ctx, args)
}
func (f Function) ExecuteStream(ctx context.Context, args json.RawMessage, onOutput func(string)) (string, error) {
	if f.RunResult != nil {
		result, err := f.RunResult(ctx, args, onOutput)
		return result.Content, err
	}
	if f.RunStream != nil {
		return f.RunStream(ctx, args, onOutput)
	}
	return f.Run(ctx, args)
}

func (f Function) ExecuteResultStream(ctx context.Context, args json.RawMessage, onOutput func(string)) (Result, error) {
	if f.RunResult != nil {
		return f.RunResult(ctx, args, onOutput)
	}
	content, err := f.ExecuteStream(ctx, args, onOutput)
	return Result{Content: content}, err
}

func schema(value string) json.RawMessage { return json.RawMessage(value) }
