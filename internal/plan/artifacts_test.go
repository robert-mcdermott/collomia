package plan

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestArtifactBriefRoundTripAndIsolation(t *testing.T) {
	board := NewBoard()
	tool := Tool(board)
	raw := json.RawMessage(`{"goal":"report","steps":[{"id":1,"title":"write","status":"in_progress"}],"artifacts":[{"path":"report.md","role":"deliverable"},{"path":"helper.sh","role":"scratch"}]}`)
	output, err := tool.Execute(t.Context(), raw)
	if err != nil || !strings.Contains(output, "Artifact (scratch): helper.sh") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	snapshot := board.Current()
	snapshot.Artifacts[0].Role = "scratch"
	if board.Current().Artifacts[0].Role != "deliverable" {
		t.Fatal("snapshot aliases the board")
	}
	encoded, err := json.Marshal(board.Current())
	if err != nil {
		t.Fatal(err)
	}
	var restored Plan
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	other := NewBoard()
	other.Restore(restored)
	if other.Current().Artifacts[0].Role != "deliverable" {
		t.Fatal("persistence lost artifacts")
	}
	if !strings.Contains(string(tool.Definition().InputSchema), `"artifacts"`) {
		t.Fatal("tool schema missing artifacts")
	}
}

func TestArtifactBriefValidationAndLegacy(t *testing.T) {
	base := Plan{Goal: "report", Steps: []Step{{ID: 1, Title: "write", Status: "in_progress"}}}
	if err := Validate(base); err != nil {
		t.Fatalf("legacy plan rejected: %v", err)
	}
	for _, artifacts := range [][]Artifact{
		{{Path: "", Role: "deliverable"}}, {{Path: "x", Role: "unknown"}},
		{{Path: "x", Role: "scratch"}, {Path: "./x", Role: "deliverable"}},
		{{Path: ".", Role: "scratch"}}, make([]Artifact, 65),
	} {
		base.Artifacts = artifacts
		if Validate(base) == nil {
			t.Fatalf("invalid artifacts accepted: %+v", artifacts)
		}
	}
}
