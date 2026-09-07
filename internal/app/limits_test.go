package app

import "testing"

func TestRuntimeExecutionLimitOverrides(t *testing.T) {
	isolateGlobalFiles(t)
	r, err := New(t.Context(), Options{Workspace: t.TempDir(), Ephemeral: true, MaxIterations: 30, MaxTurnIterations: 500})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	noProgress, total := r.Agent.ExecutionLimits()
	if noProgress != 30 || total != 500 {
		t.Fatalf("startup override lost: %d/%d", noProgress, total)
	}
	if r.Config.Options.MaxTurnIterations != 256 {
		t.Fatal("runtime override changed persistent defaults")
	}
}
