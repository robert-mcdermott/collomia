package sandbox

import (
	"fmt"
	"strings"
)

// Preparation is the result of applying a configured sandbox mode to a
// command. Degraded is intentionally user-facing: auto mode may continue with
// partial or no enforcement, but it must never do so silently.
type Preparation struct {
	Argv     []string
	Active   bool
	Degraded string
}

// Prepare validates a backend against the requested policy and wraps argv.
// Require mode fails closed for a missing protection. Auto mode uses every
// available protection and returns a warning describing the gap.
func Prepare(backend Backend, mode Mode, argv []string, policy Policy) (Preparation, error) {
	result := Preparation{Argv: argv}
	if mode == ModeOff {
		return result, nil
	}
	if backend == nil {
		if mode == ModeRequire {
			return result, fmt.Errorf("sandbox required but no backend was configured")
		}
		result.Degraded = "sandbox unavailable; command ran with normal user privileges"
		return result, nil
	}
	if err := backend.Available(); err != nil {
		if mode == ModeRequire {
			return result, fmt.Errorf("sandbox required but unavailable: %w", err)
		}
		result.Degraded = "sandbox unavailable; command ran with normal user privileges: " + err.Error()
		return result, nil
	}
	missing := backend.Capabilities().Missing(policy)
	if len(missing) > 0 && mode == ModeRequire {
		return result, fmt.Errorf("sandbox required but %s cannot enforce %s", backend.Name(), strings.Join(missing, " and "))
	}
	wrapped, err := backend.Wrap(argv, policy)
	if err != nil {
		if mode == ModeRequire {
			return result, fmt.Errorf("sandbox required: %w", err)
		}
		result.Degraded = "sandbox setup failed; command ran with normal user privileges: " + err.Error()
		return result, nil
	}
	result.Argv = wrapped
	result.Active = true
	if len(missing) > 0 {
		result.Degraded = backend.Name() + " provides partial enforcement; missing " + strings.Join(missing, " and ")
	}
	return result, nil
}
