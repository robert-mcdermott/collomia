package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/robert-mcdermott/collomia/internal/provider"
)

// Git inspection tools are read-only, run git directly (no shell), bound
// their output, and refuse argument shapes that could smuggle flags. The
// mutating counterparts live in gitwrite.go and go through the permission
// layer; nothing in this file commits, branches, or pushes.

const gitOutputCap = 128 * 1024

func runGit(ctx context.Context, workspace string, args ...string) (string, error) {
	out, err := runGitRaw(ctx, workspace, args...)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(out) == "" {
		out = "(no output)"
	}
	return out, nil
}

// runGitRaw returns git's output verbatim, including the empty string, so a
// caller can tell "no matching refs" from a line of output. The display
// substitution in runGit would otherwise have to be parsed back out.
func runGitRaw(ctx context.Context, workspace string, args ...string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", errors.New("git is not installed or not in PATH")
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", append([]string{"-C", workspace, "--no-pager"}, args...)...)
	buffer := &limitedBuffer{limit: gitOutputCap}
	cmd.Stdout = buffer
	cmd.Stderr = buffer
	setProcessGroup(cmd)
	err := cmd.Run()
	out := buffer.String()
	if err != nil {
		return out, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// safeGitArg rejects values that would be parsed as git flags.
func safeGitArg(value, what string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "-") {
		return "", fmt.Errorf("%s must not start with '-'", what)
	}
	return value, nil
}

type GitStatusTool struct{ Workspace string }

func (t GitStatusTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "git_status", Description: "Show the repository status: current branch, ahead/behind, and changed files (git status --porcelain=v1 -b).", InputSchema: schema(`{"type":"object","properties":{},"additionalProperties":false}`)}
}
func (t GitStatusTool) Assess(json.RawMessage) (Action, error) {
	return Action{Risk: RiskRead, Summary: "git status"}, nil
}
func (t GitStatusTool) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	return runGit(ctx, t.Workspace, "status", "--porcelain=v1", "-b")
}

type GitDiffTool struct{ Workspace string }

func (t GitDiffTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "git_diff", Description: "Show a unified diff. By default: uncommitted changes. Set staged=true for the index, or ref to compare against a commit/branch (e.g. main, HEAD~1). Optionally limit to one path.", InputSchema: schema(`{"type":"object","properties":{"staged":{"type":"boolean"},"ref":{"type":"string"},"path":{"type":"string"},"stat":{"type":"boolean","description":"Summary of changed files instead of the full diff"}},"additionalProperties":false}`)}
}
func (t GitDiffTool) Assess(json.RawMessage) (Action, error) {
	return Action{Risk: RiskRead, Summary: "git diff"}, nil
}
func (t GitDiffTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Staged bool   `json:"staged"`
		Ref    string `json:"ref"`
		Path   string `json:"path"`
		Stat   bool   `json:"stat"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	args := []string{"diff"}
	if a.Stat {
		args = append(args, "--stat")
	}
	if a.Staged {
		args = append(args, "--cached")
	}
	if a.Ref != "" {
		ref, err := safeGitArg(a.Ref, "ref")
		if err != nil {
			return "", err
		}
		args = append(args, ref)
	}
	if a.Path != "" {
		path, err := safeGitArg(a.Path, "path")
		if err != nil {
			return "", err
		}
		args = append(args, "--", path)
	}
	return runGit(ctx, t.Workspace, args...)
}

type GitLogTool struct{ Workspace string }

func (t GitLogTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "git_log", Description: "Show recent commits (hash, author, date, subject). Optionally limit to one path or change the count (default 20, max 100).", InputSchema: schema(`{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":100},"path":{"type":"string"}},"additionalProperties":false}`)}
}
func (t GitLogTool) Assess(json.RawMessage) (Action, error) {
	return Action{Risk: RiskRead, Summary: "git log"}, nil
}
func (t GitLogTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Limit int    `json:"limit"`
		Path  string `json:"path"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if a.Limit <= 0 {
		a.Limit = 20
	}
	if a.Limit > 100 {
		a.Limit = 100
	}
	args := []string{"log", fmt.Sprintf("-%d", a.Limit), "--pretty=format:%h %an %ad %s", "--date=short"}
	if a.Path != "" {
		path, err := safeGitArg(a.Path, "path")
		if err != nil {
			return "", err
		}
		args = append(args, "--", path)
	}
	return runGit(ctx, t.Workspace, args...)
}

type GitBlameTool struct{ Workspace string }

func (t GitBlameTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "git_blame", Description: "Show who last changed each line of a file region (git blame). Provide start_line/end_line to bound the output.", InputSchema: schema(`{"type":"object","properties":{"path":{"type":"string"},"start_line":{"type":"integer","minimum":1},"end_line":{"type":"integer","minimum":1}},"required":["path"],"additionalProperties":false}`)}
}
func (t GitBlameTool) Assess(raw json.RawMessage) (Action, error) {
	return Action{Risk: RiskRead, Summary: "git blame"}, nil
}
func (t GitBlameTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	path, err := safeGitArg(a.Path, "path")
	if err != nil || path == "" {
		return "", errors.New("path is required and must not start with '-'")
	}
	args := []string{"blame", "--date=short"}
	if a.StartLine > 0 {
		end := a.EndLine
		if end < a.StartLine {
			end = a.StartLine + 50
		}
		args = append(args, fmt.Sprintf("-L%d,%d", a.StartLine, end))
	}
	args = append(args, "--", path)
	return runGit(ctx, t.Workspace, args...)
}
