//go:build darwin

package egress

// Supported reports macOS as the one platform where the broker is enforcement
// rather than cooperation. Seatbelt can deny remote egress while keeping
// loopback reachable — `(deny network*)` paired with narrowly scoped
// `localhost` allowances — which is exactly the shape this design needs: the
// child cannot open a remote socket itself, and the only listener it can reach
// is the broker.
func Supported() (bool, string) { return true, "" }
