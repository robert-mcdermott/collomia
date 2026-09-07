package tools

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/sandbox"
)

func TestOrdinaryCommandFailureDoesNotSuggestDisablingSandbox(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixture")
	}
	tool, err := NewRunCommandTool(t.TempDir(), nil, 4096)
	if err != nil {
		t.Fatal(err)
	}
	tool.SandboxMode, tool.Backend = sandbox.ModeRequire, &recordingBackend{}
	for _, tc := range []struct {
		output string
		hint   bool
	}{
		{"Project is missing a [project] table", false},
		{"AssertionError: expected 42", false},
		{"Operation not permitted", true},
		{"Permission denied", true},
	} {
		raw, _ := json.Marshal(map[string]any{"command": "echo '" + tc.output + "'; exit 2"})
		output, err := tool.Execute(t.Context(), raw)
		if !CommandExitedNormally(err) || !strings.Contains(output, tc.output) {
			t.Fatalf("lost command failure: %q %v", output, err)
		}
		if strings.Contains(output, "command ran inside the OS sandbox") != tc.hint {
			t.Fatalf("irrelevant/missing sandbox guidance: %s", output)
		}
	}
}
