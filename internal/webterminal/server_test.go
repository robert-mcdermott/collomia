package webterminal

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type fakeProcess struct {
	reader      *io.PipeReader
	writer      *io.PipeWriter
	writesMu    sync.Mutex
	writes      bytes.Buffer
	resizes     chan [2]int
	exit        chan sessionResult
	exitOnce    sync.Once
	closeOnce   sync.Once
	wasCanceled chan struct{}
}

func newFakeProcess() *fakeProcess {
	reader, writer := io.Pipe()
	return &fakeProcess{
		reader:      reader,
		writer:      writer,
		resizes:     make(chan [2]int, 4),
		exit:        make(chan sessionResult, 1),
		wasCanceled: make(chan struct{}),
	}
}

func (p *fakeProcess) Read(data []byte) (int, error) { return p.reader.Read(data) }

func (p *fakeProcess) Write(data []byte) (int, error) {
	p.writesMu.Lock()
	defer p.writesMu.Unlock()
	return p.writes.Write(data)
}

func (p *fakeProcess) Resize(cols, rows int) error {
	p.resizes <- [2]int{cols, rows}
	return nil
}

func (p *fakeProcess) Wait() (int, error) {
	result := <-p.exit
	_ = p.writer.Close()
	return result.code, result.err
}

func (p *fakeProcess) Close() error {
	p.closeOnce.Do(func() { _ = p.reader.Close() })
	return nil
}

func (p *fakeProcess) finish(code int, err error) {
	p.exitOnce.Do(func() { p.exit <- sessionResult{code: code, err: err} })
}

func (p *fakeProcess) written() string {
	p.writesMu.Lock()
	defer p.writesMu.Unlock()
	return p.writes.String()
}

type serverHarness struct {
	t          *testing.T
	server     *server
	httpServer *httptest.Server
	process    *fakeProcess
	started    chan struct{}
	cancel     context.CancelFunc
}

func newServerHarness(t *testing.T) *serverHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	process := newFakeProcess()
	started := make(chan struct{})
	var startOnce sync.Once
	s := &server{
		ctx:         ctx,
		token:       "test-token",
		spec:        processSpec{Executable: "fake", Cols: defaultCols, Rows: defaultRows},
		done:        make(chan sessionResult, 1),
		static:      http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "static") }),
		authTimeout: time.Second,
	}
	s.start = func(ctx context.Context, _ processSpec) (terminalProcess, error) {
		startOnce.Do(func() { close(started) })
		go func() {
			<-ctx.Done()
			close(process.wasCanceled)
			process.finish(143, ctx.Err())
		}()
		return process, nil
	}
	httpServer := httptest.NewServer(s)
	s.origin = httpServer.URL
	h := &serverHarness{t: t, server: s, httpServer: httpServer, process: process, started: started, cancel: cancel}
	t.Cleanup(func() {
		cancel()
		process.finish(0, nil)
		httpServer.Close()
	})
	return h
}

func (h *serverHarness) dial(origin string) (*websocket.Conn, *http.Response, error) {
	h.t.Helper()
	header := make(http.Header)
	header.Set("Origin", origin)
	return websocket.Dial(h.t.Context(), h.httpServer.URL+"/ws", &websocket.DialOptions{HTTPHeader: header})
}

func authenticate(t *testing.T, conn *websocket.Conn, token string) {
	t.Helper()
	data, err := json.Marshal(controlMessage{Type: "auth", Token: token})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(t.Context(), websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func TestWebSocketRejectsWrongOrigin(t *testing.T) {
	h := newServerHarness(t)
	conn, response, err := h.dial("https://attacker.example")
	if conn != nil {
		_ = conn.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("expected forbidden handshake, conn=%v response=%v err=%v", conn, response, err)
	}
	select {
	case <-h.started:
		t.Fatal("terminal started for a forbidden origin")
	default:
	}
}

func TestWebSocketRequiresTokenBeforeStartingTerminal(t *testing.T) {
	h := newServerHarness(t)
	conn, _, err := h.dial(h.httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	authenticate(t, conn, "wrong-token")
	readCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, _, err = conn.Read(readCtx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("expected policy-violation close, got %v", err)
	}
	select {
	case <-h.started:
		t.Fatal("terminal started before successful authentication")
	default:
	}
}

func TestWebSocketForwardsInputAndResize(t *testing.T) {
	h := newServerHarness(t)
	conn, _, err := h.dial(h.httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	authenticate(t, conn, h.server.token)
	select {
	case <-h.started:
	case <-time.After(time.Second):
		t.Fatal("terminal did not start")
	}
	resize, _ := json.Marshal(controlMessage{Type: "resize", Cols: 132, Rows: 43})
	if err := conn.Write(t.Context(), websocket.MessageText, resize); err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(t.Context(), websocket.MessageBinary, []byte("hello\x03")); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-h.process.resizes:
		if got != [2]int{132, 43} {
			t.Fatalf("resize=%v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("resize was not forwarded")
	}
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(h.process.written(), "hello\x03") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := h.process.written(); got != "hello\x03" {
		t.Fatalf("input=%q", got)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "test complete")
	select {
	case <-h.process.wasCanceled:
	case <-time.After(time.Second):
		t.Fatal("browser disconnect did not cancel the terminal")
	}
}

func TestAdditionalControllerIsRejected(t *testing.T) {
	h := newServerHarness(t)
	first, _, err := h.dial(h.httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	authenticate(t, first, h.server.token)
	select {
	case <-h.started:
	case <-time.After(time.Second):
		t.Fatal("first terminal did not start")
	}
	second, response, err := h.dial(h.httpServer.URL)
	if second != nil {
		_ = second.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusConflict {
		t.Fatalf("expected conflict for second controller, response=%v err=%v", response, err)
	}
	_ = first.Close(websocket.StatusNormalClosure, "test complete")
}

func TestTerminalExitAndOutputReachBrowser(t *testing.T) {
	h := newServerHarness(t)
	conn, _, err := h.dial(h.httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	authenticate(t, conn, h.server.token)
	select {
	case <-h.started:
	case <-time.After(time.Second):
		t.Fatal("terminal did not start")
	}
	if _, err := h.process.writer.Write([]byte("\x1b[32mready\x1b[0m")); err != nil {
		t.Fatal(err)
	}
	h.process.finish(7, nil)

	readCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	typ, data, err := conn.Read(readCtx)
	if err != nil || typ != websocket.MessageBinary || !bytes.Contains(data, []byte("ready")) {
		t.Fatalf("output type=%v data=%q err=%v", typ, data, err)
	}
	typ, data, err = conn.Read(readCtx)
	if err != nil || typ != websocket.MessageText {
		t.Fatalf("exit type=%v data=%q err=%v", typ, data, err)
	}
	var message controlMessage
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatal(err)
	}
	if message.Type != "exit" || message.Code != 7 {
		t.Fatalf("exit message=%+v", message)
	}
}

func TestStaticAssetsUseSecurityHeaders(t *testing.T) {
	h := newServerHarness(t)
	response, err := http.Get(h.httpServer.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	for _, header := range []string{"Content-Security-Policy", "Permissions-Policy", "Referrer-Policy", "X-Content-Type-Options", "X-Frame-Options"} {
		if response.Header.Get(header) == "" {
			t.Errorf("missing %s", header)
		}
	}
}

func TestTokenAndTerminalSizeValidation(t *testing.T) {
	first, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 40 || first == second || strings.ContainsAny(first, "+/=") {
		t.Fatalf("tokens are not independent URL-safe 256-bit values: %q %q", first, second)
	}
	for _, size := range [][2]int{{2, 2}, {maxCols, maxRows}} {
		if err := validateSize(size[0], size[1]); err != nil {
			t.Errorf("valid size %v: %v", size, err)
		}
	}
	for _, size := range [][2]int{{1, 24}, {80, 1}, {maxCols + 1, 24}, {80, maxRows + 1}} {
		if err := validateSize(size[0], size[1]); err == nil {
			t.Errorf("invalid size accepted: %v", size)
		}
	}
}
