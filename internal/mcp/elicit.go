package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// elicitSchema is the flat object schema MCP form elicitation allows (the
// spec forbids nesting).
type elicitSchema struct {
	Properties map[string]elicitField `json:"properties"`
	Required   []string               `json:"required"`
}

type elicitField struct {
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Enum        []string `json:"enum"`
}

// elicit answers a server's elicitation/create request by asking the user
// typed questions through the TUI's ask flow. The user declining or
// cancelling any question declines the whole request — sensitive input never
// defaults to acceptance. URL-mode elicitation is declined outright: opening
// third-party URLs on a server's behalf is not something Collomia does.
func (m *Manager) elicit(ctx context.Context, server string, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	if req == nil || req.Params == nil {
		return &mcp.ElicitResult{Action: "decline"}, nil
	}
	params := req.Params
	if params.URL != "" || params.Mode == "url" {
		return &mcp.ElicitResult{Action: "decline"}, nil
	}
	preface := fmt.Sprintf("MCP server %s asks: %s", server, params.Message)
	schema := parseElicitSchema(params.RequestedSchema)
	if len(schema.Properties) == 0 {
		// A bare confirmation without form fields.
		answer, err := m.opts.Asker(ctx, preface, []string{"accept", "decline"})
		if err != nil {
			return &mcp.ElicitResult{Action: "cancel"}, nil
		}
		if strings.EqualFold(strings.TrimSpace(answer), "accept") {
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{}}, nil
		}
		return &mcp.ElicitResult{Action: "decline"}, nil
	}
	required := map[string]bool{}
	for _, name := range schema.Required {
		required[name] = true
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	content := map[string]any{}
	for i, name := range names {
		field := schema.Properties[name]
		label := name
		if field.Title != "" {
			label = field.Title + " (" + name + ")"
		}
		question := fmt.Sprintf("%s — %s", preface, label)
		if field.Description != "" {
			question += ": " + field.Description
		}
		if !required[name] {
			question += " (optional; empty answer skips it)"
		}
		if i == 0 {
			question += " [esc declines the whole request]"
		}
		var options []string
		switch {
		case len(field.Enum) > 0:
			options = field.Enum
		case field.Type == "boolean":
			options = []string{"true", "false"}
		}
		answer, err := m.opts.Asker(ctx, question, options)
		if err != nil {
			return &mcp.ElicitResult{Action: "decline"}, nil
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			if required[name] {
				return &mcp.ElicitResult{Action: "decline"}, nil
			}
			continue
		}
		value, convErr := coerceElicitValue(field.Type, answer)
		if convErr != nil {
			return &mcp.ElicitResult{Action: "decline"}, nil
		}
		content[name] = value
	}
	return &mcp.ElicitResult{Action: "accept", Content: content}, nil
}

func parseElicitSchema(raw any) elicitSchema {
	var schema elicitSchema
	if raw == nil {
		return schema
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return schema
	}
	_ = json.Unmarshal(data, &schema)
	return schema
}

func coerceElicitValue(fieldType, answer string) (any, error) {
	switch fieldType {
	case "number":
		return strconv.ParseFloat(answer, 64)
	case "integer":
		return strconv.ParseInt(answer, 10, 64)
	case "boolean":
		return strconv.ParseBool(answer)
	default:
		return answer, nil
	}
}
