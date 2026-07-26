// Package lsp implements a minimal Language Server Protocol client over
// stdio JSON-RPC, scoped to what the diagnostics tool needs: initialize,
// didOpen, and collecting publishDiagnostics. It speaks to any standard
// LSP server (gopls, pyright-langserver, typescript-language-server,
// rust-analyzer, …) configured or auto-detected per language.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Diagnostic is one reported problem, in workspace terms.
type Diagnostic struct {
	Path     string // workspace-relative
	Line     int    // 1-based
	Severity string // error, warning, information, hint
	Message  string
	Source   string
}

var severityNames = map[int]string{1: "error", 2: "warning", 3: "information", 4: "hint"}

// Client is one running language-server process.
type Client struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	incoming chan readResult // fed by a single reader goroutine
	root     string

	// callTimeout bounds one request. It is a field rather than a constant
	// because a cold gopls or rust-analyzer can take far longer to answer the
	// first cross-file question than to publish diagnostics for an open file.
	callTimeout time.Duration

	mu     sync.Mutex
	nextID int
	diags  map[string][]Diagnostic // keyed by relative path
}

// SetCallTimeout bounds each subsequent request.
func (c *Client) SetCallTimeout(timeout time.Duration) {
	if timeout > 0 {
		c.callTimeout = timeout
	}
}

type readResult struct {
	msg message
	err error
}

// Start launches the server command and completes the LSP initialize
// handshake rooted at workspace.
func Start(ctx context.Context, workspace string, argv []string) (*Client, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("empty language server command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = workspace
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{cmd: cmd, stdin: stdin, incoming: make(chan readResult, 16), root: workspace,
		callTimeout: 15 * time.Second, diags: map[string][]Diagnostic{}}
	go c.readLoop(bufio.NewReader(stdout))
	initParams := map[string]any{
		"processId": nil,
		"rootUri":   pathToURI(workspace),
		"capabilities": map[string]any{
			// linkSupport is deliberately not declared: without it servers
			// answer with plain Location values, which is the one shape every
			// server produces. decodeLocations still reads LocationLink for
			// servers that send it regardless.
			"textDocument": map[string]any{
				"publishDiagnostics": map[string]any{},
				"synchronization":    map[string]any{},
				"definition":         map[string]any{},
				"references":         map[string]any{},
				"formatting":         map[string]any{},
			},
		},
	}
	if _, err := c.call(ctx, "initialize", initParams); err != nil {
		c.Close()
		return nil, fmt.Errorf("initialize: %w", err)
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Close shuts the server down. Best-effort; the process is killed if it
// does not exit promptly.
func (c *Client) Close() {
	_ = c.notify("exit", nil)
	_ = c.stdin.Close()
	done := make(chan struct{})
	go func() { _ = c.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = c.cmd.Process.Kill()
		<-done
	}
}

// DiagnoseFiles opens each file (didOpen) and collects diagnostics the
// server publishes, waiting up to timeout for results to settle.
func (c *Client) DiagnoseFiles(ctx context.Context, files map[string]string, languageID string, timeout time.Duration) ([]Diagnostic, error) {
	expected := map[string]bool{}
	for rel, content := range files {
		expected[rel] = true
		params := map[string]any{"textDocument": map[string]any{
			"uri": pathToURI(filepath.Join(c.root, filepath.FromSlash(rel))), "languageId": languageID, "version": 1, "text": content,
		}}
		if err := c.notify("textDocument/didOpen", params); err != nil {
			return nil, err
		}
	}
	deadline := time.Now().Add(timeout)
	received := map[string]bool{}
	for time.Now().Before(deadline) && len(received) < len(expected) {
		remaining := time.Until(deadline)
		msg, err := c.read(ctx, remaining)
		if err != nil {
			break // timeout or EOF: report what we have
		}
		if msg.Method == "textDocument/publishDiagnostics" {
			var params struct {
				URI         string `json:"uri"`
				Diagnostics []struct {
					Range struct {
						Start struct {
							Line int `json:"line"`
						} `json:"start"`
					} `json:"range"`
					Severity int    `json:"severity"`
					Message  string `json:"message"`
					Source   string `json:"source"`
				} `json:"diagnostics"`
			}
			if json.Unmarshal(msg.Params, &params) != nil {
				continue
			}
			rel := c.relPath(uriToPath(params.URI))
			var list []Diagnostic
			for _, d := range params.Diagnostics {
				sev := severityNames[d.Severity]
				if sev == "" {
					sev = "warning"
				}
				list = append(list, Diagnostic{Path: rel, Line: d.Range.Start.Line + 1, Severity: sev, Message: d.Message, Source: d.Source})
			}
			c.mu.Lock()
			c.diags[rel] = list
			c.mu.Unlock()
			if expected[rel] {
				received[rel] = true
			}
		}
	}
	var out []Diagnostic
	c.mu.Lock()
	for rel := range expected {
		out = append(out, c.diags[rel]...)
	}
	c.mu.Unlock()
	return out, nil
}

// MethodNotFound is the JSON-RPC code a server returns for a request it does
// not implement. It is worth distinguishing: a server that cannot format is a
// configuration answer ("use a different server"), not a failure of the file.
const MethodNotFound = -32601

// ProtocolError is an error the language server returned for a request.
type ProtocolError struct {
	Method  string
	Code    int
	Message string
}

func (e *ProtocolError) Error() string { return e.Method + ": " + e.Message }

// Unsupported reports whether the server answered that it does not implement
// the request at all.
func (e *ProtocolError) Unsupported() bool { return e.Code == MethodNotFound }

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// call sends a request and waits for its response, dispatching any
// notifications that arrive in between.
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	timeout := c.callTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		msg, err := c.read(ctx, time.Until(deadline))
		if err != nil {
			return nil, err
		}
		if len(msg.ID) > 0 && strings.TrimSpace(string(msg.ID)) == strconv.Itoa(id) && msg.Method == "" {
			if msg.Error != nil {
				return nil, &ProtocolError{Method: method, Code: msg.Error.Code, Message: msg.Error.Message}
			}
			return msg.Result, nil
		}
		// Server-to-client requests (e.g. workspace/configuration) get an
		// empty result so the server does not stall.
		if len(msg.ID) > 0 && msg.Method != "" {
			_ = c.write(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "result": nil})
		}
	}
	return nil, fmt.Errorf("%s: timed out", method)
}

func (c *Client) notify(method string, params any) error {
	body := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		body["params"] = params
	}
	return c.write(body)
}

func (c *Client) write(body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n%s", len(data), data)
	return err
}

// readLoop is the only reader of the server's stdout: it parses
// Content-Length framed messages and feeds them to the incoming channel
// until the stream ends.
func (c *Client) readLoop(reader *bufio.Reader) {
	defer close(c.incoming)
	for {
		var length int
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if v, ok := strings.CutPrefix(line, "Content-Length: "); ok {
				length, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		if length <= 0 || length > 32<<20 {
			c.incoming <- readResult{err: fmt.Errorf("invalid content length %d", length)}
			return
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(reader, body); err != nil {
			return
		}
		var msg message
		if err := json.Unmarshal(body, &msg); err != nil {
			continue // tolerate one malformed frame
		}
		c.incoming <- readResult{msg: msg}
	}
}

// read returns the next framed message, or an error on timeout, stream
// end, or context cancellation.
func (c *Client) read(ctx context.Context, timeout time.Duration) (message, error) {
	select {
	case r, ok := <-c.incoming:
		if !ok {
			return message{}, io.EOF
		}
		return r.msg, r.err
	case <-time.After(timeout):
		return message{}, fmt.Errorf("read timed out")
	case <-ctx.Done():
		return message{}, ctx.Err()
	}
}

func (c *Client) relPath(path string) string {
	if rel, err := filepath.Rel(c.root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}

func pathToURI(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path // windows drive paths
	}
	return "file://" + path
}

func uriToPath(uri string) string {
	path := strings.TrimPrefix(uri, "file://")
	if strings.Contains(path, ":") && strings.HasPrefix(path, "/") {
		path = strings.TrimPrefix(path, "/") // windows /C:/...
	}
	return filepath.FromSlash(path)
}
