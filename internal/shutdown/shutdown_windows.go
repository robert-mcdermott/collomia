package shutdown

import (
	"os"
	"syscall"
)

// Windows has no SIGHUP, and adding one would be theatre: the platform reports
// terminal loss through a different mechanism that already lands in this set.
//
// Closing a console window raises CTRL_CLOSE_EVENT, and logoff and shutdown
// raise their own events; the Go runtime maps all three to SIGTERM, which is
// registered here. So the case this package exists for — the terminal going
// away — is delivered on Windows without a platform-specific signal, and
// listing syscall.SIGHUP would only add a constant nothing ever raises.
//
// One real difference is worth knowing rather than discovering: the console
// close handler is given a few seconds before the process is terminated
// regardless of what it is doing. Shutdown work on this platform is therefore
// bounded by the operating system, not by Collomia, which is why teardown stops
// background processes first rather than flushing anything lengthy.
var platformSignals = []os.Signal{syscall.SIGTERM}
