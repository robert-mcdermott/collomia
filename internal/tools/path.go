package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type PathGuard struct {
	Workspace    string
	AllowOutside bool
}

func NewPathGuard(workspace string, allowOutside bool) (*PathGuard, error) {
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err == nil {
		abs = real
	}
	return &PathGuard{Workspace: filepath.Clean(abs), AllowOutside: allowOutside}, nil
}

func (g *PathGuard) Resolve(requested string) (resolved string, outside bool, err error) {
	if requested == "" {
		requested = "."
	}
	if !filepath.IsAbs(requested) {
		requested = filepath.Join(g.Workspace, requested)
	}
	resolved, err = filepath.Abs(requested)
	if err != nil {
		return "", false, err
	}
	resolved = filepath.Clean(resolved)
	if real, evalErr := filepath.EvalSymlinks(resolved); evalErr == nil {
		resolved = real
	} else if os.IsNotExist(evalErr) {
		parent := filepath.Dir(resolved)
		if realParent, parentErr := filepath.EvalSymlinks(parent); parentErr == nil {
			resolved = filepath.Join(realParent, filepath.Base(resolved))
		}
	}
	rel, relErr := filepath.Rel(g.Workspace, resolved)
	if relErr != nil {
		return "", false, relErr
	}
	outside = rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel)
	if outside && !g.AllowOutside {
		return "", true, fmt.Errorf("path %q is outside workspace %s; set permissions.allow_outside_workspace to grant access", requested, g.Workspace)
	}
	return resolved, outside, nil
}
