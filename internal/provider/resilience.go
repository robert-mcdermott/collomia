package provider

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const (
	defaultCircuitThreshold = 3
	defaultCircuitCooldown  = 30 * time.Second
)

type HealthState string

const (
	HealthUnknown  HealthState = "unknown"
	HealthHealthy  HealthState = "healthy"
	HealthDegraded HealthState = "degraded"
	HealthOpen     HealthState = "circuit_open"
	HealthHalfOpen HealthState = "half_open"
)

// Health is a credential-safe snapshot of the active provider client's
// recent behavior. It is process-local and resets when the model changes.
type Health struct {
	State               HealthState
	ConsecutiveFailures int
	LastErrorKind       ErrorKind
	LastStatusCode      int
	LastRequestID       string
	RetryAt             time.Time
	LastSuccess         time.Time
}

func (h Health) Summary() string {
	switch h.State {
	case HealthHealthy:
		return "healthy"
	case HealthDegraded:
		if h.LastErrorKind != "" {
			return fmt.Sprintf("degraded (%s)", h.LastErrorKind)
		}
		return "degraded"
	case HealthOpen:
		if !h.RetryAt.IsZero() {
			remaining := time.Until(h.RetryAt).Round(time.Second)
			if remaining < 0 {
				remaining = 0
			}
			return fmt.Sprintf("circuit open (retry in %s)", remaining)
		}
		return "circuit open"
	case HealthHalfOpen:
		return "testing recovery"
	default:
		return "not checked yet"
	}
}

type HealthReporter interface {
	Health() Health
}

// WithResilience adds health tracking and a circuit breaker to a client. The
// adapters still own per-request retry behavior; the circuit operates across
// complete failed calls and therefore cannot duplicate a tool execution.
func WithResilience(client Client) Client {
	if client == nil {
		return nil
	}
	if _, ok := client.(*resilientClient); ok {
		return client
	}
	return newResilientClient(client, defaultCircuitThreshold, defaultCircuitCooldown, time.Now)
}

type resilientClient struct {
	inner     Client
	threshold int
	cooldown  time.Duration
	now       func() time.Time

	mu       sync.Mutex
	health   Health
	halfOpen bool
}

func newResilientClient(client Client, threshold int, cooldown time.Duration, now func() time.Time) *resilientClient {
	return &resilientClient{inner: client, threshold: threshold, cooldown: cooldown, now: now, health: Health{State: HealthUnknown}}
}

func (c *resilientClient) Name() string { return c.inner.Name() }

func (c *resilientClient) Capabilities() Capabilities {
	if reporter, ok := c.inner.(CapabilityReporter); ok {
		return reporter.Capabilities()
	}
	return Capabilities{}
}

func (c *resilientClient) Health() Health {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.health
}

func (c *resilientClient) Chat(ctx context.Context, req Request, onDelta func(Delta)) (Response, error) {
	if err := c.before(ctx); err != nil {
		return Response{}, err
	}
	response, err := c.inner.Chat(ctx, req, onDelta)
	c.after(err)
	return response, err
}

func (c *resilientClient) ListModels(ctx context.Context) ([]ModelInfo, error) {
	lister, ok := c.inner.(ModelLister)
	if !ok {
		return nil, fmt.Errorf("provider %s does not support model discovery", c.Name())
	}
	if err := c.before(ctx); err != nil {
		return nil, err
	}
	models, err := lister.ListModels(ctx)
	c.after(err)
	return models, err
}

func (c *resilientClient) before(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.halfOpen {
		return &Error{Provider: c.Name(), Operation: "request", Kind: ErrorUnavailable, Retryable: true, Message: "provider recovery check is already in progress"}
	}
	if c.health.State != HealthOpen {
		return nil
	}
	now := c.now()
	if now.Before(c.health.RetryAt) {
		retryAfter := c.health.RetryAt.Sub(now)
		if retryAfter < 0 {
			retryAfter = 0
		}
		return &Error{Provider: c.Name(), Operation: "request", Kind: ErrorUnavailable, Retryable: true, RetryAfter: retryAfter, Message: "provider circuit is open after repeated transient failures"}
	}
	c.halfOpen = true
	c.health.State = HealthHalfOpen
	return nil
}

func (c *resilientClient) after(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.halfOpen = false
	if err == nil {
		c.health = Health{State: HealthHealthy, LastSuccess: c.now()}
		return
	}
	providerErr, ok := AsError(err)
	if !ok {
		providerErr = classifyTransportError(context.Background(), c.Name(), "request", err)
	}
	c.health.State = HealthDegraded
	c.health.LastErrorKind = providerErr.Kind
	c.health.LastStatusCode = providerErr.StatusCode
	c.health.LastRequestID = providerErr.RequestID
	if !providerErr.Retryable || providerErr.Kind == ErrorCancelled {
		c.health.ConsecutiveFailures = 0
		return
	}
	c.health.ConsecutiveFailures++
	if c.health.ConsecutiveFailures >= c.threshold {
		c.health.State = HealthOpen
		c.health.RetryAt = c.now().Add(c.cooldown)
	}
}
