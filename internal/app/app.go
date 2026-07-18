package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/agent"
	"github.com/robert-mcdermott/collomia/internal/audit"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/diffmodel"
	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/logging"
	mcpclient "github.com/robert-mcdermott/collomia/internal/mcp"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/redact"
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
	Changes     *diffmodel.Tracker
	Plan        *plan.Board
	Warnings    []error
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
	r.Logger.Debug("event", "kind", string(e.Kind), "tool", tool, "error", errText)
	if r.Session != nil {
		r.Session.AppendEvent(e)
	}
}

// NewRedactor collects every secret the configuration knows about so logs,
// events, and previews can scrub them.
func NewRedactor(cfg appconfig.Config) *redact.Redactor {
	r := redact.New()
	for _, p := range cfg.Providers {
		r.AddSecret(p.APIKey)
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
	Workspace, Provider, Model, Autonomy string
	Plan, Debug                          bool
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
	providerName, p, model, err := cfg.Selected(opts.Provider, opts.Model)
	if err != nil {
		return nil, err
	}
	client, err := provider.New(providerName, p, model)
	if err != nil {
		return nil, err
	}
	registry, tracker, err := tools.Builtins(workspace, cfg)
	if err != nil {
		return nil, err
	}
	catalog, err := skills.Discover(workspace, cfg.ProjectTrusted)
	if err != nil {
		return nil, err
	}
	registry.Add(skills.Tool(catalog))
	var instructions string
	if cfg.ProjectTrusted {
		instructions, err = skills.ProjectInstructions(workspace)
		if err != nil {
			return nil, err
		}
	}
	permissions := permission.New(cfg.Permissions, opts.Approver)
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
	mcpManager, warnings := mcpclient.ConnectAll(ctx, cfg.MCP, registry)
	if ledgerErr != nil {
		warnings = append(warnings, fmt.Errorf("audit ledger unavailable: %w", ledgerErr))
	}
	if !cfg.ProjectTrusted {
		warnings = append(warnings, fmt.Errorf("workspace is not trusted: project configuration, skills, and instructions were ignored; run `collo trust` after reviewing %s", appconfig.ProjectFile))
	}
	// Durable session: create, resume by ID, or continue the latest.
	store, storeErr := session.Open(workspace)
	var sess *session.Session
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
	// Structured plan artifact, maintained by the agent via update_plan and
	// persisted with the session.
	board := plan.NewBoard()
	registry.Add(plan.Tool(board))
	if sess != nil {
		attachBoard(board, sess)
	}
	if opts.Asker != nil {
		registry.Add(askUserTool(opts.Asker))
	}
	agentOptions := agent.Options{Client: client, ProviderName: providerName, Model: model, ProviderConfig: p, Workspace: workspace, Registry: registry, Permissions: permissions, Catalog: catalog, ProjectInstructions: instructions, MaxIterations: cfg.Options.MaxIterations, MaxToolOutput: cfg.Options.MaxToolOutputBytes, DisabledTools: cfg.Options.DisabledTools, PlanMode: opts.Plan}
	if sess != nil {
		agentOptions.OnMessage = sess.AppendMessage
		agentOptions.OnCompaction = sess.AppendCompaction
	}
	agentRuntime := agent.New(agentOptions)
	agentRuntime.AddDelegationTool()
	if sess != nil && (opts.Resume != "" || opts.Continue) {
		agentRuntime.SetMessages(sess.Active())
		sess.FlushInterrupted()
	}
	for _, warning := range warnings {
		logger.Warn("startup warning", "warning", warning.Error())
	}
	return &Runtime{Workspace: workspace, Config: cfg, Agent: agentRuntime, Registry: registry, Permissions: permissions, Skills: catalog, MCP: mcpManager, Redactor: redactor, Logger: logger, LogPath: logPath, Sessions: store, Session: sess, Changes: tracker, Plan: board, Warnings: warnings}, nil
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
	r.Agent.SetMessages(sess.Active())
	sess.FlushInterrupted()
	r.Agent.SetHooks(sess.AppendMessage, sess.AppendCompaction)
	attachBoard(r.Plan, sess)
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
	r.Agent.Clear()
	r.Agent.SetHooks(sess.AppendMessage, sess.AppendCompaction)
	attachBoard(r.Plan, sess)
	return nil
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
	if r.MCP != nil {
		r.MCP.Close()
	}
	if r.Session != nil {
		r.Session.Close()
	}
	logging.Close(r.Logger)
}
func (r *Runtime) Select(providerName, model string) error {
	_, p, resolved, err := r.Config.Selected(providerName, model)
	if err != nil {
		return err
	}
	client, err := provider.New(providerName, p, resolved)
	if err != nil {
		return err
	}
	r.Agent.SetProvider(providerName, resolved, p, client)
	return nil
}
func (r *Runtime) Summary() string {
	p, m := r.Agent.Selection()
	return fmt.Sprintf("workspace: %s\nprovider: %s\nmodel: %s\nautonomy: %s\nplanning: %t\nconfig: %s", r.Workspace, p, m, r.Permissions.Mode(), r.Agent.Plan(), r.Config.Source)
}
