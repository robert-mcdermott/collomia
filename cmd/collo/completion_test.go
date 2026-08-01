package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestCompletionScriptsCoverSupportedShells(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell", "pwsh"} {
		script, err := completionScript(shell)
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		for _, want := range []string{"collo", "completion", "schema", "ephemeral", "replay", "check", "support", "include-logs", "rewind"} {
			if !strings.Contains(script, want) {
				t.Errorf("%s completion missing %q", shell, want)
			}
		}
	}
	if _, err := completionScript("csh"); err == nil {
		t.Fatal("unsupported shell should fail")
	}
}

// Every shell must offer the same vocabulary. Before the table existed, zsh,
// fish, and PowerShell each knew about `sessions` and no other subcommand,
// which is not a shell difference — it is three incomplete copies.
//
// The assertion is the per-shell fragment that actually binds a subcommand to
// its command, not a bare substring search. Half these words — show, list,
// add, set, test — appear in several arms, so a search for "show" would report
// `audit show` as present because `sessions show` exists. That is precisely how
// the documentation guards came to pass against deliberately broken
// documentation; see docs/TESTING.md.
func TestEveryShellOffersEverySubcommand(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		script, err := completionScript(shell)
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		for _, command := range commandsWithSubcommands() {
			for _, fragment := range boundSubcommandFragments(shell, command) {
				if !strings.Contains(script, fragment) {
					t.Errorf("%s completion is missing %q, so it does not offer the subcommands of %q",
						shell, fragment, command.name)
				}
			}
		}
	}
}

// boundSubcommandFragments renders the text each shell must contain to bind
// this command's subcommands to it.
func boundSubcommandFragments(shell string, command completionCommand) []string {
	joined := strings.Join(command.subcommands, " ")
	switch shell {
	case "bash":
		return []string{command.name + `) COMPREPLY=( $(compgen -W "` + joined + `"`}
	case "zsh":
		return []string{command.name + `) _describe '` + command.name + ` command' '(` + joined + `)'`}
	case "fish":
		var fragments []string
		for _, sub := range command.subcommands {
			fragments = append(fragments,
				"__fish_seen_subcommand_from "+command.name+"' -a '"+sub+"'")
		}
		return fragments
	case "powershell":
		// PowerShell's completer is a flat word list with no command binding
		// available, so the honest assertion is that every word is present as
		// a quoted entry rather than that it is bound to anything.
		var fragments []string
		for _, sub := range command.subcommands {
			fragments = append(fragments, "'"+sub+"'")
		}
		return fragments
	}
	return nil
}

// The pin. A completion table is a copy of the dispatch table, and a copy that
// nothing compares is a copy that goes stale — this one had `collo schema
// config` missing for a month while the tests stayed green.
func TestCompletionCommandsMatchDispatch(t *testing.T) {
	dispatched := dispatchedCommands(t)
	offered := map[string]bool{}
	for _, command := range completionCommands {
		offered[command.name] = true
	}
	for name := range dispatched {
		if !offered[name] {
			t.Errorf("main.go dispatches %q but completion never offers it", name)
		}
	}
	for name := range offered {
		if !dispatched[name] {
			t.Errorf("completion offers %q but main.go dispatches no such command", name)
		}
	}
}

// The same pin one level down. An offered subcommand nothing dispatches is an
// inert suggestion; a dispatched subcommand nothing offers is the `schema
// config` defect.
func TestCompletionSubcommandsMatchDispatch(t *testing.T) {
	// Only the switch-shaped dispatchers can be read this way. `policy` and
	// `support` compare a single argument with `!=` and are covered by the
	// usage-string assertions in their own tests.
	for _, tc := range []struct{ command, function, file string }{
		{"config", "runConfigCommand", "commands.go"},
		{"sessions", "runSessionsCommand", "commands.go"},
		{"skills", "runSkillsCommand", "skills.go"},
		{"mcp", "runMCPCommand", "mcp.go"},
		{"auth", "runAuthCommand", "auth.go"},
		{"audit", "runAuditCommand", "audit.go"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			clauses := caseClausesIn(t, tc.file, tc.function)
			if len(clauses) == 0 {
				t.Fatalf("no dispatch switch found in %s (%s); this test cannot pin what it cannot read", tc.function, tc.file)
			}
			offered := map[string]bool{}
			for _, command := range completionCommands {
				if command.name != tc.command {
					continue
				}
				for _, sub := range command.subcommands {
					offered[sub] = true
				}
			}
			if len(offered) == 0 {
				t.Fatalf("completion offers no subcommand for %q", tc.command)
			}
			// No inert offer: everything suggested must be dispatched.
			dispatched := map[string]bool{}
			for _, clause := range clauses {
				for _, label := range clause {
					dispatched[label] = true
				}
			}
			for sub := range offered {
				if !dispatched[sub] {
					t.Errorf("completion offers `%s %s` but %s dispatches no such subcommand", tc.command, sub, tc.function)
				}
			}
			// No missing verb: every dispatch clause must be reachable by
			// completion through at least one of its labels. One label is
			// enough, because a clause like `case "rm", "remove", "delete"`
			// is three spellings of one verb and offering all three is noise.
			for _, clause := range clauses {
				if !anyOffered(clause, offered) {
					t.Errorf("%s dispatches %v but completion offers none of them for %q", tc.function, clause, tc.command)
				}
			}
		})
	}
}

// The schema contracts are read from the variable the command dispatches on
// rather than restated, so this only has to confirm the wiring survived.
func TestSchemaContractsAreNotRestated(t *testing.T) {
	var offered []string
	for _, command := range completionCommands {
		if command.name == "schema" {
			offered = command.subcommands
		}
	}
	if len(offered) != len(schemaContracts) {
		t.Fatalf("schema completion offers %v, contracts are %v", offered, schemaContracts)
	}
	for i := range offered {
		if offered[i] != schemaContracts[i] {
			t.Fatalf("schema completion offers %v, contracts are %v", offered, schemaContracts)
		}
	}
	script, err := completionScript("bash")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "schema) COMPREPLY=( $(compgen -W \"events config\"") {
		t.Errorf("bash completion does not offer both published contracts:\n%s", script)
	}
}

func anyOffered(labels []string, offered map[string]bool) bool {
	for _, label := range labels {
		if offered[label] {
			return true
		}
	}
	return false
}

var (
	caseLabelRE   = regexp.MustCompile(`^\s*case\s+((?:"[a-z0-9-]+"\s*,\s*)*"[a-z0-9-]+")\s*:`)
	quotedLabelRE = regexp.MustCompile(`"([a-z0-9-]+)"`)
)

// dispatchedCommands reads the top-level command names main.go acts on: the
// `switch opts.command` arms plus the verbs handled before it.
func dispatchedCommands(t *testing.T) map[string]bool {
	t.Helper()
	source := readSource(t, "main.go")
	commands := map[string]bool{}
	for _, clause := range caseClausesIn(t, "main.go", "run") {
		for _, label := range clause {
			commands[label] = true
		}
	}
	// The verbs dispatched before the switch are matched on their own, because
	// they are compared with == rather than being switch arms. A name that
	// stops being handled here fails the offered-but-not-dispatched direction.
	for _, name := range []string{"tui", "run", "init", "review", "verify", "replay", "version"} {
		if !strings.Contains(source, `"`+name+`"`) {
			t.Errorf("main.go no longer mentions the %q command; the completion pin needs updating", name)
			continue
		}
		commands[name] = true
	}
	return commands
}

// caseClausesIn returns the case labels of every switch inside one function,
// each clause as the list of labels sharing it.
func caseClausesIn(t *testing.T, file, function string) [][]string {
	t.Helper()
	source := readSource(t, file)
	lines := strings.Split(source, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "func "+function+"(") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("function %s not found in %s", function, file)
	}
	var clauses [][]string
	for _, line := range lines[start+1:] {
		if strings.HasPrefix(line, "}") {
			break
		}
		match := caseLabelRE.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		var labels []string
		for _, label := range quotedLabelRE.FindAllStringSubmatch(match[1], -1) {
			labels = append(labels, label[1])
		}
		if len(labels) > 0 {
			sort.Strings(labels)
			clauses = append(clauses, labels)
		}
	}
	return clauses
}

func readSource(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(".", file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	return string(data)
}
