package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	mcpclient "github.com/robert-mcdermott/collomia/internal/mcp"
)

// mcpCommand handles /mcp subcommands: list/status, ping, reconnect,
// enable, disable, add, remove. The bare /mcp opens the server picker.
func (m *Model) mcpCommand(args []string) {
	sub := strings.ToLower(args[0])
	rest := args[1:]
	need := func(what string) (string, bool) {
		if len(rest) == 0 {
			m.addError(fmt.Errorf("usage: /mcp %s <%s>", sub, what))
			return "", false
		}
		return rest[0], true
	}
	switch sub {
	case "list", "status":
		m.addPanel("MCP servers", m.mcpStatusReport())
	case "ping":
		name, ok := need("server")
		if !ok {
			return
		}
		start := time.Now()
		if err := m.runtime.MCP.Ping(context.Background(), name); err != nil {
			m.addError(err)
			return
		}
		m.addSystem(fmt.Sprintf("%s answered the ping in %s.", name, time.Since(start).Round(time.Millisecond)))
	case "reconnect":
		name, ok := need("server")
		if !ok {
			return
		}
		if err := m.runtime.MCP.Reconnect(context.Background(), name); err != nil {
			m.addError(err)
			return
		}
		m.addSystem("Reconnected " + name + " and refreshed its tool catalog.")
		m.drainMCPNotes()
	case "enable", "disable":
		name, ok := need("server")
		if !ok {
			return
		}
		if err := m.runtime.MCP.SetEnabled(context.Background(), name, sub == "enable"); err != nil {
			m.addError(err)
			return
		}
		if sub == "enable" {
			m.addSystem("Enabled " + name + "; its tools are available again.")
			m.drainMCPNotes()
		} else {
			m.addSystem("Disabled " + name + " for this session; its tools were withdrawn. /mcp enable " + name + " restores it.")
		}
	case "prompts":
		name, ok := need("server")
		if !ok {
			return
		}
		prompts, err := m.runtime.MCP.Prompts(context.Background(), name)
		if err != nil {
			m.addError(err)
			return
		}
		if len(prompts) == 0 {
			m.addPanel("MCP prompts · "+name, "This server exposes no prompts.")
			return
		}
		var lines []string
		for _, p := range prompts {
			line := "- " + p.Name
			if p.Title != "" && p.Title != p.Name {
				line += " (" + p.Title + ")"
			}
			if p.Description != "" {
				line += ": " + p.Description
			}
			for _, arg := range p.Arguments {
				req := ""
				if arg.Required {
					req = " (required)"
				}
				line += fmt.Sprintf("\n    %s%s — %s", arg.Name, req, arg.Description)
			}
			lines = append(lines, line)
		}
		lines = append(lines, "\n/mcp prompt "+name+" <name> key=value …  expands a template into the input box for review before sending.")
		m.addPanel("MCP prompts · "+name, strings.Join(lines, "\n"))
	case "prompt":
		if len(rest) < 2 {
			m.addError(fmt.Errorf("usage: /mcp prompt <server> <name> [key=value …]"))
			return
		}
		server, promptName := rest[0], rest[1]
		promptArgs := map[string]string{}
		for _, pair := range rest[2:] {
			key, value, ok := strings.Cut(pair, "=")
			if !ok {
				m.addError(fmt.Errorf("prompt arguments are key=value pairs, got %q", pair))
				return
			}
			promptArgs[key] = value
		}
		text, err := m.runtime.MCP.GetPrompt(context.Background(), server, promptName, promptArgs)
		if err != nil {
			m.addError(err)
			return
		}
		m.input.SetValue(text)
		m.input.CursorEnd()
		m.input.Focus()
		m.addSystem(fmt.Sprintf("Prompt %s/%s expanded into the input box — review or edit it, then press enter to send.", server, promptName))
	case "resources":
		name, ok := need("server")
		if !ok {
			return
		}
		resources, err := m.runtime.MCP.Resources(context.Background(), name)
		if err != nil {
			m.addError(err)
			return
		}
		if len(resources) == 0 {
			m.addPanel("MCP resources · "+name, "This server exposes no resources.")
			return
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
		lines = append(lines, "\n/mcp resource "+name+" <uri>  previews one here; the agent reads them itself with read_mcp_resource.")
		m.addPanel("MCP resources · "+name, strings.Join(lines, "\n"))
	case "resource":
		if len(rest) < 2 {
			m.addError(fmt.Errorf("usage: /mcp resource <server> <uri>"))
			return
		}
		content, err := m.runtime.MCP.ReadResource(context.Background(), rest[0], rest[1])
		if err != nil {
			m.addError(err)
			return
		}
		const previewCap = 4000
		if len(content) > previewCap {
			content = content[:previewCap] + fmt.Sprintf("\n… (%d more bytes; the agent can read the full resource with read_mcp_resource)", len(content)-previewCap)
		}
		m.addPanel("MCP resource · "+rest[1], content)
	case "add":
		m.mcpAdd(rest)
	case "remove":
		name, ok := need("server")
		if !ok {
			return
		}
		if err := m.runtime.MCP.Remove(name); err != nil {
			m.addError(err)
			return
		}
		m.addSystem("Removed " + name + ". Servers from the configuration file return on the next start; runtime-added servers are gone.")
	default:
		m.addError(fmt.Errorf("unknown /mcp subcommand %q (list, ping, reconnect, enable, disable, add, remove)", sub))
	}
}

// drainMCPNotes surfaces pin observations (definition or remote-identity
// changes) produced by a lifecycle operation.
func (m *Model) drainMCPNotes() {
	for _, note := range m.runtime.MCP.TakeNotes() {
		m.addSystem("⚠ MCP " + note)
	}
}

// mcpAdd connects a session-scoped server the user defines inline:
//
//	/mcp add <name> <command> [args…]     stdio
//	/mcp add <name> --url <endpoint>      streamable HTTP
func (m *Model) mcpAdd(args []string) {
	if len(args) < 2 {
		m.addError(fmt.Errorf("usage: /mcp add <name> <command> [args…]  or  /mcp add <name> --url <endpoint>"))
		return
	}
	name := args[0]
	cfg := appconfig.MCPServer{Timeout: 30}
	if args[1] == "--url" {
		if len(args) < 3 {
			m.addError(fmt.Errorf("usage: /mcp add <name> --url <endpoint>"))
			return
		}
		cfg.Transport, cfg.URL = "http", args[2]
	} else {
		cfg.Transport, cfg.Command, cfg.Args = "stdio", args[1], args[2:]
	}
	if err := m.runtime.MCP.Add(context.Background(), name, cfg); err != nil {
		m.addError(err)
		return
	}
	count := 0
	for _, status := range m.runtime.MCP.Statuses() {
		if status.Name == name {
			count = len(status.Tools)
		}
	}
	m.addSystem(fmt.Sprintf("Connected %s (%d tools) for this session. Add it to the configuration file to keep it.", name, count))
}

// mcpStatusReport renders every known server with health, identity,
// negotiated capabilities, and tool counts.
func (m *Model) mcpStatusReport() string {
	statuses := m.runtime.MCP.Statuses()
	if len(statuses) == 0 {
		return "No MCP servers configured. Add mcp.<name> to the configuration file (project servers require `collo trust`), or connect one now with /mcp add <name> <command…>."
	}
	var lines []string
	for _, s := range statuses {
		glyph := "●"
		switch s.Status {
		case mcpclient.StatusConnected:
		case mcpclient.StatusDisabled:
			glyph = "◌"
		default:
			glyph = "✗"
		}
		line := fmt.Sprintf("%s %s — %s (%s", glyph, s.Name, s.Status, s.Transport)
		if s.Runtime {
			line += ", session-only"
		}
		line += ")"
		if s.ServerName != "" {
			line += fmt.Sprintf("\n    server: %s %s", s.ServerName, s.ServerVersion)
		}
		if len(s.Capabilities) > 0 {
			line += "\n    capabilities: " + strings.Join(s.Capabilities, ", ")
		}
		if len(s.Tools) > 0 {
			line += fmt.Sprintf("\n    tools: %d registered", len(s.Tools))
		}
		if !s.ConnectedAt.IsZero() {
			line += fmt.Sprintf("\n    up for %s", time.Since(s.ConnectedAt).Round(time.Second))
		}
		if s.Err != "" {
			line += "\n    error: " + s.Err
		}
		lines = append(lines, line)
	}
	lines = append(lines, "\n/mcp ping|reconnect|enable|disable|remove <name> · /mcp add <name> <command…> · /mcp prompts|resources <name>")
	return strings.Join(lines, "\n")
}
