package main

import (
	"fmt"
	"sort"
	"strings"
)

// The completion vocabulary.
//
// This used to be a flat list of command names plus one hand-written line of
// subcommands per shell, which is five copies of the same knowledge in four
// scripts. It drifted exactly as that shape always does: `schema` offered
// `events` months after `collo schema config` shipped, `auth` and `audit`
// offered nothing at all, and zsh, fish, and PowerShell knew about `sessions`
// and no other subcommand. Nothing failed, because the only test asserted that
// four shells each produced a script.
//
// One table now, read by every shell, and completion_test.go compares it
// against the real dispatch switches — an offered subcommand that nothing
// dispatches, or a dispatched subcommand nothing offers, is a build failure.
// This is the same fix the configuration schema needed: the vocabularies had to
// stop being inline literals before anything could safely read them.
type completionCommand struct {
	name string
	// subcommands are the words that may follow the command. Empty means the
	// command takes none.
	subcommands []string
	// files marks a command whose argument is a path, so the shell should
	// complete filenames rather than a fixed word list.
	files bool
}

// completionCommands must name every command main.go dispatches. The test pins
// both directions, so a command added without a completion entry fails the
// build rather than being quietly uncompletable.
var completionCommands = []completionCommand{
	{name: "tui"},
	{name: "run"},
	{name: "init"},
	{name: "setup"},
	{name: "config", subcommands: []string{"show", "validate", "reference"}},
	{name: "trust"},
	{name: "doctor"},
	{name: "capabilities"},
	{name: "support", subcommands: []string{"bundle"}},
	{name: "policy", subcommands: []string{"check"}},
	{name: "auth", subcommands: []string{"list", "status", "set", "rm", "import"}},
	{name: "audit", subcommands: []string{"show", "path"}},
	{name: "review"},
	{name: "verify"},
	{name: "sessions", subcommands: []string{"list", "show", "fork", "rewind", "rename", "archive", "unarchive", "delete"}},
	{name: "skills", subcommands: []string{"list", "show", "new", "install", "update", "remove", "enable", "disable"}},
	{name: "mcp", subcommands: []string{"list", "show", "add", "remove", "enable", "disable", "test"}},
	{name: "completion", subcommands: completionShells},
	// Read from the same variable the command itself dispatches on, so a new
	// published contract is completable the moment it exists. This is the one
	// subcommand list that cannot drift even in principle.
	{name: "schema", subcommands: schemaContracts},
	{name: "replay", files: true},
	{name: "version"},
}

// completionShells are the shells `collo completion` can emit for, and the
// values it completes for itself.
var completionShells = []string{"bash", "zsh", "fish", "powershell"}

var completionFlags = []string{
	"--help", "--version", "--cwd", "--provider", "--model", "--agent", "--autonomy",
	"--autopilot", "--workspace", "--plan", "--resume", "--continue", "--web",
	"--web-port", "--no-open", "--alt-screen", "--no-alt-screen", "--jsonl", "--ephemeral",
	"--debug", "--global", "--strict", "--yes", "--with-reference",
	"--check", "--output", "--include-logs",
	"--session", "--actor", "--tool", "--since", "--limit", "--denied",
}

func runCompletionCommand(opts options) error {
	if len(opts.args) != 1 {
		return fmt.Errorf("completion requires one shell: %s", englishList(completionShells))
	}
	script, err := completionScript(strings.ToLower(opts.args[0]))
	if err != nil {
		return err
	}
	fmt.Print(script)
	return nil
}

// commandNames returns every completable command name, in table order.
func commandNames() []string {
	names := make([]string, 0, len(completionCommands))
	for _, command := range completionCommands {
		names = append(names, command.name)
	}
	return names
}

// commandsWithSubcommands returns the entries that take a word after the
// command, sorted so generated scripts are byte-stable.
func commandsWithSubcommands() []completionCommand {
	var out []completionCommand
	for _, command := range completionCommands {
		if len(command.subcommands) > 0 {
			out = append(out, command)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func commandsCompletingFiles() []string {
	var out []string
	for _, command := range completionCommands {
		if command.files {
			out = append(out, command.name)
		}
	}
	sort.Strings(out)
	return out
}

func completionScript(shell string) (string, error) {
	commands := strings.Join(commandNames(), " ")
	flags := strings.Join(completionFlags, " ")
	switch shell {
	case "bash":
		var arms strings.Builder
		for _, command := range commandsWithSubcommands() {
			fmt.Fprintf(&arms, "    %s) COMPREPLY=( $(compgen -W \"%s\" -- \"$cur\") ); return ;;\n",
				command.name, strings.Join(command.subcommands, " "))
		}
		for _, name := range commandsCompletingFiles() {
			fmt.Fprintf(&arms, "    %s) COMPREPLY=( $(compgen -f -- \"$cur\") ); return ;;\n", name)
		}
		return fmt.Sprintf(`# bash completion for collo
_collo() {
  local cur prev
  COMPREPLY=()
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[COMP_CWORD-1]}"
  case "$prev" in
%s  esac
  if [[ "$cur" == -* ]]; then
    COMPREPLY=( $(compgen -W "%s" -- "$cur") )
  elif (( COMP_CWORD == 1 )); then
    COMPREPLY=( $(compgen -W "%s" -- "$cur") )
  fi
}
complete -F _collo collo
`, arms.String(), flags, commands), nil
	case "zsh":
		var arms strings.Builder
		for _, command := range commandsWithSubcommands() {
			fmt.Fprintf(&arms, "      %s) _describe '%s command' '(%s)' ;;\n",
				command.name, command.name, strings.Join(command.subcommands, " "))
		}
		for _, name := range commandsCompletingFiles() {
			fmt.Fprintf(&arms, "      %s) _files ;;\n", name)
		}
		return fmt.Sprintf(`#compdef collo
_collo() {
  local -a commands flags
  commands=(%s)
  flags=(%s)
  if (( CURRENT == 2 )); then
    _describe 'command' commands
  elif (( CURRENT == 3 )); then
    case $words[2] in
%s      *) _describe 'option' flags ;;
    esac
  else
    _describe 'option' flags
  fi
}
compdef _collo collo
`, commands, flags, arms.String()), nil
	case "fish":
		var b strings.Builder
		b.WriteString("# fish completion for collo\ncomplete -c collo -f\n")
		for _, command := range completionCommands {
			fmt.Fprintf(&b, "complete -c collo -n '__fish_use_subcommand' -a '%s'\n", command.name)
		}
		for _, command := range commandsWithSubcommands() {
			for _, sub := range command.subcommands {
				fmt.Fprintf(&b, "complete -c collo -n '__fish_seen_subcommand_from %s' -a '%s'\n", command.name, sub)
			}
		}
		for _, name := range commandsCompletingFiles() {
			fmt.Fprintf(&b, "complete -c collo -n '__fish_seen_subcommand_from %s' -F\n", name)
		}
		for _, flag := range completionFlags {
			fmt.Fprintf(&b, "complete -c collo -l '%s'\n", strings.TrimPrefix(flag, "--"))
		}
		return b.String(), nil
	case "powershell", "pwsh":
		words := append([]string{}, commandNames()...)
		words = append(words, completionFlags...)
		for _, command := range commandsWithSubcommands() {
			words = append(words, command.subcommands...)
		}
		quoted := make([]string, 0, len(words))
		seen := map[string]bool{}
		for _, word := range words {
			if seen[word] {
				continue
			}
			seen[word] = true
			quoted = append(quoted, "'"+word+"'")
		}
		return fmt.Sprintf(`# PowerShell completion for collo
Register-ArgumentCompleter -Native -CommandName collo -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)
  @(%s) |
    Where-Object { $_ -like "$wordToComplete*" } |
    ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_) }
}
`, strings.Join(quoted, ", ")), nil
	default:
		return "", fmt.Errorf("unsupported shell %q (expected %s)", shell, englishList(completionShells))
	}
}
