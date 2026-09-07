package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/provider"
)

// InspectEnvironmentTool resolves executable names without reading their bytes
// or running them. It is suitable for planning and read-only investigations.
type InspectEnvironmentTool struct{}

func (InspectEnvironmentTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "inspect_environment", Description: "Look up executable names (for example node, npm, uv) in Collo's inherited PATH without running commands or reading executable contents. Reports discovery, not versions or sandbox execution success. Use during planning instead of guessing installation paths or asking the user to probe installed tools. Actual version/compatibility checks belong to run_command in primary execution; read_only workers cannot run commands.", InputSchema: schema(`{"type":"object","properties":{"executables":{"type":"array","minItems":1,"maxItems":16,"items":{"type":"string","minLength":1,"maxLength":128}}},"required":["executables"],"additionalProperties":false}`)}
}

func environmentNames(raw json.RawMessage) ([]string, error) {
	var args struct {
		Executables []string `json:"executables"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	if len(args.Executables) < 1 || len(args.Executables) > 16 {
		return nil, errors.New("executables must contain 1–16 names")
	}
	for _, name := range args.Executables {
		if len(name) == 0 || len(name) > 128 || strings.TrimSpace(name) != name || name == "." || name == ".." {
			return nil, errors.New("executables must be plain executable names, not paths or commands")
		}
		for _, c := range name {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("._+-", c)) {
				return nil, errors.New("executables must be plain executable names, not paths or commands")
			}
		}
	}
	return args.Executables, nil
}

func (InspectEnvironmentTool) Assess(raw json.RawMessage) (Action, error) {
	names, err := environmentNames(raw)
	return Action{Risk: RiskRead, Summary: "locate executables: " + strings.Join(names, ", ")}, err
}

func (InspectEnvironmentTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	names, err := environmentNames(raw)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("Executable discovery in Collo's inherited PATH (no commands executed):\n")
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		path, err := exec.LookPath(name)
		if err != nil {
			fmt.Fprintf(&b, "- %s: unavailable in this process PATH (not proof it is uninstalled)\n", name)
		} else {
			fmt.Fprintf(&b, "- %s: %s\n", name, path)
		}
	}
	b.WriteString("Discovery does not establish a version, compatibility, or sandbox access. Run a necessary version check through run_command during primary execution; fold it into the node that uses the tool, not a command-dependent read_only node. Shell aliases/functions are not executables on PATH.\n")
	return b.String(), nil
}
