package tools

import (
	"fmt"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/diffmodel"
	"github.com/robert-mcdermott/collomia/internal/index"
	"github.com/robert-mcdermott/collomia/internal/sandbox"
)

func Builtins(workspace string, cfg appconfig.Config) (*Registry, *diffmodel.Tracker, *ProcessManager, error) {
	guard, err := NewPathGuard(workspace, cfg.Permissions.AllowOutsideWorkspace)
	if err != nil {
		return nil, nil, nil, err
	}
	command, err := NewRunCommandTool(guard.Workspace, cfg.Permissions.DeniedCommands, cfg.Options.MaxToolOutputBytes)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("command policy: %w", err)
	}
	if cfg.Permissions.Sandbox != "" {
		command.SandboxMode = sandbox.Mode(cfg.Permissions.Sandbox)
	}
	command.AllowNetwork = cfg.Permissions.SandboxAllowNetwork
	command.AllowReadOutsideWorkspace = cfg.Permissions.SandboxAllowReadOutsideWorkspace
	command.ExtraReadableRoots = append([]string(nil), cfg.Permissions.SandboxReadableRoots...)
	command.ExtraWritableRoots = append([]string(nil), cfg.Permissions.SandboxWritableRoots...)
	// Sandboxed configurations default to the minimal environment; an
	// explicit command_env setting always wins.
	sandboxed := command.SandboxMode == sandbox.ModeAuto || command.SandboxMode == sandbox.ModeRequire
	command.MinimalEnv = cfg.Permissions.CommandEnv == "minimal" || (cfg.Permissions.CommandEnv == "" && sandboxed)
	tracker := diffmodel.NewTracker(guard.Workspace)
	procs := NewProcessManager()
	registry := NewRegistry(
		ReadFileTool{Guard: guard}, ListFilesTool{Guard: guard}, SearchFilesTool{Guard: guard},
		WriteFileTool{Guard: guard, Tracker: tracker}, EditFileTool{Guard: guard, Tracker: tracker},
		ApplyPatchTool{Guard: guard, Tracker: tracker}, command,
		GitStatusTool{Workspace: guard.Workspace}, GitDiffTool{Workspace: guard.Workspace},
		GitLogTool{Workspace: guard.Workspace}, GitBlameTool{Workspace: guard.Workspace},
		DetectVerificationTool{Workspace: guard.Workspace},
		StartProcessTool{Manager: procs, Runner: command}, ListProcessesTool{Manager: procs},
		ProcessOutputTool{Manager: procs}, StopProcessTool{Manager: procs},
		SearchSymbolsTool{Index: index.New(guard.Workspace)},
		DiagnosticsTool{Guard: guard, Servers: cfg.LSP},
	)
	return registry, tracker, procs, nil
}
