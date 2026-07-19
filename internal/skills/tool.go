package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

func Tool(catalog Catalog) tools.Tool {
	return tools.Function{
		Def:    provider.ToolDefinition{Name: "load_skill", Description: "Load the complete instructions for a discovered skill when its description matches the current task. Skills may bundle scripts (run with run_command), references (read with read_file), and assets. Do not load unrelated skills.", InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}`)},
		Action: tools.Action{Risk: tools.RiskRead, Summary: "load skill instructions"},
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var a struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return "", err
			}
			skill, content, err := catalog.Load(a.Name)
			if err != nil {
				return "", err
			}
			return renderLoaded(skill, content), nil
		},
	}
}

// renderLoaded formats a loaded skill: the SKILL.md content followed by a
// map of the bundled files so the model knows what supporting material
// exists and how to use it without guessing at paths.
func renderLoaded(skill Skill, content string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Skill %s:\n\n%s", skill.Name, content)
	if skill.BundleCount() > 0 {
		fmt.Fprintf(&b, "\n\n---\nThis skill bundles supporting files under %s:\n", skill.Dir)
		for _, group := range []struct {
			label string
			files []string
		}{{"scripts (execute with run_command; normal permission rules apply)", skill.Scripts}, {"references (read with read_file when the instructions call for them)", skill.References}, {"assets (templates and materials for output)", skill.Assets}} {
			if len(group.files) == 0 {
				continue
			}
			fmt.Fprintf(&b, "%s:\n", group.label)
			for _, f := range group.files {
				fmt.Fprintf(&b, "  %s\n", f)
			}
		}
	}
	if len(skill.AllowedTools) > 0 {
		fmt.Fprintf(&b, "\nThe skill author expects it to need only these tools: %s.\n", strings.Join(skill.AllowedTools, ", "))
	}
	return b.String()
}
