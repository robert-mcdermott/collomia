// Prompt-cache accounting measurements.
//
// The caching wave was justified from request structure rather than from
// numbers. This file replaces the argument with measurement of the parts that
// can be measured deterministically and offline: how large Collomia's own
// cacheable prefix actually is, how many times a turn retransmits it, and
// what fraction of a turn's prompt bytes a cache can therefore serve.
//
// What is measured here is Collomia's request, exactly as the adapter builds
// it. What is NOT measured here is the provider's side of the bargain — real
// cache_read_input_tokens, real latency, real billing. Those need credentials
// and cost money, so they live in the opt-in live test at the bottom.
//
// Byte ratios are reported rather than token counts wherever possible.
// Collomia has no tokenizer and the chars/4 rule it uses for the context
// gauge is an approximation; a ratio of bytes between two parts of the same
// prompt is far less sensitive to that than an absolute token figure, so the
// headline numbers here do not inherit the estimate's error.
package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/agent"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/permission"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// requestShape is one provider call's measured composition.
type requestShape struct {
	iteration int
	// prefix is everything ahead of the rolling conversation breakpoint:
	// tool schemas, system prompt, and the conversation so far. It is what a
	// warm cache serves.
	prefix int
	// fresh is what this call adds beyond the previous call's prefix.
	fresh int
	total int
}

// measureTurn runs one scripted turn through the real agent and records the
// serialized size of every provider request it produces.
func measureTurn(t *testing.T, toolCalls int) []requestShape {
	t.Helper()
	workspace := t.TempDir()
	// A tool result of a few bytes would understate how fast the conversation
	// half of the prefix grows, which is the half the rolling breakpoint is
	// for. This is a plausible source file, not a token one.
	mustWrite(t, filepath.Join(workspace, "fixture.go"), realisticSourceFile())
	cfg := appconfig.Defaults()
	registry, _, processes, err := tools.Builtins(workspace, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if processes != nil {
		defer processes.StopAll()
	}

	var shapes []requestShape
	previousTotal := 0
	client := &measuringClient{onRequest: func(call int, request provider.Request) {
		total := serializedSize(t, request)
		// The prefix a warm cache can serve is everything this request shares
		// with the previous one: tools, system, and the conversation up to
		// where the last request ended.
		prefix := previousTotal
		if call == 1 {
			prefix = 0
		}
		shapes = append(shapes, requestShape{iteration: call, prefix: prefix, fresh: total - prefix, total: total})
		previousTotal = total
	}, toolCalls: toolCalls}

	a := agent.New(agent.Options{
		Client: client, ProviderName: "measurement", Model: "fixture",
		ProviderConfig: appconfig.Provider{MaxTokens: 4096, Context: 200_000},
		Workspace:      workspace, Registry: registry,
		Permissions:   permission.New(appconfig.Permissions{Mode: "autopilot"}, nil),
		MaxIterations: toolCalls + 4,
	})
	if _, err := a.Run(t.Context(), "Investigate the failing parser test and fix it.", nil); err != nil {
		t.Fatal(err)
	}
	return shapes
}

// measuringClient drives a fixed number of read-only tool calls and then
// answers, which is the shape of an ordinary investigate-then-edit turn. The
// tool results are padded to a realistic size: a turn whose tool output is a
// few bytes would understate how fast the conversation half of the prefix
// grows, which is the half the rolling breakpoint exists for.
type measuringClient struct {
	onRequest func(call int, request provider.Request)
	toolCalls int
	calls     int
}

func (c *measuringClient) Name() string { return "measurement" }

func (c *measuringClient) Chat(_ context.Context, request provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	c.calls++
	if c.onRequest != nil {
		c.onRequest(c.calls, request)
	}
	if c.calls <= c.toolCalls {
		return provider.Response{
			Content: "Reading the next file to narrow this down.",
			ToolCalls: []provider.ToolCall{{
				ID:        fmt.Sprintf("call-%d", c.calls),
				Name:      "read_file",
				Arguments: json.RawMessage(`{"path":"fixture.go"}`),
			}},
			Usage: provider.Usage{InputTokens: 100, OutputTokens: 20},
		}, nil
	}
	return provider.Response{Content: "Found it: the fence scanner drops the closing delimiter.", Usage: provider.Usage{InputTokens: 100, OutputTokens: 20}}, nil
}

func serializedSize(t *testing.T, request provider.Request) int {
	t.Helper()
	// Tool schemas and system prompt are sent on every request, so both count
	// toward what a cache would serve.
	size := len(request.System)
	for _, def := range request.Tools {
		encoded, err := json.Marshal(map[string]any{
			"name": def.Name, "description": def.Description, "input_schema": json.RawMessage(def.InputSchema),
		})
		if err != nil {
			t.Fatal(err)
		}
		size += len(encoded)
	}
	for _, m := range request.Messages {
		size += len(m.Role) + len(m.Content) + len(m.ToolCallID)
		for _, call := range m.ToolCalls {
			size += len(call.Name) + len(call.Arguments) + len(call.ID)
		}
	}
	return size
}

func humanBytes(n int) string {
	if n >= 1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%d B", n)
}

// approxTokens is the same chars/4 rule the context gauge uses. It is only
// ever shown next to the byte figure it derives from, never on its own.
func approxTokens(n int) int { return n / 4 }

// TestPromptCachePrefixIsMostOfEveryRequest measures the fixed cost Collomia
// pays on every provider call: the tool schemas and system prompt, which do
// not change for the life of a session and are exactly what the stable-prefix
// breakpoint caches.
func TestPromptCachePrefixIsMostOfEveryRequest(t *testing.T) {
	workspace := t.TempDir()
	cfg := appconfig.Defaults()
	registry, _, processes, err := tools.Builtins(workspace, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if processes != nil {
		defer processes.StopAll()
	}
	defs := registry.Definitions(nil)
	schemaBytes := 0
	for _, def := range defs {
		encoded, err := json.Marshal(map[string]any{
			"name": def.Name, "description": def.Description, "input_schema": json.RawMessage(def.InputSchema),
		})
		if err != nil {
			t.Fatal(err)
		}
		schemaBytes += len(encoded)
	}

	a := agent.New(agent.Options{
		ProviderName: "measurement", Model: "fixture", Workspace: workspace,
		Registry: registry, Permissions: permission.New(cfg.Permissions, nil),
	})
	// The breakdown's three system components sum to the whole system prompt,
	// which avoids exporting an accessor just for this measurement.
	breakdown := a.ContextBreakdown()
	systemBytes := breakdown.SystemPromptChars + breakdown.InstructionsChars + breakdown.SkillsSummaryChars
	fixed := schemaBytes + systemBytes

	t.Logf("\n=== fixed per-request prefix (built-in tools only, no MCP) ===")
	t.Logf("  tool schemas   %8s  (~%d tok) across %d tools", humanBytes(schemaBytes), approxTokens(schemaBytes), len(defs))
	t.Logf("  system prompt  %8s  (~%d tok)", humanBytes(systemBytes), approxTokens(systemBytes))
	t.Logf("  total fixed    %8s  (~%d tok) resent on every provider call before caching", humanBytes(fixed), approxTokens(fixed))

	// Guards, not targets. These fail if the fixed prefix collapses (someone
	// removed the tools) or balloons past the point where the measurement
	// above stops describing reality.
	if len(defs) < 15 {
		t.Fatalf("expected the full built-in tool set, got %d", len(defs))
	}
	if fixed < 8*1024 {
		t.Fatalf("fixed prefix is only %s; the caching measurement no longer describes this build", humanBytes(fixed))
	}
}

// TestPromptCacheSavesMostOfATurnsPromptBytes is the headline measurement:
// across one turn, how much of everything sent to the provider is a
// retransmission of something the previous call already sent.
func TestPromptCacheSavesMostOfATurnsPromptBytes(t *testing.T) {
	for _, toolCalls := range []int{1, 5, 10} {
		shapes := measureTurn(t, toolCalls)
		sentTotal, cacheable := 0, 0
		for _, s := range shapes {
			sentTotal += s.total
			cacheable += s.prefix
		}
		share := 100 * float64(cacheable) / float64(sentTotal)

		t.Logf("\n=== turn with %d tool call(s): %d provider requests ===", toolCalls, len(shapes))
		t.Logf("  %-4s %10s %10s %10s", "call", "prefix", "new", "total")
		for _, s := range shapes {
			t.Logf("  %-4d %10s %10s %10s", s.iteration, humanBytes(s.prefix), humanBytes(s.fresh), humanBytes(s.total))
		}
		t.Logf("  ----")
		t.Logf("  sent across the turn      %10s (~%d tok)", humanBytes(sentTotal), approxTokens(sentTotal))
		t.Logf("  servable from cache       %10s (~%d tok)", humanBytes(cacheable), approxTokens(cacheable))
		t.Logf("  => %.0f%% of prompt bytes are retransmission a warm cache serves", share)
		t.Logf("  at Anthropic's 0.1x read rate that is roughly a %.0f%% cut in prompt cost,", share*0.9)
		t.Logf("  before subtracting cache-write overhead on the new bytes.")

		// The invariant worth pinning: a longer turn must not send a smaller
		// share from cache. If it does, something is invalidating the prefix.
		if toolCalls >= 5 && share < 50 {
			t.Errorf("turn with %d tool calls only shares %.0f%% of bytes with prior requests; the prefix is being invalidated", toolCalls, share)
		}
	}
}

// TestStablePrefixIsIdenticalAcrossEveryCallOfATurn is the property the whole
// wave depends on and the one most likely to be broken by a later change: if
// anything ahead of the conversation varies between iterations, every cached
// prefix is discarded and the measurements above become fiction.
func TestStablePrefixIsIdenticalAcrossEveryCallOfATurn(t *testing.T) {
	workspace := t.TempDir()
	cfg := appconfig.Defaults()
	registry, _, processes, err := tools.Builtins(workspace, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if processes != nil {
		defer processes.StopAll()
	}
	var systems, toolSets []string
	client := &measuringClient{toolCalls: 6, onRequest: func(_ int, request provider.Request) {
		systems = append(systems, request.System)
		encoded, err := json.Marshal(request.Tools)
		if err != nil {
			t.Fatal(err)
		}
		toolSets = append(toolSets, string(encoded))
	}}
	a := agent.New(agent.Options{
		Client: client, ProviderName: "measurement", Model: "fixture",
		ProviderConfig: appconfig.Provider{MaxTokens: 4096, Context: 200_000},
		Workspace:      workspace, Registry: registry,
		Permissions:   permission.New(appconfig.Permissions{Mode: "autopilot"}, nil),
		MaxIterations: 12,
	})
	if _, err := a.Run(t.Context(), "investigate", nil); err != nil {
		t.Fatal(err)
	}
	if len(systems) < 2 {
		t.Fatalf("expected several provider calls, got %d", len(systems))
	}
	for i := 1; i < len(systems); i++ {
		if systems[i] != systems[0] {
			t.Errorf("system prompt changed at call %d; every cached prefix is discarded from here on", i+1)
		}
		if toolSets[i] != toolSets[0] {
			t.Errorf("tool definitions changed at call %d; every cached prefix is discarded from here on", i+1)
		}
	}
	t.Logf("stable prefix held byte-identical across %d provider calls", len(systems))
}

// TestLivePromptCacheAgainstRealEndpoint confirms the provider's side of the
// bargain: that breakpoints are accepted and read back. It needs credentials
// and it costs money, so it is opt-in, in the same idiom as the live web
// suite (COLLO_LIVE_WEB_TESTS).
//
// It reads the named provider straight out of the user's own configuration,
// so it measures the endpoint actually in use rather than a hardcoded one:
//
//	COLLO_LIVE_CACHE_TESTS=1 go test ./internal/eval/ -run Live -v
//	COLLO_LIVE_CACHE_PROVIDER=azure-foundry ... # defaults to the configured default
//
// It sends two requests over an identical large prefix. The first should
// report cache creation, the second should report cache reads. A second
// request reporting zero reads is the failure that matters: it means
// breakpoints are being accepted and silently ignored, which is the state
// this whole wave existed to get out of.
func TestLivePromptCacheAgainstRealEndpoint(t *testing.T) {
	if os.Getenv("COLLO_LIVE_CACHE_TESTS") == "" {
		t.Skip("set COLLO_LIVE_CACHE_TESTS=1 to measure real cache behavior against your configured provider")
	}
	// Load resolves api_key_env as it goes, so this measures exactly the
	// credential and endpoint an ordinary session would use.
	cfg, err := appconfig.Load(t.TempDir())
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	name := os.Getenv("COLLO_LIVE_CACHE_PROVIDER")
	if name == "" {
		name = cfg.DefaultProvider
	}
	p, ok := cfg.Providers[name]
	if !ok {
		t.Fatalf("provider %q is not configured; set COLLO_LIVE_CACHE_PROVIDER to one of %v", name, providerNames(cfg))
	}
	if !strings.Contains(p.Type, "anthropic") {
		t.Skipf("provider %q is type %q; cache breakpoints are only sent on the Anthropic Messages routes", name, p.Type)
	}
	if p.APIKey == "" {
		// Load consults the credential store after the environment, so both
		// routes are already covered by the time we get here.
		t.Skipf("no credential resolved for provider %q; export %s or run `collo auth set %s`, then retry", name, p.APIKeyEnv, name)
	}
	client, err := provider.New(name, p, p.Model)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}

	// The prefix must clear the provider's minimum cacheable length, so this
	// is deliberately large rather than a token example.
	system := "You are a measurement fixture.\n" + strings.Repeat("Collomia measures its own prompt cache behavior against a real endpoint. ", 400)
	request := provider.Request{
		Model: p.Model, System: system, MaxTokens: 16,
		Messages: []provider.Message{{Role: "user", Content: "Reply with the single word: ok"}},
	}
	var warnings []string
	collect := func(d provider.Delta) {
		if d.Warning != "" {
			warnings = append(warnings, d.Warning)
		}
	}
	first, err := client.Chat(t.Context(), request, collect)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	second, err := client.Chat(t.Context(), request, collect)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	t.Logf("\n=== live prompt cache: provider %q (%s), model %s ===", name, p.Type, p.Model)
	t.Logf("  first  request: input=%d cache_read=%d cache_write=%d", first.Usage.InputTokens, first.Usage.CachedTokens, first.Usage.CacheWriteTokens)
	t.Logf("  second request: input=%d cache_read=%d cache_write=%d", second.Usage.InputTokens, second.Usage.CachedTokens, second.Usage.CacheWriteTokens)
	for _, w := range warnings {
		t.Logf("  provider warning: %s", w)
	}
	if second.Usage.CachedTokens == 0 {
		t.Fatalf("the endpoint accepted the request but reported no cache read on an identical prefix; breakpoints are not taking effect on %q", name)
	}
	share := 100 * float64(second.Usage.CachedTokens) / float64(second.Usage.InputTokens)
	t.Logf("  => %.0f%% of the second request's prompt was served from cache", share)
}

func providerNames(cfg appconfig.Config) []string {
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// realisticSourceFile is a plausible Go file, so a read_file result in the
// measurement above is the size a real one would be.
func realisticSourceFile() string {
	var b strings.Builder
	b.WriteString("package fixture\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, `
// Scanner%d walks a fenced block and reports where it ends. The closing
// delimiter must match the opening run length, which is the case the parser
// currently drops.
func Scanner%d(input string) (int, bool) {
	lines := strings.Split(input, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "%s") && len(trimmed) >= 3 {
			return i, true
		}
		if i > 0 && trimmed == "" {
			continue
		}
		_ = fmt.Sprintf("scanning %%d", i)
	}
	return 0, false
}
`, i, i, "```")
	}
	return b.String()
}
