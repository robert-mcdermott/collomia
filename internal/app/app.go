package app

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/robert-mcdermott/collomia/internal/agent"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	mcpclient "github.com/robert-mcdermott/collomia/internal/mcp"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/provider"
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
	Warnings    []error
}

type Options struct {
	Workspace, Provider, Model, Autonomy string
	Plan                                 bool
	Approver                             permission.Approver
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
	registry, err := tools.Builtins(workspace, cfg)
	if err != nil {
		return nil, err
	}
	catalog, err := skills.Discover(workspace)
	if err != nil {
		return nil, err
	}
	registry.Add(skills.Tool(catalog))
	instructions, err := skills.ProjectInstructions(workspace)
	if err != nil {
		return nil, err
	}
	permissions := permission.New(cfg.Permissions, opts.Approver)
	mcpManager, warnings := mcpclient.ConnectAll(ctx, cfg.MCP, registry)
	agentRuntime := agent.New(agent.Options{Client: client, ProviderName: providerName, Model: model, ProviderConfig: p, Workspace: workspace, Registry: registry, Permissions: permissions, Catalog: catalog, ProjectInstructions: instructions, MaxIterations: cfg.Options.MaxIterations, MaxToolOutput: cfg.Options.MaxToolOutputBytes, DisabledTools: cfg.Options.DisabledTools, PlanMode: opts.Plan})
	agentRuntime.AddDelegationTool()
	return &Runtime{Workspace: workspace, Config: cfg, Agent: agentRuntime, Registry: registry, Permissions: permissions, Skills: catalog, MCP: mcpManager, Warnings: warnings}, nil
}

func (r *Runtime) Close() {
	if r.MCP != nil {
		r.MCP.Close()
	}
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
