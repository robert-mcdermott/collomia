package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCommandVerificationScopeUsesFileGuard(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	command, _ := NewRunCommandTool(dir, nil, 1024)
	raw, _ := json.Marshal(map[string]any{"command": "check", "verification": map[string]any{"paths": []string{outside}, "purpose": "Check outside"}})
	if _, err := command.Assess(raw); err == nil {
		t.Fatal("scope bypassed file boundary")
	}
	command.Guard, _ = NewPathGuard(dir, true)
	action, err := command.Assess(raw)
	if err != nil || !action.Outside || len(action.Paths) != 1 {
		t.Fatalf("outside scope omitted from permissions: %+v %v", action, err)
	}
	for _, raw := range []string{
		`{"command":"check","verification":{"paths":[],"purpose":"check"}}`,
		`{"command":"check","verification":{"paths":["file"],"purpose":""}}`,
		`{"command":"check","verification":{"paths":["file"],"purpose":"check","required_text":["not-a-supported-check"]}}`,
		`{"command":"check","verification":{"paths":[""] ,"purpose":"check"}}`,
	} {
		if _, err := command.Assess(json.RawMessage(raw)); err == nil {
			t.Fatalf("bad verification accepted: %s", raw)
		}
	}
}
