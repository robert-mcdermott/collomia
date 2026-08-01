package shell

import (
	"path/filepath"
	"strings"
)

// AnalyzeArgv classifies a command that is already tokenized — one that will be
// handed to exec directly rather than to a shell.
//
// It exists so that a built-in which constructs its own argv is classified by
// the same code as the equivalent `run_command`. A tool that builds `git commit`
// itself and describes the result in its own words would be a second
// classification site, and a second site is how the `host` matcher shipped
// inert, how `collo policy check` reported the wrong decision for a
// credential-reaching command, and how delegated verification ended up with a
// command runner missing a containment field. Here the consequence would be
// worse than a wrong report: a structured Git tool that skipped classifyGit and
// publicationLabel would be a documented way around the confirmations and the
// publication tier that govern the same command through run_command.
//
// The text-level passes in analyzeAt are deliberately not run, because an argv
// cannot contain what they look for. Splitting, quote handling, command
// substitution, redirection, and the raw-string Windows scan all describe what a
// *shell* would do with a string. There is no shell here: the words are already
// separated, and a '>' or a '$(…)' among them is an argument to the program
// rather than an instruction to anything. Running those passes anyway would
// invent findings — a commit message containing "> /dev/sda" is a message.
//
// Everything that describes what the *program* does — the executable walk
// through wrappers, network endpoints, credential arguments, operations, and
// publication — is shared with the string path, and that is the whole point.
func AnalyzeArgv(argv []string, workspace string) Analysis {
	cleaned := make([]string, 0, len(argv))
	for _, token := range argv {
		if token == "" {
			continue
		}
		cleaned = append(cleaned, token)
	}
	workspace = canonicalWorkspace(workspace)
	a := Analysis{Raw: QuoteArgv(cleaned), workspace: workspace, Inspectable: true}
	if len(cleaned) == 0 {
		a.flag("no executable could be identified")
		return a
	}
	a.analyzeSegment(cleaned)
	classifySegment(cleaned, workspace, &a)
	if len(a.Executables) == 0 && a.Inspectable {
		a.flag("no executable could be identified")
	}
	return a
}

// canonicalWorkspace resolves a workspace root the way AnalyzeInWorkspace does,
// so a relative path in either entry point resolves against the same directory.
func canonicalWorkspace(workspace string) string {
	if workspace == "" {
		return ""
	}
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	if canonical, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = canonical
	}
	return workspace
}

// QuoteArgv renders an argv as a single shell-quoted line.
//
// It is display and matching text, never something handed to a shell. Two
// things read it: the permission prompt, and the additive agent-profile denial
// regexes in Manager.decideBase, which match against Action.Command.
//
// Quoting matters for the second. A denial pattern is written against the
// command line a user would type, so the rendering has to be the one they would
// have typed. It is worth being explicit that a free-text argument is still part
// of that line: `git commit -m 'fix the git push bug'` matches a profile denial
// of `git push`, and is refused. That is a false positive, but it is the same
// false positive the identical run_command string already produces, and one
// answer for two spellings of one command is worth more than being right about
// this case in one of them.
func QuoteArgv(argv []string) string {
	quoted := make([]string, 0, len(argv))
	for _, token := range argv {
		quoted = append(quoted, quoteToken(token))
	}
	return strings.Join(quoted, " ")
}

// quoteToken single-quotes anything that is not plainly safe, using the
// POSIX '\” idiom for an embedded single quote.
func quoteToken(token string) string {
	if token == "" {
		return "''"
	}
	if !strings.ContainsAny(token, " \t\n\r\v\f'\"\\$`|&;<>()*?[]{}~#!") {
		return token
	}
	return "'" + strings.ReplaceAll(token, "'", `'\''`) + "'"
}
