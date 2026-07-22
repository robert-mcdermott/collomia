package main

import (
	"strings"
	"testing"
)

func TestCompletionScriptsCoverSupportedShells(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell", "pwsh"} {
		script, err := completionScript(shell)
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		for _, want := range []string{"collo", "completion", "schema", "ephemeral", "replay", "check", "support", "include-logs", "rewind"} {
			if !strings.Contains(script, want) {
				t.Errorf("%s completion missing %q", shell, want)
			}
		}
	}
	if _, err := completionScript("csh"); err == nil {
		t.Fatal("unsupported shell should fail")
	}
}
