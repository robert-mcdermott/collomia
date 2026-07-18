package policy

import (
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
	if d := Evaluate(rules, Request{Tool: "read_file", Paths: []string{"/repo/secrets/nested/key"}, Inspectable: true}); d.Action != "deny" {
		t.Fatalf("decision=%+v", d)
	}
	if d := Evaluate(rules, Request{Tool: "read_file", Paths: []string{"/repo/src/main.go"}, Inspectable: true}); d.Matched() {
		t.Fatalf("decision=%+v", d)
	}
}

func TestServerGlob(t *testing.T) {
	rules := []appconfig.Rule{{Action: "prompt", Tool: "mcp_*", Server: "web*"}}
	if d := Evaluate(rules, Request{Tool: "mcp_websearch_fetch", Server: "websearch", Inspectable: true}); d.Action != "prompt" {
		t.Fatalf("decision=%+v", d)
	}
}
