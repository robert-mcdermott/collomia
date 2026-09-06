package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceArtifactTextDetection(t *testing.T) {
	dir := t.TempDir()
	guard, err := NewPathGuard(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	tool := ValidateArtifactTool{Guard: guard}
	for _, ext := range []string{"html", "htm", "css", "js", "mjs", "ts", "py", "go", "sh", "yaml", "svg"} {
		name := "sample." + ext
		if err := os.WriteFile(filepath.Join(dir, name), []byte("MARKER deliberately not parsed"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, format := range []string{"auto", "text", "html"} {
			raw, _ := json.Marshal(map[string]any{"path": name, "format": format, "required_text": []string{"MARKER"}})
			result, err := tool.Execute(t.Context(), raw)
			if err != nil || !strings.Contains(result, "format: text") || !strings.Contains(result, "syntax and functionality were not tested") {
				t.Fatalf("%s/%s: %s %v", name, format, result, err)
			}
		}
	}
	if got := artifactFormat("auto", "unknown.blob"); got != "binary" {
		t.Fatalf("unknown extension guessed as %s", got)
	}
}

func TestArtifactValidationRequirementCoverage(t *testing.T) {
	guard, _ := NewPathGuard(t.TempDir(), false)
	tool := ValidateArtifactTool{Guard: guard}
	prior := tool.ValidationRequirement(json.RawMessage(`{"path":"./game.html","format":"html5","min_bytes":10,"required_text":["canvas","PRIVATE_SENTINEL"]}`))
	if prior == nil {
		t.Fatal("unsupported format lost substantive requirements")
	}
	for _, tc := range []struct {
		name, args string
		want       bool
	}{
		{"equivalent", `{"path":"game.html","format":"text","min_bytes":10,"required_text":["PRIVATE_SENTINEL","canvas"]}`, true},
		{"stronger", `{"path":"game.html","format":"auto","min_bytes":20,"required_text":["canvas","PRIVATE_SENTINEL","extra"]}`, true},
		{"drops text", `{"path":"game.html","format":"text","min_bytes":10,"required_text":["canvas"]}`, false},
		{"lowers minimum", `{"path":"game.html","format":"text","min_bytes":9,"required_text":["canvas","PRIVATE_SENTINEL"]}`, false},
		{"different file", `{"path":"other.html","format":"text","min_bytes":10,"required_text":["canvas","PRIVATE_SENTINEL"]}`, false},
		{"binary downgrade", `{"path":"game.html","format":"binary","min_bytes":10,"required_text":["canvas","PRIVATE_SENTINEL"]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tool.ValidationRequirement(json.RawMessage(tc.args)).Covers(prior); got != tc.want {
				t.Fatalf("covers=%v", got)
			}
		})
	}
	encoded, _ := json.Marshal(prior)
	if strings.Contains(string(encoded), "PRIVATE_SENTINEL") || strings.Contains(string(encoded), "game.html") {
		t.Fatal("retained raw requirement content")
	}
	for _, format := range []string{"json", "json_typo"} {
		prior := tool.ValidationRequirement(json.RawMessage(`{"path":"data.json","format":"` + format + `"}`))
		text := tool.ValidationRequirement(json.RawMessage(`{"path":"data.json","format":"text"}`))
		if text.Covers(prior) {
			t.Fatal("text replaced a JSON parsing obligation")
		}
	}
	for _, raw := range []string{`{"path":"x","required_text":[""]}`, `{"path":"x","min_bytes":-1}`, `{"path":"x","unknown_check":true}`, `{"path":"x"} {}`} {
		if tool.ValidationRequirement(json.RawMessage(raw)) != nil {
			t.Fatalf("invalid request relaxed: %s", raw)
		}
	}
}
