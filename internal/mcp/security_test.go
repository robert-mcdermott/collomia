package mcpclient

import (
	"context"
	"fmt"
	"sort"
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
	if err != nil || !strings.Contains(out, "done") || !strings.Contains(out, "BEGIN COLLOMIA_EXTERNAL_MCP_DATA_") {
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
	if out, err := registry.Execute(t.Context(), "mcp_docs_long", []byte(`{}`)); err != nil || !strings.Contains(out, "done") || !strings.Contains(out, "BEGIN COLLOMIA_EXTERNAL_MCP_DATA_") {
		t.Fatalf("execute out=%q err=%v", out, err)
	}
}

// elicitingDial serves one tool that asks the user a question, in the style
// of MCP 2026-07-28: the tool returns an input request and reads the answer
// when the client calls again.
func elicitingDial(t *testing.T, schema map[string]any) { elicitingDialStyle(t, schema, false) }

// elicitingDialStyle serves the same tool either way. legacy pins the server
// to protocol 2025-11-25, where a tool elicits by sending elicitation/create
// while it runs — which is what every server built on an older SDK still
// does, and must keep working.
func elicitingDialStyle(t *testing.T, schema map[string]any, legacy bool) {
	t.Helper()
	prior := dial
	t.Cleanup(func() { dial = prior })
	describe := func(result *mcp.ElicitResult) *mcp.CallToolResult {
		text := "action=" + result.Action
		if result.Action == "accept" {
			keys := make([]string, 0, len(result.Content))
			for key := range result.Content {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				text += fmt.Sprintf(" %s=%v", key, result.Content[key])
			}
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
	}
	dial = func(ctx context.Context, name string, cfg appconfig.MCPServer, clientOpts *mcp.ClientOptions) (*mcp.ClientSession, error) {
		serverOpts := &mcp.ServerOptions{}
		if legacy {
			serverOpts.SupportedProtocolVersions = []string{"2025-11-25"}
		}
		server := mcp.NewServer(&mcp.Implementation{Name: "fake-" + name, Version: "1.0"}, serverOpts)
		params := &mcp.ElicitParams{Message: "Which region?", RequestedSchema: schema}
		mcp.AddTool(server, &mcp.Tool{Name: "ask", Description: "asks the user"}, func(callCtx context.Context, req *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			if legacy {
				result, err := req.Session.Elicit(callCtx, params)
				if err != nil {
					return nil, nil, err
				}
				return describe(result), nil, nil
			}
			answer, ok := req.Params.InputResponses["region"]
			if !ok {
				return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{"region": params}, RequestState: "asked"}, nil, nil
			}
			return describe(answer.(*mcp.ElicitResult)), nil, nil
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
	if !strings.Contains(out, "action=accept region=us-west-2") || !strings.Contains(out, "BEGIN COLLOMIA_EXTERNAL_MCP_DATA_") {
		t.Fatalf("out=%q", out)
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "External MCP server docs asks: Which region?") || !strings.Contains(asked[0], "region") {
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
	if !strings.Contains(out, "action=decline") || !strings.Contains(out, "BEGIN COLLOMIA_EXTERNAL_MCP_DATA_") {
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

func TestElicitationStillWorksWithOlderServers(t *testing.T) {
	// Servers built on older SDKs negotiate 2025-11-25 and elicit by sending
	// elicitation/create mid-call. The upgrade must not break them.
	elicitingDialStyle(t, map[string]any{
		"type":       "object",
		"properties": map[string]any{"region": map[string]any{"type": "string"}},
		"required":   []any{"region"},
	}, true)
	opts := testOpts(t)
	opts.Asker = func(context.Context, string, []string) (string, error) { return "eu-west-1", nil }
	registry := tools.NewRegistry()
	manager, errs := ConnectAll(t.Context(), map[string]appconfig.MCPServer{"docs": trustedServer()}, registry, opts)
	defer manager.Close()
	if len(errs) != 0 {
		t.Fatalf("errs=%v", errs)
	}
	if protocol := manager.Statuses()[0].Protocol; protocol != "2025-11-25" {
		t.Fatalf("protocol=%q, want the older server's 2025-11-25", protocol)
	}
	out, err := registry.Execute(t.Context(), "mcp_docs_ask", []byte(`{}`))
	if err != nil || !strings.Contains(out, "action=accept region=eu-west-1") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestConcurrentElicitationsAskOneAtATime(t *testing.T) {
	// Under 2026-07-28 a server can ask for several inputs at once, and the
	// SDK fulfills them concurrently. Each elicitation is a sequence of
	// questions in one dialog, so they must not interleave.
	prior := dial
	t.Cleanup(func() { dial = prior })
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"a": map[string]any{"type": "string"}, "b": map[string]any{"type": "string"}}}
	dial = func(ctx context.Context, name string, cfg appconfig.MCPServer, clientOpts *mcp.ClientOptions) (*mcp.ClientSession, error) {
		server := mcp.NewServer(&mcp.Implementation{Name: "fake-" + name, Version: "1.0"}, nil)
		mcp.AddTool(server, &mcp.Tool{Name: "ask", Description: "asks twice"}, func(_ context.Context, req *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			if len(req.Params.InputResponses) == 0 {
				return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{
					"first":  &mcp.ElicitParams{Message: "first", RequestedSchema: schema},
					"second": &mcp.ElicitParams{Message: "second", RequestedSchema: schema},
				}, RequestState: "both"}, nil, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("answered=%d", len(req.Params.InputResponses))}}}, nil, nil
		})
		clientTransport, serverTransport := mcp.NewInMemoryTransports()
		if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
			return nil, err
		}
		return mcp.NewClient(&mcp.Implementation{Name: "collomia", Version: "test"}, clientOpts).Connect(ctx, clientTransport, nil)
	}
	var mu sync.Mutex
	var order []string
	opts := testOpts(t)
	opts.Asker = func(_ context.Context, question string, _ []string) (string, error) {
		mu.Lock()
		order = append(order, question)
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		return "x", nil
	}
	registry := tools.NewRegistry()
	manager, errs := ConnectAll(t.Context(), map[string]appconfig.MCPServer{"docs": trustedServer()}, registry, opts)
	defer manager.Close()
	if len(errs) != 0 {
		t.Fatalf("errs=%v", errs)
	}
	out, err := registry.Execute(t.Context(), "mcp_docs_ask", []byte(`{}`))
	if err != nil || !strings.Contains(out, "answered=2") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if len(order) != 4 {
		t.Fatalf("questions=%v", order)
	}
	// Each request's two questions must be adjacent.
	for i := 0; i < 4; i += 2 {
		first := strings.Contains(order[i], "asks: first")
		if first != strings.Contains(order[i+1], "asks: first") {
			t.Fatalf("questions from different requests interleaved: %v", order)
		}
	}
}
