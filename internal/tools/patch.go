package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/diffmodel"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

// ApplyPatchTool applies a multi-file change set atomically: every operation
// is validated against the current file contents first, then all are
// applied; any failure rolls back the files already written. The result is a
// machine-readable changeset.
type ApplyPatchTool struct {
	Guard   *PathGuard
	Tracker *diffmodel.Tracker
}

type patchOperation struct {
	Op      string `json:"op"` // update, create, delete
	Path    string `json:"path"`
	OldText string `json:"old_text,omitempty"`
	NewText string `json:"new_text,omitempty"`
	Content string `json:"content,omitempty"`
}

type patchInput struct {
	Operations []patchOperation `json:"operations"`
}

func (t ApplyPatchTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "apply_patch", Description: "Apply a multi-file change set atomically. Each operation is one of: update (replace one exact, unique old_text with new_text in an existing file), create (new file with content), delete (remove a file). All operations are validated against current file contents before any is applied; on failure nothing changes. Use this for related edits that must land together.", InputSchema: schema(`{"type":"object","properties":{"operations":{"type":"array","minItems":1,"items":{"type":"object","properties":{"op":{"type":"string","enum":["update","create","delete"]},"path":{"type":"string"},"old_text":{"type":"string"},"new_text":{"type":"string"},"content":{"type":"string"}},"required":["op","path"],"additionalProperties":false}}},"required":["operations"],"additionalProperties":false}`)}
}

// resolved is a fully validated operation ready to apply.
type resolvedOperation struct {
	patchOperation
	abs    string
	before *string
	after  *string
	mode   os.FileMode
}

func (t ApplyPatchTool) resolve(raw json.RawMessage) ([]resolvedOperation, bool, error) {
	var input patchInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, false, err
	}
	if len(input.Operations) == 0 {
		return nil, false, errors.New("operations must not be empty")
	}
	outside := false
	var ops []resolvedOperation
	staged := map[string]*string{}
	for i, op := range input.Operations {
		abs, out, err := t.Guard.Resolve(op.Path)
		if err != nil {
			return nil, false, fmt.Errorf("operations[%d]: %w", i, err)
		}
		outside = outside || out
		resolved := resolvedOperation{patchOperation: op, abs: abs, mode: 0o644}
		// Later operations see earlier staged content, so multiple updates
		// to one file validate in sequence.
		current, seen := staged[abs]
		if !seen {
			if data, readErr := os.ReadFile(abs); readErr == nil {
				text := string(data)
				current = &text
			}
			if info, statErr := os.Stat(abs); statErr == nil {
				resolved.mode = info.Mode().Perm()
			}
		}
		resolved.before = current
		switch op.Op {
		case "create":
			if current != nil {
				return nil, false, fmt.Errorf("operations[%d]: %s already exists; use update", i, op.Path)
			}
			content := op.Content
			resolved.after = &content
		case "update":
			if current == nil {
				return nil, false, fmt.Errorf("operations[%d]: %s does not exist; use create", i, op.Path)
			}
			if op.OldText == "" {
				return nil, false, fmt.Errorf("operations[%d]: update requires old_text", i)
			}
			count := strings.Count(*current, op.OldText)
			if count != 1 {
				return nil, false, fmt.Errorf("operations[%d]: old_text must match %s exactly once (found %d); the file may differ from what you expect — re-read it", i, op.Path, count)
			}
			updated := strings.Replace(*current, op.OldText, op.NewText, 1)
			resolved.after = &updated
		case "delete":
			if current == nil {
				return nil, false, fmt.Errorf("operations[%d]: %s does not exist", i, op.Path)
			}
			resolved.after = nil
		default:
			return nil, false, fmt.Errorf("operations[%d]: unknown op %q", i, op.Op)
		}
		staged[abs] = resolved.after
		ops = append(ops, resolved)
	}
	return ops, outside, nil
}

func (t ApplyPatchTool) Assess(raw json.RawMessage) (Action, error) {
	ops, outside, err := t.resolve(raw)
	if err != nil {
		return Action{}, err
	}
	var paths []string
	var names []string
	var preview strings.Builder
	for _, op := range ops {
		paths = append(paths, op.abs)
		names = append(names, op.Op+" "+displayName(t.Guard.Workspace, op.abs))
		before, after := "", ""
		if op.before != nil {
			before = *op.before
		}
		if op.after != nil {
			after = *op.after
		}
		preview.WriteString(diffmodel.Unified(displayName(t.Guard.Workspace, op.abs), before, after))
	}
	return Action{Risk: RiskWrite, Summary: fmt.Sprintf("apply patch (%s)", strings.Join(names, ", ")), Outside: outside, Paths: paths, Preview: preview.String()}, nil
}

func (t ApplyPatchTool) Execute(_ context.Context, raw json.RawMessage) (string, error) {
	ops, _, err := t.resolve(raw)
	if err != nil {
		return "", err
	}
	type applied struct {
		abs    string
		before *string
		mode   os.FileMode
	}
	var done []applied
	rollback := func() {
		for i := len(done) - 1; i >= 0; i-- {
			step := done[i]
			if step.before == nil {
				_ = os.Remove(step.abs)
			} else {
				_ = os.WriteFile(step.abs, []byte(*step.before), step.mode)
			}
		}
	}
	for _, op := range ops {
		var applyErr error
		switch {
		case op.after == nil:
			applyErr = os.Remove(op.abs)
		default:
			if mkErr := os.MkdirAll(filepath.Dir(op.abs), 0o755); mkErr != nil {
				applyErr = mkErr
			} else {
				applyErr = os.WriteFile(op.abs, []byte(*op.after), op.mode)
			}
		}
		if applyErr != nil {
			rollback()
			return "", fmt.Errorf("patch failed at %s and was rolled back: %w", op.Path, applyErr)
		}
		done = append(done, applied{abs: op.abs, before: op.before, mode: op.mode})
	}
	changeset := map[string]any{"applied": []map[string]string{}}
	var summary []map[string]string
	for _, op := range ops {
		if t.Tracker != nil {
			t.Tracker.Record(op.abs, "patch:"+op.Op, op.before, op.after)
		}
		summary = append(summary, map[string]string{"op": op.Op, "path": displayName(t.Guard.Workspace, op.abs)})
	}
	changeset["applied"] = summary
	data, _ := json.Marshal(changeset)
	return string(data), nil
}
