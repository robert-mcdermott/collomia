package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxErrorBody = 64 * 1024

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

func checkResponse(resp *http.Response, providerName string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	return fmt.Errorf("%s returned %s: %s", providerName, resp.Status, strings.TrimSpace(string(body)))
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

func httpClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Minute}
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
func doWithRetry(client *http.Client, req *http.Request, label string) (*http.Response, error) {
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
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
			body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
			resp.Body.Close()
			lastErr = fmt.Errorf("%s returned %s: %s", label, resp.Status, strings.TrimSpace(string(body)))
			if wait(req.Context(), backoffDelay(attempt, retryAfter)) != nil {
				return nil, req.Context().Err()
			}
			continue
		}
		lastErr = err
		if req.Context().Err() != nil {
			return nil, err
		}
		if wait(req.Context(), backoffDelay(attempt, 0)) != nil {
			return nil, req.Context().Err()
		}
	}
	return nil, fmt.Errorf("%s: giving up after %d attempts: %w", label, attempts, lastErr)
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
