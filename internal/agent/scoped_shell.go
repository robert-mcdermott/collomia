package agent

import "strings"

// scopedVerificationChain understands quoting in direct Standard checks. It
// deliberately does not change the graph's conventional-command recognizer.
// Shell programs and substitutions remain in scripts: only simple commands
// joined by success-dependent && are accepted here.
func scopedVerificationChain(command, workspace string) (string, string) {
	const invalid = "verification requires a direct command or && chain without shell control operators, redirection, or command substitution"
	var quote byte
	start := 0
	var segments []string
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if quote == '\'' {
			if ch == '\'' {
				quote = 0
			}
			continue
		}
		if ch == '\\' {
			if i+1 == len(command) {
				return "", invalid
			}
			i++
			continue
		}
		if ch == '`' || (ch == '$' && i+1 < len(command) && command[i+1] == '(') {
			return "", invalid
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
		case '\n', '\r', ';', '|', '<', '>', '(', ')', '{', '}', '#':
			return "", invalid
		case '&':
			if i+1 >= len(command) || command[i+1] != '&' {
				return "", invalid
			}
			segments = append(segments, strings.TrimSpace(command[start:i]))
			i++
			start = i + 1
		}
	}
	if quote != 0 {
		return "", invalid
	}
	segments = append(segments, strings.TrimSpace(command[start:]))
	for i, segment := range segments {
		if segment == "" {
			return "", invalid
		}
		fields := strings.Fields(segment)
		for len(fields) > 0 && shellEnvironmentAssignment(fields[0]) {
			fields = fields[1:]
		}
		if len(fields) == 0 {
			return "", invalid
		}
		switch fields[0] {
		case "!", "if", "then", "else", "elif", "fi", "while", "until", "for", "do", "done", "case", "esac", "function":
			return "", invalid
		}
		if i < len(segments)-1 && relocatesVerification(segment, workspace) {
			return "", "the command changes directory before verifying, so its result would not describe the workspace the evidence is bound to"
		}
	}
	return segments[len(segments)-1], ""
}
