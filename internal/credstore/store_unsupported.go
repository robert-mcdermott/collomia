//go:build !darwin && !windows

package credstore

// backendName is empty on platforms with no credential manager Collomia can
// use without adding a dependency or a weaker file-backed store.
//
// Linux is the platform this affects. Its Secret Service API requires a D-Bus
// session with gnome-keyring or kwallet running, which is normal on a desktop
// and absent on exactly the headless servers and cluster nodes where an agent
// is most often run. Rather than ship a store that works on some Linux hosts
// and silently degrades on others, credentials there come from the
// environment — which is what a headless host wants regardless, and which
// remains fully supported on every platform.
const backendName = ""

func backendGet(string) (string, bool, error) { return "", false, ErrUnsupported }
func backendSet(string, string) error         { return ErrUnsupported }
func backendDelete(string) (bool, error)      { return false, ErrUnsupported }
