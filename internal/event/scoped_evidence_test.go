package event

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestScopedEvidenceRoundTripAndPublishedSchema(t *testing.T) {
	e := New(KindToolResult)
	e.Tool = &Tool{Name: "run_command", Evidence: &Evidence{Kind: "scoped_verification", Subject: "node check.mjs", Detail: "Check game", Files: map[string]string{"game.html": "sha256:" + strings.Repeat("a", 64)}, Checks: map[string]string{"execution": "passed", "coverage": "not_assessed"}}}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var got Event
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Tool.Evidence.Files["game.html"] != e.Tool.Evidence.Files["game.html"] {
		t.Fatal("file evidence lost")
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(JSONSchema(), &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(doc); err != nil {
		t.Fatal(err)
	}
}
