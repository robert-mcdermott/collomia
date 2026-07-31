package policy

import (
	"path/filepath"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

func TestFirstMatchWins(t *testing.T) {
	rules := []appconfig.Rule{
		{Action: "deny", Tool: "run_command", Command: "curl"},
		{Action: "allow", Tool: "run_command"},
	}
	d := Evaluate(rules, Request{Tool: "run_command", Executables: []string{"curl"}, Inspectable: true})
	if d.Action != "deny" || d.Index != 0 {
		t.Fatalf("decision=%+v", d)
	}
	d = Evaluate(rules, Request{Tool: "run_command", Executables: []string{"go"}, Inspectable: true})
	if d.Action != "allow" || d.Index != 1 {
		t.Fatalf("decision=%+v", d)
	}
}

func TestAllowRequiresEveryExecutableToMatch(t *testing.T) {
	rules := []appconfig.Rule{{Action: "allow", Tool: "run_command", Command: "g*"}}
	d := Evaluate(rules, Request{Tool: "run_command", Executables: []string{"go", "git"}, Inspectable: true})
	if d.Action != "allow" {
		t.Fatalf("decision=%+v", d)
	}
	d = Evaluate(rules, Request{Tool: "run_command", Executables: []string{"go", "curl"}, Inspectable: true})
	if d.Matched() {
		t.Fatalf("pipeline with unmatched executable must not be allowed: %+v", d)
	}
}

func TestDenyFiresOnAnyExecutable(t *testing.T) {
	rules := []appconfig.Rule{{Action: "deny", Command: "rm"}}
	d := Evaluate(rules, Request{Tool: "run_command", Executables: []string{"find", "rm"}, Inspectable: true})
	if d.Action != "deny" {
		t.Fatalf("decision=%+v", d)
	}
}

func TestPathPrefixGlob(t *testing.T) {
	rules := []appconfig.Rule{{Action: "deny", Path: "/repo/secrets/**"}}
	if d := Evaluate(rules, Request{Tool: "read_file", Paths: []string{filepath.FromSlash("/repo/secrets/nested/key")}, Inspectable: true}); d.Action != "deny" {
		t.Fatalf("decision=%+v", d)
	}
	if d := Evaluate(rules, Request{Tool: "read_file", Paths: []string{filepath.FromSlash("/repo/src/main.go")}, Inspectable: true}); d.Matched() {
		t.Fatalf("decision=%+v", d)
	}
}

func TestPathGlobPortableSeparators(t *testing.T) {
	rules := []appconfig.Rule{{Action: "deny", Path: "/repo/*/*.go"}}
	d := Evaluate(rules, Request{Tool: "read_file", Paths: []string{filepath.FromSlash("/repo/src/main.go")}, Inspectable: true})
	if d.Action != "deny" {
		t.Fatalf("decision=%+v", d)
	}
}

func TestServerGlob(t *testing.T) {
	rules := []appconfig.Rule{{Action: "prompt", Tool: "mcp_*", Server: "web*"}}
	if d := Evaluate(rules, Request{Tool: "mcp_websearch_fetch", Server: "websearch", Inspectable: true}); d.Action != "prompt" {
		t.Fatalf("decision=%+v", d)
	}
}

func TestHostRulesMatchDeclaredEndpoints(t *testing.T) {
	rules := []appconfig.Rule{{Action: "deny", Host: "*.evil.com", Reason: "exfiltration"}}
	d := Evaluate(rules, Request{Tool: "run_command", Executables: []string{"curl"}, Hosts: []string{"drop.evil.com"}, Network: true, Inspectable: true})
	if d.Action != "deny" {
		t.Fatalf("decision=%+v", d)
	}
	d = Evaluate(rules, Request{Tool: "run_command", Executables: []string{"curl"}, Hosts: []string{"api.example.com"}, Network: true, Inspectable: true})
	if d.Matched() {
		t.Fatalf("unrelated endpoint must not match: %+v", d)
	}
}

func TestAllowRequiresEveryEndpointToMatch(t *testing.T) {
	rules := []appconfig.Rule{{Action: "allow", Host: "*.example.com"}}
	d := Evaluate(rules, Request{Tool: "run_command", Hosts: []string{"a.example.com", "b.example.com"}, Network: true, Inspectable: true})
	if d.Action != "allow" {
		t.Fatalf("decision=%+v", d)
	}
	d = Evaluate(rules, Request{Tool: "run_command", Hosts: []string{"a.example.com", "elsewhere.net"}, Network: true, Inspectable: true})
	if d.Matched() {
		t.Fatalf("request reaching an unmatched endpoint must not be allowed: %+v", d)
	}
}

// An endpoint the analyzer could not read is the network equivalent of an
// uninspectable command: an allow rule must not vouch for it, while deny and
// prompt rules still fire on the endpoints that were readable.
func TestAllowNeverCoversUndeterminedEndpoints(t *testing.T) {
	allow := []appconfig.Rule{{Action: "allow", Host: "*"}}
	d := Evaluate(allow, Request{Tool: "run_command", Hosts: []string{"api.example.com"}, Network: true, HostsUndetermined: true, Inspectable: true})
	if d.Matched() {
		t.Fatalf("allow must not cover an unreadable endpoint: %+v", d)
	}
	deny := []appconfig.Rule{{Action: "deny", Host: "api.example.com"}}
	d = Evaluate(deny, Request{Tool: "run_command", Hosts: []string{"api.example.com"}, Network: true, HostsUndetermined: true, Inspectable: true})
	if d.Action != "deny" {
		t.Fatalf("deny must still fire on a readable endpoint: %+v", d)
	}
}

// A host rule describes network reach; it must not silently match an action
// that declares no endpoints at all.
func TestHostRulesDoNotMatchNonNetworkActions(t *testing.T) {
	rules := []appconfig.Rule{{Action: "deny", Host: "*"}}
	d := Evaluate(rules, Request{Tool: "write_file", Paths: []string{"/work/a.go"}, Inspectable: true})
	if d.Matched() {
		t.Fatalf("decision=%+v", d)
	}
}

func TestResourcesReportUndeterminedEndpoints(t *testing.T) {
	req := Request{Tool: "run_command", Executables: []string{"git"}, Network: true, HostsUndetermined: true, Inspectable: true}
	found := false
	for _, resource := range req.Resources() {
		if resource == "host:undetermined" {
			found = true
		}
	}
	if !found {
		t.Fatalf("resources=%v", req.Resources())
	}
}

// TestCommandRuleNamingAnOperation covers the matcher's second form. Before it
// existed the whole pattern was matched against argv[0], so a rule written the
// way a user would naturally write it matched nothing at all — and validated
// clean, which is the part that made it dangerous rather than merely useless.
func TestCommandRuleNamingAnOperation(t *testing.T) {
	rules := []appconfig.Rule{
		{Action: "deny", Tool: "run_command", Command: "npm publish"},
		{Action: "allow", Tool: "run_command", Command: "npm"},
	}
	publish := Request{Tool: "run_command", Executables: []string{"npm"}, Operations: []string{"npm publish"}, Inspectable: true}
	if d := Evaluate(rules, publish); d.Action != "deny" || d.Index != 0 {
		t.Fatalf("npm publish decision = %+v, want deny from rules[0]", d)
	}
	install := Request{Tool: "run_command", Executables: []string{"npm"}, Operations: []string{"npm install"}, Inspectable: true}
	if d := Evaluate(rules, install); d.Action != "allow" || d.Index != 1 {
		t.Fatalf("npm install decision = %+v, want allow from rules[1]", d)
	}
}

// An operation pattern must not fall back to matching the executable. If it
// did, "npm publish" would deny every npm invocation.
func TestOperationRuleDoesNotMatchAnExecutableOnly(t *testing.T) {
	rules := []appconfig.Rule{{Action: "deny", Command: "npm publish"}}
	req := Request{Tool: "run_command", Executables: []string{"npm"}, Inspectable: true}
	if d := Evaluate(rules, req); d.Matched() {
		t.Fatalf("operation rule matched a command with no operations: %+v", d)
	}
}

// Allow rules must still cover the whole request. A pipeline that publishes
// and installs is not covered by an allow rule naming only the install.
func TestAllowRequiresEveryOperationToMatch(t *testing.T) {
	rules := []appconfig.Rule{{Action: "allow", Command: "npm install"}}
	req := Request{Tool: "run_command", Executables: []string{"npm", "npm"}, Operations: []string{"npm install", "npm publish"}, Inspectable: true}
	if d := Evaluate(rules, req); d.Matched() {
		t.Fatalf("allow rule covered an unmatched operation: %+v", d)
	}
}

func TestGlobsWorkOnOperations(t *testing.T) {
	rules := []appconfig.Rule{{Action: "prompt", Command: "gh pr *"}}
	req := Request{Tool: "run_command", Executables: []string{"gh"}, Operations: []string{"gh pr create"}, Inspectable: true}
	if d := Evaluate(rules, req); d.Action != "prompt" {
		t.Fatalf("glob over an operation did not match: %+v", d)
	}
}

func TestNamesOperationDistinguishesTheTwoForms(t *testing.T) {
	for _, pattern := range []string{"npm publish", "gh pr create", "ssh build-host"} {
		if !NamesOperation(pattern) {
			t.Errorf("NamesOperation(%q) = false, want true", pattern)
		}
	}
	for _, pattern := range []string{"npm", "g*", "", "  "} {
		if NamesOperation(pattern) {
			t.Errorf("NamesOperation(%q) = true, want false", pattern)
		}
	}
}

// Operations belong in the audit ledger's resource list: reconstructing an
// incident from "exec:npm" cannot distinguish an install from a release.
func TestResourcesIncludeOperations(t *testing.T) {
	req := Request{Executables: []string{"npm"}, Operations: []string{"npm publish"}}
	var found bool
	for _, resource := range req.Resources() {
		if resource == "op:npm publish" {
			found = true
		}
	}
	if !found {
		t.Fatalf("resources = %v, want an op: entry", req.Resources())
	}
}
