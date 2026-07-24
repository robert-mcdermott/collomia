//go:build windows

package eval

import (
	"runtime"
	"testing"
)

// evaluationSandboxReadableRoots grants the offline fixture access to the Go
// SDK used by its scripted `go test` verification. GitHub's Windows runner
// installs Go outside the user profile and normal application-package roots,
// so AppContainer requires this explicit read/execute-only grant.
func evaluationSandboxReadableRoots() []string {
	if root := runtime.GOROOT(); root != "" {
		return []string{root}
	}
	return nil
}

func TestEvaluationSandboxReadableRootsIncludeGoSDK(t *testing.T) {
	roots := evaluationSandboxReadableRoots()
	if len(roots) != 1 || roots[0] != runtime.GOROOT() {
		t.Fatalf("evaluation sandbox roots=%v, GOROOT=%q", roots, runtime.GOROOT())
	}
}
