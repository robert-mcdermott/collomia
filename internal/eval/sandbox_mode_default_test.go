//go:build !windows || !race

package eval

import "testing"

// evaluationSandboxMode keeps the production default for normal tests on
// every platform, including the native Windows AppContainer regression.
func evaluationSandboxMode() string {
	return "auto"
}

func TestEvaluationUsesProductionSandboxOutsideWindowsRaceBuilds(t *testing.T) {
	if mode := evaluationSandboxMode(); mode != "auto" {
		t.Fatalf("evaluation sandbox mode=%q, want auto", mode)
	}
}
