package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/sandbox"
	"github.com/robert-mcdermott/collomia/internal/shell"
)

// procOutputCap bounds how much output each background process retains.
const procOutputCap = 64 * 1024

// Process is one agent-started background job: a dev server, a watcher, a
// long test run. It runs in its own process group, its output is retained
// in a bounded buffer, and it never outlives the session.
type Process struct {
	ID      int
	Command string
	Started time.Time

	mu       sync.Mutex
	output   *limitedBuffer
	cancel   context.CancelFunc
	doneCh   chan struct{}
	done     bool
	exitErr  error
	finished time.Time
	// sandboxWarning is returned when auto mode could apply only part of the
	// requested policy or had to run without OS enforcement.
	sandboxWarning string
}

func (p *Process) status() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.done {
		return "running"
	}
	if p.exitErr != nil {
		return "exited: " + p.exitErr.Error()
	}
	return "exited: ok"
}

func (p *Process) running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.done
}

// ProcessManager owns every background process started by the agent this
// session. StopAll must run at shutdown so nothing outlives Collomia.
type ProcessManager struct {
	mu    sync.Mutex
	procs map[int]*Process
	next  int
}

func NewProcessManager() *ProcessManager {
	return &ProcessManager{procs: map[int]*Process{}}
}

// ProcessInfo is a snapshot for UI display.
type ProcessInfo struct {
	ID      int
	Command string
	Status  string
	Running bool
	Started time.Time
}

// Snapshot lists every tracked process, oldest first.
func (m *ProcessManager) Snapshot() []ProcessInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]int, 0, len(m.procs))
	for id := range m.procs {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]ProcessInfo, 0, len(ids))
	for _, id := range ids {
		p := m.procs[id]
		out = append(out, ProcessInfo{ID: p.ID, Command: p.Command, Status: p.status(), Running: p.running(), Started: p.Started})
	}
	return out
}

// Running counts processes still alive.
func (m *ProcessManager) Running() int {
	n := 0
	for _, p := range m.Snapshot() {
		if p.Running {
			n++
		}
	}
	return n
}

func (m *ProcessManager) get(id int) (*Process, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.procs[id]
	if !ok {
		return nil, fmt.Errorf("no background process with id %d", id)
	}
	return p, nil
}

// StopAll terminates every still-running background process. Safe to call
// more than once.
func (m *ProcessManager) StopAll() {
	m.mu.Lock()
	processes := make([]*Process, 0, len(m.procs))
	for _, process := range m.procs {
		processes = append(processes, process)
	}
	m.mu.Unlock()
	for _, process := range processes {
		process.mu.Lock()
		cancel := process.cancel
		running := !process.done
		process.mu.Unlock()
		if running && cancel != nil {
			cancel()
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for _, process := range processes {
		process.mu.Lock()
		done := process.done
		doneCh := process.doneCh
		process.mu.Unlock()
		if done || doneCh == nil {
			continue
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		timer := time.NewTimer(remaining)
		select {
		case <-doneCh:
			timer.Stop()
		case <-timer.C:
			return
		}
	}
}

// start launches a command detached from the tool call that created it.
// The command config (denied patterns, sandbox, environment) mirrors
// run_command exactly.
func (m *ProcessManager) start(runner *RunCommandTool, command string) (*Process, error) {
	argv := shellArgv(command)
	sandboxWarning := ""
	if runner.SandboxMode == sandbox.ModeAuto || runner.SandboxMode == sandbox.ModeRequire {
		prepared, err := sandbox.Prepare(runner.Backend, runner.SandboxMode, argv, runner.sandboxPolicy())
		if err != nil {
			return nil, err
		}
		argv = prepared.Argv
		sandboxWarning = prepared.Degraded
	}
	// The process lifetime is owned by the manager, not the tool call's
	// context: cancelling the turn must not kill a deliberately-started
	// background server.
	procCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(procCtx, argv[0], argv[1:]...)
	cmd.Dir = runner.Workspace
	if runner.MinimalEnv {
		cmd.Env = minimalEnv()
	}
	setProcessGroup(cmd)
	p := &Process{Command: command, Started: time.Now(), output: &limitedBuffer{limit: procOutputCap}, cancel: cancel, doneCh: make(chan struct{}), sandboxWarning: sandboxWarning}
	cmd.Stdout = syncWriter{p}
	cmd.Stderr = syncWriter{p}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	m.mu.Lock()
	m.next++
	p.ID = m.next
	m.procs[p.ID] = p
	m.mu.Unlock()
	go func() {
		err := cmd.Wait()
		cancel()
		p.mu.Lock()
		p.done = true
		p.exitErr = err
		p.finished = time.Now()
		p.mu.Unlock()
		close(p.doneCh)
	}()
	return p, nil
}

// syncWriter serializes concurrent stdout/stderr writes into the process's
// bounded buffer.
type syncWriter struct{ p *Process }

func (w syncWriter) Write(b []byte) (int, error) {
	w.p.mu.Lock()
	defer w.p.mu.Unlock()
	return w.p.output.Write(b)
}

// StartProcessTool launches a background process. It shares the
// RunCommandTool's safety configuration: denied patterns, conservative
// shell analysis, sandbox wrapping, and minimal environment.
type StartProcessTool struct {
	Manager *ProcessManager
	Runner  *RunCommandTool
}

func (t StartProcessTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "start_process", Description: "Start a long-running command (dev server, watcher, long test run) in the background and return its process id immediately. Use process_output to read its output, list_processes to see status, and stop_process to stop it. Background processes are killed when the session ends. For commands that finish quickly, use run_command instead.", InputSchema: schema(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`)}
}

func (t StartProcessTool) Assess(raw json.RawMessage) (Action, error) {
	var a struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return Action{}, err
	}
	if strings.TrimSpace(a.Command) == "" {
		return Action{}, errors.New("command must not be empty")
	}
	analysis := shell.AnalyzeInWorkspace(a.Command, t.Runner.Workspace)
	return ActionFromAnalysis("start background process: "+a.Command, a.Command, analysis), nil
}

func (t StartProcessTool) Execute(_ context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if err := t.Runner.checkCommandSafety(a.Command); err != nil {
		return "", err
	}
	p, err := t.Manager.start(t.Runner, a.Command)
	if err != nil {
		return "", err
	}
	result := fmt.Sprintf("started background process %d: %s\nUse process_output with id %d to read its output; stop_process to stop it.", p.ID, a.Command, p.ID)
	if p.sandboxWarning != "" {
		result += "\nSandbox warning: " + p.sandboxWarning
	}
	return result, nil
}

// ListProcessesTool reports every background process and its status.
type ListProcessesTool struct{ Manager *ProcessManager }

func (t ListProcessesTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "list_processes", Description: "List background processes started this session with their ids, commands, status, and uptime.", InputSchema: schema(`{"type":"object","properties":{},"additionalProperties":false}`)}
}
func (t ListProcessesTool) Assess(json.RawMessage) (Action, error) {
	return Action{Risk: RiskRead, Summary: "list background processes"}, nil
}
func (t ListProcessesTool) Execute(context.Context, json.RawMessage) (string, error) {
	infos := t.Manager.Snapshot()
	if len(infos) == 0 {
		return "No background processes have been started this session.", nil
	}
	var b strings.Builder
	for _, info := range infos {
		fmt.Fprintf(&b, "[%d] %s — %s (started %s ago)\n", info.ID, info.Command, info.Status, time.Since(info.Started).Round(time.Second))
	}
	return b.String(), nil
}

// ProcessOutputTool returns a background process's recent output.
type ProcessOutputTool struct{ Manager *ProcessManager }

func (t ProcessOutputTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "process_output", Description: "Read the retained output of a background process (most recent 64 KiB). Optionally limit to the last N lines with tail_lines.", InputSchema: schema(`{"type":"object","properties":{"id":{"type":"integer","minimum":1},"tail_lines":{"type":"integer","minimum":1,"maximum":2000}},"required":["id"],"additionalProperties":false}`)}
}
func (t ProcessOutputTool) Assess(raw json.RawMessage) (Action, error) {
	return Action{Risk: RiskRead, Summary: "read background process output"}, nil
}
func (t ProcessOutputTool) Execute(_ context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		ID        int `json:"id"`
		TailLines int `json:"tail_lines"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	p, err := t.Manager.get(a.ID)
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	out := p.output.String()
	p.mu.Unlock()
	if a.TailLines > 0 {
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(lines) > a.TailLines {
			lines = lines[len(lines)-a.TailLines:]
		}
		out = strings.Join(lines, "\n")
	}
	status := p.status()
	if strings.TrimSpace(out) == "" {
		return fmt.Sprintf("[%d] %s — no output yet", a.ID, status), nil
	}
	return fmt.Sprintf("[%d] %s\n%s", a.ID, status, out), nil
}

// StopProcessTool terminates one background process (and its whole process
// group). It can only affect processes this session started, so it carries
// read-level risk.
type StopProcessTool struct{ Manager *ProcessManager }

func (t StopProcessTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "stop_process", Description: "Stop a background process started this session. Kills the whole process group so descendants do not linger.", InputSchema: schema(`{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`)}
}
func (t StopProcessTool) Assess(json.RawMessage) (Action, error) {
	return Action{Risk: RiskRead, Summary: "stop a background process this session started"}, nil
}
func (t StopProcessTool) Execute(_ context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	p, err := t.Manager.get(a.ID)
	if err != nil {
		return "", err
	}
	if !p.running() {
		return fmt.Sprintf("process %d already exited (%s)", a.ID, p.status()), nil
	}
	p.mu.Lock()
	cancel := p.cancel
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// Wait on the command's completion signal rather than polling. The bound
	// keeps a broken platform kill primitive from hanging the TUI.
	p.mu.Lock()
	doneCh := p.doneCh
	p.mu.Unlock()
	if doneCh != nil {
		timer := time.NewTimer(3 * time.Second)
		select {
		case <-doneCh:
			timer.Stop()
		case <-timer.C:
			return "", fmt.Errorf("timed out waiting for process %d to stop", a.ID)
		}
	}
	return fmt.Sprintf("stopped process %d (%s)", a.ID, p.Command), nil
}
