// Package logging provides Collomia's structured debug log: JSON records
// written outside the workspace, with every attribute passed through the
// session redactor before it reaches disk.
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

func redactAttr(redact func(string) string) func([]string, slog.Attr) slog.Attr {
	if redact == nil {
		return nil
	}
	return func(_ []string, attr slog.Attr) slog.Attr {
		if attr.Value.Kind() == slog.KindString {
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
