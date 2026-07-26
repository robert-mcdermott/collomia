package tools

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/lsp"
)

func newGuardOrFail(t *testing.T, dir string) *PathGuard {
	t.Helper()
	guard, err := NewPathGuard(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return guard
}

// Locating by symbol text is the tool's ergonomic contract: a model that has
// just read a numbered file knows the name and the line, and must not be asked
// to count UTF-16 columns. A symbol that is not on the named line is a mistake
// worth reporting loudly, because the alternative is a confident answer about
// the wrong token.
func TestLocateRejectsASymbolThatIsNotOnTheLine(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc Handler() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := prepareLSPFile(newGuardOrFail(t, dir), map[string][]string{"go": {"echo"}}, "main.go")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := file.locate(3, "Missing"); err == nil || !strings.Contains(err.Error(), "does not appear") {
		t.Fatalf("err=%v, want a clear report that the symbol is not on that line", err)
	}
	if _, _, err := file.locate(99, "Handler"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err=%v, want a clear report that the line does not exist", err)
	}
	line, character, err := file.locate(3, "Handler")
	if err != nil {
		t.Fatal(err)
	}
	// Zero-based line, and the column of "Handler" in "func Handler() {}".
	if line != 2 || character != 5 {
		t.Fatalf("locate = (%d, %d), want (2, 5)", line, character)
	}
}

// A non-ASCII prefix is where a byte column and a protocol column diverge.
func TestLocateCountsUTF16UnitsNotBytes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\n// café Handler\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := prepareLSPFile(newGuardOrFail(t, dir), map[string][]string{"go": {"echo"}}, "main.go")
	if err != nil {
		t.Fatal(err)
	}
	_, character, err := file.locate(3, "Handler")
	if err != nil {
		t.Fatal(err)
	}
	// "// café " is 9 bytes but 8 UTF-16 code units.
	if character != 8 {
		t.Fatalf("character = %d, want 8 (UTF-16 units, not the 9 bytes)", character)
	}
}

func TestLSPOperationsReportAnUnmappedLanguage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	guard := newGuardOrFail(t, dir)
	tool := FindDefinitionTool{Guard: guard}
	_, err := tool.Execute(t.Context(), json.RawMessage(`{"path":"notes.txt","line":1,"symbol":"text"}`))
	if err == nil || !strings.Contains(err.Error(), "no language server mapping") {
		t.Fatalf("err=%v", err)
	}
}

func TestLSPOperationsReportAMissingServer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.rs"), []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	guard := newGuardOrFail(t, dir)
	servers := map[string][]string{"rust": {"no-such-language-server"}}
	for name, run := range map[string]func() (string, error){
		"find_definition": func() (string, error) {
			return FindDefinitionTool{Guard: guard, Servers: servers}.Execute(t.Context(), json.RawMessage(`{"path":"lib.rs","line":1,"symbol":"main"}`))
		},
		"find_references": func() (string, error) {
			return FindReferencesTool{Guard: guard, Servers: servers}.Execute(t.Context(), json.RawMessage(`{"path":"lib.rs","line":1,"symbol":"main"}`))
		},
		"format_file": func() (string, error) {
			return FormatFileTool{Guard: guard, Servers: servers}.Execute(t.Context(), json.RawMessage(`{"path":"lib.rs"}`))
		},
	} {
		if _, err := run(); err == nil || !strings.Contains(err.Error(), "not installed") {
			t.Errorf("%s: err=%v, want a missing-server report", name, err)
		}
	}
}

// Navigation is read-only; formatting is an ordinary write, so it must carry
// the path the permission layer scopes rules and grants against.
func TestLSPOperationRiskAndPaths(t *testing.T) {
	dir := t.TempDir()
	guard := newGuardOrFail(t, dir)

	read, err := FindReferencesTool{Guard: guard}.Assess(json.RawMessage(`{"path":"main.go","line":3,"symbol":"Handler"}`))
	if err != nil {
		t.Fatal(err)
	}
	if read.Risk != RiskRead || len(read.Paths) != 0 {
		t.Fatalf("find_references action = %+v, want a read with no write scope", read)
	}

	write, err := FormatFileTool{Guard: guard}.Assess(json.RawMessage(`{"path":"main.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	if write.Risk != RiskWrite {
		t.Fatalf("format_file risk = %q, want write", write.Risk)
	}
	if len(write.Paths) != 1 || !strings.HasSuffix(write.Paths[0], "main.go") {
		t.Fatalf("format_file paths = %v, want the resolved target", write.Paths)
	}
}

// Not every language server implements every request: pyright ships no
// formatter and answers textDocument/formatting with MethodNotFound. That is a
// configuration answer, and must not reach the user as the raw protocol string
// "Unhandled method textDocument/formatting".
func TestUnsupportedCapabilityNamesTheServerAndTheFix(t *testing.T) {
	file := lspFile{language: "python", argv: []string{"pyright-langserver", "--stdio"}}
	err := describeUnsupported(&lsp.ProtocolError{
		Method: "textDocument/formatting", Code: lsp.MethodNotFound, Message: "Unhandled method textDocument/formatting",
	}, file, "formatting")
	if err == nil {
		t.Fatal("an unsupported capability must still be an error")
	}
	for _, want := range []string{"pyright-langserver", "does not implement formatting", "lsp.python"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q is missing %q", err, want)
		}
	}

	// Any other protocol error must pass through unchanged: a server that is
	// merely broken is not a server that lacks the capability.
	other := &lsp.ProtocolError{Method: "textDocument/formatting", Code: -32603, Message: "internal error"}
	if got := describeUnsupported(other, file, "formatting"); got != error(other) {
		t.Fatalf("describeUnsupported rewrote an unrelated error: %v", got)
	}
}

// A language server that has to index before it can answer accounts for most
// of a slow call, and a motionless spinner cannot be told from a hang. The
// progress lines exist for that, and they must never become part of what the
// model reads: the answer is the locations, not a note about server startup.
func TestProgressIsReportedAndStaysOutOfTheResult(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc Handler() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	guard := newGuardOrFail(t, dir)
	// `echo` exits immediately, so the handshake fails fast. That is enough to
	// prove the announcement happens before the server is known to be usable —
	// which is the moment a user needs it.
	servers := map[string][]string{"go": {"echo"}}

	var streamed strings.Builder
	_, err := FindDefinitionTool{Guard: guard, Servers: servers}.ExecuteStream(
		t.Context(), json.RawMessage(`{"path":"main.go","line":3,"symbol":"Handler"}`),
		func(chunk string) { streamed.WriteString(chunk) })
	if err == nil {
		t.Fatal("expected the handshake against a non-server to fail")
	}
	if !strings.Contains(streamed.String(), "starting echo") {
		t.Fatalf("progress = %q, want the server named before the request", streamed.String())
	}
}

// A tool that is not asked to stream must behave exactly as before.
func TestExecuteWithoutStreamingEmitsNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	guard := newGuardOrFail(t, dir)
	// A nil callback must not panic anywhere on the path.
	if _, err := (FindDefinitionTool{Guard: guard, Servers: map[string][]string{"go": {"echo"}}}).
		Execute(t.Context(), json.RawMessage(`{"path":"main.go","line":1,"symbol":"package"}`)); err == nil {
		t.Fatal("expected the handshake against a non-server to fail")
	}
}

func TestFindDefinitionRequiresASymbol(t *testing.T) {
	guard := newGuardOrFail(t, t.TempDir())
	if _, err := (FindDefinitionTool{Guard: guard}).Assess(json.RawMessage(`{"path":"main.go","line":1,"symbol":""}`)); err == nil {
		t.Fatal("an empty symbol must be rejected before a server is started")
	}
}

// The new tools must be registered, or they are unreachable however well they
// work.
func TestBuiltinsRegistersLSPOperations(t *testing.T) {
	registry, _, _, err := Builtins(t.TempDir(), appconfig.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"find_definition", "find_references", "format_file"} {
		if _, ok := registry.Get(name); !ok {
			t.Errorf("%s is not registered", name)
		}
	}
}

// TestFormatFileAgainstRealGopls exercises the whole path — server start,
// formatting request, edit application, atomic replacement, and diff tracking
// — when gopls is installed.
func TestFormatFileAgainstRealGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	if testing.Short() {
		t.Skip("short mode")
	}
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module formatfixture\n\ngo 1.22\n")
	write("main.go", "package main\n\nfunc main()    {\nx := 1\n_ = x\n}\n")

	registry, tracker, _, err := Builtins(dir, appconfig.Config{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := registry.Execute(t.Context(), "format_file", json.RawMessage(`{"path":"main.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "func main() {") || !strings.Contains(string(data), "\tx := 1") {
		t.Fatalf("file was not gofmt-formatted:\n%s\n(tool said: %s)", data, out)
	}
	if len(tracker.Changed()) == 0 {
		t.Fatal("a formatting write must be tracked so /diff and /undo can see it")
	}

	// Formatting an already-formatted file must be a no-op, not a rewrite.
	before := string(data)
	if _, err := registry.Execute(t.Context(), "format_file", json.RawMessage(`{"path":"main.go"}`)); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Fatal("formatting an already-formatted file changed it")
	}
}

func TestFindDefinitionAgainstRealGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	if testing.Short() {
		t.Skip("short mode")
	}
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module navfixture\n\ngo 1.22\n")
	write("lib.go", "package main\n\n// Helper does nothing.\nfunc Helper() int { return 7 }\n")
	write("main.go", "package main\n\nfunc main() {\n\t_ = Helper()\n}\n")

	registry, _, _, err := Builtins(dir, appconfig.Config{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := registry.Execute(t.Context(), "find_definition", json.RawMessage(`{"path":"main.go","line":4,"symbol":"Helper"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "lib.go:4") {
		t.Fatalf("expected the definition at lib.go:4, got:\n%s", out)
	}

	// The same call through the streaming path must report the real server's
	// startup and still return only the answer.
	var streamed strings.Builder
	streamedOut, err := registry.ExecuteStream(t.Context(), "find_definition",
		json.RawMessage(`{"path":"main.go","line":4,"symbol":"Helper"}`),
		func(chunk string) { streamed.WriteString(chunk) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"starting gopls", "ready in", "resolving definition"} {
		if !strings.Contains(streamed.String(), want) {
			t.Errorf("progress %q is missing %q", streamed.String(), want)
		}
	}
	if strings.Contains(streamedOut, "starting gopls") || strings.Contains(streamedOut, "ready in") {
		t.Fatalf("progress leaked into the model-visible result:\n%s", streamedOut)
	}
	if !strings.Contains(streamedOut, "lib.go:4") {
		t.Fatalf("streamed result lost the answer:\n%s", streamedOut)
	}
	refs, err := registry.Execute(t.Context(), "find_references", json.RawMessage(`{"path":"lib.go","line":4,"symbol":"Helper"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(refs, "main.go:4") {
		t.Fatalf("expected the use at main.go:4, got:\n%s", refs)
	}
}
