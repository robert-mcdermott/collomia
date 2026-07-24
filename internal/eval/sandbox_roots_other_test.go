//go:build !windows

package eval

import "testing"

func evaluationSandboxReadableRoots() []string {
	return nil
}

func TestEvaluationSandboxReadableRootsRemainWindowsOnly(t *testing.T) {
	if roots := evaluationSandboxReadableRoots(); len(roots) != 0 {
		t.Fatalf("non-Windows evaluation sandbox roots=%v", roots)
	}
}
