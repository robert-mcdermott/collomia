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

	"github.com/robert-mcdermott/collomia/internal/diffmodel"
	"github.com/robert-mcdermott/collomia/internal/lsp"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// lspCallTimeout bounds one language-server request. It is longer than the
// diagnostics path allows because the first cross-file question forces a cold
// server to index the workspace, and a lookup that times out during indexing
// looks to the model like a symbol that does not exist.
const lspCallTimeout = 60 * time.Second

// maxReferenceResults bounds a references answer. A symbol with thousands of
// uses would otherwise fill the context window with one tool result.
const maxReferenceResults = 200

// languageServerArgv resolves the server command for a language, preferring
// configuration over the built-in defaults.
func languageServerArgv(servers map[string][]string, language string) ([]string, error) {
	argv := servers[language]
	if len(argv) == 0 {
		argv = defaultServers[language]
	}
	if len(argv) == 0 {
		return nil, fmt.Errorf("no language server configured for %s", language)
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		return nil, fmt.Errorf("language server %q is not installed (configure lsp.%s in the config or install it)", argv[0], language)
	}
	return argv, nil
}

// lspFile is one workspace file prepared for a language-server request.
type lspFile struct {
	absolute string
	rel      string
	content  string
	language string
	argv     []string
}

func prepareLSPFile(guard *PathGuard, servers map[string][]string, path string) (lspFile, error) {
	resolved, _, err := guard.Resolve(path)
	if err != nil {
		return lspFile{}, err
	}
	language, ok := languageForExt[strings.ToLower(filepath.Ext(resolved))]
	if !ok {
		return lspFile{}, fmt.Errorf("no language server mapping for %s (supported: go, python, ts/tsx, js/jsx, rust)", path)
	}
	rel, relErr := filepath.Rel(guard.Workspace, resolved)
	if relErr != nil || strings.HasPrefix(rel, "..") {
		return lspFile{}, fmt.Errorf("path %s is outside the workspace", path)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return lspFile{}, err
	}
	argv, err := languageServerArgv(servers, language)
	if err != nil {
		return lspFile{}, err
	}
	return lspFile{absolute: resolved, rel: filepath.ToSlash(rel), content: string(data), language: language, argv: argv}, nil
}

// startFor launches the server and opens the document, which is what makes a
// server answer about a file it has not indexed yet.
//
// It reports progress through onOutput because the wait is the part a user
// cannot otherwise account for: a warm server answers in a second, while a
// cold gopls or rust-analyzer indexing a large repository can consume most of
// the request budget, and a motionless spinner for forty seconds is
// indistinguishable from a hang. The lines are display-only — they never enter
// the string returned to the model — and the transcript replaces them with the
// real result when it arrives, so nothing lingers.
func (f lspFile) startFor(ctx context.Context, workspace, capability string, onOutput func(string)) (*lsp.Client, error) {
	progress := func(format string, args ...any) {
		if onOutput != nil {
			onOutput(fmt.Sprintf(format, args...))
		}
	}
	progress("starting %s…\n", f.argv[0])
	began := time.Now()
	client, err := lsp.Start(ctx, workspace, f.argv)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", f.argv[0], err)
	}
	client.SetCallTimeout(lspCallTimeout)
	if err := client.Open(f.rel, f.content, f.language); err != nil {
		client.Close()
		return nil, err
	}
	// Naming the elapsed start separately from the whole call is what
	// distinguishes "the server is still coming up" from "the server is
	// thinking about the answer".
	progress("%s ready in %s — %s…\n", f.argv[0], time.Since(began).Round(100*time.Millisecond), capability)
	return client, nil
}

// locate finds the column of a symbol on a 1-based line, returning the
// protocol position (both zero-based, the character in UTF-16 code units).
//
// Taking a symbol name rather than a column is the whole ergonomic difference
// between this and a raw LSP call: the model has just read the file with line
// numbers, so it knows the name and the line, and asking it to count columns —
// in UTF-16 code units, no less — invites silent off-by-one lookups that
// return a plausible answer about the wrong token.
func (f lspFile) locate(line int, symbol string) (int, int, error) {
	if line < 1 {
		return 0, 0, errors.New("line must be 1 or greater")
	}
	lines := strings.Split(strings.ReplaceAll(f.content, "\r\n", "\n"), "\n")
	if line > len(lines) {
		return 0, 0, fmt.Errorf("%s has %d lines; line %d does not exist", f.rel, len(lines), line)
	}
	text := lines[line-1]
	index := strings.Index(text, symbol)
	if index < 0 {
		return 0, 0, fmt.Errorf("%q does not appear on %s:%d — the line reads: %s", symbol, f.rel, line, strings.TrimSpace(text))
	}
	return line - 1, lsp.UTF16Column(text, index), nil
}

// sourceLine returns the trimmed text of a 1-based line in a workspace file,
// so a location list shows what is there instead of only where it is.
func sourceLine(workspace, rel string, line int) string {
	data, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if line < 1 || line > len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[line-1])
}

func formatLocations(workspace string, locations []lsp.Location) string {
	var b strings.Builder
	for _, location := range locations {
		text := sourceLine(workspace, location.Path, location.Line)
		if text != "" {
			fmt.Fprintf(&b, "%s:%d:%d  %s\n", location.Path, location.Line, location.Character, text)
			continue
		}
		fmt.Fprintf(&b, "%s:%d:%d\n", location.Path, location.Line, location.Character)
	}
	return b.String()
}

// describeUnsupported turns "Unhandled method textDocument/formatting" into a
// sentence that names the server and the fix. Language servers differ in what
// they implement — pyright deliberately provides no formatter, for instance —
// and a raw protocol string leaves the user to work that out.
func describeUnsupported(err error, file lspFile, capability string) error {
	var protocol *lsp.ProtocolError
	if errors.As(err, &protocol) && protocol.Unsupported() {
		return fmt.Errorf("the language server for %s (%s) does not implement %s; configure a different server for this language under lsp.%s",
			file.language, file.argv[0], capability, file.language)
	}
	return err
}

type positionArgs struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Symbol string `json:"symbol"`
}

// FindDefinitionTool answers "where is this defined?" using the project's
// language server, which resolves imports, aliases, and interfaces that a text
// search cannot.
type FindDefinitionTool struct {
	Guard   *PathGuard
	Servers map[string][]string
}

func (t FindDefinitionTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "find_definition", Description: "Resolve where a symbol is defined using the project's language server (gopls, pyright, typescript-language-server, rust-analyzer). Give the file and the 1-based line where the symbol appears plus the symbol text itself; the column is located for you. Unlike search_symbols this follows imports, aliases, and type information, so it finds the definition actually referenced at that position. The first call in a session may be slow while the server indexes.", InputSchema: schema(`{"type":"object","properties":{"path":{"type":"string"},"line":{"type":"integer","minimum":1},"symbol":{"type":"string","description":"The identifier as it appears on that line"}},"required":["path","line","symbol"],"additionalProperties":false}`)}
}

func (t FindDefinitionTool) Assess(raw json.RawMessage) (Action, error) {
	var a positionArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return Action{}, err
	}
	if a.Symbol == "" {
		return Action{}, errors.New("symbol must not be empty")
	}
	return Action{Risk: RiskRead, Summary: fmt.Sprintf("definition of %s at %s:%d", a.Symbol, a.Path, a.Line)}, nil
}

func (t FindDefinitionTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	return t.execute(ctx, raw, nil)
}

// ExecuteStream reports server startup while the request is in flight.
func (t FindDefinitionTool) ExecuteStream(ctx context.Context, raw json.RawMessage, onOutput func(string)) (string, error) {
	return t.execute(ctx, raw, onOutput)
}

func (t FindDefinitionTool) execute(ctx context.Context, raw json.RawMessage, onOutput func(string)) (string, error) {
	var a positionArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	file, err := prepareLSPFile(t.Guard, t.Servers, a.Path)
	if err != nil {
		return "", err
	}
	line, character, err := file.locate(a.Line, a.Symbol)
	if err != nil {
		return "", err
	}
	client, err := file.startFor(ctx, t.Guard.Workspace, "resolving definition", onOutput)
	if err != nil {
		return "", err
	}
	defer client.Close()
	locations, err := client.Definition(ctx, file.rel, line, character)
	if err != nil {
		return "", describeUnsupported(err, file, "go-to-definition")
	}
	if len(locations) == 0 {
		return fmt.Sprintf("%s reported no definition for %q at %s:%d. It may be a built-in, or the server may still be indexing.", file.argv[0], a.Symbol, file.rel, a.Line), nil
	}
	return formatLocations(t.Guard.Workspace, locations), nil
}

// FindReferencesTool answers "what uses this?".
type FindReferencesTool struct {
	Guard   *PathGuard
	Servers map[string][]string
}

func (t FindReferencesTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "find_references", Description: "List where a symbol is used across the project using the project's language server. Give the file and the 1-based line where the symbol appears plus the symbol text itself. This understands scope and types, so it excludes same-named symbols that search_files would match. Use it before renaming or deleting anything. The first call in a session may be slow while the server indexes.", InputSchema: schema(`{"type":"object","properties":{"path":{"type":"string"},"line":{"type":"integer","minimum":1},"symbol":{"type":"string","description":"The identifier as it appears on that line"},"include_declaration":{"type":"boolean","description":"Include the declaration itself (default false)"}},"required":["path","line","symbol"],"additionalProperties":false}`)}
}

func (t FindReferencesTool) Assess(raw json.RawMessage) (Action, error) {
	var a positionArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return Action{}, err
	}
	if a.Symbol == "" {
		return Action{}, errors.New("symbol must not be empty")
	}
	return Action{Risk: RiskRead, Summary: fmt.Sprintf("references to %s at %s:%d", a.Symbol, a.Path, a.Line)}, nil
}

func (t FindReferencesTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	return t.execute(ctx, raw, nil)
}

// ExecuteStream reports server startup while the request is in flight.
func (t FindReferencesTool) ExecuteStream(ctx context.Context, raw json.RawMessage, onOutput func(string)) (string, error) {
	return t.execute(ctx, raw, onOutput)
}

func (t FindReferencesTool) execute(ctx context.Context, raw json.RawMessage, onOutput func(string)) (string, error) {
	var a struct {
		positionArgs
		IncludeDeclaration bool `json:"include_declaration"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	file, err := prepareLSPFile(t.Guard, t.Servers, a.Path)
	if err != nil {
		return "", err
	}
	line, character, err := file.locate(a.Line, a.Symbol)
	if err != nil {
		return "", err
	}
	client, err := file.startFor(ctx, t.Guard.Workspace, "searching for references", onOutput)
	if err != nil {
		return "", err
	}
	defer client.Close()
	locations, err := client.References(ctx, file.rel, line, character, a.IncludeDeclaration)
	if err != nil {
		return "", describeUnsupported(err, file, "find-references")
	}
	if len(locations) == 0 {
		return fmt.Sprintf("%s reported no references to %q at %s:%d. It may be unused, or the server may still be indexing.", file.argv[0], a.Symbol, file.rel, a.Line), nil
	}
	sort.SliceStable(locations, func(i, j int) bool {
		if locations[i].Path != locations[j].Path {
			return locations[i].Path < locations[j].Path
		}
		return locations[i].Line < locations[j].Line
	})
	truncated := ""
	if len(locations) > maxReferenceResults {
		truncated = fmt.Sprintf("\n(%d more not shown)\n", len(locations)-maxReferenceResults)
		locations = locations[:maxReferenceResults]
	}
	return formatLocations(t.Guard.Workspace, locations) + truncated, nil
}

// FormatFileTool applies the project's own formatter, as the language server
// implements it, so formatting matches what the project's editors produce
// rather than what the model believes the convention to be.
type FormatFileTool struct {
	Guard   *PathGuard
	Servers map[string][]string
	Tracker *diffmodel.Tracker
}

func (t FormatFileTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "format_file", Description: "Format one file with the project's language server (gofmt via gopls, and the configured formatter for other languages). The whole file is replaced with the server's formatting; the change is tracked like any other edit and can be undone. Use it after editing rather than hand-aligning code.", InputSchema: schema(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)}
}

func (t FormatFileTool) Assess(raw json.RawMessage) (Action, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return Action{}, err
	}
	p, outside, err := t.Guard.Resolve(a.Path)
	// No preview: producing one means running the language server, and
	// running it twice — once to show the diff and once to apply it — doubles
	// the cost of every approval and can still show a stale result. The change
	// is a formatter's output over the whole file, it is recorded by the diff
	// tracker, and `/undo` reverses it.
	return Action{Risk: RiskWrite, Summary: "format " + p, Outside: outside, Paths: []string{p}}, err
}

func (t FormatFileTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	return t.execute(ctx, raw, nil)
}

// ExecuteStream reports server startup while the request is in flight.
func (t FormatFileTool) ExecuteStream(ctx context.Context, raw json.RawMessage, onOutput func(string)) (string, error) {
	return t.execute(ctx, raw, onOutput)
}

func (t FormatFileTool) execute(ctx context.Context, raw json.RawMessage, onOutput func(string)) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	file, err := prepareLSPFile(t.Guard, t.Servers, a.Path)
	if err != nil {
		return "", err
	}
	client, err := file.startFor(ctx, t.Guard.Workspace, "formatting", onOutput)
	if err != nil {
		return "", err
	}
	defer client.Close()
	edits, err := client.Formatting(ctx, file.rel, 4, file.language != "go")
	if err != nil {
		return "", describeUnsupported(err, file, "formatting")
	}
	if len(edits) == 0 {
		return fmt.Sprintf("%s is already formatted according to %s.", file.rel, file.argv[0]), nil
	}
	formatted, err := lsp.Apply(file.content, edits)
	if err != nil {
		return "", err
	}
	if formatted == file.content {
		return fmt.Sprintf("%s is already formatted according to %s.", file.rel, file.argv[0]), nil
	}
	target, _, err := t.Guard.MutationTarget(a.Path)
	if err != nil {
		return "", err
	}
	defer target.Close()
	mode := os.FileMode(0o644)
	if info, statErr := target.Stat(); statErr == nil {
		mode = info.Mode().Perm()
	}
	// Re-read through the authorized handle: the content formatted above was
	// read before the server ran, and replacing a file that changed in between
	// would silently discard the change.
	current, err := target.ReadFile()
	if err != nil {
		return "", err
	}
	if string(current) != file.content {
		return "", fmt.Errorf("%s changed while %s was formatting it; nothing was written", file.rel, file.argv[0])
	}
	if err := target.Replace([]byte(formatted), mode); err != nil {
		return "", err
	}
	if t.Tracker != nil {
		before := file.content
		t.Tracker.RecordWithMode(target.Path(), "write", &before, &formatted, mode, mode)
	}
	return fmt.Sprintf("Formatted %s with %s (%d edit(s)).", file.rel, file.argv[0], len(edits)), nil
}
