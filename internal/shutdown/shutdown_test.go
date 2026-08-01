package shutdown

import (
	"os"
	"runtime"
	"testing"
)

// The signal this package exists for. A regression here is not a cosmetic
// one: without SIGHUP the process is terminated by the runtime's default
// disposition, so no deferred teardown runs at all.
func TestHangupIsAShutdownSignalOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no SIGHUP; console close arrives as SIGTERM, covered below")
	}
	if !containsNamed(Signals(), "hangup") {
		t.Fatalf("SIGHUP is not in the shutdown set %v; closing a terminal will kill Collomia without running teardown", Signals())
	}
}

// SIGTERM is what a service manager, a container runtime, an ordinary kill,
// and — on Windows — a closed console window all send.
func TestTerminateAndInterruptAreAlwaysShutdownSignals(t *testing.T) {
	if !containsNamed(Signals(), "terminated") {
		t.Errorf("SIGTERM is not in the shutdown set %v", Signals())
	}
	found := false
	for _, signal := range Signals() {
		if signal == os.Interrupt {
			found = true
		}
	}
	if !found {
		t.Errorf("os.Interrupt is not in the shutdown set %v", Signals())
	}
}

// Duplicates would be harmless but would mean two sources are contributing the
// same signal, which is how the three copies this package replaced drifted.
func TestSignalSetHasNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, signal := range Signals() {
		if seen[signal.String()] {
			t.Errorf("signal %s appears twice in %v", signal, Signals())
		}
		seen[signal.String()] = true
	}
}

// Signals returns a fresh slice each call: the platform list is package state,
// and a caller that appended to a shared backing array would quietly extend
// what every later caller registers.
func TestSignalsDoesNotShareBackingArray(t *testing.T) {
	first := Signals()
	first = append(first, os.Kill)
	_ = first
	for _, signal := range Signals() {
		if signal == os.Kill {
			t.Fatal("appending to one Signals() result modified the next")
		}
	}
}

func containsNamed(signals []os.Signal, name string) bool {
	for _, signal := range signals {
		if signal.String() == name {
			return true
		}
	}
	return false
}
