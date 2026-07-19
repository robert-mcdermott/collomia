package lsp

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// fakeServer is a minimal LSP server implemented in Python: it answers
// initialize, and replies to every didOpen with one error diagnostic on
// line 2 of the opened file.
const fakeServer = `
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

while True:
    msg = read_msg()
    if msg is None:
        break
    method = msg.get("method", "")
    if method == "initialize":
        write_msg({"jsonrpc": "2.0", "id": msg["id"], "result": {"capabilities": {}}})
    elif method == "textDocument/didOpen":
        uri = msg["params"]["textDocument"]["uri"]
        write_msg({"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics",
                   "params": {"uri": uri, "diagnostics": [
                       {"range": {"start": {"line": 1, "character": 0}, "end": {"line": 1, "character": 5}},
                        "severity": 1, "message": "fake problem", "source": "fake-ls"}]}})
    elif method == "exit":
        break
`

func startFake(t *testing.T) (string, []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-server test uses python3; skipped on windows")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake_ls.py")
	if err := os.WriteFile(script, []byte(fakeServer), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, []string{python, script}
}

func TestClientCollectsDiagnostics(t *testing.T) {
	workspace, argv := startFake(t)
	if err := os.WriteFile(filepath.Join(workspace, "main.go"), []byte("package main\nbroken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	client, err := Start(t.Context(), workspace, argv)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	diags, err := client.DiagnoseFiles(t.Context(), map[string]string{"main.go": "package main\nbroken\n"}, "go", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 1 {
		t.Fatalf("diags=%+v", diags)
	}
	d := diags[0]
	if d.Path != "main.go" || d.Line != 2 || d.Severity != "error" || d.Message != "fake problem" || d.Source != "fake-ls" {
		t.Fatalf("diag=%+v", d)
	}
}

func TestStartFailsForMissingServer(t *testing.T) {
	if _, err := Start(t.Context(), t.TempDir(), []string{"definitely-not-a-real-ls-binary"}); err == nil {
		t.Fatal("expected start failure")
	}
}
