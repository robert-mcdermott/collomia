package tui

import (
	"fmt"
	"sort"
	"strings"
)

func (m *Model) slash(line string) bool {
	parts := strings.Fields(line)
	command := strings.ToLower(parts[0])
	args := parts[1:]
	switch command {
	case "/quit", "/exit":
		return true
	case "/help":
		m.addSystem(`Slash commands:
/status                 workspace, provider, model, and autonomy
/model [provider/model] list or switch provider/model
/models                 list configured providers and default models
/context                token usage and estimated context size
/plan [on|off]          toggle read-only planning mode
/autonomy <mode>        ask, workspace, or autopilot
/skills                 list discovered SKILL.md / skills.md instructions
/mcp                    list connected MCP servers
/tools                  list tools visible to the agent
/config                 show the active configuration path
/clear                  clear conversation context
/quit                   exit Collomia`)
	case "/status":
		m.addSystem(m.runtime.Summary())
	case "/models":
		var lines []string
		for _, name := range m.runtime.Config.ProviderNames() {
			p := m.runtime.Config.Providers[name]
			lines = append(lines, fmt.Sprintf("- %s  [%s]  %s", name, p.Type, p.Model))
		}
		m.addSystem("Configured providers:\n" + strings.Join(lines, "\n"))
	case "/model":
		if len(args) == 0 {
			provider, model := m.runtime.Agent.Selection()
			m.addSystem(fmt.Sprintf("Current model: %s/%s\nUse /model provider/model to switch.", provider, model))
			break
		}
		selection := strings.Join(args, " ")
		providerName, currentModel := m.runtime.Agent.Selection()
		model := selection
		if candidate, rest, ok := strings.Cut(selection, "/"); ok {
			if _, exists := m.runtime.Config.Providers[candidate]; exists {
				providerName = candidate
				model = rest
			}
		}
		if _, exists := m.runtime.Config.Providers[selection]; exists {
			providerName = selection
			model = ""
		}
		if err := m.runtime.Select(providerName, model); err != nil {
			m.addError(err)
			break
		}
		providerName, currentModel = m.runtime.Agent.Selection()
		m.addSystem(fmt.Sprintf("Switched to %s/%s", providerName, currentModel))
	case "/context":
		usage := m.runtime.Agent.Usage()
		estimate, window := m.runtime.Agent.ContextEstimate()
		windowText := "unknown"
		if window > 0 {
			windowText = fmt.Sprintf("%d", window)
		}
		m.addSystem(fmt.Sprintf("Provider usage this session: %d input / %d output tokens\nEstimated current prompt: ~%d tokens of %s\nMessages: %d", usage.InputTokens, usage.OutputTokens, estimate, windowText, m.runtime.Agent.MessageCount()))
	case "/plan":
		enabled := !m.runtime.Agent.Plan()
		if len(args) > 0 {
			switch strings.ToLower(args[0]) {
			case "on", "true":
				enabled = true
			case "off", "false":
				enabled = false
			}
		}
		m.runtime.Agent.SetPlan(enabled)
		if enabled {
			m.addSystem("Planning mode enabled. Only read-only discovery tools are exposed.")
		} else {
			m.addSystem("Planning mode disabled. Execution tools are available subject to permissions.")
		}
	case "/autonomy":
		if len(args) == 0 {
			m.addSystem("Autonomy mode: " + m.runtime.Permissions.Mode() + " (ask, workspace, autopilot)")
			break
		}
		if err := m.runtime.Permissions.SetMode(strings.ToLower(args[0])); err != nil {
			m.addError(err)
			break
		}
		note := "Autonomy set to " + m.runtime.Permissions.Mode()
		if m.runtime.Permissions.Mode() == "autopilot" {
			note += ". Workspace tools can now run without prompts; hard safety denials still apply."
		}
		m.addSystem(note)
	case "/skills":
		if len(m.runtime.Skills.Skills) == 0 {
			m.addSystem("No skills discovered.")
			break
		}
		var lines []string
		for _, skill := range m.runtime.Skills.Skills {
			lines = append(lines, fmt.Sprintf("- %s: %s", skill.Name, skill.Description))
		}
		m.addSystem("Discovered skills:\n" + strings.Join(lines, "\n"))
	case "/mcp":
		servers := m.runtime.MCP.Servers()
		if len(servers) == 0 {
			m.addSystem("No MCP servers connected.")
			break
		}
		sort.Strings(servers)
		m.addSystem("Connected MCP servers: " + strings.Join(servers, ", "))
	case "/tools":
		m.addSystem("Available tools: " + strings.Join(m.runtime.Registry.Names(), ", "))
	case "/config":
		m.addSystem("Active configuration: " + m.runtime.Config.Source + "\nProject configuration takes precedence over the user configuration. Run `collo init` to create " + m.runtime.Workspace + "/.collomia.json.")
	case "/clear":
		m.runtime.Agent.Clear()
		m.blocks = nil
		m.addSystem("Conversation context cleared.")
	default:
		m.addError(fmt.Errorf("unknown command %s; use /help", command))
	}
	return false
}

func (m *Model) addSystem(value string) {
	m.blocks = append(m.blocks, block{role: "system", content: value})
}
func (m *Model) addError(err error) {
	m.blocks = append(m.blocks, block{role: "error", content: err.Error()})
}
