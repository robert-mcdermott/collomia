// Package shell statically analyzes shell commands so the permission engine
// can reason about what would run. The analysis is deliberately
// conservative: anything it cannot prove — substitutions, variable commands,
// nested interpreters, eval — marks the command uninspectable, and
// uninspectable commands always require interactive approval. Analysis is a
// policy aid, never the security boundary.
package shell

import (
	"path/filepath"
	"strings"
)

type Analysis struct {
	Raw string
	// workspace is the original command root. It is deliberately kept
	// separate from a segment's current directory after `cd`.
	workspace string
	// Executables lists the argv[0] of every simple command found, plus the
	// real targets behind transparent wrappers like env or timeout.
	Executables []string
	// Hosts lists the network endpoints the command text names, normalized to
	// lowercase hostnames without scheme, credentials, port, or path.
	Hosts []string
	// NetworkCommand is true when a recognized network-bearing command was
	// found. Commands outside that set contribute nothing: any program can
	// open a socket, which is what OS-level confinement is for.
	NetworkCommand bool
	// UndeterminedHosts is true when a network-bearing command has an endpoint
	// the command text does not name — a named Git remote, a package
	// registry from configuration, a URL read from a file. Allow rules scoped
	// to a host must not cover such a request.
	UndeterminedHosts bool
	// HostReasons explains every undetermined endpoint.
	HostReasons []string
	// Inspectable is false when the command's effects cannot be determined
	// statically. Uninspectable commands must be interactively approved.
	Inspectable bool
	// Reasons explains every inspectability failure.
	Reasons []string
	// HardDenyReasons identifies catastrophic outcomes that cannot be approved
	// or overridden by configuration.
	HardDenyReasons []string
	// ConfirmReasons identifies destructive but potentially legitimate actions
	// that require a fresh interactive approval, even in autopilot mode.
	ConfirmReasons []string
	// CredentialTargets names the well-known credential stores this command's
	// arguments reach, each as "label: argument". Populating this is not by
	// itself a decision: what happens to an action that reaches one is
	// configurable, so the analysis reports and the permission layer rules.
	CredentialTargets []string
	// Operations names the action each recognized subcommand-driven invocation
	// takes, as "<executable> <verb…>" — `npm publish`, `git push`,
	// `gh pr create`. Executables alone cannot express the difference between
	// installing a dependency and publishing a package, so a rule that needs
	// to say yes to one and no to the other matches against these.
	Operations []string
	// PublicationTargets names the operations that put something outside this
	// machine, each as "label: operation". Like CredentialTargets this is a
	// report rather than a decision; permissions.publication rules on it.
	PublicationTargets []string
}

// wrappers run another command given as their arguments; the wrapped command
// matters as much as the wrapper. skipArgs counts arguments consumed by the
// wrapper before the real command (e.g. `timeout 30 make`).
var wrappers = map[string]int{
	"env": 0, "command": 0, "nohup": 0, "nice": 0, "time": 0, "stdbuf": 1,
	"timeout": 1, "xargs": 0, "sudo": 0, "doas": 0,
}

// interpreters take code as an argument; the payload cannot be analyzed.
var interpreters = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "ksh": true, "dash": true, "fish": true,
	"python": true, "python3": true, "perl": true, "ruby": true, "node": true,
	"deno": true, "osascript": true, "pwsh": true, "powershell": true, "cmd": true, "cmd.exe": true,
}

// opaque commands change what runs in ways static analysis cannot follow.
var opaque = map[string]bool{"eval": true, "exec": true, "source": true, ".": true, "trap": true, "alias": true}

func Analyze(command string) Analysis {
	return analyzeAt(command, "", "")
}

// AnalyzeInWorkspace analyzes command with workspace as its initial working
// directory, allowing catastrophic relative targets to be resolved safely.
func AnalyzeInWorkspace(command, workspace string) Analysis {
	workspace = canonicalWorkspace(workspace)
	return analyzeAt(command, workspace, workspace)
}

func analyzeAt(command, workspace, cwd string) Analysis {
	a := Analysis{Raw: command, workspace: workspace, Inspectable: true}
	classifyRawWindows(command, &a)
	segments, ok := split(command, &a)
	if !ok {
		return a
	}
	previousFollow := ""
	for _, segment := range segments {
		if previousFollow == "|" {
			classifyPipedProgram(segment.tokens, &a)
		}
		previousFollow = segment.follow
		a.analyzeSegment(segment.tokens)
		nextCWD := classifySegment(segment.tokens, cwd, &a)
		if nextCWD != cwd {
			switch segment.follow {
			case "&&":
				// A following command runs only if cd succeeded.
				cwd = nextCWD
			case ";", "newline":
				// The shell continues even when cd fails, so relative effects
				// cannot safely be resolved to either directory. Destructive
				// classifiers will require confirmation when they see that
				// unresolved state; harmless commands remain routine.
				cwd = ""
				// `||`, pipelines, and background cd do not change the current
				// shell directory on the path that executes the next command.
			}
		}
		if segment.follow == ")" || segment.follow == "}" {
			// A grouped/subshell directory change cannot leak to the outer
			// command. Reset conservatively to the protected workspace root.
			cwd = workspace
		}
	}
	classifyRedirections(command, cwd, &a)
	if len(a.Executables) == 0 && a.Inspectable && strings.TrimSpace(command) != "" {
		// Nothing recognizable (e.g. bare assignments); treat as opaque.
		a.flag("no executable could be identified")
	}
	return a
}

func (a *Analysis) hardDeny(reason string) {
	for _, existing := range a.HardDenyReasons {
		if existing == reason {
			return
		}
	}
	a.HardDenyReasons = append(a.HardDenyReasons, reason)
}

func (a *Analysis) confirm(reason string) {
	for _, existing := range a.ConfirmReasons {
		if existing == reason {
			return
		}
	}
	a.ConfirmReasons = append(a.ConfirmReasons, reason)
}

func (a *Analysis) credential(target string) {
	for _, existing := range a.CredentialTargets {
		if existing == target {
			return
		}
	}
	a.CredentialTargets = append(a.CredentialTargets, target)
}

func (a *Analysis) operation(operation string) {
	for _, existing := range a.Operations {
		if existing == operation {
			return
		}
	}
	a.Operations = append(a.Operations, operation)
}

func (a *Analysis) publication(target string) {
	for _, existing := range a.PublicationTargets {
		if existing == target {
			return
		}
	}
	a.PublicationTargets = append(a.PublicationTargets, target)
}

func (a *Analysis) flag(reason string) {
	a.Inspectable = false
	for _, existing := range a.Reasons {
		if existing == reason {
			return
		}
	}
	a.Reasons = append(a.Reasons, reason)
}

// split tokenizes the command into simple-command token lists, honoring
// quotes and splitting on ;, &, |, &&, ||, newlines, and subshell
// parentheses. It flags constructs that defeat static analysis.
type parsedSegment struct {
	tokens []string
	follow string
}

func split(command string, a *Analysis) ([]parsedSegment, bool) {
	var segments []parsedSegment
	var current []string
	var token strings.Builder
	tokenStarted := false
	flush := func() {
		if tokenStarted {
			current = append(current, token.String())
			token.Reset()
			tokenStarted = false
		}
	}
	endSegment := func(follow string) {
		flush()
		if len(current) > 0 {
			segments = append(segments, parsedSegment{tokens: current, follow: follow})
			current = nil
		}
	}
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == '\'':
			tokenStarted = true
			i++
			for i < len(runes) && runes[i] != '\'' {
				token.WriteRune(runes[i])
				i++
			}
			if i >= len(runes) {
				a.flag("unterminated single quote")
				return nil, false
			}
		case c == '"':
			tokenStarted = true
			i++
			for i < len(runes) && runes[i] != '"' {
				if runes[i] == '\\' && i+1 < len(runes) {
					i++
					token.WriteRune(runes[i])
					i++
					continue
				}
				if runes[i] == '`' || (runes[i] == '$' && i+1 < len(runes) && runes[i+1] == '(') {
					a.flag("command substitution")
				}
				if runes[i] == '$' {
					token.WriteRune('$')
					i++
					continue
				}
				token.WriteRune(runes[i])
				i++
			}
			if i >= len(runes) {
				a.flag("unterminated double quote")
				return nil, false
			}
		case c == '\\':
			tokenStarted = true
			if i+1 < len(runes) {
				i++
				token.WriteRune(runes[i])
			}
		case c == '`':
			a.flag("command substitution")
			tokenStarted = true
		case c == '$' && i+1 < len(runes) && runes[i+1] == '(':
			a.flag("command substitution")
			tokenStarted = true
		case c == '<' && i+1 < len(runes) && runes[i+1] == '(':
			a.flag("process substitution")
			tokenStarted = true
		case c == '$':
			tokenStarted = true
			token.WriteRune(c)
		case c == ';' || c == '\n':
			follow := ";"
			if c == '\n' {
				follow = "newline"
			}
			endSegment(follow)
		case c == '&' || c == '|':
			// &&, ||, |, & and redirection-adjacent forms all end the
			// current simple command.
			follow := string(c)
			if i+1 < len(runes) && (runes[i+1] == '&' || runes[i+1] == '|') {
				follow += string(runes[i+1])
				i++
			}
			endSegment(follow)
		case c == '(' || c == ')' || c == '{' || c == '}':
			endSegment(string(c))
		case c == '>' || c == '<':
			// Redirection: drop the operator and its target word.
			flush()
			i++
			for i < len(runes) && (runes[i] == '>' || runes[i] == '&' || runes[i] == ' ') {
				i++
			}
			for i < len(runes) && runes[i] != ' ' && runes[i] != ';' && runes[i] != '|' && runes[i] != '&' && runes[i] != '\n' {
				i++
			}
			i--
		case c == ' ' || c == '\t':
			flush()
		default:
			tokenStarted = true
			token.WriteRune(c)
		}
	}
	endSegment("")
	return segments, true
}

func (a *Analysis) analyzeSegment(tokens []string) {
	i := 0
	// Skip leading environment assignments (FOO=bar cmd …).
	for i < len(tokens) && isAssignment(tokens[i]) {
		i++
	}
	for {
		if i >= len(tokens) {
			return
		}
		word := tokens[i]
		if word == "" {
			i++
			continue
		}
		if strings.HasPrefix(word, "$") {
			a.flag("variable used as command")
			return
		}
		if strings.ContainsAny(word, "*?[") {
			a.flag("glob used as command")
			return
		}
		name := strings.ToLower(filepath.Base(word))
		a.Executables = append(a.Executables, name)
		if opaque[name] {
			a.flag(name + " defeats static analysis")
			return
		}
		if interpreters[name] && hasCodeFlag(tokens[i+1:]) {
			a.flag(name + " runs an inline code payload")
			return
		}
		skip, isWrapper := wrappers[name]
		if !isWrapper {
			return
		}
		i++
		// Skip the wrapper's own flags, consumed arguments, and (for env)
		// assignments, then analyze the wrapped command.
		for i < len(tokens) && (strings.HasPrefix(tokens[i], "-") || isAssignment(tokens[i])) {
			i++
		}
		i += skip
	}
}

// classifyPipedProgram flags an interpreter that takes its program from the
// previous segment's output. `curl … | sh` cannot be analyzed: the code that
// will run is not in the command text, and does not exist yet.
func classifyPipedProgram(tokens []string, a *Analysis) {
	inv, _ := normalizeInvocation(tokens)
	if inv.name == "" || !interpreters[inv.name] {
		return
	}
	for _, arg := range inv.args {
		if arg == "-" {
			// An explicit stdin script is the same unreadable payload.
			break
		}
		if !strings.HasPrefix(arg, "-") {
			// The interpreter runs a named script; stdin is only its data.
			return
		}
	}
	a.flag(inv.name + " runs a program piped from another command")
}

func isAssignment(token string) bool {
	eq := strings.IndexByte(token, '=')
	if eq <= 0 {
		return false
	}
	for _, c := range token[:eq] {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func hasCodeFlag(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "-c", "-e", "-E", "/c", "/C", "-Command", "-command", "-EncodedCommand":
			return true
		}
		if !strings.HasPrefix(arg, "-") {
			return false
		}
	}
	return false
}
