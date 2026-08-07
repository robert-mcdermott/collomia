package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/safefile"
)

type PathGuard struct {
	Workspace    string
	AllowOutside bool
	// ReadRoots are directories outside the workspace whose contents may be
	// read but never written. They exist because Collomia itself tells the
	// model about files it then could not open: a loaded skill's own
	// documentation names its reference files, and refusing to read them makes
	// the agent look broken over material the user installed deliberately.
	//
	// This is a read allowance only. Writes resolve through MutationTarget,
	// which uses the strict path, so nothing here can be modified.
	ReadRoots   []string
	workspaceID safefile.RootIdentity
}

// MutationTarget resolves policy exactly as Resolve does, then anchors the
// resulting path to an os.Root-backed safe mutation target. Workspace paths
// cannot escape through a symlink swap between authorization and execution.
// Explicitly allowed outside paths are anchored to their resolved parent.
func (g *PathGuard) MutationTarget(requested string) (*safefile.Target, bool, error) {
	resolved, outside, err := g.Resolve(requested)
	if err != nil {
		return nil, outside, err
	}
	root := g.Workspace
	if outside {
		root = filepath.Dir(resolved)
	}
	target, err := safefile.Open(root, resolved)
	if err != nil {
		return nil, outside, err
	}
	if !outside && g.workspaceID.Valid() {
		opened, identityErr := target.RootIdentity()
		if identityErr != nil || !g.workspaceID.Same(opened) {
			_ = target.Close()
			if identityErr != nil {
				return nil, outside, fmt.Errorf("verify workspace mutation root: %w", identityErr)
			}
			return nil, outside, fmt.Errorf("workspace mutation root changed since startup")
		}
	}
	return target, outside, nil
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
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace %s is not a directory", abs)
	}
	identity, err := safefile.CaptureRootIdentity(abs)
	if err != nil {
		return nil, fmt.Errorf("capture workspace mutation root: %w", err)
	}
	return &PathGuard{Workspace: filepath.Clean(abs), AllowOutside: allowOutside, workspaceID: identity}, nil
}

// ResolveRead is Resolve plus the read-only roots. Only genuinely read-only
// tools may call it; every mutation path stays on Resolve.
func (g *PathGuard) ResolveRead(requested string) (resolved string, outside bool, err error) {
	return g.resolve(requested, true)
}

func (g *PathGuard) Resolve(requested string) (resolved string, outside bool, err error) {
	return g.resolve(requested, false)
}

// AddReadRoot registers a directory whose contents may be read. The path is
// resolved through symlinks at registration, so a link planted inside it later
// still cannot widen the allowance: containment is checked against the real
// path a request resolves to.
func (g *PathGuard) AddReadRoot(dir string) {
	if g == nil || strings.TrimSpace(dir) == "" {
		return
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return
	}
	if real, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
		abs = real
	}
	abs = filepath.Clean(abs)
	if info, statErr := os.Stat(abs); statErr != nil || !info.IsDir() {
		return
	}
	for _, existing := range g.ReadRoots {
		if existing == abs {
			return
		}
	}
	g.ReadRoots = append(g.ReadRoots, abs)
}

// readableOutside reports whether an already-symlink-resolved path lies inside
// a registered read root.
func (g *PathGuard) readableOutside(resolved string) bool {
	if g == nil {
		return false
	}
	for _, root := range g.ReadRoots {
		rel, err := filepath.Rel(root, resolved)
		if err != nil {
			continue
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return true
		}
	}
	return false
}

func (g *PathGuard) resolve(requested string, read bool) (resolved string, outside bool, err error) {
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
		if read && g.readableOutside(resolved) {
			return resolved, true, nil
		}
		return "", true, fmt.Errorf("path %q is outside workspace %s; set permissions.allow_outside_workspace to grant access", requested, g.Workspace)
	}
	return resolved, outside, nil
}
