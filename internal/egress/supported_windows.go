//go:build windows

package egress

// Supported reports Windows as unable to host this design at all.
//
// AppContainer blocks loopback to unpackaged local services regardless of
// which network capability SIDs are granted, so a sandboxed command cannot
// reach the broker under any setting — this is not weaker enforcement but no
// route at all. The documented escape is a CheckNetIsolation loopback
// exemption, which requires administrator rights and leaves persistent machine
// state, and Collomia's Windows backend is deliberately built from inbox APIs
// with no administrator step. Windows keeps the all-or-nothing
// sandbox_allow_network control, which AppContainer enforces more completely
// than either Unix backend.
func Supported() (bool, string) {
	return false, "AppContainer blocks loopback to unpackaged local services, so a sandboxed command cannot reach the broker at all; Windows keeps all-or-nothing sandbox_allow_network"
}
