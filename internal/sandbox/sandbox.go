// Package sandbox provides OS-level containment for agent-executed commands.
//
// The permission engine decides whether a command may run; this package
// decides what the command can touch once it runs. Backends are
// platform-specific. When no backend is available the caller must decide —
// via configuration — whether to continue degraded (approval checks only)
// or fail closed.
package sandbox

import (
	"fmt"
	"strings"
)

// Policy describes what a sandboxed command may do. The default is
// compatibility-oriented: reads and network are allowed unless the caller
// explicitly requests confinement, while writes are limited to the workspace,
// temporary directories, and explicit grants.
type Policy struct {
	WorkspaceRoot string
	// ExtraReadableRoots lists additional files or directories the command may
	// read when ConstrainReads is true. Writable roots are also
	// readable.
	ExtraReadableRoots []string
	// ExtraWritableRoots lists additional directories the command may write.
	ExtraWritableRoots []string
	AllowNetwork       bool
	// ConstrainReads asks capable backends to limit user-data reads to the
	// workspace and explicit grants while retaining the system runtime paths
	// needed to launch tools. Its zero value preserves broad reads.
	ConstrainReads bool
}

// NetworkIsolation describes how completely a backend can deny outbound
// network traffic. Landlock controls TCP from ABI v4 and UDP from ABI v10;
// Seatbelt and AppContainer can deny both TCP and UDP.
type NetworkIsolation string

const (
	NetworkNone NetworkIsolation = "none"
	NetworkTCP  NetworkIsolation = "tcp"
	NetworkFull NetworkIsolation = "full"
)

// Capabilities reports the protections a backend can actually enforce on
// the current machine. Keeping this separate from availability prevents a
// partially capable backend from being reported as a complete sandbox.
type Capabilities struct {
	WriteIsolation bool
	ReadIsolation  bool
	// ReadIsolationAlways means the backend inherently confines user-data
	// reads even when the compatibility policy does not request it. Windows
	// AppContainer has this property; macOS and Linux make it opt-in.
	ReadIsolationAlways bool
	NetworkIsolation    NetworkIsolation
	ProcessIsolation    bool
	Notes               []string
}

// Summary is a compact, user-facing capability report used by doctor and the
// generated capability matrix.
func (c Capabilities) Summary() string {
	var parts []string
	if c.WriteIsolation {
		parts = append(parts, "workspace write confinement")
	}
	if c.ReadIsolationAlways {
		parts = append(parts, "user-data read confinement always on")
	} else if c.ReadIsolation {
		parts = append(parts, "user-data read confinement available")
	} else {
		parts = append(parts, "broad reads")
	}
	switch c.NetworkIsolation {
	case NetworkFull:
		parts = append(parts, "full network denial")
	case NetworkTCP:
		parts = append(parts, "TCP denial only")
	default:
		parts = append(parts, "no network denial")
	}
	if c.ProcessIsolation {
		parts = append(parts, "process isolation")
	}
	parts = append(parts, c.Notes...)
	return strings.Join(parts, "; ")
}

// ReadPolicySummary reports the effective user-data read stance rather than
// only echoing the requested switch. Some backends enforce a stricter boundary
// than the compatibility policy asks for.
func (c Capabilities) ReadPolicySummary(policy Policy) string {
	if c.ReadIsolationAlways {
		return "confined (backend-enforced)"
	}
	if policy.ConstrainReads {
		if c.ReadIsolation {
			return "workspace-scoped"
		}
		return "requested but unavailable"
	}
	return "broad"
}

// Missing returns the requested protections that this backend cannot fully
// enforce. Write confinement is fundamental to every current sandbox policy;
// network confinement is required only when AllowNetwork is false.
func (c Capabilities) Missing(policy Policy) []string {
	var missing []string
	if !c.WriteIsolation {
		missing = append(missing, "filesystem write confinement")
	}
	if policy.ConstrainReads && !c.ReadIsolation {
		missing = append(missing, "user-data read confinement")
	}
	if !policy.AllowNetwork && c.NetworkIsolation != NetworkFull {
		switch c.NetworkIsolation {
		case NetworkTCP:
			missing = append(missing, "UDP network denial")
		default:
			missing = append(missing, "network denial")
		}
	}
	return missing
}

// Backend wraps a command argv so the OS enforces Policy.
type Backend interface {
	Name() string
	// Capabilities reports the protections available on this machine.
	Capabilities() Capabilities
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

func (u unavailable) Name() string               { return "none" }
func (u unavailable) Capabilities() Capabilities { return Capabilities{} }
func (u unavailable) Available() error           { return fmt.Errorf("%s", u.reason) }
func (u unavailable) Wrap([]string, Policy) ([]string, error) {
	return nil, fmt.Errorf("no sandbox backend: %s", u.reason)
}
