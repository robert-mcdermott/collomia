package webterminal

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

const (
	defaultCols = 100
	defaultRows = 30
	maxCols     = 1000
	maxRows     = 500
	maxMessage  = 1 << 20
	authTimeout = 5 * time.Second
)

//go:embed assets
var embeddedAssets embed.FS

// Options configures one local browser-terminal session.
type Options struct {
	Executable  string
	Args        []string
	Dir         string
	Env         []string
	Port        int
	OpenBrowser bool
	Stderr      io.Writer
}

// ExitError reports the exact non-zero status returned by the TUI child.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string { return fmt.Sprintf("web terminal exited with status %d", e.Code) }

type sessionResult struct {
	code int
	err  error
}

type server struct {
	ctx         context.Context
	token       string
	origin      string
	spec        processSpec
	start       processStarter
	active      atomic.Bool
	done        chan sessionResult
	complete    sync.Once
	static      http.Handler
	authTimeout time.Duration
}

// Run starts a loopback-only web server and blocks until its terminal session
// exits or ctx is cancelled.
func Run(ctx context.Context, opts Options) error {
	if !platformPTYSupported {
		return errors.New("web terminal mode requires a pseudo-terminal, which this platform does not provide")
	}
	if strings.TrimSpace(opts.Executable) == "" {
		return errors.New("web terminal executable is required")
	}
	if opts.Port < 0 || opts.Port > 65535 {
		return fmt.Errorf("web terminal port must be between 0 and 65535, got %d", opts.Port)
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	ctx, stopSignals := withTerminationSignals(ctx)
	defer stopSignals()
	token, err := newToken()
	if err != nil {
		return fmt.Errorf("generate web terminal token: %w", err)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(opts.Port)))
	if err != nil {
		return fmt.Errorf("start web terminal listener: %w", err)
	}
	defer listener.Close()
	origin := "http://" + listener.Addr().String()
	assets, err := fs.Sub(embeddedAssets, "assets")
	if err != nil {
		return fmt.Errorf("load embedded web terminal assets: %w", err)
	}
	s := &server{
		ctx:         ctx,
		token:       token,
		origin:      origin,
		spec:        processSpec{Executable: opts.Executable, Args: append([]string(nil), opts.Args...), Dir: opts.Dir, Env: append([]string(nil), opts.Env...), Cols: defaultCols, Rows: defaultRows},
		start:       startPTY,
		done:        make(chan sessionResult, 1),
		static:      http.FileServer(http.FS(assets)),
		authTimeout: authTimeout,
	}
	httpServer := &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	serveDone := make(chan error, 1)
	go func() {
		err := httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveDone <- err
	}()

	browserURL := origin + "/#token=" + url.QueryEscape(token)
	fmt.Fprintln(opts.Stderr, "Web terminal:", browserURL)
	if opts.OpenBrowser {
		if err := openBrowser(browserURL); err != nil {
			fmt.Fprintf(opts.Stderr, "Could not open the default browser: %v\nOpen the URL above manually.\n", err)
		}
	}

	var result sessionResult
	var runErr error
	select {
	case <-ctx.Done():
		signal, interrupted := terminationSignal(ctx)
		if interrupted {
			result.code = 128 + int(signal)
		} else {
			runErr = ctx.Err()
		}
		if s.active.Load() {
			select {
			case childResult := <-s.done:
				if !interrupted {
					result = childResult
				}
			case <-time.After(3 * time.Second):
			}
		}
	case result = <-s.done:
	case runErr = <-serveDone:
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	select {
	case err := <-serveDone:
		if runErr == nil {
			runErr = err
		}
	default:
	}
	if runErr != nil {
		return runErr
	}
	if result.err != nil && result.code == 0 {
		return result.err
	}
	if result.code != 0 {
		return &ExitError{Code: result.code}
	}
	return nil
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w.Header())
	if r.URL.Path == "/ws" {
		s.serveWebSocket(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.static.ServeHTTP(w, r)
}

func (s *server) serveWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.validOrigin(r.Header.Get("Origin")) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	if s.active.Load() {
		http.Error(w, "a terminal controller is already connected", http.StatusConflict)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(maxMessage)
	authCtx, cancelAuth := context.WithTimeout(r.Context(), s.authTimeout)
	typ, data, err := conn.Read(authCtx)
	cancelAuth()
	if err != nil || typ != websocket.MessageText || !s.validAuth(data) {
		_ = conn.Close(websocket.StatusPolicyViolation, "authentication failed")
		return
	}
	if !s.active.CompareAndSwap(false, true) {
		_ = conn.Close(websocket.StatusPolicyViolation, "a terminal controller is already connected")
		return
	}

	sessionCtx, cancelSession := context.WithCancel(s.ctx)
	defer cancelSession()
	proc, err := s.start(sessionCtx, s.spec)
	if err != nil {
		s.sendControl(conn, controlMessage{Type: "error", Message: err.Error()})
		_ = conn.Close(websocket.StatusInternalError, "could not start terminal")
		s.finish(sessionResult{err: fmt.Errorf("start web terminal child: %w", err)})
		return
	}

	outputDone := make(chan error, 1)
	go func() { outputDone <- streamOutput(sessionCtx, conn, proc) }()
	inputDone := make(chan error, 1)
	go func() { inputDone <- s.streamInput(sessionCtx, conn, proc) }()
	waitDone := make(chan sessionResult, 1)
	go func() {
		code, err := proc.Wait()
		waitDone <- sessionResult{code: code, err: err}
	}()

	var result sessionResult
	naturalExit := false
	select {
	case result = <-waitDone:
		naturalExit = true
	case <-inputDone:
		cancelSession()
		result = <-waitDone
	case <-s.ctx.Done():
		cancelSession()
		result = <-waitDone
	}
	select {
	case <-outputDone:
	case <-time.After(500 * time.Millisecond):
	}
	_ = proc.Close()
	if naturalExit && s.ctx.Err() == nil {
		s.sendControl(conn, controlMessage{Type: "exit", Code: result.code})
		_ = conn.Close(websocket.StatusNormalClosure, "terminal exited")
	}
	_ = conn.CloseNow()
	s.finish(result)
}

func (s *server) streamInput(ctx context.Context, conn *websocket.Conn, proc terminalProcess) error {
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		switch typ {
		case websocket.MessageBinary:
			if len(data) > 0 {
				if _, err := proc.Write(data); err != nil {
					return err
				}
			}
		case websocket.MessageText:
			var message controlMessage
			if err := json.Unmarshal(data, &message); err != nil {
				return fmt.Errorf("invalid terminal control message: %w", err)
			}
			switch message.Type {
			case "input":
				if message.Data != "" {
					if _, err := io.WriteString(proc, message.Data); err != nil {
						return err
					}
				}
			case "resize":
				if err := validateSize(message.Cols, message.Rows); err != nil {
					return err
				}
				if err := proc.Resize(message.Cols, message.Rows); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unsupported terminal control message %q", message.Type)
			}
		}
	}
}

func streamOutput(ctx context.Context, conn *websocket.Conn, proc terminalProcess) error {
	buffer := make([]byte, 32*1024)
	for {
		n, err := proc.Read(buffer)
		if n > 0 {
			if writeErr := conn.Write(ctx, websocket.MessageBinary, buffer[:n]); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

type controlMessage struct {
	Type    string `json:"type"`
	Token   string `json:"token,omitempty"`
	Data    string `json:"data,omitempty"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func (s *server) sendControl(conn *websocket.Conn, message controlMessage) {
	data, err := json.Marshal(message)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = conn.Write(ctx, websocket.MessageText, data)
}

func (s *server) validAuth(data []byte) bool {
	var message controlMessage
	if err := json.Unmarshal(data, &message); err != nil || message.Type != "auth" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(message.Token), []byte(s.token)) == 1
}

func (s *server) validOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return parsed.Scheme+"://"+parsed.Host == s.origin
}

func (s *server) finish(result sessionResult) {
	s.complete.Do(func() { s.done <- result })
}

func newToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func validateSize(cols, rows int) error {
	if cols < 2 || cols > maxCols || rows < 2 || rows > maxRows {
		return fmt.Errorf("terminal size out of range: %dx%d", cols, rows)
	}
	return nil
}

func securityHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	// xterm.js creates runtime style elements for terminal geometry, so styles
	// require inline CSS. Scripts remain self-only with no inline exception.
	header.Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}
