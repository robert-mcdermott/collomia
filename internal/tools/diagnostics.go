package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/robert-mcdermott/collomia/internal/lsp"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// languageForExt maps file extensions to LSP language ids.
var languageForExt = map[string]string{
	".go": "go", ".py": "python", ".ts": "typescript", ".tsx": "typescriptreact",
	".js": "javascript", ".jsx": "javascriptreact", ".rs": "rust",
}

// defaultServers are tried on PATH when configuration does not name a
// server for a language.
var defaultServers = map[string][]string{
	"go":              {"gopls", "serve"},
	"python":          {"pyright-langserver", "--stdio"},
	"typescript":      {"typescript-language-server", "--stdio"},
	"typescriptreact": {"typescript-language-server", "--stdio"},
	"javascript":      {"typescript-language-server", "--stdio"},
	"javascriptreact": {"typescript-language-server", "--stdio"},
	"rust":            {"rust-analyzer"},
}

// DiagnosticsTool runs a real language server against requested files and
// returns its published diagnostics with exact positions.
type DiagnosticsTool struct {
	Guard *PathGuard
	// Servers maps language id to the server command; merged over defaults.
	Servers map[string][]string
}

func (t DiagnosticsTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "diagnostics", Description: "Run the project's language server (gopls, pyright, typescript-language-server, rust-analyzer — auto-detected on PATH or configured via the lsp config map) over one or more files and return compiler/analyzer diagnostics with severities and exact lines. All files in one call must share a language. Use after editing to catch errors without running a full build.", InputSchema: schema(`{"type":"object","properties":{"paths":{"type":"array","minItems":1,"maxItems":20,"items":{"type":"string"}}},"required":["paths"],"additionalProperties":false}`)}
}

func (t DiagnosticsTool) Assess(raw json.RawMessage) (Action, error) {
	var a struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return Action{}, err
	}
	if len(a.Paths) == 0 {
		return Action{}, errors.New("paths must not be empty")
	}
	// Read-risk: the language server only reads the workspace. It is a
	// real subprocess, but one whose identity is fixed by configuration,
	// not by the model.
	return Action{Risk: RiskRead, Summary: fmt.Sprintf("language-server diagnostics for %s", strings.Join(a.Paths, ", "))}, nil
}

func (t DiagnosticsTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	files := map[string]string{}
	language := ""
	for _, p := range a.Paths {
		resolved, _, err := t.Guard.Resolve(p)
		if err != nil {
			return "", err
		}
		lang, ok := languageForExt[strings.ToLower(filepath.Ext(resolved))]
		if !ok {
			return "", fmt.Errorf("no language server mapping for %s (supported: go, python, ts/tsx, js/jsx, rust)", p)
		}
		if language == "" {
			language = lang
		} else if language != lang {
			return "", errors.New("all paths in one diagnostics call must share a language; make separate calls per language")
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			return "", err
		}
		rel, relErr := filepath.Rel(t.Guard.Workspace, resolved)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			return "", fmt.Errorf("path %s is outside the workspace", p)
		}
		files[filepath.ToSlash(rel)] = string(data)
	}
	argv := t.Servers[language]
	if len(argv) == 0 {
		argv = defaultServers[language]
	}
	if len(argv) == 0 {
		return "", fmt.Errorf("no language server configured for %s", language)
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		return "", fmt.Errorf("language server %q is not installed (configure lsp.%s in the config or install it)", argv[0], language)
	}
	client, err := lsp.Start(ctx, t.Guard.Workspace, argv)
	if err != nil {
		return "", fmt.Errorf("start %s: %w", argv[0], err)
	}
	defer client.Close()
	diags, err := client.DiagnoseFiles(ctx, files, language, 25*time.Second)
	if err != nil {
		return "", err
	}
	if len(diags) == 0 {
		return fmt.Sprintf("No diagnostics reported by %s for the requested files.", argv[0]), nil
	}
	rank := map[string]int{"error": 0, "warning": 1, "information": 2, "hint": 3}
	sort.Slice(diags, func(i, j int) bool {
		if rank[diags[i].Severity] != rank[diags[j].Severity] {
			return rank[diags[i].Severity] < rank[diags[j].Severity]
		}
		if diags[i].Path != diags[j].Path {
			return diags[i].Path < diags[j].Path
		}
		return diags[i].Line < diags[j].Line
	})
	var b strings.Builder
	for _, d := range diags {
		source := ""
		if d.Source != "" {
			source = " [" + d.Source + "]"
		}
		fmt.Fprintf(&b, "%s:%d %s%s: %s\n", d.Path, d.Line, d.Severity, source, d.Message)
	}
	return b.String(), nil
}
