package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/robert-mcdermott/collomia/internal/provider"
)

type RunCommandTool struct {
	Workspace      string
	DeniedPatterns []*regexp.Regexp
	MaxOutputBytes int
}

func NewRunCommandTool(workspace string, patterns []string, maxOutput int) (*RunCommandTool, error) {
	t := &RunCommandTool{Workspace: workspace, MaxOutputBytes: maxOutput}
	if t.MaxOutputBytes <= 0 {
		t.MaxOutputBytes = 64 * 1024
	}
	for _, pattern := range patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		t.DeniedPatterns = append(t.DeniedPatterns, re)
	}
	return t, nil
}

func (t RunCommandTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "run_command", Description: "Run one shell command in the workspace and return combined stdout/stderr. Commands have a timeout and output cap. Destructive system commands are denied even in autopilot mode.", InputSchema: schema(`{"type":"object","properties":{"command":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800}},"required":["command"],"additionalProperties":false}`)}
}
func (t RunCommandTool) Assess(raw json.RawMessage) (Action, error) {
	var a struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return Action{}, err
	}
	if strings.TrimSpace(a.Command) == "" {
		return Action{}, errors.New("command must not be empty")
	}
	return Action{Risk: RiskExecute, Summary: "run: " + a.Command}, nil
}
func (t RunCommandTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	for _, re := range t.DeniedPatterns {
		if re.MatchString(a.Command) {
			return "", fmt.Errorf("command denied by safety policy (%s)", re.String())
		}
	}
	if a.Timeout <= 0 {
		a.Timeout = 120
	}
	if a.Timeout > 1800 {
		a.Timeout = 1800
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(a.Timeout)*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(runCtx, "cmd.exe", "/d", "/s", "/c", a.Command)
	} else {
		cmd = exec.CommandContext(runCtx, "/bin/sh", "-lc", a.Command)
	}
	cmd.Dir = t.Workspace
	buffer := &limitedBuffer{limit: t.MaxOutputBytes}
	cmd.Stdout = buffer
	cmd.Stderr = buffer
	err := cmd.Run()
	out := buffer.String()
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("command timed out after %d seconds", a.Timeout)
	}
	if err != nil {
		return out, fmt.Errorf("command failed: %w", err)
	}
	if out == "" {
		out = "(command completed with no output)"
	}
	return out, nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := b.limit - b.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	if original > remaining {
		b.truncated = true
	}
	return original, nil
}
func (b *limitedBuffer) String() string {
	s := b.Buffer.String()
	if b.truncated {
		s += "\n… output truncated …"
	}
	return s
}
