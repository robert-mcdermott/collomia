package mcpclient

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// testOpts isolates the pin store in a temporary HOME and pins the manager
// to a stable temporary workspace.
func testOpts(t *testing.T) Options {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return Options{Workspace: t.TempDir()}
}

func TestPinDetectsDefinitionAndIdentityChanges(t *testing.T) {
	stubDial(t, "search")
	registry := tools.NewRegistry()
	opts := testOpts(t)
	cfg := map[string]appconfig.MCPServer{"docs": trustedServer()}
	manager, errs := ConnectAll(t.Context(), cfg, registry, opts)
	if len(errs) != 0 {
		t.Fatalf("first connect should be quiet, got %v", errs)
	}
	manager.Close()
	// Same definition, same identity: still quiet.
	manager, errs = ConnectAll(t.Context(), cfg, registry, opts)
	if len(errs) != 0 {
		t.Fatalf("unchanged reconnect should be quiet, got %v", errs)
	}
	manager.Close()
	// Changed command: definition fingerprint mismatch is reported once.
	changed := map[string]appconfig.MCPServer{"docs": {Transport: "stdio", Command: "evil-binary", Trusted: true, Timeout: 5}}
	manager, errs = ConnectAll(t.Context(), changed, registry, opts)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "definition") {
		t.Fatalf("expected definition warning, got %v", errs)
	}
	manager.Close()
	// Remote identity swap behind an unchanged definition is also reported.
	prior := dial
	t.Cleanup(func() { dial = prior })
	dial = func(ctx context.Context, name string, cfg appconfig.MCPServer, clientOpts *mcp.ClientOptions) (*mcp.ClientSession, error) {
		server := mcp.NewServer(&mcp.Implementation{Name: "impostor", Version: "0.0.1"}, nil)
		mcp.AddTool(server, &mcp.Tool{Name: "search", Description: "fake"}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "x"}}}, nil, nil
		})
		clientTransport, serverTransport := mcp.NewInMemoryTransports()
		if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
			return nil, err
		}
		return mcp.NewClient(&mcp.Implementation{Name: "collomia", Version: "test"}, clientOpts).Connect(ctx, clientTransport, nil)
	}
	manager, errs = ConnectAll(t.Context(), changed, registry, opts)
	defer manager.Close()
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "impostor") {
		t.Fatalf("expected identity warning, got %v", errs)
	}
}

func TestRuntimeServersAreNotPinned(t *testing.T) {
	stubDial(t, "lookup")
	registry := tools.NewRegistry()
	opts := testOpts(t)
	manager, _ := ConnectAll(t.Context(), nil, registry, opts)
	defer manager.Close()
	if err := manager.Add(t.Context(), "adhoc", appconfig.MCPServer{Transport: "stdio", Command: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove("adhoc"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Add(t.Context(), "adhoc", appconfig.MCPServer{Transport: "stdio", Command: "two"}); err != nil {
		t.Fatal(err)
	}
	if notes := manager.TakeNotes(); len(notes) != 0 {
		t.Fatalf("session-scoped servers should not produce pin notes, got %v", notes)
	}
}

func TestProgressNotificationsStream(t *testing.T) {
	prior := dial
	t.Cleanup(func() { dial = prior })
	dial = func(ctx context.Context, name string, cfg appconfig.MCPServer, clientOpts *mcp.ClientOptions) (*mcp.ClientSession, error) {
		server := mcp.NewServer(&mcp.Implementation{Name: "fake-" + name, Version: "1.0"}, nil)
		mcp.AddTool(server, &mcp.Tool{Name: "long", Description: "reports progress"}, func(callCtx context.Context, req *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			token := req.Params.GetProgressToken()
			for step := 1; step <= 2; step++ {
				_ = req.Session.NotifyProgress(callCtx, &mcp.ProgressNotificationParams{ProgressToken: token, Progress: float64(step), Total: 2, Message: fmt.Sprintf("step %d", step)})
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, nil, nil
		})
		clientTransport, serverTransport := mcp.NewInMemoryTransports()
		if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
			return nil, err
		}
		return mcp.NewClient(&mcp.Implementation{Name: "collomia", Version: "test"}, clientOpts).Connect(ctx, clientTransport, nil)
	}
	registry := tools.NewRegistry()
	manager, errs := ConnectAll(t.Context(), map[string]appconfig.MCPServer{"docs": trustedServer()}, registry, testOpts(t))
	defer manager.Close()
	if len(errs) != 0 {
		t.Fatalf("errs=%v", errs)
	}
	// Progress notifications arrive on the SDK's handler goroutine and may
	// land after CallTool returns, so the sink must be synchronized and the
	// assertions must wait for delivery instead of reading immediately.
	var mu sync.Mutex
	var streamed []string
	out, err := registry.ExecuteStream(t.Context(), "mcp_docs_long", []byte(`{}`), func(chunk string) {
		mu.Lock()
		streamed = append(streamed, chunk)
		mu.Unlock()
	})
	if err != nil || out != "done" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	wanted := []string{"progress: 1/2 — step 1", "progress: 2/2 — step 2"}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		joined := strings.Join(streamed, "")
		mu.Unlock()
		missing := ""
		for _, want := range wanted {
			if !strings.Contains(joined, want) {
				missing = want
				break
			}
		}
		if missing == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("streamed output missing %q:\n%s", missing, joined)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Plain Execute (no stream sink) still works and stays silent.
	if out, err := registry.Execute(t.Context(), "mcp_docs_long", []byte(`{}`)); err != nil || out != "done" {
		t.Fatalf("execute out=%q err=%v", out, err)
	}
}

func elicitingDial(t *testing.T, schema map[string]any) {
	t.Helper()
	prior := dial
	t.Cleanup(func() { dial = prior })
	dial = func(ctx context.Context, name string, cfg appconfig.MCPServer, clientOpts *mcp.ClientOptions) (*mcp.ClientSession, error) {
		server := mcp.NewServer(&mcp.Implementation{Name: "fake-" + name, Version: "1.0"}, nil)
		mcp.AddTool(server, &mcp.Tool{Name: "ask", Description: "asks the user"}, func(callCtx context.Context, req *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			result, err := req.Session.Elicit(callCtx, &mcp.ElicitParams{Message: "Which region?", RequestedSchema: schema})
			if err != nil {
				return nil, nil, err
			}
			text := "action=" + result.Action
			if result.Action == "accept" {
				for key, value := range result.Content {
					text += fmt.Sprintf(" %s=%v", key, value)
				}
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
		})
		clientTransport, serverTransport := mcp.NewInMemoryTransports()
		if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
			return nil, err
		}
		return mcp.NewClient(&mcp.Implementation{Name: "collomia", Version: "test"}, clientOpts).Connect(ctx, clientTransport, nil)
	}
}

func TestElicitationAsksTheUser(t *testing.T) {
	elicitingDial(t, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"region": map[string]any{"type": "string", "description": "deployment region"},
		},
		"required": []any{"region"},
	})
	opts := testOpts(t)
	var asked []string
	opts.Asker = func(_ context.Context, question string, options []string) (string, error) {
		asked = append(asked, question)
		return "us-west-2", nil
	}
	registry := tools.NewRegistry()
	manager, errs := ConnectAll(t.Context(), map[string]appconfig.MCPServer{"docs": trustedServer()}, registry, opts)
	defer manager.Close()
	if len(errs) != 0 {
		t.Fatalf("errs=%v", errs)
	}
	out, err := registry.Execute(t.Context(), "mcp_docs_ask", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != "action=accept region=us-west-2" {
		t.Fatalf("out=%q", out)
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "MCP server docs asks: Which region?") || !strings.Contains(asked[0], "region") {
		t.Fatalf("asked=%v", asked)
	}
}

func TestElicitationDeclinedWhenUserCancels(t *testing.T) {
	elicitingDial(t, map[string]any{
		"type":       "object",
		"properties": map[string]any{"region": map[string]any{"type": "string"}},
		"required":   []any{"region"},
	})
	opts := testOpts(t)
	opts.Asker = func(context.Context, string, []string) (string, error) {
		return "", fmt.Errorf("declined")
	}
	registry := tools.NewRegistry()
	manager, _ := ConnectAll(t.Context(), map[string]appconfig.MCPServer{"docs": trustedServer()}, registry, opts)
	defer manager.Close()
	out, err := registry.Execute(t.Context(), "mcp_docs_ask", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != "action=decline" {
		t.Fatalf("out=%q", out)
	}
}

func TestElicitationWithoutAskerIsNotAdvertised(t *testing.T) {
	// Headless: no Asker. The client must not advertise elicitation, so the
	// server's Elicit call fails and the tool reports an error instead of
	// hanging or silently accepting.
	elicitingDial(t, map[string]any{"type": "object", "properties": map[string]any{}})
	registry := tools.NewRegistry()
	manager, _ := ConnectAll(t.Context(), map[string]appconfig.MCPServer{"docs": trustedServer()}, registry, testOpts(t))
	defer manager.Close()
	if _, err := registry.Execute(t.Context(), "mcp_docs_ask", []byte(`{}`)); err == nil {
		t.Fatal("elicitation without an asker should surface an error")
	}
}
