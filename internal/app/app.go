package app

import (
	"context"
	"encoding/json"
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
	Team        *agent.Team
	Processes   *tools.ProcessManager
	Warnings    []error
	Hooks       *hooks.Runner
	// Steering carries guidance typed while the primary agent is mid-turn.
	// It lives on the runtime rather than in the TUI because the agent is
	// constructed here and every surface — TUI, browser terminal, a future
	// local service API — needs the same one queue.
	Steering *agent.SteeringQueue
	// ActiveAgent is the visible named primary profile. Empty means the
	// ordinary unprofiled primary agent.
	ActiveAgent string
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
	Plan, Debug, Ephemeral                      bool
	// Resume loads an existing session ID; Continue resumes the most
	// recently updated session. Otherwise a new session is created.
	Resume   string
	Continue bool
	Approver permission.Approver
	// Asker lets the agent pause and ask the user a typed question. When
	// nil (headless), the ask_user tool is not registered.
	Asker func(ctx context.Context, question string, options []string) (string, error)
}

func New(ctx context.Context, opts Options) (*Runtime, error) {
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
		warnings = append(warnings, fmt.Errorf("audit ledger unavailable: %w", ledgerErr))
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
	// Structured plan artifact, maintained by the agent via update_plan and
	// persisted with the session.
	board := plan.NewBoard()
	registry.Add(plan.Tool(board))
	if sess != nil {
		attachBoard(board, sess)
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
	agentOptions := agent.Options{Client: client, ProviderName: providerName, Model: model, ProviderConfig: p, Workspace: workspace, Registry: registry, Permissions: permissions, Catalog: activeCatalog, ProjectInstructions: instructions, MaxIterations: maxIterations, MaxToolOutput: cfg.Options.MaxToolOutputBytes, TokenBudget: profile.TokenBudget, CostBudgetUSD: profile.CostBudgetUSD, DisabledTools: cfg.Options.DisabledTools, PlanMode: opts.Plan, Hooks: lifecycle, AuditRedact: redactor.Redact, Artifacts: artifactSink, Attachments: attachments, PinnedContext: func() string {
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
	sessionID := ""
	if sess != nil {
		sessionID = sess.Meta.ID
	}
	lifecycle.Fire(ctx, hooks.Payload{Event: "session_start", Workspace: workspace, Subject: "session_start", Detail: map[string]any{"session_id": sessionID, "provider": providerName, "model": model}})
	runtime := &Runtime{Workspace: workspace, Config: cfg, Agent: agentRuntime, Registry: registry, Permissions: permissions, Skills: catalog, MCP: mcpManager, Redactor: redactor, Logger: logger, LogPath: logPath, Sessions: store, Session: sess, Artifacts: artifacts, Attachments: attachments, Changes: tracker, Plan: board, Team: team, Processes: processes, Warnings: warnings, Hooks: lifecycle, ActiveAgent: activeAgent, Steering: steering}
	runtime.alignChangeTurns()
	runtime.addReviewedIntegrationTools()
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
	if r.Sessions == nil {
		return fmt.Errorf("session persistence is unavailable")
	}
	sess, err := r.Sessions.Load(id)
	if err != nil {
		return err
	}
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
	if r.Sessions == nil {
		return fmt.Errorf("session persistence is unavailable")
	}
	providerName, model := r.Agent.Selection()
	sess, err := r.Sessions.New(providerName, model)
	if err != nil {
		return err
	}
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
