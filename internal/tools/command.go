package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/sandbox"
	"github.com/robert-mcdermott/collomia/internal/shell"
)

type RunCommandTool struct {
	Workspace      string
	DeniedPatterns []*regexp.Regexp
	MaxOutputBytes int
	// SandboxMode selects OS enforcement: off, auto, or require.
	SandboxMode sandbox.Mode
	// AllowNetwork permits network egress inside the sandbox.
	AllowNetwork bool
	// MinimalEnv strips the parent environment down to basics so parent
	// secrets are not inherited by agent commands.
	MinimalEnv bool
	Backend    sandbox.Backend
}

func NewRunCommandTool(workspace string, patterns []string, maxOutput int) (*RunCommandTool, error) {
	t := &RunCommandTool{Workspace: workspace, MaxOutputBytes: maxOutput, SandboxMode: sandbox.ModeOff, Backend: sandbox.ForPlatform()}
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
	analysis := shell.Analyze(a.Command)
	return Action{
		Risk: RiskExecute, Summary: "run: " + a.Command,
		Executables:     analysis.Executables,
		Uninspectable:   !analysis.Inspectable,
		AnalysisReasons: analysis.Reasons,
	}, nil
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
	argv := shellArgv(a.Command)
	sandboxed := false
	if t.SandboxMode == sandbox.ModeAuto || t.SandboxMode == sandbox.ModeRequire {
		if err := t.Backend.Available(); err != nil {
			if t.SandboxMode == sandbox.ModeRequire {
				return "", fmt.Errorf("sandbox required but unavailable: %w", err)
			}
		} else {
			wrapped, err := t.Backend.Wrap(argv, sandbox.Policy{WorkspaceRoot: t.Workspace, AllowNetwork: t.AllowNetwork})
			if err != nil {
				if t.SandboxMode == sandbox.ModeRequire {
					return "", fmt.Errorf("sandbox required: %w", err)
				}
			} else {
				argv = wrapped
				sandboxed = true
			}
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(a.Timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir = t.Workspace
	if t.MinimalEnv {
		cmd.Env = minimalEnv()
	}
	// The command runs in its own process group so cancellation and timeout
	// terminate every descendant, not only the shell.
	setProcessGroup(cmd)
	buffer := &limitedBuffer{limit: t.MaxOutputBytes}
	cmd.Stdout = buffer
	cmd.Stderr = buffer
	err := cmd.Run()
	out := buffer.String()
	if sandboxed && err != nil {
		out += "\n(command ran inside the OS sandbox; denied file or network access can cause failures — see docs/SECURITY.md)"
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("command timed out after %d seconds; its process group was terminated", a.Timeout)
	}
	if err != nil {
		return out, fmt.Errorf("command failed: %w", err)
	}
	if out == "" {
		out = "(command completed with no output)"
	}
	return out, nil
}

// minimalEnv keeps only the variables a build needs, so credentials in the
// parent environment never reach agent commands.
func minimalEnv() []string {
	keep := []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "TEMP", "TMP", "TERM", "LANG", "LC_ALL", "LC_CTYPE", "COLUMNS", "LINES", "SYSTEMROOT", "COMSPEC", "PATHEXT"}
	var env []string
	for _, key := range keep {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func shellArgv(command string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd.exe", "/d", "/s", "/c", command}
	}
	return []string{"/bin/sh", "-lc", command}
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
