package mcpclient

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// renderContent renders typed MCP content blocks for the model without
// silently dropping non-text parts: binary media becomes an explicit marker
// carrying its type and size, resource links keep their URI and the hint
// needed to follow them, and embedded resources contribute their text.
func renderContent(parts []mcp.Content) []string {
	var out []string
	for _, part := range parts {
		switch c := part.(type) {
		case *mcp.TextContent:
			if c.Text != "" {
				out = append(out, c.Text)
			}
		case *mcp.ImageContent:
			out = append(out, fmt.Sprintf("[image %s, %d bytes — binary content not inlined]", c.MIMEType, len(c.Data)))
		case *mcp.AudioContent:
			out = append(out, fmt.Sprintf("[audio %s, %d bytes — binary content not inlined]", c.MIMEType, len(c.Data)))
		case *mcp.ResourceLink:
			line := fmt.Sprintf("[resource link: %s", c.URI)
			if c.Name != "" {
				line += " (" + c.Name + ")"
			}
			line += " — read it with the read_mcp_resource tool]"
			if c.Description != "" {
				line += " " + c.Description
			}
			out = append(out, line)
		case *mcp.EmbeddedResource:
			if c.Resource != nil {
				out = append(out, renderResourceContents(c.Resource))
			}
		default:
			if data, err := json.Marshal(part); err == nil {
				out = append(out, string(data))
			}
		}
	}
	return out
}

// renderResourceContents renders one resources/read content entry.
func renderResourceContents(rc *mcp.ResourceContents) string {
	if rc == nil {
		return ""
	}
	header := rc.URI
	if rc.MIMEType != "" {
		header += " (" + rc.MIMEType + ")"
	}
	if rc.Text != "" {
		return header + ":\n" + rc.Text
	}
	return fmt.Sprintf("%s: [binary resource, %d bytes — not inlined]", header, len(rc.Blob))
}

// renderToolResult flattens a tool call's typed content while preserving
// structured output and non-text markers.
func renderToolResult(result *mcp.CallToolResult) string {
	parts := renderContent(result.Content)
	if result.StructuredContent != nil {
		if value, err := json.MarshalIndent(result.StructuredContent, "", "  "); err == nil {
			parts = append(parts, string(value))
		}
	}
	output := strings.Join(parts, "\n")
	if output == "" {
		if data, err := json.Marshal(result); err == nil {
			output = string(data)
		}
	}
	return output
}

// renderPromptResult renders a server prompt's messages as reviewable text
// for the input box: single user-message prompts (the common template case)
// come through verbatim, multi-message prompts keep their roles.
func renderPromptResult(result *mcp.GetPromptResult) string {
	var sections []string
	for _, message := range result.Messages {
		text := strings.Join(renderContent([]mcp.Content{message.Content}), "\n")
		if text == "" {
			continue
		}
		sections = append(sections, strings.TrimSpace(text))
	}
	if len(result.Messages) > 1 {
		var labeled []string
		for i, message := range result.Messages {
			if i < len(sections) {
				labeled = append(labeled, fmt.Sprintf("[%s] %s", message.Role, sections[i]))
			}
		}
		return strings.Join(labeled, "\n\n")
	}
	return strings.Join(sections, "\n\n")
}
