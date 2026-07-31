package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxErrorBody = 64 * 1024

var errStreamIdleTimeout = errors.New("provider response stream exceeded its idle timeout")

func newJSONRequest(ctx context.Context, method, url string, body any) (*http.Request, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	return req, nil
}

func checkResponse(resp *http.Response, providerName, operation string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	return responseError(resp, providerName, operation, body)
}

func sseLines(body io.Reader, handle func(event, data string) error) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 32*1024), 4*1024*1024)
	var event string
	var data []string
	flush := func() error {
		if len(data) == 0 {
			event = ""
			return nil
		}
		err := handle(event, strings.Join(data, "\n"))
		event, data = "", nil
		return err
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

// newHTTPClient applies separate connect, whole-request, and response-idle
// bounds. The idle timeout is enforced per read and therefore also catches a
// streaming endpoint that connects successfully and then stops producing
// events.
func newHTTPClient(connectTimeout, requestTimeout, idleTimeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: connectTimeout, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = dialer.DialContext
	return &http.Client{
		Transport: idleTimeoutTransport{base: transport, timeout: idleTimeout},
		Timeout:   requestTimeout,
	}
}

func httpClient() *http.Client {
	return newHTTPClient(10*time.Second, 30*time.Minute, 5*time.Minute)
}

// retryableStatus reports whether a response indicates a transient failure
// worth retrying: timeouts, rate limits, server errors, and overload.
func retryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout, 529:
		return true
	}
	return false
}

// doWithRetry sends a request, retrying transient failures (network errors
// and retryable statuses) up to three attempts with exponential backoff and
// jitter, honoring Retry-After. Requests built by newJSONRequest or with a
// nil body are replayable. The final attempt's response is returned as-is so
// the caller's error reporting stays unchanged.
func doWithRetry(client *http.Client, req *http.Request, label, operation string) (*http.Response, error) {
	const attempts = 3
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		attemptReq := req
		if attempt > 0 {
			attemptReq = req.Clone(req.Context())
			if req.GetBody != nil {
				body, err := req.GetBody()
				if err != nil {
					return nil, err
				}
				attemptReq.Body = body
			}
		}
		resp, err := client.Do(attemptReq)
		if err == nil {
			if !retryableStatus(resp.StatusCode) || attempt == attempts-1 {
				return resp, nil
			}
			if req.Body != nil && req.GetBody == nil {
				return resp, nil
			}
			retryAfterHeader := resp.Header.Get("Retry-After")
			retryAfter := parseRetryAfter(retryAfterHeader)
			body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
			resp.Body.Close()
			lastErr = responseError(resp, label, operation, body)
			delay := backoffDelay(attempt, retryAfter)
			if strings.TrimSpace(retryAfterHeader) != "" && retryAfter == 0 {
				delay = 0
			}
			if wait(req.Context(), delay) != nil {
				return nil, classifyTransportError(req.Context(), label, operation, req.Context().Err())
			}
			continue
		}
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		lastErr = classifyTransportError(req.Context(), label, operation, err)
		if req.Context().Err() != nil {
			return nil, lastErr
		}
		if req.Body != nil && req.GetBody == nil {
			return nil, lastErr
		}
		if wait(req.Context(), backoffDelay(attempt, 0)) != nil {
			return nil, classifyTransportError(req.Context(), label, operation, req.Context().Err())
		}
	}
	return nil, lastErr
}

func backoffDelay(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > 30*time.Second {
			retryAfter = 30 * time.Second
		}
		return retryAfter
	}
	base := time.Duration(1<<attempt) * 500 * time.Millisecond
	jitter := time.Duration(rand.Int63n(int64(base) / 2))
	return base + jitter
}

func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if d := time.Until(at); d > 0 {
			return d
		}
	}
	return 0
}

func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func applyHeaders(req *http.Request, headers map[string]string) {
	for key, value := range headers {
		if value != "" {
			req.Header.Set(key, value)
		}
	}
}

func rawObject(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`{}`)
	}
	return raw
}

// toolParameterSchema decodes a tool's declared input schema into the form an
// adapter puts on the wire, guaranteeing that an object schema carries a
// `properties` key.
//
// JSON Schema does not require it: `{"type":"object"}` is complete and means
// "any object". Some servers validate tool definitions more strictly than the
// spec does, and reject the whole request rather than the one tool — LM Studio
// answers a parameterless tool with `invalid_type` at
// `[n, "function", "parameters", "properties"]`, which fails every request in
// the session and names only a numeric index to find it by.
//
// Adding the key changes nothing semantically in either direction: with
// `additionalProperties: false` the schema already admits no properties, and
// without it, additional properties are still permitted. It is applied here
// rather than at each tool because tools arriving over MCP are written by
// somebody else and cannot be fixed at the source.
func toolParameterSchema(name string, raw json.RawMessage) (any, error) {
	var decoded any
	if err := json.Unmarshal(rawObject(raw), &decoded); err != nil {
		return nil, fmt.Errorf("tool %s schema: %w", name, err)
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return decoded, nil
	}
	// Only object schemas get the key. A tool declaring a non-object parameter
	// schema is unusual, but adding `properties` to it would be meaningless at
	// best and misleading at worst.
	if kind, present := object["type"]; present && kind != "object" {
		return decoded, nil
	}
	if _, present := object["properties"]; !present {
		object["properties"] = map[string]any{}
	}
	return object, nil
}

type idleTimeoutTransport struct {
	base    http.RoundTripper
	timeout time.Duration
}

func (t idleTimeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil || t.timeout <= 0 {
		return resp, err
	}
	resp.Body = newIdleTimeoutBody(resp.Body, t.timeout)
	return resp, nil
}

type idleTimeoutBody struct {
	body     io.ReadCloser
	timeout  time.Duration
	mu       sync.Mutex
	timer    *time.Timer
	closed   bool
	timedOut bool
}

func newIdleTimeoutBody(body io.ReadCloser, timeout time.Duration) *idleTimeoutBody {
	b := &idleTimeoutBody{body: body, timeout: timeout}
	b.timer = time.AfterFunc(timeout, b.expire)
	return b
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	b.mu.Lock()
	timedOut := b.timedOut
	if n > 0 && !b.closed && !timedOut {
		b.timer.Stop()
		b.timer.Reset(b.timeout)
	}
	if err != nil || timedOut {
		b.timer.Stop()
	}
	b.mu.Unlock()
	if timedOut {
		return n, errStreamIdleTimeout
	}
	return n, err
}

func (b *idleTimeoutBody) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.timer.Stop()
	b.mu.Unlock()
	return b.body.Close()
}

func (b *idleTimeoutBody) expire() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.timedOut = true
	b.closed = true
	b.mu.Unlock()
	_ = b.body.Close()
}
