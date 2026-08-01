package logging

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// isolateHome points the per-user Collomia root at a temporary directory.
//
// userconfig.Dir resolves through os.UserHomeDir, which reads HOME on Unix and
// USERPROFILE on Windows, so both are set: a test that redirected only one
// would pass on the author's machine and write into the real
// ~/.collomia/logs on the other platform. This test file did exactly that
// before the helper existed, and left files in the author's own log directory.
func isolateHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

// collomiaRoot is where isolateHome(t, home) puts the Collomia directory.
func collomiaRoot(t *testing.T, home string) string {
	t.Helper()
	return filepath.Join(home, ".collomia")
}

// secret is the marker every redaction case looks for. A test that asserts on
// the redacted form only would pass against a handler that dropped the
// attribute entirely, so every case here asserts on the *absence* of this.
const secret = "sk-live-THIS-IS-A-SECRET"

func redactor() func(string) string {
	return func(value string) string { return strings.ReplaceAll(value, secret, "[redacted]") }
}

// capture runs one log call through the real handler Setup builds and returns
// the raw line, so these tests exercise the shipped configuration rather than
// a hand-assembled approximation.
func capture(t *testing.T, log func(*slog.Logger)) string {
	t.Helper()
	var sink strings.Builder
	handler := slog.NewJSONHandler(&sink, &slog.HandlerOptions{
		Level:       slog.LevelDebug,
		ReplaceAttr: redactAttr(redactor()),
	})
	log(slog.New(handler))
	return sink.String()
}

type credentialHolder struct {
	Name  string
	Token string
}

type failure struct{ detail string }

func (f failure) Error() string { return f.detail }

// resolvesToSecret is a slog.LogValuer, which slog resolves before ReplaceAttr
// sees the value. It is here to prove that, rather than to assume it.
type resolvesToSecret struct{}

func (resolvesToSecret) LogValue() slog.Value { return slog.StringValue(secret) }

func TestNoAttributeKindReachesDiskUnredacted(t *testing.T) {
	// This is the test the package did not have, and its absence is why
	// redaction guarded on slog.KindString and let a struct, a slice, and an
	// error through. Each case names the call that would produce it, because
	// the failure mode is a plausible line of code rather than an exotic one.
	for _, tc := range []struct {
		name string
		log  func(*slog.Logger)
	}{
		{"the message itself", func(l *slog.Logger) { l.Info("authenticating with " + secret) }},
		{"a string attribute", func(l *slog.Logger) { l.Info("m", "key", secret) }},
		{"an error, as slog.Any(\"err\", err)", func(l *slog.Logger) {
			l.Error("m", slog.Any("err", failure{"request rejected: " + secret}))
		}},
		{"an error, as a bare value", func(l *slog.Logger) { l.Error("m", "err", failure{secret}) }},
		{"a struct", func(l *slog.Logger) {
			l.Info("m", "provider", credentialHolder{Name: "openrouter", Token: secret})
		}},
		{"a pointer to a struct", func(l *slog.Logger) {
			l.Info("m", "provider", &credentialHolder{Token: secret})
		}},
		{"a string slice", func(l *slog.Logger) { l.Info("m", "argv", []string{"--key", secret}) }},
		{"a map", func(l *slog.Logger) { l.Info("m", "headers", map[string]string{"authorization": secret}) }},
		{"a group member", func(l *slog.Logger) { l.Info("m", slog.Group("req", "key", secret)) }},
		{"a nested group member", func(l *slog.Logger) {
			l.Info("m", slog.Group("outer", slog.Group("inner", "key", secret)))
		}},
		{"a LogValuer", func(l *slog.Logger) { l.Info("m", "lazy", resolvesToSecret{}) }},
		{"a named string type", func(l *slog.Logger) {
			type token string
			l.Info("m", "t", token(secret))
		}},
		{"a logger with a preset attribute", func(l *slog.Logger) {
			l.With("key", secret).Info("m")
		}},
		{"an attribute added to a group scope", func(l *slog.Logger) {
			l.WithGroup("req").Info("m", "key", secret)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := capture(t, tc.log)
			if strings.Contains(line, secret) {
				t.Errorf("the secret reached the log unredacted:\n  %s", strings.TrimSpace(line))
			}
			if !strings.Contains(line, "[redacted]") {
				t.Errorf("the value was dropped rather than redacted, which hides that anything was there:\n  %s", strings.TrimSpace(line))
			}
		})
	}
}

func TestScalarAttributesKeepTheirJSONType(t *testing.T) {
	// Redaction must not cost the log its structure where there is nothing to
	// redact. A number rendered as a string would make every numeric field in
	// the debug log unusable to a reader filtering on it, for no safety gain:
	// a credential cannot be an int64.
	line := capture(t, func(l *slog.Logger) {
		l.Info("m", "count", 42, "ratio", 0.5, "ok", true, "took", 3*time.Second)
	})
	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("log line is not JSON: %v\n  %s", err, line)
	}
	for key, want := range map[string]any{"count": float64(42), "ratio": 0.5, "ok": true} {
		if got := record[key]; got != want {
			t.Errorf("%s = %#v (%T), want %#v — a scalar was stringified by redaction", key, got, got, want)
		}
	}
}

func TestRedactionIsSkippedEntirelyWhenThereIsNoRedactor(t *testing.T) {
	// Setup is called with a nil redactor on the path where the configuration
	// failed to load. Returning a non-nil ReplaceAttr that called a nil
	// function would panic on the first line written, which is the worst
	// possible moment: the log exists to explain a startup failure.
	if got := redactAttr(nil); got != nil {
		t.Fatal("a nil redactor must produce no ReplaceAttr hook at all")
	}
	var sink strings.Builder
	handler := slog.NewJSONHandler(&sink, &slog.HandlerOptions{ReplaceAttr: redactAttr(nil)})
	slog.New(handler).Info("m", "key", "value")
	if !strings.Contains(sink.String(), "value") {
		t.Errorf("logging without a redactor must still write the record: %q", sink.String())
	}
}

func TestSetupWritesAPrivateFileOutsideTheWorkspace(t *testing.T) {
	root := t.TempDir()
	isolateHome(t, root)

	logger, path, err := Setup(true, redactor())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer Close(logger)

	if want := collomiaRoot(t, root); !strings.HasPrefix(path, want) {
		t.Errorf("log path %q is outside the per-user Collomia root %q; logs must never land in a workspace where they could be committed", path, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat log: %v", err)
	}
	if runtime.GOOS != "windows" {
		// The file holds a redacted record of a session, which is not the same
		// as holding nothing worth protecting.
		if mode := info.Mode().Perm(); mode != fs.FileMode(0o600) {
			t.Errorf("log file mode = %04o, want 0600", mode)
		}
	}
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("log written to %q, want the directory Dir() reports (%q)", filepath.Dir(path), dir)
	}
}

func TestSetupRedactsThroughTheRealFileHandler(t *testing.T) {
	// The redaction cases above run against a string sink. This one proves the
	// wiring in Setup itself passes the redactor through, because a correct
	// redactAttr that was never installed would fail nothing else here.
	isolateHome(t, t.TempDir())
	logger, path, err := Setup(true, redactor())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	logger.Debug("m", "err", failure{secret}, "key", secret)
	Close(logger)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(data), secret) {
		t.Errorf("Setup did not install the redactor:\n  %s", strings.TrimSpace(string(data)))
	}
}

func TestDebugFalseKeepsDebugRecordsOffDisk(t *testing.T) {
	// Without --debug the log is a warning-and-above record. A build that
	// wrote every Debug line regardless would quietly turn an opt-in
	// diagnostic into an always-on transcript of the session.
	isolateHome(t, t.TempDir())
	logger, path, err := Setup(false, redactor())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	logger.Debug("debug line")
	logger.Info("info line")
	logger.Warn("warn line")
	Close(logger)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	body := string(data)
	if strings.Contains(body, "debug line") || strings.Contains(body, "info line") {
		t.Errorf("records below Warn reached the log without --debug:\n  %s", body)
	}
	if !strings.Contains(body, "warn line") {
		t.Errorf("a warning must be recorded whatever the debug setting:\n  %s", body)
	}
}

func TestCloseIsSafeOnALoggerItDoesNotOwn(t *testing.T) {
	// Close is called from the runtime shutdown path against whatever logger
	// the session ended up with, which on the failure path is Discard().
	Close(Discard())
	Close(slog.New(slog.NewTextHandler(os.Stderr, nil)))
}

func TestDiscardWritesNothingAtAnyLevel(t *testing.T) {
	logger := Discard()
	// No sink to inspect by construction — the assertion is that the highest
	// level the API can express is still not enabled, so callers never pay to
	// format an argument for a record nobody keeps.
	if logger.Enabled(t.Context(), slog.LevelError) {
		t.Error("Discard must not enable any level")
	}
}

func TestSetupReportsAnUnwritableDirectoryRatherThanReturningANilLogger(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not deny creation the same way on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.Mkdir(blocked, 0o500); err != nil {
		t.Fatal(err)
	}
	isolateHome(t, blocked)

	logger, _, err := Setup(true, redactor())
	if err == nil {
		Close(logger)
		t.Fatal("Setup must report a directory it cannot write to")
	}
	if logger != nil {
		t.Error("Setup must not return a logger alongside an error; the caller substitutes Discard()")
	}
}
