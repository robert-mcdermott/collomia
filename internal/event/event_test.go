package event

import (
	"encoding/json"
	"slices"
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
	e.FailureID = "err-0123456789abcdef"
	e.Result = &RunResult{
		Status: "cancelled", Error: "context canceled", Failure: &Failure{ID: e.FailureID, Kind: FailureCancelled}, Partial: true,
		Ephemeral: true, Refused: true, SessionID: "abc123", ChangedFiles: []string{"main.go"}, DurationMS: 1500,
	}
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
		decoded.FailureID != e.FailureID || decoded.Result.Failure == nil || decoded.Result.Failure.ID != e.FailureID || decoded.Result.Failure.Kind != FailureCancelled || !decoded.Result.Partial || !decoded.Result.Ephemeral || !decoded.Result.Refused ||
		len(decoded.Result.ChangedFiles) != 1 || decoded.Usage == nil || decoded.Usage.InputTokens != 10 {
		t.Fatalf("round trip mismatch: %+v", decoded)
	}
}

func TestEmbeddedJSONSchemaPublishesEveryEventKind(t *testing.T) {
	data := JSONSchema()
	if !json.Valid(data) {
		t.Fatal("embedded event schema is not valid JSON")
	}
	var schema struct {
		Schema     string `json:"$schema"`
		Properties struct {
			Schema struct {
				Const int `json:"const"`
			} `json:"schema"`
			Kind struct {
				Enum []string `json:"enum"`
			} `json:"kind"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Schema != "https://json-schema.org/draft/2020-12/schema" || schema.Properties.Schema.Const != SchemaVersion {
		t.Fatalf("schema metadata=%+v", schema)
	}
	wantKinds := []Kind{
		KindSessionStart, KindTurnStart, KindTextDelta, KindReasoningDelta, KindToolCallDelta,
		KindToolStart, KindToolOutput, KindToolResult, KindPermissionRequest, KindPermissionDecision,
		KindFileChange, KindPlanUpdate, KindDelegateUpdate, KindUsage, KindCompaction, KindWarning, KindError, KindTurnEnd, KindRunResult,
	}
	for _, kind := range wantKinds {
		if !slices.Contains(schema.Properties.Kind.Enum, string(kind)) {
			t.Errorf("published schema is missing event kind %q", kind)
		}
	}
	if len(schema.Properties.Kind.Enum) != len(wantKinds) {
		t.Fatalf("published kinds=%v; want exactly %v", schema.Properties.Kind.Enum, wantKinds)
	}
}

func TestJSONSchemaReturnsDefensiveCopy(t *testing.T) {
	first := JSONSchema()
	first[0] = 'x'
	if second := JSONSchema(); len(second) == 0 || second[0] != '{' {
		t.Fatal("caller mutated the embedded schema")
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

func TestToolCallDeltaRoundTrips(t *testing.T) {
	e := New(KindToolCallDelta)
	e.ToolCall = &ToolCallDelta{Index: 2, ID: "call-2", Name: "read_file", ArgumentsDelta: `{"path":`, Done: false}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Event
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ToolCall == nil || decoded.ToolCall.Index != 2 || decoded.ToolCall.ID != "call-2" || decoded.ToolCall.Name != "read_file" || decoded.ToolCall.ArgumentsDelta != `{"path":` || decoded.ToolCall.Done {
		t.Fatalf("tool call delta round trip mismatch: %+v", decoded.ToolCall)
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
