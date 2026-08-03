package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robert-mcdermott/collomia/internal/agent"
	"github.com/robert-mcdermott/collomia/internal/audit"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/diffmodel"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/goalgraph"
	"github.com/robert-mcdermott/collomia/internal/hooks"
	"github.com/robert-mcdermott/collomia/internal/logging"
	mcpclient "github.com/robert-mcdermott/collomia/internal/mcp"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/redact"
	"github.com/robert-mcdermott/collomia/internal/sandbox"
	"github.com/robert-mcdermott/collomia/internal/session"
	"github.com/robert-mcdermott/collomia/internal/skills"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

type Runtime struct {
	Workspace   string
	Config      appconfig.Config
	Agent       *agent.Agent
	Registry    *tools.Registry
	Permissions *permission.Manager
	Skills      skills.Catalog
	MCP         *mcpclient.Manager
	Redactor    *redact.Redactor
	Logger      *slog.Logger
	LogPath     string
	Sessions    *session.Store
	Session     *session.Session
	Artifacts   *session.ArtifactManager
	Attachments *session.AttachmentManager
	Changes     *diffmodel.Tracker
	Plan        *plan.Board
	// GoalGraph is non-nil for an internal evaluation or an explicitly
	// approved/resumed TUI preview. Persisted state alone never sets it.
	GoalGraph *goalgraph.Graph
	Team      *agent.Team
	Processes *tools.ProcessManager
	Warnings  []error
	Hooks     *hooks.Runner
	// Steering carries guidance typed while the primary agent is mid-turn.
	// It lives on the runtime rather than in the TUI because the agent is
	// constructed here and every surface — TUI, browser terminal, a future
	// local service API — needs the same one queue.
	Steering *agent.SteeringQueue
	// ActiveAgent is the visible named primary profile. Empty means the
	// ordinary unprofiled primary agent.
	ActiveAgent string
	// Audit is the primary agent's ledger. Delegated agents hold their own
	// handles on the same workspace file, which is why completeness is
	// latched separately in auditHealth rather than read from this one.
	Audit                 *audit.Ledger
	auditHealth           *auditHealth
	goalStateToken        func(context.Context) (string, error)
	orchestrationMu       sync.Mutex
	orchestrationProposal *orchestrationProposal
}

type orchestrationProposal struct {
	Goal             string
	BaseRevision     uint64
	Started          time.Time
	BaseUsage        provider.Usage
	BaseIterations   int
	PreviousPlanMode bool
}

type goalAccountingSeed struct {
	Started time.Time
	Primary goalgraph.WorkUsage
}

func providerUsageDelta(current, baseline provider.Usage) provider.Usage {
	delta := provider.Usage{
		InputTokens:      max(0, current.InputTokens-baseline.InputTokens),
		OutputTokens:     max(0, current.OutputTokens-baseline.OutputTokens),
		CachedTokens:     max(0, current.CachedTokens-baseline.CachedTokens),
		CacheWriteTokens: max(0, current.CacheWriteTokens-baseline.CacheWriteTokens),
		ReasoningTokens:  max(0, current.ReasoningTokens-baseline.ReasoningTokens),
		CostUSD:          max(0, current.CostUSD-baseline.CostUSD),
		CostEstimated:    current.CostEstimated,
	}
	if delta.InputTokens+delta.OutputTokens > 0 || delta.CostUSD > 0 {
		delta.CostAvailable = current.CostAvailable
	}
	return delta
}

// OrchestratedGoalTokenBudget reports the cumulative graph allowance beside
// the per-request context window without activating saved state. During a
// proposal, it reports the exact work that approval would seed into the graph.
func (r *Runtime) OrchestratedGoalTokenBudget() (phase string, used, limit int, ok bool) {
	if r == nil || r.Agent == nil {
		return "", 0, 0, false
	}
	r.orchestrationMu.Lock()
	defer r.orchestrationMu.Unlock()
	if r.GoalGraph != nil {
		status := r.GoalGraph.BudgetStatus(time.Now())
		phase = "active"
		if outcome, _ := r.GoalGraph.Outcome(); outcome != "" {
			phase = string(outcome)
		}
		return phase, status.Usage.InputTokens + status.Usage.OutputTokens, status.Limits.MaxTokens, true
	}
	if r.orchestrationProposal == nil {
		return "", 0, 0, false
	}
	usage := providerUsageDelta(r.Agent.Usage(), r.orchestrationProposal.BaseUsage)
	return "proposal", usage.InputTokens + usage.OutputTokens, goalgraph.DefaultLimits().MaxAggregateTokens, true
}

// auditHealth latches every reason the audit record for this session might be
// incomplete — an unopenable ledger at startup, a write that failed later,
// from the primary agent or from any delegated one. A status surface asking
// "is the record complete?" must not have to consult several ledger handles
// and guess about the ones that were never created.
type auditHealth struct {
	mu       sync.Mutex
	failures int
	first    error
	firstAt  time.Time
}

func (h *auditHealth) note(err error) {
	if h == nil || err == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failures == 0 {
		h.first = err
		h.firstAt = time.Now().UTC()
	}
	h.failures++
}

// AuditHealth reports whether this session's audit record is known to be
// incomplete: how many failures were seen, and the first one. A zero count
// means every decision and outcome reached disk.
func (r *Runtime) AuditHealth() (failures int, first error, at time.Time) {
	if r == nil || r.auditHealth == nil {
		return 0, nil, time.Time{}
	}
	r.auditHealth.mu.Lock()
	defer r.auditHealth.mu.Unlock()
	return r.auditHealth.failures, r.auditHealth.first, r.auditHealth.firstAt
}

// LogEvent records a runtime event in the debug log and the durable
// session, so a crashed or killed session can be reconstructed.
func (r *Runtime) LogEvent(e event.Event) {
	tool, errText := "", ""
	if e.Tool != nil {
		tool = e.Tool.Name
	}
	if e.Error != "" {
		errText = e.Error
	}
	r.Logger.Debug("event", "kind", string(e.Kind), "tool", tool, "error", errText, "failure_id", e.FailureID)
	if r.Session != nil {
		r.Session.AppendEvent(e)
	}
	// A checkpoint is a completed turn, so the change tracker learns turn
	// boundaries from the same durable event the session counts them from.
	// Doing it here rather than in the agent keeps one funnel: the TUI, the
	// headless runner, and the browser terminal all report events through it.
	if e.Kind == event.KindTurnEnd && r.Changes != nil {
		r.Changes.CompleteTurn()
	}
}

// NewRedactor collects every secret the configuration knows about so logs,
// events, and previews can scrub them.
func NewRedactor(cfg appconfig.Config) *redact.Redactor {
	r := redact.New()
	for _, p := range cfg.Providers {
		r.AddSecret(p.APIKey)
		if p.Type == "bedrock" {
			r.AddSecret(os.Getenv(provider.BedrockBearerTokenEnv))
		}
		if p.Auth == "entra" && (p.Type == "azure-openai" || p.Type == "azure-foundry" || p.Type == "azure-foundry-anthropic") {
			// DefaultAzureCredential reads these standard environment secrets.
			// The SDK should never echo them, but register them as defense in
			// depth for debug logs and structured error events.
			r.AddSecret(os.Getenv("AZURE_CLIENT_SECRET"))
			r.AddSecret(os.Getenv("AZURE_CLIENT_CERTIFICATE_PASSWORD"))
		}
		for _, v := range p.Headers {
			r.AddSecret(v)
		}
	}
	for _, server := range cfg.MCP {
		for _, v := range server.Env {
			r.AddSecret(v)
		}
		for _, v := range server.Headers {
			r.AddSecret(v)
		}
	}
	return r
}

type Options struct {
	Workspace, Provider, Model, Agent, Autonomy string
	// ProviderCredential carries a credential verified during automatic setup
	// into the session opened immediately afterwards. It is never persisted and
	// avoids putting the value in the process environment on platforms without
	// an OS credential store.
	ProviderCredential     string
	Plan, Debug, Ephemeral bool
	// Resume loads an existing session ID; Continue resumes the most
	// recently updated session. Otherwise a new session is created.
	Resume   string
	Continue bool
	Approver permission.Approver
	// Asker lets the agent pause and ask the user a typed question. When
	// nil (headless), the ask_user tool is not registered.
	Asker func(ctx context.Context, question string, options []string) (string, error)
	// OrchestratedGoal opts this runtime into the internal graph controller
	// using an already-approved logical plan. It remains an
	// evaluation/embedder seam; the user preview activates only through
	// an explicit runtime method called by the TUI.
	// Supplying it while resuming a graph-bearing session restores the durable
	// graph instead of creating a new one.
	OrchestratedGoal *plan.Plan
}

func New(ctx context.Context, opts Options) (*Runtime, error) {
	if opts.OrchestratedGoal != nil && opts.Plan {
		return nil, fmt.Errorf("orchestrated goal execution cannot run in read-only planning mode")
	}
	if opts.OrchestratedGoal != nil && opts.Ephemeral {
		return nil, fmt.Errorf("orchestrated goal execution requires durable session persistence")
	}
	workspace, err := filepath.Abs(opts.Workspace)
	if err != nil {
		return nil, err
	}
	cfg, err := appconfig.Load(workspace)
	if err != nil {
		return nil, err
	}
	if opts.Autonomy != "" {
		cfg.Permissions.Mode = opts.Autonomy
	}
	activeAgent := strings.TrimSpace(opts.Agent)
	switch activeAgent {
	case "default", "none":
		activeAgent = ""
	case "":
		activeAgent = cfg.DefaultAgent
	}
	profile, err := cfg.PrimaryAgent(activeAgent)
	if err != nil {
		return nil, err
	}
	providerName, p, model, err := cfg.Selected(opts.Provider, opts.Model)
	if err != nil {
		return nil, err
	}
	if opts.ProviderCredential != "" {
		p.APIKey = opts.ProviderCredential
		cfg.Providers[providerName] = p
	}
	if opts.Model == "" && profile.Model != "" {
		model = profile.Model
	}
	if profile.Reasoning != nil {
		reasoning := *profile.Reasoning
		p.Reasoning = &reasoning
	}
	client, err := provider.New(providerName, p, model)
	if err != nil {
		return nil, err
	}
	client = provider.WithResilience(client)
	registry, tracker, processes, err := tools.Builtins(workspace, cfg)
	if err != nil {
		return nil, err
	}
	catalog, err := skills.Discover(workspace, cfg.ProjectTrusted)
	if err != nil {
		return nil, err
	}
	registry.Add(skills.Tool(catalog))
	// Instructions layer global (user-level) before project (trusted only);
	// later sections take precedence when they conflict.
	var sections []string
	if global, gErr := skills.GlobalInstructions(); gErr == nil && global != "" {
		sections = append(sections, global)
	}
	if cfg.ProjectTrusted {
		projectInstructions, pErr := skills.ProjectInstructions(workspace)
		if pErr != nil {
			return nil, pErr
		}
		if projectInstructions != "" {
			sections = append(sections, projectInstructions)
		}
	}
	instructions := strings.Join(sections, "\n\n")
	permissions := permission.New(cfg.Permissions, opts.Approver)
	if err := permissions.SetProfile(profile.Permissions); err != nil {
		return nil, err
	}
	redactor := NewRedactor(cfg)
	logger := logging.Discard()
	logPath := ""
	if opts.Debug || cfg.Options.Debug {
		if fileLogger, path, logErr := logging.Setup(true, redactor.Redact); logErr == nil {
			logger = fileLogger
			logPath = path
			logger.Debug("session start", "workspace", workspace, "config", cfg.Source, "provider", providerName, "model", model, "autonomy", cfg.Permissions.Mode)
		}
	}
	// The audit ledger's own health is latched here rather than only reported
	// as a startup warning: a directory that was writable at startup can stop
	// being writable mid-session, and the record must be able to say so at the
	// moment someone asks whether it is complete.
	health := &auditHealth{}
	ledger, ledgerErr := audit.Open(workspace)
	if ledgerErr == nil {
		ledger.Redact = redactor.Redact
		permissions.SetLedger(ledger)
	}
	mcpManager, warnings := mcpclient.ConnectAll(ctx, cfg.MCP, registry, mcpclient.Options{Workspace: workspace, Asker: opts.Asker})
	for _, issue := range catalog.Issues {
		warnings = append(warnings, fmt.Errorf("skills: %s", issue))
	}
	if ledgerErr != nil {
		wrapped := fmt.Errorf("audit ledger unavailable: %w", ledgerErr)
		warnings = append(warnings, wrapped)
		health.note(wrapped)
	}
	if !cfg.ProjectTrusted {
		warnings = append(warnings, fmt.Errorf("workspace is not trusted: project configuration, skills, and instructions were ignored; run `collo trust` after reviewing %s", appconfig.ProjectFile))
	}
	// Durable session: create, resume by ID, or continue the latest. Ephemeral
	// runs deliberately skip even opening the session store; audit records,
	// logs explicitly requested with --debug, and workspace changes remain.
	var store *session.Store
	var storeErr error
	var sess *session.Session
	if !opts.Ephemeral {
		store, storeErr = session.Open(workspace)
		if storeErr != nil {
			warnings = append(warnings, fmt.Errorf("session persistence unavailable: %w", storeErr))
		} else {
			switch {
			case opts.Resume != "":
				sess, storeErr = store.Load(opts.Resume)
			case opts.Continue:
				var latest string
				if latest, storeErr = store.Latest(); storeErr == nil {
					sess, storeErr = store.Load(latest)
				}
			default:
				sess, storeErr = store.New(providerName, model)
			}
			if storeErr != nil {
				return nil, fmt.Errorf("session: %w", storeErr)
			}
		}
	}
	sessionID := ""
	if sess != nil {
		sessionID = sess.Meta.ID
	}
	// runtime is captured before it exists so a ledger failure that happens
	// mid-session reaches the same event funnel every surface reads. Nothing
	// appends to the ledger before this constructor returns, so the closure
	// never observes the nil.
	var runtime *Runtime
	auditFailure := func(err error) {
		warning := event.New(event.KindWarning)
		if err != nil {
			health.note(err)
			logger.Warn("audit ledger", "error", err.Error())
			warning.Text = err.Error()
		} else {
			logger.Info("audit ledger", "recovered", true)
			warning.Text = "audit ledger writes resumed; the entries lost while it was failing are declared in the ledger as a gap"
		}
		if runtime != nil {
			runtime.LogEvent(warning)
		}
	}
	if ledger != nil {
		ledger.Identify(sessionID, audit.ActorPrimary, "")
		ledger.OnFailure = auditFailure
	}
	// Structured plan artifact, maintained by the agent via update_plan and
	// persisted with the session.
	board := plan.NewBoard()
	registry.Add(plan.Tool(board))
	if sess != nil {
		attachBoard(board, sess)
	}
	goalStateToken := func(tokenCtx context.Context) (string, error) {
		return goalgraph.WorkspaceStateToken(tokenCtx, workspace)
	}
	goal, err := attachGoalGraph(ctx, opts.OrchestratedGoal, board, sess, goalStateToken)
	if err != nil {
		return nil, fmt.Errorf("orchestrated goal: %w", err)
	}
	if goal != nil {
		registry.Add(goalgraph.RevisionTool{Graph: goal})
		registry.Add(goalgraph.BlockTool{Graph: goal})
	}
	artifacts := session.NewArtifactManager()
	artifacts.Use(sess)
	attachments := session.NewAttachmentManager()
	attachments.Use(sess)
	var artifactSink *session.ArtifactManager
	if sess != nil {
		registry.Add(session.ArtifactTool(artifacts))
		artifactSink = artifacts
		// Commands historically captured only the model-preview limit. Keep live
		// output at that size, but retain enough returned data for the agent layer
		// to create a bounded artifact when a command is noisier.
		if item, ok := registry.Get("run_command"); ok {
			if command, ok := item.(*tools.RunCommandTool); ok {
				command.StreamOutputBytes = cfg.Options.MaxToolOutputBytes
				command.MaxOutputBytes = max(cfg.Options.MaxToolOutputBytes, session.ArtifactResultLimit) + 1
			}
		}
	}
	if opts.Asker != nil {
		registry.Add(askUserTool(opts.Asker))
	}
	lifecycle := hooks.NewRunner(workspace, cfg.Hooks, func(note hooks.Note) {
		logger.Warn("hook", "event", note.Event, "command", note.Command, "note", note.Text)
	})
	activeCatalog := catalog
	if len(profile.Skills) > 0 {
		activeCatalog = catalog.Restrict(profile.Skills)
	}
	maxIterations := cfg.Options.MaxIterations
	if profile.MaxIterations > 0 {
		maxIterations = profile.MaxIterations
	}
	agentOptions := agent.Options{Client: client, ProviderName: providerName, Model: model, ProviderConfig: p, Workspace: workspace, Registry: registry, Permissions: permissions, Catalog: activeCatalog, ProjectInstructions: instructions, MaxIterations: maxIterations, MaxToolOutput: cfg.Options.MaxToolOutputBytes, TokenBudget: profile.TokenBudget, CostBudgetUSD: profile.CostBudgetUSD, DisabledTools: cfg.Options.DisabledTools, PlanMode: opts.Plan, Hooks: lifecycle, AuditRedact: redactor.Redact, Artifacts: artifactSink, Attachments: attachments, CompletionPlan: board, GoalGraph: goal, GoalStateToken: goalStateToken, PinnedContext: func() string {
		current := board.Current()
		if current == nil {
			return ""
		}
		return "Active structured plan:\n" + current.Render()
	}}
	// The primary agent reaches the same iteration-boundary hook delegated
	// children use, so guidance typed mid-turn lands where the conversation
	// is consistent rather than underneath an in-flight request.
	steering := agent.NewSteeringQueue()
	agentOptions.TakeSteering = steering.Take
	if sess != nil {
		agentOptions.OnMessage = sess.AppendMessage
		agentOptions.OnCompaction = sess.AppendCompaction
		agentOptions.PersistenceError = sess.Err
	}
	agentOptions.SessionID = sessionID
	agentOptions.AuditFailure = auditFailure
	agentRuntime := agent.New(agentOptions)
	team := agent.NewTeam()
	attachTeam(team, sess)
	agentRuntime.AddDelegationTool(cfg, opts.Approver, team, board)
	agentRuntime.ApplyProfile(agent.ProfileSettings{
		Name: activeAgent, Instructions: profile.Instructions, Catalog: activeCatalog,
		Tools: profile.Tools, DisabledTools: cfg.Options.DisabledTools, Skills: profile.Skills,
		MaxIterations: maxIterations, TokenBudget: profile.TokenBudget, CostBudgetUSD: profile.CostBudgetUSD,
	})
	if sess != nil && (opts.Resume != "" || opts.Continue) {
		agentRuntime.SetMessages(sess.Active())
		agentRuntime.SetUsage(sess.Usage())
		sess.FlushInterrupted()
	}
	for _, warning := range warnings {
		logger.Warn("startup warning", "warning", warning.Error())
	}
	lifecycle.Fire(ctx, hooks.Payload{Event: "session_start", Workspace: workspace, Subject: "session_start", Detail: map[string]any{"session_id": sessionID, "provider": providerName, "model": model}})
	runtime = &Runtime{Workspace: workspace, Config: cfg, Agent: agentRuntime, Registry: registry, Permissions: permissions, Skills: catalog, MCP: mcpManager, Redactor: redactor, Logger: logger, LogPath: logPath, Sessions: store, Session: sess, Artifacts: artifacts, Attachments: attachments, Changes: tracker, Plan: board, GoalGraph: goal, Team: team, Processes: processes, Warnings: warnings, Hooks: lifecycle, ActiveAgent: activeAgent, Steering: steering, Audit: ledger, auditHealth: health, goalStateToken: goalStateToken}
	agentRuntime.SetGoalWriterVerifier(func(verifyCtx context.Context, id string) ([]agent.DelegateVerification, error) {
		return runtime.VerifyDelegateSuite(verifyCtx, id, nil)
	})
	runtime.alignChangeTurns()
	runtime.addReviewedIntegrationTools()
	runtime.logGoalGraphUpdates()
	return runtime, nil
}

// alignChangeTurns points the change tracker's turn numbering at the active
// session's completed turns, so a checkpoint the user picks from the session's
// history means the same turn to both halves of a restore. A resumed session
// carries turns whose file mutations this process never recorded; the tracker
// keeps an empty history for them, which is what makes a restore report that it
// reversed nothing instead of implying it reversed everything.
func (r *Runtime) alignChangeTurns() {
	if r == nil || r.Changes == nil {
		return
	}
	turns := 0
	if r.Session != nil {
		turns = r.Session.Meta.Turns
	}
	r.Changes.SetCompletedTurns(turns)
}

// OrchestratedProposalPrompt begins the read-only design half of the explicit
// preview. The model can investigate and propose intent, but only the later
// user command can convert that plan into runtime execution state.
func OrchestratedProposalPrompt(goal string) string {
	return fmt.Sprintf(`Create a proposed Orchestrated Goal graph for this outcome:

%s

Remain in read-only planning mode. Investigate only as needed, then call update_plan with the complete proposal. Proposal-phase inspection is grounding, not an already-completed graph node: do not add a node merely to repeat investigation you just performed. Include a pending read_only node only when fresh bounded investigation must run after approval as an explicit dependency. Use the smallest coherent graph: prefer 1–3 substantive outcome nodes for a scoped change and 4–6 only for a broad goal. Use more only when a distinct dependency, permission, write-scope, isolation, or recovery boundary requires it; 12 steps is a hard maximum, not a target. Coalesce serial changes that touch the same scope and share one verification surface instead of creating a node for every file, layer, or command. Within a node, batch independent reads and related edits when the available tools support it. Do not begin future-node work: after a node's final successful verifier, return a tool-free completion proposal immediately so the runtime can advance the graph. Every proposed execution step should be pending, use stable non-zero IDs, declare dependencies, include at least one concrete acceptance criterion describing observable evidence for completion, and set execution to primary, read_only, or isolated_write. At approval the runtime initializes every node pending regardless of model-authored plan status; proposal prose and evidence never become runtime completion. For every primary node that can change files, include a direct build, lint, or test command that can verify that node after its last mutation. If the project has no applicable test surface yet, the first mutating node must create a focused smoke test so the detected verifier has real work to run; a server-start or model-authored success claim alone cannot satisfy the runtime evidence gate. Use read_only only for bounded repository investigation that can safely run from the shared workspace without changing files or running commands; independent dependency-ready read_only nodes may use at most two automatic workers after approval. Default to primary for end-to-end build and change goals. Use isolated_write only when the user explicitly requests terminal retained candidates for manual review: each needs an explicit narrow write_paths contract disjoint from sibling writers, the candidate-only graph may include read_only prerequisites but no primary nodes, and every isolated_write node must be a terminal leaf. Never make later work depend on isolated_write because the current preview does not select or integrate candidates or let them unlock dependents. One bounded wave of at most two eligible nodes may create independently verified worktree candidates from one clean Git base and then stop for review. Use primary for parent-workspace changes, final integration, combined verification, ambiguity, overlapping scopes, or inherently serial work. Do not implement anything. After updating the plan, summarize its critical path, expected read/write fan-out, verification expectations, and any material ambiguity for the user to review.`, strings.TrimSpace(goal))
}

// OrchestratedExecutionPrompt is submitted only after the user explicitly
// approves the visible proposal. The attached runtime graph, not this prose,
// owns readiness, attempts, evidence, and terminal completion.
func OrchestratedExecutionPrompt(goal string) string {
	return fmt.Sprintf("The user explicitly approved the visible Orchestrated Goal proposal for this session. Execute the runtime-owned graph now and pursue this outcome: %s", strings.TrimSpace(goal))
}

// OrchestratedResumePrompt is conversational context only. Resume authority
// comes from the explicit runtime transition that clears the durable pause.
func OrchestratedResumePrompt(goal string) string {
	return fmt.Sprintf("The user explicitly resumed the paused Orchestrated Goal. Continue the existing runtime-owned graph from its preserved attempts and evidence, pursuing this outcome: %s", strings.TrimSpace(goal))
}

// OrchestratedRetryPrompt tells the primary agent why a terminal blocker was
// reopened without allowing prose to rewrite the preserved blocked attempt.
func OrchestratedRetryPrompt(goal string, nodeID int) string {
	return fmt.Sprintf("The user explicitly requested one safe bounded retry of Orchestrated Goal node %d. Continue the runtime-owned graph from its preserved attempts and evidence, pursuing this outcome: %s", nodeID, strings.TrimSpace(goal))
}

// ReviewPrompt is the canned prompt behind `collo review` and `/review`:
// a read-only pass over pending changes with findings tied to files/lines.
// A ref of "-" (or "") reviews uncommitted changes; instructions, when
// non-empty, focus the review.
func ReviewPrompt(ref, instructions string) string {
	scope := "the uncommitted changes in this repository (use git_status, then git_diff; add staged=true for the index)"
	if ref != "" && ref != "-" {
		scope = fmt.Sprintf("the changes relative to %s (use git_diff with ref %q, and git_log to understand the history)", ref, ref)
	}
	focus := ""
	if strings.TrimSpace(instructions) != "" {
		focus = "\nReviewer instructions (follow these in addition to the standard checks): " + strings.TrimSpace(instructions) + "\n"
	}
	return fmt.Sprintf(`Review %s.
%s
Read enough surrounding code (read_file) to judge each change in context. Do not modify any files.

Report:
1. Findings ordered by severity (bugs, then correctness risks, then style), each with the exact file and line, the problem, and a concrete suggestion.
2. Anything the diff forgot: missing tests, stale docs, unhandled errors.
3. A one-paragraph verdict: is this safe to merge as-is?

If there are no changes to review, say so plainly.`, scope, focus)
}

// VerifyPrompt is the canned prompt behind `collo verify` and `/verify`: it
// detects the project's real build/lint/test commands, runs them, and ties
// each outcome to a plan step instead of the model guessing at commands or
// asserting success it never observed.
func VerifyPrompt(focus string) string {
	scope := "this project's standard build, lint, and test commands"
	if focus != "" {
		scope = fmt.Sprintf("this project, focused on: %s", focus)
	}
	return fmt.Sprintf(`Verify %s.

1. Call detect_verification to find the commands this project conventionally uses. If it finds nothing, inspect the repository (list_files, read_file) or ask the user how it is built and tested — do not guess.
2. Record each command as a step with update_plan before running it.
3. Run each command with run_command and watch its live output. Mark the step done with the command's outcome as evidence, or blocked with the exact failing output if it fails. Never mark a step done unless the command's own result says it passed.
4. Finish with a one-paragraph summary: what passed, what failed, and the exact failing output for anything still broken.

Verify the current working tree, not a stale build. Do not modify any files — report failures for the user to address.`, scope)
}

// ListModels queries a provider's live model catalog when its API supports
// discovery (OpenAI-compatible and Anthropic endpoints do).
func (r *Runtime) ListModels(ctx context.Context, providerName string) ([]provider.ModelInfo, error) {
	name, p, model, err := r.Config.Selected(providerName, "")
	if err != nil {
		return nil, err
	}
	client, err := provider.New(name, p, model)
	if err != nil {
		return nil, err
	}
	client = provider.WithResilience(client)
	capabilities, err := provider.CapabilitiesFor(p.Type, model, p.Context)
	if err != nil {
		return nil, err
	}
	if capabilities.ModelDiscovery == provider.CapabilityUnsupported {
		return nil, fmt.Errorf("provider %s does not expose model discovery through its %s adapter", name, p.Type)
	}
	lister, ok := client.(provider.ModelLister)
	if !ok {
		return nil, fmt.Errorf("provider %s does not support model discovery", name)
	}
	listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	models, err := lister.ListModels(listCtx)
	if err != nil {
		return nil, err
	}
	for i := range models {
		models[i].Capabilities, err = provider.CapabilitiesFor(p.Type, models[i].ID, p.Context)
		if err != nil {
			return nil, err
		}
	}
	return models, nil
}

// ProviderAvailability is deliberately four-state. A provider without a
// catalog/health endpoint is unverified, not unavailable; configured is the
// immediate state rendered while a supported live probe runs.
type ProviderAvailability string

const (
	ProviderConfigured  ProviderAvailability = "configured"
	ProviderAvailable   ProviderAvailability = "available"
	ProviderUnavailable ProviderAvailability = "unavailable"
	ProviderUnverified  ProviderAvailability = "unverified"
)

// ProviderStatus combines static adapter capabilities with a best-effort live
// model-catalog probe for TUI inspection.
type ProviderStatus struct {
	Name         string
	Type         string
	DefaultModel string
	Availability ProviderAvailability
	Models       []provider.ModelInfo
	Capabilities provider.Capabilities
	Error        string
}

// ConfiguredProviders returns a network-free snapshot suitable for immediate
// rendering while live catalog checks run in the background.
func (r *Runtime) ConfiguredProviders() []ProviderStatus {
	names := r.Config.ProviderNames()
	statuses := make([]ProviderStatus, 0, len(names))
	for _, name := range names {
		p := r.Config.Providers[name]
		model := p.Model
		if model == "" {
			model = r.Config.DefaultModel
		}
		capabilities, err := provider.CapabilitiesFor(p.Type, model, p.Context)
		status := ProviderStatus{Name: name, Type: p.Type, DefaultModel: model, Availability: ProviderConfigured, Capabilities: capabilities}
		if err != nil {
			status.Error = r.providerStatusError(err)
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// InspectProviders probes model catalogs concurrently with a small bound so a
// slow endpoint does not make /models wait serially for every configured
// provider. Unsupported discovery remains explicitly unverified.
func (r *Runtime) InspectProviders(ctx context.Context) []ProviderStatus {
	statuses := r.ConfiguredProviders()
	var wg sync.WaitGroup
	limit := make(chan struct{}, 4)
	for i := range statuses {
		if statuses[i].Capabilities.ModelDiscovery == provider.CapabilityUnsupported {
			statuses[i].Availability = ProviderUnverified
			continue
		}
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			select {
			case limit <- struct{}{}:
				defer func() { <-limit }()
			case <-ctx.Done():
				statuses[index].Availability = ProviderUnavailable
				statuses[index].Error = ctx.Err().Error()
				return
			}
			models, err := r.ListModels(ctx, statuses[index].Name)
			if err != nil {
				statuses[index].Availability = ProviderUnavailable
				statuses[index].Error = r.providerStatusError(err)
				return
			}
			statuses[index].Availability = ProviderAvailable
			statuses[index].Models = models
		}(i)
	}
	wg.Wait()
	return statuses
}

func (r *Runtime) providerStatusError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if r.Redactor != nil {
		message = r.Redactor.Redact(message)
	}
	message = strings.Map(func(char rune) rune {
		if char < 0x20 || char == 0x7f {
			return -1
		}
		return char
	}, message)
	message = strings.Join(strings.Fields(message), " ")
	const limit = 512
	runes := []rune(message)
	if len(runes) > limit {
		message = string(runes[:limit]) + "…"
	}
	return message
}

// attachBoard wires plan persistence to a session and restores its last
// recorded plan.
func attachBoard(board *plan.Board, sess *session.Session) {
	board.OnUpdate = func(p plan.Plan) {
		if data, err := json.Marshal(p); err == nil {
			sess.AppendPlan(data)
		}
	}
	if len(sess.PlanRaw) > 0 {
		var restored plan.Plan
		if json.Unmarshal(sess.PlanRaw, &restored) == nil {
			board.Restore(restored)
			return
		}
	}
	board.Clear()
}

// attachGoalGraph creates or restores the OG-1 runtime-owned graph. Requiring
// an explicit approved plan here is the activation boundary: persisted data,
// project configuration, and model output cannot turn Standard mode into
// orchestrated execution on their own.
func attachGoalGraph(ctx context.Context, approved *plan.Plan, board *plan.Board, sess *session.Session, stateToken func(context.Context) (string, error)) (*goalgraph.Graph, error) {
	if approved == nil {
		return nil, nil
	}
	if sess == nil {
		return nil, errors.New("durable session persistence is unavailable")
	}
	if len(sess.GoalGraphRaw) > 0 {
		return restoreGoalGraph(ctx, sess, stateToken)
	}
	return createGoalGraph(ctx, approved, board, sess, nil)
}

func goalGraphPersister(sess *session.Session) goalgraph.PersistFunc {
	return func(_ context.Context, snapshot goalgraph.Snapshot, durable bool) error {
		data, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		return sess.AppendGoalGraph(data, durable)
	}
}

func restoreGoalGraph(ctx context.Context, sess *session.Session, stateToken func(context.Context) (string, error)) (*goalgraph.Graph, error) {
	if sess == nil || len(sess.GoalGraphRaw) == 0 {
		return nil, errors.New("this session has no saved orchestrated goal graph")
	}
	var snapshot goalgraph.Snapshot
	if err := json.Unmarshal(sess.GoalGraphRaw, &snapshot); err != nil {
		return nil, fmt.Errorf("decode saved graph: %w", err)
	}
	graph, err := goalgraph.Restore(snapshot, goalgraph.Options{Persist: goalGraphPersister(sess)})
	if err != nil {
		return nil, fmt.Errorf("restore saved graph: %w", err)
	}
	token := ""
	if stateToken != nil {
		token, _ = stateToken(ctx)
	}
	if err := graph.Recover(ctx, token); err != nil {
		return nil, fmt.Errorf("recover saved graph: %w", err)
	}
	return graph, nil
}

func createGoalGraph(ctx context.Context, approved *plan.Plan, board *plan.Board, sess *session.Session, accounting *goalAccountingSeed) (*goalgraph.Graph, error) {
	if approved == nil {
		return nil, errors.New("approved logical plan is unavailable")
	}
	if sess == nil {
		return nil, errors.New("durable session persistence is unavailable")
	}
	if len(sess.GoalGraphRaw) > 0 {
		return nil, errors.New("this session already contains a goal graph; use /orchestrate resume or start /new")
	}

	fresh := *approved
	fresh.Steps = append([]plan.Step(nil), approved.Steps...)
	for i := range fresh.Steps {
		fresh.Steps[i].DependsOn = append([]int(nil), approved.Steps[i].DependsOn...)
		fresh.Steps[i].Acceptance = append([]string(nil), approved.Steps[i].Acceptance...)
		fresh.Steps[i].WritePaths = append([]string(nil), approved.Steps[i].WritePaths...)
		// An approved logical plan seeds execution; model-authored progress from
		// a caller is never imported as runtime evidence.
		fresh.Steps[i].Status = "pending"
		fresh.Steps[i].Evidence = ""
	}
	fresh.VerificationNote = ""
	if err := board.Set(fresh); err != nil {
		return nil, fmt.Errorf("approved plan: %w", err)
	}
	_, logicalRevision := board.Snapshot()
	spec := goalgraph.Spec{Goal: fresh.Goal, Nodes: make([]goalgraph.NodeSpec, 0, len(fresh.Steps))}
	for _, step := range fresh.Steps {
		spec.Nodes = append(spec.Nodes, goalgraph.NodeSpec{ID: step.ID, Title: step.Title, DependsOn: append([]int(nil), step.DependsOn...), Acceptance: append([]string(nil), step.Acceptance...), Execution: goalgraph.Execution(step.Execution), WritePaths: append([]string(nil), step.WritePaths...)})
	}
	opts := goalgraph.Options{Persist: goalGraphPersister(sess)}
	if accounting != nil {
		opts.AccountingStarted = accounting.Started
		opts.InitialPrimary = accounting.Primary
	}
	graph, err := goalgraph.New(spec, logicalRevision, opts)
	if err != nil {
		return nil, err
	}
	if err := graph.Persist(ctx, true); err != nil {
		return nil, err
	}
	return graph, nil
}

// BeginOrchestratedProposal records a process-local consent boundary and
// returns the read-only prompt used to build a fresh visible proposal. Neither
// a persisted plan nor repository-controlled content can create this marker.
func (r *Runtime) BeginOrchestratedProposal(goal string) (string, error) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return "", errors.New("usage: /orchestrate <goal>")
	}
	if r == nil || r.Agent == nil || r.Plan == nil {
		return "", errors.New("runtime is unavailable")
	}
	if r.Session == nil {
		return "", errors.New("Orchestrated Goal requires durable session persistence")
	}
	r.orchestrationMu.Lock()
	defer r.orchestrationMu.Unlock()
	if r.GoalGraph != nil {
		if outcome, _ := r.GoalGraph.Outcome(); outcome == "" {
			return "", errors.New("an Orchestrated Goal graph is already attached; inspect or cancel it first")
		}
		if err := r.archiveTerminalGoalGraphLocked(); err != nil {
			return "", err
		}
	}
	if len(r.Session.GoalGraphRaw) > 0 {
		if err := r.archiveSavedTerminalGoalGraphLocked(); err != nil {
			return "", err
		}
	}
	_, revision := r.Plan.Snapshot()
	previousPlanMode := r.Agent.Plan()
	if r.orchestrationProposal != nil {
		previousPlanMode = r.orchestrationProposal.PreviousPlanMode
	}
	r.orchestrationProposal = &orchestrationProposal{
		Goal: goal, BaseRevision: revision, Started: time.Now().UTC(),
		BaseUsage: r.Agent.Usage(), BaseIterations: r.Agent.ProviderIterations(), PreviousPlanMode: previousPlanMode,
	}
	r.Agent.SetPlan(true)
	return OrchestratedProposalPrompt(goal), nil
}

func orchestratedSpec(p *plan.Plan) (goalgraph.Spec, error) {
	if p == nil {
		return goalgraph.Spec{}, errors.New("the proposal did not create a structured plan")
	}
	if err := plan.Validate(*p); err != nil {
		return goalgraph.Spec{}, fmt.Errorf("proposal plan is invalid: %w", err)
	}
	spec := goalgraph.Spec{Goal: strings.TrimSpace(p.Goal), Nodes: make([]goalgraph.NodeSpec, 0, len(p.Steps))}
	for _, step := range p.Steps {
		if len(step.Acceptance) == 0 {
			return goalgraph.Spec{}, fmt.Errorf("proposal step %d (%s) needs at least one concrete acceptance criterion", step.ID, step.Title)
		}
		spec.Nodes = append(spec.Nodes, goalgraph.NodeSpec{ID: step.ID, Title: step.Title, DependsOn: append([]int(nil), step.DependsOn...), Acceptance: append([]string(nil), step.Acceptance...), Execution: goalgraph.Execution(step.Execution), WritePaths: append([]string(nil), step.WritePaths...)})
	}
	if err := goalgraph.ValidateExecutableSpec(spec); err != nil {
		return goalgraph.Spec{}, fmt.Errorf("proposal graph is invalid: %w", err)
	}
	return spec, nil
}

// ApproveOrchestratedGoal converts only the fresh proposal created after
// BeginOrchestratedProposal into durable runtime state. The returned prompt is
// submitted by the TUI to begin execution after this method succeeds.
func (r *Runtime) ApproveOrchestratedGoal(ctx context.Context) (string, string, error) {
	if r == nil || r.Agent == nil || r.Plan == nil {
		return "", "", errors.New("runtime is unavailable")
	}
	r.orchestrationMu.Lock()
	defer r.orchestrationMu.Unlock()
	if r.GoalGraph != nil {
		return "", "", errors.New("an Orchestrated Goal graph is already attached")
	}
	proposal := r.orchestrationProposal
	if proposal == nil {
		return "", "", errors.New("no new Orchestrated Goal proposal is awaiting approval; use /orchestrate <goal>")
	}
	current, revision := r.Plan.Snapshot()
	if revision <= proposal.BaseRevision {
		return "", "", errors.New("the proposal turn did not create a new structured plan; refine the proposal before approving it")
	}
	spec, err := orchestratedSpec(current)
	if err != nil {
		return "", "", err
	}
	if goalgraph.HasIsolatedWriters(spec) {
		base, baseErr := r.Agent.GoalWriterBase(ctx)
		if baseErr != nil {
			return "", "", fmt.Errorf("proposal cannot start isolated candidates: %w", baseErr)
		}
		if baseErr := goalgraph.ValidateWriterBase(base); baseErr != nil {
			return "", "", fmt.Errorf("proposal cannot start isolated candidates: %w", baseErr)
		}
	}
	usage := providerUsageDelta(r.Agent.Usage(), proposal.BaseUsage)
	iterations := max(0, r.Agent.ProviderIterations()-proposal.BaseIterations)
	graph, err := createGoalGraph(ctx, current, r.Plan, r.Session, &goalAccountingSeed{
		Started: proposal.Started,
		Primary: goalgraph.WorkUsage{
			Iterations: iterations, InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
			CostUSD: usage.CostUSD, CostAvailable: usage.CostAvailable, CostEstimated: usage.CostEstimated,
		},
	})
	if err != nil {
		return "", "", err
	}
	if err := r.Agent.SetGoalGraph(graph); err != nil {
		return "", "", err
	}
	r.Agent.RequestGoalBoundaryCompaction()
	r.Registry.Add(goalgraph.RevisionTool{Graph: graph})
	r.Registry.Add(goalgraph.BlockTool{Graph: graph})
	r.GoalGraph = graph
	r.orchestrationProposal = nil
	r.logGoalGraphUpdates()
	status, _ := graph.Inspect(0)
	return status, OrchestratedExecutionPrompt(graph.Snapshot().Goal), nil
}

// ResumeOrchestratedGoal requires a fresh user action even when the session
// already carries a durable graph. It returns runnable=false for a recovered
// terminal graph so the caller can show its exact blocker/outcome without
// starting another provider turn.
func (r *Runtime) ResumeOrchestratedGoal(ctx context.Context) (status, prompt string, runnable bool, err error) {
	if r == nil || r.Agent == nil {
		return "", "", false, errors.New("runtime is unavailable")
	}
	r.orchestrationMu.Lock()
	defer r.orchestrationMu.Unlock()
	if r.GoalGraph != nil {
		requested, _, _ := r.GoalGraph.PauseState()
		if !requested {
			return "", "", false, errors.New("the attached Orchestrated Goal graph is not paused")
		}
		if err := r.GoalGraph.Resume(ctx); err != nil {
			return "", "", false, err
		}
		r.logGoalGraphUpdates()
		status, _ = r.GoalGraph.Inspect(0)
		return status, OrchestratedResumePrompt(r.GoalGraph.Snapshot().Goal), true, nil
	}
	graph, err := restoreGoalGraph(ctx, r.Session, r.goalStateToken)
	if err != nil {
		return "", "", false, err
	}
	if requested, _, _ := graph.PauseState(); requested {
		if err := graph.Resume(ctx); err != nil {
			return "", "", false, err
		}
	} else if outcome, _ := graph.Outcome(); outcome == "" {
		if err := graph.Activate(ctx); err != nil {
			return "", "", false, err
		}
	}
	if err := r.Agent.SetGoalGraph(graph); err != nil {
		return "", "", false, err
	}
	r.Registry.Add(goalgraph.RevisionTool{Graph: graph})
	r.Registry.Add(goalgraph.BlockTool{Graph: graph})
	r.GoalGraph = graph
	r.orchestrationProposal = nil
	r.logGoalGraphUpdates()
	status, _ = graph.Inspect(0)
	outcome, _ := graph.Outcome()
	return status, OrchestratedResumePrompt(graph.Snapshot().Goal), outcome == "", nil
}

// PauseOrchestratedGoal requests a cooperative durable pause. It never
// suspends an OS process or cancels the current iteration; the agent records
// the reached boundary before another provider/scheduler iteration starts.
func (r *Runtime) PauseOrchestratedGoal(ctx context.Context) (string, error) {
	if r == nil || r.Agent == nil {
		return "", errors.New("runtime is unavailable")
	}
	r.orchestrationMu.Lock()
	defer r.orchestrationMu.Unlock()
	if r.GoalGraph == nil {
		return "", errors.New("there is no attached Orchestrated Goal graph to pause")
	}
	if err := r.GoalGraph.RequestPause(ctx, "paused explicitly by the user"); err != nil {
		return "", err
	}
	r.logGoalGraphUpdates()
	return r.GoalGraph.Inspect(0)
}

// RetryOrchestratedNode reopens only a runtime-approved safe blocker. The
// graph rejects exhausted attempts and ambiguous interrupted mutations.
func (r *Runtime) RetryOrchestratedNode(ctx context.Context, nodeID int) (status, prompt string, runnable bool, err error) {
	if r == nil || r.Agent == nil {
		return "", "", false, errors.New("runtime is unavailable")
	}
	if nodeID <= 0 {
		return "", "", false, errors.New("orchestrated goal node id must be a positive integer")
	}
	r.orchestrationMu.Lock()
	defer r.orchestrationMu.Unlock()
	graph := r.GoalGraph
	restored := false
	if graph == nil {
		if r.Session == nil || len(r.Session.GoalGraphRaw) == 0 {
			return "", "", false, errors.New("there is no attached or saved Orchestrated Goal graph to retry")
		}
		graph, err = restoreGoalGraph(ctx, r.Session, r.goalStateToken)
		if err != nil {
			return "", "", false, err
		}
		restored = true
	}
	if err := graph.RetryNode(ctx, nodeID, "retry requested explicitly by the user"); err != nil {
		return "", "", false, err
	}
	if restored {
		if err := r.Agent.SetGoalGraph(graph); err != nil {
			return "", "", false, err
		}
		r.Registry.Add(goalgraph.RevisionTool{Graph: graph})
		r.Registry.Add(goalgraph.BlockTool{Graph: graph})
		r.GoalGraph = graph
		r.orchestrationProposal = nil
	}
	r.logGoalGraphUpdates()
	status, _ = graph.Inspect(nodeID)
	requested, _, _ := graph.PauseState()
	return status, OrchestratedRetryPrompt(graph.Snapshot().Goal, nodeID), !requested, nil
}

// CancelOrchestratedGoal cancels either the unapproved process-local proposal
// or the durable active graph. It does not undo completed actions.
func (r *Runtime) CancelOrchestratedGoal(ctx context.Context) (string, error) {
	if r == nil || r.Agent == nil {
		return "", errors.New("runtime is unavailable")
	}
	r.orchestrationMu.Lock()
	defer r.orchestrationMu.Unlock()
	if r.GoalGraph != nil {
		if outcome, _ := r.GoalGraph.Outcome(); outcome != "" {
			if err := r.archiveTerminalGoalGraphLocked(); err != nil {
				return "", err
			}
			return "Terminal Orchestrated Goal archived. Its transcript and evidence remain in the session log; this session is ready for /orchestrate <goal>.", nil
		}
		if err := r.GoalGraph.Cancel(ctx, "cancelled explicitly by the user"); err != nil {
			return "", err
		}
		r.logGoalGraphUpdates()
		status, _ := r.GoalGraph.Inspect(0)
		return status, nil
	}
	if r.orchestrationProposal != nil {
		previousPlanMode := r.orchestrationProposal.PreviousPlanMode
		r.orchestrationProposal = nil
		r.Agent.SetPlan(previousPlanMode)
		return "Orchestrated Goal proposal cancelled. The structured plan remains available in /tasks, but it cannot be approved without starting a new proposal.", nil
	}
	return "", errors.New("there is no active Orchestrated Goal proposal or graph")
}

func (r *Runtime) archiveSavedTerminalGoalGraphLocked() error {
	if r == nil || r.Session == nil || len(r.Session.GoalGraphRaw) == 0 {
		return nil
	}
	var snapshot goalgraph.Snapshot
	if err := json.Unmarshal(r.Session.GoalGraphRaw, &snapshot); err != nil {
		return fmt.Errorf("decode saved graph: %w", err)
	}
	graph, err := goalgraph.Restore(snapshot, goalgraph.Options{})
	if err != nil {
		return fmt.Errorf("inspect saved graph: %w", err)
	}
	if outcome, _ := graph.Outcome(); outcome == "" {
		return errors.New("this session has a saved active Orchestrated Goal graph; use /orchestrate resume or /new")
	}
	return r.archiveTerminalGoalGraphLocked()
}

// archiveTerminalGoalGraphLocked releases a terminal graph as the session's
// current graph while retaining every prior snapshot and transcript record.
// The caller must hold orchestrationMu.
func (r *Runtime) archiveTerminalGoalGraphLocked() error {
	if r == nil || r.Session == nil {
		return errors.New("Orchestrated Goal requires durable session persistence")
	}
	if r.GoalGraph != nil {
		if outcome, _ := r.GoalGraph.Outcome(); outcome == "" {
			return errors.New("cannot archive an active Orchestrated Goal graph")
		}
	}
	if err := r.Session.ClearGoalGraph(true); err != nil {
		return fmt.Errorf("archive terminal Orchestrated Goal: %w", err)
	}
	if r.Agent != nil {
		if err := r.Agent.SetGoalGraph(nil); err != nil {
			return err
		}
		r.Agent.SetPlan(false)
	}
	if r.Registry != nil {
		r.Registry.Remove(goalgraph.ReviseToolName)
		r.Registry.Remove(goalgraph.BlockToolName)
	}
	r.GoalGraph = nil
	r.orchestrationProposal = nil
	return nil
}

// OrchestratedGoalStatus exposes process-local proposal state or durable graph
// truth without activating a saved graph. A saved snapshot is inspected as
// data; `/orchestrate resume` remains the only user activation after restart.
func (r *Runtime) OrchestratedGoalStatus(nodeID int) (string, error) {
	if r == nil {
		return "", errors.New("runtime is unavailable")
	}
	r.orchestrationMu.Lock()
	defer r.orchestrationMu.Unlock()
	if r.GoalGraph != nil {
		return r.GoalGraph.Inspect(nodeID)
	}
	if r.orchestrationProposal != nil {
		if nodeID != 0 {
			return "", errors.New("node attempts do not exist until the proposal is approved")
		}
		current, revision := r.Plan.Snapshot()
		fresh := revision > r.orchestrationProposal.BaseRevision
		limits := goalgraph.DefaultLimits()
		var b strings.Builder
		b.WriteString("Experimental Orchestrated Goal proposal\n")
		fmt.Fprintf(&b, "Requested outcome: %s\n", r.orchestrationProposal.Goal)
		fmt.Fprintf(&b, "Proposal state: %s\n", map[bool]string{true: "awaiting explicit approval", false: "waiting for a new structured plan"}[fresh])
		fmt.Fprintf(&b, "Bounds: %d nodes · %d attempts/node · %d revisions\n", limits.MaxNodes, limits.MaxAttemptsPerNode, limits.MaxRevisions)
		fmt.Fprintf(&b, "Automatic reads: at most %d concurrent · %d starts · %d tokens · %ds wall bound\n", limits.MaxReadConcurrency, limits.MaxReadStarts, limits.MaxReadTokens, limits.MaxReadWallSeconds)
		fmt.Fprintf(&b, "Automatic isolated writers: one candidate wave · at most %d concurrent · %d starts\n", limits.MaxWriterConcurrency, limits.MaxWriterStarts)
		fmt.Fprintf(&b, "Aggregate envelope after approval: %d provider iterations · %d tokens · $%.2f when pricing is complete · %ds active wall\n", limits.MaxAggregateIterations, limits.MaxAggregateTokens, limits.MaxAggregateCostUSD, limits.MaxActiveWallSeconds)
		proposalUsage := providerUsageDelta(r.Agent.Usage(), r.orchestrationProposal.BaseUsage)
		proposalIterations := max(0, r.Agent.ProviderIterations()-r.orchestrationProposal.BaseIterations)
		proposalTokens := proposalUsage.InputTokens + proposalUsage.OutputTokens
		fmt.Fprintf(&b, "Proposal work to seed at approval: %d provider iterations · %d/%d tokens · %d remain before approval-boundary compaction\n", proposalIterations, proposalTokens, limits.MaxAggregateTokens, max(0, limits.MaxAggregateTokens-proposalTokens))
		b.WriteString("Execution: end-to-end graphs use one serial primary lane; independently ready approved read_only nodes may use at most two automatic readers. An explicitly candidate-only graph may instead use one bounded wave of terminal disjoint isolated_write nodes.\n")
		b.WriteString("Write scope: parent-workspace writes remain primary-only; retained candidates require explicit narrow scopes, a clean approval-time Git base, and ordinary dispatch/command permissions, and cannot unlock dependents.\n")
		b.WriteString("Authority: approval grants no tool, path, network, publication, or budget authority.\n")
		b.WriteString("Completion: every changed workspace state needs fresh machine-observed verification.\n")
		if current != nil {
			b.WriteString("\n" + current.Render())
		}
		if _, err := orchestratedSpec(current); err != nil {
			fmt.Fprintf(&b, "\nNot yet approvable: %s\n", err)
		} else if fresh {
			b.WriteString("\nReview the graph above, then run /orchestrate approve once to execute it.\n")
		}
		return b.String(), nil
	}
	if r.Session != nil && len(r.Session.GoalGraphRaw) > 0 {
		var snapshot goalgraph.Snapshot
		if err := json.Unmarshal(r.Session.GoalGraphRaw, &snapshot); err != nil {
			return "", fmt.Errorf("decode saved graph: %w", err)
		}
		graph, err := goalgraph.Restore(snapshot, goalgraph.Options{})
		if err != nil {
			return "", fmt.Errorf("inspect saved graph: %w", err)
		}
		status, err := graph.Inspect(nodeID)
		if err != nil {
			return "", err
		}
		return status + "\n\nSaved graph is inert. Run /orchestrate resume to explicitly reattach it.", nil
	}
	return "Orchestrated Goal is off. Use /orchestrate <goal> to create a read-only proposal; Standard mode remains the default.", nil
}

// OrchestratedGoalPhase is a compact presentation hint. It never activates or
// restores state and is safe for persistent TUI badges.
func (r *Runtime) OrchestratedGoalPhase() string {
	if r == nil {
		return ""
	}
	r.orchestrationMu.Lock()
	defer r.orchestrationMu.Unlock()
	if r.GoalGraph != nil {
		if outcome, _ := r.GoalGraph.Outcome(); outcome != "" {
			return string(outcome)
		}
		if requested, reached, _ := r.GoalGraph.PauseState(); requested {
			if reached {
				return "paused"
			}
			return "pausing"
		}
		return "running"
	}
	if r.orchestrationProposal != nil {
		return "proposal"
	}
	return ""
}

func (r *Runtime) detachGoalGraph() {
	if r == nil {
		return
	}
	r.orchestrationMu.Lock()
	defer r.orchestrationMu.Unlock()
	previousPlanMode := false
	if r.orchestrationProposal != nil {
		previousPlanMode = r.orchestrationProposal.PreviousPlanMode
	}
	if r.Agent != nil {
		_ = r.Agent.SetGoalGraph(nil)
		r.Agent.SetPlan(previousPlanMode)
	}
	if r.Registry != nil {
		r.Registry.Remove(goalgraph.ReviseToolName)
		r.Registry.Remove(goalgraph.BlockToolName)
	}
	r.GoalGraph = nil
	r.orchestrationProposal = nil
}

func (r *Runtime) logGoalGraphUpdates() {
	if r == nil || r.GoalGraph == nil {
		return
	}
	for _, update := range r.GoalGraph.DrainUpdates() {
		e := event.New(event.KindGoalGraphUpdate)
		e.Time = update.Time
		e.GoalGraph = &event.GoalGraphStatus{
			ID: update.GraphID, Generation: update.Generation, NodeID: update.NodeID,
			AttemptID: update.AttemptID, State: update.State, Reason: update.Reason,
			Ready: append([]int(nil), update.Ready...), Outcome: string(update.Outcome),
		}
		r.LogEvent(e)
	}
}

// attachTeam binds process-local delegated-agent observability to the active
// durable session. Stored queued/running states are restored as interrupted by
// Team.Restore and are never submitted back to the scheduler.
func attachTeam(team *agent.Team, sess *session.Session) {
	if team == nil {
		return
	}
	team.SetObserver(nil)
	if sess == nil {
		team.Reset()
		return
	}
	stored := sess.Delegates()
	restored := make([]agent.DelegateStatus, 0, len(stored))
	for _, status := range stored {
		restored = append(restored, agent.DelegateStatusFromEvent(status))
	}
	team.Restore(restored)
	persist := func(status agent.DelegateStatus) {
		payload := status.Event()
		update := event.New(event.KindDelegateUpdate)
		update.Delegate = &payload
		sess.AppendEvent(update)
	}
	team.SetObserver(persist)
	// Persist the one-way recovery transition once. Subsequent resumes see a
	// terminal interrupted record rather than repeatedly reinterpreting stale
	// queued/running state.
	for i, status := range team.Snapshot() {
		if i < len(restored) && status.Status != restored[i].Status {
			persist(status)
		}
	}
}

// SwitchSession loads another saved session into the running agent:
// conversation, plan, and persistence hooks all move over. The previous
// session file is closed; nothing about it is lost.
func (r *Runtime) SwitchSession(id string) error {
	if r.GoalGraph != nil {
		if outcome, _ := r.GoalGraph.Outcome(); outcome == "" {
			return fmt.Errorf("switching sessions while an Orchestrated Goal is active is not supported; cancel it first")
		}
	}
	if r.Sessions == nil {
		return fmt.Errorf("session persistence is unavailable")
	}
	sess, err := r.Sessions.Load(id)
	if err != nil {
		return err
	}
	r.detachGoalGraph()
	if r.Session != nil {
		r.Session.Close()
	}
	r.Session = sess
	if r.Artifacts != nil {
		r.Artifacts.Use(sess)
	}
	if r.Attachments != nil {
		r.Attachments.Use(sess)
	}
	r.Agent.SetMessages(sess.Active())
	r.Agent.SetUsage(sess.Usage())
	sess.FlushInterrupted()
	r.Agent.SetHooks(sess.AppendMessage, sess.AppendCompaction)
	r.Agent.SetPersistenceGuard(sess.Err)
	attachBoard(r.Plan, sess)
	attachTeam(r.Team, sess)
	r.alignChangeTurns()
	return nil
}

// NewSession starts a fresh session, leaving the previous one saved.
func (r *Runtime) NewSession() error {
	if r.GoalGraph != nil {
		if outcome, _ := r.GoalGraph.Outcome(); outcome == "" {
			return fmt.Errorf("starting a new session while an Orchestrated Goal is active is not supported; cancel it first")
		}
	}
	if r.Sessions == nil {
		return fmt.Errorf("session persistence is unavailable")
	}
	providerName, model := r.Agent.Selection()
	sess, err := r.Sessions.New(providerName, model)
	if err != nil {
		return err
	}
	r.detachGoalGraph()
	if r.Session != nil {
		r.Session.Close()
	}
	r.Session = sess
	if r.Artifacts != nil {
		r.Artifacts.Use(sess)
	}
	if r.Attachments != nil {
		r.Attachments.Use(sess)
	}
	r.Agent.Reset()
	r.Agent.SetHooks(sess.AppendMessage, sess.AppendCompaction)
	r.Agent.SetPersistenceGuard(sess.Err)
	attachBoard(r.Plan, sess)
	attachTeam(r.Team, sess)
	r.alignChangeTurns()
	return nil
}

// RewindSession creates and switches to a non-destructive branch ending at a
// completed turn. The source session and workspace remain unchanged.
func (r *Runtime) RewindSession(turn int) (sourceID, rewoundID string, err error) {
	if phase := r.OrchestratedGoalPhase(); phase != "" {
		return "", "", fmt.Errorf("rewinding during an Orchestrated Goal %s is not supported; cancel a proposal, or start /new after a graph reaches a terminal state", phase)
	}
	if r.Sessions == nil || r.Session == nil {
		return "", "", fmt.Errorf("session persistence is unavailable")
	}
	sourceID = r.Session.Meta.ID
	sess, err := r.Sessions.Rewind(sourceID, turn)
	if err != nil {
		return sourceID, "", err
	}
	r.Session.Close()
	r.Session = sess
	if r.Artifacts != nil {
		r.Artifacts.Use(sess)
	}
	if r.Attachments != nil {
		r.Attachments.Use(sess)
	}
	r.Agent.SetMessages(sess.Active())
	r.Agent.SetUsage(sess.Usage())
	r.Agent.SetHooks(sess.AppendMessage, sess.AppendCompaction)
	r.Agent.SetPersistenceGuard(sess.Err)
	attachBoard(r.Plan, sess)
	attachTeam(r.Team, sess)
	r.alignChangeTurns()
	return sourceID, sess.Meta.ID, nil
}

// CheckpointRestore reports what a coupled restore moved: the conversation
// branch it created, and the workspace files it reversed.
type CheckpointRestore struct {
	SourceID  string
	SessionID string
	Turn      int
	Files     []string
	Mutations int
}

// RestoreCheckpoint returns the conversation and the workspace together to a
// completed turn: it creates the same non-destructive conversation branch
// `/rewind` does, and reverses every file mutation this process recorded after
// that turn.
//
// The workspace is verified before the conversation branches, so the failure
// this is guarded against — a file edited outside Collomia since the checkpoint
// — leaves both halves untouched and names the files. Command, network, and
// other external side effects are never reversed; only tracked file mutations
// are, and only those recorded by this process.
func (r *Runtime) RestoreCheckpoint(turn int) (CheckpointRestore, error) {
	if r.Sessions == nil || r.Session == nil {
		return CheckpointRestore{}, fmt.Errorf("session persistence is unavailable")
	}
	if r.Changes == nil {
		return CheckpointRestore{}, fmt.Errorf("change tracking is unavailable, so the workspace cannot be restored; /rewind branches the conversation alone")
	}
	result := CheckpointRestore{SourceID: r.Session.Meta.ID, Turn: turn}
	// Prove the workspace half can succeed before anything moves. A drifted
	// file discovered after the branch existed would leave a conversation
	// describing a workspace that was never restored.
	if err := r.Changes.VerifyRestore(turn); err != nil {
		return result, err
	}
	sourceID, sessionID, err := r.RewindSession(turn)
	result.SourceID = sourceID
	if err != nil {
		return result, err
	}
	result.SessionID = sessionID
	restored, err := r.Changes.RestoreTo(turn)
	result.Files = restored.Files
	result.Mutations = restored.Mutations
	if err != nil {
		return result, fmt.Errorf("the conversation branched to %s but the workspace was not fully restored: %w", sessionID, err)
	}
	return result, nil
}

// askUserTool lets the model pause for a concise typed answer without
// ending the run. Declining (empty answer) is reported, not fabricated.
func askUserTool(ask func(ctx context.Context, question string, options []string) (string, error)) tools.Tool {
	return tools.Function{
		Def: provider.ToolDefinition{
			Name:        "ask_user",
			Description: "Ask the user one concise question when a decision or missing value blocks progress (e.g. choosing between approaches, a credential name, an ambiguous requirement). Provide options when the answer is a choice. Use sparingly; never ask what you can determine yourself.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"},"options":{"type":"array","items":{"type":"string"},"maxItems":6}},"required":["question"],"additionalProperties":false}`),
		},
		Action: tools.Action{Risk: tools.RiskRead, Summary: "ask the user a question"},
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var input struct {
				Question string   `json:"question"`
				Options  []string `json:"options"`
			}
			if err := json.Unmarshal(raw, &input); err != nil {
				return "", err
			}
			answer, err := ask(ctx, input.Question, input.Options)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(answer) == "" {
				return "The user declined to answer. Proceed with your best judgment and say what you assumed.", nil
			}
			return "User answered: " + answer, nil
		},
	}
}

func (r *Runtime) Close() {
	sessionID := ""
	if r.Session != nil {
		sessionID = r.Session.Meta.ID
	}
	r.Hooks.Fire(context.Background(), hooks.Payload{Event: "session_end", Workspace: r.Workspace, Subject: "session_end", Detail: map[string]any{"session_id": sessionID}})
	if r.Team != nil {
		// Queued and running delegated tasks never intentionally outlive the
		// runtime; cancellation is requested before process/session teardown.
		r.Team.StopAll()
	}
	if r.Processes != nil {
		// Background processes never outlive the session.
		r.Processes.StopAll()
	}
	if r.MCP != nil {
		r.MCP.Close()
	}
	if r.Session != nil {
		r.Session.Close()
	}
	logging.Close(r.Logger)
}
func (r *Runtime) Select(providerName, model string) error {
	name, p, resolved, err := r.Config.Selected(providerName, model)
	if err != nil {
		return err
	}
	if profile, ok := r.Config.Agents[r.ActiveAgent]; ok && profile.Reasoning != nil {
		reasoning := *profile.Reasoning
		p.Reasoning = &reasoning
	}
	client, err := provider.New(name, p, resolved)
	if err != nil {
		return err
	}
	client = provider.WithResilience(client)
	r.Agent.SetProvider(name, resolved, p, client)
	return nil
}

// SelectAgent applies a named primary profile without resetting conversation
// context or cumulative usage. "default", "none", and an empty name restore
// the ordinary unprofiled primary agent.
func (r *Runtime) SelectAgent(name string) error {
	name = strings.TrimSpace(name)
	if name == "default" || name == "none" {
		name = ""
	}
	profile, err := r.Config.PrimaryAgent(name)
	if err != nil {
		return err
	}
	providerName, _ := r.Agent.Selection()
	_, p, model, err := r.Config.Selected(providerName, "")
	if err != nil {
		return err
	}
	if profile.Model != "" {
		model = profile.Model
	}
	if profile.Reasoning != nil {
		reasoning := *profile.Reasoning
		p.Reasoning = &reasoning
	}
	client, err := provider.New(providerName, p, model)
	if err != nil {
		return err
	}
	if err := r.Permissions.SetProfile(profile.Permissions); err != nil {
		return err
	}
	catalog := r.Skills
	if len(profile.Skills) > 0 {
		catalog = catalog.Restrict(profile.Skills)
	}
	maxIterations := r.Config.Options.MaxIterations
	if profile.MaxIterations > 0 {
		maxIterations = profile.MaxIterations
	}
	r.Agent.SetProvider(providerName, model, p, provider.WithResilience(client))
	r.Agent.ApplyProfile(agent.ProfileSettings{
		Name: name, Instructions: profile.Instructions, Catalog: catalog,
		Tools: profile.Tools, DisabledTools: r.Config.Options.DisabledTools, Skills: profile.Skills,
		MaxIterations: maxIterations, TokenBudget: profile.TokenBudget, CostBudgetUSD: profile.CostBudgetUSD,
	})
	r.ActiveAgent = name
	return nil
}

func (r *Runtime) PrimaryAgentNames() []string {
	names := []string{"default"}
	for name, profile := range r.Config.Agents {
		if name != "default" && name != "none" && appconfig.AgentAvailableFor(profile, "primary") {
			names = append(names, name)
		}
	}
	sort.Strings(names[1:])
	return names
}
func (r *Runtime) Summary() string {
	p, m := r.Agent.Selection()
	profile, reasoning, tokenBudget, costBudget := r.Agent.Profile()
	if profile == "" {
		profile = "default"
	}
	if reasoning == "" {
		reasoning = "provider default"
	}
	budgets := "none"
	if tokenBudget > 0 || costBudget > 0 {
		budgets = fmt.Sprintf("%d tokens / $%.6f", tokenBudget, costBudget)
	}
	return fmt.Sprintf("workspace: %s\nagent: %s\nprovider: %s\nmodel: %s\nreasoning: %s\nbudgets: %s\nprovider health: %s\ncapabilities: %s\nautonomy: %s\nsandbox: %s\nplanning: %t\nconfig: %s", r.Workspace, profile, p, m, reasoning, budgets, r.Agent.ProviderHealth().Summary(), r.Agent.Capabilities().CompactSummary(), r.Permissions.Mode(), r.SandboxSummary(), r.Agent.Plan(), r.Config.Source)
}

// SandboxSummary reports the effective command containment stance without
// changing external state. It is shared by doctor/status and the interactive
// Session tab.
func (r *Runtime) SandboxSummary() string {
	mode := sandbox.Mode(r.Config.Permissions.Sandbox)
	if mode == "" {
		mode = sandbox.ModeOff
	}
	if mode == sandbox.ModeOff {
		return "off (commands use normal user privileges)"
	}
	backend := sandbox.ForPlatform()
	if err := backend.Available(); err != nil {
		return fmt.Sprintf("%s; unavailable: %v", mode, err)
	}
	policy := sandbox.Policy{
		WorkspaceRoot:  r.Workspace,
		AllowNetwork:   r.Config.Permissions.SandboxAllowNetwork,
		ConstrainReads: !r.Config.Permissions.SandboxAllowReadOutsideWorkspace,
	}
	network := "denied"
	if policy.AllowNetwork {
		network = "allowed"
	}
	caps := backend.Capabilities()
	reads := caps.ReadPolicySummary(policy)
	detail := fmt.Sprintf("%s; %s; command user-data reads %s; command network %s", mode, backend.Name(), reads, network)
	if missing := caps.Missing(policy); len(missing) > 0 {
		detail += "; degraded: missing " + strings.Join(missing, " and ")
	}
	return detail
}

// PersistenceError reports a durable-session write failure, if one occurred.
// Ephemeral runs and runtimes without a session have no persistence error.
func (r *Runtime) PersistenceError() error {
	if r == nil || r.Session == nil {
		return nil
	}
	return r.Session.Err()
}
