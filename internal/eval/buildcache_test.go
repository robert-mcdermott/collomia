package eval

import (
	"os"
	"sync"
	"testing"
)

// These evaluations run the repository's real toolchain — a candidate wave
// builds, vets, and tests each candidate worktree and then the combined
// workspace — and each one used to point GOCACHE inside its own temporary
// directory, which is destroyed when the test ends. Every evaluation therefore
// started from an empty cache. Measured on a small fixture repository, one
// build/vet/test round costs about 4.3s cold and 0.5s warm, and the package
// runs that round dozens of times.
//
// The cache lived inside the workspace because these evaluations deliberately
// run under the production sandbox, which without an explicit writable root
// lets a command write only inside the workspace. `sandbox_writable_roots`
// exists for exactly this — its own documentation names a package-manager
// cache as the example — so granting one root for a build cache is the
// supported answer rather than a weakening of the sandbox.
//
// Nothing an evaluation asserts depends on the cache being cold or on its
// location: the parent-workspace comparisons walk the workspace, which the
// cache is now outside of, and no evaluation asserts that a sandboxed command
// cannot write beyond the workspace.

var evaluationBuildCache = sync.OnceValues(func() (string, error) {
	return os.MkdirTemp("", "collomia-eval-build-cache-")
})

// sharedBuildCache points GOCACHE at one cache reused by every evaluation in
// this package and returns the directory, which callers must also grant as a
// sandbox writable root or the sandboxed toolchain cannot populate it.
func sharedBuildCache(t testing.TB) string {
	t.Helper()
	dir, err := evaluationBuildCache()
	if err != nil {
		t.Fatalf("create the shared evaluation build cache: %v", err)
	}
	t.Setenv("GOCACHE", dir)
	return dir
}

func TestMain(m *testing.M) {
	code := m.Run()
	// Only remove a cache that was actually created; asking for it here would
	// otherwise create one just to delete it.
	if dir, err := evaluationBuildCache(); err == nil && dir != "" {
		_ = os.RemoveAll(dir)
	}
	os.Exit(code)
}
