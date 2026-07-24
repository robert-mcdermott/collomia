//go:build windows && race

package eval

import "testing"

// A race-instrumented Go test binary is not a production Collomia executable.
// Re-executing that instrumented binary as both the AppContainer broker and a
// nested Go toolchain supervisor can stall Windows process shutdown. The
// ordinary Windows suite still runs these evaluations with AppContainer, and
// internal/sandbox retains its native descendant regression under -race.
// Bypassing only this redundant composition lets the race job continue to
// instrument the agent, registry, permission, and command lifecycle.
func evaluationSandboxMode() string {
	return "off"
}

func TestEvaluationAvoidsNestedAppContainerBrokerInWindowsRaceBuild(t *testing.T) {
	if mode := evaluationSandboxMode(); mode != "off" {
		t.Fatalf("evaluation sandbox mode=%q, want off", mode)
	}
}
