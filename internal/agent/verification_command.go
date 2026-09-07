package agent

import (
	"os"
	"path/filepath"
	"strings"
)

// verificationDirectory tracks literal cd commands inside the workspace. A
// nested project is still part of the workspace evidence; an outside directory
// or unresolved/dynamic target is not. Resolve symlinks before checking scope.
func verificationDirectory(segment, cwd, workspace string) (string, bool) {
	fields := strings.Fields(segment)
	for _, field := range fields {
		if strings.HasPrefix(field, "CDPATH=") {
			return cwd, false
		}
	}
	for len(fields) > 0 && shellEnvironmentAssignment(fields[0]) {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return cwd, true
	}
	switch fields[0] {
	case "pushd", "popd", "chdir":
		return cwd, false
	case "cd":
		// Parse only a literal pathname, including one wholly quoted pathname.
		// Do not guess at variables, CDPATH, shell options or substitutions.
		arg := strings.TrimSpace(segment[strings.Index(segment, "cd")+2:])
		if strings.HasPrefix(arg, "-- ") {
			arg = strings.TrimSpace(arg[3:])
		}
		if len(arg) >= 2 && (arg[0] == '\'' || arg[0] == '"') && arg[len(arg)-1] == arg[0] {
			arg = arg[1 : len(arg)-1]
		} else if strings.ContainsAny(arg, " \t") {
			return cwd, false
		}
		if arg == "" || strings.HasPrefix(arg, "-") || strings.ContainsAny(arg, "'\"$`*?[]") || (filepath.Separator != '\\' && strings.ContainsAny(arg, `\~`)) || (filepath.Separator == '\\' && strings.ContainsAny(arg, "%!^")) {
			return cwd, false
		}
		if !filepath.IsAbs(arg) {
			if os.Getenv("CDPATH") != "" && arg != "." && arg != ".." && !strings.HasPrefix(arg, "./") && !strings.HasPrefix(arg, "../") {
				return cwd, false
			}
			arg = filepath.Join(cwd, arg)
		}
		target, err := filepath.EvalSymlinks(arg)
		if err != nil {
			return cwd, false
		}
		root, err := filepath.EvalSymlinks(workspace)
		if err != nil {
			return cwd, false
		}
		rel, err := filepath.Rel(root, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return cwd, false
		}
		return arg, true
	}
	return cwd, true
}

// Suggest only an intact command prefix ending at the verifier. Never extract
// a later command and silently discard cd, exports or quoted argument content.
// Preparation that may repeat effects is left to the agent to inspect instead
// of being prescribed by the runtime as a retry.
func verificationChainSuggestion(command, workspace string) string {
	var quote byte
	cut := len(command)
scan:
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if quote == '\'' {
			if ch == '\'' {
				quote = 0
			}
			continue
		}
		if ch == '\\' {
			i++
			continue
		}
		if quote == '"' {
			if ch == '"' {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
		case '<', '>':
			cut = i
			for cut > 0 && command[cut-1] >= '0' && command[cut-1] <= '9' {
				cut--
			}
			break scan
		case '|', ';', '\n', '\r':
			cut = i
			break scan
		case '&':
			if i+1 < len(command) && command[i+1] == '&' {
				i++
				continue
			}
			cut = i
			break scan
		}
	}
	if quote != 0 || cut == len(command) {
		return ""
	}
	prefix := strings.TrimSpace(command[:cut])
	final, refusal := scopedVerificationChain(prefix, workspace)
	if refusal != "" || !directVerificationCommand(final, workspace) {
		return ""
	}
	// The same quote-aware scanner accepted the prefix, but splitting && here
	// is intentionally conservative: quoted && can suppress a suggestion, never
	// change the command returned to the model.
	parts := strings.Split(prefix, "&&")
	for _, part := range parts[:len(parts)-1] {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			return ""
		}
		if fields[0] != "cd" && fields[0] != "export" {
			for _, field := range fields {
				if !shellEnvironmentAssignment(field) {
					return ""
				}
			}
		}
	}
	return prefix
}
