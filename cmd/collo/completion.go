package main

import (
	"fmt"
	"strings"
)

var completionCommands = []string{
	"tui", "run", "init", "config", "trust", "doctor", "capabilities",
	"support", "policy", "review", "verify", "sessions", "skills", "mcp", "completion", "schema", "replay", "version",
}

var completionFlags = []string{
	"--help", "--version", "--cwd", "--provider", "--model", "--autonomy",
	"--autopilot", "--workspace", "--plan", "--resume", "--continue", "--web",
	"--web-port", "--no-open", "--alt-screen", "--no-alt-screen", "--jsonl", "--ephemeral",
	"--debug", "--global", "--strict", "--yes", "--with-reference",
	"--check", "--output", "--include-logs",
}

func runCompletionCommand(opts options) error {
	if len(opts.args) != 1 {
		return fmt.Errorf("completion requires one shell: bash, zsh, fish, or powershell")
	}
	script, err := completionScript(strings.ToLower(opts.args[0]))
	if err != nil {
		return err
	}
	fmt.Print(script)
	return nil
}

func completionScript(shell string) (string, error) {
	commands := strings.Join(completionCommands, " ")
	flags := strings.Join(completionFlags, " ")
	switch shell {
	case "bash":
		return fmt.Sprintf(`# bash completion for collo
_collo() {
  local cur prev
  COMPREPLY=()
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[COMP_CWORD-1]}"
  case "$prev" in
    completion) COMPREPLY=( $(compgen -W "bash zsh fish powershell" -- "$cur") ); return ;;
    schema) COMPREPLY=( $(compgen -W "events" -- "$cur") ); return ;;
    config) COMPREPLY=( $(compgen -W "show validate reference" -- "$cur") ); return ;;
    sessions) COMPREPLY=( $(compgen -W "list show fork rename archive unarchive delete" -- "$cur") ); return ;;
    skills) COMPREPLY=( $(compgen -W "list show new install update remove enable disable" -- "$cur") ); return ;;
    mcp) COMPREPLY=( $(compgen -W "list show add remove enable disable test" -- "$cur") ); return ;;
    policy) COMPREPLY=( $(compgen -W "check" -- "$cur") ); return ;;
    support) COMPREPLY=( $(compgen -W "bundle" -- "$cur") ); return ;;
    replay) COMPREPLY=( $(compgen -f -- "$cur") ); return ;;
  esac
  if [[ "$cur" == -* ]]; then
    COMPREPLY=( $(compgen -W "%s" -- "$cur") )
  elif (( COMP_CWORD == 1 )); then
    COMPREPLY=( $(compgen -W "%s" -- "$cur") )
  fi
}
complete -F _collo collo
`, flags, commands), nil
	case "zsh":
		return fmt.Sprintf(`#compdef collo
_collo() {
  local -a commands flags
  commands=(%s)
  flags=(%s)
  if (( CURRENT == 2 )); then
    _describe 'command' commands
  else
    _describe 'option' flags
  fi
}
compdef _collo collo
`, commands, flags), nil
	case "fish":
		var b strings.Builder
		b.WriteString("# fish completion for collo\ncomplete -c collo -f\n")
		for _, command := range completionCommands {
			fmt.Fprintf(&b, "complete -c collo -n '__fish_use_subcommand' -a '%s'\n", command)
		}
		for _, flag := range completionFlags {
			fmt.Fprintf(&b, "complete -c collo -l '%s'\n", strings.TrimPrefix(flag, "--"))
		}
		return b.String(), nil
	case "powershell", "pwsh":
		words := append(append([]string{}, completionCommands...), completionFlags...)
		quoted := make([]string, len(words))
		for i, word := range words {
			quoted[i] = "'" + word + "'"
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
		return "", fmt.Errorf("unsupported shell %q (expected bash, zsh, fish, or powershell)", shell)
	}
}
