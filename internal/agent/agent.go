package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robert-mcdermott/collomia/internal/audit"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/failureid"
	"github.com/robert-mcdermott/collomia/internal/hooks"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/prompts"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/session"
	"github.com/robert-mcdermott/collomia/internal/skills"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// maxDelegateConcurrency bounds how many delegated tasks run at once,
// regardless of how many the model requests in one call.
const maxDelegateConcurrency = 4

// maxDelegateTasks bounds how many tasks a single delegate call may spawn.
const maxDelegateTasks = 6

// ErrTokenBudgetExceeded is returned before another provider request or tool
// execution when a delegated agent's configured token budget is exhausted.
var ErrTokenBudgetExceeded = errors.New("delegated-agent token budget exhausted")

// ErrCostBudgetExceeded is returned before another provider request or tool
// execution when an agent's configured estimated-cost budget is exhausted.
var ErrCostBudgetExceeded = errors.New("agent cost budget exhausted")

// ErrPersistenceUnavailable marks a fail-stop run: the durable session could
// no longer accept records, so starting another provider call or tool would
// make recovery ambiguous.
var ErrPersistenceUnavailable = errors.New("durable session persistence unavailable")

var delegateIDCounter atomic.Uint64

// Emit receives the typed runtime events defined in internal/event.
type Emit = event.Emit

type Agent struct {
	mu                  sync.RWMutex
	worktreeMu          sync.Mutex
	client              provider.Client
	providerName        string
	model               string
	providerConfig      appconfig.Provider
	registry            *tools.Registry
	permissions         *permission.Manager
	workspace           string
	catalog             skills.Catalog
	projectInstructions string
	messages            []provider.Message
	usage               provider.Usage
	lastInputTokens     int
	usageWatermark      int
	maxIterations       int
	maxToolOutput       int
	tokenBudget         int
	costBudgetUSD       float64
	disabled            map[string]bool
	allowedTools        map[string]bool
	allowedSkills       map[string]bool
	profileName         string
	profileInstructions string
	planMode            bool
	subagent            bool
	onMessage           func(provider.Message)
	onCompaction        func(summary provider.Message, replaced int)
	pinnedContext       func() string
	artifacts           *session.ArtifactManager
	attachments         *session.AttachmentManager
	lifecycle           *hooks.Runner
	auditRedact         func(string) string
	onUsage             func(provider.Usage)
	onAction            func(string)
	takeSteering        func() []string
	persistenceError    func() error
}

type Options struct {
	Client                         provider.Client
	ProviderName, Model, Workspace string
	ProviderConfig                 appconfig.Provider
	Registry                       *tools.Registry
	Permissions                    *permission.Manager
	Catalog                        skills.Catalog
	ProjectInstructions            string
	MaxIterations, MaxToolOutput   int
	// TokenBudget bounds cumulative provider-reported input plus output tokens.
	// Zero disables this additional bound.
	TokenBudget int
	// CostBudgetUSD bounds estimated provider spend. It requires explicit
	// pricing on the selected provider.
	CostBudgetUSD      float64
	DisabledTools      []string
	PlanMode, Subagent bool
	// OnMessage observes every message appended to the conversation, for
	// durable session persistence.
	OnMessage func(provider.Message)
	// OnCompaction observes context compactions (summary + replaced count).
	OnCompaction func(summary provider.Message, replaced int)
	// PinnedContext returns authoritative session state that must survive
	// compaction, such as the current structured plan.
	PinnedContext func() string
	// Artifacts retains bounded oversized tool results for on-demand reads.
	Artifacts *session.ArtifactManager
	// Attachments resolves session-local image references immediately before
	// provider requests and retains rich image results from tools.
	Attachments *session.AttachmentManager
	// Hooks runs configured lifecycle-hook commands; nil disables hooks.
	Hooks *hooks.Runner
	// AuditRedact scrubs configured secrets from delegated-agent audit entries.
	AuditRedact func(string) string
	// OnUsage observes cumulative usage after each completed provider request.
	OnUsage func(provider.Usage)
	// OnAction observes the child's current provider-side activity.
	OnAction func(string)
	// TakeSteering returns parent guidance queued for the next provider
	// boundary. It is used only by delegated agents.
	TakeSteering func() []string
	// PersistenceError reports a latched durable-session failure. The agent
	// checks it before provider and tool boundaries so a failed session log
	// cannot be followed by another external or mutating action.
	PersistenceError func() error
}

func New(opts Options) *Agent {
	disabled := map[string]bool{}
	for _, name := range opts.DisabledTools {
		disabled[name] = true
	}
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = 24
	}
	if opts.MaxToolOutput <= 0 {
		opts.MaxToolOutput = 64 * 1024
	}
	return &Agent{client: opts.Client, providerName: opts.ProviderName, model: opts.Model, providerConfig: opts.ProviderConfig, registry: opts.Registry, permissions: opts.Permissions, workspace: opts.Workspace, catalog: opts.Catalog, projectInstructions: opts.ProjectInstructions, maxIterations: opts.MaxIterations, maxToolOutput: opts.MaxToolOutput, tokenBudget: opts.TokenBudget, costBudgetUSD: opts.CostBudgetUSD, disabled: disabled, planMode: opts.PlanMode, subagent: opts.Subagent, onMessage: opts.OnMessage, onCompaction: opts.OnCompaction, pinnedContext: opts.PinnedContext, artifacts: opts.Artifacts, attachments: opts.Attachments, lifecycle: opts.Hooks, auditRedact: opts.AuditRedact, onUsage: opts.OnUsage, onAction: opts.OnAction, takeSteering: opts.TakeSteering, persistenceError: opts.PersistenceError}
}

// appendMessage adds to the conversation and notifies the persistence hook.
func (a *Agent) appendMessage(message provider.Message) {
	a.mu.Lock()
	a.messages = append(a.messages, message)
	observe := a.onMessage
	a.mu.Unlock()
	if observe != nil {
		observe(message)
	}
}

// SetHooks rebinds the persistence callbacks, used when switching durable
// sessions at runtime.
func (a *Agent) SetHooks(onMessage func(provider.Message), onCompaction func(provider.Message, int)) {
	a.mu.Lock()
	a.onMessage = onMessage
	a.onCompaction = onCompaction
	a.mu.Unlock()
}

// SetPersistenceGuard rebinds fail-stop persistence checking when the active
// durable session changes.
func (a *Agent) SetPersistenceGuard(check func() error) {
	a.mu.Lock()
	a.persistenceError = check
	a.mu.Unlock()
}

func (a *Agent) checkPersistence() error {
	a.mu.RLock()
	check := a.persistenceError
	a.mu.RUnlock()
	if check == nil {
		return nil
	}
	if err := check(); err != nil {
		return fmt.Errorf("%w; refusing further provider or tool actions: %w", ErrPersistenceUnavailable, err)
	}
	return nil
}

// SetMessages replaces the active conversation, used when resuming a
// durable session. The persistence hook is not notified (the messages are
// already stored).
func (a *Agent) SetMessages(messages []provider.Message) {
	a.mu.Lock()
	a.messages = append([]provider.Message(nil), messages...)
	a.usageWatermark = len(a.messages)
	a.lastInputTokens = 0
	a.mu.Unlock()
}

func (a *Agent) Run(ctx context.Context, prompt string, emit Emit) (string, error) {
	return a.RunWithParts(ctx, prompt, nil, emit)
}

// RunWithParts submits a prompt with optional typed content. Existing
// text-only callers use Run and retain identical request/session shapes.
func (a *Agent) RunWithParts(ctx context.Context, prompt string, parts []provider.ContentPart, emit Emit) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("prompt is empty")
	}
	if len(parts) > session.AttachmentTurnLimit {
		return "", fmt.Errorf("a turn may contain at most %d attachments", session.AttachmentTurnLimit)
	}
	for _, part := range parts {
		if part.Type != provider.ContentImage {
			return "", fmt.Errorf("unsupported prompt content type %q", part.Type)
		}
	}
	send := func(e event.Event) {
		if emit != nil {
			emit(e)
		}
	}
	send(event.New(event.KindTurnStart))
	if err := a.checkPersistence(); err != nil {
		return "", reportError(send, err)
	}
	if err := a.lifecycle.Gate(ctx, hooks.Payload{Event: "user_prompt", Workspace: a.workspace, Subject: "user_prompt", Prompt: prompt}); err != nil {
		blocked := fmt.Errorf("prompt blocked by hook: %w", err)
		blocked = reportError(send, blocked)
		return "", blocked
	}
	retainedParts, err := a.retainPromptParts(parts)
	if err != nil {
		wrapped := fmt.Errorf("retain prompt attachments: %w", err)
		wrapped = reportError(send, wrapped)
		return "", wrapped
	}
	a.appendMessage(provider.Message{Role: "user", Content: prompt, Parts: retainedParts})
	if err := a.checkPersistence(); err != nil {
		return "", reportError(send, err)
	}
	for iteration := 1; iteration <= a.maxIterations; iteration++ {
		if err := a.checkPersistence(); err != nil {
			return "", reportError(send, err)
		}
		if a.shouldCompact() {
			if _, err := a.compact(ctx, "", send); err != nil {
				reportError(send, fmt.Errorf("automatic compaction failed: %w", err))
			}
			if err := a.checkPersistence(); err != nil {
				return "", reportError(send, err)
			}
		}
		select {
		case <-ctx.Done():
			err := reportError(send, ctx.Err())
			return "", err
		default:
		}
		a.applySteering()
		a.mu.RLock()
		messages := append([]provider.Message(nil), a.messages...)
		client := a.client
		model := a.model
		plan := a.planMode
		a.mu.RUnlock()
		if a.attachments != nil {
			var resolveErr error
			messages, resolveErr = a.attachments.ResolveMessages(messages)
			if resolveErr != nil {
				wrapped := fmt.Errorf("prepare provider attachments: %w", resolveErr)
				wrapped = reportError(send, wrapped)
				return "", wrapped
			}
		}
		// After attachment resolution, because the trailing state is
		// generated here rather than retained in the session and so has no
		// attachment to resolve.
		if state, ok := a.turnState(); ok {
			messages = append(messages, state)
		}
		if client == nil {
			err := reportError(send, errors.New("no provider client configured"))
			return "", err
		}
		a.mu.RLock()
		onAction := a.onAction
		providerName := a.providerName
		a.mu.RUnlock()
		if onAction != nil {
			onAction("calling " + providerName + "/" + model)
		}
		defs := a.toolDefinitions(plan)
		requestMaxTokens, budgetErr := a.nextRequestMaxTokens()
		if budgetErr != nil {
			budgetErr = reportError(send, budgetErr)
			return "", budgetErr
		}
		reasoningEffort := ""
		if a.providerConfig.Reasoning != nil {
			reasoningEffort = a.providerConfig.Reasoning.Effort
		}
		req := provider.Request{Model: model, System: a.systemPrompt(plan), Messages: messages, Tools: defs, MaxTokens: requestMaxTokens, Temperature: a.providerConfig.Temperature, ReasoningEffort: reasoningEffort}
		if reporter, ok := client.(provider.CapabilityReporter); ok {
			if err := provider.ValidateRequest(reporter.Capabilities(), req); err != nil {
				wrapped := fmt.Errorf("provider capability preflight: %w", err)
				wrapped = reportError(send, wrapped)
				return "", wrapped
			}
		}
		var streamedUsage atomic.Bool
		response, err := client.Chat(ctx, req, func(delta provider.Delta) {
			if delta.Text != "" {
				e := event.New(event.KindTextDelta)
				e.Text = delta.Text
				send(e)
			}
			if delta.Reasoning != "" {
				e := event.New(event.KindReasoningDelta)
				e.Text = delta.Reasoning
				send(e)
			}
			if delta.ToolCall != nil {
				e := event.New(event.KindToolCallDelta)
				e.ToolCall = &event.ToolCallDelta{Index: delta.ToolCall.Index, ID: delta.ToolCall.ID, Name: delta.ToolCall.Name, ArgumentsDelta: delta.ToolCall.Arguments, Done: delta.ToolCall.Done}
				send(e)
			}
			if delta.Usage != nil {
				streamedUsage.Store(true)
				streamUsage := estimateCost(*delta.Usage, a.providerConfig.Pricing)
				e := event.New(event.KindUsage)
				e.Usage = eventUsage(streamUsage)
				send(e)
			}
			if delta.Warning != "" {
				e := event.New(event.KindWarning)
				e.Text = delta.Warning
				send(e)
			}
		})
		if err != nil {
			err = reportError(send, err)
			return "", err
		}
		response.Usage = estimateCost(response.Usage, a.providerConfig.Pricing)
		a.mu.Lock()
		a.usage.InputTokens += response.Usage.InputTokens
		a.usage.OutputTokens += response.Usage.OutputTokens
		a.usage.CachedTokens += response.Usage.CachedTokens
		a.usage.CacheWriteTokens += response.Usage.CacheWriteTokens
		a.usage.ReasoningTokens += response.Usage.ReasoningTokens
		a.usage.CostUSD += response.Usage.CostUSD
		a.usage.CostAvailable = a.usage.CostAvailable || response.Usage.CostAvailable
		a.usage.CostEstimated = a.usage.CostEstimated || response.Usage.CostEstimated
		if response.Usage.InputTokens > 0 {
			a.lastInputTokens = response.Usage.InputTokens
			a.usageWatermark = len(a.messages)
		}
		usage := a.usage
		onUsage := a.onUsage
		budget := a.tokenBudget
		costBudget := a.costBudgetUSD
		a.mu.Unlock()
		if onUsage != nil {
			onUsage(usage)
		}
		a.appendMessage(provider.Message{Role: "assistant", Content: response.Content, ToolCalls: response.ToolCalls})
		if err := a.checkPersistence(); err != nil {
			return response.Content, reportError(send, err)
		}
		if !streamedUsage.Load() && (response.Usage.InputTokens > 0 || response.Usage.OutputTokens > 0) {
			e := event.New(event.KindUsage)
			e.Usage = eventUsage(response.Usage)
			send(e)
		}
		if budget > 0 && usage.InputTokens+usage.OutputTokens > budget {
			err := fmt.Errorf("%w: provider reported %d tokens after a limit of %d", ErrTokenBudgetExceeded, usage.InputTokens+usage.OutputTokens, budget)
			err = reportError(send, err)
			return response.Content, err
		}
		if costBudget > 0 {
			if !response.Usage.CostAvailable {
				err := fmt.Errorf("%w: provider did not return usable token accounting for configured pricing", ErrCostBudgetExceeded)
				return response.Content, reportError(send, err)
			}
			if usage.CostUSD > costBudget {
				err := fmt.Errorf("%w: estimated spend $%.6f exceeded $%.6f", ErrCostBudgetExceeded, usage.CostUSD, costBudget)
				return response.Content, reportError(send, err)
			}
		}
		if len(response.ToolCalls) == 0 {
			send(event.New(event.KindTurnEnd))
			a.lifecycle.Fire(ctx, hooks.Payload{Event: "stop", Workspace: a.workspace, Subject: "stop", Detail: map[string]any{"iterations": iteration}})
			return response.Content, nil
		}
		for _, call := range response.ToolCalls {
			if err := a.checkPersistence(); err != nil {
				return response.Content, reportError(send, err)
			}
			result, fatalErr := a.executeTool(ctx, call, plan, send)
			if fatalErr != nil {
				return response.Content, reportError(send, fatalErr)
			}
			a.appendMessage(provider.Message{Role: "tool", ToolCallID: call.ID, Content: result.Content, Parts: result.Parts})
		}
	}
	err = fmt.Errorf("agent stopped after %d iterations", a.maxIterations)
	err = reportError(send, err)
	return "", err
}

// applySteering installs queued parent guidance as an explicit conversational
// update only between iterations. It cannot affect an in-flight provider call,
// executing tool, or pending permission decision.
func (a *Agent) applySteering() {
	a.mu.RLock()
	take := a.takeSteering
	// A delegated child is steered by its parent; the primary agent is
	// steered by the person at the keyboard. Naming the wrong one would tell
	// the model an instruction came from somewhere it did not, which is
	// exactly the distinction the rest of the prompt asks it to respect.
	source := "User"
	if a.subagent {
		source = "Parent"
	}
	a.mu.RUnlock()
	if take == nil {
		return
	}
	for _, guidance := range take() {
		guidance = strings.TrimSpace(guidance)
		if guidance == "" {
			continue
		}
		a.appendMessage(provider.Message{Role: "user", Content: source + " steering update (follow this for the remaining task; it does not grant permissions):\n" + guidance})
	}
}

// retainPromptParts moves in-memory user images into the durable session only
// after lifecycle hooks accept the prompt. This prevents blocked prompts from
// consuming attachment quota or leaving unreferenced files behind.
func (a *Agent) retainPromptParts(parts []provider.ContentPart) ([]provider.ContentPart, error) {
	retained := make([]provider.ContentPart, 0, len(parts))
	savedIDs := make([]string, 0, len(parts))
	cleanup := func() {
		if a.attachments == nil {
			return
		}
		for _, id := range savedIDs {
			_ = a.attachments.Remove(id)
		}
	}
	for _, part := range parts {
		if part.AttachmentID != "" && len(part.Data) == 0 {
			retained = append(retained, part)
			continue
		}
		if len(part.Data) == 0 {
			cleanup()
			return nil, fmt.Errorf("image %q has neither data nor a session attachment reference", part.Name)
		}
		if a.attachments == nil || !a.attachments.Available() {
			retained = append(retained, part)
			continue
		}
		saved, err := a.attachments.SaveBytes(part.Name, part.MediaType, part.Data)
		if err != nil {
			cleanup()
			return nil, err
		}
		retained = append(retained, saved)
		savedIDs = append(savedIDs, saved.AttachmentID)
	}
	return retained, nil
}

// withOverriddenContent replaces a write_file call's proposed content with
// a selectively-applied version (the user approved only some hunks). It is
// the only tool hunk review currently supports.
func withOverriddenContent(toolName string, args json.RawMessage, content string) (json.RawMessage, error) {
	if toolName != "write_file" {
		return nil, fmt.Errorf("hunk selection is not supported for %q", toolName)
	}
	var decoded map[string]any
	if err := json.Unmarshal(args, &decoded); err != nil {
		return nil, err
	}
	decoded["content"] = content
	return json.Marshal(decoded)
}

func errorEvent(err error) event.Event {
	e := event.New(event.KindError)
	e.Error = err.Error()
	e.FailureID = failureid.ID(err)
	if providerErr, ok := provider.AsError(err); ok {
		e.Provider = &event.ProviderFailure{
			Name: providerErr.Provider, Operation: providerErr.Operation,
			Kind: string(providerErr.Kind), StatusCode: providerErr.StatusCode,
			Retryable: providerErr.Retryable, RetryAfterMS: providerErr.RetryAfter.Milliseconds(),
			RequestID: providerErr.RequestID,
		}
	}
	return e
}

func reportError(send Emit, err error) error {
	err = failureid.Ensure(err)
	if send != nil {
		send(errorEvent(err))
	}
	return err
}

func (a *Agent) executeTool(ctx context.Context, call provider.ToolCall, plan bool, send Emit) (tools.Result, error) {
	item, hasItem := a.registry.Get(call.Name)
	a.mu.RLock()
	disabled := a.disabled[call.Name]
	if len(a.allowedTools) > 0 && !a.allowedTools[call.Name] {
		disabled = true
	}
	allowedSkills := a.allowedSkills
	if identity, ok := item.(tools.PermissionIdentity); hasItem && ok {
		disabled = disabled || a.disabled[identity.PermissionToolName()]
	}
	a.mu.RUnlock()
	if disabled || (a.subagent && parentOnlyTool(call.Name)) {
		return tools.Result{Content: "Tool blocked: " + call.Name + " is not available to this agent."}, nil
	}
	if call.Name == "load_skill" && len(allowedSkills) > 0 {
		var input struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(call.Arguments, &input) == nil && !allowedSkills[input.Name] {
			return tools.Result{Content: "Tool blocked: skill " + input.Name + " is not available to the active agent profile."}, nil
		}
	}
	action, err := a.registry.Assess(call.Name, call.Arguments)
	if err != nil {
		return tools.Result{Content: "Tool error: " + err.Error()}, nil
	}
	if plan && action.Risk != tools.RiskRead {
		return tools.Result{Content: "Tool blocked: planning mode is read-only. Switch planning mode off before making changes."}, nil
	}
	permissionTool := call.Name
	hookTool := call.Name
	if identity, ok := item.(tools.PermissionIdentity); hasItem && ok {
		permissionTool = identity.PermissionToolName()
	}
	if identity, ok := item.(tools.HookIdentity); hasItem && ok {
		hookTool = identity.HookToolName()
	}
	grant, err := a.permissions.Authorize(ctx, permissionTool, action)
	if hasItem {
		if observer, ok := item.(tools.AuthorizationObserver); ok {
			observer.ObserveAuthorization(call.Arguments, err)
		}
	}
	decided := event.New(event.KindPermissionDecision)
	decided.Permission = &event.Permission{Tool: permissionTool, Summary: action.Summary, Risk: string(action.Risk), Source: grant.Source, Rule: grant.Rule, Allowed: err == nil}
	send(decided)
	if persistenceErr := a.checkPersistence(); persistenceErr != nil {
		return tools.Result{}, persistenceErr
	}
	allowed := err == nil
	a.lifecycle.Fire(ctx, hooks.Payload{Event: "permission_decision", Workspace: a.workspace, Subject: permissionTool, Tool: permissionTool, Summary: action.Summary, Allowed: &allowed, Detail: map[string]any{"risk": string(action.Risk), "source": grant.Source, "rule": grant.Rule}})
	if err != nil {
		return tools.Result{Content: "Tool denied: " + err.Error()}, nil
	}
	if hookErr := a.lifecycle.Gate(ctx, hooks.Payload{Event: "tool_start", Workspace: a.workspace, Subject: hookTool, Tool: hookTool, Summary: action.Summary, Args: call.Arguments, Paths: action.Paths}); hookErr != nil {
		if observer, ok := item.(tools.ExecutionObserver); hasItem && ok {
			observer.ObserveExecution(call.Arguments, hookErr)
		}
		return tools.Result{Content: "Tool blocked by hook: " + hookErr.Error()}, nil
	}
	args := call.Arguments
	if grant.ContentOverride != nil {
		var overridden error
		args, overridden = withOverriddenContent(call.Name, args, *grant.ContentOverride)
		if overridden != nil {
			return tools.Result{Content: "Tool error: " + overridden.Error()}, nil
		}
	}
	start := event.New(event.KindToolStart)
	start.Tool = &event.Tool{Name: call.Name, Summary: action.Summary}
	send(start)
	if persistenceErr := a.checkPersistence(); persistenceErr != nil {
		return tools.Result{}, persistenceErr
	}
	onOutput := func(chunk string) {
		e := event.New(event.KindToolOutput)
		e.Tool = &event.Tool{Name: call.Name, Output: chunk}
		send(e)
	}
	result, err := a.registry.ExecuteResultStream(ctx, call.Name, args, onOutput)
	a.permissions.RecordOutcome(permissionTool, action, err)
	if len(result.Content) > a.maxToolOutput {
		original := result.Content
		result.Content = clipUTF8(original, a.maxToolOutput)
		if a.artifacts != nil && call.Name != "read_tool_result" {
			ref, artifactErr := a.artifacts.SaveArtifact(call.Name, original)
			if artifactErr == nil {
				completeness := "complete bounded copy"
				if !ref.Complete {
					completeness = "retained prefix"
				}
				result.Content += fmt.Sprintf("\n… output omitted from active context; %s saved as session artifact %s (%d of %d returned bytes). Use read_tool_result with this id and byte ranges to inspect it without rerunning %s. …", completeness, ref.ID, ref.StoredBytes, ref.ReturnedBytes, call.Name)
			} else {
				result.Content += "\n… tool output truncated; retaining the omitted output failed …"
				warning := event.New(event.KindWarning)
				warning.Text = "could not retain oversized output from " + call.Name + ": " + artifactErr.Error()
				send(warning)
			}
		} else {
			result.Content += "\n… tool output truncated …"
		}
	}
	result.Parts = a.retainToolParts(call.Name, result.Parts, send)
	if err != nil {
		if result.Content != "" {
			result.Content += "\n"
		}
		result.Content += "Tool error: " + err.Error()
	}
	done := event.New(event.KindToolResult)
	done.Tool = &event.Tool{Name: call.Name, Summary: action.Summary, Output: result.Content, IsError: err != nil}
	send(done)
	endPayload := hooks.Payload{Event: "tool_end", Workspace: a.workspace, Subject: hookTool, Tool: hookTool, Summary: action.Summary, Detail: map[string]any{"output_bytes": len(result.Content), "image_parts": len(result.Parts)}}
	if err != nil {
		endPayload.Error = err.Error()
	}
	a.lifecycle.Fire(ctx, endPayload)
	if err == nil && action.Risk == tools.RiskWrite && len(action.Paths) > 0 {
		a.lifecycle.Fire(ctx, hooks.Payload{Event: "file_change", Workspace: a.workspace, Subject: call.Name, Tool: call.Name, Paths: action.Paths})
	}
	return result, nil
}

func (a *Agent) retainToolParts(toolName string, parts []provider.ContentPart, send Emit) []provider.ContentPart {
	if len(parts) > session.AttachmentTurnLimit {
		parts = parts[:session.AttachmentTurnLimit]
		warning := event.New(event.KindWarning)
		warning.Text = fmt.Sprintf("tool %s returned more than %d images; additional images were omitted", toolName, session.AttachmentTurnLimit)
		send(warning)
	}
	if len(parts) == 0 {
		return nil
	}
	if a.Capabilities().Images == provider.CapabilityUnsupported {
		warning := event.New(event.KindWarning)
		warning.Text = "the active provider does not support image input; binary tool images remain metadata-only"
		send(warning)
		return nil
	}
	retained := make([]provider.ContentPart, 0, len(parts))
	for _, part := range parts {
		if part.Type != provider.ContentImage || len(part.Data) == 0 {
			continue
		}
		if a.attachments == nil || !a.attachments.Available() {
			retained = append(retained, part)
			continue
		}
		stored, err := a.attachments.SaveBytes(part.Name, part.MediaType, part.Data)
		if err != nil {
			warning := event.New(event.KindWarning)
			warning.Text = "could not retain image returned by " + toolName + ": " + err.Error()
			send(warning)
			continue
		}
		retained = append(retained, stored)
	}
	return retained
}

func (a *Agent) toolDefinitions(plan bool) []provider.ToolDefinition {
	return a.registry.Definitions(func(tool tools.Tool) bool {
		name := tool.Definition().Name
		disabled := a.disabled[name]
		if len(a.allowedTools) > 0 && !a.allowedTools[name] {
			disabled = true
		}
		if identity, ok := tool.(tools.PermissionIdentity); ok {
			disabled = disabled || a.disabled[identity.PermissionToolName()]
		}
		if disabled || (a.subagent && parentOnlyTool(name)) {
			return false
		}
		if !plan {
			return true
		}
		return planTool(name)
	})
}
func planTool(name string) bool {
	switch name {
	case "read_file", "list_files", "search_files", "search_symbols", "read_tool_result", "diagnostics", "load_skill", "delegate", "inspect_delegate_changes", "compare_delegate_changes",
		"find_definition", "find_references",
		// Research is most of what planning is. The web tools change nothing
		// on the machine, and a plan written without checking a library's
		// current API is the plan that has to be thrown away during execution.
		"web_search", "web_fetch",
		"git_status", "git_diff", "git_log", "git_blame", "update_plan", "ask_user", "detect_verification":
		return true
	}
	return false
}

func parentOnlyTool(name string) bool {
	switch name {
	case "delegate", "inspect_delegate_changes", "compare_delegate_changes", "verify_delegate_changes", "apply_delegate_changes":
		return true
	default:
		return false
	}
}

// systemPrompt assembles the model-visible system prompt. The prose lives in
// internal/prompts as embedded templates; what stays here is the conditional
// composition and the whitespace joining the fragments together.
func (a *Agent) systemPrompt(plan bool) string {
	mode := prompts.ModeExecution
	if plan {
		mode = prompts.ModePlanning
	}
	sub := ""
	if a.subagent {
		fragment := prompts.SubagentImplement
		if plan {
			fragment = prompts.SubagentResearch
		}
		sub = "\n" + prompts.Text(fragment)
	}
	return prompts.Agent(prompts.SystemView{
		Workspace:           a.workspace,
		OS:                  runtime.GOOS,
		Arch:                runtime.GOARCH,
		Mode:                prompts.Text(mode),
		Subagent:            sub,
		ProfileInstructions: profileInstructions(a.profileInstructions),
		ProjectInstructions: a.projectInstructions,
		SkillsSummary:       a.catalog.Summary(),
	})
}

// turnState renders the pinned session state as a trailing message, or
// returns false when there is none.
//
// This deliberately does not live in the system prompt. The pinned state is
// the live structured plan, which update_plan rewrites during exactly the
// multi-step work where prompt caching matters most; anything ahead of the
// conversation in the request has to stay byte-identical between iterations
// or every cached prefix is discarded. Placing it after the history keeps the
// prefix stable while leaving the state the last thing the model reads.
//
// The message is regenerated per request and never appended to a.messages, so
// the conversation cannot accumulate stale copies of a plan and compaction has
// nothing to preserve — the board is the single source of truth either way.
func (a *Agent) turnState() (provider.Message, bool) {
	if a.pinnedContext == nil {
		return provider.Message{}, false
	}
	value := strings.TrimSpace(a.pinnedContext())
	if value == "" {
		return provider.Message{}, false
	}
	return provider.Message{Role: "user", Content: prompts.Render(prompts.PinnedState, value), Volatile: true}, true
}

func profileInstructions(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return prompts.Render(prompts.ProfileInstructions, value) + "\n\n"
}

// DelegateTask is one unit of work requested through the delegate tool.
type DelegateTask struct {
	// Name is a short label used in status displays and results; the tool
	// invents one from the task text when omitted.
	Name string `json:"name,omitempty"`
	Task string `json:"task"`
	// Write allows the sub-agent to edit files and run commands. It runs in
	// its own isolated git worktree so parallel writers never race on the
	// same files; nothing is merged, committed, or pushed automatically.
	// When false (the default) the sub-agent is read-only and shares the
	// parent workspace, which is cheaper for pure investigation.
	Write bool `json:"write,omitempty"`
	// WritePaths declares the repository-relative files or directories this
	// task is expected to change. Directory scopes end in "/". Overlapping
	// writers are serialized; omitted scopes are workspace-wide. The retained
	// result is checked against this contract after execution.
	WritePaths []string `json:"write_paths,omitempty"`
	// Agent selects a named profile from configuration (model, fixed role
	// instructions, tool allowlist, iteration budget). Empty uses the
	// parent's own model and full tool set.
	Agent string `json:"agent,omitempty"`
	// PlanStep associates the child result with an existing structured plan
	// step. It does not create or autonomously execute a plan.
	PlanStep int `json:"plan_step,omitempty"`
}

// AddDelegationTool registers the delegate tool: a concurrency-limited
// scheduler that fans a batch of bounded tasks out to sub-agents, each
// read-only in the shared workspace or write-capable in its own isolated
// git worktree, and reports back one structured summary per task (the
// "parent inbox") rather than raw child transcripts.
func (a *Agent) AddDelegationTool(cfg appconfig.Config, approver permission.Approver, team *Team, boards ...*plan.Board) {
	var board *plan.Board
	if len(boards) > 0 {
		board = boards[0]
	}
	scheduler := NewScheduler(cfg.Options.DelegateMaxConcurrency, cfg.Options.DelegateProviderConcurrency)
	desc := fmt.Sprintf("Delegate up to %d bounded tasks to sub-agents. A session-wide scheduler runs up to %d at once, with optional tighter per-provider limits. Use it to parallelize independent investigations or independent file changes. Each task is read-only in the shared workspace unless write is true, in which case it runs in its own isolated git worktree (no other agent's files are affected, and nothing is merged or committed automatically). Write tasks should declare write_paths as repository-relative files or directory prefixes ending in /; overlapping scopes are serialized, omitted scopes are workspace-wide, and out-of-scope results are reported as violations. Sub-agents cannot recursively delegate.", maxDelegateTasks, scheduler.max)
	if cfg.Options.AgentIntegration == "reviewed" {
		desc += " Reviewed integration is enabled: after a successful write task, inspect its exact evidence and hunks with inspect_delegate_changes, run proportionate detected child-worktree verification with verify_delegate_changes, compare candidates when useful, and only then decide whether to publish selected changes with apply_delegate_changes."
	}
	if len(cfg.Agents) > 0 {
		names := make([]string, 0, len(cfg.Agents))
		for name, profile := range cfg.Agents {
			if appconfig.AgentAvailableFor(profile, "delegate") {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		if len(names) > 0 {
			desc += " Named agent profiles available via \"agent\": " + strings.Join(names, ", ") + "."
		}
	}
	schema := json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"tasks":{"type":"array","minItems":1,"maxItems":%d,"items":{"type":"object","properties":{"name":{"type":"string","description":"short label, e.g. \"auth-refactor\""},"task":{"type":"string","description":"the bounded task or question"},"write":{"type":"boolean","description":"true to allow file edits and shell commands in an isolated git worktree; false (default) for a fast read-only investigation"},"write_paths":{"type":"array","maxItems":64,"items":{"type":"string"},"description":"expected repository-relative files or directory prefixes ending in /; requires write=true; omitted means workspace-wide"},"agent":{"type":"string","description":"optional named agent profile from configuration"},"plan_step":{"type":"integer","minimum":1,"description":"optional existing structured plan step ID associated with this task"}},"required":["task"],"additionalProperties":false}}},"required":["tasks"],"additionalProperties":false}`, maxDelegateTasks))
	assess := func(raw json.RawMessage) (tools.Action, error) {
		var input struct {
			Tasks []DelegateTask `json:"tasks"`
		}
		risk := tools.RiskRead
		summary := "delegate one or more read-only investigations"
		if json.Unmarshal(raw, &input) == nil {
			for index, t := range input.Tasks {
				if _, err := NormalizeWriteScopes(t.WritePaths, t.Write); err != nil {
					return tools.Action{}, fmt.Errorf("tasks[%d]: %w", index, err)
				}
				if t.Write {
					risk = tools.RiskWrite
					summary = "delegate one or more sub-agent tasks, including write-capable agents in isolated worktrees"
					break
				}
			}
		}
		return tools.Action{Risk: risk, Summary: summary}, nil
	}
	a.registry.Add(tools.Function{Def: provider.ToolDefinition{Name: "delegate", Description: desc, InputSchema: schema}, Action: tools.Action{Risk: tools.RiskRead, Summary: "delegate one or more sub-agent tasks"}, AssessFn: assess, Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
		var input struct {
			Tasks []DelegateTask `json:"tasks"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return "", err
		}
		if len(input.Tasks) == 0 {
			return "", errors.New("tasks must include at least one item")
		}
		if len(input.Tasks) > maxDelegateTasks {
			input.Tasks = input.Tasks[:maxDelegateTasks]
		}
		results := make([]DelegateResult, len(input.Tasks))
		names := make([]string, len(input.Tasks))
		changed := make([][]string, len(input.Tasks))
		hunks := make([][]DelegateHunk, len(input.Tasks))
		var wg sync.WaitGroup
		for i, t := range input.Tasks {
			wg.Add(1)
			go func(i int, t DelegateTask) {
				defer wg.Done()
				results[i] = a.runScheduledDelegate(ctx, i, t, cfg, approver, team, scheduler, board)
				names[i] = results[i].Name
				changed[i] = results[i].ChangedFiles
				hunks[i] = results[i].ChangedHunks
			}(i, t)
		}
		wg.Wait()
		var warnings []string
		if warning := conflictWarning(names, changed); warning != "" {
			warnings = append(warnings, warning)
		}
		if warning := hunkConflictWarning(names, changed, hunks); warning != "" {
			warnings = append(warnings, warning)
		}
		a.mu.RLock()
		outputLimit := a.maxToolOutput
		a.mu.RUnlock()
		encoded, err := encodeDelegateInbox(results, warnings, outputLimit)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}})
}

// DelegateResult is the bounded parent-inbox contract returned by delegate.
// It carries evidence and artifact locations without injecting raw child
// transcripts into the parent's model context.
type DelegateResult struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Profile         string         `json:"profile,omitempty"`
	PlanStep        int            `json:"plan_step,omitempty"`
	Status          string         `json:"status"`
	Summary         string         `json:"summary,omitempty"`
	Error           string         `json:"error,omitempty"`
	FailureID       string         `json:"failure_id,omitempty"`
	Evidence        []string       `json:"evidence,omitempty"`
	ChangedFiles    []string       `json:"changed_files,omitempty"`
	WriteScopes     []string       `json:"write_scopes,omitempty"`
	ScopeViolations []string       `json:"scope_violations,omitempty"`
	ChangedHunks    []DelegateHunk `json:"changed_hunks,omitempty"`
	Worktree        string         `json:"worktree,omitempty"`
	Branch          string         `json:"branch,omitempty"`
	BaseCommit      string         `json:"base_commit,omitempty"`
	InputTokens     int            `json:"input_tokens,omitempty"`
	OutputTokens    int            `json:"output_tokens,omitempty"`
	CostUSD         float64        `json:"cost_usd,omitempty"`
	TokenBudget     int            `json:"token_budget,omitempty"`
	CostBudgetUSD   float64        `json:"cost_budget_usd,omitempty"`
	TimeoutSeconds  int            `json:"timeout_seconds"`
	Truncated       bool           `json:"truncated,omitempty"`
}

func encodeDelegateInbox(results []DelegateResult, warnings []string, limit int) ([]byte, error) {
	type inbox struct {
		Tasks    []DelegateResult `json:"tasks"`
		Warnings []string         `json:"warnings,omitempty"`
	}
	encode := func() ([]byte, error) { return json.Marshal(inbox{Tasks: results, Warnings: warnings}) }
	encoded, err := encode()
	if err != nil || limit <= 0 || len(encoded) <= limit {
		return encoded, err
	}

	// Preserve a valid, self-describing JSON envelope instead of letting the
	// generic tool-output cap cut it mid-object. First shed detailed evidence
	// and hunk ranges while keeping every child's identity and terminal state.
	for i := range results {
		results[i].Truncated = true
		results[i].Summary = boundedDelegateText(results[i].Summary, 2048)
		results[i].Error = boundedDelegateText(results[i].Error, 1024)
		results[i].ChangedHunks = nil
		if len(results[i].Evidence) > 2 {
			results[i].Evidence = results[i].Evidence[:2]
		}
		for j := range results[i].Evidence {
			results[i].Evidence[j] = boundedDelegateText(results[i].Evidence[j], 512)
		}
		if len(results[i].ChangedFiles) > 32 {
			results[i].ChangedFiles = results[i].ChangedFiles[:32]
		}
		if len(results[i].ScopeViolations) > 32 {
			results[i].ScopeViolations = results[i].ScopeViolations[:32]
		}
		for j := range results[i].ChangedFiles {
			results[i].ChangedFiles[j] = boundedDelegateText(results[i].ChangedFiles[j], 256)
		}
	}
	if len(warnings) > 4 {
		warnings = warnings[:4]
	}
	for i := range warnings {
		warnings[i] = boundedDelegateText(warnings[i], 2048)
	}
	encoded, err = encode()
	if err != nil || len(encoded) <= limit {
		return encoded, err
	}

	// A very small configured tool-output limit still gets one compact record
	// per task. The truncated marker tells the parent to inspect /agents or the
	// retained worktree rather than assuming an omitted manifest was empty.
	for i := range results {
		results[i].Name = boundedDelegateText(results[i].Name, 128)
		results[i].Profile = boundedDelegateText(results[i].Profile, 128)
		results[i].Summary = boundedDelegateText(results[i].Summary, 512)
		results[i].Error = boundedDelegateText(results[i].Error, 512)
		results[i].Evidence = nil
		if len(results[i].ChangedFiles) > 8 {
			results[i].ChangedFiles = results[i].ChangedFiles[:8]
		}
	}
	warnings = []string{"delegate details were compacted to fit max_tool_output_bytes; inspect /agents for durable per-task outcomes"}
	encoded, err = encode()
	if err != nil || len(encoded) <= limit {
		return encoded, err
	}

	minimal := make([]DelegateResult, len(results))
	for i, result := range results {
		minimal[i] = DelegateResult{
			ID: boundedDelegateText(result.ID, 128), Name: boundedDelegateText(result.Name, 64),
			Profile: boundedDelegateText(result.Profile, 64), PlanStep: result.PlanStep, Status: result.Status,
			Error: boundedDelegateText(result.Error, 128), FailureID: result.FailureID, TokenBudget: result.TokenBudget,
			WriteScopes: boundedDelegateValues(result.WriteScopes, 8, 128), ScopeViolations: boundedDelegateValues(result.ScopeViolations, 8, 128),
			TimeoutSeconds: result.TimeoutSeconds, Truncated: true,
		}
	}
	results = minimal
	warnings = []string{"delegate details omitted by max_tool_output_bytes; inspect /agents"}
	return encode()
}

func boundedDelegateValues(values []string, count, bytes int) []string {
	if len(values) > count {
		values = values[:count]
	}
	out := append([]string(nil), values...)
	for i := range out {
		out[i] = boundedDelegateText(out[i], bytes)
	}
	return out
}

// conflictWarning reports files touched by more than one sibling task in
// the same delegate batch — the isolation model prevents them from racing
// while running, but the user still has to reconcile the worktrees by hand.
func conflictWarning(names []string, changed [][]string) string {
	owners := map[string][]string{}
	for i, files := range changed {
		for _, f := range files {
			owners[f] = append(owners[f], names[i])
		}
	}
	var lines []string
	for _, f := range sortedKeys(owners) {
		if who := owners[f]; len(who) > 1 {
			lines = append(lines, fmt.Sprintf("  %s: %s", f, strings.Join(who, ", ")))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "⚠ conflicting changes — these files were modified by more than one sub-agent in separate worktrees; review and reconcile before merging either:\n" + strings.Join(lines, "\n")
}

// hunkConflictWarning refines same-file warnings against the common HEAD base.
// It never auto-merges: even disjoint edits stay in separate worktrees, but
// users can distinguish a likely clean integration from overlapping ranges.
func hunkConflictWarning(names []string, changed [][]string, hunks [][]DelegateHunk) string {
	owners := map[string][]int{}
	for i, files := range changed {
		for _, path := range files {
			owners[path] = append(owners[path], i)
		}
	}
	var lines []string
	for _, path := range sortedIndexKeys(owners) {
		indices := owners[path]
		if len(indices) < 2 {
			continue
		}
		overlap := false
		complete := true
		for left := 0; left < len(indices); left++ {
			leftHunks := hunksForPath(hunks[indices[left]], path)
			if len(leftHunks) == 0 {
				complete = false
			}
			for right := left + 1; right < len(indices); right++ {
				rightHunks := hunksForPath(hunks[indices[right]], path)
				if len(rightHunks) == 0 {
					complete = false
				}
				for _, a := range leftHunks {
					for _, b := range rightHunks {
						if delegateHunksOverlap(a, b) {
							overlap = true
						}
					}
				}
			}
		}
		labels := make([]string, 0, len(indices))
		for _, index := range indices {
			labels = append(labels, names[index])
		}
		detail := "range unavailable; treat as a file-level conflict"
		if complete && overlap {
			detail = "overlapping hunks"
		} else if complete {
			detail = "disjoint hunks (still review before integrating either worktree)"
		}
		lines = append(lines, fmt.Sprintf("  %s: %s — %s", path, detail, strings.Join(labels, ", ")))
	}
	if len(lines) == 0 {
		return ""
	}
	return "Hunk overlap analysis (informational; nothing was merged):\n" + strings.Join(lines, "\n")
}

func sortedIndexKeys(values map[string][]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func hunksForPath(hunks []DelegateHunk, path string) []DelegateHunk {
	var matching []DelegateHunk
	for _, hunk := range hunks {
		if hunk.Path == path {
			matching = append(matching, hunk)
		}
	}
	return matching
}

func delegateHunksOverlap(a, b DelegateHunk) bool {
	// Two insertions at the same base position conflict. Otherwise a zero-line
	// insertion occupies one comparison point for conservative overlap.
	if a.OldLines == 0 && b.OldLines == 0 {
		return a.OldStart == b.OldStart
	}
	aEnd := a.OldStart + max(a.OldLines, 1) - 1
	bEnd := b.OldStart + max(b.OldLines, 1) - 1
	return a.OldStart <= bEnd && b.OldStart <= aEnd
}

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// runScheduledDelegate admits one task through the session-wide scheduler,
// records every lifecycle transition in Team, and returns the bounded parent
// inbox result. Queueing is included in the task timeout.
func (a *Agent) runScheduledDelegate(parent context.Context, index int, task DelegateTask, cfg appconfig.Config, approver permission.Approver, team *Team, scheduler *Scheduler, board *plan.Board) DelegateResult {
	name := strings.TrimSpace(task.Name)
	if name == "" {
		name = fmt.Sprintf("task-%d", index+1)
	}
	task.Name = name
	id := fmt.Sprintf("d%d-%d", time.Now().UnixNano(), delegateIDCounter.Add(1))

	var profile appconfig.AgentDefinition
	var profileErr error
	if task.Agent != "" {
		var ok bool
		profile, ok = cfg.Agents[task.Agent]
		if !ok {
			profileErr = fmt.Errorf("unknown agent profile %q", task.Agent)
		} else if !appconfig.AgentAvailableFor(profile, "delegate") {
			profileErr = fmt.Errorf("agent profile %q is not available for delegated tasks", task.Agent)
		}
	}
	providerName, model := a.Selection()
	if profile.Model != "" {
		model = profile.Model
	}
	timeoutSeconds := profile.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 10 * 60
	}
	taskCtx, cancel := context.WithTimeout(parent, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	writeScopes, scopeErr := NormalizeWriteScopes(task.WritePaths, task.Write)
	if team != nil {
		team.Enqueue(DelegateStart{
			ID: id, Name: name, Task: task.Task, Profile: task.Agent,
			Provider: providerName, Model: model, Write: task.Write, PlanStep: task.PlanStep,
			WriteScopes: writeScopes,
			TokenBudget: profile.TokenBudget, CostBudgetUSD: profile.CostBudgetUSD, TimeoutSeconds: timeoutSeconds, Cancel: cancel,
		})
	}
	finishError := func(err error) DelegateResult {
		err = failureid.Ensure(err)
		if team != nil {
			team.FinishDetailed(id, "", nil, nil, "", "", "", provider.Usage{}, err)
			if status, ok := team.Get(id); ok {
				return delegateResult(status)
			}
		}
		return DelegateResult{ID: id, Name: name, Profile: task.Agent, PlanStep: task.PlanStep, Status: DelegateError, Error: err.Error(), FailureID: failureid.ID(err), WriteScopes: writeScopes, TokenBudget: profile.TokenBudget, CostBudgetUSD: profile.CostBudgetUSD, TimeoutSeconds: timeoutSeconds}
	}
	if strings.TrimSpace(task.Task) == "" {
		return finishError(errors.New("empty task"))
	}
	if profileErr != nil {
		return finishError(profileErr)
	}
	if scopeErr != nil {
		return finishError(scopeErr)
	}
	if err := validatePlanAssignment(board, task.PlanStep); err != nil {
		return finishError(err)
	}
	release, err := scheduler.AcquireScoped(taskCtx, providerName, writeScopes)
	if err != nil {
		return finishError(err)
	}
	defer release()
	if team != nil {
		team.MarkRunning(id)
	}

	a.lifecycle.Fire(taskCtx, hooks.Payload{Event: "subagent_start", Workspace: a.workspace, Subject: name, Detail: map[string]any{"id": id, "task": task.Task, "write": task.Write, "write_scopes": writeScopes, "agent": task.Agent, "token_budget": profile.TokenBudget, "cost_budget_usd": profile.CostBudgetUSD, "timeout_seconds": timeoutSeconds}})
	output, evidence, changed, hunks, worktreePath, branch, baseCommit, usage, runErr := a.runDelegateTask(taskCtx, id, task, profile, cfg, approver, team)
	violations := writeScopeViolations(writeScopes, changed)
	if len(violations) > 0 {
		scopeViolationErr := fmt.Errorf("delegated agent changed files outside its declared write_paths: %s", strings.Join(violations, ", "))
		if runErr != nil {
			scopeViolationErr = fmt.Errorf("%v; prior task error: %v", scopeViolationErr, runErr)
		}
		runErr = scopeViolationErr
		if team != nil {
			team.MarkScopeViolations(id, violations)
		}
	}
	a.lifecycle.Fire(taskCtx, hooks.Payload{Event: "subagent_end", Workspace: a.workspace, Subject: name, Paths: changed, Error: errorString(runErr), Detail: map[string]any{"id": id, "status": delegateTerminalStatus(runErr), "write_scopes": writeScopes, "scope_violations": violations, "input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens}})
	if team != nil {
		team.FinishDetailed(id, boundedDelegateText(output, 16<<10), evidence, changed, worktreePath, branch, baseCommit, usage, runErr)
		if status, ok := team.Get(id); ok {
			result := delegateResult(status)
			result.ChangedHunks = boundedDelegateHunks(hunks)
			return result
		}
	}
	status := DelegateDone
	if runErr != nil {
		status = delegateTerminalStatus(runErr)
	}
	return DelegateResult{ID: id, Name: name, Profile: task.Agent, PlanStep: task.PlanStep, Status: status, Summary: boundedDelegateText(output, 16<<10), Error: boundedDelegateText(errorString(runErr), 4<<10), FailureID: failureid.ID(runErr), Evidence: evidence, ChangedFiles: changed, ChangedHunks: boundedDelegateHunks(hunks), WriteScopes: writeScopes, ScopeViolations: violations, Worktree: worktreePath, Branch: branch, BaseCommit: baseCommit, InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CostUSD: usage.CostUSD, TokenBudget: profile.TokenBudget, CostBudgetUSD: profile.CostBudgetUSD, TimeoutSeconds: timeoutSeconds}
}

func validatePlanAssignment(board *plan.Board, stepID int) error {
	if stepID == 0 {
		return nil
	}
	if board == nil || board.Current() == nil {
		return fmt.Errorf("plan step %d was requested but there is no active structured plan", stepID)
	}
	current := board.Current()
	states := make(map[int]string, len(current.Steps))
	var selected *plan.Step
	for i := range current.Steps {
		states[current.Steps[i].ID] = current.Steps[i].Status
		if current.Steps[i].ID == stepID {
			selected = &current.Steps[i]
		}
	}
	if selected == nil {
		return fmt.Errorf("unknown plan step %d", stepID)
	}
	for _, dependency := range selected.DependsOn {
		if states[dependency] != "done" && states[dependency] != "skipped" {
			return fmt.Errorf("plan step %d depends on unfinished step %d", stepID, dependency)
		}
	}
	return nil
}

func boundedDelegateHunks(hunks []DelegateHunk) []DelegateHunk {
	const limit = 256
	if len(hunks) > limit {
		hunks = hunks[:limit]
	}
	bounded := append([]DelegateHunk(nil), hunks...)
	for i := range bounded {
		bounded[i].Path = boundedDelegateText(bounded[i].Path, 1024)
	}
	return bounded
}

func (a *Agent) runDelegateTask(ctx context.Context, id string, task DelegateTask, profile appconfig.AgentDefinition, cfg appconfig.Config, approver permission.Approver, team *Team) (output string, evidence, changed []string, hunks []DelegateHunk, worktreePath, branch, baseCommit string, usage provider.Usage, runErr error) {
	a.mu.RLock()
	client, providerName, model, providerConfig := a.client, a.providerName, a.model, a.providerConfig
	workspace, parentRegistry := a.workspace, a.registry
	catalog, instructions, maxOut, maxIter := a.catalog, a.projectInstructions, a.maxToolOutput, a.maxIterations
	auditRedact := a.auditRedact
	disabled := keys(a.disabled)
	persistenceError := a.persistenceError
	a.mu.RUnlock()

	if profile.Model != "" {
		model = profile.Model
	}
	if profile.Reasoning != nil {
		reasoning := *profile.Reasoning
		providerConfig.Reasoning = &reasoning
	}
	if profile.Instructions != "" {
		instructions = prompts.Render(prompts.DelegateRole, profile.Instructions) + "\n\n" + instructions
	}
	if task.Write {
		scopes, _ := NormalizeWriteScopes(task.WritePaths, true)
		instructions = prompts.Render(prompts.DelegateWriteContract, strings.Join(scopes, ", ")) + "\n\n" + instructions
	}
	if profile.MaxIterations > 0 {
		maxIter = profile.MaxIterations
	}
	childCatalog := catalog.Restrict(profile.Skills)
	childConfig := cfg
	parentPermissions := cfg.Permissions
	// /autonomy can change the live parent mode after configuration load. A
	// child must inherit that current mode, not a stale and potentially wider
	// value from cfg.
	if a.permissions != nil {
		parentPermissions.Mode = a.permissions.Mode()
	}
	childConfig.Permissions = restrictAgentPermissions(parentPermissions, profile.Permissions)

	childApprover := approver
	if approver != nil && team != nil {
		childApprover = func(approvalCtx context.Context, request permission.Request) (permission.Decision, error) {
			team.SetWaitingApproval(id, request.Tool+": "+request.Action.Summary)
			display := request
			display.Action.Summary = fmt.Sprintf("delegated agent %s (%s): %s", task.Name, id, request.Action.Summary)
			decision, err := approver(approvalCtx, display)
			team.SetAction(id, "working")
			return decision, err
		}
	}
	childManager := permission.New(childConfig.Permissions, childApprover)
	childManager.SetRestrictions(profile.Permissions.Rules)
	if ledger, ledgerErr := audit.Open(workspace); ledgerErr == nil {
		ledger.Redact = auditRedact
		childManager.SetLedger(ledger)
	}
	childWorkspace := workspace
	childRegistry := parentRegistry.Clone()
	childRegistry.Remove("delegate")
	childRegistry.Remove("inspect_delegate_changes")
	childRegistry.Remove("compare_delegate_changes")
	childRegistry.Remove("verify_delegate_changes")
	childRegistry.Remove("apply_delegate_changes")
	// A child may report suggestions in its result but must not mutate the
	// parent's shared structured plan artifact.
	childRegistry.Remove("update_plan")
	childRegistry.Remove("load_skill")
	childRegistry.Add(skills.Tool(childCatalog))
	childPlan := true
	var wt *worktree
	if task.Write {
		childPlan = false
		if !isGitRepo(ctx, workspace) {
			return "", nil, nil, nil, "", "", "", provider.Usage{}, errors.New("workspace is not a git repository; cannot isolate a write-capable agent")
		}
		var err error
		// Git mutates shared administrative state under .git/worktrees while
		// adding a worktree. Serialize only this short setup operation per
		// parent agent; child execution remains fully concurrent afterward.
		a.worktreeMu.Lock()
		wt, err = newWorktree(ctx, workspace, task.Name)
		a.worktreeMu.Unlock()
		if err != nil {
			return "", nil, nil, nil, "", "", "", provider.Usage{}, err
		}
		childWorkspace = wt.path
		reg, _, childProcs, buildErr := tools.Builtins(wt.path, childConfig)
		if buildErr != nil {
			wt.remove(ctx)
			return "", nil, nil, nil, "", "", "", provider.Usage{}, buildErr
		}
		defer childProcs.StopAll()
		if discovered, discoverErr := skills.Discover(childWorkspace, cfg.ProjectTrusted); discoverErr == nil {
			childCatalog = discovered.Restrict(profile.Skills)
		}
		reg.Add(skills.Tool(childCatalog))
		childRegistry = reg
		childManager = permission.New(childConfig.Permissions, childApprover)
		childManager.SetRestrictions(profile.Permissions.Rules)
		if ledger, ledgerErr := audit.Open(childWorkspace); ledgerErr == nil {
			ledger.Redact = auditRedact
			childManager.SetLedger(ledger)
		}
	}

	if len(profile.Tools) > 0 {
		allowed := make(map[string]bool, len(profile.Tools))
		for _, toolName := range profile.Tools {
			allowed[toolName] = true
		}
		for _, toolName := range childRegistry.Names() {
			if !allowed[toolName] {
				disabled = append(disabled, toolName)
			}
		}
	}
	var evidenceMu sync.Mutex
	child := New(Options{
		Client: client, ProviderName: providerName, Model: model, Workspace: childWorkspace,
		ProviderConfig: providerConfig, Registry: childRegistry, Permissions: childManager,
		Catalog: childCatalog, ProjectInstructions: instructions,
		MaxIterations: min(maxIter, 16), MaxToolOutput: maxOut, TokenBudget: profile.TokenBudget, CostBudgetUSD: profile.CostBudgetUSD,
		DisabledTools: disabled, PlanMode: childPlan, Subagent: true,
		PersistenceError: persistenceError,
		OnUsage: func(current provider.Usage) {
			if team != nil {
				team.SetUsage(id, current)
			}
		},
		OnAction: func(action string) {
			if team != nil {
				team.SetAction(id, action)
			}
		},
		TakeSteering: func() []string {
			if team == nil {
				return nil
			}
			return team.TakeSteering(id)
		},
	})
	emit := func(runtimeEvent event.Event) {
		if team != nil {
			chunk := ""
			switch runtimeEvent.Kind {
			case event.KindTextDelta, event.KindReasoningDelta:
				chunk = runtimeEvent.Text
			case event.KindToolOutput:
				if runtimeEvent.Tool != nil {
					chunk = runtimeEvent.Tool.Output
				}
			}
			if chunk != "" {
				if auditRedact != nil {
					chunk = auditRedact(chunk)
				}
				team.AppendOutput(id, chunk)
			}
		}
		if team != nil && runtimeEvent.Kind == event.KindToolStart && runtimeEvent.Tool != nil {
			team.SetAction(id, runtimeEvent.Tool.Name+": "+runtimeEvent.Tool.Summary)
		}
		if runtimeEvent.Kind == event.KindToolResult && runtimeEvent.Tool != nil {
			line := runtimeEvent.Tool.Name + ": "
			if runtimeEvent.Tool.IsError {
				line += "failed — "
			} else {
				line += "completed — "
			}
			line += runtimeEvent.Tool.Output
			evidenceMu.Lock()
			if len(evidence) < 8 {
				evidence = append(evidence, boundedDelegateText(line, 1024))
			}
			evidenceMu.Unlock()
		}
	}
	if team != nil {
		team.SetAction(id, "calling "+providerName+"/"+model)
	}
	output, runErr = child.Run(ctx, task.Task, emit)
	usage = child.Usage()
	if wt != nil {
		changed = wt.changedFiles(context.WithoutCancel(ctx))
		hunks = wt.changedHunks(context.WithoutCancel(ctx), changed)
		if len(changed) == 0 {
			wt.remove(context.WithoutCancel(ctx))
		} else {
			branch, worktreePath, baseCommit = wt.branch, wt.path, wt.baseCommit
		}
	}
	return output, evidence, changed, hunks, worktreePath, branch, baseCommit, usage, runErr
}

func restrictAgentPermissions(parent appconfig.Permissions, child appconfig.AgentPermissions) appconfig.Permissions {
	effective := parent
	if child.Mode != "" && autonomyRank(child.Mode) < autonomyRank(effective.Mode) {
		effective.Mode = child.Mode
	}
	effective.DeniedTools = additiveValues(parent.DeniedTools, child.DeniedTools)
	effective.DeniedCommands = additiveValues(parent.DeniedCommands, child.DeniedCommands)
	// Child rules are evaluated by permission.Manager as an independent
	// restriction layer. Keeping parent ordering intact here avoids a child
	// prompt masking an inherited deny (or a parent allow masking a child deny).
	effective.Rules = append([]appconfig.Rule(nil), parent.Rules...)
	return effective
}

func autonomyRank(mode string) int {
	switch mode {
	case "autopilot":
		return 2
	case "workspace":
		return 1
	default:
		return 0
	}
}

func additiveValues(inherited, additions []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(inherited)+len(additions))
	for _, values := range [][]string{inherited, additions} {
		for _, value := range values {
			if !seen[value] {
				seen[value] = true
				result = append(result, value)
			}
		}
	}
	return result
}

func delegateResult(status DelegateStatus) DelegateResult {
	return DelegateResult{
		ID: status.ID, Name: status.Name, Profile: status.Profile, PlanStep: status.PlanStep, Status: status.Status,
		Summary: status.Summary, Error: status.Error, FailureID: status.FailureID, Evidence: status.Evidence,
		ChangedFiles: status.Changed, WriteScopes: status.WriteScopes, ScopeViolations: status.ScopeViolations,
		Worktree: status.Worktree, Branch: status.Branch, BaseCommit: status.BaseCommit,
		InputTokens: status.Usage.InputTokens, OutputTokens: status.Usage.OutputTokens,
		CostUSD: status.Usage.CostUSD, TokenBudget: status.TokenBudget, CostBudgetUSD: status.CostBudgetUSD, TimeoutSeconds: status.TimeoutSeconds,
	}
}

func delegateTerminalStatus(err error) string {
	switch {
	case err == nil:
		return DelegateDone
	case errors.Is(err, ErrTokenBudgetExceeded):
		return DelegateBudgetExhausted
	case errors.Is(err, ErrCostBudgetExceeded):
		return DelegateBudgetExhausted
	case errors.Is(err, context.Canceled):
		return DelegateCancelled
	case errors.Is(err, context.DeadlineExceeded):
		return DelegateTimedOut
	default:
		return DelegateError
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func boundedDelegateText(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r >= 0x20 {
			return r
		}
		return -1
	}, value)
	return clipUTF8(value, limit)
}

// Clear removes model-visible conversation context without resetting durable
// token or cost accounting for the active session.
func (a *Agent) Clear() {
	a.mu.Lock()
	a.messages = nil
	a.lastInputTokens = 0
	a.usageWatermark = 0
	a.mu.Unlock()
}

// Reset starts fresh conversation and accounting state for a newly-created
// session. It must not be used for /clear within an existing session.
func (a *Agent) Reset() {
	a.mu.Lock()
	a.messages = nil
	a.usage = provider.Usage{}
	a.lastInputTokens = 0
	a.usageWatermark = 0
	a.mu.Unlock()
}

// SetUsage restores cumulative accounting reconstructed from durable events.
func (a *Agent) SetUsage(usage provider.Usage) {
	a.mu.Lock()
	a.usage = usage
	a.mu.Unlock()
}

// ProfileSettings is the effective runtime surface of a named primary
// profile. Runtime owns restoration of the ordinary defaults.
type ProfileSettings struct {
	Name          string
	Instructions  string
	Catalog       skills.Catalog
	Tools         []string
	DisabledTools []string
	Skills        []string
	MaxIterations int
	TokenBudget   int
	CostBudgetUSD float64
}

// ApplyProfile changes only local agent behavior; the Runtime separately
// changes provider/model and permission restrictions.
func (a *Agent) ApplyProfile(settings ProfileSettings) {
	disabled := map[string]bool{}
	for _, name := range settings.DisabledTools {
		disabled[name] = true
	}
	allowedTools := map[string]bool(nil)
	if len(settings.Tools) > 0 {
		allowedTools = make(map[string]bool, len(settings.Tools))
		for _, name := range settings.Tools {
			allowedTools[name] = true
		}
	}
	allowedSkills := map[string]bool(nil)
	if len(settings.Skills) > 0 {
		allowedSkills = make(map[string]bool, len(settings.Skills))
		for _, name := range settings.Skills {
			allowedSkills[name] = true
		}
	}
	a.mu.Lock()
	a.profileName = settings.Name
	a.profileInstructions = settings.Instructions
	a.catalog = settings.Catalog
	a.maxIterations = settings.MaxIterations
	a.tokenBudget = settings.TokenBudget
	a.costBudgetUSD = settings.CostBudgetUSD
	a.disabled = disabled
	a.allowedTools = allowedTools
	a.allowedSkills = allowedSkills
	a.mu.Unlock()
}

func (a *Agent) Profile() (name, reasoning string, tokenBudget int, costBudgetUSD float64) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.providerConfig.Reasoning != nil {
		reasoning = a.providerConfig.Reasoning.Effort
	}
	return a.profileName, reasoning, a.tokenBudget, a.costBudgetUSD
}

func (a *Agent) SetPlan(enabled bool) { a.mu.Lock(); a.planMode = enabled; a.mu.Unlock() }
func (a *Agent) Plan() bool           { a.mu.RLock(); defer a.mu.RUnlock(); return a.planMode }
func (a *Agent) SetProvider(name, model string, p appconfig.Provider, client provider.Client) {
	a.mu.Lock()
	a.providerName = name
	a.model = model
	a.providerConfig = p
	a.client = client
	a.mu.Unlock()
}
func (a *Agent) Selection() (string, string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.providerName, a.model
}

// Capabilities returns the active client's declared feature support. Custom
// clients that do not implement CapabilityReporter remain usable and report
// unknown model-dependent features rather than inheriting a false claim.
func (a *Agent) Capabilities() provider.Capabilities {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if reporter, ok := a.client.(provider.CapabilityReporter); ok {
		return reporter.Capabilities()
	}
	return provider.Capabilities{Model: a.model, ContextWindow: a.providerConfig.Context}
}

// ProviderHealth reports process-local health for the active provider. Test
// doubles and custom clients that do not implement health reporting remain
// usable and start in the explicit unknown state.
func (a *Agent) ProviderHealth() provider.Health {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if reporter, ok := a.client.(provider.HealthReporter); ok {
		return reporter.Health()
	}
	return provider.Health{State: provider.HealthUnknown}
}

func (a *Agent) Usage() provider.Usage { a.mu.RLock(); defer a.mu.RUnlock(); return a.usage }

// nextRequestMaxTokens reserves enough of a delegated task's total token
// budget for the estimated next input and caps the provider's output request
// to what remains. Provider-reported usage is checked again after the call
// because tokenizers and image accounting vary by model.
func (a *Agent) nextRequestMaxTokens() (int, error) {
	a.mu.RLock()
	budget := a.tokenBudget
	used := a.usage.InputTokens + a.usage.OutputTokens
	costBudget := a.costBudgetUSD
	spent := a.usage.CostUSD
	pricing := a.providerConfig.Pricing
	configured := a.providerConfig.MaxTokens
	a.mu.RUnlock()
	if budget <= 0 && costBudget <= 0 {
		return configured, nil
	}
	estimatedInput, _ := a.ContextEstimate()
	if budget > 0 {
		remaining := budget - used
		if remaining <= 0 {
			return 0, fmt.Errorf("%w: used %d of %d tokens", ErrTokenBudgetExceeded, used, budget)
		}
		if estimatedInput >= remaining {
			return 0, fmt.Errorf("%w: approximately %d input tokens need the remaining %d-token allowance", ErrTokenBudgetExceeded, estimatedInput, remaining)
		}
		allowance := remaining - estimatedInput
		if configured <= 0 || configured > allowance {
			configured = allowance
		}
	}
	if costBudget > 0 {
		if pricing == nil || pricing.InputPerMillion <= 0 || pricing.OutputPerMillion <= 0 {
			return 0, fmt.Errorf("%w: cost_budget_usd requires positive pricing.input_per_million and pricing.output_per_million on the selected provider", ErrCostBudgetExceeded)
		}
		remainingUSD := costBudget - spent - float64(estimatedInput)*pricing.InputPerMillion/1_000_000
		if remainingUSD <= 0 {
			return 0, fmt.Errorf("%w: estimated input would exceed the remaining $%.6f", ErrCostBudgetExceeded, costBudget-spent)
		}
		costAllowance := int(math.Floor(remainingUSD * 1_000_000 / pricing.OutputPerMillion))
		if configured <= 0 || configured > costAllowance {
			configured = costAllowance
		}
	}
	if configured <= 0 {
		if costBudget > 0 {
			return 0, fmt.Errorf("%w: no output allowance remains", ErrCostBudgetExceeded)
		}
		return 0, fmt.Errorf("%w: no output allowance remains", ErrTokenBudgetExceeded)
	}
	return configured, nil
}

func estimateCost(usage provider.Usage, pricing *appconfig.Pricing) provider.Usage {
	if pricing == nil || pricing.InputPerMillion <= 0 || pricing.OutputPerMillion <= 0 ||
		usage.InputTokens+usage.OutputTokens <= 0 {
		return usage
	}
	// Cache reads and writes are both subsets of InputTokens, so the three
	// rates apply to disjoint parts of one total. Clamping keeps a provider
	// reporting inconsistent counters from producing a negative remainder.
	input := max(usage.InputTokens, 0)
	cached := min(max(usage.CachedTokens, 0), input)
	written := min(max(usage.CacheWriteTokens, 0), input-cached)
	ordinaryInput := max(input-cached-written, 0)
	cachedRate := pricing.InputPerMillion
	if pricing.CachedInputPerMillion != nil {
		cachedRate = *pricing.CachedInputPerMillion
	}
	writeRate := pricing.InputPerMillion
	if pricing.CacheWritePerMillion != nil {
		writeRate = *pricing.CacheWritePerMillion
	}
	usage.CostUSD = (float64(ordinaryInput)*pricing.InputPerMillion +
		float64(cached)*cachedRate +
		float64(written)*writeRate +
		float64(max(usage.OutputTokens, 0))*pricing.OutputPerMillion) / 1_000_000
	usage.CostAvailable = true
	usage.CostEstimated = true
	return usage
}

func eventUsage(usage provider.Usage) *event.Usage {
	return &event.Usage{
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
		CachedTokens: usage.CachedTokens, CacheWriteTokens: usage.CacheWriteTokens,
		ReasoningTokens: usage.ReasoningTokens,
		CostUSD:         usage.CostUSD, CostAvailable: usage.CostAvailable, CostEstimated: usage.CostEstimated,
	}
}

// ContextEstimate combines the provider-reported input size of the last
// request with a character-based estimate of everything added since.
func (a *Agent) ContextEstimate() (estimated int, window int) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	start := a.usageWatermark
	base := a.lastInputTokens
	chars := 0
	if base == 0 {
		start = 0
		chars += len(a.systemPrompt(a.planMode))
		// Counted only here. Once a request has reported usage, the trailing
		// state is already inside base: it is resent every iteration, so
		// adding it again would double-count it on every estimate.
		if state, ok := a.turnState(); ok {
			chars += len(state.Content)
		}
	}
	if start > len(a.messages) {
		start = len(a.messages)
	}
	for _, m := range a.messages[start:] {
		chars += len(m.Content) + len(m.ToolCallID)
		for _, part := range m.Parts {
			if part.Type == provider.ContentImage {
				// Image tokenization is provider/model-specific. Reserve a
				// visible planning estimate until reported usage replaces it.
				chars += 4 * 1024
			} else {
				chars += len(part.Text)
			}
		}
		for _, c := range m.ToolCalls {
			chars += len(c.Name) + len(c.Arguments)
		}
	}
	return base + chars/4, a.providerConfig.Context
}

// ContextBreakdown explains what occupies the model-visible context, for
// the /context inspector.
type ContextBreakdown struct {
	SystemPromptChars  int
	InstructionsChars  int
	SkillsSummaryChars int
	PinnedStateChars   int
	MessagesByRole     map[string]int
	ToolResultChars    int
	Summaries          int
	ArtifactCount      int
	ArtifactBytes      int
	ImageCount         int
	Estimated, Window  int
}

func (a *Agent) ContextBreakdown() ContextBreakdown {
	estimated, window := a.ContextEstimate()
	a.mu.RLock()
	defer a.mu.RUnlock()
	pinned := ""
	if a.pinnedContext != nil {
		pinned = strings.TrimSpace(a.pinnedContext())
	}
	// The pinned state is no longer part of the system prompt, so it is not
	// subtracted here; it is reported on its own below and counted by
	// ContextEstimate as trailing state.
	totalSystem := len(a.systemPrompt(a.planMode))
	baseSystem := totalSystem - len(a.projectInstructions) - len(a.profileInstructions) - len(a.catalog.Summary())
	if baseSystem < 0 {
		baseSystem = totalSystem
	}
	b := ContextBreakdown{
		SystemPromptChars:  baseSystem,
		InstructionsChars:  len(a.projectInstructions) + len(a.profileInstructions),
		SkillsSummaryChars: len(a.catalog.Summary()),
		PinnedStateChars:   len(pinned),
		MessagesByRole:     map[string]int{},
		Estimated:          estimated,
		Window:             window,
	}
	for _, m := range a.messages {
		b.MessagesByRole[m.Role]++
		if m.Role == "tool" {
			b.ToolResultChars += len(m.Content)
		}
		if m.Role == "user" && strings.HasPrefix(m.Content, "[Context summary") {
			b.Summaries++
		}
		for _, part := range m.Parts {
			if part.Type == provider.ContentImage {
				b.ImageCount++
			}
		}
	}
	if a.artifacts != nil {
		stats := a.artifacts.Stats()
		b.ArtifactCount = stats.Count
		b.ArtifactBytes = stats.DiskBytes
	}
	return b
}

// compactKeepRecent is how many trailing messages stay verbatim through a
// compaction, so recent failures and results remain exact.
const compactKeepRecent = 6

func (a *Agent) shouldCompact() bool {
	estimated, window := a.ContextEstimate()
	if window <= 0 {
		return false
	}
	return estimated > window*80/100 && a.MessageCount() > compactKeepRecent+2
}

// Compact summarizes older conversation into one message, keeping the most
// recent messages verbatim. The durable transcript is not rewritten; only
// the active model context shrinks. Returns how many messages were replaced.
func (a *Agent) Compact(ctx context.Context, focus string) (int, error) {
	return a.compact(ctx, focus, nil)
}

// CompactWithEmit exposes compact's usage and lifecycle events to an
// interactive caller so durable accounting remains complete.
func (a *Agent) CompactWithEmit(ctx context.Context, focus string, send Emit) (int, error) {
	return a.compact(ctx, focus, send)
}

func (a *Agent) compact(ctx context.Context, focus string, send Emit) (int, error) {
	a.mu.RLock()
	messages := append([]provider.Message(nil), a.messages...)
	client := a.client
	model := a.model
	a.mu.RUnlock()
	if client == nil {
		return 0, errors.New("no provider client configured")
	}
	cut := len(messages) - compactKeepRecent
	if cut < 2 {
		return 0, errors.New("nothing to compact yet")
	}
	// Never split between an assistant tool call and its tool results.
	for cut < len(messages) && messages[cut].Role == "tool" {
		cut++
	}
	if cut >= len(messages) {
		return 0, errors.New("nothing to compact yet")
	}
	var serialized strings.Builder
	for _, m := range messages[:cut] {
		fmt.Fprintf(&serialized, "[%s] %s\n", m.Role, m.Content)
		for _, part := range m.Parts {
			if part.Type == provider.ContentImage {
				fmt.Fprintf(&serialized, "[attached image] %s (%s, %d bytes; binary remains in the durable transcript)\n", part.Name, part.MediaType, part.Size)
			}
		}
		for _, call := range m.ToolCalls {
			fmt.Fprintf(&serialized, "[tool-call] %s %s\n", call.Name, call.Arguments)
		}
	}
	instructions := prompts.Text(prompts.CompactInstructions)
	if focus != "" {
		instructions += " " + prompts.Render(prompts.CompactFocus, focus)
	}
	requestMaxTokens, err := a.nextRequestMaxTokens()
	if err != nil {
		return 0, err
	}
	reasoningEffort := ""
	if a.providerConfig.Reasoning != nil {
		reasoningEffort = a.providerConfig.Reasoning.Effort
	}
	req := provider.Request{Model: model, System: prompts.Text(prompts.CompactSystem), Messages: []provider.Message{{Role: "user", Content: instructions + "\n\n---\n" + serialized.String()}}, MaxTokens: requestMaxTokens, ReasoningEffort: reasoningEffort}
	response, err := client.Chat(ctx, req, nil)
	if err != nil {
		return 0, err
	}
	response.Usage = estimateCost(response.Usage, a.providerConfig.Pricing)
	a.mu.Lock()
	a.usage.InputTokens += response.Usage.InputTokens
	a.usage.OutputTokens += response.Usage.OutputTokens
	a.usage.CachedTokens += response.Usage.CachedTokens
	a.usage.CacheWriteTokens += response.Usage.CacheWriteTokens
	a.usage.ReasoningTokens += response.Usage.ReasoningTokens
	a.usage.CostUSD += response.Usage.CostUSD
	a.usage.CostAvailable = a.usage.CostAvailable || response.Usage.CostAvailable
	a.usage.CostEstimated = a.usage.CostEstimated || response.Usage.CostEstimated
	usage := a.usage
	onUsage := a.onUsage
	tokenBudget := a.tokenBudget
	costBudget := a.costBudgetUSD
	a.mu.Unlock()
	if onUsage != nil {
		onUsage(usage)
	}
	if response.Usage.InputTokens+response.Usage.OutputTokens > 0 && send != nil {
		e := event.New(event.KindUsage)
		e.Usage = eventUsage(response.Usage)
		send(e)
	}
	if tokenBudget > 0 && usage.InputTokens+usage.OutputTokens > tokenBudget {
		return 0, fmt.Errorf("%w: provider reported %d tokens after a limit of %d", ErrTokenBudgetExceeded, usage.InputTokens+usage.OutputTokens, tokenBudget)
	}
	if costBudget > 0 && (!response.Usage.CostAvailable || usage.CostUSD > costBudget) {
		return 0, fmt.Errorf("%w: estimated spend $%.6f exceeded or could not be verified against $%.6f", ErrCostBudgetExceeded, usage.CostUSD, costBudget)
	}
	failures := recentFailureEvidence(messages[:cut])
	summaryContent := "[Context summary — earlier conversation compressed to save space]\n" + response.Content
	if failures != "" {
		summaryContent += "\n\n[Recent failure evidence retained verbatim]\n" + failures
	}
	summary := provider.Message{Role: "user", Content: summaryContent}
	a.mu.Lock()
	// The conversation only grows during a run, so the prefix we summarized
	// is stable; re-derive the tail in case messages were appended.
	tail := append([]provider.Message(nil), a.messages[cut:]...)
	a.messages = append([]provider.Message{summary}, tail...)
	a.lastInputTokens = 0
	a.usageWatermark = 0
	notify := a.onCompaction
	a.mu.Unlock()
	if notify != nil {
		notify(summary, cut)
	}
	if send != nil {
		e := event.New(event.KindCompaction)
		e.Text = fmt.Sprintf("compacted %d messages into a summary", cut)
		send(e)
	}
	a.lifecycle.Fire(ctx, hooks.Payload{Event: "compaction", Workspace: a.workspace, Subject: "compaction", Detail: map[string]any{"replaced_messages": cut}})
	return cut, nil
}
func (a *Agent) MessageCount() int { a.mu.RLock(); defer a.mu.RUnlock(); return len(a.messages) }

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

const retainedFailureBytes = 16 << 10

const retainedFailureTruncation = "\n… failure evidence truncated at the 16 KiB retention limit …"

// recentFailureEvidence keeps exact, bounded failure text outside the
// provider-generated summary. This prevents compaction quality from silently
// turning a still-relevant error into an inaccurate paraphrase.
func recentFailureEvidence(messages []provider.Message) string {
	var failures []string
	total := 0
	for i := len(messages) - 1; i >= 0 && len(failures) < 3; i-- {
		message := messages[i]
		if message.Role != "tool" || !isFailureResult(message.Content) {
			continue
		}
		value := failureEvidence(message.Content)
		if total+len(value) > retainedFailureBytes {
			remaining := retainedFailureBytes - total
			if remaining <= len(retainedFailureTruncation) {
				break
			}
			value = clipUTF8(value, remaining-len(retainedFailureTruncation)) + retainedFailureTruncation
		}
		failures = append(failures, fmt.Sprintf("tool_call_id=%s\n%s", message.ToolCallID, value))
		total += len(value)
	}
	for left, right := 0, len(failures)-1; left < right; left, right = left+1, right-1 {
		failures[left], failures[right] = failures[right], failures[left]
	}
	return strings.Join(failures, "\n\n")
}

func isFailureResult(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "tool error:") ||
		strings.HasPrefix(lower, "tool denied:") ||
		strings.HasPrefix(lower, "tool blocked") ||
		strings.Contains(lower, "tool call interrupted:")
}

func failureEvidence(value string) string {
	lower := strings.ToLower(value)
	for _, marker := range []string{"tool error:", "tool call interrupted:", "tool denied:", "tool blocked"} {
		if index := strings.LastIndex(lower, marker); index >= 0 {
			return value[index:]
		}
	}
	return value
}

func clipUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && (value[end]&0xc0) == 0x80 {
		end--
	}
	return value[:end]
}
