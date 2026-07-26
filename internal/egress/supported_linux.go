//go:build linux

package egress

// Supported reports Linux as unable to enforce scoped egress, and the reason
// is a limit of the mechanism rather than missing work.
//
// Landlock filters TCP connect by destination *port* and never by address, so
// the only way to let a sandboxed command reach a loopback broker is to allow
// that port outright — which also allows every remote host on the same port.
// The threat this feature exists to address is a misled agent sending data to
// an endpoint an attacker controls, and that attacker chooses which port to
// listen on. An allowlist defeated by the adversary it targets would be
// enforcement in name only, so Linux keeps the all-or-nothing
// sandbox_allow_network control, which it does enforce.
func Supported() (bool, string) {
	return false, "Landlock filters TCP by port and never by address, so a loopback broker allowlist would be bypassable by any destination reachable on the broker's port; Linux keeps all-or-nothing sandbox_allow_network"
}
