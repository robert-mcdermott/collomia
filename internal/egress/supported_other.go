//go:build !darwin && !linux && !windows

package egress

// Supported reports platforms with no sandbox backend at all. Without a
// backend that denies direct remote egress, a broker would be a cooperative
// convention that any non-proxy-aware program ignores, so it is never
// presented as enforcement here.
func Supported() (bool, string) {
	return false, "no sandbox backend on this platform can deny direct remote egress"
}
