// Package sandbox provides OS-level containment for agent-executed commands.
//
// The permission engine decides whether a command may run; this package
// decides what the command can touch once it runs. Backends are
// platform-specific. When no backend is available the caller must decide —
// via configuration — whether to continue degraded (approval checks only)
// or fail closed.
package sandbox

import "fmt"

// Policy describes what a sandboxed command may do. The default is
// read-mostly: reads are allowed, writes are limited to the workspace and
// temporary directories, and network egress is denied unless granted.
type Policy struct {
	WorkspaceRoot string
	// ExtraWritableRoots lists additional directories the command may write.
	ExtraWritableRoots []string
	AllowNetwork       bool
}

// Backend wraps a command argv so the OS enforces Policy.
type Backend interface {
	Name() string
	// Available reports nil when the backend can enforce policies on this
	// machine, or an explanatory error otherwise.
	Available() error
	// Wrap returns a replacement argv that runs argv under the policy.
	Wrap(argv []string, policy Policy) ([]string, error)
}

// Mode is the configured enforcement stance.
type Mode string

const (
	// ModeOff disables OS sandboxing; approval checks are the only control.
	ModeOff Mode = "off"
	// ModeAuto uses the platform backend when available and continues
	// degraded (with a visible warning) when it is not.
	ModeAuto Mode = "auto"
	// ModeRequire refuses to run commands when no backend is available.
	ModeRequire Mode = "require"
)

// ForPlatform returns this platform's backend. It always returns a backend;
// call Available to learn whether it can actually enforce anything.
func ForPlatform() Backend { return platformBackend() }

type unavailable struct{ reason string }

func (u unavailable) Name() string     { return "none" }
func (u unavailable) Available() error { return fmt.Errorf("%s", u.reason) }
func (u unavailable) Wrap([]string, Policy) ([]string, error) {
	return nil, fmt.Errorf("no sandbox backend: %s", u.reason)
}
