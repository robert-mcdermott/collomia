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
// generated SBPL profile. Reads are permitted broadly; writes are confined
// to the workspace, temporary directories, and explicitly granted roots;
// network egress is denied unless the policy allows it.
type darwinBackend struct{}

func platformBackend() Backend { return darwinBackend{} }

func (darwinBackend) Name() string { return "sandbox-exec (Seatbelt)" }

func (darwinBackend) Capabilities() Capabilities {
	return Capabilities{
		WriteIsolation:   true,
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
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			abs = real
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		fmt.Fprintf(&b, "  (subpath %s)\n", sbplString(abs))
	}
	b.WriteString(")\n")
	if !policy.AllowNetwork {
		b.WriteString("(deny network*)\n")
		// Local loopback stays open so builds can talk to local daemons
		// (e.g. an Ollama server); remote egress is what we're denying.
		b.WriteString("(allow network* (remote ip \"localhost:*\"))\n")
		b.WriteString("(allow network* (local ip \"localhost:*\"))\n")
	}
	return b.String(), nil
}

func sbplString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}
