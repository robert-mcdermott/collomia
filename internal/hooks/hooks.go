// Package hooks runs trusted, user-configured lifecycle commands at defined
// points in a session. Hooks observe structured JSON on stdin; the two
// gating events (user_prompt, tool_start) may additionally block the action
// by exiting with code 2 or printing {"decision":"block"}. Hooks can only
// tighten behavior — a hook cannot approve anything the permission engine
// would deny, and hook failures are bounded and reported, never fatal.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

const (
	defaultTimeout = 10 * time.Second
	maxHookOutput  = 8 * 1024
	// blockExitCode is the conventional "block this action" exit status.
	blockExitCode = 2
)

// Payload is the JSON a hook receives on stdin. Event-specific fields are
// empty when not applicable.
type Payload struct {
	Event     string `json:"event"`
	Workspace string `json:"workspace"`
	// Subject is what Matcher tests against: the tool name for tool events,
	// the event name otherwise.
	Subject string `json:"subject"`
	Tool    string `json:"tool,omitempty"`
	Summary string `json:"summary,omitempty"`
	// Args carries the tool call's raw JSON arguments for tool events.
	Args    json.RawMessage `json:"args,omitempty"`
	Prompt  string          `json:"prompt,omitempty"`
	Paths   []string        `json:"paths,omitempty"`
	Error   string          `json:"error,omitempty"`
	Allowed *bool           `json:"allowed,omitempty"`
	// Detail carries small event-specific extras (decision source, replaced
	// message counts, sub-agent names).
	Detail map[string]any `json:"detail,omitempty"`
}

// Note is one observation from running hooks: a failure, a timeout, or a
// hook's own printed message worth surfacing.
type Note struct {
	Event   string
	Command string
	Text    string
}

func (n Note) String() string { return fmt.Sprintf("hook %s (%s): %s", n.Event, n.Command, n.Text) }

// Runner executes the hooks configured for each event. A nil Runner is valid
// and does nothing, so call sites never need guards.
type Runner struct {
	workspace string
	hooks     map[string][]appconfig.Hook
	// OnNote receives observations as they happen (warnings surface in the
	// TUI/debug log). Optional.
	OnNote func(Note)
}

// NewRunner builds a runner from the configuration map. It returns nil when
// no hooks are configured; a nil Runner is a no-op at every call site.
func NewRunner(workspace string, configured map[string][]appconfig.Hook, onNote func(Note)) *Runner {
	if len(configured) == 0 {
		return nil
	}
	return &Runner{workspace: workspace, hooks: configured, OnNote: onNote}
}

// Fire runs every hook for an observational event. Failures become notes.
func (r *Runner) Fire(ctx context.Context, payload Payload) {
	if r == nil {
		return
	}
	for _, hook := range r.matching(payload) {
		if _, err := r.runOne(ctx, hook, payload); err != nil {
			r.note(payload.Event, hook.Command, err.Error())
		}
	}
}

// Gate runs every hook for a gating event; the first block wins and its
// reason is returned as the error. Non-block failures become notes (hooks
// fail open — the permission engine remains the enforcement boundary, and a
// broken observer must not brick the session).
func (r *Runner) Gate(ctx context.Context, payload Payload) error {
	if r == nil {
		return nil
	}
	for _, hook := range r.matching(payload) {
		verdict, err := r.runOne(ctx, hook, payload)
		if err != nil {
			r.note(payload.Event, hook.Command, err.Error())
			continue
		}
		if verdict.blocked {
			reason := verdict.reason
			if reason == "" {
				reason = "blocked by hook " + hook.Command
			}
			return fmt.Errorf("%s", reason)
		}
	}
	return nil
}

func (r *Runner) matching(payload Payload) []appconfig.Hook {
	var out []appconfig.Hook
	for _, hook := range r.hooks[payload.Event] {
		if hook.Matcher != "" {
			re, err := regexp.Compile(hook.Matcher)
			if err != nil || !re.MatchString(payload.Subject) {
				continue
			}
		}
		out = append(out, hook)
	}
	return out
}

type verdict struct {
	blocked bool
	reason  string
}

func (r *Runner) runOne(ctx context.Context, hook appconfig.Hook, payload Payload) (verdict, error) {
	timeout := defaultTimeout
	if hook.TimeoutSeconds > 0 {
		timeout = time.Duration(hook.TimeoutSeconds) * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	input, err := json.Marshal(payload)
	if err != nil {
		return verdict{}, err
	}
	cmd := exec.CommandContext(runCtx, hook.Command, hook.Args...)
	// A killed hook can leave grandchildren holding the output pipes; don't
	// let them stall the session past the deadline.
	cmd.WaitDelay = time.Second
	cmd.Dir = r.workspace
	cmd.Env = append(os.Environ(), "COLLO_HOOK_EVENT="+payload.Event, "COLLO_WORKSPACE="+r.workspace)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = newCapped(&stdout)
	cmd.Stderr = newCapped(&stderr)
	runErr := cmd.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		return verdict{}, fmt.Errorf("timed out after %s", timeout)
	}
	out := strings.TrimSpace(stdout.String())
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok && exitErr.ExitCode() == blockExitCode {
			reason := out
			if reason == "" {
				reason = strings.TrimSpace(stderr.String())
			}
			return verdict{blocked: true, reason: reason}, nil
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = runErr.Error()
		}
		return verdict{}, fmt.Errorf("%s", message)
	}
	// A successful hook may still return a structured decision.
	var decision struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if out != "" && json.Unmarshal([]byte(out), &decision) == nil && strings.EqualFold(decision.Decision, "block") {
		return verdict{blocked: true, reason: decision.Reason}, nil
	}
	return verdict{}, nil
}

func (r *Runner) note(eventName, command, text string) {
	if r.OnNote != nil {
		r.OnNote(Note{Event: eventName, Command: command, Text: text})
	}
}

// cappedWriter bounds hook output so a runaway hook cannot consume memory.
type cappedWriter struct {
	dst *bytes.Buffer
}

func newCapped(dst *bytes.Buffer) *cappedWriter { return &cappedWriter{dst: dst} }

func (w *cappedWriter) Write(p []byte) (int, error) {
	remaining := maxHookOutput - w.dst.Len()
	if remaining > 0 {
		if len(p) > remaining {
			w.dst.Write(p[:remaining])
		} else {
			w.dst.Write(p)
		}
	}
	return len(p), nil
}
