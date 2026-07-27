package mcpclient

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/robert-mcdermott/collomia/internal/external"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

const maxMCPMetadataBytes = external.MaxMetadataBytes

// frameExternalMCPData gives every model-visible MCP payload explicit
// provenance and a content-derived boundary. MCP servers are external
// principals: trusting a server definition permits connection and calls, but
// never promotes returned text, prompt templates, descriptions, or resources
// to instructions.
//
// The framing itself lives in internal/external because the web tools need
// the same guarantees; only the field names are specific to MCP.
func frameExternalMCPData(kind, server, subject, content string) string {
	return external.Frame("MCP", []external.Field{
		{Key: "source_server", Value: server},
		{Key: "content_type", Value: kind},
		{Key: "source_subject", Value: subject},
	}, content)
}

func compactMetadata(value string) string { return external.CompactMetadata(value) }

// sanitizeToolSchema keeps the structural JSON Schema supplied by an MCP
// server while visibly downgrading prose fields that otherwise appear beside
// Collomia-authored tool instructions. Examples and comments are nonessential
// executable metadata and are removed to reduce prompt-injection surface.
func sanitizeToolSchema(raw []byte) ([]byte, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	value = sanitizeSchemaValue(value)
	return json.Marshal(value)
}

func sanitizeSchemaValue(value any) any {
	switch item := value.(type) {
	case map[string]any:
		clean := make(map[string]any, len(item))
		for key, child := range item {
			switch key {
			case "$comment", "examples":
				continue
			case "description", "title":
				if text, ok := child.(string); ok {
					clean[key] = "[external MCP metadata; descriptive only] " + compactMetadata(text)
					continue
				}
			}
			clean[key] = sanitizeSchemaValue(child)
		}
		return clean
	case []any:
		clean := make([]any, len(item))
		for i, child := range item {
			clean[i] = sanitizeSchemaValue(child)
		}
		return clean
	default:
		return value
	}
}

// renderContent renders typed MCP content blocks for the model without
// silently dropping non-text parts: binary media becomes an explicit marker
// carrying its type and size, resource links keep their URI and the hint
// needed to follow them, and embedded resources contribute their text.
type renderedMCPContent struct {
	text   []string
	images []provider.ContentPart
}

func renderRichContent(parts []mcp.Content) renderedMCPContent {
	var out renderedMCPContent
	for _, part := range parts {
		switch c := part.(type) {
		case *mcp.TextContent:
			if c.Text != "" {
				out.text = append(out.text, c.Text)
			}
		case *mcp.ImageContent:
			out.text = append(out.text, fmt.Sprintf("[image %s, %d bytes — typed binary content attached when the active provider route supports tool-result images]", c.MIMEType, len(c.Data)))
			out.images = append(out.images, provider.ContentPart{Type: provider.ContentImage, Name: "MCP image", MediaType: c.MIMEType, Size: len(c.Data), Data: append([]byte(nil), c.Data...)})
		case *mcp.AudioContent:
			out.text = append(out.text, fmt.Sprintf("[audio %s, %d bytes — binary content not inlined]", c.MIMEType, len(c.Data)))
		case *mcp.ResourceLink:
			line := fmt.Sprintf("[resource link: %s", c.URI)
			if c.Name != "" {
				line += " (" + c.Name + ")"
			}
			line += " — read it with the read_mcp_resource tool]"
			if c.Description != "" {
				line += " " + c.Description
			}
			out.text = append(out.text, line)
		case *mcp.EmbeddedResource:
			if c.Resource != nil {
				out.text = append(out.text, renderResourceContents(c.Resource))
			}
		default:
			if data, err := json.Marshal(part); err == nil {
				out.text = append(out.text, string(data))
			}
		}
	}
	return out
}

func renderContent(parts []mcp.Content) []string { return renderRichContent(parts).text }

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
	return renderRichToolResult(result).Content
}

func renderRichToolResult(result *mcp.CallToolResult) provider.Message {
	rendered := renderRichContent(result.Content)
	parts := rendered.text
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
	return provider.Message{Content: output, Parts: rendered.images}
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
