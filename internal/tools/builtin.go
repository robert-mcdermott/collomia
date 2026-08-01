package tools

import (
	"fmt"
	"strings"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/diffmodel"
	"github.com/robert-mcdermott/collomia/internal/egress"
	"github.com/robert-mcdermott/collomia/internal/index"
	"github.com/robert-mcdermott/collomia/internal/sandbox"
	"github.com/robert-mcdermott/collomia/internal/web"
)

// ConfiguredRunCommandTool builds the command runner for a workspace from the
// effective configuration.
//
// Every caller that needs a command runner must come through here rather than
// setting the containment fields by hand. Delegated verification once built
// its own copy, and a copy is how a containment field ends up applied in the
// primary session and silently absent everywhere else — the same defect shape
// that let the host matcher ship inert. Adding a field to RunCommandTool
// should require changing one function, not finding every caller.
func ConfiguredRunCommandTool(workspace string, cfg appconfig.Config, maxOutput int) (*RunCommandTool, error) {
	command, err := NewRunCommandTool(workspace, cfg.Permissions.DeniedCommands, maxOutput)
	if err != nil {
		return nil, err
	}
	if cfg.Permissions.Sandbox != "" {
		command.SandboxMode = sandbox.Mode(cfg.Permissions.Sandbox)
	}
	command.AllowNetwork = cfg.Permissions.SandboxAllowNetwork
	// The broker's allowlist is derived from the permission rules rather than
	// configured separately, so the hosts a rule allows and the hosts a
	// sandboxed command can reach are the same list by construction.
	command.EgressScoped = strings.EqualFold(strings.TrimSpace(cfg.Permissions.SandboxEgress), appconfig.SandboxEgressScoped)
	command.EgressAllowlist = egress.FromRules(cfg.Permissions.Rules)
	command.AllowReadOutsideWorkspace = cfg.Permissions.SandboxAllowReadOutsideWorkspace
	command.ExtraReadableRoots = append([]string(nil), cfg.Permissions.SandboxReadableRoots...)
	command.ExtraWritableRoots = append([]string(nil), cfg.Permissions.SandboxWritableRoots...)
	// Sandboxed configurations default to the minimal environment; an
	// explicit command_env setting always wins.
	sandboxed := command.SandboxMode == sandbox.ModeAuto || command.SandboxMode == sandbox.ModeRequire
	command.MinimalEnv = cfg.Permissions.CommandEnv == "minimal" || (cfg.Permissions.CommandEnv == "" && sandboxed)
	return command, nil
}

func Builtins(workspace string, cfg appconfig.Config) (*Registry, *diffmodel.Tracker, *ProcessManager, error) {
	guard, err := NewPathGuard(workspace, cfg.Permissions.AllowOutsideWorkspace)
	if err != nil {
		return nil, nil, nil, err
	}
	command, err := ConfiguredRunCommandTool(guard.Workspace, cfg, cfg.Options.MaxToolOutputBytes)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("command policy: %w", err)
	}
	tracker := diffmodel.NewTracker(guard.Workspace)
	procs := NewProcessManager()
	// One client is shared by both web tools: its transport, bounds, and
	// public-internet address guard are the capability, and a second
	// construction site is where one of them would go missing.
	webClient := web.New(web.Options{})
	registry := NewRegistry(
		ReadFileTool{Guard: guard}, ListFilesTool{Guard: guard}, SearchFilesTool{Guard: guard},
		WriteFileTool{Guard: guard, Tracker: tracker}, EditFileTool{Guard: guard, Tracker: tracker},
		ApplyPatchTool{Guard: guard, Tracker: tracker}, command,
		GitStatusTool{Workspace: guard.Workspace}, GitDiffTool{Workspace: guard.Workspace},
		GitLogTool{Workspace: guard.Workspace}, GitBlameTool{Workspace: guard.Workspace},
		GitCommitTool{Guard: guard}, GitBranchTool{Guard: guard},
		DetectVerificationTool{Workspace: guard.Workspace},
		StartProcessTool{Manager: procs, Runner: command}, ListProcessesTool{Manager: procs},
		ProcessOutputTool{Manager: procs}, StopProcessTool{Manager: procs},
		SearchSymbolsTool{Index: index.New(guard.Workspace)},
		DiagnosticsTool{Guard: guard, Servers: cfg.LSP},
		FindDefinitionTool{Guard: guard, Servers: cfg.LSP},
		FindReferencesTool{Guard: guard, Servers: cfg.LSP},
		FormatFileTool{Guard: guard, Servers: cfg.LSP, Tracker: tracker},
		WebSearchTool{Client: webClient}, WebFetchTool{Client: webClient},
	)
	return registry, tracker, procs, nil
}
