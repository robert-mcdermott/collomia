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
	// Executables lists the argv[0] of every simple command found, plus the
	// real targets behind transparent wrappers like env or timeout.
	Executables []string
	// Inspectable is false when the command's effects cannot be determined
	// statically. Uninspectable commands must be interactively approved.
	Inspectable bool
	// Reasons explains every inspectability failure.
	Reasons []string
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
	a := Analysis{Raw: command, Inspectable: true}
	segments, ok := split(command, &a)
	if !ok {
		return a
	}
	for _, segment := range segments {
		a.analyzeSegment(segment)
	}
	if len(a.Executables) == 0 && a.Inspectable && strings.TrimSpace(command) != "" {
		// Nothing recognizable (e.g. bare assignments); treat as opaque.
		a.flag("no executable could be identified")
	}
	return a
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
func split(command string, a *Analysis) ([][]string, bool) {
	var segments [][]string
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
	endSegment := func() {
		flush()
		if len(current) > 0 {
			segments = append(segments, current)
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
			endSegment()
		case c == '&' || c == '|':
			// &&, ||, |, & and redirection-adjacent forms all end the
			// current simple command.
			endSegment()
			if i+1 < len(runes) && (runes[i+1] == '&' || runes[i+1] == '|') {
				i++
			}
		case c == '(' || c == ')' || c == '{' || c == '}':
			endSegment()
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
	endSegment()
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
