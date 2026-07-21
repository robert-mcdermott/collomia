package mcpclient

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// stubDial replaces the dial seam with an in-memory MCP server exposing the
// given tool names, restoring the real dialer on cleanup.
func stubDial(t *testing.T, toolNames ...string) {
	t.Helper()
	prior := dial
	t.Cleanup(func() { dial = prior })
	dial = func(ctx context.Context, name string, cfg appconfig.MCPServer, clientOpts *mcp.ClientOptions) (*mcp.ClientSession, error) {
		server := mcp.NewServer(&mcp.Implementation{Name: "fake-" + name, Version: "9.9.9"}, nil)
		for _, toolName := range toolNames {
			toolName := toolName
			mcp.AddTool(server, &mcp.Tool{Name: toolName, Description: "fake tool"}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, map[string]any, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ran " + toolName}}}, nil, nil
			})
		}
		clientTransport, serverTransport := mcp.NewInMemoryTransports()
		if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
			return nil, err
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "collomia", Version: "test"}, clientOpts)
		return client.Connect(ctx, clientTransport, nil)
	}
}

func trustedServer() appconfig.MCPServer {
	return appconfig.MCPServer{Transport: "stdio", Command: "fake", Trusted: true, Timeout: 5}
}

func TestConnectAllReportsStatusAndCapabilities(t *testing.T) {
	stubDial(t, "search")
	registry := tools.NewRegistry()
	manager, errs := ConnectAll(t.Context(), map[string]appconfig.MCPServer{
		"docs":     trustedServer(),
		"off":      {Transport: "stdio", Command: "fake", Trusted: true, Disabled: true, Timeout: 5},
		"unvetted": {Transport: "stdio", Command: "fake", Timeout: 5},
	}, registry, testOpts(t))
	defer manager.Close()
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "trusted") {
		t.Fatalf("errs=%v", errs)
	}
	statuses := manager.Statuses()
	if len(statuses) != 3 {
		t.Fatalf("statuses=%+v", statuses)
	}
	byName := map[string]ServerStatus{}
	for _, s := range statuses {
		byName[s.Name] = s
	}
	docs := byName["docs"]
	if docs.Status != StatusConnected || docs.ServerName != "fake-docs" || docs.ServerVersion != "9.9.9" {
		t.Fatalf("docs=%+v", docs)
	}
	if len(docs.Capabilities) == 0 || docs.Capabilities[0] != "tools" {
		t.Fatalf("capabilities=%v", docs.Capabilities)
	}
	if len(docs.Tools) != 1 || docs.Tools[0] != "mcp_docs_search" {
		t.Fatalf("tools=%v", docs.Tools)
	}
	if byName["off"].Status != StatusDisabled {
		t.Fatalf("off=%+v", byName["off"])
	}
	if byName["unvetted"].Status != StatusUntrusted {
		t.Fatalf("unvetted=%+v", byName["unvetted"])
	}
	if _, ok := registry.Get("mcp_docs_search"); !ok {
		t.Fatal("tool not registered")
	}
}

func TestDisableWithdrawsToolsAndEnableRestores(t *testing.T) {
	stubDial(t, "search")
	registry := tools.NewRegistry()
	manager, _ := ConnectAll(t.Context(), map[string]appconfig.MCPServer{"docs": trustedServer()}, registry, testOpts(t))
	defer manager.Close()
	if err := manager.SetEnabled(t.Context(), "docs", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get("mcp_docs_search"); ok {
		t.Fatal("disable should withdraw the server's tools")
	}
	if servers := manager.Servers(); len(servers) != 0 {
		t.Fatalf("servers=%v", servers)
	}
	if err := manager.SetEnabled(t.Context(), "docs", true); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get("mcp_docs_search"); !ok {
		t.Fatal("enable should restore the server's tools")
	}
}

func TestEnableCannotOverrideMissingTrust(t *testing.T) {
	stubDial(t, "search")
	registry := tools.NewRegistry()
	manager, _ := ConnectAll(t.Context(), map[string]appconfig.MCPServer{"unvetted": {Transport: "stdio", Command: "fake", Timeout: 5}}, registry, testOpts(t))
	defer manager.Close()
	if err := manager.SetEnabled(t.Context(), "unvetted", true); err == nil || !strings.Contains(err.Error(), "untrusted") {
		t.Fatalf("expected trust refusal, got %v", err)
	}
}

func TestReconnectRefreshesToolCatalog(t *testing.T) {
	stubDial(t, "search")
	registry := tools.NewRegistry()
	manager, _ := ConnectAll(t.Context(), map[string]appconfig.MCPServer{"docs": trustedServer()}, registry, testOpts(t))
	defer manager.Close()
	// The server's catalog changes between connections.
	stubDial(t, "search", "fetch")
	if err := manager.Reconnect(t.Context(), "docs"); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get("mcp_docs_fetch"); !ok {
		t.Fatal("reconnect should register newly offered tools")
	}
	statuses := manager.Statuses()
	if len(statuses) != 1 || len(statuses[0].Tools) != 2 {
		t.Fatalf("statuses=%+v", statuses)
	}
	out, err := registry.Execute(t.Context(), "mcp_docs_fetch", []byte(`{}`))
	if err != nil || !strings.Contains(out, "ran fetch") {
		t.Fatalf("execute after reconnect: %q %v", out, err)
	}
}

func TestRuntimeAddAndRemove(t *testing.T) {
	stubDial(t, "lookup")
	registry := tools.NewRegistry()
	manager, _ := ConnectAll(t.Context(), nil, registry, testOpts(t))
	defer manager.Close()
	if err := manager.Add(t.Context(), "adhoc", appconfig.MCPServer{Transport: "stdio", Command: "fake"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Add(t.Context(), "adhoc", appconfig.MCPServer{Transport: "stdio", Command: "fake"}); err == nil {
		t.Fatal("duplicate add should fail")
	}
	if err := manager.Add(t.Context(), "bad name!", appconfig.MCPServer{}); err == nil {
		t.Fatal("invalid name should fail")
	}
	statuses := manager.Statuses()
	if len(statuses) != 1 || !statuses[0].Runtime || statuses[0].Status != StatusConnected {
		t.Fatalf("statuses=%+v", statuses)
	}
	if _, ok := registry.Get("mcp_adhoc_lookup"); !ok {
		t.Fatal("runtime server's tools should register")
	}
	if err := manager.Remove("adhoc"); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get("mcp_adhoc_lookup"); ok {
		t.Fatal("remove should withdraw tools")
	}
	if len(manager.Statuses()) != 0 {
		t.Fatal("remove should forget the server")
	}
}

func TestDiagnosticConnectionCanDisablePersistentPinning(t *testing.T) {
	stubDial(t, "lookup")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	manager, errs := ConnectAll(t.Context(), map[string]appconfig.MCPServer{"docs": trustedServer()}, tools.NewRegistry(), Options{
		Workspace:      t.TempDir(),
		DisablePinning: true,
	})
	defer manager.Close()
	if len(errs) != 0 {
		t.Fatalf("connect errors=%v", errs)
	}
	if err := manager.Ping(t.Context(), "docs"); err != nil {
		t.Fatal(err)
	}
	path, err := pinPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("diagnostic wrote pin store %s: %v", path, err)
	}
}

func TestPingHealthCheck(t *testing.T) {
	stubDial(t, "search")
	registry := tools.NewRegistry()
	manager, _ := ConnectAll(t.Context(), map[string]appconfig.MCPServer{"docs": trustedServer()}, registry, testOpts(t))
	defer manager.Close()
	if err := manager.Ping(t.Context(), "docs"); err != nil {
		t.Fatalf("ping healthy server: %v", err)
	}
	if err := manager.Ping(t.Context(), "ghost"); err == nil {
		t.Fatal("ping of unknown server should fail")
	}
	// Kill the transport behind the manager's back; ping must notice and
	// record the failure.
	manager.mu.Lock()
	_ = manager.servers["docs"].session.Close()
	manager.mu.Unlock()
	if err := manager.Ping(t.Context(), "docs"); err == nil {
		t.Fatal("ping of dead session should fail")
	}
	statuses := manager.Statuses()
	if statuses[0].Status != StatusError || !strings.Contains(statuses[0].Err, "ping") {
		t.Fatalf("statuses=%+v", statuses)
	}
	if err := manager.Reconnect(t.Context(), "docs"); err != nil {
		t.Fatalf("reconnect after failed ping: %v", err)
	}
	if manager.Statuses()[0].Status != StatusConnected {
		t.Fatalf("statuses=%+v", manager.Statuses())
	}
}

func TestFailedConnectIsRetainedAsError(t *testing.T) {
	prior := dial
	t.Cleanup(func() { dial = prior })
	dial = func(context.Context, string, appconfig.MCPServer, *mcp.ClientOptions) (*mcp.ClientSession, error) {
		return nil, fmt.Errorf("connection refused")
	}
	registry := tools.NewRegistry()
	manager, errs := ConnectAll(t.Context(), map[string]appconfig.MCPServer{"down": trustedServer()}, registry, testOpts(t))
	defer manager.Close()
	if len(errs) != 1 {
		t.Fatalf("errs=%v", errs)
	}
	statuses := manager.Statuses()
	if len(statuses) != 1 || statuses[0].Status != StatusError || !strings.Contains(statuses[0].Err, "connection refused") {
		t.Fatalf("statuses=%+v", statuses)
	}
	// The failure is repairable in place once the server is back.
	stubDial(t, "search")
	if err := manager.Reconnect(t.Context(), "down"); err != nil {
		t.Fatal(err)
	}
	if manager.Statuses()[0].Status != StatusConnected {
		t.Fatalf("statuses=%+v", manager.Statuses())
	}
}
