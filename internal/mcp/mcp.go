package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// Server status values reported by the Manager.
const (
	StatusConnected = "connected"
	StatusError     = "error"
	StatusDisabled  = "disabled"
	StatusUntrusted = "untrusted"
)

// ServerStatus is a point-in-time snapshot of one configured or runtime-added
// MCP server for display and diagnostics.
type ServerStatus struct {
	Name      string
	Transport string
	Status    string
	Err       string
	// Runtime marks servers added with /mcp add for this session only.
	Runtime     bool
	ConnectedAt time.Time
	// Tools are the registry (public) tool names contributed by the server.
	Tools []string
	// ServerName/ServerVersion identify the remote implementation, and
	// Capabilities lists what it negotiated at initialize time
	// (tools, resources, prompts, logging, completions).
	ServerName    string
	ServerVersion string
	Capabilities  []string
}

type serverState struct {
	name        string
	cfg         appconfig.MCPServer
	runtime     bool
	session     *mcp.ClientSession
	status      string
	err         error
	connectedAt time.Time
	toolNames   []string
}

// Manager owns every MCP server session for the process lifetime and
// supports runtime lifecycle operations: list with health and negotiated
// capabilities, reconnect, enable/disable, and session-scoped add/remove.
type Manager struct {
	mu       sync.Mutex
	registry *tools.Registry
	servers  map[string]*serverState
}

// ConnectAll starts every trusted, enabled configured server. Untrusted,
// disabled, and failed servers are retained with their status so the runtime
// can report and repair them instead of forgetting they exist.
func ConnectAll(ctx context.Context, configured map[string]appconfig.MCPServer, registry *tools.Registry) (*Manager, []error) {
	manager := &Manager{registry: registry, servers: map[string]*serverState{}}
	var errs []error
	for name, cfg := range configured {
		state := &serverState{name: name, cfg: cfg}
		manager.servers[name] = state
		switch {
		case cfg.Disabled:
			state.status = StatusDisabled
		case !cfg.Trusted:
			state.status = StatusUntrusted
			state.err = fmt.Errorf("not started because trusted is false; review the server and set trusted to true")
			errs = append(errs, fmt.Errorf("MCP %s: %w", name, state.err))
		default:
			if err := manager.startLocked(ctx, state); err != nil {
				errs = append(errs, fmt.Errorf("MCP %s: %w", name, err))
			}
		}
	}
	if len(configured) > 0 {
		manager.registerResourceTools()
	}
	return manager, errs
}

// startLocked connects a server and registers its tools; the caller must
// hold no expectation of partial success — on any failure the session is
// closed and the state records the error.
func (m *Manager) startLocked(ctx context.Context, state *serverState) error {
	session, err := dial(ctx, state.name, state.cfg)
	if err != nil {
		state.status, state.err = StatusError, err
		return err
	}
	toolNames, err := registerTools(ctx, state.name, state.cfg, session, m.registry)
	if err != nil {
		_ = session.Close()
		state.status, state.err = StatusError, fmt.Errorf("tools: %w", err)
		return state.err
	}
	state.session = session
	state.status, state.err = StatusConnected, nil
	state.connectedAt = time.Now()
	state.toolNames = toolNames
	return nil
}

// stopLocked closes the session (if any) and removes the server's tools
// from the registry.
func (m *Manager) stopLocked(state *serverState) {
	if state.session != nil {
		_ = state.session.Close()
		state.session = nil
	}
	for _, name := range state.toolNames {
		m.registry.Remove(name)
	}
	state.toolNames = nil
	state.connectedAt = time.Time{}
}

// Statuses reports every known server, sorted by name. Health is the last
// known state; Ping refreshes it for connected servers.
func (m *Manager) Statuses() []ServerStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ServerStatus
	for _, state := range m.servers {
		s := ServerStatus{
			Name:        state.name,
			Transport:   transportName(state.cfg),
			Status:      state.status,
			Runtime:     state.runtime,
			ConnectedAt: state.connectedAt,
			Tools:       append([]string(nil), state.toolNames...),
		}
		if state.err != nil {
			s.Err = state.err.Error()
		}
		if state.session != nil {
			if init := state.session.InitializeResult(); init != nil {
				if init.ServerInfo != nil {
					s.ServerName, s.ServerVersion = init.ServerInfo.Name, init.ServerInfo.Version
				}
				s.Capabilities = capabilityNames(init.Capabilities)
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Servers lists connected server names (the model-visible set).
func (m *Manager) Servers() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var names []string
	for _, state := range m.servers {
		if state.status == StatusConnected {
			names = append(names, state.name)
		}
	}
	sort.Strings(names)
	return names
}

// Ping health-checks one connected server and records the outcome: a failed
// ping moves the server to the error state (its tools stay registered until
// a reconnect or disable, since a transient blip may heal).
func (m *Manager) Ping(ctx context.Context, name string) error {
	m.mu.Lock()
	state, ok := m.servers[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("unknown MCP server %q", name)
	}
	session := state.session
	timeout := time.Duration(state.cfg.Timeout) * time.Second
	m.mu.Unlock()
	if session == nil {
		return fmt.Errorf("server %s is not connected (%s)", name, state.status)
	}
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := session.Ping(pingCtx, nil)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		state.status, state.err = StatusError, fmt.Errorf("ping failed: %w", err)
		return state.err
	}
	if state.session == session {
		state.status, state.err = StatusConnected, nil
	}
	return nil
}

// Reconnect tears down and re-establishes one server, refreshing its tool
// catalog in the registry.
func (m *Manager) Reconnect(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.servers[name]
	if !ok {
		return fmt.Errorf("unknown MCP server %q", name)
	}
	if state.cfg.Disabled || state.status == StatusDisabled {
		return fmt.Errorf("server %s is disabled; enable it first", name)
	}
	if state.status == StatusUntrusted {
		return fmt.Errorf("server %s is untrusted; set trusted to true in the configuration after reviewing it", name)
	}
	m.stopLocked(state)
	return m.startLocked(ctx, state)
}

// SetEnabled disables a server (closing it and withdrawing its tools) or
// re-enables and connects it. Runtime enablement cannot override missing
// trust for configured servers.
func (m *Manager) SetEnabled(ctx context.Context, name string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.servers[name]
	if !ok {
		return fmt.Errorf("unknown MCP server %q", name)
	}
	if !enabled {
		m.stopLocked(state)
		state.status, state.err = StatusDisabled, nil
		return nil
	}
	if state.status == StatusConnected {
		return nil
	}
	if !state.runtime && !state.cfg.Trusted {
		state.status = StatusUntrusted
		return fmt.Errorf("server %s is untrusted; set trusted to true in the configuration after reviewing it", name)
	}
	return m.startLocked(ctx, state)
}

// Add connects a server defined at runtime by the user (not by project
// configuration), for this session only. User-initiated definitions are
// inherently trusted — the trust gate exists to quarantine repository-supplied
// configuration, not the user's own explicit commands.
func (m *Manager) Add(ctx context.Context, name string, cfg appconfig.MCPServer) error {
	if !serverNameRE.MatchString(name) {
		return fmt.Errorf("server name must be letters, digits, hyphens, or underscores, got %q", name)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.servers[name]; exists {
		return fmt.Errorf("an MCP server named %q already exists; remove it first or pick another name", name)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30
	}
	cfg.Trusted = true
	state := &serverState{name: name, cfg: cfg, runtime: true}
	m.servers[name] = state
	if err := m.startLocked(ctx, state); err != nil {
		delete(m.servers, name)
		return err
	}
	m.registerResourceTools()
	return nil
}

// Remove disconnects a server and forgets it for this session. Configured
// servers return on the next start; runtime-added ones are gone for good.
func (m *Manager) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.servers[name]
	if !ok {
		return fmt.Errorf("unknown MCP server %q", name)
	}
	m.stopLocked(state)
	delete(m.servers, name)
	return nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, state := range m.servers {
		if state.session != nil {
			_ = state.session.Close()
			state.session = nil
		}
	}
}

var serverNameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func transportName(cfg appconfig.MCPServer) string {
	if t := strings.ToLower(cfg.Transport); t != "" && t != "stdio" {
		return t
	}
	return "stdio"
}

// capabilityNames flattens the negotiated server capabilities for display.
func capabilityNames(caps *mcp.ServerCapabilities) []string {
	if caps == nil {
		return nil
	}
	var names []string
	if caps.Tools != nil {
		names = append(names, "tools")
	}
	if caps.Resources != nil {
		names = append(names, "resources")
	}
	if caps.Prompts != nil {
		names = append(names, "prompts")
	}
	if caps.Logging != nil {
		names = append(names, "logging")
	}
	if caps.Completions != nil {
		names = append(names, "completions")
	}
	return names
}

// dial is the connection seam; tests replace it with an in-memory server.
var dial = connect

func connect(ctx context.Context, name string, cfg appconfig.MCPServer) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "collomia", Version: "0.1.0"}, nil)
	timeout := time.Duration(cfg.Timeout) * time.Second
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch strings.ToLower(cfg.Transport) {
	case "stdio", "":
		if cfg.Command == "" {
			return nil, fmt.Errorf("stdio transport requires command")
		}
		cmd := exec.Command(cfg.Command, cfg.Args...)
		cmd.Env = os.Environ()
		for key, value := range cfg.Env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		return client.Connect(connectCtx, &mcp.CommandTransport{Command: cmd}, nil)
	case "http", "streamable-http":
		if cfg.URL == "" {
			return nil, fmt.Errorf("HTTP transport requires url")
		}
		httpClient := &http.Client{Timeout: timeout, Transport: headerTransport{base: http.DefaultTransport, headers: cfg.Headers}}
		return client.Connect(connectCtx, &mcp.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: httpClient}, nil)
	default:
		return nil, fmt.Errorf("unsupported transport %q", cfg.Transport)
	}
}

func registerTools(ctx context.Context, server string, cfg appconfig.MCPServer, session *mcp.ClientSession, registry *tools.Registry) ([]string, error) {
	listCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Timeout)*time.Second)
	defer cancel()
	var registered []string
	cursor := ""
	for {
		result, err := session.ListTools(listCtx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, remote := range result.Tools {
			remote := remote
			publicName := sanitize("mcp_" + server + "_" + remote.Name)
			schema, err := json.Marshal(remote.InputSchema)
			if err != nil {
				return nil, err
			}
			if string(schema) == "null" {
				schema = []byte(`{"type":"object"}`)
			}
			registered = append(registered, publicName)
			registry.Add(tools.Function{Def: provider.ToolDefinition{Name: publicName, Description: fmt.Sprintf("MCP server %s tool %s. %s", server, remote.Name, remote.Description), InputSchema: schema}, Action: tools.Action{Risk: tools.RiskExternal, Summary: "call MCP tool " + server + "/" + remote.Name, Server: server}, Run: func(callCtx context.Context, raw json.RawMessage) (string, error) {
				var args map[string]any
				if err := json.Unmarshal(raw, &args); err != nil {
					return "", err
				}
				timeoutCtx, cancel := context.WithTimeout(callCtx, time.Duration(cfg.Timeout)*time.Second)
				defer cancel()
				response, err := session.CallTool(timeoutCtx, &mcp.CallToolParams{Name: remote.Name, Arguments: args})
				if err != nil {
					return "", err
				}
				output := renderToolResult(response)
				if response.IsError {
					return output, fmt.Errorf("MCP tool returned an error")
				}
				return output, nil
			}})
		}
		cursor = result.NextCursor
		if cursor == "" {
			break
		}
	}
	return registered, nil
}

var invalidName = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func sanitize(value string) string {
	value = invalidName.ReplaceAllString(value, "_")
	if len(value) > 64 {
		value = value[:64]
	}
	return value
}

type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	for key, value := range t.headers {
		if value != "" {
			clone.Header.Set(key, value)
		}
	}
	return t.base.RoundTrip(clone)
}
