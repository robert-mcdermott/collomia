// Package logging provides Collomia's structured debug log: JSON records
// written outside the workspace, with the message and every attribute that can
// carry text passed through the session redactor before it reaches disk.
//
// "Every attribute that can carry text" is the exact claim, and the qualifier
// is load-bearing. Numbers, booleans, durations, and timestamps are written
// through unchanged because they cannot hold a credential; everything else —
// strings, structs, slices, errors — is redacted. The earlier wording here
// promised *every* attribute while the code checked only for strings, which
// meant the one guarantee this package makes was the one thing it did not do.
//
// Redaction is a defence in depth, not the boundary. The redactor knows the
// secrets the configuration named (see app.NewRedactor); a credential that
// reaches a log without ever passing through configuration is not something it
// can recognize. The log is written 0600 under the per-user Collomia root and
// never inside the workspace, so it cannot be committed by accident.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/robert-mcdermott/collomia/internal/userconfig"
)

// Dir returns the log directory under the per-user Collomia root — never the
// workspace, so logs cannot leak into commits.
func Dir() (string, error) {
	return userconfig.Path("logs")
}

// Setup opens a session log file and returns a logger plus the file path.
// When debug is false the logger discards everything below Warn.
func Setup(debug bool, redact func(string) string) (*slog.Logger, string, error) {
	dir, err := Dir()
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	path := filepath.Join(dir, time.Now().UTC().Format("20060102-150405")+fmt.Sprintf("-%d.log", os.Getpid()))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, "", err
	}
	level := slog.LevelWarn
	if debug {
		level = slog.LevelDebug
	}
	handler := slog.NewJSONHandler(file, &slog.HandlerOptions{Level: level, ReplaceAttr: redactAttr(redact)})
	return slog.New(&closingHandler{Handler: handler, closer: file}), path, nil
}

// Discard returns a logger that drops everything; used when logging setup
// fails so callers never need nil checks.
func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.Level(127)}))
}

// redactAttr passes every attribute that can carry a secret through the
// session redactor.
//
// The kinds are enumerated deliberately rather than filtered to strings, which
// is what this did before and is the reason the package comment above used to
// be false. Guarding on slog.KindString covers the message and ordinary string
// attributes and misses everything else — a struct, a slice, and above all an
// error, since slog.Any("err", err) is the most idiomatic call in the package
// and a provider error carries the response body that may echo a token back.
// That was latent rather than live (every call site here passes strings), and
// "one ordinary-looking line away from writing a live API key to disk" is not
// a property a debug log should have.
//
// Two kinds are handled differently on purpose:
//
//   - KindAny is rendered to its string form and redacted, which changes the
//     JSON type of that field from an object to a string. That is a real loss
//     of fidelity in a debug log, and it is the right trade: slog builds a
//     KindAny only for values it could not map to a scalar, so the set of
//     things affected is exactly the set that can hold a secret.
//   - The scalar kinds — bool, ints, floats, durations, times — are left
//     untouched. A number cannot contain a credential, and stringifying them
//     would make the log worse for no gain.
//
// LogValuer values need no case: slog resolves them before ReplaceAttr runs,
// so they arrive here as whatever they resolved to and are covered by it.
func redactAttr(redact func(string) string) func([]string, slog.Attr) slog.Attr {
	if redact == nil {
		return nil
	}
	return func(_ []string, attr slog.Attr) slog.Attr {
		switch attr.Value.Kind() {
		case slog.KindString:
			attr.Value = slog.StringValue(redact(attr.Value.String()))
		case slog.KindAny:
			// Value.String() renders the underlying value with fmt, which
			// reaches an error's Error(), a struct's fields, and a slice's
			// elements alike.
			attr.Value = slog.StringValue(redact(attr.Value.String()))
		}
		return attr
	}
}

type closingHandler struct {
	slog.Handler
	closer io.Closer
}

func (h *closingHandler) Handle(ctx context.Context, record slog.Record) error {
	return h.Handler.Handle(ctx, record)
}

// Close flushes the underlying file. Exposed for the app runtime shutdown.
func (h *closingHandler) Close() error { return h.closer.Close() }

// Close closes the file behind a logger produced by Setup, when possible.
func Close(logger *slog.Logger) {
	if h, ok := logger.Handler().(*closingHandler); ok {
		_ = h.Close()
	}
}
