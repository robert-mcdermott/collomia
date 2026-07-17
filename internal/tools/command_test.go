package tools

import "testing"

func TestRunCommandHardDenial(t *testing.T) {
	tool, err := NewRunCommandTool(t.TempDir(), []string{`(?i)rm\s+-rf\s+/`}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tool.Execute(t.Context(), []byte(`{"command":"rm -rf /"}`)); err == nil {
		t.Fatal("expected dangerous command to be denied")
	}
}
