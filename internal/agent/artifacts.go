package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/plan"
	"github.com/robert-mcdermott/collomia/internal/safefile"
	"github.com/robert-mcdermott/collomia/internal/taskmode"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// toolEffects records execution scope separately from permission policy. Paths
// identify possible file effects, including partial failures. Unknown means
// execution cannot establish its complete local/remote effect set; it is never
// a claim that nothing changed, or a reason to replay an external action.
type toolEffects struct {
	Paths   []string
	Unknown bool
}

func executionEffects(name string, action tools.Action) toolEffects {
	switch name {
	case "write_file", "edit_file", "apply_patch", "format_file":
		return toolEffects{Paths: slices.Clone(action.Paths), Unknown: len(action.Paths) == 0}
	case "run_command":
		// Command Paths include inputs and working directories, not a reliable
		// write set. Never interpret them as final deliverables.
		return toolEffects{Unknown: true}
	case "read_file", "list_files", "search_files", "validate_artifact", "update_plan", "detect_verification", "read_tool_result",
		"read_task_context", "update_task_context", "read_session", "search_session", "git_status", "git_diff", "git_log", "git_blame", "load_skill", "ask_user", "inspect_delegate_changes", "compare_delegate_changes", "web_fetch", "web_search", "search_symbols", "find_definition", "find_references", "diagnostics", "list_processes", "process_output":
		return toolEffects{}
	default:
		// Compatibility for tools without an effect contract: writes remain
		// conservatively tracked; other opaque tools cannot attest no effects.
		if action.Risk == tools.RiskWrite {
			return toolEffects{Paths: slices.Clone(action.Paths), Unknown: true}
		}
		return toolEffects{Unknown: true}
	}
}

type artifactReceipt struct {
	path   string // original, absolute spelling: detects a retargeted symlink
	target string // canonical path authorized for the successful validation
	digest string
	root   safefile.RootIdentity
}

type artifactTracker struct {
	roles    map[string]string
	receipts map[string]artifactReceipt
	overflow bool
}

func (c *completionController) artifactPath(path string) string {
	path = strings.TrimSpace(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.workspace, path)
	}
	return filepath.Clean(path)
}

func (c *completionController) syncArtifactBrief(current *plan.Plan) {
	if c.taskMode != taskmode.Work || current == nil {
		return
	}
	if c.artifacts.roles == nil {
		c.artifacts.roles = map[string]string{}
	}
	for _, artifact := range current.Artifacts {
		path := c.artifactPath(artifact.Path)
		// Retain obligations across full-plan updates within this turn. A
		// missing deliverable cannot be cleared by deleting/demoting its entry.
		if c.artifacts.roles[path] == "deliverable" {
			continue
		}
		if _, exists := c.artifacts.roles[path]; !exists && len(c.artifacts.roles) >= 64 {
			c.artifacts.overflow = true
			continue
		}
		c.artifacts.roles[path] = artifact.Role
	}
}

func (c *completionController) recordArtifactReceipt(observation toolObservation) {
	evidence := observation.ArtifactEvidence
	if evidence == nil || evidence.Kind != "artifact_validated" || !tools.ValidArtifactDigest(evidence.Digest) || !evidence.ArtifactRoot.Valid() || len(observation.Action.Paths) != 1 {
		return // prose and a boolean are not a digest-bearing validation receipt
	}
	target := completionPath(observation.Action.Paths[0])
	if completionPath(c.artifactPath(evidence.Subject)) != target {
		return
	}
	path := observation.ArtifactPath
	if path == "" {
		path = observation.Action.Paths[0]
	}
	path = c.artifactPath(path)
	if completionPath(path) != target {
		return
	}
	if c.artifacts.receipts == nil {
		c.artifacts.receipts = map[string]artifactReceipt{}
	}
	if _, exists := c.artifacts.receipts[path]; !exists && len(c.artifacts.receipts) >= 64 {
		c.artifacts.overflow = true
		return
	}
	// Validating an otherwise unclassified artifact makes it an implicit
	// deliverable. Only a previously declared scratch role opts out.
	scratch := false
	for declared, role := range c.artifacts.roles {
		if completionPath(declared) == target && role == "scratch" {
			scratch = true
		}
	}
	if !scratch {
		c.syncArtifactBrief(&plan.Plan{Artifacts: []plan.Artifact{{Path: path, Role: "deliverable"}}})
	}
	c.artifacts.receipts[path] = artifactReceipt{path: path, target: target, digest: evidence.Digest, root: evidence.ArtifactRoot}
	c.acceptArtifactValidation([]string{target})
}

// checkArtifacts hashes only files for which an authorized validation returned
// a typed receipt. Declarations alone grant no reads. Rechecks apply to Work's
// Standard completion controller; Developer and graph authority are unchanged.
func (c *completionController) checkArtifacts(current *plan.Plan, active bool) []string {
	if c.taskMode != taskmode.Work {
		return nil
	}
	if active {
		c.syncArtifactBrief(current)
	}
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	var issues []string
	valid := map[string]bool{}
	deliverables := map[string]bool{}
	scratch := map[string]bool{}
	for path, role := range c.artifacts.roles {
		target := completionPath(path)
		if role == "deliverable" {
			deliverables[target] = true
		} else if role == "scratch" {
			scratch[target] = true
		}
	}
	for _, receipt := range c.artifacts.receipts {
		if scratch[receipt.target] && !deliverables[receipt.target] {
			continue
		}
		digest, err := tools.RecheckArtifactDigest(ctx, receipt.path, receipt.target, receipt.root)
		if err != nil || digest != receipt.digest {
			reason := "contents changed since validation"
			if err != nil {
				reason = err.Error()
			}
			issues = append(issues, fmt.Sprintf("artifact %s has no current receipt: %s; run validate_artifact after its final write", c.displayArtifact(receipt.path), reason))
			continue
		}
		valid[receipt.target] = true
		c.acceptArtifactValidation([]string{receipt.target})
	}
	for path, role := range c.artifacts.roles {
		if role != "deliverable" {
			continue
		}
		target := completionPath(path)
		deliverables[target] = true
		if !valid[target] {
			// Do not read a newly declared path just to explain why its receipt
			// is missing. validate_artifact uses the ordinary permission path.
			issues = append(issues, fmt.Sprintf("declared deliverable %s needs a successful current validate_artifact receipt (it may be missing or changed)", c.displayArtifact(path)))
		}
	}
	for path, role := range c.artifacts.roles {
		target := completionPath(path)
		if role == "scratch" && !deliverables[target] {
			delete(c.dirtyPaths, target)
		}
	}
	c.dirty = c.dirtyUnknown || len(c.dirtyPaths) > 0
	if c.artifacts.overflow {
		issues = append(issues, "artifact tracking exceeded 64 declarations or validation paths in this turn; split the work into smaller tasks")
	}
	slices.Sort(issues)
	return issues
}

func (c *completionController) displayArtifact(path string) string {
	if relative, err := filepath.Rel(c.workspace, path); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		path = filepath.ToSlash(relative)
	}
	return fmt.Sprintf("%q", clipUTF8(path, 240))
}
