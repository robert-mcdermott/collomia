package mcpclient

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// dynamicFixture connects Collomia to a capability-rich in-memory server and
// returns the server so tests can mutate its catalogs after initialization.
func dynamicFixture(t *testing.T) (*Manager, *tools.Registry, *mcp.Server) {
	t.Helper()
	prior := dial
	t.Cleanup(func() { dial = prior })

	var server *mcp.Server
	dial = func(ctx context.Context, name string, _ appconfig.MCPServer, clientOpts *mcp.ClientOptions) (*mcp.ClientSession, error) {
		server = mcp.NewServer(&mcp.Implementation{Name: "conformance-" + name, Version: "1.0.0"}, nil)
		addFixtureTool(server, "alpha")
		addFixtureResource(server, "doc://one")
		addFixturePrompt(server, "first")
		clientTransport, serverTransport := mcp.NewInMemoryTransports()
		if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
			return nil, err
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "collomia", Version: "test"}, clientOpts)
		return client.Connect(ctx, clientTransport, nil)
	}

	registry := tools.NewRegistry()
	manager, errs := ConnectAll(t.Context(), map[string]appconfig.MCPServer{"fixture": trustedServer()}, registry, testOpts(t))
	if len(errs) != 0 {
		t.Fatalf("connect fixture: %v", errs)
	}
	t.Cleanup(manager.Close)
	return manager, registry, server
}

func addFixtureTool(server *mcp.Server, name string) {
	mcp.AddTool(server, &mcp.Tool{Name: name, Description: "fixture tool"}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ran " + name}}}, nil, nil
	})
}

func addFixtureResource(server *mcp.Server, uri string) {
	server.AddResource(&mcp.Resource{URI: uri, Name: uri}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, Text: req.Params.URI}}}, nil
	})
}

func addFixturePrompt(server *mcp.Server, name string) {
	server.AddPrompt(&mcp.Prompt{Name: name}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: req.Params.Name}}}}, nil
	})
}

func waitFor(t *testing.T, description string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func TestProtocolConformanceAndCatalogListChanges(t *testing.T) {
	manager, registry, server := dynamicFixture(t)
	status := manager.Statuses()[0]
	if status.Protocol != "2025-11-25" {
		t.Fatalf("negotiated protocol=%q, want 2025-11-25", status.Protocol)
	}
	for _, capability := range []string{"tools", "resources", "prompts", "logging"} {
		if !containsString(status.Capabilities, capability) {
			t.Fatalf("capabilities=%v, missing %s", status.Capabilities, capability)
		}
	}
	if got := strings.Join(status.ListChanges, ","); got != "prompts,resources,tools" {
		t.Fatalf("list-change capabilities=%q", got)
	}

	server.RemoveTools("alpha")
	addFixtureTool(server, "beta")
	addFixtureResource(server, "doc://two")
	addFixturePrompt(server, "second")

	waitFor(t, "automatic tool catalog refresh", func() bool {
		_, old := registry.Get("mcp_fixture_alpha")
		_, next := registry.Get("mcp_fixture_beta")
		return !old && next
	})
	waitFor(t, "all catalog notifications", func() bool {
		status := manager.Statuses()[0]
		return status.CatalogRevision >= 3 && strings.Join(status.PendingCatalogs, ",") == "prompts,resources"
	})

	resources, err := manager.Resources(t.Context(), "fixture")
	if err != nil || len(resources) != 2 {
		t.Fatalf("resources after notification=%+v, %v", resources, err)
	}
	prompts, err := manager.Prompts(t.Context(), "fixture")
	if err != nil || len(prompts) != 2 {
		t.Fatalf("prompts after notification=%+v, %v", prompts, err)
	}
	if pending := manager.Statuses()[0].PendingCatalogs; len(pending) != 0 {
		t.Fatalf("pending catalogs after live reads=%v", pending)
	}
	out, err := registry.Execute(t.Context(), "mcp_fixture_beta", []byte(`{}`))
	if err != nil || out != "ran beta" {
		t.Fatalf("refreshed tool result=%q, %v", out, err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestFailedCatalogRefreshPreservesLastKnownGoodTools(t *testing.T) {
	manager, registry, server := dynamicFixture(t)
	longPrefix := strings.Repeat("a", 70)
	addFixtureTool(server, longPrefix+"x")
	addFixtureTool(server, longPrefix+"y")

	waitFor(t, "catalog refresh error", func() bool {
		return manager.Statuses()[0].CatalogError != ""
	})
	if _, ok := registry.Get("mcp_fixture_alpha"); !ok {
		t.Fatal("failed refresh withdrew the last known-good tool")
	}
	status := manager.Statuses()[0]
	if !strings.Contains(status.CatalogError, "both map") || strings.Join(status.PendingCatalogs, ",") != "tools" {
		t.Fatalf("status after failed refresh=%+v", status)
	}

	server.RemoveTools(longPrefix + "y")
	waitFor(t, "catalog recovery", func() bool {
		status := manager.Statuses()[0]
		return status.CatalogError == "" && len(status.PendingCatalogs) == 0 && len(status.Tools) == 2
	})
}

func TestListChangeFromSupersededSessionIsIgnored(t *testing.T) {
	manager, registry, _ := dynamicFixture(t)
	manager.mu.Lock()
	oldGeneration := manager.servers["fixture"].generation
	manager.mu.Unlock()
	if err := manager.Reconnect(t.Context(), "fixture"); err != nil {
		t.Fatal(err)
	}
	manager.catalogChanged("fixture", "tools", oldGeneration)
	status := manager.Statuses()[0]
	if status.CatalogRevision != 0 || len(status.PendingCatalogs) != 0 {
		t.Fatalf("stale notification changed status: %+v", status)
	}
	if _, ok := registry.Get("mcp_fixture_alpha"); !ok {
		t.Fatal("stale notification disturbed the active registry")
	}
}
