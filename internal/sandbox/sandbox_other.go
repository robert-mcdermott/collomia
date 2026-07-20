//go:build !darwin && !linux && !windows

package sandbox

import "runtime"

// A Windows backend (AppContainer/job objects) is planned; until it lands
// Windows runs degraded or fails closed depending on the configured mode,
// and `collo doctor` reports it.
func platformBackend() Backend {
	return unavailable{reason: "no sandbox backend is implemented for " + runtime.GOOS + " yet; commands run with normal user privileges"}
}
