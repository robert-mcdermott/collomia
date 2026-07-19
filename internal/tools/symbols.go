package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/index"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// SearchSymbolsTool queries the incremental workspace symbol index. Each
// call refreshes the index first (only changed files are re-parsed), so
// results always reflect the current tree.
type SearchSymbolsTool struct{ Index *index.Index }

func (t SearchSymbolsTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "search_symbols", Description: "Find symbol definitions (functions, methods, types, classes, constants) by name across the workspace using an incremental index. Faster and more precise than search_files for locating where something is defined. Supports Go, Python, JavaScript/TypeScript, and Rust. Use search_files for references and arbitrary text.", InputSchema: schema(`{"type":"object","properties":{"query":{"type":"string","description":"Symbol name or fragment (case-insensitive; exact and prefix matches rank first)"},"kind":{"type":"string","enum":["func","method","type","class","struct","interface","const","var","enum","trait"],"description":"Optional kind filter"},"max_results":{"type":"integer","minimum":1,"maximum":200}},"required":["query"],"additionalProperties":false}`)}
}

func (t SearchSymbolsTool) Assess(json.RawMessage) (Action, error) {
	return Action{Risk: RiskRead, Summary: "search workspace symbol index"}, nil
}

func (t SearchSymbolsTool) Execute(_ context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Query      string `json:"query"`
		Kind       string `json:"kind"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Query) == "" {
		return "", errors.New("query must not be empty")
	}
	if _, err := t.Index.Refresh(); err != nil {
		return "", err
	}
	symbols := t.Index.Query(a.Query, a.Kind, a.MaxResults)
	if len(symbols) == 0 {
		return fmt.Sprintf("No symbol definitions matching %q found. Try search_files for references or arbitrary text.", a.Query), nil
	}
	var b strings.Builder
	for _, s := range symbols {
		fmt.Fprintf(&b, "%s:%d  %s %s\n", s.Path, s.Line, s.Kind, s.Name)
	}
	return b.String(), nil
}
