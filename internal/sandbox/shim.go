package sandbox

import (
	"fmt"
	"os"
)

// Reserved argv[1] values for the hidden process entry points used by sandbox
// backends that must re-execute the current binary before launching a command.
const (
	reexecLandlock     = "__landlock"
	reexecAppContainer = "__appcontainer"
)

// init claims the sandbox re-exec entry points before any main or test
// function observes them.
//
// The Linux and Windows backends wrap a command as
// `os.Executable() __shim <policy> -- <command…>`, and under `go test` that
// executable is the calling package's own test binary rather than collo. A
// test binary that reached its own TestMain with those arguments would treat
// them as ignored positional arguments and re-run the package's entire test
// suite inside the sandbox instead of launching the requested command. The
// recursive run outlives the test that started it and holds its temporary
// workspace open, which fails TempDir cleanup on Windows. Dispatching from
// init means every binary that links this package handles the entry point,
// with no per-package opt-in left to forget.
func init() {
	handled, err := dispatchReexec(os.Args[1:])
	if !handled {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

// dispatchReexec runs the shim named by args[0]. It returns handled=false for
// ordinary application and test arguments.
func dispatchReexec(args []string) (handled bool, err error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case reexecLandlock:
		return true, runLandlockShim(args[1:])
	case reexecAppContainer:
		return true, runAppContainerShim(args[1:])
	default:
		return false, nil
	}
}
