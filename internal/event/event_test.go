package event

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewStampsSchemaAndTime(t *testing.T) {
	e := New(KindToolStart)
	if e.Schema != SchemaVersion || e.Time.IsZero() || e.Kind != KindToolStart {
		t.Fatalf("unexpected event: %+v", e)
	}
}

func TestJSONLWriterEmitsOneLinePerEvent(t *testing.T) {
	var out strings.Builder
	w := NewJSONLWriter(&out)
	e := New(KindTextDelta)
	e.Text = "hello"
	w.Handle(e)
	w.Handle(New(KindTurnEnd))
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), out.String())
	}
	var decoded Event
	if err := json.Unmarshal([]byte(lines[0]), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Kind != KindTextDelta || decoded.Text != "hello" || decoded.Schema != SchemaVersion {
		t.Fatalf("round trip mismatch: %+v", decoded)
	}
}

func TestRunResultRoundTrips(t *testing.T) {
	e := New(KindRunResult)
	e.Result = &RunResult{Status: "cancelled", Error: "context canceled", SessionID: "abc123", ChangedFiles: []string{"main.go"}, DurationMS: 1500}
	e.Usage = &Usage{InputTokens: 10, OutputTokens: 4}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Event
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Kind != KindRunResult || decoded.Result == nil || decoded.Result.Status != "cancelled" ||
		len(decoded.Result.ChangedFiles) != 1 || decoded.Usage == nil || decoded.Usage.InputTokens != 10 {
		t.Fatalf("round trip mismatch: %+v", decoded)
	}
}

func TestProviderFailureRoundTrips(t *testing.T) {
	e := New(KindError)
	e.Error = "rate limited"
	e.Provider = &ProviderFailure{Name: "openrouter/glm", Operation: "chat", Kind: "rate_limit", StatusCode: 429, Retryable: true, RetryAfterMS: 3000, RequestID: "req-123"}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Event
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Provider == nil || decoded.Provider.Kind != "rate_limit" || decoded.Provider.StatusCode != 429 || !decoded.Provider.Retryable || decoded.Provider.RetryAfterMS != 3000 || decoded.Provider.RequestID != "req-123" {
		t.Fatalf("provider failure round trip mismatch: %+v", decoded.Provider)
	}
}

func TestJSONLWriterAppliesRedaction(t *testing.T) {
	var out strings.Builder
	w := NewJSONLWriter(&out)
	w.Redact = func(s string) string { return strings.ReplaceAll(s, "secret-value", "[redacted]") }
	e := New(KindToolResult)
	e.Tool = &Tool{Name: "run_command", Output: "token=secret-value"}
	w.Handle(e)
	if strings.Contains(out.String(), "secret-value") {
		t.Fatalf("secret leaked into JSONL: %s", out.String())
	}
}
