package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every flag the parser accepts must be discoverable in `collo --help`,
// either in the Flags block or in a command's usage line. A flag that only
// exists in the parser is a flag users find by accident.
func TestHelpDocumentsEveryParsedFlag(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	parsed := map[string]bool{}
	for _, pattern := range []string{`arg == "(--[a-z-]+)"`, `case "(--[a-z-]+)"`} {
		for _, m := range regexp.MustCompile(pattern).FindAllStringSubmatch(string(source), -1) {
			if m[1] != "--" {
				parsed[m[1]] = true
			}
		}
	}
	if len(parsed) < 20 {
		t.Fatalf("flag extraction found only %d flags; the parser shape changed and this guard needs updating", len(parsed))
	}
	help := helpText
	var missing []string
	for flag := range parsed {
		if !strings.Contains(help, flag) {
			missing = append(missing, flag)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("flags accepted by the parser but absent from --help: %v", missing)
	}
}

// The reverse: help must not advertise a flag the parser will reject.
func TestHelpDoesNotAdvertiseUnparsedFlags(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, m := range regexp.MustCompile(`(?m)^\s*(--[a-z-]+)`).FindAllStringSubmatch(helpText, -1) {
		flag := m[1]
		if !strings.Contains(text, `"`+flag+`"`) {
			t.Errorf("--help advertises %s but the parser never matches it", flag)
		}
	}
}
