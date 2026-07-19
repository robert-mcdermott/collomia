package mcpclient

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// stubRichDial serves tools, resources, and prompts from an in-memory server.
func stubRichDial(t *testing.T) {
	t.Helper()
	prior := dial
	t.Cleanup(func() { dial = prior })
	dial = func(ctx context.Context, name string, cfg appconfig.MCPServer, clientOpts *mcp.ClientOptions) (*mcp.ClientSession, error) {
		server := mcp.NewServer(&mcp.Implementation{Name: "fake-" + name, Version: "9.9.9"}, nil)
		mcp.AddTool(server, &mcp.Tool{Name: "media", Description: "returns mixed content"}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.TextContent{Text: "summary text"},
				&mcp.ImageContent{MIMEType: "image/png", Data: []byte{1, 2, 3}},
				&mcp.ResourceLink{URI: "doc://guide", Name: "guide"},
				&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "doc://embedded", MIMEType: "text/plain", Text: "embedded text"}},
			}}, nil, nil
		})
		server.AddResource(&mcp.Resource{URI: "doc://guide", Name: "guide", Description: "The user guide", MIMEType: "text/markdown"}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "text/markdown", Text: "# Guide\nUse it well."}}}, nil
		})
		server.AddResource(&mcp.Resource{URI: "doc://logo", Name: "logo", MIMEType: "image/png"}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "image/png", Blob: []byte{9, 9}}}}, nil
		})
		server.AddPrompt(&mcp.Prompt{Name: "summarize", Description: "Summarize a document", Arguments: []*mcp.PromptArgument{{Name: "doc", Description: "document uri", Required: true}}}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: "Summarize " + req.Params.Arguments["doc"] + " in three bullets."}}}}, nil
		})
		clientTransport, serverTransport := mcp.NewInMemoryTransports()
		if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
			return nil, err
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "collomia", Version: "test"}, clientOpts)
		return client.Connect(ctx, clientTransport, nil)
	}
}

func richManager(t *testing.T) (*Manager, *tools.Registry) {
	t.Helper()
	stubRichDial(t)
	registry := tools.NewRegistry()
	manager, errs := ConnectAll(t.Context(), map[string]appconfig.MCPServer{"docs": trustedServer()}, registry, testOpts(t))
	t.Cleanup(manager.Close)
	if len(errs) != 0 {
		t.Fatalf("errs=%v", errs)
	}
	return manager, registry
}

func TestResourcesListAndRead(t *testing.T) {
	manager, _ := richManager(t)
	resources, err := manager.Resources(t.Context(), "docs")
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 2 || resources[0].URI != "doc://guide" || resources[0].MIMEType != "text/markdown" {
		t.Fatalf("resources=%+v", resources)
	}
	content, err := manager.ReadResource(t.Context(), "docs", "doc://guide")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "Use it well.") || !strings.Contains(content, "doc://guide") {
		t.Fatalf("content=%q", content)
	}
	binary, err := manager.ReadResource(t.Context(), "docs", "doc://logo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(binary, "binary resource") || !strings.Contains(binary, "image/png") {
		t.Fatalf("binary=%q", binary)
	}
}

func TestPromptsListAndExpand(t *testing.T) {
	manager, _ := richManager(t)
	prompts, err := manager.Prompts(t.Context(), "docs")
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 1 || prompts[0].Name != "summarize" || len(prompts[0].Arguments) != 1 || !prompts[0].Arguments[0].Required {
		t.Fatalf("prompts=%+v", prompts)
	}
	text, err := manager.GetPrompt(t.Context(), "docs", "summarize", map[string]string{"doc": "doc://guide"})
	if err != nil {
		t.Fatal(err)
	}
	if text != "Summarize doc://guide in three bullets." {
		t.Fatalf("text=%q", text)
	}
}

func TestCapabilityErrorsAreActionable(t *testing.T) {
	// The plain stub (tools only) negotiates neither resources nor prompts.
	stubDial(t, "search")
	registry := tools.NewRegistry()
	manager, _ := ConnectAll(t.Context(), map[string]appconfig.MCPServer{"docs": trustedServer()}, registry, testOpts(t))
	defer manager.Close()
	if _, err := manager.Resources(t.Context(), "docs"); err == nil || !strings.Contains(err.Error(), "capability") {
		t.Fatalf("expected capability error, got %v", err)
	}
	if _, err := manager.Prompts(t.Context(), "docs"); err == nil || !strings.Contains(err.Error(), "capability") {
		t.Fatalf("expected capability error, got %v", err)
	}
}

func TestResourceToolsRegisteredAndWorking(t *testing.T) {
	manager, registry := richManager(t)
	_ = manager
	listing, err := registry.Execute(t.Context(), "list_mcp_resources", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Server docs:", "doc://guide", "text/markdown", "The user guide"} {
		if !strings.Contains(listing, want) {
			t.Fatalf("listing missing %q:\n%s", want, listing)
		}
	}
	content, err := registry.Execute(t.Context(), "read_mcp_resource", []byte(`{"server":"docs","uri":"doc://guide"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "Use it well.") {
		t.Fatalf("content=%q", content)
	}
	action, err := registry.Assess("read_mcp_resource", []byte(`{"server":"docs","uri":"doc://guide"}`))
	if err != nil {
		t.Fatal(err)
	}
	if action.Server != "docs" || action.Risk != tools.RiskExternal {
		t.Fatalf("action=%+v", action)
	}
}

func TestRichToolContentIsPreserved(t *testing.T) {
	_, registry := richManager(t)
	out, err := registry.Execute(t.Context(), "mcp_docs_media", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"summary text", "[image image/png, 3 bytes", "[resource link: doc://guide", "read_mcp_resource", "embedded text"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}
