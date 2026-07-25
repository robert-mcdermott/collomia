package permission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robert-mcdermott/collomia/internal/audit"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/policy"
	"github.com/robert-mcdermott/collomia/internal/secrets"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

var ErrDenied = errors.New("permission denied")

type Request struct {
	Tool   string
	Action tools.Action
	// Reason explains why the prompt is being shown (matched prompt rule,
	// uninspectable command analysis, a posture gate, or the autonomy mode).
	Reason string
	// Capabilities describe the action's reach one dimension at a time so the
	// prompt can show it and grant it a dimension at a time.
	Capabilities []Capability
	// PostureGated marks a prompt produced by the scoped-network or
	// command-allowlist posture. A tool-wide "always" would not satisfy that
	// posture, so the prompt offers the scoped grants instead.
	PostureGated bool
}

// Capability kinds. They name what an action reaches, not which tool asked.
const (
	CapabilityFilesystem = "filesystem"
	CapabilityExecutable = "executable"
	CapabilityNetwork    = "network"
	CapabilityServer     = "server"
)

// Capability is one dimension of an action's reach. Values are the normalized
// resources in that dimension; Unknown marks a dimension the analyzer could
// not fully determine, which is why a session grant is never offered for it.
type Capability struct {
	Kind    string
	Values  []string
	Unknown bool
	Reasons []string
	// Grantable is true when approving this action can also record a session
	// grant for this dimension's values.
	Grantable bool
	// Granted is true when a session grant already covers every value.
	Granted bool
}

type Decision struct {
	Allow  bool
	Always bool
	// Grants lists capability kinds to remember for the rest of the session,
	// scoped to this action's values rather than to the whole tool.
	Grants []string
	// Content, when set, replaces the write's proposed content before
	// execution — used when the user approved only some hunks of a
	// write_file diff rather than the whole file.
	Content *string
}

// Grant reports how an authorization was decided, for events and audit.
type Grant struct {
	Source string // rule, mode, session, session-scope, posture, interactive, implicit-read, denied-tool, analysis
	Rule   string
	// ContentOverride carries a selectively-approved write, when the user
	// picked hunks instead of accepting the whole proposed change.
	ContentOverride *string
}

type Approver func(context.Context, Request) (Decision, error)

type Manager struct {
	mu          sync.RWMutex
	mode        string
	baseMode    string
	profileMode string
	allowed     map[string]bool
	denied      map[string]bool
	// network and commands are the posture settings. They never widen a
	// decision: each can only turn an automatic approval into a prompt.
	network  string
	commands string
	// protectCredentials decides what an action reaching a well-known
	// credential store gets: off, prompt, or deny. Unlike the postures above
	// it can deny, because refusing to hand a private key to a model is a
	// defensible default in a way that refusing an ordinary command is not.
	protectCredentials string
	// allowedCommands and allowedHosts are session grants scoped to one
	// executable or one endpoint, the narrow counterpart of "always allow
	// this tool".
	allowedCommands       map[string]bool
	allowedHosts          map[string]bool
	profileDenied         map[string]bool
	profileDeniedCommands []*regexp.Regexp
	allowOutside          bool
	rules                 []appconfig.Rule
	// restrictions are an independent, deny-or-prompt-only policy layer used
	// by delegated agents. Evaluating it after the base policy and taking the
	// stricter outcome prevents either layer's ordered rules from masking a
	// denial in the other.
	restrictions []appconfig.Rule
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
	network := strings.ToLower(strings.TrimSpace(cfg.Network))
	if network == "" {
		network = "open"
	}
	commands := strings.ToLower(strings.TrimSpace(cfg.Commands))
	if commands == "" {
		commands = "open"
	}
	protectCredentials := strings.ToLower(strings.TrimSpace(cfg.ProtectCredentials))
	if protectCredentials == "" {
		protectCredentials = appconfig.ProtectCredentialsPrompt
	}
	return &Manager{
		mode: cfg.Mode, baseMode: cfg.Mode, allowed: allowed, denied: denied,
		network: network, commands: commands, protectCredentials: protectCredentials,
		allowedCommands: map[string]bool{}, allowedHosts: map[string]bool{},
		profileDenied: map[string]bool{}, allowOutside: cfg.AllowOutsideWorkspace,
		rules: cfg.Rules, reviewer: cfg.ReviewerCommand, approver: approver,
	}
}

// SetLedger attaches the persistent audit ledger. Nil is safe.
func (m *Manager) SetLedger(ledger *audit.Ledger) {
	m.mu.Lock()
	m.ledger = ledger
	m.mu.Unlock()
}

// SetRestrictions installs a second policy layer that may only prompt or
// deny. Invalid allow rules are ignored defensively even though configuration
// validation rejects them. The effective result is the stricter of the base
// policy and this layer.
func (m *Manager) SetRestrictions(rules []appconfig.Rule) {
	restricted := make([]appconfig.Rule, 0, len(rules))
	for _, rule := range rules {
		if rule.Action == "prompt" || rule.Action == "deny" {
			restricted = append(restricted, rule)
		}
	}
	m.mu.Lock()
	m.restrictions = restricted
	m.mu.Unlock()
}

func (m *Manager) Mode() string { m.mu.RLock(); defer m.mu.RUnlock(); return m.mode }
func (m *Manager) SetMode(mode string) error {
	if !slices.Contains([]string{"ask", "workspace", "autopilot"}, mode) {
		return fmt.Errorf("unknown autonomy mode %q", mode)
	}
	m.mu.Lock()
	m.baseMode = mode
	m.mode = restrictedMode(mode, m.profileMode)
	m.mu.Unlock()
	return nil
}

// SetProfile installs the additive permission restrictions for the active
// primary profile. It never changes the underlying user/project policy and
// cannot widen autonomy.
func (m *Manager) SetProfile(profile appconfig.AgentPermissions) error {
	denied := make(map[string]bool, len(profile.DeniedTools))
	for _, name := range profile.DeniedTools {
		denied[name] = true
	}
	patterns := make([]*regexp.Regexp, 0, len(profile.DeniedCommands))
	for _, pattern := range profile.DeniedCommands {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("agent denied command %q: %w", pattern, err)
		}
		patterns = append(patterns, re)
	}
	restricted := make([]appconfig.Rule, 0, len(profile.Rules))
	for _, rule := range profile.Rules {
		if rule.Action == "prompt" || rule.Action == "deny" {
			restricted = append(restricted, rule)
		}
	}
	m.mu.Lock()
	m.profileMode = profile.Mode
	m.mode = restrictedMode(m.baseMode, profile.Mode)
	m.profileDenied = denied
	m.profileDeniedCommands = patterns
	m.restrictions = restricted
	m.mu.Unlock()
	return nil
}

func restrictedMode(base, profile string) string {
	if profile == "" {
		return base
	}
	rank := map[string]int{"ask": 0, "workspace": 1, "autopilot": 2}
	if rank[profile] < rank[base] {
		return profile
	}
	return base
}

// Evaluate runs the decision pipeline without prompting or recording; used
// by `collo policy check` to explain what would happen.
func (m *Manager) Evaluate(tool string, action tools.Action) (Grant, string) {
	grant, outcome, _ := m.decide(tool, action)
	return grant, outcome
}

// decide returns the stricter result of the ordinary parent policy and an
// optional delegated-agent restriction layer.
func (m *Manager) decide(tool string, action tools.Action) (Grant, string, string) {
	grant, outcome, reason := m.decideBase(tool, action)
	if outcome == "deny" {
		return grant, outcome, reason
	}
	m.mu.RLock()
	restrictions := append([]appconfig.Rule(nil), m.restrictions...)
	m.mu.RUnlock()
	if len(restrictions) == 0 {
		return grant, outcome, reason
	}
	request := policy.Request{Tool: tool, Paths: action.Paths, Executables: action.Executables, Hosts: action.Hosts, Network: action.Network, HostsUndetermined: action.HostsUndetermined, Server: action.Server, Inspectable: !action.Uninspectable}
	restriction := policy.Evaluate(restrictions, request)
	if !restriction.Matched() {
		return grant, outcome, reason
	}
	restrictedGrant := Grant{Source: "rule", Rule: "agent restriction: " + restriction.Describe()}
	switch restriction.Action {
	case "deny":
		return restrictedGrant, "deny", ""
	case "prompt":
		if outcome == "allow" {
			return restrictedGrant, "prompt", "agent profile requires approval: " + restriction.Describe()
		}
	}
	return grant, outcome, reason
}

func (m *Manager) decideBase(tool string, action tools.Action) (Grant, string, string) {
	m.mu.RLock()
	mode := m.mode
	sessionAllowed := m.allowed[tool]
	denied := m.denied[tool]
	profileDenied := m.profileDenied[tool]
	profilePatterns := append([]*regexp.Regexp(nil), m.profileDeniedCommands...)
	rules := m.rules
	allowOutside := m.allowOutside
	networkPosture := m.network
	commandPosture := m.commands
	protectCredentials := m.protectCredentials
	scopes := m.scopeSnapshotLocked()
	m.mu.RUnlock()
	credentials := credentialTargets(action, protectCredentials)

	if denied || profileDenied {
		return Grant{Source: "denied-tool"}, "deny", ""
	}
	for _, pattern := range profilePatterns {
		if action.Command != "" && pattern.MatchString(action.Command) {
			return Grant{Source: "agent-profile", Rule: "command matches additive agent-profile denial " + pattern.String()}, "deny", ""
		}
	}
	// Catastrophic outcomes are not permissions. They are refused before
	// configurable rules, autonomy modes, and session grants can widen access.
	if len(action.HardDenyReasons) > 0 {
		reason := strings.Join(action.HardDenyReasons, "; ")
		return Grant{Source: "safety", Rule: reason}, "deny", ""
	}
	request := policy.Request{Tool: tool, Paths: action.Paths, Executables: action.Executables, Hosts: action.Hosts, Network: action.Network, HostsUndetermined: action.HostsUndetermined, Server: action.Server, Inspectable: !action.Uninspectable}
	decision := policy.Evaluate(rules, request)
	if decision.Matched() {
		grant := Grant{Source: "rule", Rule: decision.Describe()}
		switch decision.Action {
		case "deny":
			return grant, "deny", ""
		case "allow":
			// One-time confirmations and opaque commands may not be widened
			// into automatic approval by an allow rule.
			//
			// A credential store is different: a rule that names its path is a
			// deliberate written-down exception and is honored, while a
			// blanket rule covering a tool or a whole directory is not allowed
			// to sweep a private key in as a side effect.
			if len(action.ConfirmReasons) == 0 && !action.Uninspectable && (len(credentials) == 0 || namesSpecificPath(decision.Rule.Path)) {
				return grant, "allow", ""
			}
		case "prompt":
			return grant, "prompt", "policy rule requires approval: " + decision.Describe()
		}
	}
	// Credential stores are gated before the implicit-read fast path: a .env
	// or a key checked into the workspace is an in-workspace read, and would
	// otherwise be approved without anyone seeing it.
	if len(credentials) > 0 {
		reason := "action reaches " + strings.Join(credentials, "; ")
		if protectCredentials == appconfig.ProtectCredentialsDeny {
			return Grant{Source: "credentials", Rule: reason}, "deny", ""
		}
		return Grant{Source: "credentials", Rule: reason}, "prompt", reason
	}
	if action.Risk == tools.RiskRead && !action.Outside {
		return Grant{Source: "implicit-read"}, "allow", ""
	}
	// A command whose full effect cannot be determined statically always
	// needs a human, regardless of autonomy mode.
	if action.Uninspectable && action.Risk == tools.RiskExecute {
		return Grant{Source: "analysis"}, "prompt", "command could not be fully analyzed: " + strings.Join(action.AnalysisReasons, "; ")
	}
	if len(action.ConfirmReasons) > 0 && action.Risk == tools.RiskExecute {
		reason := strings.Join(action.ConfirmReasons, "; ")
		return Grant{Source: "safety", Rule: reason}, "prompt", "one-time confirmation required: " + reason
	}
	// A grant scoped to this action's executables and endpoints is the narrow
	// counterpart of "always allow this tool", and satisfies the postures
	// below on its own.
	if scopes.cover(action) {
		return Grant{Source: "session-scope", Rule: scopes.describe(action)}, "allow", ""
	}
	// Postures can only turn an automatic approval into a prompt. They are
	// evaluated before the tool-wide session grant and the autonomy mode so
	// neither can hand out the access the posture exists to withhold.
	if reason := posturePrompt(action, networkPosture, commandPosture); reason != "" {
		return Grant{Source: "posture"}, "prompt", reason
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

// credentialTargets reports the well-known credential stores this action
// reaches, or nil when the protection is off.
//
// Targets carried on the action come from shell analysis, which sees paths no
// structured field holds. Targets are additionally derived here from the
// action's declared paths, so a tool added later is covered by virtue of
// declaring what it touches rather than by remembering to classify it.
func credentialTargets(action tools.Action, setting string) []string {
	if setting == appconfig.ProtectCredentialsOff {
		return nil
	}
	targets := append([]string(nil), action.CredentialTargets...)
	seen := make(map[string]bool, len(targets))
	for _, target := range targets {
		seen[target] = true
	}
	for _, path := range action.Paths {
		label := secrets.Classify(path)
		if label == "" {
			continue
		}
		if target := label + ": " + path; !seen[target] {
			seen[target] = true
			targets = append(targets, target)
		}
	}
	return targets
}

func (m *Manager) credentialSetting() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.protectCredentials
}

// namesSpecificPath reports whether a rule's path pattern identifies a
// location deliberately, rather than matching everything. A rule that names
// where a credential lives is an exception its author wrote on purpose; a bare
// "**" is a blanket grant that should not quietly include one.
func namesSpecificPath(pattern string) bool {
	trimmed := strings.TrimSpace(pattern)
	if trimmed == "" {
		return false
	}
	return strings.Trim(trimmed, `*?/\`) != ""
}

// scopeGrants is a point-in-time copy of the session's per-capability grants.
type scopeGrants struct {
	commands map[string]bool
	hosts    map[string]bool
}

func (m *Manager) scopeSnapshotLocked() scopeGrants {
	snapshot := scopeGrants{commands: make(map[string]bool, len(m.allowedCommands)), hosts: make(map[string]bool, len(m.allowedHosts))}
	for name := range m.allowedCommands {
		snapshot.commands[name] = true
	}
	for host := range m.allowedHosts {
		snapshot.hosts[host] = true
	}
	return snapshot
}

// cover reports whether the session's scoped grants account for every
// capability this action reaches. A dimension the analyzer could not read is
// never covered: a grant applies to the values the user saw, not to whatever
// the action turns out to touch.
func (g scopeGrants) cover(action tools.Action) bool {
	if action.Uninspectable || len(action.ConfirmReasons) > 0 {
		return false
	}
	if len(action.Executables) == 0 && !action.Network {
		return false
	}
	if !g.coversExecutables(action) || !g.coversNetwork(action) {
		return false
	}
	return true
}

func (g scopeGrants) coversExecutables(action tools.Action) bool {
	for _, executable := range action.Executables {
		if !g.commands[executable] {
			return false
		}
	}
	return true
}

func (g scopeGrants) coversNetwork(action tools.Action) bool {
	if !action.Network {
		return true
	}
	if action.HostsUndetermined || len(action.Hosts) == 0 {
		return false
	}
	for _, host := range action.Hosts {
		if !g.hosts[host] {
			return false
		}
	}
	return true
}

func (g scopeGrants) describe(action tools.Action) string {
	var parts []string
	if len(action.Executables) > 0 {
		parts = append(parts, "session grant for command "+strings.Join(action.Executables, ", "))
	}
	if action.Network && len(action.Hosts) > 0 {
		parts = append(parts, "session grant for host "+strings.Join(action.Hosts, ", "))
	}
	return strings.Join(parts, "; ")
}

// posturePrompt returns the reason an otherwise automatic approval must be
// escalated to a person under the configured postures, or "" when neither
// posture applies. It never allows or denies.
func posturePrompt(action tools.Action, network, commands string) string {
	if commands == "allowlist" && action.Risk == tools.RiskExecute && len(action.Executables) > 0 {
		return "command allowlist requires an explicit grant for: " + strings.Join(action.Executables, ", ")
	}
	if network == "scoped" && action.Network {
		if action.HostsUndetermined {
			reason := "scoped network posture requires an explicit grant, and this action's endpoints could not be read"
			if len(action.HostReasons) > 0 {
				reason += ": " + strings.Join(action.HostReasons, "; ")
			}
			return reason
		}
		if len(action.Hosts) == 0 {
			return "scoped network posture requires an explicit grant, and this action named no endpoint"
		}
		return "scoped network posture requires an explicit grant for: " + strings.Join(action.Hosts, ", ")
	}
	return ""
}

// Capabilities describes an action's reach one dimension at a time, marking
// which dimensions a session grant could cover. A dimension the analyzer
// could not fully read is reported as unknown and is never grantable.
func (m *Manager) Capabilities(action tools.Action) []Capability {
	m.mu.RLock()
	scopes := m.scopeSnapshotLocked()
	protectCredentials := m.protectCredentials
	m.mu.RUnlock()
	// An action reaching a credential store is approved once or not at all, so
	// no dimension of it is offered as a reusable grant.
	oneTime := action.Uninspectable || len(action.ConfirmReasons) > 0 || len(credentialTargets(action, protectCredentials)) > 0
	var out []Capability
	if len(action.Paths) > 0 {
		out = append(out, Capability{Kind: CapabilityFilesystem, Values: action.Paths})
	}
	if len(action.Executables) > 0 || action.Uninspectable {
		capability := Capability{
			Kind:      CapabilityExecutable,
			Values:    action.Executables,
			Unknown:   action.Uninspectable,
			Reasons:   action.AnalysisReasons,
			Grantable: !oneTime && len(action.Executables) > 0,
		}
		capability.Granted = capability.Grantable && scopes.coversExecutables(action)
		out = append(out, capability)
	}
	if action.Network {
		capability := Capability{
			Kind:      CapabilityNetwork,
			Values:    action.Hosts,
			Unknown:   action.HostsUndetermined,
			Reasons:   action.HostReasons,
			Grantable: !oneTime && !action.HostsUndetermined && len(action.Hosts) > 0,
		}
		capability.Granted = capability.Grantable && scopes.coversNetwork(action)
		out = append(out, capability)
	}
	if action.Server != "" {
		out = append(out, Capability{Kind: CapabilityServer, Values: []string{action.Server}})
	}
	return out
}

// SessionGrants reports the per-capability grants accumulated this process,
// sorted, so the user can see what they have handed out without reading the
// audit ledger.
func (m *Manager) SessionGrants() (commands, hosts []string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for name := range m.allowedCommands {
		commands = append(commands, name)
	}
	for host := range m.allowedHosts {
		hosts = append(hosts, host)
	}
	sort.Strings(commands)
	sort.Strings(hosts)
	return commands, hosts
}

// GrantableKinds lists the dimensions an approver may hand a session grant
// for. Anything omitted here — an unreadable dimension, a one-time
// confirmation — cannot be remembered, so a caller cannot grant access the
// user was never shown.
func GrantableKinds(capabilities []Capability) []string {
	var kinds []string
	for _, capability := range capabilities {
		if capability.Grantable && !capability.Granted {
			kinds = append(kinds, capability.Kind)
		}
	}
	return kinds
}

// applyGrants records the session grants the user chose alongside an
// approval. Only fully-readable dimensions can be granted, so a grant can
// never cover an endpoint or executable the user was not shown.
func (m *Manager) applyGrants(action tools.Action, kinds []string) {
	if len(kinds) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, kind := range kinds {
		switch kind {
		case CapabilityExecutable:
			if action.Uninspectable || len(action.ConfirmReasons) > 0 {
				continue
			}
			for _, executable := range action.Executables {
				m.allowedCommands[executable] = true
			}
		case CapabilityNetwork:
			if action.HostsUndetermined || len(action.ConfirmReasons) > 0 {
				continue
			}
			for _, host := range action.Hosts {
				m.allowedHosts[host] = true
			}
		}
	}
}

func (m *Manager) Authorize(ctx context.Context, tool string, action tools.Action) (Grant, error) {
	grant, outcome, reason := m.decide(tool, action)
	request := policy.Request{Tool: tool, Paths: action.Paths, Executables: action.Executables, Hosts: action.Hosts, Network: action.Network, HostsUndetermined: action.HostsUndetermined, Server: action.Server, Inspectable: !action.Uninspectable}
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
	decision, err := approver(ctx, Request{
		Tool: tool, Action: action, Reason: reason,
		Capabilities: m.Capabilities(action), PostureGated: grant.Source == "posture",
	})
	if err != nil {
		record(false)
		return grant, err
	}
	grant = Grant{Source: "interactive", Rule: grant.Rule, ContentOverride: decision.Content}
	if !decision.Allow {
		record(false)
		return grant, fmt.Errorf("%w: %s", ErrDenied, action.Summary)
	}
	// "Always" never sticks for commands the analyzer could not read, that
	// carry a mandatory confirmation, or that reach a credential store; each
	// must be approved on its own. The same restriction applies to the
	// narrower per-capability grants.
	if decision.Always && !action.Uninspectable && len(action.ConfirmReasons) == 0 && len(credentialTargets(action, m.credentialSetting())) == 0 {
		m.mu.Lock()
		m.allowed[tool] = true
		m.mu.Unlock()
	}
	m.applyGrants(action, decision.Grants)
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
