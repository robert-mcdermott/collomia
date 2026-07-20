package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	// ExtraWritableRoots grants explicit additional write locations to the OS
	// sandbox, commonly for build or package-manager caches.
	ExtraWritableRoots []string
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
	return provider.ToolDefinition{Name: "run_command", Description: "Run one shell command in the workspace and return combined stdout/stderr. Commands have a timeout and output cap. Destructive system commands are denied even in autopilot mode. When OS sandbox networking is disabled, package installs and online CLIs may fail; the user can enable command networking with permissions.sandbox_allow_network. Provider and remote MCP traffic are unaffected. Set pty=true (Unix only) for programs that need a terminal — interactive-only CLIs, or tools whose output depends on isatty.", InputSchema: schema(`{"type":"object","properties":{"command":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"pty":{"type":"boolean","description":"Run attached to a pseudo-terminal (Unix only)"}},"required":["command"],"additionalProperties":false}`)}
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
	analysis := shell.AnalyzeInWorkspace(a.Command, t.Workspace)
	return Action{
		Risk: RiskExecute, Summary: "run: " + a.Command,
		Executables:     analysis.Executables,
		Uninspectable:   !analysis.Inspectable,
		AnalysisReasons: analysis.Reasons,
		HardDenyReasons: analysis.HardDenyReasons,
		ConfirmReasons:  analysis.ConfirmReasons,
	}, nil
}
func (t RunCommandTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	return t.run(ctx, raw, nil)
}

// ExecuteStream runs the command while forwarding output chunks as they
// arrive, so the UI can show long builds and test runs live.
func (t RunCommandTool) ExecuteStream(ctx context.Context, raw json.RawMessage, onOutput func(string)) (string, error) {
	return t.run(ctx, raw, onOutput)
}

func (t RunCommandTool) run(ctx context.Context, raw json.RawMessage, onOutput func(string)) (string, error) {
	var a struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout_seconds"`
		PTY     bool   `json:"pty"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if a.PTY && !ptySupported {
		return "", errors.New("pty execution is not supported on this platform; run the command without pty")
	}
	if err := t.checkCommandSafety(a.Command); err != nil {
		return "", err
	}
	if a.Timeout <= 0 {
		a.Timeout = 120
	}
	if a.Timeout > 1800 {
		a.Timeout = 1800
	}
	argv := shellArgv(a.Command)
	sandboxed := false
	sandboxWarning := ""
	if t.SandboxMode == sandbox.ModeAuto || t.SandboxMode == sandbox.ModeRequire {
		prepared, err := sandbox.Prepare(t.Backend, t.SandboxMode, argv, sandbox.Policy{WorkspaceRoot: t.Workspace, ExtraWritableRoots: t.resolvedWritableRoots(), AllowNetwork: t.AllowNetwork})
		if err != nil {
			return "", err
		}
		argv = prepared.Argv
		sandboxed = prepared.Active
		sandboxWarning = prepared.Degraded
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(a.Timeout)*time.Second)
	defer cancel()
	buffer := &limitedBuffer{limit: t.MaxOutputBytes, onChunk: onOutput}
	if sandboxWarning != "" {
		_, _ = buffer.Write([]byte("sandbox warning: " + sandboxWarning + "\n"))
	}
	var err error
	if a.PTY {
		var env []string
		if t.MinimalEnv {
			env = append(minimalEnv(), "TERM=xterm-256color")
		}
		err = runUnderPTY(runCtx, argv, t.Workspace, env, buffer)
	} else {
		cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
		cmd.Dir = t.Workspace
		if t.MinimalEnv {
			cmd.Env = minimalEnv()
		}
		// The command runs in its own process group so cancellation and
		// timeout target the group, not only the shell.
		setProcessGroup(cmd)
		// One writer instance serves both streams so os/exec serializes writes.
		cmd.Stdout = buffer
		cmd.Stderr = buffer
		err = cmd.Run()
	}
	out := buffer.String()
	if sandboxed && err != nil {
		out += "\n(command ran inside the OS sandbox; denied file or network access can cause failures — set permissions.sandbox_allow_network=true for commands that need outbound access, or see docs/SECURITY.md)"
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

// checkCommandSafety repeats non-overridable checks immediately before
// execution. Authorization and execution are deliberately separate; this
// closes any future call path that might execute a command without Assess.
func (t RunCommandTool) checkCommandSafety(command string) error {
	analysis := shell.AnalyzeInWorkspace(command, t.Workspace)
	if len(analysis.HardDenyReasons) > 0 {
		return fmt.Errorf("command denied by built-in catastrophic-command protection: %s", strings.Join(analysis.HardDenyReasons, "; "))
	}
	for _, re := range t.DeniedPatterns {
		if re.MatchString(command) {
			return fmt.Errorf("command denied by safety policy (%s)", re.String())
		}
	}
	return nil
}

func (t RunCommandTool) resolvedWritableRoots() []string {
	roots := make([]string, 0, len(t.ExtraWritableRoots))
	for _, root := range t.ExtraWritableRoots {
		root = os.ExpandEnv(strings.TrimSpace(root))
		if root == "" {
			continue
		}
		if !filepath.IsAbs(root) {
			root = filepath.Join(t.Workspace, root)
		}
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
		if real, err := filepath.EvalSymlinks(root); err == nil {
			root = real
		}
		roots = append(roots, root)
	}
	return roots
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
	// onChunk, when set, observes every accepted chunk for live streaming.
	onChunk func(string)
}

// ReadFrom overrides the embedded bytes.Buffer promotion: io.Copy (which
// os/exec uses to drain pipes) prefers ReadFrom, and the embedded version
// would bypass Write — defeating both the output cap and live streaming.
func (b *limitedBuffer) ReadFrom(r io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, err := r.Read(buf)
		if n > 0 {
			total += int64(n)
			_, _ = b.Write(buf[:n])
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := b.limit - b.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
		if b.onChunk != nil {
			b.onChunk(string(p))
		}
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
