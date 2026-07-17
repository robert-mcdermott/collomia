package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

type Manager struct {
	mu       sync.Mutex
	sessions []*mcp.ClientSession
	servers  []string
}

func ConnectAll(ctx context.Context, configured map[string]appconfig.MCPServer, registry *tools.Registry) (*Manager, []error) {
	manager := &Manager{}
	var errs []error
	for name, cfg := range configured {
		if cfg.Disabled {
			continue
		}
		if !cfg.Trusted {
			errs = append(errs, fmt.Errorf("MCP %s: not started because trusted is false; review the server and set trusted to true", name))
			continue
		}
		session, err := connect(ctx, name, cfg)
		if err != nil {
			errs = append(errs, fmt.Errorf("MCP %s: %w", name, err))
			continue
		}
		manager.sessions = append(manager.sessions, session)
		manager.servers = append(manager.servers, name)
		if err = registerTools(ctx, name, cfg, session, registry); err != nil {
			_ = session.Close()
			manager.sessions = manager.sessions[:len(manager.sessions)-1]
			manager.servers = manager.servers[:len(manager.servers)-1]
			errs = append(errs, fmt.Errorf("MCP %s tools: %w", name, err))
		}
	}
	return manager, errs
}

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

func registerTools(ctx context.Context, server string, cfg appconfig.MCPServer, session *mcp.ClientSession, registry *tools.Registry) error {
	listCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Timeout)*time.Second)
	defer cancel()
	cursor := ""
	for {
		result, err := session.ListTools(listCtx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return err
		}
		for _, remote := range result.Tools {
			remote := remote
			publicName := sanitize("mcp_" + server + "_" + remote.Name)
			schema, err := json.Marshal(remote.InputSchema)
			if err != nil {
				return err
			}
			if string(schema) == "null" {
				schema = []byte(`{"type":"object"}`)
			}
			registry.Add(tools.Function{Def: provider.ToolDefinition{Name: publicName, Description: fmt.Sprintf("MCP server %s tool %s. %s", server, remote.Name, remote.Description), InputSchema: schema}, Action: tools.Action{Risk: tools.RiskExternal, Summary: "call MCP tool " + server + "/" + remote.Name}, Run: func(callCtx context.Context, raw json.RawMessage) (string, error) {
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
				data, err := json.Marshal(response)
				if err != nil {
					return "", err
				}
				var wire struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
					Structured any  `json:"structuredContent"`
					IsError    bool `json:"isError"`
				}
				if err = json.Unmarshal(data, &wire); err != nil {
					return "", err
				}
				var parts []string
				for _, part := range wire.Content {
					if part.Type == "text" && part.Text != "" {
						parts = append(parts, part.Text)
					}
				}
				if wire.Structured != nil {
					value, _ := json.MarshalIndent(wire.Structured, "", "  ")
					parts = append(parts, string(value))
				}
				output := strings.Join(parts, "\n")
				if output == "" {
					output = string(data)
				}
				if wire.IsError {
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
	return nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, session := range m.sessions {
		_ = session.Close()
	}
	m.sessions = nil
}
func (m *Manager) Servers() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.servers...)
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
