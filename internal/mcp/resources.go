package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// ResourceInfo describes one server resource for browsing.
type ResourceInfo struct {
	URI         string
	Name        string
	Description string
	MIMEType    string
	Size        int64
}

// PromptInfo describes one server prompt template.
type PromptInfo struct {
	Name        string
	Title       string
	Description string
	Arguments   []PromptArgInfo
}

type PromptArgInfo struct {
	Name        string
	Description string
	Required    bool
}

// connectedSession returns the live session and timeout for one server, or
// an actionable error naming the server's actual state.
func (m *Manager) connectedSession(name string) (*mcp.ClientSession, time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.servers[name]
	if !ok {
		return nil, 0, fmt.Errorf("unknown MCP server %q", name)
	}
	if state.session == nil {
		return nil, 0, fmt.Errorf("server %s is not connected (%s)", name, state.status)
	}
	return state.session, time.Duration(state.cfg.Timeout) * time.Second, nil
}

// hasCapability reports whether a connected server negotiated a capability.
func hasCapability(session *mcp.ClientSession, capability string) bool {
	init := session.InitializeResult()
	if init == nil {
		return false
	}
	for _, name := range capabilityNames(init.Capabilities) {
		if name == capability {
			return true
		}
	}
	return false
}

// Resources lists a server's resources (paginated to completion).
func (m *Manager) Resources(ctx context.Context, server string) (out []ResourceInfo, err error) {
	session, timeout, err := m.connectedSession(server)
	if err != nil {
		return nil, err
	}
	if !hasCapability(session, "resources") {
		return nil, fmt.Errorf("server %s did not negotiate the resources capability", server)
	}
	version := m.catalogVersion(server, session, "resources")
	defer func() { m.markCatalogObserved(server, session, "resources", version, err) }()
	listCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cursor := ""
	for {
		result, err := session.ListResources(listCtx, &mcp.ListResourcesParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, r := range result.Resources {
			out = append(out, ResourceInfo{URI: r.URI, Name: r.Name, Description: r.Description, MIMEType: r.MIMEType, Size: r.Size})
		}
		cursor = result.NextCursor
		if cursor == "" {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URI < out[j].URI })
	return out, nil
}

// ReadResource fetches one resource and renders its contents.
func (m *Manager) ReadResource(ctx context.Context, server, uri string) (string, error) {
	session, timeout, err := m.connectedSession(server)
	if err != nil {
		return "", err
	}
	readCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := session.ReadResource(readCtx, &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		return "", err
	}
	var parts []string
	for _, contents := range result.Contents {
		if rendered := renderResourceContents(contents); rendered != "" {
			parts = append(parts, rendered)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("resource %s returned no content", uri)
	}
	return strings.Join(parts, "\n\n"), nil
}

// Prompts lists a server's prompt templates (paginated to completion).
func (m *Manager) Prompts(ctx context.Context, server string) (out []PromptInfo, err error) {
	session, timeout, err := m.connectedSession(server)
	if err != nil {
		return nil, err
	}
	if !hasCapability(session, "prompts") {
		return nil, fmt.Errorf("server %s did not negotiate the prompts capability", server)
	}
	version := m.catalogVersion(server, session, "prompts")
	defer func() { m.markCatalogObserved(server, session, "prompts", version, err) }()
	listCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cursor := ""
	for {
		result, err := session.ListPrompts(listCtx, &mcp.ListPromptsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, p := range result.Prompts {
			info := PromptInfo{Name: p.Name, Title: p.Title, Description: p.Description}
			for _, arg := range p.Arguments {
				info.Arguments = append(info.Arguments, PromptArgInfo{Name: arg.Name, Description: arg.Description, Required: arg.Required})
			}
			out = append(out, info)
		}
		cursor = result.NextCursor
		if cursor == "" {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// markCatalogObserved clears a pending list-change marker only after the live
// catalog was read successfully. Errors remain visible in /mcp status while
// the last known-good tool registry (if any) remains usable.
func (m *Manager) catalogVersion(server string, session *mcp.ClientSession, catalog string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.servers[server]
	if state == nil || state.session != session {
		return 0
	}
	return state.catalogVersions[catalog]
}

func (m *Manager) markCatalogObserved(server string, session *mcp.ClientSession, catalog string, version uint64, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.servers[server]
	if state == nil || state.session != session {
		return
	}
	if state.catalogErrors == nil {
		state.catalogErrors = map[string]error{}
	}
	if err != nil {
		state.catalogErrors[catalog] = err
		return
	}
	if state.catalogVersions[catalog] == version {
		state.pendingCatalogs[catalog] = false
	}
	state.catalogUpdatedAt = time.Now()
	delete(state.catalogErrors, catalog)
}

// GetPrompt expands a server prompt template with arguments and renders the
// resulting messages as text.
func (m *Manager) GetPrompt(ctx context.Context, server, name string, args map[string]string) (string, error) {
	session, timeout, err := m.connectedSession(server)
	if err != nil {
		return "", err
	}
	getCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := session.GetPrompt(getCtx, &mcp.GetPromptParams{Name: name, Arguments: args})
	if err != nil {
		return "", err
	}
	text := renderPromptResult(result)
	if text == "" {
		return "", fmt.Errorf("prompt %s produced no text", name)
	}
	return text, nil
}

// registerResourceTools adds the model-facing resource tools once MCP is in
// play. Both are external calls scoped to the server named in the arguments
// so permission rules on a server keep matching.
func (m *Manager) registerResourceTools() {
	type listArgs struct {
		Server string `json:"server"`
	}
	m.registry.Add(tools.Function{
		Def:    provider.ToolDefinition{Name: "list_mcp_resources", Description: "List the resources (documents, data, files) an MCP server exposes. Use read_mcp_resource to fetch one.", InputSchema: json.RawMessage(`{"type":"object","properties":{"server":{"type":"string","description":"MCP server name; omit to list every connected server's resources"}},"additionalProperties":false}`)},
		Action: tools.Action{Risk: tools.RiskExternal, Summary: "list MCP resources"},
		AssessFn: func(raw json.RawMessage) (tools.Action, error) {
			var a listArgs
			_ = json.Unmarshal(raw, &a)
			return tools.Action{Risk: tools.RiskExternal, Summary: "list MCP resources on " + orAll(a.Server), Server: a.Server}, nil
		},
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a listArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return "", err
			}
			servers := []string{a.Server}
			if a.Server == "" {
				servers = m.Servers()
			}
			var sections []string
			for _, server := range servers {
				resources, err := m.Resources(ctx, server)
				if err != nil {
					if a.Server == "" && strings.Contains(err.Error(), "capability") {
						continue
					}
					return "", err
				}
				var lines []string
				for _, r := range resources {
					line := "- " + r.URI
					if r.Name != "" && r.Name != r.URI {
						line += " (" + r.Name + ")"
					}
					if r.MIMEType != "" {
						line += " [" + r.MIMEType + "]"
					}
					if r.Description != "" {
						line += ": " + r.Description
					}
					lines = append(lines, line)
				}
				if len(lines) > 0 {
					sections = append(sections, fmt.Sprintf("Server %s:\n%s", server, strings.Join(lines, "\n")))
				}
			}
			if len(sections) == 0 {
				return "No MCP resources available.", nil
			}
			return strings.Join(sections, "\n\n"), nil
		},
	})
	type readArgs struct {
		Server string `json:"server"`
		URI    string `json:"uri"`
	}
	m.registry.Add(tools.Function{
		Def:    provider.ToolDefinition{Name: "read_mcp_resource", Description: "Read one resource from an MCP server by URI (discover URIs with list_mcp_resources).", InputSchema: json.RawMessage(`{"type":"object","properties":{"server":{"type":"string"},"uri":{"type":"string"}},"required":["server","uri"],"additionalProperties":false}`)},
		Action: tools.Action{Risk: tools.RiskExternal, Summary: "read MCP resource"},
		AssessFn: func(raw json.RawMessage) (tools.Action, error) {
			var a readArgs
			_ = json.Unmarshal(raw, &a)
			return tools.Action{Risk: tools.RiskExternal, Summary: "read MCP resource " + a.URI + " from " + a.Server, Server: a.Server}, nil
		},
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a readArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return "", err
			}
			return m.ReadResource(ctx, a.Server, a.URI)
		},
	})
}

func orAll(server string) string {
	if server == "" {
		return "all servers"
	}
	return server
}
