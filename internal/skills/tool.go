package skills

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

func Tool(catalog Catalog) tools.Tool {
	return tools.Function{
		Def:    provider.ToolDefinition{Name: "load_skill", Description: "Load the complete instructions for a discovered skill when its description matches the current task. Do not load unrelated skills.", InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}`)},
		Action: tools.Action{Risk: tools.RiskRead, Summary: "load skill instructions"},
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var a struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return "", err
			}
			content, err := catalog.Load(a.Name)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Skill %s:\n\n%s", a.Name, content), nil
		},
	}
}
