package eval

import (
	"fmt"
	"os"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/sandbox"
)

// TestMain handles sandbox re-exec entry points before the Go test runner sees
// them. On Linux and Windows the sandbox wrapper re-executes the current
// executable, which is this test binary during evaluations.
func TestMain(m *testing.M) {
	if handled, err := sandbox.DispatchReexec(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
