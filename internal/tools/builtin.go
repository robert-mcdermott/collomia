package tools

import (
	"fmt"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

func Builtins(workspace string, cfg appconfig.Config) (*Registry, error) {
	guard, err := NewPathGuard(workspace, cfg.Permissions.AllowOutsideWorkspace)
	if err != nil {
		return nil, err
	}
	command, err := NewRunCommandTool(guard.Workspace, cfg.Permissions.DeniedCommands, cfg.Options.MaxToolOutputBytes)
	if err != nil {
		return nil, fmt.Errorf("command policy: %w", err)
	}
	registry := NewRegistry(
		ReadFileTool{Guard: guard}, ListFilesTool{Guard: guard}, SearchFilesTool{Guard: guard},
		WriteFileTool{Guard: guard}, EditFileTool{Guard: guard}, *command,
	)
	return registry, nil
}
