//go:build !windows

package shutdown

import (
	"os"
	"syscall"
)

// SIGHUP is the signal a terminal sends when it disappears — the window is
// closed, the ssh connection drops, the laptop's lid comes down. It is the
// most likely way an interactive session ends abnormally, and it was the one
// signal Collomia did not handle.
//
// SIGTERM is here as well because it is what an orderly `kill`, a service
// manager, and a container runtime send.
var platformSignals = []os.Signal{syscall.SIGHUP, syscall.SIGTERM}
