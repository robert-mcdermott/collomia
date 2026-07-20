package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// ErrorKind is a stable, provider-neutral classification suitable for user
// messages, retry decisions, health reporting, and future automation events.
type ErrorKind string

const (
	ErrorAuthentication ErrorKind = "authentication"
	ErrorPermission     ErrorKind = "permission"
	ErrorRateLimit      ErrorKind = "rate_limit"
	ErrorInvalidRequest ErrorKind = "invalid_request"
	ErrorNotFound       ErrorKind = "not_found"
	ErrorTimeout        ErrorKind = "timeout"
	ErrorUnavailable    ErrorKind = "unavailable"
	ErrorProtocol       ErrorKind = "protocol"
	ErrorCancelled      ErrorKind = "cancelled"
	ErrorUnknown        ErrorKind = "unknown"
)

// Error is the normalized failure returned by built-in provider clients.
// RequestID is safe diagnostic metadata; response bodies and credentials are
// never retained beyond the bounded, extracted Message.
type Error struct {
	Provider   string
	Operation  string
	Kind       ErrorKind
	StatusCode int
	Retryable  bool
	RetryAfter time.Duration
	RequestID  string
	Message    string
	Err        error
}

func (e *Error) Error() string {
	if e == nil {
		return "provider error"
	}
	label := strings.TrimSpace(e.Provider)
	if label == "" {
		label = "provider"
	}
	if e.Operation != "" {
		label += " " + e.Operation
	}
	parts := []string{fmt.Sprintf("%s failed (%s)", label, nonemptyKind(e.Kind))}
	if e.StatusCode > 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", e.StatusCode))
	}
	if message := strings.TrimSpace(e.Message); message != "" {
		parts = append(parts, message)
	}
	if e.RetryAfter > 0 {
		parts = append(parts, "retry after "+e.RetryAfter.Round(time.Second).String())
	}
	if e.RequestID != "" {
		parts = append(parts, "request id "+e.RequestID)
	}
	return strings.Join(parts, ": ")
}

func (e *Error) Unwrap() error { return e.Err }

func nonemptyKind(kind ErrorKind) ErrorKind {
	if kind == "" {
		return ErrorUnknown
	}
	return kind
}

// AsError exposes the normalized failure without callers depending on an
// adapter-specific response type.
func AsError(err error) (*Error, bool) {
	var providerErr *Error
	ok := errors.As(err, &providerErr)
	return providerErr, ok
}

func responseError(resp *http.Response, providerName, operation string, body []byte) error {
	kind, retryable := classifyStatus(resp.StatusCode)
	return &Error{
		Provider: providerName, Operation: operation, Kind: kind,
		StatusCode: resp.StatusCode, Retryable: retryable,
		RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		RequestID:  requestID(resp.Header),
		Message:    responseMessage(body, resp.Status),
	}
}

func classifyStatus(code int) (ErrorKind, bool) {
	switch code {
	case http.StatusBadRequest, http.StatusConflict, http.StatusLengthRequired,
		http.StatusPreconditionFailed, http.StatusRequestEntityTooLarge,
		http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		return ErrorInvalidRequest, false
	case http.StatusUnauthorized:
		return ErrorAuthentication, false
	case http.StatusForbidden:
		return ErrorPermission, false
	case http.StatusNotFound:
		return ErrorNotFound, false
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return ErrorTimeout, true
	case http.StatusTooManyRequests:
		return ErrorRateLimit, true
	case http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, 529:
		return ErrorUnavailable, true
	default:
		return ErrorUnknown, code >= 500
	}
}

func classifyTransportError(ctx context.Context, providerName, operation string, err error) *Error {
	if providerErr, ok := AsError(err); ok {
		return providerErr
	}
	kind, retryable := ErrorUnavailable, true
	switch {
	case errors.Is(err, context.Canceled), errors.Is(ctx.Err(), context.Canceled):
		kind, retryable = ErrorCancelled, false
	case errors.Is(err, context.DeadlineExceeded), errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, errStreamIdleTimeout):
		kind, retryable = ErrorTimeout, true
	default:
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			kind = ErrorTimeout
		}
	}
	return &Error{Provider: providerName, Operation: operation, Kind: kind, Retryable: retryable, Message: sanitizeProviderText(err.Error(), 2048), Err: err}
}

func protocolError(providerName, operation string, err error) error {
	if err == nil {
		return nil
	}
	if providerErr, ok := AsError(err); ok {
		return providerErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errStreamIdleTimeout) {
		return classifyTransportError(context.Background(), providerName, operation, err)
	}
	return &Error{Provider: providerName, Operation: operation, Kind: ErrorProtocol, Message: sanitizeProviderText(err.Error(), 2048), Err: err}
}

func responseMessage(body []byte, fallback string) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return fallback
	}
	var payload struct {
		Message string `json:"message"`
		Error   *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil {
		if payload.Error != nil && strings.TrimSpace(payload.Error.Message) != "" {
			return sanitizeProviderText(payload.Error.Message, 2048)
		}
		if strings.TrimSpace(payload.Message) != "" {
			return sanitizeProviderText(payload.Message, 2048)
		}
	}
	return sanitizeProviderText(trimmed, 2048)
}

func requestID(header http.Header) string {
	for _, name := range []string{"x-request-id", "request-id", "x-amzn-requestid", "x-amz-request-id"} {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			return sanitizeProviderText(value, 256)
		}
	}
	return ""
}

func sanitizeProviderText(value string, limit int) string {
	value = strings.Map(func(char rune) rune {
		if char < 0x20 || char == 0x7f {
			return ' '
		}
		return char
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return value
}
