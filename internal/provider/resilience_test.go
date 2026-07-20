package provider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type resilienceFixtureClient struct {
	mu       sync.Mutex
	calls    int
	results  []error
	response Response
}

func (c *resilienceFixtureClient) Name() string { return "fixture/model" }

func (c *resilienceFixtureClient) Chat(context.Context, Request, func(Delta)) (Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if len(c.results) == 0 {
		return c.response, nil
	}
	err := c.results[0]
	c.results = c.results[1:]
	return c.response, err
}

func TestProviderErrorClassifiesHTTPFailures(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Status:     "429 Too Many Requests",
		Header: http.Header{
			"Retry-After":      []string{"7"},
			"X-Amzn-Requestid": []string{"request-123"},
		},
		Body: io.NopCloser(strings.NewReader(`{"error":{"message":"quota exhausted","type":"rate_limit"}}`)),
	}
	err := checkResponse(resp, "fixture/model", "chat")
	providerErr, ok := AsError(err)
	if !ok {
		t.Fatalf("error type=%T: %v", err, err)
	}
	if providerErr.Kind != ErrorRateLimit || !providerErr.Retryable || providerErr.StatusCode != 429 || providerErr.RetryAfter != 7*time.Second || providerErr.RequestID != "request-123" || providerErr.Message != "quota exhausted" {
		t.Fatalf("classified error=%+v", providerErr)
	}
	for _, want := range []string{"rate_limit", "retry after 7s", "request id request-123"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestProviderErrorDoesNotRetryAuthenticationFailures(t *testing.T) {
	kind, retryable := classifyStatus(http.StatusUnauthorized)
	if kind != ErrorAuthentication || retryable {
		t.Fatalf("kind=%s retryable=%t", kind, retryable)
	}
	kind, retryable = classifyStatus(http.StatusServiceUnavailable)
	if kind != ErrorUnavailable || !retryable {
		t.Fatalf("kind=%s retryable=%t", kind, retryable)
	}
}

func TestRetryRefusesNonReplayableRequestBody(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("temporary network failure")
	})}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.invalid/chat", io.NopCloser(strings.NewReader("payload")))
	if err != nil {
		t.Fatal(err)
	}
	if req.GetBody != nil {
		t.Fatal("fixture body unexpectedly became replayable")
	}
	if _, err := doWithRetry(client, req, "fixture/model", "chat"); err == nil {
		t.Fatal("expected transport failure")
	}
	if calls != 1 {
		t.Fatalf("non-replayable body was retried: calls=%d", calls)
	}
}

func TestCircuitBreakerOpensAndRecovers(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	transient := &Error{Provider: "fixture/model", Operation: "chat", Kind: ErrorUnavailable, Retryable: true, Message: "overloaded"}
	inner := &resilienceFixtureClient{results: []error{transient, transient, nil}, response: Response{Content: "recovered"}}
	client := newResilientClient(inner, 2, 30*time.Second, func() time.Time { return now })

	for i := 0; i < 2; i++ {
		if _, err := client.Chat(t.Context(), Request{}, nil); err == nil {
			t.Fatal("expected transient failure")
		}
	}
	if health := client.Health(); health.State != HealthOpen || health.ConsecutiveFailures != 2 {
		t.Fatalf("health=%+v", health)
	}
	if _, err := client.Chat(t.Context(), Request{}, nil); err == nil || inner.calls != 2 {
		t.Fatalf("open circuit should reject without calling provider: calls=%d err=%v", inner.calls, err)
	}

	now = now.Add(31 * time.Second)
	response, err := client.Chat(t.Context(), Request{}, nil)
	if err != nil || response.Content != "recovered" {
		t.Fatalf("recovery response=%+v err=%v", response, err)
	}
	if health := client.Health(); health.State != HealthHealthy || health.ConsecutiveFailures != 0 || health.LastSuccess.IsZero() {
		t.Fatalf("health after recovery=%+v", health)
	}
}

func TestCircuitBreakerDoesNotOpenForInvalidRequests(t *testing.T) {
	invalid := &Error{Provider: "fixture/model", Operation: "chat", Kind: ErrorInvalidRequest, Retryable: false, Message: "bad model"}
	inner := &resilienceFixtureClient{results: []error{invalid, invalid, invalid, invalid}}
	client := newResilientClient(inner, 2, time.Minute, time.Now)
	for i := 0; i < 4; i++ {
		_, _ = client.Chat(t.Context(), Request{}, nil)
	}
	if health := client.Health(); health.State != HealthDegraded || health.ConsecutiveFailures != 0 {
		t.Fatalf("health=%+v", health)
	}
	if inner.calls != 4 {
		t.Fatalf("non-retryable failures should remain visible to the provider: calls=%d", inner.calls)
	}
}

type blockingReadCloser struct {
	closed chan struct{}
	once   sync.Once
}

func (b *blockingReadCloser) Read([]byte) (int, error) {
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *blockingReadCloser) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func TestResponseBodyIdleTimeoutInterruptsBlockedRead(t *testing.T) {
	body := newIdleTimeoutBody(&blockingReadCloser{closed: make(chan struct{})}, 10*time.Millisecond)
	done := make(chan error, 1)
	go func() {
		_, err := body.Read(make([]byte, 1))
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, errStreamIdleTimeout) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("idle timeout did not interrupt the blocked read")
	}
}

type cancellableStreamBody struct {
	prefix bytes.Reader
	ctx    context.Context
	closed chan struct{}
	once   sync.Once
}

func newCancellableStreamBody(ctx context.Context, prefix []byte) *cancellableStreamBody {
	return &cancellableStreamBody{prefix: *bytes.NewReader(prefix), ctx: ctx, closed: make(chan struct{})}
}

func (b *cancellableStreamBody) Read(dst []byte) (int, error) {
	if b.prefix.Len() > 0 {
		return b.prefix.Read(dst)
	}
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-b.closed:
		return 0, io.ErrClosedPipe
	}
}

func (b *cancellableStreamBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func TestProviderStreamsHonorCancellation(t *testing.T) {
	bedrockPrefix := bedrockEventStream(t,
		bedrockEvent{"messageStart", `{"role":"assistant"}`},
		bedrockEvent{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"partial"}}`},
	)
	tests := []struct {
		name        string
		contentType string
		prefix      []byte
		client      func(*http.Client) Client
	}{
		{
			name: "openai", contentType: "text/event-stream",
			prefix: []byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"),
			client: func(httpClient *http.Client) Client {
				return &OpenAIClient{Label: "openai-cancel", BaseURL: "https://example.invalid", APIKey: "secret", HTTP: httpClient}
			},
		},
		{
			name: "anthropic", contentType: "text/event-stream",
			prefix: []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n"),
			client: func(httpClient *http.Client) Client {
				return &AnthropicClient{Label: "anthropic-cancel", BaseURL: "https://example.invalid", APIKey: "secret", HTTP: httpClient}
			},
		},
		{
			name: "responses", contentType: "text/event-stream",
			prefix: []byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"partial\"}\n\n"),
			client: func(httpClient *http.Client) Client {
				return &ResponsesClient{Label: "responses-cancel", BaseURL: "https://example.invalid", APIKey: "secret", HTTP: httpClient}
			},
		},
		{
			name: "bedrock", contentType: "application/vnd.amazon.eventstream", prefix: bedrockPrefix,
			client: func(httpClient *http.Client) Client {
				return &BedrockClient{Label: "bedrock-cancel", Region: "us-east-1", Auth: "bearer", APIKey: "secret", HTTP: httpClient}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var body *cancellableStreamBody
			calls := 0
			httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				body = newCancellableStreamBody(req.Context(), test.prefix)
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": []string{test.contentType}},
					Body:       body,
					Request:    req,
				}, nil
			})}
			client := test.client(httpClient)
			streamed := make(chan struct{}, 1)
			done := make(chan error, 1)
			go func() {
				_, err := client.Chat(ctx, contractRequest(), func(delta Delta) {
					if delta.Text == "partial" {
						select {
						case streamed <- struct{}{}:
						default:
						}
					}
				})
				done <- err
			}()

			select {
			case <-streamed:
				cancel()
			case <-time.After(time.Second):
				t.Fatal("provider did not emit the initial stream delta")
			}

			select {
			case err := <-done:
				providerErr, ok := AsError(err)
				if !ok || providerErr.Kind != ErrorCancelled || providerErr.Retryable {
					t.Fatalf("cancellation error=%+v raw=%v", providerErr, err)
				}
			case <-time.After(time.Second):
				t.Fatal("provider stream did not stop after cancellation")
			}
			if calls != 1 {
				t.Fatalf("cancelled stream was replayed: calls=%d", calls)
			}
			select {
			case <-body.closed:
			case <-time.After(time.Second):
				t.Fatal("cancelled response body was not closed")
			}
		})
	}
}
