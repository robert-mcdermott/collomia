// Package policy evaluates ordered, scoped approval rules against
// normalized tool requests. Rules refine the autonomy mode: they can allow
// specific commands or paths without a prompt, force prompts, or deny
// outright. Denials are absolute; allowances never apply to requests the
// analyzer could not inspect.
package policy

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

// Request is a normalized description of what a tool wants to do.
type Request struct {
	Tool        string
	Paths       []string
	Executables []string
	// Operations name what each recognized invocation does, as
	// "<executable> <verb…>". A command rule whose pattern contains a space
	// is matched against these instead of against Executables, because an
	// executable name cannot distinguish `npm install` from `npm publish`.
	Operations []string
	Hosts      []string
	Server     string
	// Network marks a request that reaches the network at all.
	Network bool
	// HostsUndetermined is true when the request reaches an endpoint it could
	// not name. Like Inspectable, it blocks host-scoped allow rules: an
	// endpoint nobody could read must not be vouched for.
	HostsUndetermined bool
	// Inspectable is false when the request's resources could not be fully
	// determined (e.g. a shell command with substitutions).
	Inspectable bool
}

// Resources renders the request's normalized resources for display/audit.
func (r Request) Resources() []string {
	var out []string
	for _, p := range r.Paths {
		out = append(out, "path:"+p)
	}
	for _, e := range r.Executables {
		out = append(out, "exec:"+e)
	}
	for _, o := range r.Operations {
		out = append(out, "op:"+o)
	}
	for _, h := range r.Hosts {
		out = append(out, "host:"+h)
	}
	if r.HostsUndetermined {
		out = append(out, "host:undetermined")
	}
	if r.Server != "" {
		out = append(out, "server:"+r.Server)
	}
	if !r.Inspectable {
		out = append(out, "uninspectable")
	}
	return out
}

type Decision struct {
	// Action is allow, prompt, deny, or "" when no rule matched.
	Action string
	Index  int
	Rule   appconfig.Rule
}

func (d Decision) Matched() bool { return d.Action != "" }

// Describe renders the matched rule for prompts and the audit ledger.
func (d Decision) Describe() string {
	if !d.Matched() {
		return ""
	}
	parts := []string{fmt.Sprintf("rules[%d] %s", d.Index, d.Rule.Action)}
	for label, value := range map[string]string{"tool": d.Rule.Tool, "path": d.Rule.Path, "command": d.Rule.Command, "host": d.Rule.Host, "server": d.Rule.Server} {
		if value != "" {
			parts = append(parts, label+"="+value)
		}
	}
	if d.Rule.Reason != "" {
		parts = append(parts, "("+d.Rule.Reason+")")
	}
	return strings.Join(parts, " ")
}

// Evaluate returns the first matching rule's decision. Rules with resource
// constraints the request cannot prove (uninspectable commands) are skipped
// for allow but honored for deny/prompt when any known resource matches.
func Evaluate(rules []appconfig.Rule, req Request) Decision {
	for i, rule := range rules {
		if matches(rule, req) {
			return Decision{Action: rule.Action, Index: i, Rule: rule}
		}
	}
	return Decision{}
}

func matches(rule appconfig.Rule, req Request) bool {
	if rule.Tool != "" && !glob(rule.Tool, req.Tool) {
		return false
	}
	if rule.Server != "" && !glob(rule.Server, req.Server) {
		return false
	}
	requireAll := rule.Action == "allow"
	if rule.Command != "" {
		// An allow rule cannot vouch for a command we cannot fully read.
		if requireAll && !req.Inspectable {
			return false
		}
		// A pattern containing a space names an operation, not an executable.
		// Before this existed the whole pattern was matched against argv[0],
		// so `{"command": "npm publish"}` matched nothing at all and validated
		// clean — a rule that read as protection and was inert.
		values := req.Executables
		if NamesOperation(rule.Command) {
			values = req.Operations
		}
		if !matchSet(rule.Command, values, requireAll, glob) {
			return false
		}
	}
	if rule.Path != "" {
		if !matchSet(rule.Path, req.Paths, requireAll, pathGlob) {
			return false
		}
	}
	if rule.Host != "" {
		// An allow rule cannot vouch for an endpoint the analyzer could not
		// read, exactly as it cannot vouch for an uninspectable command.
		if requireAll && req.HostsUndetermined {
			return false
		}
		if !matchSet(rule.Host, req.Hosts, requireAll, glob) {
			return false
		}
	}
	return true
}

// matchSet applies a pattern to a value set. Allow rules need every value to
// match (the whole request must be covered); deny/prompt rules fire when any
// value matches.
func matchSet(pattern string, values []string, requireAll bool, match func(string, string) bool) bool {
	if len(values) == 0 {
		return false
	}
	matched := 0
	for _, value := range values {
		if match(pattern, value) {
			matched++
		}
	}
	if requireAll {
		return matched == len(values)
	}
	return matched > 0
}

// NamesOperation reports whether a command-rule pattern names an operation
// (`npm publish`) rather than an executable (`npm`). The permission layer asks
// the same question when deciding whether a rule is a deliberate publication
// exception, so both sides read one definition rather than two spellings of it.
func NamesOperation(pattern string) bool { return strings.Contains(strings.TrimSpace(pattern), " ") }

func glob(pattern, value string) bool {
	ok, err := filepath.Match(pattern, value)
	return err == nil && ok
}

// HostMatches reports whether a host-scoped rule pattern covers a normalized
// hostname. The egress broker builds its allowlist from the same rules this
// package evaluates, so it matches through this function rather than
// reimplementing the glob: a destination the policy layer would allow and a
// destination the broker will dial must never disagree.
func HostMatches(pattern, host string) bool { return glob(pattern, host) }

// pathGlob matches resolved paths after normalizing native separators to
// slashes. A pattern ending in "/**" matches the directory and everything
// beneath it; otherwise path.Match semantics apply to the whole path.
func pathGlob(pattern, value string) bool {
	pattern = filepath.ToSlash(pattern)
	value = filepath.ToSlash(value)
	if strings.HasSuffix(pattern, "/**") {
		root := strings.TrimSuffix(pattern, "/**")
		return value == root || strings.HasPrefix(value, root+"/")
	}
	ok, err := path.Match(pattern, value)
	return err == nil && ok
}
