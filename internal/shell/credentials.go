package shell

import (
	"github.com/robert-mcdermott/collomia/internal/secrets"
)

// classifyCredentialTargets records every argument that names a well-known
// credential store, so the permission layer can decide what an action reaching
// one is allowed to do.
//
// This runs for every command rather than for a table of "reading" commands.
// The set of programs that can read a file is effectively every program, and a
// table would turn each omission into a silent bypass — `awk`, `xxd`, `busybox
// cat`, or a build script all read a file just as well as `cat` does.
//
// The analysis stays textual, so it describes what the command names, never
// what it will open. A command that hides its target behind a variable or a
// substitution is already reported as uninspectable, which forces approval on
// its own.
func classifyCredentialTargets(inv invocation, cwd string, a *Analysis) {
	for _, arg := range inv.args {
		if label := secrets.ClassifyArgument(arg, cwd); label != "" {
			a.credential(label + ": " + arg)
		}
	}
}
