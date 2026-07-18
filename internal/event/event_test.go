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
