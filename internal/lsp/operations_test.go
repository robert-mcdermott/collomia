package lsp

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Position arithmetic is where a language-server client quietly goes wrong: the
// protocol counts UTF-16 code units, Go counts bytes, and a file with one
// non-ASCII character earlier in a line is enough to make every later column
// point at the wrong token. These run on every platform and need no server.

func TestUTF16ColumnRoundTripsThroughNonASCII(t *testing.T) {
	tests := []struct {
		line      string
		byteIndex int
		want      int
	}{
		{"func main() {", 5, 5},
		{"// café — note", 9, 8}, // é is 2 bytes/1 unit; byte 9 is the em-dash boundary
		{"x := \"𝄞\"; y", 11, 9}, // the clef is 4 bytes but 2 UTF-16 units
		{"plain", 99, 5},         // past the end clamps
		{"plain", -1, 0},
	}
	for _, test := range tests {
		if got := UTF16Column(test.line, test.byteIndex); got != test.want {
			t.Errorf("UTF16Column(%q, %d) = %d, want %d", test.line, test.byteIndex, got, test.want)
		}
		if test.byteIndex >= 0 && test.byteIndex <= len(test.line) {
			if back := byteOffsetForUTF16(test.line, test.want); back != test.byteIndex {
				t.Errorf("byteOffsetForUTF16(%q, %d) = %d, want %d", test.line, test.want, back, test.byteIndex)
			}
		}
	}
}

func TestApplyEditsFromTheEndBackwards(t *testing.T) {
	content := "one\ntwo\nthree\n"
	// Two edits addressing the original document. Applied in the given order
	// front-to-back the second would land in the wrong place.
	edits := []TextEdit{
		{StartLine: 0, StartChar: 0, EndLine: 0, EndChar: 3, NewText: "first-line"},
		{StartLine: 2, StartChar: 0, EndLine: 2, EndChar: 5, NewText: "3"},
	}
	got, err := Apply(content, edits)
	if err != nil {
		t.Fatal(err)
	}
	if want := "first-line\ntwo\n3\n"; got != want {
		t.Fatalf("Apply = %q, want %q", got, want)
	}
}

func TestApplyRejectsOverlappingEdits(t *testing.T) {
	edits := []TextEdit{
		{StartLine: 0, StartChar: 0, EndLine: 0, EndChar: 5, NewText: "a"},
		{StartLine: 0, StartChar: 3, EndLine: 0, EndChar: 8, NewText: "b"},
	}
	if _, err := Apply("abcdefghij\n", edits); err == nil {
		t.Fatal("overlapping edits must be rejected rather than silently mangling the file")
	}
}

func TestApplyAppendsAtEndOfDocument(t *testing.T) {
	// Servers address one line past the end to append; that must not fail.
	got, err := Apply("a\n", []TextEdit{{StartLine: 1, StartChar: 0, EndLine: 9, EndChar: 0, NewText: "b\n"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "a\nb\n"; got != want {
		t.Fatalf("Apply = %q, want %q", got, want)
	}
}

func TestApplyPreservesCRLFLineCounting(t *testing.T) {
	// Line 1 is "two" whether or not the file uses CRLF; normalizing endings
	// before computing offsets would shift every edit after the first line.
	got, err := Apply("one\r\ntwo\r\n", []TextEdit{{StartLine: 1, StartChar: 0, EndLine: 1, EndChar: 3, NewText: "2"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "one\r\n2\r\n"; got != want {
		t.Fatalf("Apply = %q, want %q", got, want)
	}
}

func TestApplyWithNoEditsIsIdentity(t *testing.T) {
	if got, err := Apply("unchanged\n", nil); err != nil || got != "unchanged\n" {
		t.Fatalf("Apply = (%q, %v)", got, err)
	}
}

// navigationServer answers definition, references, and formatting with fixed
// results, so the client's request shapes and response decoding are exercised
// without installing a real language server.
const navigationServer = `
import json, sys

def read_msg():
    length = 0
    while True:
        line = sys.stdin.buffer.readline().decode()
        if not line or line == "\r\n" or line == "\n":
            break
        if line.lower().startswith("content-length:"):
            length = int(line.split(":")[1].strip())
    if length == 0:
        return None
    return json.loads(sys.stdin.buffer.read(length).decode())

def write_msg(body):
    data = json.dumps(body).encode()
    sys.stdout.buffer.write(b"Content-Length: %d\r\n\r\n" % len(data))
    sys.stdout.buffer.write(data)
    sys.stdout.buffer.flush()

opened = {}
while True:
    msg = read_msg()
    if msg is None:
        break
    method = msg.get("method", "")
    if method == "initialize":
        write_msg({"jsonrpc": "2.0", "id": msg["id"], "result": {"capabilities": {}}})
    elif method == "textDocument/didOpen":
        opened[msg["params"]["textDocument"]["uri"]] = True
    elif method == "textDocument/definition":
        uri = msg["params"]["textDocument"]["uri"]
        pos = msg["params"]["position"]
        # Echo the requested position back so the test can assert the client
        # sent the column it meant to send.
        write_msg({"jsonrpc": "2.0", "id": msg["id"], "result": {
            "uri": uri, "range": {"start": {"line": pos["character"], "character": pos["line"]},
                                   "end": {"line": pos["character"], "character": pos["line"]}}}})
    elif method == "textDocument/references":
        uri = msg["params"]["textDocument"]["uri"]
        include = msg["params"]["context"]["includeDeclaration"]
        results = [{"uri": uri, "range": {"start": {"line": 3, "character": 2}, "end": {"line": 3, "character": 6}}}]
        if include:
            results.append({"uri": uri, "range": {"start": {"line": 0, "character": 5}, "end": {"line": 0, "character": 9}}})
        write_msg({"jsonrpc": "2.0", "id": msg["id"], "result": results})
    elif method == "textDocument/formatting":
        write_msg({"jsonrpc": "2.0", "id": msg["id"], "result": [
            {"range": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 5}}, "newText": "tidy"}]})
    elif method == "exit":
        break
`

func startNavigationServer(t *testing.T) (string, []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-server test uses python3; skipped on windows")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "navigation_ls.py")
	if err := os.WriteFile(script, []byte(navigationServer), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, []string{python, script}
}

func TestDefinitionSendsThePositionAndDecodesTheResult(t *testing.T) {
	workspace, argv := startNavigationServer(t)
	client, err := Start(t.Context(), workspace, argv)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Open("main.go", "package main\n", "go"); err != nil {
		t.Fatal(err)
	}
	// The server echoes the position with line and character swapped, so an
	// off-by-one or a swapped argument shows up here rather than in the field.
	locations, err := client.Definition(t.Context(), "main.go", 7, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(locations) != 1 {
		t.Fatalf("locations = %+v", locations)
	}
	if locations[0].Path != "main.go" || locations[0].Line != 13 || locations[0].Character != 8 {
		t.Fatalf("location = %+v; the client sent the wrong position or decoded it wrongly", locations[0])
	}
}

func TestReferencesHonorsIncludeDeclaration(t *testing.T) {
	workspace, argv := startNavigationServer(t)
	client, err := Start(t.Context(), workspace, argv)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Open("main.go", "package main\n", "go"); err != nil {
		t.Fatal(err)
	}
	without, err := client.References(t.Context(), "main.go", 0, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	with, err := client.References(t.Context(), "main.go", 0, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(without) != 1 || len(with) != 2 {
		t.Fatalf("references without=%d with=%d, want 1 and 2", len(without), len(with))
	}
	if without[0].Line != 4 {
		t.Fatalf("reference line = %d, want 4 (1-based)", without[0].Line)
	}
}

func TestFormattingReturnsEditsThatApply(t *testing.T) {
	workspace, argv := startNavigationServer(t)
	client, err := Start(t.Context(), workspace, argv)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Open("main.go", "messy code\n", "go"); err != nil {
		t.Fatal(err)
	}
	edits, err := client.Formatting(t.Context(), "main.go", 4, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Apply("messy code\n", edits)
	if err != nil {
		t.Fatal(err)
	}
	if want := "tidy code\n"; got != want {
		t.Fatalf("formatted = %q, want %q", got, want)
	}
}

func TestDecodeLocationsAcceptsLocationLink(t *testing.T) {
	client := &Client{root: "/workspace"}
	raw := []byte(`[{"targetUri":"file:///workspace/pkg/a.go","targetSelectionRange":{"start":{"line":9,"character":5}}}]`)
	locations := client.decodeLocations(raw)
	if len(locations) != 1 || locations[0].Line != 10 || locations[0].Character != 6 {
		t.Fatalf("locations = %+v; LocationLink results must decode like Location results", locations)
	}
}

func TestDecodeLocationsTreatsNullAsNoResults(t *testing.T) {
	client := &Client{root: "/workspace"}
	if got := client.decodeLocations([]byte("null")); got != nil {
		t.Fatalf("decodeLocations(null) = %+v, want nil", got)
	}
}
