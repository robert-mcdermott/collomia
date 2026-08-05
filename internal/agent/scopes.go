package agent

import (
	"github.com/robert-mcdermott/collomia/internal/writescope"
)

const (
	workspaceWriteScope = writescope.Workspace
	maxWriteScopes      = writescope.MaxItems
	maxWriteScopeBytes  = writescope.MaxBytes
)

// NormalizeWriteScopes validates the scheduling contract for a delegated
// task. Scopes use repository-relative forward-slash paths. A trailing slash
// denotes a directory; "*" denotes the whole workspace. Writers that omit a
// scope are deliberately treated as workspace-wide.
func NormalizeWriteScopes(scopes []string, write bool) ([]string, error) {
	return writescope.Normalize(scopes, write)
}

func writeScopesOverlap(left, right []string) bool {
	return writescope.Overlap(left, right)
}

func writeScopeViolations(scopes, changed []string) []string {
	return writescope.Violations(scopes, changed)
}
