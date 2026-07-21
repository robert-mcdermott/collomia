//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// darwinBackend enforces policies with the system sandbox-exec utility and a
// generated SBPL profile. Writes are confined to the workspace, temporary
// directories, and explicitly granted roots. Reads remain broad by default,
// or can be confined to the workspace, system runtime paths, PATH entries,
// and explicit grants. Network egress is denied unless the policy allows it.
type darwinBackend struct{}

func platformBackend() Backend { return darwinBackend{} }

func (darwinBackend) Name() string { return "sandbox-exec (Seatbelt)" }

func (darwinBackend) Capabilities() Capabilities {
	return Capabilities{
		WriteIsolation:   true,
		ReadIsolation:    true,
		NetworkIsolation: NetworkFull,
		Notes:            []string{"localhost remains available when remote network is denied", "process-group termination is best effort"},
	}
}

func (darwinBackend) Available() error {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		return fmt.Errorf("sandbox-exec not found: %w", err)
	}
	return nil
}

func (b darwinBackend) Wrap(argv []string, policy Policy) ([]string, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	if err := b.Available(); err != nil {
		return nil, err
	}
	profile, err := Profile(policy)
	if err != nil {
		return nil, err
	}
	wrapped := append([]string{"sandbox-exec", "-p", profile}, argv...)
	return wrapped, nil
}

// Profile renders the SBPL profile for a policy. Exported for tests and for
// `collo doctor --debug` inspection.
func Profile(policy Policy) (string, error) {
	if policy.WorkspaceRoot == "" {
		return "", fmt.Errorf("sandbox policy requires a workspace root")
	}
	writable := []string{policy.WorkspaceRoot, os.TempDir(), "/private/tmp", "/tmp", "/dev"}
	writable = append(writable, policy.ExtraWritableRoots...)
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")
	b.WriteString("(deny file-write*)\n")
	b.WriteString("(allow file-write*\n")
	seen := map[string]bool{}
	for _, root := range writable {
		abs, ok := normalizedRoot(root)
		if !ok {
			continue
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		fmt.Fprintf(&b, "  %s\n", sbplPathFilter(abs))
	}
	b.WriteString(")\n")
	if policy.ConstrainReads {
		// Deny content reads from the user's data roots rather than using a
		// global file-read-data denial. Modern macOS processes consult runtime
		// data outside a stable, documented set of system paths during exec and
		// abort if those reads are globally hidden. User homes and mounted
		// volumes are the sensitive boundary; workspace/PATH/explicit grants
		// below reopen only the requested subpaths. Metadata remains visible so
		// ordinary path lookup can fail cleanly without leaking file contents.
		b.WriteString("(deny file-read-data\n")
		deniedRoots := []string{"/Users", "/Volumes"}
		if home, err := os.UserHomeDir(); err == nil {
			deniedRoots = append(deniedRoots, home)
		}
		seenDenied := map[string]bool{}
		for _, root := range deniedRoots {
			abs, ok := normalizedRoot(root)
			if !ok || seenDenied[abs] {
				continue
			}
			seenDenied[abs] = true
			fmt.Fprintf(&b, "  %s\n", sbplPathFilter(abs))
		}
		b.WriteString(")\n")
		b.WriteString("(allow file-read-data\n")
		seen = map[string]bool{}
		readable := append([]string{}, darwinSystemReadableRoots()...)
		readable = append(readable, filepath.SplitList(os.Getenv("PATH"))...)
		readable = append(readable, writable...)
		readable = append(readable, policy.ExtraReadableRoots...)
		for _, root := range readable {
			abs, ok := normalizedRoot(root)
			if !ok || seen[abs] {
				continue
			}
			seen[abs] = true
			fmt.Fprintf(&b, "  %s\n", sbplPathFilter(abs))
		}
		b.WriteString(")\n")
	}
	if !policy.AllowNetwork {
		b.WriteString("(deny network*)\n")
		// Local loopback stays open so builds can talk to local daemons
		// (e.g. an Ollama server); remote egress is what we're denying.
		b.WriteString("(allow network* (remote ip \"localhost:*\"))\n")
		b.WriteString("(allow network* (local ip \"localhost:*\"))\n")
	}
	return b.String(), nil
}

func darwinSystemReadableRoots() []string {
	// These roots contain operating-system runtimes and conventional shared
	// tool installations, not the user's home directory. PATH entries are
	// added separately so user-installed executables can still launch without
	// granting the rest of the home directory.
	return []string{
		"/System", "/usr", "/bin", "/sbin", "/Library", "/Applications",
		"/opt/homebrew", "/opt/local", "/nix", "/private/etc",
		"/private/var/db", "/private/var/select", "/dev",
	}
}

func normalizedRoot(root string) (string, bool) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", false
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	return filepath.Clean(abs), true
}

func sbplPathFilter(path string) string {
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return "(literal " + sbplString(path) + ")"
	}
	return "(subpath " + sbplString(path) + ")"
}

func sbplString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}
