// Package writescope owns the canonical repository-relative write-scope
// contract shared by logical plans, the goal graph, and delegated execution.
package writescope

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

const (
	Workspace = "*"
	MaxItems  = 64
	MaxBytes  = 1024
)

// Normalize validates repository-relative forward-slash paths. A trailing
// slash denotes a directory; omitted writer scopes conservatively mean the
// whole workspace. Callers that require explicit narrow scopes must reject an
// empty input or the normalized Workspace marker themselves.
func Normalize(scopes []string, write bool) ([]string, error) {
	if !write {
		if len(scopes) > 0 {
			return nil, fmt.Errorf("write_paths requires write=true")
		}
		return nil, nil
	}
	if len(scopes) == 0 {
		return []string{Workspace}, nil
	}
	if len(scopes) > MaxItems {
		return nil, fmt.Errorf("write_paths contains %d entries; maximum is %d", len(scopes), MaxItems)
	}
	seen := make(map[string]bool, len(scopes))
	workspaceWide := false
	for _, raw := range scopes {
		scope := strings.TrimSpace(raw)
		if scope == Workspace {
			workspaceWide = true
			continue
		}
		if scope == "" || strings.ContainsAny(scope, "\x00\r\n\\:*?[]") || strings.HasPrefix(scope, "/") {
			return nil, fmt.Errorf("write scope %q must be a repository-relative forward-slash path", raw)
		}
		if len(scope) > MaxBytes {
			return nil, fmt.Errorf("write scope is longer than %d bytes", MaxBytes)
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
		return []string{Workspace}, nil
	}
	out := make([]string, 0, len(seen))
	for scope := range seen {
		out = append(out, scope)
	}
	sort.Strings(out)
	return compact(out), nil
}

func compact(scopes []string) []string {
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

// Overlap reports whether two normalized writer scope sets could address the
// same repository path.
func Overlap(left, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	for _, a := range left {
		for _, b := range right {
			aFolded, bFolded := strings.ToLower(a), strings.ToLower(b)
			switch {
			case a == Workspace || b == Workspace:
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

// Violations returns changed paths outside the normalized declared scopes.
func Violations(scopes, changed []string) []string {
	if len(scopes) == 0 || len(changed) == 0 || len(scopes) == 1 && scopes[0] == Workspace {
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
