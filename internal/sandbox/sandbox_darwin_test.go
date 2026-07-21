//go:build darwin

package sandbox

import (
	"strings"
	"testing"
)

func TestDarwinProfileReadConfinementIsExplicit(t *testing.T) {
	workspace := t.TempDir()
	broad, err := Profile(Policy{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(broad, "(deny file-read-data") {
		t.Fatalf("broad-read policy unexpectedly denied reads:\n%s", broad)
	}

	confined, err := Profile(Policy{WorkspaceRoot: workspace, ConstrainReads: true, ExtraReadableRoots: []string{"/read-only-sdk"}})
	if err != nil {
		t.Fatal(err)
	}
	canonicalWorkspace, ok := normalizedRoot(workspace)
	if !ok {
		t.Fatal("could not normalize workspace")
	}
	for _, want := range []string{"(deny file-read-data", sbplString(canonicalWorkspace), sbplString("/read-only-sdk"), sbplString("/System")} {
		if !strings.Contains(confined, want) {
			t.Fatalf("confined profile missing %q:\n%s", want, confined)
		}
	}
}

func TestDarwinBackendReportsReadIsolation(t *testing.T) {
	if !ForPlatform().Capabilities().ReadIsolation {
		t.Fatal("Seatbelt backend must advertise its read-confinement support")
	}
}
