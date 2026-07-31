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

	"github.com/robert-mcdermott/collomia/internal/egress"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/sandbox"
	"github.com/robert-mcdermott/collomia/internal/shell"
)

type RunCommandTool struct {
	Workspace      string
	DeniedPatterns []*regexp.Regexp
	MaxOutputBytes int
	// StreamOutputBytes can be lower than MaxOutputBytes when the runtime is
	// retaining a larger bounded result as a session artifact. Live UI output
	// remains at the configured preview size while the returned string carries
	// enough data for the agent layer to create the artifact.
	StreamOutputBytes int
	// SandboxMode selects OS enforcement: off, auto, or require.
	SandboxMode sandbox.Mode
	// AllowNetwork permits network egress inside the sandbox.
	AllowNetwork bool
	// AllowReadOutsideWorkspace preserves broad command reads. Set false to
	// request OS-enforced user-data read confinement.
	AllowReadOutsideWorkspace bool
	// ExtraReadableRoots grants explicit additional read locations to the OS
	// sandbox, commonly for dependency stores or read-only SDKs.
	ExtraReadableRoots []string
	// ExtraWritableRoots grants explicit additional write locations to the OS
	// sandbox, commonly for build or package-manager caches.
	ExtraWritableRoots []string
	// MinimalEnv strips the parent environment down to basics so parent
	// secrets are not inherited by agent commands.
	MinimalEnv bool
	Backend    sandbox.Backend
	// EgressScoped requests brokered per-host egress instead of the
	// all-or-nothing AllowNetwork switch. It is honored only where the sandbox
	// backend can deny direct remote traffic while leaving loopback reachable;
	// elsewhere it fails closed under require and degrades visibly under auto.
	EgressScoped bool
	// EgressAllowlist names the destinations the broker may dial. It is built
	// from the same host-scoped allow rules the policy layer matches.
	EgressAllowlist egress.Allowlist
	// EgressObserve receives each brokered decision for the audit ledger.
	EgressObserve func(egress.Decision)
}

// egressPlan is the resolved decision about brokering one command: whether to
// start a broker, and what to tell the user when the answer is no.
type egressPlan struct {
	broker   bool
	degraded string
	err      error
}

// planEgress decides whether scoped egress applies to this command.
//
// Scoped egress is only ever enforcement in combination with a sandbox that
// denies direct remote traffic. Every path that cannot provide that half
// refuses to start a broker rather than injecting proxy variables anyway: a
// proxy the user believes is a boundary, which any program that ignores
// HTTP_PROXY walks straight past, is worse than an honest coarse control.
func (t RunCommandTool) planEgress() egressPlan {
	if !t.EgressScoped {
		return egressPlan{}
	}
	if t.SandboxMode == sandbox.ModeOff {
		return egressPlan{degraded: "permissions.sandbox_egress is \"scoped\" but the OS sandbox is off, so nothing stops a command from bypassing the broker; command networking followed sandbox_allow_network instead"}
	}
	if supported, why := egress.Supported(); !supported {
		if t.SandboxMode == sandbox.ModeRequire {
			return egressPlan{err: fmt.Errorf("permissions.sandbox_egress is \"scoped\" and permissions.sandbox is \"require\", but this platform cannot enforce it: %s", why)}
		}
		return egressPlan{degraded: "permissions.sandbox_egress is \"scoped\" but this platform cannot enforce it, so command networking followed sandbox_allow_network instead: " + why}
	}
	if t.EgressAllowlist.Empty() {
		// Strictly correct and almost always a mistake, so it is stated up
		// front rather than surfacing as a wall of refused connections.
		return egressPlan{broker: true, degraded: "permissions.sandbox_egress is \"scoped\" but no allow rule names a host, so every outbound connection will be refused; add {\"action\":\"allow\",\"host\":\"…\"} to permissions.rules"}
	}
	return egressPlan{broker: true}
}

func NewRunCommandTool(workspace string, patterns []string, maxOutput int) (*RunCommandTool, error) {
	t := &RunCommandTool{Workspace: workspace, MaxOutputBytes: maxOutput, SandboxMode: sandbox.ModeOff, AllowReadOutsideWorkspace: true, Backend: sandbox.ForPlatform()}
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
	return provider.ToolDefinition{Name: "run_command", Description: "Run one shell command in the workspace and return combined stdout/stderr. Commands have a timeout and output cap. Destructive system commands are denied even in autopilot mode. OS sandbox policy may deny outside-workspace reads or writes and command networking; required read-only dependencies belong in permissions.sandbox_readable_roots, writable caches in sandbox_writable_roots, and outbound access is controlled by sandbox_allow_network. Provider and remote MCP traffic are unaffected. Set pty=true for programs that need a terminal — interactive-only CLIs, or tools whose output depends on isatty.", InputSchema: schema(`{"type":"object","properties":{"command":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"pty":{"type":"boolean","description":"Run attached to a pseudo-terminal"}},"required":["command"],"additionalProperties":false}`)}
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
	return ActionFromAnalysis("run: "+a.Command, a.Command, analysis), nil
}

// ActionFromAnalysis builds the permission-facing description of a shell
// command from its static analysis.
//
// Every caller that evaluates a command must go through here rather than
// assembling an Action by hand. A second construction site is how the host
// matcher once shipped inert — documented, validated, and never populated —
// and a hand-written copy in "collo policy check" reported the wrong decision
// for a credential-reaching command for the same reason. Adding a field to
// Analysis should require changing one function, not finding every caller.
func ActionFromAnalysis(summary, command string, analysis shell.Analysis) Action {
	return Action{
		Risk:               RiskExecute,
		Summary:            summary,
		Command:            command,
		Executables:        analysis.Executables,
		Operations:         analysis.Operations,
		Hosts:              analysis.Hosts,
		Network:            analysis.NetworkCommand,
		HostsUndetermined:  analysis.UndeterminedHosts,
		HostReasons:        analysis.HostReasons,
		Uninspectable:      !analysis.Inspectable,
		AnalysisReasons:    analysis.Reasons,
		HardDenyReasons:    analysis.HardDenyReasons,
		ConfirmReasons:     analysis.ConfirmReasons,
		CredentialTargets:  analysis.CredentialTargets,
		PublicationTargets: analysis.PublicationTargets,
	}
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
	plan := t.planEgress()
	if plan.err != nil {
		return "", plan.err
	}
	var broker *egress.Broker
	if plan.broker {
		started, err := egress.Start(t.EgressAllowlist, t.EgressObserve)
		if err != nil {
			if t.SandboxMode == sandbox.ModeRequire {
				return "", fmt.Errorf("scoped egress required but the broker could not start: %w", err)
			}
			plan.broker = false
			plan.degraded = "scoped egress broker could not start, so command networking followed sandbox_allow_network instead: " + err.Error()
		} else {
			broker = started
			defer broker.Close()
		}
	}
	if t.SandboxMode == sandbox.ModeAuto || t.SandboxMode == sandbox.ModeRequire {
		prepared, err := sandbox.Prepare(t.Backend, t.SandboxMode, argv, t.sandboxPolicy(broker != nil))
		if err != nil {
			return "", err
		}
		argv = prepared.Argv
		sandboxed = prepared.Active
		sandboxWarning = prepared.Degraded
	}
	if plan.degraded != "" {
		sandboxWarning = strings.TrimSpace(plan.degraded + "\n" + sandboxWarning)
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(a.Timeout)*time.Second)
	defer cancel()
	streamLimit := t.StreamOutputBytes
	if streamLimit <= 0 {
		streamLimit = t.MaxOutputBytes
	}
	buffer := &limitedBuffer{limit: t.MaxOutputBytes, streamLimit: streamLimit, onChunk: onOutput}
	if sandboxWarning != "" {
		_, _ = buffer.Write([]byte("sandbox warning: " + sandboxWarning + "\n"))
	}
	var err error
	if a.PTY {
		var extra []string
		if t.MinimalEnv {
			extra = append(extra, "TERM=xterm-256color")
		}
		err = runUnderPTY(runCtx, argv, t.Workspace, t.commandEnv(broker, extra...), buffer)
	} else {
		cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
		cmd.Dir = t.Workspace
		cmd.Env = t.commandEnv(broker)
		// The command runs in its own process group so cancellation and
		// timeout target the group, not only the shell.
		setProcessGroup(cmd)
		// One writer instance serves both streams so os/exec serializes writes.
		cmd.Stdout = buffer
		cmd.Stderr = buffer
		err = cmd.Run()
	}
	out := buffer.String()
	refusedEgress := false
	if broker != nil {
		// A refusal is reported whether or not the command failed: a build that
		// quietly skipped an optional download still needs to say which host it
		// could not reach.
		if refused := broker.Refused(); len(refused) > 0 {
			refusedEgress = true
			out += "\n(scoped egress refused " + strings.Join(refused, ", ") + "; permissions.sandbox_egress is \"scoped\" and no allow rule names " + plural(len(refused), "that host", "those hosts") + ". Add {\"action\":\"allow\",\"host\":\"" + refused[0] + "\"} to permissions.rules)"
		}
	}
	if sandboxed && err != nil && !refusedEgress {
		// The generic hint is suppressed after an egress refusal: that message
		// already names the host and the rule to add, and pointing at
		// sandbox_allow_network would send the user to the switch scoped egress
		// exists to replace.
		out += "\n(command ran inside the OS sandbox; it may also have failed normally. If access was denied, use permissions.sandbox_readable_roots for required read-only dependencies, permissions.sandbox_writable_roots for caches, " + networkHint(broker != nil) + ", or permissions.command_env=full for deliberately inherited environment variables. To opt out of OS containment entirely, set permissions.preset=frictionless; inspect `collo doctor` and docs/SECURITY.md)"
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
	return t.resolvedRoots(t.ExtraWritableRoots)
}

func (t RunCommandTool) resolvedReadableRoots() []string {
	return t.resolvedRoots(t.ExtraReadableRoots)
}

func (t RunCommandTool) resolvedRoots(configured []string) []string {
	roots := make([]string, 0, len(configured))
	for _, root := range configured {
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

// sandboxPolicy builds the OS policy for one command. When a broker is in
// play, network is denied at the OS level regardless of AllowNetwork: that
// denial is what turns the broker from a convention into a boundary, because
// a backend that denies remote traffic while leaving loopback reachable leaves
// the broker as the only way out.
func (t RunCommandTool) sandboxPolicy(brokered bool) sandbox.Policy {
	allowNetwork := t.AllowNetwork
	if brokered {
		allowNetwork = false
	}
	return sandbox.Policy{
		WorkspaceRoot:      t.Workspace,
		ExtraReadableRoots: t.resolvedReadableRoots(),
		ExtraWritableRoots: t.resolvedWritableRoots(),
		AllowNetwork:       allowNetwork,
		ConstrainReads:     !t.AllowReadOutsideWorkspace,
	}
}

// commandEnv builds the child environment, returning nil when the child should
// simply inherit the parent's — the common case, kept free of a snapshot that
// could drift from os.Environ.
//
// Any inherited proxy variable is dropped before the broker's are added. That
// is deliberate rather than relying on os/exec resolving duplicate keys in the
// caller's favor: routing a sandboxed command's traffic is not a detail to
// leave to a library's de-duplication order.
func (t RunCommandTool) commandEnv(broker *egress.Broker, extra ...string) []string {
	if !t.MinimalEnv && broker == nil && len(extra) == 0 {
		return nil
	}
	var env []string
	if t.MinimalEnv {
		env = minimalEnv()
	} else {
		env = os.Environ()
	}
	env = append(env, extra...)
	if broker != nil {
		kept := env[:0]
		for _, entry := range env {
			if !proxyVariable(entry) {
				kept = append(kept, entry)
			}
		}
		env = append(kept, broker.Environ()...)
	}
	return env
}

// proxyVariable reports an environment entry that configures an HTTP proxy.
func proxyVariable(entry string) bool {
	name, _, ok := strings.Cut(entry, "=")
	if !ok {
		return false
	}
	switch strings.ToLower(name) {
	case "http_proxy", "https_proxy", "all_proxy", "no_proxy", "ftp_proxy":
		return true
	}
	return false
}

// networkHint names the setting that actually governs this command's egress.
// Under scoped egress sandbox_allow_network is not the relevant switch, and
// sending a user to it would suggest turning off the narrower control rather
// than naming the host it needs.
func networkHint(brokered bool) string {
	if brokered {
		return "a host-scoped allow rule in permissions.rules for outbound access (permissions.sandbox_egress is \"scoped\")"
	}
	return "permissions.sandbox_allow_network=true for outbound access"
}

// plural picks the singular or plural phrasing for a count.
func plural(n int, singular, many string) string {
	if n == 1 {
		return singular
	}
	return many
}

// minimalEnv keeps only the variables a build needs, so credentials in the
// parent environment never reach agent commands.
func minimalEnv() []string {
	keep := []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "TEMP", "TMP", "TERM", "LANG", "LC_ALL", "LC_CTYPE", "COLUMNS", "LINES", "SYSTEMROOT", "COMSPEC", "PATHEXT", "USERPROFILE", "LOCALAPPDATA", "GOCACHE"}
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
	limit       int
	truncated   bool
	streamLimit int
	streamed    int
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
	if b.onChunk != nil {
		streamLimit := b.streamLimit
		if streamLimit <= 0 {
			streamLimit = b.limit
		}
		remaining := streamLimit - b.streamed
		if remaining > 0 {
			chunk := p
			if len(chunk) > remaining {
				chunk = chunk[:remaining]
			}
			b.onChunk(string(chunk))
			b.streamed += len(chunk)
		}
	}
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
