package permission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/robert-mcdermott/collomia/internal/audit"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/policy"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

var ErrDenied = errors.New("permission denied")

type Request struct {
	Tool   string
	Action tools.Action
	// Reason explains why the prompt is being shown (matched prompt rule,
	// uninspectable command analysis, or the autonomy mode).
	Reason string
}

type Decision struct {
	Allow  bool
	Always bool
}

// Grant reports how an authorization was decided, for events and audit.
type Grant struct {
	Source string // rule, mode, session, interactive, implicit-read, denied-tool, analysis
	Rule   string
}

type Approver func(context.Context, Request) (Decision, error)

type Manager struct {
	mu           sync.RWMutex
	mode         string
	allowed      map[string]bool
	denied       map[string]bool
	allowOutside bool
	rules        []appconfig.Rule
	reviewer     string
	approver     Approver
	ledger       *audit.Ledger
}

func New(cfg appconfig.Permissions, approver Approver) *Manager {
	allowed := map[string]bool{}
	for _, name := range cfg.AllowedTools {
		allowed[name] = true
	}
	denied := map[string]bool{}
	for _, name := range cfg.DeniedTools {
		denied[name] = true
	}
	return &Manager{mode: cfg.Mode, allowed: allowed, denied: denied, allowOutside: cfg.AllowOutsideWorkspace, rules: cfg.Rules, reviewer: cfg.ReviewerCommand, approver: approver}
}

// SetLedger attaches the persistent audit ledger. Nil is safe.
func (m *Manager) SetLedger(ledger *audit.Ledger) {
	m.mu.Lock()
	m.ledger = ledger
	m.mu.Unlock()
}

func (m *Manager) Mode() string { m.mu.RLock(); defer m.mu.RUnlock(); return m.mode }
func (m *Manager) SetMode(mode string) error {
	if !slices.Contains([]string{"ask", "workspace", "autopilot"}, mode) {
		return fmt.Errorf("unknown autonomy mode %q", mode)
	}
	m.mu.Lock()
	m.mode = mode
	m.mu.Unlock()
	return nil
}

// Evaluate runs the decision pipeline without prompting or recording; used
// by `collo policy check` to explain what would happen.
func (m *Manager) Evaluate(tool string, action tools.Action) (Grant, string) {
	grant, outcome, _ := m.decide(tool, action)
	return grant, outcome
}

// decide returns the non-interactive decision: outcome is "allow", "deny",
// or "prompt", with the reason a prompt would carry.
func (m *Manager) decide(tool string, action tools.Action) (Grant, string, string) {
	m.mu.RLock()
	mode := m.mode
	sessionAllowed := m.allowed[tool]
	denied := m.denied[tool]
	rules := m.rules
	allowOutside := m.allowOutside
	m.mu.RUnlock()

	if denied {
		return Grant{Source: "denied-tool"}, "deny", ""
	}
	request := policy.Request{Tool: tool, Paths: action.Paths, Executables: action.Executables, Hosts: action.Hosts, Server: action.Server, Inspectable: !action.Uninspectable}
	decision := policy.Evaluate(rules, request)
	if decision.Matched() {
		grant := Grant{Source: "rule", Rule: decision.Describe()}
		switch decision.Action {
		case "deny":
			return grant, "deny", ""
		case "allow":
			return grant, "allow", ""
		case "prompt":
			return grant, "prompt", "policy rule requires approval: " + decision.Describe()
		}
	}
	if action.Risk == tools.RiskRead && !action.Outside {
		return Grant{Source: "implicit-read"}, "allow", ""
	}
	// A command whose full effect cannot be determined statically always
	// needs a human, regardless of autonomy mode.
	if action.Uninspectable && action.Risk == tools.RiskExecute {
		return Grant{Source: "analysis"}, "prompt", "command could not be fully analyzed: " + strings.Join(action.AnalysisReasons, "; ")
	}
	if sessionAllowed {
		return Grant{Source: "session"}, "allow", ""
	}
	if mode == "autopilot" && action.Risk != tools.RiskExternal {
		if !action.Outside || allowOutside {
			return Grant{Source: "mode"}, "allow", ""
		}
	}
	if mode == "workspace" && !action.Outside && action.Risk == tools.RiskWrite {
		return Grant{Source: "mode"}, "allow", ""
	}
	return Grant{Source: "interactive"}, "prompt", ""
}

func (m *Manager) Authorize(ctx context.Context, tool string, action tools.Action) (Grant, error) {
	grant, outcome, reason := m.decide(tool, action)
	request := policy.Request{Tool: tool, Paths: action.Paths, Executables: action.Executables, Hosts: action.Hosts, Server: action.Server, Inspectable: !action.Uninspectable}
	record := func(allowed bool) {
		decision := "deny"
		if allowed {
			decision = "allow"
		}
		m.mu.RLock()
		ledger := m.ledger
		m.mu.RUnlock()
		ledger.Append(audit.Entry{Kind: "decision", Tool: tool, Summary: action.Summary, Risk: string(action.Risk), Resources: request.Resources(), Decision: decision, Source: grant.Source, Rule: grant.Rule})
	}
	switch outcome {
	case "deny":
		record(false)
		if grant.Rule != "" {
			return grant, fmt.Errorf("%w: %s (%s)", ErrDenied, action.Summary, grant.Rule)
		}
		return grant, fmt.Errorf("%w: tool %s is disabled", ErrDenied, tool)
	case "allow":
		// An optional external reviewer can veto auto-approvals of non-read
		// actions; a veto escalates to the human rather than silently
		// allowing. The reviewer can only tighten decisions, never widen.
		if action.Risk != tools.RiskRead {
			if vetoReason := m.reviewerVeto(ctx, tool, action, request); vetoReason != "" {
				outcome = "prompt"
				reason = "reviewer requires approval: " + vetoReason
				grant = Grant{Source: "reviewer", Rule: grant.Rule}
				break
			}
		}
		record(true)
		return grant, nil
	}

	m.mu.RLock()
	approver := m.approver
	m.mu.RUnlock()
	if approver == nil {
		record(false)
		return grant, fmt.Errorf("%w: %s requires interactive approval", ErrDenied, action.Summary)
	}
	decision, err := approver(ctx, Request{Tool: tool, Action: action, Reason: reason})
	if err != nil {
		record(false)
		return grant, err
	}
	grant = Grant{Source: "interactive", Rule: grant.Rule}
	if !decision.Allow {
		record(false)
		return grant, fmt.Errorf("%w: %s", ErrDenied, action.Summary)
	}
	// "Always" never sticks for commands the analyzer could not read; each
	// uninspectable command must be approved on its own.
	if decision.Always && !action.Uninspectable {
		m.mu.Lock()
		m.allowed[tool] = true
		m.mu.Unlock()
	}
	record(true)
	return grant, nil
}

// reviewerVeto runs the configured reviewer command with the request as
// JSON on stdin. A non-empty return is the veto reason. Reviewer failures
// never widen access: an unrunnable reviewer is reported as a veto so the
// human stays in the loop.
func (m *Manager) reviewerVeto(ctx context.Context, tool string, action tools.Action, request policy.Request) string {
	m.mu.RLock()
	reviewer := m.reviewer
	m.mu.RUnlock()
	if reviewer == "" {
		return ""
	}
	payload, err := json.Marshal(map[string]any{
		"tool": tool, "summary": action.Summary, "risk": string(action.Risk),
		"resources": request.Resources(), "uninspectable": action.Uninspectable,
	})
	if err != nil {
		return "reviewer request could not be encoded"
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, reviewerArgv(reviewer)[0], reviewerArgv(reviewer)[1:]...)
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Sprintf("reviewer command failed (%v)", err)
	}
	var verdict struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if json.Unmarshal(bytes.TrimSpace(out), &verdict) != nil {
		return "reviewer returned an unreadable verdict"
	}
	if strings.EqualFold(verdict.Decision, "deny") {
		if verdict.Reason != "" {
			return verdict.Reason
		}
		return "denied by reviewer"
	}
	return ""
}

func reviewerArgv(command string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd.exe", "/d", "/s", "/c", command}
	}
	return []string{"/bin/sh", "-c", command}
}

// RecordOutcome persists what actually happened after an authorized tool
// executed, completing the audit trail.
func (m *Manager) RecordOutcome(tool string, action tools.Action, execErr error) {
	m.mu.RLock()
	ledger := m.ledger
	m.mu.RUnlock()
	outcome := "ok"
	if execErr != nil {
		outcome = "error: " + execErr.Error()
	}
	ledger.Append(audit.Entry{Kind: "outcome", Tool: tool, Summary: action.Summary, Risk: string(action.Risk), Outcome: outcome})
}
