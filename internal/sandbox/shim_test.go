package sandbox

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// reexecProbeEnv marks the child spawned by
// TestReexecEntryPointIsDispatchedBeforeTestsRun. It only matters when the
// dispatch is broken: without it, a child that fell through to the test runner
// would spawn a child of its own.
const reexecProbeEnv = "COLLOMIA_REEXEC_PROBE"

func TestDispatchReexecIgnoresOrdinaryArguments(t *testing.T) {
	for _, args := range [][]string{nil, {}, {"-test.v"}, {"doctor"}} {
		handled, err := dispatchReexec(args)
		if handled || err != nil {
			t.Fatalf("dispatchReexec(%q) = handled %t, err %v", args, handled, err)
		}
	}
}

func TestDispatchReexecRecognizesSandboxEntryPoints(t *testing.T) {
	for _, name := range []string{reexecLandlock, reexecAppContainer} {
		handled, err := dispatchReexec([]string{name})
		if !handled {
			t.Fatalf("dispatchReexec(%q) did not recognize the sandbox entry point", name)
		}
		if err == nil {
			t.Fatalf("dispatchReexec(%q) accepted a malformed invocation", name)
		}
	}
}

// TestReexecEntryPointIsDispatchedBeforeTestsRun guards the failure that
// motivated init-time dispatch. The Linux and Windows backends re-execute
// os.Executable(), which under `go test` is the calling package's own test
// binary. A test binary that does not claim the entry point before the test
// runner starts runs its whole suite inside the sandbox instead of the
// requested command, leaving process trees that outlive the test and hold its
// temporary workspace open.
func TestReexecEntryPointIsDispatchedBeforeTestsRun(t *testing.T) {
	if os.Getenv(reexecProbeEnv) == "1" {
		t.Skip("running as the re-exec probe child")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{reexecLandlock, reexecAppContainer} {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		// A malformed invocation is enough: the shim must reject it, and the
		// probe stays free of real sandbox setup.
		cmd := exec.CommandContext(ctx, self, entry)
		cmd.Env = append(os.Environ(), reexecProbeEnv+"=1")
		output, err := cmd.CombinedOutput()
		cancel()
		text := string(output)
		for _, runnerOutput := range []string{"PASS", "FAIL", "--- ", "no tests to run"} {
			if strings.Contains(text, runnerOutput) {
				t.Fatalf("%s re-exec ran the test suite instead of the sandbox shim: %s", entry, text)
			}
		}
		if err == nil {
			t.Fatalf("%s re-exec accepted a malformed invocation: %s", entry, text)
		}
		if !strings.Contains(text, "usage:") && !strings.Contains(text, "only used on") {
			t.Fatalf("%s re-exec did not report a shim error: %v: %s", entry, err, text)
		}
	}
}
