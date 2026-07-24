package agent

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

const (
	workspaceWriteScope = "*"
	maxWriteScopes      = 64
	maxWriteScopeBytes  = 1024
)

// NormalizeWriteScopes validates the scheduling contract for a delegated
// task. Scopes use repository-relative forward-slash paths. A trailing slash
// denotes a directory; "*" denotes the whole workspace. Writers that omit a
// scope are deliberately treated as workspace-wide.
func NormalizeWriteScopes(scopes []string, write bool) ([]string, error) {
	if !write {
		if len(scopes) > 0 {
			return nil, fmt.Errorf("write_paths requires write=true")
		}
		return nil, nil
	}
	if len(scopes) == 0 {
		return []string{workspaceWriteScope}, nil
	}
	if len(scopes) > maxWriteScopes {
		return nil, fmt.Errorf("write_paths contains %d entries; maximum is %d", len(scopes), maxWriteScopes)
	}
	seen := make(map[string]bool, len(scopes))
	workspaceWide := false
	for _, raw := range scopes {
		scope := strings.TrimSpace(raw)
		if scope == workspaceWriteScope {
			workspaceWide = true
			continue
		}
		if scope == "" || strings.ContainsAny(scope, "\x00\r\n\\:*?[]") || strings.HasPrefix(scope, "/") {
			return nil, fmt.Errorf("write scope %q must be a repository-relative forward-slash path", raw)
		}
		if len(scope) > maxWriteScopeBytes {
			return nil, fmt.Errorf("write scope is longer than %d bytes", maxWriteScopeBytes)
		}
		directory := strings.HasSuffix(scope, "/")
		clean := path.Clean(scope)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("write scope %q escapes the workspace", raw)
		}
		if directory {
			clean += "/"
		}
		seen[clean] = true
	}
	if workspaceWide {
		return []string{workspaceWriteScope}, nil
	}
	out := make([]string, 0, len(seen))
	for scope := range seen {
		out = append(out, scope)
	}
	sort.Strings(out)
	return compactWriteScopes(out), nil
}

func compactWriteScopes(scopes []string) []string {
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		covered := false
		for _, existing := range out {
			if strings.EqualFold(existing, scope) || strings.HasSuffix(existing, "/") && strings.HasPrefix(strings.ToLower(scope), strings.ToLower(existing)) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, scope)
		}
	}
	return out
}

func writeScopesOverlap(left, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	for _, a := range left {
		for _, b := range right {
			aFolded, bFolded := strings.ToLower(a), strings.ToLower(b)
			switch {
			case a == workspaceWriteScope || b == workspaceWriteScope:
				return true
			case aFolded == bFolded:
				return true
			case strings.HasSuffix(a, "/") && strings.HasPrefix(bFolded, aFolded):
				return true
			case strings.HasSuffix(b, "/") && strings.HasPrefix(aFolded, bFolded):
				return true
			}
		}
	}
	return false
}

func writeScopeViolations(scopes, changed []string) []string {
	if len(scopes) == 0 || len(changed) == 0 || len(scopes) == 1 && scopes[0] == workspaceWriteScope {
		return nil
	}
	var violations []string
	for _, raw := range changed {
		changedPath := strings.TrimPrefix(strings.ReplaceAll(raw, "\\", "/"), "./")
		changedFolded := strings.ToLower(changedPath)
		allowed := false
		for _, scope := range scopes {
			scopeFolded := strings.ToLower(scope)
			if scopeFolded == changedFolded || strings.HasSuffix(scope, "/") && strings.HasPrefix(changedFolded, scopeFolded) {
				allowed = true
				break
			}
		}
		if !allowed {
			violations = append(violations, changedPath)
		}
	}
	sort.Strings(violations)
	return violations
}
