// Package shutdown defines the signals that mean "stop now, but stop
// properly", in one place.
//
// It exists because the set was written out three times — the TUI's entry
// point, the MCP command, and the browser terminal — and all three said
// os.Interrupt and SIGTERM. That omitted the signal a terminal actually sends
// when it goes away, which is the failure this package was extracted to fix.
//
// The cost of the omission was not a missing message. Go's default disposition
// for SIGHUP terminates the process immediately, so `defer runtime.Close()`
// never ran: background processes were left behind because ProcessManager gives
// each one its own process group (Setpgid) and a hangup reaches only the
// foreground group; the durable session was never closed; the log was never
// flushed. Closing a laptop lid on an ssh session, or closing a terminal
// window, orphaned every background process the agent had started.
package shutdown

import (
	"context"
	"os"
	"os/signal"
)

// Signals returns the signals that should begin an orderly shutdown, most
// specific first. Every entry means the same thing to Collomia — the session is
// over — and none of them means "reload configuration": this is an interactive
// terminal program, not a daemon, and SIGHUP here is a terminal that has gone
// away rather than an administrator asking for anything.
func Signals() []os.Signal { return append([]os.Signal{os.Interrupt}, platformSignals...) }

// NotifyContext is signal.NotifyContext over Signals(). Callers use it rather
// than naming signals themselves so a fourth entry point cannot reintroduce the
// gap this package closed.
func NotifyContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, Signals()...)
}
