package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/safefile"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// A scope is a proposed relationship between a check and files. Only native
// execution plus byte observations can produce the resulting receipts. This
// does not establish that a model-selected test covers all user requirements.
type scopedVerification struct {
	purpose string
	files   []artifactReceipt
}

func prepareScopedVerification(ctx context.Context, raw json.RawMessage, action tools.Action, workspace string) (*scopedVerification, error) {
	v, err := tools.ParseCommandVerification(raw)
	if err != nil || v == nil {
		return nil, err
	}
	if _, reason := safeVerificationChain(stripSafeVerificationStderrMerge(action.Command), workspace); reason != "" {
		return nil, fmt.Errorf("verification was not started: %s; put the check in a script that exits nonzero on failure and run it directly", reason)
	}
	if len(v.Paths) != len(action.Paths) {
		return nil, fmt.Errorf("verification scope does not match authorized paths")
	}
	check := &scopedVerification{purpose: v.Purpose}
	for i, path := range v.Paths {
		if !filepath.IsAbs(path) {
			path = filepath.Join(workspace, path)
		}
		target := action.Paths[i]
		root, err := safefile.Open(filepath.Dir(target), target)
		if err != nil {
			return nil, fmt.Errorf("verification scope %q: %w", path, err)
		}
		identity, identityErr := root.RootIdentity()
		root.Close()
		if identityErr != nil {
			return nil, identityErr
		}
		digest, err := tools.RecheckArtifactDigest(ctx, path, target, identity)
		if err != nil {
			return nil, fmt.Errorf("verification scope %q: %w", path, err)
		}
		check.files = append(check.files, artifactReceipt{path: path, target: target, digest: digest, root: identity})
	}
	return check, nil
}

func (v *scopedVerification) finish(ctx context.Context) error {
	for _, file := range v.files {
		digest, err := tools.RecheckArtifactDigest(ctx, file.path, file.target, file.root)
		if err != nil || digest != file.digest {
			return fmt.Errorf("verification command finished, but %q changed or became inaccessible during the check; inspect it and rerun the check on the final files", file.path)
		}
	}
	return nil
}

func (v *scopedVerification) summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nVerification recorded: %s\nScope: command exited zero; listed file bytes matched before and after the check. Test coverage is determined by the command, not by this receipt.", v.purpose)
	for _, file := range v.files {
		fmt.Fprintf(&b, "\n%s %s", file.path, file.digest)
	}
	return b.String()
}

func (v *scopedVerification) digests(workspace string) map[string]string {
	files := make(map[string]string, len(v.files))
	for _, file := range v.files {
		path := file.path
		if rel, err := filepath.Rel(workspace, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			path = filepath.ToSlash(rel)
		}
		files[path] = file.digest
	}
	return files
}

func (c *completionController) recordScopedVerification(files []artifactReceipt) {
	if c.artifacts.receipts == nil {
		c.artifacts.receipts = make(map[string]artifactReceipt)
	}
	if c.artifacts.roles == nil {
		c.artifacts.roles = make(map[string]string)
	}
	for _, file := range files {
		if _, exists := c.artifacts.receipts[file.path]; !exists && len(c.artifacts.receipts) >= 64 {
			c.artifacts.overflow = true
			continue
		}
		c.artifacts.receipts[file.path] = file
		// Scope survives restart as an obligation, never as a passing receipt.
		if c.artifacts.roles[file.path] != "scratch" {
			if _, exists := c.artifacts.roles[file.path]; !exists && len(c.artifacts.roles) >= 64 {
				c.artifacts.overflow = true
			} else {
				c.artifacts.roles[file.path] = "deliverable"
			}
		}
		c.acceptArtifactValidation([]string{file.target})
	}
	c.recognizedEver = true
}

// A successfully completed native file replacement followed by current
// verification can recover a failed file edit/patch. Neither an unrelated
// success nor a prose assertion is enough. Permission/hook/opaque failures
// lack this identity and retain explicit recovery.
func (c *completionController) recoverVerifiedFileFailures() {
	remaining := c.failures[:0]
	for _, failure := range c.failures {
		recovered := len(failure.repairPaths) > 0 && failure.repairReady
		for _, path := range failure.repairPaths {
			found := false
			for _, receipt := range c.artifacts.receipts {
				if receipt.target != path {
					continue
				}
				digest, err := tools.RecheckArtifactDigest(c.ctxOrBackground(), receipt.path, receipt.target, receipt.root)
				if err == nil && digest == receipt.digest {
					found = true
					break
				}
			}
			if !found {
				recovered = false
				break
			}
		}
		if !recovered {
			remaining = append(remaining, failure)
		}
	}
	c.failures = remaining
}

func (c *completionController) markFileRepairs(o toolObservation) {
	if o.Failed || o.ExecutionPrevented || o.Effects.Unknown {
		return
	}
	switch o.Name {
	case "write_file", "edit_file", "apply_patch", "format_file":
	default:
		return
	}
	paths := make([]string, 0, len(o.Effects.Paths))
	for _, path := range o.Effects.Paths {
		paths = append(paths, completionPath(path))
	}
	for i := range c.failures {
		failure := &c.failures[i]
		covered := len(failure.repairPaths) > 0
		for _, path := range failure.repairPaths {
			if !slices.Contains(paths, path) {
				covered = false
			}
		}
		if covered {
			failure.repairReady = true
		}
	}
}

func (c *completionController) ctxOrBackground() context.Context {
	if c.ctx != nil {
		return c.ctx
	}
	return context.Background()
}
